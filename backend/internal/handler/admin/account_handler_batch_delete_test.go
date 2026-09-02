package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type batchDeleteAdminService struct {
	*stubAdminService

	actor authz.Actor
	ids   []int64
	calls int
	err   error
}

func (s *batchDeleteAdminService) BatchDeleteAccounts(_ context.Context, actor authz.Actor, ids []int64) error {
	s.actor = actor
	s.ids = append([]int64(nil), ids...)
	s.calls++
	return s.err
}

func setupAccountBatchDeleteRouter(adminSvc service.AdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(withAdminTestUserActorID(1))
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router.POST("/api/v1/admin/accounts/batch-delete", handler.BatchDelete)
	return router
}

func TestAccountHandlerBatchDeleteUsesOneAtomicServiceCommand(t *testing.T) {
	adminSvc := &batchDeleteAdminService{stubAdminService: newStubAdminService()}
	router := setupAccountBatchDeleteRouter(adminSvc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/accounts/batch-delete",
		bytes.NewBufferString(`{"account_ids":[5,4,3,2,1,2,0,-1]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, adminSvc.calls)
	require.Equal(t, []int64{1, 2, 3, 4, 5}, adminSvc.ids)
	userID, ok := adminSvc.actor.UserID()
	require.True(t, ok)
	require.Equal(t, int64(1), userID)

	var payload struct {
		Data struct {
			Total      int     `json:"total"`
			Success    int     `json:"success"`
			Failed     int     `json:"failed"`
			SuccessIDs []int64 `json:"success_ids"`
			FailedIDs  []int64 `json:"failed_ids"`
			Errors     []struct {
				AccountID int64  `json:"account_id"`
				Error     string `json:"error"`
			} `json:"errors"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Equal(t, 5, payload.Data.Total)
	require.Equal(t, 5, payload.Data.Success)
	require.Zero(t, payload.Data.Failed)
	require.Equal(t, []int64{1, 2, 3, 4, 5}, payload.Data.SuccessIDs)
	require.Empty(t, payload.Data.FailedIDs)
	require.Empty(t, payload.Data.Errors)
}

func TestAccountHandlerBatchDeleteReturnsRequestLevelError(t *testing.T) {
	adminSvc := &batchDeleteAdminService{
		stubAdminService: newStubAdminService(),
		err:              service.ErrAccountNotFound,
	}
	router := setupAccountBatchDeleteRouter(adminSvc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/accounts/batch-delete",
		bytes.NewBufferString(`{"account_ids":[1,2,3]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, 1, adminSvc.calls)
	require.Equal(t, []int64{1, 2, 3}, adminSvc.ids)

	var payload map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.NotContains(t, payload, "data")
}

func TestAccountHandlerBatchDeleteRejectsEmptyNormalizedIDs(t *testing.T) {
	adminSvc := &batchDeleteAdminService{stubAdminService: newStubAdminService()}
	router := setupAccountBatchDeleteRouter(adminSvc)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/accounts/batch-delete",
		bytes.NewBufferString(`{"account_ids":[0,-1]}`),
	)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.Zero(t, adminSvc.calls)
}
