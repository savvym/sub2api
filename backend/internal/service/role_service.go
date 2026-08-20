package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrRoleAuthorizationUnavailable = infraerrors.Conflict(
		"ROLE_AUTHORIZATION_UNAVAILABLE",
		"role authorization management is unavailable",
	)
	ErrRoleActorNotAuthorized = infraerrors.Forbidden(
		"ROLE_ACTOR_NOT_AUTHORIZED",
		"an active administrator is required to manage roles",
	)
	ErrRoleMutationConflict = infraerrors.Conflict(
		"ROLE_MUTATION_CONFLICT",
		"the user's role changed before this request was applied",
	)
	ErrLastAdminDemotion = infraerrors.Conflict(
		"LAST_ADMIN_DEMOTION",
		"cannot demote the last active administrator",
	)
	ErrAdminSelfDemotion = infraerrors.BadRequest(
		"ADMIN_SELF_DEMOTION",
		"cannot demote yourself from administrator",
	)
	ErrAdminCannotBeDisabled = infraerrors.BadRequest(
		"ADMIN_CANNOT_BE_DISABLED",
		"an administrator must remain active",
	)
	ErrRoleAuthorizationModeTransitionRequired = infraerrors.Conflict(
		"ROLE_AUTHORIZATION_MODE_TRANSITION_REQUIRED",
		"role_authorization_mode can only be changed through the guarded transition workflow",
	)
	ErrRoleAuthorizationModeNotReady = infraerrors.Conflict(
		"ROLE_AUTHORIZATION_MODE_NOT_READY",
		"role authorization data is not ready for the requested mode",
	)
	ErrRBACConsumersNotMigrated = infraerrors.Conflict(
		"RBAC_CONSUMERS_NOT_MIGRATED",
		"RBAC authorization cannot be enabled until all authorization consumers are migrated",
	)
)

const (
	RoleReadinessMigrationMissing                = "MIGRATION_MISSING"
	RoleReadinessSystemRoleMissing               = "SYSTEM_ROLE_MISSING"
	RoleReadinessBootstrapPrincipalMissing       = "BOOTSTRAP_PRINCIPAL_MISSING"
	RoleReadinessLegacyRoleUnmappable            = "LEGACY_ROLE_UNMAPPABLE"
	RoleReadinessCompatibilityRoleMissing        = "COMPATIBILITY_ROLE_MISSING"
	RoleReadinessStaleBootstrapCompatibilityRole = "STALE_BOOTSTRAP_COMPATIBILITY_ROLE"
	RoleReadinessSubjectVersionInvalid           = "SUBJECT_VERSION_INVALID"
	RoleReadinessRoleVersionInvalid              = "ROLE_VERSION_INVALID"
	RoleReadinessRBACAdminLegacyMismatch         = "RBAC_ADMIN_LEGACY_MISMATCH"
	RoleReadinessServicePrincipalRoleUnmappable  = "SERVICE_PRINCIPAL_ROLE_UNMAPPABLE"
)

// RoleSubject is the narrow user projection needed by role-management commands.
type RoleSubject struct {
	ID           int64
	LegacyRole   string
	Status       string
	AuthzVersion int64
	Deleted      bool
}

// LegacyRoleMutationResult describes a committed compatibility-role mutation.
type LegacyRoleMutationResult struct {
	Changed      bool
	AuthzVersion int64
	UpdatedAt    time.Time
}

// RoleAuthorizationReadinessBlocker is a stable, count-based transition blocker.
type RoleAuthorizationReadinessBlocker struct {
	Code  string `json:"code"`
	Count int64  `json:"count"`
}

// RoleAuthorizationReadiness is evaluated from a stable database snapshot.
type RoleAuthorizationReadiness struct {
	Blockers []RoleAuthorizationReadinessBlocker `json:"blockers"`
}

func (r RoleAuthorizationReadiness) Ready() bool {
	return len(r.Blockers) == 0
}

type LegacyRoleChangeInput struct {
	ActorUserID        int64
	TargetUserID       int64
	ExpectedLegacyRole string
	DesiredLegacyRole  string
	// DesiredStatus is the target's status after the surrounding user mutation.
	// Empty means the status currently locked in the database remains unchanged.
	DesiredStatus string
}

type RoleAuthorizationModeTransitionInput struct {
	ActorUserID  int64
	ExpectedMode string
	TargetMode   string
}

type RoleAuthorizationModeTransitionResult struct {
	PreviousMode string
	CurrentMode  string
	Changed      bool
	Readiness    RoleAuthorizationReadiness
}

// RoleRepository owns serialization, row locking and the compatibility-role writes.
// Every method except WithRoleManagementTx must be called with the transaction
// context supplied to its callback.
type RoleRepository interface {
	WithRoleManagementTx(ctx context.Context, fn func(txCtx context.Context) error) error
	GetAuthorizationModeForUpdate(ctx context.Context) (string, error)
	LockRoleSubjects(ctx context.Context, userIDs []int64) (map[int64]RoleSubject, error)
	CountActiveLegacyAdmins(ctx context.Context) (int64, error)
	ReconcileLegacyRole(ctx context.Context, userID int64, expectedRole, desiredRole string) (LegacyRoleMutationResult, error)
	InspectAuthorizationReadiness(ctx context.Context, targetMode string) (RoleAuthorizationReadiness, error)
	SetAuthorizationMode(ctx context.Context, mode string) error
}

// RoleService is the sole command path for legacy admin/user role changes and
// role_authorization_mode transitions.
type RoleService struct {
	repo RoleRepository
}

func NewRoleService(repo RoleRepository) *RoleService {
	return &RoleService{repo: repo}
}

// ChangeLegacyRole serializes role changes and lets the caller include adjacent
// user fields in the same transaction. In legacy and shadow modes users.role
// remains authoritative, while the system compatibility assignment is kept in
// lockstep for shadow comparison.
func (s *RoleService) ChangeLegacyRole(
	ctx context.Context,
	input LegacyRoleChangeInput,
	mutateUser func(txCtx context.Context) error,
) (LegacyRoleMutationResult, error) {
	var result LegacyRoleMutationResult
	if s == nil || s.repo == nil {
		return result, ErrRoleAuthorizationUnavailable
	}
	if input.ActorUserID <= 0 || input.TargetUserID <= 0 {
		return result, ErrRoleActorNotAuthorized
	}
	expectedRole, err := normalizeUserRole(input.ExpectedLegacyRole, "")
	if err != nil || expectedRole == "" {
		return result, infraerrors.BadRequest("INVALID_EXPECTED_ROLE", "expected role must be admin or user")
	}
	desiredRole, err := normalizeUserRole(input.DesiredLegacyRole, "")
	if err != nil || desiredRole == "" {
		return result, infraerrors.BadRequest("INVALID_ROLE", "role must be admin or user")
	}
	desiredStatus := strings.TrimSpace(input.DesiredStatus)
	if desiredStatus != "" && desiredStatus != StatusActive && desiredStatus != StatusDisabled {
		return result, infraerrors.BadRequest("INVALID_USER_STATUS", "status must be active or disabled")
	}

	err = s.repo.WithRoleManagementTx(ctx, func(txCtx context.Context) error {
		mode, modeErr := s.repo.GetAuthorizationModeForUpdate(txCtx)
		if modeErr != nil {
			return modeErr
		}
		parsedMode, valid := parseRoleAuthorizationMode(mode)
		if !valid || parsedMode == RoleAuthorizationModeRBAC {
			return ErrRoleAuthorizationUnavailable.WithMetadata(map[string]string{"mode": mode})
		}

		subjects, lockErr := s.repo.LockRoleSubjects(txCtx, []int64{input.ActorUserID, input.TargetUserID})
		if lockErr != nil {
			return lockErr
		}
		actor, ok := subjects[input.ActorUserID]
		if !ok || actor.Deleted || actor.Status != StatusActive || actor.LegacyRole != RoleAdmin {
			return ErrRoleActorNotAuthorized
		}
		target, ok := subjects[input.TargetUserID]
		if !ok || target.Deleted {
			return ErrUserNotFound
		}
		if target.LegacyRole != expectedRole {
			return ErrRoleMutationConflict.WithMetadata(map[string]string{
				"expected_role": expectedRole,
				"current_role":  target.LegacyRole,
			})
		}
		if input.ActorUserID == input.TargetUserID && expectedRole == RoleAdmin && desiredRole == RoleUser {
			return ErrAdminSelfDemotion
		}
		finalStatus := target.Status
		if desiredStatus != "" {
			finalStatus = desiredStatus
		}
		if desiredRole == RoleAdmin && finalStatus != StatusActive {
			return ErrAdminCannotBeDisabled
		}
		if expectedRole == RoleAdmin && desiredRole == RoleUser {
			adminCount, countErr := s.repo.CountActiveLegacyAdmins(txCtx)
			if countErr != nil {
				return fmt.Errorf("count active administrators: %w", countErr)
			}
			if adminCount <= 1 {
				return ErrLastAdminDemotion
			}
		}

		result, err = s.repo.ReconcileLegacyRole(txCtx, input.TargetUserID, expectedRole, desiredRole)
		if err != nil {
			return err
		}
		if mutateUser != nil {
			return mutateUser(txCtx)
		}
		return nil
	})
	return result, err
}

// TransitionAuthorizationMode is the only write path for role_authorization_mode.
// Phase 1.7 intentionally permits only legacy <-> shadow; rbac remains blocked
// until Actor and every authorization consumer have migrated in Phase 1.8.
func (s *RoleService) TransitionAuthorizationMode(
	ctx context.Context,
	input RoleAuthorizationModeTransitionInput,
) (RoleAuthorizationModeTransitionResult, error) {
	var result RoleAuthorizationModeTransitionResult
	if s == nil || s.repo == nil {
		return result, ErrRoleAuthorizationUnavailable
	}
	if input.ActorUserID <= 0 {
		return result, ErrRoleActorNotAuthorized
	}
	expectedMode, expectedValid := parseRoleAuthorizationMode(input.ExpectedMode)
	targetMode, targetValid := parseRoleAuthorizationMode(input.TargetMode)
	if !expectedValid || !targetValid || strings.TrimSpace(input.ExpectedMode) == "" || strings.TrimSpace(input.TargetMode) == "" {
		return result, infraerrors.BadRequest(
			"INVALID_ROLE_AUTHORIZATION_MODE",
			"expected_mode and target_mode must be legacy, shadow, or rbac",
		)
	}
	if expectedMode == RoleAuthorizationModeRBAC || targetMode == RoleAuthorizationModeRBAC {
		return result, ErrRBACConsumersNotMigrated
	}

	err := s.repo.WithRoleManagementTx(ctx, func(txCtx context.Context) error {
		currentRaw, getErr := s.repo.GetAuthorizationModeForUpdate(txCtx)
		if getErr != nil {
			return getErr
		}
		currentMode, valid := parseRoleAuthorizationMode(currentRaw)
		if !valid {
			return ErrRoleAuthorizationUnavailable.WithMetadata(map[string]string{"mode": currentRaw})
		}
		result.PreviousMode = currentMode
		result.CurrentMode = currentMode

		transitionRequested := currentMode != targetMode
		directTransition := (currentMode == RoleAuthorizationModeLegacy && targetMode == RoleAuthorizationModeShadow) ||
			(currentMode == RoleAuthorizationModeShadow && targetMode == RoleAuthorizationModeLegacy)
		if transitionRequested && directTransition {
			// Readiness takes SHARE table locks to establish a stable snapshot. Take
			// them before any users row lock; reversing that order can deadlock with
			// a concurrent UPDATE that already holds RowExclusive and waits on the
			// actor row.
			result.Readiness, getErr = s.repo.InspectAuthorizationReadiness(txCtx, targetMode)
			if getErr != nil {
				return getErr
			}
		}

		subjects, lockErr := s.repo.LockRoleSubjects(txCtx, []int64{input.ActorUserID})
		if lockErr != nil {
			return lockErr
		}
		actor, ok := subjects[input.ActorUserID]
		if !ok || actor.Deleted || actor.Status != StatusActive || actor.LegacyRole != RoleAdmin {
			return ErrRoleActorNotAuthorized
		}
		if currentMode != expectedMode {
			return ErrRoleMutationConflict.WithMetadata(map[string]string{
				"expected_mode": expectedMode,
				"current_mode":  currentMode,
			})
		}
		if !transitionRequested {
			return nil
		}
		if !directTransition {
			return ErrRoleAuthorizationModeTransitionRequired
		}
		if !result.Readiness.Ready() {
			return roleReadinessError(result.Readiness)
		}
		if setErr := s.repo.SetAuthorizationMode(txCtx, targetMode); setErr != nil {
			return setErr
		}
		result.CurrentMode = targetMode
		result.Changed = true
		return nil
	})
	return result, err
}

func roleReadinessError(readiness RoleAuthorizationReadiness) error {
	blockers := append([]RoleAuthorizationReadinessBlocker(nil), readiness.Blockers...)
	sort.Slice(blockers, func(i, j int) bool { return blockers[i].Code < blockers[j].Code })
	parts := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		parts = append(parts, blocker.Code+":"+strconv.FormatInt(blocker.Count, 10))
	}
	return ErrRoleAuthorizationModeNotReady.WithMetadata(map[string]string{
		"blockers": strings.Join(parts, ","),
	})
}
