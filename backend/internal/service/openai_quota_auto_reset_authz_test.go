package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

const (
	autoResetAuthzTestPrincipalID int64 = 704
	autoResetAuthzTestVersion     int64 = 9
)

type autoResetAuthzEventRecorder struct {
	mu       sync.Mutex
	expected authz.Actor
	events   []string
	errors   []string
}

func (r *autoResetAuthzEventRecorder) setExpected(actor authz.Actor) {
	r.mu.Lock()
	r.expected = actor
	r.mu.Unlock()
}

func (r *autoResetAuthzEventRecorder) record(ctx context.Context, event string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	if !r.expected.Valid() {
		return
	}
	actor, ok := authz.ActorFromContext(ctx)
	if !ok {
		r.errors = append(r.errors, event+": missing actor")
		return
	}
	if !r.expected.SameAuthorizationState(actor) {
		r.errors = append(r.errors, event+": actor authorization state changed")
	}
}

func (r *autoResetAuthzEventRecorder) reset() {
	r.mu.Lock()
	r.events = nil
	r.errors = nil
	r.mu.Unlock()
}

func (r *autoResetAuthzEventRecorder) snapshot() ([]string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.events...), append([]string(nil), r.errors...)
}

type autoResetAuthorizationStore struct {
	mu sync.Mutex

	configuration  authz.PolicyConfiguration
	principalID    int64
	authzVersion   int64
	active         bool
	missing        bool
	subjectErr     error
	workerErr      error
	roleVersions   map[int64]int64
	capabilities   []authz.Capability
	roleCount      int
	permissions    []string
	accountExists  bool
	accountDeleted bool

	subjectCalls int
	workerCalls  int
	workerHook   func(call int, accountID int64)
	workerCtxs   []context.Context
	recorder     *autoResetAuthzEventRecorder
}

func newAutoResetAuthorizationStore(t testing.TB, recorder *autoResetAuthzEventRecorder) *autoResetAuthorizationStore {
	t.Helper()
	configuration, err := authz.NewPolicyConfiguration(authz.PolicyConfigurationInput{
		RoleAuthorizationMode: authz.RoleAuthorizationModeLegacy,
	})
	if err != nil {
		t.Fatalf("create worker policy configuration: %v", err)
	}
	return &autoResetAuthorizationStore{
		configuration: configuration,
		principalID:   autoResetAuthzTestPrincipalID,
		authzVersion:  autoResetAuthzTestVersion,
		active:        true,
		capabilities:  []authz.Capability{authz.CapabilityPlatformAccountOpenAIQuotaAutoReset},
		permissions:   []string{string(authz.CapabilityPlatformAccountOpenAIQuotaAutoReset)},
		accountExists: true,
		recorder:      recorder,
	}
}

func (s *autoResetAuthorizationStore) LoadSubjectSnapshot(
	_ context.Context,
	subject authz.SubjectRef,
) (authz.SubjectSnapshot, error) {
	if subject.Kind() != authz.SubjectKindServicePrincipal {
		return authz.SubjectSnapshot{}, authz.ErrSubjectNotFound
	}
	s.mu.Lock()
	principalID := s.principalID
	s.mu.Unlock()
	if subject.ID() != principalID {
		return authz.SubjectSnapshot{}, authz.ErrSubjectNotFound
	}
	return s.loadSubjectSnapshot()
}

func (s *autoResetAuthorizationStore) LoadServicePrincipalSubjectSnapshotByCode(
	_ context.Context,
	code string,
) (authz.SubjectSnapshot, error) {
	if strings.TrimSpace(code) != authz.OpenAIQuotaAutoResetServicePrincipalCode {
		return authz.SubjectSnapshot{}, authz.ErrSubjectNotFound
	}
	return s.loadSubjectSnapshot()
}

func (s *autoResetAuthorizationStore) loadSubjectSnapshot() (authz.SubjectSnapshot, error) {
	s.mu.Lock()
	s.subjectCalls++
	missing := s.missing
	subjectErr := s.subjectErr
	principalID := s.principalID
	authzVersion := s.authzVersion
	active := s.active
	roles := cloneAutoResetRoleVersions(s.roleVersions)
	capabilities := append([]authz.Capability(nil), s.capabilities...)
	configuration := s.configuration
	s.mu.Unlock()

	if subjectErr != nil {
		return authz.SubjectSnapshot{}, subjectErr
	}
	if missing {
		return authz.SubjectSnapshot{}, authz.ErrSubjectNotFound
	}
	subject, err := authz.NewSubjectRef(authz.SubjectKindServicePrincipal, principalID)
	if err != nil {
		return authz.SubjectSnapshot{}, err
	}
	return authz.NewSubjectSnapshot(authz.SubjectSnapshotInput{
		Subject:       subject,
		Exists:        true,
		Active:        active,
		AuthzVersion:  authzVersion,
		RoleVersions:  roles,
		Capabilities:  capabilities,
		Configuration: configuration,
	})
}

func (s *autoResetAuthorizationStore) LoadWorkerAuthorizationSnapshot(
	ctx context.Context,
	servicePrincipalCode string,
	accountID int64,
) (authz.WorkerAuthorizationSnapshot, error) {
	s.mu.Lock()
	s.workerCalls++
	call := s.workerCalls
	hook := s.workerHook
	s.workerCtxs = append(s.workerCtxs, ctx)
	s.mu.Unlock()
	if s.recorder != nil {
		s.recorder.record(ctx, fmt.Sprintf("authorize:%d", accountID))
	}
	if hook != nil {
		hook(call, accountID)
	}

	s.mu.Lock()
	missing := s.missing
	workerErr := s.workerErr
	principalID := s.principalID
	authzVersion := s.authzVersion
	active := s.active
	roleCount := s.roleCount
	permissions := append([]string(nil), s.permissions...)
	accountExists := s.accountExists
	accountDeleted := s.accountDeleted
	s.mu.Unlock()

	if workerErr != nil {
		return authz.WorkerAuthorizationSnapshot{}, workerErr
	}
	if missing {
		return authz.WorkerAuthorizationSnapshot{}, authz.ErrSubjectNotFound
	}
	return authz.NewWorkerAuthorizationSnapshot(authz.WorkerAuthorizationSnapshotInput{
		ServicePrincipalID:   principalID,
		ServicePrincipalCode: servicePrincipalCode,
		Active:               active,
		AuthzVersion:         authzVersion,
		RoleCount:            roleCount,
		PermissionCodes:      permissions,
		AccountID:            accountID,
		AccountExists:        accountID > 0 && accountExists,
		AccountDeleted:       accountID > 0 && accountDeleted,
	})
}

func (s *autoResetAuthorizationStore) setMissing(missing bool) {
	s.mu.Lock()
	s.missing = missing
	s.mu.Unlock()
}

func (s *autoResetAuthorizationStore) setActive(active bool) {
	s.mu.Lock()
	s.active = active
	s.mu.Unlock()
}

func (s *autoResetAuthorizationStore) setSubjectError(err error) {
	s.mu.Lock()
	s.subjectErr = err
	s.mu.Unlock()
}

func (s *autoResetAuthorizationStore) setWorkerError(err error) {
	s.mu.Lock()
	s.workerErr = err
	s.mu.Unlock()
}

func (s *autoResetAuthorizationStore) setWorkerPermissions(permissions []string) {
	s.mu.Lock()
	s.permissions = append([]string(nil), permissions...)
	s.mu.Unlock()
}

func (s *autoResetAuthorizationStore) setWorkerRoles(roleVersions map[int64]int64) {
	s.mu.Lock()
	s.roleVersions = cloneAutoResetRoleVersions(roleVersions)
	s.roleCount = len(roleVersions)
	s.mu.Unlock()
}

func (s *autoResetAuthorizationStore) setAccountAuthorizationState(exists, deleted bool) {
	s.mu.Lock()
	s.accountExists = exists
	s.accountDeleted = deleted
	s.mu.Unlock()
}

func (s *autoResetAuthorizationStore) setAuthzVersion(version int64) {
	s.mu.Lock()
	s.authzVersion = version
	s.mu.Unlock()
}

func (s *autoResetAuthorizationStore) setWorkerHook(hook func(call int, accountID int64)) {
	s.mu.Lock()
	s.workerHook = hook
	s.mu.Unlock()
}

func (s *autoResetAuthorizationStore) callCounts() (subject, worker int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.subjectCalls, s.workerCalls
}

func (s *autoResetAuthorizationStore) workerContexts() []context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]context.Context(nil), s.workerCtxs...)
}

func cloneAutoResetRoleVersions(input map[int64]int64) map[int64]int64 {
	result := make(map[int64]int64, len(input))
	for roleID, version := range input {
		result[roleID] = version
	}
	return result
}

type autoResetAuthzAccountRepo struct {
	AccountRepository
	mu sync.Mutex

	account      *Account
	listAccounts []Account
	getCalls     int
	updateCalls  int
	listCalls    int
	stateWrites  []string
	getHook      func(call int)
	recorder     *autoResetAuthzEventRecorder
}

func (r *autoResetAuthzAccountRepo) GetByID(ctx context.Context, _ int64) (*Account, error) {
	if r.recorder != nil {
		r.recorder.record(ctx, "get")
	}
	r.mu.Lock()
	r.getCalls++
	call := r.getCalls
	hook := r.getHook
	account := cloneAutoResetAuthzAccount(r.account)
	r.mu.Unlock()
	if hook != nil {
		hook(call)
	}
	return account, nil
}

func (r *autoResetAuthzAccountRepo) UpdateExtra(ctx context.Context, _ int64, updates map[string]any) error {
	if r.recorder != nil {
		r.recorder.record(ctx, "update")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updateCalls++
	if r.account.Extra == nil {
		r.account.Extra = make(map[string]any)
	}
	for key, value := range updates {
		r.account.Extra[key] = value
	}
	if rawState, ok := updates[OpenAIAutoResetCreditStateExtraKey]; ok {
		if state := autoResetAuthzState(rawState); state != nil {
			r.stateWrites = append(r.stateWrites, state.Status)
		}
	}
	return nil
}

func (r *autoResetAuthzAccountRepo) ListWithFilters(
	ctx context.Context,
	_ pagination.PaginationParams,
	_, _, _, _ string,
	_ int64,
	_ string,
) ([]Account, *pagination.PaginationResult, error) {
	if r.recorder != nil {
		r.recorder.record(ctx, "list")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.listCalls++
	result := make([]Account, len(r.listAccounts))
	for i := range r.listAccounts {
		result[i] = *cloneAutoResetAuthzAccount(&r.listAccounts[i])
	}
	return result, nil, nil
}

func (r *autoResetAuthzAccountRepo) counts() (get, update, list int, states []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.getCalls, r.updateCalls, r.listCalls, append([]string(nil), r.stateWrites...)
}

func (r *autoResetAuthzAccountRepo) setGetHook(hook func(call int)) {
	r.mu.Lock()
	r.getHook = hook
	r.mu.Unlock()
}

func (r *autoResetAuthzAccountRepo) mutateAccount(mutate func(*Account)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	mutate(r.account)
}

func cloneAutoResetAuthzAccount(account *Account) *Account {
	if account == nil {
		return nil
	}
	copy := *account
	copy.Extra = cloneOpenAIAutoResetExtra(account.Extra)
	return &copy
}

func autoResetAuthzState(raw any) *OpenAIAutoResetCreditState {
	switch state := raw.(type) {
	case *OpenAIAutoResetCreditState:
		if state == nil {
			return nil
		}
		copy := *state
		return &copy
	case OpenAIAutoResetCreditState:
		copy := state
		return &copy
	default:
		return nil
	}
}

type autoResetAuthzQuota struct {
	mu sync.Mutex

	usage       *OpenAIQuotaUsage
	resetResult *OpenAIQuotaResetResult
	queryCalls  int
	cacheCalls  int
	resetCalls  int
	requestIDs  []string
	queryHook   func(call int)
	resetHook   func(call int)
	recorder    *autoResetAuthzEventRecorder
}

func (q *autoResetAuthzQuota) QueryUsage(ctx context.Context, _ int64) (*OpenAIQuotaUsage, error) {
	if q.recorder != nil {
		q.recorder.record(ctx, "query")
	}
	q.mu.Lock()
	q.queryCalls++
	call := q.queryCalls
	hook := q.queryHook
	usage := q.usage
	q.mu.Unlock()
	if hook != nil {
		hook(call)
	}
	if usage == nil {
		return nil, nil
	}
	copy := *usage
	return &copy, nil
}

func (q *autoResetAuthzQuota) CacheResetCreditsSnapshot(
	ctx context.Context,
	_ int64,
	_ *OpenAIRateLimitResetCredits,
) error {
	if q.recorder != nil {
		q.recorder.record(ctx, "cache")
	}
	q.mu.Lock()
	q.cacheCalls++
	q.mu.Unlock()
	return nil
}

func (q *autoResetAuthzQuota) ResetCreditTargeted(
	ctx context.Context,
	_ int64,
	_ string,
	requestID string,
) (*OpenAIQuotaResetResult, error) {
	if q.recorder != nil {
		q.recorder.record(ctx, "reset")
	}
	q.mu.Lock()
	q.resetCalls++
	call := q.resetCalls
	q.requestIDs = append(q.requestIDs, requestID)
	result := q.resetResult
	hook := q.resetHook
	q.mu.Unlock()
	if hook != nil {
		hook(call)
	}
	if result == nil {
		result = &OpenAIQuotaResetResult{Code: "ok", WindowsReset: 2}
	}
	copy := *result
	return &copy, nil
}

func (q *autoResetAuthzQuota) lastRequestID() string {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.requestIDs) == 0 {
		return ""
	}
	return q.requestIDs[len(q.requestIDs)-1]
}

func (q *autoResetAuthzQuota) setQueryHook(hook func(call int)) {
	q.mu.Lock()
	q.queryHook = hook
	q.mu.Unlock()
}

func (q *autoResetAuthzQuota) setResetHook(hook func(call int)) {
	q.mu.Lock()
	q.resetHook = hook
	q.mu.Unlock()
}

func (q *autoResetAuthzQuota) setUsage(usage *OpenAIQuotaUsage) {
	q.mu.Lock()
	q.usage = usage
	q.mu.Unlock()
}

func (q *autoResetAuthzQuota) setResetResult(result *OpenAIQuotaResetResult) {
	q.mu.Lock()
	q.resetResult = result
	q.mu.Unlock()
}

func (q *autoResetAuthzQuota) counts() (query, cache, reset int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.queryCalls, q.cacheCalls, q.resetCalls
}

type autoResetAuthzRecoverer struct {
	mu       sync.Mutex
	calls    int
	recorder *autoResetAuthzEventRecorder
}

func (r *autoResetAuthzRecoverer) RecoverAccountState(
	ctx context.Context,
	_ int64,
	_ AccountRecoveryOptions,
) (*SuccessfulTestRecoveryResult, error) {
	if r.recorder != nil {
		r.recorder.record(ctx, "recover")
	}
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
	return &SuccessfulTestRecoveryResult{ClearedRateLimit: true}, nil
}

func (r *autoResetAuthzRecoverer) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

type autoResetAuthzLeaderLock struct {
	mu           sync.Mutex
	acquireCalls int
	releaseCalls int
	recorder     *autoResetAuthzEventRecorder
}

func (l *autoResetAuthzLeaderLock) TryAcquireLeaderLock(
	ctx context.Context,
	_, _ string,
	_ time.Duration,
) (bool, error) {
	if l.recorder != nil {
		l.recorder.record(ctx, "lock")
	}
	l.mu.Lock()
	l.acquireCalls++
	l.mu.Unlock()
	return true, nil
}

func (l *autoResetAuthzLeaderLock) ReleaseLeaderLock(ctx context.Context, _, _ string) error {
	if l.recorder != nil {
		l.recorder.record(ctx, "release")
	}
	l.mu.Lock()
	l.releaseCalls++
	l.mu.Unlock()
	return nil
}

func (l *autoResetAuthzLeaderLock) counts() (acquire, release int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.acquireCalls, l.releaseCalls
}

type autoResetDurableAuditRepo struct {
	AuditLogRepository
	mu sync.Mutex

	insertErr error
	entries   []*AuditLog
	recorder  *autoResetAuthzEventRecorder
}

type autoResetAtomicFinalizer struct {
	repo      IdempotencyRepository
	auditRepo *autoResetDurableAuditRepo
	recorder  *autoResetAuthzEventRecorder
}

type autoResetScriptedFinalizer struct {
	mu     sync.Mutex
	errors []error
	calls  int
}

func (f *autoResetScriptedFinalizer) FinalizeOpenAIQuotaAutoReset(
	_ context.Context,
	_ *OpenAIQuotaAutoResetFinalization,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := f.calls
	f.calls++
	if call < len(f.errors) {
		return f.errors[call]
	}
	return nil
}

func (f *autoResetScriptedFinalizer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *autoResetAtomicFinalizer) FinalizeOpenAIQuotaAutoReset(
	ctx context.Context,
	input *OpenAIQuotaAutoResetFinalization,
) error {
	if f.recorder != nil {
		f.recorder.record(ctx, "atomic.finalize")
	}
	if input == nil {
		return ErrOpenAIQuotaAutoResetFinalizationInvalid
	}
	f.auditRepo.mu.Lock()
	auditErr := f.auditRepo.insertErr
	f.auditRepo.mu.Unlock()
	if auditErr != nil {
		return auditErr
	}
	if err := f.repo.MarkSucceeded(
		ctx,
		input.IdempotencyRecordID,
		input.ResponseStatus,
		input.ResponseBody,
		input.ExpiresAt,
	); err != nil {
		return err
	}
	f.auditRepo.mu.Lock()
	f.auditRepo.entries = append(f.auditRepo.entries, cloneAutoResetAuditLog(&input.Audit))
	f.auditRepo.mu.Unlock()
	return nil
}

func TestOpenAIQuotaAutoResetFinalizeRetriesOnlyTransientFailures(t *testing.T) {
	transientErr := errors.New("transient finalization failure")
	tests := []struct {
		name        string
		errors      []error
		cancel      bool
		wantCalls   int
		wantErr     error
		wantNoError bool
	}{
		{
			name:        "transient failure succeeds on second attempt",
			errors:      []error{transientErr, nil},
			wantCalls:   2,
			wantNoError: true,
		},
		{
			name:      "conflict is not retried",
			errors:    []error{ErrOpenAIQuotaAutoResetFinalizationConflict},
			wantCalls: 1,
			wantErr:   ErrOpenAIQuotaAutoResetFinalizationConflict,
		},
		{
			name:      "canceled caller context is not retried",
			errors:    []error{transientErr},
			cancel:    true,
			wantCalls: 1,
			wantErr:   transientErr,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			if testCase.cancel {
				cancel()
			} else {
				defer cancel()
			}
			finalizer := &autoResetScriptedFinalizer{errors: testCase.errors}
			service := &OpenAIQuotaAutoResetService{finalizer: finalizer}

			err := service.finalizeOpenAIAutoReset(ctx, 401, IdempotencySuccessFinalization{
				RecordID:           17,
				Scope:              "actor-qualified-scope",
				RequestFingerprint: "request-fingerprint",
				ResponseStatus:     http.StatusOK,
				ResponseBody:       `{"status":"success"}`,
				ExpiresAt:          time.Now().Add(time.Hour),
			}, &AuditLog{})

			if testCase.wantNoError {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, errOpenAIAutoResetFinalization)
				require.ErrorIs(t, err, testCase.wantErr)
			}
			require.Equal(t, testCase.wantCalls, finalizer.count())
		})
	}
}

func (r *autoResetDurableAuditRepo) Insert(ctx context.Context, entry *AuditLog) error {
	if r.recorder != nil {
		r.recorder.record(ctx, "audit")
	}
	r.mu.Lock()
	r.entries = append(r.entries, cloneAutoResetAuditLog(entry))
	err := r.insertErr
	r.mu.Unlock()
	return err
}

func (r *autoResetDurableAuditRepo) setError(err error) {
	r.mu.Lock()
	r.insertErr = err
	r.mu.Unlock()
}

func (r *autoResetDurableAuditRepo) snapshot() []*AuditLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]*AuditLog, len(r.entries))
	for i := range r.entries {
		result[i] = cloneAutoResetAuditLog(r.entries[i])
	}
	return result
}

func cloneAutoResetAuditLog(entry *AuditLog) *AuditLog {
	if entry == nil {
		return nil
	}
	copy := *entry
	if entry.ActorServicePrincipalID != nil {
		principalID := *entry.ActorServicePrincipalID
		copy.ActorServicePrincipalID = &principalID
	}
	if entry.Extra != nil {
		copy.Extra = make(map[string]any, len(entry.Extra))
		for key, value := range entry.Extra {
			copy.Extra[key] = value
		}
	}
	return &copy
}

type autoResetObservedIdempotencyRepo struct {
	*inMemoryIdempotencyRepo
	mu          sync.Mutex
	createCalls int
	createHook  func(call int, record *IdempotencyRecord)
	recorder    *autoResetAuthzEventRecorder
}

func (r *autoResetObservedIdempotencyRepo) CreateProcessing(ctx context.Context, record *IdempotencyRecord) (bool, error) {
	r.record(ctx, "idempotency.create")
	created, err := r.inMemoryIdempotencyRepo.CreateProcessing(ctx, record)
	r.mu.Lock()
	r.createCalls++
	call := r.createCalls
	hook := r.createHook
	r.mu.Unlock()
	if hook != nil {
		hook(call, cloneRecord(record))
	}
	return created, err
}

func (r *autoResetObservedIdempotencyRepo) GetByScopeAndKeyHash(ctx context.Context, scope, keyHash string) (*IdempotencyRecord, error) {
	r.record(ctx, "idempotency.get")
	return r.inMemoryIdempotencyRepo.GetByScopeAndKeyHash(ctx, scope, keyHash)
}

func (r *autoResetObservedIdempotencyRepo) ExtendExpiration(ctx context.Context, id int64, fingerprint string, expiresAt time.Time) (bool, error) {
	r.record(ctx, "idempotency.extend")
	return r.inMemoryIdempotencyRepo.ExtendExpiration(ctx, id, fingerprint, expiresAt)
}

func (r *autoResetObservedIdempotencyRepo) TryReclaim(
	ctx context.Context,
	id int64,
	status string,
	now, lockedUntil, expiresAt time.Time,
) (bool, error) {
	r.record(ctx, "idempotency.reclaim")
	return r.inMemoryIdempotencyRepo.TryReclaim(ctx, id, status, now, lockedUntil, expiresAt)
}

func (r *autoResetObservedIdempotencyRepo) ExtendProcessingLock(
	ctx context.Context,
	id int64,
	fingerprint string,
	lockedUntil, expiresAt time.Time,
) (bool, error) {
	r.record(ctx, "idempotency.extend_lock")
	return r.inMemoryIdempotencyRepo.ExtendProcessingLock(ctx, id, fingerprint, lockedUntil, expiresAt)
}

func (r *autoResetObservedIdempotencyRepo) MarkSucceeded(
	ctx context.Context,
	id int64,
	status int,
	body string,
	expiresAt time.Time,
) error {
	r.record(ctx, "idempotency.succeeded")
	return r.inMemoryIdempotencyRepo.MarkSucceeded(ctx, id, status, body, expiresAt)
}

func (r *autoResetObservedIdempotencyRepo) MarkFailedRetryable(
	ctx context.Context,
	id int64,
	reason string,
	lockedUntil, expiresAt time.Time,
) error {
	r.record(ctx, "idempotency.failed")
	return r.inMemoryIdempotencyRepo.MarkFailedRetryable(ctx, id, reason, lockedUntil, expiresAt)
}

func (r *autoResetObservedIdempotencyRepo) DeleteExpired(ctx context.Context, now time.Time, limit int) (int64, error) {
	r.record(ctx, "idempotency.delete")
	return r.inMemoryIdempotencyRepo.DeleteExpired(ctx, now, limit)
}

func (r *autoResetObservedIdempotencyRepo) record(ctx context.Context, event string) {
	if r.recorder != nil {
		r.recorder.record(ctx, event)
	}
}

func (r *autoResetObservedIdempotencyRepo) createCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.createCalls
}

func (r *autoResetObservedIdempotencyRepo) setCreateHook(hook func(call int, record *IdempotencyRecord)) {
	r.mu.Lock()
	r.createHook = hook
	r.mu.Unlock()
}

type autoResetAuthzContextObserver struct {
	mu       sync.Mutex
	contexts []context.Context
}

func (o *autoResetAuthzContextObserver) record(ctx context.Context) {
	o.mu.Lock()
	o.contexts = append(o.contexts, ctx)
	o.mu.Unlock()
}

func (o *autoResetAuthzContextObserver) snapshot() []context.Context {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]context.Context(nil), o.contexts...)
}

type autoResetAuthzFixture struct {
	service     *OpenAIQuotaAutoResetService
	store       *autoResetAuthorizationStore
	recorder    *autoResetAuthzEventRecorder
	accountRepo *autoResetAuthzAccountRepo
	quota       *autoResetAuthzQuota
	recoverer   *autoResetAuthzRecoverer
	idempotency *autoResetObservedIdempotencyRepo
	auditRepo   *autoResetDurableAuditRepo
	finalizer   *autoResetAtomicFinalizer
	accountLock *autoResetTestAccountLocker
	lockCtxs    *autoResetAuthzContextObserver
	resolver    authz.Resolver
	policy      authz.WorkerPolicy
	actor       authz.Actor
}

func newAutoResetAuthzFixture(
	t testing.TB,
	account *Account,
	usage *OpenAIQuotaUsage,
) *autoResetAuthzFixture {
	t.Helper()
	recorder := &autoResetAuthzEventRecorder{}
	store := newAutoResetAuthorizationStore(t, recorder)
	resolver := authz.NewActorResolver(store)
	policy := authz.NewWorkerPolicy(store)
	actor, err := resolver.ResolveServicePrincipal(
		context.Background(),
		authz.OpenAIQuotaAutoResetServicePrincipalCode,
		authz.AuthMethodServicePrincipal,
	)
	if err != nil {
		t.Fatalf("resolve expected worker actor: %v", err)
	}
	recorder.setExpected(actor)
	recorder.reset()

	accountRepo := &autoResetAuthzAccountRepo{account: account, recorder: recorder}
	quota := &autoResetAuthzQuota{usage: usage, recorder: recorder}
	recoverer := &autoResetAuthzRecoverer{recorder: recorder}
	idempotencyRepo := &autoResetObservedIdempotencyRepo{
		inMemoryIdempotencyRepo: newInMemoryIdempotencyRepo(),
		recorder:                recorder,
	}
	idempotencyConfig := DefaultIdempotencyConfig()
	idempotencyConfig.ObserveOnly = false
	idempotencyConfig.FailedRetryBackoff = 0
	auditRepo := &autoResetDurableAuditRepo{recorder: recorder}
	audit := NewAuditLogService(auditRepo, nil)
	finalizer := &autoResetAtomicFinalizer{repo: idempotencyRepo, auditRepo: auditRepo, recorder: recorder}
	accountLock := newAutoResetTestAccountLocker()
	lockCtxs := &autoResetAuthzContextObserver{}
	accountLock.setAcquireHook(func(ctx context.Context, _ int64) {
		lockCtxs.record(ctx)
		recorder.record(ctx, "account_lock")
	})
	service := NewOpenAIQuotaAutoResetService(
		accountRepo,
		quota,
		recoverer,
		NewIdempotencyCoordinator(idempotencyRepo, idempotencyConfig),
		finalizer,
		accountLock,
		audit,
		nil,
		nil,
		resolver,
		policy,
	)
	return &autoResetAuthzFixture{
		service:     service,
		store:       store,
		recorder:    recorder,
		accountRepo: accountRepo,
		quota:       quota,
		recoverer:   recoverer,
		idempotency: idempotencyRepo,
		auditRepo:   auditRepo,
		finalizer:   finalizer,
		accountLock: accountLock,
		lockCtxs:    lockCtxs,
		resolver:    resolver,
		policy:      policy,
		actor:       actor,
	}
}

func newOpenAIAutoResetTestSecurity(
	t testing.TB,
	idempotencyRepo IdempotencyRepository,
) (authz.Resolver, authz.WorkerPolicy, *AuditLogService, OpenAIQuotaAutoResetFinalizer) {
	t.Helper()
	store := newAutoResetAuthorizationStore(t, nil)
	auditRepo := &autoResetDurableAuditRepo{}
	return authz.NewActorResolver(store),
		authz.NewWorkerPolicy(store),
		NewAuditLogService(auditRepo, nil),
		&autoResetAtomicFinalizer{repo: idempotencyRepo, auditRepo: auditRepo}
}

func TestOpenAIQuotaAutoResetStartFailsClosedWithoutAuthorizedWorker(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name    string
		prepare func(*autoResetAuthzFixture) error
		wantErr error
	}{
		{
			name: "subject missing",
			prepare: func(fixture *autoResetAuthzFixture) error {
				fixture.store.setMissing(true)
				return nil
			},
			wantErr: authz.ErrActorInactive,
		},
		{
			name: "subject disabled",
			prepare: func(fixture *autoResetAuthzFixture) error {
				fixture.store.setActive(false)
				return nil
			},
			wantErr: authz.ErrActorInactive,
		},
		{
			name: "resolver store unavailable",
			prepare: func(fixture *autoResetAuthzFixture) error {
				cause := errors.New("resolver database unavailable")
				fixture.store.setSubjectError(cause)
				return cause
			},
			wantErr: authz.ErrAuthorizationUnavailable,
		},
		{
			name: "worker policy store unavailable",
			prepare: func(fixture *autoResetAuthzFixture) error {
				cause := errors.New("policy database unavailable")
				fixture.store.setWorkerError(cause)
				return cause
			},
			wantErr: authz.ErrAuthorizationUnavailable,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newAutoResetAuthzFixture(t, newAutoResetAuthzAccount(now, 301), newAutoResetAuthzUsage(now))
			cause := testCase.prepare(fixture)
			before := currentOpenAIAutoResetNotifierForTest()

			err := fixture.service.Start()
			fixture.service.Stop()

			require.ErrorIs(t, err, testCase.wantErr)
			if cause != nil {
				require.ErrorIs(t, err, cause)
			}
			require.Same(t, before, currentOpenAIAutoResetNotifierForTest())
			getCalls, updateCalls, listCalls, _ := fixture.accountRepo.counts()
			require.Zero(t, getCalls)
			require.Zero(t, updateCalls)
			require.Zero(t, listCalls)
			queryCalls, cacheCalls, resetCalls := fixture.quota.counts()
			require.Zero(t, queryCalls)
			require.Zero(t, cacheCalls)
			require.Zero(t, resetCalls)
		})
	}
}

func TestOpenAIQuotaAutoResetStartFailsClosedWithIncompleteDependencies(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		mutate func(*OpenAIQuotaAutoResetService)
	}{
		{name: "account repository", mutate: func(service *OpenAIQuotaAutoResetService) { service.accountRepo = nil }},
		{name: "quota", mutate: func(service *OpenAIQuotaAutoResetService) { service.quota = nil }},
		{name: "recoverer", mutate: func(service *OpenAIQuotaAutoResetService) { service.recoverer = nil }},
		{name: "idempotency", mutate: func(service *OpenAIQuotaAutoResetService) { service.idempotency = nil }},
		{name: "idempotency repository", mutate: func(service *OpenAIQuotaAutoResetService) { service.idempotency.repo = nil }},
		{name: "terminal finalizer", mutate: func(service *OpenAIQuotaAutoResetService) { service.finalizer = nil }},
		{name: "account lock", mutate: func(service *OpenAIQuotaAutoResetService) { service.accountLock = nil }},
		{name: "audit", mutate: func(service *OpenAIQuotaAutoResetService) { service.audit = nil }},
		{name: "audit repository", mutate: func(service *OpenAIQuotaAutoResetService) { service.audit = NewAuditLogService(nil, nil) }},
		{name: "resolver", mutate: func(service *OpenAIQuotaAutoResetService) { service.resolver = nil }},
		{name: "worker policy", mutate: func(service *OpenAIQuotaAutoResetService) { service.policy = nil }},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newAutoResetAuthzFixture(t, newAutoResetAuthzAccount(now, 302), newAutoResetAuthzUsage(now))
			testCase.mutate(fixture.service)
			before := currentOpenAIAutoResetNotifierForTest()

			err := fixture.service.Start()
			fixture.service.Stop()

			require.ErrorIs(t, err, authz.ErrAuthorizationUnavailable)
			require.Same(t, before, currentOpenAIAutoResetNotifierForTest())
			_, workerCalls := fixture.store.callCounts()
			require.Zero(t, workerCalls)
		})
	}
}

func TestOpenAIQuotaAutoResetScannerAuthorizesBeforeLockAndList(t *testing.T) {
	now := time.Now().UTC()

	t.Run("authorized order", func(t *testing.T) {
		fixture := newAutoResetAuthzFixture(t, newAutoResetAuthzAccount(now, 311), newAutoResetAuthzUsage(now))
		leader := &autoResetAuthzLeaderLock{recorder: fixture.recorder}
		fixture.service.leaderLock = leader
		fixture.recorder.reset()

		fixture.service.scanEnabledAccounts(context.Background())

		events, actorErrors := fixture.recorder.snapshot()
		require.Empty(t, actorErrors)
		require.Equal(t, []string{
			"authorize:0",
			"authorize:0",
			"lock",
			"authorize:0",
			"recovery.list",
			"authorize:0",
			"list",
			"authorize:0",
			"release",
		}, events)
		acquireCalls, releaseCalls := leader.counts()
		require.Equal(t, 1, acquireCalls)
		require.Equal(t, 1, releaseCalls)
	})

	t.Run("denied before lock and list", func(t *testing.T) {
		fixture := newAutoResetAuthzFixture(t, newAutoResetAuthzAccount(now, 312), newAutoResetAuthzUsage(now))
		leader := &autoResetAuthzLeaderLock{recorder: fixture.recorder}
		fixture.service.leaderLock = leader
		fixture.store.setWorkerPermissions(nil)
		fixture.recorder.reset()

		fixture.service.scanEnabledAccounts(context.Background())

		events, actorErrors := fixture.recorder.snapshot()
		require.Empty(t, actorErrors)
		require.Equal(t, []string{"authorize:0"}, events)
		acquireCalls, releaseCalls := leader.counts()
		require.Zero(t, acquireCalls)
		require.Zero(t, releaseCalls)
		_, _, listCalls, _ := fixture.accountRepo.counts()
		require.Zero(t, listCalls)
	})

	t.Run("revoked before release", func(t *testing.T) {
		fixture := newAutoResetAuthzFixture(t, newAutoResetAuthzAccount(now, 313), newAutoResetAuthzUsage(now))
		leader := &autoResetAuthzLeaderLock{recorder: fixture.recorder}
		fixture.service.leaderLock = leader
		fixture.store.setWorkerHook(func(call int, _ int64) {
			if call == 5 {
				fixture.store.setWorkerPermissions(nil)
			}
		})
		fixture.recorder.reset()

		fixture.service.scanEnabledAccounts(context.Background())

		events, actorErrors := fixture.recorder.snapshot()
		require.Empty(t, actorErrors)
		require.Equal(t, []string{
			"authorize:0",
			"authorize:0",
			"lock",
			"authorize:0",
			"recovery.list",
			"authorize:0",
			"list",
			"authorize:0",
		}, events)
		acquireCalls, releaseCalls := leader.counts()
		require.Equal(t, 1, acquireCalls)
		require.Zero(t, releaseCalls)
		_, _, listCalls, _ := fixture.accountRepo.counts()
		require.Equal(t, 1, listCalls)
	})
}

func TestOpenAIQuotaAutoResetEvaluateAuthorizesBeforeFirstAccountLoad(t *testing.T) {
	now := time.Now().UTC()

	t.Run("authorized order", func(t *testing.T) {
		type callerContextKey struct{}

		account := newAutoResetAuthzAccount(now, 321)
		account.Extra[OpenAIAutoResetCreditEnabledExtraKey] = false
		fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))
		fixture.recorder.reset()
		callerMarker := &struct{}{}
		callerCtx := context.WithValue(context.Background(), callerContextKey{}, callerMarker)

		err := fixture.service.evaluateAccount(callerCtx, account.ID)

		require.NoError(t, err)
		events, actorErrors := fixture.recorder.snapshot()
		require.Empty(t, actorErrors)
		require.Equal(t, []string{"authorize:321", "authorize:321", "account_lock", "authorize:321", "get"}, events)
		acquireCalls, releaseCalls, _ := fixture.accountLock.counts()
		require.Equal(t, 1, acquireCalls)
		require.Equal(t, 1, releaseCalls)
		lockContexts := fixture.lockCtxs.snapshot()
		require.Len(t, lockContexts, 1)
		workerContexts := fixture.store.workerContexts()
		require.Len(t, workerContexts, 3)
		require.Same(t, workerContexts[1], lockContexts[0], "the authorized context must be passed unchanged to the account locker")
		require.Same(t, callerMarker, lockContexts[0].Value(callerContextKey{}))
		require.True(t, isOpenAIAutoResetContext(lockContexts[0]))
		lockActor, ok := authz.ActorFromContext(lockContexts[0])
		require.True(t, ok)
		require.True(t, fixture.actor.SameAuthorizationState(lockActor))
		require.Zero(t, fixture.accountLock.rowLockCount(), "an ineligible account must not upgrade the advisory lease")
	})

	t.Run("revoked before load", func(t *testing.T) {
		account := newAutoResetAuthzAccount(now, 322)
		account.Extra[OpenAIAutoResetCreditEnabledExtraKey] = false
		fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))
		fixture.store.setWorkerHook(func(call int, _ int64) {
			if call == 3 {
				fixture.store.setWorkerPermissions(nil)
			}
		})
		fixture.recorder.reset()

		err := fixture.service.evaluateAccount(context.Background(), account.ID)

		require.ErrorIs(t, err, authz.ErrPolicyAccessDenied)
		events, actorErrors := fixture.recorder.snapshot()
		require.Empty(t, actorErrors)
		require.Equal(t, []string{"authorize:322", "authorize:322", "account_lock", "authorize:322"}, events)
		getCalls, _, _, _ := fixture.accountRepo.counts()
		require.Zero(t, getCalls)
		acquireCalls, releaseCalls, _ := fixture.accountLock.counts()
		require.Equal(t, 1, acquireCalls)
		require.Equal(t, 1, releaseCalls)
		require.Zero(t, fixture.accountLock.rowLockCount())

		fixture.store.setWorkerHook(nil)
		fixture.store.setWorkerPermissions([]string{string(authz.CapabilityPlatformAccountOpenAIQuotaAutoReset)})
		fixture.recorder.reset()

		require.NoError(t, fixture.service.evaluateAccount(context.Background(), account.ID))
		events, actorErrors = fixture.recorder.snapshot()
		require.Empty(t, actorErrors)
		require.Equal(t, []string{"authorize:322", "authorize:322", "account_lock", "authorize:322", "get"}, events)
		acquireCalls, releaseCalls, _ = fixture.accountLock.counts()
		require.Equal(t, 2, acquireCalls)
		require.Equal(t, 2, releaseCalls)
		require.Zero(t, fixture.accountLock.rowLockCount())
	})
}

func TestOpenAIQuotaAutoResetAuthorizationDenialHasZeroSideEffects(t *testing.T) {
	now := time.Now().UTC()
	requiredPermission := string(authz.CapabilityPlatformAccountOpenAIQuotaAutoReset)
	tests := []struct {
		name                 string
		prepare              func(*autoResetAuthzFixture)
		refreshExpectedActor bool
		wantErr              error
	}{
		{
			name: "target missing",
			prepare: func(fixture *autoResetAuthzFixture) {
				fixture.store.setAccountAuthorizationState(false, false)
			},
			wantErr: authz.ErrPolicyAccessDenied,
		},
		{
			name: "target deleted",
			prepare: func(fixture *autoResetAuthzFixture) {
				fixture.store.setAccountAuthorizationState(true, true)
			},
			wantErr: authz.ErrPolicyAccessDenied,
		},
		{
			name: "direct permission missing",
			prepare: func(fixture *autoResetAuthzFixture) {
				fixture.store.setWorkerPermissions(nil)
			},
			wantErr: authz.ErrPolicyAccessDenied,
		},
		{
			name: "extra direct permission",
			prepare: func(fixture *autoResetAuthzFixture) {
				fixture.store.setWorkerPermissions([]string{
					requiredPermission,
					string(authz.CapabilityPlatformResourceViewAll),
				})
			},
			wantErr: authz.ErrPolicyAccessDenied,
		},
		{
			name: "service principal has role",
			prepare: func(fixture *autoResetAuthzFixture) {
				fixture.store.setWorkerRoles(map[int64]int64{17: 3})
			},
			refreshExpectedActor: true,
			wantErr:              authz.ErrSessionInvalid,
		},
		{
			name: "service principal disabled",
			prepare: func(fixture *autoResetAuthzFixture) {
				fixture.store.setActive(false)
			},
			wantErr: authz.ErrActorInactive,
		},
		{
			name: "service principal authorization stale",
			prepare: func(fixture *autoResetAuthzFixture) {
				fixture.store.setWorkerHook(func(call int, _ int64) {
					if call == 1 {
						fixture.store.setAuthzVersion(autoResetAuthzTestVersion + 1)
					}
				})
			},
			wantErr: authz.ErrSessionInvalid,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			account := newAutoResetAuthzAccount(now, 324)
			fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))
			testCase.prepare(fixture)
			if testCase.refreshExpectedActor {
				currentActor, err := fixture.resolver.ResolveServicePrincipal(
					context.Background(),
					authz.OpenAIQuotaAutoResetServicePrincipalCode,
					authz.AuthMethodServicePrincipal,
				)
				require.NoError(t, err)
				fixture.recorder.setExpected(currentActor)
			}
			fixture.recorder.reset()

			err := fixture.service.evaluateAccount(context.Background(), account.ID)

			require.ErrorIs(t, err, testCase.wantErr)
			requireAutoResetAuthorizationDenialHasNoSideEffects(t, fixture)
		})
	}
}

func requireAutoResetAuthorizationDenialHasNoSideEffects(
	t testing.TB,
	fixture *autoResetAuthzFixture,
) {
	t.Helper()
	acquireCalls, releaseCalls, contentions := fixture.accountLock.counts()
	require.Zero(t, acquireCalls)
	require.Zero(t, releaseCalls)
	require.Zero(t, contentions)
	require.Empty(t, fixture.lockCtxs.snapshot())

	getCalls, updateCalls, listCalls, states := fixture.accountRepo.counts()
	require.Zero(t, getCalls)
	require.Zero(t, updateCalls)
	require.Zero(t, listCalls)
	require.Empty(t, states)
	queryCalls, cacheCalls, resetCalls := fixture.quota.counts()
	require.Zero(t, queryCalls)
	require.Zero(t, cacheCalls)
	require.Zero(t, resetCalls)
	require.Zero(t, fixture.idempotency.createCount())
	require.Zero(t, fixture.recoverer.count())
	require.Empty(t, fixture.auditRepo.snapshot())

	events, actorErrors := fixture.recorder.snapshot()
	require.Empty(t, actorErrors)
	for _, event := range events {
		require.NotEqual(t, "account_lock", event)
		require.NotEqual(t, "get", event)
		require.NotEqual(t, "update", event)
		require.NotEqual(t, "list", event)
		require.NotEqual(t, "query", event)
		require.NotEqual(t, "cache", event)
		require.NotEqual(t, "reset", event)
		require.NotEqual(t, "recover", event)
		require.NotEqual(t, "audit", event)
		require.NotEqual(t, "atomic.finalize", event)
		require.False(t, strings.HasPrefix(event, "idempotency."))
	}
}

func TestOpenAIQuotaAutoResetAccountLockBoundaryFailsClosed(t *testing.T) {
	now := time.Now().UTC()
	acquireErr := errors.New("account lock backend unavailable")
	tests := []struct {
		name      string
		configure func(*autoResetTestAccountLocker)
		wantErr   error
	}{
		{
			name: "acquisition error",
			configure: func(locker *autoResetTestAccountLocker) {
				locker.setAcquireError(acquireErr)
			},
			wantErr: acquireErr,
		},
		{
			name: "acquired with nil lease",
			configure: func(locker *autoResetTestAccountLocker) {
				locker.setNilLeaseMode(true, false)
			},
			wantErr: errOpenAIAutoResetLeaseMissing,
		},
		{
			name: "acquired with typed nil lease",
			configure: func(locker *autoResetTestAccountLocker) {
				locker.setNilLeaseMode(false, true)
			},
			wantErr: errOpenAIAutoResetLeaseMissing,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			account := newAutoResetAuthzAccount(now, 323)
			fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))
			testCase.configure(fixture.accountLock)
			fixture.recorder.reset()

			err := fixture.service.evaluateAccount(context.Background(), account.ID)

			require.ErrorIs(t, err, testCase.wantErr)
			getCalls, updateCalls, listCalls, states := fixture.accountRepo.counts()
			require.Zero(t, getCalls)
			require.Zero(t, updateCalls)
			require.Zero(t, listCalls)
			require.Empty(t, states)
			queryCalls, cacheCalls, resetCalls := fixture.quota.counts()
			require.Zero(t, queryCalls)
			require.Zero(t, cacheCalls)
			require.Zero(t, resetCalls)
			require.Zero(t, fixture.idempotency.createCount())
			require.Zero(t, fixture.recoverer.count())
			require.Empty(t, fixture.auditRepo.snapshot())
			acquireCalls, releaseCalls, contentions := fixture.accountLock.counts()
			require.Equal(t, 1, acquireCalls)
			require.Zero(t, releaseCalls)
			require.Zero(t, contentions)
			require.Zero(t, fixture.accountLock.rowLockCount())
			events, actorErrors := fixture.recorder.snapshot()
			require.Empty(t, actorErrors)
			require.Equal(t, []string{"authorize:323", "authorize:323", "account_lock"}, events)
		})
	}
}

func TestOpenAIQuotaAutoResetAccountRowProtectionFailsClosedAfterOwnerClaim(t *testing.T) {
	now := time.Now().UTC()
	protectErr := errors.New("account row protection unavailable")
	tests := []struct {
		name      string
		configure func(*autoResetTestAccountLocker)
		wantErr   error
		wantAudit bool
	}{
		{
			name: "row lock error",
			configure: func(locker *autoResetTestAccountLocker) {
				locker.setRowProtection(protectErr, false)
			},
			wantErr:   protectErr,
			wantAudit: true,
		},
		{
			name: "account missing at upgrade",
			configure: func(locker *autoResetTestAccountLocker) {
				locker.setRowProtection(nil, true)
			},
			wantErr: nil,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			account := newAutoResetAuthzAccount(now, 324)
			fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))
			testCase.configure(fixture.accountLock)
			fixture.recorder.reset()

			err := fixture.service.evaluateAccount(context.Background(), account.ID)

			if testCase.wantErr == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, testCase.wantErr)
			}
			acquireCalls, releaseCalls, contentions := fixture.accountLock.counts()
			require.Equal(t, 1, acquireCalls)
			require.Equal(t, 1, releaseCalls)
			require.Zero(t, contentions)
			require.Equal(t, 1, fixture.accountLock.rowLockCount())
			_, _, resetCalls := fixture.quota.counts()
			require.Zero(t, resetCalls)
			require.Zero(t, fixture.recoverer.count())
			audits := fixture.auditRepo.snapshot()
			if testCase.wantAudit {
				require.Len(t, audits, 1)
				require.Equal(t, http.StatusConflict, audits[0].StatusCode)
			} else {
				require.Empty(t, audits)
			}

			record := findAutoResetExecutionRecord(t, fixture.idempotency.inMemoryIdempotencyRepo)
			require.Equal(t, IdempotencyStatusFailedRetryable, record.Status)
			require.Nil(t, record.ResponseBody)
			_, _, _, states := fixture.accountRepo.counts()
			require.Contains(t, states, OpenAIAutoResetStatusResetting)
			require.Equal(t, OpenAIAutoResetStatusFailed, states[len(states)-1])
			events, actorErrors := fixture.recorder.snapshot()
			require.Empty(t, actorErrors)
			require.Contains(t, events, "idempotency.failed")
			require.NotContains(t, events, "reset")
			require.NotContains(t, events, "atomic.finalize")
		})
	}
}

func TestOpenAIQuotaAutoResetRevocationAfterQueryStopsAllSubsequentEffects(t *testing.T) {
	now := time.Now().UTC()
	account := newAutoResetAuthzAccount(now, 331)
	fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))
	fixture.quota.setQueryHook(func(call int) {
		if call == 1 {
			fixture.store.setActive(false)
		}
	})

	err := fixture.service.evaluateAccount(context.Background(), account.ID)

	require.ErrorIs(t, err, authz.ErrActorInactive)
	queryCalls, cacheCalls, resetCalls := fixture.quota.counts()
	require.Equal(t, 1, queryCalls)
	require.Zero(t, cacheCalls)
	require.Zero(t, resetCalls)
	require.Zero(t, fixture.idempotency.createCount())
	require.Zero(t, fixture.recoverer.count())
	require.Empty(t, fixture.auditRepo.snapshot())
	_, updateCalls, _, states := fixture.accountRepo.counts()
	require.Equal(t, 1, updateCalls, "only the pre-query checking state may be written")
	require.Equal(t, []string{OpenAIAutoResetStatusChecking}, states)
	require.NotContains(t, states, OpenAIAutoResetStatusFailed)
	_, actorErrors := fixture.recorder.snapshot()
	require.Empty(t, actorErrors)
}

func TestOpenAIQuotaAutoResetReloadsRecheckAccountEligibility(t *testing.T) {
	now := time.Now().UTC()
	type mutationPoint int
	const (
		duringQuotaQuery mutationPoint = iota
		duringPostQueryReload
	)
	tests := []struct {
		name         string
		point        mutationPoint
		mutate       func(*Account)
		wantGetCalls int
	}{
		{
			name:  "config disabled during quota query",
			point: duringQuotaQuery,
			mutate: func(account *Account) {
				account.Extra[OpenAIAutoResetCreditEnabledExtraKey] = false
			},
			wantGetCalls: 2,
		},
		{
			name:  "account disabled during quota query",
			point: duringQuotaQuery,
			mutate: func(account *Account) {
				account.Status = StatusDisabled
			},
			wantGetCalls: 2,
		},
		{
			name:  "account unscheduled during quota query",
			point: duringQuotaQuery,
			mutate: func(account *Account) {
				account.Schedulable = false
			},
			wantGetCalls: 2,
		},
		{
			name:  "config disabled during post-query reload",
			point: duringPostQueryReload,
			mutate: func(account *Account) {
				account.Extra[OpenAIAutoResetCreditEnabledExtraKey] = false
			},
			wantGetCalls: 3,
		},
		{
			name:  "account disabled during post-query reload",
			point: duringPostQueryReload,
			mutate: func(account *Account) {
				account.Status = StatusDisabled
			},
			wantGetCalls: 3,
		},
		{
			name:  "account unscheduled during post-query reload",
			point: duringPostQueryReload,
			mutate: func(account *Account) {
				account.Schedulable = false
			},
			wantGetCalls: 3,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			account := newAutoResetAuthzAccount(now, 335)
			fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))
			mutateAccount := func() {
				fixture.accountRepo.mutateAccount(testCase.mutate)
			}
			switch testCase.point {
			case duringQuotaQuery:
				fixture.quota.setQueryHook(func(call int) {
					if call == 1 {
						mutateAccount()
					}
				})
			case duringPostQueryReload:
				fixture.accountRepo.setGetHook(func(call int) {
					if call == 2 {
						mutateAccount()
					}
				})
			}

			err := fixture.service.evaluateAccount(context.Background(), account.ID)

			require.NoError(t, err)
			getCalls, _, _, states := fixture.accountRepo.counts()
			require.Equal(t, testCase.wantGetCalls, getCalls)
			require.Equal(t, []string{OpenAIAutoResetStatusChecking}, states)
			queryCalls, cacheCalls, resetCalls := fixture.quota.counts()
			require.Equal(t, 1, queryCalls)
			require.Equal(t, 1, cacheCalls)
			require.Zero(t, resetCalls)
			require.Zero(t, fixture.idempotency.createCount())
			require.Zero(t, fixture.recoverer.count())
			require.Empty(t, fixture.auditRepo.snapshot())
			events, actorErrors := fixture.recorder.snapshot()
			require.Empty(t, actorErrors)
			for _, event := range events {
				require.False(t, strings.HasPrefix(event, "idempotency."))
				require.NotEqual(t, "reset", event)
				require.NotEqual(t, "audit", event)
			}
		})
	}
}

func TestOpenAIQuotaAutoResetPostResetRevocationFinalizesAndRecoversWithoutSecondReset(t *testing.T) {
	now := time.Now().UTC()
	account := newAutoResetAuthzAccount(now, 332)
	usage := newAutoResetAuthzUsage(now)
	fixture := newAutoResetAuthzFixture(t, account, usage)
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
	entries := fixture.auditRepo.snapshot()
	require.Len(t, entries, 1)
	require.Equal(t, http.StatusConflict, entries[0].StatusCode)
	require.Equal(t, "recovery_deferred", entries[0].Extra["result_code"])
	require.Equal(t, fixture.quota.lastRequestID(), entries[0].RequestID)

	scope, keyHash, _ := autoResetMigratedRecordIdentity(t, fixture.actor, account.ID, usage)
	record, recordErr := fixture.idempotency.inMemoryIdempotencyRepo.GetByScopeAndKeyHash(context.Background(), scope, keyHash)
	require.NoError(t, recordErr)
	require.NotNil(t, record)
	require.Equal(t, IdempotencyStatusSucceeded, record.Status)
	fixture.accountRepo.mu.Lock()
	stateAfterRevocation := openAIAutoResetStateFromExtra(fixture.accountRepo.account.Extra)
	fixture.accountRepo.mu.Unlock()
	require.NotNil(t, stateAfterRevocation)
	require.Equal(t, OpenAIAutoResetStatusResetting, stateAfterRevocation.Status)

	changedUsage := newAutoResetAuthzUsage(now.Add(2 * time.Hour))
	changedUsage.RateLimit.PrimaryWindow.ResetAt = now.Add(3 * time.Hour).Unix()
	changedUsage.RateLimit.SecondaryWindow.ResetAt = now.Add(8 * 24 * time.Hour).Unix()
	changedUsage.RateLimitResetCredits.AvailableCount = 0
	changedUsage.RateLimitResetCredits.Credits = nil
	changedUsage.autoResetCandidates = nil
	fixture.quota.setUsage(changedUsage)
	fixture.quota.setResetHook(nil)
	fixture.store.setWorkerPermissions([]string{string(authz.CapabilityPlatformAccountOpenAIQuotaAutoReset)})
	fixture.recorder.reset()

	require.NoError(t, fixture.service.evaluateAccount(context.Background(), account.ID))

	_, _, resetCalls = fixture.quota.counts()
	require.Equal(t, 1, resetCalls, "restored authorization may only converge local recovery")
	require.Equal(t, 1, fixture.recoverer.count())
	require.Len(t, fixture.auditRepo.snapshot(), 1, "recovery replay must not duplicate the reset audit")
	events, actorErrors := fixture.recorder.snapshot()
	require.Empty(t, actorErrors)
	require.NotContains(t, events, "reset")
	require.NotContains(t, events, "atomic.finalize")
	fixture.accountRepo.mu.Lock()
	recoveredState := openAIAutoResetStateFromExtra(fixture.accountRepo.account.Extra)
	fixture.accountRepo.mu.Unlock()
	require.NotNil(t, recoveredState)
	require.Equal(t, OpenAIAutoResetStatusSuccess, recoveredState.Status)
	require.Zero(t, recoveredState.AvailableCount)
}

func TestOpenAIQuotaAutoResetPreResetRevocationSafelyReleasesClaimForRetry(t *testing.T) {
	now := time.Now().UTC()
	account := newAutoResetAuthzAccount(now, 333)
	usage := newAutoResetAuthzUsage(now)
	fixture := newAutoResetAuthzFixture(t, account, usage)
	fixture.idempotency.setCreateHook(func(call int, _ *IdempotencyRecord) {
		if call == 2 {
			fixture.store.setWorkerPermissions(nil)
		}
	})

	err := fixture.service.evaluateAccount(context.Background(), account.ID)

	require.ErrorIs(t, err, authz.ErrPolicyAccessDenied)
	_, _, resetCalls := fixture.quota.counts()
	require.Zero(t, resetCalls)
	require.Empty(t, fixture.auditRepo.snapshot())
	scope, keyHash, _ := autoResetMigratedRecordIdentity(t, fixture.actor, account.ID, usage)
	record, recordErr := fixture.idempotency.inMemoryIdempotencyRepo.GetByScopeAndKeyHash(context.Background(), scope, keyHash)
	require.NoError(t, recordErr)
	require.NotNil(t, record)
	require.Equal(t, IdempotencyStatusFailedRetryable, record.Status)

	fixture.idempotency.setCreateHook(nil)
	fixture.store.setWorkerPermissions([]string{string(authz.CapabilityPlatformAccountOpenAIQuotaAutoReset)})
	require.NoError(t, fixture.service.evaluateAccount(context.Background(), account.ID))

	_, _, resetCalls = fixture.quota.counts()
	require.Equal(t, 1, resetCalls)
	require.Equal(t, 1, fixture.recoverer.count())
	require.Len(t, fixture.auditRepo.snapshot(), 1)
	record, recordErr = fixture.idempotency.inMemoryIdempotencyRepo.GetByScopeAndKeyHash(context.Background(), scope, keyHash)
	require.NoError(t, recordErr)
	require.Equal(t, IdempotencyStatusSucceeded, record.Status)
}

func TestOpenAIQuotaAutoResetDoesNotPublishResettingBeforeDurableClaim(t *testing.T) {
	now := time.Now().UTC()
	account := newAutoResetAuthzAccount(now, 334)
	fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))
	fixture.accountRepo.setGetHook(func(call int) {
		if call == 3 {
			fixture.store.setWorkerPermissions(nil)
		}
	})

	err := fixture.service.evaluateAccount(context.Background(), account.ID)

	require.ErrorIs(t, err, authz.ErrPolicyAccessDenied)
	_, _, resetCalls := fixture.quota.counts()
	require.Zero(t, resetCalls)
	require.Zero(t, fixture.idempotency.createCount())
	require.Empty(t, fixture.auditRepo.snapshot())
	_, _, _, states := fixture.accountRepo.counts()
	require.NotContains(t, states, OpenAIAutoResetStatusResetting)
	require.Equal(t, []string{OpenAIAutoResetStatusChecking}, states)
}

func TestOpenAIQuotaAutoResetIdempotencyRepositoryReauthorizesEveryOperation(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name   string
		invoke func(IdempotencyRepository) error
	}{
		{
			name: "create processing",
			invoke: func(repo IdempotencyRepository) error {
				_, err := repo.CreateProcessing(context.Background(), &IdempotencyRecord{})
				return err
			},
		},
		{
			name: "get",
			invoke: func(repo IdempotencyRepository) error {
				_, err := repo.GetByScopeAndKeyHash(context.Background(), "scope", "hash")
				return err
			},
		},
		{
			name: "extend expiration",
			invoke: func(repo IdempotencyRepository) error {
				_, err := repo.ExtendExpiration(context.Background(), 1, "fingerprint", now.Add(time.Hour))
				return err
			},
		},
		{
			name: "reclaim",
			invoke: func(repo IdempotencyRepository) error {
				_, err := repo.TryReclaim(context.Background(), 1, IdempotencyStatusFailedRetryable, now, now.Add(time.Minute), now.Add(time.Hour))
				return err
			},
		},
		{
			name: "extend processing lock",
			invoke: func(repo IdempotencyRepository) error {
				_, err := repo.ExtendProcessingLock(context.Background(), 1, "fingerprint", now.Add(time.Minute), now.Add(time.Hour))
				return err
			},
		},
		{
			name: "mark succeeded",
			invoke: func(repo IdempotencyRepository) error {
				return repo.MarkSucceeded(context.Background(), 1, http.StatusOK, `{}`, now.Add(time.Hour))
			},
		},
		{
			name: "mark failed",
			invoke: func(repo IdempotencyRepository) error {
				return repo.MarkFailedRetryable(context.Background(), 1, "failed", now.Add(time.Minute), now.Add(time.Hour))
			},
		},
		{
			name: "delete expired",
			invoke: func(repo IdempotencyRepository) error {
				_, err := repo.DeleteExpired(context.Background(), now, 10)
				return err
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			account := newAutoResetAuthzAccount(now, 332)
			fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))
			guarded := &openAIAutoResetAuthorizedIdempotencyRepository{
				delegate:  fixture.idempotency,
				service:   fixture.service,
				actor:     fixture.actor,
				accountID: account.ID,
			}
			fixture.store.setActive(false)
			fixture.recorder.reset()

			err := testCase.invoke(guarded)

			require.ErrorIs(t, err, authz.ErrActorInactive)
			events, actorErrors := fixture.recorder.snapshot()
			require.Empty(t, actorErrors)
			for _, event := range events {
				require.NotContains(t, event, "idempotency.", "revoked calls must not reach the delegate")
			}
			require.Zero(t, fixture.idempotency.createCount())
		})
	}
}

func TestOpenAIQuotaAutoResetIdempotencyRepositoryOnlyReclaimsRetryableFailures(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name         string
		fromStatus   string
		wantDelegate bool
	}{
		{name: "processing", fromStatus: IdempotencyStatusProcessing},
		{name: "succeeded", fromStatus: IdempotencyStatusSucceeded},
		{name: "failed retryable", fromStatus: IdempotencyStatusFailedRetryable, wantDelegate: true},
		{name: "unknown", fromStatus: "unknown"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			account := newAutoResetAuthzAccount(now, 333)
			fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))
			guarded := &openAIAutoResetAuthorizedIdempotencyRepository{
				delegate:  fixture.idempotency,
				service:   fixture.service,
				actor:     fixture.actor,
				accountID: account.ID,
			}
			fixture.recorder.reset()

			taken, err := guarded.TryReclaim(
				context.Background(),
				1,
				testCase.fromStatus,
				now,
				now.Add(time.Minute),
				now.Add(time.Hour),
			)

			require.NoError(t, err)
			require.False(t, taken)
			events, actorErrors := fixture.recorder.snapshot()
			require.Empty(t, actorErrors)
			require.NotEmpty(t, events)
			require.Equal(t, fmt.Sprintf("authorize:%d", account.ID), events[0], "reauthorization must run before status filtering")
			if testCase.wantDelegate {
				require.Equal(t, []string{fmt.Sprintf("authorize:%d", account.ID), "idempotency.reclaim"}, events)
			} else {
				require.Equal(t, []string{fmt.Sprintf("authorize:%d", account.ID)}, events)
			}
		})
	}
}

func TestOpenAIQuotaAutoResetCarriesActorAndWritesDurableServicePrincipalAudit(t *testing.T) {
	now := time.Now().UTC()
	account := newAutoResetAuthzAccount(now, 341)
	fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))

	err := fixture.service.evaluateAccount(context.Background(), account.ID)

	require.NoError(t, err)
	events, actorErrors := fixture.recorder.snapshot()
	require.Empty(t, actorErrors)
	for _, requiredEvent := range []string{
		"get",
		"update",
		"query",
		"cache",
		"idempotency.create",
		"reset",
		"recover",
		"atomic.finalize",
		"idempotency.succeeded",
	} {
		require.Contains(t, events, requiredEvent)
	}
	require.GreaterOrEqual(t, countAutoResetEvent(events, "get"), 3)
	require.GreaterOrEqual(t, countAutoResetEvent(events, "query"), 2)
	require.GreaterOrEqual(t, countAutoResetEvent(events, "cache"), 2)
	require.Less(t, indexAutoResetEvent(events, "reset"), indexAutoResetEvent(events, "recover"))
	require.Less(t, indexAutoResetEvent(events, "recover"), indexAutoResetEvent(events, "atomic.finalize"))
	require.Less(t, indexAutoResetEvent(events, "atomic.finalize"), indexAutoResetEvent(events, "idempotency.succeeded"))

	entries := fixture.auditRepo.snapshot()
	require.Len(t, entries, 1)
	entry := entries[0]
	require.NotNil(t, entry.ActorServicePrincipalID)
	require.Equal(t, autoResetAuthzTestPrincipalID, *entry.ActorServicePrincipalID)
	require.Nil(t, entry.ActorUserID)
	require.Equal(t, AuditAuthMethodServicePrincipal, entry.AuthMethod)
	require.Equal(t, AuditActionOpenAIQuotaAutoReset, entry.Action)
	require.Equal(t, "SYSTEM", entry.Method)
	require.Equal(t, fmt.Sprintf("/system/openai/accounts/%d/auto-reset-credit", account.ID), entry.Path)
	require.NotEmpty(t, entry.RequestID)
	require.Equal(t, fixture.quota.lastRequestID(), entry.RequestID)
	require.Equal(t, http.StatusOK, entry.StatusCode)
	require.EqualValues(t, account.ID, entry.Extra["account_id"])
}

func TestOpenAIQuotaAutoResetPropagatesDurableAuditFailure(t *testing.T) {
	now := time.Now().UTC()
	account := newAutoResetAuthzAccount(now, 351)
	usage := newAutoResetAuthzUsage(now)
	fixture := newAutoResetAuthzFixture(t, account, usage)
	auditErr := errors.New("durable audit repository failed")
	fixture.auditRepo.setError(auditErr)

	err := fixture.service.evaluateAccount(context.Background(), account.ID)

	require.ErrorIs(t, err, errOpenAIAutoResetFinalization)
	require.ErrorIs(t, err, auditErr)
	_, _, resetCalls := fixture.quota.counts()
	require.Equal(t, 1, resetCalls)
	require.Equal(t, 1, fixture.recoverer.count())
	require.Empty(t, fixture.auditRepo.snapshot())
	_, _, _, states := fixture.accountRepo.counts()
	require.NotContains(t, states, OpenAIAutoResetStatusFailed)
	events, actorErrors := fixture.recorder.snapshot()
	require.Empty(t, actorErrors)
	for _, requiredEvent := range []string{"reset", "recover", "atomic.finalize"} {
		require.Contains(t, events, requiredEvent)
	}
	require.Less(t, indexAutoResetEvent(events, "reset"), indexAutoResetEvent(events, "recover"))
	require.Less(t, indexAutoResetEvent(events, "recover"), indexAutoResetEvent(events, "atomic.finalize"))
	require.NotContains(t, events, "idempotency.failed")
	require.NotContains(t, events, "idempotency.succeeded")

	scope, keyHash, _ := autoResetMigratedRecordIdentity(t, fixture.actor, account.ID, usage)
	record, recordErr := fixture.idempotency.inMemoryIdempotencyRepo.GetByScopeAndKeyHash(context.Background(), scope, keyHash)
	require.NoError(t, recordErr)
	require.NotNil(t, record)
	require.Equal(t, IdempotencyStatusProcessing, record.Status)

	require.NoError(t, fixture.service.evaluateAccount(context.Background(), account.ID))
	_, _, resetCalls = fixture.quota.counts()
	require.Equal(t, 1, resetCalls, "a failed terminal transaction must never cause a second reset")
	require.Empty(t, fixture.auditRepo.snapshot())
}

func TestOpenAIQuotaAutoResetRejectsAmbiguousSuccessfulResetCodesWithoutFinalizing(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name string
		code string
	}{
		{name: "empty code", code: ""},
		{name: "unknown code", code: "pending"},
	}

	for index, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			account := newAutoResetAuthzAccount(now, int64(352+index))
			fixture := newAutoResetAuthzFixture(t, account, newAutoResetAuthzUsage(now))
			fixture.quota.setResetResult(&OpenAIQuotaResetResult{Code: testCase.code})

			err := fixture.service.evaluateAccount(context.Background(), account.ID)

			require.Error(t, err)
			require.True(t,
				errors.Is(err, ErrOpenAIAutoResetReconciliationRequired) ||
					errors.Is(err, errOpenAIAutoResetExternalEffectUnfinalized),
				"an ambiguous result after reset must require reconciliation: %v", err,
			)
			_, _, resetCalls := fixture.quota.counts()
			require.Equal(t, 1, resetCalls, "the upstream external effect must have started exactly once")
			require.Zero(t, fixture.recoverer.count())
			require.Empty(t, fixture.auditRepo.snapshot(), "an ambiguous result must not write a success audit")

			events, actorErrors := fixture.recorder.snapshot()
			require.Empty(t, actorErrors)
			require.Contains(t, events, "reset")
			require.NotContains(t, events, "recover")
			require.NotContains(t, events, "atomic.finalize")
			require.NotContains(t, events, "idempotency.succeeded")
			require.NotContains(t, events, "idempotency.failed")

			record := findAutoResetExecutionRecord(t, fixture.idempotency.inMemoryIdempotencyRepo)
			require.Equal(t, IdempotencyStatusProcessing, record.Status)
			require.Nil(t, record.ResponseStatus)
			require.Nil(t, record.ResponseBody)
			require.Nil(t, record.ErrorReason)

			fixture.idempotency.inMemoryIdempotencyRepo.mu.Lock()
			for _, stored := range fixture.idempotency.inMemoryIdempotencyRepo.data {
				if stored.ID == record.ID {
					past := time.Now().Add(-time.Minute)
					stored.LockedUntil = &past
				}
			}
			fixture.idempotency.inMemoryIdempotencyRepo.mu.Unlock()

			err = fixture.service.evaluateAccount(context.Background(), account.ID)
			require.ErrorIs(t, err, ErrOpenAIAutoResetReconciliationRequired)
			_, _, resetCalls = fixture.quota.counts()
			require.Equal(t, 1, resetCalls, "reconciliation must never issue a second reset")
			require.Zero(t, fixture.recoverer.count())
			require.Empty(t, fixture.auditRepo.snapshot())
		})
	}
}

func TestOpenAIQuotaAutoResetReplaysMigratedLegacyFingerprintWithoutReset(t *testing.T) {
	now := time.Now().UTC()
	tests := []struct {
		name    string
		status  string
		expired bool
	}{
		{name: "succeeded", status: IdempotencyStatusSucceeded},
		{name: "expired succeeded", status: IdempotencyStatusSucceeded, expired: true},
		{name: "processing", status: IdempotencyStatusProcessing},
		{name: "expired processing", status: IdempotencyStatusProcessing, expired: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			account := newAutoResetAuthzAccount(now, 361)
			usage := newAutoResetAuthzUsage(now)
			fixture := newAutoResetAuthzFixture(t, account, usage)
			scope, keyHash, legacyFingerprint := autoResetMigratedRecordIdentity(t, fixture.actor, account.ID, usage)
			currentFingerprint, err := BuildIdempotencyFingerprint(
				http.MethodPost,
				"/system/openai/reset-credit/auto",
				fmt.Sprintf("service_principal:%d", autoResetAuthzTestPrincipalID),
				autoResetIdempotencyPayload(account.ID, usage),
			)
			require.NoError(t, err)
			require.NotEqual(t, currentFingerprint, legacyFingerprint)
			require.Equal(t, BuildActorQualifiedIdempotencyScope(
				"openai_auto_reset_credit",
				fmt.Sprintf("service_principal:%d", autoResetAuthzTestPrincipalID),
			), scope)

			record := &IdempotencyRecord{
				ID:                 90,
				Scope:              scope,
				IdempotencyKeyHash: keyHash,
				RequestFingerprint: legacyFingerprint,
				Status:             testCase.status,
				ExpiresAt:          now.Add(24 * time.Hour),
				CreatedAt:          now.Add(-time.Hour),
				UpdatedAt:          now.Add(-time.Hour),
			}
			if testCase.expired {
				record.ExpiresAt = now.Add(-time.Minute)
			}
			if testCase.status == IdempotencyStatusSucceeded {
				status := http.StatusOK
				body := `{"code":"ok","windows_reset":2}`
				record.ResponseStatus = &status
				record.ResponseBody = &body
			} else {
				lockedUntil := now.Add(time.Hour)
				if testCase.expired {
					lockedUntil = now.Add(-time.Minute)
				}
				record.LockedUntil = &lockedUntil
			}
			seedAutoResetIdempotencyRecord(fixture.idempotency.inMemoryIdempotencyRepo, record)

			err = fixture.service.evaluateAccount(context.Background(), account.ID)

			require.NoError(t, err)
			_, actorErrors := fixture.recorder.snapshot()
			require.Empty(t, actorErrors)
			_, _, resetCalls := fixture.quota.counts()
			require.Zero(t, resetCalls, "migrated records must never consume the same credit again")
			require.Empty(t, fixture.auditRepo.snapshot(), "replay or in-progress conflict must not emit a second reset audit")
			if testCase.status == IdempotencyStatusSucceeded {
				require.Equal(t, 1, fixture.recoverer.count())
			} else {
				require.Zero(t, fixture.recoverer.count())
			}
		})
	}
}

func currentOpenAIAutoResetNotifierForTest() *OpenAIQuotaAutoResetService {
	openAIAutoResetNotifierRegistry.RLock()
	defer openAIAutoResetNotifierRegistry.RUnlock()
	return openAIAutoResetNotifierRegistry.service
}

func countAutoResetEvent(events []string, want string) int {
	count := 0
	for _, event := range events {
		if event == want {
			count++
		}
	}
	return count
}

func indexAutoResetEvent(events []string, want string) int {
	for index, event := range events {
		if event == want {
			return index
		}
	}
	return len(events)
}

func newAutoResetAuthzAccount(now time.Time, accountID int64) *Account {
	return &Account{
		ID:          accountID,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Status:      StatusActive,
		Schedulable: true,
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
}

func newAutoResetAuthzUsage(now time.Time) *OpenAIQuotaUsage {
	expiresAt := now.Add(48 * time.Hour).Format(time.RFC3339)
	return &OpenAIQuotaUsage{
		FetchedAt: now.Unix(),
		RateLimit: &OpenAIRateLimit{
			PrimaryWindow: &OpenAIRateLimitWindow{
				UsedPercent:        100,
				LimitWindowSeconds: 5 * 60 * 60,
				ResetAfterSeconds:  3600,
				ResetAt:            now.Add(time.Hour).Unix(),
			},
			SecondaryWindow: &OpenAIRateLimitWindow{
				UsedPercent:        10,
				LimitWindowSeconds: 7 * 24 * 60 * 60,
				ResetAfterSeconds:  86400,
				ResetAt:            now.Add(24 * time.Hour).Unix(),
			},
		},
		RateLimitResetCredits: &OpenAIRateLimitResetCredits{
			AvailableCount: 1,
			Credits:        []OpenAIRateLimitResetCreditDetail{{ExpiresAt: expiresAt}},
		},
		autoResetCandidates: []openAIAutoResetCreditCandidate{{
			ID:        "authz-test-credit",
			ExpiresAt: expiresAt,
		}},
	}
}

func autoResetMigratedRecordIdentity(
	t testing.TB,
	actor authz.Actor,
	accountID int64,
	usage *OpenAIQuotaUsage,
) (scope, keyHash, legacyFingerprint string) {
	t.Helper()
	actorScope, ok := actor.SubjectKey()
	if !ok {
		t.Fatal("worker actor has no durable subject key")
	}
	cycleHash := shortOpenAIAutoResetHash(openAIAutoResetCycleSeed(usage))
	creditHash := shortOpenAIAutoResetHash(usage.autoResetCandidates[0].ID)
	stableKey := fmt.Sprintf("oarc:%d:%s:%s", accountID, creditHash, cycleHash)
	payload := map[string]any{
		"account_id":  accountID,
		"credit_hash": creditHash,
		"cycle_hash":  cycleHash,
	}
	fingerprint, err := BuildIdempotencyFingerprint(
		http.MethodPost,
		"/system/openai/reset-credit/auto",
		fmt.Sprintf("account:%d", accountID),
		payload,
	)
	if err != nil {
		t.Fatalf("build migrated legacy fingerprint: %v", err)
	}
	return BuildActorQualifiedIdempotencyScope("openai_auto_reset_credit", actorScope), HashIdempotencyKey(stableKey), fingerprint
}

func autoResetIdempotencyPayload(accountID int64, usage *OpenAIQuotaUsage) map[string]any {
	cycleHash := shortOpenAIAutoResetHash(openAIAutoResetCycleSeed(usage))
	return map[string]any{
		"account_id":  accountID,
		"credit_hash": shortOpenAIAutoResetHash(usage.autoResetCandidates[0].ID),
		"cycle_hash":  cycleHash,
	}
}

func seedAutoResetIdempotencyRecord(repo *inMemoryIdempotencyRepo, record *IdempotencyRecord) {
	repo.mu.Lock()
	defer repo.mu.Unlock()
	repo.data[repo.key(record.Scope, record.IdempotencyKeyHash)] = cloneRecord(record)
	if repo.nextID <= record.ID {
		repo.nextID = record.ID + 1
	}
}
