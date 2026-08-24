//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestAuthorizationExpiryRepositoryAtomicallyConvergesAllSourceTypesPostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newResourceMutationPostgresFixture(t)
	suffix := time.Now().UnixNano()
	roleCode := fmt.Sprintf("expiry-role-%d", suffix)
	principalCode := fmt.Sprintf("expiry-principal-%d", suffix)
	expiresAt := time.Now().UTC().Add(-time.Minute)

	var roleID, principalID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
INSERT INTO roles (code, name)
VALUES ($1, $2)
RETURNING id
`, roleCode, roleCode).Scan(&roleID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
INSERT INTO service_principals (code, name, status)
VALUES ($1, $2, 'active')
RETURNING id
`, principalCode, principalCode).Scan(&principalID))

	var userRoleID, principalRoleID, accountGrantID, groupGrantID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
INSERT INTO user_roles (user_id, role_id, granted_by_user_id, expires_at)
VALUES ($1, $2, $1, $3)
RETURNING id
`, fixture.actorUserID, roleID, expiresAt).Scan(&userRoleID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
INSERT INTO service_principal_roles (service_principal_id, role_id, granted_by_user_id, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING id
`, principalID, roleID, fixture.actorUserID, expiresAt).Scan(&principalRoleID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
INSERT INTO account_access_grants (
    account_id, grantee_user_id, access_level, granted_by_user_id, expires_at
)
VALUES ($1, $2, 'viewer', $2, $3)
RETURNING id
`, fixture.account.ID, fixture.actorUserID, expiresAt).Scan(&accountGrantID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
INSERT INTO group_access_grants (
    group_id, grantee_user_id, access_level, granted_by_user_id, expires_at
)
VALUES ($1, $2, 'viewer', $2, $3)
RETURNING id
`, fixture.group.ID, fixture.actorUserID, expiresAt).Scan(&groupGrantID))
	sourceIDs := map[string]int64{
		service.AuthorizationExpirySourceUserRole:             userRoleID,
		service.AuthorizationExpirySourceServicePrincipalRole: principalRoleID,
		service.AuthorizationExpirySourceAccountAccessGrant:   accountGrantID,
		service.AuthorizationExpirySourceGroupAccessGrant:     groupGrantID,
	}

	apiKeyValue := fmt.Sprintf("sk-authorization-expiry-%d", suffix)
	apiKey, err := fixture.client.APIKey.Create().
		SetUserID(fixture.actorUserID).
		SetGroupID(fixture.group.ID).
		SetKey(apiKeyValue).
		SetName("authorization expiry integration").
		Save(ctx)
	require.NoError(t, err)
	cacheHash := sha256.Sum256([]byte(apiKeyValue))
	cacheKey := hex.EncodeToString(cacheHash[:])
	require.NoError(t, clearAuthorizationExpiryFixtureOutboxes(ctx, cacheKey, fixture.account.ID, fixture.group.ID))

	t.Cleanup(func() {
		cleanupAuthorizationExpiryFixture(
			t, roleID, principalID, apiKey.ID, cacheKey, fixture.account.ID, fixture.group.ID, sourceIDs,
		)
	})

	initialVersions := authorizationExpiryFixtureVersions(t, fixture.actorUserID, principalID, fixture.account.ID, fixture.group.ID)
	repo := NewAuthorizationExpiryRepository(integrationDB)
	jobs, err := repo.Claim(ctx, "expiry-integration-worker", 10, 30*time.Second)
	require.NoError(t, err)
	claimed := make(map[string]service.AuthorizationExpiryJob, len(jobs))
	for _, job := range jobs {
		if sourceIDs[job.SourceType] == job.SourceID {
			claimed[job.SourceType] = job
		}
	}
	require.Len(t, claimed, 4)

	for _, sourceType := range []string{
		service.AuthorizationExpirySourceUserRole,
		service.AuthorizationExpirySourceServicePrincipalRole,
		service.AuthorizationExpirySourceAccountAccessGrant,
		service.AuthorizationExpirySourceGroupAccessGrant,
	} {
		result, processErr := repo.ProcessClaimed(ctx, claimed[sourceType], "expiry-integration-worker")
		require.NoError(t, processErr, sourceType)
		require.True(t, result.Processed, sourceType)
	}

	finalVersions := authorizationExpiryFixtureVersions(t, fixture.actorUserID, principalID, fixture.account.ID, fixture.group.ID)
	for index := range initialVersions {
		require.Equal(t, initialVersions[index]+1, finalVersions[index])
	}
	var processed int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM authorization_expiry_jobs
WHERE processed_at IS NOT NULL
  AND (source_type, source_id) IN (
      ('user_role', $1),
      ('service_principal_role', $2),
      ('account_access_grant', $3),
      ('group_access_grant', $4)
  )
`,
		sourceIDs[service.AuthorizationExpirySourceUserRole],
		sourceIDs[service.AuthorizationExpirySourceServicePrincipalRole],
		sourceIDs[service.AuthorizationExpirySourceAccountAccessGrant],
		sourceIDs[service.AuthorizationExpirySourceGroupAccessGrant],
	).Scan(&processed))
	require.Equal(t, 4, processed)

	var auditCount, resourceEventCount, schedulerCount, authInvalidationCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM audit_logs
WHERE request_id IN ($1, $2)
	`, authorizationExpiryRequestID(claimed[service.AuthorizationExpirySourceUserRole]),
		authorizationExpiryRequestID(claimed[service.AuthorizationExpirySourceServicePrincipalRole])).Scan(&auditCount))
	require.Equal(t, 2, auditCount)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM resource_authorization_events
WHERE request_id IN ($1, $2)
	`, authorizationExpiryRequestID(claimed[service.AuthorizationExpirySourceAccountAccessGrant]),
		authorizationExpiryRequestID(claimed[service.AuthorizationExpirySourceGroupAccessGrant])).Scan(&resourceEventCount))
	require.Equal(t, 2, resourceEventCount)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM scheduler_outbox
WHERE (account_id = $1 OR group_id = $2)
  AND payload->>'reason' = 'authorization_expiry'
`, fixture.account.ID, fixture.group.ID).Scan(&schedulerCount))
	require.Equal(t, 2, schedulerCount)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM auth_cache_invalidation_outbox WHERE cache_key = $1",
		cacheKey,
	).Scan(&authInvalidationCount))
	require.GreaterOrEqual(t, authInvalidationCount, 2)

	result, err := repo.ProcessClaimed(ctx, claimed[service.AuthorizationExpirySourceUserRole], "expiry-integration-worker")
	require.NoError(t, err)
	require.False(t, result.Processed)
	require.Equal(t, finalVersions, authorizationExpiryFixtureVersions(
		t, fixture.actorUserID, principalID, fixture.account.ID, fixture.group.ID,
	))
}

func TestAuthorizationExpiryRepositoryRecoversLeasesAndReleasesOnlyOwnedClaimsPostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newResourceMutationPostgresFixture(t)
	expiresAt := time.Now().UTC().Add(-time.Minute)
	_, sourceID := createAuthorizationExpiryUserRole(t, fixture.actorUserID, expiresAt)
	repo := NewAuthorizationExpiryRepository(integrationDB)

	first := claimAuthorizationExpiryJob(t, repo, "expiry-lease-a", service.AuthorizationExpirySourceUserRole, sourceID, 30*time.Second)
	jobs, err := repo.Claim(ctx, "expiry-lease-b", 1000, 30*time.Second)
	require.NoError(t, err)
	require.NotContains(t, authorizationExpiryJobIDs(jobs), first.ID)
	require.NoError(t, releaseAuthorizationExpiryJobs(ctx, repo, "expiry-lease-b", jobs))

	_, err = integrationDB.ExecContext(ctx, `
UPDATE authorization_expiry_jobs
SET claimed_at = statement_timestamp() - INTERVAL '1 minute'
WHERE id = $1
`, first.ID)
	require.NoError(t, err)
	second := claimAuthorizationExpiryJob(t, repo, "expiry-lease-b", service.AuthorizationExpirySourceUserRole, sourceID, time.Second)
	require.Equal(t, first.ID, second.ID)

	require.NoError(t, repo.ReleaseClaims(ctx, "expiry-lease-a", []int64{second.ID}))
	var owner string
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT claimed_by FROM authorization_expiry_jobs WHERE id = $1", second.ID,
	).Scan(&owner))
	require.Equal(t, "expiry-lease-b", owner)

	require.NoError(t, repo.ReleaseClaims(ctx, "expiry-lease-b", []int64{second.ID}))
	third := claimAuthorizationExpiryJob(t, repo, "expiry-lease-c", service.AuthorizationExpirySourceUserRole, sourceID, 30*time.Second)
	require.Equal(t, second.ID, third.ID)
}

func TestAuthorizationExpiryRepositoryRearmsGenerationAndKeepsExactOnceEffectsPostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newResourceMutationPostgresFixture(t)
	firstExpiry := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Microsecond)
	_, sourceID := createAuthorizationExpiryUserRole(t, fixture.actorUserID, firstExpiry)
	repo := NewAuthorizationExpiryRepository(integrationDB)
	initialVersion := authorizationExpiryUserVersion(t, fixture.actorUserID)

	first := claimAuthorizationExpiryJob(t, repo, "expiry-rearm-a", service.AuthorizationExpirySourceUserRole, sourceID, 30*time.Second)
	result, err := repo.ProcessClaimed(ctx, first, "expiry-rearm-a")
	require.NoError(t, err)
	require.True(t, result.Processed)

	secondExpiry := firstExpiry.Add(time.Minute)
	_, err = integrationDB.ExecContext(ctx, `
UPDATE user_roles
SET expires_at = $2, updated_at = statement_timestamp()
WHERE id = $1
`, sourceID, secondExpiry)
	require.NoError(t, err)
	second := claimAuthorizationExpiryJob(t, repo, "expiry-rearm-b", service.AuthorizationExpirySourceUserRole, sourceID, 30*time.Second)
	require.Equal(t, first.ID, second.ID)
	require.True(t, secondExpiry.Equal(second.ExpiresAt))
	require.NotEqual(t, authorizationExpiryRequestID(first), authorizationExpiryRequestID(second))

	result, err = repo.ProcessClaimed(ctx, second, "expiry-rearm-b")
	require.NoError(t, err)
	require.True(t, result.Processed)
	result, err = repo.ProcessClaimed(ctx, second, "expiry-rearm-b")
	require.NoError(t, err)
	require.False(t, result.Processed)

	var version int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT authz_version FROM users WHERE id = $1", fixture.actorUserID,
	).Scan(&version))
	require.Equal(t, initialVersion+2, version)
	var auditCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM audit_logs
WHERE request_id IN ($1, $2)
`, authorizationExpiryRequestID(first), authorizationExpiryRequestID(second)).Scan(&auditCount))
	require.Equal(t, 2, auditCount)
}

func TestAuthorizationExpiryRepositoryFinishesOrphanedJobWithoutAuthorizationEffectsPostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newResourceMutationPostgresFixture(t)
	repo := NewAuthorizationExpiryRepository(integrationDB)
	expiresAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	var sourceID, jobID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(id), 0) + 1000000 FROM user_roles",
	).Scan(&sourceID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
INSERT INTO authorization_expiry_jobs (source_type, source_id, expires_at, available_at)
VALUES ('user_role', $1, $2, $2)
RETURNING id
`, sourceID, expiresAt).Scan(&jobID))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(),
			"DELETE FROM authorization_expiry_jobs WHERE id = $1", jobID)
	})
	initialVersion := authorizationExpiryUserVersion(t, fixture.actorUserID)

	job := claimAuthorizationExpiryJob(t, repo, "expiry-orphan", service.AuthorizationExpirySourceUserRole, sourceID, 30*time.Second)
	result, err := repo.ProcessClaimed(ctx, job, "expiry-orphan")
	require.NoError(t, err)
	require.True(t, result.Processed)
	require.True(t, result.SourceMissing)

	var processed bool
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT processed_at IS NOT NULL FROM authorization_expiry_jobs WHERE id = $1", job.ID,
	).Scan(&processed))
	require.True(t, processed)
	var version int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT authz_version FROM users WHERE id = $1", fixture.actorUserID,
	).Scan(&version))
	require.Equal(t, initialVersion, version)
}

func TestAuthorizationExpiryRepositoryRollsBackVersionAndJobOnAuditFailurePostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newResourceMutationPostgresFixture(t)
	expiresAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	_, sourceID := createAuthorizationExpiryUserRole(t, fixture.actorUserID, expiresAt)
	repo := NewAuthorizationExpiryRepository(integrationDB)
	job := claimAuthorizationExpiryJob(t, repo, "expiry-rollback", service.AuthorizationExpirySourceUserRole, sourceID, 30*time.Second)
	requestID := authorizationExpiryRequestID(job)
	dropFailureTrigger := installAuthorizationExpiryAuditFailureTrigger(t, requestID)
	var initialVersion int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT authz_version FROM users WHERE id = $1", fixture.actorUserID,
	).Scan(&initialVersion))

	result, err := repo.ProcessClaimed(ctx, job, "expiry-rollback")
	require.ErrorContains(t, err, "forced authorization expiry audit failure")
	require.False(t, result.Processed)
	var (
		version   int64
		owner     string
		processed bool
	)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT authz_version FROM users WHERE id = $1", fixture.actorUserID,
	).Scan(&version))
	require.Equal(t, initialVersion, version)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT claimed_by, processed_at IS NOT NULL
FROM authorization_expiry_jobs
WHERE id = $1
`, job.ID).Scan(&owner, &processed))
	require.Equal(t, "expiry-rollback", owner)
	require.False(t, processed)
	var auditCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM audit_logs WHERE request_id = $1", requestID,
	).Scan(&auditCount))
	require.Zero(t, auditCount)

	dropFailureTrigger()
	result, err = repo.ProcessClaimed(ctx, job, "expiry-rollback")
	require.NoError(t, err)
	require.True(t, result.Processed)
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT authz_version FROM users WHERE id = $1", fixture.actorUserID,
	).Scan(&version))
	require.Equal(t, initialVersion+1, version)
}

func TestAuthorizationExpiryRepositoryParentFirstLockAvoidsManagementDeadlockPostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newResourceMutationPostgresFixture(t)
	firstExpiry := time.Now().UTC().Add(-2 * time.Minute).Truncate(time.Microsecond)
	var sourceID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
INSERT INTO account_access_grants (
    account_id, grantee_user_id, access_level, granted_by_user_id, expires_at
)
VALUES ($1, $2, 'viewer', $2, $3)
RETURNING id
`, fixture.account.ID, fixture.actorUserID, firstExpiry).Scan(&sourceID))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(),
			"DELETE FROM account_access_grants WHERE id = $1", sourceID)
	})
	t.Cleanup(func() {
		cleanupAuthorizationExpiryResourceEvents(sourceID)
	})
	repo := NewAuthorizationExpiryRepository(integrationDB)
	job := claimAuthorizationExpiryJob(t, repo, "expiry-lock-order-a", service.AuthorizationExpirySourceAccountAccessGrant, sourceID, 30*time.Second)
	initialVersion := authorizationExpiryAccountVersion(t, fixture.account.ID)

	manager, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = manager.Rollback() }()
	_, err = manager.ExecContext(ctx, "SET LOCAL lock_timeout = '3s'")
	require.NoError(t, err)
	var ignored int64
	require.NoError(t, manager.QueryRowContext(ctx,
		"SELECT id FROM accounts WHERE id = $1 FOR UPDATE", fixture.account.ID,
	).Scan(&ignored))

	type processOutcome struct {
		result service.AuthorizationExpiryResult
		err    error
	}
	processDone := make(chan processOutcome, 1)
	go func() {
		result, processErr := repo.ProcessClaimed(context.Background(), job, "expiry-lock-order-a")
		processDone <- processOutcome{result: result, err: processErr}
	}()
	waitForAuthorizationExpiryParentLock(t, "accounts")

	secondExpiry := firstExpiry.Add(time.Minute)
	_, err = manager.ExecContext(ctx, `
UPDATE account_access_grants
SET expires_at = $2, updated_at = statement_timestamp()
WHERE id = $1
`, sourceID, secondExpiry)
	require.NoError(t, err)
	require.NoError(t, manager.Commit())

	select {
	case outcome := <-processDone:
		require.False(t, outcome.result.Processed)
		var pgErr *pq.Error
		require.Error(t, outcome.err)
		require.True(t, errors.As(outcome.err, &pgErr), outcome.err)
		require.Equal(t, pq.ErrorCode("40001"), pgErr.Code)
	case <-time.After(5 * time.Second):
		t.Fatal("authorization expiry processing remained blocked after management commit")
	}

	rearmed := claimAuthorizationExpiryJob(t, repo, "expiry-lock-order-b", service.AuthorizationExpirySourceAccountAccessGrant, sourceID, 30*time.Second)
	require.True(t, secondExpiry.Equal(rearmed.ExpiresAt))
	result, err := repo.ProcessClaimed(ctx, rearmed, "expiry-lock-order-b")
	require.NoError(t, err)
	require.True(t, result.Processed)
	var version int64
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT access_version FROM accounts WHERE id = $1", fixture.account.ID,
	).Scan(&version))
	require.Equal(t, initialVersion+1, version)
}

func TestAuthorizationExpiryRepositoryCoordinatorLockAndReadinessPostgres(t *testing.T) {
	ctx := context.Background()
	fixture := newResourceMutationPostgresFixture(t)
	expiresAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
	roleID, sourceID := createAuthorizationExpiryUserRole(t, fixture.actorUserID, expiresAt)
	repo := NewAuthorizationExpiryRepository(integrationDB)
	job := claimAuthorizationExpiryJob(t, repo, "expiry-coordinator-lock", service.AuthorizationExpirySourceUserRole, sourceID, 30*time.Second)
	requestID := authorizationExpiryRequestID(job)
	var coordinatorID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT id
FROM service_principals
WHERE code = $1
`, authorizationExpiryCoordinatorCode).Scan(&coordinatorID))
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = integrationDB.ExecContext(cleanupCtx, `
DELETE FROM service_principal_roles
WHERE service_principal_id = $1 AND role_id = $2
`, coordinatorID, roleID)
		_, _ = integrationDB.ExecContext(cleanupCtx, `
UPDATE service_principals
SET status = 'active'
WHERE id = $1
`, coordinatorID)
	})
	stats, err := repo.Stats(ctx)
	require.NoError(t, err)
	require.True(t, stats.CoordinatorReady)

	const advisoryKey int64 = 811024238
	blocker, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback() }()
	require.NoError(t, blocker.QueryRowContext(ctx,
		"SELECT pg_advisory_xact_lock($1)", advisoryKey,
	).Scan(new(any)))
	installAuthorizationExpiryAuditAdvisoryLockTrigger(t, requestID, advisoryKey)

	type processOutcome struct {
		result service.AuthorizationExpiryResult
		err    error
	}
	processDone := make(chan processOutcome, 1)
	go func() {
		result, processErr := repo.ProcessClaimed(context.Background(), job, "expiry-coordinator-lock")
		processDone <- processOutcome{result: result, err: processErr}
	}()
	waitForAuthorizationExpiryQueryLock(t, "INSERT INTO audit_logs")

	expectAuthorizationExpiryCoordinatorWriteBlocked(t, `
UPDATE service_principals
SET status = 'disabled'
WHERE id = $1
`, coordinatorID)
	expectAuthorizationExpiryCoordinatorWriteBlocked(t, `
INSERT INTO service_principal_roles (
    service_principal_id, role_id, granted_by_user_id
)
VALUES ($1, $2, $3)
`, coordinatorID, roleID, fixture.actorUserID)
	require.NoError(t, blocker.Commit())

	select {
	case outcome := <-processDone:
		require.NoError(t, outcome.err)
		require.True(t, outcome.result.Processed)
	case <-time.After(5 * time.Second):
		t.Fatal("authorization expiry processing remained blocked after advisory lock release")
	}

	_, err = integrationDB.ExecContext(ctx, `
INSERT INTO service_principal_roles (
    service_principal_id, role_id, granted_by_user_id
)
VALUES ($1, $2, $3)
`, coordinatorID, roleID, fixture.actorUserID)
	require.NoError(t, err)
	stats, err = repo.Stats(ctx)
	require.NoError(t, err)
	require.False(t, stats.CoordinatorReady)
	_, err = integrationDB.ExecContext(ctx, `
DELETE FROM service_principal_roles
WHERE service_principal_id = $1 AND role_id = $2
`, coordinatorID, roleID)
	require.NoError(t, err)

	_, err = integrationDB.ExecContext(ctx, `
UPDATE service_principals
SET status = 'disabled'
WHERE id = $1
`, coordinatorID)
	require.NoError(t, err)
	stats, err = repo.Stats(ctx)
	require.NoError(t, err)
	require.False(t, stats.CoordinatorReady)
	_, err = integrationDB.ExecContext(ctx, `
UPDATE service_principals
SET status = 'active'
WHERE id = $1
`, coordinatorID)
	require.NoError(t, err)
}

func authorizationExpiryFixtureVersions(t testing.TB, userID, principalID, accountID, groupID int64) []int64 {
	t.Helper()
	versions := make([]int64, 4)
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
SELECT
    (SELECT authz_version FROM users WHERE id = $1),
    (SELECT authz_version FROM service_principals WHERE id = $2),
    (SELECT access_version FROM accounts WHERE id = $3),
    (SELECT access_version FROM groups WHERE id = $4)
`, userID, principalID, accountID, groupID).Scan(&versions[0], &versions[1], &versions[2], &versions[3]))
	return versions
}

func authorizationExpiryUserVersion(t testing.TB, userID int64) int64 {
	t.Helper()
	var version int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(),
		"SELECT authz_version FROM users WHERE id = $1", userID,
	).Scan(&version))
	return version
}

func authorizationExpiryAccountVersion(t testing.TB, accountID int64) int64 {
	t.Helper()
	var version int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(),
		"SELECT access_version FROM accounts WHERE id = $1", accountID,
	).Scan(&version))
	return version
}

func createAuthorizationExpiryUserRole(t testing.TB, userID int64, expiresAt time.Time) (int64, int64) {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	roleCode := fmt.Sprintf("expiry-user-role-%d", suffix)
	var roleID, sourceID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
INSERT INTO roles (code, name)
VALUES ($1, $1)
RETURNING id
`, roleCode).Scan(&roleID))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
INSERT INTO user_roles (user_id, role_id, granted_by_user_id, expires_at)
VALUES ($1, $2, $1, $3)
RETURNING id
`, userID, roleID, expiresAt).Scan(&sourceID))
	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = integrationDB.ExecContext(cleanupCtx, "DELETE FROM user_roles WHERE id = $1", sourceID)
		_, _ = integrationDB.ExecContext(cleanupCtx, `
DELETE FROM audit_logs
WHERE extra->>'source_type' = 'user_role'
  AND (extra->>'source_id')::BIGINT = $1
`, sourceID)
		_, _ = integrationDB.ExecContext(cleanupCtx, "DELETE FROM roles WHERE id = $1", roleID)
	})
	return roleID, sourceID
}

func claimAuthorizationExpiryJob(
	t testing.TB,
	repo service.AuthorizationExpiryRepository,
	workerID, sourceType string,
	sourceID int64,
	lease time.Duration,
) service.AuthorizationExpiryJob {
	t.Helper()
	jobs, err := repo.Claim(context.Background(), workerID, 1000, lease)
	require.NoError(t, err)
	var (
		claimed service.AuthorizationExpiryJob
		found   bool
		other   []service.AuthorizationExpiryJob
	)
	for _, job := range jobs {
		if job.SourceType == sourceType && job.SourceID == sourceID {
			claimed = job
			found = true
			continue
		}
		other = append(other, job)
	}
	require.NoError(t, releaseAuthorizationExpiryJobs(context.Background(), repo, workerID, other))
	require.True(t, found, "authorization expiry job was not claimable for %s/%d", sourceType, sourceID)
	return claimed
}

func releaseAuthorizationExpiryJobs(
	ctx context.Context,
	repo service.AuthorizationExpiryRepository,
	workerID string,
	jobs []service.AuthorizationExpiryJob,
) error {
	if len(jobs) == 0 {
		return nil
	}
	return repo.ReleaseClaims(ctx, workerID, authorizationExpiryJobIDs(jobs))
}

func authorizationExpiryJobIDs(jobs []service.AuthorizationExpiryJob) []int64 {
	ids := make([]int64, 0, len(jobs))
	for _, job := range jobs {
		ids = append(ids, job.ID)
	}
	return ids
}

func installAuthorizationExpiryAuditFailureTrigger(t testing.TB, requestID string) func() {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	functionName := fmt.Sprintf("fail_authorization_expiry_audit_%d", suffix)
	triggerName := fmt.Sprintf("fail_authorization_expiry_audit_trigger_%d", suffix)
	_, err := integrationDB.ExecContext(ctx, fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.request_id = %s THEN
        RAISE EXCEPTION 'forced authorization expiry audit failure';
    END IF;
    RETURN NEW;
END;
$$
`, functionName, pq.QuoteLiteral(requestID)))
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`
CREATE TRIGGER %s
BEFORE INSERT ON audit_logs
FOR EACH ROW EXECUTE FUNCTION %s()
`, triggerName, functionName))
	require.NoError(t, err)
	drop := func() {
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON audit_logs", triggerName,
		))
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			"DROP FUNCTION IF EXISTS %s()", functionName,
		))
	}
	t.Cleanup(drop)
	return drop
}

func installAuthorizationExpiryAuditAdvisoryLockTrigger(
	t testing.TB,
	requestID string,
	advisoryKey int64,
) {
	t.Helper()
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	functionName := fmt.Sprintf("block_authorization_expiry_audit_%d", suffix)
	triggerName := fmt.Sprintf("block_authorization_expiry_audit_trigger_%d", suffix)
	_, err := integrationDB.ExecContext(ctx, fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.request_id = %s THEN
        PERFORM pg_advisory_xact_lock(%d);
    END IF;
    RETURN NEW;
END;
$$
`, functionName, pq.QuoteLiteral(requestID), advisoryKey))
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`
CREATE TRIGGER %s
BEFORE INSERT ON audit_logs
FOR EACH ROW EXECUTE FUNCTION %s()
`, triggerName, functionName))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON audit_logs", triggerName,
		))
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			"DROP FUNCTION IF EXISTS %s()", functionName,
		))
	})
}

func expectAuthorizationExpiryCoordinatorWriteBlocked(t testing.TB, query string, args ...any) {
	t.Helper()
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, "SET LOCAL lock_timeout = '250ms'")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, query, args...)
	var pgErr *pq.Error
	require.Error(t, err)
	require.True(t, errors.As(err, &pgErr), err)
	require.Equal(t, pq.ErrorCode("55P03"), pgErr.Code)
}

func cleanupAuthorizationExpiryResourceEvents(sourceID int64) {
	ctx := context.Background()
	tx, err := integrationDB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, "SET LOCAL session_replication_role = replica"); err != nil {
		return
	}
	if _, err = tx.ExecContext(ctx, `
DELETE FROM resource_authorization_events
WHERE (details->>'source_id')::BIGINT = $1
`, sourceID); err != nil {
		return
	}
	_ = tx.Commit()
}

func waitForAuthorizationExpiryParentLock(t testing.TB, table string) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		err := integrationDB.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM pg_stat_activity
    WHERE pid <> pg_backend_pid()
      AND state = 'active'
      AND wait_event_type = 'Lock'
      AND query LIKE '%FROM ' || $1 || '%'
      AND query LIKE '%WHERE id%'
      AND query LIKE '%FOR UPDATE%'
)
`, table).Scan(&waiting)
		require.NoError(t, err)
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("authorization expiry worker did not block on %s parent lock", table)
}

func waitForAuthorizationExpiryQueryLock(t testing.TB, queryFragment string) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		err := integrationDB.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM pg_stat_activity
    WHERE pid <> pg_backend_pid()
      AND state = 'active'
      AND wait_event_type = 'Lock'
      AND query LIKE '%' || $1 || '%'
)
`, queryFragment).Scan(&waiting)
		require.NoError(t, err)
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("authorization expiry worker did not block while executing %q", queryFragment)
}

func clearAuthorizationExpiryFixtureOutboxes(ctx context.Context, cacheKey string, accountID, groupID int64) error {
	if _, err := integrationDB.ExecContext(ctx,
		"DELETE FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey,
	); err != nil {
		return err
	}
	_, err := integrationDB.ExecContext(ctx,
		"DELETE FROM scheduler_outbox WHERE account_id = $1 OR group_id = $2", accountID, groupID,
	)
	return err
}

func cleanupAuthorizationExpiryFixture(
	t testing.TB,
	roleID, principalID, apiKeyID int64,
	cacheKey string,
	accountID, groupID int64,
	sourceIDs map[string]int64,
) {
	t.Helper()
	ctx := context.Background()
	_, _ = integrationDB.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1", apiKeyID)
	_, _ = integrationDB.ExecContext(ctx, "DELETE FROM account_access_grants WHERE id = $1", sourceIDs[service.AuthorizationExpirySourceAccountAccessGrant])
	_, _ = integrationDB.ExecContext(ctx, "DELETE FROM group_access_grants WHERE id = $1", sourceIDs[service.AuthorizationExpirySourceGroupAccessGrant])
	_, _ = integrationDB.ExecContext(ctx, "DELETE FROM user_roles WHERE id = $1", sourceIDs[service.AuthorizationExpirySourceUserRole])
	_, _ = integrationDB.ExecContext(ctx, "DELETE FROM service_principal_roles WHERE id = $1", sourceIDs[service.AuthorizationExpirySourceServicePrincipalRole])
	_, _ = integrationDB.ExecContext(ctx, "DELETE FROM service_principals WHERE id = $1", principalID)
	_, _ = integrationDB.ExecContext(ctx, "DELETE FROM roles WHERE id = $1", roleID)
	_, _ = integrationDB.ExecContext(ctx, `
DELETE FROM audit_logs
WHERE (extra->>'source_id')::BIGINT IN ($1, $2)
`, sourceIDs[service.AuthorizationExpirySourceUserRole],
		sourceIDs[service.AuthorizationExpirySourceServicePrincipalRole])
	_, _ = integrationDB.ExecContext(ctx, "DELETE FROM auth_cache_invalidation_outbox WHERE cache_key = $1", cacheKey)
	_, _ = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1 OR group_id = $2", accountID, groupID)

	tx, err := integrationDB.BeginTx(ctx, nil)
	if err != nil {
		return
	}
	_, _ = tx.ExecContext(ctx, "SET LOCAL session_replication_role = replica")
	_, _ = tx.ExecContext(ctx, `
DELETE FROM resource_authorization_events
WHERE (details->>'source_id')::BIGINT IN ($1, $2)
`, sourceIDs[service.AuthorizationExpirySourceAccountAccessGrant],
		sourceIDs[service.AuthorizationExpirySourceGroupAccessGrant])
	_ = tx.Commit()
}
