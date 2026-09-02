package service

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
)

var errOpenAIAutoResetExternalEffectUnfinalized = errors.New("OpenAI quota auto-reset external effect has no terminal record")

type openAIAutoResetExecutionGuard struct {
	ownerExecutionStarted atomic.Bool
	externalEffectStarted atomic.Bool
}

func (g *openAIAutoResetExecutionGuard) markOwnerExecutionStarted() {
	if g != nil {
		g.ownerExecutionStarted.Store(true)
	}
}

func (g *openAIAutoResetExecutionGuard) markExternalEffectStarted() {
	if g != nil {
		g.externalEffectStarted.Store(true)
	}
}

func (g *openAIAutoResetExecutionGuard) canFinalizeNoEffectFailure() bool {
	return g != nil && g.ownerExecutionStarted.Load() && !g.externalEffectStarted.Load()
}

func (g *openAIAutoResetExecutionGuard) hasExternalEffect() bool {
	return g != nil && g.externalEffectStarted.Load()
}

type openAIAutoResetAuthorizedIdempotencyRepository struct {
	delegate  IdempotencyRepository
	service   *OpenAIQuotaAutoResetService
	actor     authz.Actor
	accountID int64
	execution *openAIAutoResetExecutionGuard
}

func (r *openAIAutoResetAuthorizedIdempotencyRepository) authorize(ctx context.Context) (context.Context, error) {
	return r.service.reauthorizeWorkerContext(ctx, r.actor, r.accountID)
}

func (r *openAIAutoResetAuthorizedIdempotencyRepository) CreateProcessing(
	ctx context.Context,
	record *IdempotencyRecord,
) (bool, error) {
	authorizedCtx, err := r.authorize(ctx)
	if err != nil {
		return false, err
	}
	return r.delegate.CreateProcessing(authorizedCtx, record)
}

func (r *openAIAutoResetAuthorizedIdempotencyRepository) GetByScopeAndKeyHash(
	ctx context.Context,
	scope string,
	keyHash string,
) (*IdempotencyRecord, error) {
	authorizedCtx, err := r.authorize(ctx)
	if err != nil {
		return nil, err
	}
	return r.delegate.GetByScopeAndKeyHash(authorizedCtx, scope, keyHash)
}

func (r *openAIAutoResetAuthorizedIdempotencyRepository) ExtendExpiration(
	ctx context.Context,
	id int64,
	requestFingerprint string,
	newExpiresAt time.Time,
) (bool, error) {
	authorizedCtx, err := r.authorize(ctx)
	if err != nil {
		return false, err
	}
	return r.delegate.ExtendExpiration(authorizedCtx, id, requestFingerprint, newExpiresAt)
}

func (r *openAIAutoResetAuthorizedIdempotencyRepository) TryReclaim(
	ctx context.Context,
	id int64,
	fromStatus string,
	now time.Time,
	newLockedUntil time.Time,
	newExpiresAt time.Time,
) (bool, error) {
	authorizedCtx, err := r.authorize(ctx)
	if err != nil {
		return false, err
	}
	if fromStatus != IdempotencyStatusFailedRetryable {
		return false, nil
	}
	return r.delegate.TryReclaim(authorizedCtx, id, fromStatus, now, newLockedUntil, newExpiresAt)
}

func (r *openAIAutoResetAuthorizedIdempotencyRepository) ExtendProcessingLock(
	ctx context.Context,
	id int64,
	requestFingerprint string,
	newLockedUntil time.Time,
	newExpiresAt time.Time,
) (bool, error) {
	authorizedCtx, err := r.authorize(ctx)
	if err != nil {
		return false, err
	}
	return r.delegate.ExtendProcessingLock(authorizedCtx, id, requestFingerprint, newLockedUntil, newExpiresAt)
}

func (r *openAIAutoResetAuthorizedIdempotencyRepository) MarkSucceeded(
	ctx context.Context,
	id int64,
	responseStatus int,
	responseBody string,
	expiresAt time.Time,
) error {
	authorizedCtx, err := r.authorize(ctx)
	if err != nil {
		return err
	}
	return r.delegate.MarkSucceeded(authorizedCtx, id, responseStatus, responseBody, expiresAt)
}

func (r *openAIAutoResetAuthorizedIdempotencyRepository) MarkFailedRetryable(
	ctx context.Context,
	id int64,
	errorReason string,
	lockedUntil time.Time,
	expiresAt time.Time,
) error {
	authorizedCtx, err := r.authorize(ctx)
	if err != nil {
		if r.execution == nil || !r.execution.canFinalizeNoEffectFailure() {
			return err
		}
		authorizedCtx = ctx
	}
	if r.execution != nil && r.execution.hasExternalEffect() {
		return errOpenAIAutoResetExternalEffectUnfinalized
	}
	return r.delegate.MarkFailedRetryable(authorizedCtx, id, errorReason, lockedUntil, expiresAt)
}

func (r *openAIAutoResetAuthorizedIdempotencyRepository) DeleteExpired(
	ctx context.Context,
	now time.Time,
	limit int,
) (int64, error) {
	authorizedCtx, err := r.authorize(ctx)
	if err != nil {
		return 0, err
	}
	return r.delegate.DeleteExpired(authorizedCtx, now, limit)
}

func (s *OpenAIQuotaAutoResetService) authorizedIdempotencyCoordinator(
	actor authz.Actor,
	accountID int64,
	execution *openAIAutoResetExecutionGuard,
) *IdempotencyCoordinator {
	return &IdempotencyCoordinator{
		repo: &openAIAutoResetAuthorizedIdempotencyRepository{
			delegate:  s.idempotency.repo,
			service:   s,
			actor:     actor,
			accountID: accountID,
			execution: execution,
		},
		cfg: s.idempotency.cfg,
	}
}
