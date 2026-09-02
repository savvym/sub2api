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

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type hostingEntitlementHandlerRepositoryStub struct {
	record        service.HostingEntitlementRecord
	changed       bool
	txErr         error
	auditErr      error
	snapshotCalls int
	txCalls       int
	lockCalls     int
	applyCalls    int
	auditCalls    int
	applyInput    service.HostingEntitlementMutationInput
	auditTrace    service.HostingEntitlementAuditTrace
}

func (r *hostingEntitlementHandlerRepositoryStub) WithHostingEntitlementSnapshot(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	r.snapshotCalls++
	return fn(ctx)
}

func (r *hostingEntitlementHandlerRepositoryStub) WithHostingEntitlementTx(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	r.txCalls++
	if r.txErr != nil {
		return r.txErr
	}
	return fn(ctx)
}

func (r *hostingEntitlementHandlerRepositoryStub) LockHostingEntitlementSubjects(
	context.Context,
	int64,
	int64,
) error {
	r.lockCalls++
	return nil
}

func (r *hostingEntitlementHandlerRepositoryStub) ReadHostingEntitlement(
	context.Context,
	int64,
) (service.HostingEntitlementRecord, error) {
	return r.record, nil
}

func (r *hostingEntitlementHandlerRepositoryStub) ApplyHostingEntitlement(
	_ context.Context,
	input service.HostingEntitlementMutationInput,
) (service.HostingEntitlementMutationResult, error) {
	r.applyCalls++
	r.applyInput = input
	if !r.changed {
		return service.HostingEntitlementMutationResult{}, nil
	}
	r.record.Hoster = input.Hoster
	r.record.HosterAssignmentExists = input.Hoster
	r.record.HosterAssignmentPermanent = input.Hoster
	r.record.AccountLimit = input.AccountLimit
	r.record.GroupLimit = input.GroupLimit
	if r.record.Version == 0 {
		r.record.Version = 1
	} else {
		r.record.Version++
	}
	return service.HostingEntitlementMutationResult{Changed: true}, nil
}

func (r *hostingEntitlementHandlerRepositoryStub) AppendHostingEntitlementAudit(
	_ context.Context,
	_ int64,
	_ service.HostingEntitlementRecord,
	_ service.HostingEntitlementRecord,
	trace service.HostingEntitlementAuditTrace,
) error {
	r.auditCalls++
	r.auditTrace = trace
	return r.auditErr
}

func (r *hostingEntitlementHandlerRepositoryStub) LockHostingCapacity(
	context.Context,
	int64,
	authz.ResourceType,
) (service.HostingCapacityRecord, error) {
	return service.HostingCapacityRecord{}, errors.New("unexpected capacity check")
}

type hostingEntitlementHandlerResolverStub struct {
	actor authz.Actor
}

func (r hostingEntitlementHandlerResolverStub) ResolveUser(
	context.Context,
	int64,
	authz.AuthMethod,
) (authz.Actor, error) {
	return r.actor, nil
}

func (r hostingEntitlementHandlerResolverStub) ResolveLegacyAdminUser(
	context.Context,
	int64,
) (authz.Actor, error) {
	return r.actor, nil
}

func (r hostingEntitlementHandlerResolverStub) ResolveServicePrincipal(
	context.Context,
	string,
	authz.AuthMethod,
) (authz.Actor, error) {
	return r.actor, nil
}

type hostingEntitlementHandlerUserRepositoryStub struct {
	service.UserRepository
	user *service.User
}

func (r *hostingEntitlementHandlerUserRepositoryStub) GetByID(
	context.Context,
	int64,
) (*service.User, error) {
	return r.user, nil
}

func (r *hostingEntitlementHandlerUserRepositoryStub) GetUserAvatar(
	context.Context,
	int64,
) (*service.UserAvatar, error) {
	return nil, nil
}

type hostingEntitlementHandlerTotpCacheStub struct {
	service.TotpCache
	granted    bool
	grantCalls int
	userID     int64
	sessionKey string
}

func (c *hostingEntitlementHandlerTotpCacheStub) HasStepUpGrant(
	_ context.Context,
	userID int64,
	sessionKey string,
) (bool, error) {
	c.grantCalls++
	c.userID = userID
	c.sessionKey = sessionKey
	return c.granted, nil
}

func TestHostingEntitlementHandlerUpdateRequiresSessionBoundStepUp(t *testing.T) {
	t.Run("recent grant required", func(t *testing.T) {
		handler, repo, cache, actor := newHostingEntitlementHandlerHarness(t, false, true)
		c, recorder := newHostingEntitlementHandlerContext(t, actor, http.MethodPut, "72", validHostingEntitlementBody())

		handler.Update(c)

		require.Equal(t, http.StatusForbidden, recorder.Code)
		require.Equal(t, 1, cache.grantCalls)
		require.Zero(t, repo.txCalls)
	})

	t.Run("session id required", func(t *testing.T) {
		handler, repo, cache, actor := newHostingEntitlementHandlerHarness(t, true, true)
		c, recorder := newHostingEntitlementHandlerContext(t, actor, http.MethodPut, "72", validHostingEntitlementBody())
		c.Set(middleware.ContextKeySessionID, " ")

		handler.Update(c)

		require.Equal(t, http.StatusUnauthorized, recorder.Code)
		require.Contains(t, recorder.Body.String(), "STEP_UP_SESSION_REQUIRED")
		require.Zero(t, cache.grantCalls)
		require.Zero(t, repo.txCalls)
	})

	t.Run("admin api key rejected", func(t *testing.T) {
		handler, repo, cache, _ := newHostingEntitlementHandlerHarness(t, true, true)
		servicePrincipal := adminHandlerTestActor(t, authz.SubjectKindServicePrincipal, 91)
		handler.service = service.NewHostingEntitlementService(
			repo,
			hostingEntitlementHandlerResolverStub{actor: servicePrincipal},
			nil,
		)
		c, recorder := newHostingEntitlementHandlerContext(
			t, servicePrincipal, http.MethodPut, "72", validHostingEntitlementBody(),
		)
		c.Set("auth_method", service.AuditAuthMethodAdminAPIKey)

		handler.Update(c)

		require.Equal(t, http.StatusForbidden, recorder.Code)
		require.Contains(t, recorder.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
		require.Zero(t, cache.grantCalls)
		require.Zero(t, repo.txCalls)
	})
}

func TestHostingEntitlementHandlerUpdateUsesStrictCompletePayload(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "unknown field", body: `{"expected_version":1,"hoster":true,"account_limit":2,"group_limit":3,"actor_user_id":99}`},
		{name: "missing expected version", body: `{"hoster":true,"account_limit":2,"group_limit":3}`},
		{name: "missing hoster", body: `{"expected_version":1,"account_limit":2,"group_limit":3}`},
		{name: "missing account limit", body: `{"expected_version":1,"hoster":true,"group_limit":3}`},
		{name: "missing group limit", body: `{"expected_version":1,"hoster":true,"account_limit":2}`},
		{name: "negative limit", body: `{"expected_version":1,"hoster":true,"account_limit":-1,"group_limit":3}`},
		{name: "trailing json", body: validHostingEntitlementBody() + `{}`},
		{name: "null body", body: `null`},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			handler, repo, _, actor := newHostingEntitlementHandlerHarness(t, true, true)
			c, recorder := newHostingEntitlementHandlerContext(t, actor, http.MethodPut, "72", testCase.body)

			handler.Update(c)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "INVALID_REQUEST")
			require.Zero(t, repo.txCalls)
			require.False(t, c.GetBool("audit_skip"))
		})
	}
}

func TestHostingEntitlementHandlerValidatesPathBeforeService(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		for _, userID := range []string{"", "0", "-1", "abc", "1.5"} {
			t.Run(method+"/"+userID, func(t *testing.T) {
				handler, repo, _, actor := newHostingEntitlementHandlerHarness(t, true, true)
				body := ""
				if method == http.MethodPut {
					body = validHostingEntitlementBody()
				}
				c, recorder := newHostingEntitlementHandlerContext(t, actor, method, userID, body)

				if method == http.MethodGet {
					handler.Get(c)
				} else {
					handler.Update(c)
				}

				require.Equal(t, http.StatusBadRequest, recorder.Code)
				require.Contains(t, recorder.Body.String(), "INVALID_USER_ID")
				require.Zero(t, repo.snapshotCalls)
				require.Zero(t, repo.txCalls)
			})
		}
	}
}

func TestHostingEntitlementHandlerUpdateForwardsTraceAndSkipsDuplicateAudit(t *testing.T) {
	handler, repo, cache, actor := newHostingEntitlementHandlerHarness(t, true, true)
	c, recorder := newHostingEntitlementHandlerContext(t, actor, http.MethodPut, "72", validHostingEntitlementBody())

	handler.Update(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, c.GetBool("audit_skip"))
	require.Equal(t, 1, cache.grantCalls)
	require.Equal(t, int64(42), cache.userID)
	require.Equal(t, "session-42", cache.sessionKey)
	require.Equal(t, service.HostingEntitlementAuditTrace{
		RequestID: "request-hosting-entitlement-1",
		ClientIP:  "198.51.100.22",
		UserAgent: "hosting-entitlement-test/1.0",
	}, repo.auditTrace)
	require.Equal(t, int64(42), repo.applyInput.ActorUserID)
	require.Equal(t, int64(72), repo.applyInput.TargetUserID)
	require.True(t, repo.applyInput.Hoster)
	require.Equal(t, int64(2), repo.applyInput.AccountLimit)
	require.Equal(t, int64(3), repo.applyInput.GroupLimit)
	require.Equal(t, 1, repo.auditCalls)

	var envelope hostingEntitlementHandlerResponseEnvelope
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	var result service.HostingEntitlementUpdateResult
	require.NoError(t, json.Unmarshal(envelope.Data, &result))
	require.True(t, result.Changed)
	require.Equal(t, int64(2), result.Version)
}

func TestHostingEntitlementHandlerNoopKeepsAttemptAuditEnabled(t *testing.T) {
	handler, repo, _, actor := newHostingEntitlementHandlerHarness(t, true, false)
	c, recorder := newHostingEntitlementHandlerContext(t, actor, http.MethodPut, "72", validHostingEntitlementBody())

	handler.Update(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.False(t, c.GetBool("audit_skip"))
	require.Zero(t, repo.auditCalls)
}

func TestHostingEntitlementHandlerGetDoesNotRequireStepUp(t *testing.T) {
	handler, repo, cache, actor := newHostingEntitlementHandlerHarness(t, false, false)
	c, recorder := newHostingEntitlementHandlerContext(t, actor, http.MethodGet, "72", "")

	handler.Get(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, repo.snapshotCalls)
	require.Zero(t, cache.grantCalls)
}

func newHostingEntitlementHandlerHarness(
	t *testing.T,
	stepUpGranted bool,
	changed bool,
) (*HostingEntitlementHandler, *hostingEntitlementHandlerRepositoryStub, *hostingEntitlementHandlerTotpCacheStub, authz.Actor) {
	t.Helper()
	actor := adminHandlerTestActor(t, authz.SubjectKindUser, 42)
	record := service.HostingEntitlementRecord{
		UserID:                    72,
		UserActive:                true,
		Hoster:                    true,
		HosterAssignmentExists:    true,
		HosterAssignmentPermanent: true,
		AccountLimit:              2,
		GroupLimit:                3,
		Version:                   1,
		AuthzVersion:              4,
	}
	if changed {
		record.Hoster = false
		record.HosterAssignmentExists = false
		record.HosterAssignmentPermanent = false
		record.AccountLimit = 0
		record.GroupLimit = 0
	}
	repo := &hostingEntitlementHandlerRepositoryStub{
		record:  record,
		changed: changed,
	}
	userRepo := &hostingEntitlementHandlerUserRepositoryStub{user: &service.User{
		ID: 42, Role: service.RoleAdmin, Status: service.StatusActive, TotpEnabled: true,
	}}
	cache := &hostingEntitlementHandlerTotpCacheStub{granted: stepUpGranted}
	userService := service.NewUserService(userRepo, nil, nil, nil)
	totpService := service.NewTotpService(userRepo, nil, cache, nil, nil, nil)
	hostingService := service.NewHostingEntitlementService(
		repo,
		hostingEntitlementHandlerResolverStub{actor: actor},
		nil,
	)
	return NewHostingEntitlementHandler(hostingService, totpService, userService), repo, cache, actor
}

func newHostingEntitlementHandlerContext(
	t *testing.T,
	actor authz.Actor,
	method string,
	userID string,
	body string,
) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(
		method,
		"/api/v1/admin/authorization/hosting-entitlements/"+userID,
		bytes.NewBufferString(body),
	)
	request = request.WithContext(context.WithValue(request.Context(), ctxkey.RequestID, "request-hosting-entitlement-1"))
	request = request.WithContext(authz.ContextWithActor(request.Context(), actor))
	request.RemoteAddr = "198.51.100.22:443"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "hosting-entitlement-test/1.0")
	c.Request = request
	c.Params = gin.Params{{Key: "user_id", Value: userID}}
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 42})
	c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
	c.Set(middleware.ContextKeySessionID, "session-42")
	c.Set("auth_method", service.AuditAuthMethodJWT)
	return c, recorder
}

func validHostingEntitlementBody() string {
	return `{"expected_version":1,"hoster":true,"account_limit":2,"group_limit":3}`
}

type hostingEntitlementHandlerResponseEnvelope struct {
	Data json.RawMessage `json:"data"`
}

var _ service.HostingEntitlementRepository = (*hostingEntitlementHandlerRepositoryStub)(nil)
var _ service.TotpCache = (*hostingEntitlementHandlerTotpCacheStub)(nil)
