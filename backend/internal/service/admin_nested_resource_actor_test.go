package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type adminGroupSubscriptionsRepoStub struct {
	UserSubscriptionRepository
	calls   int
	groupID int64
	params  pagination.PaginationParams
}

func (s *adminGroupSubscriptionsRepoStub) ListByGroupID(_ context.Context, groupID int64, params pagination.PaginationParams) ([]UserSubscription, *pagination.PaginationResult, error) {
	s.calls++
	s.groupID = groupID
	s.params = params
	return []UserSubscription{}, &pagination.PaginationResult{Page: params.Page, PageSize: params.PageSize}, nil
}

type adminScheduledPlanRepoStub struct {
	ScheduledTestPlanRepository
	createCalls int
	getCalls    int
	listCalls   int
	updateCalls int
	deleteCalls int
	accountID   int64
}

func (s *adminScheduledPlanRepoStub) Create(_ context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	s.createCalls++
	return plan, nil
}

func (s *adminScheduledPlanRepoStub) GetByID(_ context.Context, id int64) (*ScheduledTestPlan, error) {
	s.getCalls++
	return &ScheduledTestPlan{ID: id, AccountID: 23, CronExpression: "* * * * *"}, nil
}

func (s *adminScheduledPlanRepoStub) ListByAccountID(_ context.Context, accountID int64) ([]*ScheduledTestPlan, error) {
	s.listCalls++
	s.accountID = accountID
	return []*ScheduledTestPlan{}, nil
}

func (s *adminScheduledPlanRepoStub) Update(_ context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	s.updateCalls++
	return plan, nil
}

func (s *adminScheduledPlanRepoStub) Delete(_ context.Context, _ int64) error {
	s.deleteCalls++
	return nil
}

func (s *adminScheduledPlanRepoStub) totalCalls() int {
	return s.createCalls + s.getCalls + s.listCalls + s.updateCalls + s.deleteCalls
}

type adminScheduledResultRepoStub struct {
	ScheduledTestResultRepository
	listCalls int
	planID    int64
	limit     int
}

func (s *adminScheduledResultRepoStub) ListByPlanID(_ context.Context, planID int64, limit int) ([]*ScheduledTestResult, error) {
	s.listCalls++
	s.planID = planID
	s.limit = limit
	return []*ScheduledTestResult{}, nil
}

func TestListGroupSubscriptionsRequiresAdminResourceActorBeforeRepositoryAccess(t *testing.T) {
	repo := &adminGroupSubscriptionsRepoStub{}
	svc := &SubscriptionService{userSubRepo: repo}

	subscriptions, result, err := svc.AdminListGroupSubscriptions(context.Background(), authz.Actor{}, 17, 2, 25)

	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, subscriptions)
	require.Nil(t, result)
	require.Zero(t, repo.calls)
}

func TestListGroupSubscriptionsAcceptsTrustedAdminResourceActors(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		actor authz.Actor
	}{
		{name: "jwt user", actor: adminResourceTestActor(t, authz.SubjectKindUser, 41)},
		{name: "admin api key service principal", actor: adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 73)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &adminGroupSubscriptionsRepoStub{}
			svc := &SubscriptionService{userSubRepo: repo}

			subscriptions, result, err := svc.AdminListGroupSubscriptions(context.Background(), testCase.actor, 17, 2, 25)

			require.NoError(t, err)
			require.Empty(t, subscriptions)
			require.NotNil(t, result)
			require.Equal(t, 1, repo.calls)
			require.Equal(t, int64(17), repo.groupID)
			require.Equal(t, pagination.PaginationParams{Page: 2, PageSize: 25}, repo.params)
		})
	}
}

func TestScheduledTestAdminMethodsRequireActorBeforeRepositoryAccess(t *testing.T) {
	planRepo := &adminScheduledPlanRepoStub{}
	resultRepo := &adminScheduledResultRepoStub{}
	svc := NewScheduledTestService(planRepo, resultRepo)
	ctx := context.Background()
	missingActor := authz.Actor{}
	plan := &ScheduledTestPlan{AccountID: 23, CronExpression: "* * * * *"}

	operations := []struct {
		name string
		call func() error
	}{
		{name: "create", call: func() error { _, err := svc.AdminCreatePlan(ctx, missingActor, plan); return err }},
		{name: "get", call: func() error { _, err := svc.AdminGetPlan(ctx, missingActor, 5); return err }},
		{name: "list by account", call: func() error { _, err := svc.AdminListPlansByAccount(ctx, missingActor, 23); return err }},
		{name: "update", call: func() error { _, err := svc.AdminUpdatePlan(ctx, missingActor, plan); return err }},
		{name: "delete", call: func() error { return svc.AdminDeletePlan(ctx, missingActor, 5) }},
		{name: "list results", call: func() error { _, err := svc.AdminListResults(ctx, missingActor, 5, 50); return err }},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			require.ErrorIs(t, operation.call(), ErrAdminResourceActorUnavailable)
		})
	}

	require.Zero(t, planRepo.totalCalls())
	require.Zero(t, resultRepo.listCalls)
}

func TestScheduledTestAdminMethodsAcceptTrustedAdminResourceActors(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		actor authz.Actor
	}{
		{name: "jwt user", actor: adminResourceTestActor(t, authz.SubjectKindUser, 41)},
		{name: "admin api key service principal", actor: adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 73)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			planRepo := &adminScheduledPlanRepoStub{}
			resultRepo := &adminScheduledResultRepoStub{}
			svc := NewScheduledTestService(planRepo, resultRepo)
			ctx := context.Background()
			plan := &ScheduledTestPlan{AccountID: 23, CronExpression: "* * * * *"}

			_, err := svc.AdminCreatePlan(ctx, testCase.actor, plan)
			require.NoError(t, err)
			_, err = svc.AdminGetPlan(ctx, testCase.actor, 5)
			require.NoError(t, err)
			plans, err := svc.AdminListPlansByAccount(ctx, testCase.actor, 23)
			require.NoError(t, err)
			require.Empty(t, plans)
			_, err = svc.AdminUpdatePlan(ctx, testCase.actor, plan)
			require.NoError(t, err)
			require.NoError(t, svc.AdminDeletePlan(ctx, testCase.actor, 5))
			results, err := svc.AdminListResults(ctx, testCase.actor, 5, 25)
			require.NoError(t, err)
			require.Empty(t, results)

			require.Equal(t, 1, planRepo.createCalls)
			require.Equal(t, 1, planRepo.getCalls)
			require.Equal(t, 1, planRepo.listCalls)
			require.Equal(t, 1, planRepo.updateCalls)
			require.Equal(t, 1, planRepo.deleteCalls)
			require.Equal(t, int64(23), planRepo.accountID)
			require.Equal(t, 1, resultRepo.listCalls)
			require.Equal(t, int64(5), resultRepo.planID)
			require.Equal(t, 25, resultRepo.limit)
		})
	}
}
