package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type userGroupResourceRequest struct {
	method string
	path   string
	body   string
}

func setupUserGroupResourceActorRouter(actor *authz.Actor, compatibilityUserID int64, svc *stubAdminService) *gin.Engine {
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

	users := NewUserHandler(svc, nil, nil, nil, nil, nil, nil)
	apiKeys := NewAdminAPIKeyHandler(svc)
	router.GET("/users", users.List)
	router.GET("/users/:id", users.GetByID)
	router.POST("/users", users.Create)
	router.PUT("/users/:id", users.Update)
	router.DELETE("/users/:id", users.Delete)
	router.GET("/users/:id/api-keys", users.GetUserAPIKeys)
	router.POST("/users/:id/replace-group", users.ReplaceGroup)
	router.GET("/users/:id/rpm-status", users.GetUserRPMStatus)
	router.PUT("/api-keys/:id", apiKeys.UpdateGroup)
	return router
}

func performUserGroupResourceRequest(router *gin.Engine, request userGroupResourceRequest) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(request.method, request.path, strings.NewReader(request.body))
	if request.body != "" {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(recorder, httpRequest)
	return recorder
}

func TestUserGroupResourceHandlersRejectMissingActorBeforeMalformedInput(t *testing.T) {
	svc := newStubAdminService()
	router := setupUserGroupResourceActorRouter(nil, 1, svc)
	requests := []userGroupResourceRequest{
		{method: http.MethodGet, path: "/users?api_key_group_id=bad"},
		{method: http.MethodGet, path: "/users/bad"},
		{method: http.MethodPost, path: "/users", body: `{`},
		{method: http.MethodPut, path: "/users/bad", body: `{`},
		{method: http.MethodDelete, path: "/users/bad"},
		{method: http.MethodGet, path: "/users/bad/api-keys"},
		{method: http.MethodPost, path: "/users/bad/replace-group", body: `{`},
		{method: http.MethodGet, path: "/users/bad/rpm-status"},
		{method: http.MethodPut, path: "/api-keys/bad", body: `{`},
	}

	for _, request := range requests {
		recorder := performUserGroupResourceRequest(router, request)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code, "%s %s: %s", request.method, request.path, recorder.Body.String())
		require.Contains(t, recorder.Body.String(), `"reason":"AUTHORIZATION_UNAVAILABLE"`)
	}
	require.Zero(t, svc.resourceActorCalls)
}

func TestUserGroupResourceHandlersPropagateTrustedActorKinds(t *testing.T) {
	requests := []userGroupResourceRequest{
		{method: http.MethodGet, path: "/users"},
		{method: http.MethodGet, path: "/users/1"},
		{method: http.MethodGet, path: "/users/1?include_deleted=true"},
		{method: http.MethodPost, path: "/users", body: `{"email":"new@example.com","password":"password"}`},
		{method: http.MethodPut, path: "/users/1", body: `{"email":"updated@example.com"}`},
		{method: http.MethodDelete, path: "/users/1"},
		{method: http.MethodGet, path: "/users/1/api-keys"},
		{method: http.MethodPost, path: "/users/1/replace-group", body: `{"old_group_id":2,"new_group_id":3}`},
		{method: http.MethodGet, path: "/users/1/rpm-status"},
		{method: http.MethodPut, path: "/api-keys/10", body: `{"group_id":2}`},
	}

	for _, testCase := range []struct {
		name                string
		actor               authz.Actor
		compatibilityUserID int64
	}{
		{name: "jwt user", actor: adminHandlerTestActor(t, authz.SubjectKindUser, 41), compatibilityUserID: 41},
		{name: "admin api key service principal", actor: adminHandlerTestActor(t, authz.SubjectKindServicePrincipal, 73), compatibilityUserID: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			svc := newStubAdminService()
			router := setupUserGroupResourceActorRouter(&testCase.actor, testCase.compatibilityUserID, svc)
			wantKey, ok := testCase.actor.SubjectKey()
			require.True(t, ok)

			for _, request := range requests {
				before := svc.resourceActorCalls
				recorder := performUserGroupResourceRequest(router, request)
				require.NotEqual(t, http.StatusServiceUnavailable, recorder.Code, "%s %s: %s", request.method, request.path, recorder.Body.String())
				require.Equal(t, before+1, svc.resourceActorCalls, "%s %s", request.method, request.path)
				gotKey, gotKeyOK := svc.lastResourceActor.SubjectKey()
				require.True(t, gotKeyOK)
				require.Equal(t, wantKey, gotKey)
			}
		})
	}
}
