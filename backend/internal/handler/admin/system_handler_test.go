//go:build unit

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type systemHandlerUpdateServiceStub struct {
	performErr            error
	performHook           func()
	updateInfo            *service.UpdateInfo
	checkErr              error
	checkForces           []bool
	performCall           int
	performCtxErr         error
	performHasDeadline    bool
	rollbackCall          int
	rollbackToCall        int
	rollbackToCtxErr      error
	rollbackToHasDeadline bool
	rollbackToVersions    []string
	rollbackToErr         error
	rollbackVersions      []service.RollbackVersion
	rollbackVersionsErr   error
	rollbackVersionsCall  int
}

func (s *systemHandlerUpdateServiceStub) CheckUpdate(_ context.Context, force bool) (*service.UpdateInfo, error) {
	s.checkForces = append(s.checkForces, force)
	return s.updateInfo, s.checkErr
}

func (s *systemHandlerUpdateServiceStub) PerformUpdate(ctx context.Context) error {
	s.performCall++
	if s.performHook != nil {
		s.performHook()
	}
	s.performCtxErr = ctx.Err()
	_, s.performHasDeadline = ctx.Deadline()
	return s.performErr
}

func (s *systemHandlerUpdateServiceStub) Rollback() error {
	s.rollbackCall++
	return nil
}

func (s *systemHandlerUpdateServiceStub) ListRollbackVersions(context.Context) ([]service.RollbackVersion, error) {
	s.rollbackVersionsCall++
	return s.rollbackVersions, s.rollbackVersionsErr
}

func (s *systemHandlerUpdateServiceStub) RollbackToVersion(ctx context.Context, version string) error {
	s.rollbackToCall++
	s.rollbackToCtxErr = ctx.Err()
	_, s.rollbackToHasDeadline = ctx.Deadline()
	s.rollbackToVersions = append(s.rollbackToVersions, version)
	return s.rollbackToErr
}

type systemUpdateResponseEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Message         string `json:"message"`
		AlreadyUpToDate bool   `json:"already_up_to_date"`
		CurrentVersion  string `json:"current_version"`
		LatestVersion   string `json:"latest_version"`
		OperationID     string `json:"operation_id"`
	} `json:"data"`
}

type systemUpdateErrorEnvelope struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newSystemHandlerTestRouter(t *testing.T, updateSvc *systemHandlerUpdateServiceStub, repo service.IdempotencyRepository) *gin.Engine {
	t.Helper()
	return newSystemHandlerTestRouterWithRestartScheduler(t, updateSvc, repo, nil)
}

func newSystemHandlerTestRouterWithRestartScheduler(
	t *testing.T,
	updateSvc *systemHandlerUpdateServiceStub,
	repo service.IdempotencyRepository,
	scheduleRestart func(),
) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	service.SetDefaultIdempotencyCoordinator(nil)
	t.Cleanup(func() {
		service.SetDefaultIdempotencyCoordinator(nil)
	})

	lockSvc := service.NewSystemOperationLockService(repo, service.IdempotencyConfig{
		ProcessingTimeout:  time.Second,
		SystemOperationTTL: time.Minute,
	})
	handler := NewSystemHandler(updateSvc, lockSvc)
	if scheduleRestart != nil {
		handler.scheduleRestart = scheduleRestart
	}

	router := gin.New()
	router.Use(withAdminTestActor(t, adminHandlerTestActor(t, authz.SubjectKindUser, 1)))
	router.POST("/api/v1/admin/system/update", handler.PerformUpdate)
	router.POST("/api/v1/admin/system/rollback", handler.Rollback)
	router.POST("/api/v1/admin/system/restart", handler.RestartService)
	router.GET("/api/v1/admin/system/rollback-versions", handler.GetRollbackVersions)
	return router
}

type cancelRejectingIdempotencyRepo struct {
	*memoryIdempotencyRepoStub
	canceledFinalizationCalls int
}

type blockingSecondSuccessIdempotencyRepo struct {
	*memoryIdempotencyRepoStub

	callMu               sync.Mutex
	successCalls         int
	secondSuccessStarted chan struct{}
	releaseSecondSuccess chan struct{}
	releaseOnce          sync.Once
}

func newBlockingSecondSuccessIdempotencyRepo() *blockingSecondSuccessIdempotencyRepo {
	return &blockingSecondSuccessIdempotencyRepo{
		memoryIdempotencyRepoStub: newMemoryIdempotencyRepoStub(),
		secondSuccessStarted:      make(chan struct{}),
		releaseSecondSuccess:      make(chan struct{}),
	}
}

func (r *blockingSecondSuccessIdempotencyRepo) MarkSucceeded(
	ctx context.Context,
	id int64,
	responseStatus int,
	responseBody string,
	expiresAt time.Time,
) error {
	r.callMu.Lock()
	r.successCalls++
	call := r.successCalls
	r.callMu.Unlock()

	if call == 2 {
		close(r.secondSuccessStarted)
		<-r.releaseSecondSuccess
	}
	return r.memoryIdempotencyRepoStub.MarkSucceeded(ctx, id, responseStatus, responseBody, expiresAt)
}

func (r *blockingSecondSuccessIdempotencyRepo) releaseFinalization() {
	r.releaseOnce.Do(func() {
		close(r.releaseSecondSuccess)
	})
}

func (r *blockingSecondSuccessIdempotencyRepo) successCallCount() int {
	r.callMu.Lock()
	defer r.callMu.Unlock()
	return r.successCalls
}

func (r *cancelRejectingIdempotencyRepo) MarkSucceeded(
	ctx context.Context,
	id int64,
	responseStatus int,
	responseBody string,
	expiresAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		r.canceledFinalizationCalls++
		return err
	}
	return r.memoryIdempotencyRepoStub.MarkSucceeded(ctx, id, responseStatus, responseBody, expiresAt)
}

func (r *cancelRejectingIdempotencyRepo) MarkFailedRetryable(
	ctx context.Context,
	id int64,
	errorReason string,
	lockedUntil time.Time,
	expiresAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		r.canceledFinalizationCalls++
		return err
	}
	return r.memoryIdempotencyRepoStub.MarkFailedRetryable(ctx, id, errorReason, lockedUntil, expiresAt)
}

func requireSystemLockStatus(t *testing.T, repo *memoryIdempotencyRepoStub, wantStatus string) {
	t.Helper()
	repo.mu.Lock()
	defer repo.mu.Unlock()

	for _, record := range repo.data {
		if record.Status == wantStatus {
			return
		}
	}
	t.Fatalf("system lock status %q not found in records: %#v", wantStatus, repo.data)
}

func TestSystemHandlerPerformUpdateAlreadyUpToDateReturnsOK(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{
		performErr: service.ErrNoUpdateAvailable,
		updateInfo: &service.UpdateInfo{
			CurrentVersion: "0.1.132",
			LatestVersion:  "0.1.132",
			HasUpdate:      false,
		},
	}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/update", nil)
	req.Header.Set("Idempotency-Key", "already-up-to-date")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, updateSvc.performCall)
	require.Equal(t, []bool{false}, updateSvc.checkForces)
	requireSystemLockStatus(t, repo, service.IdempotencyStatusSucceeded)

	var body systemUpdateResponseEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 0, body.Code)
	require.Equal(t, "success", body.Message)
	require.Equal(t, "Already up to date", body.Data.Message)
	require.True(t, body.Data.AlreadyUpToDate)
	require.Equal(t, "0.1.132", body.Data.CurrentVersion)
	require.Equal(t, "0.1.132", body.Data.LatestVersion)
	require.NotEmpty(t, body.Data.OperationID)
}

func TestSystemHandlerPerformUpdateFailureStillReturnsInternalError(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{
		performErr: errors.New("download failed"),
	}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/update", nil)
	req.Header.Set("Idempotency-Key", "real-failure")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, 1, updateSvc.performCall)
	require.Empty(t, updateSvc.checkForces)
	requireSystemLockStatus(t, repo, service.IdempotencyStatusFailedRetryable)

	var body systemUpdateErrorEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, http.StatusInternalServerError, body.Code)
	require.Equal(t, "internal error", body.Message)
}

// TestSystemHandlerPerformUpdateSurvivesClientDisconnect reproduces #4504:
// the browser or a reverse proxy (axios 30s default, nginx proxy_read_timeout
// 60s) aborts the long-running update request and cancels the request
// context. The download must keep running on a detached, bounded context
// instead of dying with "download failed: context canceled".
func TestSystemHandlerPerformUpdateSurvivesClientDisconnect(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/update", nil)
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req = req.WithContext(canceledCtx)
	req.Header.Set("Idempotency-Key", "disconnected-update")
	router.ServeHTTP(rec, req)

	require.Equal(t, 1, updateSvc.performCall)
	require.NoError(t, updateSvc.performCtxErr,
		"update must not observe the canceled request context")
	require.True(t, updateSvc.performHasDeadline,
		"detached update context must still be bounded by a deadline")
	requireSystemLockStatus(t, repo, service.IdempotencyStatusSucceeded)
}

func TestSystemHandlerPerformUpdateFinalizesIdempotencyAfterClientDisconnect(t *testing.T) {
	requestCtx, cancelRequest := context.WithCancel(context.Background())
	updateSvc := &systemHandlerUpdateServiceStub{performHook: cancelRequest}
	memoryRepo := newMemoryIdempotencyRepoStub()
	repo := &cancelRejectingIdempotencyRepo{memoryIdempotencyRepoStub: memoryRepo}
	router := newSystemHandlerTestRouter(t, updateSvc, repo)
	cfg := service.DefaultIdempotencyConfig()
	cfg.ObserveOnly = false
	service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(repo, cfg))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/update", nil).WithContext(requestCtx)
	request.Header.Set("Idempotency-Key", "disconnect-finalization")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, 1, updateSvc.performCall)
	require.NoError(t, updateSvc.performCtxErr)
	require.Zero(t, repo.canceledFinalizationCalls)

	qualified, err := repo.GetByScopeAndKeyHash(
		context.Background(),
		service.BuildActorQualifiedIdempotencyScope("admin.system.update", "user:1"),
		service.HashIdempotencyKey("disconnect-finalization"),
	)
	require.NoError(t, err)
	require.NotNil(t, qualified)
	require.Equal(t, service.IdempotencyStatusSucceeded, qualified.Status)
}

func TestSystemHandlerRestartPersistsIdempotencyBeforeSchedulingAndDoesNotRescheduleReplay(t *testing.T) {
	repo := newBlockingSecondSuccessIdempotencyRepo()
	t.Cleanup(repo.releaseFinalization)

	scheduled := make(chan struct{}, 2)
	var scheduleMu sync.Mutex
	scheduleCalls := 0
	scheduleRestart := func() {
		scheduleMu.Lock()
		scheduleCalls++
		scheduleMu.Unlock()
		scheduled <- struct{}{}
	}
	scheduleCallCount := func() int {
		scheduleMu.Lock()
		defer scheduleMu.Unlock()
		return scheduleCalls
	}

	router := newSystemHandlerTestRouterWithRestartScheduler(
		t,
		&systemHandlerUpdateServiceStub{},
		repo,
		scheduleRestart,
	)
	cfg := service.DefaultIdempotencyConfig()
	cfg.ObserveOnly = false
	service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(repo, cfg))

	const (
		route          = "/api/v1/admin/system/restart"
		idempotencyKey = "restart-after-idempotency-finalization"
	)
	qualifiedScope := service.BuildActorQualifiedIdempotencyScope("admin.system.restart", "user:1")
	keyHash := service.HashIdempotencyKey(idempotencyKey)

	firstRecorder := httptest.NewRecorder()
	firstRequest := httptest.NewRequest(http.MethodPost, route, nil)
	firstRequest.Header.Set("Idempotency-Key", idempotencyKey)
	firstDone := make(chan struct{})
	go func() {
		router.ServeHTTP(firstRecorder, firstRequest)
		close(firstDone)
	}()

	select {
	case <-repo.secondSuccessStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("outer idempotency finalization did not reach MarkSucceeded")
	}

	select {
	case <-scheduled:
		t.Fatal("restart was scheduled before the idempotency result was persisted")
	case <-firstDone:
		t.Fatal("handler completed while the idempotency result was still blocked")
	default:
	}
	require.Zero(t, scheduleCallCount())
	require.Equal(t, 2, repo.successCallCount(), "system lock release must precede outer idempotency finalization")

	processing, err := repo.GetByScopeAndKeyHash(context.Background(), qualifiedScope, keyHash)
	require.NoError(t, err)
	require.NotNil(t, processing)
	require.Equal(t, service.IdempotencyStatusProcessing, processing.Status)

	repo.releaseFinalization()
	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("restart handler did not complete after idempotency finalization was released")
	}
	require.Equal(t, http.StatusOK, firstRecorder.Code)
	select {
	case <-scheduled:
	default:
		t.Fatal("restart was not scheduled after the idempotency result was persisted")
	}
	require.Equal(t, 1, scheduleCallCount())

	persisted, err := repo.GetByScopeAndKeyHash(context.Background(), qualifiedScope, keyHash)
	require.NoError(t, err)
	require.NotNil(t, persisted)
	require.Equal(t, service.IdempotencyStatusSucceeded, persisted.Status)

	replayRecorder := httptest.NewRecorder()
	replayRequest := httptest.NewRequest(http.MethodPost, route, nil)
	replayRequest.Header.Set("Idempotency-Key", idempotencyKey)
	router.ServeHTTP(replayRecorder, replayRequest)

	require.Equal(t, http.StatusOK, replayRecorder.Code)
	require.Equal(t, "true", replayRecorder.Header().Get("X-Idempotency-Replayed"))
	require.Equal(t, 1, scheduleCallCount(), "replaying a successful restart must not schedule another restart")
	require.Equal(t, 2, repo.successCallCount(), "replay must not write another success terminal state")
	select {
	case <-scheduled:
		t.Fatal("replaying a successful restart scheduled another restart")
	default:
	}
}

func TestSystemHandlerRollbackToVersionSurvivesClientDisconnect(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/rollback",
		strings.NewReader(`{"version":"0.1.146"}`))
	req.Header.Set("Content-Type", "application/json")
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	req = req.WithContext(canceledCtx)
	req.Header.Set("Idempotency-Key", "disconnected-rollback")
	router.ServeHTTP(rec, req)

	require.Equal(t, 1, updateSvc.rollbackToCall)
	require.NoError(t, updateSvc.rollbackToCtxErr,
		"versioned rollback must not observe the canceled request context")
	require.True(t, updateSvc.rollbackToHasDeadline,
		"detached rollback context must still be bounded by a deadline")
	requireSystemLockStatus(t, repo, service.IdempotencyStatusSucceeded)
}

func TestSystemHandlerRollbackWithoutBodyUsesLegacyBackup(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/rollback", nil)
	req.Header.Set("Idempotency-Key", "legacy-rollback")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, updateSvc.rollbackCall)
	require.Equal(t, 0, updateSvc.rollbackToCall)
	requireSystemLockStatus(t, repo, service.IdempotencyStatusSucceeded)
}

func TestSystemHandlerRollbackWithVersionCallsRollbackToVersion(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/rollback",
		strings.NewReader(`{"version":"0.1.146"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "rollback-to-146")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 0, updateSvc.rollbackCall)
	require.Equal(t, 1, updateSvc.rollbackToCall)
	require.Equal(t, []string{"0.1.146"}, updateSvc.rollbackToVersions)
	requireSystemLockStatus(t, repo, service.IdempotencyStatusSucceeded)

	var body systemUpdateResponseEnvelope
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 0, body.Code)
	require.Equal(t, "Rollback completed. Please restart the service.", body.Data.Message)
}

func TestSystemHandlerRollbackWithDisallowedVersionReturnsBadRequest(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{
		rollbackToErr: service.ErrRollbackVersionNotAllowed,
	}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/rollback",
		strings.NewReader(`{"version":"9.9.9"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "rollback-to-bad")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Equal(t, 1, updateSvc.rollbackToCall)
}

func TestSystemHandlerGetRollbackVersions(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{
		rollbackVersions: []service.RollbackVersion{
			{Version: "0.1.146", PublishedAt: "2026-07-07T00:00:00Z", HTMLURL: "https://example.com/v0.1.146"},
			{Version: "0.1.145", PublishedAt: "2026-07-06T00:00:00Z", HTMLURL: "https://example.com/v0.1.145"},
		},
	}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/rollback-versions", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, updateSvc.rollbackVersionsCall)

	var body struct {
		Code int `json:"code"`
		Data struct {
			Versions []service.RollbackVersion `json:"versions"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Equal(t, 0, body.Code)
	require.Len(t, body.Data.Versions, 2)
	require.Equal(t, "0.1.146", body.Data.Versions[0].Version)
}

func TestSystemHandlerGetRollbackVersionsError(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{
		rollbackVersionsErr: errors.New("github unavailable"),
	}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/system/rollback-versions", nil)
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestBuildSystemOperationIDUsesCanonicalActorScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	build := func(actor authz.Actor, compatibilityUserID int64) string {
		context, _ := gin.CreateTestContext(httptest.NewRecorder())
		context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/update", nil)
		context.Request.Header.Set("Idempotency-Key", "same-key")
		if actor.Valid() {
			context.Request = context.Request.WithContext(authz.ContextWithActor(context.Request.Context(), actor))
		}
		if compatibilityUserID > 0 {
			context.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: compatibilityUserID})
		}
		return buildSystemOperationID(context, "update")
	}

	userActor := adminHandlerTestActor(t, authz.SubjectKindUser, 42)
	principalActor := adminHandlerTestActor(t, authz.SubjectKindServicePrincipal, 42)
	require.Empty(t, build(authz.Actor{}, 0))
	require.Empty(t, build(userActor, 0))
	require.Empty(t, build(userActor, 43))
	require.Empty(t, build(principalActor, 0))

	userOperationID := build(userActor, 42)
	principalOperationID := build(principalActor, 1)
	require.NotEmpty(t, userOperationID)
	require.NotEmpty(t, principalOperationID)
	require.NotEqual(t, userOperationID, principalOperationID)
}

func TestBuildSystemOperationIDWithoutKeyIsBoundedAndScoped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	build := func(route, actorScope string) string {
		var operationID string
		router := gin.New()
		router.POST(route, func(c *gin.Context) {
			operationID = buildSystemOperationIDForActorScope(
				c,
				"rollback:"+strings.Repeat("版本", 80),
				actorScope,
			)
			c.Status(http.StatusNoContent)
		})
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, route, nil))
		return operationID
	}

	first := build("/system/rollback-a", "service_principal:7")
	secondActor := build("/system/rollback-a", "service_principal:8")
	secondRoute := build("/system/rollback-b", "service_principal:7")
	for _, operationID := range []string{first, secondActor, secondRoute} {
		require.Len(t, operationID, len("sysop-")+24)
		require.True(t, strings.HasPrefix(operationID, "sysop-"))
	}
	require.NotEqual(t, first, secondActor)
	require.NotEqual(t, first, secondRoute)
}

func TestBuildSystemOperationIdempotencyPayloads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/api/v1/admin/system/test", nil)
	context.Request.Header.Set("Idempotency-Key", "payload-table")

	emptyVersion := ""
	targetVersion := "0.1.146"
	for _, testCase := range []struct {
		name      string
		operation string
		version   *string
	}{
		{name: "update", operation: "update"},
		{name: "backup rollback", operation: "rollback", version: &emptyVersion},
		{name: "version rollback", operation: "rollback:" + targetVersion, version: &targetVersion},
		{name: "restart", operation: "restart"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			for _, actorScope := range []string{"service_principal:7", "admin:1"} {
				payload := buildSystemOperationIdempotencyPayload(context, testCase.operation, actorScope, testCase.version)
				require.Equal(
					t,
					buildSystemOperationIDForActorScope(context, testCase.operation, actorScope),
					payload["operation_id"],
				)
				if testCase.version == nil {
					require.NotContains(t, payload, "version")
				} else {
					require.Equal(t, *testCase.version, payload["version"])
				}
			}
		})
	}
}

func TestSystemHandlerReplaysLegacyActorScopedIdempotencyRecord(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	const (
		route            = "/api/v1/admin/system/update"
		idempotencyKey   = "legacy-system-update"
		legacyActorScope = "admin:1"
	)
	legacySeed := "update|" + legacyActorScope + "|" + route + "|" + idempotencyKey
	legacyOperationID := "sysop-" + service.HashIdempotencyKey(legacySeed)[:24]
	legacyPayload := gin.H{"operation_id": legacyOperationID}
	legacyFingerprint, err := service.BuildIdempotencyFingerprint(
		http.MethodPost,
		route,
		legacyActorScope,
		legacyPayload,
	)
	require.NoError(t, err)

	now := time.Now()
	legacyRecord := &service.IdempotencyRecord{
		Scope:              "admin.system.update",
		IdempotencyKeyHash: service.HashIdempotencyKey(idempotencyKey),
		RequestFingerprint: legacyFingerprint,
		Status:             service.IdempotencyStatusProcessing,
		ExpiresAt:          now.Add(time.Hour),
	}
	created, err := repo.CreateProcessing(context.Background(), legacyRecord)
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, repo.MarkSucceeded(
		context.Background(),
		legacyRecord.ID,
		http.StatusOK,
		`{"message":"legacy replay","operation_id":"`+legacyOperationID+`"}`,
		now.Add(time.Hour),
	))

	cfg := service.DefaultIdempotencyConfig()
	cfg.ObserveOnly = false
	service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(repo, cfg))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, route, nil)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "true", recorder.Header().Get("X-Idempotency-Replayed"))
	require.Contains(t, recorder.Body.String(), "legacy replay")
	require.Zero(t, updateSvc.performCall, "an upgrade retry must not execute the system update again")

	qualified, err := repo.GetByScopeAndKeyHash(
		context.Background(),
		service.BuildActorQualifiedIdempotencyScope("admin.system.update", "user:1"),
		service.HashIdempotencyKey(idempotencyKey),
	)
	require.NoError(t, err)
	require.Nil(t, qualified, "legacy replay must not create a second actor-qualified owner")
}

func TestSystemHandlerReplaysPreviousQualifiedSystemPayload(t *testing.T) {
	updateSvc := &systemHandlerUpdateServiceStub{}
	repo := newMemoryIdempotencyRepoStub()
	router := newSystemHandlerTestRouter(t, updateSvc, repo)

	const (
		route          = "/api/v1/admin/system/update"
		idempotencyKey = "previous-qualified-system-update"
		canonicalActor = "user:1"
		legacyActor    = "admin:1"
	)
	legacySeed := "update|" + legacyActor + "|" + route + "|" + idempotencyKey
	legacyOperationID := "sysop-" + service.HashIdempotencyKey(legacySeed)[:24]
	legacyPayload := gin.H{"operation_id": legacyOperationID}
	legacyFingerprint, err := service.BuildIdempotencyFingerprint(
		http.MethodPost,
		route,
		canonicalActor,
		legacyPayload,
	)
	require.NoError(t, err)

	now := time.Now()
	qualifiedRecord := &service.IdempotencyRecord{
		Scope: service.BuildActorQualifiedIdempotencyScope(
			"admin.system.update",
			canonicalActor,
		),
		IdempotencyKeyHash: service.HashIdempotencyKey(idempotencyKey),
		RequestFingerprint: legacyFingerprint,
		Status:             service.IdempotencyStatusProcessing,
		ExpiresAt:          now.Add(time.Hour),
	}
	created, err := repo.CreateProcessing(context.Background(), qualifiedRecord)
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, repo.MarkSucceeded(
		context.Background(),
		qualifiedRecord.ID,
		http.StatusOK,
		`{"message":"qualified legacy replay","operation_id":"`+legacyOperationID+`"}`,
		now.Add(time.Hour),
	))

	cfg := service.DefaultIdempotencyConfig()
	cfg.ObserveOnly = false
	service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(repo, cfg))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, route, nil)
	request.Header.Set("Idempotency-Key", idempotencyKey)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, "true", recorder.Header().Get("X-Idempotency-Replayed"))
	require.Contains(t, recorder.Body.String(), "qualified legacy replay")
	require.Zero(t, updateSvc.performCall)
}
