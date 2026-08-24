package repository

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerOutboxRepositoryClaimUsesAtomicLeaseAndSkipLocked(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &schedulerOutboxRepository{db: db}
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	leaseExpiresAt := createdAt.Add(45 * time.Second)
	const expectedSQL = `
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
	`
	mock.ExpectQuery(regexp.QuoteMeta(expectedSQL)).
		WithArgs(2, sqlmock.AnyArg(), int64((45 * time.Second).Milliseconds())).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "event_type", "account_id", "group_id", "payload", "created_at",
			"lease_token", "lease_expires_at", "attempt_count", "last_error",
		}).AddRow(
			int64(7), service.SchedulerOutboxEventAccountChanged, int64(42), nil,
			[]byte(`{"group_ids":[11,12]}`), createdAt, "lease-new", leaseExpiresAt, int64(3), "prior failure",
		))

	events, err := repo.Claim(context.Background(), 2, 45*time.Second)

	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, int64(7), events[0].ID)
	require.Equal(t, service.SchedulerOutboxEventAccountChanged, events[0].EventType)
	require.Equal(t, int64(42), *events[0].AccountID)
	require.Nil(t, events[0].GroupID)
	require.Equal(t, []any{float64(11), float64(12)}, events[0].Payload["group_ids"])
	require.Equal(t, createdAt, events[0].CreatedAt)
	require.Equal(t, "lease-new", events[0].LeaseToken)
	require.Equal(t, leaseExpiresAt, events[0].LeaseExpiresAt)
	require.Equal(t, int64(3), events[0].AttemptCount)
	require.Equal(t, "prior failure", events[0].LastError)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSchedulerOutboxRepositoryClaimBoundsLimitAndLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectQuery("(?s)WITH claimable AS MATERIALIZED.*FOR UPDATE SKIP LOCKED.*dedup_key = NULL").
		WithArgs(schedulerOutboxMaxClaimSize, sqlmock.AnyArg(), int64(schedulerOutboxMaxLease.Milliseconds())).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "event_type", "account_id", "group_id", "payload", "created_at",
			"lease_token", "lease_expires_at", "attempt_count", "last_error",
		}))

	repo := &schedulerOutboxRepository{db: db}
	events, err := repo.Claim(context.Background(), schedulerOutboxMaxClaimSize+1, schedulerOutboxMaxLease+time.Second)

	require.NoError(t, err)
	require.Empty(t, events)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSchedulerOutboxRepositoryAcknowledgeUsesLeaseTokenFencing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	const expectedSQL = `
		DELETE FROM scheduler_outbox
		WHERE id = $1 AND lease_token = $2
	`
	mock.ExpectExec(regexp.QuoteMeta(expectedSQL)).
		WithArgs(int64(9), "current-token").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(expectedSQL)).
		WithArgs(int64(9), "stale-token").
		WillReturnResult(sqlmock.NewResult(0, 0))

	repo := &schedulerOutboxRepository{db: db}
	acknowledged, err := repo.Acknowledge(context.Background(), 9, "current-token")
	require.NoError(t, err)
	require.True(t, acknowledged)

	acknowledged, err = repo.Acknowledge(context.Background(), 9, "stale-token")
	require.NoError(t, err)
	require.False(t, acknowledged)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSchedulerOutboxRepositoryRetryUsesDatabaseTimeBoundsAndLeaseTokenFencing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	const expectedSQL = `
		UPDATE scheduler_outbox
		SET lease_token = NULL,
			lease_expires_at = NULL,
			next_attempt_at = statement_timestamp() + ($3 * INTERVAL '1 millisecond'),
			last_error = LEFT($4, $5)
		WHERE id = $1 AND lease_token = $2
	`
	longError := strings.Repeat("x", schedulerOutboxMaxErrorLength+100)
	mock.ExpectExec(regexp.QuoteMeta(expectedSQL)).
		WithArgs(
			int64(13),
			"current-token",
			int64(schedulerOutboxMaxRetryDelay.Milliseconds()),
			longError,
			schedulerOutboxMaxErrorLength,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta(expectedSQL)).
		WithArgs(int64(13), "stale-token", int64(time.Second.Milliseconds()), "failed again", schedulerOutboxMaxErrorLength).
		WillReturnResult(sqlmock.NewResult(0, 0))

	repo := &schedulerOutboxRepository{db: db}
	retried, err := repo.Retry(context.Background(), 13, "current-token", longError, schedulerOutboxMaxRetryDelay+time.Hour)
	require.NoError(t, err)
	require.True(t, retried)

	retried, err = repo.Retry(context.Background(), 13, "stale-token", "failed again", time.Second)
	require.NoError(t, err)
	require.False(t, retried)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSchedulerOutboxRepositoryPendingStats(t *testing.T) {
	tests := []struct {
		name       string
		count      int64
		oldest     any
		wantOldest time.Time
	}{
		{
			name:       "pending rows",
			count:      5,
			oldest:     time.Date(2026, time.August, 24, 9, 30, 0, 0, time.UTC),
			wantOldest: time.Date(2026, time.August, 24, 9, 30, 0, 0, time.UTC),
		},
		{
			name:   "empty outbox",
			count:  0,
			oldest: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer func() { _ = db.Close() }()

			const expectedSQL = `
		SELECT COUNT(*), MIN(created_at)
		FROM scheduler_outbox
	`
			mock.ExpectQuery(regexp.QuoteMeta(expectedSQL)).
				WillReturnRows(sqlmock.NewRows([]string{"count", "oldest"}).AddRow(tt.count, tt.oldest))

			repo := &schedulerOutboxRepository{db: db}
			stats, err := repo.PendingStats(context.Background())

			require.NoError(t, err)
			require.Equal(t, tt.count, stats.Count)
			require.Equal(t, tt.wantOldest, stats.OldestCreatedAt)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestSchedulerOutboxRepositoryFirstCreatedAtAfter(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &schedulerOutboxRepository{db: db}
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	const expectedSQL = `
		SELECT created_at
		FROM scheduler_outbox
		WHERE id > $1
		ORDER BY id ASC
		LIMIT 1
	`
	mock.ExpectQuery(regexp.QuoteMeta(expectedSQL)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(createdAt))

	got, ok, err := repo.FirstCreatedAtAfter(context.Background(), 42)

	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, createdAt, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSchedulerOutboxRepositoryFirstCreatedAtAfterReturnsNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &schedulerOutboxRepository{db: db}
	const expectedSQL = `
		SELECT created_at
		FROM scheduler_outbox
		WHERE id > $1
		ORDER BY id ASC
		LIMIT 1
	`
	mock.ExpectQuery(regexp.QuoteMeta(expectedSQL)).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}))

	got, ok, err := repo.FirstCreatedAtAfter(context.Background(), 42)

	require.NoError(t, err)
	require.False(t, ok)
	require.True(t, got.IsZero())
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSchedulerOutboxRepositoryDeleteConsumedUpToUsesBoundedCTE(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &schedulerOutboxRepository{db: db}
	const expectedSQL = `
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
	`
	mock.ExpectExec(regexp.QuoteMeta(expectedSQL)).
		WithArgs(int64(42), 5000).
		WillReturnResult(sqlmock.NewResult(0, 17))

	deleted, err := repo.DeleteConsumedUpTo(context.Background(), 42, 5000)

	require.NoError(t, err)
	require.EqualValues(t, 17, deleted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSchedulerOutboxRepositoryDeleteConsumedUpToSkipsNonPositiveWatermark(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &schedulerOutboxRepository{db: db}

	deleted, err := repo.DeleteConsumedUpTo(context.Background(), 0, 5000)

	require.NoError(t, err)
	require.EqualValues(t, 0, deleted)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSchedulerOutboxRepositoryTryAcquireCleanupLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &schedulerOutboxRepository{db: db}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_lock(hashtext('scheduler_outbox_cleanup'))")).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(true))
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_unlock(hashtext('scheduler_outbox_cleanup'))")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	lease, acquired, err := repo.TryAcquireCleanupLock(context.Background())
	require.NoError(t, err)
	require.True(t, acquired)
	require.NotNil(t, lease)

	lease.Release()

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSchedulerOutboxRepositoryTryAcquireCleanupLockUnavailable(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &schedulerOutboxRepository{db: db}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT pg_try_advisory_lock(hashtext('scheduler_outbox_cleanup'))")).
		WillReturnRows(sqlmock.NewRows([]string{"pg_try_advisory_lock"}).AddRow(false))

	lease, acquired, err := repo.TryAcquireCleanupLock(context.Background())
	require.NoError(t, err)
	require.False(t, acquired)
	require.Nil(t, lease)

	require.NoError(t, mock.ExpectationsWereMet())
}

// buildSchedulerGroupPayload 在 groupIDs 为空时必须返回 untyped nil（any），
// 否则 enqueueSchedulerOutbox 的 "payload != nil" 接口判空会被 typed-nil 欺骗，
// 把 payload marshal 成 "null" 写入 dedup_key 哈希，破坏与其他 nil-payload
// 调用的去重一致性。本测试用 ungrouped 账号场景验证两条路径的 dedup_key 一致。
func TestEnqueueSchedulerOutbox_UngroupedAccountDedupesWithLiteralNilPayload(t *testing.T) {
	accountID := int64(42)

	// Path A: 显式 nil payload（如 SetError、SetStatus 等调用模式）
	keyLiteralNil := schedulerOutboxDedupKey("account_changed", &accountID, nil, nil)

	// Path B: buildSchedulerGroupPayload(account.GroupIDs) 当账号没有任何分组
	emptyGroupsPayload := buildSchedulerGroupPayload(nil)
	require.Nil(t, emptyGroupsPayload,
		"buildSchedulerGroupPayload(empty) must return untyped-nil any to avoid typed-nil marshal")

	// 模拟 enqueueSchedulerOutbox 内部的判空逻辑
	var payloadJSON []byte
	if emptyGroupsPayload != nil {
		t.Fatalf("typed-nil regression: buildSchedulerGroupPayload(empty) interface should be nil")
	}
	keyEmptyGroups := schedulerOutboxDedupKey("account_changed", &accountID, nil, payloadJSON)

	require.Equal(t, keyLiteralNil, keyEmptyGroups,
		"ungrouped-account account_changed must share dedup_key with other nil-payload variants")
}
