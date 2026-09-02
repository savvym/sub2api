package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalizeOpenAIAutoResetCreditExtra(t *testing.T) {
	t.Run("历史账号默认关闭", func(t *testing.T) {
		account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
		config := ResolveOpenAIAutoResetCreditConfig(account)
		require.False(t, config.Enabled)
		require.Equal(t, 1.0, config.Threshold5h)
		require.Equal(t, 1.0, config.Threshold7d)
	})

	t.Run("开启时补齐两个百分百阈值并剥离运行态", func(t *testing.T) {
		extra, err := normalizeOpenAIAutoResetCreditExtra(PlatformOpenAI, AccountTypeOAuth, false, map[string]any{
			OpenAIAutoResetCreditEnabledExtraKey: true,
			OpenAIAutoResetCreditStateExtraKey:   map[string]any{"status": "success"},
		})
		require.NoError(t, err)
		require.Equal(t, 1.0, extra[OpenAIAutoResetCredit5hThresholdExtraKey])
		require.Equal(t, 1.0, extra[OpenAIAutoResetCredit7dThresholdExtraKey])
		require.NotContains(t, extra, OpenAIAutoResetCreditStateExtraKey)
	})

	t.Run("阈值和账号类型严格校验", func(t *testing.T) {
		_, err := normalizeOpenAIAutoResetCreditExtra(PlatformOpenAI, AccountTypeOAuth, false, map[string]any{
			OpenAIAutoResetCreditEnabledExtraKey:     true,
			OpenAIAutoResetCredit5hThresholdExtraKey: 0.0009,
		})
		require.Error(t, err)

		_, err = normalizeOpenAIAutoResetCreditExtra(PlatformOpenAI, AccountTypeOAuth, true, map[string]any{
			OpenAIAutoResetCreditEnabledExtraKey: true,
		})
		require.Error(t, err)
	})
}

func TestShouldAutoPauseOpenAIAccountByQuota_AutoResetCreditStates(t *testing.T) {
	now := time.Now().UTC()
	baseExtra := map[string]any{
		OpenAIAutoResetCreditEnabledExtraKey:     true,
		OpenAIAutoResetCredit5hThresholdExtraKey: 1.0,
		OpenAIAutoResetCredit7dThresholdExtraKey: 1.0,
		"auto_pause_5h_threshold":                0.8,
		"auto_pause_7d_disabled":                 true,
		"codex_5h_used_percent":                  90.0,
		"codex_usage_updated_at":                 now.Format(time.RFC3339),
		"codex_5h_reset_at":                      now.Add(time.Hour).Format(time.RFC3339),
	}

	t.Run("卡状态未知时暂停并触发异步查询", func(t *testing.T) {
		account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: cloneOpenAIAutoResetExtra(baseExtra)}
		paused, decision := shouldAutoPauseOpenAIAccountByQuota(context.Background(), account)
		require.True(t, paused)
		require.Equal(t, "quota_auto_reset_credit_check_5h", decision.reason)
	})

	t.Run("明确有卡时允许继续到用卡阈值", func(t *testing.T) {
		extra := cloneOpenAIAutoResetExtra(baseExtra)
		extra[OpenAIAutoResetCreditStateExtraKey] = OpenAIAutoResetCreditState{
			Status: OpenAIAutoResetStatusAvailable, AvailableCount: 1, CheckedAt: now.Format(time.RFC3339),
		}
		account := &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
		paused, _ := shouldAutoPauseOpenAIAccountByQuota(context.Background(), account)
		require.False(t, paused)
	})

	t.Run("达到用卡阈值后即使有卡也退出调度", func(t *testing.T) {
		extra := cloneOpenAIAutoResetExtra(baseExtra)
		extra["codex_5h_used_percent"] = 100.0
		extra[OpenAIAutoResetCreditStateExtraKey] = OpenAIAutoResetCreditState{
			Status: OpenAIAutoResetStatusAvailable, AvailableCount: 1, CheckedAt: now.Format(time.RFC3339),
		}
		account := &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
		paused, decision := shouldAutoPauseOpenAIAccountByQuota(context.Background(), account)
		require.True(t, paused)
		require.Equal(t, "quota_auto_reset_pending_5h", decision.reason)
	})

	t.Run("自然窗口重置后清除动态阻塞", func(t *testing.T) {
		extra := cloneOpenAIAutoResetExtra(baseExtra)
		extra["codex_5h_used_percent"] = 100.0
		extra["codex_5h_reset_at"] = now.Add(-time.Second).Format(time.RFC3339)
		extra[OpenAIAutoResetCreditStateExtraKey] = OpenAIAutoResetCreditState{
			Status: OpenAIAutoResetStatusFailed, TriggerWindow: "5h", ErrorCode: "RESET_FAILED",
		}
		account := &Account{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: extra}
		paused, _ := shouldAutoPauseOpenAIAccountByQuota(context.Background(), account)
		require.False(t, paused)
	})
}

func TestSelectOpenAIAutoResetCandidate_FailsClosed(t *testing.T) {
	candidates := []openAIAutoResetCreditCandidate{
		{ID: "later", ExpiresAt: "2026-09-02T00:00:00Z"},
		{ID: "earlier", ExpiresAt: "2026-09-01T00:00:00Z"},
	}
	selected, err := selectOpenAIAutoResetCandidate(candidates, 2, nil, "cycle-a")
	require.NoError(t, err)
	require.Equal(t, "earlier", selected.ID)

	_, err = selectOpenAIAutoResetCandidate([]openAIAutoResetCreditCandidate{
		{ExpiresAt: "2026-09-01T00:00:00Z"},
	}, 1, nil, "cycle-a")
	require.Error(t, err)

	_, err = selectOpenAIAutoResetCandidate(candidates, 2, &OpenAIAutoResetCreditState{
		AttemptCycleHash: "cycle-a", AttemptCreditHash: shortOpenAIAutoResetHash("missing"),
	}, "cycle-a")
	require.Error(t, err, "模糊结果后原卡消失时不得切换下一张卡")
}

func TestOpenAIAutoResetConsumeResultCanonicalNoCreditSurvivesStoredResponseRedaction(t *testing.T) {
	coordinator := NewIdempotencyCoordinator(newInMemoryIdempotencyRepo(), DefaultIdempotencyConfig())
	body, err := coordinator.marshalStoredResponse(openAIAutoResetConsumeResult{
		ResultCode:   "no_credit",
		WindowsReset: 0,
	})
	require.NoError(t, err)
	require.Contains(t, body, `"result_code":"no_credit"`)
	require.NotContains(t, body, `"code":`)
	require.NotContains(t, body, `***`)

	stored, err := coordinator.decodeStoredResponse(&body)
	require.NoError(t, err)
	decoded, err := decodeOpenAIAutoResetConsumeResult(stored)
	require.NoError(t, err)
	require.Equal(t, "no_credit", decoded.ResultCode)
	require.Zero(t, decoded.WindowsReset)
}

func TestOpenAIAutoResetConsumeResultDeferredRecoverySurvivesStoredResponseRoundTrip(t *testing.T) {
	coordinator := NewIdempotencyCoordinator(newInMemoryIdempotencyRepo(), DefaultIdempotencyConfig())
	body, err := coordinator.marshalStoredResponse(openAIAutoResetConsumeResult{
		ResultCode:            "success",
		WindowsReset:          3,
		RecoveryPending:       true,
		RecoveryDeferred:      true,
		PostProcessRecorded:   false,
		AccountStateRecovered: false,
	})
	require.NoError(t, err)
	require.NotContains(t, body, `***`)

	stored, err := coordinator.decodeStoredResponse(&body)
	require.NoError(t, err)
	decoded, err := decodeOpenAIAutoResetConsumeResult(stored)
	require.NoError(t, err)
	require.Equal(t, "success", decoded.ResultCode)
	require.Equal(t, 3, decoded.WindowsReset)
	require.True(t, decoded.RecoveryPending)
	require.True(t, decoded.RecoveryDeferred)
	require.False(t, decoded.PostProcessRecorded)
	require.False(t, decoded.AccountStateRecovered)
}

func TestParseOpenAIAutoResetCanonicalResponseAcceptsMigrationRecoveryStates(t *testing.T) {
	bodies := []string{
		`{"result_code":"no_credit","windows_reset":0}`,
		`{"account_state_recovered":false,"recovery_pending":true,"result_code":"success","windows_reset":2}`,
		`{"account_state_recovered":false,"recovery_deferred":true,"recovery_pending":true,"result_code":"success","windows_reset":3}`,
		`{"account_state_recovered":false,"recovery_pending":true,"result_code":"success","warning_code":"OPENAI_AUTO_RESET_RECONCILED_RECOVERY_FAILED","windows_reset":4}`,
	}
	for _, body := range bodies {
		_, err := ParseOpenAIAutoResetCanonicalResponse(body)
		require.NoError(t, err, body)
	}
}

func TestNormalizeOpenAIAutoResetUpstreamResultCodeAllowlist(t *testing.T) {
	allowed := []struct {
		code string
		want string
	}{
		{code: "ok", want: "success"},
		{code: "success", want: "success"},
		{code: "no_credit", want: "no_credit"},
	}
	for _, test := range allowed {
		t.Run(test.code, func(t *testing.T) {
			result, err := normalizeOpenAIAutoResetUpstreamResultCode(test.code)
			require.NoError(t, err)
			require.Equal(t, test.want, result)
		})
	}

	for _, code := range []string{"", "pending", "reconciled_success", "***"} {
		t.Run("reject_"+code, func(t *testing.T) {
			_, err := normalizeOpenAIAutoResetUpstreamResultCode(code)
			require.Error(t, err)
		})
	}
}

func TestDecodeOpenAIAutoResetConsumeResultLegacyCompatibilityAndStrictValidation(t *testing.T) {
	t.Run("legacy success", func(t *testing.T) {
		decoded, err := decodeOpenAIAutoResetConsumeResult(map[string]any{
			"code":          "success",
			"windows_reset": 2,
		})
		require.NoError(t, err)
		require.Equal(t, "success", decoded.ResultCode)
		require.Equal(t, 2, decoded.WindowsReset)
	})

	t.Run("legacy no credit", func(t *testing.T) {
		decoded, err := decodeOpenAIAutoResetConsumeResult(map[string]any{
			"code":          "no_credit",
			"windows_reset": 0,
		})
		require.NoError(t, err)
		require.Equal(t, "no_credit", decoded.ResultCode)
		require.Zero(t, decoded.WindowsReset)
	})

	invalid := []struct {
		name  string
		value any
	}{
		{
			name:  "redacted legacy result",
			value: map[string]any{"code": "***", "windows_reset": 0},
		},
		{
			name:  "empty legacy result",
			value: map[string]any{"code": "", "windows_reset": 0},
		},
		{
			name:  "unknown legacy result",
			value: map[string]any{"code": "pending", "windows_reset": 0},
		},
		{
			name:  "missing canonical result",
			value: map[string]any{"windows_reset": 0},
		},
		{
			name:  "unknown canonical result",
			value: map[string]any{"result_code": "pending", "windows_reset": 0},
		},
		{
			name:  "no credit cannot reset windows",
			value: map[string]any{"result_code": "no_credit", "windows_reset": 1},
		},
		{
			name: "unknown field",
			value: map[string]any{
				"result_code":   "success",
				"windows_reset": 1,
				"access_token":  "must-not-be-accepted",
			},
		},
		{
			name: "wrong field type",
			value: map[string]any{
				"result_code":   "success",
				"windows_reset": "1",
			},
		},
		{
			name: "canonical and legacy conflict",
			value: map[string]any{
				"result_code":   "success",
				"code":          "no_credit",
				"windows_reset": 1,
			},
		},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeOpenAIAutoResetConsumeResult(test.value)
			require.Error(t, err)
		})
	}
}

func TestOpenAIQuotaAutoResetService_AssessesIndependentWindows(t *testing.T) {
	service := &OpenAIQuotaAutoResetService{}
	account := &Account{Extra: map[string]any{
		"auto_pause_5h_disabled": true,
		"auto_pause_7d_disabled": true,
	}}
	config := OpenAIAutoResetCreditConfig{Enabled: true, Threshold5h: 0.8, Threshold7d: 0.9}
	tests := []struct {
		name       string
		fiveHour   float64
		sevenDay   float64
		wantWindow string
	}{
		{name: "5h", fiveHour: 0.8, sevenDay: 0.2, wantWindow: "5h"},
		{name: "7d", fiveHour: 0.2, sevenDay: 0.9, wantWindow: "7d"},
		{name: "同时触发", fiveHour: 0.95, sevenDay: 0.95, wantWindow: "5h+7d"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assessment := service.buildAssessment(context.Background(), account, config, test.fiveHour, test.sevenDay)
			require.True(t, assessment.resetReached)
			require.Equal(t, test.wantWindow, assessment.triggerWindow)
		})
	}
}

type autoResetTestAccountRepo struct {
	AccountRepository
	mu      sync.Mutex
	account *Account
}

func (r *autoResetTestAccountRepo) GetByID(_ context.Context, id int64) (*Account, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *r.account
	copy.Extra = cloneOpenAIAutoResetExtra(r.account.Extra)
	return &copy, nil
}

func (r *autoResetTestAccountRepo) UpdateExtra(_ context.Context, id int64, updates map[string]any) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account.Extra == nil {
		r.account.Extra = make(map[string]any)
	}
	for key, value := range updates {
		r.account.Extra[key] = value
	}
	return nil
}

type autoResetTestQuota struct {
	usage        *OpenAIQuotaUsage
	resetResult  *OpenAIQuotaResetResult
	resetCalls   atomic.Int32
	resetEntered chan struct{}
	releaseReset chan struct{}
	enterOnce    sync.Once
	mu           sync.Mutex
	resetArgs    [][2]string
	failFirst    bool
}

func (q *autoResetTestQuota) QueryUsage(context.Context, int64) (*OpenAIQuotaUsage, error) {
	copy := *q.usage
	return &copy, nil
}

func (q *autoResetTestQuota) CacheResetCreditsSnapshot(context.Context, int64, *OpenAIRateLimitResetCredits) error {
	return nil
}

func (q *autoResetTestQuota) ResetCreditTargeted(_ context.Context, _ int64, creditID, redeemRequestID string) (*OpenAIQuotaResetResult, error) {
	if creditID == "" || redeemRequestID == "" {
		panic("targeted reset identifiers must be present")
	}
	call := q.resetCalls.Add(1)
	q.mu.Lock()
	q.resetArgs = append(q.resetArgs, [2]string{creditID, redeemRequestID})
	q.mu.Unlock()
	if q.failFirst && call == 1 {
		return nil, context.DeadlineExceeded
	}
	if q.resetEntered != nil {
		q.enterOnce.Do(func() { close(q.resetEntered) })
	}
	if q.releaseReset != nil {
		<-q.releaseReset
	}
	if q.resetResult != nil {
		result := *q.resetResult
		return &result, nil
	}
	return &OpenAIQuotaResetResult{Code: "ok", WindowsReset: 2}, nil
}

type autoResetTestRecoverer struct{}

func (autoResetTestRecoverer) RecoverAccountState(context.Context, int64, AccountRecoveryOptions) (*SuccessfulTestRecoveryResult, error) {
	return &SuccessfulTestRecoveryResult{ClearedRateLimit: true}, nil
}

type autoResetNoCreditQuota struct {
	usage      *OpenAIQuotaUsage
	queryCalls atomic.Int32
	resetCalls atomic.Int32
}

func (q *autoResetNoCreditQuota) QueryUsage(context.Context, int64) (*OpenAIQuotaUsage, error) {
	q.queryCalls.Add(1)
	copy := *q.usage
	return &copy, nil
}

func (*autoResetNoCreditQuota) CacheResetCreditsSnapshot(context.Context, int64, *OpenAIRateLimitResetCredits) error {
	return nil
}

func (q *autoResetNoCreditQuota) ResetCreditTargeted(context.Context, int64, string, string) (*OpenAIQuotaResetResult, error) {
	q.resetCalls.Add(1)
	return &OpenAIQuotaResetResult{Code: "no_credit"}, nil
}

type autoResetCountingRecoverer struct {
	calls atomic.Int32
}

func (r *autoResetCountingRecoverer) RecoverAccountState(context.Context, int64, AccountRecoveryOptions) (*SuccessfulTestRecoveryResult, error) {
	r.calls.Add(1)
	return &SuccessfulTestRecoveryResult{ClearedRateLimit: true}, nil
}

type autoResetTestAccountLocker struct {
	mu                  sync.Mutex
	held                map[int64]struct{}
	contentions         int
	acquireCalls        int
	releaseCalls        int
	acquireErr          error
	returnNilLease      bool
	returnTypedNilLease bool
	acquireHook         func(context.Context, int64)
	rowLockCalls        int
	rowLockErr          error
	rowMissing          bool
}

func newAutoResetTestAccountLocker() *autoResetTestAccountLocker {
	return &autoResetTestAccountLocker{held: make(map[int64]struct{})}
}

func (l *autoResetTestAccountLocker) TryAcquire(ctx context.Context, accountID int64) (OpenAIQuotaAutoResetAccountLease, bool, error) {
	l.mu.Lock()
	l.acquireCalls++
	acquireErr := l.acquireErr
	returnNilLease := l.returnNilLease
	returnTypedNilLease := l.returnTypedNilLease
	acquireHook := l.acquireHook
	l.mu.Unlock()
	if acquireHook != nil {
		acquireHook(ctx, accountID)
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if acquireErr != nil {
		return nil, false, acquireErr
	}
	if returnNilLease {
		return nil, true, nil
	}
	if returnTypedNilLease {
		var lease *autoResetTestAccountLease
		return lease, true, nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, exists := l.held[accountID]; exists {
		l.contentions++
		return nil, false, nil
	}
	l.held[accountID] = struct{}{}
	return &autoResetTestAccountLease{
		protect: func(ctx context.Context) (bool, error) {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			l.mu.Lock()
			defer l.mu.Unlock()
			l.rowLockCalls++
			if l.rowLockErr != nil {
				return false, l.rowLockErr
			}
			return !l.rowMissing, nil
		},
		release: func() {
			l.mu.Lock()
			delete(l.held, accountID)
			l.releaseCalls++
			l.mu.Unlock()
		},
	}, true, nil
}

func (l *autoResetTestAccountLocker) contentionCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.contentions
}

func (l *autoResetTestAccountLocker) setAcquireError(err error) {
	l.mu.Lock()
	l.acquireErr = err
	l.mu.Unlock()
}

func (l *autoResetTestAccountLocker) setNilLeaseMode(untyped, typed bool) {
	l.mu.Lock()
	l.returnNilLease = untyped
	l.returnTypedNilLease = typed
	l.mu.Unlock()
}

func (l *autoResetTestAccountLocker) setAcquireHook(hook func(context.Context, int64)) {
	l.mu.Lock()
	l.acquireHook = hook
	l.mu.Unlock()
}

func (l *autoResetTestAccountLocker) setRowProtection(err error, missing bool) {
	l.mu.Lock()
	l.rowLockErr = err
	l.rowMissing = missing
	l.mu.Unlock()
}

func (l *autoResetTestAccountLocker) counts() (acquire, release, contentions int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.acquireCalls, l.releaseCalls, l.contentions
}

func (l *autoResetTestAccountLocker) rowLockCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rowLockCalls
}

type autoResetTestAccountLease struct {
	protectOnce sync.Once
	releaseOnce sync.Once
	protect     func(context.Context) (bool, error)
	release     func()
	exists      bool
	err         error
}

func (l *autoResetTestAccountLease) LockAccountRow(ctx context.Context) (bool, error) {
	if l == nil {
		return false, fmt.Errorf("lock account row: nil lease")
	}
	l.protectOnce.Do(func() {
		if l.protect == nil {
			l.err = fmt.Errorf("lock account row: missing test protector")
			return
		}
		l.exists, l.err = l.protect(ctx)
	})
	return l.exists, l.err
}

func (l *autoResetTestAccountLease) Release() error {
	if l == nil {
		return nil
	}
	l.releaseOnce.Do(l.release)
	return nil
}

func TestOpenAIQuotaAutoResetService_ConcurrentInstancesConsumeOnce(t *testing.T) {
	now := time.Now().UTC()
	account := &Account{
		ID: 99, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
		Extra: map[string]any{
			OpenAIAutoResetCreditEnabledExtraKey:     true,
			OpenAIAutoResetCredit5hThresholdExtraKey: 1.0,
			OpenAIAutoResetCredit7dThresholdExtraKey: 1.0,
			"codex_5h_used_percent":                  100.0,
			"codex_7d_used_percent":                  10.0,
			"codex_usage_updated_at":                 now.Format(time.RFC3339),
			"codex_5h_reset_at":                      now.Add(time.Hour).Format(time.RFC3339),
			"codex_7d_reset_at":                      now.Add(24 * time.Hour).Format(time.RFC3339),
		},
	}
	repo := &autoResetTestAccountRepo{account: account}
	usage := &OpenAIQuotaUsage{
		FetchedAt: now.Unix(),
		RateLimit: &OpenAIRateLimit{
			PrimaryWindow:   &OpenAIRateLimitWindow{UsedPercent: 100, LimitWindowSeconds: 5 * 60 * 60, ResetAfterSeconds: 3600, ResetAt: now.Add(time.Hour).Unix()},
			SecondaryWindow: &OpenAIRateLimitWindow{UsedPercent: 10, LimitWindowSeconds: 7 * 24 * 60 * 60, ResetAfterSeconds: 86400, ResetAt: now.Add(24 * time.Hour).Unix()},
		},
		RateLimitResetCredits: &OpenAIRateLimitResetCredits{
			AvailableCount: 1,
			Credits:        []OpenAIRateLimitResetCreditDetail{{ExpiresAt: now.Add(48 * time.Hour).Format(time.RFC3339)}},
		},
		autoResetCandidates: []openAIAutoResetCreditCandidate{{ID: "credit-sensitive-id", ExpiresAt: now.Add(48 * time.Hour).Format(time.RFC3339)}},
	}
	quota := &autoResetTestQuota{usage: usage, resetEntered: make(chan struct{}), releaseReset: make(chan struct{})}
	idempotencyRepo := newInMemoryIdempotencyRepo()
	config := DefaultIdempotencyConfig()
	config.ObserveOnly = false
	config.ProcessingTimeout = time.Second
	resolver, policy, audit, finalizer := newOpenAIAutoResetTestSecurity(t, idempotencyRepo)
	accountLock := newAutoResetTestAccountLocker()
	serviceA := NewOpenAIQuotaAutoResetService(repo, quota, autoResetTestRecoverer{}, NewIdempotencyCoordinator(idempotencyRepo, config), finalizer, accountLock, audit, nil, nil, resolver, policy)
	serviceB := NewOpenAIQuotaAutoResetService(repo, quota, autoResetTestRecoverer{}, NewIdempotencyCoordinator(idempotencyRepo, config), finalizer, accountLock, audit, nil, nil, resolver, policy)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = serviceA.evaluateAccount(context.Background(), account.ID)
	}()
	<-quota.resetEntered
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- serviceB.evaluateAccount(context.Background(), account.ID)
	}()
	select {
	case err := <-secondDone:
		require.NoError(t, err)
	case <-time.After(2 * time.Second):
		close(quota.releaseReset)
		wg.Wait()
		t.Fatal("second service did not observe the in-progress idempotency record")
	}
	close(quota.releaseReset)
	wg.Wait()

	require.Equal(t, int32(1), quota.resetCalls.Load())
	acquireCalls, releaseCalls, contentions := accountLock.counts()
	require.Equal(t, 2, acquireCalls)
	require.Equal(t, 1, releaseCalls)
	require.Equal(t, 1, contentions, "the advisory lease must stop the duplicate before account reads and claim creation")
	require.Equal(t, 1, accountLock.rowLockCount())
	repo.mu.Lock()
	state := openAIAutoResetStateFromExtra(repo.account.Extra)
	repo.mu.Unlock()
	require.NotNil(t, state)
	require.Equal(t, OpenAIAutoResetStatusSuccess, state.Status)
	encodedState, err := json.Marshal(state)
	require.NoError(t, err)
	require.NotContains(t, string(encodedState), "credit-sensitive-id")
}

func TestOpenAIQuotaAutoResetService_NoCreditReplayDoesNotResetOrRecoverAgain(t *testing.T) {
	now := time.Now().UTC()
	account := &Account{
		ID: 102, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
		Extra: map[string]any{
			OpenAIAutoResetCreditEnabledExtraKey:     true,
			OpenAIAutoResetCredit5hThresholdExtraKey: 1.0,
			OpenAIAutoResetCredit7dThresholdExtraKey: 1.0,
			"codex_5h_used_percent":                  100.0,
			"codex_usage_updated_at":                 now.Format(time.RFC3339),
			"codex_5h_reset_at":                      now.Add(time.Hour).Format(time.RFC3339),
		},
	}
	expiresAt := now.Add(48 * time.Hour).Format(time.RFC3339)
	quota := &autoResetNoCreditQuota{usage: &OpenAIQuotaUsage{
		FetchedAt: now.Unix(),
		RateLimit: &OpenAIRateLimit{
			PrimaryWindow: &OpenAIRateLimitWindow{
				UsedPercent:        100,
				LimitWindowSeconds: 5 * 60 * 60,
				ResetAfterSeconds:  3600,
				ResetAt:            now.Add(time.Hour).Unix(),
			},
		},
		RateLimitResetCredits: &OpenAIRateLimitResetCredits{
			AvailableCount: 1,
			Credits:        []OpenAIRateLimitResetCreditDetail{{ExpiresAt: expiresAt}},
		},
		autoResetCandidates: []openAIAutoResetCreditCandidate{{
			ID:        "no-credit-candidate",
			ExpiresAt: expiresAt,
		}},
	}}
	repo := &autoResetTestAccountRepo{account: account}
	idempotencyRepo := newInMemoryIdempotencyRepo()
	config := DefaultIdempotencyConfig()
	config.ObserveOnly = false
	resolver, policy, audit, finalizer := newOpenAIAutoResetTestSecurity(t, idempotencyRepo)
	recoverer := &autoResetCountingRecoverer{}
	service := NewOpenAIQuotaAutoResetService(
		repo,
		quota,
		recoverer,
		NewIdempotencyCoordinator(idempotencyRepo, config),
		finalizer,
		newAutoResetTestAccountLocker(),
		audit,
		nil,
		nil,
		resolver,
		policy,
	)

	require.NoError(t, service.evaluateAccount(context.Background(), account.ID))
	require.Equal(t, int32(1), quota.queryCalls.Load())
	require.Equal(t, int32(1), quota.resetCalls.Load())
	require.Zero(t, recoverer.calls.Load())
	repo.mu.Lock()
	firstState := openAIAutoResetStateFromExtra(repo.account.Extra)
	repo.mu.Unlock()
	require.NotNil(t, firstState)
	require.Equal(t, OpenAIAutoResetStatusNoCredit, firstState.Status)
	require.Equal(t, "NO_RESET_CREDIT", firstState.ErrorCode)

	record := findAutoResetExecutionRecord(t, idempotencyRepo)
	require.NotNil(t, record.ResponseBody)
	require.Contains(t, *record.ResponseBody, `"result_code":"no_credit"`)
	require.NotContains(t, *record.ResponseBody, `"code":`)
	require.NotContains(t, *record.ResponseBody, `***`)

	require.NoError(t, service.evaluateAccount(context.Background(), account.ID))
	require.Equal(t, int32(2), quota.queryCalls.Load(), "second evaluation must reach the idempotent consume boundary")
	require.Equal(t, int32(1), quota.resetCalls.Load(), "replay must not issue a second upstream reset")
	require.Zero(t, recoverer.calls.Load(), "no-credit owner result and replay must both skip recovery")
	repo.mu.Lock()
	replayedState := openAIAutoResetStateFromExtra(repo.account.Extra)
	repo.mu.Unlock()
	require.NotNil(t, replayedState)
	require.Equal(t, OpenAIAutoResetStatusNoCredit, replayedState.Status)
	require.Equal(t, "NO_RESET_CREDIT", replayedState.ErrorCode)
}

func TestOpenAIQuotaAutoResetService_UnknownUpstreamResultRemainsProcessing(t *testing.T) {
	for index, code := range []string{"", "pending"} {
		t.Run(fmt.Sprintf("code_%d", index), func(t *testing.T) {
			now := time.Now().UTC()
			account := &Account{
				ID: int64(120 + index), Platform: PlatformOpenAI, Type: AccountTypeOAuth,
				Status: StatusActive, Schedulable: true,
				Extra: map[string]any{
					OpenAIAutoResetCreditEnabledExtraKey:     true,
					OpenAIAutoResetCredit5hThresholdExtraKey: 1.0,
					OpenAIAutoResetCredit7dThresholdExtraKey: 1.0,
					"codex_5h_used_percent":                  100.0,
					"codex_usage_updated_at":                 now.Format(time.RFC3339),
					"codex_5h_reset_at":                      now.Add(time.Hour).Format(time.RFC3339),
				},
			}
			expiresAt := now.Add(48 * time.Hour).Format(time.RFC3339)
			quota := &autoResetTestQuota{
				resetResult: &OpenAIQuotaResetResult{Code: code},
				usage: &OpenAIQuotaUsage{
					FetchedAt: now.Unix(),
					RateLimit: &OpenAIRateLimit{PrimaryWindow: &OpenAIRateLimitWindow{
						UsedPercent:        100,
						LimitWindowSeconds: 5 * 60 * 60,
						ResetAfterSeconds:  3600,
						ResetAt:            now.Add(time.Hour).Unix(),
					}},
					RateLimitResetCredits: &OpenAIRateLimitResetCredits{
						AvailableCount: 1,
						Credits:        []OpenAIRateLimitResetCreditDetail{{ExpiresAt: expiresAt}},
					},
					autoResetCandidates: []openAIAutoResetCreditCandidate{{
						ID: "unknown-result-candidate", ExpiresAt: expiresAt,
					}},
				},
			}
			accountRepo := &autoResetTestAccountRepo{account: account}
			idempotencyRepo := newInMemoryIdempotencyRepo()
			config := DefaultIdempotencyConfig()
			config.ObserveOnly = false
			resolver, policy, audit, finalizer := newOpenAIAutoResetTestSecurity(t, idempotencyRepo)
			recoverer := &autoResetCountingRecoverer{}
			service := NewOpenAIQuotaAutoResetService(
				accountRepo,
				quota,
				recoverer,
				NewIdempotencyCoordinator(idempotencyRepo, config),
				finalizer,
				newAutoResetTestAccountLocker(),
				audit,
				nil,
				nil,
				resolver,
				policy,
			)

			err := service.evaluateAccount(context.Background(), account.ID)
			require.ErrorIs(t, err, ErrOpenAIAutoResetReconciliationRequired)
			require.Equal(t, int32(1), quota.resetCalls.Load())
			require.Zero(t, recoverer.calls.Load())
			record := findAutoResetExecutionRecord(t, idempotencyRepo)
			require.Equal(t, IdempotencyStatusProcessing, record.Status)
			require.Nil(t, record.ResponseBody)
			auditRepo, ok := audit.repo.(*autoResetDurableAuditRepo)
			require.True(t, ok)
			require.Empty(t, auditRepo.snapshot())

			idempotencyRepo.mu.Lock()
			for _, stored := range idempotencyRepo.data {
				if stored.ID == record.ID {
					past := time.Now().Add(-time.Minute)
					stored.LockedUntil = &past
				}
			}
			idempotencyRepo.mu.Unlock()

			err = service.evaluateAccount(context.Background(), account.ID)
			require.ErrorIs(t, err, ErrOpenAIAutoResetReconciliationRequired)
			require.Equal(t, int32(1), quota.resetCalls.Load(), "unknown terminal semantics must never be consumed twice")
			require.Zero(t, recoverer.calls.Load())
		})
	}
}

func TestOpenAIQuotaAutoResetService_AccountLockSerializesDifferentAttempts(t *testing.T) {
	now := time.Now().UTC()
	account := &Account{
		ID: 101, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
		Extra: map[string]any{
			OpenAIAutoResetCreditEnabledExtraKey:     true,
			OpenAIAutoResetCredit5hThresholdExtraKey: 1.0,
			OpenAIAutoResetCredit7dThresholdExtraKey: 1.0,
			"codex_5h_used_percent":                  100.0,
			"codex_usage_updated_at":                 now.Format(time.RFC3339),
			"codex_5h_reset_at":                      now.Add(time.Hour).Format(time.RFC3339),
		},
	}
	newUsage := func(resetAt time.Time, candidateID string) *OpenAIQuotaUsage {
		return &OpenAIQuotaUsage{
			FetchedAt: now.Unix(),
			RateLimit: &OpenAIRateLimit{
				PrimaryWindow: &OpenAIRateLimitWindow{
					UsedPercent:        100,
					LimitWindowSeconds: 5 * 60 * 60,
					ResetAfterSeconds:  3600,
					ResetAt:            resetAt.Unix(),
				},
			},
			RateLimitResetCredits: &OpenAIRateLimitResetCredits{
				AvailableCount: 1,
				Credits:        []OpenAIRateLimitResetCreditDetail{{ExpiresAt: now.Add(48 * time.Hour).Format(time.RFC3339)}},
			},
			autoResetCandidates: []openAIAutoResetCreditCandidate{{
				ID:        candidateID,
				ExpiresAt: now.Add(48 * time.Hour).Format(time.RFC3339),
			}},
		}
	}
	usageA := newUsage(now.Add(time.Hour), "candidate-a")
	usageB := newUsage(now.Add(2*time.Hour), "candidate-b")
	require.NotEqual(t, openAIAutoResetCycleSeed(usageA), openAIAutoResetCycleSeed(usageB))
	require.NotEqual(t, shortOpenAIAutoResetHash("candidate-a"), shortOpenAIAutoResetHash("candidate-b"))

	repo := &autoResetTestAccountRepo{account: account}
	quotaA := &autoResetTestQuota{usage: usageA, resetEntered: make(chan struct{}), releaseReset: make(chan struct{})}
	quotaB := &autoResetTestQuota{usage: usageB}
	idempotencyRepo := newInMemoryIdempotencyRepo()
	idempotencyConfig := DefaultIdempotencyConfig()
	idempotencyConfig.ObserveOnly = false
	resolver, policy, audit, finalizer := newOpenAIAutoResetTestSecurity(t, idempotencyRepo)
	accountLock := newAutoResetTestAccountLocker()
	serviceA := NewOpenAIQuotaAutoResetService(repo, quotaA, autoResetTestRecoverer{}, NewIdempotencyCoordinator(idempotencyRepo, idempotencyConfig), finalizer, accountLock, audit, nil, nil, resolver, policy)
	serviceB := NewOpenAIQuotaAutoResetService(repo, quotaB, autoResetTestRecoverer{}, NewIdempotencyCoordinator(idempotencyRepo, idempotencyConfig), finalizer, accountLock, audit, nil, nil, resolver, policy)

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- serviceA.evaluateAccount(context.Background(), account.ID)
	}()
	<-quotaA.resetEntered
	require.NoError(t, serviceB.evaluateAccount(context.Background(), account.ID))
	require.Equal(t, int32(0), quotaB.resetCalls.Load(), "the advisory lease must prevent a different attempt from reaching the POST guard")
	require.Equal(t, 1, accountLock.contentionCount())

	close(quotaA.releaseReset)
	require.NoError(t, <-firstDone)
	require.Equal(t, int32(1), quotaA.resetCalls.Load())
	require.Equal(t, 1, accountLock.rowLockCount())
	quotaA.mu.Lock()
	resetArgs := append([][2]string(nil), quotaA.resetArgs...)
	quotaA.mu.Unlock()
	require.Len(t, resetArgs, 1)
	require.Equal(t, "candidate-a", resetArgs[0][0])
}

func TestOpenAIQuotaAutoResetService_TimeoutRemainsProcessingWithoutRetry(t *testing.T) {
	now := time.Now().UTC()
	account := &Account{
		ID: 100, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
		Status: StatusActive, Schedulable: true,
		Extra: map[string]any{
			OpenAIAutoResetCreditEnabledExtraKey:     true,
			OpenAIAutoResetCredit5hThresholdExtraKey: 1.0,
			OpenAIAutoResetCredit7dThresholdExtraKey: 1.0,
			"codex_5h_used_percent":                  100.0,
			"codex_usage_updated_at":                 now.Format(time.RFC3339),
			"codex_5h_reset_at":                      now.Add(time.Hour).Format(time.RFC3339),
		},
	}
	repo := &autoResetTestAccountRepo{account: account}
	expiresAt := now.Add(48 * time.Hour).Format(time.RFC3339)
	quota := &autoResetTestQuota{
		failFirst: true,
		usage: &OpenAIQuotaUsage{
			FetchedAt: now.Unix(),
			RateLimit: &OpenAIRateLimit{
				PrimaryWindow: &OpenAIRateLimitWindow{UsedPercent: 100, LimitWindowSeconds: 5 * 60 * 60, ResetAfterSeconds: 3600, ResetAt: now.Add(time.Hour).Unix()},
			},
			RateLimitResetCredits: &OpenAIRateLimitResetCredits{
				AvailableCount: 1,
				Credits:        []OpenAIRateLimitResetCreditDetail{{ExpiresAt: expiresAt}},
			},
			autoResetCandidates: []openAIAutoResetCreditCandidate{{ID: "retry-credit", ExpiresAt: expiresAt}},
		},
	}
	idempotencyConfig := DefaultIdempotencyConfig()
	idempotencyConfig.ObserveOnly = false
	idempotencyConfig.FailedRetryBackoff = 0
	idempotencyRepo := newInMemoryIdempotencyRepo()
	resolver, policy, audit, finalizer := newOpenAIAutoResetTestSecurity(t, idempotencyRepo)
	service := NewOpenAIQuotaAutoResetService(
		repo,
		quota,
		autoResetTestRecoverer{},
		NewIdempotencyCoordinator(idempotencyRepo, idempotencyConfig),
		finalizer, newAutoResetTestAccountLocker(), audit, nil, nil,
		resolver, policy,
	)

	require.Error(t, service.evaluateAccount(context.Background(), account.ID))
	require.NoError(t, service.evaluateAccount(context.Background(), account.ID))
	quota.mu.Lock()
	args := append([][2]string(nil), quota.resetArgs...)
	quota.mu.Unlock()
	require.Len(t, args, 1, "an ambiguous timeout must never issue a second reset request")
	record := findAutoResetExecutionRecord(t, idempotencyRepo)
	require.Equal(t, IdempotencyStatusProcessing, record.Status)
	idempotencyRepo.mu.Lock()
	for _, stored := range idempotencyRepo.data {
		if stored.ID == record.ID {
			past := time.Now().Add(-time.Minute)
			stored.LockedUntil = &past
			stored.ExpiresAt = past
		}
	}
	idempotencyRepo.mu.Unlock()

	err := service.evaluateAccount(context.Background(), account.ID)
	require.ErrorIs(t, err, ErrOpenAIAutoResetReconciliationRequired)
	quota.mu.Lock()
	args = append([][2]string(nil), quota.resetArgs...)
	quota.mu.Unlock()
	require.Len(t, args, 1, "expired ambiguous processing records remain fail closed")
}

func findAutoResetExecutionRecord(t testing.TB, repo *inMemoryIdempotencyRepo) *IdempotencyRecord {
	t.Helper()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	for _, record := range repo.data {
		if strings.HasPrefix(record.Scope, openAIAutoResetIdempotencyScope+"|") {
			return cloneRecord(record)
		}
	}
	t.Fatal("auto-reset execution record was not found")
	return nil
}
