//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type resourceAccessControlSettingRepoStub struct {
	values         map[string]string
	getMultipleErr error
	updates        map[string]string
}

func (s *resourceAccessControlSettingRepoStub) Get(context.Context, string) (*Setting, error) {
	panic("unexpected Get call")
}

func (s *resourceAccessControlSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	value, ok := s.values[key]
	if !ok {
		return "", ErrSettingNotFound
	}
	return value, nil
}

func (s *resourceAccessControlSettingRepoStub) Set(context.Context, string, string) error {
	panic("unexpected Set call")
}

func (s *resourceAccessControlSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	if s.getMultipleErr != nil {
		return nil, s.getMultipleErr
	}
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func (s *resourceAccessControlSettingRepoStub) SetMultiple(_ context.Context, settings map[string]string) error {
	if s.values == nil {
		s.values = make(map[string]string)
	}
	s.updates = make(map[string]string, len(settings))
	for key, value := range settings {
		s.values[key] = value
		s.updates[key] = value
	}
	return nil
}

func (s *resourceAccessControlSettingRepoStub) GetAll(context.Context) (map[string]string, error) {
	result := make(map[string]string, len(s.values))
	for key, value := range s.values {
		result[key] = value
	}
	return result, nil
}

func (s *resourceAccessControlSettingRepoStub) Delete(context.Context, string) error {
	panic("unexpected Delete call")
}

func TestResourceAccessControlRuntimeSettings_DefaultsFailClosed(t *testing.T) {
	tests := []struct {
		name string
		svc  *SettingService
	}{
		{name: "nil service"},
		{
			name: "missing settings",
			svc: NewSettingService(&resourceAccessControlSettingRepoStub{
				values: map[string]string{},
			}, &config.Config{}),
		},
		{
			name: "storage error",
			svc: NewSettingService(&resourceAccessControlSettingRepoStub{
				getMultipleErr: errors.New("database unavailable"),
			}, &config.Config{}),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.svc.GetResourceAccessControlRuntimeSettings(context.Background())
			require.Equal(t, ResourceAccessControlRuntimeSettings{
				RoleAuthorizationMode: RoleAuthorizationModeLegacy,
			}, got)
		})
	}
}

func TestResourceAccessControlRuntimeSettings_MasterSwitchConstrainsAdvancedFeatures(t *testing.T) {
	repo := &resourceAccessControlSettingRepoStub{values: map[string]string{
		SettingKeyResourceAccessControlEnabled:   "false",
		SettingKeySelfServiceHostingEnabled:      "true",
		SettingKeyGroupSharingEnabled:            "true",
		SettingKeyAccountSharingEnabled:          "true",
		SettingKeyRoleBasedResourceGrantsEnabled: "true",
		SettingKeyRoleAuthorizationMode:          RoleAuthorizationModeShadow,
	}}
	svc := NewSettingService(repo, &config.Config{})

	got := svc.GetResourceAccessControlRuntimeSettings(context.Background())
	require.False(t, got.ResourceAccessControlEnabled)
	require.False(t, got.SelfServiceHostingEnabled)
	require.False(t, got.GroupSharingEnabled)
	require.False(t, got.AccountSharingEnabled)
	require.False(t, got.RoleBasedResourceGrantsEnabled)
	require.Equal(t, RoleAuthorizationModeShadow, got.RoleAuthorizationMode)
}

func TestResourceAccessControlRuntimeSettings_SharingRequiresSelfServiceHosting(t *testing.T) {
	repo := &resourceAccessControlSettingRepoStub{values: map[string]string{
		SettingKeyResourceAccessControlEnabled:   "true",
		SettingKeySelfServiceHostingEnabled:      "false",
		SettingKeyGroupSharingEnabled:            "true",
		SettingKeyAccountSharingEnabled:          "true",
		SettingKeyRoleBasedResourceGrantsEnabled: "true",
	}}
	svc := NewSettingService(repo, &config.Config{})

	got := svc.GetResourceAccessControlRuntimeSettings(context.Background())
	require.True(t, got.ResourceAccessControlEnabled)
	require.False(t, got.SelfServiceHostingEnabled)
	require.False(t, got.GroupSharingEnabled)
	require.False(t, got.AccountSharingEnabled)
	require.True(t, got.RoleBasedResourceGrantsEnabled,
		"role-based resource grants depend on the ACL master switch, not self-service hosting")
}

func TestResourceAccessControlRuntimeSettings_EnabledConfiguration(t *testing.T) {
	repo := &resourceAccessControlSettingRepoStub{values: map[string]string{
		SettingKeyResourceAccessControlEnabled:   "true",
		SettingKeySelfServiceHostingEnabled:      "true",
		SettingKeyGroupSharingEnabled:            "true",
		SettingKeyAccountSharingEnabled:          "true",
		SettingKeyRoleBasedResourceGrantsEnabled: "true",
		SettingKeyRoleAuthorizationMode:          RoleAuthorizationModeRBAC,
	}}
	svc := NewSettingService(repo, &config.Config{})

	require.Equal(t, ResourceAccessControlRuntimeSettings{
		ResourceAccessControlEnabled:   true,
		SelfServiceHostingEnabled:      true,
		GroupSharingEnabled:            true,
		AccountSharingEnabled:          true,
		RoleBasedResourceGrantsEnabled: true,
		RoleAuthorizationMode:          RoleAuthorizationModeRBAC,
	}, svc.GetResourceAccessControlRuntimeSettings(context.Background()))
}

func TestResourceAccessControlRuntimeSettings_InvalidModeFallsBackToLegacy(t *testing.T) {
	repo := &resourceAccessControlSettingRepoStub{values: map[string]string{
		SettingKeyResourceAccessControlEnabled:   "true",
		SettingKeySelfServiceHostingEnabled:      "true",
		SettingKeyGroupSharingEnabled:            "true",
		SettingKeyAccountSharingEnabled:          "true",
		SettingKeyRoleBasedResourceGrantsEnabled: "true",
		SettingKeyRoleAuthorizationMode:          "acl",
	}}
	svc := NewSettingService(repo, &config.Config{})

	require.Equal(t, ResourceAccessControlRuntimeSettings{
		ResourceAccessControlEnabled:   true,
		SelfServiceHostingEnabled:      true,
		GroupSharingEnabled:            true,
		AccountSharingEnabled:          true,
		RoleBasedResourceGrantsEnabled: true,
		RoleAuthorizationMode:          RoleAuthorizationModeLegacy,
	}, svc.GetResourceAccessControlRuntimeSettings(context.Background()))
}

func TestResourceAccessControlRuntimeSettings_NonCanonicalBooleanFailsClosed(t *testing.T) {
	repo := &resourceAccessControlSettingRepoStub{values: map[string]string{
		SettingKeyResourceAccessControlEnabled:   "TRUE",
		SettingKeySelfServiceHostingEnabled:      "true",
		SettingKeyGroupSharingEnabled:            "true",
		SettingKeyAccountSharingEnabled:          "true",
		SettingKeyRoleBasedResourceGrantsEnabled: "true",
	}}
	svc := NewSettingService(repo, &config.Config{})

	require.Equal(t, ResourceAccessControlRuntimeSettings{
		RoleAuthorizationMode: RoleAuthorizationModeLegacy,
	}, svc.GetResourceAccessControlRuntimeSettings(context.Background()))
}

func TestResourceAccessControlSettings_DefaultParsingAndPersistence(t *testing.T) {
	t.Run("missing values parse to safe defaults", func(t *testing.T) {
		svc := NewSettingService(&resourceAccessControlSettingRepoStub{
			values: map[string]string{},
		}, &config.Config{})

		got, err := svc.GetAllSettings(context.Background())
		require.NoError(t, err)
		require.False(t, got.ResourceAccessControlEnabled)
		require.False(t, got.SelfServiceHostingEnabled)
		require.False(t, got.GroupSharingEnabled)
		require.False(t, got.AccountSharingEnabled)
		require.False(t, got.RoleBasedResourceGrantsEnabled)
		require.Equal(t, RoleAuthorizationModeLegacy, got.RoleAuthorizationMode)
	})

	t.Run("fresh-install defaults are explicit", func(t *testing.T) {
		repo := &resourceAccessControlSettingRepoStub{values: map[string]string{}}
		svc := NewSettingService(repo, &config.Config{})

		require.NoError(t, svc.InitializeDefaultSettings(context.Background()))
		require.Equal(t, "false", repo.values[SettingKeyResourceAccessControlEnabled])
		require.Equal(t, "false", repo.values[SettingKeySelfServiceHostingEnabled])
		require.Equal(t, "false", repo.values[SettingKeyGroupSharingEnabled])
		require.Equal(t, "false", repo.values[SettingKeyAccountSharingEnabled])
		require.Equal(t, "false", repo.values[SettingKeyRoleBasedResourceGrantsEnabled])
		require.Equal(t, RoleAuthorizationModeLegacy, repo.values[SettingKeyRoleAuthorizationMode])
	})

	t.Run("generic updates preserve guarded role mode", func(t *testing.T) {
		repo := &resourceAccessControlSettingRepoStub{values: map[string]string{
			SettingKeyRoleAuthorizationMode: RoleAuthorizationModeLegacy,
		}}
		svc := NewSettingService(repo, &config.Config{})

		err := svc.UpdateSettings(context.Background(), &SystemSettings{
			ResourceAccessControlEnabled:   true,
			SelfServiceHostingEnabled:      true,
			GroupSharingEnabled:            true,
			AccountSharingEnabled:          true,
			RoleBasedResourceGrantsEnabled: true,
			RoleAuthorizationMode:          RoleAuthorizationModeShadow,
		})
		require.NoError(t, err)
		require.Equal(t, "true", repo.updates[SettingKeyResourceAccessControlEnabled])
		require.Equal(t, "true", repo.updates[SettingKeySelfServiceHostingEnabled])
		require.Equal(t, "true", repo.updates[SettingKeyGroupSharingEnabled])
		require.Equal(t, "true", repo.updates[SettingKeyAccountSharingEnabled])
		require.Equal(t, "true", repo.updates[SettingKeyRoleBasedResourceGrantsEnabled])
		_, wroteMode := repo.updates[SettingKeyRoleAuthorizationMode]
		require.False(t, wroteMode)
		require.Equal(t, RoleAuthorizationModeLegacy, repo.values[SettingKeyRoleAuthorizationMode])
	})

	t.Run("empty mode is not persisted", func(t *testing.T) {
		repo := &resourceAccessControlSettingRepoStub{values: map[string]string{
			SettingKeyRoleAuthorizationMode: RoleAuthorizationModeShadow,
		}}
		svc := NewSettingService(repo, &config.Config{})

		require.NoError(t, svc.UpdateSettings(context.Background(), &SystemSettings{}))
		_, wroteMode := repo.updates[SettingKeyRoleAuthorizationMode]
		require.False(t, wroteMode)
		require.Equal(t, RoleAuthorizationModeShadow, repo.values[SettingKeyRoleAuthorizationMode])
	})

	t.Run("invalid mode cannot bypass guarded transition", func(t *testing.T) {
		repo := &resourceAccessControlSettingRepoStub{values: map[string]string{
			SettingKeyRoleAuthorizationMode: RoleAuthorizationModeLegacy,
		}}
		svc := NewSettingService(repo, &config.Config{})

		err := svc.UpdateSettings(context.Background(), &SystemSettings{
			RoleAuthorizationMode: "acl",
		})
		require.NoError(t, err)
		_, wroteMode := repo.updates[SettingKeyRoleAuthorizationMode]
		require.False(t, wroteMode)
		require.Equal(t, RoleAuthorizationModeLegacy, repo.values[SettingKeyRoleAuthorizationMode])
	})
}
