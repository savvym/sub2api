package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type groupAccountsHandlerServiceStub struct {
	service.AdminService
	listResult      *service.GroupAccountListPage
	updateResult    *service.GroupAccountMembershipDiffResult
	updateErr       error
	lastGroupID     int64
	lastFilters     service.GroupAccountListFilters
	lastUpdateInput service.GroupAccountMembershipDiffInput
}

func (s *groupAccountsHandlerServiceStub) ListGroupAccounts(_ context.Context, groupID int64, filters service.GroupAccountListFilters) (*service.GroupAccountListPage, error) {
	s.lastGroupID = groupID
	s.lastFilters = filters
	return s.listResult, nil
}

func (s *groupAccountsHandlerServiceStub) ListGroupAccountCandidates(_ context.Context, groupID int64, filters service.GroupAccountListFilters) (*service.GroupAccountListPage, error) {
	s.lastGroupID = groupID
	s.lastFilters = filters
	return s.listResult, nil
}

func (s *groupAccountsHandlerServiceStub) ApplyGroupAccountMembershipDiff(_ context.Context, groupID int64, input service.GroupAccountMembershipDiffInput) (*service.GroupAccountMembershipDiffResult, error) {
	s.lastGroupID = groupID
	s.lastUpdateInput = input
	return s.updateResult, s.updateErr
}

func setupGroupAccountsHandlerRouter(t *testing.T, svc service.AdminService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previous := service.DefaultIdempotencyCoordinator()
	service.SetDefaultIdempotencyCoordinator(nil)
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(previous) })
	handler := NewGroupHandler(svc, nil, nil)
	router := gin.New()
	router.GET("/api/v1/admin/groups/:id/accounts", handler.ListAccounts)
	router.GET("/api/v1/admin/groups/:id/account-candidates", handler.ListAccountCandidates)
	router.PATCH("/api/v1/admin/groups/:id/accounts", handler.UpdateAccounts)
	return router
}

func TestGroupHandlerListAccountsContractIncludesZeroMemberTotal(t *testing.T) {
	zero := int64(0)
	svc := &groupAccountsHandlerServiceStub{listResult: &service.GroupAccountListPage{
		Items:       []service.GroupAccountSummary{},
		Page:        2,
		PageSize:    25,
		Pages:       1,
		MemberTotal: &zero,
	}}
	router := setupGroupAccountsHandlerRouter(t, svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/admin/groups/42/accounts?page=2&page_size=25&search=needle&type=oauth&status=active&platform=openai", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, int64(42), svc.lastGroupID)
	require.Equal(t, service.GroupAccountListFilters{Page: 2, PageSize: 25, Search: "needle", AccountType: "oauth", Status: "active", Platform: "openai"}, svc.lastFilters)
	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	data := body["data"].(map[string]any)
	require.Contains(t, data, "member_total")
	require.Equal(t, float64(0), data["member_total"])
	require.NotContains(t, data, "eligible_total")
}

func TestGroupHandlerUpdateAccountsReturnsMixedRiskChallengeMetadata(t *testing.T) {
	svc := &groupAccountsHandlerServiceStub{updateErr: infraerrors.Conflict("mixed_channel_warning", "confirm mixed channel risk").WithMetadata(map[string]string{
		"group_id":                "42",
		"risk_confirmation_token": "bound-token",
	})}
	router := setupGroupAccountsHandlerRouter(t, svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/groups/42/accounts", bytes.NewBufferString(`{"add_account_ids":[2],"remove_account_ids":[3]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "membership-risk-1")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Equal(t, int64(42), svc.lastGroupID)
	require.Equal(t, []int64{2}, svc.lastUpdateInput.AddAccountIDs)
	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "mixed_channel_warning", body["reason"])
	metadata := body["metadata"].(map[string]any)
	require.Equal(t, "bound-token", metadata["risk_confirmation_token"])
}

func TestGroupHandlerUpdateAccountsSuccessContract(t *testing.T) {
	svc := &groupAccountsHandlerServiceStub{updateResult: &service.GroupAccountMembershipDiffResult{
		AddedAccountIDs:         []int64{2},
		RemovedAccountIDs:       []int64{3},
		AlreadyMemberAccountIDs: []int64{},
		NotMemberAccountIDs:     []int64{},
		AccountCount:            5,
		ActiveAccountCount:      4,
		RateLimitedAccountCount: 1,
	}}
	router := setupGroupAccountsHandlerRouter(t, svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/admin/groups/42/accounts", bytes.NewBufferString(`{"add_account_ids":[2],"remove_account_ids":[3]}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "membership-success-1")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	data := body["data"].(map[string]any)
	require.Equal(t, []any{float64(2)}, data["added_account_ids"])
	require.Equal(t, float64(5), data["account_count"])
}
