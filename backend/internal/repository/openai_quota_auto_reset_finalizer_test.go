package repository

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

const (
	canonicalOpenAIAutoResetSuccessResponseBody          = `{"result_code":"success","windows_reset":2,"available_count":3,"available_count_known":true,"post_process_recorded":true,"account_state_recovered":true}`
	canonicalOpenAIAutoResetNoCreditResponseBody         = `{"result_code":"no_credit","windows_reset":0}`
	canonicalOpenAIAutoResetRecoveryDeferredResponseBody = `{"result_code":"success","windows_reset":3,"recovery_pending":true,"recovery_deferred":true}`
	canonicalOpenAIAutoResetRecoveryFailedResponseBody   = `{"result_code":"success","windows_reset":4,"post_process_recorded":true,"recovery_pending":true,"warning_code":"account_state_recovery_failed"}`
)

func TestOpenAIQuotaAutoResetFinalizerCommitsAuditAndSucceededStateAtomically(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	input := openAIAutoResetFinalizationTestInput()
	extraJSON := openAIAutoResetFinalizationTestExtraJSON(t, input.Audit.Extra)

	mock.ExpectBegin()
	expectOpenAIAutoResetFinalizationTableLock(mock)
	expectOpenAIAutoResetFinalizationLock(mock, input, service.IdempotencyStatusProcessing, nil, nil, true)
	mock.ExpectExec(regexp.QuoteMeta(openAIAutoResetAuditInsertSQL)).
		WithArgs(
			input.Audit.CreatedAt,
			nil,
			*input.Audit.ActorServicePrincipalID,
			"",
			"",
			service.AuditAuthMethodServicePrincipal,
			"",
			service.AuditActionOpenAIQuotaAutoReset,
			openAIAutoResetAuditMethod,
			input.Audit.Path,
			input.Audit.RequestID,
			"",
			"",
			"",
			input.Audit.StatusCode,
			int64(0),
			extraJSON,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectOpenAIAutoResetAuditVerification(mock, input, extraJSON, input.Audit.Path)
	mock.ExpectExec(regexp.QuoteMeta(openAIAutoResetIdempotencySucceedSQL)).
		WithArgs(
			input.IdempotencyRecordID,
			service.IdempotencyStatusSucceeded,
			input.ResponseStatus,
			canonicalOpenAIAutoResetSuccessResponseBody,
			input.ExpiresAt,
			input.ActorQualifiedScope,
			input.RequestFingerprint,
			service.IdempotencyStatusProcessing,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	finalizer := NewOpenAIQuotaAutoResetFinalizer(db)
	require.NoError(t, finalizer.FinalizeOpenAIQuotaAutoReset(context.Background(), input))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIQuotaAutoResetFinalizerAcceptsExactSucceededCommitRetry(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	input := openAIAutoResetFinalizationTestInput()
	extraJSON := openAIAutoResetFinalizationTestExtraJSON(t, input.Audit.Extra)
	responseStatus := int64(input.ResponseStatus)

	mock.ExpectBegin()
	expectOpenAIAutoResetFinalizationTableLock(mock)
	expectOpenAIAutoResetFinalizationLock(
		mock,
		input,
		service.IdempotencyStatusSucceeded,
		responseStatus,
		canonicalOpenAIAutoResetSuccessResponseBody,
		true,
	)
	expectOpenAIAutoResetAuditVerification(mock, input, extraJSON, input.Audit.Path)
	mock.ExpectCommit()

	finalizer := NewOpenAIQuotaAutoResetFinalizer(db)
	require.NoError(t, finalizer.FinalizeOpenAIQuotaAutoReset(context.Background(), input))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIQuotaAutoResetFinalizerRejectsSucceededOutcomeMismatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	input := openAIAutoResetFinalizationTestInput()
	responseStatus := int64(input.ResponseStatus)
	mock.ExpectBegin()
	expectOpenAIAutoResetFinalizationTableLock(mock)
	expectOpenAIAutoResetFinalizationLock(
		mock,
		input,
		service.IdempotencyStatusSucceeded,
		responseStatus,
		canonicalOpenAIAutoResetNoCreditResponseBody,
		true,
	)
	mock.ExpectRollback()

	finalizer := NewOpenAIQuotaAutoResetFinalizer(db)
	err = finalizer.FinalizeOpenAIQuotaAutoReset(context.Background(), input)
	require.ErrorIs(t, err, service.ErrOpenAIQuotaAutoResetFinalizationConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIQuotaAutoResetFinalizerRejectsSucceededExpiryMismatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	input := openAIAutoResetFinalizationTestInput()
	responseStatus := int64(input.ResponseStatus)
	mock.ExpectBegin()
	expectOpenAIAutoResetFinalizationTableLock(mock)
	expectOpenAIAutoResetFinalizationLock(
		mock,
		input,
		service.IdempotencyStatusSucceeded,
		responseStatus,
		canonicalOpenAIAutoResetSuccessResponseBody,
		false,
	)
	mock.ExpectRollback()

	finalizer := NewOpenAIQuotaAutoResetFinalizer(db)
	err = finalizer.FinalizeOpenAIQuotaAutoReset(context.Background(), input)
	require.ErrorIs(t, err, service.ErrOpenAIQuotaAutoResetFinalizationConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIQuotaAutoResetFinalizerRejectsSucceededRowWithoutAudit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	input := openAIAutoResetFinalizationTestInput()
	responseStatus := int64(input.ResponseStatus)
	extraJSON := openAIAutoResetFinalizationTestExtraJSON(t, input.Audit.Extra)
	mock.ExpectBegin()
	expectOpenAIAutoResetFinalizationTableLock(mock)
	expectOpenAIAutoResetFinalizationLock(
		mock,
		input,
		service.IdempotencyStatusSucceeded,
		responseStatus,
		canonicalOpenAIAutoResetSuccessResponseBody,
		true,
	)
	mock.ExpectQuery(regexp.QuoteMeta(openAIAutoResetAuditVerifySQL)).
		WithArgs(service.AuditActionOpenAIQuotaAutoReset, input.Audit.RequestID, extraJSON).
		WillReturnRows(sqlmock.NewRows([]string{
			"id",
			"actor_user_id",
			"actor_service_principal_id",
			"actor_email",
			"actor_role",
			"auth_method",
			"credential_masked",
			"action",
			"method",
			"path",
			"request_id",
			"client_ip",
			"user_agent",
			"request_body",
			"status_code",
			"latency_ms",
			"extra_matches",
		}))
	mock.ExpectRollback()

	finalizer := NewOpenAIQuotaAutoResetFinalizer(db)
	err = finalizer.FinalizeOpenAIQuotaAutoReset(context.Background(), input)
	require.ErrorIs(t, err, service.ErrOpenAIQuotaAutoResetFinalizationConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIQuotaAutoResetFinalizerRejectsMismatchedExistingAudit(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	input := openAIAutoResetFinalizationTestInput()
	extraJSON := openAIAutoResetFinalizationTestExtraJSON(t, input.Audit.Extra)
	mock.ExpectBegin()
	expectOpenAIAutoResetFinalizationTableLock(mock)
	expectOpenAIAutoResetFinalizationLock(mock, input, service.IdempotencyStatusProcessing, nil, nil, true)
	mock.ExpectExec(regexp.QuoteMeta(openAIAutoResetAuditInsertSQL)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	expectOpenAIAutoResetAuditVerification(mock, input, extraJSON, "/system/openai/accounts/99/auto-reset-credit")
	mock.ExpectRollback()

	finalizer := NewOpenAIQuotaAutoResetFinalizer(db)
	err = finalizer.FinalizeOpenAIQuotaAutoReset(context.Background(), input)
	require.ErrorIs(t, err, service.ErrOpenAIQuotaAutoResetFinalizationConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIQuotaAutoResetFinalizerRollsBackInsertedAuditWhenVerificationMismatches(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	input := openAIAutoResetFinalizationTestInput()
	extraJSON := openAIAutoResetFinalizationTestExtraJSON(t, input.Audit.Extra)
	mock.ExpectBegin()
	expectOpenAIAutoResetFinalizationTableLock(mock)
	expectOpenAIAutoResetFinalizationLock(mock, input, service.IdempotencyStatusProcessing, nil, nil, true)
	mock.ExpectExec(regexp.QuoteMeta(openAIAutoResetAuditInsertSQL)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectOpenAIAutoResetAuditVerification(mock, input, extraJSON, "/system/openai/accounts/99/auto-reset-credit")
	mock.ExpectRollback()

	finalizer := NewOpenAIQuotaAutoResetFinalizer(db)
	err = finalizer.FinalizeOpenAIQuotaAutoReset(context.Background(), input)
	require.ErrorIs(t, err, service.ErrOpenAIQuotaAutoResetFinalizationConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIQuotaAutoResetFinalizerRollsBackAuditWhenFencedUpdateLoses(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	input := openAIAutoResetFinalizationTestInput()
	extraJSON := openAIAutoResetFinalizationTestExtraJSON(t, input.Audit.Extra)
	mock.ExpectBegin()
	expectOpenAIAutoResetFinalizationTableLock(mock)
	expectOpenAIAutoResetFinalizationLock(mock, input, service.IdempotencyStatusProcessing, nil, nil, true)
	mock.ExpectExec(regexp.QuoteMeta(openAIAutoResetAuditInsertSQL)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectOpenAIAutoResetAuditVerification(mock, input, extraJSON, input.Audit.Path)
	mock.ExpectExec(regexp.QuoteMeta(openAIAutoResetIdempotencySucceedSQL)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	finalizer := NewOpenAIQuotaAutoResetFinalizer(db)
	err = finalizer.FinalizeOpenAIQuotaAutoReset(context.Background(), input)
	require.ErrorIs(t, err, service.ErrOpenAIQuotaAutoResetFinalizationConflict)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIQuotaAutoResetFinalizerRejectsIdentityBeforeStartingTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	input := openAIAutoResetFinalizationTestInput()
	input.ActorQualifiedScope = "openai_auto_reset_credit|service_principal:999"

	finalizer := NewOpenAIQuotaAutoResetFinalizer(db)
	err = finalizer.FinalizeOpenAIQuotaAutoReset(context.Background(), input)
	require.ErrorIs(t, err, service.ErrOpenAIQuotaAutoResetFinalizationInvalid)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIQuotaAutoResetFinalizerCommitsCanonicalNoCreditResponse(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	input := openAIAutoResetFinalizationTestInput()
	applyOpenAIAutoResetFinalizationTestOutcome(input, "no_credit")
	extraJSON := openAIAutoResetFinalizationTestExtraJSON(t, input.Audit.Extra)

	mock.ExpectBegin()
	expectOpenAIAutoResetFinalizationTableLock(mock)
	expectOpenAIAutoResetFinalizationLock(mock, input, service.IdempotencyStatusProcessing, nil, nil, true)
	mock.ExpectExec(regexp.QuoteMeta(openAIAutoResetAuditInsertSQL)).
		WithArgs(
			input.Audit.CreatedAt,
			nil,
			*input.Audit.ActorServicePrincipalID,
			"",
			"",
			service.AuditAuthMethodServicePrincipal,
			"",
			service.AuditActionOpenAIQuotaAutoReset,
			openAIAutoResetAuditMethod,
			input.Audit.Path,
			input.Audit.RequestID,
			"",
			"",
			"",
			input.Audit.StatusCode,
			int64(0),
			extraJSON,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	expectOpenAIAutoResetAuditVerification(mock, input, extraJSON, input.Audit.Path)
	mock.ExpectExec(regexp.QuoteMeta(openAIAutoResetIdempotencySucceedSQL)).
		WithArgs(
			input.IdempotencyRecordID,
			service.IdempotencyStatusSucceeded,
			input.ResponseStatus,
			canonicalOpenAIAutoResetNoCreditResponseBody,
			input.ExpiresAt,
			input.ActorQualifiedScope,
			input.RequestFingerprint,
			service.IdempotencyStatusProcessing,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	finalizer := NewOpenAIQuotaAutoResetFinalizer(db)
	require.NoError(t, finalizer.FinalizeOpenAIQuotaAutoReset(context.Background(), input))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPrepareOpenAIQuotaAutoResetFinalizationAcceptsLinkedRuntimeOutcomes(t *testing.T) {
	for _, outcome := range []string{"success", "no_credit", "recovery_deferred", "recovery_failed"} {
		t.Run(outcome, func(t *testing.T) {
			input := openAIAutoResetFinalizationTestInput()
			applyOpenAIAutoResetFinalizationTestOutcome(input, outcome)

			prepared, err := prepareOpenAIAutoResetFinalization(input)
			require.NoError(t, err)
			require.JSONEq(t, input.ResponseBody, prepared.responseBody)
			require.JSONEq(t, openAIAutoResetFinalizationTestExtraJSON(t, input.Audit.Extra), prepared.auditExtraJSON)
		})
	}
}

func TestOpenAIQuotaAutoResetFinalizerRejectsInvalidAuditExtraBeforeStartingTransaction(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*service.OpenAIQuotaAutoResetFinalization)
	}{
		{
			name: "missing required key",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				delete(input.Audit.Extra, "trigger_window")
			},
		},
		{
			name: "unknown key",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["unexpected"] = true
			},
		},
		{
			name: "account id wrong type",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["account_id"] = "42"
			},
		},
		{
			name: "threshold wrong type",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["threshold_5h"] = "0.9"
			},
		},
		{
			name: "utilization wrong type",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["utilization_7d"] = "0.85"
			},
		},
		{
			name: "count wrong type",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["available_count"] = 1.5
			},
		},
		{
			name: "windows reset wrong type",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["windows_reset"] = "2"
			},
		},
		{
			name: "result code wrong type",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["result_code"] = 17
			},
		},
		{
			name: "error code wrong type",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["error_code"] = 17
			},
		},
		{
			name: "invalid trigger window",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["trigger_window"] = "daily"
			},
		},
		{
			name: "negative threshold",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["threshold_5h"] = -0.01
			},
		},
		{
			name: "threshold above one",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["threshold_7d"] = 1.01
			},
		},
		{
			name: "threshold below runtime minimum",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["threshold_5h"] = 0.0009
			},
		},
		{
			name: "negative utilization",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["utilization_5h"] = -0.01
			},
		},
		{
			name: "negative available count",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["available_count"] = -1
			},
		},
		{
			name: "available count exceeds int32",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["available_count"] = int64(math.MaxInt32) + 1
			},
		},
		{
			name: "windows reset exceeds int32",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				tooLarge := int64(math.MaxInt32) + 1
				input.ResponseBody = `{"result_code":"success","windows_reset":2147483648,"post_process_recorded":true,"account_state_recovered":true}`
				input.Audit.Extra["windows_reset"] = tooLarge
			},
		},
		{
			name: "error code too long",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["error_code"] = strings.Repeat("E", 129)
			},
		},
		{
			name: "unknown audit result",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["result_code"] = "pending"
			},
		},
		{
			name: "audit status mismatches success",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.StatusCode = 409
			},
		},
		{
			name: "audit result mismatches response",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.StatusCode = 409
				input.Audit.Extra = openAIAutoResetFinalizationTestAuditExtra(input.AccountID, "no_credit", 0, "NO_RESET_CREDIT")
			},
		},
		{
			name: "windows reset mismatches response",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.Extra["windows_reset"] = 1
			},
		},
		{
			name: "deferred recovery flags missing",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				input.Audit.StatusCode = 409
				input.Audit.Extra = openAIAutoResetFinalizationTestAuditExtra(
					input.AccountID,
					"recovery_deferred",
					2,
					"OPENAI_AUTO_RESET_RECOVERY_AUTHORIZATION_DEFERRED",
				)
			},
		},
		{
			name: "failed recovery error mismatches warning",
			mutate: func(input *service.OpenAIQuotaAutoResetFinalization) {
				applyOpenAIAutoResetFinalizationTestOutcome(input, "recovery_failed")
				input.Audit.Extra["error_code"] = "DIFFERENT_WARNING"
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			input := openAIAutoResetFinalizationTestInput()
			test.mutate(input)

			_, prepareErr := prepareOpenAIAutoResetFinalization(input)
			require.ErrorIs(t, prepareErr, service.ErrOpenAIQuotaAutoResetFinalizationInvalid)

			finalizer := NewOpenAIQuotaAutoResetFinalizer(db)
			err = finalizer.FinalizeOpenAIQuotaAutoReset(context.Background(), input)
			require.ErrorIs(t, err, service.ErrOpenAIQuotaAutoResetFinalizationInvalid)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestOpenAIQuotaAutoResetFinalizerRejectsNonCanonicalResponseBeforeStartingTransaction(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "sensitive field", body: `{"result_code":"success","access_token":"secret"}`},
		{name: "unknown field", body: `{"result_code":"success","unexpected":true}`},
		{name: "legacy code field", body: `{"code":"no_credit"}`},
		{name: "wrong type", body: `{"result_code":42}`},
		{name: "unknown result code", body: `{"result_code":"recovery_failed"}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })

			input := openAIAutoResetFinalizationTestInput()
			input.ResponseBody = test.body

			_, prepareErr := prepareOpenAIAutoResetFinalization(input)
			require.ErrorIs(t, prepareErr, service.ErrOpenAIQuotaAutoResetFinalizationInvalid)

			finalizer := NewOpenAIQuotaAutoResetFinalizer(db)
			err = finalizer.FinalizeOpenAIQuotaAutoReset(context.Background(), input)
			require.ErrorIs(t, err, service.ErrOpenAIQuotaAutoResetFinalizationInvalid)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestOpenAIQuotaAutoResetFinalizerRollsBackWhenTableLockFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	input := openAIAutoResetFinalizationTestInput()
	injected := errors.New("table lock unavailable")
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(openAIAutoResetFinalizationTableLockSQL)).WillReturnError(injected)
	mock.ExpectRollback()

	finalizer := NewOpenAIQuotaAutoResetFinalizer(db)
	err = finalizer.FinalizeOpenAIQuotaAutoReset(context.Background(), input)
	require.ErrorIs(t, err, injected)
	require.ErrorContains(t, err, "lock OpenAI quota auto-reset idempotency table")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestOpenAIQuotaAutoResetFinalizerRollsBackRepositoryFailures(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	input := openAIAutoResetFinalizationTestInput()
	injected := errors.New("audit store unavailable")
	mock.ExpectBegin()
	expectOpenAIAutoResetFinalizationTableLock(mock)
	expectOpenAIAutoResetFinalizationLock(mock, input, service.IdempotencyStatusProcessing, nil, nil, true)
	mock.ExpectExec(regexp.QuoteMeta(openAIAutoResetAuditInsertSQL)).WillReturnError(injected)
	mock.ExpectRollback()

	finalizer := NewOpenAIQuotaAutoResetFinalizer(db)
	err = finalizer.FinalizeOpenAIQuotaAutoReset(context.Background(), input)
	require.ErrorIs(t, err, injected)
	require.NoError(t, mock.ExpectationsWereMet())
}

func openAIAutoResetFinalizationTestInput() *service.OpenAIQuotaAutoResetFinalization {
	principalID := int64(17)
	expiresAt := time.Date(2026, time.August, 26, 1, 2, 3, 456000000, time.UTC)
	return &service.OpenAIQuotaAutoResetFinalization{
		AccountID:           42,
		IdempotencyRecordID: 73,
		ActorQualifiedScope: openAIAutoResetIdempotencyScopePrefix + "17",
		RequestFingerprint:  strings.Repeat("a", 64),
		ResponseStatus:      200,
		ResponseBody:        canonicalOpenAIAutoResetSuccessResponseBody,
		ExpiresAt:           expiresAt,
		Audit: service.AuditLog{
			CreatedAt:               time.Date(2026, time.August, 25, 1, 2, 3, 0, time.UTC),
			ActorServicePrincipalID: &principalID,
			AuthMethod:              service.AuditAuthMethodServicePrincipal,
			Action:                  service.AuditActionOpenAIQuotaAutoReset,
			Method:                  openAIAutoResetAuditMethod,
			Path:                    "/system/openai/accounts/42/auto-reset-credit",
			RequestID:               "d0d0beef-0000-4000-8000-000000000042",
			StatusCode:              200,
			Extra:                   openAIAutoResetFinalizationTestAuditExtra(42, "success", 2, ""),
		},
	}
}

func openAIAutoResetFinalizationTestAuditExtra(
	accountID int64,
	resultCode string,
	windowsReset int,
	errorCode string,
) map[string]any {
	return map[string]any{
		"account_id":      accountID,
		"trigger_window":  "5h+7d",
		"threshold_5h":    0.9,
		"threshold_7d":    0.8,
		"utilization_5h":  0.95,
		"utilization_7d":  0.85,
		"available_count": 4,
		"result_code":     resultCode,
		"windows_reset":   windowsReset,
		"error_code":      errorCode,
	}
}

func applyOpenAIAutoResetFinalizationTestOutcome(
	input *service.OpenAIQuotaAutoResetFinalization,
	outcome string,
) {
	input.Audit.StatusCode = 200
	switch outcome {
	case "success":
		input.ResponseBody = canonicalOpenAIAutoResetSuccessResponseBody
		input.Audit.Extra = openAIAutoResetFinalizationTestAuditExtra(input.AccountID, outcome, 2, "")
	case "no_credit":
		input.ResponseBody = canonicalOpenAIAutoResetNoCreditResponseBody
		input.Audit.StatusCode = 409
		input.Audit.Extra = openAIAutoResetFinalizationTestAuditExtra(input.AccountID, outcome, 0, "NO_RESET_CREDIT")
	case "recovery_deferred":
		input.ResponseBody = canonicalOpenAIAutoResetRecoveryDeferredResponseBody
		input.Audit.StatusCode = 409
		input.Audit.Extra = openAIAutoResetFinalizationTestAuditExtra(
			input.AccountID,
			outcome,
			3,
			"OPENAI_AUTO_RESET_RECOVERY_AUTHORIZATION_DEFERRED",
		)
	case "recovery_failed":
		input.ResponseBody = canonicalOpenAIAutoResetRecoveryFailedResponseBody
		input.Audit.StatusCode = 409
		input.Audit.Extra = openAIAutoResetFinalizationTestAuditExtra(
			input.AccountID,
			outcome,
			4,
			"account_state_recovery_failed",
		)
	default:
		panic("unsupported OpenAI auto-reset test outcome: " + outcome)
	}
}

func openAIAutoResetFinalizationTestExtraJSON(t *testing.T, extra map[string]any) string {
	t.Helper()
	value, err := json.Marshal(extra)
	require.NoError(t, err)
	return string(value)
}

func expectOpenAIAutoResetFinalizationTableLock(mock sqlmock.Sqlmock) {
	mock.ExpectExec(regexp.QuoteMeta(openAIAutoResetFinalizationTableLockSQL)).
		WillReturnResult(sqlmock.NewResult(0, 0))
}

func expectOpenAIAutoResetFinalizationLock(
	mock sqlmock.Sqlmock,
	input *service.OpenAIQuotaAutoResetFinalization,
	status string,
	responseStatus any,
	responseBody any,
	expiresAtMatches bool,
) {
	mock.ExpectQuery(regexp.QuoteMeta(openAIAutoResetFinalizationLockSQL)).
		WithArgs(input.IdempotencyRecordID, input.ExpiresAt).
		WillReturnRows(sqlmock.NewRows([]string{
			"scope",
			"request_fingerprint",
			"status",
			"response_status",
			"response_body",
			"error_reason",
			"locked_until",
			"expires_at_matches",
		}).AddRow(
			input.ActorQualifiedScope,
			input.RequestFingerprint,
			status,
			responseStatus,
			responseBody,
			nil,
			nil,
			expiresAtMatches,
		))
}

func expectOpenAIAutoResetAuditVerification(
	mock sqlmock.Sqlmock,
	input *service.OpenAIQuotaAutoResetFinalization,
	extraJSON string,
	path string,
) {
	mock.ExpectQuery(regexp.QuoteMeta(openAIAutoResetAuditVerifySQL)).
		WithArgs(service.AuditActionOpenAIQuotaAutoReset, input.Audit.RequestID, extraJSON).
		WillReturnRows(sqlmock.NewRows([]string{
			"id",
			"actor_user_id",
			"actor_service_principal_id",
			"actor_email",
			"actor_role",
			"auth_method",
			"credential_masked",
			"action",
			"method",
			"path",
			"request_id",
			"client_ip",
			"user_agent",
			"request_body",
			"status_code",
			"latency_ms",
			"extra_matches",
		}).AddRow(
			91,
			nil,
			*input.Audit.ActorServicePrincipalID,
			"",
			"",
			input.Audit.AuthMethod,
			"",
			input.Audit.Action,
			input.Audit.Method,
			path,
			input.Audit.RequestID,
			"",
			"",
			"",
			input.Audit.StatusCode,
			int64(0),
			true,
		))
}
