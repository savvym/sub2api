package admin

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGroupOAuthAndCNResourceHandlersGuardActorAtEntry(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	entryGuarded := entryGuardedAdminHandlerMethods(t, filepath.Dir(currentFile))

	for _, handler := range []string{
		"GroupHandler.GetLiveCapability",
		"GroupHandler.List",
		"GroupHandler.GetAll",
		"GroupHandler.GetUsageSummary",
		"GroupHandler.GetCapacitySummary",
		"GroupHandler.UpdateSortOrder",
		"GroupHandler.GetModelsListCandidates",
		"GroupHandler.ListCompositeRoutes",
		"GroupHandler.CreateCompositeRoute",
		"GroupHandler.PreviewCompositeRoute",
		"GroupHandler.UpdateCompositeRoute",
		"GroupHandler.DeleteCompositeRoute",
		"GroupHandler.GetByID",
		"GroupHandler.Create",
		"GroupHandler.Duplicate",
		"GroupHandler.Update",
		"GroupHandler.Delete",
		"GroupHandler.GetStats",
		"GroupHandler.GetGroupRateMultipliers",
		"GroupHandler.BatchSetGroupRateMultipliers",
		"GroupHandler.ClearGroupRateMultipliers",
		"GroupHandler.BatchSetGroupRPMOverrides",
		"GroupHandler.ClearGroupRPMOverrides",
		"GroupHandler.GetGroupAPIKeys",
		"OpenAIOAuthHandler.GenerateAuthURL",
		"OpenAIOAuthHandler.ExchangeCode",
		"OpenAIOAuthHandler.RefreshToken",
		"OpenAIOAuthHandler.RefreshAccountToken",
		"OpenAIOAuthHandler.CreateAccountFromOAuth",
		"OpenAIOAuthHandler.CreateAccountFromCodexPAT",
		"OpenAIOAuthHandler.QueryQuota",
		"OpenAIOAuthHandler.RefreshQuota",
		"OpenAIOAuthHandler.CreateShadow",
		"OpenAIOAuthHandler.ResetQuota",
		"GrokOAuthHandler.GenerateAuthURL",
		"GrokOAuthHandler.ExchangeCode",
		"GrokOAuthHandler.RefreshToken",
		"GrokOAuthHandler.ValidateSSOToken",
		"GrokOAuthHandler.AuthorizePassword",
		"GrokOAuthHandler.RefreshAccountToken",
		"GrokOAuthHandler.ReconcileOAuthAccounts",
		"GrokOAuthHandler.CreateAccountFromOAuth",
		"GrokOAuthHandler.CreateAccountsFromSSO",
		"GrokOAuthHandler.QueryQuota",
		"GrokOAuthHandler.ResetQuota",
		"GeminiOAuthHandler.GenerateAuthURL",
		"GeminiOAuthHandler.ExchangeCode",
		"AntigravityOAuthHandler.GenerateAuthURL",
		"AntigravityOAuthHandler.ExchangeCode",
		"AntigravityOAuthHandler.RefreshToken",
		"CNProviderHandler.QueryQuota",
		"CNProviderHandler.QueryBalance",
	} {
		require.Truef(t, entryGuarded[handler], "%s must guard Actor before parsing request input", handler)
	}

	for _, excluded := range []string{
		"GeminiOAuthHandler.GetCapabilities",
		"GrokOAuthHandler.GetCapabilities",
		"GrokOAuthHandler.RuntimeSanity",
	} {
		require.Falsef(t, entryGuarded[excluded], "%s is an intentional static capability/runtime exclusion", excluded)
	}
}

func TestGroupOAuthAndCNResourceHandlersFailClosedBeforeMalformedInput(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name     string
		method   string
		path     string
		body     string
		register func(*gin.Engine)
	}{
		{name: "group path", method: http.MethodGet, path: "/groups/bad", register: func(router *gin.Engine) {
			router.GET("/groups/:id", NewGroupHandler(nil, nil, nil).GetByID)
		}},
		{name: "group body", method: http.MethodPost, path: "/groups", body: `{`, register: func(router *gin.Engine) {
			router.POST("/groups", NewGroupHandler(nil, nil, nil).Create)
		}},
		{name: "openai body", method: http.MethodPost, path: "/openai/exchange-code", body: `{`, register: func(router *gin.Engine) {
			router.POST("/openai/exchange-code", NewOpenAIOAuthHandler(nil, nil, nil, nil).ExchangeCode)
		}},
		{name: "openai path", method: http.MethodGet, path: "/openai/accounts/bad/quota", register: func(router *gin.Engine) {
			router.GET("/openai/accounts/:id/quota", NewOpenAIOAuthHandler(nil, nil, nil, nil).QueryQuota)
		}},
		{name: "grok body", method: http.MethodPost, path: "/grok/oauth/exchange-code", body: `{`, register: func(router *gin.Engine) {
			router.POST("/grok/oauth/exchange-code", NewGrokOAuthHandler(nil, nil, nil, nil).ExchangeCode)
		}},
		{name: "grok path", method: http.MethodGet, path: "/grok/accounts/bad/quota", register: func(router *gin.Engine) {
			router.GET("/grok/accounts/:id/quota", NewGrokOAuthHandler(nil, nil, nil, nil).QueryQuota)
		}},
		{name: "gemini body", method: http.MethodPost, path: "/gemini/oauth/auth-url", body: `{`, register: func(router *gin.Engine) {
			router.POST("/gemini/oauth/auth-url", NewGeminiOAuthHandler(nil).GenerateAuthURL)
		}},
		{name: "antigravity body", method: http.MethodPost, path: "/antigravity/oauth/auth-url", body: `{`, register: func(router *gin.Engine) {
			router.POST("/antigravity/oauth/auth-url", NewAntigravityOAuthHandler(nil).GenerateAuthURL)
		}},
		{name: "cn provider path", method: http.MethodGet, path: "/cn-providers/accounts/bad/quota", register: func(router *gin.Engine) {
			router.GET("/cn-providers/accounts/:id/quota", NewCNProviderHandler(nil, nil).QueryQuota)
		}},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
				c.Next()
			})
			testCase.register(router)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(testCase.method, testCase.path, strings.NewReader(testCase.body))
			if testCase.body != "" {
				request.Header.Set("Content-Type", "application/json")
			}
			router.ServeHTTP(recorder, request)

			require.Equal(t, http.StatusServiceUnavailable, recorder.Code, recorder.Body.String())
			require.Contains(t, recorder.Body.String(), `"reason":"AUTHORIZATION_UNAVAILABLE"`)
		})
	}
}

func TestGroupOAuthAndCNResourceHandlersReachValidationWithTrustedActor(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name     string
		method   string
		path     string
		body     string
		register func(*gin.Engine)
	}{
		{name: "group path", method: http.MethodGet, path: "/groups/bad", register: func(router *gin.Engine) {
			router.GET("/groups/:id", NewGroupHandler(nil, nil, nil).GetByID)
		}},
		{name: "openai body", method: http.MethodPost, path: "/openai/exchange-code", body: `{`, register: func(router *gin.Engine) {
			router.POST("/openai/exchange-code", NewOpenAIOAuthHandler(nil, nil, nil, nil).ExchangeCode)
		}},
		{name: "grok path", method: http.MethodGet, path: "/grok/accounts/bad/quota", register: func(router *gin.Engine) {
			router.GET("/grok/accounts/:id/quota", NewGrokOAuthHandler(nil, nil, nil, nil).QueryQuota)
		}},
		{name: "gemini body", method: http.MethodPost, path: "/gemini/oauth/auth-url", body: `{`, register: func(router *gin.Engine) {
			router.POST("/gemini/oauth/auth-url", NewGeminiOAuthHandler(nil).GenerateAuthURL)
		}},
		{name: "antigravity body", method: http.MethodPost, path: "/antigravity/oauth/auth-url", body: `{`, register: func(router *gin.Engine) {
			router.POST("/antigravity/oauth/auth-url", NewAntigravityOAuthHandler(nil).GenerateAuthURL)
		}},
		{name: "cn provider path", method: http.MethodGet, path: "/cn-providers/accounts/bad/quota", register: func(router *gin.Engine) {
			router.GET("/cn-providers/accounts/:id/quota", NewCNProviderHandler(nil, nil).QueryQuota)
		}},
	}

	for _, actorCase := range []struct {
		name                string
		actor               authz.Actor
		compatibilityUserID int64
	}{
		{name: "jwt user", actor: adminHandlerTestActor(t, authz.SubjectKindUser, 41), compatibilityUserID: 41},
		{name: "admin api key service principal", actor: adminHandlerTestActor(t, authz.SubjectKindServicePrincipal, 73), compatibilityUserID: 1},
	} {
		t.Run(actorCase.name, func(t *testing.T) {
			for _, testCase := range tests {
				t.Run(testCase.name, func(t *testing.T) {
					router := gin.New()
					router.Use(func(c *gin.Context) {
						c.Request = c.Request.WithContext(authz.ContextWithActor(c.Request.Context(), actorCase.actor))
						c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: actorCase.compatibilityUserID})
						c.Next()
					})
					testCase.register(router)

					recorder := httptest.NewRecorder()
					request := httptest.NewRequest(testCase.method, testCase.path, strings.NewReader(testCase.body))
					if testCase.body != "" {
						request.Header.Set("Content-Type", "application/json")
					}
					router.ServeHTTP(recorder, request)

					require.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
					require.NotContains(t, recorder.Body.String(), `"reason":"AUTHORIZATION_UNAVAILABLE"`)
				})
			}
		})
	}
}
