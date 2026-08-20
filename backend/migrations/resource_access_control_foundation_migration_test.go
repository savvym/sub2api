package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResourceAuthorizationRBACMigrationContract(t *testing.T) {
	content, err := FS.ReadFile("229_resource_authorization_rbac.sql")
	require.NoError(t, err)

	sql := string(content)
	compact := strings.Join(strings.Fields(sql), " ")

	for _, table := range []string{
		"roles",
		"permissions",
		"role_permissions",
		"service_principals",
		"user_roles",
		"service_principal_roles",
	} {
		require.Contains(t, compact, "CREATE TABLE IF NOT EXISTS "+table)
	}

	for _, permission := range []string{
		"api_key.create",
		"account.create",
		"group.create",
		"resource.share",
		"resource.transfer",
		"platform.resource.view_all",
		"platform.resource.operate_all",
		"platform.resource.manage_all",
		"platform.role.manage",
		"platform.grant.manage",
		"platform.secret.export",
	} {
		require.Contains(t, sql, "'"+permission+"'")
	}

	for _, role := range []string{"user", "hoster", "platform_operator", "admin"} {
		require.Contains(t, sql, "('"+role+"',")
	}

	// Break-glass export is defined but is deliberately not inherited by any
	// normal system role, including admin.
	require.Equal(t, 1, strings.Count(sql, "'platform.secret.export'"))
	require.Contains(t, compact, "('admin', 'platform.resource.manage_all')")
	require.Contains(t, compact, "('admin', 'platform.role.manage')")
	require.Contains(t, compact, "('admin', 'platform.grant.manage')")

	require.Contains(t, compact, "CHECK (authz_version > 0)")
	require.Contains(t, compact, "VALIDATE CONSTRAINT users_authz_version_positive")
	require.Contains(t, compact, "CHECK (num_nonnulls(granted_by_user_id, granted_by_service_principal_id) = 1)")
	require.Contains(t, compact, "REFERENCES roles(id) ON DELETE RESTRICT")
	require.Contains(t, compact, "VALUES ('system_bootstrap', 'System Bootstrap', 'active')")
	require.Contains(t, compact, "CASE WHEN users.role = 'admin' THEN 'admin' ELSE 'user' END")
	require.Contains(t, compact, "DELETE FROM user_roles USING users, roles, service_principals")
	require.Contains(t, compact, "service_principals.code = 'system_bootstrap'")
	require.Contains(t, compact, "roles.code IN ('user', 'admin')")
	require.Contains(t, compact, "roles.code <> CASE WHEN users.role = 'admin' THEN 'admin' ELSE 'user' END")
	require.Contains(t, compact, "ON CONFLICT (user_id, role_id) DO NOTHING")
}

func TestResourceAccessControlFoundationMigrationContract(t *testing.T) {
	content, err := FS.ReadFile("230_resource_access_control_foundation.sql")
	require.NoError(t, err)

	sql := string(content)
	compact := strings.Join(strings.Fields(sql), " ")

	for _, table := range []string{
		"account_access_grants",
		"group_access_grants",
		"resource_authorization_events",
	} {
		require.Contains(t, compact, "CREATE TABLE IF NOT EXISTS "+table)
	}

	for _, table := range []string{"accounts", "groups"} {
		require.Contains(t, compact, "ALTER TABLE "+table)
		require.Contains(t, sql, table+"_owner_user_id_fkey")
		require.Contains(t, sql, table+"_created_by_user_id_fkey")
		require.Contains(t, sql, table+"_public_access_level_check")
		require.Contains(t, sql, table+"_access_version_positive")
	}

	require.Contains(t, compact, "CHECK (public_access_level IS NULL OR public_access_level IN ('viewer', 'consumer'))")
	require.Contains(t, compact, "CHECK (authorization_mode IN ('legacy', 'shadow', 'acl'))")
	require.Contains(t, compact, "VALIDATE CONSTRAINT accounts_owner_user_id_fkey")
	require.Contains(t, compact, "VALIDATE CONSTRAINT groups_authorization_mode_check")
	require.Contains(t, compact, "CHECK (access_level IN ('viewer', 'consumer', 'maintainer', 'manager'))")
	require.Contains(t, compact, "CHECK (num_nonnulls(grantee_user_id, grantee_role_id) = 1)")
	require.Contains(t, compact, "CHECK (num_nonnulls(granted_by_user_id, granted_by_service_principal_id) = 1)")

	for _, index := range []string{
		"account_access_grants_account_user_key",
		"account_access_grants_account_role_key",
		"group_access_grants_group_user_key",
		"group_access_grants_group_role_key",
	} {
		require.Contains(t, compact, "CREATE UNIQUE INDEX IF NOT EXISTS "+index)
	}
	require.Contains(t, compact, "WHERE grantee_user_id IS NOT NULL")
	require.Contains(t, compact, "WHERE grantee_role_id IS NOT NULL")

	require.Contains(t, compact, "CHECK (num_nonnulls(account_id, group_id) = 1)")
	require.Contains(t, compact, "CHECK (num_nonnulls(actor_user_id, actor_service_principal_id) = 1)")
	require.Contains(t, compact, "resource_owner_user_id BIGINT")
	require.Contains(t, compact, "FOREIGN KEY (resource_owner_user_id) REFERENCES users(id) ON DELETE RESTRICT")
	require.Contains(t, compact, "auth_method VARCHAR(32) NOT NULL DEFAULT 'unknown'")
	require.Contains(t, compact, "CHECK (BTRIM(auth_method) <> '')")
	require.Contains(t, compact, "BEFORE UPDATE OR DELETE OR TRUNCATE ON resource_authorization_events")
	require.Contains(t, compact, "RAISE EXCEPTION 'resource_authorization_events is append-only'")
	for _, index := range []string{
		"idx_accounts_owner_user_id",
		"idx_accounts_created_by_user_id",
		"idx_groups_owner_user_id",
		"idx_groups_created_by_user_id",
		"idx_groups_authorization_mode",
	} {
		require.NotContains(t, sql, index)
	}
	require.NotContains(t, sql, "CONCURRENTLY")
}

func TestResourceAccessControlFoundationIndexesMigrationContract(t *testing.T) {
	content, err := FS.ReadFile("231_resource_access_control_foundation_indexes_notx.sql")
	require.NoError(t, err)

	compact := strings.Join(strings.Fields(string(content)), " ")
	for _, index := range []string{
		"idx_accounts_owner_user_id",
		"idx_accounts_created_by_user_id",
		"idx_groups_owner_user_id",
		"idx_groups_created_by_user_id",
		"idx_groups_authorization_mode",
	} {
		require.Contains(t, compact, "CREATE INDEX CONCURRENTLY IF NOT EXISTS "+index)
	}
	require.Contains(t, compact, "ON accounts (owner_user_id) WHERE owner_user_id IS NOT NULL")
	require.Contains(t, compact, "ON accounts (created_by_user_id) WHERE created_by_user_id IS NOT NULL")
	require.Contains(t, compact, "ON groups (owner_user_id) WHERE owner_user_id IS NOT NULL")
	require.Contains(t, compact, "ON groups (created_by_user_id) WHERE created_by_user_id IS NOT NULL")
	require.Contains(t, compact, "ON groups (authorization_mode, id)")
}

func TestResourceAuthorizationCompatibilityBackfillMigrationContract(t *testing.T) {
	rbacContent, err := FS.ReadFile("229_resource_authorization_rbac.sql")
	require.NoError(t, err)
	backfillContent, err := FS.ReadFile("232_resource_authorization_compatibility_backfill.sql")
	require.NoError(t, err)

	// Migration 232 must retain migration 229's exact convergence boundary:
	// only stale user/admin grants attributed to system_bootstrap are removed,
	// while manual grants, other service-principal grants, and other roles stay.
	require.Equal(
		t,
		compatibilityRoleConvergenceSQL(t, string(rbacContent)),
		compatibilityRoleConvergenceSQL(t, string(backfillContent)),
	)

	compact := strings.Join(strings.Fields(string(backfillContent)), " ")
	require.NotContains(t, compact, "INSERT INTO roles")
	require.NotContains(t, compact, "INSERT INTO permissions")
	require.NotContains(t, compact, "INSERT INTO service_principals")
}

func compatibilityRoleConvergenceSQL(t *testing.T, sql string) string {
	t.Helper()
	compact := strings.Join(strings.Fields(sql), " ")
	start := strings.Index(compact, "DELETE FROM user_roles")
	require.NotEqual(t, -1, start)
	return compact[start:]
}

func TestResourceAuthorizationMigrationsDoNotSeedFeatureFlags(t *testing.T) {
	rbacContent, err := FS.ReadFile("229_resource_authorization_rbac.sql")
	require.NoError(t, err)
	aclContent, err := FS.ReadFile("230_resource_access_control_foundation.sql")
	require.NoError(t, err)
	indexContent, err := FS.ReadFile("231_resource_access_control_foundation_indexes_notx.sql")
	require.NoError(t, err)
	backfillContent, err := FS.ReadFile("232_resource_authorization_compatibility_backfill.sql")
	require.NoError(t, err)

	sql := string(rbacContent) + string(aclContent) + string(indexContent) + string(backfillContent)
	for _, setting := range []string{
		"resource_access_control_enabled",
		"self_service_hosting_enabled",
		"group_sharing_enabled",
		"account_sharing_enabled",
		"role_based_resource_grants_enabled",
	} {
		require.NotContains(t, sql, setting)
	}
	require.NotContains(t, sql, "INSERT INTO settings")
}
