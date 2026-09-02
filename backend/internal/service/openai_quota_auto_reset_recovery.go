package service

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrOpenAIAutoResetReconciliationRequired = infraerrors.Conflict(
	"OPENAI_AUTO_RESET_RECONCILIATION_REQUIRED",
	"automatic reset outcome requires explicit reconciliation",
)

func (s *OpenAIQuotaAutoResetService) resumeOpenAIAutoResetRecovery(
	ctx context.Context,
	actor authz.Actor,
	accountID int64,
	state *OpenAIAutoResetCreditState,
) (bool, error) {
	if state == nil ||
		(state.Status != OpenAIAutoResetStatusResetting && state.Status != OpenAIAutoResetStatusFailed) {
		return false, nil
	}

	creditHash := state.AttemptCreditHash
	cycleHash := state.AttemptCycleHash
	if state.Status == OpenAIAutoResetStatusFailed && creditHash == "" && cycleHash == "" {
		// Query and other pre-effect failures legitimately have no attempt
		// identity and may proceed through normal eligibility evaluation.
		return false, nil
	}
	if !validOpenAIAutoResetAttemptHash(creditHash) || !validOpenAIAutoResetAttemptHash(cycleHash) {
		return true, openAIAutoResetReconciliationError("pending attempt identity is malformed")
	}

	record, err := s.loadOpenAIAutoResetAttempt(ctx, actor, accountID, state)
	if err != nil {
		return true, err
	}
	if record == nil {
		return true, openAIAutoResetReconciliationError("terminal idempotency record is missing")
	}
	switch record.Status {
	case IdempotencyStatusProcessing:
		if record.LockedUntil != nil && record.LockedUntil.After(time.Now()) {
			return true, nil
		}
		return true, openAIAutoResetReconciliationError("external-effecting attempt remains processing")
	case IdempotencyStatusFailedRetryable:
		// Migration 243 converts legacy retryable auto-reset outcomes to protected
		// processing rows. A retryable row created by this binary is therefore known
		// to have failed before ResetCreditTargeted was entered.
		return false, nil
	case IdempotencyStatusSucceeded:
		if record.ResponseStatus == nil || *record.ResponseStatus != http.StatusOK || record.ResponseBody == nil {
			return true, openAIAutoResetReconciliationError("terminal idempotency response is incomplete")
		}
	default:
		return true, openAIAutoResetReconciliationError("idempotency status is not recoverable")
	}

	stored, err := s.idempotency.decodeStoredResponse(record.ResponseBody)
	if err != nil {
		return true, openAIAutoResetReconciliationError("terminal idempotency response is corrupt")
	}
	consumeResult, decodeErr := decodeOpenAIAutoResetConsumeResult(stored)
	if decodeErr != nil {
		return true, openAIAutoResetReconciliationError("terminal idempotency response is corrupt")
	}
	if consumeResult.ResultCode == openAIAutoResetResultCodeNoCredit {
		resultAt := time.Now().UTC().Format(time.RFC3339)
		return true, s.persistState(ctx, actor, accountID, &OpenAIAutoResetCreditState{
			Status:            OpenAIAutoResetStatusNoCredit,
			TriggerWindow:     state.TriggerWindow,
			AvailableCount:    0,
			CheckedAt:         resultAt,
			LastResultAt:      resultAt,
			ErrorCode:         "NO_RESET_CREDIT",
			AttemptCycleHash:  state.AttemptCycleHash,
			AttemptCreditHash: state.AttemptCreditHash,
		})
	}

	if !consumeResult.PostProcessRecorded || consumeResult.RecoveryPending ||
		!consumeResult.AccountStateRecovered || consumeResult.WarningCode != "" {
		postCtx, cancelPost := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
		postGuard := &openAIAutoResetPostProcessGuard{service: s, actor: actor}
		post := RunOpenAIQuotaResetPostProcess(postCtx, accountID, postGuard, postGuard, postGuard.LoadAccount)
		cancelPost()
		if authorizeErr := postGuard.AuthorizationError(); authorizeErr != nil {
			return true, authorizeErr
		}
		consumeResult.PostProcessRecorded = true
		consumeResult.RecoveryPending = !post.AccountStateRecovered || post.WarningCode != ""
		consumeResult.RecoveryDeferred = false
		consumeResult.AccountStateRecovered = post.AccountStateRecovered
		consumeResult.WarningCode = post.WarningCode
		if post.Quota != nil && post.Quota.RateLimitResetCredits != nil {
			consumeResult.AvailableCount = post.Quota.RateLimitResetCredits.AvailableCount
			consumeResult.AvailableCountKnown = true
		}
	}
	if consumeResult.RecoveryPending || !consumeResult.AccountStateRecovered || consumeResult.WarningCode != "" {
		code := consumeResult.WarningCode
		if code == "" {
			code = OpenAIQuotaResetWarningAccountRecoveryFailed
		}
		return true, s.failState(ctx, actor, accountID, state, code, nil)
	}

	resultAt := time.Now().UTC().Format(time.RFC3339)
	available := max(0, state.AvailableCount-1)
	if consumeResult.AvailableCountKnown {
		available = consumeResult.AvailableCount
	}
	return true, s.persistState(ctx, actor, accountID, &OpenAIAutoResetCreditState{
		Status:            OpenAIAutoResetStatusSuccess,
		TriggerWindow:     state.TriggerWindow,
		AvailableCount:    available,
		CheckedAt:         resultAt,
		LastResultAt:      resultAt,
		AttemptCycleHash:  state.AttemptCycleHash,
		AttemptCreditHash: state.AttemptCreditHash,
	})
}

func validOpenAIAutoResetAttemptHash(value string) bool {
	if len(value) != 24 {
		return false
	}
	for _, char := range value {
		if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
			return false
		}
	}
	return true
}

func (s *OpenAIQuotaAutoResetService) loadOpenAIAutoResetAttempt(
	ctx context.Context,
	actor authz.Actor,
	accountID int64,
	state *OpenAIAutoResetCreditState,
) (*IdempotencyRecord, error) {
	actorScope, ok := actor.SubjectKey()
	if !ok {
		return nil, authz.ErrInvalidActor
	}
	payload := map[string]any{
		"account_id":  accountID,
		"credit_hash": state.AttemptCreditHash,
		"cycle_hash":  state.AttemptCycleHash,
	}
	currentFingerprint, err := BuildIdempotencyFingerprint(
		http.MethodPost,
		openAIAutoResetIdempotencyRoute,
		actorScope,
		payload,
	)
	if err != nil {
		return nil, err
	}
	legacyFingerprint, err := BuildIdempotencyFingerprint(
		http.MethodPost,
		openAIAutoResetIdempotencyRoute,
		fmt.Sprintf("account:%d", accountID),
		payload,
	)
	if err != nil {
		return nil, err
	}
	compatible := map[string]struct{}{
		currentFingerprint: {},
		legacyFingerprint:  {},
	}
	stableKey := fmt.Sprintf("oarc:%d:%s:%s", accountID, state.AttemptCreditHash, state.AttemptCycleHash)
	keyHash := HashIdempotencyKey(stableKey)
	repo := &openAIAutoResetAuthorizedIdempotencyRepository{
		delegate:  s.idempotency.repo,
		service:   s,
		actor:     actor,
		accountID: accountID,
	}

	raw, err := repo.GetByScopeAndKeyHash(ctx, openAIAutoResetIdempotencyScope, keyHash)
	if err != nil {
		return nil, err
	}
	if raw != nil && raw.RequestFingerprint != idempotencyUpgradeFenceFingerprint {
		if _, ok := compatible[raw.RequestFingerprint]; !ok {
			return nil, openAIAutoResetReconciliationError("legacy idempotency fingerprint conflicts with pending attempt")
		}
		return raw, nil
	}

	qualifiedScope := BuildActorQualifiedIdempotencyScope(openAIAutoResetIdempotencyScope, actorScope)
	qualified, err := repo.GetByScopeAndKeyHash(ctx, qualifiedScope, keyHash)
	if err != nil {
		return nil, err
	}
	if qualified != nil {
		if _, ok := compatible[qualified.RequestFingerprint]; !ok {
			return nil, openAIAutoResetReconciliationError("idempotency fingerprint conflicts with pending attempt")
		}
	}
	return qualified, nil
}

func openAIAutoResetReconciliationError(reason string) error {
	return fmt.Errorf("%w: %s", ErrOpenAIAutoResetReconciliationRequired, reason)
}
