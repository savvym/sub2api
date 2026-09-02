package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type authorizationExpiryRepositoryStub struct {
	jobs          []AuthorizationExpiryJob
	claimErr      error
	processResult AuthorizationExpiryResult
	processErr    error
	retryErr      error
	releaseErr    error
	stats         AuthorizationExpiryStats
	statsErr      error
	claimedLimit  int
	processedIDs  []int64
	retriedIDs    []int64
	retryDelay    time.Duration
	retryError    string
	releasedIDs   []int64
	retryCtxErr   error
	releaseCtxErr error
	processFn     func(context.Context, AuthorizationExpiryJob) (AuthorizationExpiryResult, error)
	beforeRetry   func()
}

func (s *authorizationExpiryRepositoryStub) Claim(_ context.Context, _ string, limit int, _ time.Duration) ([]AuthorizationExpiryJob, error) {
	s.claimedLimit = limit
	return append([]AuthorizationExpiryJob(nil), s.jobs...), s.claimErr
}

func (s *authorizationExpiryRepositoryStub) ProcessClaimed(ctx context.Context, job AuthorizationExpiryJob, _ string) (AuthorizationExpiryResult, error) {
	s.processedIDs = append(s.processedIDs, job.ID)
	if s.processFn != nil {
		return s.processFn(ctx, job)
	}
	return s.processResult, s.processErr
}

func (s *authorizationExpiryRepositoryStub) RetryClaimed(ctx context.Context, id int64, _ string, delay time.Duration, lastError string) error {
	s.retriedIDs = append(s.retriedIDs, id)
	s.retryDelay = delay
	s.retryError = lastError
	if s.beforeRetry != nil {
		s.beforeRetry()
	}
	s.retryCtxErr = ctx.Err()
	return s.retryErr
}

func (s *authorizationExpiryRepositoryStub) ReleaseClaims(ctx context.Context, _ string, jobIDs []int64) error {
	s.releasedIDs = append(s.releasedIDs, jobIDs...)
	s.releaseCtxErr = ctx.Err()
	return s.releaseErr
}

func (s *authorizationExpiryRepositoryStub) Stats(context.Context) (AuthorizationExpiryStats, error) {
	return s.stats, s.statsErr
}

func TestAuthorizationExpiryWorkerProcessesAndRetriesDurableClaims(t *testing.T) {
	for _, testCase := range []struct {
		name          string
		processResult AuthorizationExpiryResult
		processErr    error
		processed     uint64
		retried       bool
	}{
		{name: "atomic processing succeeds", processResult: AuthorizationExpiryResult{Processed: true}, processed: 1},
		{name: "stale claim is an idempotent no-op", processResult: AuthorizationExpiryResult{}},
		{name: "processing failure releases claim", processErr: errors.New("serialization failure"), retried: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &authorizationExpiryRepositoryStub{
				jobs:          []AuthorizationExpiryJob{{ID: 7, SourceID: 11, SourceType: AuthorizationExpirySourceUserRole}},
				processResult: testCase.processResult,
				processErr:    testCase.processErr,
			}
			worker := NewAuthorizationExpiryWorker(repo)
			worker.poll()

			require.Equal(t, authorizationExpiryBatchSize, repo.claimedLimit)
			require.Equal(t, []int64{7}, repo.processedIDs)
			require.Equal(t, testCase.processed, worker.processed.Load())
			if testCase.retried {
				require.Equal(t, []int64{7}, repo.retriedIDs)
				require.Positive(t, repo.retryDelay)
				require.Contains(t, repo.retryError, testCase.processErr.Error())
			} else {
				require.Empty(t, repo.retriedIDs)
			}
		})
	}
}

func TestAuthorizationExpiryWorkerHealthUsesDatabaseTime(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	oldest := now.Add(-7 * time.Second)
	repo := &authorizationExpiryRepositoryStub{stats: AuthorizationExpiryStats{
		DatabaseTime: now, CoordinatorReady: true, Pending: 5, Due: 2, OldestDueExpiresAt: &oldest,
		MaxAttempts: 3, LastError: "previous retry",
	}}
	worker := NewAuthorizationExpiryWorker(repo)
	worker.running.Store(true)

	health := worker.Health(context.Background())
	require.True(t, health.Running)
	require.True(t, health.CoordinatorReady)
	require.Equal(t, int64(5), health.Pending)
	require.Equal(t, int64(2), health.Due)
	require.Equal(t, 7*time.Second, health.OldestDueLag)
	require.Equal(t, 3, health.MaxAttempts)
	require.Equal(t, "previous retry", health.LastError)
}

func TestAuthorizationExpiryWorkerHealthFailsClosedWhenCoordinatorIsNotReady(t *testing.T) {
	worker := NewAuthorizationExpiryWorker(&authorizationExpiryRepositoryStub{
		stats: AuthorizationExpiryStats{DatabaseTime: time.Now()},
	})

	health := worker.Health(context.Background())
	require.False(t, health.CoordinatorReady)
}

func TestAuthorizationExpiryWorkerClearsLastErrorAfterSuccessfulProcessing(t *testing.T) {
	attempt := 0
	repo := &authorizationExpiryRepositoryStub{
		jobs:  []AuthorizationExpiryJob{{ID: 7, SourceID: 11, SourceType: AuthorizationExpirySourceUserRole}},
		stats: AuthorizationExpiryStats{CoordinatorReady: true},
	}
	repo.processFn = func(context.Context, AuthorizationExpiryJob) (AuthorizationExpiryResult, error) {
		attempt++
		if attempt == 1 {
			return AuthorizationExpiryResult{}, errors.New("serialization failure")
		}
		return AuthorizationExpiryResult{Processed: true}, nil
	}
	worker := NewAuthorizationExpiryWorker(repo)

	worker.poll()
	require.Contains(t, worker.Health(context.Background()).LastError, "serialization failure")
	worker.poll()
	require.Empty(t, worker.Health(context.Background()).LastError)
	require.Equal(t, uint64(1), worker.processed.Load())
	require.Equal(t, uint64(1), worker.failures.Load())
}

func TestAuthorizationExpiryWorkerStopReleasesUnprocessedClaimsWithDetachedContext(t *testing.T) {
	processStarted := make(chan struct{})
	repo := &authorizationExpiryRepositoryStub{
		jobs: []AuthorizationExpiryJob{
			{ID: 7, SourceID: 11, SourceType: AuthorizationExpirySourceUserRole},
			{ID: 8, SourceID: 12, SourceType: AuthorizationExpirySourceUserRole},
		},
	}
	repo.processFn = func(ctx context.Context, _ AuthorizationExpiryJob) (AuthorizationExpiryResult, error) {
		close(processStarted)
		<-ctx.Done()
		return AuthorizationExpiryResult{}, ctx.Err()
	}
	worker := NewAuthorizationExpiryWorker(repo)
	worker.Start()

	select {
	case <-processStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("authorization expiry processing did not start")
	}
	worker.Stop()

	require.NoError(t, repo.releaseCtxErr)
	require.Empty(t, repo.retriedIDs)
	require.Equal(t, []int64{7, 8}, repo.releasedIDs)
	require.False(t, worker.running.Load())
}

func TestAuthorizationExpiryWorkerRetryUsesDetachedContext(t *testing.T) {
	repo := &authorizationExpiryRepositoryStub{
		jobs:       []AuthorizationExpiryJob{{ID: 7, SourceID: 11, SourceType: AuthorizationExpirySourceUserRole}},
		processErr: errors.New("serialization failure"),
	}
	worker := NewAuthorizationExpiryWorker(repo)
	repo.beforeRetry = worker.cancel

	worker.poll()

	require.NoError(t, repo.retryCtxErr)
	require.Equal(t, []int64{7}, repo.retriedIDs)
	require.Empty(t, repo.releasedIDs)
}

func TestAuthorizationExpiryWorkerReleasesBatchWhenRetryFails(t *testing.T) {
	repo := &authorizationExpiryRepositoryStub{
		jobs: []AuthorizationExpiryJob{
			{ID: 7, SourceID: 11, SourceType: AuthorizationExpirySourceUserRole},
			{ID: 8, SourceID: 12, SourceType: AuthorizationExpirySourceUserRole},
		},
		processErr: errors.New("serialization failure"),
		retryErr:   errors.New("retry unavailable"),
	}
	worker := NewAuthorizationExpiryWorker(repo)

	worker.poll()

	require.Equal(t, []int64{7}, repo.processedIDs)
	require.Equal(t, []int64{7}, repo.retriedIDs)
	require.Equal(t, []int64{7, 8}, repo.releasedIDs)
	require.NoError(t, repo.releaseCtxErr)
	require.Contains(t, worker.Health(context.Background()).LastError, "retry unavailable")
}

func TestAuthorizationExpiryRetryDelayIsBounded(t *testing.T) {
	require.Equal(t, 250*time.Millisecond, authorizationExpiryRetryDelay(1))
	require.Equal(t, 30*time.Second, authorizationExpiryRetryDelay(100))
}
