package service

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	AuthorizationPropagationTarget = 5 * time.Second
	AuthorizationPropagationLimit  = 30 * time.Second
)

var ErrAuthorizationPropagationDegraded = infraerrors.New(
	http.StatusServiceUnavailable,
	"AUTHORIZATION_PROPAGATION_DEGRADED",
	"authorization propagation is degraded",
)

type AuthorizationPropagationQueueStats struct {
	Pending          int64
	Ready            int64
	OldestRelevantAt *time.Time
}

type AuthorizationPropagationStats struct {
	DatabaseTime           time.Time
	AuthPrimary            AuthorizationPropagationQueueStats
	AuthSafetyPass         AuthorizationPropagationQueueStats
	Scheduler              AuthorizationPropagationQueueStats
	Expiry                 AuthorizationPropagationQueueStats
	ExpiryCoordinatorReady bool
}

type AuthorizationPropagationStatsRepository interface {
	Snapshot(ctx context.Context) (AuthorizationPropagationStats, error)
}

type AuthorizationPropagationWorkerState struct {
	Name    string `json:"name"`
	Running bool   `json:"running"`
}

type AuthorizationPropagationWorker interface {
	AuthorizationPropagationWorkerState() AuthorizationPropagationWorkerState
}

type AuthorizationPropagationQueueHealth struct {
	Pending          int64         `json:"pending"`
	Ready            int64         `json:"ready"`
	OldestRelevantAt *time.Time    `json:"oldest_relevant_at,omitempty"`
	OldestLag        time.Duration `json:"oldest_lag"`
}

type AuthorizationPropagationHealth struct {
	DatabaseTime           time.Time                             `json:"database_time,omitempty"`
	Target                 time.Duration                         `json:"target"`
	SafetyLimit            time.Duration                         `json:"safety_limit"`
	TargetMet              bool                                  `json:"target_met"`
	SafetyLimitMet         bool                                  `json:"safety_limit_met"`
	ExpansionAllowed       bool                                  `json:"expansion_allowed"`
	AuthPrimary            AuthorizationPropagationQueueHealth   `json:"auth_primary"`
	AuthSafetyPass         AuthorizationPropagationQueueHealth   `json:"auth_safety_pass"`
	Scheduler              AuthorizationPropagationQueueHealth   `json:"scheduler"`
	Expiry                 AuthorizationPropagationQueueHealth   `json:"expiry"`
	ExpiryCoordinatorReady bool                                  `json:"expiry_coordinator_ready"`
	Workers                []AuthorizationPropagationWorkerState `json:"workers"`
	DegradedReasons        []string                              `json:"degraded_reasons,omitempty"`
	StatsError             string                                `json:"stats_error,omitempty"`
}

type AuthorizationPropagationGuard struct {
	repo    AuthorizationPropagationStatsRepository
	workers []AuthorizationPropagationWorker
}

func newAuthorizationPropagationGuard(
	repo AuthorizationPropagationStatsRepository,
	workers ...AuthorizationPropagationWorker,
) *AuthorizationPropagationGuard {
	return &AuthorizationPropagationGuard{repo: repo, workers: workers}
}

func ProvideAuthorizationPropagationGuard(
	repo AuthorizationPropagationStatsRepository,
	authWorker *AuthCacheInvalidationWorker,
	schedulerWorker *SchedulerSnapshotService,
	expiryWorker *AuthorizationExpiryWorker,
) *AuthorizationPropagationGuard {
	return newAuthorizationPropagationGuard(repo, authWorker, schedulerWorker, expiryWorker)
}

func (g *AuthorizationPropagationGuard) RequireExpansion(ctx context.Context) error {
	health := g.Health(ctx)
	if health.ExpansionAllowed {
		return nil
	}
	if health.StatsError != "" {
		return ErrAuthorizationPropagationDegraded.WithCause(fmt.Errorf("propagation stats: %s", health.StatsError))
	}
	if len(health.DegradedReasons) > 0 {
		return ErrAuthorizationPropagationDegraded.WithCause(errorsFromPropagationReasons(health.DegradedReasons))
	}
	return ErrAuthorizationPropagationDegraded
}

func errorsFromPropagationReasons(reasons []string) error {
	return fmt.Errorf("%s", strings.Join(reasons, ", "))
}

func (g *AuthorizationPropagationGuard) Health(ctx context.Context) AuthorizationPropagationHealth {
	health := AuthorizationPropagationHealth{
		Target:      AuthorizationPropagationTarget,
		SafetyLimit: AuthorizationPropagationLimit,
	}
	if g == nil || g.repo == nil || ctx == nil {
		health.DegradedReasons = []string{"propagation_stats_unavailable"}
		return health
	}

	workerNames := make(map[string]struct{}, len(g.workers))
	workersHealthy := true
	for _, worker := range g.workers {
		state := AuthorizationPropagationWorkerState{}
		if worker != nil {
			state = worker.AuthorizationPropagationWorkerState()
		}
		state.Name = strings.TrimSpace(state.Name)
		if state.Name == "" {
			state.Name = "unknown"
		}
		workerNames[state.Name] = struct{}{}
		health.Workers = append(health.Workers, state)
		if !state.Running {
			workersHealthy = false
			health.DegradedReasons = append(health.DegradedReasons, state.Name+"_worker_not_running")
		}
	}
	for _, required := range []string{
		"auth_cache_invalidation",
		"scheduler_outbox",
		"authorization_expiry",
	} {
		if _, ok := workerNames[required]; !ok {
			workersHealthy = false
			health.DegradedReasons = append(health.DegradedReasons, required+"_worker_missing")
		}
	}
	sort.Slice(health.Workers, func(i, j int) bool {
		return health.Workers[i].Name < health.Workers[j].Name
	})

	stats, err := g.repo.Snapshot(ctx)
	if err != nil {
		health.StatsError = boundedAuthorizationPropagationError(err)
		health.DegradedReasons = append(health.DegradedReasons, "propagation_stats_error")
		health.DegradedReasons = uniqueSortedPropagationReasons(health.DegradedReasons)
		return health
	}
	health.DatabaseTime = stats.DatabaseTime
	health.AuthPrimary = authorizationPropagationQueueHealth(stats.DatabaseTime, stats.AuthPrimary)
	health.AuthSafetyPass = authorizationPropagationQueueHealth(stats.DatabaseTime, stats.AuthSafetyPass)
	health.Scheduler = authorizationPropagationQueueHealth(stats.DatabaseTime, stats.Scheduler)
	health.Expiry = authorizationPropagationQueueHealth(stats.DatabaseTime, stats.Expiry)
	health.ExpiryCoordinatorReady = stats.ExpiryCoordinatorReady
	if !health.ExpiryCoordinatorReady {
		workersHealthy = false
		health.DegradedReasons = append(
			health.DegradedReasons,
			"authorization_expiry_coordinator_unavailable",
		)
	}

	primaryLags := []struct {
		name string
		lag  time.Duration
	}{
		{name: "auth_primary", lag: health.AuthPrimary.OldestLag},
		{name: "scheduler", lag: health.Scheduler.OldestLag},
		{name: "expiry", lag: health.Expiry.OldestLag},
	}
	health.TargetMet = workersHealthy
	health.SafetyLimitMet = workersHealthy
	for _, queue := range primaryLags {
		if queue.lag >= AuthorizationPropagationTarget {
			health.TargetMet = false
		}
		if queue.lag >= AuthorizationPropagationLimit {
			health.SafetyLimitMet = false
			health.DegradedReasons = append(health.DegradedReasons, queue.name+"_safety_limit_exceeded")
		}
	}
	if health.AuthSafetyPass.OldestLag >= AuthorizationPropagationLimit {
		health.SafetyLimitMet = false
		health.DegradedReasons = append(health.DegradedReasons, "auth_safety_pass_limit_exceeded")
	}
	health.ExpansionAllowed = health.SafetyLimitMet
	health.DegradedReasons = uniqueSortedPropagationReasons(health.DegradedReasons)
	return health
}

func authorizationPropagationQueueHealth(
	databaseTime time.Time,
	stats AuthorizationPropagationQueueStats,
) AuthorizationPropagationQueueHealth {
	health := AuthorizationPropagationQueueHealth{
		Pending: stats.Pending,
		Ready:   stats.Ready,
	}
	if stats.OldestRelevantAt == nil {
		return health
	}
	value := stats.OldestRelevantAt.UTC()
	health.OldestRelevantAt = &value
	health.OldestLag = databaseTime.Sub(value)
	if health.OldestLag < 0 {
		health.OldestLag = 0
	}
	return health
}

func boundedAuthorizationPropagationError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 1024 {
		return message[:1024]
	}
	return message
}

func uniqueSortedPropagationReasons(values []string) []string {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func (w *AuthCacheInvalidationWorker) AuthorizationPropagationWorkerState() AuthorizationPropagationWorkerState {
	return AuthorizationPropagationWorkerState{
		Name:    "auth_cache_invalidation",
		Running: w != nil && w.running.Load(),
	}
}

func (w *AuthorizationExpiryWorker) AuthorizationPropagationWorkerState() AuthorizationPropagationWorkerState {
	return AuthorizationPropagationWorkerState{
		Name:    "authorization_expiry",
		Running: w != nil && w.running.Load(),
	}
}
