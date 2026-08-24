//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSchedulerOutboxClaimDoesNotLoseLowerIDCommittedAfterHigherID(t *testing.T) {
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, "TRUNCATE scheduler_outbox RESTART IDENTITY")
	require.NoError(t, err)

	slowTx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = slowTx.Rollback() })

	var lowerID int64
	err = slowTx.QueryRowContext(ctx, `
		INSERT INTO scheduler_outbox (event_type)
		VALUES ('lower_id_late_commit')
		RETURNING id
	`).Scan(&lowerID)
	require.NoError(t, err)

	var higherID int64
	err = integrationDB.QueryRowContext(ctx, `
		INSERT INTO scheduler_outbox (event_type)
		VALUES ('higher_id_early_commit')
		RETURNING id
	`).Scan(&higherID)
	require.NoError(t, err)
	require.Less(t, lowerID, higherID)

	repo := NewSchedulerOutboxRepository(integrationDB)
	claimed, err := repo.Claim(ctx, 10, time.Minute)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, higherID, claimed[0].ID)
	acknowledged, err := repo.Acknowledge(ctx, higherID, claimed[0].LeaseToken)
	require.NoError(t, err)
	require.True(t, acknowledged)

	require.NoError(t, slowTx.Commit())
	claimed, err = repo.Claim(ctx, 10, time.Minute)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.Equal(t, lowerID, claimed[0].ID)
	acknowledged, err = repo.Acknowledge(ctx, lowerID, claimed[0].LeaseToken)
	require.NoError(t, err)
	require.True(t, acknowledged)

	stats, err := repo.PendingStats(ctx)
	require.NoError(t, err)
	require.Zero(t, stats.Count)
}

func TestSchedulerOutboxExpiredLeaseIsReclaimedAndFenced(t *testing.T) {
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, "TRUNCATE scheduler_outbox RESTART IDENTITY")
	require.NoError(t, err)

	var eventID int64
	err = integrationDB.QueryRowContext(ctx, `
		INSERT INTO scheduler_outbox (event_type, dedup_key)
		VALUES ('lease_recovery', 'lease-recovery-key')
		RETURNING id
	`).Scan(&eventID)
	require.NoError(t, err)

	firstWorker := NewSchedulerOutboxRepository(integrationDB)
	secondWorker := NewSchedulerOutboxRepository(integrationDB)
	firstClaim, err := firstWorker.Claim(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Len(t, firstClaim, 1)
	require.Equal(t, eventID, firstClaim[0].ID)

	var dedupKey sql.NullString
	err = integrationDB.QueryRowContext(ctx, "SELECT dedup_key FROM scheduler_outbox WHERE id = $1", eventID).Scan(&dedupKey)
	require.NoError(t, err)
	require.False(t, dedupKey.Valid, "claim must release the pending dedup key")

	_, err = integrationDB.ExecContext(ctx, `
		UPDATE scheduler_outbox
		SET lease_expires_at = statement_timestamp() - INTERVAL '1 second'
		WHERE id = $1
	`, eventID)
	require.NoError(t, err)

	secondClaim, err := secondWorker.Claim(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Len(t, secondClaim, 1)
	require.Equal(t, eventID, secondClaim[0].ID)
	require.NotEqual(t, firstClaim[0].LeaseToken, secondClaim[0].LeaseToken)
	require.Equal(t, int64(2), secondClaim[0].AttemptCount)

	acknowledged, err := firstWorker.Acknowledge(ctx, eventID, firstClaim[0].LeaseToken)
	require.NoError(t, err)
	require.False(t, acknowledged, "expired worker token must be fenced")
	acknowledged, err = secondWorker.Acknowledge(ctx, eventID, secondClaim[0].LeaseToken)
	require.NoError(t, err)
	require.True(t, acknowledged)
}
