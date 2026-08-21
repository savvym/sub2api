package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

type dashboardGroupUsageRepoStub struct {
	UsageLogRepository
	calls   int
	results []usagestats.GroupUsageSummary
}

func (s *dashboardGroupUsageRepoStub) GetAllGroupUsageSummary(context.Context, time.Time) ([]usagestats.GroupUsageSummary, error) {
	s.calls++
	return s.results, nil
}

func TestDashboardGroupUsageSummaryRequiresActorBeforeRepositoryAccess(t *testing.T) {
	repo := &dashboardGroupUsageRepoStub{}
	svc := &DashboardService{usageRepo: repo}

	results, err := svc.GetGroupUsageSummary(context.Background(), authz.Actor{}, time.Now())

	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, results)
	require.Zero(t, repo.calls)
}

func TestDashboardGroupUsageSummaryAcceptsServicePrincipalActor(t *testing.T) {
	expected := []usagestats.GroupUsageSummary{{GroupID: 7, TodayCost: 1.25}}
	repo := &dashboardGroupUsageRepoStub{results: expected}
	svc := &DashboardService{usageRepo: repo}
	actor := adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 73)

	results, err := svc.GetGroupUsageSummary(context.Background(), actor, time.Now())

	require.NoError(t, err)
	require.Equal(t, expected, results)
	require.Equal(t, 1, repo.calls)
}
