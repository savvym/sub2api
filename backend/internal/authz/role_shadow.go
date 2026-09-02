package authz

import "context"

// PolicyOperation identifies one public PolicyService authorization surface.
// It is deliberately low-cardinality so shadow observations can be aggregated.
type PolicyOperation string

const (
	PolicyOperationCheckCapability PolicyOperation = "check_capability"
	PolicyOperationCanCreate       PolicyOperation = "can_create"
	PolicyOperationAuthorize       PolicyOperation = "authorize"
	PolicyOperationAccessibleScope PolicyOperation = "accessible_scope"
)

func (o PolicyOperation) Valid() bool {
	switch o {
	case PolicyOperationCheckCapability,
		PolicyOperationCanCreate,
		PolicyOperationAuthorize,
		PolicyOperationAccessibleScope:
		return true
	default:
		return false
	}
}

// RoleShadowEffect is the externally relevant effect of one role authority.
// Scope effects distinguish global and filtered result sets; changing between
// them is a behavioral difference even though both construct a valid scope.
type RoleShadowEffect string

const (
	RoleShadowEffectAllow         RoleShadowEffect = "allow"
	RoleShadowEffectDeny          RoleShadowEffect = "deny"
	RoleShadowEffectScopeGlobal   RoleShadowEffect = "scope_global"
	RoleShadowEffectScopeFiltered RoleShadowEffect = "scope_filtered"
)

func (e RoleShadowEffect) Valid() bool {
	switch e {
	case RoleShadowEffectAllow,
		RoleShadowEffectDeny,
		RoleShadowEffectScopeGlobal,
		RoleShadowEffectScopeFiltered:
		return true
	default:
		return false
	}
}

// RoleShadowOutcome contains only bounded, non-identifying authorization
// fields. Grant, role, subject, and resource IDs never cross this boundary.
type RoleShadowOutcome struct {
	Effect     RoleShadowEffect
	Source     MatchSource
	DenyReason DenyReason
}

func (o RoleShadowOutcome) Valid() bool {
	switch o.Effect {
	case RoleShadowEffectDeny:
		return o.Source == "" && o.DenyReason.Valid()
	case RoleShadowEffectAllow, RoleShadowEffectScopeGlobal:
		return o.Source.Valid() && o.DenyReason == ""
	case RoleShadowEffectScopeFiltered:
		return o.Source == "" && o.DenyReason == ""
	default:
		return false
	}
}

// RoleShadowComparison is safe for structured logging. It intentionally has
// no free-form strings or numeric identifiers.
type RoleShadowComparison struct {
	Operation         PolicyOperation
	SubjectKind       SubjectKind
	AuthMethod        AuthMethod
	Capability        Capability
	ResourceType      ResourceType
	Action            Action
	Legacy            RoleShadowOutcome
	RBAC              RoleShadowOutcome
	BehaviorMismatch  bool
	ProvenanceChanged bool
}

func (c RoleShadowComparison) Valid() bool {
	if !c.Operation.Valid() || !c.SubjectKind.Valid() || !c.AuthMethod.validFor(c.SubjectKind) ||
		!c.Legacy.Valid() || !c.RBAC.Valid() {
		return false
	}
	var fieldsValid bool
	switch c.Operation {
	case PolicyOperationCheckCapability:
		fieldsValid = c.Capability.Valid() && c.ResourceType == "" && c.Action == ""
	case PolicyOperationCanCreate:
		fieldsValid = c.Capability.Valid() && c.ResourceType.Valid() && c.Action == ""
	case PolicyOperationAuthorize, PolicyOperationAccessibleScope:
		fieldsValid = c.Capability == "" && c.ResourceType.Valid() && c.Action.ValidFor(c.ResourceType)
	default:
		return false
	}
	behaviorMismatch := c.Legacy.Effect != c.RBAC.Effect ||
		(c.Legacy.Effect == RoleShadowEffectDeny && c.Legacy.DenyReason != c.RBAC.DenyReason)
	provenanceChanged := !behaviorMismatch && c.Legacy.Source != c.RBAC.Source
	return fieldsValid && c.BehaviorMismatch == behaviorMismatch && c.ProvenanceChanged == provenanceChanged
}

// RoleShadowObserver receives best-effort, redacted shadow comparisons. An
// observer must never be used as an authorization input.
type RoleShadowObserver interface {
	ObserveRoleShadow(ctx context.Context, comparison RoleShadowComparison)
}

func roleShadowDecisionOutcome(decision Decision) RoleShadowOutcome {
	if decision.Allowed() {
		return RoleShadowOutcome{Effect: RoleShadowEffectAllow, Source: decision.MatchSource()}
	}
	return RoleShadowOutcome{Effect: RoleShadowEffectDeny, DenyReason: decision.DenyReason()}
}

func newRoleShadowComparison(
	operation PolicyOperation,
	actor Actor,
	capability Capability,
	resourceType ResourceType,
	action Action,
	legacy RoleShadowOutcome,
	rbac RoleShadowOutcome,
) RoleShadowComparison {
	comparison := RoleShadowComparison{
		Operation:    operation,
		SubjectKind:  actor.Kind(),
		AuthMethod:   actor.AuthMethod(),
		Capability:   capability,
		ResourceType: resourceType,
		Action:       action,
		Legacy:       legacy,
		RBAC:         rbac,
	}
	comparison.BehaviorMismatch = legacy.Effect != rbac.Effect ||
		(legacy.Effect == RoleShadowEffectDeny && legacy.DenyReason != rbac.DenyReason)
	comparison.ProvenanceChanged = !comparison.BehaviorMismatch && legacy.Source != rbac.Source
	return comparison
}

func (s *PolicyService) observeRoleShadow(ctx context.Context, comparison RoleShadowComparison) {
	if s == nil || s.shadowObserver == nil || !comparison.Valid() {
		return
	}
	// Observability is never allowed to change an authorization response.
	defer func() {
		_ = recover()
	}()
	s.shadowObserver.ObserveRoleShadow(ctx, comparison)
}
