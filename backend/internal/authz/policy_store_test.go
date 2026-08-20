package authz

import (
	"errors"
	"reflect"
	"testing"
)

func TestPolicyConfigurationDerivesEffectiveFeatureDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        PolicyConfigurationInput
		master       bool
		selfService  bool
		groupSharing bool
		accountShare bool
		roleGrant    bool
	}{
		{
			name: "master disables every dependent setting",
			input: PolicyConfigurationInput{
				RoleAuthorizationMode:          RoleAuthorizationModeRBAC,
				SelfServiceHostingEnabled:      true,
				GroupSharingEnabled:            true,
				AccountSharingEnabled:          true,
				RoleBasedResourceGrantsEnabled: true,
			},
		},
		{
			name: "self service disables sharing but not role grant infrastructure",
			input: PolicyConfigurationInput{
				RoleAuthorizationMode:          RoleAuthorizationModeRBAC,
				ResourceAccessControlEnabled:   true,
				GroupSharingEnabled:            true,
				AccountSharingEnabled:          true,
				RoleBasedResourceGrantsEnabled: true,
			},
			master:    true,
			roleGrant: true,
		},
		{
			name: "fully enabled",
			input: PolicyConfigurationInput{
				RoleAuthorizationMode:          RoleAuthorizationModeShadow,
				ResourceAccessControlEnabled:   true,
				SelfServiceHostingEnabled:      true,
				GroupSharingEnabled:            true,
				AccountSharingEnabled:          true,
				RoleBasedResourceGrantsEnabled: true,
			},
			master:       true,
			selfService:  true,
			groupSharing: true,
			accountShare: true,
			roleGrant:    true,
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			configuration := mustPolicyConfiguration(t, testCase.input)
			if !configuration.Valid() ||
				configuration.ResourceAccessControlEnabled() != testCase.master ||
				configuration.SelfServiceHostingEnabled() != testCase.selfService ||
				configuration.SharingEnabled(ResourceTypeGroup) != testCase.groupSharing ||
				configuration.SharingEnabled(ResourceTypeAccount) != testCase.accountShare ||
				configuration.RoleBasedResourceGrantsEnabled() != testCase.roleGrant {
				t.Fatalf("unexpected effective configuration: %+v", configuration)
			}
		})
	}

	if configuration, err := NewPolicyConfiguration(PolicyConfigurationInput{}); !errors.Is(err, ErrInvalidPolicySnapshot) || configuration.Valid() {
		t.Fatalf("invalid role mode accepted: %+v, %v", configuration, err)
	}
	if (PolicyConfiguration{}).Valid() {
		t.Fatal("zero policy configuration is valid")
	}
}

func TestSubjectRefIsAValidatedLookupKey(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		kind SubjectKind
		id   int64
	}{
		{kind: SubjectKindUser, id: 1},
		{kind: SubjectKindServicePrincipal, id: 2},
	} {
		ref, err := NewSubjectRef(testCase.kind, testCase.id)
		if err != nil || !ref.Valid() || ref.Kind() != testCase.kind || ref.ID() != testCase.id {
			t.Fatalf("valid lookup key rejected: %+v, %v", testCase, err)
		}
	}
	for _, testCase := range []struct {
		kind SubjectKind
		id   int64
	}{
		{kind: SubjectKindSystem, id: 1},
		{kind: SubjectKindUser, id: 0},
		{kind: SubjectKind("request"), id: 1},
	} {
		ref, err := NewSubjectRef(testCase.kind, testCase.id)
		if !errors.Is(err, ErrInvalidSubjectRef) || ref.Valid() {
			t.Fatalf("invalid lookup key accepted: %+v, %v", testCase, err)
		}
	}
}

func TestSubjectSnapshotIsImmutableAndRejectsMalformedState(t *testing.T) {
	t.Parallel()

	actor := mustUserActor(t, 41, 3, map[int64]int64{9: 2}, []Capability{CapabilityAccountCreate}, true)
	subject, _ := subjectRefFromActor(actor)
	configuration := fullyEnabledConfiguration(t, RoleAuthorizationModeRBAC)
	roles := map[int64]int64{9: 2}
	capabilities := []Capability{CapabilityAccountCreate}
	snapshot, err := NewSubjectSnapshot(SubjectSnapshotInput{
		Subject:            subject,
		Exists:             true,
		Active:             true,
		AuthzVersion:       3,
		RoleVersions:       roles,
		Capabilities:       capabilities,
		CurrentLegacyAdmin: true,
		Configuration:      configuration,
	})
	if err != nil {
		t.Fatalf("create subject snapshot: %v", err)
	}
	roles[9] = 99
	capabilities[0] = Capability("modified")
	returnedRoles := snapshot.RoleVersions()
	returnedRoles[9] = 100
	returnedCapabilities := snapshot.Capabilities()
	returnedCapabilities[0] = Capability("modified-again")
	if snapshot.RoleVersions()[9] != 2 || !reflect.DeepEqual(snapshot.Capabilities(), []Capability{CapabilityAccountCreate}) {
		t.Fatal("subject snapshot exposed mutable authorization state")
	}

	invalidInputs := []SubjectSnapshotInput{
		{Subject: subject, Configuration: configuration, Active: true},
		{Subject: subject, Exists: true, Active: true, Configuration: configuration},
		{Subject: subject, Exists: true, Active: true, AuthzVersion: 1, RoleVersions: map[int64]int64{0: 1}, Configuration: configuration},
		{Subject: subject, Exists: true, Active: true, AuthzVersion: 1, Capabilities: []Capability{"unknown"}, Configuration: configuration},
	}
	for _, input := range invalidInputs {
		if result, snapshotErr := NewSubjectSnapshot(input); !errors.Is(snapshotErr, ErrInvalidPolicySnapshot) || result.Valid() {
			t.Fatalf("malformed subject snapshot accepted: %+v, %v", input, snapshotErr)
		}
	}
	if (SubjectSnapshot{}).Valid() || (SubjectRef{}).Valid() {
		t.Fatal("zero subject state is valid")
	}
	principal := mustServicePrincipalActor(t, 7, 1, nil, nil)
	principalSubject, _ := subjectRefFromActor(principal)
	if result, snapshotErr := NewSubjectSnapshot(SubjectSnapshotInput{
		Subject:            principalSubject,
		Exists:             true,
		Active:             true,
		AuthzVersion:       1,
		CurrentLegacyAdmin: true,
		Configuration:      configuration,
	}); !errors.Is(snapshotErr, ErrInvalidPolicySnapshot) || result.Valid() {
		t.Fatalf("service principal accepted legacy admin state: %+v, %v", result, snapshotErr)
	}
}

func TestGrantAndResourceSnapshotsAreImmutableAndFailClosed(t *testing.T) {
	t.Parallel()

	actor := mustUserActor(t, 8, 1, map[int64]int64{7: 3}, nil, false)
	subject := mustSubjectSnapshotForActor(t, actor, fullyEnabledConfiguration(t, RoleAuthorizationModeRBAC), false)
	ref := mustResourceRef(t, ResourceTypeGroup, 22)
	userGrant := mustUserGrant(t, 20, AccessLevelConsumer)
	roleGrant := mustRoleGrant(t, 10, 7, AccessLevelMaintainer)
	userGrants := []GrantSnapshot{userGrant}
	roleGrants := []GrantSnapshot{roleGrant}
	snapshot := mustResourceSnapshot(t, ResourceAccessSnapshotInput{
		Subject:           subject,
		Resource:          ref,
		Exists:            true,
		OwnerUserID:       int64Pointer(9),
		PublicAccessLevel: accessLevelPointer(AccessLevelViewer),
		AccessVersion:     4,
		UserGrants:        userGrants,
		RoleGrants:        roleGrants,
	})
	userGrants[0] = GrantSnapshot{}
	roleGrants[0] = GrantSnapshot{}
	returnedUsers := snapshot.UserGrants()
	returnedRoles := snapshot.RoleGrants()
	returnedUsers[0] = GrantSnapshot{}
	returnedRoles[0] = GrantSnapshot{}
	if !snapshot.Valid() || snapshot.UserGrants()[0].GrantID() != 20 || snapshot.RoleGrants()[0].GrantID() != 10 {
		t.Fatal("resource snapshot exposed mutable grants")
	}

	if _, err := NewUserGrantSnapshot(0, AccessLevelViewer); !errors.Is(err, ErrInvalidPolicySnapshot) {
		t.Fatalf("invalid user grant accepted: %v", err)
	}
	if _, err := NewRoleGrantSnapshot(1, 0, AccessLevelViewer); !errors.Is(err, ErrInvalidPolicySnapshot) {
		t.Fatalf("invalid role grant accepted: %v", err)
	}
	if _, err := NewResourceAccessSnapshot(ResourceAccessSnapshotInput{
		Subject:           subject,
		Resource:          ref,
		Exists:            true,
		PublicAccessLevel: accessLevelPointer(AccessLevelManager),
		AccessVersion:     1,
	}); !errors.Is(err, ErrInvalidPolicySnapshot) {
		t.Fatalf("manager public access accepted: %v", err)
	}
	if _, err := NewResourceAccessSnapshot(ResourceAccessSnapshotInput{
		Subject:       subject,
		Resource:      ref,
		Exists:        true,
		AccessVersion: 1,
		RoleGrants:    []GrantSnapshot{mustRoleGrant(t, 1, 999, AccessLevelViewer)},
	}); !errors.Is(err, ErrInvalidPolicySnapshot) {
		t.Fatalf("inactive role grant accepted: %v", err)
	}
	if _, err := NewResourceAccessSnapshot(ResourceAccessSnapshotInput{
		Subject:       subject,
		Resource:      ref,
		AccessVersion: 1,
	}); !errors.Is(err, ErrInvalidPolicySnapshot) {
		t.Fatalf("missing resource carried access state: %v", err)
	}
	principal := mustServicePrincipalActor(t, 14, 1, nil, nil)
	principalSubject := mustSubjectSnapshotForActor(t, principal, fullyEnabledConfiguration(t, RoleAuthorizationModeRBAC), false)
	if _, err := NewResourceAccessSnapshot(ResourceAccessSnapshotInput{
		Subject:       principalSubject,
		Resource:      ref,
		Exists:        true,
		AccessVersion: 1,
		UserGrants:    []GrantSnapshot{mustUserGrant(t, 1, AccessLevelViewer)},
	}); !errors.Is(err, ErrInvalidPolicySnapshot) {
		t.Fatalf("service principal accepted direct user grant candidates: %v", err)
	}
	if (GrantSnapshot{}).Valid() || (ResourceAccessSnapshot{}).Valid() {
		t.Fatal("zero grant or resource snapshot is valid")
	}
}
