package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSchedulerOutboxClaimLeaseMigrationContract(t *testing.T) {
	content, err := FS.ReadFile("239_scheduler_outbox_claim_lease.sql")
	require.NoError(t, err)

	compact := strings.Join(strings.Fields(string(content)), " ")

	for _, column := range []string{
		"lease_token VARCHAR(64)",
		"lease_expires_at TIMESTAMPTZ",
		"next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp()",
		"attempt_count BIGINT NOT NULL DEFAULT 0",
		"last_error VARCHAR(1024)",
	} {
		require.Contains(t, compact, "ADD COLUMN IF NOT EXISTS "+column)
	}

	require.Contains(t, compact, "CONSTRAINT scheduler_outbox_attempt_count_nonnegative CHECK (attempt_count >= 0)")
	require.Contains(t, compact, "CONSTRAINT scheduler_outbox_lease_pair_consistent CHECK ((lease_token IS NULL) = (lease_expires_at IS NULL))")
	require.Equal(t, 2, strings.Count(compact, "NOT VALID"))
	require.Equal(t, 2, strings.Count(compact, "AND conrelid = 'scheduler_outbox'::regclass"))
	require.NotContains(t, compact, "CREATE INDEX")
}

func TestSchedulerOutboxClaimIndexMigrationIsOnline(t *testing.T) {
	content, err := FS.ReadFile("241_scheduler_outbox_claim_index_notx.sql")
	require.NoError(t, err)

	compact := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, compact, "CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_scheduler_outbox_claimable")
	require.Contains(t, compact, "ON scheduler_outbox (next_attempt_at, lease_expires_at, id)")
}
