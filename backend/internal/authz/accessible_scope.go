package authz

import (
	"context"
	"fmt"
	"sort"
)

type accessibleScopeMarker struct{}

var trustedAccessibleScopeMarker = &accessibleScopeMarker{}

// AccessibleScope is an opaque, trusted SQL-filter input. It carries current
// subject/version snapshots so repository adapters can revalidate them while
// evaluating owner, public, direct-user and role-grant predicates.
type AccessibleScope struct {
	resourceType             ResourceType
	action                   Action
	subject                  SubjectRef
	subjectAuthzVersion      int64
	roleVersions             map[int64]int64
	capabilities             []Capability
	roleAuthorizationMode    RoleAuthorizationMode
	legacyAdminBypass        bool
	platformCapabilityBypass Capability
	includeOwner             bool
	includePublicAccess      bool
	includeDirectUserGrants  bool
	includeRoleGrants        bool
	publicAccessLevels       []AccessLevel
	grantAccessLevels        []AccessLevel
	marker                   *accessibleScopeMarker
}

func (s *PolicyService) AccessibleScope(ctx context.Context, actor Actor, resourceType ResourceType, minimum Action) (AccessibleScope, error) {
	if !actor.Valid() {
		return AccessibleScope{}, ErrInvalidActor
	}
	if actor.Kind() == SubjectKindSystem {
		return AccessibleScope{}, fmt.Errorf("%w: system actor has no generic resource scope", ErrPolicyAccessDenied)
	}
	if !resourceType.Valid() || !minimum.Valid() || !minimum.ValidFor(resourceType) {
		return AccessibleScope{}, ErrInvalidResourceRef
	}
	// A group.use list can contain legacy, shadow, and ACL rows. Until the
	// authority-aware legacy/ACL union lands, a generic ACL scope must not omit
	// legacy-authoritative groups or let ACL affect their response.
	if resourceType == ResourceTypeGroup && minimum == ActionGroupUse {
		return AccessibleScope{}, ErrLegacyGroupAuthorityRequired
	}
	subject, ok := subjectRefFromActor(actor)
	if !ok {
		return AccessibleScope{}, ErrInvalidActor
	}
	snapshot, err := s.loadSubjectSnapshot(ctx, subject)
	if err != nil {
		return AccessibleScope{}, err
	}
	decision := validateCurrentSubject(actor, subject, snapshot)
	if !decision.Allowed() {
		return AccessibleScope{}, scopeDecisionError(decision)
	}

	legacyBypass := hasLegacyAdminAuthority(actor, snapshot)
	platformCapability, platformBypass := currentPlatformCapability(snapshot, minimum)
	configuration := snapshot.Configuration()
	if !legacyBypass && !platformBypass &&
		(!configuration.ResourceAccessControlEnabled() || !configuration.SelfServiceHostingEnabled()) {
		return AccessibleScope{}, ErrFeatureDisabled
	}

	grantLevels := accessLevelsCovering(resourceType, minimum, false)
	publicLevels := accessLevelsCovering(resourceType, minimum, true)
	sharingEnabled := configuration.SharingEnabled(resourceType)
	userSubject := subject.Kind() == SubjectKindUser

	scope := AccessibleScope{
		resourceType:             resourceType,
		action:                   minimum,
		subject:                  subject,
		subjectAuthzVersion:      snapshot.AuthzVersion(),
		roleVersions:             snapshot.RoleVersions(),
		capabilities:             snapshot.Capabilities(),
		roleAuthorizationMode:    configuration.RoleMode(),
		legacyAdminBypass:        legacyBypass,
		platformCapabilityBypass: platformCapability,
		includeOwner:             userSubject,
		includePublicAccess:      userSubject && sharingEnabled && len(publicLevels) > 0,
		includeDirectUserGrants:  userSubject && sharingEnabled && len(grantLevels) > 0,
		includeRoleGrants:        sharingEnabled && configuration.RoleBasedResourceGrantsEnabled() && len(grantLevels) > 0,
		publicAccessLevels:       publicLevels,
		grantAccessLevels:        grantLevels,
		marker:                   trustedAccessibleScopeMarker,
	}
	if !scope.Valid() {
		return AccessibleScope{}, invalidSnapshotError("accessible scope")
	}
	return scope, nil
}

func scopeDecisionError(decision Decision) error {
	switch decision.DenyReason() {
	case DenyReasonInvalidActor:
		return ErrInvalidActor
	case DenyReasonActorInactive:
		return ErrActorInactive
	case DenyReasonSessionInvalid:
		return ErrSessionInvalid
	case DenyReasonFeatureDisabled:
		return ErrFeatureDisabled
	case DenyReasonAuthorizationDataUnavailable, DenyReasonInvalidDecision:
		return ErrAuthorizationUnavailable
	default:
		return fmt.Errorf("%w: %s", ErrPolicyAccessDenied, decision.DenyReason())
	}
}

func accessLevelsCovering(resourceType ResourceType, action Action, publicOnly bool) []AccessLevel {
	levels := make([]AccessLevel, 0, len(AllAccessLevels()))
	for _, level := range AllAccessLevels() {
		if publicOnly && !level.AllowedAsPublic() {
			continue
		}
		if level.Covers(resourceType, action) {
			levels = append(levels, level)
		}
	}
	return levels
}

func (s AccessibleScope) Valid() bool {
	if s.marker != trustedAccessibleScopeMarker || !s.resourceType.Valid() || !s.action.ValidFor(s.resourceType) ||
		!s.subject.Valid() || s.subjectAuthzVersion <= 0 || !s.roleAuthorizationMode.Valid() {
		return false
	}
	for roleID, version := range s.roleVersions {
		if roleID <= 0 || version <= 0 {
			return false
		}
	}
	for _, capability := range s.capabilities {
		if !capability.Valid() {
			return false
		}
	}
	if s.legacyAdminBypass && (s.roleAuthorizationMode == RoleAuthorizationModeRBAC || s.subject.Kind() != SubjectKindUser) {
		return false
	}
	if s.platformCapabilityBypass != "" {
		if s.roleAuthorizationMode != RoleAuthorizationModeRBAC || !s.platformCapabilityBypass.Valid() {
			return false
		}
		capabilityPresent := false
		for _, capability := range s.capabilities {
			if capability == s.platformCapabilityBypass {
				capabilityPresent = true
				break
			}
		}
		if !capabilityPresent {
			return false
		}
		covered := false
		for _, capability := range platformCapabilitiesForAction(s.action) {
			if capability == s.platformCapabilityBypass {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	if s.subject.Kind() != SubjectKindUser && (s.includeOwner || s.includePublicAccess || s.includeDirectUserGrants) {
		return false
	}
	if (s.includePublicAccess && len(s.publicAccessLevels) == 0) ||
		((s.includeDirectUserGrants || s.includeRoleGrants) && len(s.grantAccessLevels) == 0) {
		return false
	}
	for _, level := range s.publicAccessLevels {
		if !level.AllowedAsPublic() || !level.Covers(s.resourceType, s.action) {
			return false
		}
	}
	for _, level := range s.grantAccessLevels {
		if !level.Valid() || !level.Covers(s.resourceType, s.action) {
			return false
		}
	}
	return true
}

func (s AccessibleScope) ResourceType() ResourceType {
	if !s.Valid() {
		return ""
	}
	return s.resourceType
}

func (s AccessibleScope) Action() Action {
	if !s.Valid() {
		return ""
	}
	return s.action
}

func (s AccessibleScope) SubjectKind() SubjectKind {
	if !s.Valid() {
		return ""
	}
	return s.subject.Kind()
}

func (s AccessibleScope) SubjectID() int64 {
	if !s.Valid() {
		return 0
	}
	return s.subject.ID()
}

func (s AccessibleScope) SubjectAuthzVersion() int64 {
	if !s.Valid() {
		return 0
	}
	return s.subjectAuthzVersion
}

func (s AccessibleScope) RoleVersions() map[int64]int64 {
	if !s.Valid() {
		return nil
	}
	result := make(map[int64]int64, len(s.roleVersions))
	for roleID, version := range s.roleVersions {
		result[roleID] = version
	}
	return result
}

func (s AccessibleScope) RoleIDs() []int64 {
	if !s.Valid() {
		return nil
	}
	result := make([]int64, 0, len(s.roleVersions))
	for roleID := range s.roleVersions {
		result = append(result, roleID)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (s AccessibleScope) Capabilities() []Capability {
	if !s.Valid() {
		return nil
	}
	return append([]Capability(nil), s.capabilities...)
}

func (s AccessibleScope) RoleMode() RoleAuthorizationMode {
	if !s.Valid() {
		return ""
	}
	return s.roleAuthorizationMode
}

func (s AccessibleScope) LegacyAdminBypass() bool {
	return s.Valid() && s.legacyAdminBypass
}

func (s AccessibleScope) PlatformCapabilityBypass() (Capability, bool) {
	return s.platformCapabilityBypass, s.Valid() && s.platformCapabilityBypass.Valid()
}

func (s AccessibleScope) IncludesOwner() bool {
	return s.Valid() && s.includeOwner
}

func (s AccessibleScope) IncludesPublicAccess() bool {
	return s.Valid() && s.includePublicAccess
}

func (s AccessibleScope) IncludesDirectUserGrants() bool {
	return s.Valid() && s.includeDirectUserGrants
}

func (s AccessibleScope) IncludesRoleGrants() bool {
	return s.Valid() && s.includeRoleGrants
}

func (s AccessibleScope) PublicAccessLevels() []AccessLevel {
	if !s.Valid() {
		return nil
	}
	return append([]AccessLevel(nil), s.publicAccessLevels...)
}

func (s AccessibleScope) GrantAccessLevels() []AccessLevel {
	if !s.Valid() {
		return nil
	}
	return append([]AccessLevel(nil), s.grantAccessLevels...)
}
