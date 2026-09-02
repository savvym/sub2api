package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type authorizationPropagationStatsStub struct {
	stats AuthorizationPropagationStats
	err   error
}

func (s authorizationPropagationStatsStub) Snapshot(context.Context) (AuthorizationPropagationStats, error) {
	return s.stats, s.err
}

type authorizationPropagationWorkerStub struct {
	name    string
	running bool
}

func (s authorizationPropagationWorkerStub) AuthorizationPropagationWorkerState() AuthorizationPropagationWorkerState {
	return AuthorizationPropagationWorkerState{Name: s.name, Running: s.running}
}

func TestAuthorizationPropagationGuardTargetAndSafetyThresholds(t *testing.T) {
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	workers := []AuthorizationPropagationWorker{
		authorizationPropagationWorkerStub{name: "auth_cache_invalidation", running: true},
		authorizationPropagationWorkerStub{name: "scheduler_outbox", running: true},
		authorizationPropagationWorkerStub{name: "authorization_expiry", running: true},
	}

	for _, testCase := range []struct {
		name             string
		oldest           time.Time
		targetMet        bool
		expansionAllowed bool
	}{
		{name: "within target", oldest: now.Add(-4 * time.Second), targetMet: true, expansionAllowed: true},
		{name: "target missed remains inside safety limit", oldest: now.Add(-5 * time.Second), targetMet: false, expansionAllowed: true},
		{name: "safety limit blocks expansion", oldest: now.Add(-30 * time.Second), targetMet: false, expansionAllowed: false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			oldest := testCase.oldest
			guard := newAuthorizationPropagationGuard(authorizationPropagationStatsStub{stats: AuthorizationPropagationStats{
				DatabaseTime:           now,
				ExpiryCoordinatorReady: true,
				AuthPrimary: AuthorizationPropagationQueueStats{
					Pending: 1, Ready: 1, OldestRelevantAt: &oldest,
				},
			}}, workers...)

			health := guard.Health(context.Background())
			require.Equal(t, testCase.targetMet, health.TargetMet)
			require.Equal(t, testCase.expansionAllowed, health.ExpansionAllowed)
			require.Equal(t, testCase.expansionAllowed, health.SafetyLimitMet)
			require.Equal(t, now.Sub(oldest), health.AuthPrimary.OldestLag)
			if testCase.expansionAllowed {
				require.NoError(t, guard.RequireExpansion(context.Background()))
			} else {
				err := guard.RequireExpansion(context.Background())
				require.ErrorIs(t, err, ErrAuthorizationPropagationDegraded)
				status, _ := infraerrors.ToHTTP(err)
				require.Equal(t, http.StatusServiceUnavailable, status)
			}
		})
	}
}

func TestAuthorizationPropagationGuardReportsSafetyPassSeparately(t *testing.T) {
	now := time.Now().UTC()
	stage0Oldest := now.Add(-time.Second)
	stage1Oldest := now.Add(-31 * time.Second)
	guard := newAuthorizationPropagationGuard(authorizationPropagationStatsStub{stats: AuthorizationPropagationStats{
		DatabaseTime:           now,
		ExpiryCoordinatorReady: true,
		AuthPrimary: AuthorizationPropagationQueueStats{
			Pending: 1, Ready: 1, OldestRelevantAt: &stage0Oldest,
		},
		AuthSafetyPass: AuthorizationPropagationQueueStats{
			Pending: 8, Ready: 2, OldestRelevantAt: &stage1Oldest,
		},
	}},
		authorizationPropagationWorkerStub{name: "auth_cache_invalidation", running: true},
		authorizationPropagationWorkerStub{name: "scheduler_outbox", running: true},
		authorizationPropagationWorkerStub{name: "authorization_expiry", running: true},
	)

	health := guard.Health(context.Background())
	require.True(t, health.TargetMet, "the intentionally delayed safety pass is not part of the five-second target")
	require.False(t, health.ExpansionAllowed)
	require.Equal(t, int64(1), health.AuthPrimary.Pending)
	require.Equal(t, int64(8), health.AuthSafetyPass.Pending)
	require.Equal(t, int64(2), health.AuthSafetyPass.Ready)
	require.Contains(t, health.DegradedReasons, "auth_safety_pass_limit_exceeded")
}

func TestAuthorizationPropagationGuardFailsClosedOnUnknownRuntimeState(t *testing.T) {
	now := time.Now().UTC()
	for _, testCase := range []struct {
		name    string
		repo    AuthorizationPropagationStatsRepository
		workers []AuthorizationPropagationWorker
		reason  string
	}{
		{
			name: "stats error",
			repo: authorizationPropagationStatsStub{err: errors.New("database unavailable")},
			workers: []AuthorizationPropagationWorker{
				authorizationPropagationWorkerStub{name: "auth_cache_invalidation", running: true},
				authorizationPropagationWorkerStub{name: "scheduler_outbox", running: true},
				authorizationPropagationWorkerStub{name: "authorization_expiry", running: true},
			},
			reason: "propagation_stats_error",
		},
		{
			name: "worker stopped",
			repo: authorizationPropagationStatsStub{stats: AuthorizationPropagationStats{
				DatabaseTime:           now,
				ExpiryCoordinatorReady: true,
			}},
			workers: []AuthorizationPropagationWorker{
				authorizationPropagationWorkerStub{name: "auth_cache_invalidation", running: false},
				authorizationPropagationWorkerStub{name: "scheduler_outbox", running: true},
				authorizationPropagationWorkerStub{name: "authorization_expiry", running: true},
			},
			reason: "auth_cache_invalidation_worker_not_running",
		},
		{
			name: "worker missing",
			repo: authorizationPropagationStatsStub{stats: AuthorizationPropagationStats{
				DatabaseTime:           now,
				ExpiryCoordinatorReady: true,
			}},
			workers: []AuthorizationPropagationWorker{
				authorizationPropagationWorkerStub{name: "scheduler_outbox", running: true},
				authorizationPropagationWorkerStub{name: "authorization_expiry", running: true},
			},
			reason: "auth_cache_invalidation_worker_missing",
		},
		{
			name: "expiry coordinator unavailable",
			repo: authorizationPropagationStatsStub{stats: AuthorizationPropagationStats{DatabaseTime: now}},
			workers: []AuthorizationPropagationWorker{
				authorizationPropagationWorkerStub{name: "auth_cache_invalidation", running: true},
				authorizationPropagationWorkerStub{name: "scheduler_outbox", running: true},
				authorizationPropagationWorkerStub{name: "authorization_expiry", running: true},
			},
			reason: "authorization_expiry_coordinator_unavailable",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			guard := newAuthorizationPropagationGuard(testCase.repo, testCase.workers...)
			health := guard.Health(context.Background())
			require.False(t, health.ExpansionAllowed)
			require.Contains(t, health.DegradedReasons, testCase.reason)
			if testCase.reason == "authorization_expiry_coordinator_unavailable" {
				require.False(t, health.ExpiryCoordinatorReady)
				require.False(t, health.TargetMet)
				require.False(t, health.SafetyLimitMet)
				require.Equal(t, []string{testCase.reason}, health.DegradedReasons)
			}
			require.ErrorIs(t, guard.RequireExpansion(context.Background()), ErrAuthorizationPropagationDegraded)
		})
	}
}
