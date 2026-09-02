package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
)

const (
	openAIAutoResetIdempotencyScope = "openai_auto_reset_credit"
	openAIAutoResetIdempotencyRoute = "/system/openai/reset-credit/auto"
	openAIAutoResetFinalizeAttempts = 2
)

func buildOpenAIAutoResetFinalAudit(
	actor authz.Actor,
	accountID int64,
	requestID string,
	assessment openAIAutoResetAssessment,
	available int,
	resultCode string,
	windowsReset int,
	errorCode string,
) (*AuditLog, error) {
	principalID, ok := actor.ServicePrincipalID()
	if !ok {
		return nil, authz.ErrInvalidActor
	}
	statusCode := http.StatusOK
	if resultCode != "success" {
		statusCode = http.StatusConflict
	}
	return &AuditLog{
		CreatedAt:               time.Now().UTC(),
		ActorServicePrincipalID: &principalID,
		AuthMethod:              AuditAuthMethodServicePrincipal,
		Action:                  AuditActionOpenAIQuotaAutoReset,
		Method:                  "SYSTEM",
		Path:                    fmt.Sprintf("/system/openai/accounts/%d/auto-reset-credit", accountID),
		RequestID:               requestID,
		StatusCode:              statusCode,
		Extra: map[string]any{
			"account_id":      accountID,
			"trigger_window":  assessment.triggerWindow,
			"threshold_5h":    assessment.threshold5h,
			"threshold_7d":    assessment.threshold7d,
			"utilization_5h":  assessment.utilization5h,
			"utilization_7d":  assessment.utilization7d,
			"available_count": available,
			"result_code":     resultCode,
			"windows_reset":   windowsReset,
			"error_code":      errorCode,
		},
	}, nil
}

func (s *OpenAIQuotaAutoResetService) finalizeOpenAIAutoReset(
	ctx context.Context,
	accountID int64,
	finalization IdempotencySuccessFinalization,
	audit *AuditLog,
) error {
	if s == nil || s.finalizer == nil {
		return fmt.Errorf("%w: finalizer is not configured", errOpenAIAutoResetFinalization)
	}
	if audit == nil {
		return fmt.Errorf("%w: audit outcome is missing", errOpenAIAutoResetFinalization)
	}
	input := &OpenAIQuotaAutoResetFinalization{
		AccountID:           accountID,
		IdempotencyRecordID: finalization.RecordID,
		ActorQualifiedScope: finalization.Scope,
		RequestFingerprint:  finalization.RequestFingerprint,
		ResponseStatus:      finalization.ResponseStatus,
		ResponseBody:        finalization.ResponseBody,
		ExpiresAt:           finalization.ExpiresAt,
		Audit:               *audit,
	}

	var finalizationErr error
	for attempt := 0; attempt < openAIAutoResetFinalizeAttempts; attempt++ {
		finalizationErr = s.finalizer.FinalizeOpenAIQuotaAutoReset(ctx, input)
		if finalizationErr == nil {
			return nil
		}
		if ctx.Err() != nil ||
			errors.Is(finalizationErr, ErrOpenAIQuotaAutoResetFinalizationInvalid) ||
			errors.Is(finalizationErr, ErrOpenAIQuotaAutoResetFinalizationConflict) {
			break
		}
	}
	return fmt.Errorf("%w: %w", errOpenAIAutoResetFinalization, finalizationErr)
}
