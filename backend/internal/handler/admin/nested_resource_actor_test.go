package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type nestedSubscriptionsRepoStub struct {
	service.UserSubscriptionRepository
	calls   int
	groupID int64
}

func (s *nestedSubscriptionsRepoStub) ListByGroupID(_ context.Context, groupID int64, params pagination.PaginationParams) ([]service.UserSubscription, *pagination.PaginationResult, error) {
	s.calls++
	s.groupID = groupID
	return []service.UserSubscription{}, &pagination.PaginationResult{Page: params.Page, PageSize: params.PageSize}, nil
}

type nestedScheduledPlanRepoStub struct {
	service.ScheduledTestPlanRepository
	createCalls int
	getCalls    int
	listCalls   int
	updateCalls int
	deleteCalls int
	accountID   int64
}

func (s *nestedScheduledPlanRepoStub) Create(_ context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	s.createCalls++
	plan.ID = 5
	return plan, nil
}

func (s *nestedScheduledPlanRepoStub) GetByID(_ context.Context, id int64) (*service.ScheduledTestPlan, error) {
	s.getCalls++
	return &service.ScheduledTestPlan{ID: id, AccountID: 23, CronExpression: "* * * * *"}, nil
}

func (s *nestedScheduledPlanRepoStub) ListByAccountID(_ context.Context, accountID int64) ([]*service.ScheduledTestPlan, error) {
	s.listCalls++
	s.accountID = accountID
	return []*service.ScheduledTestPlan{}, nil
}

func (s *nestedScheduledPlanRepoStub) Update(_ context.Context, plan *service.ScheduledTestPlan) (*service.ScheduledTestPlan, error) {
	s.updateCalls++
	return plan, nil
}

func (s *nestedScheduledPlanRepoStub) Delete(_ context.Context, _ int64) error {
	s.deleteCalls++
	return nil
}

func (s *nestedScheduledPlanRepoStub) totalCalls() int {
	return s.createCalls + s.getCalls + s.listCalls + s.updateCalls + s.deleteCalls
}

type nestedScheduledResultRepoStub struct {
	service.ScheduledTestResultRepository
	listCalls int
}

func (s *nestedScheduledResultRepoStub) ListByPlanID(_ context.Context, _ int64, _ int) ([]*service.ScheduledTestResult, error) {
	s.listCalls++
	return []*service.ScheduledTestResult{}, nil
}

func setupNestedAdminResourceRouter(t *testing.T, actor *authz.Actor, compatibilityUserID int64, subscriptions *nestedSubscriptionsRepoStub, plans *nestedScheduledPlanRepoStub, results *nestedScheduledResultRepoStub) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if actor != nil {
			c.Request = c.Request.WithContext(authz.ContextWithActor(c.Request.Context(), *actor))
		}
		if compatibilityUserID > 0 {
			c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: compatibilityUserID})
		}
		c.Next()
	})
	subscriptionService := service.NewSubscriptionService(nil, subscriptions, nil, nil, nil)
	scheduledTestService := service.NewScheduledTestService(plans, results)
	scheduledTestHandler := NewScheduledTestHandler(scheduledTestService)
	router.GET("/admin/groups/:id/subscriptions", NewSubscriptionHandler(subscriptionService).ListByGroup)
	router.GET("/admin/accounts/:id/scheduled-test-plans", scheduledTestHandler.ListByAccount)
	router.POST("/admin/scheduled-test-plans", scheduledTestHandler.Create)
	router.PUT("/admin/scheduled-test-plans/:id", scheduledTestHandler.Update)
	router.DELETE("/admin/scheduled-test-plans/:id", scheduledTestHandler.Delete)
	router.GET("/admin/scheduled-test-plans/:id/results", scheduledTestHandler.ListResults)
	return router
}

type nestedAdminResourceRequest struct {
	method string
	path   string
	body   string
}

var nestedAdminResourceRequests = []nestedAdminResourceRequest{
	{method: http.MethodGet, path: "/admin/groups/17/subscriptions"},
	{method: http.MethodGet, path: "/admin/accounts/23/scheduled-test-plans"},
	{method: http.MethodPost, path: "/admin/scheduled-test-plans", body: `{"account_id":23,"cron_expression":"* * * * *"}`},
	{method: http.MethodPut, path: "/admin/scheduled-test-plans/5", body: `{"cron_expression":"*/5 * * * *"}`},
	{method: http.MethodDelete, path: "/admin/scheduled-test-plans/5"},
	{method: http.MethodGet, path: "/admin/scheduled-test-plans/5/results"},
}

func performNestedAdminResourceRequest(router *gin.Engine, request nestedAdminResourceRequest) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(request.method, request.path, strings.NewReader(request.body))
	if request.body != "" {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(recorder, httpRequest)
	return recorder
}

func TestNestedAdminResourceHandlersFailClosedWithoutActor(t *testing.T) {
	subscriptions := &nestedSubscriptionsRepoStub{}
	plans := &nestedScheduledPlanRepoStub{}
	results := &nestedScheduledResultRepoStub{}
	router := setupNestedAdminResourceRouter(t, nil, 1, subscriptions, plans, results)

	for _, request := range nestedAdminResourceRequests {
		recorder := performNestedAdminResourceRequest(router, request)

		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.Contains(t, recorder.Body.String(), `"reason":"AUTHORIZATION_UNAVAILABLE"`)
	}
	require.Zero(t, subscriptions.calls)
	require.Zero(t, plans.totalCalls())
	require.Zero(t, results.listCalls)
}

func TestNestedAdminResourceHandlersAcceptAndPassTrustedActors(t *testing.T) {
	for _, testCase := range []struct {
		name                string
		actor               authz.Actor
		compatibilityUserID int64
	}{
		{
			name:                "jwt user",
			actor:               adminHandlerTestActor(t, authz.SubjectKindUser, 41),
			compatibilityUserID: 41,
		},
		{
			name:                "admin api key service principal",
			actor:               adminHandlerTestActor(t, authz.SubjectKindServicePrincipal, 73),
			compatibilityUserID: 1,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			subscriptions := &nestedSubscriptionsRepoStub{}
			plans := &nestedScheduledPlanRepoStub{}
			results := &nestedScheduledResultRepoStub{}
			router := setupNestedAdminResourceRouter(t, &testCase.actor, testCase.compatibilityUserID, subscriptions, plans, results)

			for _, request := range nestedAdminResourceRequests {
				recorder := performNestedAdminResourceRequest(router, request)
				require.Equal(t, http.StatusOK, recorder.Code)
			}

			require.Equal(t, 1, subscriptions.calls)
			require.Equal(t, int64(17), subscriptions.groupID)
			require.Equal(t, 1, plans.createCalls)
			require.Equal(t, 1, plans.getCalls)
			require.Equal(t, 1, plans.listCalls)
			require.Equal(t, 1, plans.updateCalls)
			require.Equal(t, 1, plans.deleteCalls)
			require.Equal(t, int64(23), plans.accountID)
			require.Equal(t, 1, results.listCalls)
		})
	}
}
