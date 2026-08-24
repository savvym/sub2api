package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAuthorizationPropagationHealthHandlerFailsClosedWhenRuntimeIsUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/health", NewOpsHandler(service.NewOpsService(
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
	)).GetAuthorizationPropagationHealth)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusOK, recorder.Code)

	var envelope struct {
		Data service.AuthorizationPropagationHealth `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	require.False(t, envelope.Data.ExpansionAllowed)
	require.False(t, envelope.Data.TargetMet)
	require.False(t, envelope.Data.SafetyLimitMet)
	require.False(t, envelope.Data.ExpiryCoordinatorReady)
	require.Contains(t, recorder.Body.String(), `"expiry_coordinator_ready":false`)
	require.Equal(t, service.AuthorizationPropagationTarget, envelope.Data.Target)
	require.Equal(t, service.AuthorizationPropagationLimit, envelope.Data.SafetyLimit)
	require.Contains(t, envelope.Data.DegradedReasons, "propagation_stats_unavailable")
}

func TestAuthorizationPropagationHealthHandlerRequiresOpsService(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/health", NewOpsHandler(nil).GetAuthorizationPropagationHealth)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}

func TestAuthorizationPropagationHealthHandlerRemainsAvailableWhenOptionalOpsIsDisabled(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/health", NewOpsHandler(service.NewOpsService(
		nil,
		nil,
		&config.Config{Ops: config.OpsConfig{Enabled: false}},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)).GetAuthorizationPropagationHealth)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"expansion_allowed":false`)
	require.Contains(t, recorder.Body.String(), `"propagation_stats_unavailable"`)
}
