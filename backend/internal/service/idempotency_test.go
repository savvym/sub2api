package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type inMemoryIdempotencyRepo struct {
	mu     sync.Mutex
	nextID int64
	data   map[string]*IdempotencyRecord
}

func newInMemoryIdempotencyRepo() *inMemoryIdempotencyRepo {
	return &inMemoryIdempotencyRepo{
		nextID: 1,
		data:   make(map[string]*IdempotencyRecord),
	}
}

func (r *inMemoryIdempotencyRepo) key(scope, hash string) string {
	return scope + "|" + hash
}

func cloneRecord(in *IdempotencyRecord) *IdempotencyRecord {
	if in == nil {
		return nil
	}
	out := *in
	if in.ResponseStatus != nil {
		v := *in.ResponseStatus
		out.ResponseStatus = &v
	}
	if in.ResponseBody != nil {
		v := *in.ResponseBody
		out.ResponseBody = &v
	}
	if in.ErrorReason != nil {
		v := *in.ErrorReason
		out.ErrorReason = &v
	}
	if in.LockedUntil != nil {
		v := *in.LockedUntil
		out.LockedUntil = &v
	}
	return &out
}

func (r *inMemoryIdempotencyRepo) CreateProcessing(_ context.Context, record *IdempotencyRecord) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	k := r.key(record.Scope, record.IdempotencyKeyHash)
	if _, ok := r.data[k]; ok {
		return false, nil
	}
	rec := cloneRecord(record)
	rec.ID = r.nextID
	rec.CreatedAt = time.Now()
	rec.UpdatedAt = rec.CreatedAt
	r.nextID++
	r.data[k] = rec
	record.ID = rec.ID
	record.CreatedAt = rec.CreatedAt
	record.UpdatedAt = rec.UpdatedAt
	return true, nil
}

func (r *inMemoryIdempotencyRepo) GetByScopeAndKeyHash(_ context.Context, scope, keyHash string) (*IdempotencyRecord, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneRecord(r.data[r.key(scope, keyHash)]), nil
}

func (r *inMemoryIdempotencyRepo) ExtendExpiration(_ context.Context, id int64, requestFingerprint string, newExpiresAt time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.data {
		if rec.ID != id || rec.RequestFingerprint != requestFingerprint {
			continue
		}
		if newExpiresAt.After(rec.ExpiresAt) {
			rec.ExpiresAt = newExpiresAt
		}
		rec.UpdatedAt = time.Now()
		return true, nil
	}
	return false, nil
}

func (r *inMemoryIdempotencyRepo) TryReclaim(_ context.Context, id int64, fromStatus string, now, newLockedUntil, newExpiresAt time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.data {
		if rec.ID != id {
			continue
		}
		if rec.Status != fromStatus {
			return false, nil
		}
		if rec.LockedUntil != nil && rec.LockedUntil.After(now) {
			return false, nil
		}
		rec.Status = IdempotencyStatusProcessing
		if rec.LockedUntil == nil || newLockedUntil.After(*rec.LockedUntil) {
			rec.LockedUntil = &newLockedUntil
		}
		if newExpiresAt.After(rec.ExpiresAt) {
			rec.ExpiresAt = newExpiresAt
		}
		rec.ErrorReason = nil
		rec.UpdatedAt = time.Now()
		return true, nil
	}
	return false, nil
}

func (r *inMemoryIdempotencyRepo) ExtendProcessingLock(_ context.Context, id int64, requestFingerprint string, newLockedUntil, newExpiresAt time.Time) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, rec := range r.data {
		if rec.ID != id {
			continue
		}
		if rec.Status != IdempotencyStatusProcessing || rec.RequestFingerprint != requestFingerprint {
			return false, nil
		}
		rec.LockedUntil = &newLockedUntil
		rec.ExpiresAt = newExpiresAt
		rec.UpdatedAt = time.Now()
		return true, nil
	}
	return false, nil
}

func (r *inMemoryIdempotencyRepo) MarkSucceeded(_ context.Context, id int64, responseStatus int, responseBody string, expiresAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.data {
		if rec.ID != id {
			continue
		}
		rec.Status = IdempotencyStatusSucceeded
		rec.LockedUntil = nil
		rec.ExpiresAt = expiresAt
		rec.UpdatedAt = time.Now()
		rec.ErrorReason = nil
		rec.ResponseStatus = &responseStatus
		rec.ResponseBody = &responseBody
		return nil
	}
	return errors.New("record not found")
}

func (r *inMemoryIdempotencyRepo) MarkFailedRetryable(_ context.Context, id int64, errorReason string, lockedUntil, expiresAt time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range r.data {
		if rec.ID != id {
			continue
		}
		rec.Status = IdempotencyStatusFailedRetryable
		rec.LockedUntil = &lockedUntil
		rec.ExpiresAt = expiresAt
		rec.UpdatedAt = time.Now()
		rec.ErrorReason = &errorReason
		return nil
	}
	return errors.New("record not found")
}

func (r *inMemoryIdempotencyRepo) DeleteExpired(_ context.Context, now time.Time, _ int) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var deleted int64
	for k, rec := range r.data {
		if !rec.ExpiresAt.After(now) {
			delete(r.data, k)
			deleted++
		}
	}
	return deleted, nil
}

func TestIdempotencyCoordinator_RequireKey(t *testing.T) {
	resetIdempotencyMetricsForTest()
	repo := newInMemoryIdempotencyRepo()
	cfg := DefaultIdempotencyConfig()
	cfg.ObserveOnly = false
	coordinator := NewIdempotencyCoordinator(repo, cfg)

	_, err := coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
		Scope:      "test.scope",
		Method:     "POST",
		Route:      "/test",
		ActorScope: "admin:1",
		RequireKey: true,
		Payload:    map[string]any{"a": 1},
	}, func(ctx context.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(err), infraerrors.Code(ErrIdempotencyKeyRequired))
}

func TestIdempotencyCoordinator_ReplaySucceededResult(t *testing.T) {
	resetIdempotencyMetricsForTest()
	repo := newInMemoryIdempotencyRepo()
	cfg := DefaultIdempotencyConfig()
	coordinator := NewIdempotencyCoordinator(repo, cfg)

	execCount := 0
	exec := func(ctx context.Context) (any, error) {
		execCount++
		return map[string]any{"count": execCount}, nil
	}

	opts := IdempotencyExecuteOptions{
		Scope:          "test.scope",
		Method:         "POST",
		Route:          "/test",
		ActorScope:     "user:1",
		RequireKey:     true,
		IdempotencyKey: "case-1",
		Payload:        map[string]any{"a": 1},
	}

	first, err := coordinator.Execute(context.Background(), opts, exec)
	require.NoError(t, err)
	require.False(t, first.Replayed)

	second, err := coordinator.Execute(context.Background(), opts, exec)
	require.NoError(t, err)
	require.True(t, second.Replayed)
	require.Equal(t, 1, execCount, "second request should replay without executing business logic")

	metrics := GetIdempotencyMetricsSnapshot()
	require.Equal(t, uint64(1), metrics.ClaimTotal)
	require.Equal(t, uint64(1), metrics.ReplayTotal)
}

func TestIdempotencyCoordinator_ReclaimExpiredSucceededRecord(t *testing.T) {
	resetIdempotencyMetricsForTest()
	repo := newInMemoryIdempotencyRepo()
	coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())

	opts := IdempotencyExecuteOptions{
		Scope:          "test.scope.expired",
		Method:         "POST",
		Route:          "/test/expired",
		ActorScope:     "user:99",
		RequireKey:     true,
		IdempotencyKey: "expired-case",
		Payload:        map[string]any{"k": "v"},
	}

	execCount := 0
	exec := func(ctx context.Context) (any, error) {
		execCount++
		return map[string]any{"count": execCount}, nil
	}

	first, err := coordinator.Execute(context.Background(), opts, exec)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.False(t, first.Replayed)
	require.Equal(t, 1, execCount)

	keyHash := HashIdempotencyKey(opts.IdempotencyKey)
	repo.mu.Lock()
	persistedScope := BuildActorQualifiedIdempotencyScope(opts.Scope, opts.ActorScope)
	existing := repo.data[repo.key(persistedScope, keyHash)]
	require.NotNil(t, existing)
	existing.ExpiresAt = time.Now().Add(-time.Second)
	repo.mu.Unlock()

	second, err := coordinator.Execute(context.Background(), opts, exec)
	require.NoError(t, err)
	require.NotNil(t, second)
	require.False(t, second.Replayed, "expired record should be reclaimed and execute business logic again")
	require.Equal(t, 2, execCount)

	third, err := coordinator.Execute(context.Background(), opts, exec)
	require.NoError(t, err)
	require.NotNil(t, third)
	require.True(t, third.Replayed)
	payload, ok := third.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, float64(2), payload["count"])

	metrics := GetIdempotencyMetricsSnapshot()
	require.GreaterOrEqual(t, metrics.ClaimTotal, uint64(2))
	require.GreaterOrEqual(t, metrics.ReplayTotal, uint64(1))
}

func TestIdempotencyCoordinator_SameKeyDifferentPayloadConflict(t *testing.T) {
	resetIdempotencyMetricsForTest()
	repo := newInMemoryIdempotencyRepo()
	cfg := DefaultIdempotencyConfig()
	coordinator := NewIdempotencyCoordinator(repo, cfg)

	_, err := coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
		Scope:          "test.scope",
		Method:         "POST",
		Route:          "/test",
		ActorScope:     "user:1",
		RequireKey:     true,
		IdempotencyKey: "case-2",
		Payload:        map[string]any{"a": 1},
	}, func(ctx context.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	require.NoError(t, err)

	_, err = coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
		Scope:          "test.scope",
		Method:         "POST",
		Route:          "/test",
		ActorScope:     "user:1",
		RequireKey:     true,
		IdempotencyKey: "case-2",
		Payload:        map[string]any{"a": 2},
	}, func(ctx context.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(err), infraerrors.Code(ErrIdempotencyKeyConflict))

	metrics := GetIdempotencyMetricsSnapshot()
	require.Equal(t, uint64(1), metrics.ConflictTotal)
}

func TestIdempotencyCoordinator_BackoffAfterRetryableFailure(t *testing.T) {
	resetIdempotencyMetricsForTest()
	repo := newInMemoryIdempotencyRepo()
	cfg := DefaultIdempotencyConfig()
	cfg.FailedRetryBackoff = 2 * time.Second
	coordinator := NewIdempotencyCoordinator(repo, cfg)

	opts := IdempotencyExecuteOptions{
		Scope:          "test.scope",
		Method:         "POST",
		Route:          "/test",
		ActorScope:     "user:1",
		RequireKey:     true,
		IdempotencyKey: "case-3",
		Payload:        map[string]any{"a": 1},
	}

	_, err := coordinator.Execute(context.Background(), opts, func(ctx context.Context) (any, error) {
		return nil, infraerrors.InternalServer("UPSTREAM_ERROR", "upstream error")
	})
	require.Error(t, err)

	_, err = coordinator.Execute(context.Background(), opts, func(ctx context.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(err), infraerrors.Code(ErrIdempotencyRetryBackoff))
	require.Greater(t, RetryAfterSecondsFromError(err), 0)

	metrics := GetIdempotencyMetricsSnapshot()
	require.GreaterOrEqual(t, metrics.RetryBackoffTotal, uint64(2))
	require.GreaterOrEqual(t, metrics.ConflictTotal, uint64(1))
	require.GreaterOrEqual(t, metrics.ProcessingDurationCount, uint64(1))
}

func TestIdempotencyCoordinator_ConcurrentSameKeySingleSideEffect(t *testing.T) {
	resetIdempotencyMetricsForTest()
	repo := newInMemoryIdempotencyRepo()
	cfg := DefaultIdempotencyConfig()
	cfg.ProcessingTimeout = 2 * time.Second
	coordinator := NewIdempotencyCoordinator(repo, cfg)

	opts := IdempotencyExecuteOptions{
		Scope:          "test.scope.concurrent",
		Method:         "POST",
		Route:          "/test/concurrent",
		ActorScope:     "user:7",
		RequireKey:     true,
		IdempotencyKey: "concurrent-case",
		Payload:        map[string]any{"v": 1},
	}

	var execCount int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = coordinator.Execute(context.Background(), opts, func(ctx context.Context) (any, error) {
				atomic.AddInt32(&execCount, 1)
				time.Sleep(80 * time.Millisecond)
				return map[string]any{"ok": true}, nil
			})
		}()
	}
	wg.Wait()

	replayed, err := coordinator.Execute(context.Background(), opts, func(ctx context.Context) (any, error) {
		atomic.AddInt32(&execCount, 1)
		return map[string]any{"ok": true}, nil
	})
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	require.Equal(t, int32(1), atomic.LoadInt32(&execCount), "concurrent same-key requests should execute business side-effect once")

	metrics := GetIdempotencyMetricsSnapshot()
	require.Equal(t, uint64(1), metrics.ClaimTotal)
	require.Equal(t, uint64(1), metrics.ReplayTotal)
	require.GreaterOrEqual(t, metrics.ConflictTotal, uint64(1))
}

type failingIdempotencyRepo struct{}

func (failingIdempotencyRepo) CreateProcessing(context.Context, *IdempotencyRecord) (bool, error) {
	return false, errors.New("store unavailable")
}
func (failingIdempotencyRepo) GetByScopeAndKeyHash(context.Context, string, string) (*IdempotencyRecord, error) {
	return nil, errors.New("store unavailable")
}
func (failingIdempotencyRepo) ExtendExpiration(context.Context, int64, string, time.Time) (bool, error) {
	return false, errors.New("store unavailable")
}
func (failingIdempotencyRepo) TryReclaim(context.Context, int64, string, time.Time, time.Time, time.Time) (bool, error) {
	return false, errors.New("store unavailable")
}
func (failingIdempotencyRepo) ExtendProcessingLock(context.Context, int64, string, time.Time, time.Time) (bool, error) {
	return false, errors.New("store unavailable")
}
func (failingIdempotencyRepo) MarkSucceeded(context.Context, int64, int, string, time.Time) error {
	return errors.New("store unavailable")
}
func (failingIdempotencyRepo) MarkFailedRetryable(context.Context, int64, string, time.Time, time.Time) error {
	return errors.New("store unavailable")
}
func (failingIdempotencyRepo) DeleteExpired(context.Context, time.Time, int) (int64, error) {
	return 0, errors.New("store unavailable")
}

func TestIdempotencyCoordinator_StoreUnavailableMetrics(t *testing.T) {
	resetIdempotencyMetricsForTest()
	coordinator := NewIdempotencyCoordinator(failingIdempotencyRepo{}, DefaultIdempotencyConfig())

	_, err := coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
		Scope:          "test.scope.unavailable",
		Method:         "POST",
		Route:          "/test/unavailable",
		ActorScope:     "admin:1",
		RequireKey:     true,
		IdempotencyKey: "case-unavailable",
		Payload:        map[string]any{"v": 1},
	}, func(ctx context.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrIdempotencyStoreUnavail), infraerrors.Code(err))
	require.GreaterOrEqual(t, GetIdempotencyMetricsSnapshot().StoreUnavailableTotal, uint64(1))
}

type utf8RejectingIdempotencyRepo struct {
	inMemoryIdempotencyRepo
}

func newUTF8RejectingIdempotencyRepo() *utf8RejectingIdempotencyRepo {
	return &utf8RejectingIdempotencyRepo{inMemoryIdempotencyRepo: *newInMemoryIdempotencyRepo()}
}

func (r *utf8RejectingIdempotencyRepo) MarkSucceeded(ctx context.Context, id int64, responseStatus int, responseBody string, expiresAt time.Time) error {
	if !utf8.ValidString(responseBody) {
		return errors.New(`pq: invalid byte sequence for encoding "UTF8": 0xe8 0xb4 0x2e`)
	}
	return r.inMemoryIdempotencyRepo.MarkSucceeded(ctx, id, responseStatus, responseBody, expiresAt)
}

func TestIdempotencyCoordinator_TruncatedStoredResponseRemainsUTF8(t *testing.T) {
	repo := newUTF8RejectingIdempotencyRepo()
	cfg := DefaultIdempotencyConfig()
	cfg.MaxStoredResponseLen = len(`{"message":"`) + 2
	coordinator := NewIdempotencyCoordinator(repo, cfg)

	opts := IdempotencyExecuteOptions{
		Scope:          "test.scope.truncate_utf8",
		Method:         "POST",
		Route:          "/api/v1/accounts/import/codex-session",
		ActorScope:     "admin:1",
		RequireKey:     true,
		IdempotencyKey: "truncate-utf8",
		Payload:        map[string]any{"content": "codex-session"},
	}

	result, err := coordinator.Execute(context.Background(), opts, func(ctx context.Context) (any, error) {
		return map[string]any{"message": strings.Repeat("\u8d26", 8)}, nil
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	stored, err := repo.GetByScopeAndKeyHash(
		context.Background(),
		BuildActorQualifiedIdempotencyScope(opts.Scope, opts.ActorScope),
		HashIdempotencyKey(opts.IdempotencyKey),
	)
	require.NoError(t, err)
	require.NotNil(t, stored)
	require.NotNil(t, stored.ResponseBody)
	require.True(t, utf8.ValidString(*stored.ResponseBody))
	require.Contains(t, *stored.ResponseBody, "...(truncated)")
}

func TestDefaultIdempotencyCoordinatorAndTTLs(t *testing.T) {
	SetDefaultIdempotencyCoordinator(nil)
	require.Nil(t, DefaultIdempotencyCoordinator())
	require.Equal(t, DefaultIdempotencyConfig().DefaultTTL, DefaultWriteIdempotencyTTL())
	require.Equal(t, DefaultIdempotencyConfig().SystemOperationTTL, DefaultSystemOperationIdempotencyTTL())

	coordinator := NewIdempotencyCoordinator(newInMemoryIdempotencyRepo(), IdempotencyConfig{
		DefaultTTL:         2 * time.Hour,
		SystemOperationTTL: 15 * time.Minute,
		ProcessingTimeout:  10 * time.Second,
		FailedRetryBackoff: 3 * time.Second,
		ObserveOnly:        false,
	})
	SetDefaultIdempotencyCoordinator(coordinator)
	t.Cleanup(func() {
		SetDefaultIdempotencyCoordinator(nil)
	})

	require.Same(t, coordinator, DefaultIdempotencyCoordinator())
	require.Equal(t, 2*time.Hour, DefaultWriteIdempotencyTTL())
	require.Equal(t, 15*time.Minute, DefaultSystemOperationIdempotencyTTL())
}

func TestNormalizeIdempotencyKeyAndFingerprint(t *testing.T) {
	key, err := NormalizeIdempotencyKey("  abc-123  ")
	require.NoError(t, err)
	require.Equal(t, "abc-123", key)

	key, err = NormalizeIdempotencyKey("")
	require.NoError(t, err)
	require.Equal(t, "", key)

	_, err = NormalizeIdempotencyKey(string(make([]byte, 129)))
	require.Error(t, err)

	_, err = NormalizeIdempotencyKey("bad\nkey")
	require.Error(t, err)

	fp1, err := BuildIdempotencyFingerprint("", "", "", map[string]any{"a": 1})
	require.NoError(t, err)
	require.NotEmpty(t, fp1)
	fp2, err := BuildIdempotencyFingerprint("POST", "/", "anonymous", map[string]any{"a": 1})
	require.NoError(t, err)
	require.Equal(t, fp1, fp2)

	_, err = BuildIdempotencyFingerprint("POST", "/x", "u:1", map[string]any{"bad": make(chan int)})
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrIdempotencyInvalidPayload), infraerrors.Code(err))
}

func TestIdempotencyCoordinator_RejectsInvalidScopeBeforeRepository(t *testing.T) {
	coordinator := NewIdempotencyCoordinator(nil, DefaultIdempotencyConfig())
	tests := map[string]IdempotencyExecuteOptions{
		"nul": {
			Scope: "admin.accounts\x00create",
		},
		"control character": {
			Scope: "admin.accounts\ncreate",
		},
		"raw scope too long": {
			Scope: strings.Repeat("界", MaxIdempotencyScopeCharacters+1),
		},
		"qualified scope too long": {
			Scope:      strings.Repeat("s", MaxIdempotencyScopeCharacters-5),
			ActorScope: "user:42",
		},
	}

	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			opts.IdempotencyKey = "scope-validation"
			called := false
			_, err := coordinator.Execute(context.Background(), opts, func(context.Context) (any, error) {
				called = true
				return nil, nil
			})
			require.Error(t, err)
			require.Equal(t, infraerrors.Code(ErrIdempotencyScopeInvalid), infraerrors.Code(err))
			require.False(t, called)
		})
	}
}

func TestIdempotencyCoordinator_PartitionsPersistedScopeByActor(t *testing.T) {
	repo := newInMemoryIdempotencyRepo()
	coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
	base := IdempotencyExecuteOptions{
		Scope:          "admin.accounts.create",
		Method:         "POST",
		Route:          "/api/v1/admin/accounts",
		IdempotencyKey: "same-key",
		Payload:        map[string]any{"name": "isolated"},
	}

	executions := map[string]int{}
	for _, actorScope := range []string{"user:42", "service_principal:42"} {
		opts := base
		opts.ActorScope = actorScope
		result, err := coordinator.Execute(context.Background(), opts, func(context.Context) (any, error) {
			executions[actorScope]++
			return map[string]any{"actor": actorScope}, nil
		})
		require.NoError(t, err)
		require.False(t, result.Replayed)

		replayed, err := coordinator.Execute(context.Background(), opts, func(context.Context) (any, error) {
			executions[actorScope]++
			return nil, nil
		})
		require.NoError(t, err)
		require.True(t, replayed.Replayed)
		require.Equal(t, 1, executions[actorScope])

		stored, err := repo.GetByScopeAndKeyHash(
			context.Background(),
			BuildActorQualifiedIdempotencyScope(base.Scope, actorScope),
			HashIdempotencyKey(base.IdempotencyKey),
		)
		require.NoError(t, err)
		require.NotNil(t, stored)
		wantFingerprint, err := BuildIdempotencyFingerprint(base.Method, base.Route, actorScope, base.Payload)
		require.NoError(t, err)
		require.Equal(t, wantFingerprint, stored.RequestFingerprint, "qualified records must persist the canonical actor fingerprint")
	}

	legacy, err := repo.GetByScopeAndKeyHash(context.Background(), base.Scope, HashIdempotencyKey(base.IdempotencyKey))
	require.NoError(t, err)
	require.NotNil(t, legacy)
	require.Equal(t, idempotencyUpgradeFenceFingerprint, legacy.RequestFingerprint)
	require.Equal(t, IdempotencyStatusProcessing, legacy.Status)
}

func TestIdempotencyCoordinator_LegacyRawScopeRequiresExactFingerprint(t *testing.T) {
	base := IdempotencyExecuteOptions{
		Scope:          "admin.groups.duplicate",
		Method:         "POST",
		Route:          "/api/v1/admin/groups/:id/duplicate",
		ActorScope:     "user:7",
		IdempotencyKey: "legacy-key",
		Payload:        map[string]any{"group_id": 9},
	}
	keyHash := HashIdempotencyKey(base.IdempotencyKey)

	t.Run("matching legacy record replays", func(t *testing.T) {
		repo := newInMemoryIdempotencyRepo()
		fingerprint, err := BuildIdempotencyFingerprint(base.Method, base.Route, base.ActorScope, base.Payload)
		require.NoError(t, err)
		body := `{"legacy":true}`
		status := 200
		repo.data[repo.key(base.Scope, keyHash)] = &IdempotencyRecord{
			ID:                 1,
			Scope:              base.Scope,
			IdempotencyKeyHash: keyHash,
			RequestFingerprint: fingerprint,
			Status:             IdempotencyStatusSucceeded,
			ResponseStatus:     &status,
			ResponseBody:       &body,
			ExpiresAt:          time.Now().Add(time.Hour),
		}

		executed := 0
		result, err := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig()).Execute(
			context.Background(),
			base,
			func(context.Context) (any, error) {
				executed++
				return nil, nil
			},
		)
		require.NoError(t, err)
		require.True(t, result.Replayed)
		require.Zero(t, executed)
		qualified, err := repo.GetByScopeAndKeyHash(
			context.Background(),
			BuildActorQualifiedIdempotencyScope(base.Scope, base.ActorScope),
			keyHash,
		)
		require.NoError(t, err)
		require.Nil(t, qualified, "legacy replay must not create a second record")
	})

	t.Run("mismatched legacy actor fails closed", func(t *testing.T) {
		repo := newInMemoryIdempotencyRepo()
		otherFingerprint, err := BuildIdempotencyFingerprint(base.Method, base.Route, "user:8", base.Payload)
		require.NoError(t, err)
		repo.data[repo.key(base.Scope, keyHash)] = &IdempotencyRecord{
			ID:                 1,
			Scope:              base.Scope,
			IdempotencyKeyHash: keyHash,
			RequestFingerprint: otherFingerprint,
			Status:             IdempotencyStatusProcessing,
			ExpiresAt:          time.Now().Add(time.Hour),
		}

		executed := 0
		result, err := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig()).Execute(
			context.Background(),
			base,
			func(context.Context) (any, error) {
				executed++
				return map[string]any{"ok": true}, nil
			},
		)
		require.ErrorIs(t, err, ErrIdempotencyKeyConflict)
		require.Nil(t, result)
		require.Zero(t, executed)
		qualified, err := repo.GetByScopeAndKeyHash(
			context.Background(),
			BuildActorQualifiedIdempotencyScope(base.Scope, base.ActorScope),
			keyHash,
		)
		require.NoError(t, err)
		require.Nil(t, qualified)
	})
}

func TestIdempotencyCoordinator_LegacyRequestsRequireExactActorPayloadPair(t *testing.T) {
	base := IdempotencyExecuteOptions{
		Scope:          "admin.system.update",
		Method:         "POST",
		Route:          "/api/v1/admin/system/update",
		ActorScope:     "service_principal:7",
		IdempotencyKey: "legacy-pair",
		Payload:        map[string]any{"operation_id": "canonical-operation"},
		LegacyRequests: []IdempotencyLegacyRequest{{
			ActorScope: "admin:1",
			Payload:    map[string]any{"operation_id": "legacy-operation"},
		}},
	}
	keyHash := HashIdempotencyKey(base.IdempotencyKey)

	for _, testCase := range []struct {
		name         string
		payload      any
		wantReplay   bool
		wantConflict bool
	}{
		{name: "explicit legacy pair replays", payload: map[string]any{"operation_id": "legacy-operation"}, wantReplay: true},
		{name: "legacy actor with canonical payload is rejected", payload: base.Payload, wantConflict: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := newInMemoryIdempotencyRepo()
			fingerprint, err := BuildIdempotencyFingerprint(base.Method, base.Route, "admin:1", testCase.payload)
			require.NoError(t, err)
			body := `{"legacy":true}`
			status := 200
			repo.data[repo.key(base.Scope, keyHash)] = &IdempotencyRecord{
				ID:                 1,
				Scope:              base.Scope,
				IdempotencyKeyHash: keyHash,
				RequestFingerprint: fingerprint,
				Status:             IdempotencyStatusSucceeded,
				ResponseStatus:     &status,
				ResponseBody:       &body,
				ExpiresAt:          time.Now().Add(time.Hour),
			}

			executed := 0
			result, err := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig()).Execute(
				context.Background(),
				base,
				func(context.Context) (any, error) {
					executed++
					return nil, nil
				},
			)
			if testCase.wantConflict {
				require.ErrorIs(t, err, ErrIdempotencyKeyConflict)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				require.Equal(t, testCase.wantReplay, result.Replayed)
			}
			require.Zero(t, executed)
		})
	}
}

func TestIdempotencyCoordinator_QualifiedLegacyRequestIsNotAcceptedInRawScope(t *testing.T) {
	legacyPayload := map[string]any{"operation_id": "legacy-admin-operation"}
	base := IdempotencyExecuteOptions{
		Scope:          "admin.system.update",
		Method:         "POST",
		Route:          "/api/v1/admin/system/update",
		ActorScope:     "service_principal:7",
		IdempotencyKey: "qualified-legacy-pair",
		Payload:        map[string]any{"operation_id": "canonical-principal-operation"},
		LegacyRequests: []IdempotencyLegacyRequest{{
			ActorScope: "admin:1",
			Payload:    legacyPayload,
		}},
		QualifiedLegacyRequests: []IdempotencyLegacyRequest{{
			ActorScope: "service_principal:7",
			Payload:    legacyPayload,
		}},
	}
	keyHash := HashIdempotencyKey(base.IdempotencyKey)
	legacyFingerprint, err := BuildIdempotencyFingerprint(base.Method, base.Route, base.ActorScope, legacyPayload)
	require.NoError(t, err)
	body := `{"legacy":true}`
	status := 200

	for _, testCase := range []struct {
		name              string
		scope             string
		actorScope        string
		wantReplay        bool
		assertNoQualified bool
	}{
		{
			name:       "qualified historical record replays",
			scope:      BuildActorQualifiedIdempotencyScope(base.Scope, base.ActorScope),
			actorScope: base.ActorScope,
			wantReplay: true,
		},
		{
			name:              "same fingerprint in raw scope conflicts",
			scope:             base.Scope,
			actorScope:        base.ActorScope,
			assertNoQualified: true,
		},
		{
			name:       "missing actor never turns raw scope into qualified scope",
			scope:      base.Scope,
			actorScope: "",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := newInMemoryIdempotencyRepo()
			repo.data[repo.key(testCase.scope, keyHash)] = &IdempotencyRecord{
				ID:                 1,
				Scope:              testCase.scope,
				IdempotencyKeyHash: keyHash,
				RequestFingerprint: legacyFingerprint,
				Status:             IdempotencyStatusSucceeded,
				ResponseStatus:     &status,
				ResponseBody:       &body,
				ExpiresAt:          time.Now().Add(time.Hour),
			}

			executed := 0
			opts := base
			opts.ActorScope = testCase.actorScope
			result, executeErr := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig()).Execute(
				context.Background(),
				opts,
				func(context.Context) (any, error) {
					executed++
					return nil, nil
				},
			)
			if testCase.wantReplay {
				require.NoError(t, executeErr)
				require.NotNil(t, result)
				require.True(t, result.Replayed)
			} else {
				require.ErrorIs(t, executeErr, ErrIdempotencyKeyConflict)
				require.Nil(t, result)
				if testCase.assertNoQualified {
					qualified, getErr := repo.GetByScopeAndKeyHash(
						context.Background(),
						BuildActorQualifiedIdempotencyScope(base.Scope, base.ActorScope),
						keyHash,
					)
					require.NoError(t, getErr)
					require.Nil(t, qualified)
				}
			}
			require.Zero(t, executed)
		})
	}
}

type failedFenceExtensionRepo struct {
	*inMemoryIdempotencyRepo
	extendErr error
}

func (r *failedFenceExtensionRepo) ExtendExpiration(context.Context, int64, string, time.Time) (bool, error) {
	return false, r.extendErr
}

func TestIdempotencyCoordinator_RenewsUpgradeFenceBeforeQualifiedClaim(t *testing.T) {
	repo := newInMemoryIdempotencyRepo()
	coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
	base := IdempotencyExecuteOptions{
		Scope:          "admin.accounts.create",
		Method:         "POST",
		Route:          "/api/v1/admin/accounts",
		ActorScope:     "user:1",
		IdempotencyKey: "upgrade-fence-renewal",
		Payload:        map[string]any{"name": "first"},
		TTL:            time.Hour,
	}

	_, err := coordinator.Execute(context.Background(), base, func(context.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	require.NoError(t, err)

	keyHash := HashIdempotencyKey(base.IdempotencyKey)
	repo.mu.Lock()
	raw := repo.data[repo.key(base.Scope, keyHash)]
	require.NotNil(t, raw)
	raw.ExpiresAt = time.Now().Add(-time.Minute)
	repo.mu.Unlock()

	second := base
	second.ActorScope = "user:2"
	second.Payload = map[string]any{"name": "second"}
	executed := 0
	_, err = coordinator.Execute(context.Background(), second, func(context.Context) (any, error) {
		executed++
		return map[string]any{"ok": true}, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, executed)

	renewed, err := repo.GetByScopeAndKeyHash(context.Background(), base.Scope, keyHash)
	require.NoError(t, err)
	require.NotNil(t, renewed)
	require.True(t, renewed.ExpiresAt.After(time.Now().Add(50*time.Minute)))
	deleted, err := repo.DeleteExpired(context.Background(), time.Now(), 100)
	require.NoError(t, err)
	require.Zero(t, deleted, "cleanup must not delete a renewed upgrade fence")

	oldBinaryClaim := &IdempotencyRecord{
		Scope:              base.Scope,
		IdempotencyKeyHash: keyHash,
		RequestFingerprint: "old-binary-request",
		Status:             IdempotencyStatusProcessing,
		ExpiresAt:          time.Now().Add(time.Hour),
	}
	owner, err := repo.CreateProcessing(context.Background(), oldBinaryClaim)
	require.NoError(t, err)
	require.False(t, owner, "a renewed raw fence must continue blocking old binaries")
}

func TestIdempotencyCoordinator_FailsClosedWhenUpgradeFenceCannotBeRenewed(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
	}{
		{name: "condition update missed"},
		{name: "store error", err: errors.New("extend failed")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			baseRepo := newInMemoryIdempotencyRepo()
			repo := &failedFenceExtensionRepo{inMemoryIdempotencyRepo: baseRepo, extendErr: testCase.err}
			coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
			opts := IdempotencyExecuteOptions{
				Scope:          "admin.groups.create",
				Method:         "POST",
				Route:          "/api/v1/admin/groups",
				ActorScope:     "user:1",
				IdempotencyKey: "fence-extension-failure",
				Payload:        map[string]any{"name": "group"},
			}
			_, err := coordinator.Execute(context.Background(), opts, func(context.Context) (any, error) {
				return map[string]any{"ok": true}, nil
			})
			require.NoError(t, err)

			opts.ActorScope = "user:2"
			executed := 0
			result, err := coordinator.Execute(context.Background(), opts, func(context.Context) (any, error) {
				executed++
				return nil, nil
			})
			require.Error(t, err)
			require.Equal(t, infraerrors.Code(ErrIdempotencyStoreUnavail), infraerrors.Code(err))
			require.Nil(t, result)
			require.Zero(t, executed)
		})
	}
}

func TestDuplicateOperationIDsNeverUseSharedMissingActorBucket(t *testing.T) {
	for name, build := range map[string]func(int64, string, string) string{
		"account": duplicateAccountOperationID,
		"group":   duplicateGroupOperationID,
		"monitor": duplicateChannelMonitorOperationID,
	} {
		t.Run(name, func(t *testing.T) {
			require.Empty(t, build(9, "", "same-key"))
			userID := build(9, "user:42", "same-key")
			principalID := build(9, "service_principal:42", "same-key")
			require.NotEmpty(t, userID)
			require.NotEmpty(t, principalID)
			require.NotEqual(t, userID, principalID)
		})
	}
}

func TestRetryAfterSecondsFromErrorBranches(t *testing.T) {
	require.Equal(t, 0, RetryAfterSecondsFromError(nil))
	require.Equal(t, 0, RetryAfterSecondsFromError(errors.New("plain")))

	err := ErrIdempotencyInProgress.WithMetadata(map[string]string{"retry_after": "12"})
	require.Equal(t, 12, RetryAfterSecondsFromError(err))

	err = ErrIdempotencyInProgress.WithMetadata(map[string]string{"retry_after": "bad"})
	require.Equal(t, 0, RetryAfterSecondsFromError(err))
}

func TestIdempotencyCoordinator_ExecuteNilExecutorAndNoKeyPassThrough(t *testing.T) {
	repo := newInMemoryIdempotencyRepo()
	coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())

	_, err := coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
		Scope:          "scope",
		IdempotencyKey: "k",
		Payload:        map[string]any{"a": 1},
	}, nil)
	require.Error(t, err)
	require.Equal(t, "IDEMPOTENCY_EXECUTOR_NIL", infraerrors.Reason(err))

	called := 0
	result, err := coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
		Scope:      "scope",
		RequireKey: true,
		Payload:    map[string]any{"a": 1},
	}, func(ctx context.Context) (any, error) {
		called++
		return map[string]any{"ok": true}, nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, called)
	require.NotNil(t, result)
	require.False(t, result.Replayed)
}

type noIDOwnerRepo struct{}

func (noIDOwnerRepo) CreateProcessing(context.Context, *IdempotencyRecord) (bool, error) {
	return true, nil
}
func (noIDOwnerRepo) GetByScopeAndKeyHash(context.Context, string, string) (*IdempotencyRecord, error) {
	return nil, nil
}
func (noIDOwnerRepo) ExtendExpiration(context.Context, int64, string, time.Time) (bool, error) {
	return false, nil
}
func (noIDOwnerRepo) TryReclaim(context.Context, int64, string, time.Time, time.Time, time.Time) (bool, error) {
	return false, nil
}
func (noIDOwnerRepo) ExtendProcessingLock(context.Context, int64, string, time.Time, time.Time) (bool, error) {
	return false, nil
}
func (noIDOwnerRepo) MarkSucceeded(context.Context, int64, int, string, time.Time) error { return nil }
func (noIDOwnerRepo) MarkFailedRetryable(context.Context, int64, string, time.Time, time.Time) error {
	return nil
}
func (noIDOwnerRepo) DeleteExpired(context.Context, time.Time, int) (int64, error) { return 0, nil }

func TestIdempotencyCoordinator_RepoNilScopeRequiredAndRecordIDMissing(t *testing.T) {
	cfg := DefaultIdempotencyConfig()
	coordinator := NewIdempotencyCoordinator(nil, cfg)

	_, err := coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
		Scope:          "scope",
		IdempotencyKey: "k",
		Payload:        map[string]any{"a": 1},
	}, func(ctx context.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrIdempotencyStoreUnavail), infraerrors.Code(err))

	coordinator = NewIdempotencyCoordinator(newInMemoryIdempotencyRepo(), cfg)
	_, err = coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
		IdempotencyKey: "k2",
		Payload:        map[string]any{"a": 1},
	}, func(ctx context.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	require.Error(t, err)
	require.Equal(t, "IDEMPOTENCY_SCOPE_REQUIRED", infraerrors.Reason(err))

	coordinator = NewIdempotencyCoordinator(noIDOwnerRepo{}, cfg)
	_, err = coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
		Scope:          "scope-no-id",
		IdempotencyKey: "k3",
		Payload:        map[string]any{"a": 1},
	}, func(ctx context.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrIdempotencyStoreUnavail), infraerrors.Code(err))
}

type conflictBranchRepo struct {
	existing      *IdempotencyRecord
	tryReclaimErr error
	tryReclaimOK  bool
}

func (r *conflictBranchRepo) CreateProcessing(context.Context, *IdempotencyRecord) (bool, error) {
	return false, nil
}
func (r *conflictBranchRepo) GetByScopeAndKeyHash(context.Context, string, string) (*IdempotencyRecord, error) {
	return cloneRecord(r.existing), nil
}
func (r *conflictBranchRepo) ExtendExpiration(context.Context, int64, string, time.Time) (bool, error) {
	return true, nil
}
func (r *conflictBranchRepo) TryReclaim(context.Context, int64, string, time.Time, time.Time, time.Time) (bool, error) {
	if r.tryReclaimErr != nil {
		return false, r.tryReclaimErr
	}
	return r.tryReclaimOK, nil
}
func (r *conflictBranchRepo) ExtendProcessingLock(context.Context, int64, string, time.Time, time.Time) (bool, error) {
	return false, nil
}
func (r *conflictBranchRepo) MarkSucceeded(context.Context, int64, int, string, time.Time) error {
	return nil
}
func (r *conflictBranchRepo) MarkFailedRetryable(context.Context, int64, string, time.Time, time.Time) error {
	return nil
}
func (r *conflictBranchRepo) DeleteExpired(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func TestIdempotencyCoordinator_ConflictBranchesAndDecodeError(t *testing.T) {
	now := time.Now()
	fp, err := BuildIdempotencyFingerprint("POST", "/x", "u:1", map[string]any{"a": 1})
	require.NoError(t, err)
	badBody := "{bad-json"
	repo := &conflictBranchRepo{
		existing: &IdempotencyRecord{
			ID:                 1,
			Scope:              "scope",
			IdempotencyKeyHash: HashIdempotencyKey("k"),
			RequestFingerprint: fp,
			Status:             IdempotencyStatusSucceeded,
			ResponseBody:       &badBody,
			ExpiresAt:          now.Add(time.Hour),
		},
	}
	coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
	_, err = coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
		Scope:          "scope",
		IdempotencyKey: "k",
		Method:         "POST",
		Route:          "/x",
		ActorScope:     "u:1",
		Payload:        map[string]any{"a": 1},
	}, func(ctx context.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrIdempotencyStoreUnavail), infraerrors.Code(err))

	repo.existing = &IdempotencyRecord{
		ID:                 2,
		Scope:              "scope",
		IdempotencyKeyHash: HashIdempotencyKey("k"),
		RequestFingerprint: fp,
		Status:             "unknown",
		ExpiresAt:          now.Add(time.Hour),
	}
	_, err = coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
		Scope:          "scope",
		IdempotencyKey: "k",
		Method:         "POST",
		Route:          "/x",
		ActorScope:     "u:1",
		Payload:        map[string]any{"a": 1},
	}, func(ctx context.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrIdempotencyKeyConflict), infraerrors.Code(err))

	repo.existing = &IdempotencyRecord{
		ID:                 3,
		Scope:              "scope",
		IdempotencyKeyHash: HashIdempotencyKey("k"),
		RequestFingerprint: fp,
		Status:             IdempotencyStatusFailedRetryable,
		LockedUntil:        ptrTime(now.Add(-time.Second)),
		ExpiresAt:          now.Add(time.Hour),
	}
	repo.tryReclaimErr = errors.New("reclaim down")
	_, err = coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
		Scope:          "scope",
		IdempotencyKey: "k",
		Method:         "POST",
		Route:          "/x",
		ActorScope:     "u:1",
		Payload:        map[string]any{"a": 1},
	}, func(ctx context.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrIdempotencyStoreUnavail), infraerrors.Code(err))

	repo.tryReclaimErr = nil
	repo.tryReclaimOK = false
	_, err = coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
		Scope:          "scope",
		IdempotencyKey: "k",
		Method:         "POST",
		Route:          "/x",
		ActorScope:     "u:1",
		Payload:        map[string]any{"a": 1},
	}, func(ctx context.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrIdempotencyInProgress), infraerrors.Code(err))
}

type markBehaviorRepo struct {
	inMemoryIdempotencyRepo
	failMarkSucceeded bool
	failMarkFailed    bool
}

func (r *markBehaviorRepo) MarkSucceeded(ctx context.Context, id int64, responseStatus int, responseBody string, expiresAt time.Time) error {
	if r.failMarkSucceeded {
		return errors.New("mark succeeded failed")
	}
	return r.inMemoryIdempotencyRepo.MarkSucceeded(ctx, id, responseStatus, responseBody, expiresAt)
}

func (r *markBehaviorRepo) MarkFailedRetryable(ctx context.Context, id int64, errorReason string, lockedUntil, expiresAt time.Time) error {
	if r.failMarkFailed {
		return errors.New("mark failed retryable failed")
	}
	return r.inMemoryIdempotencyRepo.MarkFailedRetryable(ctx, id, errorReason, lockedUntil, expiresAt)
}

func TestIdempotencyCoordinator_MarkAndMarshalBranches(t *testing.T) {
	repo := &markBehaviorRepo{inMemoryIdempotencyRepo: *newInMemoryIdempotencyRepo()}
	coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())

	repo.failMarkSucceeded = true
	_, err := coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
		Scope:          "scope-success",
		IdempotencyKey: "k1",
		Method:         "POST",
		Route:          "/ok",
		ActorScope:     "u:1",
		Payload:        map[string]any{"a": 1},
	}, func(ctx context.Context) (any, error) {
		return map[string]any{"ok": true}, nil
	})
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrIdempotencyStoreUnavail), infraerrors.Code(err))

	repo.failMarkSucceeded = false
	_, err = coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
		Scope:          "scope-marshal",
		IdempotencyKey: "k2",
		Method:         "POST",
		Route:          "/bad",
		ActorScope:     "u:1",
		Payload:        map[string]any{"a": 1},
	}, func(ctx context.Context) (any, error) {
		return map[string]any{"bad": make(chan int)}, nil
	})
	require.Error(t, err)
	require.Equal(t, infraerrors.Code(ErrIdempotencyStoreUnavail), infraerrors.Code(err))

	repo.failMarkFailed = true
	_, err = coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
		Scope:          "scope-fail",
		IdempotencyKey: "k3",
		Method:         "POST",
		Route:          "/fail",
		ActorScope:     "u:1",
		Payload:        map[string]any{"a": 1},
	}, func(ctx context.Context) (any, error) {
		return nil, errors.New("plain failure")
	})
	require.Error(t, err)
	require.Equal(t, "plain failure", err.Error())
}

func TestIdempotencyCoordinator_CustomSuccessFinalizer(t *testing.T) {
	t.Run("invoked with persisted response on detached context", func(t *testing.T) {
		repo := newInMemoryIdempotencyRepo()
		coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
		requestCtx, cancelRequest := context.WithCancel(context.Background())
		var got IdempotencySuccessFinalization
		calls := 0

		result, err := coordinator.Execute(requestCtx, IdempotencyExecuteOptions{
			Scope:          "custom-finalizer",
			ActorScope:     "user:42",
			Method:         "POST",
			Route:          "/custom-finalizer",
			IdempotencyKey: "success",
			Payload:        map[string]any{"value": "stored"},
			TTL:            time.Hour,
			SuccessFinalizer: func(ctx context.Context, finalization IdempotencySuccessFinalization) error {
				calls++
				got = finalization
				require.NoError(t, ctx.Err())
				_, hasDeadline := ctx.Deadline()
				require.True(t, hasDeadline)
				return repo.MarkSucceeded(
					ctx,
					finalization.RecordID,
					finalization.ResponseStatus,
					finalization.ResponseBody,
					finalization.ExpiresAt,
				)
			},
		}, func(context.Context) (any, error) {
			cancelRequest()
			return map[string]any{"ok": true}, nil
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		require.Equal(t, 1, calls)
		require.NotZero(t, got.RecordID)
		require.Equal(t, "custom-finalizer|user:42", got.Scope)
		fingerprint, fingerprintErr := BuildIdempotencyFingerprint(
			"POST",
			"/custom-finalizer",
			"user:42",
			map[string]any{"value": "stored"},
		)
		require.NoError(t, fingerprintErr)
		require.Equal(t, fingerprint, got.RequestFingerprint)
		require.Equal(t, 200, got.ResponseStatus)
		require.JSONEq(t, `{"ok":true}`, got.ResponseBody)
		require.WithinDuration(t, time.Now().Add(time.Hour), got.ExpiresAt, 5*time.Second)
	})

	t.Run("error is wrapped with original cause", func(t *testing.T) {
		repo := newInMemoryIdempotencyRepo()
		coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
		finalizeErr := errors.New("custom finalization failed")

		result, err := coordinator.Execute(context.Background(), IdempotencyExecuteOptions{
			Scope:          "custom-finalizer-error",
			ActorScope:     "user:42",
			Method:         "POST",
			Route:          "/custom-finalizer-error",
			IdempotencyKey: "failure",
			Payload:        map[string]any{"value": 1},
			SuccessFinalizer: func(context.Context, IdempotencySuccessFinalization) error {
				return finalizeErr
			},
		}, func(context.Context) (any, error) {
			return map[string]any{"ok": true}, nil
		})

		require.Nil(t, result)
		require.ErrorIs(t, err, ErrIdempotencyStoreUnavail)
		require.ErrorIs(t, err, finalizeErr)
	})

	t.Run("not invoked for replay or execution failure", func(t *testing.T) {
		repo := newInMemoryIdempotencyRepo()
		coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
		base := IdempotencyExecuteOptions{
			Scope:          "custom-finalizer-skipped",
			ActorScope:     "user:42",
			Method:         "POST",
			Route:          "/custom-finalizer-skipped",
			IdempotencyKey: "replay",
			Payload:        map[string]any{"value": 1},
		}
		_, err := coordinator.Execute(context.Background(), base, func(context.Context) (any, error) {
			return map[string]any{"ok": true}, nil
		})
		require.NoError(t, err)

		calls := 0
		base.SuccessFinalizer = func(context.Context, IdempotencySuccessFinalization) error {
			calls++
			return nil
		}
		replayed, err := coordinator.Execute(context.Background(), base, func(context.Context) (any, error) {
			return nil, errors.New("replay executor must not run")
		})
		require.NoError(t, err)
		require.True(t, replayed.Replayed)

		base.IdempotencyKey = "execution-failure"
		executionErr := errors.New("execution failed")
		result, err := coordinator.Execute(context.Background(), base, func(context.Context) (any, error) {
			return nil, executionErr
		})
		require.Nil(t, result)
		require.ErrorIs(t, err, executionErr)
		require.Zero(t, calls)
	})
}

func TestIdempotencyCoordinator_CustomSuccessFinalizerUsesReclaimedRecordIdentity(t *testing.T) {
	now := time.Now()
	canonicalPayload := map[string]any{"operation_id": "canonical"}
	legacyPayload := map[string]any{"operation_id": "legacy"}

	for _, testCase := range []struct {
		name               string
		scope              string
		fingerprintActor   string
		fingerprintPayload any
		status             string
		expiresAt          time.Time
		lockedUntil        *time.Time
		configure          func(*IdempotencyExecuteOptions)
	}{
		{
			name:               "expired raw legacy record",
			scope:              "legacy-finalizer",
			fingerprintActor:   "account:42",
			fingerprintPayload: canonicalPayload,
			status:             IdempotencyStatusSucceeded,
			expiresAt:          now.Add(-time.Minute),
			configure: func(opts *IdempotencyExecuteOptions) {
				opts.LegacyActorScopes = []string{"account:42"}
			},
		},
		{
			name:               "retryable qualified legacy record",
			scope:              "legacy-finalizer|service_principal:7",
			fingerprintActor:   "service_principal:7",
			fingerprintPayload: legacyPayload,
			status:             IdempotencyStatusFailedRetryable,
			expiresAt:          now.Add(time.Hour),
			lockedUntil:        func() *time.Time { value := now.Add(-time.Minute); return &value }(),
			configure: func(opts *IdempotencyExecuteOptions) {
				opts.QualifiedLegacyRequests = []IdempotencyLegacyRequest{{
					ActorScope: "service_principal:7",
					Payload:    legacyPayload,
				}}
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := newInMemoryIdempotencyRepo()
			key := "reclaimed-identity"
			keyHash := HashIdempotencyKey(key)
			legacyFingerprint, err := BuildIdempotencyFingerprint(
				"POST",
				"/legacy-finalizer",
				testCase.fingerprintActor,
				testCase.fingerprintPayload,
			)
			require.NoError(t, err)
			repo.data[repo.key(testCase.scope, keyHash)] = &IdempotencyRecord{
				ID:                 73,
				Scope:              testCase.scope,
				IdempotencyKeyHash: keyHash,
				RequestFingerprint: legacyFingerprint,
				Status:             testCase.status,
				LockedUntil:        testCase.lockedUntil,
				ExpiresAt:          testCase.expiresAt,
			}

			var got IdempotencySuccessFinalization
			opts := IdempotencyExecuteOptions{
				Scope:          "legacy-finalizer",
				ActorScope:     "service_principal:7",
				Method:         "POST",
				Route:          "/legacy-finalizer",
				IdempotencyKey: key,
				Payload:        canonicalPayload,
				SuccessFinalizer: func(ctx context.Context, finalization IdempotencySuccessFinalization) error {
					got = finalization
					return repo.MarkSucceeded(
						ctx,
						finalization.RecordID,
						finalization.ResponseStatus,
						finalization.ResponseBody,
						finalization.ExpiresAt,
					)
				},
			}
			testCase.configure(&opts)

			result, err := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig()).Execute(
				context.Background(),
				opts,
				func(context.Context) (any, error) { return map[string]any{"ok": true}, nil },
			)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, int64(73), got.RecordID)
			require.Equal(t, testCase.scope, got.Scope)
			require.Equal(t, legacyFingerprint, got.RequestFingerprint)
		})
	}
}

type contextValidatingFinalizationRepo struct {
	*inMemoryIdempotencyRepo
	markSucceededContextErr error
	markSucceededDeadline   bool
	markFailedContextErr    error
	markFailedDeadline      bool
}

func (r *contextValidatingFinalizationRepo) MarkSucceeded(
	ctx context.Context,
	id int64,
	responseStatus int,
	responseBody string,
	expiresAt time.Time,
) error {
	r.markSucceededContextErr = ctx.Err()
	_, r.markSucceededDeadline = ctx.Deadline()
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.inMemoryIdempotencyRepo.MarkSucceeded(ctx, id, responseStatus, responseBody, expiresAt)
}

func (r *contextValidatingFinalizationRepo) MarkFailedRetryable(
	ctx context.Context,
	id int64,
	errorReason string,
	lockedUntil time.Time,
	expiresAt time.Time,
) error {
	r.markFailedContextErr = ctx.Err()
	_, r.markFailedDeadline = ctx.Deadline()
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.inMemoryIdempotencyRepo.MarkFailedRetryable(ctx, id, errorReason, lockedUntil, expiresAt)
}

func TestIdempotencyCoordinator_FinalizesAfterRequestCancellation(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		repo := &contextValidatingFinalizationRepo{inMemoryIdempotencyRepo: newInMemoryIdempotencyRepo()}
		coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
		requestCtx, cancelRequest := context.WithCancel(context.Background())

		result, err := coordinator.Execute(requestCtx, IdempotencyExecuteOptions{
			Scope:          "admin.system.update",
			IdempotencyKey: "disconnect-success",
			Method:         "POST",
			Route:          "/api/v1/admin/system/update",
			ActorScope:     "user:1",
			Payload:        map[string]any{"operation_id": "sysop-success"},
		}, func(context.Context) (any, error) {
			cancelRequest()
			return map[string]any{"ok": true}, nil
		})

		require.NoError(t, err)
		require.NotNil(t, result)
		require.NoError(t, repo.markSucceededContextErr)
		require.True(t, repo.markSucceededDeadline)
	})

	t.Run("retryable failure", func(t *testing.T) {
		repo := &contextValidatingFinalizationRepo{inMemoryIdempotencyRepo: newInMemoryIdempotencyRepo()}
		coordinator := NewIdempotencyCoordinator(repo, DefaultIdempotencyConfig())
		requestCtx, cancelRequest := context.WithCancel(context.Background())
		executionErr := errors.New("detached operation failed")

		result, err := coordinator.Execute(requestCtx, IdempotencyExecuteOptions{
			Scope:          "admin.system.rollback",
			IdempotencyKey: "disconnect-failure",
			Method:         "POST",
			Route:          "/api/v1/admin/system/rollback",
			ActorScope:     "user:1",
			Payload:        map[string]any{"operation_id": "sysop-failure"},
		}, func(context.Context) (any, error) {
			cancelRequest()
			return nil, executionErr
		})

		require.ErrorIs(t, err, executionErr)
		require.Nil(t, result)
		require.NoError(t, repo.markFailedContextErr)
		require.True(t, repo.markFailedDeadline)
	})
}

func TestIdempotencyCoordinator_HelperBranches(t *testing.T) {
	c := NewIdempotencyCoordinator(newInMemoryIdempotencyRepo(), IdempotencyConfig{
		DefaultTTL:           time.Hour,
		SystemOperationTTL:   time.Hour,
		ProcessingTimeout:    time.Second,
		FailedRetryBackoff:   time.Second,
		MaxStoredResponseLen: 12,
		ObserveOnly:          false,
	})

	// conflictWithRetryAfter without locked_until should return base error.
	base := ErrIdempotencyInProgress
	err := c.conflictWithRetryAfter(base, nil, time.Now())
	require.Equal(t, infraerrors.Code(base), infraerrors.Code(err))

	// marshalStoredResponse should truncate.
	body, err := c.marshalStoredResponse(map[string]any{"long": "abcdefghijklmnopqrstuvwxyz"})
	require.NoError(t, err)
	require.Contains(t, body, "...(truncated)")

	// decodeStoredResponse empty and invalid json.
	out, err := c.decodeStoredResponse(nil)
	require.NoError(t, err)
	_, ok := out.(map[string]any)
	require.True(t, ok)

	invalid := "{invalid"
	_, err = c.decodeStoredResponse(&invalid)
	require.Error(t, err)
}
