package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func setupGroupAccountCreateRouter(t *testing.T, adminSvc *stubAdminService) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	previous := service.DefaultIdempotencyCoordinator()
	service.SetDefaultIdempotencyCoordinator(nil)
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(previous) })

	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router := gin.New()
	router.POST("/api/v1/admin/groups/:id/accounts", handler.CreateInGroup)
	return router
}

func TestAccountHandlerCreateInGroupForcesPathGroupAndMapsRiskToken(t *testing.T) {
	adminSvc := newStubAdminService()
	router := setupGroupAccountCreateRouter(t, adminSvc)
	body := map[string]any{
		"name":                       "group account",
		"platform":                   "anthropic",
		"type":                       "oauth",
		"credentials":                map[string]any{"access_token": "token"},
		"group_ids":                  []int64{28},
		"confirm_mixed_channel_risk": true,
		"risk_confirmation_token":    "bound-risk-token",
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/groups/27/accounts", bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "group-create-27")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Len(t, adminSvc.createdAccounts, 1)
	input := adminSvc.createdAccounts[0]
	require.Equal(t, int64(27), input.RequiredGroupID)
	require.Equal(t, []int64{28}, input.GroupIDs)
	require.Equal(t, "bound-risk-token", input.RiskConfirmationToken)
	require.True(t, input.SkipDefaultGroupBind)
	require.False(t, input.SkipMixedChannelCheck, "the legacy boolean must not bypass group-context confirmation")
}

func TestAccountHandlerCreateInGroupReturnsStructuredRiskChallenge(t *testing.T) {
	adminSvc := newStubAdminService()
	adminSvc.createAccountErr = infraerrors.Conflict("mixed_channel_warning", "confirm mixed channels").WithMetadata(map[string]string{
		"group_id":                "27",
		"risk_confirmation_token": "new-risk-token",
	})
	router := setupGroupAccountCreateRouter(t, adminSvc)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/groups/27/accounts",
		bytes.NewBufferString(`{"name":"group account","platform":"anthropic","type":"oauth","credentials":{"access_token":"token"}}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "group-create-risk-27")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusConflict, recorder.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
	require.Equal(t, "mixed_channel_warning", body["reason"])
	metadata := body["metadata"].(map[string]any)
	require.Equal(t, "new-risk-token", metadata["risk_confirmation_token"])
}

func TestAccountHandlerCreateInGroupRejectsInvalidPathID(t *testing.T) {
	adminSvc := newStubAdminService()
	router := setupGroupAccountCreateRouter(t, adminSvc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/groups/not-a-number/accounts", bytes.NewBufferString(`{}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Empty(t, adminSvc.createdAccounts)
}
