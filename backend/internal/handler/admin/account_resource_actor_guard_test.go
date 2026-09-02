package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccountResourceHandlersRejectMissingActorBeforeRequestParsing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	accounts := &AccountHandler{}
	claudeOAuth := &OAuthHandler{}

	tests := []struct {
		name    string
		handler gin.HandlerFunc
	}{
		{name: "list", handler: accounts.List},
		{name: "get by id", handler: accounts.GetByID},
		{name: "check mixed channel", handler: accounts.CheckMixedChannel},
		{name: "create", handler: accounts.Create},
		{name: "duplicate", handler: accounts.Duplicate},
		{name: "update", handler: accounts.Update},
		{name: "delete", handler: accounts.Delete},
		{name: "test", handler: accounts.Test},
		{name: "recover state", handler: accounts.RecoverState},
		{name: "sync from crs", handler: accounts.SyncFromCRS},
		{name: "preview from crs", handler: accounts.PreviewFromCRS},
		{name: "refresh", handler: accounts.Refresh},
		{name: "apply oauth credentials", handler: accounts.ApplyOAuthCredentials},
		{name: "get stats", handler: accounts.GetStats},
		{name: "clear error", handler: accounts.ClearError},
		{name: "revert proxy fallback", handler: accounts.RevertProxyFallback},
		{name: "batch delete", handler: accounts.BatchDelete},
		{name: "batch clear error", handler: accounts.BatchClearError},
		{name: "batch refresh", handler: accounts.BatchRefresh},
		{name: "batch create", handler: accounts.BatchCreate},
		{name: "batch update credentials", handler: accounts.BatchUpdateCredentials},
		{name: "bulk update", handler: accounts.BulkUpdate},
		{name: "get usage", handler: accounts.GetUsage},
		{name: "clear rate limit", handler: accounts.ClearRateLimit},
		{name: "reset quota", handler: accounts.ResetQuota},
		{name: "get temp unschedulable", handler: accounts.GetTempUnschedulable},
		{name: "clear temp unschedulable", handler: accounts.ClearTempUnschedulable},
		{name: "get today stats", handler: accounts.GetTodayStats},
		{name: "get batch today stats", handler: accounts.GetBatchTodayStats},
		{name: "get batch usage", handler: accounts.GetBatchUsage},
		{name: "set schedulable", handler: accounts.SetSchedulable},
		{name: "get available models", handler: accounts.GetAvailableModels},
		{name: "sync upstream models", handler: accounts.SyncUpstreamModels},
		{name: "sync upstream models preview", handler: accounts.SyncUpstreamModelsPreview},
		{name: "set privacy", handler: accounts.SetPrivacy},
		{name: "refresh tier", handler: accounts.RefreshTier},
		{name: "batch refresh tier", handler: accounts.BatchRefreshTier},
		{name: "export data", handler: accounts.ExportData},
		{name: "import data", handler: accounts.ImportData},
		{name: "import codex session", handler: accounts.ImportCodexSession},
		{name: "get ollama cloud usage settings", handler: accounts.GetOllamaCloudUsageSettings},
		{name: "update ollama cloud usage settings", handler: accounts.UpdateOllamaCloudUsageSettings},
		{name: "get ollama cloud usage", handler: accounts.GetOllamaCloudUsage},
		{name: "save ollama cloud usage session", handler: accounts.SaveOllamaCloudUsageSession},
		{name: "delete ollama cloud usage session", handler: accounts.DeleteOllamaCloudUsageSession},
		{name: "set ollama cloud usage auto refresh", handler: accounts.SetOllamaCloudUsageAutoRefresh},
		{name: "refresh ollama cloud usage", handler: accounts.RefreshOllamaCloudUsage},
		{name: "get upstream billing probe settings", handler: accounts.GetUpstreamBillingProbeSettings},
		{name: "update upstream billing probe settings", handler: accounts.UpdateUpstreamBillingProbeSettings},
		{name: "set upstream billing probe enabled", handler: accounts.SetUpstreamBillingProbeEnabled},
		{name: "probe upstream billing", handler: accounts.ProbeUpstreamBilling},
		{name: "probe upstream billing batch", handler: accounts.ProbeUpstreamBillingBatch},
		{name: "claude generate auth url", handler: claudeOAuth.GenerateAuthURL},
		{name: "claude generate setup token url", handler: claudeOAuth.GenerateSetupTokenURL},
		{name: "claude exchange code", handler: claudeOAuth.ExchangeCode},
		{name: "claude exchange setup token code", handler: claudeOAuth.ExchangeSetupTokenCode},
		{name: "claude cookie auth", handler: claudeOAuth.CookieAuth},
		{name: "claude setup token cookie auth", handler: claudeOAuth.SetupTokenCookieAuth},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			context, _ := gin.CreateTestContext(recorder)
			context.Request = httptest.NewRequest(
				http.MethodPost,
				"/?group=not-an-id&ids=not-an-id",
				strings.NewReader("{"),
			)
			context.Request.Header.Set("Content-Type", "application/json")

			testCase.handler(context)

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			require.Contains(t, recorder.Body.String(), `"reason":"AUTHORIZATION_UNAVAILABLE"`)
		})
	}
}

func TestStaticAccountModelMappingDoesNotRequireResourceActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	(&AccountHandler{}).GetAntigravityDefaultModelMapping(context)

	require.Equal(t, http.StatusOK, recorder.Code)
}
