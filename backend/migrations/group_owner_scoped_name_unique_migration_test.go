package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupOwnerScopedNameUniqueMigrationIsOnlineAndScoped(t *testing.T) {
	content, err := FS.ReadFile("245_group_owner_scoped_name_unique_notx.sql")
	require.NoError(t, err)

	compact := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, compact, `CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_groups_platform_name_unique_active ON groups (lower(name)) WHERE owner_user_id IS NULL AND deleted_at IS NULL`)
	require.Contains(t, compact, `CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS idx_groups_owner_name_unique_active ON groups (owner_user_id, lower(name)) WHERE owner_user_id IS NOT NULL AND deleted_at IS NULL`)
	require.Contains(t, compact, `DROP INDEX CONCURRENTLY IF EXISTS groups_name_unique_active`)
	require.NotContains(t, compact, `DROP INDEX IF EXISTS groups_name_unique_active`)
}
