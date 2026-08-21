package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestResourceMutationRepositoryRejectsExistingTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := &resourceMutationRepository{client: client}

	mock.ExpectBegin()
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	callbackCalled := false
	err = repo.WithSerializableTx(dbent.NewTxContext(context.Background(), tx), func(context.Context) error {
		callbackCalled = true
		return nil
	})

	require.ErrorIs(t, err, service.ErrResourceMutationUnavailable)
	require.False(t, callbackCalled)
	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestResourceMutationRepositoryAuthorizationEventRecordsSuccessResult(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	repo := &resourceMutationRepository{client: client}

	mock.ExpectExec(`(?s)INSERT INTO resource_authorization_events`).WithArgs(
		int64(7),
		nil,
		nil,
		int64(41),
		nil,
		string(authz.AuthMethodJWT),
		"account.updated",
		int64(4),
		"request-17",
		`{"changed_fields":["configuration"],"result":"success"}`,
	).WillReturnResult(sqlmock.NewResult(1, 1))

	err = repo.AppendAuthorizationEvents(context.Background(), []service.ResourceAuthorizationEventRecord{{
		Key: service.ResourceMutationKey{
			ResourceType: authz.ResourceTypeAccount,
			ResourceID:   7,
		},
		ActorKind:             authz.SubjectKindUser,
		ActorID:               41,
		AuthMethod:            authz.AuthMethodJWT,
		EventType:             "account.updated",
		ResourceAccessVersion: 4,
		RequestID:             "request-17",
		ChangedFields:         []string{"configuration"},
	}})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
