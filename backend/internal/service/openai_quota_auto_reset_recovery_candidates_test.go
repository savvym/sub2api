package service

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/stretchr/testify/require"
)

func (r *autoResetAuthzAccountRepo) ListOpenAIAutoResetRecoveryCandidatePage(
	ctx context.Context,
	_ OpenAIAutoResetRecoveryCandidatePageOptions,
) (*OpenAIAutoResetRecoveryCandidatePage, error) {
	if r.recorder != nil {
		r.recorder.record(ctx, "recovery.list")
	}
	return &OpenAIAutoResetRecoveryCandidatePage{AccountIDs: []int64{}}, nil
}

type autoResetRecoveryCandidateTestRepo struct {
	*autoResetAuthzAccountRepo

	mu      sync.Mutex
	pages   map[int64]*OpenAIAutoResetRecoveryCandidatePage
	options []OpenAIAutoResetRecoveryCandidatePageOptions
	err     error
	nilPage bool
}

func (r *autoResetRecoveryCandidateTestRepo) ListOpenAIAutoResetRecoveryCandidatePage(
	ctx context.Context,
	options OpenAIAutoResetRecoveryCandidatePageOptions,
) (*OpenAIAutoResetRecoveryCandidatePage, error) {
	if r.recorder != nil {
		r.recorder.record(ctx, "recovery.list")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.options = append(r.options, options)
	if r.err != nil {
		return nil, r.err
	}
	if r.nilPage {
		return nil, nil
	}
	page := r.pages[options.AfterID]
	if page == nil {
		return &OpenAIAutoResetRecoveryCandidatePage{AccountIDs: []int64{}}, nil
	}
	copy := *page
	copy.AccountIDs = append([]int64(nil), page.AccountIDs...)
	return &copy, nil
}

func (r *autoResetRecoveryCandidateTestRepo) optionSnapshot() []OpenAIAutoResetRecoveryCandidatePageOptions {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]OpenAIAutoResetRecoveryCandidatePageOptions(nil), r.options...)
}

type autoResetAccountRepositoryWithoutRecoveryPager struct {
	AccountRepository
}

func TestOpenAIQuotaAutoResetStartRequiresRecoveryCandidatePager(t *testing.T) {
	now := time.Now().UTC()
	fixture := newAutoResetAuthzFixture(t, newAutoResetAuthzAccount(now, 401), newAutoResetAuthzUsage(now))
	fixture.service.accountRepo = &autoResetAccountRepositoryWithoutRecoveryPager{AccountRepository: fixture.accountRepo}

	err := fixture.service.Start()
	fixture.service.Stop()

	require.ErrorIs(t, err, authz.ErrAuthorizationUnavailable)
}

func TestOpenAIQuotaAutoResetPendingRecoveryPrecedesMutableEligibility(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		mutate func(*Account)
	}{
		{
			name: "auto reset disabled",
			mutate: func(account *Account) {
				account.Extra[OpenAIAutoResetCreditEnabledExtraKey] = false
			},
		},
		{
			name: "account inactive",
			mutate: func(account *Account) {
				account.Status = StatusDisabled
			},
		},
		{
			name: "account error status",
			mutate: func(account *Account) {
				account.Status = StatusError
			},
		},
		{
			name: "account unschedulable",
			mutate: func(account *Account) {
				account.Schedulable = false
			},
		},
	}

	for index, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			account := newAutoResetAuthzAccount(now, int64(410+index))
			fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))
			fixture.quota.setResetHook(func(call int) {
				if call == 1 {
					fixture.store.setWorkerPermissions(nil)
				}
			})

			err := fixture.service.evaluateAccount(context.Background(), account.ID)
			require.ErrorIs(t, err, authz.ErrPolicyAccessDenied)
			_, _, resetCalls := fixture.quota.counts()
			require.Equal(t, 1, resetCalls)
			require.Zero(t, fixture.recoverer.count())

			fixture.accountRepo.mu.Lock()
			pendingState := openAIAutoResetStateFromExtra(fixture.accountRepo.account.Extra)
			require.NotNil(t, pendingState)
			require.Equal(t, OpenAIAutoResetStatusResetting, pendingState.Status)
			testCase.mutate(fixture.accountRepo.account)
			fixture.accountRepo.mu.Unlock()
			fixture.quota.setResetHook(nil)
			fixture.store.setWorkerPermissions([]string{string(authz.CapabilityPlatformAccountOpenAIQuotaAutoReset)})

			require.NoError(t, fixture.service.evaluateAccount(context.Background(), account.ID))

			_, _, resetCalls = fixture.quota.counts()
			require.Equal(t, 1, resetCalls, "pending recovery must not issue a second upstream reset")
			require.Equal(t, 1, fixture.recoverer.count())
			fixture.accountRepo.mu.Lock()
			recoveredState := openAIAutoResetStateFromExtra(fixture.accountRepo.account.Extra)
			fixture.accountRepo.mu.Unlock()
			require.NotNil(t, recoveredState)
			require.Equal(t, OpenAIAutoResetStatusSuccess, recoveredState.Status)
			require.Len(t, fixture.auditRepo.snapshot(), 1)
		})
	}
}

func TestOpenAIQuotaAutoResetMalformedPendingIdentityFailsClosedBeforeEligibility(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name     string
		status   string
		credit   string
		cycle    string
		disabled bool
		rawState any
	}{
		{name: "resetting missing identity", status: OpenAIAutoResetStatusResetting},
		{
			name:   "resetting uppercase identity",
			status: OpenAIAutoResetStatusResetting,
			credit: "AAAAAAAAAAAAAAAAAAAAAAAA",
			cycle:  "222222222222222222222222",
		},
		{
			name:   "failed partial identity",
			status: OpenAIAutoResetStatusFailed,
			credit: "111111111111111111111111",
		},
		{
			name:     "disabled failed malformed identity",
			status:   OpenAIAutoResetStatusFailed,
			credit:   "short",
			cycle:    "222222222222222222222222",
			disabled: true,
		},
		{
			name: "numeric attempt identity",
			rawState: map[string]any{
				"status":              OpenAIAutoResetStatusResetting,
				"attempt_credit_hash": 111111111111111111,
				"attempt_cycle_hash":  "222222222222222222222222",
			},
		},
		{
			name: "malformed auxiliary state",
			rawState: map[string]any{
				"status":               OpenAIAutoResetStatusResetting,
				"attempt_credit_hash":  "111111111111111111111111",
				"attempt_cycle_hash":   "222222222222222222222222",
				"available_count":      "1",
				"trigger_window":       7,
				"unknown_future_field": true,
			},
		},
		{
			name: "out of range available count",
			rawState: map[string]any{
				"status":                OpenAIAutoResetStatusResetting,
				"attempt_credit_hash":   "111111111111111111111111",
				"attempt_cycle_hash":    "222222222222222222222222",
				"available_count":       float64(2147483648),
				"post_process_recorded": false,
			},
		},
		{
			name: "fractional representation available count",
			rawState: map[string]any{
				"status":                OpenAIAutoResetStatusResetting,
				"attempt_credit_hash":   "111111111111111111111111",
				"attempt_cycle_hash":    "222222222222222222222222",
				"available_count":       json.Number("1.0"),
				"post_process_recorded": false,
			},
		},
		{
			name: "unknown managed state field",
			rawState: map[string]any{
				"status":               OpenAIAutoResetStatusResetting,
				"attempt_credit_hash":  "111111111111111111111111",
				"attempt_cycle_hash":   "222222222222222222222222",
				"unknown_future_field": true,
			},
		},
		{
			name: "unknown managed status",
			rawState: map[string]any{
				"status":              "mystery",
				"attempt_credit_hash": "111111111111111111111111",
				"attempt_cycle_hash":  "222222222222222222222222",
			},
		},
		{
			name:     "non object managed state",
			rawState: "resetting",
		},
	}

	for index, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			account := newAutoResetAuthzAccount(now, int64(440+index))
			if testCase.disabled {
				account.Status = StatusDisabled
				account.Schedulable = false
				account.Extra[OpenAIAutoResetCreditEnabledExtraKey] = false
			}
			if testCase.rawState != nil {
				account.Extra[OpenAIAutoResetCreditStateExtraKey] = testCase.rawState
			} else {
				account.Extra[OpenAIAutoResetCreditStateExtraKey] = &OpenAIAutoResetCreditState{
					Status:            testCase.status,
					AttemptCreditHash: testCase.credit,
					AttemptCycleHash:  testCase.cycle,
				}
			}
			fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))

			err := fixture.service.evaluateAccount(context.Background(), account.ID)

			require.ErrorIs(t, err, ErrOpenAIAutoResetReconciliationRequired)
			queryCalls, cacheCalls, resetCalls := fixture.quota.counts()
			require.Zero(t, queryCalls)
			require.Zero(t, cacheCalls)
			require.Zero(t, resetCalls)
			require.Zero(t, fixture.recoverer.count())
			require.Zero(t, fixture.idempotency.createCount())
		})
	}
}

func TestOpenAIQuotaAutoResetFailedWithoutAttemptIdentityIsPreEffectState(t *testing.T) {
	service := &OpenAIQuotaAutoResetService{}
	handled, err := service.resumeOpenAIAutoResetRecovery(
		context.Background(),
		authz.Actor{},
		1,
		&OpenAIAutoResetCreditState{Status: OpenAIAutoResetStatusFailed},
	)

	require.NoError(t, err)
	require.False(t, handled)
}

func TestOpenAIQuotaAutoResetMutableIneligibilityWithoutPendingDoesNotExecute(t *testing.T) {
	now := time.Now().UTC()
	account := newAutoResetAuthzAccount(now, 419)
	account.Status = StatusError
	account.Schedulable = false
	account.Extra[OpenAIAutoResetCreditEnabledExtraKey] = false
	fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))

	require.NoError(t, fixture.service.evaluateAccount(context.Background(), account.ID))

	require.Zero(t, fixture.recoverer.count())
	queryCalls, cacheCalls, resetCalls := fixture.quota.counts()
	require.Zero(t, queryCalls)
	require.Zero(t, cacheCalls)
	require.Zero(t, resetCalls)
	require.Zero(t, fixture.idempotency.createCount())
	_, updateCalls, _, states := fixture.accountRepo.counts()
	require.Zero(t, updateCalls)
	require.Empty(t, states)
}

func TestOpenAIQuotaAutoResetPendingRecoveryRespectsStructuralIdentity(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name         string
		mutate       func(*Account)
		wantParentID int64
	}{
		{
			name: "different account id",
			mutate: func(account *Account) {
				account.ID++
			},
		},
		{
			name: "non OpenAI platform",
			mutate: func(account *Account) {
				account.Platform = PlatformAnthropic
			},
		},
		{
			name: "non OAuth type",
			mutate: func(account *Account) {
				account.Type = AccountTypeAPIKey
			},
		},
		{
			name: "shadow account",
			mutate: func(account *Account) {
				parentID := int64(999)
				account.ParentAccountID = &parentID
			},
			wantParentID: 999,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			account := newAutoResetAuthzAccount(now, 418)
			account.Extra[OpenAIAutoResetCreditStateExtraKey] = &OpenAIAutoResetCreditState{
				Status:            OpenAIAutoResetStatusResetting,
				AttemptCreditHash: "111111111111111111111111",
				AttemptCycleHash:  "222222222222222222222222",
			}
			testCase.mutate(account)
			fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))

			require.NoError(t, fixture.service.evaluateAccount(context.Background(), 418))

			require.Zero(t, fixture.recoverer.count())
			require.Zero(t, fixture.idempotency.createCount())
			events, actorErrors := fixture.recorder.snapshot()
			require.Empty(t, actorErrors)
			require.NotContains(t, events, "idempotency.get")
			if testCase.wantParentID == 0 {
				require.Empty(t, fixture.service.queue)
			} else {
				require.Equal(t, 1, len(fixture.service.queue))
				require.Equal(t, testCase.wantParentID, <-fixture.service.queue)
			}
		})
	}
}

func TestOpenAIQuotaAutoResetScannerPrioritizesRecoveryCandidates(t *testing.T) {
	now := time.Now().UTC()
	fixture := newAutoResetAuthzFixture(t, newAutoResetAuthzAccount(now, 420), newAutoResetAuthzUsage(now))
	regular := newAutoResetAuthzAccount(now, 425)
	fixture.accountRepo.listAccounts = []Account{*regular}
	repo := &autoResetRecoveryCandidateTestRepo{
		autoResetAuthzAccountRepo: fixture.accountRepo,
		pages: map[int64]*OpenAIAutoResetRecoveryCandidatePage{
			0: {
				AccountIDs:  []int64{421, 422},
				NextAfterID: 422,
				HasMore:     true,
			},
			422: {
				AccountIDs: []int64{423, regular.ID},
			},
		},
	}
	fixture.service.accountRepo = repo
	fixture.recorder.reset()

	fixture.service.scanEnabledAccounts(context.Background())

	queued := make([]int64, 0, 4)
	for len(fixture.service.queue) > 0 {
		queued = append(queued, <-fixture.service.queue)
	}
	require.Equal(t, []int64{421, 422, 423, regular.ID}, queued)
	require.Equal(t, []OpenAIAutoResetRecoveryCandidatePageOptions{
		{AfterID: 0, Limit: openAIAutoResetBatchSize},
		{AfterID: 422, Limit: openAIAutoResetBatchSize},
	}, repo.optionSnapshot())
	_, _, listCalls, _ := fixture.accountRepo.counts()
	require.Equal(t, 1, listCalls)
	events, actorErrors := fixture.recorder.snapshot()
	require.Empty(t, actorErrors)
	require.Equal(t, 2, countAutoResetEvent(events, "recovery.list"))
	require.Less(t, indexAutoResetEvent(events, "recovery.list"), indexAutoResetEvent(events, "list"))
}

func TestOpenAIQuotaAutoResetRecoveryScannerDoesNotDropCandidateWhenQueueIsFull(t *testing.T) {
	now := time.Now().UTC()
	fixture := newAutoResetAuthzFixture(t, newAutoResetAuthzAccount(now, 426), newAutoResetAuthzUsage(now))
	repo := &autoResetRecoveryCandidateTestRepo{
		autoResetAuthzAccountRepo: fixture.accountRepo,
		pages: map[int64]*OpenAIAutoResetRecoveryCandidatePage{
			0: {AccountIDs: []int64{427, 428}},
		},
	}
	fixture.service.accountRepo = repo
	fixture.service.queue = make(chan int64, 1)

	scanDone := make(chan bool, 1)
	go func() {
		scanDone <- fixture.service.scanOpenAIAutoResetRecoveryCandidates(
			context.Background(),
			fixture.actor,
		)
	}()

	require.Eventually(t, func() bool {
		return len(fixture.service.queue) == 1
	}, time.Second, time.Millisecond)
	select {
	case <-scanDone:
		t.Fatal("recovery scan returned before the full queue accepted the later candidate")
	default:
	}
	require.Equal(t, int64(427), <-fixture.service.queue)
	require.Equal(t, int64(428), <-fixture.service.queue)
	require.True(t, <-scanDone)
}

func TestOpenAIQuotaAutoResetScannerStopsWhenRecoveryScanCannotAdvance(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*autoResetRecoveryCandidateTestRepo)
		wantQueue []int64
	}{
		{
			name: "query error",
			configure: func(repo *autoResetRecoveryCandidateTestRepo) {
				repo.err = context.Canceled
			},
		},
		{
			name: "nil page",
			configure: func(repo *autoResetRecoveryCandidateTestRepo) {
				repo.nilPage = true
			},
		},
		{
			name: "cursor does not advance",
			configure: func(repo *autoResetRecoveryCandidateTestRepo) {
				repo.pages = map[int64]*OpenAIAutoResetRecoveryCandidatePage{
					0: {
						AccountIDs:  []int64{431},
						NextAfterID: 0,
						HasMore:     true,
					},
				}
			},
			wantQueue: []int64{431},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			now := time.Now().UTC()
			fixture := newAutoResetAuthzFixture(t, newAutoResetAuthzAccount(now, 430), newAutoResetAuthzUsage(now))
			repo := &autoResetRecoveryCandidateTestRepo{autoResetAuthzAccountRepo: fixture.accountRepo}
			testCase.configure(repo)
			fixture.service.accountRepo = repo

			fixture.service.scanEnabledAccounts(context.Background())

			var queued []int64
			for len(fixture.service.queue) > 0 {
				queued = append(queued, <-fixture.service.queue)
			}
			require.Equal(t, testCase.wantQueue, queued)
			_, _, listCalls, _ := fixture.accountRepo.counts()
			require.Zero(t, listCalls, "ordinary enabled scan must not run after an incomplete recovery scan")
			require.Len(t, repo.optionSnapshot(), 1)
		})
	}
}
