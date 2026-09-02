package service

import (
	"context"
	"time"
)

type SchedulerOutboxEvent struct {
	ID                 int64
	EventType          string
	AccountID          *int64
	GroupID            *int64
	Payload            map[string]any
	PayloadDecodeError string
	CreatedAt          time.Time
	LeaseToken         string
	LeaseExpiresAt     time.Time
	AttemptCount       int64
	LastError          string
}

type SchedulerOutboxPendingStats struct {
	Count           int64
	OldestCreatedAt time.Time
}

type SchedulerOutboxWorkerHealth struct {
	Running                bool
	Healthy                bool
	LastPollAt             time.Time
	LastSuccessAt          time.Time
	LastFailureAt          time.Time
	LastError              string
	PendingCount           int64
	OldestPendingCreatedAt time.Time
	OldestPendingAge       time.Duration
}

// SchedulerOutboxRepository 提供调度 outbox 的读取接口。
type SchedulerOutboxRepository interface {
	Claim(ctx context.Context, limit int, leaseDuration time.Duration) ([]SchedulerOutboxEvent, error)
	Acknowledge(ctx context.Context, eventID int64, leaseToken string) (bool, error)
	Retry(ctx context.Context, eventID int64, leaseToken, lastError string, retryDelay time.Duration) (bool, error)
	PendingStats(ctx context.Context) (SchedulerOutboxPendingStats, error)
	// Deprecated compatibility surface. Production workers use Claim/Acknowledge/Retry.
	ListAfterAndReleaseDedup(ctx context.Context, afterID int64, limit int) ([]SchedulerOutboxEvent, error)
	// FirstCreatedAtAfter 返回指定水位之后第一条待消费事件的创建时间，不领取事件或修改去重键。
	FirstCreatedAtAfter(ctx context.Context, afterID int64) (time.Time, bool, error)
	MaxID(ctx context.Context) (int64, error)
	DeleteConsumedUpTo(ctx context.Context, watermark int64, limit int) (int64, error)
	TryAcquireCleanupLock(ctx context.Context) (SchedulerOutboxCleanupLease, bool, error)
}

type SchedulerOutboxWorkerHealthProvider interface {
	OutboxWorkerHealth() SchedulerOutboxWorkerHealth
}

// SchedulerOutboxCleanupLease holds the PostgreSQL advisory lock used by
// scheduler outbox cleanup.
type SchedulerOutboxCleanupLease interface {
	Release()
}
