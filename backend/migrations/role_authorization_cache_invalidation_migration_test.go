package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRoleAuthorizationCacheInvalidationMigrationContract(t *testing.T) {
	content, err := FS.ReadFile("233_role_authorization_cache_invalidation.sql")
	require.NoError(t, err)

	sql := string(content)
	compact := strings.Join(strings.Fields(sql), " ")

	require.Contains(t, compact, "CREATE OR REPLACE FUNCTION enqueue_user_auth_cache_invalidation()")
	for _, comparison := range []string{
		"OLD.status IS NOT DISTINCT FROM NEW.status",
		"OLD.role IS NOT DISTINCT FROM NEW.role",
		"OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at",
		"OLD.authz_version IS NOT DISTINCT FROM NEW.authz_version",
	} {
		require.Contains(t, compact, comparison)
	}

	require.Equal(t, 1, strings.Count(compact, "INSERT INTO auth_cache_invalidation_outbox"))
	require.Contains(t, compact, "SELECT encode(sha256(convert_to(k.key, 'UTF8')), 'hex')")
	require.Contains(t, compact, "WHERE k.user_id = target_user_id")
	require.Contains(t, compact, "AND k.deleted_at IS NULL")
	require.Contains(t, compact, "AND k.key <> ''")
	require.NotContains(t, compact, "VALUES (k.key)")
	require.NotContains(t, compact, "DROP TRIGGER")
	require.NotContains(t, compact, "CREATE TRIGGER")
}
