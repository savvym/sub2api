package repository

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type schedulerOutboxRepository struct {
	db *sql.DB
}

type schedulerOutboxCleanupLease struct {
	conn *sql.Conn
}

const schedulerOutboxDefaultCleanSize = 5000

const (
	schedulerOutboxDefaultClaimSize = 100
	schedulerOutboxMaxClaimSize     = 1000
	schedulerOutboxDefaultLease     = 3 * time.Minute
	schedulerOutboxMaxLease         = 24 * time.Hour
	schedulerOutboxMaxRetryDelay    = 24 * time.Hour
	schedulerOutboxMaxErrorLength   = 1024
)

func NewSchedulerOutboxRepository(db *sql.DB) service.SchedulerOutboxRepository {
	return &schedulerOutboxRepository{db: db}
}

func (r *schedulerOutboxRepository) Claim(
	ctx context.Context,
	limit int,
	leaseDuration time.Duration,
) ([]service.SchedulerOutboxEvent, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("scheduler outbox repository is unavailable")
	}
	if limit <= 0 {
		limit = schedulerOutboxDefaultClaimSize
	} else if limit > schedulerOutboxMaxClaimSize {
		limit = schedulerOutboxMaxClaimSize
	}
	if leaseDuration <= 0 {
		leaseDuration = schedulerOutboxDefaultLease
	} else if leaseDuration > schedulerOutboxMaxLease {
		leaseDuration = schedulerOutboxMaxLease
	}
	leaseToken, err := newSchedulerOutboxLeaseToken()
	if err != nil {
		return nil, err
	}
	leaseMilliseconds := max(leaseDuration.Milliseconds(), int64(1))
	rows, err := r.db.QueryContext(ctx, `
		WITH claimable AS MATERIALIZED (
			SELECT id
			FROM scheduler_outbox
			WHERE next_attempt_at <= statement_timestamp()
				AND (lease_token IS NULL OR lease_expires_at <= statement_timestamp())
			ORDER BY next_attempt_at ASC, id ASC
			LIMIT $1
			FOR UPDATE SKIP LOCKED
		), claimed AS (
			UPDATE scheduler_outbox AS o
			SET lease_token = $2,
				lease_expires_at = statement_timestamp() + ($3 * INTERVAL '1 millisecond'),
				dedup_key = NULL,
				attempt_count = o.attempt_count + 1
			FROM claimable AS c
			WHERE o.id = c.id
			RETURNING o.id, o.event_type, o.account_id, o.group_id, o.payload,
				o.created_at, o.lease_token, o.lease_expires_at, o.attempt_count, o.last_error
		)
		SELECT id, event_type, account_id, group_id, payload, created_at,
			lease_token, lease_expires_at, attempt_count, last_error
		FROM claimed
		ORDER BY id ASC
	`, limit, leaseToken, leaseMilliseconds)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	events := make([]service.SchedulerOutboxEvent, 0, limit)
	for rows.Next() {
		event, scanErr := scanSchedulerOutboxEvent(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *schedulerOutboxRepository) Acknowledge(ctx context.Context, eventID int64, leaseToken string) (bool, error) {
	if r == nil || r.db == nil || eventID <= 0 || leaseToken == "" {
		return false, nil
	}
	result, err := r.db.ExecContext(ctx, `
		DELETE FROM scheduler_outbox
		WHERE id = $1 AND lease_token = $2
	`, eventID, leaseToken)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (r *schedulerOutboxRepository) Retry(
	ctx context.Context,
	eventID int64,
	leaseToken string,
	lastError string,
	retryDelay time.Duration,
) (bool, error) {
	if r == nil || r.db == nil || eventID <= 0 || leaseToken == "" {
		return false, nil
	}
	if retryDelay < 0 {
		retryDelay = 0
	} else if retryDelay > schedulerOutboxMaxRetryDelay {
		retryDelay = schedulerOutboxMaxRetryDelay
	}
	retryMilliseconds := retryDelay.Milliseconds()
	result, err := r.db.ExecContext(ctx, `
		UPDATE scheduler_outbox
		SET lease_token = NULL,
			lease_expires_at = NULL,
			next_attempt_at = statement_timestamp() + ($3 * INTERVAL '1 millisecond'),
			last_error = LEFT($4, $5)
		WHERE id = $1 AND lease_token = $2
	`, eventID, leaseToken, retryMilliseconds, lastError, schedulerOutboxMaxErrorLength)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (r *schedulerOutboxRepository) PendingStats(ctx context.Context) (service.SchedulerOutboxPendingStats, error) {
	if r == nil || r.db == nil {
		return service.SchedulerOutboxPendingStats{}, fmt.Errorf("scheduler outbox repository is unavailable")
	}
	var (
		count  int64
		oldest sql.NullTime
	)
	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*), MIN(created_at)
		FROM scheduler_outbox
	`).Scan(&count, &oldest); err != nil {
		return service.SchedulerOutboxPendingStats{}, err
	}
	stats := service.SchedulerOutboxPendingStats{Count: count}
	if oldest.Valid {
		stats.OldestCreatedAt = oldest.Time
	}
	return stats, nil
}

func newSchedulerOutboxLeaseToken() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate scheduler outbox lease token: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

type schedulerOutboxRowScanner interface {
	Scan(dest ...any) error
}

func scanSchedulerOutboxEvent(row schedulerOutboxRowScanner) (service.SchedulerOutboxEvent, error) {
	var (
		payloadRaw []byte
		accountID  sql.NullInt64
		groupID    sql.NullInt64
		lastError  sql.NullString
		event      service.SchedulerOutboxEvent
	)
	if err := row.Scan(
		&event.ID,
		&event.EventType,
		&accountID,
		&groupID,
		&payloadRaw,
		&event.CreatedAt,
		&event.LeaseToken,
		&event.LeaseExpiresAt,
		&event.AttemptCount,
		&lastError,
	); err != nil {
		return service.SchedulerOutboxEvent{}, err
	}
	if accountID.Valid {
		value := accountID.Int64
		event.AccountID = &value
	}
	if groupID.Valid {
		value := groupID.Int64
		event.GroupID = &value
	}
	if lastError.Valid {
		event.LastError = lastError.String
	}
	if len(payloadRaw) > 0 {
		if err := json.Unmarshal(payloadRaw, &event.Payload); err != nil {
			event.PayloadDecodeError = err.Error()
		}
	}
	return event, nil
}

func (r *schedulerOutboxRepository) ListAfterAndReleaseDedup(ctx context.Context, afterID int64, limit int) ([]service.SchedulerOutboxEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		WITH selected AS MATERIALIZED (
			SELECT id, event_type, account_id, group_id, payload, created_at
			FROM scheduler_outbox
			WHERE id > $1
			ORDER BY id ASC
			LIMIT $2
			FOR UPDATE
		), released AS (
			UPDATE scheduler_outbox AS o
			SET dedup_key = NULL
			FROM selected AS s
			WHERE o.id = s.id
				AND o.dedup_key IS NOT NULL
			RETURNING o.id
		)
		SELECT s.id, s.event_type, s.account_id, s.group_id, s.payload, s.created_at
		FROM selected AS s
		CROSS JOIN (SELECT COUNT(*) FROM released) AS release_barrier
		ORDER BY s.id ASC
	`, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	events := make([]service.SchedulerOutboxEvent, 0, limit)
	for rows.Next() {
		var (
			payloadRaw []byte
			accountID  sql.NullInt64
			groupID    sql.NullInt64
			event      service.SchedulerOutboxEvent
		)
		if err := rows.Scan(&event.ID, &event.EventType, &accountID, &groupID, &payloadRaw, &event.CreatedAt); err != nil {
			return nil, err
		}
		if accountID.Valid {
			v := accountID.Int64
			event.AccountID = &v
		}
		if groupID.Valid {
			v := groupID.Int64
			event.GroupID = &v
		}
		if len(payloadRaw) > 0 {
			var payload map[string]any
			if err := json.Unmarshal(payloadRaw, &payload); err != nil {
				return nil, err
			}
			event.Payload = payload
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return events, nil
}

func (r *schedulerOutboxRepository) FirstCreatedAtAfter(ctx context.Context, afterID int64) (time.Time, bool, error) {
	var createdAt time.Time
	err := r.db.QueryRowContext(ctx, `
		SELECT created_at
		FROM scheduler_outbox
		WHERE id > $1
		ORDER BY id ASC
		LIMIT 1
	`, afterID).Scan(&createdAt)
	if err == sql.ErrNoRows {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return createdAt, true, nil
}

func (r *schedulerOutboxRepository) MaxID(ctx context.Context) (int64, error) {
	var maxID int64
	if err := r.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM scheduler_outbox").Scan(&maxID); err != nil {
		return 0, err
	}
	return maxID, nil
}

func (r *schedulerOutboxRepository) DeleteConsumedUpTo(ctx context.Context, watermark int64, limit int) (int64, error) {
	if watermark <= 0 {
		return 0, nil
	}
	if limit <= 0 {
		limit = schedulerOutboxDefaultCleanSize
	}
	// created_at < NOW() - INTERVAL '10 seconds' 防御 PG 序列号在事务内提前分配但
	// 提交延迟的竞争：若某 Tx 在 watermark 推进前持有 id=N（未提交），watermark
	// 跨过 N 后该 Tx 才提交，此时 row N 已经"低于 watermark"但从未被 poll；10s
	// 宽限期让此类慢事务有机会提交后被消费，再被 cleanup 删除。
	result, err := r.db.ExecContext(ctx, `
		WITH doomed AS (
			SELECT id
			FROM scheduler_outbox
			WHERE id <= $1
				AND created_at < NOW() - INTERVAL '10 seconds'
			ORDER BY id ASC
			LIMIT $2
		)
		DELETE FROM scheduler_outbox o
		USING doomed d
		WHERE o.id = d.id
	`, watermark, limit)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r *schedulerOutboxRepository) TryAcquireCleanupLock(ctx context.Context) (service.SchedulerOutboxCleanupLease, bool, error) {
	conn, err := r.db.Conn(ctx)
	if err != nil {
		return nil, false, err
	}

	var acquired bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock(hashtext('scheduler_outbox_cleanup'))").Scan(&acquired); err != nil {
		_ = conn.Close()
		return nil, false, err
	}
	if !acquired {
		_ = conn.Close()
		return nil, false, nil
	}
	return &schedulerOutboxCleanupLease{conn: conn}, true, nil
}

func (l *schedulerOutboxCleanupLease) Release() {
	if l == nil || l.conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = l.conn.ExecContext(ctx, "SELECT pg_advisory_unlock(hashtext('scheduler_outbox_cleanup'))")
	_ = l.conn.Close()
	l.conn = nil
}

func enqueueSchedulerOutbox(ctx context.Context, exec sqlExecutor, eventType string, accountID *int64, groupID *int64, payload any) error {
	if exec == nil {
		return nil
	}
	var payloadArg any
	var payloadJSON []byte
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		payloadArg = encoded
		payloadJSON = encoded
	}
	query := `
		INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
		VALUES ($1, $2, $3, $4)
	`
	args := []any{eventType, accountID, groupID, payloadArg}
	if schedulerOutboxEventSupportsDedup(eventType) {
		dedupKey := schedulerOutboxDedupKey(eventType, accountID, groupID, payloadJSON)
		query = `
			INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload, dedup_key)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING
		`
		args = append(args, dedupKey)
	}
	_, err := exec.ExecContext(ctx, query, args...)
	return err
}

func schedulerOutboxDedupKey(eventType string, accountID *int64, groupID *int64, payloadJSON []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(eventType))
	_, _ = h.Write([]byte{0})
	if accountID != nil {
		_, _ = h.Write([]byte(strconv.FormatInt(*accountID, 10)))
	}
	_, _ = h.Write([]byte{0})
	if groupID != nil {
		_, _ = h.Write([]byte(strconv.FormatInt(*groupID, 10)))
	}
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(payloadJSON)
	return fmt.Sprintf("scheduler_outbox:%s", hex.EncodeToString(h.Sum(nil)))
}

func schedulerOutboxEventSupportsDedup(eventType string) bool {
	switch eventType {
	case service.SchedulerOutboxEventAccountChanged,
		service.SchedulerOutboxEventGroupChanged,
		service.SchedulerOutboxEventFullRebuild:
		return true
	default:
		return false
	}
}
