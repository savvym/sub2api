package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

type rejectingGroupSQLExecutor struct {
	called bool
	err    error
}

func (e *rejectingGroupSQLExecutor) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	e.called = true
	return nil, e.err
}

func (e *rejectingGroupSQLExecutor) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	e.called = true
	return nil, e.err
}

func TestGroupEntityToServiceMapsResourceAccessFields(t *testing.T) {
	ownerUserID := int64(11)
	createdByUserID := int64(12)
	publicAccessLevel := "consumer"

	got := groupEntityToService(&dbent.Group{
		ID:                9,
		OwnerUserID:       &ownerUserID,
		CreatedByUserID:   &createdByUserID,
		PublicAccessLevel: &publicAccessLevel,
		AccessVersion:     7,
		AuthorizationMode: "shadow",
	})

	require.Equal(t, &ownerUserID, got.OwnerUserID)
	require.Equal(t, &createdByUserID, got.CreatedByUserID)
	require.Equal(t, &publicAccessLevel, got.PublicAccessLevel)
	require.EqualValues(t, 7, got.AccessVersion)
	require.Equal(t, "shadow", got.AuthorizationMode)
}

func TestGroupResourceAccessDefaults(t *testing.T) {
	require.EqualValues(t, 1, normalizedGroupAccessVersion(0))
	require.EqualValues(t, 1, normalizedGroupAccessVersion(-1))
	require.EqualValues(t, 8, normalizedGroupAccessVersion(8))
	require.Equal(t, "legacy", normalizedGroupAuthorizationMode(""))
	require.Equal(t, "acl", normalizedGroupAuthorizationMode("acl"))
}

func TestBindAccountsToGroupUsesOuterTransactionForMutationAndOutbox(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	mock.ExpectBegin()
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO account_groups")).
		WillReturnResult(sqlmock.NewResult(0, 2))
	outboxErr := errors.New("outbox unavailable")
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
		WillReturnError(outboxErr)
	fallback := &rejectingGroupSQLExecutor{err: errors.New("must use transaction client")}
	repo := newGroupRepositoryWithSQL(client, fallback)

	err = repo.BindAccountsToGroup(
		dbent.NewTxContext(context.Background(), tx),
		9,
		[]int64{21, 22},
	)

	require.ErrorIs(t, err, outboxErr)
	require.False(t, fallback.called)
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBindAccountsToGroupDoesNotCommitOuterTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	mock.ExpectBegin()
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO account_groups")).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)")).
		WithArgs(service.SchedulerOutboxEventAccountBulkChanged, nil, nil, accountIDsPayloadMatcher{want: []int64{21, 22}}).
		WillReturnResult(sqlmock.NewResult(0, 1))
	fallback := &rejectingGroupSQLExecutor{err: errors.New("must use transaction client")}
	repo := newGroupRepositoryWithSQL(client, fallback)

	err = repo.BindAccountsToGroup(
		dbent.NewTxContext(context.Background(), tx),
		9,
		[]int64{21, 22},
	)

	require.NoError(t, err)
	require.False(t, fallback.called)
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBindAccountsToGroupRollsBackOwnedTransactionWhenOutboxFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO account_groups")).
		WillReturnResult(sqlmock.NewResult(0, 2))
	outboxErr := errors.New("outbox unavailable")
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
		WillReturnError(outboxErr)
	mock.ExpectRollback()
	repo := newGroupRepositoryWithSQL(client, db)

	err = repo.BindAccountsToGroup(context.Background(), 9, []int64{21, 22})

	require.ErrorIs(t, err, outboxErr)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBindAccountsToGroupRollsBackOwnedTransactionWhenAccountOutboxFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO account_groups")).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	outboxErr := errors.New("account outbox unavailable")
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)")).
		WithArgs(service.SchedulerOutboxEventAccountBulkChanged, nil, nil, accountIDsPayloadMatcher{want: []int64{21, 22}}).
		WillReturnError(outboxErr)
	mock.ExpectRollback()
	repo := newGroupRepositoryWithSQL(client, db)

	err = repo.BindAccountsToGroup(context.Background(), 9, []int64{22, 21, 22})

	require.ErrorIs(t, err, outboxErr)
	require.NoError(t, mock.ExpectationsWereMet())
}
