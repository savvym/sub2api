package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrHostingEntitlementUnavailable = infraerrors.ServiceUnavailable(
		"HOSTING_ENTITLEMENT_UNAVAILABLE",
		"hosting entitlement data is unavailable",
	)
	ErrHostingEntitlementConflict = infraerrors.Conflict(
		"HOSTING_ENTITLEMENT_CONFLICT",
		"the hosting entitlement changed before this request was applied",
	)
	ErrHostingActorNotAuthorized = infraerrors.Forbidden(
		"HOSTING_ACTOR_NOT_AUTHORIZED",
		"an active administrator is required to manage hosting entitlements",
	)
	ErrHostingQualificationRequired = infraerrors.Forbidden(
		"HOSTING_QUALIFICATION_REQUIRED",
		"an active hoster qualification is required",
	)
	ErrHostingCreateForbidden = infraerrors.Forbidden(
		"HOSTING_CREATE_FORBIDDEN",
		"private resource creation is not allowed",
	)
	ErrHostingQuotaExceeded = infraerrors.Conflict(
		"HOSTING_QUOTA_EXCEEDED",
		"the private resource quota has been reached",
	)
)

const AuditActionHostingEntitlementUpdate = "admin.authorization.hosting_entitlement.update"

// HostingEntitlement is the administrator-facing qualification and capacity
// view. Version zero means no quota row has been materialized yet.
type HostingEntitlement struct {
	UserID           int64      `json:"user_id"`
	Hoster           bool       `json:"hoster"`
	AccountLimit     int64      `json:"account_limit"`
	AccountUsage     int64      `json:"account_usage"`
	AccountRemaining int64      `json:"account_remaining"`
	GroupLimit       int64      `json:"group_limit"`
	GroupUsage       int64      `json:"group_usage"`
	GroupRemaining   int64      `json:"group_remaining"`
	Version          int64      `json:"version"`
	AuthzVersion     int64      `json:"authz_version"`
	CreatedByUserID  *int64     `json:"created_by_user_id"`
	UpdatedByUserID  *int64     `json:"updated_by_user_id"`
	CreatedAt        *time.Time `json:"created_at"`
	UpdatedAt        *time.Time `json:"updated_at"`
}

// HostingEntitlementRecord is the locked repository projection used to make a
// composite hoster-role and quota decision.
type HostingEntitlementRecord struct {
	UserID                    int64
	UserActive                bool
	Hoster                    bool
	HosterAssignmentExists    bool
	HosterAssignmentPermanent bool
	AccountLimit              int64
	AccountUsage              int64
	GroupLimit                int64
	GroupUsage                int64
	Version                   int64
	AuthzVersion              int64
	CreatedByUserID           *int64
	UpdatedByUserID           *int64
	CreatedAt                 *time.Time
	UpdatedAt                 *time.Time
}

type HostingEntitlementUpdateInput struct {
	Actor           authz.Actor
	TargetUserID    int64
	ExpectedVersion int64
	Hoster          bool
	AccountLimit    int64
	GroupLimit      int64
	AuditTrace      HostingEntitlementAuditTrace
}

type HostingEntitlementAuditTrace struct {
	RequestID string
	ClientIP  string
	UserAgent string
}

type HostingEntitlementUpdateResult struct {
	HostingEntitlement
	Changed bool `json:"changed"`
}

type HostingEntitlementMutationInput struct {
	ActorUserID  int64
	TargetUserID int64
	Hoster       bool
	AccountLimit int64
	GroupLimit   int64
	Current      HostingEntitlementRecord
}

type HostingEntitlementMutationResult struct {
	Changed     bool
	RoleChanged bool
}

type HostingCapacityRecord struct {
	UserID       int64
	UserActive   bool
	Hoster       bool
	Version      int64
	AccountLimit int64
	AccountUsage int64
	GroupLimit   int64
	GroupUsage   int64
}

type HostingCapacity struct {
	UserID       int64              `json:"user_id"`
	ResourceType authz.ResourceType `json:"resource_type"`
	Limit        int64              `json:"limit"`
	Usage        int64              `json:"usage"`
	Remaining    int64              `json:"remaining"`
	Version      int64              `json:"version"`
}

// HostingEntitlementRepository owns all database snapshots, locks and writes.
// Capacity checks require a caller-owned SERIALIZABLE transaction so the lock
// remains held until the matching resource insert commits or rolls back.
type HostingEntitlementRepository interface {
	WithHostingEntitlementSnapshot(ctx context.Context, fn func(snapshotCtx context.Context) error) error
	WithHostingEntitlementTx(ctx context.Context, fn func(txCtx context.Context) error) error
	LockHostingEntitlementSubjects(ctx context.Context, actorUserID, targetUserID int64) error
	ReadHostingEntitlement(ctx context.Context, targetUserID int64) (HostingEntitlementRecord, error)
	ApplyHostingEntitlement(ctx context.Context, input HostingEntitlementMutationInput) (HostingEntitlementMutationResult, error)
	AppendHostingEntitlementAudit(
		ctx context.Context,
		actorUserID int64,
		previous HostingEntitlementRecord,
		current HostingEntitlementRecord,
		trace HostingEntitlementAuditTrace,
	) error
	LockHostingCapacity(ctx context.Context, userID int64, resourceType authz.ResourceType) (HostingCapacityRecord, error)
}

// HostingCapacityGuard is the SERIALIZABLE transaction-bound capacity contract
// consumed by the private Account and Group creation flows added later in Phase 2.
type HostingCapacityGuard interface {
	RequireCreateCapacity(ctx context.Context, actor authz.Actor, resourceType authz.ResourceType) (HostingCapacity, error)
}

type HostingEntitlementService struct {
	repo     HostingEntitlementRepository
	resolver authz.Resolver
	policy   authz.ResourcePolicy
}

var _ HostingCapacityGuard = (*HostingEntitlementService)(nil)

func NewHostingEntitlementService(
	repo HostingEntitlementRepository,
	resolver authz.Resolver,
	policy authz.ResourcePolicy,
) *HostingEntitlementService {
	return &HostingEntitlementService{repo: repo, resolver: resolver, policy: policy}
}

func (s *HostingEntitlementService) Get(
	ctx context.Context,
	actor authz.Actor,
	targetUserID int64,
) (HostingEntitlement, error) {
	var result HostingEntitlement
	if err := ValidateAdminResourceActor(actor); err != nil {
		return result, ErrHostingActorNotAuthorized
	}
	if targetUserID <= 0 {
		return result, infraerrors.BadRequest("INVALID_USER_ID", "user_id must be positive")
	}
	if s == nil || s.repo == nil || s.resolver == nil || ctx == nil {
		return result, ErrHostingEntitlementUnavailable
	}

	err := s.repo.WithHostingEntitlementSnapshot(ctx, func(snapshotCtx context.Context) error {
		if _, err := s.resolveCurrentAdmin(snapshotCtx, actor); err != nil {
			return err
		}
		record, err := s.repo.ReadHostingEntitlement(snapshotCtx, targetUserID)
		if err != nil {
			return err
		}
		result = hostingEntitlementFromRecord(record)
		return nil
	})
	if err != nil {
		return HostingEntitlement{}, hostingEntitlementError(err)
	}
	return result, nil
}

func (s *HostingEntitlementService) Update(
	ctx context.Context,
	input HostingEntitlementUpdateInput,
) (HostingEntitlementUpdateResult, error) {
	var result HostingEntitlementUpdateResult
	actorUserID, ok := input.Actor.UserID()
	if !ok || input.Actor.AuthMethod() != authz.AuthMethodJWT {
		return result, ErrHostingActorNotAuthorized
	}
	if input.TargetUserID <= 0 {
		return result, infraerrors.BadRequest("INVALID_USER_ID", "user_id must be positive")
	}
	if input.ExpectedVersion < 0 || input.AccountLimit < 0 || input.GroupLimit < 0 {
		return result, infraerrors.BadRequest(
			"INVALID_HOSTING_ENTITLEMENT",
			"expected_version, account_limit, and group_limit must be nonnegative",
		)
	}
	if s == nil || s.repo == nil || s.resolver == nil || ctx == nil {
		return result, ErrHostingEntitlementUnavailable
	}

	err := s.repo.WithHostingEntitlementTx(ctx, func(txCtx context.Context) error {
		if err := s.repo.LockHostingEntitlementSubjects(txCtx, actorUserID, input.TargetUserID); err != nil {
			return err
		}
		if _, err := s.resolveCurrentAdmin(txCtx, input.Actor); err != nil {
			return err
		}

		previous, err := s.repo.ReadHostingEntitlement(txCtx, input.TargetUserID)
		if err != nil {
			return err
		}
		if previous.Version != input.ExpectedVersion {
			return ErrHostingEntitlementConflict.WithMetadata(map[string]string{
				"expected_version": strconv.FormatInt(input.ExpectedVersion, 10),
				"current_version":  strconv.FormatInt(previous.Version, 10),
			})
		}

		mutation, err := s.repo.ApplyHostingEntitlement(txCtx, HostingEntitlementMutationInput{
			ActorUserID:  actorUserID,
			TargetUserID: input.TargetUserID,
			Hoster:       input.Hoster,
			AccountLimit: input.AccountLimit,
			GroupLimit:   input.GroupLimit,
			Current:      previous,
		})
		if err != nil {
			return err
		}

		current, err := s.repo.ReadHostingEntitlement(txCtx, input.TargetUserID)
		if err != nil {
			return err
		}
		result.HostingEntitlement = hostingEntitlementFromRecord(current)
		result.Changed = mutation.Changed
		if !mutation.Changed {
			return nil
		}
		if err := s.repo.AppendHostingEntitlementAudit(
			txCtx,
			actorUserID,
			previous,
			current,
			boundedHostingEntitlementAuditTrace(input.AuditTrace),
		); err != nil {
			return fmt.Errorf("append hosting entitlement audit: %w", err)
		}
		return nil
	})
	if err != nil {
		return HostingEntitlementUpdateResult{}, hostingEntitlementError(err)
	}
	return result, nil
}

func (s *HostingEntitlementService) RequireCreateCapacity(
	ctx context.Context,
	actor authz.Actor,
	resourceType authz.ResourceType,
) (HostingCapacity, error) {
	var result HostingCapacity
	userID, ok := actor.UserID()
	if !ok || actor.AuthMethod() != authz.AuthMethodJWT {
		return result, ErrHostingQualificationRequired
	}
	if resourceType != authz.ResourceTypeAccount && resourceType != authz.ResourceTypeGroup {
		return result, infraerrors.BadRequest("INVALID_RESOURCE_TYPE", "resource_type must be account or group")
	}
	if s == nil || s.repo == nil || s.resolver == nil || s.policy == nil || ctx == nil {
		return result, ErrHostingEntitlementUnavailable
	}

	record, err := s.repo.LockHostingCapacity(ctx, userID, resourceType)
	if err != nil {
		return result, hostingEntitlementError(err)
	}
	currentActor, err := s.resolver.ResolveUser(ctx, userID, authz.AuthMethodJWT)
	if err != nil {
		return result, hostingEntitlementError(err)
	}
	if !actor.SameAuthorizationState(currentActor) {
		return result, ErrHostingEntitlementConflict
	}
	if !record.UserActive || !record.Hoster {
		return result, ErrHostingQualificationRequired
	}

	decision, policyErr := s.policy.CanCreate(ctx, currentActor, resourceType)
	if policyErr != nil {
		return result, ErrHostingEntitlementUnavailable.WithCause(policyErr)
	}
	if !decision.Allowed() {
		return result, ErrHostingCreateForbidden.WithMetadata(map[string]string{
			"reason": string(decision.DenyReason()),
		})
	}

	result = HostingCapacity{
		UserID:       userID,
		ResourceType: resourceType,
		Version:      record.Version,
	}
	switch resourceType {
	case authz.ResourceTypeAccount:
		result.Limit = record.AccountLimit
		result.Usage = record.AccountUsage
	case authz.ResourceTypeGroup:
		result.Limit = record.GroupLimit
		result.Usage = record.GroupUsage
	}
	result.Remaining = remainingCapacity(result.Limit, result.Usage)
	if result.Usage >= result.Limit {
		return HostingCapacity{}, ErrHostingQuotaExceeded.WithMetadata(map[string]string{
			"resource_type": string(resourceType),
			"limit":         strconv.FormatInt(result.Limit, 10),
			"usage":         strconv.FormatInt(result.Usage, 10),
			"version":       strconv.FormatInt(result.Version, 10),
		})
	}
	return result, nil
}

func (s *HostingEntitlementService) resolveCurrentAdmin(
	ctx context.Context,
	actor authz.Actor,
) (authz.Actor, error) {
	var (
		current authz.Actor
		err     error
	)
	if userID, ok := actor.UserID(); ok && actor.AuthMethod() == authz.AuthMethodJWT {
		current, err = s.resolver.ResolveLegacyAdminUser(ctx, userID)
	} else if _, ok := actor.ServicePrincipalID(); ok && actor.AuthMethod() == authz.AuthMethodAdminAPIKey {
		current, err = s.resolver.ResolveServicePrincipal(
			ctx,
			authz.AdminAPIKeyServicePrincipalCode,
			authz.AuthMethodAdminAPIKey,
		)
	} else {
		return authz.Actor{}, ErrHostingActorNotAuthorized
	}
	if err != nil {
		return authz.Actor{}, hostingEntitlementError(err)
	}
	if !actor.SameAuthorizationState(current) {
		return authz.Actor{}, ErrHostingEntitlementConflict
	}
	return current, nil
}

func hostingEntitlementFromRecord(record HostingEntitlementRecord) HostingEntitlement {
	return HostingEntitlement{
		UserID:           record.UserID,
		Hoster:           record.Hoster,
		AccountLimit:     record.AccountLimit,
		AccountUsage:     record.AccountUsage,
		AccountRemaining: remainingCapacity(record.AccountLimit, record.AccountUsage),
		GroupLimit:       record.GroupLimit,
		GroupUsage:       record.GroupUsage,
		GroupRemaining:   remainingCapacity(record.GroupLimit, record.GroupUsage),
		Version:          record.Version,
		AuthzVersion:     record.AuthzVersion,
		CreatedByUserID:  cloneHostingInt64Pointer(record.CreatedByUserID),
		UpdatedByUserID:  cloneHostingInt64Pointer(record.UpdatedByUserID),
		CreatedAt:        cloneHostingTimePointer(record.CreatedAt),
		UpdatedAt:        cloneHostingTimePointer(record.UpdatedAt),
	}
}

func remainingCapacity(limit, usage int64) int64 {
	if limit <= usage {
		return 0
	}
	return limit - usage
}

func boundedHostingEntitlementAuditTrace(trace HostingEntitlementAuditTrace) HostingEntitlementAuditTrace {
	trace.RequestID = truncateHostingRunes(strings.TrimSpace(trace.RequestID), 64)
	trace.ClientIP = truncateHostingRunes(strings.TrimSpace(trace.ClientIP), 64)
	trace.UserAgent = truncateHostingRunes(strings.TrimSpace(trace.UserAgent), 512)
	return trace
}

func truncateHostingRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func cloneHostingInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func cloneHostingTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}

func hostingEntitlementError(err error) error {
	if err == nil {
		return nil
	}
	var applicationErr *infraerrors.ApplicationError
	if errors.As(err, &applicationErr) {
		return err
	}
	if errors.Is(err, ErrUserNotFound) {
		return ErrUserNotFound
	}
	if errors.Is(err, authz.ErrActorInactive) || errors.Is(err, authz.ErrPolicyAccessDenied) ||
		errors.Is(err, authz.ErrInvalidActor) {
		return ErrHostingActorNotAuthorized
	}
	if errors.Is(err, authz.ErrAuthorizationUnavailable) {
		return ErrHostingEntitlementUnavailable.WithCause(err)
	}
	return ErrHostingEntitlementUnavailable.WithCause(err)
}
