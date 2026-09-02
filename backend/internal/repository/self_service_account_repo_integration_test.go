//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type selfServiceAccountPostgresFixture struct {
	client      *dbent.Client
	repository  service.SelfServiceAccountRepository
	userID      int64
	requestBase string
}

func TestSelfServiceAccountRepositoryCreatesPrivateAccountOutboxAndEventPostgres(t *testing.T) {
	fixture := newSelfServiceAccountPostgresFixture(t)
	requestID := fixture.requestBase + "-create"
	apiKey := fmt.Sprintf("sk-self-service-%d", time.Now().UnixNano())
	var created service.SelfServiceAccountState

	err := fixture.repository.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		var createErr error
		created, createErr = fixture.repository.CreateAccount(txCtx, service.SelfServiceAccountCreateRecord{
			Name:          "Personal OpenAI",
			Platform:      service.PlatformOpenAI,
			AccountType:   service.AccountTypeAPIKey,
			APIKey:        apiKey,
			OwnerUserID:   fixture.userID,
			CreatorUserID: fixture.userID,
		})
		if createErr != nil {
			return createErr
		}
		return fixture.repository.AppendAuthorizationEvent(
			txCtx,
			selfServiceAccountPostgresEvent(created, fixture.userID, "account.created", requestID, []string{
				"configuration", "credentials", "ownership",
			}),
		)
	})

	require.NoError(t, err)
	require.Positive(t, created.ID)
	require.Equal(t, int64(1), created.AccessVersion)
	require.False(t, created.Deleted)
	require.True(t, created.CredentialConfigured)
	require.Equal(t, &fixture.userID, created.OwnerUserID)
	require.Nil(t, created.PublicAccessLevel)

	var (
		name               string
		platform           string
		accountType        string
		credentialsJSON    string
		extraJSON          string
		proxyID            sql.NullInt64
		concurrency        int
		priority           int
		status             string
		autoPauseOnExpired bool
		ownerUserID        sql.NullInt64
		createdByUserID    sql.NullInt64
		publicAccessLevel  sql.NullString
		accessVersion      int64
		schedulable        bool
		deletedAt          sql.NullTime
		parentAccountID    sql.NullInt64
		quotaDimension     string
	)
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
SELECT
    name,
    platform,
    type,
    credentials::text,
    extra::text,
    proxy_id,
    concurrency,
    priority,
    status,
    auto_pause_on_expired,
    owner_user_id,
    created_by_user_id,
    public_access_level,
    access_version,
    schedulable,
    deleted_at,
    parent_account_id,
    quota_dimension
FROM accounts
WHERE id = $1`, created.ID).Scan(
		&name,
		&platform,
		&accountType,
		&credentialsJSON,
		&extraJSON,
		&proxyID,
		&concurrency,
		&priority,
		&status,
		&autoPauseOnExpired,
		&ownerUserID,
		&createdByUserID,
		&publicAccessLevel,
		&accessVersion,
		&schedulable,
		&deletedAt,
		&parentAccountID,
		&quotaDimension,
	))
	require.Equal(t, "Personal OpenAI", name)
	require.Equal(t, service.PlatformOpenAI, platform)
	require.Equal(t, service.AccountTypeAPIKey, accountType)
	require.JSONEq(t, fmt.Sprintf(`{"api_key":%q}`, apiKey), credentialsJSON)
	require.JSONEq(t, `{"openai_long_context_billing_enabled":false}`, extraJSON)
	require.False(t, proxyID.Valid)
	require.Equal(t, selfServiceAccountDefaultConcurrency, concurrency)
	require.Equal(t, selfServiceAccountDefaultPriority, priority)
	require.Equal(t, service.StatusActive, status)
	require.True(t, autoPauseOnExpired)
	require.Equal(t, sql.NullInt64{Int64: fixture.userID, Valid: true}, ownerUserID)
	require.Equal(t, sql.NullInt64{Int64: fixture.userID, Valid: true}, createdByUserID)
	require.False(t, publicAccessLevel.Valid)
	require.Equal(t, int64(1), accessVersion)
	require.False(t, schedulable)
	require.False(t, deletedAt.Valid)
	require.False(t, parentAccountID.Valid)
	require.Equal(t, "global", quotaDimension)
	require.Zero(t, selfServiceAccountPostgresCount(t,
		"SELECT COUNT(*) FROM account_groups WHERE account_id = $1", created.ID))
	require.Equal(t, 1, selfServiceAccountPostgresCount(t, `
SELECT COUNT(*)
FROM scheduler_outbox
WHERE event_type = $1 AND account_id = $2`, service.SchedulerOutboxEventAccountChanged, created.ID))

	var (
		eventOwnerUserID   sql.NullInt64
		eventActorUserID   sql.NullInt64
		eventActorSPID     sql.NullInt64
		authMethod         string
		eventType          string
		eventAccessVersion int64
		eventRequestID     string
		detailsJSON        string
	)
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
SELECT
    resource_owner_user_id,
    actor_user_id,
    actor_service_principal_id,
    auth_method,
    event_type,
    resource_access_version,
    request_id,
    details::text
FROM resource_authorization_events
WHERE account_id = $1 AND request_id = $2`, created.ID, requestID).Scan(
		&eventOwnerUserID,
		&eventActorUserID,
		&eventActorSPID,
		&authMethod,
		&eventType,
		&eventAccessVersion,
		&eventRequestID,
		&detailsJSON,
	))
	require.Equal(t, sql.NullInt64{Int64: fixture.userID, Valid: true}, eventOwnerUserID)
	require.Equal(t, sql.NullInt64{Int64: fixture.userID, Valid: true}, eventActorUserID)
	require.False(t, eventActorSPID.Valid)
	require.Equal(t, string(authz.AuthMethodJWT), authMethod)
	require.Equal(t, "account.created", eventType)
	require.Equal(t, int64(1), eventAccessVersion)
	require.Equal(t, requestID, eventRequestID)
	require.JSONEq(t, `{"changed_fields":["configuration","credentials","ownership"],"result":"success"}`, detailsJSON)
}

func TestSelfServiceAccountRepositoryEventFailureRollsBackAccountAndOutboxPostgres(t *testing.T) {
	fixture := newSelfServiceAccountPostgresFixture(t)
	requestID := fixture.requestBase + "-event-failure"
	accountName := fmt.Sprintf("self-service-event-rollback-%d", time.Now().UnixNano())
	var created service.SelfServiceAccountState

	err := fixture.repository.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		var createErr error
		created, createErr = fixture.repository.CreateAccount(txCtx, service.SelfServiceAccountCreateRecord{
			Name: accountName, Platform: service.PlatformAnthropic, AccountType: service.AccountTypeAPIKey,
			APIKey: "sk-event-rollback", OwnerUserID: fixture.userID, CreatorUserID: fixture.userID,
		})
		if createErr != nil {
			return createErr
		}
		event := selfServiceAccountPostgresEvent(
			created,
			fixture.userID,
			"account.created",
			requestID,
			[]string{"configuration"},
		)
		event.ActorID = math.MaxInt64
		return fixture.repository.AppendAuthorizationEvent(txCtx, event)
	})

	require.Error(t, err)
	require.Positive(t, created.ID)
	require.Zero(t, selfServiceAccountPostgresCount(t,
		"SELECT COUNT(*) FROM accounts WHERE id = $1", created.ID))
	require.Zero(t, selfServiceAccountPostgresCount(t,
		"SELECT COUNT(*) FROM accounts WHERE name = $1", accountName))
	require.Zero(t, selfServiceAccountPostgresCount(t,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE account_id = $1", created.ID))
	require.Zero(t, selfServiceAccountPostgresCount(t,
		"SELECT COUNT(*) FROM resource_authorization_events WHERE request_id = $1", requestID))
}

func TestSelfServiceAccountRepositoryOutboxFailureRollsBackAccountPostgres(t *testing.T) {
	fixture := newSelfServiceAccountPostgresFixture(t)
	requestID := fixture.requestBase + "-outbox-failure"
	accountName := fmt.Sprintf("self-service-outbox-rollback-%d", time.Now().UnixNano())
	installSelfServiceAccountOutboxFailureTrigger(t, accountName)

	err := fixture.repository.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		created, createErr := fixture.repository.CreateAccount(txCtx, service.SelfServiceAccountCreateRecord{
			Name: accountName, Platform: service.PlatformGemini, AccountType: service.AccountTypeAPIKey,
			APIKey: "sk-outbox-rollback", OwnerUserID: fixture.userID, CreatorUserID: fixture.userID,
		})
		if createErr != nil {
			return createErr
		}
		return fixture.repository.AppendAuthorizationEvent(
			txCtx,
			selfServiceAccountPostgresEvent(created, fixture.userID, "account.created", requestID, nil),
		)
	})

	require.ErrorContains(t, err, "forced self-service account outbox failure")
	require.Zero(t, selfServiceAccountPostgresCount(t,
		"SELECT COUNT(*) FROM accounts WHERE name = $1", accountName))
	require.Zero(t, selfServiceAccountPostgresCount(t, `
SELECT COUNT(*)
FROM scheduler_outbox AS outbox
JOIN accounts AS account ON account.id = outbox.account_id
WHERE account.name = $1`, accountName))
	require.Zero(t, selfServiceAccountPostgresCount(t,
		"SELECT COUNT(*) FROM resource_authorization_events WHERE request_id = $1", requestID))
}

func TestSelfServiceAccountRepositoryRenameAndSoftDeleteIncrementVersionPostgres(t *testing.T) {
	fixture := newSelfServiceAccountPostgresFixture(t)
	var created service.SelfServiceAccountState
	err := fixture.repository.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		var createErr error
		created, createErr = fixture.repository.CreateAccount(txCtx, service.SelfServiceAccountCreateRecord{
			Name: "before", Platform: service.PlatformOpenAI, AccountType: service.AccountTypeAPIKey,
			APIKey: "sk-mutation", OwnerUserID: fixture.userID, CreatorUserID: fixture.userID,
		})
		if createErr != nil {
			return createErr
		}
		return fixture.repository.AppendAuthorizationEvent(
			txCtx,
			selfServiceAccountPostgresEvent(
				created,
				fixture.userID,
				"account.created",
				fixture.requestBase+"-create",
				[]string{"configuration"},
			),
		)
	})
	require.NoError(t, err)

	_, err = integrationDB.ExecContext(context.Background(),
		"DELETE FROM scheduler_outbox WHERE account_id = $1", created.ID)
	require.NoError(t, err)

	var renamed service.SelfServiceAccountState
	err = fixture.repository.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		if lockErr := fixture.repository.LockActorAuthorization(txCtx, fixture.userID); lockErr != nil {
			return lockErr
		}
		locked, lockErr := fixture.repository.LockAccount(txCtx, created.ID)
		if lockErr != nil {
			return lockErr
		}
		var renameErr error
		renamed, renameErr = fixture.repository.RenameAccount(
			txCtx,
			created.ID,
			fixture.userID,
			locked.AccessVersion,
			"after",
		)
		if renameErr != nil {
			return renameErr
		}
		return fixture.repository.AppendAuthorizationEvent(
			txCtx,
			selfServiceAccountPostgresEvent(
				renamed,
				fixture.userID,
				"account.updated",
				fixture.requestBase+"-rename",
				[]string{"name"},
			),
		)
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), renamed.AccessVersion)
	require.Equal(t, "after", renamed.Name)
	require.False(t, renamed.Deleted)
	require.Equal(t, 1, selfServiceAccountPostgresCount(t,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE account_id = $1", created.ID))
	require.Equal(t, 1, selfServiceAccountPostgresCount(t, `
SELECT COUNT(*)
FROM resource_authorization_events
WHERE account_id = $1 AND request_id = $2 AND resource_access_version = 2`,
		created.ID, fixture.requestBase+"-rename"))

	_, err = integrationDB.ExecContext(context.Background(),
		"DELETE FROM scheduler_outbox WHERE account_id = $1", created.ID)
	require.NoError(t, err)

	var deleted service.SelfServiceAccountState
	err = fixture.repository.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		if lockErr := fixture.repository.LockActorAuthorization(txCtx, fixture.userID); lockErr != nil {
			return lockErr
		}
		locked, lockErr := fixture.repository.LockAccount(txCtx, created.ID)
		if lockErr != nil {
			return lockErr
		}
		var deleteErr error
		deleted, deleteErr = fixture.repository.DeleteAccount(
			txCtx,
			created.ID,
			fixture.userID,
			locked.AccessVersion,
		)
		if deleteErr != nil {
			return deleteErr
		}
		return fixture.repository.AppendAuthorizationEvent(
			txCtx,
			selfServiceAccountPostgresEvent(
				deleted,
				fixture.userID,
				"account.deleted",
				fixture.requestBase+"-delete",
				[]string{"lifecycle"},
			),
		)
	})
	require.NoError(t, err)
	require.Equal(t, int64(3), deleted.AccessVersion)
	require.True(t, deleted.Deleted)

	var (
		persistedName    string
		persistedVersion int64
		deletedAt        sql.NullTime
	)
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
SELECT name, access_version, deleted_at
FROM accounts
WHERE id = $1`, created.ID).Scan(&persistedName, &persistedVersion, &deletedAt))
	require.Equal(t, "after", persistedName)
	require.Equal(t, int64(3), persistedVersion)
	require.True(t, deletedAt.Valid)
	require.Equal(t, 1, selfServiceAccountPostgresCount(t,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE account_id = $1", created.ID))
	require.Equal(t, 1, selfServiceAccountPostgresCount(t, `
SELECT COUNT(*)
FROM resource_authorization_events
WHERE account_id = $1 AND request_id = $2 AND resource_access_version = 3`,
		created.ID, fixture.requestBase+"-delete"))
}

func newSelfServiceAccountPostgresFixture(t *testing.T) *selfServiceAccountPostgresFixture {
	t.Helper()
	suffix := time.Now().UnixNano()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{
		Email: fmt.Sprintf("self-service-account-%d@example.com", suffix),
	})
	fixture := &selfServiceAccountPostgresFixture{
		client:      client,
		repository:  NewSelfServiceAccountRepository(client),
		userID:      user.ID,
		requestBase: fmt.Sprintf("ssa-%d", suffix),
	}
	t.Cleanup(func() { cleanupSelfServiceAccountPostgresFixture(t, fixture) })
	return fixture
}

func selfServiceAccountPostgresEvent(
	state service.SelfServiceAccountState,
	userID int64,
	eventType string,
	requestID string,
	changedFields []string,
) service.ResourceAuthorizationEventRecord {
	ownerUserID := userID
	return service.ResourceAuthorizationEventRecord{
		Key: service.ResourceMutationKey{
			ResourceType: authz.ResourceTypeAccount,
			ResourceID:   state.ID,
		},
		OwnerUserID:           &ownerUserID,
		ActorKind:             authz.SubjectKindUser,
		ActorID:               userID,
		AuthMethod:            authz.AuthMethodJWT,
		EventType:             eventType,
		ResourceAccessVersion: state.AccessVersion,
		RequestID:             requestID,
		ChangedFields:         append([]string(nil), changedFields...),
	}
}

func installSelfServiceAccountOutboxFailureTrigger(t *testing.T, accountName string) {
	t.Helper()
	suffix := time.Now().UnixNano()
	functionName := fmt.Sprintf("fail_self_service_account_outbox_%d", suffix)
	triggerName := fmt.Sprintf("fail_self_service_account_outbox_trigger_%d", suffix)
	_, err := integrationDB.ExecContext(context.Background(), fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.event_type = %s
       AND EXISTS (
           SELECT 1
           FROM accounts
           WHERE id = NEW.account_id
             AND name = %s
       ) THEN
        RAISE EXCEPTION 'forced self-service account outbox failure';
    END IF;
    RETURN NEW;
END;
$$`,
		functionName,
		pq.QuoteLiteral(service.SchedulerOutboxEventAccountChanged),
		pq.QuoteLiteral(accountName),
	))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON scheduler_outbox", triggerName,
		))
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			"DROP FUNCTION IF EXISTS %s()", functionName,
		))
	})
	_, err = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
		"CREATE TRIGGER %s BEFORE INSERT ON scheduler_outbox FOR EACH ROW EXECUTE FUNCTION %s()",
		triggerName,
		functionName,
	))
	require.NoError(t, err)
}

func cleanupSelfServiceAccountPostgresFixture(
	t testing.TB,
	fixture *selfServiceAccountPostgresFixture,
) {
	t.Helper()
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, `
DELETE FROM scheduler_outbox
WHERE account_id IN (
    SELECT id
    FROM accounts
    WHERE owner_user_id = $1
)`, fixture.userID)
	require.NoError(t, err)

	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "SET LOCAL session_replication_role = replica")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
DELETE FROM resource_authorization_events
WHERE actor_user_id = $1 OR resource_owner_user_id = $1`, fixture.userID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	_, err = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE owner_user_id = $1", fixture.userID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", fixture.userID)
	require.NoError(t, err)
}

func selfServiceAccountPostgresCount(t testing.TB, query string, args ...any) int {
	t.Helper()
	var count int
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), query, args...).Scan(&count))
	return count
}
