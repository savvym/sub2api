package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
)

var (
	errOpenAIAutoResetAccountLockContended = errors.New("OpenAI quota auto-reset account lock is already held")
	errOpenAIAutoResetEligibilityChanged   = errors.New("OpenAI quota auto-reset account eligibility changed before the external reset")
)

// openAIAutoResetExternalEffectGuard runs immediately before an upstream reset
// POST. The returned release function must be called as soon as that POST
// returns, before any account recovery or credential mutation is attempted.
type openAIAutoResetExternalEffectGuard func(ctx context.Context) (release func(), err error)

func (s *OpenAIQuotaAutoResetService) acquireOpenAIAutoResetAccountLease(
	ctx context.Context,
	actor authz.Actor,
	accountID int64,
) (OpenAIQuotaAutoResetAccountLease, error) {
	lockCtx, err := s.reauthorizeWorkerContext(ctx, actor, accountID)
	if err != nil {
		return nil, err
	}
	lease, acquired, err := s.accountLock.TryAcquire(lockCtx, accountID)
	if err != nil {
		return nil, fmt.Errorf("acquire OpenAI quota auto-reset account lock: %w", err)
	}
	if !acquired {
		return nil, errOpenAIAutoResetAccountLockContended
	}
	if openAIAutoResetLeaseIsNil(lease) {
		return nil, fmt.Errorf("acquire OpenAI quota auto-reset account lock: %w", errOpenAIAutoResetLeaseMissing)
	}
	return lease, nil
}

func releaseOpenAIAutoResetAccountLease(lease OpenAIQuotaAutoResetAccountLease, accountID int64) {
	if openAIAutoResetLeaseIsNil(lease) {
		return
	}
	if err := lease.Release(); err != nil {
		slog.Warn("openai_auto_reset_account_lock_release_failed", "account_id", accountID, "error", err)
	}
}

func (s *OpenAIQuotaAutoResetService) openAIAutoResetExternalEffectGuard(
	actor authz.Actor,
	accountID int64,
	usage *OpenAIQuotaUsage,
	now time.Time,
	assessment *openAIAutoResetAssessment,
	execution *openAIAutoResetExecutionGuard,
	initialLease OpenAIQuotaAutoResetAccountLease,
) openAIAutoResetExternalEffectGuard {
	var leaseMu sync.Mutex
	nextLease := initialLease
	return func(ctx context.Context) (func(), error) {
		leaseMu.Lock()
		lease := nextLease
		nextLease = nil
		leaseMu.Unlock()
		if openAIAutoResetLeaseIsNil(lease) {
			var err error
			lease, err = s.acquireOpenAIAutoResetAccountLease(ctx, actor, accountID)
			if err != nil {
				return nil, err
			}
		}

		release := func() {
			releaseOpenAIAutoResetAccountLease(lease, accountID)
		}
		fail := func(err error) (func(), error) {
			release()
			return nil, err
		}

		eligibilityCtx, err := s.reauthorizeWorkerContext(ctx, actor, accountID)
		if err != nil {
			return fail(err)
		}
		exists, err := lease.LockAccountRow(eligibilityCtx)
		if err != nil {
			return fail(fmt.Errorf("protect OpenAI quota auto-reset account eligibility: %w", err))
		}
		if !exists {
			return fail(errOpenAIAutoResetEligibilityChanged)
		}
		account, err := s.accountRepo.GetByID(eligibilityCtx, accountID)
		if err != nil {
			return fail(err)
		}
		config, eligible := s.resolveOpenAIAutoResetAccountEligibility(account, accountID)
		if !eligible {
			return fail(errOpenAIAutoResetEligibilityChanged)
		}

		assessmentCtx, err := s.reauthorizeWorkerContext(ctx, actor, accountID)
		if err != nil {
			return fail(err)
		}
		lockedAssessment := s.assessUsage(assessmentCtx, usage, account, config, now)
		if !lockedAssessment.resetReached {
			return fail(errOpenAIAutoResetEligibilityChanged)
		}
		if requestIdentity, hasRequestIdentity := openAIQuotaAutoResetRequestIdentityFromContext(ctx); hasRequestIdentity {
			if usage == nil || usage.autoResetIdentity == nil ||
				!openAIQuotaAutoResetIdentitiesMatch(*usage.autoResetIdentity, requestIdentity.identity, requestIdentity.allowTaskChange) {
				return fail(errOpenAIAutoResetUpstreamIdentityChanged)
			}
			lockedAuth, identityErr := openAIQuotaAuthIdentityFromAccount(account)
			if identityErr != nil {
				return fail(identityErr)
			}
			lockedIdentity, identityErr := openAIQuotaAutoResetIdentityFromAccount(account, lockedAuth)
			if identityErr != nil {
				return fail(identityErr)
			}
			if !openAIQuotaAutoResetIdentitiesMatch(requestIdentity.identity, lockedIdentity, false) {
				return fail(errOpenAIAutoResetUpstreamIdentityChanged)
			}
		}
		if _, err := s.reauthorizeWorkerContext(ctx, actor, accountID); err != nil {
			return fail(err)
		}

		if assessment != nil {
			*assessment = lockedAssessment
		}
		execution.markExternalEffectStarted()
		return release, nil
	}
}
