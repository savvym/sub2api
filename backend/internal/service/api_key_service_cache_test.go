//go:build unit

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type authRepoStub struct {
	getByKeyForAuth   func(ctx context.Context, key string) (*APIKey, error)
	listKeysByUserID  func(ctx context.Context, userID int64) ([]string, error)
	listKeysByGroupID func(ctx context.Context, groupID int64) ([]string, error)
}

func (s *authRepoStub) Create(ctx context.Context, key *APIKey) error {
	panic("unexpected Create call")
}

func (s *authRepoStub) GetByID(ctx context.Context, id int64) (*APIKey, error) {
	panic("unexpected GetByID call")
}

func (s *authRepoStub) GetKeyAndOwnerID(ctx context.Context, id int64) (string, int64, error) {
	panic("unexpected GetKeyAndOwnerID call")
}

func (s *authRepoStub) GetByKey(ctx context.Context, key string) (*APIKey, error) {
	panic("unexpected GetByKey call")
}

func (s *authRepoStub) GetByKeyForAuth(ctx context.Context, key string) (*APIKey, error) {
	if s.getByKeyForAuth == nil {
		panic("unexpected GetByKeyForAuth call")
	}
	return s.getByKeyForAuth(ctx, key)
}

func (s *authRepoStub) Update(ctx context.Context, key *APIKey, _ APIKeyUpdateFields) error {
	panic("unexpected Update call")
}

func (s *authRepoStub) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *authRepoStub) DeleteWithAudit(ctx context.Context, id int64) error {
	panic("unexpected DeleteWithAudit call")
}

func (s *authRepoStub) ListByUserID(ctx context.Context, userID int64, params pagination.PaginationParams, filters APIKeyListFilters) ([]APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserID call")
}

func (s *authRepoStub) VerifyOwnership(ctx context.Context, userID int64, apiKeyIDs []int64) ([]int64, error) {
	panic("unexpected VerifyOwnership call")
}

func (s *authRepoStub) CountByUserID(ctx context.Context, userID int64) (int64, error) {
	panic("unexpected CountByUserID call")
}

func (s *authRepoStub) ExistsByKey(ctx context.Context, key string) (bool, error) {
	panic("unexpected ExistsByKey call")
}

func (s *authRepoStub) ListByGroupID(ctx context.Context, groupID int64, params pagination.PaginationParams) ([]APIKey, *pagination.PaginationResult, error) {
	panic("unexpected ListByGroupID call")
}

func (s *authRepoStub) SearchAPIKeys(ctx context.Context, userID int64, keyword string, limit int) ([]APIKey, error) {
	panic("unexpected SearchAPIKeys call")
}

func (s *authRepoStub) ClearGroupIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected ClearGroupIDByGroupID call")
}
func (s *authRepoStub) UpdateGroupIDByUserAndGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (int64, error) {
	panic("unexpected UpdateGroupIDByUserAndGroup call")
}

func (s *authRepoStub) CountByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected CountByGroupID call")
}

func (s *authRepoStub) ListKeysByUserID(ctx context.Context, userID int64) ([]string, error) {
	if s.listKeysByUserID == nil {
		panic("unexpected ListKeysByUserID call")
	}
	return s.listKeysByUserID(ctx, userID)
}

func (s *authRepoStub) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	if s.listKeysByGroupID == nil {
		panic("unexpected ListKeysByGroupID call")
	}
	return s.listKeysByGroupID(ctx, groupID)
}

func (s *authRepoStub) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) (float64, error) {
	panic("unexpected IncrementQuotaUsed call")
}

func (s *authRepoStub) UpdateLastUsed(ctx context.Context, id int64, usedAt time.Time) error {
	panic("unexpected UpdateLastUsed call")
}
func (s *authRepoStub) IncrementRateLimitUsage(ctx context.Context, id int64, cost float64) error {
	panic("unexpected IncrementRateLimitUsage call")
}
func (s *authRepoStub) ResetRateLimitWindows(ctx context.Context, id int64) error {
	panic("unexpected ResetRateLimitWindows call")
}
func (s *authRepoStub) GetRateLimitData(ctx context.Context, id int64) (*APIKeyRateLimitData, error) {
	panic("unexpected GetRateLimitData call")
}

type authCacheStub struct {
	getAuthCache    func(ctx context.Context, key string) (*APIKeyAuthCacheEntry, error)
	deleteAuthCache func(ctx context.Context, key string) error
	setAuthKeys     []string
	setAuthTTLs     []time.Duration
	deleteAuthKeys  []string
}

func (s *authCacheStub) GetCreateAttemptCount(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

func (s *authCacheStub) IncrementCreateAttemptCount(ctx context.Context, userID int64) error {
	return nil
}

func (s *authCacheStub) DeleteCreateAttemptCount(ctx context.Context, userID int64) error {
	return nil
}

func (s *authCacheStub) IncrementDailyUsage(ctx context.Context, apiKey string) error {
	return nil
}

func (s *authCacheStub) SetDailyUsageExpiry(ctx context.Context, apiKey string, ttl time.Duration) error {
	return nil
}

func (s *authCacheStub) GetAuthCache(ctx context.Context, key string) (*APIKeyAuthCacheEntry, error) {
	if s.getAuthCache == nil {
		return nil, redis.Nil
	}
	return s.getAuthCache(ctx, key)
}

func (s *authCacheStub) SetAuthCache(ctx context.Context, key string, entry *APIKeyAuthCacheEntry, ttl time.Duration) error {
	s.setAuthKeys = append(s.setAuthKeys, key)
	s.setAuthTTLs = append(s.setAuthTTLs, ttl)
	return nil
}

func TestAPIKeyAuthPositiveTTLNeverExceedsHardLimitAfterJitter(t *testing.T) {
	for _, configured := range []time.Duration{time.Second, 30 * time.Second, 5 * time.Minute} {
		cfg := apiKeyAuthCacheConfig{jitterPercent: 100}
		for range 1_000 {
			ttl := cfg.positiveTTL(configured)
			require.Positive(t, ttl)
			require.LessOrEqual(t, ttl, apiKeyAuthPositiveTTLMax)
		}
	}
}

func TestAPIKeyAuthPositiveSnapshotTTLNeverOutlivesItsAbsoluteDeadline(t *testing.T) {
	createdAt := time.Date(2026, time.August, 24, 1, 2, 3, 0, time.UTC)
	snapshot := &APIKeyAuthSnapshot{
		Version:             apiKeyAuthSnapshotVersion,
		CacheCreatedAt:      createdAt,
		localCacheExpiresAt: createdAt.Add(apiKeyAuthPositiveTTLMax),
	}
	cfg := apiKeyAuthCacheConfig{}

	require.Equal(t, apiKeyAuthPositiveTTLMax, cfg.positiveSnapshotTTL(snapshot, 5*time.Minute, createdAt))
	require.Equal(t, 5*time.Second, cfg.positiveSnapshotTTL(snapshot, 5*time.Minute, createdAt.Add(25*time.Second)))
	require.Equal(t, 500*time.Millisecond, cfg.positiveSnapshotTTL(snapshot, 5*time.Minute, createdAt.Add(29500*time.Millisecond)))
	require.Zero(t, cfg.positiveSnapshotTTL(snapshot, 5*time.Minute, createdAt.Add(apiKeyAuthPositiveTTLMax)))
	require.Zero(t, cfg.positiveSnapshotTTL(snapshot, 5*time.Minute, createdAt.Add(-time.Nanosecond)),
		"a snapshot timestamp from a clock ahead of this instance must fail closed")
	restored := *snapshot
	restored.localCacheExpiresAt = time.Time{}
	require.Zero(t, cfg.positiveSnapshotTTL(&restored, 5*time.Minute, createdAt),
		"a deserialized L2 snapshot must not acquire a new process-local TTL")
	skewCapped := *snapshot
	skewNow := createdAt.Add(5 * time.Second)
	skewCapped.localCacheExpiresAt = skewNow.Add(2 * time.Second)
	require.Equal(t, 2*time.Second, cfg.positiveSnapshotTTL(&skewCapped, 5*time.Minute, skewNow),
		"the monotonic deadline must cap a longer wall-clock estimate")

	jittered := apiKeyAuthCacheConfig{jitterPercent: 100}
	for range 1_000 {
		ttl := jittered.positiveSnapshotTTL(snapshot, 5*time.Minute, createdAt.Add(25*time.Second))
		require.Positive(t, ttl)
		require.LessOrEqual(t, ttl, 5*time.Second)
	}
}

func TestAPIKeyServicePositiveAuthCacheCapsBothTiersAtThirtySeconds(t *testing.T) {
	cache := &authCacheStub{}
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, cache, &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L1Size:        100,
			L1TTLSeconds:  300,
			L2TTLSeconds:  300,
			JitterPercent: 100,
		},
	})
	now := time.Now()
	entry := &APIKeyAuthCacheEntry{Snapshot: &APIKeyAuthSnapshot{
		Version:             apiKeyAuthSnapshotVersion,
		CacheCreatedAt:      now.UTC(),
		localCacheExpiresAt: now.Add(apiKeyAuthPositiveTTLMax),
	}}

	for i := range 100 {
		cacheKey := svc.authCacheKey(fmt.Sprintf("positive-ttl-%d", i))
		svc.setAuthCacheEntry(context.Background(), cacheKey, entry, svc.authCfg.l2TTL)
	}
	svc.authCacheL1.Wait()

	require.Len(t, cache.setAuthTTLs, 100)
	for _, ttl := range cache.setAuthTTLs {
		require.Positive(t, ttl)
		require.LessOrEqual(t, ttl, apiKeyAuthPositiveTTLMax)
	}
	observedL1 := 0
	for i := range 100 {
		cacheKey := svc.authCacheKey(fmt.Sprintf("positive-ttl-%d", i))
		if ttl, ok := svc.authCacheL1.GetTTL(cacheKey); ok {
			observedL1++
			require.Positive(t, ttl)
			require.LessOrEqual(t, ttl, apiKeyAuthPositiveTTLMax)
		}
	}
	require.Positive(t, observedL1)
}

func TestAPIKeyServicePositiveEntryRewriteCannotRenewAbsoluteLifetime(t *testing.T) {
	cache := &authCacheStub{}
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, cache, &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L1Size:       100,
			L1TTLSeconds: 300,
			L2TTLSeconds: 300,
		},
	})
	now := time.Now()
	entry := &APIKeyAuthCacheEntry{Snapshot: &APIKeyAuthSnapshot{
		Version:             apiKeyAuthSnapshotVersion,
		CacheCreatedAt:      now.UTC().Add(-25 * time.Second),
		localCacheExpiresAt: now.Add(5 * time.Second),
	}}
	cacheKey := svc.authCacheKey("absolute-rewrite")
	remainingBefore := positiveAuthSnapshotRemaining(entry.Snapshot, time.Now())

	svc.setAuthCacheEntry(context.Background(), cacheKey, entry, svc.authCfg.l2TTL)
	svc.setAuthCacheEntry(context.Background(), cacheKey, entry, svc.authCfg.l2TTL)
	svc.authCacheL1.Wait()

	require.Len(t, cache.setAuthTTLs, 2)
	require.Positive(t, cache.setAuthTTLs[0])
	require.LessOrEqual(t, cache.setAuthTTLs[0], remainingBefore)
	require.LessOrEqual(t, cache.setAuthTTLs[1], cache.setAuthTTLs[0],
		"rewriting the same snapshot must consume, not renew, its lifetime")
	l1TTL, ok := svc.authCacheL1.GetTTL(cacheKey)
	require.True(t, ok)
	require.Positive(t, l1TTL)
	require.LessOrEqual(t, l1TTL, remainingBefore)
}

func TestAPIKeyServiceL2PositiveHitIsNotPromotedAcrossClockSkew(t *testing.T) {
	var l2Reads atomic.Int32
	receiverNow := time.Now().UTC()
	actualAge := 15 * time.Second
	creatorClockAhead := 10 * time.Second
	entry := &APIKeyAuthCacheEntry{Snapshot: &APIKeyAuthSnapshot{
		Version:        apiKeyAuthSnapshotVersion,
		CacheCreatedAt: receiverNow.Add(-(actualAge - creatorClockAhead)),
	}}
	wallRemaining := positiveAuthSnapshotRemaining(entry.Snapshot, receiverNow)
	require.Greater(t, wallRemaining, apiKeyAuthPositiveTTLMax-actualAge,
		"the receiver's wall clock underestimates the snapshot's real age")
	cache := &authCacheStub{getAuthCache: func(context.Context, string) (*APIKeyAuthCacheEntry, error) {
		l2Reads.Add(1)
		return entry, nil
	}}
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, cache, &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L1Size:       100,
			L1TTLSeconds: 300,
			L2TTLSeconds: 300,
		},
	})
	cacheKey := svc.authCacheKey("clock-skew-no-promotion")

	got, ok := svc.getAuthCacheEntry(context.Background(), cacheKey)
	require.True(t, ok)
	require.Same(t, entry, got)
	svc.authCacheL1.Wait()
	_, promoted := svc.authCacheL1.Get(cacheKey)
	require.False(t, promoted,
		"a receiver cannot safely promote a positive L2 hit using another instance's wall clock")

	got, ok = svc.getAuthCacheEntry(context.Background(), cacheKey)
	require.True(t, ok)
	require.Same(t, entry, got)
	require.Equal(t, int32(2), l2Reads.Load())
}

func (s *authCacheStub) DeleteAuthCache(ctx context.Context, key string) error {
	s.deleteAuthKeys = append(s.deleteAuthKeys, key)
	if s.deleteAuthCache != nil {
		return s.deleteAuthCache(ctx, key)
	}
	return nil
}

func (s *authCacheStub) PublishAuthCacheInvalidation(ctx context.Context, cacheKey string) error {
	return nil
}

func (s *authCacheStub) SubscribeAuthCacheInvalidation(ctx context.Context, handler func(cacheKey string)) error {
	return nil
}

func TestAPIKeyService_GetByKey_UsesL2Cache(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*APIKey, error) {
			return nil, errors.New("unexpected repo call")
		},
	}
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L2TTLSeconds:       60,
			NegativeTTLSeconds: 30,
		},
	}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)

	groupID := int64(9)
	cacheEntry := &APIKeyAuthCacheEntry{
		Snapshot: &APIKeyAuthSnapshot{
			Version:        apiKeyAuthSnapshotVersion,
			CacheCreatedAt: time.Now().UTC(),
			APIKeyID:       1,
			UserID:         2,
			GroupID:        &groupID,
			Status:         StatusActive,
			User: APIKeyAuthUserSnapshot{
				ID:          2,
				Status:      StatusActive,
				Role:        RoleUser,
				Balance:     10,
				Concurrency: 3,
			},
			Group: &APIKeyAuthGroupSnapshot{
				ID:                  groupID,
				Name:                "g",
				Platform:            PlatformAnthropic,
				Status:              StatusActive,
				SubscriptionType:    SubscriptionTypeStandard,
				RateMultiplier:      1,
				ModelRoutingEnabled: true,
				ModelRouting: map[string][]int64{
					"claude-opus-*": {1, 2},
				},
			},
		},
	}
	cache.getAuthCache = func(ctx context.Context, key string) (*APIKeyAuthCacheEntry, error) {
		return cacheEntry, nil
	}

	apiKey, err := svc.GetByKey(context.Background(), "k1")
	require.NoError(t, err)
	require.Equal(t, int64(1), apiKey.ID)
	require.Equal(t, int64(2), apiKey.User.ID)
	require.Equal(t, groupID, apiKey.Group.ID)
	require.True(t, apiKey.Group.ModelRoutingEnabled)
	require.Equal(t, map[string][]int64{"claude-opus-*": {1, 2}}, apiKey.Group.ModelRouting)
}

func TestAPIKeyService_GetByKeyEvictsInvalidPositiveLifetimeAndReloads(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name      string
		createdAt time.Time
	}{
		{name: "missing deadline"},
		{name: "expired", createdAt: now.Add(-apiKeyAuthPositiveTTLMax - time.Second)},
		{name: "future clock", createdAt: now.Add(time.Hour)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var repoCalls atomic.Int32
			stale := &APIKeyAuthCacheEntry{Snapshot: &APIKeyAuthSnapshot{
				Version:        apiKeyAuthSnapshotVersion,
				CacheCreatedAt: tt.createdAt,
				APIKeyID:       1,
				UserID:         2,
				Status:         StatusActive,
			}}
			cache := &authCacheStub{getAuthCache: func(context.Context, string) (*APIKeyAuthCacheEntry, error) {
				return stale, nil
			}}
			repo := &authRepoStub{getByKeyForAuth: func(context.Context, string) (*APIKey, error) {
				repoCalls.Add(1)
				return &APIKey{
					ID: 42, UserID: 7, Status: StatusActive,
					User: &User{ID: 7, Status: StatusActive, Role: RoleUser, Balance: 10, Concurrency: 2},
				}, nil
			}}
			svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, &config.Config{
				APIKeyAuth: config.APIKeyAuthCacheConfig{
					L1Size:       100,
					L1TTLSeconds: 300,
					L2TTLSeconds: 300,
				},
			})
			cacheKey := svc.authCacheKey("invalid-positive-" + tt.name)
			require.True(t, svc.authCacheL1.SetWithTTL(cacheKey, stale, 1, time.Minute))
			svc.authCacheL1.Wait()

			apiKey, err := svc.GetByKey(context.Background(), "invalid-positive-"+tt.name)
			require.NoError(t, err)
			require.Equal(t, int64(42), apiKey.ID)
			require.Equal(t, int32(1), repoCalls.Load())
			require.Empty(t, cache.deleteAuthKeys,
				"a stale reader must not delete a newer L2 value written after its read")
			require.Equal(t, []string{cacheKey}, cache.setAuthKeys)
			require.Len(t, cache.setAuthTTLs, 1)
			require.Positive(t, cache.setAuthTTLs[0])
			require.LessOrEqual(t, cache.setAuthTTLs[0], apiKeyAuthPositiveTTLMax)
		})
	}
}

func TestAPIKeyService_InvalidL2ReadDoesNotDeleteConcurrentNewerSnapshot(t *testing.T) {
	stale := &APIKeyAuthCacheEntry{Snapshot: &APIKeyAuthSnapshot{Version: apiKeyAuthSnapshotVersion}}
	newer := &APIKeyAuthCacheEntry{Snapshot: &APIKeyAuthSnapshot{
		Version:        apiKeyAuthSnapshotVersion,
		CacheCreatedAt: time.Now().UTC(),
	}}
	current := stale
	cache := &authCacheStub{}
	cache.getAuthCache = func(context.Context, string) (*APIKeyAuthCacheEntry, error) {
		read := current
		// Model a concurrent writer replacing the value after Redis served the
		// stale bytes but before this reader acts on them.
		current = newer
		return read, nil
	}
	cache.deleteAuthCache = func(context.Context, string) error {
		current = nil
		return nil
	}
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, cache, &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{L2TTLSeconds: 300},
	})

	entry, ok := svc.getAuthCacheEntry(context.Background(), svc.authCacheKey("stale-reader"))

	require.False(t, ok)
	require.Nil(t, entry)
	require.Same(t, newer, current, "the concurrent replacement must survive the stale read")
	require.Empty(t, cache.deleteAuthKeys)
}

func TestAPIKeyService_SnapshotRoundTrip_PreservesMessagesDispatchModelConfig(t *testing.T) {
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, nil, &config.Config{})
	groupID := int64(9)
	apiKey := &APIKey{
		ID:      1,
		UserID:  2,
		GroupID: &groupID,
		Key:     "k-roundtrip",
		Name:    "Audit Key",
		Status:  StatusActive,
		User: &User{
			ID:          2,
			Status:      StatusActive,
			Role:        RoleUser,
			Balance:     10,
			Concurrency: 3,
		},
		Group: &Group{
			ID:                    groupID,
			Name:                  "openai",
			Platform:              PlatformOpenAI,
			Status:                StatusActive,
			SubscriptionType:      SubscriptionTypeStandard,
			RateMultiplier:        1,
			AllowMessagesDispatch: true,
			DefaultMappedModel:    "gpt-5.4",
			MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{
				OpusMappedModel:   "gpt-5.4-nano",
				SonnetMappedModel: "gpt-5.3-codex",
				HaikuMappedModel:  "gpt-5.4-mini",
				ExactModelMappings: map[string]string{
					"claude-sonnet-4.5": "gpt-5.4-nano",
				},
			},
		},
	}

	snapshot := svc.snapshotFromAPIKey(context.Background(), apiKey)
	roundTrip := svc.snapshotToAPIKey(apiKey.Key, snapshot)

	require.NotNil(t, roundTrip)
	require.Equal(t, apiKey.Name, roundTrip.Name)
	require.NotNil(t, roundTrip.Group)
	require.Equal(t, apiKey.Group.MessagesDispatchModelConfig, roundTrip.Group.MessagesDispatchModelConfig)
}

func TestAPIKeyService_SnapshotRoundTrip_PreservesReasoningEffortPolicy(t *testing.T) {
	svc := NewAPIKeyService(nil, nil, nil, nil, nil, nil, &config.Config{})
	groupID := int64(9)
	apiKey := &APIKey{
		ID:      1,
		UserID:  2,
		GroupID: &groupID,
		Key:     "k-reasoning-policy",
		Status:  StatusActive,
		User: &User{
			ID:          2,
			Status:      StatusActive,
			Role:        RoleUser,
			Balance:     10,
			Concurrency: 3,
		},
		Group: &Group{
			ID:                 groupID,
			Name:               "composite",
			Platform:           PlatformComposite,
			Status:             StatusActive,
			SubscriptionType:   SubscriptionTypeStandard,
			RateMultiplier:     1,
			MaxReasoningEffort: "medium",
			ReasoningEffortMappings: []ReasoningEffortMapping{
				{From: "max", To: "xhigh"},
			},
		},
	}

	snapshot := svc.snapshotFromAPIKey(context.Background(), apiKey)
	roundTrip := svc.snapshotToAPIKey(apiKey.Key, snapshot)

	require.NotNil(t, roundTrip)
	require.NotNil(t, roundTrip.Group)
	require.Equal(t, PlatformComposite, roundTrip.Group.Platform)
	require.Equal(t, "medium", roundTrip.Group.MaxReasoningEffort)
	require.Equal(t, apiKey.Group.ReasoningEffortMappings, roundTrip.Group.ReasoningEffortMappings)
}

func TestAPIKeyService_GetByKey_IgnoresLegacyAuthCacheSnapshotWithoutMessagesDispatchConfig(t *testing.T) {
	cache := &authCacheStub{}
	var repoCalls int32
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*APIKey, error) {
			atomic.AddInt32(&repoCalls, 1)
			groupID := int64(9)
			return &APIKey{
				ID:      1,
				UserID:  2,
				GroupID: &groupID,
				Status:  StatusActive,
				User: &User{
					ID:          2,
					Status:      StatusActive,
					Role:        RoleUser,
					Balance:     10,
					Concurrency: 3,
				},
				Group: &Group{
					ID:                    groupID,
					Name:                  "openai",
					Platform:              PlatformOpenAI,
					Status:                StatusActive,
					Hydrated:              true,
					SubscriptionType:      SubscriptionTypeStandard,
					RateMultiplier:        1,
					AllowMessagesDispatch: true,
					DefaultMappedModel:    "gpt-5.4",
					MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{
						OpusMappedModel: "gpt-5.4-nano",
					},
				},
			}, nil
		},
	}
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L2TTLSeconds: 60,
		},
	}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)

	groupID := int64(9)
	cache.getAuthCache = func(ctx context.Context, key string) (*APIKeyAuthCacheEntry, error) {
		return &APIKeyAuthCacheEntry{
			Snapshot: &APIKeyAuthSnapshot{
				APIKeyID: 1,
				UserID:   2,
				GroupID:  &groupID,
				Status:   StatusActive,
				User: APIKeyAuthUserSnapshot{
					ID:          2,
					Status:      StatusActive,
					Role:        RoleUser,
					Balance:     10,
					Concurrency: 3,
				},
				Group: &APIKeyAuthGroupSnapshot{
					ID:                    groupID,
					Name:                  "openai",
					Platform:              PlatformOpenAI,
					Status:                StatusActive,
					SubscriptionType:      SubscriptionTypeStandard,
					RateMultiplier:        1,
					AllowMessagesDispatch: true,
					DefaultMappedModel:    "gpt-5.4",
				},
			},
		}, nil
	}

	apiKey, err := svc.GetByKey(context.Background(), "k-legacy")
	require.NoError(t, err)
	require.Equal(t, int32(1), atomic.LoadInt32(&repoCalls))
	require.NotNil(t, apiKey.Group)
	require.Equal(t, "gpt-5.4-nano", apiKey.Group.MessagesDispatchModelConfig.OpusMappedModel)
}

func TestAPIKeyService_GetByKey_NegativeCache(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*APIKey, error) {
			return nil, errors.New("unexpected repo call")
		},
	}
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L2TTLSeconds:       60,
			NegativeTTLSeconds: 30,
		},
	}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
	cache.getAuthCache = func(ctx context.Context, key string) (*APIKeyAuthCacheEntry, error) {
		return &APIKeyAuthCacheEntry{NotFound: true}, nil
	}

	_, err := svc.GetByKey(context.Background(), "missing")
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
}

func TestAPIKeyService_GetByKey_CacheMissStoresL2(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*APIKey, error) {
			return &APIKey{
				ID:     5,
				UserID: 7,
				Status: StatusActive,
				User: &User{
					ID:          7,
					Status:      StatusActive,
					Role:        RoleUser,
					Balance:     12,
					Concurrency: 2,
				},
			}, nil
		},
	}
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L2TTLSeconds:       60,
			NegativeTTLSeconds: 30,
		},
	}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
	cache.getAuthCache = func(ctx context.Context, key string) (*APIKeyAuthCacheEntry, error) {
		return nil, redis.Nil
	}

	apiKey, err := svc.GetByKey(context.Background(), "k2")
	require.NoError(t, err)
	require.Equal(t, int64(5), apiKey.ID)
	require.Len(t, cache.setAuthKeys, 1)
}

func TestAPIKeyService_GetByKey_UsesL1Cache(t *testing.T) {
	var calls int32
	cache := &authCacheStub{}
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*APIKey, error) {
			atomic.AddInt32(&calls, 1)
			return &APIKey{
				ID:     21,
				UserID: 3,
				Status: StatusActive,
				User: &User{
					ID:          3,
					Status:      StatusActive,
					Role:        RoleUser,
					Balance:     5,
					Concurrency: 2,
				},
			}, nil
		},
	}
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L1Size:       1000,
			L1TTLSeconds: 60,
		},
	}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
	require.NotNil(t, svc.authCacheL1)

	_, err := svc.GetByKey(context.Background(), "k-l1")
	require.NoError(t, err)
	svc.authCacheL1.Wait()
	cacheKey := svc.authCacheKey("k-l1")
	_, ok := svc.authCacheL1.Get(cacheKey)
	require.True(t, ok)
	_, err = svc.GetByKey(context.Background(), "k-l1")
	require.NoError(t, err)
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))
}

func TestAPIKeyService_InvalidateAuthCacheByUserID(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		listKeysByUserID: func(ctx context.Context, userID int64) ([]string, error) {
			return []string{"k1", "k2"}, nil
		},
	}
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L2TTLSeconds:       60,
			NegativeTTLSeconds: 30,
		},
	}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)

	svc.InvalidateAuthCacheByUserID(context.Background(), 7)
	require.Len(t, cache.deleteAuthKeys, 2)
}

func TestAPIKeyService_InvalidateAuthCacheByGroupID(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		listKeysByGroupID: func(ctx context.Context, groupID int64) ([]string, error) {
			return []string{"k1", "k2"}, nil
		},
	}
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L2TTLSeconds: 60,
		},
	}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)

	svc.InvalidateAuthCacheByGroupID(context.Background(), 9)
	require.Len(t, cache.deleteAuthKeys, 2)
}

func TestAPIKeyService_InvalidateAuthCacheByKey(t *testing.T) {
	cache := &authCacheStub{}
	repo := &authRepoStub{
		listKeysByUserID: func(ctx context.Context, userID int64) ([]string, error) {
			return nil, nil
		},
	}
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L2TTLSeconds: 60,
		},
	}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)

	svc.InvalidateAuthCacheByKey(context.Background(), "k1")
	require.Len(t, cache.deleteAuthKeys, 1)
}

func TestAPIKeyService_GetByKey_CachesNegativeOnRepoMiss(t *testing.T) {
	var repoCalls atomic.Int32
	cache := &authCacheStub{}
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*APIKey, error) {
			repoCalls.Add(1)
			return nil, ErrAPIKeyNotFound
		},
	}
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			L1Size:             100,
			L1TTLSeconds:       60,
			L2TTLSeconds:       60,
			NegativeTTLSeconds: 30,
		},
	}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)
	cache.getAuthCache = func(ctx context.Context, key string) (*APIKeyAuthCacheEntry, error) {
		return nil, redis.Nil
	}

	_, err := svc.GetByKey(context.Background(), "missing")
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	require.Empty(t, cache.setAuthKeys, "attacker-controlled misses must not be written to Redis")
	svc.authNegativeCacheL1.Wait()
	_, err = svc.GetByKey(context.Background(), "missing")
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	require.Equal(t, int32(1), repoCalls.Load())
}

func TestAPIKeyService_GetByKeyRejectsInvalidLengthBeforeCaches(t *testing.T) {
	var cacheCalls atomic.Int32
	cache := &authCacheStub{getAuthCache: func(context.Context, string) (*APIKeyAuthCacheEntry, error) {
		cacheCalls.Add(1)
		return nil, redis.Nil
	}}
	repo := &authRepoStub{getByKeyForAuth: func(context.Context, string) (*APIKey, error) {
		t.Fatal("invalid credential reached repository")
		return nil, nil
	}}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{L2TTLSeconds: 60}})

	for _, key := range []string{"", strings.Repeat("x", MaxAPIKeyCredentialBytes+1)} {
		_, err := svc.GetByKey(context.Background(), key)
		require.ErrorIs(t, err, ErrAPIKeyNotFound)
	}
	require.Zero(t, cacheCalls.Load())
}

func TestAPIKeyService_GetByKeyAllowsMaximumLength(t *testing.T) {
	key := strings.Repeat("x", MaxAPIKeyCredentialBytes)
	var repoCalls atomic.Int32
	repo := &authRepoStub{getByKeyForAuth: func(_ context.Context, got string) (*APIKey, error) {
		repoCalls.Add(1)
		require.Equal(t, key, got)
		return nil, ErrAPIKeyNotFound
	}}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, nil, &config.Config{})
	_, err := svc.GetByKey(context.Background(), key)
	require.ErrorIs(t, err, ErrAPIKeyNotFound)
	require.Equal(t, int32(1), repoCalls.Load())
}

func TestAPIKeyService_AuthLookupBulkheadRejectsExcessMisses(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	repo := &authRepoStub{getByKeyForAuth: func(context.Context, string) (*APIKey, error) {
		close(entered)
		<-release
		return nil, ErrAPIKeyNotFound
	}}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, nil, &config.Config{APIKeyAuth: config.APIKeyAuthCacheConfig{LookupConcurrency: 1}})

	done := make(chan error, 1)
	go func() {
		_, err := svc.GetByKey(context.Background(), "first")
		done <- err
	}()
	<-entered

	_, err := svc.GetByKey(context.Background(), "second")
	require.ErrorIs(t, err, ErrAPIKeyAuthOverloaded)
	metrics := svc.AuthLookupMetrics()
	require.Equal(t, uint64(2), metrics.Total)
	require.Equal(t, uint64(1), metrics.Rejected)
	require.Equal(t, int64(1), metrics.InFlight)
	require.Equal(t, 1, metrics.Capacity)

	close(release)
	require.ErrorIs(t, <-done, ErrAPIKeyNotFound)
}

func TestAPIKeyService_GetByKey_SingleflightCollapses(t *testing.T) {
	var calls int32
	cache := &authCacheStub{}
	repo := &authRepoStub{
		getByKeyForAuth: func(ctx context.Context, key string) (*APIKey, error) {
			atomic.AddInt32(&calls, 1)
			time.Sleep(50 * time.Millisecond)
			return &APIKey{
				ID:     11,
				UserID: 2,
				Status: StatusActive,
				User: &User{
					ID:          2,
					Status:      StatusActive,
					Role:        RoleUser,
					Balance:     1,
					Concurrency: 1,
				},
			}, nil
		},
	}
	cfg := &config.Config{
		APIKeyAuth: config.APIKeyAuthCacheConfig{
			Singleflight: true,
		},
	}
	svc := NewAPIKeyService(repo, nil, nil, nil, nil, cache, cfg)

	start := make(chan struct{})
	wg := sync.WaitGroup{}
	errs := make([]error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			_, err := svc.GetByKey(context.Background(), "k1")
			errs[idx] = err
		}(i)
	}
	close(start)
	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), atomic.LoadInt32(&calls))
}
