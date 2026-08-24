package authz

import (
	"context"
	"errors"
	"testing"
)

func TestCheckCapabilityUsesExactlyOneAuthoritativeMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		mode               RoleAuthorizationMode
		servicePrincipal   bool
		actorLegacyAdmin   bool
		currentLegacyAdmin bool
		capabilities       []Capability
		requested          Capability
		allowed            bool
		source             MatchSource
		reason             DenyReason
	}{
		{
			name:      "legacy active user keeps api key creation",
			mode:      RoleAuthorizationModeLegacy,
			requested: CapabilityAPIKeyCreate,
			allowed:   true,
			source:    MatchSourceLegacyUser,
		},
		{
			name:      "shadow active user keeps api key creation",
			mode:      RoleAuthorizationModeShadow,
			requested: CapabilityAPIKeyCreate,
			allowed:   true,
			source:    MatchSourceLegacyUser,
		},
		{
			name:               "legacy admin uses old authority without rbac capability",
			mode:               RoleAuthorizationModeLegacy,
			actorLegacyAdmin:   true,
			currentLegacyAdmin: true,
			requested:          CapabilityAccountCreate,
			allowed:            true,
			source:             MatchSourceLegacyAdmin,
		},
		{
			name:               "legacy admin cannot export secrets",
			mode:               RoleAuthorizationModeLegacy,
			actorLegacyAdmin:   true,
			currentLegacyAdmin: true,
			requested:          CapabilityPlatformSecretExport,
			reason:             DenyReasonMissingCapability,
		},
		{
			name:               "legacy db flag alone cannot elevate user",
			mode:               RoleAuthorizationModeLegacy,
			currentLegacyAdmin: true,
			capabilities:       []Capability{CapabilityAccountCreate},
			requested:          CapabilityAccountCreate,
			reason:             DenyReasonMissingCapability,
		},
		{
			name:         "shadow rbac capability only compares and does not allow",
			mode:         RoleAuthorizationModeShadow,
			capabilities: []Capability{CapabilityAccountCreate},
			requested:    CapabilityAccountCreate,
			reason:       DenyReasonMissingCapability,
		},
		{
			name:             "shadow actor admin flag alone cannot elevate user",
			mode:             RoleAuthorizationModeShadow,
			actorLegacyAdmin: true,
			capabilities:     []Capability{CapabilityAccountCreate},
			requested:        CapabilityAccountCreate,
			reason:           DenyReasonMissingCapability,
		},
		{
			name:         "rbac capability allows user",
			mode:         RoleAuthorizationModeRBAC,
			capabilities: []Capability{CapabilityAccountCreate},
			requested:    CapabilityAccountCreate,
			allowed:      true,
			source:       MatchSourcePlatformCapability,
		},
		{
			name:               "rbac ignores stale legacy admin state",
			mode:               RoleAuthorizationModeRBAC,
			actorLegacyAdmin:   true,
			currentLegacyAdmin: true,
			requested:          CapabilityAccountCreate,
			reason:             DenyReasonMissingCapability,
		},
		{
			name:             "rbac capability allows service principal",
			mode:             RoleAuthorizationModeRBAC,
			servicePrincipal: true,
			capabilities:     []Capability{CapabilityGroupCreate},
			requested:        CapabilityGroupCreate,
			allowed:          true,
			source:           MatchSourcePlatformCapability,
		},
		{
			name:             "legacy user compatibility excludes service principal",
			mode:             RoleAuthorizationModeLegacy,
			servicePrincipal: true,
			capabilities:     []Capability{CapabilityAPIKeyCreate},
			requested:        CapabilityAPIKeyCreate,
			reason:           DenyReasonMissingCapability,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			var actor Actor
			if testCase.servicePrincipal {
				actor = mustServicePrincipalActor(t, 71, 2, nil, testCase.capabilities)
			} else {
				actor = mustUserActor(t, 41, 2, nil, testCase.capabilities, testCase.actorLegacyAdmin)
			}
			snapshot := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, testCase.mode), testCase.currentLegacyAdmin)
			store := &stubPolicyStore{subjectSnapshot: snapshot}
			decision, err := NewPolicyService(store).CheckCapability(context.Background(), actor, testCase.requested)
			if err != nil {
				t.Fatalf("check capability: %v", err)
			}
			if decision.Allowed() != testCase.allowed || decision.MatchSource() != testCase.source {
				t.Fatalf("unexpected decision: allowed=%v source=%q reason=%q", decision.Allowed(), decision.MatchSource(), decision.DenyReason())
			}
			if !testCase.allowed && decision.DenyReason() != testCase.reason {
				t.Fatalf("unexpected denial: got %q want %q", decision.DenyReason(), testCase.reason)
			}
			if testCase.source == MatchSourcePlatformCapability {
				provenance, ok := decision.Provenance()
				if !ok {
					t.Fatal("rbac allow lacks provenance")
				}
				capability, ok := provenance.Capability()
				if !ok || capability != testCase.requested {
					t.Fatalf("wrong capability provenance: %q, %v", capability, ok)
				}
			}
			if store.subjectCalls != 1 || store.resourceCalls != 0 {
				t.Fatalf("unexpected store calls: subject=%d resource=%d", store.subjectCalls, store.resourceCalls)
			}
		})
	}
}

func TestCanCreateEnforcesCapabilityModeAndFeatureGates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		mode               RoleAuthorizationMode
		configuration      PolicyConfigurationInput
		capabilities       []Capability
		actorLegacyAdmin   bool
		currentLegacyAdmin bool
		allowed            bool
		source             MatchSource
		reason             DenyReason
	}{
		{
			name:         "rbac self service user",
			mode:         RoleAuthorizationModeRBAC,
			capabilities: []Capability{CapabilityAccountCreate},
			allowed:      true,
			source:       MatchSourcePlatformCapability,
		},
		{
			name: "ordinary user fails closed when self service is off",
			mode: RoleAuthorizationModeRBAC,
			configuration: PolicyConfigurationInput{
				RoleAuthorizationMode:        RoleAuthorizationModeRBAC,
				ResourceAccessControlEnabled: true,
			},
			capabilities: []Capability{CapabilityAccountCreate},
			reason:       DenyReasonFeatureDisabled,
		},
		{
			name: "rbac governance authority survives feature rollback",
			mode: RoleAuthorizationModeRBAC,
			configuration: PolicyConfigurationInput{
				RoleAuthorizationMode: RoleAuthorizationModeRBAC,
			},
			capabilities: []Capability{CapabilityAccountCreate, CapabilityPlatformResourceManageAll},
			allowed:      true,
			source:       MatchSourcePlatformCapability,
		},
		{
			name: "legacy admin survives feature rollback",
			mode: RoleAuthorizationModeLegacy,
			configuration: PolicyConfigurationInput{
				RoleAuthorizationMode: RoleAuthorizationModeLegacy,
			},
			actorLegacyAdmin:   true,
			currentLegacyAdmin: true,
			allowed:            true,
			source:             MatchSourceLegacyAdmin,
		},
		{
			name:         "shadow rbac create capability is non-authoritative",
			mode:         RoleAuthorizationModeShadow,
			capabilities: []Capability{CapabilityAccountCreate},
			reason:       DenyReasonMissingCapability,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			actor := mustUserActor(t, 5, 1, nil, testCase.capabilities, testCase.actorLegacyAdmin)
			var configuration PolicyConfiguration
			if testCase.configuration.RoleAuthorizationMode.Valid() {
				configuration = mustPolicyConfiguration(t, testCase.configuration)
			} else {
				configuration = fullyEnabledConfiguration(t, testCase.mode)
			}
			store := &stubPolicyStore{
				subjectSnapshot: mustSubjectSnapshotForActor(t, actor, configuration, testCase.currentLegacyAdmin),
			}
			decision, err := NewPolicyService(store).CanCreate(context.Background(), actor, ResourceTypeAccount)
			if err != nil {
				t.Fatalf("can create: %v", err)
			}
			if decision.Allowed() != testCase.allowed || decision.MatchSource() != testCase.source || (!testCase.allowed && decision.DenyReason() != testCase.reason) {
				t.Fatalf("unexpected decision: allowed=%v source=%q reason=%q", decision.Allowed(), decision.MatchSource(), decision.DenyReason())
			}
		})
	}
}

func TestAdminAPIKeyLegacyCompatibilityIsStrict(t *testing.T) {
	t.Parallel()

	actor := mustAdminAPIKeyActor(t, 91, 7, nil, nil)
	ref := mustResourceRef(t, ResourceTypeAccount, 44)
	for _, mode := range []RoleAuthorizationMode{RoleAuthorizationModeLegacy, RoleAuthorizationModeShadow} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			subject := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, mode), false)
			createDecision, err := NewPolicyService(&stubPolicyStore{subjectSnapshot: subject}).CanCreate(
				context.Background(), actor, ResourceTypeAccount,
			)
			if err != nil || !createDecision.Allowed() || createDecision.MatchSource() != MatchSourceLegacyAdmin {
				t.Fatalf("admin API key create denied: source=%q reason=%q err=%v", createDecision.MatchSource(), createDecision.DenyReason(), err)
			}

			resource := mustResourceSnapshot(t, ResourceAccessSnapshotInput{
				Subject:       subject,
				Resource:      ref,
				Exists:        true,
				AccessVersion: 1,
			})
			authorizeDecision, err := NewPolicyService(&stubPolicyStore{resourceSnapshot: resource}).Authorize(
				context.Background(), actor, ActionAccountEdit, ref,
			)
			if err != nil || !authorizeDecision.Allowed() || authorizeDecision.MatchSource() != MatchSourceLegacyAdmin {
				t.Fatalf("admin API key resource edit denied: source=%q reason=%q err=%v", authorizeDecision.MatchSource(), authorizeDecision.DenyReason(), err)
			}
		})
	}

	legacySnapshot := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeLegacy), false)
	secretDecision, err := NewPolicyService(&stubPolicyStore{subjectSnapshot: legacySnapshot}).CheckCapability(
		context.Background(), actor, CapabilityPlatformSecretExport,
	)
	if err != nil || secretDecision.Allowed() || secretDecision.DenyReason() != DenyReasonMissingCapability {
		t.Fatalf("admin API key secret export bypassed policy: reason=%q err=%v", secretDecision.DenyReason(), err)
	}

	rbacSnapshot := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeRBAC), false)
	rbacDecision, err := NewPolicyService(&stubPolicyStore{subjectSnapshot: rbacSnapshot}).CanCreate(
		context.Background(), actor, ResourceTypeAccount,
	)
	if err != nil || rbacDecision.Allowed() || rbacDecision.DenyReason() != DenyReasonMissingCapability {
		t.Fatalf("admin API key bypass survived RBAC mode: reason=%q err=%v", rbacDecision.DenyReason(), err)
	}

	subject, _ := subjectRefFromActor(actor)
	staleSnapshot, err := NewSubjectSnapshot(SubjectSnapshotInput{
		Subject:       subject,
		Exists:        true,
		Active:        true,
		AuthzVersion:  actor.subjectVersion() + 1,
		Configuration: fullyEnabledConfiguration(t, RoleAuthorizationModeLegacy),
	})
	if err != nil {
		t.Fatalf("create stale admin API key snapshot: %v", err)
	}
	staleDecision, err := NewPolicyService(&stubPolicyStore{subjectSnapshot: staleSnapshot}).CanCreate(
		context.Background(), actor, ResourceTypeAccount,
	)
	if err != nil || staleDecision.Allowed() || staleDecision.DenyReason() != DenyReasonSessionInvalid {
		t.Fatalf("stale admin API key snapshot accepted: reason=%q err=%v", staleDecision.DenyReason(), err)
	}
	staleResource := mustResourceSnapshot(t, ResourceAccessSnapshotInput{
		Subject:       staleSnapshot,
		Resource:      ref,
		Exists:        true,
		AccessVersion: 1,
	})
	staleDecision, err = NewPolicyService(&stubPolicyStore{resourceSnapshot: staleResource}).Authorize(
		context.Background(), actor, ActionAccountEdit, ref,
	)
	if err != nil || staleDecision.Allowed() || staleDecision.DenyReason() != DenyReasonSessionInvalid {
		t.Fatalf("stale admin API key resource snapshot accepted: reason=%q err=%v", staleDecision.DenyReason(), err)
	}

	roleActor := mustAdminAPIKeyActor(t, 92, 3, map[int64]int64{8: 1}, nil)
	roleSnapshot := mustSubjectSnapshotForActor(t, roleActor, fullyEnabledConfiguration(t, RoleAuthorizationModeLegacy), false)
	roleDecision, err := NewPolicyService(&stubPolicyStore{subjectSnapshot: roleSnapshot}).CanCreate(
		context.Background(), roleActor, ResourceTypeAccount,
	)
	if err != nil || roleDecision.Allowed() || roleDecision.DenyReason() != DenyReasonMissingCapability {
		t.Fatalf("role-bearing admin API key gained legacy bypass: reason=%q err=%v", roleDecision.DenyReason(), err)
	}
}

func TestPolicyRevalidatesCurrentSubjectState(t *testing.T) {
	t.Parallel()

	actor := mustUserActor(t, 12, 3, map[int64]int64{5: 2}, []Capability{CapabilityAccountCreate}, false)
	subject, _ := subjectRefFromActor(actor)
	configuration := fullyEnabledConfiguration(t, RoleAuthorizationModeRBAC)
	tests := []struct {
		name         string
		exists       bool
		active       bool
		version      int64
		roles        map[int64]int64
		capabilities []Capability
		reason       DenyReason
	}{
		{name: "missing subject", reason: DenyReasonActorInactive},
		{name: "inactive subject", exists: true, version: 3, roles: map[int64]int64{5: 2}, capabilities: []Capability{CapabilityAccountCreate}, reason: DenyReasonActorInactive},
		{name: "subject version changed", exists: true, active: true, version: 4, roles: map[int64]int64{5: 2}, capabilities: []Capability{CapabilityAccountCreate}, reason: DenyReasonSessionInvalid},
		{name: "role version changed", exists: true, active: true, version: 3, roles: map[int64]int64{5: 3}, capabilities: []Capability{CapabilityAccountCreate}, reason: DenyReasonSessionInvalid},
		{name: "capability set changed", exists: true, active: true, version: 3, roles: map[int64]int64{5: 2}, capabilities: []Capability{CapabilityGroupCreate}, reason: DenyReasonSessionInvalid},
	}
	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			snapshot, err := NewSubjectSnapshot(SubjectSnapshotInput{
				Subject:       subject,
				Exists:        testCase.exists,
				Active:        testCase.active,
				AuthzVersion:  testCase.version,
				RoleVersions:  testCase.roles,
				Capabilities:  testCase.capabilities,
				Configuration: configuration,
			})
			if err != nil {
				t.Fatalf("create test snapshot: %v", err)
			}
			decision, checkErr := NewPolicyService(&stubPolicyStore{subjectSnapshot: snapshot}).CheckCapability(context.Background(), actor, CapabilityAccountCreate)
			if checkErr != nil || decision.Allowed() || decision.DenyReason() != testCase.reason {
				t.Fatalf("unexpected stale-session decision: reason=%q err=%v", decision.DenyReason(), checkErr)
			}
		})
	}
}

func TestAuthorizeAllowsOwnerForEveryResourceAction(t *testing.T) {
	t.Parallel()

	actor := mustUserActor(t, 90, 1, nil, nil, false)
	subject := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeRBAC), false)
	for index, action := range AllActions() {
		action := action
		t.Run(string(action), func(t *testing.T) {
			t.Parallel()
			resourceType, _ := action.ResourceType()
			ref := mustResourceRef(t, resourceType, int64(index+1))
			store := &stubPolicyStore{resourceSnapshot: mustResourceSnapshot(t, ResourceAccessSnapshotInput{
				Subject:       subject,
				Resource:      ref,
				Exists:        true,
				OwnerUserID:   int64Pointer(90),
				AccessVersion: 1,
			})}
			decision, err := NewPolicyService(store).Authorize(context.Background(), actor, action, ref)
			if err != nil || !decision.Allowed() || decision.MatchSource() != MatchSourceOwner {
				t.Fatalf("owner denied %q: allowed=%v source=%q reason=%q err=%v", action, decision.Allowed(), decision.MatchSource(), decision.DenyReason(), err)
			}
		})
	}
}

func TestAuthorizeChoosesHighestAccessThenStableProvenance(t *testing.T) {
	t.Parallel()

	actor := mustUserActor(t, 20, 1, map[int64]int64{3: 1, 7: 1}, nil, false)
	subject := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeRBAC), false)
	ref := mustResourceRef(t, ResourceTypeGroup, 7)
	tests := []struct {
		name       string
		action     Action
		public     *AccessLevel
		userGrants []GrantSnapshot
		roleGrants []GrantSnapshot
		source     MatchSource
		level      AccessLevel
		grantID    int64
		roleID     int64
	}{
		{
			name:       "highest level beats source priority",
			action:     ActionGroupEdit,
			public:     accessLevelPointer(AccessLevelConsumer),
			userGrants: []GrantSnapshot{mustUserGrant(t, 4, AccessLevelViewer)},
			roleGrants: []GrantSnapshot{mustRoleGrant(t, 9, 7, AccessLevelManager)},
			source:     MatchSourceRoleGrant,
			level:      AccessLevelManager,
			grantID:    9,
			roleID:     7,
		},
		{
			name:       "public wins equal level",
			action:     ActionGroupUse,
			public:     accessLevelPointer(AccessLevelConsumer),
			userGrants: []GrantSnapshot{mustUserGrant(t, 4, AccessLevelConsumer)},
			roleGrants: []GrantSnapshot{mustRoleGrant(t, 9, 7, AccessLevelConsumer)},
			source:     MatchSourcePublicAccess,
			level:      AccessLevelConsumer,
		},
		{
			name:       "smallest direct grant id wins",
			action:     ActionGroupEdit,
			userGrants: []GrantSnapshot{mustUserGrant(t, 9, AccessLevelManager), mustUserGrant(t, 2, AccessLevelManager)},
			source:     MatchSourceUserGrant,
			level:      AccessLevelManager,
			grantID:    2,
		},
		{
			name:       "role id breaks equal role grant tie",
			action:     ActionGroupEdit,
			roleGrants: []GrantSnapshot{mustRoleGrant(t, 10, 7, AccessLevelManager), mustRoleGrant(t, 10, 3, AccessLevelManager)},
			source:     MatchSourceRoleGrant,
			level:      AccessLevelManager,
			grantID:    10,
			roleID:     3,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			store := &stubPolicyStore{resourceSnapshot: mustResourceSnapshot(t, ResourceAccessSnapshotInput{
				Subject:           subject,
				Resource:          ref,
				Exists:            true,
				OwnerUserID:       int64Pointer(999),
				PublicAccessLevel: testCase.public,
				AccessVersion:     1,
				UserGrants:        testCase.userGrants,
				RoleGrants:        testCase.roleGrants,
			})}
			decision, err := NewPolicyService(store).Authorize(context.Background(), actor, testCase.action, ref)
			if err != nil || !decision.Allowed() || decision.MatchSource() != testCase.source {
				t.Fatalf("unexpected access match: source=%q reason=%q err=%v", decision.MatchSource(), decision.DenyReason(), err)
			}
			if level, ok := decision.AccessLevel(); !ok || level != testCase.level {
				t.Fatalf("wrong access level: %q, %v", level, ok)
			}
			provenance, _ := decision.Provenance()
			if testCase.grantID > 0 {
				if grantID, ok := provenance.GrantID(); !ok || grantID != testCase.grantID {
					t.Fatalf("wrong grant id: %d, %v", grantID, ok)
				}
			}
			if testCase.roleID > 0 {
				if roleID, ok := provenance.GranteeRoleID(); !ok || roleID != testCase.roleID {
					t.Fatalf("wrong role id: %d, %v", roleID, ok)
				}
			}
		})
	}
}

func TestAuthorizeGroupUseRequiresPerResourceAuthorityResolver(t *testing.T) {
	t.Parallel()

	actor := mustUserActor(t, 20, 1, nil, nil, false)
	subject := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeRBAC), false)
	ref := mustResourceRef(t, ResourceTypeGroup, 18)

	for _, mode := range []GroupAuthorizationMode{GroupAuthorizationModeLegacy, GroupAuthorizationModeShadow} {
		mode := mode
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			resource := mustResourceSnapshot(t, ResourceAccessSnapshotInput{
				Subject:                subject,
				Resource:               ref,
				GroupAuthorizationMode: mode,
				Exists:                 true,
				OwnerUserID:            int64Pointer(20),
				PublicAccessLevel:      accessLevelPointer(AccessLevelConsumer),
				AccessVersion:          1,
			})
			decision, err := NewPolicyService(&stubPolicyStore{resourceSnapshot: resource}).Authorize(
				context.Background(), actor, ActionGroupUse, ref,
			)
			if !errors.Is(err, ErrLegacyGroupAuthorityRequired) || decision.Allowed() ||
				decision.DenyReason() != DenyReasonLegacyGroupAuthorityRequired {
				t.Fatalf("%s authority accepted ACL path: reason=%q err=%v", mode, decision.DenyReason(), err)
			}
		})
	}

	aclResource := mustResourceSnapshot(t, ResourceAccessSnapshotInput{
		Subject:                subject,
		Resource:               ref,
		GroupAuthorizationMode: GroupAuthorizationModeACL,
		Exists:                 true,
		PublicAccessLevel:      accessLevelPointer(AccessLevelConsumer),
		AccessVersion:          1,
	})
	decision, err := NewPolicyService(&stubPolicyStore{resourceSnapshot: aclResource}).Authorize(
		context.Background(), actor, ActionGroupUse, ref,
	)
	if err != nil || !decision.Allowed() || decision.MatchSource() != MatchSourcePublicAccess {
		t.Fatalf("ACL group.use denied: source=%q reason=%q err=%v", decision.MatchSource(), decision.DenyReason(), err)
	}
}

func TestAuthorizeGroupUseKeepsLegacyAdminAuthority(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name  string
		actor Actor
	}{
		{name: "JWT admin", actor: mustUserActor(t, 21, 1, nil, nil, true)},
		{name: "admin API key", actor: mustAdminAPIKeyActor(t, 22, 1, nil, nil)},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			for _, roleMode := range []RoleAuthorizationMode{RoleAuthorizationModeLegacy, RoleAuthorizationModeShadow} {
				roleMode := roleMode
				t.Run(string(roleMode), func(t *testing.T) {
					t.Parallel()
					subject := mustSubjectSnapshotForActor(t, testCase.actor, fullyEnabledConfiguration(t, roleMode), testCase.actor.Kind() == SubjectKindUser)
					ref := mustResourceRef(t, ResourceTypeGroup, 19)
					for _, groupMode := range []GroupAuthorizationMode{GroupAuthorizationModeLegacy, GroupAuthorizationModeShadow} {
						resource := mustResourceSnapshot(t, ResourceAccessSnapshotInput{
							Subject:                subject,
							Resource:               ref,
							GroupAuthorizationMode: groupMode,
							Exists:                 true,
							AccessVersion:          1,
						})
						decision, err := NewPolicyService(&stubPolicyStore{resourceSnapshot: resource}).Authorize(
							context.Background(), testCase.actor, ActionGroupUse, ref,
						)
						if err != nil || !decision.Allowed() || decision.MatchSource() != MatchSourceLegacyAdmin {
							t.Fatalf("legacy admin group.use denied: role_mode=%s group_mode=%s source=%q reason=%q err=%v",
								roleMode, groupMode, decision.MatchSource(), decision.DenyReason(), err)
						}
					}
				})
			}
		})
	}
}

func TestServicePrincipalUsesOnlyRoleAndPlatformResourceSources(t *testing.T) {
	t.Parallel()

	actor := mustServicePrincipalActor(t, 55, 1, map[int64]int64{7: 1}, nil)
	subject := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeRBAC), false)
	ref := mustResourceRef(t, ResourceTypeGroup, 3)
	base := ResourceAccessSnapshotInput{
		Subject:           subject,
		Resource:          ref,
		Exists:            true,
		OwnerUserID:       int64Pointer(55),
		PublicAccessLevel: accessLevelPointer(AccessLevelConsumer),
		AccessVersion:     1,
	}
	withRole := base
	withRole.RoleGrants = []GrantSnapshot{mustRoleGrant(t, 2, 7, AccessLevelConsumer)}
	decision, err := NewPolicyService(&stubPolicyStore{resourceSnapshot: mustResourceSnapshot(t, withRole)}).Authorize(context.Background(), actor, ActionGroupUse, ref)
	if err != nil || !decision.Allowed() || decision.MatchSource() != MatchSourceRoleGrant {
		t.Fatalf("service principal did not use role grant: source=%q reason=%q err=%v", decision.MatchSource(), decision.DenyReason(), err)
	}
	decision, err = NewPolicyService(&stubPolicyStore{resourceSnapshot: mustResourceSnapshot(t, base)}).Authorize(context.Background(), actor, ActionGroupView, ref)
	if err != nil || decision.Allowed() || decision.DenyReason() != DenyReasonNoMatchingAccess {
		t.Fatalf("service principal used user-only source: source=%q reason=%q err=%v", decision.MatchSource(), decision.DenyReason(), err)
	}
}

func TestAuthorizeDistinguishesVisibilityFeatureAndLifecycleDenials(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		mode               RoleAuthorizationMode
		configuration      PolicyConfiguration
		capabilities       []Capability
		actorLegacyAdmin   bool
		currentLegacyAdmin bool
		exists             bool
		deleted            bool
		owner              *int64
		public             *AccessLevel
		userGrants         []GrantSnapshot
		roleGrants         []GrantSnapshot
		action             Action
		allowed            bool
		source             MatchSource
		reason             DenyReason
	}{
		{name: "invisible resource", mode: RoleAuthorizationModeRBAC, exists: true, action: ActionGroupView, reason: DenyReasonNoMatchingAccess},
		{name: "visible grant lacks action", mode: RoleAuthorizationModeRBAC, exists: true, userGrants: []GrantSnapshot{mustUserGrant(t, 1, AccessLevelViewer)}, action: ActionGroupEdit, reason: DenyReasonInsufficientAccess},
		{name: "platform view makes edit denial visible", mode: RoleAuthorizationModeRBAC, capabilities: []Capability{CapabilityPlatformResourceViewAll}, exists: true, action: ActionGroupEdit, reason: DenyReasonInsufficientAccess},
		{name: "missing resource", mode: RoleAuthorizationModeRBAC, action: ActionGroupView, reason: DenyReasonResourceNotFound},
		{name: "deleted resource", mode: RoleAuthorizationModeRBAC, exists: true, deleted: true, owner: int64Pointer(3), action: ActionGroupView, reason: DenyReasonResourceDeleted},
		{name: "master gate disables owner path", mode: RoleAuthorizationModeRBAC, configuration: mustPolicyConfiguration(t, PolicyConfigurationInput{RoleAuthorizationMode: RoleAuthorizationModeRBAC}), exists: true, owner: int64Pointer(3), action: ActionGroupView, reason: DenyReasonFeatureDisabled},
		{name: "legacy admin governs while flags are off", mode: RoleAuthorizationModeLegacy, configuration: mustPolicyConfiguration(t, PolicyConfigurationInput{RoleAuthorizationMode: RoleAuthorizationModeLegacy}), actorLegacyAdmin: true, currentLegacyAdmin: true, exists: true, action: ActionGroupDelete, allowed: true, source: MatchSourceLegacyAdmin},
		{name: "shadow legacy admin remains authoritative", mode: RoleAuthorizationModeShadow, configuration: mustPolicyConfiguration(t, PolicyConfigurationInput{RoleAuthorizationMode: RoleAuthorizationModeShadow}), actorLegacyAdmin: true, currentLegacyAdmin: true, exists: true, action: ActionGroupDelete, allowed: true, source: MatchSourceLegacyAdmin},
		{name: "rbac ignores legacy admin residue", mode: RoleAuthorizationModeRBAC, actorLegacyAdmin: true, currentLegacyAdmin: true, exists: true, action: ActionGroupView, reason: DenyReasonNoMatchingAccess},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			configuration := testCase.configuration
			if !configuration.Valid() {
				configuration = fullyEnabledConfiguration(t, testCase.mode)
			}
			actor := mustUserActor(t, 3, 1, nil, testCase.capabilities, testCase.actorLegacyAdmin)
			subject := mustSubjectSnapshotForActor(t, actor, configuration, testCase.currentLegacyAdmin)
			ref := mustResourceRef(t, ResourceTypeGroup, 60)
			input := ResourceAccessSnapshotInput{Subject: subject, Resource: ref}
			if testCase.exists {
				input.Exists = true
				input.Deleted = testCase.deleted
				input.OwnerUserID = testCase.owner
				input.PublicAccessLevel = testCase.public
				input.AccessVersion = 1
				input.UserGrants = testCase.userGrants
				input.RoleGrants = testCase.roleGrants
			}
			decision, err := NewPolicyService(&stubPolicyStore{resourceSnapshot: mustResourceSnapshot(t, input)}).Authorize(context.Background(), actor, testCase.action, ref)
			if err != nil || decision.Allowed() != testCase.allowed || decision.MatchSource() != testCase.source || (!testCase.allowed && decision.DenyReason() != testCase.reason) {
				t.Fatalf("unexpected decision: allowed=%v source=%q reason=%q err=%v", decision.Allowed(), decision.MatchSource(), decision.DenyReason(), err)
			}
		})
	}
}

func TestAuthorizeHonorsSharingAndRoleGrantFeatureFlags(t *testing.T) {
	t.Parallel()

	actor := mustUserActor(t, 4, 1, map[int64]int64{8: 1}, nil, false)
	ref := mustResourceRef(t, ResourceTypeGroup, 1)
	tests := []struct {
		name          string
		configuration PolicyConfiguration
		userGrants    []GrantSnapshot
		roleGrants    []GrantSnapshot
		reason        DenyReason
	}{
		{
			name: "group sharing excludes direct grant",
			configuration: mustPolicyConfiguration(t, PolicyConfigurationInput{
				RoleAuthorizationMode:        RoleAuthorizationModeRBAC,
				ResourceAccessControlEnabled: true,
				SelfServiceHostingEnabled:    true,
			}),
			userGrants: []GrantSnapshot{mustUserGrant(t, 1, AccessLevelViewer)},
			reason:     DenyReasonNoMatchingAccess,
		},
		{
			name: "role grant flag excludes role grant",
			configuration: mustPolicyConfiguration(t, PolicyConfigurationInput{
				RoleAuthorizationMode:        RoleAuthorizationModeRBAC,
				ResourceAccessControlEnabled: true,
				SelfServiceHostingEnabled:    true,
				GroupSharingEnabled:          true,
			}),
			roleGrants: []GrantSnapshot{mustRoleGrant(t, 1, 8, AccessLevelViewer)},
			reason:     DenyReasonNoMatchingAccess,
		},
	}
	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			subject := mustSubjectSnapshotForActor(t, actor, testCase.configuration, false)
			resource := mustResourceSnapshot(t, ResourceAccessSnapshotInput{
				Subject:       subject,
				Resource:      ref,
				Exists:        true,
				AccessVersion: 1,
				UserGrants:    testCase.userGrants,
				RoleGrants:    testCase.roleGrants,
			})
			decision, err := NewPolicyService(&stubPolicyStore{resourceSnapshot: resource}).Authorize(context.Background(), actor, ActionGroupView, ref)
			if err != nil || decision.DenyReason() != testCase.reason {
				t.Fatalf("disabled grant source was used: reason=%q err=%v", decision.DenyReason(), err)
			}
		})
	}
}

func TestPlatformResourceCapabilitiesAuthorizeWithNarrowestProvenance(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		action       Action
		capabilities []Capability
		matched      Capability
	}{
		{
			name:         "view all is narrower than operate and manage",
			action:       ActionAccountView,
			capabilities: []Capability{CapabilityPlatformResourceManageAll, CapabilityPlatformResourceOperateAll, CapabilityPlatformResourceViewAll},
			matched:      CapabilityPlatformResourceViewAll,
		},
		{
			name:         "operate all is narrower than manage",
			action:       ActionAccountOperate,
			capabilities: []Capability{CapabilityPlatformResourceManageAll, CapabilityPlatformResourceOperateAll},
			matched:      CapabilityPlatformResourceOperateAll,
		},
		{
			name:         "manage all covers management action",
			action:       ActionGroupTransfer,
			capabilities: []Capability{CapabilityPlatformResourceManageAll},
			matched:      CapabilityPlatformResourceManageAll,
		},
	}
	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			actor := mustUserActor(t, 9, 1, nil, testCase.capabilities, false)
			subject := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeRBAC), false)
			resourceType, _ := testCase.action.ResourceType()
			ref := mustResourceRef(t, resourceType, 1)
			resource := mustResourceSnapshot(t, ResourceAccessSnapshotInput{Subject: subject, Resource: ref, Exists: true, AccessVersion: 1})
			decision, err := NewPolicyService(&stubPolicyStore{resourceSnapshot: resource}).Authorize(context.Background(), actor, testCase.action, ref)
			if err != nil || !decision.Allowed() || decision.MatchSource() != MatchSourcePlatformCapability {
				t.Fatalf("platform capability denied: reason=%q err=%v", decision.DenyReason(), err)
			}
			provenance, _ := decision.Provenance()
			matched, ok := provenance.Capability()
			if !ok || matched != testCase.matched {
				t.Fatalf("wrong platform provenance: %q, %v", matched, ok)
			}
		})
	}
}

func TestPolicyFailuresAreUnavailableAndLocalDenialsSkipStore(t *testing.T) {
	t.Parallel()

	actor := mustUserActor(t, 2, 1, nil, []Capability{CapabilityAccountCreate}, false)
	configuration := fullyEnabledConfiguration(t, RoleAuthorizationModeRBAC)
	subject := mustSubjectSnapshotForActor(t, actor, configuration, false)
	ref := mustResourceRef(t, ResourceTypeAccount, 5)
	backendFailure := errors.New("database unavailable")

	for _, testCase := range []struct {
		name    string
		service *PolicyService
	}{
		{name: "nil store", service: NewPolicyService(nil)},
		{name: "store failure", service: NewPolicyService(&stubPolicyStore{subjectErr: backendFailure})},
		{name: "malformed snapshot", service: NewPolicyService(&stubPolicyStore{})},
	} {
		decision, err := testCase.service.CheckCapability(context.Background(), actor, CapabilityAccountCreate)
		if decision.DenyReason() != DenyReasonAuthorizationDataUnavailable || !errors.Is(err, ErrAuthorizationUnavailable) {
			t.Fatalf("%s did not return unavailable: reason=%q err=%v", testCase.name, decision.DenyReason(), err)
		}
	}
	storeCause := errors.New("subject store sentinel")
	decision, err := NewPolicyService(&stubPolicyStore{subjectErr: storeCause}).CheckCapability(context.Background(), actor, CapabilityAccountCreate)
	if decision.DenyReason() != DenyReasonAuthorizationDataUnavailable || !errors.Is(err, ErrAuthorizationUnavailable) || !errors.Is(err, storeCause) {
		t.Fatalf("subject failure lost error chain: reason=%q err=%v", decision.DenyReason(), err)
	}

	resourceFailure := &stubPolicyStore{resourceErr: backendFailure}
	decision, err = NewPolicyService(resourceFailure).Authorize(context.Background(), actor, ActionAccountView, ref)
	if decision.DenyReason() != DenyReasonAuthorizationDataUnavailable || !errors.Is(err, ErrAuthorizationUnavailable) || !errors.Is(err, backendFailure) {
		t.Fatalf("resource failure did not return unavailable: reason=%q err=%v", decision.DenyReason(), err)
	}

	otherActor := mustUserActor(t, 99, 1, nil, []Capability{CapabilityAccountCreate}, false)
	wrongSubject := mustSubjectSnapshotForActor(t, otherActor, configuration, false)
	wrongResource := mustResourceSnapshot(t, ResourceAccessSnapshotInput{Subject: wrongSubject, Resource: ref, Exists: true, AccessVersion: 1})
	decision, err = NewPolicyService(&stubPolicyStore{resourceSnapshot: wrongResource}).Authorize(context.Background(), actor, ActionAccountView, ref)
	if decision.DenyReason() != DenyReasonAuthorizationDataUnavailable || !errors.Is(err, ErrAuthorizationUnavailable) {
		t.Fatalf("wrong subject snapshot did not return unavailable: reason=%q err=%v", decision.DenyReason(), err)
	}

	validResource := mustResourceSnapshot(t, ResourceAccessSnapshotInput{Subject: subject, Resource: ref, Exists: true, AccessVersion: 1})
	validStore := &stubPolicyStore{resourceSnapshot: validResource}
	_, err = NewPolicyService(validStore).Authorize(context.Background(), actor, ActionAccountView, ref)
	if err != nil || validStore.resourceCalls != 1 || validStore.subjectCalls != 0 {
		t.Fatalf("Authorize did not use exactly one resource snapshot: subject=%d resource=%d err=%v", validStore.subjectCalls, validStore.resourceCalls, err)
	}

	localStore := &stubPolicyStore{}
	service := NewPolicyService(localStore)
	if decision, err = service.CheckCapability(context.Background(), Actor{}, CapabilityAccountCreate); err != nil || decision.DenyReason() != DenyReasonInvalidActor {
		t.Fatalf("invalid actor decision: %q, %v", decision.DenyReason(), err)
	}
	if decision, err = service.CheckCapability(context.Background(), actor, Capability("unknown")); err != nil || decision.DenyReason() != DenyReasonUnknownCapability {
		t.Fatalf("unknown capability decision: %q, %v", decision.DenyReason(), err)
	}
	if decision, err = service.CanCreate(context.Background(), actor, ResourceType("workspace")); err != nil || decision.DenyReason() != DenyReasonUnknownResourceType {
		t.Fatalf("unknown resource decision: %q, %v", decision.DenyReason(), err)
	}
	if decision, err = service.Authorize(context.Background(), actor, ActionGroupView, ref); err != nil || decision.DenyReason() != DenyReasonActionResourceMismatch {
		t.Fatalf("mismatched action decision: %q, %v", decision.DenyReason(), err)
	}
	systemActor, systemErr := newSystemActor("worker")
	if systemErr != nil {
		t.Fatalf("create system actor: %v", systemErr)
	}
	if decision, err = service.CheckCapability(context.Background(), systemActor, CapabilityAccountCreate); err != nil || decision.DenyReason() != DenyReasonMissingCapability {
		t.Fatalf("system capability decision: %q, %v", decision.DenyReason(), err)
	}
	if decision, err = service.CanCreate(context.Background(), systemActor, ResourceTypeAccount); err != nil || decision.DenyReason() != DenyReasonMissingCapability {
		t.Fatalf("system create decision: %q, %v", decision.DenyReason(), err)
	}
	if decision, err = service.Authorize(context.Background(), systemActor, ActionAccountView, ref); err != nil || decision.DenyReason() != DenyReasonNoMatchingAccess {
		t.Fatalf("system resource decision: %q, %v", decision.DenyReason(), err)
	}
	if localStore.subjectCalls != 0 || localStore.resourceCalls != 0 {
		t.Fatalf("local denials called store: subject=%d resource=%d", localStore.subjectCalls, localStore.resourceCalls)
	}
}
