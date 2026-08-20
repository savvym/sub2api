package authz

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestAccessibleScopeCarriesExactImmutableSQLInputs(t *testing.T) {
	t.Parallel()

	actor := mustUserActor(t, 42, 7, map[int64]int64{9: 3, 2: 1}, []Capability{CapabilityResourceShare}, false)
	snapshot := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeRBAC), false)
	scope, err := NewPolicyService(&stubPolicyStore{subjectSnapshot: snapshot}).AccessibleScope(
		context.Background(), actor, ResourceTypeAccount, ActionAccountUse,
	)
	if err != nil {
		t.Fatalf("build accessible scope: %v", err)
	}
	if !scope.Valid() || scope.ResourceType() != ResourceTypeAccount || scope.Action() != ActionAccountUse ||
		scope.SubjectKind() != SubjectKindUser || scope.SubjectID() != 42 || scope.SubjectAuthzVersion() != 7 ||
		scope.RoleMode() != RoleAuthorizationModeRBAC {
		t.Fatalf("unexpected scope identity: %+v", scope)
	}
	if !reflect.DeepEqual(scope.RoleIDs(), []int64{2, 9}) ||
		!reflect.DeepEqual(scope.RoleVersions(), map[int64]int64{2: 1, 9: 3}) ||
		!reflect.DeepEqual(scope.Capabilities(), []Capability{CapabilityResourceShare}) {
		t.Fatalf("unexpected scope authorization snapshot: roles=%v versions=%v capabilities=%v", scope.RoleIDs(), scope.RoleVersions(), scope.Capabilities())
	}
	if !scope.IncludesOwner() || !scope.IncludesPublicAccess() || !scope.IncludesDirectUserGrants() || !scope.IncludesRoleGrants() {
		t.Fatalf("expected every user scope source: %+v", scope)
	}
	if !reflect.DeepEqual(scope.PublicAccessLevels(), []AccessLevel{AccessLevelConsumer}) ||
		!reflect.DeepEqual(scope.GrantAccessLevels(), []AccessLevel{AccessLevelConsumer, AccessLevelMaintainer, AccessLevelManager}) {
		t.Fatalf("unexpected minimum access levels: public=%v grants=%v", scope.PublicAccessLevels(), scope.GrantAccessLevels())
	}
	if scope.LegacyAdminBypass() {
		t.Fatal("ordinary rbac scope received legacy bypass")
	}
	if _, ok := scope.PlatformCapabilityBypass(); ok {
		t.Fatal("non-covering capability became platform bypass")
	}

	roles := scope.RoleVersions()
	roleIDs := scope.RoleIDs()
	capabilities := scope.Capabilities()
	publicLevels := scope.PublicAccessLevels()
	grantLevels := scope.GrantAccessLevels()
	roles[2] = 99
	roleIDs[0] = 99
	capabilities[0] = Capability("modified")
	publicLevels[0] = AccessLevelViewer
	grantLevels[0] = AccessLevelViewer
	if scope.RoleVersions()[2] != 1 || scope.RoleIDs()[0] != 2 || scope.Capabilities()[0] != CapabilityResourceShare ||
		scope.PublicAccessLevels()[0] != AccessLevelConsumer || scope.GrantAccessLevels()[0] != AccessLevelConsumer {
		t.Fatal("accessible scope exposed mutable SQL inputs")
	}
}

func TestAccessibleScopeSeparatesUserAndServicePrincipalSources(t *testing.T) {
	t.Parallel()

	configuration := fullyEnabledConfiguration(t, RoleAuthorizationModeRBAC)
	principal := mustServicePrincipalActor(t, 17, 1, map[int64]int64{5: 2}, nil)
	principalScope, err := NewPolicyService(&stubPolicyStore{
		subjectSnapshot: mustSubjectSnapshotForActor(t, principal, configuration, false),
	}).AccessibleScope(context.Background(), principal, ResourceTypeAccount, ActionAccountUse)
	if err != nil {
		t.Fatalf("build service principal scope: %v", err)
	}
	if !principalScope.Valid() || principalScope.IncludesOwner() || principalScope.IncludesPublicAccess() ||
		principalScope.IncludesDirectUserGrants() || !principalScope.IncludesRoleGrants() {
		t.Fatalf("service principal received user-only predicates: %+v", principalScope)
	}

	user := mustUserActor(t, 17, 1, nil, nil, false)
	deleteScope, err := NewPolicyService(&stubPolicyStore{
		subjectSnapshot: mustSubjectSnapshotForActor(t, user, configuration, false),
	}).AccessibleScope(context.Background(), user, ResourceTypeAccount, ActionAccountDelete)
	if err != nil {
		t.Fatalf("build owner-only delete scope: %v", err)
	}
	if !deleteScope.Valid() || !deleteScope.IncludesOwner() || deleteScope.IncludesPublicAccess() ||
		deleteScope.IncludesDirectUserGrants() || deleteScope.IncludesRoleGrants() ||
		len(deleteScope.PublicAccessLevels()) != 0 || len(deleteScope.GrantAccessLevels()) != 0 {
		t.Fatalf("delete scope was not owner-only: %+v", deleteScope)
	}
}

func TestAccessibleScopeAppliesSharingFeatureMatrix(t *testing.T) {
	t.Parallel()

	actor := mustUserActor(t, 6, 1, map[int64]int64{3: 1}, nil, false)
	tests := []struct {
		name          string
		configuration PolicyConfigurationInput
		public        bool
		direct        bool
		role          bool
	}{
		{
			name: "sharing off leaves owner only",
			configuration: PolicyConfigurationInput{
				RoleAuthorizationMode:          RoleAuthorizationModeRBAC,
				ResourceAccessControlEnabled:   true,
				SelfServiceHostingEnabled:      true,
				RoleBasedResourceGrantsEnabled: true,
			},
		},
		{
			name: "sharing allows public and direct but role flag stays independent",
			configuration: PolicyConfigurationInput{
				RoleAuthorizationMode:        RoleAuthorizationModeRBAC,
				ResourceAccessControlEnabled: true,
				SelfServiceHostingEnabled:    true,
				GroupSharingEnabled:          true,
			},
			public: true,
			direct: true,
		},
		{
			name: "role grant requires both flags",
			configuration: PolicyConfigurationInput{
				RoleAuthorizationMode:          RoleAuthorizationModeRBAC,
				ResourceAccessControlEnabled:   true,
				SelfServiceHostingEnabled:      true,
				GroupSharingEnabled:            true,
				RoleBasedResourceGrantsEnabled: true,
			},
			public: true,
			direct: true,
			role:   true,
		},
	}
	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			configuration := mustPolicyConfiguration(t, testCase.configuration)
			scope, err := NewPolicyService(&stubPolicyStore{
				subjectSnapshot: mustSubjectSnapshotForActor(t, actor, configuration, false),
			}).AccessibleScope(context.Background(), actor, ResourceTypeGroup, ActionGroupView)
			if err != nil {
				t.Fatalf("build feature scope: %v", err)
			}
			if !scope.IncludesOwner() || scope.IncludesPublicAccess() != testCase.public ||
				scope.IncludesDirectUserGrants() != testCase.direct || scope.IncludesRoleGrants() != testCase.role {
				t.Fatalf("unexpected feature predicates: owner=%v public=%v direct=%v role=%v", scope.IncludesOwner(), scope.IncludesPublicAccess(), scope.IncludesDirectUserGrants(), scope.IncludesRoleGrants())
			}
		})
	}
}

func TestAccessibleScopeAllowsOnlyCurrentPlatformOrLegacyGovernanceBypass(t *testing.T) {
	t.Parallel()

	rbacActor := mustUserActor(t, 1, 1, nil, []Capability{
		CapabilityPlatformResourceManageAll,
		CapabilityPlatformResourceOperateAll,
		CapabilityPlatformResourceViewAll,
	}, false)
	rbacConfiguration := mustPolicyConfiguration(t, PolicyConfigurationInput{RoleAuthorizationMode: RoleAuthorizationModeRBAC})
	rbacScope, err := NewPolicyService(&stubPolicyStore{
		subjectSnapshot: mustSubjectSnapshotForActor(t, rbacActor, rbacConfiguration, false),
	}).AccessibleScope(context.Background(), rbacActor, ResourceTypeAccount, ActionAccountView)
	if err != nil {
		t.Fatalf("build platform scope while features are off: %v", err)
	}
	capability, ok := rbacScope.PlatformCapabilityBypass()
	if !ok || capability != CapabilityPlatformResourceViewAll || rbacScope.LegacyAdminBypass() {
		t.Fatalf("wrong rbac scope bypass: %q, %v", capability, ok)
	}

	legacyActor := mustUserActor(t, 2, 1, nil, nil, true)
	legacyConfiguration := mustPolicyConfiguration(t, PolicyConfigurationInput{RoleAuthorizationMode: RoleAuthorizationModeShadow})
	legacyScope, err := NewPolicyService(&stubPolicyStore{
		subjectSnapshot: mustSubjectSnapshotForActor(t, legacyActor, legacyConfiguration, true),
	}).AccessibleScope(context.Background(), legacyActor, ResourceTypeGroup, ActionGroupDelete)
	if err != nil {
		t.Fatalf("build legacy governance scope while features are off: %v", err)
	}
	if !legacyScope.LegacyAdminBypass() {
		t.Fatal("trusted shadow legacy admin lacks governance bypass")
	}
	if _, ok := legacyScope.PlatformCapabilityBypass(); ok {
		t.Fatal("shadow scope used rbac platform bypass")
	}

	corruptPlatform := rbacScope
	corruptPlatform.capabilities = nil
	if corruptPlatform.Valid() {
		t.Fatal("platform bypass absent from capability snapshot remained valid")
	}
	corruptLegacy := principalShapedScopeForValidation(t)
	corruptLegacy.legacyAdminBypass = true
	if corruptLegacy.Valid() {
		t.Fatal("service principal scope accepted legacy admin bypass")
	}
}

func principalShapedScopeForValidation(t testing.TB) AccessibleScope {
	t.Helper()
	configuration := fullyEnabledConfiguration(t, RoleAuthorizationModeShadow)
	actor := mustServicePrincipalActor(t, 77, 1, map[int64]int64{4: 1}, nil)
	scope, err := NewPolicyService(&stubPolicyStore{
		subjectSnapshot: mustSubjectSnapshotForActor(t, actor, configuration, false),
	}).AccessibleScope(context.Background(), actor, ResourceTypeGroup, ActionGroupView)
	if err != nil {
		t.Fatalf("build principal validation scope: %v", err)
	}
	return scope
}

func TestAccessibleScopeReturnsTypedFailClosedErrors(t *testing.T) {
	t.Parallel()

	actor := mustUserActor(t, 12, 3, map[int64]int64{5: 2}, []Capability{CapabilityGroupCreate}, false)
	subject, _ := subjectRefFromActor(actor)
	configuration := fullyEnabledConfiguration(t, RoleAuthorizationModeRBAC)
	systemActor, systemErr := newSystemActor("scheduler")
	if systemErr != nil {
		t.Fatalf("create system actor: %v", systemErr)
	}

	localStore := &stubPolicyStore{}
	service := NewPolicyService(localStore)
	if _, err := service.AccessibleScope(context.Background(), Actor{}, ResourceTypeGroup, ActionGroupView); !errors.Is(err, ErrInvalidActor) {
		t.Fatalf("invalid actor error: %v", err)
	}
	if _, err := service.AccessibleScope(context.Background(), systemActor, ResourceTypeGroup, ActionGroupView); !errors.Is(err, ErrPolicyAccessDenied) {
		t.Fatalf("system actor error: %v", err)
	}
	if _, err := service.AccessibleScope(context.Background(), actor, ResourceTypeAccount, ActionGroupView); !errors.Is(err, ErrInvalidResourceRef) {
		t.Fatalf("mismatched action error: %v", err)
	}
	if _, err := service.AccessibleScope(context.Background(), actor, ResourceTypeGroup, ActionGroupUse); !errors.Is(err, ErrLegacyGroupAuthorityRequired) {
		t.Fatalf("group.use authority error: %v", err)
	}
	if localStore.subjectCalls != 0 || localStore.resourceCalls != 0 {
		t.Fatalf("invalid local scope inputs called store: subject=%d resource=%d", localStore.subjectCalls, localStore.resourceCalls)
	}

	missing, err := NewSubjectSnapshot(SubjectSnapshotInput{Subject: subject, Configuration: configuration})
	if err != nil {
		t.Fatalf("create missing subject snapshot: %v", err)
	}
	if _, err = NewPolicyService(&stubPolicyStore{subjectSnapshot: missing}).AccessibleScope(context.Background(), actor, ResourceTypeGroup, ActionGroupView); !errors.Is(err, ErrActorInactive) {
		t.Fatalf("missing actor error: %v", err)
	}
	stale, err := NewSubjectSnapshot(SubjectSnapshotInput{
		Subject:       subject,
		Exists:        true,
		Active:        true,
		AuthzVersion:  4,
		RoleVersions:  actor.roleVersionsSnapshot(),
		Capabilities:  actor.capabilitiesSnapshot(),
		Configuration: configuration,
	})
	if err != nil {
		t.Fatalf("create stale snapshot: %v", err)
	}
	if _, err = NewPolicyService(&stubPolicyStore{subjectSnapshot: stale}).AccessibleScope(context.Background(), actor, ResourceTypeGroup, ActionGroupView); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("stale session error: %v", err)
	}
	if _, err = NewPolicyService(&stubPolicyStore{subjectErr: errors.New("db down")}).AccessibleScope(context.Background(), actor, ResourceTypeGroup, ActionGroupView); !errors.Is(err, ErrAuthorizationUnavailable) {
		t.Fatalf("store failure error: %v", err)
	}
	if _, err = NewPolicyService(&stubPolicyStore{}).AccessibleScope(context.Background(), actor, ResourceTypeGroup, ActionGroupView); !errors.Is(err, ErrAuthorizationUnavailable) {
		t.Fatalf("malformed snapshot error: %v", err)
	}

	featureOff := mustPolicyConfiguration(t, PolicyConfigurationInput{RoleAuthorizationMode: RoleAuthorizationModeRBAC})
	if _, err = NewPolicyService(&stubPolicyStore{
		subjectSnapshot: mustSubjectSnapshotForActor(t, actor, featureOff, false),
	}).AccessibleScope(context.Background(), actor, ResourceTypeGroup, ActionGroupView); !errors.Is(err, ErrFeatureDisabled) {
		t.Fatalf("feature gate error: %v", err)
	}
}

func TestZeroAccessibleScopeIsInvalidAndOpaque(t *testing.T) {
	t.Parallel()

	scope := AccessibleScope{}
	if scope.Valid() || scope.ResourceType() != "" || scope.Action() != "" || scope.SubjectKind() != "" || scope.SubjectID() != 0 ||
		scope.SubjectAuthzVersion() != 0 || scope.RoleVersions() != nil || scope.RoleIDs() != nil || scope.Capabilities() != nil ||
		scope.RoleMode() != "" || scope.LegacyAdminBypass() || scope.IncludesOwner() || scope.IncludesPublicAccess() ||
		scope.IncludesDirectUserGrants() || scope.IncludesRoleGrants() || scope.PublicAccessLevels() != nil || scope.GrantAccessLevels() != nil {
		t.Fatalf("zero scope exposed trusted state: %+v", scope)
	}
	if _, ok := scope.PlatformCapabilityBypass(); ok {
		t.Fatal("zero scope exposed platform bypass")
	}
}
