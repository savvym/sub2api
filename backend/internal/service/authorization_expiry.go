package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
)

const (
	AuthorizationExpirySourceUserRole             = "user_role"
	AuthorizationExpirySourceServicePrincipalRole = "service_principal_role"
	AuthorizationExpirySourceAccountAccessGrant   = "account_access_grant"
	AuthorizationExpirySourceGroupAccessGrant     = "group_access_grant"

	authorizationExpiryBatchSize      = 100
	authorizationExpiryPollInterval   = 500 * time.Millisecond
	authorizationExpiryLease          = 30 * time.Second
	authorizationExpiryCleanupTimeout = 2 * time.Second
)

type AuthorizationExpiryJob struct {
	ID         int64
	SourceType string
	SourceID   int64
	ExpiresAt  time.Time
	Attempts   int
}

type AuthorizationExpiryResult struct {
	Processed     bool
	SourceMissing bool
}

type AuthorizationExpiryStats struct {
	DatabaseTime       time.Time
	CoordinatorReady   bool
	Pending            int64
	Due                int64
	OldestDueExpiresAt *time.Time
	MaxAttempts        int
	LastError          string
}

type AuthorizationExpiryRepository interface {
	Claim(ctx context.Context, workerID string, limit int, lease time.Duration) ([]AuthorizationExpiryJob, error)
	ProcessClaimed(ctx context.Context, job AuthorizationExpiryJob, workerID string) (AuthorizationExpiryResult, error)
	RetryClaimed(ctx context.Context, id int64, workerID string, delay time.Duration, lastError string) error
	ReleaseClaims(ctx context.Context, workerID string, jobIDs []int64) error
	Stats(ctx context.Context) (AuthorizationExpiryStats, error)
}

type AuthorizationExpiryHealth struct {
	Running          bool          `json:"running"`
	CoordinatorReady bool          `json:"coordinator_ready"`
	Processed        uint64        `json:"processed"`
	Failures         uint64        `json:"failures"`
	Pending          int64         `json:"pending"`
	Due              int64         `json:"due"`
	OldestDueLag     time.Duration `json:"oldest_due_lag"`
	MaxAttempts      int           `json:"max_attempts"`
	LastError        string        `json:"last_error,omitempty"`
	StatsError       string        `json:"stats_error,omitempty"`
	DatabaseTime     time.Time     `json:"database_time,omitempty"`
}

type AuthorizationExpiryWorker struct {
	repo      AuthorizationExpiryRepository
	workerID  string
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	start     sync.Once
	stop      sync.Once
	running   atomic.Bool
	processed atomic.Uint64
	failures  atomic.Uint64
	lastError atomic.Value
}

func NewAuthorizationExpiryWorker(repo AuthorizationExpiryRepository) *AuthorizationExpiryWorker {
	ctx, cancel := context.WithCancel(context.Background())
	worker := &AuthorizationExpiryWorker{
		repo: repo, workerID: uuid.NewString(), ctx: ctx, cancel: cancel,
	}
	worker.lastError.Store("")
	return worker
}

func ProvideAuthorizationExpiryWorker(repo AuthorizationExpiryRepository) *AuthorizationExpiryWorker {
	worker := NewAuthorizationExpiryWorker(repo)
	worker.Start()
	return worker
}

func (w *AuthorizationExpiryWorker) Start() {
	if w == nil || w.repo == nil {
		return
	}
	w.start.Do(func() {
		w.running.Store(true)
		w.wg.Add(1)
		go w.run()
	})
}

func (w *AuthorizationExpiryWorker) Stop() {
	if w == nil {
		return
	}
	w.stop.Do(func() {
		w.cancel()
		w.wg.Wait()
		w.running.Store(false)
	})
}

func (w *AuthorizationExpiryWorker) run() {
	defer w.wg.Done()
	ticker := time.NewTicker(authorizationExpiryPollInterval)
	defer ticker.Stop()
	for {
		w.poll()
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (w *AuthorizationExpiryWorker) poll() {
	jobs, err := w.repo.Claim(w.ctx, w.workerID, authorizationExpiryBatchSize, authorizationExpiryLease)
	if err != nil {
		if w.ctx.Err() == nil {
			w.recordFailure(fmt.Errorf("claim authorization expiry jobs: %w", err))
		}
		return
	}
	settled := make([]bool, len(jobs))
	defer func() {
		jobIDs := make([]int64, 0, len(jobs))
		for i := range jobs {
			if !settled[i] {
				jobIDs = append(jobIDs, jobs[i].ID)
			}
		}
		w.releaseClaims(jobIDs)
	}()

	for i, job := range jobs {
		if w.ctx.Err() != nil {
			return
		}
		result, processErr := w.repo.ProcessClaimed(w.ctx, job, w.workerID)
		if processErr == nil {
			settled[i] = true
			w.lastError.Store("")
			if result.Processed {
				w.processed.Add(1)
			}
			continue
		}
		if w.ctx.Err() != nil {
			return
		}
		w.recordFailure(processErr)
		delay := authorizationExpiryRetryDelay(job.Attempts + 1)
		retryCtx, retryCancel := authorizationExpiryDetachedContext()
		retryErr := w.repo.RetryClaimed(
			retryCtx,
			job.ID,
			w.workerID,
			delay,
			boundedAuthorizationExpiryError(processErr),
		)
		retryCancel()
		if retryErr != nil {
			w.recordFailure(fmt.Errorf("retry authorization expiry job %d: %w", job.ID, retryErr))
			return
		}
		settled[i] = true
	}
}

func (w *AuthorizationExpiryWorker) releaseClaims(jobIDs []int64) {
	if w == nil || w.repo == nil || len(jobIDs) == 0 {
		return
	}
	ctx, cancel := authorizationExpiryDetachedContext()
	defer cancel()
	if err := w.repo.ReleaseClaims(ctx, w.workerID, jobIDs); err != nil {
		w.recordFailure(fmt.Errorf("release authorization expiry claims: %w", err))
	}
}

func authorizationExpiryDetachedContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), authorizationExpiryCleanupTimeout)
}

func authorizationExpiryRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 8 {
		attempt = 8
	}
	delay := 250 * time.Millisecond * time.Duration(1<<(attempt-1))
	if delay > 30*time.Second {
		return 30 * time.Second
	}
	return delay
}

func (w *AuthorizationExpiryWorker) recordFailure(err error) {
	if w == nil || err == nil {
		return
	}
	w.failures.Add(1)
	message := boundedAuthorizationExpiryError(err)
	w.lastError.Store(message)
	slog.Error("authorization expiry coordinator failed", "error", message)
}

func boundedAuthorizationExpiryError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 1024 {
		message = message[:1024]
	}
	return message
}

func (w *AuthorizationExpiryWorker) Health(ctx context.Context) AuthorizationExpiryHealth {
	health := AuthorizationExpiryHealth{}
	if w == nil {
		return health
	}
	health.Running = w.running.Load()
	health.Processed = w.processed.Load()
	health.Failures = w.failures.Load()
	if value := w.lastError.Load(); value != nil {
		health.LastError, _ = value.(string)
	}
	if w.repo == nil {
		return health
	}
	stats, err := w.repo.Stats(ctx)
	if err != nil {
		health.StatsError = boundedAuthorizationExpiryError(err)
		return health
	}
	health.DatabaseTime = stats.DatabaseTime
	health.CoordinatorReady = stats.CoordinatorReady
	health.Pending = stats.Pending
	health.Due = stats.Due
	health.MaxAttempts = stats.MaxAttempts
	if health.LastError == "" {
		health.LastError = stats.LastError
	}
	if stats.OldestDueExpiresAt != nil {
		health.OldestDueLag = stats.DatabaseTime.Sub(*stats.OldestDueExpiresAt)
		if health.OldestDueLag < 0 {
			health.OldestDueLag = 0
		}
	}
	return health
}
