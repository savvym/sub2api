package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

type outboxCleanupCache struct {
	watermark          int64
	watermarkReads     int
	setWatermarks      []int64
	updateErr          error
	lastUsedUpdates    []map[int64]time.Time
	bucketLockBusyOnce bool
	bucketLockAttempts int
	listBucketErr      error
	listBuckets        []SchedulerBucket
	listBucketCalls    int
}

func (c *outboxCleanupCache) GetSnapshot(ctx context.Context, bucket SchedulerBucket) ([]*Account, bool, error) {
	return nil, false, nil
}

func (c *outboxCleanupCache) CaptureBucketWriteToken(ctx context.Context, bucket SchedulerBucket) (SchedulerBucketWriteToken, error) {
	return SchedulerBucketWriteToken{Bucket: bucket, Epoch: 1}, nil
}

func (c *outboxCleanupCache) SetSnapshot(ctx context.Context, bucket SchedulerBucket, token SchedulerBucketWriteToken, accounts []Account) error {
	return nil
}

func (c *outboxCleanupCache) RetireBucket(ctx context.Context, bucket SchedulerBucket) error {
	return nil
}

func (c *outboxCleanupCache) ReopenBucket(ctx context.Context, bucket SchedulerBucket) (SchedulerBucketWriteToken, error) {
	return SchedulerBucketWriteToken{Bucket: bucket, Epoch: 1}, nil
}

func (c *outboxCleanupCache) TryAcquireGroupLifecycleLease(context.Context, int64, time.Duration) (SchedulerGroupLifecycleLease, bool, error) {
	return SchedulerGroupLifecycleLease{}, false, nil
}

func (c *outboxCleanupCache) ReleaseGroupLifecycleLease(context.Context, SchedulerGroupLifecycleLease) error {
	return nil
}

func (c *outboxCleanupCache) GetAccount(ctx context.Context, accountID int64) (*Account, error) {
	return nil, nil
}

func (c *outboxCleanupCache) SetAccount(ctx context.Context, account *Account) error {
	return nil
}

func (c *outboxCleanupCache) DeleteAccount(ctx context.Context, accountID int64) error {
	return nil
}

func (c *outboxCleanupCache) UpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	if c.updateErr == nil {
		cloned := make(map[int64]time.Time, len(updates))
		for id, value := range updates {
			cloned[id] = value
		}
		c.lastUsedUpdates = append(c.lastUsedUpdates, cloned)
	}
	return c.updateErr
}

func (c *outboxCleanupCache) TryLockBucket(ctx context.Context, bucket SchedulerBucket, ttl time.Duration) (bool, error) {
	c.bucketLockAttempts++
	if c.bucketLockBusyOnce {
		c.bucketLockBusyOnce = false
		return false, nil
	}
	return true, nil
}

func (c *outboxCleanupCache) UnlockBucket(ctx context.Context, bucket SchedulerBucket) error {
	return nil
}

func (c *outboxCleanupCache) ListBuckets(ctx context.Context) ([]SchedulerBucket, error) {
	c.listBucketCalls++
	return c.listBuckets, c.listBucketErr
}

func (c *outboxCleanupCache) GetOutboxWatermark(ctx context.Context) (int64, error) {
	c.watermarkReads++
	return c.watermark, nil
}

func (c *outboxCleanupCache) SetOutboxWatermark(ctx context.Context, id int64) error {
	c.watermark = id
	c.setWatermarks = append(c.setWatermarks, id)
	return nil
}

type outboxCleanupDeleteCall struct {
	watermark int64
	limit     int
}

type outboxCleanupRepo struct {
	events              []SchedulerOutboxEvent
	rows                []int64
	claimed             map[int64]string
	retryAt             map[int64]time.Time
	leaseSequence       int64
	acknowledged        []int64
	retried             []int64
	retryErrors         []string
	claimErr            error
	ackErr              error
	ackMiss             bool
	retryErr            error
	retryMiss           bool
	pendingStatsErr     error
	pendingStatsCalls   int
	pendingStats        *SchedulerOutboxPendingStats
	claimCalls          int
	claimLimits         []int
	claimLeases         []time.Duration
	retryDelays         []time.Duration
	maxIDCalls          int
	maxIDErr            error
	lockAcquired        bool
	lockAttempts        int
	releaseCount        int
	deleteCalls         []outboxCleanupDeleteCall
	firstCreatedAfterID []int64
}

type outboxCleanupAccountRepo struct {
	AccountRepository
}

func (r *outboxCleanupAccountRepo) ListSchedulableUngroupedByPlatform(context.Context, string) ([]Account, error) {
	return nil, nil
}

type outboxLockRetryAccountRepo struct {
	AccountRepository
	account *Account
}

func (r *outboxLockRetryAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	return r.account, nil
}

func (r *outboxLockRetryAccountRepo) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]Account, error) {
	return nil, nil
}

type blockingOutboxCleanupCache struct {
	*outboxCleanupCache
	mu      sync.Mutex
	calls   int
	started chan struct{}
	release chan struct{}
}

func (c *blockingOutboxCleanupCache) ListBuckets(context.Context) ([]SchedulerBucket, error) {
	c.mu.Lock()
	c.calls++
	call := c.calls
	c.mu.Unlock()
	if call == 1 {
		close(c.started)
		<-c.release
	}
	return c.listBuckets, c.listBucketErr
}

func (c *blockingOutboxCleanupCache) listCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (r *outboxCleanupRepo) ListAfterAndReleaseDedup(ctx context.Context, afterID int64, limit int) ([]SchedulerOutboxEvent, error) {
	events := make([]SchedulerOutboxEvent, 0, len(r.events))
	for _, event := range r.events {
		if event.ID <= afterID {
			continue
		}
		events = append(events, event)
		if limit > 0 && len(events) >= limit {
			break
		}
	}
	return events, nil
}

func (r *outboxCleanupRepo) Claim(_ context.Context, limit int, leaseDuration time.Duration) ([]SchedulerOutboxEvent, error) {
	r.claimCalls++
	r.claimLimits = append(r.claimLimits, limit)
	r.claimLeases = append(r.claimLeases, leaseDuration)
	if r.claimErr != nil {
		return nil, r.claimErr
	}
	if limit <= 0 {
		limit = 100
	}
	if r.claimed == nil {
		r.claimed = make(map[int64]string)
	}
	if r.retryAt == nil {
		r.retryAt = make(map[int64]time.Time)
	}
	now := time.Now()
	claimed := make([]SchedulerOutboxEvent, 0, limit)
	for index := range r.events {
		event := &r.events[index]
		if r.claimed[event.ID] != "" || now.Before(r.retryAt[event.ID]) {
			continue
		}
		r.leaseSequence++
		event.LeaseToken = fmt.Sprintf("lease-%d", r.leaseSequence)
		event.LeaseExpiresAt = now.Add(leaseDuration)
		event.AttemptCount++
		r.claimed[event.ID] = event.LeaseToken
		claimed = append(claimed, *event)
		if len(claimed) >= limit {
			break
		}
	}
	return claimed, nil
}

func (r *outboxCleanupRepo) Acknowledge(_ context.Context, eventID int64, leaseToken string) (bool, error) {
	if r.ackErr != nil {
		return false, r.ackErr
	}
	if r.ackMiss {
		return false, nil
	}
	if r.claimed[eventID] != leaseToken || leaseToken == "" {
		return false, nil
	}
	delete(r.claimed, eventID)
	r.acknowledged = append(r.acknowledged, eventID)
	kept := r.events[:0]
	for _, event := range r.events {
		if event.ID != eventID {
			kept = append(kept, event)
		}
	}
	r.events = kept
	return true, nil
}

func (r *outboxCleanupRepo) Retry(_ context.Context, eventID int64, leaseToken, lastError string, retryDelay time.Duration) (bool, error) {
	if r.retryErr != nil {
		return false, r.retryErr
	}
	if r.retryMiss {
		return false, nil
	}
	if r.claimed[eventID] != leaseToken || leaseToken == "" {
		return false, nil
	}
	delete(r.claimed, eventID)
	if r.retryAt == nil {
		r.retryAt = make(map[int64]time.Time)
	}
	r.retryAt[eventID] = time.Now().Add(retryDelay)
	r.retried = append(r.retried, eventID)
	r.retryErrors = append(r.retryErrors, lastError)
	r.retryDelays = append(r.retryDelays, retryDelay)
	return true, nil
}

func (r *outboxCleanupRepo) PendingStats(context.Context) (SchedulerOutboxPendingStats, error) {
	r.pendingStatsCalls++
	if r.pendingStatsErr != nil {
		return SchedulerOutboxPendingStats{}, r.pendingStatsErr
	}
	if r.pendingStats != nil {
		return *r.pendingStats, nil
	}
	stats := SchedulerOutboxPendingStats{Count: int64(len(r.events))}
	for _, event := range r.events {
		if stats.OldestCreatedAt.IsZero() || event.CreatedAt.Before(stats.OldestCreatedAt) {
			stats.OldestCreatedAt = event.CreatedAt
		}
	}
	return stats, nil
}

func (r *outboxCleanupRepo) FirstCreatedAtAfter(ctx context.Context, afterID int64) (time.Time, bool, error) {
	r.firstCreatedAfterID = append(r.firstCreatedAfterID, afterID)
	for _, event := range r.events {
		if event.ID > afterID {
			return event.CreatedAt, true, nil
		}
	}
	return time.Time{}, false, nil
}

func (r *outboxCleanupRepo) MaxID(ctx context.Context) (int64, error) {
	r.maxIDCalls++
	if r.maxIDErr != nil {
		return 0, r.maxIDErr
	}
	var maxID int64
	for _, id := range r.rows {
		if id > maxID {
			maxID = id
		}
	}
	return maxID, nil
}

func (r *outboxCleanupRepo) DeleteConsumedUpTo(ctx context.Context, watermark int64, limit int) (int64, error) {
	r.deleteCalls = append(r.deleteCalls, outboxCleanupDeleteCall{
		watermark: watermark,
		limit:     limit,
	})
	if watermark <= 0 || limit <= 0 {
		return 0, nil
	}

	deleted := int64(0)
	kept := make([]int64, 0, len(r.rows))
	for _, id := range r.rows {
		if id <= watermark && deleted < int64(limit) {
			deleted++
			continue
		}
		kept = append(kept, id)
	}
	r.rows = kept
	return deleted, nil
}

func (r *outboxCleanupRepo) TryAcquireCleanupLock(ctx context.Context) (SchedulerOutboxCleanupLease, bool, error) {
	r.lockAttempts++
	if !r.lockAcquired {
		return nil, false, nil
	}
	return outboxCleanupLease{release: func() {
		r.releaseCount++
	}}, true, nil
}

type outboxCleanupLease struct {
	release func()
}

func (l outboxCleanupLease) Release() {
	if l.release != nil {
		l.release()
	}
}

func TestSchedulerSnapshotServicePollOutboxAcknowledgesClaimedRowsWithoutWatermark(t *testing.T) {
	cache := &outboxCleanupCache{}
	repo := &outboxCleanupRepo{
		events: []SchedulerOutboxEvent{
			{ID: 10000, EventType: SchedulerOutboxEventAccountLastUsed},
		},
		rows:         int64Range(1, 10003),
		lockAcquired: true,
	}
	svc := NewSchedulerSnapshotService(cache, repo, nil, nil, nil)

	svc.pollOutbox()

	if !reflect.DeepEqual(repo.acknowledged, []int64{10000}) {
		t.Fatalf("expected claimed event to be acknowledged, got %#v", repo.acknowledged)
	}
	if len(repo.events) != 0 {
		t.Fatalf("expected acknowledged event to be deleted, got %#v", repo.events)
	}
	if cache.watermarkReads != 0 || cache.watermark != 0 || len(cache.setWatermarks) != 0 {
		t.Fatalf("production poll must not use the Redis watermark, got reads=%d value=%d writes=%#v", cache.watermarkReads, cache.watermark, cache.setWatermarks)
	}
	if !reflect.DeepEqual(repo.rows, int64Range(1, 10003)) {
		t.Fatal("legacy cleanup rows must remain untouched")
	}
	if repo.lockAttempts != 0 || repo.releaseCount != 0 || len(repo.deleteCalls) != 0 {
		t.Fatalf("production poll must not invoke legacy cleanup: lock=%d release=%d deletes=%#v", repo.lockAttempts, repo.releaseCount, repo.deleteCalls)
	}
	if repo.pendingStatsCalls != 1 {
		t.Fatalf("expected one authoritative pending-stats read, got %d", repo.pendingStatsCalls)
	}
}

func TestSchedulerSnapshotServicePollOutboxIgnoresLegacyCleanupLock(t *testing.T) {
	cache := &outboxCleanupCache{}
	repo := &outboxCleanupRepo{
		events: []SchedulerOutboxEvent{
			{ID: 3, EventType: SchedulerOutboxEventAccountLastUsed},
		},
		rows:         []int64{1, 2, 3, 4},
		lockAcquired: false,
	}
	svc := NewSchedulerSnapshotService(cache, repo, nil, nil, nil)

	svc.pollOutbox()

	if !reflect.DeepEqual(repo.acknowledged, []int64{3}) {
		t.Fatalf("expected claim/ack consumption, got %#v", repo.acknowledged)
	}
	if cache.watermarkReads != 0 || cache.watermark != 0 || len(cache.setWatermarks) != 0 {
		t.Fatalf("production poll must not use the Redis watermark, got reads=%d value=%d writes=%#v", cache.watermarkReads, cache.watermark, cache.setWatermarks)
	}
	if !reflect.DeepEqual(repo.rows, []int64{1, 2, 3, 4}) {
		t.Fatalf("expected legacy cleanup rows to remain untouched, got %#v", repo.rows)
	}
	if repo.lockAttempts != 0 {
		t.Fatalf("expected no legacy cleanup lock attempt, got %d", repo.lockAttempts)
	}
	if len(repo.deleteCalls) != 0 {
		t.Fatalf("expected no delete calls, got %#v", repo.deleteCalls)
	}
	if repo.releaseCount != 0 {
		t.Fatalf("expected no release without lock, got %d", repo.releaseCount)
	}
}

func TestSchedulerSnapshotServicePollOutboxRetriesOnHandleFailure(t *testing.T) {
	cache := &outboxCleanupCache{
		updateErr: errors.New("cache update failed"),
	}
	repo := &outboxCleanupRepo{
		events: []SchedulerOutboxEvent{
			{
				ID:        5,
				EventType: SchedulerOutboxEventAccountLastUsed,
				Payload: map[string]any{
					"last_used": map[string]any{"101": float64(123)},
				},
			},
		},
		rows:         []int64{1, 2, 3, 4, 5, 6},
		lockAcquired: true,
	}
	svc := NewSchedulerSnapshotService(cache, repo, nil, nil, nil)

	svc.pollOutbox()

	if !reflect.DeepEqual(repo.retried, []int64{5}) {
		t.Fatalf("expected failed event to be released for retry, got %#v", repo.retried)
	}
	if len(repo.acknowledged) != 0 {
		t.Fatalf("failed event must not be acknowledged, got %#v", repo.acknowledged)
	}
	if !reflect.DeepEqual(repo.retryDelays, []time.Duration{time.Second}) {
		t.Fatalf("unexpected retry delay: %#v", repo.retryDelays)
	}
	if len(repo.retryErrors) != 1 || repo.retryErrors[0] != "cache update failed" {
		t.Fatalf("unexpected retry error: %#v", repo.retryErrors)
	}
	if len(cache.setWatermarks) != 0 {
		t.Fatalf("expected no watermark write on handle failure, got %#v", cache.setWatermarks)
	}
	if repo.lockAttempts != 0 {
		t.Fatalf("expected cleanup lock not to be attempted, got %d", repo.lockAttempts)
	}
	if len(repo.deleteCalls) != 0 {
		t.Fatalf("expected no delete calls, got %#v", repo.deleteCalls)
	}
	if !reflect.DeepEqual(repo.rows, []int64{1, 2, 3, 4, 5, 6}) {
		t.Fatalf("expected rows unchanged, got %#v", repo.rows)
	}
}

func TestSchedulerSnapshotServicePollOutboxRetriesBucketLockBusyThenAcknowledges(t *testing.T) {
	accountID := int64(101)
	cache := &outboxCleanupCache{bucketLockBusyOnce: true}
	repo := &outboxCleanupRepo{events: []SchedulerOutboxEvent{{
		ID:        6,
		EventType: SchedulerOutboxEventAccountChanged,
		AccountID: &accountID,
	}}}
	accounts := &outboxLockRetryAccountRepo{account: &Account{
		ID:       accountID,
		Platform: PlatformOpenAI,
		GroupIDs: []int64{42},
	}}
	svc := NewSchedulerSnapshotService(cache, repo, accounts, nil, nil)

	svc.pollOutbox()

	if !reflect.DeepEqual(repo.retried, []int64{6}) || len(repo.acknowledged) != 0 {
		t.Fatalf("bucket lock contention must retry the durable event, got retries=%#v acks=%#v", repo.retried, repo.acknowledged)
	}
	if len(repo.retryErrors) != 1 || !strings.Contains(repo.retryErrors[0], ErrSchedulerBucketRebuildBusy.Error()) {
		t.Fatalf("unexpected retry error: %#v", repo.retryErrors)
	}

	repo.retryAt[6] = time.Now().Add(-time.Second)
	svc.pollOutbox()

	if !reflect.DeepEqual(repo.acknowledged, []int64{6}) || len(repo.events) != 0 {
		t.Fatalf("recovered rebuild must acknowledge the event, got acks=%#v events=%#v", repo.acknowledged, repo.events)
	}
	if cache.bucketLockAttempts != 4 {
		t.Fatalf("expected both OpenAI buckets to be attempted on both deliveries, got %d", cache.bucketLockAttempts)
	}
}

func TestSchedulerSnapshotServicePollOutboxDoesNotUseConsumedEventForLag(t *testing.T) {
	cache := &outboxCleanupCache{}
	repo := &outboxCleanupRepo{
		events: []SchedulerOutboxEvent{
			{
				ID:        7,
				EventType: SchedulerOutboxEventAccountLastUsed,
				CreatedAt: time.Now().Add(-time.Hour),
			},
		},
	}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{
				OutboxLagWarnSeconds:     1,
				OutboxLagRebuildSeconds:  1,
				OutboxLagRebuildFailures: 1,
			},
		},
	}
	svc := NewSchedulerSnapshotService(cache, repo, nil, nil, cfg)

	svc.pollOutbox()

	if !reflect.DeepEqual(repo.acknowledged, []int64{7}) {
		t.Fatalf("expected old event to be acknowledged, got %#v", repo.acknowledged)
	}
	if repo.pendingStatsCalls != 1 {
		t.Fatalf("expected lag to use pending rows after ack, got %d stats reads", repo.pendingStatsCalls)
	}
	if len(repo.firstCreatedAfterID) != 0 || repo.maxIDCalls != 0 {
		t.Fatalf("legacy watermark lag queries must not run: first=%#v max=%d", repo.firstCreatedAfterID, repo.maxIDCalls)
	}
	if cache.listBucketCalls != 0 {
		t.Fatalf("expected consumed event not to trigger full rebuild, got %d attempts", cache.listBucketCalls)
	}
	if svc.lagFailures != 0 {
		t.Fatalf("expected lag failures to remain reset, got %d", svc.lagFailures)
	}
}

func TestSchedulerSnapshotServicePollOutboxRetriesPayloadDecodeFailure(t *testing.T) {
	cache := &outboxCleanupCache{}
	repo := &outboxCleanupRepo{events: []SchedulerOutboxEvent{{
		ID:                 21,
		EventType:          SchedulerOutboxEventAccountLastUsed,
		PayloadDecodeError: "invalid character",
	}}}
	svc := NewSchedulerSnapshotService(cache, repo, nil, nil, nil)

	svc.pollOutbox()

	if !reflect.DeepEqual(repo.retried, []int64{21}) || len(repo.acknowledged) != 0 {
		t.Fatalf("decode failure must be retried, got retries=%#v acks=%#v", repo.retried, repo.acknowledged)
	}
	if len(repo.retryErrors) != 1 || !strings.Contains(repo.retryErrors[0], "decode scheduler outbox payload") {
		t.Fatalf("unexpected retry error: %#v", repo.retryErrors)
	}
}

func TestSchedulerSnapshotServicePollOutboxReportsLostAckLease(t *testing.T) {
	cache := &outboxCleanupCache{}
	repo := &outboxCleanupRepo{
		events:  []SchedulerOutboxEvent{{ID: 22, EventType: "unknown_event"}},
		ackMiss: true,
	}
	svc := NewSchedulerSnapshotService(cache, repo, nil, nil, nil)

	svc.pollOutbox()

	health := svc.OutboxWorkerHealth()
	if health.Healthy || !strings.Contains(health.LastError, "ack lease lost") {
		t.Fatalf("lost ack lease must degrade worker health, got %#v", health)
	}
	if health.PendingCount != 1 || len(repo.acknowledged) != 0 {
		t.Fatalf("lost ack lease must leave the event replayable, health=%#v acks=%#v", health, repo.acknowledged)
	}
}

func TestSchedulerSnapshotServicePollOutboxReportsLostRetryLease(t *testing.T) {
	cache := &outboxCleanupCache{updateErr: errors.New("cache update failed")}
	repo := &outboxCleanupRepo{
		events: []SchedulerOutboxEvent{{
			ID:        23,
			EventType: SchedulerOutboxEventAccountLastUsed,
			Payload: map[string]any{
				"last_used": map[string]any{"101": float64(123)},
			},
		}},
		retryMiss: true,
	}
	svc := NewSchedulerSnapshotService(cache, repo, nil, nil, nil)

	svc.pollOutbox()

	health := svc.OutboxWorkerHealth()
	if health.Healthy || !strings.Contains(health.LastError, "cache update failed") ||
		!strings.Contains(health.LastError, "retry lease lost") {
		t.Fatalf("lost retry lease must retain failed health, got %#v", health)
	}
	if len(repo.retried) != 0 || len(repo.acknowledged) != 0 || health.PendingCount != 1 {
		t.Fatalf("stale worker must not finish the event: retries=%#v acks=%#v health=%#v", repo.retried, repo.acknowledged, health)
	}
}

func TestSchedulerSnapshotServicePollOutboxRecordsRepositoryFailures(t *testing.T) {
	t.Run("claim", func(t *testing.T) {
		repo := &outboxCleanupRepo{claimErr: errors.New("claim unavailable")}
		svc := NewSchedulerSnapshotService(&outboxCleanupCache{}, repo, nil, nil, nil)
		svc.recordOutboxPollSuccess(SchedulerOutboxPendingStats{Count: 9})

		svc.pollOutbox()

		health := svc.OutboxWorkerHealth()
		if health.Healthy || health.LastError != "claim unavailable" || health.LastFailureAt.IsZero() || health.PendingCount != 9 {
			t.Fatalf("unexpected claim failure health: %#v", health)
		}
		if repo.pendingStatsCalls != 0 {
			t.Fatalf("claim failure must stop the poll, got %d pending reads", repo.pendingStatsCalls)
		}
	})

	t.Run("pending stats", func(t *testing.T) {
		repo := &outboxCleanupRepo{
			events:          []SchedulerOutboxEvent{{ID: 24, EventType: "unknown_event"}},
			pendingStatsErr: errors.New("stats unavailable"),
		}
		svc := NewSchedulerSnapshotService(&outboxCleanupCache{}, repo, nil, nil, nil)

		svc.pollOutbox()

		health := svc.OutboxWorkerHealth()
		if health.Healthy || health.LastError != "stats unavailable" {
			t.Fatalf("unexpected pending-stats failure health: %#v", health)
		}
		if !reflect.DeepEqual(repo.acknowledged, []int64{24}) {
			t.Fatalf("event handling should complete before stats failure, got %#v", repo.acknowledged)
		}
	})
}

func TestSchedulerSnapshotServicePollOutboxDuplicateReplayIsIdempotent(t *testing.T) {
	payload := map[string]any{"last_used": map[string]any{"101": float64(123)}}
	cache := &outboxCleanupCache{}
	repo := &outboxCleanupRepo{events: []SchedulerOutboxEvent{
		{ID: 25, EventType: SchedulerOutboxEventAccountLastUsed, Payload: payload},
		{ID: 26, EventType: SchedulerOutboxEventAccountLastUsed, Payload: payload},
	}}
	svc := NewSchedulerSnapshotService(cache, repo, nil, nil, nil)

	svc.pollOutbox()

	if !reflect.DeepEqual(repo.acknowledged, []int64{25, 26}) {
		t.Fatalf("expected both deliveries to be acknowledged, got %#v", repo.acknowledged)
	}
	if len(cache.lastUsedUpdates) != 2 || !reflect.DeepEqual(cache.lastUsedUpdates[0], cache.lastUsedUpdates[1]) {
		t.Fatalf("duplicate delivery must converge to the same cache state, got %#v", cache.lastUsedUpdates)
	}
	if repo.claimCalls != 3 {
		t.Fatalf("expected one-event claims followed by an empty claim, got %d", repo.claimCalls)
	}
	for index, limit := range repo.claimLimits {
		if limit != 1 || repo.claimLeases[index] != schedulerOutboxLeaseDuration {
			t.Fatalf("unexpected claim %d: limit=%d lease=%s", index, limit, repo.claimLeases[index])
		}
	}
}

func TestSchedulerOutboxRetryDelayAndFailureAreBounded(t *testing.T) {
	tests := []struct {
		attempt int64
		want    time.Duration
	}{
		{attempt: -1, want: time.Second},
		{attempt: 1, want: time.Second},
		{attempt: 2, want: 2 * time.Second},
		{attempt: 3, want: 4 * time.Second},
		{attempt: 7, want: time.Minute},
		{attempt: 100, want: time.Minute},
	}
	for _, tt := range tests {
		if got := schedulerOutboxRetryDelay(tt.attempt); got != tt.want {
			t.Fatalf("attempt %d: expected %s, got %s", tt.attempt, tt.want, got)
		}
	}

	bounded := boundedSchedulerOutboxFailure(errors.New(strings.Repeat("界", schedulerOutboxFailureMaxRunes+10)))
	if got := len([]rune(bounded)); got != schedulerOutboxFailureMaxRunes {
		t.Fatalf("expected %d error runes, got %d", schedulerOutboxFailureMaxRunes, got)
	}
}

func TestSchedulerSnapshotServiceOutboxWorkerStateTracksLifecycle(t *testing.T) {
	svc := NewSchedulerSnapshotService(&outboxCleanupCache{}, &outboxCleanupRepo{}, nil, nil, nil)
	done := make(chan struct{})
	go func() {
		svc.runOutboxWorker(time.Hour)
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for {
		state := svc.AuthorizationPropagationWorkerState()
		health := svc.OutboxWorkerHealth()
		if state.Name == "scheduler_outbox" && state.Running && health.Healthy && !health.LastSuccessAt.IsZero() {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker did not become healthy: state=%#v health=%#v", state, health)
		}
		time.Sleep(time.Millisecond)
	}

	close(svc.stopCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not stop")
	}
	state := svc.AuthorizationPropagationWorkerState()
	health := svc.OutboxWorkerHealth()
	if state.Name != "scheduler_outbox" || state.Running || health.Running || health.Healthy {
		t.Fatalf("unexpected stopped worker state: state=%#v health=%#v", state, health)
	}
}

func TestSchedulerSnapshotServiceCheckOutboxLagLatchesPersistentDegradation(t *testing.T) {
	tests := []struct {
		name             string
		createdAt        time.Time
		pendingCount     int64
		lagSeconds       int
		backlogThreshold int
	}{
		{
			name:         "lag",
			createdAt:    time.Now().Add(-time.Hour),
			pendingCount: 1,
			lagSeconds:   1,
		},
		{
			name:             "backlog",
			createdAt:        time.Now(),
			pendingCount:     100,
			backlogThreshold: 50,
		},
		{
			name:             "lag_and_backlog",
			createdAt:        time.Now().Add(-time.Hour),
			pendingCount:     100,
			lagSeconds:       1,
			backlogThreshold: 50,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cache := &outboxCleanupCache{listBuckets: []SchedulerBucket{{Platform: PlatformOpenAI, Mode: SchedulerModeSingle}}}
			repo := &outboxCleanupRepo{
				pendingStats: &SchedulerOutboxPendingStats{
					Count:           tt.pendingCount,
					OldestCreatedAt: tt.createdAt,
				},
			}
			cfg := &config.Config{
				Gateway: config.GatewayConfig{
					Scheduling: config.GatewaySchedulingConfig{
						OutboxLagRebuildSeconds:  tt.lagSeconds,
						OutboxLagRebuildFailures: 1,
						OutboxBacklogRebuildRows: tt.backlogThreshold,
					},
				},
			}
			svc := NewSchedulerSnapshotService(cache, repo, &outboxCleanupAccountRepo{}, nil, cfg)

			for range 3 {
				svc.checkOutboxLag(context.Background(), 0)
			}

			if cache.listBucketCalls != 1 {
				t.Fatalf("expected one rebuild attempt during a persistent degraded episode, got %d", cache.listBucketCalls)
			}
		})
	}
}

func TestSchedulerSnapshotServiceCheckOutboxLagFailedRebuildRearmsAfterRecovery(t *testing.T) {
	cache := &outboxCleanupCache{listBucketErr: errors.New("list buckets failed")}
	repo := &outboxCleanupRepo{
		events: []SchedulerOutboxEvent{{ID: 1, CreatedAt: time.Now().Add(-time.Hour)}},
		rows:   []int64{1},
	}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{
				OutboxLagRebuildSeconds:  1,
				OutboxLagRebuildFailures: 1,
			},
		},
	}
	svc := NewSchedulerSnapshotService(cache, repo, nil, nil, cfg)

	svc.checkOutboxLag(context.Background(), 0)
	svc.checkOutboxLag(context.Background(), 0)
	if cache.listBucketCalls != 1 {
		t.Fatalf("expected a failed rebuild to stay bounded within the episode, got %d attempts", cache.listBucketCalls)
	}

	repo.events = nil
	svc.checkOutboxLag(context.Background(), 1)
	repo.events = []SchedulerOutboxEvent{{ID: 2, CreatedAt: time.Now().Add(-time.Hour)}}
	svc.checkOutboxLag(context.Background(), 1)

	if cache.listBucketCalls != 2 {
		t.Fatalf("expected recovery to rearm a failed rebuild for the next episode, got %d attempts", cache.listBucketCalls)
	}
}

func TestSchedulerSnapshotServiceCheckOutboxLagFailedRebuildRetriesAfterCooldownWithoutRecovery(t *testing.T) {
	cache := &outboxCleanupCache{
		listBucketErr: errors.New("list buckets failed"),
		listBuckets:   []SchedulerBucket{{Platform: PlatformOpenAI, Mode: SchedulerModeSingle}},
	}
	repo := &outboxCleanupRepo{
		events: []SchedulerOutboxEvent{{ID: 1, CreatedAt: time.Now().Add(-time.Hour)}},
		rows:   []int64{1},
	}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{
				OutboxLagRebuildSeconds:    1,
				OutboxLagRebuildFailures:   1,
				FullRebuildIntervalSeconds: 0,
			},
		},
	}
	svc := NewSchedulerSnapshotService(cache, repo, &outboxCleanupAccountRepo{}, nil, cfg)

	svc.checkOutboxLag(context.Background(), 0)
	for range 3 {
		svc.checkOutboxLag(context.Background(), 0)
	}
	if cache.listBucketCalls != 1 {
		t.Fatalf("expected failed rebuild polls to be rate limited, got %d attempts", cache.listBucketCalls)
	}

	svc.lagMu.Lock()
	if !svc.outboxRebuildRetryAt.After(time.Now()) {
		t.Fatal("expected failed rebuild to schedule a future retry")
	}
	svc.outboxRebuildRetryAt = time.Now().Add(-time.Second)
	svc.lagMu.Unlock()
	svc.checkOutboxLag(context.Background(), 0)
	if cache.listBucketCalls != 2 {
		t.Fatalf("expected persistent degradation to retry after cooldown, got %d attempts", cache.listBucketCalls)
	}

	for range 3 {
		svc.checkOutboxLag(context.Background(), 0)
	}
	if cache.listBucketCalls != 2 {
		t.Fatalf("expected repeated rebuild failures to stay rate limited, got %d attempts", cache.listBucketCalls)
	}

	svc.lagMu.Lock()
	svc.outboxRebuildRetryAt = time.Now().Add(-time.Second)
	svc.lagMu.Unlock()
	cache.listBucketErr = nil
	svc.checkOutboxLag(context.Background(), 0)
	if cache.listBucketCalls != 3 {
		t.Fatalf("expected degraded episode to retry after cooldown, got %d attempts", cache.listBucketCalls)
	}

	for range 3 {
		svc.checkOutboxLag(context.Background(), 0)
	}
	if cache.listBucketCalls != 3 {
		t.Fatalf("expected successful retry to latch the degraded episode, got %d attempts", cache.listBucketCalls)
	}
}

func TestSchedulerSnapshotServiceCheckOutboxLagBacklogRetryDoesNotBypassNewLagThreshold(t *testing.T) {
	cache := &outboxCleanupCache{listBucketErr: errors.New("list buckets failed")}
	repo := &outboxCleanupRepo{
		pendingStats: &SchedulerOutboxPendingStats{Count: 100, OldestCreatedAt: time.Now()},
	}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{
				OutboxLagRebuildSeconds:  1,
				OutboxLagRebuildFailures: 3,
				OutboxBacklogRebuildRows: 50,
			},
		},
	}
	svc := NewSchedulerSnapshotService(cache, repo, nil, nil, cfg)

	// Start with backlog-only degradation and leave its failed rebuild retry due.
	svc.checkOutboxLag(context.Background(), 0)
	if cache.listBucketCalls != 1 {
		t.Fatalf("expected the backlog degradation to attempt one rebuild, got %d", cache.listBucketCalls)
	}
	svc.lagMu.Lock()
	svc.outboxRebuildRetryAt = time.Now().Add(-time.Second)
	svc.lagMu.Unlock()

	// The backlog recovers while lag becomes newly degraded. The stale backlog
	// retry must not make the first lag observation bypass its failure threshold.
	repo.pendingStats.Count = 1
	repo.pendingStats.OldestCreatedAt = time.Now().Add(-time.Hour)
	svc.checkOutboxLag(context.Background(), 0)
	if cache.listBucketCalls != 1 {
		t.Fatalf("expected the new lag episode to start at its own threshold, got %d rebuild attempts", cache.listBucketCalls)
	}

	svc.checkOutboxLag(context.Background(), 0)
	svc.checkOutboxLag(context.Background(), 0)
	if cache.listBucketCalls != 2 {
		t.Fatalf("expected lag rebuild only after three lag observations, got %d attempts", cache.listBucketCalls)
	}
}

func TestSchedulerSnapshotServiceCheckOutboxLagLagRetryDoesNotDelayOrEscalateNewBacklog(t *testing.T) {
	cache := &outboxCleanupCache{listBucketErr: errors.New("list buckets failed")}
	repo := &outboxCleanupRepo{
		pendingStats: &SchedulerOutboxPendingStats{Count: 1, OldestCreatedAt: time.Now().Add(-time.Hour)},
	}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{
				OutboxLagRebuildSeconds:  1,
				OutboxLagRebuildFailures: 1,
				OutboxBacklogRebuildRows: 50,
			},
		},
	}
	svc := NewSchedulerSnapshotService(cache, repo, nil, nil, cfg)

	// Start with lag-only degradation and a failed rebuild in cooldown.
	svc.checkOutboxLag(context.Background(), 0)
	if cache.listBucketCalls != 1 {
		t.Fatalf("expected the lag degradation to attempt one rebuild, got %d", cache.listBucketCalls)
	}

	// Lag recovers while backlog becomes newly degraded. It must start immediately
	// and its first failure must use the base retry generation, not lag's count.
	repo.pendingStats.Count = 100
	repo.pendingStats.OldestCreatedAt = time.Now()
	svc.checkOutboxLag(context.Background(), 0)
	if cache.listBucketCalls != 2 {
		t.Fatalf("expected the new backlog degradation not to inherit lag cooldown, got %d rebuild attempts", cache.listBucketCalls)
	}
	svc.lagMu.Lock()
	failures := svc.outboxRebuildFailures
	svc.lagMu.Unlock()
	if failures != 1 {
		t.Fatalf("expected backlog retry failures to restart at one, got %d", failures)
	}
}

func TestSchedulerSnapshotServiceCheckOutboxLagBacklogRetrySurvivesUnknownBacklog(t *testing.T) {
	cache := &outboxCleanupCache{listBucketErr: errors.New("list buckets failed")}
	repo := &outboxCleanupRepo{
		pendingStats: &SchedulerOutboxPendingStats{Count: 100, OldestCreatedAt: time.Now()},
	}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{
				OutboxBacklogRebuildRows: 50,
			},
		},
	}
	svc := NewSchedulerSnapshotService(cache, repo, nil, nil, cfg)

	// A failed backlog rebuild starts a reason-scoped cooldown.
	svc.checkOutboxLag(context.Background(), 0)
	if cache.listBucketCalls != 1 {
		t.Fatalf("expected one initial backlog rebuild, got %d", cache.listBucketCalls)
	}
	svc.lagMu.Lock()
	retryAt := svc.outboxRebuildRetryAt
	svc.lagMu.Unlock()
	if !retryAt.After(time.Now()) {
		t.Fatalf("expected a future backlog retry, got %s", retryAt)
	}

	// A temporary pending-stats failure makes queue health unknown, not recovered.
	repo.pendingStatsErr = errors.New("pending stats unavailable")
	svc.checkOutboxLag(context.Background(), 0)
	svc.lagMu.Lock()
	retryReason := svc.outboxRebuildRetryReason
	failures := svc.outboxRebuildFailures
	retryAtAfterUnknown := svc.outboxRebuildRetryAt
	svc.lagMu.Unlock()
	if retryReason != "outbox_backlog" || failures != 1 || !retryAtAfterUnknown.Equal(retryAt) {
		t.Fatalf("expected unknown backlog to preserve retry state, got reason=%q failures=%d retry_at=%s", retryReason, failures, retryAtAfterUnknown)
	}

	// When pending stats recover and backlog remains degraded, the original cooldown
	// still applies; only an expired cooldown may trigger the retry.
	repo.pendingStatsErr = nil
	svc.checkOutboxLag(context.Background(), 0)
	if cache.listBucketCalls != 1 {
		t.Fatalf("expected backlog recovery before cooldown to stay rate limited, got %d attempts", cache.listBucketCalls)
	}
	svc.lagMu.Lock()
	svc.outboxRebuildRetryAt = time.Now().Add(-time.Second)
	svc.lagMu.Unlock()
	svc.checkOutboxLag(context.Background(), 0)
	if cache.listBucketCalls != 2 {
		t.Fatalf("expected backlog retry after cooldown expiry, got %d attempts", cache.listBucketCalls)
	}
}

func TestSchedulerSnapshotServiceCheckOutboxLagPreemptsBacklogRetryAfterStatsRecovery(t *testing.T) {
	cache := &outboxCleanupCache{listBucketErr: errors.New("list buckets failed")}
	repo := &outboxCleanupRepo{
		pendingStats: &SchedulerOutboxPendingStats{Count: 100, OldestCreatedAt: time.Now()},
	}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{
				OutboxLagRebuildSeconds:  1,
				OutboxLagRebuildFailures: 3,
				OutboxBacklogRebuildRows: 50,
			},
		},
	}
	svc := NewSchedulerSnapshotService(cache, repo, nil, nil, cfg)

	// Backlog starts the first failed rebuild generation.
	svc.checkOutboxLag(context.Background(), 0)
	if cache.listBucketCalls != 1 {
		t.Fatalf("expected one initial backlog rebuild, got %d", cache.listBucketCalls)
	}
	repo.pendingStatsErr = errors.New("pending stats unavailable")
	svc.checkOutboxLag(context.Background(), 0)
	repo.pendingStatsErr = nil
	repo.pendingStats.Count = 1
	repo.pendingStats.OldestCreatedAt = time.Now().Add(-time.Hour)

	// Once the atomic stats read recovers, lag starts a reason-scoped episode and
	// preempts the old backlog cooldown only after reaching its own threshold.
	for observation := 1; observation <= 2; observation++ {
		svc.checkOutboxLag(context.Background(), 0)
		if cache.listBucketCalls != 1 {
			t.Fatalf("expected lag observation %d to stay below threshold, got %d rebuild attempts", observation, cache.listBucketCalls)
		}
	}
	svc.checkOutboxLag(context.Background(), 0)
	if cache.listBucketCalls != 2 {
		t.Fatalf("expected lag to preempt backlog cooldown at its threshold, got %d attempts", cache.listBucketCalls)
	}

	svc.lagMu.Lock()
	retryReason := svc.outboxRebuildRetryReason
	failures := svc.outboxRebuildFailures
	retryAt := svc.outboxRebuildRetryAt
	svc.lagMu.Unlock()
	if retryReason != "outbox_lag" || failures != 1 || !retryAt.After(time.Now()) {
		t.Fatalf("expected a fresh lag retry generation, got reason=%q failures=%d retry_at=%s", retryReason, failures, retryAt)
	}
}

func TestOutboxRebuildRetryDelayIsExponentiallyBounded(t *testing.T) {
	previous := time.Duration(0)
	for failures := 1; failures <= 20; failures++ {
		delay := outboxRebuildRetryDelay(failures)
		if delay < previous {
			t.Fatalf("expected retry delay to be monotonic, failure %d produced %s after %s", failures, delay, previous)
		}
		if delay > outboxRebuildRetryMaxDelay {
			t.Fatalf("expected retry delay to stay bounded, got %s", delay)
		}
		previous = delay
	}
	if previous != outboxRebuildRetryMaxDelay {
		t.Fatalf("expected repeated failures to reach max delay %s, got %s", outboxRebuildRetryMaxDelay, previous)
	}
}

func TestSchedulerSnapshotServicePollOutboxEmptyBatchClearsDegradedEpisode(t *testing.T) {
	cache := &outboxCleanupCache{listBuckets: []SchedulerBucket{{Platform: PlatformOpenAI, Mode: SchedulerModeSingle}}}
	repo := &outboxCleanupRepo{
		events: []SchedulerOutboxEvent{{ID: 1, CreatedAt: time.Now().Add(-time.Hour)}},
		rows:   []int64{1},
	}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{
				OutboxLagRebuildSeconds:  1,
				OutboxLagRebuildFailures: 1,
				OutboxBacklogRebuildRows: 1,
			},
		},
	}
	svc := NewSchedulerSnapshotService(cache, repo, &outboxCleanupAccountRepo{}, nil, cfg)

	svc.checkOutboxLag(context.Background(), 0)
	repo.events = nil
	svc.pollOutbox()

	if repo.pendingStatsCalls != 2 {
		t.Fatalf("expected degraded check and empty poll to read pending stats, got %d calls", repo.pendingStatsCalls)
	}
	if len(repo.firstCreatedAfterID) != 0 || repo.maxIDCalls != 0 {
		t.Fatalf("expected no legacy lag queries, got first=%#v max=%d", repo.firstCreatedAfterID, repo.maxIDCalls)
	}

	repo.events = []SchedulerOutboxEvent{{ID: 2, CreatedAt: time.Now().Add(-time.Hour)}}
	svc.checkOutboxLag(context.Background(), 1)
	if cache.listBucketCalls != 2 {
		t.Fatalf("expected empty-poll recovery to rearm the next degraded episode, got %d attempts", cache.listBucketCalls)
	}
}

func TestSchedulerSnapshotServiceOutboxLagWarningIsTransitionLimited(t *testing.T) {
	svc := NewSchedulerSnapshotService(nil, nil, nil, nil, nil)

	if !svc.shouldLogOutboxLagWarning(true) {
		t.Fatal("expected the initial degraded transition to log")
	}
	if svc.shouldLogOutboxLagWarning(true) {
		t.Fatal("expected persistent degradation to suppress repeated warnings")
	}
	if svc.shouldLogOutboxLagWarning(true) {
		t.Fatal("expected persistent degradation to suppress repeated warnings")
	}
	if svc.shouldLogOutboxLagWarning(false) {
		t.Fatal("expected recovery not to emit a lag warning")
	}
	if !svc.shouldLogOutboxLagWarning(true) {
		t.Fatal("expected renewed degradation to log after recovery")
	}
}

func TestSchedulerSnapshotServiceCheckOutboxLagSamplesMaxIDErrors(t *testing.T) {
	svc := NewSchedulerSnapshotService(nil, nil, nil, nil, nil)
	now := time.Now()

	if !svc.shouldLogOutboxMaxIDError(now) {
		t.Fatal("expected the first MaxID error to log")
	}
	if svc.shouldLogOutboxMaxIDError(now.Add(outboxMaxIDErrorLogSampleInterval / 2)) {
		t.Fatal("expected MaxID errors inside the sample interval to be suppressed")
	}
	if !svc.shouldLogOutboxMaxIDError(now.Add(outboxMaxIDErrorLogSampleInterval)) {
		t.Fatal("expected MaxID error logging to rearm after the sample interval")
	}
}

func TestSchedulerSnapshotServicePollOutboxHealthyEmptyBatchReadsPendingStats(t *testing.T) {
	cache := &outboxCleanupCache{}
	repo := &outboxCleanupRepo{}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{
				OutboxLagRebuildSeconds:  1,
				OutboxLagRebuildFailures: 1,
				OutboxBacklogRebuildRows: 1,
			},
		},
	}
	svc := NewSchedulerSnapshotService(cache, repo, nil, nil, cfg)

	svc.pollOutbox()

	if repo.pendingStatsCalls != 1 {
		t.Fatalf("expected healthy empty poll to verify pending rows, got %d calls", repo.pendingStatsCalls)
	}
	if len(repo.firstCreatedAfterID) != 0 || repo.maxIDCalls != 0 {
		t.Fatalf("expected healthy empty poll to avoid legacy health queries, got first=%#v max=%d", repo.firstCreatedAfterID, repo.maxIDCalls)
	}
	health := svc.OutboxWorkerHealth()
	if !health.Healthy || health.PendingCount != 0 || health.LastSuccessAt.IsZero() {
		t.Fatalf("unexpected worker health after empty poll: %#v", health)
	}
}

func TestSchedulerSnapshotServiceEmptyPollDoesNotReleaseRunningRebuild(t *testing.T) {
	baseCache := &outboxCleanupCache{
		watermark:   1,
		listBuckets: []SchedulerBucket{{Platform: PlatformOpenAI, Mode: SchedulerModeSingle}},
	}
	cache := &blockingOutboxCleanupCache{
		outboxCleanupCache: baseCache,
		started:            make(chan struct{}),
		release:            make(chan struct{}),
	}
	repo := &outboxCleanupRepo{
		events: []SchedulerOutboxEvent{{ID: 1, CreatedAt: time.Now().Add(-time.Hour)}},
		rows:   []int64{1},
	}
	cfg := &config.Config{
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{
				OutboxLagRebuildSeconds:  1,
				OutboxLagRebuildFailures: 1,
			},
		},
	}
	svc := NewSchedulerSnapshotService(cache, repo, &outboxCleanupAccountRepo{}, nil, cfg)

	firstDone := make(chan struct{})
	go func() {
		svc.checkOutboxLag(context.Background(), 0)
		close(firstDone)
	}()
	select {
	case <-cache.started:
	case <-time.After(time.Second):
		t.Fatal("first rebuild did not start")
	}

	// The empty batch proves recovery for episode/retry state, but it must not
	// release ownership of the still-running rebuild.
	svc.pollOutbox()

	secondDone := make(chan struct{})
	go func() {
		svc.checkOutboxLag(context.Background(), 0)
		close(secondDone)
	}()
	select {
	case <-secondDone:
	case <-time.After(200 * time.Millisecond):
		close(cache.release)
		<-firstDone
		<-secondDone
		t.Fatal("second lag check queued another rebuild while the first was running")
	}

	close(cache.release)
	<-firstDone
	if calls := cache.listCalls(); calls != 1 {
		t.Fatalf("expected one rebuild generation, got %d", calls)
	}
}

func TestSchedulerSnapshotServiceCleanupSkipsNonPositiveWatermark(t *testing.T) {
	repo := &outboxCleanupRepo{
		rows:         []int64{1, 2, 3},
		lockAcquired: true,
	}
	svc := NewSchedulerSnapshotService(&outboxCleanupCache{}, repo, nil, nil, nil)

	svc.cleanupConsumedOutbox(0)

	if repo.lockAttempts != 0 {
		t.Fatalf("expected no lock attempt for non-positive watermark, got %d", repo.lockAttempts)
	}
	if len(repo.deleteCalls) != 0 {
		t.Fatalf("expected no delete calls, got %#v", repo.deleteCalls)
	}
	if !reflect.DeepEqual(repo.rows, []int64{1, 2, 3}) {
		t.Fatalf("expected rows unchanged, got %#v", repo.rows)
	}
}

func int64Range(start, end int64) []int64 {
	values := make([]int64, 0, end-start+1)
	for id := start; id <= end; id++ {
		values = append(values, id)
	}
	return values
}
