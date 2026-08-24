package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyAuthCacheRedisRoundTripPreservesRelativeTTLAndCreationTime(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache := &apiKeyCache{rdb: client}
	createdAt := time.Date(2026, time.August, 24, 1, 2, 3, 456000000, time.UTC)
	entry := &service.APIKeyAuthCacheEntry{Snapshot: &service.APIKeyAuthSnapshot{
		Version:        22,
		CacheCreatedAt: createdAt,
		APIKeyID:       11,
		UserID:         22,
	}}
	const ttl = 30 * time.Second
	const cacheKey = "v22-relative-deadline"

	require.NoError(t, cache.SetAuthCache(context.Background(), cacheKey, entry, ttl))
	require.Equal(t, ttl, server.TTL(apiKeyAuthCacheKey(cacheKey)))

	server.FastForward(7 * time.Second)
	require.Equal(t, 23*time.Second, server.TTL(apiKeyAuthCacheKey(cacheKey)))

	got, err := cache.GetAuthCache(context.Background(), cacheKey)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.NotNil(t, got.Snapshot)
	require.Equal(t, 22, got.Snapshot.Version)
	require.True(t, createdAt.Equal(got.Snapshot.CacheCreatedAt))
	require.Equal(t, int64(11), got.Snapshot.APIKeyID)
	require.Equal(t, int64(22), got.Snapshot.UserID)
	require.Equal(t, 23*time.Second, server.TTL(apiKeyAuthCacheKey(cacheKey)),
		"reading the serialized snapshot must not renew Redis's relative TTL")
}
