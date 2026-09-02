package service

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type redeemManagementActorRepoStub struct {
	RedeemCodeRepository
	listCalls   int
	getCalls    int
	deleteCalls int
	updateCalls int
}

func (s *redeemManagementActorRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string) ([]RedeemCode, *pagination.PaginationResult, error) {
	s.listCalls++
	groupID := int64(17)
	return []RedeemCode{{ID: 1, Code: "GROUP-CODE", GroupID: &groupID}}, &pagination.PaginationResult{Total: 1}, nil
}

func (s *redeemManagementActorRepoStub) GetByID(context.Context, int64) (*RedeemCode, error) {
	s.getCalls++
	groupID := int64(17)
	return &RedeemCode{ID: 1, Code: "GROUP-CODE", GroupID: &groupID}, nil
}

func (s *redeemManagementActorRepoStub) Delete(context.Context, int64) error {
	s.deleteCalls++
	return nil
}

func (s *redeemManagementActorRepoStub) Update(context.Context, *RedeemCode) error {
	s.updateCalls++
	return nil
}

func (s *redeemManagementActorRepoStub) totalCalls() int {
	return s.listCalls + s.getCalls + s.deleteCalls + s.updateCalls
}

func TestRedeemManagementAdminFacadesRejectMissingActorBeforeRepositoryAccess(t *testing.T) {
	repo := &redeemManagementActorRepoStub{}
	svc := &adminServiceImpl{redeemCodeRepo: repo}
	ctx := context.Background()
	missingActor := authz.Actor{}

	operations := []struct {
		name string
		call func() error
	}{
		{name: "list", call: func() error {
			_, _, err := svc.AdminListRedeemCodes(ctx, missingActor, 1, 20, "", "", "", "id", "desc")
			return err
		}},
		{name: "get", call: func() error { _, err := svc.AdminGetRedeemCode(ctx, missingActor, 0); return err }},
		{name: "delete", call: func() error { return svc.AdminDeleteRedeemCode(ctx, missingActor, 0) }},
		{name: "batch delete", call: func() error { _, err := svc.AdminBatchDeleteRedeemCodes(ctx, missingActor, nil); return err }},
		{name: "expire", call: func() error { _, err := svc.AdminExpireRedeemCode(ctx, missingActor, 0); return err }},
	}

	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			require.ErrorIs(t, operation.call(), ErrAdminResourceActorUnavailable)
		})
	}
	require.Zero(t, repo.totalCalls())
}

func TestRedeemManagementAdminFacadesAcceptTrustedActorKinds(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		actor authz.Actor
	}{
		{name: "jwt user", actor: adminResourceTestActor(t, authz.SubjectKindUser, 41)},
		{name: "admin api key service principal", actor: adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 73)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &redeemManagementActorRepoStub{}
			svc := &adminServiceImpl{redeemCodeRepo: repo}
			ctx := context.Background()

			codes, total, err := svc.AdminListRedeemCodes(ctx, testCase.actor, 1, 20, "", "", "", "id", "desc")
			require.NoError(t, err)
			require.Equal(t, int64(1), total)
			require.Equal(t, int64(17), *codes[0].GroupID)
			code, err := svc.AdminGetRedeemCode(ctx, testCase.actor, 1)
			require.NoError(t, err)
			require.Equal(t, int64(17), *code.GroupID)
			require.NoError(t, svc.AdminDeleteRedeemCode(ctx, testCase.actor, 1))
			deleted, err := svc.AdminBatchDeleteRedeemCodes(ctx, testCase.actor, []int64{2, 3})
			require.NoError(t, err)
			require.Equal(t, int64(2), deleted)
			expired, err := svc.AdminExpireRedeemCode(ctx, testCase.actor, 1)
			require.NoError(t, err)
			require.Equal(t, StatusExpired, expired.Status)
			require.Equal(t, 7, repo.totalCalls())
		})
	}
}

type settingAdminActorRepoStub struct {
	values map[string]string
	calls  int
}

func (s *settingAdminActorRepoStub) Get(context.Context, string) (*Setting, error) {
	s.calls++
	return nil, ErrSettingNotFound
}

func (s *settingAdminActorRepoStub) GetValue(_ context.Context, key string) (string, error) {
	s.calls++
	return s.values[key], nil
}

func (s *settingAdminActorRepoStub) Set(context.Context, string, string) error {
	s.calls++
	return nil
}

func (s *settingAdminActorRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	s.calls++
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (s *settingAdminActorRepoStub) SetMultiple(_ context.Context, updates map[string]string) error {
	s.calls++
	if s.values == nil {
		s.values = make(map[string]string, len(updates))
	}
	for key, value := range updates {
		s.values[key] = value
	}
	return nil
}

func (s *settingAdminActorRepoStub) GetAll(context.Context) (map[string]string, error) {
	s.calls++
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out, nil
}

func (s *settingAdminActorRepoStub) Delete(context.Context, string) error {
	s.calls++
	return nil
}

func TestSettingAdminFacadesRejectMissingActorBeforeRepositoryAccess(t *testing.T) {
	repo := &settingAdminActorRepoStub{}
	svc := NewSettingService(repo, &config.Config{})
	ctx := context.Background()
	missingActor := authz.Actor{}

	_, err := svc.AdminGetAllSettings(ctx, missingActor)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	_, err = svc.AdminGetAuthSourceDefaultSettings(ctx, missingActor)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	err = svc.AdminUpdateSettingsWithAuthSourceDefaultsOmitting(ctx, missingActor, nil, nil, nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Zero(t, repo.calls)
}

func TestSettingAdminFacadesAcceptTrustedActorKinds(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		actor authz.Actor
	}{
		{name: "jwt user", actor: adminResourceTestActor(t, authz.SubjectKindUser, 41)},
		{name: "admin api key service principal", actor: adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 73)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &settingAdminActorRepoStub{values: map[string]string{
				SettingKeyDefaultSubscriptions:                `[{"group_id":17,"validity_days":30}]`,
				SettingKeyAuthSourceDefaultEmailSubscriptions: `[{"group_id":19,"validity_days":14}]`,
			}}
			svc := NewSettingService(repo, &config.Config{})
			ctx := context.Background()

			settings, err := svc.AdminGetAllSettings(ctx, testCase.actor)
			require.NoError(t, err)
			require.Equal(t, int64(17), settings.DefaultSubscriptions[0].GroupID)
			authDefaults, err := svc.AdminGetAuthSourceDefaultSettings(ctx, testCase.actor)
			require.NoError(t, err)
			require.Equal(t, int64(19), authDefaults.Email.Subscriptions[0].GroupID)
			err = svc.AdminUpdateSettingsWithAuthSourceDefaultsOmitting(ctx, testCase.actor, &SystemSettings{}, &AuthSourceDefaultSettings{}, nil)
			require.NoError(t, err)
			require.Positive(t, repo.calls)
		})
	}
}

func TestRedeemAndSettingAdminFacadesValidateActorAtEntry(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	require.True(t, ok)
	packageDir := filepath.Dir(currentFile)
	targetsByFile := map[string]map[string]bool{
		"admin_redeem_resource_actor.go": {
			"AdminListRedeemCodes":        false,
			"AdminGetRedeemCode":          false,
			"AdminDeleteRedeemCode":       false,
			"AdminBatchDeleteRedeemCodes": false,
			"AdminExpireRedeemCode":       false,
		},
		"setting_admin_resource_actor.go": {
			"AdminGetAllSettings":                               false,
			"AdminGetAuthSourceDefaultSettings":                 false,
			"AdminUpdateSettingsWithAuthSourceDefaultsOmitting": false,
		},
	}

	for filename, targets := range targetsByFile {
		parsed, err := parser.ParseFile(token.NewFileSet(), filepath.Join(packageDir, filename), nil, 0)
		require.NoError(t, err)
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || function.Body == nil {
				continue
			}
			if _, wanted := targets[function.Name.Name]; !wanted {
				continue
			}
			targets[function.Name.Name] = true
			require.NotEmpty(t, function.Body.List, "%s.%s has no body", filename, function.Name.Name)
			conditional, ok := function.Body.List[0].(*ast.IfStmt)
			require.True(t, ok, "%s.%s must validate Actor in its first statement", filename, function.Name.Name)
			assignment, ok := conditional.Init.(*ast.AssignStmt)
			require.True(t, ok, "%s.%s first statement must assign ValidateAdminResourceActor", filename, function.Name.Name)
			require.Len(t, assignment.Rhs, 1)
			call, ok := assignment.Rhs[0].(*ast.CallExpr)
			require.True(t, ok)
			validator, ok := call.Fun.(*ast.Ident)
			require.True(t, ok)
			require.Equal(t, "ValidateAdminResourceActor", validator.Name)
		}
		for method, found := range targets {
			require.Truef(t, found, "%s.%s facade missing", filename, method)
		}
	}
}
