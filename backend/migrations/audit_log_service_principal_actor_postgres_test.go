//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestAuditLogServicePrincipalActorPostgres(t *testing.T) {
	db := newRoleCachePostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, repository.ApplyMigrations(ctx, db))

	var principalID int64
	var status string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT id, status
FROM service_principals
WHERE code = 'admin_api_key'`).Scan(&principalID, &status))
	require.Equal(t, "active", status)
	require.Zero(t, auditServicePrincipalRoleCount(t, ctx, db, principalID))

	// Simulate a pre-migration code collision: the reserved identity already
	// exists, is deliberately disabled, and carries an admin role. Reapplying
	// the migration must preserve its status while restoring the zero-role
	// identity-anchor invariant.
	var grantorUserID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role, status)
VALUES ('audit-principal-migration@example.test', 'not-a-real-password-hash', 'admin', 'active')
RETURNING id`).Scan(&grantorUserID))

	_, err := db.ExecContext(ctx, `
UPDATE service_principals
SET status = 'disabled'
WHERE id = $1`, principalID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO service_principal_roles (service_principal_id, role_id, granted_by_user_id)
SELECT $1, id, $2
FROM roles
WHERE code = 'admin'`, principalID, grantorUserID)
	require.NoError(t, err)
	require.Equal(t, 1, auditServicePrincipalRoleCount(t, ctx, db, principalID))

	migrationSQL, err := dbmigrations.FS.ReadFile("234_audit_log_service_principal_actor.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err, "migration 234 must be safe to reapply")
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT status
FROM service_principals
WHERE id = $1`, principalID).Scan(&status))
	require.Equal(t, "disabled", status)
	require.Zero(t, auditServicePrincipalRoleCount(t, ctx, db, principalID))

	_, err = db.ExecContext(ctx, `INSERT INTO audit_logs (action) VALUES ('actorless.login.failure')`)
	require.NoError(t, err, "actor-less authentication failures must remain valid")
	_, err = db.ExecContext(ctx, `
INSERT INTO audit_logs (actor_user_id, action)
VALUES (101, 'user.action')`)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `
INSERT INTO audit_logs (actor_service_principal_id, action)
VALUES ($1, 'service-principal.action')`, principalID)
	require.NoError(t, err)

	_, err = db.ExecContext(ctx, `
INSERT INTO audit_logs (actor_user_id, actor_service_principal_id, action)
VALUES (101, $1, 'invalid.dual-actor')`, principalID)
	require.Error(t, err, "one audit row cannot impersonate both subject kinds")

	_, err = db.ExecContext(ctx, `DELETE FROM service_principals WHERE id = $1`, principalID)
	require.Error(t, err, "append-only audit provenance must restrict principal deletion")

	var checkDefinition string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT pg_get_constraintdef(oid)
FROM pg_constraint
WHERE conrelid = 'audit_logs'::regclass
  AND conname = 'audit_logs_actor_at_most_one_check'`).Scan(&checkDefinition))
	require.Contains(t, strings.ToLower(checkDefinition), "num_nonnulls(actor_user_id, actor_service_principal_id) <= 1")

	for _, indexName := range []string{
		"idx_audit_logs_actor_created",
		"idx_audit_logs_service_principal_created",
	} {
		var definition string
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT indexdef
FROM pg_indexes
WHERE schemaname = current_schema()
  AND indexname = $1`, indexName).Scan(&definition))
		require.Contains(t, strings.ToUpper(definition), " WHERE ")
	}
}

func auditServicePrincipalRoleCount(t *testing.T, ctx context.Context, db *sql.DB, principalID int64) int {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM service_principal_roles
WHERE service_principal_id = $1`, principalID).Scan(&count))
	return count
}
