package antigravity

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionStoreTryConsumeSessionConcurrent(t *testing.T) {
	store := NewSessionStore()
	t.Cleanup(store.Stop)
	store.Set("session", &OAuthSession{CreatedAt: time.Now()})

	var winners atomic.Int32
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 64 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			if store.TryConsumeSession("session") {
				winners.Add(1)
			}
		}()
	}
	close(start)
	workers.Wait()

	if got := winners.Load(); got != 1 {
		t.Fatalf("consume winners = %d, want 1", got)
	}
	if store.TryConsumeSession("session") {
		t.Fatal("consumed session was accepted again")
	}
}
