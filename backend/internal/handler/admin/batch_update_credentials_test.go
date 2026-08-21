//go:build unit

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

type batchCredentialsAdminService struct {
	*stubAdminService

	actor authz.Actor
	input *service.BulkUpdateAccountsInput
	calls int
	err   error
}

func (s *batchCredentialsAdminService) BulkUpdateAccounts(
	_ context.Context,
	actor authz.Actor,
	input *service.BulkUpdateAccountsInput,
) (*service.BulkUpdateAccountsResult, error) {
	s.actor = actor
	s.input = input
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	result := &service.BulkUpdateAccountsResult{
		Success:    len(input.AccountIDs),
		SuccessIDs: append([]int64(nil), input.AccountIDs...),
		FailedIDs:  make([]int64, 0),
		Results:    make([]service.BulkUpdateAccountResult, 0, len(input.AccountIDs)),
	}
	for _, accountID := range input.AccountIDs {
		result.Results = append(result.Results, service.BulkUpdateAccountResult{AccountID: accountID, Success: true})
	}
	return result, nil
}

func setupAccountHandlerWithService(adminSvc service.AdminService) (*gin.Engine, *AccountHandler) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(withAdminTestUserActorID(1))
	handler := NewAccountHandler(adminSvc, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
	router.POST("/api/v1/admin/accounts/batch-update-credentials", handler.BatchUpdateCredentials)
	return router, handler
}

func TestBatchUpdateCredentialsUsesOneAtomicBulkCommand(t *testing.T) {
	svc := &batchCredentialsAdminService{stubAdminService: newStubAdminService()}
	router, _ := setupAccountHandlerWithService(svc)

	body, err := json.Marshal(BatchUpdateCredentialsRequest{
		AccountIDs: []int64{3, 1, 2, 2, 0, -1},
		Field:      "account_uuid",
		Value:      "test-uuid",
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, svc.calls)
	require.Equal(t, []int64{1, 2, 3}, svc.input.AccountIDs)
	require.Equal(t, map[string]any{"account_uuid": "test-uuid"}, svc.input.Credentials)
	userID, ok := svc.actor.UserID()
	require.True(t, ok)
	require.Equal(t, int64(1), userID)

	var payload struct {
		Data service.BulkUpdateAccountsResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.Equal(t, 3, payload.Data.Success)
	require.Zero(t, payload.Data.Failed)
	require.Equal(t, []int64{1, 2, 3}, payload.Data.SuccessIDs)
	require.Empty(t, payload.Data.FailedIDs)
}

func TestBatchUpdateCredentialsReturnsRequestLevelError(t *testing.T) {
	svc := &batchCredentialsAdminService{
		stubAdminService: newStubAdminService(),
		err:              service.ErrAccountNotFound,
	}
	router, _ := setupAccountHandlerWithService(svc)

	body, err := json.Marshal(BatchUpdateCredentialsRequest{
		AccountIDs: []int64{1, 2, 3},
		Field:      "org_uuid",
		Value:      "test-org",
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Equal(t, 1, svc.calls)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &payload))
	require.NotContains(t, payload, "data")
}

func TestBatchUpdateCredentialsRejectsEmptyNormalizedIDs(t *testing.T) {
	svc := &batchCredentialsAdminService{stubAdminService: newStubAdminService()}
	router, _ := setupAccountHandlerWithService(svc)

	body, err := json.Marshal(BatchUpdateCredentialsRequest{
		AccountIDs: []int64{0, -1},
		Field:      "org_uuid",
		Value:      "test-org",
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Zero(t, svc.calls)
}

func TestBatchUpdateCredentialsInterceptWarmupRequestsNonBool(t *testing.T) {
	svc := &batchCredentialsAdminService{stubAdminService: newStubAdminService()}
	router, _ := setupAccountHandlerWithService(svc)

	body, err := json.Marshal(map[string]any{
		"account_ids": []int64{1},
		"field":       "intercept_warmup_requests",
		"value":       "not-a-bool",
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Zero(t, svc.calls)
}

func TestBatchUpdateCredentialsInterceptWarmupRequestsValidBool(t *testing.T) {
	svc := &batchCredentialsAdminService{stubAdminService: newStubAdminService()}
	router, _ := setupAccountHandlerWithService(svc)

	body, err := json.Marshal(map[string]any{
		"account_ids": []int64{1},
		"field":       "intercept_warmup_requests",
		"value":       true,
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, svc.calls)
	require.Equal(t, map[string]any{"intercept_warmup_requests": true}, svc.input.Credentials)
}

func TestBatchUpdateCredentialsAccountUUIDNonString(t *testing.T) {
	svc := &batchCredentialsAdminService{stubAdminService: newStubAdminService()}
	router, _ := setupAccountHandlerWithService(svc)

	body, err := json.Marshal(map[string]any{
		"account_ids": []int64{1},
		"field":       "account_uuid",
		"value":       12345,
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Zero(t, svc.calls)
}

func TestBatchUpdateCredentialsAccountUUIDNullValue(t *testing.T) {
	svc := &batchCredentialsAdminService{stubAdminService: newStubAdminService()}
	router, _ := setupAccountHandlerWithService(svc)

	body, err := json.Marshal(map[string]any{
		"account_ids": []int64{1},
		"field":       "account_uuid",
		"value":       nil,
	})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/batch-update-credentials", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1, svc.calls)
	require.Contains(t, svc.input.Credentials, "account_uuid")
	require.Nil(t, svc.input.Credentials["account_uuid"])
}
