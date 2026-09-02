package routes

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type selfServiceAccountRouteActorStore struct {
	snapshot authz.SubjectSnapshot
}

func (s selfServiceAccountRouteActorStore) LoadSubjectSnapshot(
	context.Context,
	authz.SubjectRef,
) (authz.SubjectSnapshot, error) {
	return s.snapshot, nil
}

func (s selfServiceAccountRouteActorStore) LoadServicePrincipalSubjectSnapshotByCode(
	context.Context,
	string,
) (authz.SubjectSnapshot, error) {
	return authz.SubjectSnapshot{}, errors.New("unexpected service principal lookup")
}

func TestSelfServiceAccountRoutesAreRegisteredBehindUserMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	actor := selfServiceAccountRouteActor(t, 61)
	var handlerNames []string

	jwtHandler := func(c *gin.Context) {
		c.Request = c.Request.WithContext(authz.ContextWithActor(c.Request.Context(), actor))
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 61})
		c.Next()
	}
	auditHandler := func(c *gin.Context) {
		handlerNames = append([]string(nil), c.HandlerNames()...)
		c.Next()
	}

	router := gin.New()
	RegisterUserRoutes(
		router.Group("/api/v1"),
		selfServiceAccountRouteHandlers(),
		middleware.JWTAuthMiddleware(jwtHandler),
		middleware.AuditLogMiddleware(auditHandler),
		nil,
		(*middleware.PanelRateLimiter)(nil),
	)

	routes := make(map[string]struct{})
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = struct{}{}
	}
	for _, route := range []string{
		"GET /api/v1/accounts",
		"POST /api/v1/accounts",
		"GET /api/v1/accounts/products",
		"GET /api/v1/accounts/:id",
		"PATCH /api/v1/accounts/:id",
		"DELETE /api/v1/accounts/:id",
	} {
		_, found := routes[route]
		require.True(t, found, "missing route %s", route)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/accounts", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusBadRequest, recorder.Code)

	jwtName := runtime.FuncForPC(reflect.ValueOf(jwtHandler).Pointer()).Name()
	auditName := runtime.FuncForPC(reflect.ValueOf(auditHandler).Pointer()).Name()
	jwtIndex := exactHandlerNameIndex(handlerNames, jwtName)
	backendModeIndex := matchingHandlerNameIndex(handlerNames, "BackendModeUserGuard")
	rateLimitIndex := matchingHandlerNameIndex(handlerNames, "PanelRateLimiter")
	auditIndex := exactHandlerNameIndex(handlerNames, auditName)
	endpointIndex := matchingHandlerNameIndex(handlerNames, "SelfServiceAccountHandler).Create")

	require.NotEqual(t, -1, jwtIndex, "JWT middleware missing from %v", handlerNames)
	require.NotEqual(t, -1, backendModeIndex, "backend-mode middleware missing from %v", handlerNames)
	require.NotEqual(t, -1, rateLimitIndex, "panel rate limiter missing from %v", handlerNames)
	require.NotEqual(t, -1, auditIndex, "audit middleware missing from %v", handlerNames)
	require.NotEqual(t, -1, endpointIndex, "account endpoint missing from %v", handlerNames)
	require.Less(t, jwtIndex, backendModeIndex)
	require.Less(t, backendModeIndex, rateLimitIndex)
	require.Less(t, rateLimitIndex, auditIndex)
	require.Less(t, auditIndex, endpointIndex)
}

func selfServiceAccountRouteHandlers() *handler.Handlers {
	return &handler.Handlers{
		User:             &handler.UserHandler{},
		APIKey:           &handler.APIKeyHandler{},
		Usage:            &handler.UsageHandler{},
		Redeem:           &handler.RedeemHandler{},
		Subscription:     &handler.SubscriptionHandler{},
		Announcement:     &handler.AnnouncementHandler{},
		ChannelMonitor:   &handler.ChannelMonitorUserHandler{},
		ChannelMonitorV2: &handler.ChannelMonitorV2Handler{},
		Totp:             &handler.TotpHandler{},
		Passkey:          &handler.PasskeyHandler{},
		AvailableChannel: &handler.AvailableChannelHandler{},
		Account:          handler.NewSelfServiceAccountHandler(nil),
	}
}

func selfServiceAccountRouteActor(t testing.TB, userID int64) authz.Actor {
	t.Helper()
	subject, err := authz.NewSubjectRef(authz.SubjectKindUser, userID)
	require.NoError(t, err)
	configuration, err := authz.NewPolicyConfiguration(authz.PolicyConfigurationInput{
		RoleAuthorizationMode:        authz.RoleAuthorizationModeRBAC,
		ResourceAccessControlEnabled: true,
		SelfServiceHostingEnabled:    true,
	})
	require.NoError(t, err)
	snapshot, err := authz.NewSubjectSnapshot(authz.SubjectSnapshotInput{
		Subject: subject, Exists: true, Active: true, AuthzVersion: 1,
		Capabilities:  []authz.Capability{authz.CapabilityAccountCreate},
		Configuration: configuration,
	})
	require.NoError(t, err)
	actor, err := authz.NewActorResolver(selfServiceAccountRouteActorStore{snapshot: snapshot}).ResolveUser(
		context.Background(),
		userID,
		authz.AuthMethodJWT,
	)
	require.NoError(t, err)
	return actor
}

func exactHandlerNameIndex(names []string, target string) int {
	for index, name := range names {
		if name == target {
			return index
		}
	}
	return -1
}

func matchingHandlerNameIndex(names []string, fragment string) int {
	for index, name := range names {
		if strings.Contains(name, fragment) {
			return index
		}
	}
	return -1
}
