//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

type resourceMutationPostgresFixture struct {
	client      *dbent.Client
	mutation    service.ResourceMutationRepository
	accounts    *accountRepository
	groups      *groupRepository
	actorUserID int64
	account     *service.Account
	group       *service.Group
	requestBase string
}

func TestResourceMutationRepositoryCommitsBusinessVersionAuditAndOutboxPostgres(t *testing.T) {
	fixture := newResourceMutationPostgresFixture(t)
	requestID := fixture.requestBase + "-commit"
	accountName := fixture.account.Name + "-committed"
	groupName := fixture.group.Name + "-committed"
	keys := fixture.resourceKeys()

	err := fixture.mutation.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		locked, lockErr := fixture.mutation.LockResources(txCtx, keys)
		if lockErr != nil {
			return lockErr
		}
		for _, key := range keys {
			if locked[key].AccessVersion != 1 {
				return fmt.Errorf("unexpected initial access version for %v", key)
			}
		}
		fixture.account.Name = accountName
		if updateErr := fixture.accounts.Update(txCtx, fixture.account); updateErr != nil {
			return updateErr
		}
		fixture.group.Name = groupName
		if updateErr := fixture.groups.Update(txCtx, fixture.group); updateErr != nil {
			return updateErr
		}
		postStates, versionErr := fixture.mutation.IncrementAccessVersions(txCtx, keys)
		if versionErr != nil {
			return versionErr
		}
		return fixture.mutation.AppendAuthorizationEvents(
			txCtx,
			fixture.authorizationEvents(postStates, requestID, fixture.actorUserID),
		)
	})

	require.NoError(t, err)
	requireResourceMutationAccountState(t, fixture.account.ID, accountName, 2)
	requireResourceMutationGroupState(t, fixture.group.ID, groupName, 2)
	require.Equal(t, 2, resourceMutationEventCount(t, requestID))
	require.Equal(t, 1, resourceMutationOutboxCount(t, service.SchedulerOutboxEventAccountChanged, fixture.account.ID, 0))
	require.Equal(t, 1, resourceMutationOutboxCount(t, service.SchedulerOutboxEventGroupChanged, 0, fixture.group.ID))
}

func TestResourceMutationRepositoryAuditFailureRollsBackEverythingPostgres(t *testing.T) {
	fixture := newResourceMutationPostgresFixture(t)
	requestID := fixture.requestBase + "-audit-failure"
	originalAccountName := fixture.account.Name
	originalGroupName := fixture.group.Name
	keys := fixture.resourceKeys()

	err := fixture.mutation.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		fixture.account.Name += "-must-rollback"
		if updateErr := fixture.accounts.Update(txCtx, fixture.account); updateErr != nil {
			return updateErr
		}
		fixture.group.Name += "-must-rollback"
		if updateErr := fixture.groups.Update(txCtx, fixture.group); updateErr != nil {
			return updateErr
		}
		postStates, versionErr := fixture.mutation.IncrementAccessVersions(txCtx, keys)
		if versionErr != nil {
			return versionErr
		}
		return fixture.mutation.AppendAuthorizationEvents(
			txCtx,
			fixture.authorizationEvents(postStates, requestID, math.MaxInt64),
		)
	})

	require.Error(t, err)
	requireResourceMutationAccountState(t, fixture.account.ID, originalAccountName, 1)
	requireResourceMutationGroupState(t, fixture.group.ID, originalGroupName, 1)
	require.Zero(t, resourceMutationEventCount(t, requestID))
	require.Zero(t, resourceMutationOutboxCount(t, service.SchedulerOutboxEventAccountChanged, fixture.account.ID, 0))
	require.Zero(t, resourceMutationOutboxCount(t, service.SchedulerOutboxEventGroupChanged, 0, fixture.group.ID))
}

func TestResourceMutationRepositoryOutboxFailureRollsBackBusinessVersionAndAuditPostgres(t *testing.T) {
	fixture := newResourceMutationPostgresFixture(t)
	requestID := fixture.requestBase + "-outbox-failure"
	originalAccountName := fixture.account.Name
	originalGroupName := fixture.group.Name
	keys := fixture.resourceKeys()
	installResourceMutationOutboxFailureTrigger(t, fixture.group.ID)

	err := fixture.mutation.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		postStates, versionErr := fixture.mutation.IncrementAccessVersions(txCtx, keys)
		if versionErr != nil {
			return versionErr
		}
		if auditErr := fixture.mutation.AppendAuthorizationEvents(
			txCtx,
			fixture.authorizationEvents(postStates, requestID, fixture.actorUserID),
		); auditErr != nil {
			return auditErr
		}

		accountKey := service.ResourceMutationKey{ResourceType: authz.ResourceTypeAccount, ResourceID: fixture.account.ID}
		fixture.account.AccessVersion = postStates[accountKey].AccessVersion
		fixture.account.Name += "-must-rollback"
		if updateErr := fixture.accounts.Update(txCtx, fixture.account); updateErr != nil {
			return updateErr
		}
		fixture.group.Name += "-must-rollback"
		return fixture.groups.Update(txCtx, fixture.group)
	})

	require.ErrorContains(t, err, "forced resource mutation scheduler outbox failure")
	requireResourceMutationAccountState(t, fixture.account.ID, originalAccountName, 1)
	requireResourceMutationGroupState(t, fixture.group.ID, originalGroupName, 1)
	require.Zero(t, resourceMutationEventCount(t, requestID))
	require.Zero(t, resourceMutationOutboxCount(t, service.SchedulerOutboxEventAccountChanged, fixture.account.ID, 0))
	require.Zero(t, resourceMutationOutboxCount(t, service.SchedulerOutboxEventGroupChanged, 0, fixture.group.ID))
}

func TestResourceMutationRepositoryAuthOutboxFailureRollsBackEverythingPostgres(t *testing.T) {
	fixture := newResourceMutationPostgresFixture(t)
	requestID := fixture.requestBase + "-auth-outbox-failure"
	originalAccountName := fixture.account.Name
	originalGroupName := fixture.group.Name
	originalExclusive := fixture.group.IsExclusive
	keys := fixture.resourceKeys()

	apiKeyValue := fmt.Sprintf("sk-resource-mutation-auth-%d", time.Now().UnixNano())
	apiKey, err := fixture.client.APIKey.Create().
		SetUserID(fixture.actorUserID).
		SetGroupID(fixture.group.ID).
		SetKey(apiKeyValue).
		SetName("resource mutation auth outbox").
		Save(context.Background())
	require.NoError(t, err)
	cacheKeyBytes := sha256.Sum256([]byte(apiKeyValue))
	cacheKey := hex.EncodeToString(cacheKeyBytes[:])
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM api_keys WHERE id = $1", apiKey.ID)
		_, _ = integrationDB.ExecContext(context.Background(),
			"DELETE FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey)
	})
	_, err = integrationDB.ExecContext(context.Background(),
		"DELETE FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey)
	require.NoError(t, err)
	installResourceMutationAuthOutboxSecondInsertFailureTrigger(t, cacheKey)

	err = fixture.mutation.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		postStates, versionErr := fixture.mutation.IncrementAccessVersions(txCtx, keys)
		if versionErr != nil {
			return versionErr
		}
		if auditErr := fixture.mutation.AppendAuthorizationEvents(
			txCtx,
			fixture.authorizationEvents(postStates, requestID, fixture.actorUserID),
		); auditErr != nil {
			return auditErr
		}

		fixture.account.Name += "-must-rollback"
		if updateErr := fixture.accounts.Update(txCtx, fixture.account); updateErr != nil {
			return updateErr
		}
		fixture.group.Name += "-must-rollback"
		fixture.group.IsExclusive = !fixture.group.IsExclusive
		return fixture.groups.Update(txCtx, fixture.group)
	})

	require.ErrorContains(t, err, "forced resource mutation auth outbox failure")
	requireResourceMutationAccountState(t, fixture.account.ID, originalAccountName, 1)
	requireResourceMutationGroupState(t, fixture.group.ID, originalGroupName, 1)
	var persistedExclusive bool
	require.NoError(t, integrationDB.QueryRowContext(
		context.Background(),
		"SELECT is_exclusive FROM groups WHERE id = $1",
		fixture.group.ID,
	).Scan(&persistedExclusive))
	require.Equal(t, originalExclusive, persistedExclusive)
	require.Zero(t, resourceMutationEventCount(t, requestID))
	require.Zero(t, resourceMutationOutboxCount(t, service.SchedulerOutboxEventAccountChanged, fixture.account.ID, 0))
	require.Zero(t, resourceMutationOutboxCount(t, service.SchedulerOutboxEventGroupChanged, 0, fixture.group.ID))
	require.Zero(t, resourceMutationAuthOutboxCount(t, cacheKey))
}

func newResourceMutationPostgresFixture(t *testing.T) *resourceMutationPostgresFixture {
	t.Helper()
	client := testEntClient(t)
	suffix := time.Now().UnixNano()
	actor := mustCreateUser(t, client, &service.User{
		Email: fmt.Sprintf("resource-mutation-actor-%d@example.com", suffix),
	})
	account := mustCreateAccount(t, client, &service.Account{
		Name: fmt.Sprintf("resource-mutation-account-%d", suffix),
	})
	group := mustCreateGroup(t, client, &service.Group{
		Name:           fmt.Sprintf("resource-mutation-group-%d", suffix),
		RateMultiplier: 1,
	})
	accountRepo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	groupRepo := newGroupRepositoryWithSQL(client, integrationDB)
	loadedAccount, err := accountRepo.GetByID(context.Background(), account.ID)
	require.NoError(t, err)
	loadedGroup, err := groupRepo.GetByIDLite(context.Background(), group.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, loadedAccount.AccessVersion)
	require.EqualValues(t, 1, loadedGroup.AccessVersion)

	fixture := &resourceMutationPostgresFixture{
		client:      client,
		mutation:    NewResourceMutationRepository(client),
		accounts:    accountRepo,
		groups:      groupRepo,
		actorUserID: actor.ID,
		account:     loadedAccount,
		group:       loadedGroup,
		requestBase: fmt.Sprintf("rm-%d", suffix),
	}
	t.Cleanup(func() { cleanupResourceMutationPostgresFixture(t, fixture) })
	return fixture
}

func (f *resourceMutationPostgresFixture) resourceKeys() []service.ResourceMutationKey {
	return []service.ResourceMutationKey{
		{ResourceType: authz.ResourceTypeAccount, ResourceID: f.account.ID},
		{ResourceType: authz.ResourceTypeGroup, ResourceID: f.group.ID},
	}
}

func (f *resourceMutationPostgresFixture) authorizationEvents(
	states map[service.ResourceMutationKey]service.ResourceMutationState,
	requestID string,
	groupActorUserID int64,
) []service.ResourceAuthorizationEventRecord {
	accountKey := service.ResourceMutationKey{ResourceType: authz.ResourceTypeAccount, ResourceID: f.account.ID}
	groupKey := service.ResourceMutationKey{ResourceType: authz.ResourceTypeGroup, ResourceID: f.group.ID}
	return []service.ResourceAuthorizationEventRecord{
		{
			Key:                   accountKey,
			OwnerUserID:           states[accountKey].OwnerUserID,
			ActorKind:             authz.SubjectKindUser,
			ActorID:               f.actorUserID,
			AuthMethod:            authz.AuthMethodJWT,
			EventType:             "account.updated",
			ResourceAccessVersion: states[accountKey].AccessVersion,
			RequestID:             requestID,
			ChangedFields:         []string{"configuration"},
		},
		{
			Key:                   groupKey,
			OwnerUserID:           states[groupKey].OwnerUserID,
			ActorKind:             authz.SubjectKindUser,
			ActorID:               groupActorUserID,
			AuthMethod:            authz.AuthMethodJWT,
			EventType:             "group.updated",
			ResourceAccessVersion: states[groupKey].AccessVersion,
			RequestID:             requestID,
			ChangedFields:         []string{"configuration"},
		},
	}
}

func installResourceMutationOutboxFailureTrigger(t *testing.T, groupID int64) {
	t.Helper()
	suffix := time.Now().UnixNano()
	functionName := fmt.Sprintf("fail_resource_mutation_outbox_%d", suffix)
	triggerName := fmt.Sprintf("fail_resource_mutation_outbox_trigger_%d", suffix)
	_, err := integrationDB.ExecContext(context.Background(), fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.group_id = %d THEN
        RAISE EXCEPTION 'forced resource mutation scheduler outbox failure';
    END IF;
    RETURN NEW;
END;
$$`, functionName, groupID))
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

func installResourceMutationAuthOutboxSecondInsertFailureTrigger(t *testing.T, cacheKey string) {
	t.Helper()
	suffix := time.Now().UnixNano()
	functionName := fmt.Sprintf("fail_resource_mutation_auth_outbox_%d", suffix)
	triggerName := fmt.Sprintf("fail_resource_mutation_auth_outbox_trigger_%d", suffix)
	_, err := integrationDB.ExecContext(context.Background(), fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.cache_key = %s
       AND EXISTS (
           SELECT 1
           FROM auth_cache_invalidation_outbox
           WHERE cache_key = NEW.cache_key
       ) THEN
        RAISE EXCEPTION 'forced resource mutation auth outbox failure';
    END IF;
    RETURN NEW;
END;
$$`, functionName, pq.QuoteLiteral(cacheKey)))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON auth_cache_invalidation_outbox", triggerName,
		))
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			"DROP FUNCTION IF EXISTS %s()", functionName,
		))
	})
	_, err = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
		"CREATE TRIGGER %s BEFORE INSERT ON auth_cache_invalidation_outbox FOR EACH ROW EXECUTE FUNCTION %s()",
		triggerName,
		functionName,
	))
	require.NoError(t, err)
}

func cleanupResourceMutationPostgresFixture(t testing.TB, fixture *resourceMutationPostgresFixture) {
	t.Helper()
	ctx := context.Background()
	_, err := integrationDB.ExecContext(
		ctx,
		"DELETE FROM scheduler_outbox WHERE account_id = $1 OR group_id = $2",
		fixture.account.ID,
		fixture.group.ID,
	)
	require.NoError(t, err)

	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "SET LOCAL session_replication_role = replica")
	require.NoError(t, err)
	_, err = tx.ExecContext(
		ctx,
		"DELETE FROM resource_authorization_events WHERE request_id LIKE $1",
		fixture.requestBase+"%",
	)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	_, err = integrationDB.ExecContext(ctx, "DELETE FROM accounts WHERE id = $1", fixture.account.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", fixture.group.ID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", fixture.actorUserID)
	require.NoError(t, err)
}

func requireResourceMutationAccountState(t testing.TB, accountID int64, wantName string, wantVersion int64) {
	t.Helper()
	var name string
	var version int64
	require.NoError(t, integrationDB.QueryRowContext(
		context.Background(),
		"SELECT name, access_version FROM accounts WHERE id = $1",
		accountID,
	).Scan(&name, &version))
	require.Equal(t, wantName, name)
	require.Equal(t, wantVersion, version)
}

func requireResourceMutationGroupState(t testing.TB, groupID int64, wantName string, wantVersion int64) {
	t.Helper()
	var name string
	var version int64
	require.NoError(t, integrationDB.QueryRowContext(
		context.Background(),
		"SELECT name, access_version FROM groups WHERE id = $1",
		groupID,
	).Scan(&name, &version))
	require.Equal(t, wantName, name)
	require.Equal(t, wantVersion, version)
}

func resourceMutationEventCount(t testing.TB, requestID string) int {
	t.Helper()
	var count int
	require.NoError(t, integrationDB.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM resource_authorization_events WHERE request_id = $1",
		requestID,
	).Scan(&count))
	return count
}

func resourceMutationOutboxCount(t testing.TB, eventType string, accountID, groupID int64) int {
	t.Helper()
	var count int
	require.NoError(t, integrationDB.QueryRowContext(
		context.Background(),
		`SELECT COUNT(*)
           FROM scheduler_outbox
          WHERE event_type = $1
            AND ($2 = 0 OR account_id = $2)
            AND ($3 = 0 OR group_id = $3)`,
		eventType,
		accountID,
		groupID,
	).Scan(&count))
	return count
}

func resourceMutationAuthOutboxCount(t testing.TB, cacheKey string) int {
	t.Helper()
	var count int
	require.NoError(t, integrationDB.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM auth_cache_invalidation_outbox WHERE cache_key = $1",
		cacheKey,
	).Scan(&count))
	return count
}
