//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type roleServiceTxContextKey struct{}
type roleServiceSnapshotContextKey struct{}

type roleRepositoryFake struct {
	mode       string
	subjects   map[int64]RoleSubject
	adminCount int64
	readiness  RoleAuthorizationReadiness

	reconcileResult LegacyRoleMutationResult
	reconcileUserID int64
	reconcileFrom   string
	reconcileTo     string
	setMode         string
	readinessTarget string
	callOrder       []string
	auditActor      RoleSubject
	auditFrom       string
	auditTo         string
	auditTrace      RoleAuthorizationModeAuditTrace
	auditErr        error

	snapshotCalls  int
	txCalls        int
	lockCalls      int
	countCalls     int
	reconcileCalls int
	readinessCalls int
	setModeCalls   int
	auditCalls     int
}

func newRoleRepositoryFake() *roleRepositoryFake {
	return &roleRepositoryFake{
		mode: RoleAuthorizationModeLegacy,
		subjects: map[int64]RoleSubject{
			1: {
				ID:         1,
				Email:      "admin@example.com",
				LegacyRole: RoleAdmin,
				Status:     StatusActive,
			},
		},
		adminCount: 2,
	}
}

func (f *roleRepositoryFake) requireTx(ctx context.Context) {
	if ctx.Value(roleServiceTxContextKey{}) != true {
		panic("role repository method called outside role-management transaction")
	}
}

func (f *roleRepositoryFake) requireSnapshot(ctx context.Context) {
	if ctx.Value(roleServiceSnapshotContextKey{}) != true {
		panic("role repository method called outside authorization snapshot")
	}
}

func (f *roleRepositoryFake) WithRoleAuthorizationSnapshot(ctx context.Context, fn func(context.Context) error) error {
	f.snapshotCalls++
	return fn(context.WithValue(ctx, roleServiceSnapshotContextKey{}, true))
}

func (f *roleRepositoryFake) GetAuthorizationMode(ctx context.Context) (string, error) {
	f.requireSnapshot(ctx)
	return f.mode, nil
}

func (f *roleRepositoryFake) ReadRoleSubjects(ctx context.Context, userIDs []int64) (map[int64]RoleSubject, error) {
	f.requireSnapshot(ctx)
	f.callOrder = append(f.callOrder, "read_subjects")
	result := make(map[int64]RoleSubject, len(userIDs))
	for _, userID := range userIDs {
		if subject, ok := f.subjects[userID]; ok {
			result[userID] = subject
		}
	}
	return result, nil
}

func (f *roleRepositoryFake) InspectAuthorizationReadinessSnapshot(
	ctx context.Context,
	targetMode string,
) (RoleAuthorizationReadiness, error) {
	f.requireSnapshot(ctx)
	f.readinessCalls++
	f.readinessTarget = targetMode
	f.callOrder = append(f.callOrder, "readiness_snapshot")
	return f.readiness, nil
}

func (f *roleRepositoryFake) WithRoleManagementTx(ctx context.Context, fn func(context.Context) error) error {
	f.txCalls++
	modeBefore := f.mode
	err := fn(context.WithValue(ctx, roleServiceTxContextKey{}, true))
	if err != nil {
		f.mode = modeBefore
	}
	return err
}

func (f *roleRepositoryFake) GetAuthorizationModeForUpdate(ctx context.Context) (string, error) {
	f.requireTx(ctx)
	return f.mode, nil
}

func (f *roleRepositoryFake) LockRoleSubjects(ctx context.Context, userIDs []int64) (map[int64]RoleSubject, error) {
	f.requireTx(ctx)
	f.lockCalls++
	f.callOrder = append(f.callOrder, "lock_subjects")
	locked := make(map[int64]RoleSubject, len(userIDs))
	for _, userID := range userIDs {
		if subject, ok := f.subjects[userID]; ok {
			locked[userID] = subject
		}
	}
	return locked, nil
}

func (f *roleRepositoryFake) CountActiveLegacyAdmins(ctx context.Context) (int64, error) {
	f.requireTx(ctx)
	f.countCalls++
	return f.adminCount, nil
}

func (f *roleRepositoryFake) ReconcileLegacyRole(ctx context.Context, userID int64, expectedRole, desiredRole string) (LegacyRoleMutationResult, error) {
	f.requireTx(ctx)
	f.reconcileCalls++
	f.reconcileUserID = userID
	f.reconcileFrom = expectedRole
	f.reconcileTo = desiredRole
	return f.reconcileResult, nil
}

func (f *roleRepositoryFake) InspectAuthorizationReadiness(ctx context.Context, targetMode string) (RoleAuthorizationReadiness, error) {
	f.requireTx(ctx)
	f.readinessCalls++
	f.readinessTarget = targetMode
	f.callOrder = append(f.callOrder, "readiness")
	return f.readiness, nil
}

func (f *roleRepositoryFake) SetAuthorizationMode(ctx context.Context, mode string) error {
	f.requireTx(ctx)
	f.setModeCalls++
	f.setMode = mode
	f.mode = mode
	f.callOrder = append(f.callOrder, "set_mode")
	return nil
}

func (f *roleRepositoryFake) AppendAuthorizationModeTransitionAudit(
	ctx context.Context,
	actor RoleSubject,
	previousMode string,
	currentMode string,
	trace RoleAuthorizationModeAuditTrace,
) error {
	f.requireTx(ctx)
	f.auditCalls++
	f.auditActor = actor
	f.auditFrom = previousMode
	f.auditTo = currentMode
	f.auditTrace = trace
	f.callOrder = append(f.callOrder, "audit")
	return f.auditErr
}

func TestRoleService_ChangeLegacyRole_ActiveAdminCanPromoteUser(t *testing.T) {
	repo := newRoleRepositoryFake()
	repo.subjects[2] = RoleSubject{ID: 2, LegacyRole: RoleUser, Status: StatusActive}
	repo.reconcileResult = LegacyRoleMutationResult{Changed: true, AuthzVersion: 8}
	svc := NewRoleService(repo)
	callbackCalls := 0

	result, err := svc.ChangeLegacyRole(context.Background(), LegacyRoleChangeInput{
		ActorUserID:        1,
		TargetUserID:       2,
		ExpectedLegacyRole: RoleUser,
		DesiredLegacyRole:  RoleAdmin,
	}, func(ctx context.Context) error {
		repo.requireTx(ctx)
		callbackCalls++
		return nil
	})

	require.NoError(t, err)
	require.Equal(t, LegacyRoleMutationResult{Changed: true, AuthzVersion: 8}, result)
	require.Equal(t, 1, callbackCalls)
	require.Equal(t, 1, repo.reconcileCalls)
	require.Equal(t, int64(2), repo.reconcileUserID)
	require.Equal(t, RoleUser, repo.reconcileFrom)
	require.Equal(t, RoleAdmin, repo.reconcileTo)
	require.Zero(t, repo.countCalls, "promotion must not count administrators")
}

func TestRoleService_ChangeLegacyRole_RejectsPromotionOfDisabledUser(t *testing.T) {
	repo := newRoleRepositoryFake()
	repo.subjects[2] = RoleSubject{ID: 2, LegacyRole: RoleUser, Status: StatusDisabled}
	svc := NewRoleService(repo)

	_, err := svc.ChangeLegacyRole(context.Background(), LegacyRoleChangeInput{
		ActorUserID:        1,
		TargetUserID:       2,
		ExpectedLegacyRole: RoleUser,
		DesiredLegacyRole:  RoleAdmin,
	}, nil)

	require.ErrorIs(t, err, ErrAdminCannotBeDisabled)
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Equal(t, "ADMIN_CANNOT_BE_DISABLED", infraerrors.Reason(err))
	require.Zero(t, repo.reconcileCalls)
}

func TestRoleService_ChangeLegacyRole_RequiresActiveAdminActor(t *testing.T) {
	tests := []struct {
		name  string
		actor RoleSubject
		found bool
	}{
		{name: "missing", found: false},
		{name: "regular user", actor: RoleSubject{ID: 1, LegacyRole: RoleUser, Status: StatusActive}, found: true},
		{name: "disabled admin", actor: RoleSubject{ID: 1, LegacyRole: RoleAdmin, Status: StatusDisabled}, found: true},
		{name: "deleted admin", actor: RoleSubject{ID: 1, LegacyRole: RoleAdmin, Status: StatusActive, Deleted: true}, found: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newRoleRepositoryFake()
			if tt.found {
				repo.subjects[1] = tt.actor
			} else {
				delete(repo.subjects, 1)
			}
			repo.subjects[2] = RoleSubject{ID: 2, LegacyRole: RoleUser, Status: StatusActive}
			svc := NewRoleService(repo)

			_, err := svc.ChangeLegacyRole(context.Background(), LegacyRoleChangeInput{
				ActorUserID:        1,
				TargetUserID:       2,
				ExpectedLegacyRole: RoleUser,
				DesiredLegacyRole:  RoleAdmin,
			}, nil)

			require.ErrorIs(t, err, ErrRoleActorNotAuthorized)
			require.Equal(t, http.StatusForbidden, infraerrors.Code(err))
			require.Zero(t, repo.reconcileCalls)
		})
	}
}

func TestRoleService_ChangeLegacyRole_RejectsStaleExpectedRoleWithConflict(t *testing.T) {
	repo := newRoleRepositoryFake()
	repo.subjects[2] = RoleSubject{ID: 2, LegacyRole: RoleAdmin, Status: StatusActive}
	svc := NewRoleService(repo)
	callbackCalls := 0

	_, err := svc.ChangeLegacyRole(context.Background(), LegacyRoleChangeInput{
		ActorUserID:        1,
		TargetUserID:       2,
		ExpectedLegacyRole: RoleUser,
		DesiredLegacyRole:  RoleAdmin,
	}, func(context.Context) error {
		callbackCalls++
		return nil
	})

	require.ErrorIs(t, err, ErrRoleMutationConflict)
	require.Equal(t, http.StatusConflict, infraerrors.Code(err))
	require.Equal(t, "ROLE_MUTATION_CONFLICT", infraerrors.Reason(err))
	require.Equal(t, map[string]string{
		"expected_role": RoleUser,
		"current_role":  RoleAdmin,
	}, infraerrors.FromError(err).Metadata)
	require.Zero(t, repo.reconcileCalls)
	require.Zero(t, callbackCalls)
}

func TestRoleService_ChangeLegacyRole_RejectsAdminSelfDemotion(t *testing.T) {
	repo := newRoleRepositoryFake()
	svc := NewRoleService(repo)

	_, err := svc.ChangeLegacyRole(context.Background(), LegacyRoleChangeInput{
		ActorUserID:        1,
		TargetUserID:       1,
		ExpectedLegacyRole: RoleAdmin,
		DesiredLegacyRole:  RoleUser,
	}, nil)

	require.ErrorIs(t, err, ErrAdminSelfDemotion)
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Zero(t, repo.countCalls)
	require.Zero(t, repo.reconcileCalls)
}

func TestRoleService_ChangeLegacyRole_RejectsLastAdminDemotion(t *testing.T) {
	repo := newRoleRepositoryFake()
	repo.subjects[2] = RoleSubject{ID: 2, LegacyRole: RoleAdmin, Status: StatusActive}
	repo.adminCount = 1
	svc := NewRoleService(repo)

	_, err := svc.ChangeLegacyRole(context.Background(), LegacyRoleChangeInput{
		ActorUserID:        1,
		TargetUserID:       2,
		ExpectedLegacyRole: RoleAdmin,
		DesiredLegacyRole:  RoleUser,
	}, nil)

	require.ErrorIs(t, err, ErrLastAdminDemotion)
	require.Equal(t, http.StatusConflict, infraerrors.Code(err))
	require.Equal(t, 1, repo.countCalls)
	require.Zero(t, repo.reconcileCalls)
}

func TestRoleService_ChangeLegacyRole_AllowsDemotionWhenAnotherAdminRemains(t *testing.T) {
	repo := newRoleRepositoryFake()
	repo.subjects[2] = RoleSubject{ID: 2, LegacyRole: RoleAdmin, Status: StatusActive}
	repo.adminCount = 2
	repo.reconcileResult = LegacyRoleMutationResult{Changed: true, AuthzVersion: 11}
	svc := NewRoleService(repo)

	result, err := svc.ChangeLegacyRole(context.Background(), LegacyRoleChangeInput{
		ActorUserID:        1,
		TargetUserID:       2,
		ExpectedLegacyRole: RoleAdmin,
		DesiredLegacyRole:  RoleUser,
	}, nil)

	require.NoError(t, err)
	require.Equal(t, LegacyRoleMutationResult{Changed: true, AuthzVersion: 11}, result)
	require.Equal(t, 1, repo.countCalls)
	require.Equal(t, 1, repo.reconcileCalls)
}

func TestRoleService_ChangeLegacyRole_NoOpStillRunsAdjacentMutation(t *testing.T) {
	repo := newRoleRepositoryFake()
	repo.subjects[2] = RoleSubject{ID: 2, LegacyRole: RoleUser, Status: StatusActive}
	repo.reconcileResult = LegacyRoleMutationResult{Changed: false, AuthzVersion: 4}
	svc := NewRoleService(repo)
	callbackCalls := 0

	result, err := svc.ChangeLegacyRole(context.Background(), LegacyRoleChangeInput{
		ActorUserID:        1,
		TargetUserID:       2,
		ExpectedLegacyRole: RoleUser,
		DesiredLegacyRole:  RoleUser,
	}, func(ctx context.Context) error {
		repo.requireTx(ctx)
		callbackCalls++
		return nil
	})

	require.NoError(t, err)
	require.False(t, result.Changed)
	require.Equal(t, int64(4), result.AuthzVersion)
	require.Equal(t, 1, repo.reconcileCalls)
	require.Equal(t, 1, callbackCalls)
}

func TestRoleService_RBACHardStop(t *testing.T) {
	t.Run("legacy role mutation while current mode is rbac", func(t *testing.T) {
		repo := newRoleRepositoryFake()
		repo.mode = RoleAuthorizationModeRBAC
		repo.subjects[2] = RoleSubject{ID: 2, LegacyRole: RoleUser, Status: StatusActive}
		svc := NewRoleService(repo)

		_, err := svc.ChangeLegacyRole(context.Background(), LegacyRoleChangeInput{
			ActorUserID:        1,
			TargetUserID:       2,
			ExpectedLegacyRole: RoleUser,
			DesiredLegacyRole:  RoleAdmin,
		}, nil)

		require.ErrorIs(t, err, ErrRoleAuthorizationUnavailable)
		require.Equal(t, http.StatusConflict, infraerrors.Code(err))
		require.Equal(t, map[string]string{"mode": RoleAuthorizationModeRBAC}, infraerrors.FromError(err).Metadata)
		require.Zero(t, repo.lockCalls)
		require.Zero(t, repo.reconcileCalls)
	})

	t.Run("transition to rbac is rejected before transaction", func(t *testing.T) {
		repo := newRoleRepositoryFake()
		svc := NewRoleService(repo)

		_, err := svc.TransitionAuthorizationMode(context.Background(), RoleAuthorizationModeTransitionInput{
			ActorUserID:  1,
			ExpectedMode: RoleAuthorizationModeLegacy,
			TargetMode:   RoleAuthorizationModeRBAC,
		})

		require.ErrorIs(t, err, ErrRBACConsumersNotMigrated)
		require.Zero(t, repo.txCalls)
		require.Zero(t, repo.setModeCalls)
		require.Zero(t, repo.auditCalls)
	})
}

func TestRoleService_GetAuthorizationModeStatus_ReturnsNextHopReadiness(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		readiness  RoleAuthorizationReadiness
		targetMode string
		canChange  bool
	}{
		{
			name:       "legacy to shadow ready",
			mode:       RoleAuthorizationModeLegacy,
			readiness:  RoleAuthorizationReadiness{Blockers: []RoleAuthorizationReadinessBlocker{}},
			targetMode: RoleAuthorizationModeShadow,
			canChange:  true,
		},
		{
			name: "shadow to legacy blocked",
			mode: RoleAuthorizationModeShadow,
			readiness: RoleAuthorizationReadiness{Blockers: []RoleAuthorizationReadinessBlocker{
				{Code: RoleReadinessServicePrincipalRoleUnmappable, Count: 2},
			}},
			targetMode: RoleAuthorizationModeLegacy,
			canChange:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newRoleRepositoryFake()
			repo.mode = tt.mode
			repo.readiness = tt.readiness
			svc := NewRoleService(repo)

			status, err := svc.GetAuthorizationModeStatus(context.Background(), 1)

			require.NoError(t, err)
			require.Equal(t, RoleAuthorizationModeStatus{
				CurrentMode:   tt.mode,
				TargetMode:    tt.targetMode,
				Readiness:     tt.readiness,
				CanTransition: tt.canChange,
			}, status)
			require.Equal(t, tt.targetMode, repo.readinessTarget)
			require.Equal(t, []string{"read_subjects", "readiness_snapshot"}, repo.callOrder)
			require.Equal(t, 1, repo.snapshotCalls)
			require.Zero(t, repo.txCalls)
			require.Zero(t, repo.setModeCalls)
			require.Zero(t, repo.auditCalls)
		})
	}
}

func TestRoleService_GetAuthorizationModeStatus_RequiresActiveAdmin(t *testing.T) {
	repo := newRoleRepositoryFake()
	repo.subjects[1] = RoleSubject{ID: 1, Email: "user@example.com", LegacyRole: RoleUser, Status: StatusActive}
	svc := NewRoleService(repo)

	_, err := svc.GetAuthorizationModeStatus(context.Background(), 1)

	require.ErrorIs(t, err, ErrRoleActorNotAuthorized)
	require.Equal(t, []string{"read_subjects"}, repo.callOrder)
	require.Equal(t, 1, repo.snapshotCalls)
	require.Zero(t, repo.txCalls)
	require.Zero(t, repo.readinessCalls)
	require.Zero(t, repo.setModeCalls)
	require.Zero(t, repo.auditCalls)
}

func TestRoleService_GetAuthorizationModeStatus_RBACHardStop(t *testing.T) {
	repo := newRoleRepositoryFake()
	repo.mode = RoleAuthorizationModeRBAC
	svc := NewRoleService(repo)

	status, err := svc.GetAuthorizationModeStatus(context.Background(), 1)

	require.ErrorIs(t, err, ErrRBACConsumersNotMigrated)
	require.Equal(t, RoleAuthorizationModeRBAC, status.CurrentMode)
	require.Empty(t, status.TargetMode)
	require.False(t, status.CanTransition)
	require.Equal(t, []string{"read_subjects"}, repo.callOrder)
	require.Equal(t, 1, repo.snapshotCalls)
	require.Zero(t, repo.txCalls)
	require.Zero(t, repo.readinessCalls)
	require.Zero(t, repo.auditCalls)
}

func TestRoleService_TransitionAuthorizationMode_LegacyToShadowRequiresReadiness(t *testing.T) {
	t.Run("blocked", func(t *testing.T) {
		repo := newRoleRepositoryFake()
		repo.readiness = RoleAuthorizationReadiness{Blockers: []RoleAuthorizationReadinessBlocker{
			{Code: RoleReadinessSystemRoleMissing, Count: 2},
			{Code: RoleReadinessMigrationMissing, Count: 1},
		}}
		svc := NewRoleService(repo)

		result, err := svc.TransitionAuthorizationMode(context.Background(), RoleAuthorizationModeTransitionInput{
			ActorUserID:  1,
			ExpectedMode: RoleAuthorizationModeLegacy,
			TargetMode:   RoleAuthorizationModeShadow,
		})

		require.ErrorIs(t, err, ErrRoleAuthorizationModeNotReady)
		require.Equal(t, http.StatusConflict, infraerrors.Code(err))
		require.Equal(t, "MIGRATION_MISSING:1,SYSTEM_ROLE_MISSING:2", infraerrors.FromError(err).Metadata["blockers"])
		require.Equal(t, repo.readiness, result.Readiness)
		require.Equal(t, RoleAuthorizationModeLegacy, result.PreviousMode)
		require.Equal(t, RoleAuthorizationModeLegacy, result.CurrentMode)
		require.Equal(t, 1, repo.readinessCalls)
		require.Equal(t, RoleAuthorizationModeShadow, repo.readinessTarget)
		require.Equal(t, []string{"readiness", "lock_subjects"}, repo.callOrder)
		require.Zero(t, repo.setModeCalls)
		require.Zero(t, repo.auditCalls)
	})

	t.Run("ready", func(t *testing.T) {
		repo := newRoleRepositoryFake()
		svc := NewRoleService(repo)

		trace := RoleAuthorizationModeAuditTrace{
			RequestID: "request-123",
			ClientIP:  "203.0.113.7",
			UserAgent: "role-mode-test",
		}
		result, err := svc.TransitionAuthorizationMode(context.Background(), RoleAuthorizationModeTransitionInput{
			ActorUserID:  1,
			ExpectedMode: RoleAuthorizationModeLegacy,
			TargetMode:   RoleAuthorizationModeShadow,
			AuditTrace:   trace,
		})

		require.NoError(t, err)
		require.Equal(t, RoleAuthorizationModeTransitionResult{
			PreviousMode: RoleAuthorizationModeLegacy,
			CurrentMode:  RoleAuthorizationModeShadow,
			Changed:      true,
			Readiness:    RoleAuthorizationReadiness{Blockers: []RoleAuthorizationReadinessBlocker{}},
		}, result)
		require.Equal(t, 1, repo.readinessCalls)
		require.Equal(t, RoleAuthorizationModeShadow, repo.readinessTarget)
		require.Equal(t, []string{"readiness", "lock_subjects", "set_mode", "audit"}, repo.callOrder)
		require.Equal(t, 1, repo.setModeCalls)
		require.Equal(t, RoleAuthorizationModeShadow, repo.setMode)
		require.Equal(t, 1, repo.auditCalls)
		require.Equal(t, repo.subjects[1], repo.auditActor)
		require.Equal(t, RoleAuthorizationModeLegacy, repo.auditFrom)
		require.Equal(t, RoleAuthorizationModeShadow, repo.auditTo)
		require.Equal(t, trace, repo.auditTrace)
	})
}

func TestRoleService_TransitionAuthorizationMode_ShadowToLegacyRequiresReadiness(t *testing.T) {
	t.Run("blocked", func(t *testing.T) {
		repo := newRoleRepositoryFake()
		repo.mode = RoleAuthorizationModeShadow
		repo.readiness = RoleAuthorizationReadiness{Blockers: []RoleAuthorizationReadinessBlocker{
			{Code: RoleReadinessServicePrincipalRoleUnmappable, Count: 1},
		}}
		svc := NewRoleService(repo)

		result, err := svc.TransitionAuthorizationMode(context.Background(), RoleAuthorizationModeTransitionInput{
			ActorUserID:  1,
			ExpectedMode: RoleAuthorizationModeShadow,
			TargetMode:   RoleAuthorizationModeLegacy,
		})

		require.ErrorIs(t, err, ErrRoleAuthorizationModeNotReady)
		require.False(t, result.Changed)
		require.Equal(t, RoleAuthorizationModeShadow, result.PreviousMode)
		require.Equal(t, RoleAuthorizationModeShadow, result.CurrentMode)
		require.Equal(t, RoleAuthorizationModeLegacy, repo.readinessTarget)
		require.Equal(t, []string{"readiness", "lock_subjects"}, repo.callOrder)
		require.Zero(t, repo.setModeCalls)
		require.Zero(t, repo.auditCalls)
	})

	t.Run("ready", func(t *testing.T) {
		repo := newRoleRepositoryFake()
		repo.mode = RoleAuthorizationModeShadow
		svc := NewRoleService(repo)

		result, err := svc.TransitionAuthorizationMode(context.Background(), RoleAuthorizationModeTransitionInput{
			ActorUserID:  1,
			ExpectedMode: RoleAuthorizationModeShadow,
			TargetMode:   RoleAuthorizationModeLegacy,
		})

		require.NoError(t, err)
		require.True(t, result.Changed)
		require.Equal(t, RoleAuthorizationModeShadow, result.PreviousMode)
		require.Equal(t, RoleAuthorizationModeLegacy, result.CurrentMode)
		require.Equal(t, RoleAuthorizationModeLegacy, repo.readinessTarget)
		require.Equal(t, []string{"readiness", "lock_subjects", "set_mode", "audit"}, repo.callOrder)
		require.Equal(t, RoleAuthorizationModeLegacy, repo.setMode)
		require.Equal(t, 1, repo.auditCalls)
	})
}

func TestRoleService_TransitionAuthorizationMode_UsesExpectedModeCAS(t *testing.T) {
	repo := newRoleRepositoryFake()
	repo.mode = RoleAuthorizationModeShadow
	svc := NewRoleService(repo)

	result, err := svc.TransitionAuthorizationMode(context.Background(), RoleAuthorizationModeTransitionInput{
		ActorUserID:  1,
		ExpectedMode: RoleAuthorizationModeLegacy,
		TargetMode:   RoleAuthorizationModeShadow,
	})

	require.ErrorIs(t, err, ErrRoleMutationConflict)
	require.Equal(t, http.StatusConflict, infraerrors.Code(err))
	require.Equal(t, map[string]string{
		"expected_mode": RoleAuthorizationModeLegacy,
		"current_mode":  RoleAuthorizationModeShadow,
	}, infraerrors.FromError(err).Metadata)
	require.Equal(t, RoleAuthorizationModeShadow, result.PreviousMode)
	require.Equal(t, RoleAuthorizationModeShadow, result.CurrentMode)
	require.Zero(t, repo.readinessCalls)
	require.Zero(t, repo.setModeCalls)
	require.Zero(t, repo.auditCalls)
}

func TestRoleService_TransitionAuthorizationMode_RequiresActiveAdmin(t *testing.T) {
	repo := newRoleRepositoryFake()
	repo.subjects[1] = RoleSubject{ID: 1, Email: "user@example.com", LegacyRole: RoleUser, Status: StatusActive}
	svc := NewRoleService(repo)

	result, err := svc.TransitionAuthorizationMode(context.Background(), RoleAuthorizationModeTransitionInput{
		ActorUserID:  1,
		ExpectedMode: RoleAuthorizationModeLegacy,
		TargetMode:   RoleAuthorizationModeShadow,
	})

	require.ErrorIs(t, err, ErrRoleActorNotAuthorized)
	require.False(t, result.Changed)
	require.Equal(t, RoleAuthorizationModeLegacy, result.PreviousMode)
	require.Equal(t, RoleAuthorizationModeLegacy, result.CurrentMode)
	require.Zero(t, repo.setModeCalls)
	require.Zero(t, repo.auditCalls)
}

func TestRoleService_TransitionAuthorizationMode_AuditFailureRollsBackTransition(t *testing.T) {
	auditFailure := errors.New("audit insert rejected")
	repo := newRoleRepositoryFake()
	repo.auditErr = auditFailure
	svc := NewRoleService(repo)

	result, err := svc.TransitionAuthorizationMode(context.Background(), RoleAuthorizationModeTransitionInput{
		ActorUserID:  1,
		ExpectedMode: RoleAuthorizationModeLegacy,
		TargetMode:   RoleAuthorizationModeShadow,
		AuditTrace: RoleAuthorizationModeAuditTrace{
			RequestID: "rollback-request",
		},
	})

	require.ErrorIs(t, err, auditFailure)
	require.False(t, result.Changed)
	require.Equal(t, RoleAuthorizationModeLegacy, result.PreviousMode)
	require.Equal(t, RoleAuthorizationModeLegacy, result.CurrentMode)
	require.Equal(t, RoleAuthorizationModeLegacy, repo.mode)
	require.Equal(t, []string{"readiness", "lock_subjects", "set_mode", "audit"}, repo.callOrder)
	require.Equal(t, 1, repo.setModeCalls)
	require.Equal(t, 1, repo.auditCalls)
}
