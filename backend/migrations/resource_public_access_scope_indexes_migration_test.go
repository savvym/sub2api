package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResourcePublicAccessScopeIndexesMigrationIsOnlineAndPartial(t *testing.T) {
	content, err := FS.ReadFile("242_resource_public_access_scope_indexes_notx.sql")
	require.NoError(t, err)

	compact := strings.Join(strings.Fields(string(content)), " ")
	for _, index := range []string{
		"idx_accounts_public_access_level",
		"idx_groups_public_access_level",
	} {
		require.Contains(t, compact, "CREATE INDEX CONCURRENTLY IF NOT EXISTS "+index)
	}
	require.Contains(t, compact, "ON accounts (public_access_level, id) WHERE public_access_level IS NOT NULL AND deleted_at IS NULL")
	require.Contains(t, compact, "ON groups (public_access_level, id) WHERE public_access_level IS NOT NULL AND deleted_at IS NULL")
}
