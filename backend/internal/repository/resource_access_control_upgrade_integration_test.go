//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestResourceAccessControlUpgradeFrom228ThroughCurrent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	db := newResourceAccessUpgradeDatabase(t, ctx)
	applyResourceAccessMigrationsThrough(t, ctx, db, 228)

	legacy := seedResourceAccessUpgradeLegacyData(t, ctx, db)
	applyResourceAccessMigrationsThrough(t, ctx, db, 231)

	assertLegacyResourceDefaults(t, ctx, db, legacy.accountID, legacy.groupID)
	assertBootstrapCompatibilityRole(t, ctx, db, legacy.adminUserID, "admin")
	assertBootstrapCompatibilityRole(t, ctx, db, legacy.normalUserID, "user")

	var post229AdminID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role, status)
VALUES ('authz-upgrade-post-229-admin@example.test', 'hash', 'admin', 'active')
RETURNING id`).Scan(&post229AdminID))
	assertNoBootstrapCompatibilityRole(t, ctx, db, post229AdminID)

	applyResourceAccessMigrationsThrough(t, ctx, db, 232)
	assertBootstrapCompatibilityRole(t, ctx, db, post229AdminID, "admin")

	applyResourceAccessMigrationsThrough(t, ctx, db, 233)
	_, err := db.ExecContext(ctx, `
UPDATE users
SET authz_version = authz_version + 1
WHERE id = $1`, legacy.normalUserID)
	require.NoError(t, err)

	cacheKeyHash := sha256.Sum256([]byte(legacy.apiKey))
	var versionInvalidationCount int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM auth_cache_invalidation_outbox
WHERE cache_key = $1`, hex.EncodeToString(cacheKeyHash[:])).Scan(&versionInvalidationCount))
	require.Equal(t, 1, versionInvalidationCount)

	var adminAPIKeyPrincipalID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO service_principals (code, name, status)
VALUES ('admin_api_key', 'Pre-234 collision', 'disabled')
RETURNING id`).Scan(&adminAPIKeyPrincipalID))
	_, err = db.ExecContext(ctx, `
INSERT INTO service_principal_roles (service_principal_id, role_id, granted_by_user_id)
SELECT $1, id, $2
FROM roles
WHERE code = 'admin'`, adminAPIKeyPrincipalID, legacy.adminUserID)
	require.NoError(t, err)

	applyResourceAccessMigrationsThrough(t, ctx, db, 237)
	assertRolelessPrincipal(t, ctx, db, "admin_api_key", "disabled")
	assertLegacyAuditRowPreserved(t, ctx, db, legacy.auditLogID, legacy.adminUserID)
	_, err = db.ExecContext(ctx, `
UPDATE groups
SET access_version = access_version + 1
WHERE id = $1`, legacy.groupID)
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM auth_cache_invalidation_outbox
WHERE cache_key = $1`, hex.EncodeToString(cacheKeyHash[:])).Scan(&versionInvalidationCount))
	require.Equal(t, 2, versionInvalidationCount)

	expiring := seedResourceAccessUpgradeExpiringSources(t, ctx, db, legacy, post229AdminID)
	applyResourceAccessMigrationsThrough(t, ctx, db, latestEmbeddedMigrationVersion(t))

	assertAuthorizationExpiryBackfill(t, ctx, db, expiring)
	assertSchedulerOutboxUpgrade(t, ctx, db, legacy.schedulerOutboxID)
	assertResourceAccessSeeds(t, ctx, db)
	assertRolelessPrincipal(t, ctx, db, "authorization_expiry_coordinator", "active")
	assertResourceAccessMigrationIndexesValid(t, ctx, db)
	assertResourceAccessMigrationsRecordedOnce(t, ctx, db)
	assertResourceAccessUpgradeDataPreserved(t, ctx, db, legacy)

	var seedCountsBefore [3]int
	readResourceAccessSeedCounts(t, ctx, db, &seedCountsBefore)
	require.NoError(t, ApplyMigrations(ctx, db))
	require.NoError(t, ApplyMigrations(ctx, db))
	var seedCountsAfter [3]int
	readResourceAccessSeedCounts(t, ctx, db, &seedCountsAfter)
	require.Equal(t, seedCountsBefore, seedCountsAfter)
	assertAuthorizationExpiryBackfill(t, ctx, db, expiring)
	assertResourceAccessMigrationsRecordedOnce(t, ctx, db)
}

type resourceAccessUpgradeLegacyData struct {
	adminUserID       int64
	normalUserID      int64
	accountID         int64
	groupID           int64
	auditLogID        int64
	idempotencyID     int64
	schedulerOutboxID int64
	authOutboxID      int64
	apiKey            string
}

type resourceAccessUpgradeExpiringSource struct {
	sourceType string
	sourceID   int64
	expiresAt  time.Time
}

func newResourceAccessUpgradeDatabase(t *testing.T, ctx context.Context) *sql.DB {
	t.Helper()
	require.NotEmpty(t, integrationPostgresDSN)

	databaseName := fmt.Sprintf("sub2api_authz_upgrade_%d_%d", os.Getpid(), time.Now().UnixNano())
	_, err := integrationDB.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(databaseName))
	require.NoError(t, err)

	parsed, err := url.Parse(integrationPostgresDSN)
	require.NoError(t, err)
	parsed.Path = "/" + databaseName
	parsed.RawPath = ""

	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	require.NoError(t, db.PingContext(ctx))
	t.Cleanup(func() {
		_ = db.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		_, dropErr := integrationDB.ExecContext(
			cleanupCtx,
			"DROP DATABASE "+pq.QuoteIdentifier(databaseName)+" WITH (FORCE)",
		)
		require.NoError(t, dropErr)
	})
	return db
}

func applyResourceAccessMigrationsThrough(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	maxVersion int,
) {
	t.Helper()
	require.NoError(t, applyMigrationsFS(ctx, db, resourceAccessMigrationFS(t, maxVersion)))
}

func resourceAccessMigrationFS(t *testing.T, maxVersion int) fs.FS {
	t.Helper()
	entries, err := fs.ReadDir(dbmigrations.FS, ".")
	require.NoError(t, err)

	result := fstest.MapFS{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		version, ok := migrationNumericPrefix(name)
		if !ok || version > maxVersion {
			continue
		}
		content, readErr := fs.ReadFile(dbmigrations.FS, name)
		require.NoError(t, readErr)
		result[name] = &fstest.MapFile{Data: content, Mode: 0o444}
	}
	require.NotEmpty(t, result)
	return result
}

func migrationNumericPrefix(name string) (int, bool) {
	end := 0
	for end < len(name) && name[end] >= '0' && name[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0, false
	}
	version, err := strconv.Atoi(name[:end])
	return version, err == nil
}

func latestEmbeddedMigrationVersion(t *testing.T) int {
	t.Helper()
	entries, err := fs.ReadDir(dbmigrations.FS, ".")
	require.NoError(t, err)
	latest := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, ok := migrationNumericPrefix(entry.Name())
		if ok && version > latest {
			latest = version
		}
	}
	require.GreaterOrEqual(t, latest, 242)
	return latest
}

func seedResourceAccessUpgradeLegacyData(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
) resourceAccessUpgradeLegacyData {
	t.Helper()
	var data resourceAccessUpgradeLegacyData
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role, status)
VALUES ('authz-upgrade-admin@example.test', 'hash', 'admin', 'active')
RETURNING id`).Scan(&data.adminUserID))
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role, status)
VALUES ('authz-upgrade-user@example.test', 'hash', 'user', 'active')
RETURNING id`).Scan(&data.normalUserID))
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, status)
VALUES ('authz-upgrade-account', 'openai', 'api_key', 'active')
RETURNING id`).Scan(&data.accountID))
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO groups (name, rate_multiplier, is_exclusive, status)
VALUES ('authz-upgrade-group', 1, FALSE, 'active')
RETURNING id`).Scan(&data.groupID))

	data.apiKey = "sk-authz-upgrade-existing-key"
	_, err := db.ExecContext(ctx, `
INSERT INTO api_keys (user_id, group_id, key, name, status)
VALUES ($1, $2, $3, 'authz-upgrade-key', 'active')`, data.normalUserID, data.groupID, data.apiKey)
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO audit_logs (actor_user_id, action)
VALUES ($1, 'authz.upgrade.existing')
RETURNING id`, data.adminUserID).Scan(&data.auditLogID))
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status, expires_at
)
VALUES ('authz.upgrade', repeat('b', 64), repeat('c', 64), 'completed', statement_timestamp() + INTERVAL '1 hour')
RETURNING id`).Scan(&data.idempotencyID))
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO scheduler_outbox (event_type, account_id, group_id, dedup_key)
VALUES ('authz_upgrade_existing', $1, $2, 'authz-upgrade-existing')
RETURNING id`, data.accountID, data.groupID).Scan(&data.schedulerOutboxID))
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO auth_cache_invalidation_outbox (cache_key, delivery_stage)
VALUES (repeat('d', 64), 0)
RETURNING id`).Scan(&data.authOutboxID))
	return data
}

func assertLegacyResourceDefaults(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	accountID int64,
	groupID int64,
) {
	t.Helper()
	var accountOwner, accountCreator sql.NullInt64
	var accountPublic sql.NullString
	var accountVersion int64
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT owner_user_id, created_by_user_id, public_access_level, access_version
FROM accounts
WHERE id = $1`, accountID).Scan(
		&accountOwner,
		&accountCreator,
		&accountPublic,
		&accountVersion,
	))
	require.False(t, accountOwner.Valid)
	require.False(t, accountCreator.Valid)
	require.False(t, accountPublic.Valid)
	require.Equal(t, int64(1), accountVersion)

	var groupOwner, groupCreator sql.NullInt64
	var groupPublic sql.NullString
	var groupVersion int64
	var groupMode string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT owner_user_id, created_by_user_id, public_access_level, access_version, authorization_mode
FROM groups
WHERE id = $1`, groupID).Scan(
		&groupOwner,
		&groupCreator,
		&groupPublic,
		&groupVersion,
		&groupMode,
	))
	require.False(t, groupOwner.Valid)
	require.False(t, groupCreator.Valid)
	require.False(t, groupPublic.Valid)
	require.Equal(t, int64(1), groupVersion)
	require.Equal(t, "legacy", groupMode)
}

func assertBootstrapCompatibilityRole(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	userID int64,
	wantRole string,
) {
	t.Helper()
	var count int
	var role string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*), COALESCE(MIN(r.code), '')
FROM user_roles ur
JOIN roles r ON r.id = ur.role_id
JOIN service_principals sp ON sp.id = ur.granted_by_service_principal_id
WHERE ur.user_id = $1
  AND sp.code = 'system_bootstrap'
  AND r.code IN ('user', 'admin')`, userID).Scan(&count, &role))
	require.Equal(t, 1, count)
	require.Equal(t, wantRole, role)
}

func assertNoBootstrapCompatibilityRole(t *testing.T, ctx context.Context, db *sql.DB, userID int64) {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM user_roles ur
JOIN roles r ON r.id = ur.role_id
JOIN service_principals sp ON sp.id = ur.granted_by_service_principal_id
WHERE ur.user_id = $1
  AND sp.code = 'system_bootstrap'
  AND r.code IN ('user', 'admin')`, userID).Scan(&count))
	require.Zero(t, count)
}

func assertRolelessPrincipal(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	code string,
	wantStatus string,
) {
	t.Helper()
	var status string
	var roleCount int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT sp.status, COUNT(spr.id)
FROM service_principals sp
LEFT JOIN service_principal_roles spr ON spr.service_principal_id = sp.id
WHERE sp.code = $1
GROUP BY sp.id, sp.status`, code).Scan(&status, &roleCount))
	require.Equal(t, wantStatus, status)
	require.Zero(t, roleCount)
}

func assertLegacyAuditRowPreserved(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	auditLogID int64,
	actorUserID int64,
) {
	t.Helper()
	var actualActorUserID sql.NullInt64
	var actorServicePrincipalID sql.NullInt64
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT actor_user_id, actor_service_principal_id
FROM audit_logs
WHERE id = $1`, auditLogID).Scan(&actualActorUserID, &actorServicePrincipalID))
	require.True(t, actualActorUserID.Valid)
	require.Equal(t, actorUserID, actualActorUserID.Int64)
	require.False(t, actorServicePrincipalID.Valid)
}

func seedResourceAccessUpgradeExpiringSources(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	legacy resourceAccessUpgradeLegacyData,
	post229AdminID int64,
) []resourceAccessUpgradeExpiringSource {
	t.Helper()
	futureExpiry := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
	overdueExpiry := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)

	var testPrincipalID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO service_principals (code, name, status)
VALUES ('authz_upgrade_expiring_principal', 'Upgrade Expiring Principal', 'active')
RETURNING id`).Scan(&testPrincipalID))

	var userRoleID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO user_roles (user_id, role_id, granted_by_user_id, expires_at)
SELECT $1, id, $2, $3
FROM roles
WHERE code = 'hoster'
RETURNING id`, legacy.normalUserID, legacy.adminUserID, futureExpiry).Scan(&userRoleID))

	var principalRoleID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO service_principal_roles (service_principal_id, role_id, granted_by_user_id, expires_at)
SELECT $1, id, $2, $3
FROM roles
WHERE code = 'hoster'
RETURNING id`, testPrincipalID, legacy.adminUserID, overdueExpiry).Scan(&principalRoleID))

	var accountGrantID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO account_access_grants (
    account_id, grantee_user_id, access_level, granted_by_user_id, expires_at
)
VALUES ($1, $2, 'viewer', $3, $4)
RETURNING id`, legacy.accountID, post229AdminID, legacy.adminUserID, futureExpiry).Scan(&accountGrantID))

	var groupGrantID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO group_access_grants (
    group_id, grantee_user_id, access_level, granted_by_user_id, expires_at
)
VALUES ($1, $2, 'consumer', $3, $4)
RETURNING id`, legacy.groupID, post229AdminID, legacy.adminUserID, overdueExpiry).Scan(&groupGrantID))

	return []resourceAccessUpgradeExpiringSource{
		{sourceType: "user_role", sourceID: userRoleID, expiresAt: futureExpiry},
		{sourceType: "service_principal_role", sourceID: principalRoleID, expiresAt: overdueExpiry},
		{sourceType: "account_access_grant", sourceID: accountGrantID, expiresAt: futureExpiry},
		{sourceType: "group_access_grant", sourceID: groupGrantID, expiresAt: overdueExpiry},
	}
}

func assertAuthorizationExpiryBackfill(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	sources []resourceAccessUpgradeExpiringSource,
) {
	t.Helper()
	for _, source := range sources {
		var expiresAt, availableAt time.Time
		var processedAt sql.NullTime
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT expires_at, available_at, processed_at
FROM authorization_expiry_jobs
WHERE source_type = $1 AND source_id = $2`, source.sourceType, source.sourceID).Scan(
			&expiresAt,
			&availableAt,
			&processedAt,
		))
		require.WithinDuration(t, source.expiresAt, expiresAt, time.Microsecond)
		require.WithinDuration(t, source.expiresAt, availableAt, time.Microsecond)
		require.False(t, processedAt.Valid)
	}

	var jobCount int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM authorization_expiry_jobs`,
	).Scan(&jobCount))
	require.Equal(t, len(sources), jobCount)
}

func assertSchedulerOutboxUpgrade(t *testing.T, ctx context.Context, db *sql.DB, eventID int64) {
	t.Helper()
	var leaseToken sql.NullString
	var leaseExpiresAt sql.NullTime
	var nextAttemptAt time.Time
	var attemptCount int64
	var lastError sql.NullString
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT lease_token, lease_expires_at, next_attempt_at, attempt_count, last_error
FROM scheduler_outbox
WHERE id = $1`, eventID).Scan(
		&leaseToken,
		&leaseExpiresAt,
		&nextAttemptAt,
		&attemptCount,
		&lastError,
	))
	require.False(t, leaseToken.Valid)
	require.False(t, leaseExpiresAt.Valid)
	require.False(t, nextAttemptAt.IsZero())
	require.Zero(t, attemptCount)
	require.False(t, lastError.Valid)
	for _, constraintName := range []string{
		"scheduler_outbox_attempt_count_nonnegative",
		"scheduler_outbox_lease_pair_consistent",
	} {
		var validated bool
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT convalidated
FROM pg_constraint
WHERE conrelid = 'scheduler_outbox'::regclass
  AND conname = $1`, constraintName).Scan(&validated))
		require.False(t, validated, "%s must remain NOT VALID during the online upgrade", constraintName)
	}

	repo := NewSchedulerOutboxRepository(db)
	claimed, err := repo.Claim(ctx, 10, time.Minute)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, eventID, claimed[0].ID)
	acknowledged, err := repo.Acknowledge(ctx, eventID, claimed[0].LeaseToken)
	require.NoError(t, err)
	require.True(t, acknowledged)
}

func assertResourceAccessSeeds(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var counts [3]int
	readResourceAccessSeedCounts(t, ctx, db, &counts)
	require.Equal(t, [3]int{4, 11, 17}, counts)
}

func readResourceAccessSeedCounts(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	counts *[3]int,
) {
	t.Helper()
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT
    (SELECT COUNT(*) FROM roles WHERE is_system AND code IN ('user', 'hoster', 'platform_operator', 'admin')),
    (SELECT COUNT(*) FROM permissions WHERE code IN (
        'api_key.create', 'account.create', 'group.create', 'resource.share',
        'resource.transfer', 'platform.resource.view_all',
        'platform.resource.operate_all', 'platform.resource.manage_all',
        'platform.role.manage', 'platform.grant.manage', 'platform.secret.export'
    )),
    (SELECT COUNT(*)
     FROM role_permissions rp
     JOIN roles r ON r.id = rp.role_id
     WHERE r.code IN ('user', 'hoster', 'platform_operator', 'admin'))`).Scan(
		&counts[0],
		&counts[1],
		&counts[2],
	))
}

func assertResourceAccessMigrationIndexesValid(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	indexNames := []string{
		"idx_accounts_owner_user_id",
		"idx_accounts_created_by_user_id",
		"idx_groups_owner_user_id",
		"idx_groups_created_by_user_id",
		"idx_groups_authorization_mode",
		"idx_audit_logs_actor_created",
		"idx_audit_logs_service_principal_created",
		"idx_auth_cache_invalidation_outbox_stage_available",
		"idx_scheduler_outbox_claimable",
		"idx_accounts_public_access_level",
		"idx_groups_public_access_level",
	}
	for _, indexName := range indexNames {
		var valid bool
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT i.indisvalid
FROM pg_class c
JOIN pg_index i ON i.indexrelid = c.oid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema()
  AND c.relname = $1`, indexName).Scan(&valid))
		require.True(t, valid, "index %s must be valid after upgrade", indexName)
	}
}

func assertResourceAccessMigrationsRecordedOnce(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	entries, err := fs.ReadDir(dbmigrations.FS, ".")
	require.NoError(t, err)
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		version, ok := migrationNumericPrefix(name)
		if !ok || version <= 228 {
			continue
		}
		var count int
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM schema_migrations
WHERE filename = $1`, name).Scan(&count))
		require.Equal(t, 1, count, "migration %s must be recorded exactly once", name)
	}
}

func assertResourceAccessUpgradeDataPreserved(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	legacy resourceAccessUpgradeLegacyData,
) {
	t.Helper()
	for _, record := range []struct {
		table string
		id    int64
	}{
		{table: "users", id: legacy.adminUserID},
		{table: "users", id: legacy.normalUserID},
		{table: "accounts", id: legacy.accountID},
		{table: "groups", id: legacy.groupID},
		{table: "audit_logs", id: legacy.auditLogID},
		{table: "idempotency_records", id: legacy.idempotencyID},
		{table: "auth_cache_invalidation_outbox", id: legacy.authOutboxID},
	} {
		var count int
		require.NoError(t, db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM "+pq.QuoteIdentifier(record.table)+" WHERE id = $1",
			record.id,
		).Scan(&count))
		require.Equal(t, 1, count, "%s row %d must survive upgrade", record.table, record.id)
	}
}
