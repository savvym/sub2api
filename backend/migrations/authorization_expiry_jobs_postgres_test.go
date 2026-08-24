//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestAuthorizationExpiryJobsPostgres(t *testing.T) {
	db := newRoleCachePostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, repository.ApplyMigrations(ctx, db))

	migrationSQL, err := dbmigrations.FS.ReadFile("238_authorization_expiry_jobs.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err, "migration 238 must be safe to reapply")

	fixture := createAuthorizationExpiryFixture(t, ctx, db)

	t.Run("all source kinds enqueue and rearm", func(t *testing.T) {
		sources := []authorizationExpirySource{
			{sourceType: "user_role", table: "user_roles", id: fixture.userRoleID},
			{sourceType: "service_principal_role", table: "service_principal_roles", id: fixture.servicePrincipalRoleID},
			{sourceType: "account_access_grant", table: "account_access_grants", id: fixture.accountGrantID},
			{sourceType: "group_access_grant", table: "group_access_grants", id: fixture.groupGrantID},
		}

		for _, source := range sources {
			t.Run(source.sourceType, func(t *testing.T) {
				firstExpiry := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
				_, updateErr := db.ExecContext(ctx,
					"UPDATE "+source.table+" SET expires_at = $1 WHERE id = $2",
					firstExpiry,
					source.id,
				)
				require.NoError(t, updateErr)
				assertAuthorizationExpiryJob(t, ctx, db, source.sourceType, source.id, firstExpiry, false)

				_, updateErr = db.ExecContext(ctx, `
UPDATE authorization_expiry_jobs
SET attempts = 3,
    last_error = 'transient failure',
    available_at = expires_at + INTERVAL '5 minutes',
    claimed_at = expires_at + INTERVAL '1 minute',
    claimed_by = 'worker-one',
    processed_at = expires_at + INTERVAL '2 minutes'
WHERE source_type = $1 AND source_id = $2`, source.sourceType, source.id)
				require.NoError(t, updateErr)

				secondExpiry := firstExpiry.Add(20 * time.Minute)
				_, updateErr = db.ExecContext(ctx,
					"UPDATE "+source.table+" SET expires_at = $1 WHERE id = $2",
					secondExpiry,
					source.id,
				)
				require.NoError(t, updateErr)
				assertAuthorizationExpiryJob(t, ctx, db, source.sourceType, source.id, secondExpiry, true)

				_, updateErr = db.ExecContext(ctx,
					"UPDATE "+source.table+" SET expires_at = NULL WHERE id = $1",
					source.id,
				)
				require.NoError(t, updateErr)
				assertAuthorizationExpiryJobMissing(t, ctx, db, source.sourceType, source.id)
			})
		}
	})

	t.Run("source deletion removes job", func(t *testing.T) {
		expiresAt := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
		var grantID int64
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO account_access_grants (
    account_id, grantee_role_id, access_level, granted_by_user_id, expires_at
)
VALUES ($1, $2, 'viewer', $3, $4)
RETURNING id`, fixture.accountID, fixture.roleID, fixture.userID, expiresAt).Scan(&grantID))
		assertAuthorizationExpiryJob(t, ctx, db, "account_access_grant", grantID, expiresAt, false)

		_, err := db.ExecContext(ctx, `DELETE FROM account_access_grants WHERE id = $1`, grantID)
		require.NoError(t, err)
		assertAuthorizationExpiryJobMissing(t, ctx, db, "account_access_grant", grantID)
	})

	t.Run("backfill preserves completed job with unchanged expiry", func(t *testing.T) {
		expiresAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
		_, err := db.ExecContext(ctx,
			`UPDATE user_roles SET expires_at = $1 WHERE id = $2`,
			expiresAt,
			fixture.userRoleID,
		)
		require.NoError(t, err)

		processedAt := time.Now().UTC().Truncate(time.Microsecond)
		_, err = db.ExecContext(ctx, `
UPDATE authorization_expiry_jobs
SET attempts = 4,
    last_error = 'kept for evidence',
    available_at = expires_at + INTERVAL '10 minutes',
    processed_at = $3
WHERE source_type = $1 AND source_id = $2`, "user_role", fixture.userRoleID, processedAt)
		require.NoError(t, err)

		_, err = db.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, err)

		var attempts int
		var lastError string
		var actualProcessedAt time.Time
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT attempts, last_error, processed_at
FROM authorization_expiry_jobs
WHERE source_type = 'user_role' AND source_id = $1`, fixture.userRoleID).Scan(
			&attempts,
			&lastError,
			&actualProcessedAt,
		))
		require.Equal(t, 4, attempts)
		require.Equal(t, "kept for evidence", lastError)
		require.WithinDuration(t, processedAt, actualProcessedAt, time.Microsecond)
	})

	t.Run("reapply backfills missing future and overdue jobs", func(t *testing.T) {
		futureExpiry := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
		overdueExpiry := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
		_, err := db.ExecContext(ctx,
			`UPDATE account_access_grants SET expires_at = $1 WHERE id = $2`,
			futureExpiry,
			fixture.accountGrantID,
		)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx,
			`UPDATE group_access_grants SET expires_at = $1 WHERE id = $2`,
			overdueExpiry,
			fixture.groupGrantID,
		)
		require.NoError(t, err)

		_, err = db.ExecContext(ctx, `
DELETE FROM authorization_expiry_jobs
WHERE (source_type = 'account_access_grant' AND source_id = $1)
   OR (source_type = 'group_access_grant' AND source_id = $2)`,
			fixture.accountGrantID,
			fixture.groupGrantID,
		)
		require.NoError(t, err)

		_, err = db.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, err)
		assertAuthorizationExpiryJob(t, ctx, db, "account_access_grant", fixture.accountGrantID, futureExpiry, false)
		assertAuthorizationExpiryJob(t, ctx, db, "group_access_grant", fixture.groupGrantID, overdueExpiry, false)
	})

	t.Run("coordinator principal collision stays disabled and roleless", func(t *testing.T) {
		var principalID int64
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT id
FROM service_principals
WHERE code = 'authorization_expiry_coordinator'`).Scan(&principalID))

		_, err := db.ExecContext(ctx, `
UPDATE service_principals SET status = 'disabled' WHERE id = $1`, principalID)
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `
INSERT INTO service_principal_roles (
    service_principal_id, role_id, granted_by_user_id
)
VALUES ($1, $2, $3)`, principalID, fixture.roleID, fixture.userID)
		require.NoError(t, err)

		_, err = db.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, err)

		var status string
		var roleCount int
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT status,
       (SELECT COUNT(*) FROM service_principal_roles WHERE service_principal_id = $1)
FROM service_principals
WHERE id = $1`, principalID).Scan(&status, &roleCount))
		require.Equal(t, "disabled", status)
		require.Zero(t, roleCount)
	})
}

type authorizationExpiryFixture struct {
	userID                 int64
	roleID                 int64
	servicePrincipalID     int64
	accountID              int64
	groupID                int64
	userRoleID             int64
	servicePrincipalRoleID int64
	accountGrantID         int64
	groupGrantID           int64
}

type authorizationExpirySource struct {
	sourceType string
	table      string
	id         int64
}

func createAuthorizationExpiryFixture(t *testing.T, ctx context.Context, db *sql.DB) authorizationExpiryFixture {
	t.Helper()
	var fixture authorizationExpiryFixture

	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role, status)
VALUES ('authorization-expiry@example.test', 'not-a-real-password-hash', 'user', 'active')
RETURNING id`).Scan(&fixture.userID))

	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO roles (code, name, description)
VALUES ('authorization-expiry-test-role', 'Authorization Expiry Test Role', '')
RETURNING id`).Scan(&fixture.roleID))

	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO service_principals (code, name, status)
VALUES ('authorization-expiry-test-principal', 'Authorization Expiry Test Principal', 'active')
RETURNING id`).Scan(&fixture.servicePrincipalID))

	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, status)
VALUES ('authorization-expiry-account', 'openai', 'api_key', 'active')
RETURNING id`).Scan(&fixture.accountID))

	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO groups (name, rate_multiplier, is_exclusive, status)
VALUES ('authorization-expiry-group', 1, FALSE, 'active')
RETURNING id`).Scan(&fixture.groupID))

	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO user_roles (user_id, role_id, granted_by_user_id)
VALUES ($1, $2, $1)
RETURNING id`, fixture.userID, fixture.roleID).Scan(&fixture.userRoleID))

	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO service_principal_roles (service_principal_id, role_id, granted_by_user_id)
VALUES ($1, $2, $3)
RETURNING id`, fixture.servicePrincipalID, fixture.roleID, fixture.userID).Scan(&fixture.servicePrincipalRoleID))

	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO account_access_grants (
    account_id, grantee_user_id, access_level, granted_by_user_id
)
VALUES ($1, $2, 'viewer', $2)
RETURNING id`, fixture.accountID, fixture.userID).Scan(&fixture.accountGrantID))

	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO group_access_grants (
    group_id, grantee_user_id, access_level, granted_by_user_id
)
VALUES ($1, $2, 'viewer', $2)
RETURNING id`, fixture.groupID, fixture.userID).Scan(&fixture.groupGrantID))

	return fixture
}

func assertAuthorizationExpiryJob(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	sourceType string,
	sourceID int64,
	wantExpiry time.Time,
	wantRearmed bool,
) {
	t.Helper()
	var expiresAt time.Time
	var availableAt time.Time
	var attempts int
	var lastError string
	var claimedAt sql.NullTime
	var claimedBy sql.NullString
	var processedAt sql.NullTime
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT expires_at, available_at, attempts, last_error,
       claimed_at, claimed_by, processed_at
FROM authorization_expiry_jobs
WHERE source_type = $1 AND source_id = $2`, sourceType, sourceID).Scan(
		&expiresAt,
		&availableAt,
		&attempts,
		&lastError,
		&claimedAt,
		&claimedBy,
		&processedAt,
	))
	require.WithinDuration(t, wantExpiry, expiresAt, time.Microsecond)
	require.WithinDuration(t, wantExpiry, availableAt, time.Microsecond)
	if wantRearmed {
		require.Zero(t, attempts)
		require.Empty(t, lastError)
		require.False(t, claimedAt.Valid)
		require.False(t, claimedBy.Valid)
		require.False(t, processedAt.Valid)
	}
}

func assertAuthorizationExpiryJobMissing(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	sourceType string,
	sourceID int64,
) {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM authorization_expiry_jobs
WHERE source_type = $1 AND source_id = $2`, sourceType, sourceID).Scan(&count))
	require.Zero(t, count)
}
