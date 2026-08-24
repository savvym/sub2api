package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthorizationExpiryJobsMigrationContract(t *testing.T) {
	content, err := FS.ReadFile("238_authorization_expiry_jobs.sql")
	require.NoError(t, err)

	compact := strings.Join(strings.Fields(string(content)), " ")

	require.Contains(t, compact, "CREATE TABLE IF NOT EXISTS authorization_expiry_jobs")
	for _, column := range []string{
		"source_type VARCHAR(40) NOT NULL",
		"source_id BIGINT NOT NULL",
		"expires_at TIMESTAMPTZ NOT NULL",
		"available_at TIMESTAMPTZ NOT NULL",
		"attempts INTEGER NOT NULL DEFAULT 0",
		"last_error TEXT NOT NULL DEFAULT ''",
		"claimed_at TIMESTAMPTZ",
		"claimed_by TEXT",
		"processed_at TIMESTAMPTZ",
		"created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()",
		"updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()",
	} {
		require.Contains(t, compact, column)
	}

	for _, sourceType := range []string{
		"user_role",
		"service_principal_role",
		"account_access_grant",
		"group_access_grant",
	} {
		require.Contains(t, compact, "'"+sourceType+"'")
		require.Contains(t, compact,
			"EXECUTE FUNCTION sync_authorization_expiry_job('"+sourceType+"')",
		)
	}

	require.Contains(t, compact, "UNIQUE (source_type, source_id)")
	require.Contains(t, compact, "CHECK (available_at >= expires_at)")
	require.Contains(t, compact, "CHECK ((claimed_at IS NULL) = (claimed_by IS NULL))")
	require.Contains(t, compact, "CREATE INDEX IF NOT EXISTS idx_authorization_expiry_jobs_due ON authorization_expiry_jobs (available_at, id) WHERE processed_at IS NULL AND claimed_at IS NULL")
	require.Contains(t, compact, "CREATE INDEX IF NOT EXISTS idx_authorization_expiry_jobs_lease ON authorization_expiry_jobs (claimed_at, id) WHERE processed_at IS NULL AND claimed_at IS NOT NULL")
	require.Contains(t, compact, "CREATE INDEX IF NOT EXISTS idx_authorization_expiry_jobs_lag ON authorization_expiry_jobs (expires_at, id) WHERE processed_at IS NULL")

	require.Contains(t, compact, "CREATE OR REPLACE FUNCTION sync_authorization_expiry_job()")
	require.Contains(t, compact, "IF TG_OP = 'DELETE' THEN DELETE FROM authorization_expiry_jobs")
	require.Contains(t, compact, "IF NEW.expires_at IS NULL THEN DELETE FROM authorization_expiry_jobs")
	require.Contains(t, compact, "ON CONFLICT (source_type, source_id) DO UPDATE")
	require.Contains(t, compact, "WHERE authorization_expiry_jobs.expires_at IS DISTINCT FROM EXCLUDED.expires_at")
	require.GreaterOrEqual(t, strings.Count(compact, "WHERE authorization_expiry_jobs.expires_at IS DISTINCT FROM EXCLUDED.expires_at"), 2)

	for _, table := range []string{
		"user_roles",
		"service_principal_roles",
		"account_access_grants",
		"group_access_grants",
	} {
		require.Contains(t, compact, "AFTER INSERT OR DELETE OR UPDATE OF expires_at ON "+table)
		require.Contains(t, compact, "FROM "+table+" WHERE expires_at IS NOT NULL")
	}

	require.Contains(t, compact, "VALUES ( 'authorization_expiry_coordinator', 'Authorization Expiry Coordinator', 'active' ) ON CONFLICT (code) DO NOTHING")
	require.NotContains(t, compact, "ON CONFLICT (code) DO UPDATE")
	require.NotContains(t, compact, "UPDATE service_principals")
	require.NotContains(t, compact, "INSERT INTO service_principal_roles")
	require.Contains(t, compact, "DELETE FROM service_principal_roles WHERE service_principal_id = ( SELECT id FROM service_principals WHERE code = 'authorization_expiry_coordinator' )")
}
