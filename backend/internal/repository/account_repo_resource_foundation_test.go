package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestAccountEntityToServiceMapsResourceAccessFields(t *testing.T) {
	ownerUserID := int64(11)
	createdByUserID := int64(12)
	publicAccessLevel := "viewer"

	account := accountEntityToService(&dbent.Account{
		OwnerUserID:       &ownerUserID,
		CreatedByUserID:   &createdByUserID,
		PublicAccessLevel: &publicAccessLevel,
		AccessVersion:     7,
	})

	require.NotNil(t, account)
	require.Equal(t, &ownerUserID, account.OwnerUserID)
	require.Equal(t, &createdByUserID, account.CreatedByUserID)
	require.Equal(t, &publicAccessLevel, account.PublicAccessLevel)
	require.Equal(t, int64(7), account.AccessVersion)
}

func TestCreateAccountRecordPersistsResourceAccessFieldsAndDefaults(t *testing.T) {
	tests := []struct {
		name                string
		account             *service.Account
		wantAccessVersion   int64
		wantNullableColumns bool
	}{
		{
			name:              "platform resource defaults",
			account:           resourceFoundationAccount(),
			wantAccessVersion: initialAccountAccessVersion,
		},
		{
			name: "explicit resource access",
			account: func() *service.Account {
				account := resourceFoundationAccount()
				ownerUserID := int64(21)
				createdByUserID := int64(22)
				publicAccessLevel := "consumer"
				account.OwnerUserID = &ownerUserID
				account.CreatedByUserID = &createdByUserID
				account.PublicAccessLevel = &publicAccessLevel
				account.AccessVersion = 9
				return account
			}(),
			wantAccessVersion:   9,
			wantNullableColumns: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedSQL string
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(accountResourceQueryMatcher{actual: &capturedSQL}))
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })

			mock.ExpectQuery("create account").WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(101)))

			err = createAccountRecord(context.Background(), client, tt.account)
			require.NoError(t, err)
			require.NoError(t, mock.ExpectationsWereMet())
			require.Equal(t, int64(101), tt.account.ID)
			require.Equal(t, tt.wantAccessVersion, tt.account.AccessVersion)

			normalizedSQL := normalizeSQLWhitespace(capturedSQL)
			require.Contains(t, normalizedSQL, `"access_version"`)
			if tt.wantNullableColumns {
				require.Contains(t, normalizedSQL, `"owner_user_id"`)
				require.Contains(t, normalizedSQL, `"created_by_user_id"`)
				require.Contains(t, normalizedSQL, `"public_access_level"`)
			} else {
				require.NotContains(t, normalizedSQL, `"owner_user_id"`)
				require.NotContains(t, normalizedSQL, `"created_by_user_id"`)
				require.NotContains(t, normalizedSQL, `"public_access_level"`)
			}
		})
	}
}

func TestAddToGroupJoinsOuterTransactionAndReturnsOutboxFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })

	mock.ExpectBegin()
	tx, err := client.Tx(context.Background())
	require.NoError(t, err)
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO "account_groups"`)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).
		WillReturnError(errors.New("outbox failed"))

	repo := newAccountRepositoryWithSQL(client, accountResourceFailingExecutor{err: errors.New("fallback executor used")}, nil)
	err = repo.AddToGroup(dbent.NewTxContext(context.Background(), tx), 17, 29, 3)
	require.EqualError(t, err, "outbox failed")

	mock.ExpectRollback()
	require.NoError(t, tx.Rollback())
	require.NoError(t, mock.ExpectationsWereMet())
}

type accountResourceQueryMatcher struct {
	actual *string
}

func (m accountResourceQueryMatcher) Match(_, actual string) error {
	*m.actual = actual
	return nil
}

type accountResourceFailingExecutor struct {
	err error
}

func (e accountResourceFailingExecutor) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, e.err
}

func (e accountResourceFailingExecutor) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, e.err
}

func resourceFoundationAccount() *service.Account {
	return &service.Account{
		Name:        "resource-foundation",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-test"},
		Extra:       map[string]any{},
		Concurrency: 1,
		Priority:    1,
		Status:      service.StatusActive,
		Schedulable: true,
	}
}

func TestNormalizeAccountAccessVersion(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   int64
		want int64
	}{
		{name: "zero", in: 0, want: initialAccountAccessVersion},
		{name: "negative", in: -1, want: initialAccountAccessVersion},
		{name: "existing", in: 13, want: 13},
	} {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, normalizeAccountAccessVersion(tt.in))
		})
	}
}
