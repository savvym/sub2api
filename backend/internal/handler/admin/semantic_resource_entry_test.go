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

type semanticResourceSubscriptionRepo struct {
	service.UserSubscriptionRepository
	listCalls int
}

func (r *semanticResourceSubscriptionRepo) List(_ context.Context, params pagination.PaginationParams, _, _ *int64, _, _, _, _ string) ([]service.UserSubscription, *pagination.PaginationResult, error) {
	r.listCalls++
	return []service.UserSubscription{}, &pagination.PaginationResult{Page: params.Page, PageSize: params.PageSize}, nil
}

type semanticResourceRequest struct {
	method string
	path   string
	body   string
}

var semanticResourceRequests = []semanticResourceRequest{
	{method: http.MethodPost, path: "/gemini/oauth/auth-url", body: `{}`},
	{method: http.MethodPost, path: "/gemini/oauth/exchange-code", body: `{}`},
	{method: http.MethodPost, path: "/antigravity/oauth/auth-url", body: `{}`},
	{method: http.MethodPost, path: "/antigravity/oauth/exchange-code", body: `{}`},
	{method: http.MethodPost, path: "/antigravity/oauth/refresh-token", body: `{}`},
	{method: http.MethodGet, path: "/cn-providers/accounts/9/quota"},
	{method: http.MethodGet, path: "/cn-providers/accounts/9/balance"},
	{method: http.MethodGet, path: "/proxies/9/accounts"},
	{method: http.MethodGet, path: "/subscriptions"},
	{method: http.MethodGet, path: "/subscriptions/9"},
	{method: http.MethodGet, path: "/subscriptions/9/progress"},
	{method: http.MethodPost, path: "/subscriptions/assign", body: `{}`},
	{method: http.MethodPost, path: "/subscriptions/bulk-assign", body: `{}`},
	{method: http.MethodPost, path: "/subscriptions/9/extend", body: `{"days":1}`},
	{method: http.MethodPost, path: "/subscriptions/9/reset-quota", body: `{"daily":true}`},
	{method: http.MethodPost, path: "/subscriptions/9/revoke"},
	{method: http.MethodPost, path: "/subscriptions/9/restore"},
	{method: http.MethodGet, path: "/groups/9/subscriptions"},
	{method: http.MethodGet, path: "/users/9/subscriptions"},
}

var semanticResourceTrustedActorRequests = []semanticResourceRequest{
	{method: http.MethodPost, path: "/gemini/oauth/auth-url", body: `{`},
	{method: http.MethodPost, path: "/gemini/oauth/exchange-code", body: `{}`},
	{method: http.MethodPost, path: "/antigravity/oauth/auth-url", body: `{`},
	{method: http.MethodPost, path: "/antigravity/oauth/exchange-code", body: `{}`},
	{method: http.MethodPost, path: "/antigravity/oauth/refresh-token", body: `{}`},
	{method: http.MethodGet, path: "/cn-providers/accounts/bad/quota"},
	{method: http.MethodGet, path: "/cn-providers/accounts/bad/balance"},
	{method: http.MethodGet, path: "/proxies/bad/accounts"},
	{method: http.MethodGet, path: "/subscriptions"},
	{method: http.MethodGet, path: "/subscriptions/bad"},
	{method: http.MethodGet, path: "/subscriptions/bad/progress"},
	{method: http.MethodPost, path: "/subscriptions/assign", body: `{}`},
	{method: http.MethodPost, path: "/subscriptions/bulk-assign", body: `{}`},
	{method: http.MethodPost, path: "/subscriptions/bad/extend", body: `{}`},
	{method: http.MethodPost, path: "/subscriptions/bad/reset-quota", body: `{}`},
	{method: http.MethodPost, path: "/subscriptions/bad/revoke"},
	{method: http.MethodPost, path: "/subscriptions/bad/restore"},
	{method: http.MethodGet, path: "/groups/bad/subscriptions"},
	{method: http.MethodGet, path: "/users/bad/subscriptions"},
}

func setupSemanticResourceRouter(actor *authz.Actor, compatibilityUserID int64) (*gin.Engine, *semanticResourceSubscriptionRepo) {
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

	gemini := NewGeminiOAuthHandler(nil)
	antigravity := NewAntigravityOAuthHandler(nil)
	cnProvider := NewCNProviderHandler(nil, nil)
	proxy := NewProxyHandler(nil)
	subscriptionRepo := &semanticResourceSubscriptionRepo{}
	subscriptions := NewSubscriptionHandler(service.NewSubscriptionService(nil, subscriptionRepo, nil, nil, nil))

	router.POST("/gemini/oauth/auth-url", gemini.GenerateAuthURL)
	router.POST("/gemini/oauth/exchange-code", gemini.ExchangeCode)
	router.POST("/antigravity/oauth/auth-url", antigravity.GenerateAuthURL)
	router.POST("/antigravity/oauth/exchange-code", antigravity.ExchangeCode)
	router.POST("/antigravity/oauth/refresh-token", antigravity.RefreshToken)
	router.GET("/cn-providers/accounts/:id/quota", cnProvider.QueryQuota)
	router.GET("/cn-providers/accounts/:id/balance", cnProvider.QueryBalance)
	router.GET("/proxies/:id/accounts", proxy.GetProxyAccounts)
	router.GET("/subscriptions", subscriptions.List)
	router.GET("/subscriptions/:id", subscriptions.GetByID)
	router.GET("/subscriptions/:id/progress", subscriptions.GetProgress)
	router.POST("/subscriptions/assign", subscriptions.Assign)
	router.POST("/subscriptions/bulk-assign", subscriptions.BulkAssign)
	router.POST("/subscriptions/:id/extend", subscriptions.Extend)
	router.POST("/subscriptions/:id/reset-quota", subscriptions.ResetQuota)
	router.POST("/subscriptions/:id/revoke", subscriptions.Revoke)
	router.POST("/subscriptions/:id/restore", subscriptions.Restore)
	router.GET("/groups/:id/subscriptions", subscriptions.ListByGroup)
	router.GET("/users/:id/subscriptions", subscriptions.ListByUser)

	return router, subscriptionRepo
}

func performSemanticResourceRequest(router *gin.Engine, request semanticResourceRequest) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(request.method, request.path, strings.NewReader(request.body))
	if request.body != "" {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(recorder, httpRequest)
	return recorder
}

func TestSemanticResourceHandlersFailClosedWithoutActor(t *testing.T) {
	router, subscriptions := setupSemanticResourceRouter(nil, 1)

	for _, request := range semanticResourceRequests {
		recorder := performSemanticResourceRequest(router, request)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code, "%s %s: %s", request.method, request.path, recorder.Body.String())
		require.Contains(t, recorder.Body.String(), `"reason":"AUTHORIZATION_UNAVAILABLE"`)
	}
	require.Zero(t, subscriptions.listCalls)
}

func TestSemanticResourceHandlersAcceptTrustedActorKinds(t *testing.T) {
	for _, testCase := range []struct {
		name                string
		actor               authz.Actor
		compatibilityUserID int64
	}{
		{name: "jwt user", actor: adminHandlerTestActor(t, authz.SubjectKindUser, 41), compatibilityUserID: 41},
		{name: "admin api key service principal", actor: adminHandlerTestActor(t, authz.SubjectKindServicePrincipal, 73), compatibilityUserID: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			router, subscriptions := setupSemanticResourceRouter(&testCase.actor, testCase.compatibilityUserID)

			for _, request := range semanticResourceTrustedActorRequests {
				recorder := performSemanticResourceRequest(router, request)
				require.NotEqual(t, http.StatusServiceUnavailable, recorder.Code, "%s %s: %s", request.method, request.path, recorder.Body.String())
			}
			require.Equal(t, 1, subscriptions.listCalls)
		})
	}
}
