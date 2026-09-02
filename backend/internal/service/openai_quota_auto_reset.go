package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/google/uuid"
)

const (
	openAIAutoResetScanInterval  = time.Minute
	openAIAutoResetSnapshotTTL   = openAIProbeCacheTTL
	openAIAutoResetBatchSize     = 100
	openAIAutoResetWorkerCount   = 4
	openAIAutoResetQueueCapacity = 1024
	openAIAutoResetAttemptTTL    = 8 * 24 * time.Hour
	openAIAutoResetLeaderLockKey = "jobs:openai-auto-reset-credit"
)

var (
	errOpenAIAutoResetAudit        = errors.New("OpenAI quota auto-reset durable audit failed")
	errOpenAIAutoResetFinalization = errors.New("OpenAI quota auto-reset terminal finalization failed")
	errOpenAIAutoResetLeaseMissing = errors.New("OpenAI quota auto-reset account lock reported acquisition with a nil lease")
)

const (
	OpenAIAutoResetStatusChecking  = "checking"
	OpenAIAutoResetStatusAvailable = "available"
	OpenAIAutoResetStatusResetting = "resetting"
	OpenAIAutoResetStatusSuccess   = "success"
	OpenAIAutoResetStatusNoCredit  = "no_credit"
	OpenAIAutoResetStatusFailed    = "failed"
)

// OpenAIAutoResetCreditState 是可返回管理端的脱敏运行态。Attempt* 仅保存不可逆
// 指纹，用于重启后拒绝切换到另一张卡；不会保存卡 ID 或兑换 ID。
type OpenAIAutoResetCreditState struct {
	Status            string `json:"status"`
	TriggerWindow     string `json:"trigger_window,omitempty"`
	AvailableCount    int    `json:"available_count"`
	CheckedAt         string `json:"checked_at,omitempty"`
	LastResultAt      string `json:"last_result_at,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
	AttemptCycleHash  string `json:"attempt_cycle_hash,omitempty"`
	AttemptCreditHash string `json:"attempt_credit_hash,omitempty"`
}

type openAIAutoResetQuota interface {
	QueryUsage(ctx context.Context, accountID int64) (*OpenAIQuotaUsage, error)
	CacheResetCreditsSnapshot(ctx context.Context, accountID int64, credits *OpenAIRateLimitResetCredits) error
	resetCreditTargetedGuarded(ctx context.Context, accountID int64, creditID, redeemRequestID string, guard openAIAutoResetExternalEffectGuard) (*OpenAIQuotaResetResult, error)
}

type openAIAutoResetContextKey struct{}

func withOpenAIAutoResetContext(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAIAutoResetContextKey{}, true)
}

func isOpenAIAutoResetContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	value, _ := ctx.Value(openAIAutoResetContextKey{}).(bool)
	return value
}

type openAIAutoResetRecovery interface {
	RecoverAccountState(ctx context.Context, accountID int64, options AccountRecoveryOptions) (*SuccessfulTestRecoveryResult, error)
}

// OpenAIQuotaAutoResetService 通过小型去重队列承接实时信号，并用分钟扫描补偿
// 重启、漏事件和多实例读取；真正消费仍由 PostgreSQL 幂等记录串行化。
type OpenAIQuotaAutoResetService struct {
	accountRepo AccountRepository
	quota       openAIAutoResetQuota
	recoverer   openAIAutoResetRecovery
	idempotency *IdempotencyCoordinator
	finalizer   OpenAIQuotaAutoResetFinalizer
	accountLock OpenAIQuotaAutoResetAccountLocker
	audit       *AuditLogService
	settings    *SettingService
	leaderLock  LeaderLockCache
	resolver    authz.Resolver
	policy      authz.WorkerPolicy

	ctx     context.Context
	cancel  context.CancelFunc
	queue   chan int64
	pending sync.Map
	owner   string
	start   sync.Once
	stop    sync.Once
	wg      sync.WaitGroup
}

func NewOpenAIQuotaAutoResetService(
	accountRepo AccountRepository,
	quota openAIAutoResetQuota,
	recoverer openAIAutoResetRecovery,
	idempotency *IdempotencyCoordinator,
	finalizer OpenAIQuotaAutoResetFinalizer,
	accountLock OpenAIQuotaAutoResetAccountLocker,
	audit *AuditLogService,
	settings *SettingService,
	leaderLock LeaderLockCache,
	resolver authz.Resolver,
	policy authz.WorkerPolicy,
) *OpenAIQuotaAutoResetService {
	ctx, cancel := context.WithCancel(context.Background())
	return &OpenAIQuotaAutoResetService{
		accountRepo: accountRepo,
		quota:       quota,
		recoverer:   recoverer,
		idempotency: idempotency,
		finalizer:   finalizer,
		accountLock: accountLock,
		audit:       audit,
		settings:    settings,
		leaderLock:  leaderLock,
		resolver:    resolver,
		policy:      policy,
		ctx:         ctx,
		cancel:      cancel,
		queue:       make(chan int64, openAIAutoResetQueueCapacity),
		owner:       uuid.NewString(),
	}
}

func (s *OpenAIQuotaAutoResetService) Start() error {
	if err := s.validateWorkerDependencies(); err != nil {
		return err
	}
	if _, ok := s.accountRepo.(OpenAIAutoResetRecoveryCandidatePager); !ok {
		return fmt.Errorf("%w: OpenAI quota auto-reset recovery candidate pager is not configured", authz.ErrAuthorizationUnavailable)
	}
	workerCtx, _, err := s.resolveWorkerContext(s.ctx, 0)
	if err != nil {
		return err
	}
	s.start.Do(func() {
		s.ctx = workerCtx
		setOpenAIAutoResetNotifier(s)
		for range openAIAutoResetWorkerCount {
			s.wg.Add(1)
			go s.runWorker()
		}
		s.wg.Add(1)
		go s.runScanner()
	})
	return nil
}

func (s *OpenAIQuotaAutoResetService) validateWorkerDependencies() error {
	if s == nil {
		return fmt.Errorf("%w: OpenAI quota auto-reset service is nil", authz.ErrAuthorizationUnavailable)
	}
	if s.accountRepo == nil || s.quota == nil || s.recoverer == nil || s.idempotency == nil || s.idempotency.repo == nil || s.finalizer == nil || s.accountLock == nil || s.audit == nil || s.audit.repo == nil {
		return fmt.Errorf("%w: OpenAI quota auto-reset dependencies are incomplete", authz.ErrAuthorizationUnavailable)
	}
	if s.resolver == nil || s.policy == nil {
		return fmt.Errorf("%w: OpenAI quota auto-reset authorization is not configured", authz.ErrAuthorizationUnavailable)
	}
	return nil
}

func (s *OpenAIQuotaAutoResetService) resolveWorkerContext(ctx context.Context, accountID int64) (context.Context, authz.Actor, error) {
	if err := s.validateWorkerDependencies(); err != nil {
		return ctx, authz.Actor{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	actor, err := s.resolver.ResolveServicePrincipal(
		ctx,
		authz.OpenAIQuotaAutoResetServicePrincipalCode,
		authz.AuthMethodServicePrincipal,
	)
	if err != nil {
		return ctx, authz.Actor{}, fmt.Errorf("resolve OpenAI quota auto-reset worker: %w", err)
	}
	ctx = authz.ContextWithActor(ctx, actor)
	if err := s.authorizeWorkerActor(ctx, actor, accountID); err != nil {
		return ctx, authz.Actor{}, err
	}
	return ctx, actor, nil
}

func (s *OpenAIQuotaAutoResetService) reauthorizeWorkerContext(
	ctx context.Context,
	expected authz.Actor,
	accountID int64,
) (context.Context, error) {
	if !expected.Valid() {
		return ctx, authz.ErrInvalidActor
	}
	if ctx == nil {
		ctx = context.Background()
	}
	current, err := s.resolver.ResolveServicePrincipal(
		ctx,
		authz.OpenAIQuotaAutoResetServicePrincipalCode,
		authz.AuthMethodServicePrincipal,
	)
	if err != nil {
		return ctx, fmt.Errorf("re-resolve OpenAI quota auto-reset worker: %w", err)
	}
	if !expected.SameAuthorizationState(current) {
		return ctx, authz.ErrSessionInvalid
	}
	ctx = authz.ContextWithActor(ctx, current)
	if err := s.authorizeWorkerActor(ctx, current, accountID); err != nil {
		return ctx, err
	}
	return ctx, nil
}

func (s *OpenAIQuotaAutoResetService) authorizeWorkerActor(ctx context.Context, actor authz.Actor, accountID int64) error {
	if accountID == 0 {
		if err := s.policy.CheckWorkerCapability(ctx, actor, authz.CapabilityPlatformAccountOpenAIQuotaAutoReset); err != nil {
			return fmt.Errorf("authorize OpenAI quota auto-reset worker capability: %w", err)
		}
		return nil
	}
	ref, err := authz.NewResourceRef(authz.ResourceTypeAccount, accountID)
	if err != nil {
		return err
	}
	if err := s.policy.AuthorizeWorker(
		ctx,
		actor,
		authz.CapabilityPlatformAccountOpenAIQuotaAutoReset,
		authz.ActionAccountOperate,
		ref,
	); err != nil {
		return fmt.Errorf("authorize OpenAI quota auto-reset account %d: %w", accountID, err)
	}
	return nil
}

func (s *OpenAIQuotaAutoResetService) Stop() {
	if s == nil {
		return
	}
	s.stop.Do(func() {
		clearOpenAIAutoResetNotifier(s)
		s.cancel()
		s.wg.Wait()
	})
}

// Notify 是请求热路径的非阻塞入口。同一账号尚在队列时只保留一个任务；队列
// 满时丢弃本次信号，分钟扫描仍会补偿，因此不会反向拖慢网关请求。
func (s *OpenAIQuotaAutoResetService) Notify(accountID int64) {
	if s == nil || accountID <= 0 {
		return
	}
	if _, loaded := s.pending.LoadOrStore(accountID, struct{}{}); loaded {
		return
	}
	select {
	case <-s.ctx.Done():
		s.pending.Delete(accountID)
	case s.queue <- accountID:
	default:
		s.pending.Delete(accountID)
		slog.Warn("openai_auto_reset_queue_full", "account_id", accountID)
	}
}

// enqueueOpenAIAutoResetRecoveryCandidate is used only by the background
// recovery scanner. Unlike the request-path notifier, it waits for bounded
// queue capacity so a persistent low-ID reconciliation failure cannot starve
// later recovery candidates on every scan.
func (s *OpenAIQuotaAutoResetService) enqueueOpenAIAutoResetRecoveryCandidate(
	ctx context.Context,
	accountID int64,
) bool {
	if s == nil || accountID <= 0 {
		return true
	}
	if _, loaded := s.pending.LoadOrStore(accountID, struct{}{}); loaded {
		return true
	}
	select {
	case <-ctx.Done():
		s.pending.Delete(accountID)
		return false
	case <-s.ctx.Done():
		s.pending.Delete(accountID)
		return false
	case s.queue <- accountID:
		return true
	}
}

func (s *OpenAIQuotaAutoResetService) runWorker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case accountID := <-s.queue:
			ctx, cancel := context.WithTimeout(s.ctx, 50*time.Second)
			if err := s.evaluateAccount(ctx, accountID); err != nil && !errors.Is(err, context.Canceled) {
				slog.Warn("openai_auto_reset_evaluate_failed", "account_id", accountID, "error_code", infraerrors.Reason(err))
			}
			cancel()
			s.pending.Delete(accountID)
		}
	}
}

func (s *OpenAIQuotaAutoResetService) runScanner() {
	defer s.wg.Done()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-s.ctx.Done():
		return
	case <-timer.C:
		s.scanEnabledAccounts(s.ctx)
	}
	ticker := time.NewTicker(openAIAutoResetScanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			s.scanEnabledAccounts(s.ctx)
		}
	}
}

func (s *OpenAIQuotaAutoResetService) scanEnabledAccounts(ctx context.Context) {
	ctx, actor, err := s.resolveWorkerContext(ctx, 0)
	if err != nil {
		slog.Warn("openai_auto_reset_scan_unauthorized", "error", err)
		return
	}
	release, scan, err := s.tryAcquireScanLock(ctx, actor)
	if err != nil {
		slog.Warn("openai_auto_reset_scan_unauthorized", "error", err)
		return
	}
	if !scan {
		return
	}
	if release != nil {
		defer release()
	}
	if !s.scanOpenAIAutoResetRecoveryCandidates(ctx, actor) {
		return
	}
	for page := 1; ; page++ {
		pageCtx, err := s.reauthorizeWorkerContext(ctx, actor, 0)
		if err != nil {
			slog.Warn("openai_auto_reset_scan_unauthorized", "page", page, "error", err)
			return
		}
		accounts, pageInfo, err := s.accountRepo.ListWithFilters(pageCtx, pagination.PaginationParams{
			Page: page, PageSize: openAIAutoResetBatchSize,
		}, PlatformOpenAI, AccountTypeOAuth, StatusActive, "", 0, "")
		if err != nil {
			slog.Warn("openai_auto_reset_scan_failed", "page", page, "error", err)
			return
		}
		for i := range accounts {
			account := &accounts[i]
			if account.Schedulable && ResolveOpenAIAutoResetCreditConfig(account).Enabled {
				s.Notify(account.ID)
			}
		}
		if len(accounts) < openAIAutoResetBatchSize || pageInfo == nil || page >= pageInfo.Pages {
			return
		}
	}
}

func (s *OpenAIQuotaAutoResetService) scanOpenAIAutoResetRecoveryCandidates(
	ctx context.Context,
	actor authz.Actor,
) bool {
	pager, ok := s.accountRepo.(OpenAIAutoResetRecoveryCandidatePager)
	if !ok {
		slog.Warn("openai_auto_reset_recovery_candidate_pager_unavailable")
		return false
	}

	var afterID int64
	for pageNumber := 1; ; pageNumber++ {
		pageCtx, err := s.reauthorizeWorkerContext(ctx, actor, 0)
		if err != nil {
			slog.Warn("openai_auto_reset_recovery_scan_unauthorized", "page", pageNumber, "error", err)
			return false
		}
		page, err := pager.ListOpenAIAutoResetRecoveryCandidatePage(
			pageCtx,
			OpenAIAutoResetRecoveryCandidatePageOptions{
				AfterID: afterID,
				Limit:   openAIAutoResetBatchSize,
			},
		)
		if err != nil {
			slog.Warn("openai_auto_reset_recovery_scan_failed", "page", pageNumber, "error", err)
			return false
		}
		if page == nil {
			slog.Warn("openai_auto_reset_recovery_scan_failed", "page", pageNumber, "error", "repository returned a nil page")
			return false
		}
		for _, accountID := range page.AccountIDs {
			if !s.enqueueOpenAIAutoResetRecoveryCandidate(ctx, accountID) {
				return false
			}
		}
		if !page.HasMore {
			return true
		}
		if page.NextAfterID <= afterID {
			slog.Warn(
				"openai_auto_reset_recovery_scan_failed",
				"page", pageNumber,
				"error", "repository cursor did not advance",
			)
			return false
		}
		afterID = page.NextAfterID
	}
}

// Redis 锁异常时允许重复扫描，避免协调设施故障导致所有实例同时停止补偿；
// 消费唯一性由数据库幂等记录负责，扫描锁只用于削减重复查询。
func (s *OpenAIQuotaAutoResetService) tryAcquireScanLock(ctx context.Context, actor authz.Actor) (func(), bool, error) {
	lockCtx, err := s.reauthorizeWorkerContext(ctx, actor, 0)
	if err != nil {
		return nil, false, err
	}
	if s.leaderLock == nil {
		return func() {}, true, nil
	}
	ok, err := s.leaderLock.TryAcquireLeaderLock(lockCtx, openAIAutoResetLeaderLockKey, s.owner, 55*time.Second)
	if err != nil {
		slog.Warn("openai_auto_reset_leader_lock_unavailable", "error", err)
		return func() {}, true, nil
	}
	if !ok {
		return nil, false, nil
	}
	return func() {
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(lockCtx), 2*time.Second)
		defer cancel()
		releaseCtx, err := s.reauthorizeWorkerContext(releaseCtx, actor, 0)
		if err != nil {
			slog.Warn("openai_auto_reset_scan_release_unauthorized", "error", err)
			return
		}
		_ = s.leaderLock.ReleaseLeaderLock(releaseCtx, openAIAutoResetLeaderLockKey, s.owner)
	}, true, nil
}

type openAIAutoResetAssessment struct {
	triggerWindow string
	resetReached  bool
	pauseReached  bool
	utilization5h float64
	utilization7d float64
	threshold5h   float64
	threshold7d   float64
}

func openAIAutoResetLeaseIsNil(lease OpenAIQuotaAutoResetAccountLease) bool {
	if lease == nil {
		return true
	}
	value := reflect.ValueOf(lease)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func (s *OpenAIQuotaAutoResetService) isOpenAIAutoResetAccountStructureValid(account *Account, accountID int64) bool {
	if account == nil || account.ID != accountID || account.Platform != PlatformOpenAI || account.Type != AccountTypeOAuth {
		return false
	}
	if account.IsShadow() {
		if account.ParentAccountID != nil {
			s.Notify(*account.ParentAccountID)
		}
		return false
	}
	return true
}

func (s *OpenAIQuotaAutoResetService) resolveOpenAIAutoResetAccountEligibility(account *Account, accountID int64) (OpenAIAutoResetCreditConfig, bool) {
	if !s.isOpenAIAutoResetAccountStructureValid(account, accountID) {
		return OpenAIAutoResetCreditConfig{}, false
	}
	config := ResolveOpenAIAutoResetCreditConfig(account)
	return config, config.Enabled && account.IsActive() && account.Schedulable
}

func (s *OpenAIQuotaAutoResetService) evaluateAccount(ctx context.Context, accountID int64) error {
	ctx = withOpenAIAutoResetContext(ctx)
	ctx, actor, err := s.resolveWorkerContext(ctx, accountID)
	if err != nil {
		return err
	}
	lease, err := s.acquireOpenAIAutoResetAccountLease(ctx, actor, accountID)
	if err != nil {
		if errors.Is(err, errOpenAIAutoResetAccountLockContended) {
			return nil
		}
		return err
	}
	defer releaseOpenAIAutoResetAccountLease(lease, accountID)

	loadCtx, err := s.reauthorizeWorkerContext(ctx, actor, accountID)
	if err != nil {
		return err
	}
	account, err := s.accountRepo.GetByID(loadCtx, accountID)
	if err != nil || account == nil {
		return err
	}
	if !s.isOpenAIAutoResetAccountStructureValid(account, accountID) {
		return nil
	}
	state, stateErr := parseOpenAIAutoResetStateFromExtra(account.Extra)
	if stateErr != nil {
		return openAIAutoResetReconciliationError("managed state is malformed")
	}
	if handled, resumeErr := s.resumeOpenAIAutoResetRecovery(ctx, actor, accountID, state); handled {
		return resumeErr
	}
	config, eligible := s.resolveOpenAIAutoResetAccountEligibility(account, accountID)
	if !eligible {
		return nil
	}

	now := time.Now()
	assessmentCtx, err := s.reauthorizeWorkerContext(ctx, actor, accountID)
	if err != nil {
		return err
	}
	assessment := s.assessExtra(assessmentCtx, account, config, now)
	needsQuery := openAIAutoResetSnapshotStale(account.Extra, now) || assessment.resetReached
	if assessment.pauseReached && !assessment.resetReached {
		needsQuery = needsQuery || state == nil || state.Status == OpenAIAutoResetStatusChecking || state.Status == OpenAIAutoResetStatusFailed || openAIAutoResetStateStale(state, now)
	}
	if !needsQuery {
		if !assessment.pauseReached && state != nil && state.TriggerWindow != "" {
			state.TriggerWindow = ""
			state.ErrorCode = ""
			state.CheckedAt = now.UTC().Format(time.RFC3339)
			if state.AvailableCount > 0 {
				state.Status = OpenAIAutoResetStatusAvailable
			} else {
				state.Status = OpenAIAutoResetStatusNoCredit
			}
			return s.persistState(ctx, actor, accountID, state)
		}
		return nil
	}

	checking := &OpenAIAutoResetCreditState{
		Status:         OpenAIAutoResetStatusChecking,
		TriggerWindow:  assessment.triggerWindow,
		AvailableCount: stateAvailableCount(state),
		CheckedAt:      now.UTC().Format(time.RFC3339),
	}
	copyOpenAIAutoResetAttempt(checking, state)
	if err := s.persistState(ctx, actor, accountID, checking); err != nil {
		return err
	}

	queryCtx, err := s.reauthorizeWorkerContext(ctx, actor, accountID)
	if err != nil {
		return err
	}
	usage, err := s.quota.QueryUsage(queryCtx, accountID)
	if err != nil || usage == nil {
		return s.failState(ctx, actor, accountID, checking, "RESET_CREDIT_QUERY_FAILED", err)
	}
	if err := s.persistFreshUsage(ctx, actor, accountID, usage, now); err != nil {
		if isOpenAIAutoResetAuthorizationError(err) {
			return err
		}
		return s.failState(ctx, actor, accountID, checking, "USAGE_SNAPSHOT_WRITE_FAILED", err)
	}
	if usage.RateLimitResetCredits == nil {
		return s.failState(ctx, actor, accountID, checking, "RESET_CREDIT_DETAILS_UNAVAILABLE", nil)
	}

	// 查询期间管理员可能关闭自动重置、禁用账号或撤出调度；消费前重新读取账号，
	// 确保尚未发出的任务可取消。
	loadCtx, err = s.reauthorizeWorkerContext(ctx, actor, accountID)
	if err != nil {
		return err
	}
	account, err = s.accountRepo.GetByID(loadCtx, accountID)
	if err != nil || account == nil {
		return err
	}
	config, eligible = s.resolveOpenAIAutoResetAccountEligibility(account, accountID)
	if !eligible {
		return nil
	}
	assessmentCtx, err = s.reauthorizeWorkerContext(ctx, actor, accountID)
	if err != nil {
		return err
	}
	assessment = s.assessUsage(assessmentCtx, usage, account, config, now)
	available := usage.RateLimitResetCredits.AvailableCount
	if !assessment.resetReached {
		status := OpenAIAutoResetStatusNoCredit
		if available > 0 {
			status = OpenAIAutoResetStatusAvailable
		}
		return s.persistState(ctx, actor, accountID, &OpenAIAutoResetCreditState{
			Status:         status,
			TriggerWindow:  assessment.triggerWindow,
			AvailableCount: available,
			CheckedAt:      now.UTC().Format(time.RFC3339),
		})
	}
	if available <= 0 {
		return s.persistState(ctx, actor, accountID, &OpenAIAutoResetCreditState{
			Status:         OpenAIAutoResetStatusNoCredit,
			TriggerWindow:  assessment.triggerWindow,
			AvailableCount: 0,
			CheckedAt:      now.UTC().Format(time.RFC3339),
			LastResultAt:   now.UTC().Format(time.RFC3339),
			ErrorCode:      "NO_RESET_CREDIT",
		})
	}

	cycleSeed := openAIAutoResetCycleSeed(usage)
	cycleHash := shortOpenAIAutoResetHash(cycleSeed)
	candidate, selectErr := selectOpenAIAutoResetCandidate(usage.autoResetCandidates, available, state, cycleHash)
	if selectErr != nil {
		failed := checking
		failed.AvailableCount = available
		failed.TriggerWindow = assessment.triggerWindow
		failed.AttemptCycleHash = cycleHash
		return s.failState(ctx, actor, accountID, failed, infraerrors.Reason(selectErr), selectErr)
	}
	creditHash := shortOpenAIAutoResetHash(candidate.ID)
	stableKey := fmt.Sprintf("oarc:%d:%s:%s", accountID, creditHash, cycleHash)
	redeemRequestID := uuid.NewSHA1(uuid.NameSpaceURL, []byte(stableKey)).String()
	resetting := &OpenAIAutoResetCreditState{
		Status:            OpenAIAutoResetStatusResetting,
		TriggerWindow:     assessment.triggerWindow,
		AvailableCount:    available,
		CheckedAt:         now.UTC().Format(time.RFC3339),
		AttemptCycleHash:  cycleHash,
		AttemptCreditHash: creditHash,
	}
	loadCtx, err = s.reauthorizeWorkerContext(ctx, actor, accountID)
	if err != nil {
		return err
	}
	account, err = s.accountRepo.GetByID(loadCtx, accountID)
	if err != nil || account == nil {
		return err
	}
	if _, eligible = s.resolveOpenAIAutoResetAccountEligibility(account, accountID); !eligible {
		return nil
	}
	actorScope, ok := actor.SubjectKey()
	if !ok {
		return authz.ErrInvalidActor
	}
	idempotencyCtx, err := s.reauthorizeWorkerContext(ctx, actor, accountID)
	if err != nil {
		return err
	}
	executionGuard := &openAIAutoResetExecutionGuard{}
	var terminalAudit *AuditLog
	var deferredAuthorizationErr error
	result, err := s.authorizedIdempotencyCoordinator(actor, accountID, executionGuard).Execute(idempotencyCtx, IdempotencyExecuteOptions{
		Scope:             openAIAutoResetIdempotencyScope,
		ActorScope:        actorScope,
		LegacyActorScopes: []string{fmt.Sprintf("account:%d", accountID)},
		Method:            http.MethodPost,
		Route:             openAIAutoResetIdempotencyRoute,
		IdempotencyKey:    stableKey,
		Payload: map[string]any{
			"account_id":  accountID,
			"credit_hash": creditHash,
			"cycle_hash":  cycleHash,
		},
		TTL:        openAIAutoResetAttemptTTL,
		RequireKey: true,
		SuccessFinalizer: func(finalizeCtx context.Context, finalization IdempotencySuccessFinalization) error {
			return s.finalizeOpenAIAutoReset(finalizeCtx, accountID, finalization, terminalAudit)
		},
	}, func(execCtx context.Context) (any, error) {
		executionGuard.markOwnerExecutionStarted()
		// Publish resetting only after the durable owner claim exists. A failure
		// before this point is provably free of upstream side effects and can release
		// the claim through the guarded failure finalizer.
		if persistErr := s.persistState(execCtx, actor, accountID, resetting); persistErr != nil {
			return nil, persistErr
		}
		resetResult, resetErr := s.quota.resetCreditTargetedGuarded(
			execCtx,
			accountID,
			candidate.ID,
			redeemRequestID,
			s.openAIAutoResetExternalEffectGuard(actor, accountID, usage, now, &assessment, executionGuard, lease),
		)
		if resetErr != nil {
			return nil, resetErr
		}
		if resetResult == nil {
			return nil, infraerrors.InternalServer("OPENAI_AUTO_RESET_EMPTY_RESULT", "automatic reset returned an empty result")
		}
		resultCode, resultCodeErr := normalizeOpenAIAutoResetUpstreamResultCode(resetResult.Code)
		if resultCodeErr != nil {
			return nil, openAIAutoResetReconciliationError("upstream reset result is not recognized")
		}
		errorCode := ""
		if resultCode == openAIAutoResetResultCodeNoCredit {
			errorCode = "NO_RESET_CREDIT"
		}
		consumeResult := openAIAutoResetConsumeResult{
			ResultCode:   resultCode,
			WindowsReset: resetResult.WindowsReset,
		}
		if resultCode != openAIAutoResetResultCodeNoCredit {
			postCtx, cancelPost := context.WithTimeout(context.WithoutCancel(execCtx), 8*time.Second)
			postGuard := &openAIAutoResetPostProcessGuard{service: s, actor: actor}
			post := RunOpenAIQuotaResetPostProcess(postCtx, accountID, postGuard, postGuard, postGuard.LoadAccount)
			cancelPost()
			if postAuthorizeErr := postGuard.AuthorizationError(); postAuthorizeErr != nil {
				deferredAuthorizationErr = postAuthorizeErr
				consumeResult.RecoveryPending = true
				consumeResult.RecoveryDeferred = true
				resultCode = "recovery_deferred"
				errorCode = infraerrors.Reason(postAuthorizeErr)
				if errorCode == "" {
					errorCode = "OPENAI_AUTO_RESET_RECOVERY_AUTHORIZATION_DEFERRED"
				}
			} else {
				consumeResult.PostProcessRecorded = true
				consumeResult.AccountStateRecovered = post.AccountStateRecovered
				consumeResult.WarningCode = post.WarningCode
				consumeResult.RecoveryPending = !post.AccountStateRecovered || post.WarningCode != ""
				if post.Quota != nil && post.Quota.RateLimitResetCredits != nil {
					consumeResult.AvailableCount = post.Quota.RateLimitResetCredits.AvailableCount
					consumeResult.AvailableCountKnown = true
				}
			}
			if consumeResult.RecoveryPending && !consumeResult.RecoveryDeferred {
				resultCode = "recovery_failed"
				errorCode = consumeResult.WarningCode
				if errorCode == "" {
					errorCode = OpenAIQuotaResetWarningAccountRecoveryFailed
					consumeResult.WarningCode = errorCode
				}
			}
		}
		if responseErr := validateOpenAIAutoResetConsumeResult(consumeResult, false); responseErr != nil {
			return nil, openAIAutoResetReconciliationError("upstream reset result is internally inconsistent")
		}
		var auditErr error
		terminalAudit, auditErr = buildOpenAIAutoResetFinalAudit(
			actor,
			accountID,
			redeemRequestID,
			assessment,
			available,
			resultCode,
			resetResult.WindowsReset,
			errorCode,
		)
		if auditErr != nil {
			return nil, auditErr
		}
		// 幂等表只保存脱敏结果，避免上游返回的卡 ID 被持久化到响应体列。
		return consumeResult, nil
	})
	if err != nil {
		if errors.Is(err, errOpenAIAutoResetAccountLockContended) {
			return nil
		}
		if errors.Is(err, errOpenAIAutoResetEligibilityChanged) || errors.Is(err, errOpenAIAutoResetUpstreamIdentityChanged) {
			resetting.Status = OpenAIAutoResetStatusFailed
			resetting.ErrorCode = "OPENAI_AUTO_RESET_ELIGIBILITY_CHANGED"
			if errors.Is(err, errOpenAIAutoResetUpstreamIdentityChanged) {
				resetting.ErrorCode = "OPENAI_AUTO_RESET_UPSTREAM_IDENTITY_CHANGED"
			}
			resetting.LastResultAt = time.Now().UTC().Format(time.RFC3339)
			return s.persistState(ctx, actor, accountID, resetting)
		}
		if executionGuard.hasExternalEffect() {
			return err
		}
		if isOpenAIAutoResetAuthorizationError(err) || errors.Is(err, errOpenAIAutoResetFinalization) {
			return err
		}
		// 另一个实例已持有同一周期的兑换时保持 resetting，等待下一轮读取同一
		// 幂等结果；不能把并发冲突误报成上游消费失败，更不能改选下一张卡。
		reason := infraerrors.Reason(err)
		if reason == infraerrors.Reason(ErrIdempotencyInProgress) || reason == infraerrors.Reason(ErrIdempotencyRetryBackoff) {
			return nil
		}
		if auditErr := s.recordAudit(ctx, actor, accountID, assessment, available, "failed", 0, infraerrors.Reason(err)); auditErr != nil {
			return auditErr
		}
		return s.failState(ctx, actor, accountID, resetting, infraerrors.Reason(err), err)
	}
	if deferredAuthorizationErr != nil {
		return deferredAuthorizationErr
	}

	consumeResult, decodeErr := decodeOpenAIAutoResetConsumeResult(result.Data)
	if decodeErr != nil {
		return openAIAutoResetReconciliationError("terminal idempotency response is corrupt")
	}
	if consumeResult.ResultCode == openAIAutoResetResultCodeNoCredit {
		noCreditAt := time.Now().UTC().Format(time.RFC3339)
		noCredit := &OpenAIAutoResetCreditState{
			Status:            OpenAIAutoResetStatusNoCredit,
			TriggerWindow:     assessment.triggerWindow,
			AvailableCount:    0,
			CheckedAt:         noCreditAt,
			LastResultAt:      noCreditAt,
			ErrorCode:         "NO_RESET_CREDIT",
			AttemptCycleHash:  cycleHash,
			AttemptCreditHash: creditHash,
		}
		return s.persistState(ctx, actor, accountID, noCredit)
	}
	if !consumeResult.PostProcessRecorded || (result.Replayed && consumeResult.RecoveryPending) {
		postCtx, cancelPost := context.WithTimeout(context.WithoutCancel(ctx), 8*time.Second)
		postGuard := &openAIAutoResetPostProcessGuard{service: s, actor: actor}
		post := RunOpenAIQuotaResetPostProcess(postCtx, accountID, postGuard, postGuard, postGuard.LoadAccount)
		cancelPost()
		if authorizeErr := postGuard.AuthorizationError(); authorizeErr != nil {
			return authorizeErr
		}
		consumeResult.PostProcessRecorded = true
		consumeResult.RecoveryDeferred = false
		consumeResult.AccountStateRecovered = post.AccountStateRecovered
		consumeResult.WarningCode = post.WarningCode
		consumeResult.RecoveryPending = !post.AccountStateRecovered || post.WarningCode != ""
		if post.Quota != nil && post.Quota.RateLimitResetCredits != nil {
			consumeResult.AvailableCount = post.Quota.RateLimitResetCredits.AvailableCount
			consumeResult.AvailableCountKnown = true
		}
	}
	if !consumeResult.AccountStateRecovered || consumeResult.WarningCode != "" {
		code := consumeResult.WarningCode
		if code == "" {
			code = OpenAIQuotaResetWarningAccountRecoveryFailed
		}
		return s.failState(ctx, actor, accountID, resetting, code, nil)
	}

	successAt := time.Now().UTC().Format(time.RFC3339)
	success := &OpenAIAutoResetCreditState{
		Status:            OpenAIAutoResetStatusSuccess,
		TriggerWindow:     assessment.triggerWindow,
		AvailableCount:    max(0, available-1),
		CheckedAt:         successAt,
		LastResultAt:      successAt,
		AttemptCycleHash:  cycleHash,
		AttemptCreditHash: creditHash,
	}
	if consumeResult.AvailableCountKnown {
		success.AvailableCount = consumeResult.AvailableCount
	}
	if err := s.persistState(ctx, actor, accountID, success); err != nil {
		return err
	}
	slog.Info("openai_auto_reset_credit_success",
		"account_id", accountID,
		"trigger_window", assessment.triggerWindow,
		"threshold_5h", assessment.threshold5h,
		"threshold_7d", assessment.threshold7d,
		"utilization_5h", assessment.utilization5h,
		"utilization_7d", assessment.utilization7d,
		"windows_reset", consumeResult.WindowsReset,
	)
	return nil
}

func (s *OpenAIQuotaAutoResetService) assessExtra(ctx context.Context, account *Account, config OpenAIAutoResetCreditConfig, now time.Time) openAIAutoResetAssessment {
	utilization5h, _ := resolveOpenAIQuotaUtilization(account.Extra, "5h", now)
	utilization7d, _ := resolveOpenAIQuotaUtilization(account.Extra, "7d", now)
	return s.buildAssessment(ctx, account, config, utilization5h, utilization7d)
}

func (s *OpenAIQuotaAutoResetService) assessUsage(ctx context.Context, usage *OpenAIQuotaUsage, account *Account, config OpenAIAutoResetCreditConfig, now time.Time) openAIAutoResetAssessment {
	updates := buildOpenAIAutoResetUsageUpdates(usage, now)
	utilization5h := readOpenAIQuotaUsedPercent(updates, "5h") / 100
	utilization7d := readOpenAIQuotaUsedPercent(updates, "7d") / 100
	return s.buildAssessment(ctx, account, config, utilization5h, utilization7d)
}

func (s *OpenAIQuotaAutoResetService) buildAssessment(ctx context.Context, account *Account, config OpenAIAutoResetCreditConfig, utilization5h, utilization7d float64) openAIAutoResetAssessment {
	if ctx == nil {
		ctx = context.Background()
	}
	assessment := openAIAutoResetAssessment{
		utilization5h: utilization5h,
		utilization7d: utilization7d,
		threshold5h:   config.Threshold5h,
		threshold7d:   config.Threshold7d,
	}
	reset5h := utilization5h >= config.Threshold5h
	reset7d := utilization7d >= config.Threshold7d
	assessment.resetReached = reset5h || reset7d
	assessment.triggerWindow = joinOpenAIAutoResetWindows(reset5h, reset7d)

	pause5h, pause7d := resolveOpenAIQuotaAutoPauseThresholds(ctx, account)
	if s.settings != nil {
		pause5h, pause7d = resolveOpenAIQuotaAutoPauseThresholds(
			withOpenAIQuotaAutoPauseSettings(ctx, s.settings.GetOpenAIQuotaAutoPauseSettings(ctx)),
			account,
		)
	}
	pauseReached5h := !resolveAccountExtraBool(account.Extra, "auto_pause_5h_disabled") && pause5h > 0 && utilization5h >= pause5h
	pauseReached7d := !resolveAccountExtraBool(account.Extra, "auto_pause_7d_disabled") && pause7d > 0 && utilization7d >= pause7d
	assessment.pauseReached = pauseReached5h || pauseReached7d || assessment.resetReached
	if assessment.triggerWindow == "" {
		assessment.triggerWindow = joinOpenAIAutoResetWindows(pauseReached5h, pauseReached7d)
	}
	return assessment
}

func joinOpenAIAutoResetWindows(fiveHour, sevenDay bool) string {
	switch {
	case fiveHour && sevenDay:
		return "5h+7d"
	case fiveHour:
		return "5h"
	case sevenDay:
		return "7d"
	default:
		return ""
	}
}

func buildOpenAIAutoResetUsageUpdates(usage *OpenAIQuotaUsage, now time.Time) map[string]any {
	if usage == nil || usage.RateLimit == nil {
		return nil
	}
	rateLimit := usage.RateLimit
	snapshot := &OpenAICodexUsageSnapshot{UpdatedAt: now.UTC().Format(time.RFC3339)}
	applyWindow := func(window *OpenAIRateLimitWindow, primary bool) {
		if window == nil {
			return
		}
		used := window.UsedPercent
		resetAfter := int(window.ResetAfterSeconds)
		windowMinutes := int(window.LimitWindowSeconds / 60)
		if primary {
			snapshot.PrimaryUsedPercent = &used
			snapshot.PrimaryResetAfterSeconds = &resetAfter
			snapshot.PrimaryWindowMinutes = &windowMinutes
		} else {
			snapshot.SecondaryUsedPercent = &used
			snapshot.SecondaryResetAfterSeconds = &resetAfter
			snapshot.SecondaryWindowMinutes = &windowMinutes
		}
	}
	applyWindow(rateLimit.PrimaryWindow, true)
	applyWindow(rateLimit.SecondaryWindow, false)
	return buildCodexUsageExtraUpdates(snapshot, now)
}

func (s *OpenAIQuotaAutoResetService) persistFreshUsage(ctx context.Context, actor authz.Actor, accountID int64, usage *OpenAIQuotaUsage, now time.Time) error {
	updates := buildOpenAIAutoResetUsageUpdates(usage, now)
	if len(updates) > 0 {
		updateCtx, err := s.reauthorizeWorkerContext(ctx, actor, accountID)
		if err != nil {
			return err
		}
		if err := s.accountRepo.UpdateExtra(updateCtx, accountID, updates); err != nil {
			return err
		}
	}
	cacheCtx, err := s.reauthorizeWorkerContext(ctx, actor, accountID)
	if err != nil {
		return err
	}
	return s.quota.CacheResetCreditsSnapshot(cacheCtx, accountID, usage.RateLimitResetCredits)
}

func selectOpenAIAutoResetCandidate(candidates []openAIAutoResetCreditCandidate, available int, previous *OpenAIAutoResetCreditState, cycleHash string) (openAIAutoResetCreditCandidate, error) {
	if available <= 0 {
		return openAIAutoResetCreditCandidate{}, infraerrors.Conflict("OPENAI_AUTO_RESET_NO_CREDIT", "no reset credit is available")
	}
	if len(candidates) < available {
		return openAIAutoResetCreditCandidate{}, infraerrors.Conflict("OPENAI_AUTO_RESET_CREDIT_DETAILS_INCOMPLETE", "reset credit details are incomplete")
	}
	for _, candidate := range candidates {
		if _, err := time.Parse(time.RFC3339, candidate.ExpiresAt); err != nil {
			return openAIAutoResetCreditCandidate{}, infraerrors.Conflict("OPENAI_AUTO_RESET_CREDIT_EXPIRY_INVALID", "reset credit expiration is invalid")
		}
	}
	sorted := append([]openAIAutoResetCreditCandidate(nil), candidates...)
	sort.SliceStable(sorted, func(i, j int) bool {
		left, leftErr := time.Parse(time.RFC3339, sorted[i].ExpiresAt)
		right, rightErr := time.Parse(time.RFC3339, sorted[j].ExpiresAt)
		if leftErr != nil {
			return false
		}
		if rightErr != nil {
			return true
		}
		return left.Before(right)
	})
	if previous != nil && previous.AttemptCycleHash == cycleHash && previous.AttemptCreditHash != "" {
		for _, candidate := range sorted {
			if shortOpenAIAutoResetHash(candidate.ID) == previous.AttemptCreditHash {
				if strings.TrimSpace(candidate.ID) == "" {
					break
				}
				return candidate, nil
			}
		}
		return openAIAutoResetCreditCandidate{}, infraerrors.Conflict("OPENAI_AUTO_RESET_ORIGINAL_CREDIT_UNAVAILABLE", "the original reset credit cannot be confirmed; refusing to switch credits")
	}
	if len(sorted) == 0 || strings.TrimSpace(sorted[0].ID) == "" {
		return openAIAutoResetCreditCandidate{}, infraerrors.Conflict("OPENAI_AUTO_RESET_CREDIT_ID_MISSING", "the earliest reset credit has no official id")
	}
	return sorted[0], nil
}

func openAIAutoResetCycleSeed(usage *OpenAIQuotaUsage) string {
	if usage == nil || usage.RateLimit == nil {
		return "5h:0|7d:0"
	}
	var fiveHour, sevenDay int64
	for _, window := range []*OpenAIRateLimitWindow{usage.RateLimit.PrimaryWindow, usage.RateLimit.SecondaryWindow} {
		if window == nil {
			continue
		}
		resetAt := window.ResetAt
		if resetAt <= 0 {
			resetAt = usage.FetchedAt + window.ResetAfterSeconds
		}
		if window.LimitWindowSeconds <= 6*60*60 {
			fiveHour = resetAt
		} else {
			sevenDay = resetAt
		}
	}
	return fmt.Sprintf("5h:%d|7d:%d", fiveHour, sevenDay)
}

func shortOpenAIAutoResetHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:12])
}

func openAIAutoResetSnapshotStale(extra map[string]any, now time.Time) bool {
	if len(extra) == 0 {
		return true
	}
	raw, ok := extra["codex_usage_updated_at"]
	if !ok {
		return true
	}
	updatedAt, err := parseTime(fmt.Sprint(raw))
	return err != nil || now.Sub(updatedAt) >= openAIAutoResetSnapshotTTL
}

func openAIAutoResetStateFromExtra(extra map[string]any) *OpenAIAutoResetCreditState {
	state, _ := parseOpenAIAutoResetStateFromExtra(extra)
	return state
}

func parseOpenAIAutoResetStateFromExtra(extra map[string]any) (*OpenAIAutoResetCreditState, error) {
	if len(extra) == 0 {
		return nil, nil
	}
	raw, ok := extra[OpenAIAutoResetCreditStateExtraKey]
	if !ok || raw == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("encode OpenAI auto-reset managed state: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var state OpenAIAutoResetCreditState
	if err := decoder.Decode(&state); err != nil {
		return nil, fmt.Errorf("decode OpenAI auto-reset managed state: %w", err)
	}
	if !validOpenAIAutoResetManagedStatus(state.Status) {
		return nil, errors.New("decode OpenAI auto-reset managed state: status is not recognized")
	}
	hasCreditHash := state.AttemptCreditHash != ""
	hasCycleHash := state.AttemptCycleHash != ""
	if hasCreditHash != hasCycleHash ||
		(hasCreditHash && (!validOpenAIAutoResetAttemptHash(state.AttemptCreditHash) ||
			!validOpenAIAutoResetAttemptHash(state.AttemptCycleHash))) {
		return nil, errors.New("decode OpenAI auto-reset managed state: attempt identity is malformed")
	}
	if state.Status == OpenAIAutoResetStatusResetting && !hasCreditHash {
		return nil, errors.New("decode OpenAI auto-reset managed state: resetting attempt identity is missing")
	}
	if state.AvailableCount < 0 || state.AvailableCount > openAIAutoResetMaxCount {
		return nil, errors.New("decode OpenAI auto-reset managed state: available count is outside the supported range")
	}
	return &state, nil
}

func validOpenAIAutoResetManagedStatus(status string) bool {
	switch status {
	case OpenAIAutoResetStatusChecking,
		OpenAIAutoResetStatusAvailable,
		OpenAIAutoResetStatusResetting,
		OpenAIAutoResetStatusSuccess,
		OpenAIAutoResetStatusNoCredit,
		OpenAIAutoResetStatusFailed:
		return true
	default:
		return false
	}
}

func openAIAutoResetStateStale(state *OpenAIAutoResetCreditState, now time.Time) bool {
	if state == nil || state.CheckedAt == "" {
		return true
	}
	checkedAt, err := time.Parse(time.RFC3339, state.CheckedAt)
	return err != nil || now.Sub(checkedAt) >= openAIAutoResetSnapshotTTL
}

func stateAvailableCount(state *OpenAIAutoResetCreditState) int {
	if state == nil {
		return 0
	}
	return state.AvailableCount
}

func copyOpenAIAutoResetAttempt(target, source *OpenAIAutoResetCreditState) {
	if target == nil || source == nil {
		return
	}
	target.AttemptCycleHash = source.AttemptCycleHash
	target.AttemptCreditHash = source.AttemptCreditHash
}

func (s *OpenAIQuotaAutoResetService) persistState(ctx context.Context, actor authz.Actor, accountID int64, state *OpenAIAutoResetCreditState) error {
	if state == nil {
		return nil
	}
	updateCtx, err := s.reauthorizeWorkerContext(ctx, actor, accountID)
	if err != nil {
		return err
	}
	return s.accountRepo.UpdateExtra(updateCtx, accountID, map[string]any{OpenAIAutoResetCreditStateExtraKey: state})
}

func (s *OpenAIQuotaAutoResetService) failState(ctx context.Context, actor authz.Actor, accountID int64, state *OpenAIAutoResetCreditState, code string, cause error) error {
	if state == nil {
		state = &OpenAIAutoResetCreditState{}
	}
	if strings.TrimSpace(code) == "" {
		code = "OPENAI_AUTO_RESET_FAILED"
	}
	state.Status = OpenAIAutoResetStatusFailed
	state.ErrorCode = code
	state.LastResultAt = time.Now().UTC().Format(time.RFC3339)
	if err := s.persistState(ctx, actor, accountID, state); err != nil {
		return err
	}
	slog.Warn("openai_auto_reset_credit_failed",
		"account_id", accountID,
		"trigger_window", state.TriggerWindow,
		"available_count", state.AvailableCount,
		"error_code", code,
	)
	if cause != nil {
		return cause
	}
	return infraerrors.Conflict(code, "automatic reset credit operation failed")
}

func (s *OpenAIQuotaAutoResetService) recordAudit(
	ctx context.Context,
	actor authz.Actor,
	accountID int64,
	assessment openAIAutoResetAssessment,
	available int,
	resultCode string,
	windowsReset int,
	errorCode string,
) error {
	if s.audit == nil {
		return fmt.Errorf("%w: audit service is not configured", errOpenAIAutoResetAudit)
	}
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	auditCtx, err := s.reauthorizeWorkerContext(auditCtx, actor, accountID)
	if err != nil {
		return err
	}
	principalID, ok := actor.ServicePrincipalID()
	if !ok {
		return authz.ErrInvalidActor
	}
	statusCode := http.StatusOK
	if resultCode != "success" {
		statusCode = http.StatusConflict
	}
	if err := s.audit.RecordDurable(auditCtx, &AuditLog{
		ActorServicePrincipalID: &principalID,
		AuthMethod:              AuditAuthMethodServicePrincipal,
		Action:                  AuditActionOpenAIQuotaAutoReset,
		Method:                  "SYSTEM",
		Path:                    fmt.Sprintf("/system/openai/accounts/%d/auto-reset-credit", accountID),
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
	}); err != nil {
		return fmt.Errorf("%w: %w", errOpenAIAutoResetAudit, err)
	}
	return nil
}

func isOpenAIAutoResetAuthorizationError(err error) bool {
	return errors.Is(err, authz.ErrInvalidActor) ||
		errors.Is(err, authz.ErrActorInactive) ||
		errors.Is(err, authz.ErrSubjectNotFound) ||
		errors.Is(err, authz.ErrSessionInvalid) ||
		errors.Is(err, authz.ErrPolicyAccessDenied) ||
		errors.Is(err, authz.ErrAuthorizationUnavailable)
}

type openAIAutoResetPostProcessGuard struct {
	service          *OpenAIQuotaAutoResetService
	actor            authz.Actor
	authorizationErr error
}

func (g *openAIAutoResetPostProcessGuard) authorize(ctx context.Context, accountID int64) (context.Context, error) {
	if g.authorizationErr != nil {
		return ctx, g.authorizationErr
	}
	authorizedCtx, err := g.service.reauthorizeWorkerContext(ctx, g.actor, accountID)
	if err != nil {
		if isOpenAIAutoResetAuthorizationError(err) {
			g.authorizationErr = err
		}
		return ctx, err
	}
	return authorizedCtx, nil
}

func (g *openAIAutoResetPostProcessGuard) RecoverAccountState(
	ctx context.Context,
	accountID int64,
	options AccountRecoveryOptions,
) (*SuccessfulTestRecoveryResult, error) {
	authorizedCtx, err := g.authorize(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return g.service.recoverer.RecoverAccountState(authorizedCtx, accountID, options)
}

func (g *openAIAutoResetPostProcessGuard) QueryUsage(ctx context.Context, accountID int64) (*OpenAIQuotaUsage, error) {
	authorizedCtx, err := g.authorize(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return g.service.quota.QueryUsage(authorizedCtx, accountID)
}

func (g *openAIAutoResetPostProcessGuard) CacheResetCreditsSnapshot(
	ctx context.Context,
	accountID int64,
	credits *OpenAIRateLimitResetCredits,
) error {
	authorizedCtx, err := g.authorize(ctx, accountID)
	if err != nil {
		return err
	}
	return g.service.quota.CacheResetCreditsSnapshot(authorizedCtx, accountID, credits)
}

func (g *openAIAutoResetPostProcessGuard) LoadAccount(ctx context.Context, accountID int64) (*Account, error) {
	authorizedCtx, err := g.authorize(ctx, accountID)
	if err != nil {
		return nil, err
	}
	return g.service.accountRepo.GetByID(authorizedCtx, accountID)
}

func (g *openAIAutoResetPostProcessGuard) AuthorizationError() error {
	return g.authorizationErr
}

var openAIAutoResetNotifierRegistry struct {
	sync.RWMutex
	service *OpenAIQuotaAutoResetService
}

func setOpenAIAutoResetNotifier(service *OpenAIQuotaAutoResetService) {
	openAIAutoResetNotifierRegistry.Lock()
	openAIAutoResetNotifierRegistry.service = service
	openAIAutoResetNotifierRegistry.Unlock()
}

func clearOpenAIAutoResetNotifier(service *OpenAIQuotaAutoResetService) {
	openAIAutoResetNotifierRegistry.Lock()
	if openAIAutoResetNotifierRegistry.service == service {
		openAIAutoResetNotifierRegistry.service = nil
	}
	openAIAutoResetNotifierRegistry.Unlock()
}

func notifyOpenAIAutoReset(accountID int64) {
	openAIAutoResetNotifierRegistry.RLock()
	service := openAIAutoResetNotifierRegistry.service
	openAIAutoResetNotifierRegistry.RUnlock()
	if service != nil {
		service.Notify(accountID)
	}
}

// NotifyOpenAIAutoResetCredit 供额度查询入口发送轻量信号；不执行同步上游请求。
func NotifyOpenAIAutoResetCredit(accountID int64) {
	notifyOpenAIAutoReset(accountID)
}
