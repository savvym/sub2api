package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func (q *autoResetTestQuota) resetCreditTargetedGuarded(
	ctx context.Context,
	accountID int64,
	creditID string,
	redeemRequestID string,
	guard openAIAutoResetExternalEffectGuard,
) (*OpenAIQuotaResetResult, error) {
	release, err := guard(ctx)
	if err != nil {
		return nil, err
	}
	if release != nil {
		defer release()
	}
	return q.ResetCreditTargeted(ctx, accountID, creditID, redeemRequestID)
}

func (q *autoResetNoCreditQuota) resetCreditTargetedGuarded(
	ctx context.Context,
	accountID int64,
	creditID string,
	redeemRequestID string,
	guard openAIAutoResetExternalEffectGuard,
) (*OpenAIQuotaResetResult, error) {
	release, err := guard(ctx)
	if err != nil {
		return nil, err
	}
	if release != nil {
		defer release()
	}
	return q.ResetCreditTargeted(ctx, accountID, creditID, redeemRequestID)
}

func (q *autoResetAuthzQuota) resetCreditTargetedGuarded(
	ctx context.Context,
	accountID int64,
	creditID string,
	redeemRequestID string,
	guard openAIAutoResetExternalEffectGuard,
) (*OpenAIQuotaResetResult, error) {
	release, err := guard(ctx)
	if err != nil {
		return nil, err
	}
	if release != nil {
		defer release()
	}
	return q.ResetCreditTargeted(ctx, accountID, creditID, redeemRequestID)
}

type autoResetEligibilityMutationQuota struct {
	delegate *autoResetAuthzQuota
	mutate   func()

	mu          sync.Mutex
	guardCalls  int
	effectCalls int
}

func (q *autoResetEligibilityMutationQuota) QueryUsage(ctx context.Context, accountID int64) (*OpenAIQuotaUsage, error) {
	return q.delegate.QueryUsage(ctx, accountID)
}

func (q *autoResetEligibilityMutationQuota) CacheResetCreditsSnapshot(
	ctx context.Context,
	accountID int64,
	credits *OpenAIRateLimitResetCredits,
) error {
	return q.delegate.CacheResetCreditsSnapshot(ctx, accountID, credits)
}

func (q *autoResetEligibilityMutationQuota) resetCreditTargetedGuarded(
	ctx context.Context,
	accountID int64,
	creditID string,
	redeemRequestID string,
	guard openAIAutoResetExternalEffectGuard,
) (*OpenAIQuotaResetResult, error) {
	q.mu.Lock()
	q.guardCalls++
	mutate := q.mutate
	q.mu.Unlock()
	if mutate != nil {
		mutate()
	}
	release, err := guard(ctx)
	if err != nil {
		return nil, err
	}
	if release != nil {
		defer release()
	}
	q.mu.Lock()
	q.effectCalls++
	q.mu.Unlock()
	return q.delegate.ResetCreditTargeted(ctx, accountID, creditID, redeemRequestID)
}

func (q *autoResetEligibilityMutationQuota) counts() (guard, effect int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.guardCalls, q.effectCalls
}

func TestOpenAIQuotaAutoResetFinalEligibilityGuardRejectsConcurrentWriter(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		mutate func(*Account)
		assert func(testing.TB, *Account)
	}{
		{
			name: "configuration disabled",
			mutate: func(account *Account) {
				account.Extra[OpenAIAutoResetCreditEnabledExtraKey] = false
			},
			assert: func(t testing.TB, account *Account) {
				require.False(t, ResolveOpenAIAutoResetCreditConfig(account).Enabled)
			},
		},
		{
			name: "account disabled",
			mutate: func(account *Account) {
				account.Status = StatusDisabled
			},
			assert: func(t testing.TB, account *Account) {
				require.Equal(t, StatusDisabled, account.Status)
			},
		},
		{
			name: "account made unschedulable",
			mutate: func(account *Account) {
				account.Schedulable = false
			},
			assert: func(t testing.TB, account *Account) {
				require.False(t, account.Schedulable)
			},
		},
		{
			name: "threshold raised above current usage",
			mutate: func(account *Account) {
				account.Extra[OpenAIAutoResetCredit5hThresholdExtraKey] = 1.0
				account.Extra[OpenAIAutoResetCredit7dThresholdExtraKey] = 1.0
			},
			assert: func(t testing.TB, account *Account) {
				config := ResolveOpenAIAutoResetCreditConfig(account)
				require.Equal(t, 1.0, config.Threshold5h)
				require.Equal(t, 1.0, config.Threshold7d)
			},
		},
	}

	for index, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			account := newAutoResetAuthzAccount(now, int64(420+index))
			account.Extra[OpenAIAutoResetCredit5hThresholdExtraKey] = 0.5
			account.Extra[OpenAIAutoResetCredit7dThresholdExtraKey] = 0.5
			account.Extra["codex_5h_used_percent"] = 80.0
			usage := newAutoResetAuthzUsage(now)
			usage.RateLimit.PrimaryWindow.UsedPercent = 80
			fixture := newAutoResetAuthzFixture(t, account, usage)
			quota := &autoResetEligibilityMutationQuota{
				delegate: fixture.quota,
				mutate: func() {
					fixture.accountRepo.mutateAccount(testCase.mutate)
				},
			}
			fixture.service.quota = quota
			fixture.recorder.reset()

			err := fixture.service.evaluateAccount(context.Background(), account.ID)

			require.NoError(t, err)
			guardCalls, effectCalls := quota.counts()
			require.Equal(t, 1, guardCalls)
			require.Zero(t, effectCalls, "the external reset must not start after eligibility changes")
			acquireCalls, releaseCalls, contentions := fixture.accountLock.counts()
			require.Equal(t, 1, acquireCalls)
			require.Equal(t, 1, releaseCalls)
			require.Zero(t, contentions)
			require.Equal(t, 1, fixture.accountLock.rowLockCount())
			_, _, resetCalls := fixture.quota.counts()
			require.Zero(t, resetCalls)
			require.Zero(t, fixture.recoverer.count())
			require.Empty(t, fixture.auditRepo.snapshot())

			fixture.accountRepo.mu.Lock()
			storedAccount := cloneAutoResetAuthzAccount(fixture.accountRepo.account)
			fixture.accountRepo.mu.Unlock()
			testCase.assert(t, storedAccount)
			state := openAIAutoResetStateFromExtra(storedAccount.Extra)
			require.NotNil(t, state)
			require.Equal(t, OpenAIAutoResetStatusFailed, state.Status)
			require.Equal(t, "OPENAI_AUTO_RESET_ELIGIBILITY_CHANGED", state.ErrorCode)
			require.NotEmpty(t, state.AttemptCycleHash)
			require.NotEmpty(t, state.AttemptCreditHash)

			record := findAutoResetExecutionRecord(t, fixture.idempotency.inMemoryIdempotencyRepo)
			require.Equal(t, IdempotencyStatusFailedRetryable, record.Status)
			require.Nil(t, record.ResponseBody)
			events, actorErrors := fixture.recorder.snapshot()
			require.Empty(t, actorErrors)
			require.Contains(t, events, "account_lock")
			require.Contains(t, events, "idempotency.failed")
			require.NotContains(t, events, "reset")
			require.NotContains(t, events, "atomic.finalize")
		})
	}
}
