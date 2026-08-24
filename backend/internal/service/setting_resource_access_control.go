package service

import (
	"context"
	"log/slog"
	"strconv"
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

type resourceAccessControlExpansionState struct {
	master     bool
	selfServe  bool
	groupShare bool
	acctShare  bool
	roleGrants bool
}

func (s *SettingService) resourceAccessControlUpdateExpands(
	ctx context.Context,
	settings *SystemSettings,
	omitted OmittedSettingKeys,
) (bool, error) {
	if settings == nil {
		return false, nil
	}
	requested := map[string]bool{
		SettingKeyResourceAccessControlEnabled:   settings.ResourceAccessControlEnabled,
		SettingKeySelfServiceHostingEnabled:      settings.SelfServiceHostingEnabled,
		SettingKeyGroupSharingEnabled:            settings.GroupSharingEnabled,
		SettingKeyAccountSharingEnabled:          settings.AccountSharingEnabled,
		SettingKeyRoleBasedResourceGrantsEnabled: settings.RoleBasedResourceGrantsEnabled,
	}

	current, err := s.settingRepo.GetMultiple(ctx, resourceAccessControlSettingKeys)
	if err != nil {
		// A true value may activate a stored authorization path. When the previous
		// state is unknown, treat it as an expansion candidate; explicit false-only
		// updates remain available for emergency restriction.
		for key, enabled := range requested {
			if _, skip := omitted[key]; !skip && enabled {
				return true, nil
			}
		}
		return false, nil
	}

	proposed := make(map[string]string, len(current)+len(requested))
	for key, value := range current {
		proposed[key] = value
	}
	for key, enabled := range requested {
		if _, skip := omitted[key]; skip {
			continue
		}
		proposed[key] = strconv.FormatBool(enabled)
	}
	before := resourceAccessControlExpansionStateFromValues(current)
	after := resourceAccessControlExpansionStateFromValues(proposed)
	return (!before.master && after.master) ||
		(!before.selfServe && after.selfServe) ||
		(!before.groupShare && after.groupShare) ||
		(!before.acctShare && after.acctShare) ||
		(!before.roleGrants && after.roleGrants), nil
}

func resourceAccessControlExpansionStateFromValues(values map[string]string) resourceAccessControlExpansionState {
	master := values[SettingKeyResourceAccessControlEnabled] == "true"
	selfServe := master && values[SettingKeySelfServiceHostingEnabled] == "true"
	return resourceAccessControlExpansionState{
		master:     master,
		selfServe:  selfServe,
		groupShare: selfServe && values[SettingKeyGroupSharingEnabled] == "true",
		acctShare:  selfServe && values[SettingKeyAccountSharingEnabled] == "true",
		roleGrants: master && values[SettingKeyRoleBasedResourceGrantsEnabled] == "true",
	}
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
