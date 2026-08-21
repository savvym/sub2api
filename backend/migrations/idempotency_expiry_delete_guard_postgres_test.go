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

func TestIdempotencyExpiryDeleteGuardBlocksStaleCleanupCandidate(t *testing.T) {
	db := newRoleCachePostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, repository.ApplyMigrations(ctx, db))

	var recordID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status, expires_at
) VALUES (
    'integration.upgrade-fence', repeat('a', 64),
    'upgrade-fence:actor-qualified:v1', 'processing', CURRENT_TIMESTAMP - INTERVAL '1 minute'
)
RETURNING id`).Scan(&recordID))

	renewTx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	require.NoError(t, err)
	committed := false
	defer func() {
		if !committed {
			_ = renewTx.Rollback()
		}
	}()
	_, err = renewTx.ExecContext(ctx, `
UPDATE idempotency_records
SET expires_at = CURRENT_TIMESTAMP + INTERVAL '1 hour'
WHERE id = $1`, recordID)
	require.NoError(t, err)

	cleanupConn, err := db.Conn(ctx)
	require.NoError(t, err)
	defer func() { _ = cleanupConn.Close() }()
	var cleanupPID int
	require.NoError(t, cleanupConn.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&cleanupPID))

	type cleanupResult struct {
		result sql.Result
		err    error
	}
	cleanupDone := make(chan cleanupResult, 1)
	go func() {
		result, cleanupErr := cleanupConn.ExecContext(ctx, `
WITH victims AS (
    SELECT id
    FROM idempotency_records
    WHERE expires_at <= CURRENT_TIMESTAMP
    ORDER BY expires_at ASC
    LIMIT 500
)
DELETE FROM idempotency_records
WHERE id IN (SELECT id FROM victims)`)
		cleanupDone <- cleanupResult{result: result, err: cleanupErr}
	}()

	require.Eventually(t, func() bool {
		var waitEventType sql.NullString
		queryErr := db.QueryRowContext(ctx, `
SELECT wait_event_type
FROM pg_stat_activity
WHERE pid = $1`, cleanupPID).Scan(&waitEventType)
		return queryErr == nil && waitEventType.Valid && waitEventType.String == "Lock"
	}, 10*time.Second, 20*time.Millisecond, "old cleanup did not block behind the renewal row lock")

	require.NoError(t, renewTx.Commit())
	committed = true
	cleanup := <-cleanupDone
	require.NoError(t, cleanup.err)
	affected, err := cleanup.result.RowsAffected()
	require.NoError(t, err)
	require.Zero(t, affected, "stale cleanup must not delete a record renewed after its CTE snapshot")

	var expiresAt time.Time
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT expires_at
FROM idempotency_records
WHERE id = $1`, recordID).Scan(&expiresAt))
	require.True(t, expiresAt.After(time.Now().Add(50*time.Minute)))
}
