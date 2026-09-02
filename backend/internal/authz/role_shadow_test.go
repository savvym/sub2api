package authz

import (
	"context"
	"testing"
)

type captureRoleShadowObserver struct {
	comparisons []RoleShadowComparison
	panicOnCall bool
}

func (o *captureRoleShadowObserver) ObserveRoleShadow(_ context.Context, comparison RoleShadowComparison) {
	if o.panicOnCall {
		panic("observer failure")
	}
	o.comparisons = append(o.comparisons, comparison)
}

func TestPolicyRoleShadowReturnsLegacyAndObservesRBAC(t *testing.T) {
	actor := mustUserActor(t, 401, 3, nil, nil, true)
	subject := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeShadow), true)
	observer := &captureRoleShadowObserver{}
	policy := NewPolicyServiceWithShadowObserver(&stubPolicyStore{subjectSnapshot: subject}, observer)

	decision, err := policy.CheckCapability(context.Background(), actor, CapabilityAccountCreate)
	if err != nil || !decision.Allowed() || decision.MatchSource() != MatchSourceLegacyAdmin {
		t.Fatalf("shadow changed authoritative capability result: decision=%+v err=%v", decision, err)
	}
	if len(observer.comparisons) != 1 {
		t.Fatalf("expected one comparison, got %d", len(observer.comparisons))
	}
	comparison := observer.comparisons[0]
	if !comparison.Valid() || comparison.Operation != PolicyOperationCheckCapability ||
		comparison.SubjectKind != SubjectKindUser || comparison.AuthMethod != AuthMethodJWT ||
		comparison.Capability != CapabilityAccountCreate || !comparison.BehaviorMismatch || comparison.ProvenanceChanged ||
		comparison.Legacy.Effect != RoleShadowEffectAllow || comparison.Legacy.Source != MatchSourceLegacyAdmin ||
		comparison.RBAC.Effect != RoleShadowEffectDeny || comparison.RBAC.DenyReason != DenyReasonMissingCapability {
		t.Fatalf("unexpected comparison: %+v", comparison)
	}
}

func TestPolicyRoleShadowCoversCreateAuthorizeAndScope(t *testing.T) {
	t.Run("create records both decisions and uses rbac for an ordinary user", func(t *testing.T) {
		actor := mustUserActor(t, 402, 2, nil, []Capability{CapabilityAccountCreate}, false)
		subject := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeShadow), false)
		observer := &captureRoleShadowObserver{}
		policy := NewPolicyServiceWithShadowObserver(&stubPolicyStore{subjectSnapshot: subject}, observer)

		decision, err := policy.CanCreate(context.Background(), actor, ResourceTypeAccount)
		if err != nil || !decision.Allowed() || decision.MatchSource() != MatchSourcePlatformCapability {
			t.Fatalf("shadow did not use ordinary-user create capability: decision=%+v err=%v", decision, err)
		}
		comparison := observer.comparisons[0]
		if comparison.Operation != PolicyOperationCanCreate || !comparison.BehaviorMismatch ||
			comparison.Legacy.Effect != RoleShadowEffectDeny || comparison.RBAC.Effect != RoleShadowEffectAllow ||
			comparison.RBAC.Source != MatchSourcePlatformCapability {
			t.Fatalf("unexpected create comparison: %+v", comparison)
		}
	})

	t.Run("authorize records candidate denial without changing legacy allow", func(t *testing.T) {
		actor := mustUserActor(t, 403, 4, nil, nil, true)
		subject := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeShadow), true)
		ref := mustResourceRef(t, ResourceTypeGroup, 987654321)
		resource := mustResourceSnapshot(t, ResourceAccessSnapshotInput{
			Subject:       subject,
			Resource:      ref,
			Exists:        true,
			AccessVersion: 1,
		})
		observer := &captureRoleShadowObserver{}
		policy := NewPolicyServiceWithShadowObserver(&stubPolicyStore{resourceSnapshot: resource}, observer)

		decision, err := policy.Authorize(context.Background(), actor, ActionGroupDelete, ref)
		if err != nil || !decision.Allowed() || decision.MatchSource() != MatchSourceLegacyAdmin {
			t.Fatalf("shadow changed authoritative resource result: decision=%+v err=%v", decision, err)
		}
		comparison := observer.comparisons[0]
		if comparison.Operation != PolicyOperationAuthorize || comparison.ResourceType != ResourceTypeGroup ||
			comparison.Action != ActionGroupDelete || !comparison.BehaviorMismatch ||
			comparison.RBAC.DenyReason != DenyReasonNoMatchingAccess {
			t.Fatalf("unexpected authorize comparison: %+v", comparison)
		}
	})

	t.Run("scope compares global and filtered result sets", func(t *testing.T) {
		actor := mustUserActor(t, 404, 5, nil, nil, true)
		subject := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeShadow), true)
		observer := &captureRoleShadowObserver{}
		policy := NewPolicyServiceWithShadowObserver(&stubPolicyStore{subjectSnapshot: subject}, observer)

		scope, err := policy.AccessibleScope(context.Background(), actor, ResourceTypeAccount, ActionAccountView)
		if err != nil || !scope.Valid() || !scope.LegacyAdminBypass() {
			t.Fatalf("shadow changed authoritative scope: scope=%+v err=%v", scope, err)
		}
		comparison := observer.comparisons[0]
		if comparison.Operation != PolicyOperationAccessibleScope || !comparison.BehaviorMismatch ||
			comparison.Legacy.Effect != RoleShadowEffectScopeGlobal ||
			comparison.RBAC.Effect != RoleShadowEffectScopeFiltered {
			t.Fatalf("unexpected scope comparison: %+v", comparison)
		}
	})
}

func TestPolicyRoleShadowTreatsEquivalentAllowsAsProvenanceOnly(t *testing.T) {
	actor := mustUserActor(t, 405, 1, nil, []Capability{CapabilityAPIKeyCreate}, false)
	subject := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeShadow), false)
	observer := &captureRoleShadowObserver{}
	policy := NewPolicyServiceWithShadowObserver(&stubPolicyStore{subjectSnapshot: subject}, observer)

	decision, err := policy.CheckCapability(context.Background(), actor, CapabilityAPIKeyCreate)
	if err != nil || !decision.Allowed() || decision.MatchSource() != MatchSourceLegacyUser {
		t.Fatalf("shadow changed legacy user compatibility: decision=%+v err=%v", decision, err)
	}
	comparison := observer.comparisons[0]
	if comparison.BehaviorMismatch || !comparison.ProvenanceChanged ||
		comparison.Legacy.Source != MatchSourceLegacyUser || comparison.RBAC.Source != MatchSourcePlatformCapability {
		t.Fatalf("expected provenance-only comparison, got %+v", comparison)
	}
}

func TestPolicyRoleShadowCreateExceptionIsNarrow(t *testing.T) {
	t.Run("check capability remains legacy authoritative", func(t *testing.T) {
		actor := mustUserActor(t, 407, 1, nil, []Capability{CapabilityAccountCreate}, false)
		subject := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeShadow), false)
		policy := NewPolicyService(&stubPolicyStore{subjectSnapshot: subject})

		decision, err := policy.CheckCapability(context.Background(), actor, CapabilityAccountCreate)
		if err != nil || decision.Allowed() || decision.DenyReason() != DenyReasonMissingCapability {
			t.Fatalf("shadow capability check became rbac authoritative: decision=%+v err=%v", decision, err)
		}
	})

	t.Run("service principal remains legacy authoritative", func(t *testing.T) {
		actor := mustServicePrincipalActor(t, 408, 1, nil, []Capability{CapabilityAccountCreate})
		subject := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeShadow), false)
		policy := NewPolicyService(&stubPolicyStore{subjectSnapshot: subject})

		decision, err := policy.CanCreate(context.Background(), actor, ResourceTypeAccount)
		if err != nil || decision.Allowed() || decision.DenyReason() != DenyReasonMissingCapability {
			t.Fatalf("shadow service principal gained ordinary-user create authority: decision=%+v err=%v", decision, err)
		}
	})
}

func TestPolicyRoleShadowObserverCannotChangeAuthorization(t *testing.T) {
	actor := mustUserActor(t, 406, 1, nil, nil, true)
	subject := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeShadow), true)
	policy := NewPolicyServiceWithShadowObserver(
		&stubPolicyStore{subjectSnapshot: subject},
		&captureRoleShadowObserver{panicOnCall: true},
	)

	decision, err := policy.CheckCapability(context.Background(), actor, CapabilityGroupCreate)
	if err != nil || !decision.Allowed() || decision.MatchSource() != MatchSourceLegacyAdmin {
		t.Fatalf("observer panic changed authorization: decision=%+v err=%v", decision, err)
	}
}

func TestPolicyDoesNotObserveOutsideRoleShadow(t *testing.T) {
	for _, mode := range []RoleAuthorizationMode{RoleAuthorizationModeLegacy, RoleAuthorizationModeRBAC} {
		actor := mustUserActor(t, 407, 1, nil, []Capability{CapabilityAccountCreate}, mode == RoleAuthorizationModeLegacy)
		subject := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, mode), mode == RoleAuthorizationModeLegacy)
		observer := &captureRoleShadowObserver{}
		policy := NewPolicyServiceWithShadowObserver(&stubPolicyStore{subjectSnapshot: subject}, observer)

		_, _ = policy.CheckCapability(context.Background(), actor, CapabilityAccountCreate)
		if len(observer.comparisons) != 0 {
			t.Fatalf("mode %s emitted role shadow comparison", mode)
		}
	}
}

func TestRoleShadowComparisonRejectsContradictoryFlags(t *testing.T) {
	actor := mustUserActor(t, 408, 1, nil, nil, true)
	comparison := newRoleShadowComparison(
		PolicyOperationCheckCapability,
		actor,
		CapabilityAccountCreate,
		"",
		"",
		RoleShadowOutcome{Effect: RoleShadowEffectAllow, Source: MatchSourceLegacyAdmin},
		RoleShadowOutcome{Effect: RoleShadowEffectDeny, DenyReason: DenyReasonMissingCapability},
	)
	if !comparison.Valid() || !comparison.BehaviorMismatch {
		t.Fatalf("expected valid mismatch: %+v", comparison)
	}
	comparison.BehaviorMismatch = false
	if comparison.Valid() {
		t.Fatalf("accepted contradictory mismatch flag: %+v", comparison)
	}
}
