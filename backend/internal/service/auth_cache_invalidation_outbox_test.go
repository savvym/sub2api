package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dgraph-io/ristretto"
	"github.com/stretchr/testify/require"
)

type authInvalidationRepoStub struct {
	mu             sync.Mutex
	events         []AuthCacheInvalidationEvent
	claimLimit     int
	scheduled      []int64
	scheduleDelays []time.Duration
	deleted        []int64
	retried        []int64
	retryDelays    []time.Duration
	released       []int64
	releaseCtx     error
	retryError     string
	scheduleErr    error
	deleteErr      error
	stats          AuthCacheInvalidationOutboxStats
	statsErr       error
}

func (r *authInvalidationRepoStub) Claim(_ context.Context, _ string, limit int, _ time.Duration) ([]AuthCacheInvalidationEvent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.claimLimit = limit
	return append([]AuthCacheInvalidationEvent(nil), r.events...), nil
}
func (r *authInvalidationRepoStub) DeleteClaimed(_ context.Context, id int64, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deleted = append(r.deleted, id)
	return r.deleteErr
}
func (r *authInvalidationRepoStub) ScheduleSecondPass(_ context.Context, id int64, _ string, delay time.Duration) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.scheduled = append(r.scheduled, id)
	r.scheduleDelays = append(r.scheduleDelays, delay)
	return r.scheduleErr
}
func (r *authInvalidationRepoStub) RetryClaimed(_ context.Context, id int64, _ string, delay time.Duration, lastError string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retried = append(r.retried, id)
	r.retryDelays = append(r.retryDelays, delay)
	r.retryError = lastError
	return nil
}
func (r *authInvalidationRepoStub) ReleaseClaims(ctx context.Context, _ string, eventIDs []int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.released = append(r.released, eventIDs...)
	r.releaseCtx = ctx.Err()
	return nil
}
func (r *authInvalidationRepoStub) Stats(context.Context) (AuthCacheInvalidationOutboxStats, error) {
	return r.stats, r.statsErr
}

type authInvalidationCacheStub struct {
	mu          sync.Mutex
	deleteFn    func(context.Context, string) error
	publishFn   func(context.Context, string) error
	subscribeFn func(context.Context, func(string)) error
	deleted     []string
	published   []string
}

func (*authInvalidationCacheStub) GetCreateAttemptCount(context.Context, int64) (int, error) {
	return 0, nil
}
func (*authInvalidationCacheStub) IncrementCreateAttemptCount(context.Context, int64) error {
	return nil
}
func (*authInvalidationCacheStub) DeleteCreateAttemptCount(context.Context, int64) error { return nil }
func (*authInvalidationCacheStub) IncrementDailyUsage(context.Context, string) error     { return nil }
func (*authInvalidationCacheStub) SetDailyUsageExpiry(context.Context, string, time.Duration) error {
	return nil
}
func (*authInvalidationCacheStub) GetAuthCache(context.Context, string) (*APIKeyAuthCacheEntry, error) {
	return nil, errors.New("miss")
}
func (*authInvalidationCacheStub) SetAuthCache(context.Context, string, *APIKeyAuthCacheEntry, time.Duration) error {
	return nil
}
func (c *authInvalidationCacheStub) DeleteAuthCache(ctx context.Context, key string) error {
	c.mu.Lock()
	c.deleted = append(c.deleted, key)
	c.mu.Unlock()
	if c.deleteFn != nil {
		return c.deleteFn(ctx, key)
	}
	return nil
}
func (c *authInvalidationCacheStub) PublishAuthCacheInvalidation(ctx context.Context, key string) error {
	c.mu.Lock()
	c.published = append(c.published, key)
	c.mu.Unlock()
	if c.publishFn != nil {
		return c.publishFn(ctx, key)
	}
	return nil
}
func (c *authInvalidationCacheStub) SubscribeAuthCacheInvalidation(ctx context.Context, handler func(string)) error {
	if c.subscribeFn != nil {
		return c.subscribeFn(ctx, handler)
	}
	return nil
}

func TestAuthCacheInvalidationWorker_FirstPassSchedulesSafetyPass(t *testing.T) {
	repo := &authInvalidationRepoStub{}
	cache := &authInvalidationCacheStub{}
	worker := NewAuthCacheInvalidationWorker(repo, cache)
	worker.processEvent(context.Background(), AuthCacheInvalidationEvent{ID: 7, CacheKey: "hash", Stage: 0})
	require.Equal(t, []string{"hash"}, cache.deleted)
	require.Equal(t, []string{"hash"}, cache.published)
	require.Equal(t, []int64{7}, repo.scheduled)
	require.Equal(t, []time.Duration{authInvalidationSafetyDelay}, repo.scheduleDelays)
	require.Empty(t, repo.deleted)
	health := worker.Health(context.Background())
	require.Equal(t, uint64(1), health.Processed)
	require.Equal(t, uint64(1), health.Stage0.Processed)
	require.Zero(t, health.Stage1.Processed)
}

func TestAuthCacheInvalidationWorker_SecondPassCleansEvent(t *testing.T) {
	repo := &authInvalidationRepoStub{}
	cache := &authInvalidationCacheStub{}
	worker := NewAuthCacheInvalidationWorker(repo, cache)
	worker.processEvent(context.Background(), AuthCacheInvalidationEvent{ID: 8, CacheKey: "hash", Stage: 1})
	require.Equal(t, []int64{8}, repo.deleted)
	health := worker.Health(context.Background())
	require.Equal(t, uint64(1), health.Processed)
	require.Zero(t, health.Stage0.Processed)
	require.Equal(t, uint64(1), health.Stage1.Processed)
}

func TestAuthCacheInvalidationWorker_RetriesRedisAndPublishFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		stage      int
		deleteErr  error
		publishErr error
		published  int
	}{
		{name: "stage0 redis down", stage: 0, deleteErr: errors.New("redis unavailable")},
		{name: "stage1 publish failure after delete", stage: 1, publishErr: errors.New("publish failed"), published: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &authInvalidationRepoStub{}
			cache := &authInvalidationCacheStub{
				deleteFn:  func(context.Context, string) error { return tc.deleteErr },
				publishFn: func(context.Context, string) error { return tc.publishErr },
			}
			worker := NewAuthCacheInvalidationWorker(repo, cache)
			worker.processEvent(context.Background(), AuthCacheInvalidationEvent{ID: 9, CacheKey: "hash", Stage: tc.stage})
			require.Equal(t, []int64{9}, repo.retried)
			require.Len(t, repo.retryDelays, 1)
			require.Positive(t, repo.retryDelays[0])
			require.Len(t, cache.published, tc.published)
			require.NotEmpty(t, repo.retryError)
			require.Empty(t, repo.deleted)
			health := worker.Health(context.Background())
			require.Equal(t, uint64(1), health.Failures)
			if tc.stage == 0 {
				require.Equal(t, uint64(1), health.Stage0.Failures)
				require.Zero(t, health.Stage1.Failures)
				require.Contains(t, health.Stage0.LastError, tc.deleteErr.Error())
			} else {
				require.Zero(t, health.Stage0.Failures)
				require.Equal(t, uint64(1), health.Stage1.Failures)
				require.Contains(t, health.Stage1.LastError, tc.publishErr.Error())
			}
		})
	}
}

func TestAuthCacheInvalidationWorker_AttributesTransitionFailuresToTheirPass(t *testing.T) {
	for _, tc := range []struct {
		name  string
		stage int
		repo  *authInvalidationRepoStub
	}{
		{
			name:  "stage0 schedule failure",
			stage: 0,
			repo:  &authInvalidationRepoStub{scheduleErr: errors.New("schedule failed")},
		},
		{
			name:  "stage1 acknowledgement failure",
			stage: 1,
			repo:  &authInvalidationRepoStub{deleteErr: errors.New("ack failed")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			worker := NewAuthCacheInvalidationWorker(tc.repo, &authInvalidationCacheStub{})
			worker.processEvent(context.Background(), AuthCacheInvalidationEvent{ID: 11, CacheKey: "hash", Stage: tc.stage})

			health := worker.Health(context.Background())
			require.Equal(t, uint64(1), health.Failures)
			require.Zero(t, health.Processed)
			if tc.stage == 0 {
				require.Equal(t, uint64(1), health.Stage0.Failures)
				require.Zero(t, health.Stage1.Failures)
				require.Contains(t, health.Stage0.LastError, "schedule failed")
				return
			}
			require.Zero(t, health.Stage0.Failures)
			require.Equal(t, uint64(1), health.Stage1.Failures)
			require.Contains(t, health.Stage1.LastError, "ack failed")
		})
	}
}

func TestAuthCacheInvalidationWorker_RedisSlowIsTimedOut(t *testing.T) {
	repo := &authInvalidationRepoStub{}
	cache := &authInvalidationCacheStub{deleteFn: func(ctx context.Context, _ string) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	worker := NewAuthCacheInvalidationWorker(repo, cache)
	started := time.Now()
	worker.processEvent(context.Background(), AuthCacheInvalidationEvent{ID: 10, CacheKey: "hash"})
	require.Less(t, time.Since(started), 3*time.Second)
	require.Equal(t, []int64{10}, repo.retried)
	require.Contains(t, repo.retryError, "deadline")
}

func TestAuthCacheInvalidationWorker_BoundedBatchAndHealth(t *testing.T) {
	oldest := time.Now().Add(-time.Minute)
	stage0Oldest := time.Now().Add(-10 * time.Second)
	stage1Oldest := time.Now().Add(-45 * time.Second)
	repo := &authInvalidationRepoStub{stats: AuthCacheInvalidationOutboxStats{
		Pending: 12, OldestCreatedAt: &oldest, MaxAttempts: 4, LastError: "redis down",
		Stage0: AuthCacheInvalidationPassStats{
			Pending: 3, OldestCreatedAt: &stage0Oldest, MaxAttempts: 2, LastError: "stage0 down",
		},
		Stage1: AuthCacheInvalidationPassStats{
			Pending: 9, OldestCreatedAt: &stage1Oldest, MaxAttempts: 4, LastError: "stage1 down",
		},
	}}
	worker := NewAuthCacheInvalidationWorker(repo, &authInvalidationCacheStub{})
	require.NoError(t, worker.processBatch(context.Background()))
	require.Equal(t, authInvalidationBatchSize, repo.claimLimit)
	health := worker.Health(context.Background())
	require.Equal(t, int64(12), health.Pending)
	require.Equal(t, 4, health.MaxAttempts)
	require.Equal(t, "redis down", health.LastError)
	require.GreaterOrEqual(t, health.OldestLag, time.Minute)
	require.Equal(t, 35*time.Second, health.HealthySLA)
	require.Equal(t, 6*time.Minute, health.RecoverySLA)
	require.Equal(t, int64(3), health.Stage0.Pending)
	require.Equal(t, 2, health.Stage0.MaxAttempts)
	require.Equal(t, "stage0 down", health.Stage0.LastError)
	require.GreaterOrEqual(t, health.Stage0.OldestLag, 10*time.Second)
	require.Equal(t, int64(9), health.Stage1.Pending)
	require.Equal(t, 4, health.Stage1.MaxAttempts)
	require.Equal(t, "stage1 down", health.Stage1.LastError)
	require.GreaterOrEqual(t, health.Stage1.OldestLag, 45*time.Second)
}

func TestAuthCacheInvalidationHealthJSONKeepsLegacyFieldsAndAddsPasses(t *testing.T) {
	payload, err := json.Marshal(AuthCacheInvalidationHealth{
		Running: true, Processed: 2, Failures: 1, Pending: 3,
		OldestLag: time.Second, LastError: "legacy", StatsError: "stats",
		HealthySLA: 35 * time.Second, RecoverySLA: 6 * time.Minute, MaxAttempts: 4,
		Stage0: AuthCacheInvalidationPassHealth{Processed: 1, Pending: 1},
		Stage1: AuthCacheInvalidationPassHealth{Processed: 1, Pending: 2},
	})
	require.NoError(t, err)

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload, &fields))
	for _, field := range []string{
		"running", "processed", "failures", "pending", "oldest_lag", "last_error",
		"stats_error", "healthy_sla", "recovery_sla", "max_attempts", "stage0", "stage1",
	} {
		require.Contains(t, fields, field)
	}

	for _, pass := range []string{"stage0", "stage1"} {
		var passFields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(fields[pass], &passFields))
		for _, field := range []string{"processed", "failures", "pending", "oldest_lag", "max_attempts"} {
			require.Contains(t, passFields, field)
		}
	}
}

func TestAuthCacheInvalidationWorker_ProcessesClaimedBatchConcurrently(t *testing.T) {
	events := make([]AuthCacheInvalidationEvent, 32)
	for i := range events {
		events[i] = AuthCacheInvalidationEvent{ID: int64(i + 1), CacheKey: "hash", Stage: 1}
	}
	repo := &authInvalidationRepoStub{events: events}
	cache := &authInvalidationCacheStub{deleteFn: func(context.Context, string) error {
		time.Sleep(100 * time.Millisecond)
		return nil
	}}
	worker := NewAuthCacheInvalidationWorker(repo, cache)
	started := time.Now()
	require.NoError(t, worker.processBatch(context.Background()))
	require.Less(t, time.Since(started), time.Second)
	require.Len(t, repo.deleted, 32)
}

func TestAuthCacheInvalidationWorker_LifecycleIsManagedAndIdempotent(t *testing.T) {
	worker := NewAuthCacheInvalidationWorker(&authInvalidationRepoStub{}, &authInvalidationCacheStub{})
	worker.Start()
	require.Eventually(t, func() bool { return worker.Health(context.Background()).Running }, time.Second, 10*time.Millisecond)
	require.NotPanics(t, func() { worker.Stop(); worker.Stop() })
	require.False(t, worker.Health(context.Background()).Running)
}

func TestAuthCacheInvalidationWorker_StopReleasesWholeUnsettledBatch(t *testing.T) {
	events := make([]AuthCacheInvalidationEvent, 32)
	for i := range events {
		events[i] = AuthCacheInvalidationEvent{ID: int64(i + 1), CacheKey: "hash", Stage: 0}
	}
	repo := &authInvalidationRepoStub{events: events}
	started := make(chan struct{})
	var once sync.Once
	cache := &authInvalidationCacheStub{deleteFn: func(ctx context.Context, _ string) error {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return ctx.Err()
	}}
	worker := NewAuthCacheInvalidationWorker(repo, cache)
	worker.Start()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("auth invalidation processing did not start")
	}
	worker.Stop()

	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.NoError(t, repo.releaseCtx)
	require.Empty(t, repo.retried)
	require.Len(t, repo.released, len(events))
	require.ElementsMatch(t, func() []int64 {
		ids := make([]int64, len(events))
		for i := range ids {
			ids[i] = int64(i + 1)
		}
		return ids
	}(), repo.released)
}

func TestAuthInvalidationRetryDelayIsBoundedAndJittered(t *testing.T) {
	for attempt := 1; attempt <= 20; attempt++ {
		delay := authInvalidationRetryDelay(attempt)
		require.GreaterOrEqual(t, delay, 800*time.Millisecond)
		require.LessOrEqual(t, delay, 308*time.Second)
	}
}

func TestAuthCacheInvalidationSubscriber_RetriesInitialFailureAndStops(t *testing.T) {
	ready := make(chan struct{})
	var calls int
	cache := &authInvalidationCacheStub{subscribeFn: func(ctx context.Context, _ func(string)) error {
		calls++
		if calls == 1 {
			return errors.New("redis starting")
		}
		NotifyAuthCacheSubscriptionReady(ctx)
		close(ready)
		<-ctx.Done()
		return ctx.Err()
	}}
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, cache, nil)
	localCache, err := ristretto.NewCache(&ristretto.Config{NumCounters: 10, MaxCost: 1, BufferItems: 64})
	require.NoError(t, err)
	defer localCache.Close()
	svc.authNegativeCacheL1 = localCache
	svc.StartAuthCacheInvalidationSubscriber(context.Background())
	select {
	case <-ready:
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber did not retry")
	}
	require.Eventually(t, func() bool { return svc.AuthCacheInvalidationSubscriberHealth().Connected }, time.Second, 10*time.Millisecond)
	require.Equal(t, uint64(1), svc.AuthCacheInvalidationSubscriberHealth().Failures)
	require.NotPanics(t, func() { svc.StopAuthCacheInvalidationSubscriber(); svc.StopAuthCacheInvalidationSubscriber() })
}

func TestAuthCacheInvalidationSubscriber_ReconnectsAfterRuntimeDisconnect(t *testing.T) {
	ready := make(chan int, 2)
	var calls int
	cache := &authInvalidationCacheStub{subscribeFn: func(ctx context.Context, _ func(string)) error {
		calls++
		NotifyAuthCacheSubscriptionReady(ctx)
		ready <- calls
		if calls == 1 {
			return errors.New("connection dropped")
		}
		<-ctx.Done()
		return ctx.Err()
	}}
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, cache, nil)
	localCache, err := ristretto.NewCache(&ristretto.Config{NumCounters: 10, MaxCost: 1, BufferItems: 64})
	require.NoError(t, err)
	defer localCache.Close()
	svc.authNegativeCacheL1 = localCache
	svc.StartAuthCacheInvalidationSubscriber(context.Background())

	select {
	case call := <-ready:
		require.Equal(t, 1, call)
	case <-time.After(time.Second):
		t.Fatal("initial subscription did not start")
	}
	select {
	case call := <-ready:
		require.Equal(t, 2, call)
	case <-time.After(2 * time.Second):
		t.Fatal("subscriber did not reconnect after runtime disconnect")
	}
	require.Eventually(t, func() bool { return svc.AuthCacheInvalidationSubscriberHealth().Connected }, time.Second, 10*time.Millisecond)
	require.Equal(t, uint64(1), svc.AuthCacheInvalidationSubscriberHealth().Failures)
	svc.StopAuthCacheInvalidationSubscriber()
}
