package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthCacheInvalidationPriorityIndexMigrationIsOnlineAndSupportsClaimOrder(t *testing.T) {
	content, err := FS.ReadFile("240_auth_cache_invalidation_priority_index_notx.sql")
	require.NoError(t, err)
	sql := strings.Join(strings.Fields(string(content)), " ")

	require.Contains(t, sql, "CREATE INDEX CONCURRENTLY IF NOT EXISTS")
	require.Contains(t, sql, "(delivery_stage, available_at, id)")
}
