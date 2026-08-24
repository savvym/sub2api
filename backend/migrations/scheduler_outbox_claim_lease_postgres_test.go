//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestSchedulerOutboxClaimLeasePostgres(t *testing.T) {
	db := newRoleCachePostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, repository.ApplyMigrations(ctx, db))

	t.Run("lower id committing late remains claimable", func(t *testing.T) {
		_, err := db.ExecContext(ctx, "TRUNCATE scheduler_outbox RESTART IDENTITY")
		require.NoError(t, err)

		slowTx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		t.Cleanup(func() { _ = slowTx.Rollback() })

		var lowerID int64
		require.NoError(t, slowTx.QueryRowContext(ctx, `
INSERT INTO scheduler_outbox (event_type)
VALUES ('lower_id_late_commit')
RETURNING id`).Scan(&lowerID))

		var higherID int64
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO scheduler_outbox (event_type)
VALUES ('higher_id_early_commit')
RETURNING id`).Scan(&higherID))
		require.Less(t, lowerID, higherID)

		repo := repository.NewSchedulerOutboxRepository(db)
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
	})

	t.Run("expired lease is reclaimed and old token is fenced", func(t *testing.T) {
		_, err := db.ExecContext(ctx, "TRUNCATE scheduler_outbox RESTART IDENTITY")
		require.NoError(t, err)

		var eventID int64
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO scheduler_outbox (event_type, dedup_key)
VALUES ('lease_recovery', 'lease-recovery-key')
RETURNING id`).Scan(&eventID))

		firstWorker := repository.NewSchedulerOutboxRepository(db)
		secondWorker := repository.NewSchedulerOutboxRepository(db)
		firstClaim, err := firstWorker.Claim(ctx, 1, time.Minute)
		require.NoError(t, err)
		require.Len(t, firstClaim, 1)

		var dedupKey sql.NullString
		require.NoError(t, db.QueryRowContext(
			ctx,
			"SELECT dedup_key FROM scheduler_outbox WHERE id = $1",
			eventID,
		).Scan(&dedupKey))
		require.False(t, dedupKey.Valid, "claim must release the pending dedup key")

		_, err = db.ExecContext(ctx, `
UPDATE scheduler_outbox
SET lease_expires_at = statement_timestamp() - INTERVAL '1 second'
WHERE id = $1`, eventID)
		require.NoError(t, err)

		secondClaim, err := secondWorker.Claim(ctx, 1, time.Minute)
		require.NoError(t, err)
		require.Len(t, secondClaim, 1)
		require.Equal(t, eventID, secondClaim[0].ID)
		require.NotEqual(t, firstClaim[0].LeaseToken, secondClaim[0].LeaseToken)
		require.Equal(t, int64(2), secondClaim[0].AttemptCount)

		acknowledged, err := firstWorker.Acknowledge(ctx, eventID, firstClaim[0].LeaseToken)
		require.NoError(t, err)
		require.False(t, acknowledged)
		acknowledged, err = secondWorker.Acknowledge(ctx, eventID, secondClaim[0].LeaseToken)
		require.NoError(t, err)
		require.True(t, acknowledged)
	})
}
