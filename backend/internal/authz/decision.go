package authz

import "errors"

var errInvalidMatchProvenance = errors.New("authz: invalid match provenance")

type MatchSource string

const (
	MatchSourceSystem             MatchSource = "system"
	MatchSourceLegacyAdmin        MatchSource = "legacy_admin"
	MatchSourceLegacyUser         MatchSource = "legacy_user"
	MatchSourcePlatformCapability MatchSource = "platform_capability"
	MatchSourceOwner              MatchSource = "owner"
	MatchSourcePublicAccess       MatchSource = "public_access"
	MatchSourceUserGrant          MatchSource = "user_grant"
	MatchSourceRoleGrant          MatchSource = "role_grant"
)

func (s MatchSource) Valid() bool {
	switch s {
	case MatchSourceSystem,
		MatchSourceLegacyAdmin,
		MatchSourceLegacyUser,
		MatchSourcePlatformCapability,
		MatchSourceOwner,
		MatchSourcePublicAccess,
		MatchSourceUserGrant,
		MatchSourceRoleGrant:
		return true
	default:
		return false
	}
}

// MatchProvenance preserves the exact authorization source needed by audit,
// shadow comparison, and grant-driven invalidation.
type MatchProvenance struct {
	source        MatchSource
	accessLevel   AccessLevel
	capability    Capability
	grantID       int64
	granteeRoleID int64
}

func systemMatch() MatchProvenance {
	return MatchProvenance{source: MatchSourceSystem}
}

func legacyAdminMatch() MatchProvenance {
	return MatchProvenance{source: MatchSourceLegacyAdmin}
}

func legacyUserMatch() MatchProvenance {
	return MatchProvenance{source: MatchSourceLegacyUser}
}

func platformCapabilityMatch(capability Capability) (MatchProvenance, error) {
	if !capability.Valid() {
		return MatchProvenance{}, errInvalidMatchProvenance
	}
	return MatchProvenance{source: MatchSourcePlatformCapability, capability: capability}, nil
}

func ownerMatch() MatchProvenance {
	return MatchProvenance{source: MatchSourceOwner}
}

func publicAccessMatch(accessLevel AccessLevel) (MatchProvenance, error) {
	if !accessLevel.AllowedAsPublic() {
		return MatchProvenance{}, errInvalidMatchProvenance
	}
	return MatchProvenance{source: MatchSourcePublicAccess, accessLevel: accessLevel}, nil
}

func userGrantMatch(grantID int64, accessLevel AccessLevel) (MatchProvenance, error) {
	if grantID <= 0 || !accessLevel.Valid() {
		return MatchProvenance{}, errInvalidMatchProvenance
	}
	return MatchProvenance{source: MatchSourceUserGrant, grantID: grantID, accessLevel: accessLevel}, nil
}

func roleGrantMatch(grantID, roleID int64, accessLevel AccessLevel) (MatchProvenance, error) {
	if grantID <= 0 || roleID <= 0 || !accessLevel.Valid() {
		return MatchProvenance{}, errInvalidMatchProvenance
	}
	return MatchProvenance{
		source:        MatchSourceRoleGrant,
		grantID:       grantID,
		granteeRoleID: roleID,
		accessLevel:   accessLevel,
	}, nil
}

func (p MatchProvenance) Valid() bool {
	switch p.source {
	case MatchSourceSystem, MatchSourceLegacyAdmin, MatchSourceLegacyUser, MatchSourceOwner:
		return p.accessLevel == "" && p.capability == "" && p.grantID == 0 && p.granteeRoleID == 0
	case MatchSourcePlatformCapability:
		return p.capability.Valid() && p.accessLevel == "" && p.grantID == 0 && p.granteeRoleID == 0
	case MatchSourcePublicAccess:
		return p.accessLevel.AllowedAsPublic() && p.capability == "" && p.grantID == 0 && p.granteeRoleID == 0
	case MatchSourceUserGrant:
		return p.accessLevel.Valid() && p.capability == "" && p.grantID > 0 && p.granteeRoleID == 0
	case MatchSourceRoleGrant:
		return p.accessLevel.Valid() && p.capability == "" && p.grantID > 0 && p.granteeRoleID > 0
	default:
		return false
	}
}

func (p MatchProvenance) Source() MatchSource {
	if !p.Valid() {
		return ""
	}
	return p.source
}

func (p MatchProvenance) AccessLevel() (AccessLevel, bool) {
	return p.accessLevel, p.Valid() && p.accessLevel.Valid()
}

func (p MatchProvenance) Capability() (Capability, bool) {
	return p.capability, p.Valid() && p.capability.Valid()
}

func (p MatchProvenance) GrantID() (int64, bool) {
	return p.grantID, p.Valid() && p.grantID > 0
}

func (p MatchProvenance) GranteeRoleID() (int64, bool) {
	return p.granteeRoleID, p.Valid() && p.granteeRoleID > 0
}

type DenialClass string

const (
	DenialClassNotFound        DenialClass = "not_found"
	DenialClassForbidden       DenialClass = "forbidden"
	DenialClassUnauthenticated DenialClass = "unauthenticated"
	DenialClassUnavailable     DenialClass = "unavailable"
	DenialClassInvalid         DenialClass = "invalid"
)

type DenyReason string

const (
	DenyReasonInvalidActor                 DenyReason = "invalid_actor"
	DenyReasonActorInactive                DenyReason = "actor_inactive"
	DenyReasonSessionInvalid               DenyReason = "session_invalid"
	DenyReasonResourceNotFound             DenyReason = "resource_not_found"
	DenyReasonResourceDeleted              DenyReason = "resource_deleted"
	DenyReasonUnknownResourceType          DenyReason = "unknown_resource_type"
	DenyReasonUnknownAction                DenyReason = "unknown_action"
	DenyReasonActionResourceMismatch       DenyReason = "action_resource_mismatch"
	DenyReasonUnknownCapability            DenyReason = "unknown_capability"
	DenyReasonMissingCapability            DenyReason = "missing_capability"
	DenyReasonFeatureDisabled              DenyReason = "feature_disabled"
	DenyReasonLegacyGroupAuthorityRequired DenyReason = "legacy_group_authority_required"
	DenyReasonNoMatchingAccess             DenyReason = "no_matching_access"
	DenyReasonInsufficientAccess           DenyReason = "insufficient_access"
	DenyReasonAuthorizationDataUnavailable DenyReason = "authorization_data_unavailable"
	DenyReasonInvalidDecision              DenyReason = "invalid_decision"
)

func (r DenyReason) Valid() bool {
	_, ok := r.Class()
	return ok
}

func (r DenyReason) Class() (DenialClass, bool) {
	switch r {
	case DenyReasonResourceNotFound, DenyReasonResourceDeleted, DenyReasonNoMatchingAccess:
		return DenialClassNotFound, true
	case DenyReasonMissingCapability, DenyReasonFeatureDisabled, DenyReasonInsufficientAccess:
		return DenialClassForbidden, true
	case DenyReasonInvalidActor, DenyReasonActorInactive, DenyReasonSessionInvalid:
		return DenialClassUnauthenticated, true
	case DenyReasonAuthorizationDataUnavailable, DenyReasonLegacyGroupAuthorityRequired:
		return DenialClassUnavailable, true
	case DenyReasonUnknownResourceType,
		DenyReasonUnknownAction,
		DenyReasonActionResourceMismatch,
		DenyReasonUnknownCapability,
		DenyReasonInvalidDecision:
		return DenialClassInvalid, true
	default:
		return "", false
	}
}

func (r DenyReason) ConcealsResource() bool {
	class, ok := r.Class()
	return ok && class == DenialClassNotFound
}

type Decision struct {
	allowed    bool
	provenance MatchProvenance
	denyReason DenyReason
}

// allow and the provenance constructors remain package-private so callers
// cannot manufacture an authorization result without PolicyService.
func allow(provenance MatchProvenance) Decision {
	if !provenance.Valid() {
		return deny(DenyReasonInvalidDecision)
	}
	return Decision{allowed: true, provenance: provenance}
}

func deny(reason DenyReason) Decision {
	if !reason.Valid() {
		reason = DenyReasonInvalidDecision
	}
	return Decision{denyReason: reason}
}

func (d Decision) Valid() bool {
	if d.allowed {
		return d.denyReason == "" && d.provenance.Valid()
	}
	return d.provenance == (MatchProvenance{}) && d.denyReason.Valid()
}

func (d Decision) Allowed() bool {
	return d.Valid() && d.allowed
}

func (d Decision) Provenance() (MatchProvenance, bool) {
	return d.provenance, d.Allowed()
}

func (d Decision) MatchSource() MatchSource {
	if !d.Allowed() {
		return ""
	}
	return d.provenance.Source()
}

func (d Decision) AccessLevel() (AccessLevel, bool) {
	if !d.Allowed() {
		return "", false
	}
	return d.provenance.AccessLevel()
}

func (d Decision) DenyReason() DenyReason {
	if d.Allowed() {
		return ""
	}
	if !d.Valid() {
		return DenyReasonInvalidDecision
	}
	return d.denyReason
}

func (d Decision) DenialClass() (DenialClass, bool) {
	if d.Allowed() {
		return "", false
	}
	return d.DenyReason().Class()
}

func (d Decision) ConcealsResource() bool {
	return !d.Allowed() && d.DenyReason().ConcealsResource()
}
