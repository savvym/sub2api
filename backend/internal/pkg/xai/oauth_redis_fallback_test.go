//go:build unit

package xai

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/oauthflow"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestSessionStoreRedisFallbackIsLimitedToFailedWrites(t *testing.T) {
	mr := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	store := NewRedisSessionStore(client)
	defer store.Stop()
	session := func(state string) *OAuthSession { return &OAuthSession{State: state, CreatedAt: time.Now()} }

	store.Set("remote", session("remote"))
	require.NoError(t, store.remote.Delete(context.Background(), "remote"))
	_, ok := store.Get("remote")
	require.False(t, ok, "a remote miss must not revive the stale local copy")

	mr.Close()
	store.Set("local-only", session("local"))
	got, ok := store.Get("local-only")
	require.True(t, ok)
	require.Equal(t, "local", got.State)
	require.True(t, store.TryConsumeSession("local-only"))
	require.False(t, store.TryConsumeSession("local-only"))
}

func TestSessionStoreRedisRoundTripsBindingAndConsumesAcrossInstances(t *testing.T) {
	mr := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	clientB := redis.NewClient(&redis.Options{Addr: mr.Addr(), MaxRetries: -1})
	t.Cleanup(func() {
		_ = clientA.Close()
		_ = clientB.Close()
	})
	storeA := NewRedisSessionStore(clientA)
	storeB := NewRedisSessionStore(clientB)
	t.Cleanup(storeA.Stop)
	t.Cleanup(storeB.Stop)

	binding, err := oauthflow.NewPlatformBinding("service_principal:73")
	require.NoError(t, err)
	proxyID := int64(17)
	storeA.Set("shared", &OAuthSession{
		State:        "state",
		CodeVerifier: "verifier",
		ProxyID:      &proxyID,
		ProxyURL:     "http://proxy.example:8080",
		RedirectURI:  DefaultRedirectURI,
		Binding:      binding,
		CreatedAt:    time.Now(),
	})

	got, ok := storeB.Get("shared")
	require.True(t, ok)
	require.True(t, got.Binding.Equal(binding))
	require.Equal(t, &proxyID, got.ProxyID)

	var winners atomic.Int32
	start := make(chan struct{})
	var workers sync.WaitGroup
	for _, store := range []*SessionStore{storeA, storeB} {
		workers.Add(1)
		go func(store *SessionStore) {
			defer workers.Done()
			<-start
			if store.TryConsumeSession("shared") {
				winners.Add(1)
			}
		}(store)
	}
	close(start)
	workers.Wait()
	require.Equal(t, int32(1), winners.Load())
}
