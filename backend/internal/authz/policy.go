package authz

import (
	"context"
	"fmt"
)

type ResourcePolicy interface {
	CheckCapability(ctx context.Context, actor Actor, capability Capability) (Decision, error)
	CanCreate(ctx context.Context, actor Actor, resourceType ResourceType) (Decision, error)
	Authorize(ctx context.Context, actor Actor, action Action, ref ResourceRef) (Decision, error)
	AccessibleScope(ctx context.Context, actor Actor, resourceType ResourceType, minimum Action) (AccessibleScope, error)
}

type PolicyService struct {
	store PolicyStore
}

var _ ResourcePolicy = (*PolicyService)(nil)

func NewPolicyService(store PolicyStore) *PolicyService {
	return &PolicyService{store: store}
}

func (s *PolicyService) CheckCapability(ctx context.Context, actor Actor, capability Capability) (Decision, error) {
	if !actor.Valid() {
		return deny(DenyReasonInvalidActor), nil
	}
	if !capability.Valid() {
		return deny(DenyReasonUnknownCapability), nil
	}
	if actor.Kind() == SubjectKindSystem {
		return deny(DenyReasonMissingCapability), nil
	}

	subject, ok := subjectRefFromActor(actor)
	if !ok {
		return deny(DenyReasonInvalidActor), nil
	}
	snapshot, err := s.loadSubjectSnapshot(ctx, subject)
	if err != nil {
		return deny(DenyReasonAuthorizationDataUnavailable), err
	}
	if decision := validateCurrentSubject(actor, subject, snapshot); !decision.Allowed() {
		return decision, nil
	}
	return capabilityDecision(actor, snapshot, capability), nil
}

func (s *PolicyService) CanCreate(ctx context.Context, actor Actor, resourceType ResourceType) (Decision, error) {
	if !actor.Valid() {
		return deny(DenyReasonInvalidActor), nil
	}
	capability, ok := createCapability(resourceType)
	if !ok {
		return deny(DenyReasonUnknownResourceType), nil
	}
	if actor.Kind() == SubjectKindSystem {
		return deny(DenyReasonMissingCapability), nil
	}

	subject, ok := subjectRefFromActor(actor)
	if !ok {
		return deny(DenyReasonInvalidActor), nil
	}
	snapshot, err := s.loadSubjectSnapshot(ctx, subject)
	if err != nil {
		return deny(DenyReasonAuthorizationDataUnavailable), err
	}
	if decision := validateCurrentSubject(actor, subject, snapshot); !decision.Allowed() {
		return decision, nil
	}

	decision := capabilityDecision(actor, snapshot, capability)
	if !decision.Allowed() {
		return decision, nil
	}
	configuration := snapshot.Configuration()
	if !configuration.ResourceAccessControlEnabled() || !configuration.SelfServiceHostingEnabled() {
		if !hasPlatformManagementAuthority(actor, snapshot) {
			return deny(DenyReasonFeatureDisabled), nil
		}
	}
	return decision, nil
}

func (s *PolicyService) Authorize(ctx context.Context, actor Actor, action Action, ref ResourceRef) (Decision, error) {
	if !actor.Valid() {
		return deny(DenyReasonInvalidActor), nil
	}
	if !action.Valid() {
		return deny(DenyReasonUnknownAction), nil
	}
	if !ref.Valid() {
		return deny(DenyReasonUnknownResourceType), nil
	}
	if !action.ValidFor(ref.Type()) {
		return deny(DenyReasonActionResourceMismatch), nil
	}
	if actor.Kind() == SubjectKindSystem {
		return deny(DenyReasonNoMatchingAccess), nil
	}

	subject, ok := subjectRefFromActor(actor)
	if !ok {
		return deny(DenyReasonInvalidActor), nil
	}
	snapshot, err := s.loadResourceAccessSnapshot(ctx, subject, ref)
	if err != nil {
		return deny(DenyReasonAuthorizationDataUnavailable), err
	}
	if !snapshot.Valid() || snapshot.Resource() != ref || snapshot.SubjectSnapshot().Subject() != subject {
		return deny(DenyReasonAuthorizationDataUnavailable), invalidSnapshotError("resource snapshot")
	}
	subjectSnapshot := snapshot.SubjectSnapshot()
	if decision := validateCurrentSubject(actor, subject, subjectSnapshot); !decision.Allowed() {
		return decision, nil
	}

	legacyBypass := hasLegacyAdminAuthority(actor, subjectSnapshot)
	platformCapability, platformBypass := currentPlatformCapability(subjectSnapshot, action)
	configuration := subjectSnapshot.Configuration()
	if !legacyBypass && !platformBypass &&
		(!configuration.ResourceAccessControlEnabled() || !configuration.SelfServiceHostingEnabled()) {
		return deny(DenyReasonFeatureDisabled), nil
	}

	if !snapshot.Exists() {
		return deny(DenyReasonResourceNotFound), nil
	}
	if snapshot.Deleted() {
		return deny(DenyReasonResourceDeleted), nil
	}
	if legacyBypass {
		return allow(legacyAdminMatch()), nil
	}
	if action == ActionGroupUse {
		mode, ok := snapshot.GroupAuthorizationMode()
		if !ok {
			return deny(DenyReasonAuthorizationDataUnavailable), invalidSnapshotError("group authorization mode")
		}
		if mode != GroupAuthorizationModeACL {
			return deny(DenyReasonLegacyGroupAuthorityRequired), fmt.Errorf(
				"%w: group %d uses %s authority",
				ErrLegacyGroupAuthorityRequired,
				ref.ID(),
				mode,
			)
		}
	}
	if platformBypass {
		match, matchErr := platformCapabilityMatch(platformCapability)
		if matchErr != nil {
			return deny(DenyReasonAuthorizationDataUnavailable), invalidSnapshotError("platform capability provenance")
		}
		return allow(match), nil
	}

	if actor.Kind() == SubjectKindUser {
		if ownerUserID, hasOwner := snapshot.OwnerUserID(); hasOwner {
			actorUserID, _ := actor.UserID()
			if ownerUserID == actorUserID {
				return allow(ownerMatch()), nil
			}
		}
	}

	match, found, matchErr := highestResourceAccessMatch(actor, snapshot, configuration)
	if matchErr != nil {
		return deny(DenyReasonAuthorizationDataUnavailable), matchErr
	}
	if !found {
		if hasCurrentPlatformVisibility(subjectSnapshot, ref.Type()) {
			return deny(DenyReasonInsufficientAccess), nil
		}
		return deny(DenyReasonNoMatchingAccess), nil
	}
	level, ok := match.AccessLevel()
	if !ok {
		return deny(DenyReasonAuthorizationDataUnavailable), invalidSnapshotError("access match level")
	}
	if level.Covers(ref.Type(), action) {
		return allow(match), nil
	}
	return deny(DenyReasonInsufficientAccess), nil
}

func (s *PolicyService) loadSubjectSnapshot(ctx context.Context, subject SubjectRef) (SubjectSnapshot, error) {
	if s == nil || s.store == nil {
		return SubjectSnapshot{}, fmt.Errorf("%w: policy store is nil", ErrAuthorizationUnavailable)
	}
	snapshot, err := s.store.LoadSubjectSnapshot(ctx, subject)
	if err != nil {
		return SubjectSnapshot{}, fmt.Errorf("%w: load subject snapshot: %w", ErrAuthorizationUnavailable, err)
	}
	if !snapshot.Valid() || snapshot.Subject() != subject {
		return SubjectSnapshot{}, invalidSnapshotError("subject snapshot")
	}
	return snapshot, nil
}

func (s *PolicyService) loadResourceAccessSnapshot(ctx context.Context, subject SubjectRef, ref ResourceRef) (ResourceAccessSnapshot, error) {
	if s == nil || s.store == nil {
		return ResourceAccessSnapshot{}, fmt.Errorf("%w: policy store is nil", ErrAuthorizationUnavailable)
	}
	snapshot, err := s.store.LoadResourceAccessSnapshot(ctx, subject, ref)
	if err != nil {
		return ResourceAccessSnapshot{}, fmt.Errorf("%w: load resource access snapshot: %w", ErrAuthorizationUnavailable, err)
	}
	return snapshot, nil
}

func invalidSnapshotError(part string) error {
	return fmt.Errorf("%w: %s", ErrAuthorizationUnavailable, part)
}

// validateCurrentSubject uses an allow decision only as an internal success
// marker. It never escapes as the authorization result of a public method.
func validateCurrentSubject(actor Actor, subject SubjectRef, snapshot SubjectSnapshot) Decision {
	if !snapshot.Valid() || snapshot.Subject() != subject {
		return deny(DenyReasonAuthorizationDataUnavailable)
	}
	if !snapshot.Exists() || !snapshot.Active() {
		return deny(DenyReasonActorInactive)
	}
	if actor.subjectVersion() != snapshot.AuthzVersion() ||
		!equalRoleVersions(actor.roleVersionsSnapshot(), snapshot.RoleVersions()) ||
		!equalCapabilities(actor.capabilitiesSnapshot(), snapshot.Capabilities()) {
		return deny(DenyReasonSessionInvalid)
	}
	return allow(ownerMatch())
}

func equalRoleVersions(left, right map[int64]int64) bool {
	if len(left) != len(right) {
		return false
	}
	for roleID, version := range left {
		if right[roleID] != version {
			return false
		}
	}
	return true
}

func equalCapabilities(left, right []Capability) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func capabilityDecision(actor Actor, snapshot SubjectSnapshot, capability Capability) Decision {
	mode := snapshot.Configuration().RoleMode()
	switch mode {
	case RoleAuthorizationModeLegacy, RoleAuthorizationModeShadow:
		if hasLegacyAdminAuthority(actor, snapshot) && legacyAdminCapabilityAllowed(capability) {
			return allow(legacyAdminMatch())
		}
		if actor.Kind() == SubjectKindUser && capability == CapabilityAPIKeyCreate {
			return allow(legacyUserMatch())
		}
		return deny(DenyReasonMissingCapability)
	case RoleAuthorizationModeRBAC:
		if !snapshot.HasCapability(capability) {
			return deny(DenyReasonMissingCapability)
		}
	default:
		return deny(DenyReasonAuthorizationDataUnavailable)
	}
	match, err := platformCapabilityMatch(capability)
	if err != nil {
		return deny(DenyReasonInvalidDecision)
	}
	return allow(match)
}

func hasLegacyAdminAuthority(actor Actor, snapshot SubjectSnapshot) bool {
	mode := snapshot.Configuration().RoleMode()
	if mode != RoleAuthorizationModeLegacy && mode != RoleAuthorizationModeShadow {
		return false
	}
	if actor.hasLegacyAdminBypass() && snapshot.CurrentLegacyAdmin() {
		return true
	}
	// The compatibility Admin API Key is represented by one fixed, zero-role
	// Service Principal. ActorResolver only permits AuthMethodAdminAPIKey for
	// that principal code and rejects any attached role/capability, so this is
	// the machine-principal equivalent of the legacy HTTP admin gate rather
	// than a general Service Principal bypass.
	_, adminAPIKey := actor.ServicePrincipalID()
	return adminAPIKey && actor.AuthMethod() == AuthMethodAdminAPIKey &&
		len(snapshot.RoleVersions()) == 0 && len(snapshot.Capabilities()) == 0
}

func currentPlatformCapability(snapshot SubjectSnapshot, action Action) (Capability, bool) {
	if snapshot.Configuration().RoleMode() != RoleAuthorizationModeRBAC {
		return "", false
	}
	for _, capability := range platformCapabilitiesForAction(action) {
		if snapshot.HasCapability(capability) {
			return capability, true
		}
	}
	return "", false
}

func hasCurrentPlatformVisibility(snapshot SubjectSnapshot, resourceType ResourceType) bool {
	var viewAction Action
	switch resourceType {
	case ResourceTypeAccount:
		viewAction = ActionAccountView
	case ResourceTypeGroup:
		viewAction = ActionGroupView
	default:
		return false
	}
	_, ok := currentPlatformCapability(snapshot, viewAction)
	return ok
}

func hasPlatformManagementAuthority(actor Actor, snapshot SubjectSnapshot) bool {
	if hasLegacyAdminAuthority(actor, snapshot) {
		return true
	}
	return snapshot.Configuration().RoleMode() == RoleAuthorizationModeRBAC &&
		snapshot.HasCapability(CapabilityPlatformResourceManageAll)
}

type rankedAccessMatch struct {
	provenance MatchProvenance
	rank       int
	priority   int
	grantID    int64
	roleID     int64
}

func highestResourceAccessMatch(actor Actor, snapshot ResourceAccessSnapshot, configuration PolicyConfiguration) (MatchProvenance, bool, error) {
	if !snapshot.Valid() || !configuration.Valid() {
		return MatchProvenance{}, false, invalidSnapshotError("resource access candidates")
	}
	if !configuration.SharingEnabled(snapshot.Resource().Type()) {
		return MatchProvenance{}, false, nil
	}

	candidates := make([]rankedAccessMatch, 0, 1+len(snapshot.UserGrants())+len(snapshot.RoleGrants()))
	if actor.Kind() == SubjectKindUser {
		if level, ok := snapshot.PublicAccessLevel(); ok {
			match, err := publicAccessMatch(level)
			if err != nil {
				return MatchProvenance{}, false, invalidSnapshotError("public access")
			}
			rank, _ := level.Rank()
			candidates = append(candidates, rankedAccessMatch{provenance: match, rank: rank, priority: 0})
		}
		for _, grant := range snapshot.UserGrants() {
			match, err := userGrantMatch(grant.GrantID(), grant.AccessLevel())
			if err != nil {
				return MatchProvenance{}, false, invalidSnapshotError("user grant")
			}
			rank, _ := grant.AccessLevel().Rank()
			candidates = append(candidates, rankedAccessMatch{
				provenance: match,
				rank:       rank,
				priority:   1,
				grantID:    grant.GrantID(),
			})
		}
	}
	if configuration.RoleBasedResourceGrantsEnabled() {
		for _, grant := range snapshot.RoleGrants() {
			roleID, ok := grant.RoleID()
			if !ok {
				return MatchProvenance{}, false, invalidSnapshotError("role grant role")
			}
			match, err := roleGrantMatch(grant.GrantID(), roleID, grant.AccessLevel())
			if err != nil {
				return MatchProvenance{}, false, invalidSnapshotError("role grant")
			}
			rank, _ := grant.AccessLevel().Rank()
			candidates = append(candidates, rankedAccessMatch{
				provenance: match,
				rank:       rank,
				priority:   2,
				grantID:    grant.GrantID(),
				roleID:     roleID,
			})
		}
	}
	if len(candidates) == 0 {
		return MatchProvenance{}, false, nil
	}

	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if betterAccessMatch(candidate, best) {
			best = candidate
		}
	}
	return best.provenance, true, nil
}

func betterAccessMatch(candidate, current rankedAccessMatch) bool {
	if candidate.rank != current.rank {
		return candidate.rank > current.rank
	}
	if candidate.priority != current.priority {
		return candidate.priority < current.priority
	}
	if candidate.grantID != current.grantID {
		return candidate.grantID < current.grantID
	}
	return candidate.roleID < current.roleID
}
