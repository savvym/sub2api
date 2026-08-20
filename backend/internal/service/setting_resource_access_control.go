package service

import (
	"context"
	"log/slog"
	"strings"
)

// ResourceAccessControlRuntimeSettings is the effective, fail-closed view of
// the resource authorization dark-launch configuration. Callers must consume
// this view instead of reading the individual setting keys.
type ResourceAccessControlRuntimeSettings struct {
	ResourceAccessControlEnabled   bool
	SelfServiceHostingEnabled      bool
	GroupSharingEnabled            bool
	AccountSharingEnabled          bool
	RoleBasedResourceGrantsEnabled bool
	RoleAuthorizationMode          string
}

var resourceAccessControlSettingKeys = []string{
	SettingKeyResourceAccessControlEnabled,
	SettingKeySelfServiceHostingEnabled,
	SettingKeyGroupSharingEnabled,
	SettingKeyAccountSharingEnabled,
	SettingKeyRoleBasedResourceGrantsEnabled,
	SettingKeyRoleAuthorizationMode,
}

// GetResourceAccessControlRuntimeSettings returns the effective feature state.
// Missing or unreadable settings fail closed. Advanced resource features are
// effective only while the master resource access control switch is enabled;
// sharing also requires self-service hosting. Backend Mode routing remains a
// separate, higher-priority guard.
func (s *SettingService) GetResourceAccessControlRuntimeSettings(ctx context.Context) ResourceAccessControlRuntimeSettings {
	closed := ResourceAccessControlRuntimeSettings{
		RoleAuthorizationMode: RoleAuthorizationModeLegacy,
	}
	if s == nil || s.settingRepo == nil {
		return closed
	}

	values, err := s.settingRepo.GetMultiple(ctx, resourceAccessControlSettingKeys)
	if err != nil {
		slog.Warn("failed to load resource access control runtime settings", "error", err)
		return closed
	}

	masterEnabled := values[SettingKeyResourceAccessControlEnabled] == "true"
	selfServiceEnabled := masterEnabled && values[SettingKeySelfServiceHostingEnabled] == "true"
	roleAuthorizationMode, validMode := parseRoleAuthorizationMode(values[SettingKeyRoleAuthorizationMode])
	if !validMode {
		slog.Warn("invalid role authorization mode; falling back to legacy",
			"value", values[SettingKeyRoleAuthorizationMode],
		)
	}
	return ResourceAccessControlRuntimeSettings{
		ResourceAccessControlEnabled:   masterEnabled,
		SelfServiceHostingEnabled:      selfServiceEnabled,
		GroupSharingEnabled:            selfServiceEnabled && values[SettingKeyGroupSharingEnabled] == "true",
		AccountSharingEnabled:          selfServiceEnabled && values[SettingKeyAccountSharingEnabled] == "true",
		RoleBasedResourceGrantsEnabled: masterEnabled && values[SettingKeyRoleBasedResourceGrantsEnabled] == "true",
		RoleAuthorizationMode:          roleAuthorizationMode,
	}
}

func normalizeRoleAuthorizationMode(value string) string {
	mode, _ := parseRoleAuthorizationMode(value)
	return mode
}

func parseRoleAuthorizationMode(value string) (string, bool) {
	switch strings.TrimSpace(value) {
	case "", RoleAuthorizationModeLegacy:
		return RoleAuthorizationModeLegacy, true
	case RoleAuthorizationModeShadow:
		return RoleAuthorizationModeShadow, true
	case RoleAuthorizationModeRBAC:
		return RoleAuthorizationModeRBAC, true
	default:
		return RoleAuthorizationModeLegacy, false
	}
}
