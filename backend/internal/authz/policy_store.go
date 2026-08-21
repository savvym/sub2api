package authz

import (
	"context"
	"errors"
	"sort"
)

var (
	ErrActorInactive                = errors.New("authz: actor inactive")
	ErrSubjectNotFound              = errors.New("authz: subject not found")
	ErrInvalidSubjectRef            = errors.New("authz: invalid subject reference")
	ErrSessionInvalid               = errors.New("authz: session invalid")
	ErrFeatureDisabled              = errors.New("authz: feature disabled")
	ErrLegacyGroupAuthorityRequired = errors.New("authz: legacy group authority required")
	ErrAuthorizationUnavailable     = errors.New("authz: authorization unavailable")
	ErrPolicyAccessDenied           = errors.New("authz: policy access denied")
	ErrInvalidPolicySnapshot        = errors.New("authz: invalid policy snapshot")
)

type RoleAuthorizationMode string

const (
	RoleAuthorizationModeLegacy RoleAuthorizationMode = "legacy"
	RoleAuthorizationModeShadow RoleAuthorizationMode = "shadow"
	RoleAuthorizationModeRBAC   RoleAuthorizationMode = "rbac"
)

func (m RoleAuthorizationMode) Valid() bool {
	return m == RoleAuthorizationModeLegacy || m == RoleAuthorizationModeShadow || m == RoleAuthorizationModeRBAC
}

// GroupAuthorizationMode selects the only authoritative source for group.use.
// Legacy and shadow remain legacy-authoritative; ACL must not be ORed into
// their response path.
type GroupAuthorizationMode string

const (
	GroupAuthorizationModeLegacy GroupAuthorizationMode = "legacy"
	GroupAuthorizationModeShadow GroupAuthorizationMode = "shadow"
	GroupAuthorizationModeACL    GroupAuthorizationMode = "acl"
)

func (m GroupAuthorizationMode) Valid() bool {
	return m == GroupAuthorizationModeLegacy || m == GroupAuthorizationModeShadow || m == GroupAuthorizationModeACL
}

// PolicyConfigurationInput is the validated bridge from runtime settings to
// the authorization domain. Callers cannot mutate PolicyConfiguration after
// construction.
type PolicyConfigurationInput struct {
	RoleAuthorizationMode          RoleAuthorizationMode
	ResourceAccessControlEnabled   bool
	SelfServiceHostingEnabled      bool
	GroupSharingEnabled            bool
	AccountSharingEnabled          bool
	RoleBasedResourceGrantsEnabled bool
}

type policyConfigurationMarker struct{}

var trustedPolicyConfigurationMarker = &policyConfigurationMarker{}

type PolicyConfiguration struct {
	roleAuthorizationMode          RoleAuthorizationMode
	resourceAccessControlEnabled   bool
	selfServiceHostingEnabled      bool
	groupSharingEnabled            bool
	accountSharingEnabled          bool
	roleBasedResourceGrantsEnabled bool
	marker                         *policyConfigurationMarker
}

func NewPolicyConfiguration(input PolicyConfigurationInput) (PolicyConfiguration, error) {
	if !input.RoleAuthorizationMode.Valid() {
		return PolicyConfiguration{}, ErrInvalidPolicySnapshot
	}
	masterEnabled := input.ResourceAccessControlEnabled
	selfServiceEnabled := masterEnabled && input.SelfServiceHostingEnabled
	return PolicyConfiguration{
		roleAuthorizationMode:          input.RoleAuthorizationMode,
		resourceAccessControlEnabled:   masterEnabled,
		selfServiceHostingEnabled:      selfServiceEnabled,
		groupSharingEnabled:            selfServiceEnabled && input.GroupSharingEnabled,
		accountSharingEnabled:          selfServiceEnabled && input.AccountSharingEnabled,
		roleBasedResourceGrantsEnabled: masterEnabled && input.RoleBasedResourceGrantsEnabled,
		marker:                         trustedPolicyConfigurationMarker,
	}, nil
}

func (c PolicyConfiguration) Valid() bool {
	return c.marker == trustedPolicyConfigurationMarker && c.roleAuthorizationMode.Valid() &&
		(!c.selfServiceHostingEnabled || c.resourceAccessControlEnabled) &&
		(!c.groupSharingEnabled || c.selfServiceHostingEnabled) &&
		(!c.accountSharingEnabled || c.selfServiceHostingEnabled) &&
		(!c.roleBasedResourceGrantsEnabled || c.resourceAccessControlEnabled)
}

func (c PolicyConfiguration) RoleMode() RoleAuthorizationMode {
	if !c.Valid() {
		return ""
	}
	return c.roleAuthorizationMode
}

func (c PolicyConfiguration) ResourceAccessControlEnabled() bool {
	return c.Valid() && c.resourceAccessControlEnabled
}

func (c PolicyConfiguration) SelfServiceHostingEnabled() bool {
	return c.Valid() && c.selfServiceHostingEnabled
}

func (c PolicyConfiguration) SharingEnabled(resourceType ResourceType) bool {
	if !c.Valid() {
		return false
	}
	switch resourceType {
	case ResourceTypeAccount:
		return c.accountSharingEnabled
	case ResourceTypeGroup:
		return c.groupSharingEnabled
	default:
		return false
	}
}

func (c PolicyConfiguration) RoleBasedResourceGrantsEnabled() bool {
	return c.Valid() && c.roleBasedResourceGrantsEnabled
}

// SubjectRef is a validated database lookup key, not an authorization
// credential. PolicyService still derives it only from a trusted Actor.
type SubjectRef struct {
	kind SubjectKind
	id   int64
}

func NewSubjectRef(kind SubjectKind, id int64) (SubjectRef, error) {
	if (kind != SubjectKindUser && kind != SubjectKindServicePrincipal) || id <= 0 {
		return SubjectRef{}, ErrInvalidSubjectRef
	}
	return SubjectRef{kind: kind, id: id}, nil
}

func subjectRefFromActor(actor Actor) (SubjectRef, bool) {
	if !actor.Valid() {
		return SubjectRef{}, false
	}
	if userID, ok := actor.UserID(); ok {
		return SubjectRef{kind: SubjectKindUser, id: userID}, true
	}
	if principalID, ok := actor.ServicePrincipalID(); ok {
		return SubjectRef{kind: SubjectKindServicePrincipal, id: principalID}, true
	}
	return SubjectRef{}, false
}

func (s SubjectRef) Valid() bool {
	return (s.kind == SubjectKindUser || s.kind == SubjectKindServicePrincipal) && s.id > 0
}

func (s SubjectRef) Kind() SubjectKind {
	if !s.Valid() {
		return ""
	}
	return s.kind
}

func (s SubjectRef) ID() int64 {
	if !s.Valid() {
		return 0
	}
	return s.id
}

type SubjectSnapshotInput struct {
	Subject            SubjectRef
	Exists             bool
	Active             bool
	AuthzVersion       int64
	RoleVersions       map[int64]int64
	Capabilities       []Capability
	CurrentLegacyAdmin bool
	Configuration      PolicyConfiguration
}

type subjectSnapshotMarker struct{}

var trustedSubjectSnapshotMarker = &subjectSnapshotMarker{}

// SubjectSnapshot contains current database state. RoleVersions includes only
// role assignments active at database time; Capabilities is derived from that
// same active role set.
type SubjectSnapshot struct {
	subject            SubjectRef
	exists             bool
	active             bool
	authzVersion       int64
	roleVersions       map[int64]int64
	capabilities       map[Capability]struct{}
	currentLegacyAdmin bool
	configuration      PolicyConfiguration
	marker             *subjectSnapshotMarker
}

func NewSubjectSnapshot(input SubjectSnapshotInput) (SubjectSnapshot, error) {
	if !input.Subject.Valid() || !input.Configuration.Valid() {
		return SubjectSnapshot{}, ErrInvalidPolicySnapshot
	}
	if input.Subject.Kind() != SubjectKindUser && input.CurrentLegacyAdmin {
		return SubjectSnapshot{}, ErrInvalidPolicySnapshot
	}
	if !input.Exists {
		if input.Active || input.AuthzVersion != 0 || len(input.RoleVersions) != 0 || len(input.Capabilities) != 0 || input.CurrentLegacyAdmin {
			return SubjectSnapshot{}, ErrInvalidPolicySnapshot
		}
		return SubjectSnapshot{
			subject:       input.Subject,
			roleVersions:  map[int64]int64{},
			capabilities:  map[Capability]struct{}{},
			configuration: input.Configuration,
			marker:        trustedSubjectSnapshotMarker,
		}, nil
	}
	if input.AuthzVersion <= 0 {
		return SubjectSnapshot{}, ErrInvalidPolicySnapshot
	}

	roles := make(map[int64]int64, len(input.RoleVersions))
	for roleID, version := range input.RoleVersions {
		if roleID <= 0 || version <= 0 {
			return SubjectSnapshot{}, ErrInvalidPolicySnapshot
		}
		roles[roleID] = version
	}
	capabilities := make(map[Capability]struct{}, len(input.Capabilities))
	for _, capability := range input.Capabilities {
		if !capability.Valid() {
			return SubjectSnapshot{}, ErrInvalidPolicySnapshot
		}
		capabilities[capability] = struct{}{}
	}

	return SubjectSnapshot{
		subject:            input.Subject,
		exists:             true,
		active:             input.Active,
		authzVersion:       input.AuthzVersion,
		roleVersions:       roles,
		capabilities:       capabilities,
		currentLegacyAdmin: input.CurrentLegacyAdmin,
		configuration:      input.Configuration,
		marker:             trustedSubjectSnapshotMarker,
	}, nil
}

func (s SubjectSnapshot) Valid() bool {
	if s.marker != trustedSubjectSnapshotMarker || !s.subject.Valid() || !s.configuration.Valid() {
		return false
	}
	if !s.exists {
		return !s.active && s.authzVersion == 0 && len(s.roleVersions) == 0 && len(s.capabilities) == 0 && !s.currentLegacyAdmin
	}
	if s.authzVersion <= 0 {
		return false
	}
	if s.subject.Kind() != SubjectKindUser && s.currentLegacyAdmin {
		return false
	}
	for roleID, version := range s.roleVersions {
		if roleID <= 0 || version <= 0 {
			return false
		}
	}
	for capability := range s.capabilities {
		if !capability.Valid() {
			return false
		}
	}
	return true
}

func (s SubjectSnapshot) Subject() SubjectRef {
	if !s.Valid() {
		return SubjectRef{}
	}
	return s.subject
}

func (s SubjectSnapshot) Exists() bool { return s.Valid() && s.exists }
func (s SubjectSnapshot) Active() bool { return s.Valid() && s.exists && s.active }

func (s SubjectSnapshot) AuthzVersion() int64 {
	if !s.Valid() || !s.exists {
		return 0
	}
	return s.authzVersion
}

func (s SubjectSnapshot) RoleVersions() map[int64]int64 {
	if !s.Valid() {
		return nil
	}
	result := make(map[int64]int64, len(s.roleVersions))
	for roleID, version := range s.roleVersions {
		result[roleID] = version
	}
	return result
}

func (s SubjectSnapshot) Capabilities() []Capability {
	if !s.Valid() {
		return nil
	}
	result := make([]Capability, 0, len(s.capabilities))
	for capability := range s.capabilities {
		result = append(result, capability)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (s SubjectSnapshot) HasCapability(capability Capability) bool {
	if !s.Valid() || !capability.Valid() {
		return false
	}
	_, ok := s.capabilities[capability]
	return ok
}

func (s SubjectSnapshot) CurrentLegacyAdmin() bool {
	return s.Valid() && s.exists && s.currentLegacyAdmin
}

func (s SubjectSnapshot) Configuration() PolicyConfiguration {
	if !s.Valid() {
		return PolicyConfiguration{}
	}
	return s.configuration
}

type grantSnapshotMarker struct{}

var trustedGrantSnapshotMarker = &grantSnapshotMarker{}

type GrantSnapshot struct {
	source      MatchSource
	grantID     int64
	roleID      int64
	accessLevel AccessLevel
	marker      *grantSnapshotMarker
}

func NewUserGrantSnapshot(grantID int64, level AccessLevel) (GrantSnapshot, error) {
	if grantID <= 0 || !level.Valid() {
		return GrantSnapshot{}, ErrInvalidPolicySnapshot
	}
	return GrantSnapshot{source: MatchSourceUserGrant, grantID: grantID, accessLevel: level, marker: trustedGrantSnapshotMarker}, nil
}

func NewRoleGrantSnapshot(grantID, roleID int64, level AccessLevel) (GrantSnapshot, error) {
	if grantID <= 0 || roleID <= 0 || !level.Valid() {
		return GrantSnapshot{}, ErrInvalidPolicySnapshot
	}
	return GrantSnapshot{source: MatchSourceRoleGrant, grantID: grantID, roleID: roleID, accessLevel: level, marker: trustedGrantSnapshotMarker}, nil
}

func (g GrantSnapshot) Valid() bool {
	if g.marker != trustedGrantSnapshotMarker || g.grantID <= 0 || !g.accessLevel.Valid() {
		return false
	}
	switch g.source {
	case MatchSourceUserGrant:
		return g.roleID == 0
	case MatchSourceRoleGrant:
		return g.roleID > 0
	default:
		return false
	}
}

func (g GrantSnapshot) Source() MatchSource {
	if !g.Valid() {
		return ""
	}
	return g.source
}

func (g GrantSnapshot) GrantID() int64 {
	if !g.Valid() {
		return 0
	}
	return g.grantID
}

func (g GrantSnapshot) RoleID() (int64, bool) {
	return g.roleID, g.Valid() && g.source == MatchSourceRoleGrant
}

func (g GrantSnapshot) AccessLevel() AccessLevel {
	if !g.Valid() {
		return ""
	}
	return g.accessLevel
}

type ResourceAccessSnapshotInput struct {
	Subject                SubjectSnapshot
	Resource               ResourceRef
	GroupAuthorizationMode GroupAuthorizationMode
	Exists                 bool
	Deleted                bool
	OwnerUserID            *int64
	PublicAccessLevel      *AccessLevel
	AccessVersion          int64
	UserGrants             []GrantSnapshot
	RoleGrants             []GrantSnapshot
}

type resourceAccessSnapshotMarker struct{}

var trustedResourceAccessSnapshotMarker = &resourceAccessSnapshotMarker{}

type ResourceAccessSnapshot struct {
	subject                SubjectSnapshot
	resource               ResourceRef
	groupAuthorizationMode GroupAuthorizationMode
	exists                 bool
	deleted                bool
	ownerUserID            int64
	hasOwner               bool
	publicAccessLevel      AccessLevel
	hasPublicAccess        bool
	accessVersion          int64
	userGrants             []GrantSnapshot
	roleGrants             []GrantSnapshot
	marker                 *resourceAccessSnapshotMarker
}

func NewResourceAccessSnapshot(input ResourceAccessSnapshotInput) (ResourceAccessSnapshot, error) {
	if !input.Subject.Valid() || !input.Resource.Valid() {
		return ResourceAccessSnapshot{}, ErrInvalidPolicySnapshot
	}
	if input.Resource.Type() == ResourceTypeAccount && input.GroupAuthorizationMode != "" {
		return ResourceAccessSnapshot{}, ErrInvalidPolicySnapshot
	}
	if input.Subject.Subject().Kind() != SubjectKindUser && len(input.UserGrants) != 0 {
		return ResourceAccessSnapshot{}, ErrInvalidPolicySnapshot
	}
	if !input.Exists {
		if input.GroupAuthorizationMode != "" || input.Deleted || input.OwnerUserID != nil || input.PublicAccessLevel != nil || input.AccessVersion != 0 || len(input.UserGrants) != 0 || len(input.RoleGrants) != 0 {
			return ResourceAccessSnapshot{}, ErrInvalidPolicySnapshot
		}
		return ResourceAccessSnapshot{
			subject:    input.Subject,
			resource:   input.Resource,
			userGrants: []GrantSnapshot{},
			roleGrants: []GrantSnapshot{},
			marker:     trustedResourceAccessSnapshotMarker,
		}, nil
	}
	if input.AccessVersion <= 0 {
		return ResourceAccessSnapshot{}, ErrInvalidPolicySnapshot
	}
	if input.Resource.Type() == ResourceTypeGroup && !input.GroupAuthorizationMode.Valid() {
		return ResourceAccessSnapshot{}, ErrInvalidPolicySnapshot
	}

	snapshot := ResourceAccessSnapshot{
		subject:                input.Subject,
		resource:               input.Resource,
		groupAuthorizationMode: input.GroupAuthorizationMode,
		exists:                 true,
		deleted:                input.Deleted,
		accessVersion:          input.AccessVersion,
		userGrants:             append([]GrantSnapshot(nil), input.UserGrants...),
		roleGrants:             append([]GrantSnapshot(nil), input.RoleGrants...),
		marker:                 trustedResourceAccessSnapshotMarker,
	}
	if input.OwnerUserID != nil {
		if *input.OwnerUserID <= 0 {
			return ResourceAccessSnapshot{}, ErrInvalidPolicySnapshot
		}
		snapshot.ownerUserID = *input.OwnerUserID
		snapshot.hasOwner = true
	}
	if input.PublicAccessLevel != nil {
		if !input.PublicAccessLevel.AllowedAsPublic() {
			return ResourceAccessSnapshot{}, ErrInvalidPolicySnapshot
		}
		snapshot.publicAccessLevel = *input.PublicAccessLevel
		snapshot.hasPublicAccess = true
	}
	for _, grant := range snapshot.userGrants {
		if !grant.Valid() || grant.Source() != MatchSourceUserGrant {
			return ResourceAccessSnapshot{}, ErrInvalidPolicySnapshot
		}
	}
	activeRoles := input.Subject.roleVersions
	for _, grant := range snapshot.roleGrants {
		roleID, ok := grant.RoleID()
		if !grant.Valid() || grant.Source() != MatchSourceRoleGrant || !ok {
			return ResourceAccessSnapshot{}, ErrInvalidPolicySnapshot
		}
		if _, active := activeRoles[roleID]; !active {
			return ResourceAccessSnapshot{}, ErrInvalidPolicySnapshot
		}
	}
	return snapshot, nil
}

func (s ResourceAccessSnapshot) Valid() bool {
	if s.marker != trustedResourceAccessSnapshotMarker || !s.subject.Valid() || !s.resource.Valid() {
		return false
	}
	if !s.exists {
		return !s.deleted && !s.hasOwner && !s.hasPublicAccess && s.accessVersion == 0 && len(s.userGrants) == 0 && len(s.roleGrants) == 0
	}
	if s.accessVersion <= 0 || (s.hasOwner && s.ownerUserID <= 0) || (s.hasPublicAccess && !s.publicAccessLevel.AllowedAsPublic()) {
		return false
	}
	if (s.resource.Type() == ResourceTypeGroup && !s.groupAuthorizationMode.Valid()) ||
		(s.resource.Type() == ResourceTypeAccount && s.groupAuthorizationMode != "") {
		return false
	}
	if s.subject.Subject().Kind() != SubjectKindUser && len(s.userGrants) != 0 {
		return false
	}
	for _, grant := range s.userGrants {
		if !grant.Valid() || grant.Source() != MatchSourceUserGrant {
			return false
		}
	}
	for _, grant := range s.roleGrants {
		roleID, ok := grant.RoleID()
		if !grant.Valid() || grant.Source() != MatchSourceRoleGrant || !ok {
			return false
		}
		if _, active := s.subject.roleVersions[roleID]; !active {
			return false
		}
	}
	return true
}

func (s ResourceAccessSnapshot) SubjectSnapshot() SubjectSnapshot {
	if !s.Valid() {
		return SubjectSnapshot{}
	}
	return s.subject
}

func (s ResourceAccessSnapshot) Resource() ResourceRef {
	if !s.Valid() {
		return ResourceRef{}
	}
	return s.resource
}

func (s ResourceAccessSnapshot) GroupAuthorizationMode() (GroupAuthorizationMode, bool) {
	return s.groupAuthorizationMode, s.Valid() && s.exists && s.resource.Type() == ResourceTypeGroup && s.groupAuthorizationMode.Valid()
}

func (s ResourceAccessSnapshot) Exists() bool  { return s.Valid() && s.exists }
func (s ResourceAccessSnapshot) Deleted() bool { return s.Valid() && s.exists && s.deleted }

func (s ResourceAccessSnapshot) OwnerUserID() (int64, bool) {
	return s.ownerUserID, s.Valid() && s.exists && s.hasOwner
}

func (s ResourceAccessSnapshot) PublicAccessLevel() (AccessLevel, bool) {
	return s.publicAccessLevel, s.Valid() && s.exists && s.hasPublicAccess
}

func (s ResourceAccessSnapshot) AccessVersion() int64 {
	if !s.Valid() || !s.exists {
		return 0
	}
	return s.accessVersion
}

func (s ResourceAccessSnapshot) UserGrants() []GrantSnapshot {
	if !s.Valid() {
		return nil
	}
	return append([]GrantSnapshot(nil), s.userGrants...)
}

func (s ResourceAccessSnapshot) RoleGrants() []GrantSnapshot {
	if !s.Valid() {
		return nil
	}
	return append([]GrantSnapshot(nil), s.roleGrants...)
}

// PolicyStore is the only database-facing port used by PolicyService. Store
// implementations must exclude grants and role assignments whose expires_at
// is less than or equal to database current time. UserGrants must already be
// filtered to the supplied user subject. A resource snapshot must read its
// subject, configuration, resource, and matching grants in one statement or
// one transaction so PolicyService never combines states from different times.
type PolicyStore interface {
	LoadSubjectSnapshot(ctx context.Context, subject SubjectRef) (SubjectSnapshot, error)
	LoadResourceAccessSnapshot(ctx context.Context, subject SubjectRef, resource ResourceRef) (ResourceAccessSnapshot, error)
}
