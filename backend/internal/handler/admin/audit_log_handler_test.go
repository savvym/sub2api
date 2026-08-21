package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type auditLogHandlerRepository struct {
	filter *service.AuditLogFilter
}

func (r *auditLogHandlerRepository) BatchInsert(context.Context, []*service.AuditLog) (int64, error) {
	return 0, nil
}

func (r *auditLogHandlerRepository) Insert(context.Context, *service.AuditLog) error {
	return nil
}

func (r *auditLogHandlerRepository) List(_ context.Context, filter *service.AuditLogFilter) (*service.AuditLogList, error) {
	r.filter = filter
	return &service.AuditLogList{Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (r *auditLogHandlerRepository) GetByID(context.Context, int64) (*service.AuditLog, error) {
	return nil, service.ErrAuditLogNotFound
}

func (r *auditLogHandlerRepository) Count(context.Context) (int64, error) { return 0, nil }
func (r *auditLogHandlerRepository) TruncateAll(context.Context) error    { return nil }
func (r *auditLogHandlerRepository) DeleteBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func TestAuditLogHandlerListParsesServicePrincipalFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repository := &auditLogHandlerRepository{}
	handler := NewAuditLogHandler(service.NewAuditLogService(repository, nil), nil)
	router := gin.New()
	router.GET("/api/v1/admin/audit-logs", handler.List)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/audit-logs?actor_service_principal_id=41",
		nil,
	))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, repository.filter)
	require.NotNil(t, repository.filter.ActorServicePrincipalID)
	require.Equal(t, int64(41), *repository.filter.ActorServicePrincipalID)
}

type auditLogHandlerActorStore struct {
	snapshot authz.SubjectSnapshot
}

func (s *auditLogHandlerActorStore) LoadSubjectSnapshot(context.Context, authz.SubjectRef) (authz.SubjectSnapshot, error) {
	return s.snapshot, nil
}

func (s *auditLogHandlerActorStore) LoadServicePrincipalSubjectSnapshotByCode(context.Context, string) (authz.SubjectSnapshot, error) {
	return s.snapshot, nil
}

func mustAuditLogHandlerServicePrincipal(t *testing.T, id int64) authz.Actor {
	t.Helper()
	configuration, err := authz.NewPolicyConfiguration(authz.PolicyConfigurationInput{
		RoleAuthorizationMode: authz.RoleAuthorizationModeLegacy,
	})
	require.NoError(t, err)
	subject, err := authz.NewSubjectRef(authz.SubjectKindServicePrincipal, id)
	require.NoError(t, err)
	snapshot, err := authz.NewSubjectSnapshot(authz.SubjectSnapshotInput{
		Subject:       subject,
		Exists:        true,
		Active:        true,
		AuthzVersion:  1,
		Configuration: configuration,
	})
	require.NoError(t, err)
	resolver := authz.NewActorResolver(&auditLogHandlerActorStore{snapshot: snapshot})
	actor, err := resolver.ResolveServicePrincipal(
		context.Background(),
		authz.AdminAPIKeyServicePrincipalCode,
		authz.AuthMethodAdminAPIKey,
	)
	require.NoError(t, err)
	return actor
}

func TestAuditLogHandlerClearRejectsTrustedServicePrincipalBeforeLegacyAdminFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewAuditLogHandler(nil, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		actor := mustAuditLogHandlerServicePrincipal(t, 41)
		c.Request = c.Request.WithContext(authz.ContextWithActor(c.Request.Context(), actor))
		c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1})
		c.Set(string(middleware.ContextKeyUserRole), service.RoleAdmin)
		c.Set(middleware.ContextKeyAuthEmail, "first-admin@example.test")
		c.Next()
	})
	router.POST("/api/v1/admin/audit-logs/clear", handler.Clear)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/audit-logs/clear",
		strings.NewReader(`{"totp_code":"123456"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.Contains(t, recorder.Body.String(), "STEP_UP_ADMIN_API_KEY_FORBIDDEN")
}
