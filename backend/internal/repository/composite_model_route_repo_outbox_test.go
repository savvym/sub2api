package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestCompositeModelRouteCreateUsesOuterTransactionWithoutCommittingIt(t *testing.T) {
	client, mock, repo := newCompositeModelRouteMock(t)
	mock.ExpectBegin()
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)

	route := compositeModelRouteFixture(41)
	mock.ExpectQuery(`(?s)INSERT INTO "composite_model_routes"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(101)))
	expectCompositeRouteGroupChanged(mock, 41, nil)

	err = repo.Create(dbent.NewTxContext(context.Background(), tx), route)

	require.NoError(t, err)
	require.EqualValues(t, 101, route.ID)
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompositeModelRouteCreateReturnsOutboxFailureToOuterTransaction(t *testing.T) {
	client, mock, repo := newCompositeModelRouteMock(t)
	mock.ExpectBegin()
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)

	route := compositeModelRouteFixture(42)
	mock.ExpectQuery(`(?s)INSERT INTO "composite_model_routes"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(102)))
	outboxErr := errors.New("outbox unavailable")
	expectCompositeRouteGroupChanged(mock, 42, outboxErr)

	err = repo.Create(dbent.NewTxContext(context.Background(), tx), route)

	require.ErrorIs(t, err, outboxErr)
	require.Zero(t, route.ID)
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompositeModelRouteCreateRollsBackOwnedTransactionWhenOutboxFails(t *testing.T) {
	_, mock, repo := newCompositeModelRouteMock(t)
	mock.ExpectBegin()
	route := compositeModelRouteFixture(43)
	mock.ExpectQuery(`(?s)INSERT INTO "composite_model_routes"`).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(103)))
	outboxErr := errors.New("outbox unavailable")
	expectCompositeRouteGroupChanged(mock, 43, outboxErr)
	mock.ExpectRollback()

	err := repo.Create(context.Background(), route)

	require.ErrorIs(t, err, outboxErr)
	require.Zero(t, route.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompositeModelRouteUpdateEnqueuesReturnedGroupID(t *testing.T) {
	_, mock, repo := newCompositeModelRouteMock(t)
	mock.ExpectBegin()
	route := compositeModelRouteFixture(999)
	route.ID = 104
	mock.ExpectExec(`(?s)UPDATE "composite_model_routes" SET .* WHERE "id" = \$[0-9]+`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`(?s)SELECT .* FROM "composite_model_routes" WHERE "id" = \$1`).
		WillReturnRows(compositeModelRouteRows(104, 44))
	expectCompositeRouteGroupChanged(mock, 44, nil)
	mock.ExpectCommit()

	err := repo.Update(context.Background(), route)

	require.NoError(t, err)
	require.EqualValues(t, 44, route.GroupID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompositeModelRouteDeleteEnqueuesSelectedGroupID(t *testing.T) {
	_, mock, repo := newCompositeModelRouteMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(`(?s)SELECT .*"group_id".* FROM "composite_model_routes"`).
		WillReturnRows(sqlmock.NewRows([]string{"group_id"}).AddRow(int64(45)))
	mock.ExpectExec(`(?s)UPDATE "composite_model_routes" SET .*"deleted_at" = .* WHERE .*"id" =`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectCompositeRouteGroupChanged(mock, 45, nil)
	mock.ExpectCommit()

	err := repo.Delete(context.Background(), 105)

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCompositeModelRouteDeleteByGroupEnqueuesOnlyWhenRowsChange(t *testing.T) {
	t.Run("changed", func(t *testing.T) {
		_, mock, repo := newCompositeModelRouteMock(t)
		mock.ExpectBegin()
		mock.ExpectExec(`(?s)UPDATE "composite_model_routes" SET .*"deleted_at" = .* WHERE .*"group_id" =`).
			WillReturnResult(sqlmock.NewResult(0, 2))
		expectCompositeRouteGroupChanged(mock, 46, nil)
		mock.ExpectCommit()

		err := repo.DeleteByGroup(context.Background(), 46)

		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("unchanged", func(t *testing.T) {
		_, mock, repo := newCompositeModelRouteMock(t)
		mock.ExpectBegin()
		mock.ExpectExec(`(?s)UPDATE "composite_model_routes" SET .*"deleted_at" = .* WHERE .*"group_id" =`).
			WillReturnResult(sqlmock.NewResult(0, 0))
		mock.ExpectCommit()

		err := repo.DeleteByGroup(context.Background(), 47)

		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func newCompositeModelRouteMock(t *testing.T) (*dbent.Client, sqlmock.Sqlmock, *compositeModelRouteRepository) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	return client, mock, &compositeModelRouteRepository{client: client}
}

func compositeModelRouteFixture(groupID int64) *service.CompositeModelRoute {
	return &service.CompositeModelRoute{
		GroupID:        groupID,
		PublicModel:    "claude-3",
		MatchType:      service.CompositeRouteMatchExact,
		TargetPlatform: service.PlatformAnthropic,
		UpstreamModel:  "claude-3-5-sonnet",
		Endpoint:       service.CompositeRouteEndpointMessages,
		Priority:       10,
		Enabled:        true,
		Notes:          "test route",
	}
}

func compositeModelRouteRows(id, groupID int64) *sqlmock.Rows {
	now := time.Now().UTC()
	return sqlmock.NewRows([]string{
		"id", "created_at", "updated_at", "deleted_at", "group_id", "public_model",
		"match_type", "target_platform", "upstream_model", "endpoint", "priority", "enabled", "notes",
	}).AddRow(
		id, now, now, nil, groupID, "claude-3", service.CompositeRouteMatchExact,
		service.PlatformAnthropic, "claude-3-5-sonnet", service.CompositeRouteEndpointMessages,
		10, true, "test route",
	)
}

func expectCompositeRouteGroupChanged(mock sqlmock.Sqlmock, groupID int64, resultErr error) {
	expectation := mock.ExpectExec(`INSERT INTO scheduler_outbox`).
		WithArgs(
			service.SchedulerOutboxEventGroupChanged,
			nil,
			groupID,
			nil,
			sqlmock.AnyArg(),
		)
	if resultErr != nil {
		expectation.WillReturnError(resultErr)
		return
	}
	expectation.WillReturnResult(sqlmock.NewResult(1, 1))
}
