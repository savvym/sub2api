package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuditLogServicePrincipalActorMigrationContract(t *testing.T) {
	content, err := FS.ReadFile("234_audit_log_service_principal_actor.sql")
	require.NoError(t, err)

	sql := string(content)
	compact := strings.Join(strings.Fields(sql), " ")

	require.Contains(t, compact, "VALUES ('admin_api_key', 'Admin API Key', 'active') ON CONFLICT (code) DO NOTHING")
	require.NotContains(t, compact, "ON CONFLICT (code) DO UPDATE")
	require.NotContains(t, compact, "UPDATE service_principals")
	require.NotContains(t, compact, "INSERT INTO service_principal_roles")
	require.NotContains(t, compact, "INSERT INTO role_permissions")
	require.Contains(t, compact, "DELETE FROM service_principal_roles WHERE service_principal_id = ( SELECT id FROM service_principals WHERE code = 'admin_api_key' )")

	require.Contains(t, compact, "ADD COLUMN IF NOT EXISTS actor_service_principal_id BIGINT")
	require.Contains(t, compact, "FOREIGN KEY (actor_service_principal_id) REFERENCES service_principals(id) ON DELETE RESTRICT NOT VALID")
	require.Contains(t, compact, "VALIDATE CONSTRAINT audit_logs_actor_service_principal_id_fkey")
	require.Contains(t, compact, "CHECK (num_nonnulls(actor_user_id, actor_service_principal_id) <= 1) NOT VALID")
	require.NotContains(t, compact, "num_nonnulls(actor_user_id, actor_service_principal_id) = 1")
	require.Contains(t, compact, "VALIDATE CONSTRAINT audit_logs_actor_at_most_one_check")
	require.NotContains(t, compact, "CREATE INDEX")
	require.NotContains(t, compact, "CONCURRENTLY")

	indexContent, err := FS.ReadFile("235_audit_log_actor_indexes_notx.sql")
	require.NoError(t, err)
	indexSQL := strings.Join(strings.Fields(string(indexContent)), " ")

	require.Contains(t, indexSQL, "DROP INDEX CONCURRENTLY IF EXISTS idx_audit_logs_actor_created")
	require.Contains(t, indexSQL, "DROP INDEX CONCURRENTLY IF EXISTS idx_audit_logs_service_principal_created")
	require.Contains(t, indexSQL, "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_actor_created ON audit_logs (actor_user_id, created_at DESC) WHERE actor_user_id IS NOT NULL")
	require.Contains(t, indexSQL, "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_service_principal_created ON audit_logs (actor_service_principal_id, created_at DESC) WHERE actor_service_principal_id IS NOT NULL")
}
