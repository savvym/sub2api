//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type roleUpdateUserRepoStub struct {
	*userRepoStub
	lastUpdated *User
	lastFields  UserUpdateFields
	updateCalls int
}

func (s *roleUpdateUserRepoStub) Update(_ context.Context, user *User, fields UserUpdateFields) error {
	s.updateCalls++
	s.lastFields = fields
	if user != nil {
		if !fields.IsEmpty() {
			user.UpdatedAt = time.Date(2026, time.August, 20, 12, 35, 0, 0, time.UTC)
		}
		clone := *user
		s.lastUpdated = &clone
		s.userRepoStub.user = &clone
	}
	return nil
}

func TestAdminService_UpdateUser_RoleAndAdjacentFieldKeepRepositoryTimestamp(t *testing.T) {
	roleRepo := newRoleRepositoryFake()
	roleRepo.subjects[42] = RoleSubject{ID: 42, LegacyRole: RoleUser, Status: StatusActive}
	roleUpdatedAt := time.Date(2026, time.August, 20, 12, 34, 56, 0, time.UTC)
	roleRepo.reconcileResult = LegacyRoleMutationResult{Changed: true, AuthzVersion: 2, UpdatedAt: roleUpdatedAt}
	svc, _ := newAdminRoleTestService(
		&User{ID: 42, Email: "u@example.com", Role: RoleUser},
		roleRepo,
		nil,
	)
	username := "renamed"

	updated, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{
		Role:         RoleAdmin,
		Username:     &username,
		ActorAdminID: 1,
	})

	require.NoError(t, err)
	require.Equal(t, time.Date(2026, time.August, 20, 12, 35, 0, 0, time.UTC), updated.UpdatedAt)
}

func newAdminRoleTestService(target *User, roleRepo *roleRepositoryFake, invalidator APIKeyAuthCacheInvalidator) (*adminServiceImpl, *roleUpdateUserRepoStub) {
	userRepo := &roleUpdateUserRepoStub{userRepoStub: &userRepoStub{user: target}}
	return &adminServiceImpl{
		userRepo:             userRepo,
		roleService:          NewRoleService(roleRepo),
		redeemCodeRepo:       &redeemRepoStub{},
		authCacheInvalidator: invalidator,
	}, userRepo
}

func TestAdminService_CreateUser_WithAdminRole(t *testing.T) {
	repo := &userRepoStub{nextID: 30}
	svc := &adminServiceImpl{userRepo: repo}

	user, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:    "admin@test.com",
		Password: "strong-pass",
		Role:     RoleAdmin,
	})
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, user.Role)
}

func TestAdminService_CreateUser_DefaultsToUserRole(t *testing.T) {
	repo := &userRepoStub{nextID: 31}
	svc := &adminServiceImpl{userRepo: repo}

	user, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:    "plain@test.com",
		Password: "strong-pass",
	})
	require.NoError(t, err)
	require.Equal(t, RoleUser, user.Role)
}

func TestAdminService_CreateUser_InvalidRoleRejected(t *testing.T) {
	repo := &userRepoStub{nextID: 32}
	svc := &adminServiceImpl{userRepo: repo}

	_, err := svc.CreateUser(context.Background(), &CreateUserInput{
		Email:    "bad@test.com",
		Password: "strong-pass",
		Role:     "superuser",
	})
	require.Error(t, err)
	require.Empty(t, repo.created, "非法角色不应写入用户")
}

func TestAdminService_UpdateUser_PromoteToAdmin(t *testing.T) {
	roleRepo := newRoleRepositoryFake()
	roleRepo.subjects[42] = RoleSubject{ID: 42, LegacyRole: RoleUser, Status: StatusActive}
	roleUpdatedAt := time.Date(2026, time.August, 20, 12, 34, 56, 0, time.UTC)
	roleRepo.reconcileResult = LegacyRoleMutationResult{Changed: true, AuthzVersion: 2, UpdatedAt: roleUpdatedAt}
	invalidator := &authCacheInvalidatorStub{}
	svc, userRepo := newAdminRoleTestService(
		&User{ID: 42, Email: "u@example.com", Role: RoleUser},
		roleRepo,
		invalidator,
	)

	updated, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{
		Role:         RoleAdmin,
		ActorAdminID: 1,
	})
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, updated.Role)
	require.Equal(t, roleUpdatedAt, updated.UpdatedAt, "pure role updates return the committed database timestamp")
	require.Equal(t, 1, roleRepo.reconcileCalls)
	require.Equal(t, RoleUser, userRepo.lastUpdated.Role, "legacy role is persisted only by RoleRepository")
	require.Equal(t, []int64{42}, invalidator.userIDs, "角色变更应失效认证缓存")
}

func TestAdminService_UpdateUser_DisabledUserCannotBePromotedWithoutReactivation(t *testing.T) {
	roleRepo := newRoleRepositoryFake()
	roleRepo.subjects[42] = RoleSubject{ID: 42, LegacyRole: RoleUser, Status: StatusDisabled}
	svc, userRepo := newAdminRoleTestService(
		&User{ID: 42, Email: "disabled@example.com", Role: RoleUser, Status: StatusDisabled},
		roleRepo,
		nil,
	)

	_, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{
		Role:         RoleAdmin,
		ActorAdminID: 1,
	})

	require.ErrorIs(t, err, ErrAdminCannotBeDisabled)
	require.Zero(t, roleRepo.txCalls)
	require.Zero(t, userRepo.updateCalls)
}

func TestAdminService_UpdateUser_RoleOmittedKeepsExisting(t *testing.T) {
	roleRepo := newRoleRepositoryFake()
	svc, userRepo := newAdminRoleTestService(
		&User{ID: 42, Email: "u@example.com", Role: RoleAdmin},
		roleRepo,
		nil,
	)

	newName := "renamed"
	updated, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{Username: &newName})
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, updated.Role, "未提供 role 时不应改变现有角色")
	require.Zero(t, roleRepo.txCalls, "omitting role must bypass RoleService")
	require.True(t, userRepo.lastFields.Username)
}

func TestAdminService_UpdateUser_InvalidRoleRejected(t *testing.T) {
	roleRepo := newRoleRepositoryFake()
	svc, userRepo := newAdminRoleTestService(
		&User{ID: 42, Email: "u@example.com", Role: RoleUser},
		roleRepo,
		nil,
	)

	_, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{Role: "root", ActorAdminID: 1})
	require.Error(t, err)
	require.Nil(t, userRepo.lastUpdated, "非法角色不应触发持久化")
	require.Zero(t, roleRepo.txCalls)
}

func TestAdminService_UpdateUser_DemoteLastAdminRejected(t *testing.T) {
	roleRepo := newRoleRepositoryFake()
	roleRepo.subjects[42] = RoleSubject{ID: 42, LegacyRole: RoleAdmin, Status: StatusActive}
	roleRepo.adminCount = 1
	svc, userRepo := newAdminRoleTestService(
		&User{ID: 42, Email: "a@example.com", Role: RoleAdmin},
		roleRepo,
		nil,
	)

	_, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{
		Role:         RoleUser,
		ActorAdminID: 1,
	})
	require.ErrorIs(t, err, ErrLastAdminDemotion)
	require.Nil(t, userRepo.lastUpdated, "最后一个管理员不应被降级持久化")
	require.Equal(t, 1, roleRepo.countCalls, "降级路径应在 RoleRepository 事务内重算管理员")
}

func TestAdminService_UpdateUser_DemoteAdminAllowedWhenOthersExist(t *testing.T) {
	roleRepo := newRoleRepositoryFake()
	roleRepo.subjects[42] = RoleSubject{ID: 42, LegacyRole: RoleAdmin, Status: StatusActive}
	roleRepo.adminCount = 2
	roleRepo.reconcileResult = LegacyRoleMutationResult{Changed: true, AuthzVersion: 5}
	invalidator := &authCacheInvalidatorStub{}
	svc, userRepo := newAdminRoleTestService(
		&User{ID: 42, Email: "a@example.com", Role: RoleAdmin},
		roleRepo,
		invalidator,
	)

	updated, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{
		Role:         RoleUser,
		ActorAdminID: 1,
	})
	require.NoError(t, err)
	require.Equal(t, RoleUser, updated.Role)
	require.NotNil(t, userRepo.lastUpdated)
	require.Equal(t, RoleAdmin, userRepo.lastUpdated.Role, "UserRepository only receives the pre-change role snapshot")
	require.Equal(t, 1, roleRepo.countCalls)
	require.Equal(t, 1, roleRepo.reconcileCalls)
}

func TestAdminService_UpdateUser_PromoteDoesNotCountAdmins(t *testing.T) {
	roleRepo := newRoleRepositoryFake()
	roleRepo.subjects[42] = RoleSubject{ID: 42, LegacyRole: RoleUser, Status: StatusActive}
	roleRepo.adminCount = 1
	roleRepo.reconcileResult = LegacyRoleMutationResult{Changed: true, AuthzVersion: 7}
	svc, _ := newAdminRoleTestService(
		&User{ID: 42, Email: "u@example.com", Role: RoleUser},
		roleRepo,
		&authCacheInvalidatorStub{},
	)

	updated, err := svc.UpdateUser(context.Background(), 42, &UpdateUserInput{
		Role:         RoleAdmin,
		ActorAdminID: 1,
	})
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, updated.Role)
	require.Equal(t, 0, roleRepo.countCalls, "升级路径不应触发管理员计数")
}
