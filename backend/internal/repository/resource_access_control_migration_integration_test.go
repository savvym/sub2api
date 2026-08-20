//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestResourceAccessControlFoundationSchema(t *testing.T) {
	tx := testTx(t)

	requireColumn(t, tx, "users", "authz_version", "bigint", 0, false)
	requireColumnDefaultContains(t, tx, "users", "authz_version", "1")
	requireConstraintValidated(t, tx, "users", "users_authz_version_positive")
	for _, table := range []string{"accounts", "groups"} {
		requireColumn(t, tx, table, "owner_user_id", "bigint", 0, true)
		requireColumn(t, tx, table, "created_by_user_id", "bigint", 0, true)
		requireColumn(t, tx, table, "public_access_level", "character varying", 20, true)
		requireColumn(t, tx, table, "access_version", "bigint", 0, false)
		requireColumnDefaultContains(t, tx, table, "access_version", "1")
		requireForeignKeyOnDelete(t, tx, table, "owner_user_id", "users", "RESTRICT")
		requireForeignKeyOnDelete(t, tx, table, "created_by_user_id", "users", "RESTRICT")
		requireConstraintValidated(t, tx, table, table+"_owner_user_id_fkey")
		requireConstraintValidated(t, tx, table, table+"_created_by_user_id_fkey")
		requireConstraintValidated(t, tx, table, table+"_public_access_level_check")
		requireConstraintValidated(t, tx, table, table+"_access_version_positive")
	}
	requireColumn(t, tx, "groups", "authorization_mode", "character varying", 20, false)
	requireColumnDefaultContains(t, tx, "groups", "authorization_mode", "legacy")
	requireConstraintValidated(t, tx, "groups", "groups_authorization_mode_check")
	for _, tc := range []struct {
		table string
		index string
	}{
		{"accounts", "idx_accounts_owner_user_id"},
		{"accounts", "idx_accounts_created_by_user_id"},
		{"groups", "idx_groups_owner_user_id"},
		{"groups", "idx_groups_created_by_user_id"},
		{"groups", "idx_groups_authorization_mode"},
	} {
		requireValidResourceAccessIndex(t, tx, tc.table, tc.index)
	}

	for _, table := range []string{
		"roles",
		"permissions",
		"role_permissions",
		"service_principals",
		"user_roles",
		"service_principal_roles",
		"account_access_grants",
		"group_access_grants",
		"resource_authorization_events",
	} {
		requireTable(t, tx, table)
	}

	requireForeignKeyOnDelete(t, tx, "user_roles", "user_id", "users", "CASCADE")
	requireForeignKeyOnDelete(t, tx, "user_roles", "role_id", "roles", "RESTRICT")
	requireForeignKeyOnDelete(t, tx, "user_roles", "granted_by_user_id", "users", "RESTRICT")
	requireForeignKeyOnDelete(t, tx, "user_roles", "granted_by_service_principal_id", "service_principals", "RESTRICT")
	requireForeignKeyOnDelete(t, tx, "service_principal_roles", "service_principal_id", "service_principals", "CASCADE")
	requireForeignKeyOnDelete(t, tx, "service_principal_roles", "role_id", "roles", "RESTRICT")
	requireForeignKeyOnDelete(t, tx, "service_principal_roles", "granted_by_user_id", "users", "RESTRICT")
	requireForeignKeyOnDelete(t, tx, "role_permissions", "role_id", "roles", "CASCADE")
	requireForeignKeyOnDelete(t, tx, "role_permissions", "permission_id", "permissions", "CASCADE")
	requireForeignKeyOnDelete(t, tx, "account_access_grants", "account_id", "accounts", "CASCADE")
	requireForeignKeyOnDelete(t, tx, "account_access_grants", "grantee_user_id", "users", "CASCADE")
	requireForeignKeyOnDelete(t, tx, "account_access_grants", "grantee_role_id", "roles", "RESTRICT")
	requireForeignKeyOnDelete(t, tx, "account_access_grants", "granted_by_user_id", "users", "RESTRICT")
	requireForeignKeyOnDelete(t, tx, "account_access_grants", "granted_by_service_principal_id", "service_principals", "RESTRICT")
	requireForeignKeyOnDelete(t, tx, "group_access_grants", "group_id", "groups", "CASCADE")
	requireForeignKeyOnDelete(t, tx, "group_access_grants", "grantee_user_id", "users", "CASCADE")
	requireForeignKeyOnDelete(t, tx, "group_access_grants", "grantee_role_id", "roles", "RESTRICT")
	requireForeignKeyOnDelete(t, tx, "group_access_grants", "granted_by_user_id", "users", "RESTRICT")
	requireForeignKeyOnDelete(t, tx, "group_access_grants", "granted_by_service_principal_id", "service_principals", "RESTRICT")
	requireColumn(t, tx, "resource_authorization_events", "resource_owner_user_id", "bigint", 0, true)
	requireColumn(t, tx, "resource_authorization_events", "auth_method", "character varying", 32, false)
	requireColumnDefaultContains(t, tx, "resource_authorization_events", "auth_method", "unknown")
	requireForeignKeyOnDelete(t, tx, "resource_authorization_events", "account_id", "accounts", "RESTRICT")
	requireForeignKeyOnDelete(t, tx, "resource_authorization_events", "group_id", "groups", "RESTRICT")
	requireForeignKeyOnDelete(t, tx, "resource_authorization_events", "actor_user_id", "users", "RESTRICT")
	requireForeignKeyOnDelete(t, tx, "resource_authorization_events", "actor_service_principal_id", "service_principals", "RESTRICT")
	requireForeignKeyOnDelete(t, tx, "resource_authorization_events", "resource_owner_user_id", "users", "RESTRICT")

	for _, tc := range []struct {
		table      string
		constraint string
		parts      []string
	}{
		{"user_roles", "user_roles_grantor_exactly_one_check", []string{"num_nonnulls", "granted_by_user_id", "granted_by_service_principal_id", "= 1"}},
		{"account_access_grants", "account_access_grants_grantee_exactly_one_check", []string{"num_nonnulls", "grantee_user_id", "grantee_role_id", "= 1"}},
		{"account_access_grants", "account_access_grants_grantor_exactly_one_check", []string{"num_nonnulls", "granted_by_user_id", "granted_by_service_principal_id", "= 1"}},
		{"account_access_grants", "account_access_grants_access_level_check", []string{"'viewer'", "'consumer'", "'maintainer'", "'manager'"}},
		{"group_access_grants", "group_access_grants_grantee_exactly_one_check", []string{"num_nonnulls", "grantee_user_id", "grantee_role_id", "= 1"}},
		{"group_access_grants", "group_access_grants_grantor_exactly_one_check", []string{"num_nonnulls", "granted_by_user_id", "granted_by_service_principal_id", "= 1"}},
		{"group_access_grants", "group_access_grants_access_level_check", []string{"'viewer'", "'consumer'", "'maintainer'", "'manager'"}},
		{"resource_authorization_events", "resource_authorization_events_resource_exactly_one_check", []string{"num_nonnulls", "account_id", "group_id", "= 1"}},
		{"resource_authorization_events", "resource_authorization_events_actor_exactly_one_check", []string{"num_nonnulls", "actor_user_id", "actor_service_principal_id", "= 1"}},
		{"resource_authorization_events", "resource_authorization_events_auth_method_not_empty_check", []string{"btrim", "auth_method", "<> ''"}},
		{"resource_authorization_events", "resource_authorization_events_details_object_check", []string{"jsonb_typeof", "details", "'object'"}},
	} {
		requireConstraintDefinitionContains(t, tx, tc.table, tc.constraint, tc.parts...)
	}

	for _, tc := range []struct {
		table string
		name  string
		parts []string
	}{
		{"account_access_grants", "account_access_grants_account_user_key", []string{"account_id", "grantee_user_id", "WHERE", "IS NOT NULL"}},
		{"account_access_grants", "account_access_grants_account_role_key", []string{"account_id", "grantee_role_id", "WHERE", "IS NOT NULL"}},
		{"group_access_grants", "group_access_grants_group_user_key", []string{"group_id", "grantee_user_id", "WHERE", "IS NOT NULL"}},
		{"group_access_grants", "group_access_grants_group_role_key", []string{"group_id", "grantee_role_id", "WHERE", "IS NOT NULL"}},
	} {
		requirePartialUniqueIndexDefinition(t, tx, tc.table, tc.name, tc.parts...)
	}

	var roleCount, permissionCount, exportGrantCount int
	require.NoError(t, tx.QueryRowContext(context.Background(), `
SELECT COUNT(*) FROM roles WHERE is_system = TRUE AND code IN ('user', 'hoster', 'platform_operator', 'admin')
`).Scan(&roleCount))
	require.Equal(t, 4, roleCount)
	require.NoError(t, tx.QueryRowContext(context.Background(), `
SELECT COUNT(*) FROM permissions
WHERE code IN (
    'api_key.create', 'account.create', 'group.create', 'resource.share',
    'resource.transfer', 'platform.resource.view_all',
    'platform.resource.operate_all', 'platform.resource.manage_all',
    'platform.role.manage', 'platform.grant.manage', 'platform.secret.export'
)
`).Scan(&permissionCount))
	require.Equal(t, 11, permissionCount)
	require.NoError(t, tx.QueryRowContext(context.Background(), `
SELECT COUNT(*)
FROM role_permissions rp
JOIN permissions p ON p.id = rp.permission_id
WHERE p.code = 'platform.secret.export'
`).Scan(&exportGrantCount))
	require.Zero(t, exportGrantCount)
}

func TestResourceAccessControlMigrationsReapplyAndEnforceConstraints(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()

	var adminUserID, normalUserID, manuallyGrantedUserID, serviceGrantedUserID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role)
VALUES ('authz-migration-admin@example.test', 'hash', 'admin')
RETURNING id
`).Scan(&adminUserID))
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role)
VALUES ('authz-migration-user@example.test', 'hash', 'unexpected_legacy_value')
RETURNING id
`).Scan(&normalUserID))
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role)
VALUES ('authz-migration-manual-grant@example.test', 'hash', 'admin')
RETURNING id
`).Scan(&manuallyGrantedUserID))
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role)
VALUES ('authz-migration-service-grant@example.test', 'hash', 'user')
RETURNING id
`).Scan(&serviceGrantedUserID))

	var userRoleID, adminRoleID, hosterRoleID, bootstrapPrincipalID, otherPrincipalID int64
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT id FROM roles WHERE code = 'user'").Scan(&userRoleID))
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT id FROM roles WHERE code = 'admin'").Scan(&adminRoleID))
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT id FROM roles WHERE code = 'hoster'").Scan(&hosterRoleID))
	require.NoError(t, tx.QueryRowContext(ctx, "SELECT id FROM service_principals WHERE code = 'system_bootstrap'").Scan(&bootstrapPrincipalID))
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO service_principals (code, name, status)
VALUES ('authz-migration-other-principal', 'Migration Test Principal', 'active')
RETURNING id
`).Scan(&otherPrincipalID))

	_, err := tx.ExecContext(ctx, `
INSERT INTO user_roles (user_id, role_id, granted_by_user_id)
VALUES ($1, $2, $3)
`, manuallyGrantedUserID, userRoleID, adminUserID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
INSERT INTO user_roles (user_id, role_id, granted_by_service_principal_id)
VALUES ($1, $2, $3)
`, serviceGrantedUserID, adminRoleID, otherPrincipalID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
INSERT INTO user_roles (user_id, role_id, granted_by_service_principal_id)
VALUES ($1, $2, $3)
`, adminUserID, hosterRoleID, bootstrapPrincipalID)
	require.NoError(t, err)

	compatibilityBackfillContent, err := dbmigrations.FS.ReadFile("232_resource_authorization_compatibility_backfill.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(compatibilityBackfillContent))
	require.NoError(t, err, "reapply 232 pass 1")

	_, err = tx.ExecContext(ctx, `
UPDATE users
SET role = CASE id
    WHEN $1 THEN 'user'
    WHEN $2 THEN 'admin'
    ELSE role
END
WHERE id IN ($1, $2)
`, adminUserID, normalUserID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(compatibilityBackfillContent))
	require.NoError(t, err, "reapply 232 pass 2 after legacy role changes")

	for _, tc := range []struct {
		userID       int64
		expectedRole string
	}{
		{adminUserID, "user"},
		{normalUserID, "admin"},
	} {
		var compatibilityRoleCount int
		var actualRole string
		require.NoError(t, tx.QueryRowContext(ctx, `
SELECT COUNT(*), MIN(r.code)
FROM user_roles ur
JOIN roles r ON r.id = ur.role_id
JOIN service_principals sp ON sp.id = ur.granted_by_service_principal_id
WHERE ur.user_id = $1
  AND sp.code = 'system_bootstrap'
  AND r.code IN ('user', 'admin')
`, tc.userID).Scan(&compatibilityRoleCount, &actualRole))
		require.Equal(t, 1, compatibilityRoleCount)
		require.Equal(t, tc.expectedRole, actualRole)
	}

	var preservedGrantCount int
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM user_roles
WHERE (user_id = $1 AND role_id = $2 AND granted_by_user_id = $3)
   OR (user_id = $4 AND role_id = $5 AND granted_by_service_principal_id = $6)
   OR (user_id = $7 AND role_id = $8 AND granted_by_service_principal_id = $9)
`, manuallyGrantedUserID, userRoleID, adminUserID,
		serviceGrantedUserID, adminRoleID, otherPrincipalID,
		adminUserID, hosterRoleID, bootstrapPrincipalID).Scan(&preservedGrantCount))
	require.Equal(t, 3, preservedGrantCount)

	aclContent, err := dbmigrations.FS.ReadFile("230_resource_access_control_foundation.sql")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = tx.ExecContext(ctx, string(aclContent))
		require.NoErrorf(t, err, "reapply 230 pass %d", i+1)
	}

	var duplicateCompatibilityRoles int
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM (
    SELECT user_id, role_id, COUNT(*)
    FROM user_roles
    WHERE user_id IN ($1, $2)
    GROUP BY user_id, role_id
    HAVING COUNT(*) > 1
) duplicates
`, adminUserID, normalUserID).Scan(&duplicateCompatibilityRoles))
	require.Zero(t, duplicateCompatibilityRoles)

	var accountID, groupID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type)
VALUES ('authz-migration-account', 'anthropic', 'oauth')
RETURNING id
`).Scan(&accountID))
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO groups (name)
VALUES ('authz-migration-group')
RETURNING id
`).Scan(&groupID))
	var ownerID, creatorID sql.NullInt64
	var publicLevel sql.NullString
	var accountVersion int64
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT owner_user_id, created_by_user_id, public_access_level, access_version
FROM accounts WHERE id = $1
`, accountID).Scan(&ownerID, &creatorID, &publicLevel, &accountVersion))
	require.False(t, ownerID.Valid)
	require.False(t, creatorID.Valid)
	require.False(t, publicLevel.Valid)
	require.Equal(t, int64(1), accountVersion)

	var groupMode string
	var groupVersion int64
	require.NoError(t, tx.QueryRowContext(ctx, `
SELECT authorization_mode, access_version FROM groups WHERE id = $1
`, groupID).Scan(&groupMode, &groupVersion))
	require.Equal(t, "legacy", groupMode)
	require.Equal(t, int64(1), groupVersion)

	requireStatementRejected(t, tx, `
INSERT INTO account_access_grants (
    account_id, grantee_user_id, grantee_role_id, access_level, granted_by_user_id
) VALUES ($1, $2, $3, 'viewer', $4)
`, accountID, normalUserID, userRoleID, adminUserID)
	requireStatementRejected(t, tx, `
INSERT INTO account_access_grants (account_id, grantee_user_id, access_level)
VALUES ($1, $2, 'viewer')
`, accountID, normalUserID)
	requireStatementRejected(t, tx, `
INSERT INTO group_access_grants (group_id, grantee_user_id, access_level, granted_by_user_id)
VALUES ($1, $2, 'owner', $3)
`, groupID, normalUserID, adminUserID)
	requireStatementRejected(t, tx, "UPDATE accounts SET access_version = 0 WHERE id = $1", accountID)
	requireStatementRejected(t, tx, "UPDATE groups SET authorization_mode = 'mixed' WHERE id = $1", groupID)
	requireStatementRejected(t, tx, `
INSERT INTO resource_authorization_events (
    account_id, actor_user_id, auth_method, event_type, resource_access_version
) VALUES ($1, $2, '', 'grant_created', 1)
`, accountID, adminUserID)
	_, err = tx.ExecContext(ctx, "UPDATE accounts SET owner_user_id = $1 WHERE id = $2", normalUserID, accountID)
	require.NoError(t, err)

	_, err = tx.ExecContext(ctx, `
INSERT INTO account_access_grants (
    account_id, grantee_user_id, access_level, granted_by_user_id
) VALUES ($1, $2, 'viewer', $3)
`, accountID, normalUserID, adminUserID)
	require.NoError(t, err)
	requireStatementRejected(t, tx, `
INSERT INTO account_access_grants (
    account_id, grantee_user_id, access_level, granted_by_user_id
) VALUES ($1, $2, 'consumer', $3)
`, accountID, normalUserID, adminUserID)
	requireStatementRejected(t, tx, "DELETE FROM roles WHERE id = $1", userRoleID)

	var eventID int64
	require.NoError(t, tx.QueryRowContext(ctx, `
INSERT INTO resource_authorization_events (
    account_id, resource_owner_user_id, actor_user_id, auth_method,
    event_type, resource_access_version, details
) VALUES ($1, $2, $3, 'session', 'grant_created', 1, '{"access_level":"viewer"}'::jsonb)
RETURNING id
`, accountID, normalUserID, adminUserID).Scan(&eventID))
	requireStatementRejected(t, tx, `
UPDATE resource_authorization_events SET event_type = 'changed' WHERE id = $1
`, eventID)
	requireStatementRejected(t, tx, "DELETE FROM resource_authorization_events WHERE id = $1", eventID)
	requireStatementRejected(t, tx, "TRUNCATE resource_authorization_events")
	requireStatementRejected(t, tx, "DELETE FROM accounts WHERE id = $1", accountID)
}

func requireValidResourceAccessIndex(t *testing.T, tx *sql.Tx, table, index string) {
	t.Helper()

	var valid bool
	require.NoError(t, tx.QueryRowContext(context.Background(), `
SELECT i.indisvalid
FROM pg_class idx
JOIN pg_index i ON i.indexrelid = idx.oid
JOIN pg_class tbl ON tbl.oid = i.indrelid
JOIN pg_namespace ns ON ns.oid = tbl.relnamespace
WHERE ns.nspname = 'public'
  AND tbl.relname = $1
  AND idx.relname = $2
`, table, index).Scan(&valid))
	require.True(t, valid, "expected index %s on %s to be valid", index, table)
}

func requireTable(t *testing.T, tx *sql.Tx, table string) {
	t.Helper()

	var name sql.NullString
	require.NoError(t, tx.QueryRowContext(
		context.Background(),
		"SELECT to_regclass($1)",
		"public."+table,
	).Scan(&name))
	require.True(t, name.Valid, "expected table %s to exist", table)
}

func requireStatementRejected(t *testing.T, tx *sql.Tx, query string, args ...any) {
	t.Helper()

	const savepoint = "resource_access_control_expected_rejection"
	_, err := tx.ExecContext(context.Background(), "SAVEPOINT "+savepoint)
	require.NoError(t, err)
	_, err = tx.ExecContext(context.Background(), query, args...)
	require.Error(t, err, "expected statement to be rejected")
	_, rollbackErr := tx.ExecContext(context.Background(), "ROLLBACK TO SAVEPOINT "+savepoint)
	require.NoError(t, rollbackErr)
	_, releaseErr := tx.ExecContext(context.Background(), "RELEASE SAVEPOINT "+savepoint)
	require.NoError(t, releaseErr)
}

func requireConstraintValidated(t *testing.T, tx *sql.Tx, table, constraint string) {
	t.Helper()

	var validated bool
	require.NoError(t, tx.QueryRowContext(context.Background(), `
SELECT convalidated
FROM pg_constraint
WHERE conrelid = $1::regclass
  AND conname = $2
`, table, constraint).Scan(&validated))
	require.True(t, validated, "expected %s.%s to be validated", table, constraint)
}
