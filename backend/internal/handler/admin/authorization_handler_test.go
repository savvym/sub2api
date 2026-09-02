//go:build unit

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type authorizationRoleRepositoryStub struct {
	service.RoleRepository
	mode          string
	readiness     service.RoleAuthorizationReadiness
	txErr         error
	modeErr       error
	readinessErr  error
	actor         service.RoleSubject
	snapshotCalls int
	txCalls       int
	setCalls      int
	auditCalls    int
	auditActor    service.RoleSubject
	auditPrevious string
	auditCurrent  string
	auditTrace    service.RoleAuthorizationModeAuditTrace
}

func (r *authorizationRoleRepositoryStub) WithRoleAuthorizationSnapshot(ctx context.Context, fn func(context.Context) error) error {
	r.snapshotCalls++
	return fn(ctx)
}

func (r *authorizationRoleRepositoryStub) GetAuthorizationMode(context.Context) (string, error) {
	return r.mode, r.modeErr
}

func (r *authorizationRoleRepositoryStub) ReadRoleSubjects(_ context.Context, userIDs []int64) (map[int64]service.RoleSubject, error) {
	return r.roleSubjects(userIDs), nil
}

func (r *authorizationRoleRepositoryStub) InspectAuthorizationReadinessSnapshot(context.Context, string) (service.RoleAuthorizationReadiness, error) {
	return r.readiness, r.readinessErr
}

func (r *authorizationRoleRepositoryStub) WithRoleManagementTx(ctx context.Context, fn func(context.Context) error) error {
	r.txCalls++
	if r.txErr != nil {
		return r.txErr
	}
	return fn(ctx)
}

func (r *authorizationRoleRepositoryStub) GetAuthorizationModeForUpdate(context.Context) (string, error) {
	return r.mode, r.modeErr
}

func (r *authorizationRoleRepositoryStub) LockRoleSubjects(_ context.Context, userIDs []int64) (map[int64]service.RoleSubject, error) {
	return r.roleSubjects(userIDs), nil
}

func (r *authorizationRoleRepositoryStub) roleSubjects(userIDs []int64) map[int64]service.RoleSubject {
	result := make(map[int64]service.RoleSubject)
	for _, userID := range userIDs {
		if userID == r.actor.ID {
			result[userID] = r.actor
		}
	}
	return result
}

func (r *authorizationRoleRepositoryStub) InspectAuthorizationReadiness(context.Context, string) (service.RoleAuthorizationReadiness, error) {
	return r.readiness, r.readinessErr
}

func (r *authorizationRoleRepositoryStub) SetAuthorizationMode(_ context.Context, mode string) error {
	r.setCalls++
	r.mode = mode
	return nil
}

func (r *authorizationRoleRepositoryStub) AppendAuthorizationModeTransitionAudit(
	_ context.Context,
	actor service.RoleSubject,
	previousMode string,
	currentMode string,
	trace service.RoleAuthorizationModeAuditTrace,
) error {
	r.auditCalls++
	r.auditActor = actor
	r.auditPrevious = previousMode
	r.auditCurrent = currentMode
	r.auditTrace = trace
	return nil
}

type authorizationUserRepositoryStub struct {
	service.UserRepository
	user *service.User
	err  error
}

func (r *authorizationUserRepositoryStub) GetByID(context.Context, int64) (*service.User, error) {
	return r.user, r.err
}

func (r *authorizationUserRepositoryStub) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}

type authorizationTotpCacheStub struct {
	service.TotpCache
	granted    bool
	err        error
	grantCalls int
	userID     int64
	sessionKey string
}

func (c *authorizationTotpCacheStub) HasStepUpGrant(_ context.Context, userID int64, sessionKey string) (bool, error) {
	c.grantCalls++
	c.userID = userID
	c.sessionKey = sessionKey
	return c.granted, c.err
}

func newAuthorizationHandlerTestHarness(
	granted bool,
) (*AuthorizationHandler, *authorizationRoleRepositoryStub, *authorizationTotpCacheStub) {
	roleRepo := &authorizationRoleRepositoryStub{
		mode:      service.RoleAuthorizationModeLegacy,
		readiness: service.RoleAuthorizationReadiness{Blockers: []service.RoleAuthorizationReadinessBlocker{}},
		actor: service.RoleSubject{
			ID:         42,
			Email:      "admin@example.com",
			LegacyRole: service.RoleAdmin,
			Status:     service.StatusActive,
		},
	}
	userRepo := &authorizationUserRepositoryStub{
		user: &service.User{ID: 42, Role: service.RoleAdmin, Status: service.StatusActive, TotpEnabled: true},
	}
	cache := &authorizationTotpCacheStub{granted: granted}
	userService := service.NewUserService(userRepo, nil, nil, nil)
	totpService := service.NewTotpService(userRepo, nil, cache, nil, nil, nil)
	return NewAuthorizationHandler(service.NewRoleService(roleRepo), totpService, userService), roleRepo, cache
}

func newAuthorizationHandlerContext(
	t *testing.T,
	method string,
	body string,
) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(method, "/api/v1/admin/authorization/role-mode", bytes.NewBufferString(body))
	request = request.WithContext(context.WithValue(request.Context(), ctxkey.RequestID, "request-role-mode-1"))
	request.RemoteAddr = "198.51.100.8:443"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "role-mode-test/1.0")
	c.Request = request
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
	c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
	c.Set(middleware.ContextKeySessionID, "session-42")
	c.Set("auth_method", service.AuditAuthMethodJWT)
	return c, recorder
}

type authorizationResponseEnvelope struct {
	Code   any             `json:"code"`
	Reason string          `json:"reason"`
	Data   json.RawMessage `json:"data"`
}

func decodeAuthorizationResponse(t *testing.T, recorder *httptest.ResponseRecorder) authorizationResponseEnvelope {
	t.Helper()
	var envelope authorizationResponseEnvelope
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	return envelope
}

func TestAuthorizationHandlerTransitionRequiresStepUpEvenWhenNoSettingServiceIsInjected(t *testing.T) {
	handler, roleRepo, cache := newAuthorizationHandlerTestHarness(false)
	c, recorder := newAuthorizationHandlerContext(t, http.MethodPost, `{"expected_mode":"legacy","target_mode":"shadow"}`)

	handler.TransitionRoleAuthorizationMode(c)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "STEP_UP_REQUIRED")
	require.Equal(t, 1, cache.grantCalls)
	require.Zero(t, roleRepo.txCalls)
}

func TestAuthorizationHandlerTransitionRejectsAdminAPIKey(t *testing.T) {
	handler, roleRepo, cache := newAuthorizationHandlerTestHarness(true)
	c, recorder := newAuthorizationHandlerContext(t, http.MethodPost, `{"expected_mode":"legacy","target_mode":"shadow"}`)
	c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)

	handler.TransitionRoleAuthorizationMode(c)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
	require.Zero(t, cache.grantCalls)
	require.Zero(t, roleRepo.txCalls)
}

func TestAuthorizationHandlerTransitionRequiresJWTAuthMethod(t *testing.T) {
	tests := []struct {
		name       string
		authMethod string
	}{
		{name: "missing", authMethod: ""},
		{name: "unknown", authMethod: "future_auth_method"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, roleRepo, cache := newAuthorizationHandlerTestHarness(true)
			c, recorder := newAuthorizationHandlerContext(t, http.MethodPost, `{"expected_mode":"legacy","target_mode":"shadow"}`)
			c.Set("auth_method", test.authMethod)

			handler.TransitionRoleAuthorizationMode(c)

			require.Equal(t, http.StatusUnauthorized, recorder.Code)
			require.Equal(t, "UNAUTHORIZED", decodeAuthorizationResponse(t, recorder).Code)
			require.Zero(t, cache.grantCalls)
			require.Zero(t, roleRepo.txCalls)
		})
	}
}

func TestAuthorizationHandlerTransitionRejectsJWTWithoutSessionID(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
	}{
		{name: "missing", sessionID: ""},
		{name: "whitespace", sessionID: "   "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, roleRepo, cache := newAuthorizationHandlerTestHarness(true)
			c, recorder := newAuthorizationHandlerContext(t, http.MethodPost, `{"expected_mode":"legacy","target_mode":"shadow"}`)
			c.Set(middleware.ContextKeySessionID, test.sessionID)

			handler.TransitionRoleAuthorizationMode(c)

			require.Equal(t, http.StatusUnauthorized, recorder.Code)
			require.Equal(t, "STEP_UP_SESSION_REQUIRED", decodeAuthorizationResponse(t, recorder).Code)
			require.Zero(t, cache.grantCalls)
			require.Zero(t, roleRepo.txCalls)
		})
	}
}

func TestAuthorizationHandlerTransitionForwardsTraceAndSkipsDuplicateSuccessAudit(t *testing.T) {
	handler, roleRepo, cache := newAuthorizationHandlerTestHarness(true)
	c, recorder := newAuthorizationHandlerContext(t, http.MethodPost, `{"expected_mode":"legacy","target_mode":"shadow"}`)

	handler.TransitionRoleAuthorizationMode(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, c.GetBool("audit_skip"))
	require.Equal(t, 1, cache.grantCalls)
	require.Equal(t, int64(42), cache.userID)
	require.Equal(t, "session-42", cache.sessionKey)
	require.Equal(t, 1, roleRepo.setCalls)
	require.Equal(t, 1, roleRepo.auditCalls)
	require.Equal(t, int64(42), roleRepo.auditActor.ID)
	require.Equal(t, service.RoleAuthorizationModeLegacy, roleRepo.auditPrevious)
	require.Equal(t, service.RoleAuthorizationModeShadow, roleRepo.auditCurrent)
	require.Equal(t, service.RoleAuthorizationModeAuditTrace{
		RequestID: "request-role-mode-1",
		ClientIP:  "198.51.100.8",
		UserAgent: "role-mode-test/1.0",
	}, roleRepo.auditTrace)

	envelope := decodeAuthorizationResponse(t, recorder)
	var result service.RoleAuthorizationModeTransitionResult
	require.NoError(t, json.Unmarshal(envelope.Data, &result))
	require.True(t, result.Changed)
	require.Equal(t, service.RoleAuthorizationModeLegacy, result.PreviousMode)
	require.Equal(t, service.RoleAuthorizationModeShadow, result.CurrentMode)
}

func TestAuthorizationHandlerTransitionDoesNotSkipBestEffortAttemptAuditOnErrors(t *testing.T) {
	t.Run("compare and swap conflict", func(t *testing.T) {
		handler, roleRepo, _ := newAuthorizationHandlerTestHarness(true)
		roleRepo.mode = service.RoleAuthorizationModeShadow
		c, recorder := newAuthorizationHandlerContext(t, http.MethodPost, `{"expected_mode":"legacy","target_mode":"shadow"}`)

		handler.TransitionRoleAuthorizationMode(c)

		require.Equal(t, http.StatusConflict, recorder.Code)
		require.Equal(t, "ROLE_MUTATION_CONFLICT", decodeAuthorizationResponse(t, recorder).Reason)
		require.False(t, c.GetBool("audit_skip"))
		require.Zero(t, roleRepo.auditCalls)
	})

	t.Run("repository error", func(t *testing.T) {
		handler, roleRepo, _ := newAuthorizationHandlerTestHarness(true)
		roleRepo.txErr = errors.New("database unavailable")
		c, recorder := newAuthorizationHandlerContext(t, http.MethodPost, `{"expected_mode":"legacy","target_mode":"shadow"}`)

		handler.TransitionRoleAuthorizationMode(c)

		require.Equal(t, http.StatusInternalServerError, recorder.Code)
		require.False(t, c.GetBool("audit_skip"))
		require.Zero(t, roleRepo.auditCalls)
	})
}

func TestAuthorizationHandlerTransitionUsesStrictRequestContract(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"expected_mode":"legacy","target_mode":"shadow","actor_user_id":999}`},
		{name: "missing target", body: `{"expected_mode":"legacy"}`},
		{name: "trailing json", body: `{"expected_mode":"legacy","target_mode":"shadow"}{}`},
		{name: "null body", body: `null`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler, roleRepo, _ := newAuthorizationHandlerTestHarness(true)
			c, recorder := newAuthorizationHandlerContext(t, http.MethodPost, test.body)

			handler.TransitionRoleAuthorizationMode(c)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Equal(t, "INVALID_REQUEST", decodeAuthorizationResponse(t, recorder).Reason)
			require.False(t, c.GetBool("audit_skip"))
			require.Zero(t, roleRepo.txCalls)
		})
	}
}

func TestAuthorizationHandlerGetReturnsNextHopReadinessWithoutStepUp(t *testing.T) {
	handler, roleRepo, cache := newAuthorizationHandlerTestHarness(false)
	roleRepo.readiness = service.RoleAuthorizationReadiness{Blockers: []service.RoleAuthorizationReadinessBlocker{
		{Code: service.RoleReadinessCompatibilityRoleMissing, Count: 2},
	}}
	c, recorder := newAuthorizationHandlerContext(t, http.MethodGet, "")

	handler.GetRoleAuthorizationMode(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Zero(t, cache.grantCalls)
	envelope := decodeAuthorizationResponse(t, recorder)
	var status service.RoleAuthorizationModeStatus
	require.NoError(t, json.Unmarshal(envelope.Data, &status))
	require.Equal(t, service.RoleAuthorizationModeLegacy, status.CurrentMode)
	require.Equal(t, service.RoleAuthorizationModeShadow, status.TargetMode)
	require.False(t, status.CanTransition)
	require.Equal(t, roleRepo.readiness, status.Readiness)
}

func TestAuthorizationHandlerDoesNotAcceptActorFromRequestData(t *testing.T) {
	handler, roleRepo, _ := newAuthorizationHandlerTestHarness(true)
	c, recorder := newAuthorizationHandlerContext(t, http.MethodPost, `{"expected_mode":"legacy","target_mode":"shadow"}`)
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{})

	handler.TransitionRoleAuthorizationMode(c)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Zero(t, roleRepo.txCalls)
}

// Compile-time check that the embedded cache stub remains a complete TotpCache.
var _ service.TotpCache = (*authorizationTotpCacheStub)(nil)
