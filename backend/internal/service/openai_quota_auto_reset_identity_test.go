package service

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type autoResetIdentityMutationLocker struct {
	delegate OpenAIQuotaAutoResetAccountLocker
	mutate   func()
	once     sync.Once
}

func (l *autoResetIdentityMutationLocker) TryAcquire(
	ctx context.Context,
	accountID int64,
) (OpenAIQuotaAutoResetAccountLease, bool, error) {
	lease, acquired, err := l.delegate.TryAcquire(ctx, accountID)
	if err != nil || !acquired || openAIAutoResetLeaseIsNil(lease) {
		return lease, acquired, err
	}
	return &autoResetIdentityMutationLease{
		delegate: lease,
		mutate: func() {
			l.once.Do(l.mutate)
		},
	}, true, nil
}

type autoResetIdentityMutationLease struct {
	delegate OpenAIQuotaAutoResetAccountLease
	mutate   func()
}

func (l *autoResetIdentityMutationLease) LockAccountRow(ctx context.Context) (bool, error) {
	if l.mutate != nil {
		l.mutate()
	}
	return l.delegate.LockAccountRow(ctx)
}

func (l *autoResetIdentityMutationLease) Release() error {
	return l.delegate.Release()
}

type autoResetIdentityCapturingQuota struct {
	delegate *OpenAIQuotaService

	mu       sync.Mutex
	identity *openAIQuotaAutoResetUpstreamIdentity
}

func (q *autoResetIdentityCapturingQuota) QueryUsage(ctx context.Context, accountID int64) (*OpenAIQuotaUsage, error) {
	usage, err := q.delegate.QueryUsage(ctx, accountID)
	if usage != nil && usage.autoResetIdentity != nil {
		q.mu.Lock()
		if q.identity == nil {
			identity := *usage.autoResetIdentity
			q.identity = &identity
		}
		q.mu.Unlock()
	}
	return usage, err
}

func (q *autoResetIdentityCapturingQuota) CacheResetCreditsSnapshot(
	ctx context.Context,
	accountID int64,
	credits *OpenAIRateLimitResetCredits,
) error {
	return q.delegate.CacheResetCreditsSnapshot(ctx, accountID, credits)
}

func (q *autoResetIdentityCapturingQuota) resetCreditTargetedGuarded(
	ctx context.Context,
	accountID int64,
	creditID string,
	redeemRequestID string,
	guard openAIAutoResetExternalEffectGuard,
) (*OpenAIQuotaResetResult, error) {
	return q.delegate.resetCreditTargetedGuarded(ctx, accountID, creditID, redeemRequestID, guard)
}

func (q *autoResetIdentityCapturingQuota) firstIdentity() (openAIQuotaAutoResetUpstreamIdentity, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.identity == nil {
		return openAIQuotaAutoResetUpstreamIdentity{}, false
	}
	return *q.identity, true
}

// Agent task recovery persists credentials through this repository path. Keep
// the test repository's copy isolated from the caller just like a DB reload.
func (r *autoResetAuthzAccountRepo) UpdateCredentials(ctx context.Context, _ int64, credentials map[string]any) error {
	if r.recorder != nil {
		r.recorder.record(ctx, "credentials.update")
	}
	copy := make(map[string]any, len(credentials))
	for key, value := range credentials {
		copy[key] = value
	}
	r.mu.Lock()
	r.account.Credentials = copy
	r.mu.Unlock()
	return nil
}

func TestOpenAIQuotaAutoResetFinalGuardBindsDetailsIdentityToLockedAccount(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		mutate func(*Account)
		assert func(testing.TB, *Account)
	}{
		{
			name: "access token changed",
			mutate: func(account *Account) {
				credentials := cloneAutoResetIdentityCredentials(account.Credentials)
				credentials["access_token"] = "access-token-b"
				account.Credentials = credentials
			},
			assert: func(t testing.TB, account *Account) {
				require.Equal(t, "access-token-b", account.GetOpenAIAccessToken())
			},
		},
		{
			name: "ChatGPT account changed",
			mutate: func(account *Account) {
				credentials := cloneAutoResetIdentityCredentials(account.Credentials)
				credentials["chatgpt_account_id"] = "chatgpt-account-b"
				account.Credentials = credentials
			},
			assert: func(t testing.TB, account *Account) {
				require.Equal(t, "chatgpt-account-b", openAIQuotaChatGPTAccountID(account))
			},
		},
		{
			name: "FedRAMP mode changed",
			mutate: func(account *Account) {
				credentials := cloneAutoResetIdentityCredentials(account.Credentials)
				credentials["chatgpt_account_is_fedramp"] = true
				account.Credentials = credentials
			},
			assert: func(t testing.TB, account *Account) {
				require.True(t, account.IsChatGPTAccountFedRAMP())
			},
		},
		{
			name: "configured proxy content changed",
			mutate: func(account *Account) {
				account.Proxy = &Proxy{
					ID:       *account.ProxyID,
					Protocol: "http",
					Host:     "proxy-a-rotated.internal",
					Port:     8080,
				}
			},
			assert: func(t testing.TB, account *Account) {
				require.Equal(t, "http://proxy-a-rotated.internal:8080", account.Proxy.URL())
			},
		},
		{
			name: "configured proxy changed",
			mutate: func(account *Account) {
				proxyID := int64(902)
				account.ProxyID = &proxyID
				account.Proxy = &Proxy{
					ID:       proxyID,
					Protocol: "http",
					Host:     "proxy-b.internal",
					Port:     8081,
				}
			},
			assert: func(t testing.TB, account *Account) {
				require.NotNil(t, account.ProxyID)
				require.Equal(t, int64(902), *account.ProxyID)
				require.Equal(t, "http://proxy-b.internal:8081", account.Proxy.URL())
			},
		},
	}

	for index, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			account := newAutoResetIdentityAccount(now, int64(801+index))
			fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))

			var usageCalls atomic.Int32
			var detailsCalls atomic.Int32
			var consumeCalls atomic.Int32
			server := newAutoResetIdentityUpstreamServer(
				t,
				now,
				&usageCalls,
				&detailsCalls,
				func(w http.ResponseWriter, _ *http.Request) {
					consumeCalls.Add(1)
					_, _ = w.Write([]byte(`{"code":"ok","windows_reset":2}`))
				},
			)
			defer server.Close()

			tokenCache := &stubQuotaTokenCache{tokens: map[string]string{
				OpenAITokenCacheKey(account): "access-token-a",
			}}
			quota := NewOpenAIQuotaService(
				fixture.accountRepo,
				nil,
				NewOpenAITokenProvider(fixture.accountRepo, tokenCache, nil),
				newQuotaRedirectingFactory(server),
			)
			capturingQuota := &autoResetIdentityCapturingQuota{delegate: quota}
			fixture.service.quota = capturingQuota
			fixture.service.accountLock = &autoResetIdentityMutationLocker{
				delegate: fixture.accountLock,
				mutate: func() {
					fixture.accountRepo.mutateAccount(testCase.mutate)
				},
			}
			fixture.recorder.reset()

			err := fixture.service.evaluateAccount(context.Background(), account.ID)

			require.NoError(t, err)
			require.EqualValues(t, 1, usageCalls.Load())
			require.EqualValues(t, 1, detailsCalls.Load())
			queryIdentity, captured := capturingQuota.firstIdentity()
			require.True(t, captured, "the details request must bind the queried upstream identity")
			require.Equal(t, account.ID, queryIdentity.credentialAccountID)
			require.Equal(t, "chatgpt-account-a", queryIdentity.chatGPTAccountID)
			require.True(t, queryIdentity.proxyConfigured)
			require.Equal(t, int64(901), queryIdentity.proxyID)
			require.Equal(t, openAIQuotaBearerAuthIdentity("access-token-a"), queryIdentity.auth)
			require.Zero(t, consumeCalls.Load(), "identity changes must be rejected before the consume POST")
			require.Zero(t, fixture.recoverer.count())
			require.Empty(t, fixture.auditRepo.snapshot())
			require.Equal(t, 1, fixture.accountLock.rowLockCount())

			fixture.accountRepo.mu.Lock()
			storedAccount := cloneAutoResetAuthzAccount(fixture.accountRepo.account)
			fixture.accountRepo.mu.Unlock()
			testCase.assert(t, storedAccount)
			state := openAIAutoResetStateFromExtra(storedAccount.Extra)
			require.NotNil(t, state)
			require.Equal(t, OpenAIAutoResetStatusFailed, state.Status)
			require.Equal(t, "OPENAI_AUTO_RESET_UPSTREAM_IDENTITY_CHANGED", state.ErrorCode)
			require.NotEmpty(t, state.AttemptCycleHash)
			require.NotEmpty(t, state.AttemptCreditHash)

			record := findAutoResetExecutionRecord(t, fixture.idempotency.inMemoryIdempotencyRepo)
			require.Equal(t, IdempotencyStatusFailedRetryable, record.Status)
			require.Nil(t, record.ResponseBody)
			events, actorErrors := fixture.recorder.snapshot()
			require.Empty(t, actorErrors)
			require.Contains(t, events, "idempotency.failed")
			require.NotContains(t, events, "atomic.finalize")
			require.NotContains(t, events, "recover")
		})
	}
}

func TestOpenAIQuotaAutoResetConfiguredProxyMissingFailsClosedBeforeNetwork(t *testing.T) {
	now := time.Now().UTC()
	account := newAutoResetIdentityAccount(now, 810)
	account.Proxy = nil
	repo := &stubQuotaAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	tokenCache := &stubQuotaTokenCache{tokens: map[string]string{
		OpenAITokenCacheKey(account): "access-token-a",
	}}

	var upstreamCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		http.Error(w, "unexpected upstream request", http.StatusInternalServerError)
	}))
	defer server.Close()
	quota := NewOpenAIQuotaService(
		repo,
		nil,
		NewOpenAITokenProvider(repo, tokenCache, nil),
		newQuotaRedirectingFactory(server),
	)

	usage, err := quota.QueryUsage(withOpenAIAutoResetContext(context.Background()), account.ID)

	require.Nil(t, usage)
	require.ErrorIs(t, err, errOpenAIAutoResetUpstreamIdentityChanged)
	require.Zero(t, upstreamCalls.Load(), "a missing bound proxy must never fall back to a direct upstream call")
}

func TestOpenAIQuotaAutoResetAgentIdentityRetryAllowsOnlyTaskIDChange(t *testing.T) {
	_, originalPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	_, replacementPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	encodePrivateKey := func(key ed25519.PrivateKey) string {
		der, marshalErr := x509.MarshalPKCS8PrivateKey(key)
		require.NoError(t, marshalErr)
		return base64.StdEncoding.EncodeToString(der)
	}

	account := &Account{
		ID:       812,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Credentials: map[string]any{
			"auth_mode":          OpenAIAuthModeAgentIdentity,
			"agent_runtime_id":   "runtime-original",
			"agent_private_key":  encodePrivateKey(originalPrivateKey),
			"task_id":            "task-original",
			"chatgpt_account_id": "chatgpt-agent",
		},
	}
	expectedAuth, err := openAIQuotaAuthIdentityFromAccount(account)
	require.NoError(t, err)
	expected, err := openAIQuotaAutoResetIdentityFromAccount(account, expectedAuth)
	require.NoError(t, err)

	tests := []struct {
		name   string
		mutate func(map[string]any)
		allow  bool
		match  bool
	}{
		{
			name: "recovered task ID",
			mutate: func(credentials map[string]any) {
				credentials["task_id"] = "task-recovered"
			},
			allow: true,
			match: true,
		},
		{
			name: "task ID without internal retry allowance",
			mutate: func(credentials map[string]any) {
				credentials["task_id"] = "task-external"
			},
			allow: false,
			match: false,
		},
		{
			name: "runtime ID",
			mutate: func(credentials map[string]any) {
				credentials["agent_runtime_id"] = "runtime-replaced"
			},
			allow: true,
			match: false,
		},
		{
			name: "private key",
			mutate: func(credentials map[string]any) {
				credentials["agent_private_key"] = encodePrivateKey(replacementPrivateKey)
			},
			allow: true,
			match: false,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			mutated := *account
			mutated.Credentials = cloneAutoResetIdentityCredentials(account.Credentials)
			testCase.mutate(mutated.Credentials)
			actualAuth, authErr := openAIQuotaAuthIdentityFromAccount(&mutated)
			require.NoError(t, authErr)
			actual, identityErr := openAIQuotaAutoResetIdentityFromAccount(&mutated, actualAuth)
			require.NoError(t, identityErr)
			require.Equal(t, testCase.match, openAIQuotaAutoResetIdentitiesMatch(expected, actual, testCase.allow))
		})
	}
}

func TestOpenAIQuotaAutoResetAgentInvalidTaskRetryAllowsOnlyRecoveredTaskID(t *testing.T) {
	now := time.Now().UTC()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)

	account := newAutoResetIdentityAccount(now, 811)
	account.ProxyID = nil
	account.Proxy = nil
	account.Credentials = map[string]any{
		"auth_mode":          OpenAIAuthModeAgentIdentity,
		"agent_runtime_id":   "runtime-auto-reset-identity",
		"agent_private_key":  base64.StdEncoding.EncodeToString(privateKeyDER),
		"task_id":            "task-auto-reset-old",
		"chatgpt_account_id": "chatgpt-auto-reset-agent",
	}
	fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))

	var usageCalls atomic.Int32
	var detailsCalls atomic.Int32
	var consumeCalls atomic.Int32
	var registerCalls atomic.Int32
	consume := func(w http.ResponseWriter, _ *http.Request) {
		call := consumeCalls.Add(1)
		if call == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_task_id"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":"ok","windows_reset":2}`))
	}
	quotaHandler := serveAutoResetIdentityUpstream(now, &usageCalls, &detailsCalls, consume)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/task/register") {
			registerCalls.Add(1)
			w.Header().Set("content-type", "application/json")
			_, _ = w.Write([]byte(`{"task_id":"task-auto-reset-new"}`))
			return
		}
		quotaHandler(w, r)
	}))
	defer server.Close()

	originalAuthBase := openAIAgentIdentityAuthAPIBaseURL
	openAIAgentIdentityAuthAPIBaseURL = server.URL
	t.Cleanup(func() { openAIAgentIdentityAuthAPIBaseURL = originalAuthBase })

	quota := NewOpenAIQuotaService(fixture.accountRepo, nil, nil, newQuotaRedirectingFactory(server))
	capturingQuota := &autoResetIdentityCapturingQuota{delegate: quota}
	fixture.service.quota = capturingQuota
	fixture.recorder.reset()

	err = fixture.service.evaluateAccount(context.Background(), account.ID)

	require.NoError(t, err)
	require.EqualValues(t, 2, consumeCalls.Load(), "the recovered task ID should authorize exactly one retry")
	require.EqualValues(t, 1, registerCalls.Load())
	require.GreaterOrEqual(t, usageCalls.Load(), int32(1))
	require.GreaterOrEqual(t, detailsCalls.Load(), int32(1))
	queryIdentity, captured := capturingQuota.firstIdentity()
	require.True(t, captured)
	require.Equal(t, "task-auto-reset-old", queryIdentity.auth.taskID)
	require.Equal(t, 2, fixture.accountLock.rowLockCount())
	require.Equal(t, 1, fixture.recoverer.count())

	fixture.accountRepo.mu.Lock()
	storedAccount := cloneAutoResetAuthzAccount(fixture.accountRepo.account)
	fixture.accountRepo.mu.Unlock()
	require.Equal(t, "task-auto-reset-new", storedAccount.GetCredential("task_id"))
	state := openAIAutoResetStateFromExtra(storedAccount.Extra)
	require.NotNil(t, state)
	require.Equal(t, OpenAIAutoResetStatusSuccess, state.Status)
	record := findAutoResetExecutionRecord(t, fixture.idempotency.inMemoryIdempotencyRepo)
	require.Equal(t, IdempotencyStatusSucceeded, record.Status)
	require.NotNil(t, record.ResponseBody)
	require.Len(t, fixture.auditRepo.snapshot(), 1)
}

func newAutoResetIdentityAccount(now time.Time, accountID int64) *Account {
	account := newAutoResetAuthzAccount(now, accountID)
	account.Credentials = map[string]any{
		"access_token":       "access-token-a",
		"chatgpt_account_id": "chatgpt-account-a",
	}
	proxyID := int64(901)
	account.ProxyID = &proxyID
	account.Proxy = &Proxy{
		ID:       proxyID,
		Protocol: "http",
		Host:     "proxy-a.internal",
		Port:     8080,
	}
	return account
}

func cloneAutoResetIdentityCredentials(credentials map[string]any) map[string]any {
	copy := make(map[string]any, len(credentials))
	for key, value := range credentials {
		copy[key] = value
	}
	return copy
}

func newAutoResetIdentityUpstreamServer(
	t testing.TB,
	now time.Time,
	usageCalls *atomic.Int32,
	detailsCalls *atomic.Int32,
	consume http.HandlerFunc,
) *httptest.Server {
	t.Helper()
	return httptest.NewServer(serveAutoResetIdentityUpstream(now, usageCalls, detailsCalls, consume))
}

func serveAutoResetIdentityUpstream(
	now time.Time,
	usageCalls *atomic.Int32,
	detailsCalls *atomic.Int32,
	consume http.HandlerFunc,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		switch r.URL.Path {
		case "/backend-api/wham/usage":
			usageCalls.Add(1)
			_, _ = fmt.Fprintf(w, `{
				"rate_limit":{
					"primary_window":{"used_percent":100,"limit_window_seconds":18000,"reset_after_seconds":3600,"reset_at":%d},
					"secondary_window":{"used_percent":10,"limit_window_seconds":604800,"reset_after_seconds":86400,"reset_at":%d}
				}
			}`, now.Add(time.Hour).Unix(), now.Add(24*time.Hour).Unix())
		case "/backend-api/wham/rate-limit-reset-credits":
			detailsCalls.Add(1)
			_, _ = fmt.Fprintf(w, `{
				"available_count":1,
				"credits":[{"id":"identity-bound-credit","reset_type":"codex_rate_limits","status":"available","expires_at":%q}]
			}`, now.Add(48*time.Hour).Format(time.RFC3339))
		case "/backend-api/wham/rate-limit-reset-credits/consume":
			consume(w, r)
		default:
			http.NotFound(w, r)
		}
	}
}
