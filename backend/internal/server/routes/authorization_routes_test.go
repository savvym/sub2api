package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRoleAuthorizationModeRoutesAreRegisteredBehindAdminAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{
		Authorization: adminhandler.NewAuthorizationHandler(nil, nil, nil),
	}}
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		if c.GetHeader("Authorization") == "" {
			servermiddleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization required")
			return
		}
		servermiddleware.AbortWithError(c, http.StatusForbidden, "FORBIDDEN", "Admin access required")
	})
	auditLog := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), handlers, adminAuth, auditLog, stepUp, nil, nil)

	expectedRoutes := map[string]bool{
		"GET /api/v1/admin/authorization/role-mode":              false,
		"POST /api/v1/admin/authorization/role-mode/transitions": false,
	}
	for _, route := range router.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := expectedRoutes[key]; ok {
			expectedRoutes[key] = true
		}
	}
	for route, registered := range expectedRoutes {
		require.Truef(t, registered, "%s must be registered", route)
	}

	for route := range expectedRoutes {
		parts := splitAuthorizationRoute(route)
		for _, test := range []struct {
			name       string
			auth       string
			wantStatus int
		}{
			{name: "unauthenticated", wantStatus: http.StatusUnauthorized},
			{name: "non-admin", auth: "Bearer user-token", wantStatus: http.StatusForbidden},
		} {
			t.Run(route+"/"+test.name, func(t *testing.T) {
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(parts.method, parts.path, nil)
				if test.auth != "" {
					request.Header.Set("Authorization", test.auth)
				}
				router.ServeHTTP(recorder, request)
				require.Equal(t, test.wantStatus, recorder.Code)
			})
		}
	}
}

type authorizationRouteParts struct {
	method string
	path   string
}

func splitAuthorizationRoute(route string) authorizationRouteParts {
	for index := range route {
		if route[index] == ' ' {
			return authorizationRouteParts{method: route[:index], path: route[index+1:]}
		}
	}
	return authorizationRouteParts{}
}
