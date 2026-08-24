package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type authCacheInvalidationOutboxRepository struct {
	db *sql.DB
}

func NewAuthCacheInvalidationOutboxRepository(db *sql.DB) service.AuthCacheInvalidationOutboxRepository {
	return &authCacheInvalidationOutboxRepository{db: db}
}

func (r *authCacheInvalidationOutboxRepository) Claim(ctx context.Context, workerID string, limit int, lease time.Duration) ([]service.AuthCacheInvalidationEvent, error) {
	if r == nil || r.db == nil {
		return nil, errors.New("nil auth cache invalidation outbox database")
	}
	if limit <= 0 {
		limit = 100
	}
	leaseSeconds := int64(lease / time.Second)
	if leaseSeconds < 1 {
		leaseSeconds = 30
	}
	rows, err := r.db.QueryContext(ctx, `
		WITH candidates AS (
			SELECT id
			FROM auth_cache_invalidation_outbox
				WHERE available_at <= statement_timestamp()
				  AND (
					claimed_at IS NULL
					OR claimed_at < statement_timestamp() - ($3 * INTERVAL '1 second')
				  )
				ORDER BY delivery_stage ASC, available_at ASC, id ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE auth_cache_invalidation_outbox AS o
		SET claimed_at = NOW(), claimed_by = $1
		FROM candidates AS c
		WHERE o.id = c.id
		RETURNING o.id, o.cache_key, o.attempts, o.delivery_stage, o.created_at
	`, workerID, limit, leaseSeconds)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	events := make([]service.AuthCacheInvalidationEvent, 0, limit)
	for rows.Next() {
		var event service.AuthCacheInvalidationEvent
		if err := rows.Scan(&event.ID, &event.CacheKey, &event.Attempts, &event.Stage, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.CacheKey = strings.TrimSpace(event.CacheKey)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *authCacheInvalidationOutboxRepository) ScheduleSecondPass(ctx context.Context, id int64, workerID string, delay time.Duration) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE auth_cache_invalidation_outbox
		SET delivery_stage = 1,
			available_at = statement_timestamp() + ($3 * INTERVAL '1 millisecond'),
			last_error = NULL,
			claimed_at = NULL,
			claimed_by = NULL
		WHERE id = $1 AND claimed_by = $2 AND delivery_stage = 0
	`, id, workerID, delay.Milliseconds())
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("auth cache invalidation claim %d cannot schedule second pass", id)
	}
	return nil
}

func (r *authCacheInvalidationOutboxRepository) DeleteClaimed(ctx context.Context, id int64, workerID string) error {
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM auth_cache_invalidation_outbox
		WHERE id = $1 AND claimed_by = $2
	`, id, workerID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("auth cache invalidation claim %d is no longer owned by %s", id, workerID)
	}
	return nil
}

func (r *authCacheInvalidationOutboxRepository) RetryClaimed(ctx context.Context, id int64, workerID string, delay time.Duration, lastError string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE auth_cache_invalidation_outbox
		SET attempts = attempts + 1,
			available_at = statement_timestamp() + ($3 * INTERVAL '1 millisecond'),
			last_error = $4,
			claimed_at = NULL,
			claimed_by = NULL
		WHERE id = $1 AND claimed_by = $2
	`, id, workerID, delay.Milliseconds(), lastError)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("auth cache invalidation claim %d is no longer owned by %s", id, workerID)
	}
	return nil
}

func (r *authCacheInvalidationOutboxRepository) ReleaseClaims(
	ctx context.Context,
	workerID string,
	eventIDs []int64,
) error {
	if r == nil || r.db == nil || ctx == nil || workerID == "" {
		return errors.New("invalid auth cache invalidation claim release")
	}
	if len(eventIDs) == 0 {
		return nil
	}
	for _, eventID := range eventIDs {
		if eventID <= 0 {
			return errors.New("invalid auth cache invalidation claim release")
		}
	}
	_, err := r.db.ExecContext(ctx, `
		UPDATE auth_cache_invalidation_outbox
		SET claimed_at = NULL,
			claimed_by = NULL
		WHERE claimed_by = $1
		  AND id = ANY($2::BIGINT[])
	`, workerID, pq.Array(eventIDs))
	return err
}

func (r *authCacheInvalidationOutboxRepository) Stats(ctx context.Context) (service.AuthCacheInvalidationOutboxStats, error) {
	var (
		stats           service.AuthCacheInvalidationOutboxStats
		oldest          sql.NullTime
		lastError       sql.NullString
		stage0Oldest    sql.NullTime
		stage0LastError sql.NullString
		stage1Oldest    sql.NullTime
		stage1LastError sql.NullString
	)
	err := r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(*),
			MIN(created_at),
			COALESCE(MAX(attempts), 0),
			(SELECT last_error
			 FROM auth_cache_invalidation_outbox
			 WHERE last_error IS NOT NULL
			 ORDER BY available_at DESC, id DESC
			 LIMIT 1),
			COUNT(*) FILTER (WHERE delivery_stage = 0),
			MIN(created_at) FILTER (WHERE delivery_stage = 0),
			COALESCE(MAX(attempts) FILTER (WHERE delivery_stage = 0), 0),
			(SELECT last_error
			 FROM auth_cache_invalidation_outbox
			 WHERE delivery_stage = 0 AND last_error IS NOT NULL
			 ORDER BY available_at DESC, id DESC
			 LIMIT 1),
			COUNT(*) FILTER (WHERE delivery_stage = 1),
			MIN(created_at) FILTER (WHERE delivery_stage = 1),
			COALESCE(MAX(attempts) FILTER (WHERE delivery_stage = 1), 0),
			(SELECT last_error
			 FROM auth_cache_invalidation_outbox
			 WHERE delivery_stage = 1 AND last_error IS NOT NULL
			 ORDER BY available_at DESC, id DESC
			 LIMIT 1)
		FROM auth_cache_invalidation_outbox
	`).Scan(
		&stats.Pending,
		&oldest,
		&stats.MaxAttempts,
		&lastError,
		&stats.Stage0.Pending,
		&stage0Oldest,
		&stats.Stage0.MaxAttempts,
		&stage0LastError,
		&stats.Stage1.Pending,
		&stage1Oldest,
		&stats.Stage1.MaxAttempts,
		&stage1LastError,
	)
	if err != nil {
		return stats, err
	}
	stats.OldestCreatedAt = authCacheInvalidationNullableTime(oldest)
	stats.LastError = authCacheInvalidationNullableString(lastError)
	stats.Stage0.OldestCreatedAt = authCacheInvalidationNullableTime(stage0Oldest)
	stats.Stage0.LastError = authCacheInvalidationNullableString(stage0LastError)
	stats.Stage1.OldestCreatedAt = authCacheInvalidationNullableTime(stage1Oldest)
	stats.Stage1.LastError = authCacheInvalidationNullableString(stage1LastError)
	return stats, nil
}

func authCacheInvalidationNullableTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	timestamp := value.Time
	return &timestamp
}

func authCacheInvalidationNullableString(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}
