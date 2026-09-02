package authz

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const OpenAIQuotaAutoResetServicePrincipalCode = "openai_quota_auto_reset_worker"

// WorkerAuthorizationSnapshotInput is the repository boundary for one
// current, mode-independent worker authorization decision.
type WorkerAuthorizationSnapshotInput struct {
	ServicePrincipalID   int64
	ServicePrincipalCode string
	Active               bool
	AuthzVersion         int64
	RoleCount            int
	PermissionCodes      []string
	AccountID            int64
	AccountExists        bool
	AccountDeleted       bool
}

type workerAuthorizationSnapshotMarker struct{}

var trustedWorkerAuthorizationSnapshotMarker = &workerAuthorizationSnapshotMarker{}

// WorkerAuthorizationSnapshot keeps raw direct-permission codes so an unknown
// or additional grant fails closed instead of being discarded during parsing.
type WorkerAuthorizationSnapshot struct {
	servicePrincipalID   int64
	servicePrincipalCode string
	active               bool
	authzVersion         int64
	roleCount            int
	permissionCodes      []string
	accountID            int64
	accountExists        bool
	accountDeleted       bool
	marker               *workerAuthorizationSnapshotMarker
}

func NewWorkerAuthorizationSnapshot(input WorkerAuthorizationSnapshotInput) (WorkerAuthorizationSnapshot, error) {
	input.ServicePrincipalCode = strings.TrimSpace(input.ServicePrincipalCode)
	if input.ServicePrincipalID <= 0 || input.ServicePrincipalCode == "" || len(input.ServicePrincipalCode) > 64 ||
		input.AuthzVersion <= 0 || input.RoleCount < 0 || input.AccountID < 0 {
		return WorkerAuthorizationSnapshot{}, ErrInvalidPolicySnapshot
	}
	if input.AccountID == 0 && (input.AccountExists || input.AccountDeleted) {
		return WorkerAuthorizationSnapshot{}, ErrInvalidPolicySnapshot
	}
	if input.AccountID > 0 && input.AccountDeleted && !input.AccountExists {
		return WorkerAuthorizationSnapshot{}, ErrInvalidPolicySnapshot
	}

	permissionCodes := make([]string, 0, len(input.PermissionCodes))
	seen := make(map[string]struct{}, len(input.PermissionCodes))
	for _, rawCode := range input.PermissionCodes {
		code := strings.TrimSpace(rawCode)
		if code == "" || len(code) > 128 {
			return WorkerAuthorizationSnapshot{}, ErrInvalidPolicySnapshot
		}
		if _, duplicate := seen[code]; duplicate {
			return WorkerAuthorizationSnapshot{}, ErrInvalidPolicySnapshot
		}
		seen[code] = struct{}{}
		permissionCodes = append(permissionCodes, code)
	}

	return WorkerAuthorizationSnapshot{
		servicePrincipalID:   input.ServicePrincipalID,
		servicePrincipalCode: input.ServicePrincipalCode,
		active:               input.Active,
		authzVersion:         input.AuthzVersion,
		roleCount:            input.RoleCount,
		permissionCodes:      permissionCodes,
		accountID:            input.AccountID,
		accountExists:        input.AccountExists,
		accountDeleted:       input.AccountDeleted,
		marker:               trustedWorkerAuthorizationSnapshotMarker,
	}, nil
}

func (s WorkerAuthorizationSnapshot) valid() bool {
	if s.marker != trustedWorkerAuthorizationSnapshotMarker || s.servicePrincipalID <= 0 ||
		s.servicePrincipalCode == "" || len(s.servicePrincipalCode) > 64 || s.authzVersion <= 0 ||
		s.roleCount < 0 || s.accountID < 0 ||
		(s.accountID == 0 && (s.accountExists || s.accountDeleted)) ||
		(s.accountID > 0 && s.accountDeleted && !s.accountExists) {
		return false
	}
	seen := make(map[string]struct{}, len(s.permissionCodes))
	for _, code := range s.permissionCodes {
		if strings.TrimSpace(code) != code || code == "" || len(code) > 128 {
			return false
		}
		if _, duplicate := seen[code]; duplicate {
			return false
		}
		seen[code] = struct{}{}
	}
	return true
}

type WorkerPolicyStore interface {
	LoadWorkerAuthorizationSnapshot(
		ctx context.Context,
		servicePrincipalCode string,
		accountID int64,
	) (WorkerAuthorizationSnapshot, error)
}

// WorkerPolicy is deliberately narrower than ResourcePolicy. The only valid
// operation is the built-in OpenAI quota auto-reset worker operating on an
// existing Account.
type WorkerPolicy interface {
	CheckWorkerCapability(ctx context.Context, actor Actor, capability Capability) error
	AuthorizeWorker(ctx context.Context, actor Actor, capability Capability, action Action, ref ResourceRef) error
}

var _ WorkerPolicy = (*PolicyService)(nil)

func NewWorkerPolicy(store WorkerPolicyStore) WorkerPolicy {
	return &PolicyService{workerStore: store}
}

func (s *PolicyService) CheckWorkerCapability(ctx context.Context, actor Actor, capability Capability) error {
	return s.authorizeOpenAIQuotaAutoResetWorker(ctx, actor, capability, 0)
}

func (s *PolicyService) AuthorizeWorker(
	ctx context.Context,
	actor Actor,
	capability Capability,
	action Action,
	ref ResourceRef,
) error {
	if !action.Valid() || !ref.Valid() || action != ActionAccountOperate || ref.Type() != ResourceTypeAccount {
		return ErrPolicyAccessDenied
	}
	return s.authorizeOpenAIQuotaAutoResetWorker(ctx, actor, capability, ref.ID())
}

func (s *PolicyService) authorizeOpenAIQuotaAutoResetWorker(
	ctx context.Context,
	actor Actor,
	capability Capability,
	accountID int64,
) error {
	principalID, isServicePrincipal := actor.ServicePrincipalID()
	if !isServicePrincipal || actor.AuthMethod() != AuthMethodServicePrincipal ||
		!capability.Valid() || capability != CapabilityPlatformAccountOpenAIQuotaAutoReset || accountID < 0 {
		return ErrPolicyAccessDenied
	}
	if ctx == nil || s == nil || s.workerStore == nil {
		return fmt.Errorf("%w: worker policy is not configured", ErrAuthorizationUnavailable)
	}

	snapshot, err := s.workerStore.LoadWorkerAuthorizationSnapshot(
		ctx,
		OpenAIQuotaAutoResetServicePrincipalCode,
		accountID,
	)
	if err != nil {
		if errors.Is(err, ErrSubjectNotFound) {
			return ErrActorInactive
		}
		return fmt.Errorf("%w: load worker authorization snapshot: %w", ErrAuthorizationUnavailable, err)
	}
	if !snapshot.valid() {
		return fmt.Errorf("%w: invalid worker authorization snapshot", ErrAuthorizationUnavailable)
	}
	capabilities := actor.capabilitiesSnapshot()
	if snapshot.servicePrincipalID != principalID || snapshot.servicePrincipalCode != OpenAIQuotaAutoResetServicePrincipalCode ||
		actor.subjectVersion() != snapshot.authzVersion || len(actor.roleVersionsSnapshot()) != 0 ||
		len(capabilities) != 1 || capabilities[0] != CapabilityPlatformAccountOpenAIQuotaAutoReset {
		return ErrSessionInvalid
	}
	if !snapshot.active {
		return ErrActorInactive
	}
	if snapshot.roleCount != 0 || len(snapshot.permissionCodes) != 1 ||
		snapshot.permissionCodes[0] != string(CapabilityPlatformAccountOpenAIQuotaAutoReset) {
		return ErrPolicyAccessDenied
	}
	if accountID > 0 && (snapshot.accountID != accountID || !snapshot.accountExists || snapshot.accountDeleted) {
		return ErrPolicyAccessDenied
	}
	return nil
}
