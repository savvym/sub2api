package authz

import (
	"context"
	"testing"
)

type stubPolicyStore struct {
	subjectSnapshot  SubjectSnapshot
	resourceSnapshot ResourceAccessSnapshot
	subjectErr       error
	resourceErr      error
	subjectCalls     int
	resourceCalls    int
	lastSubject      SubjectRef
	lastResource     ResourceRef
}

func (s *stubPolicyStore) LoadSubjectSnapshot(_ context.Context, subject SubjectRef) (SubjectSnapshot, error) {
	s.subjectCalls++
	s.lastSubject = subject
	return s.subjectSnapshot, s.subjectErr
}

func (s *stubPolicyStore) LoadResourceAccessSnapshot(_ context.Context, subject SubjectRef, resource ResourceRef) (ResourceAccessSnapshot, error) {
	s.resourceCalls++
	s.lastSubject = subject
	s.lastResource = resource
	return s.resourceSnapshot, s.resourceErr
}

func mustPolicyConfiguration(t testing.TB, input PolicyConfigurationInput) PolicyConfiguration {
	t.Helper()
	configuration, err := NewPolicyConfiguration(input)
	if err != nil {
		t.Fatalf("create policy configuration: %v", err)
	}
	return configuration
}

func fullyEnabledConfiguration(t testing.TB, mode RoleAuthorizationMode) PolicyConfiguration {
	t.Helper()
	return mustPolicyConfiguration(t, PolicyConfigurationInput{
		RoleAuthorizationMode:          mode,
		ResourceAccessControlEnabled:   true,
		SelfServiceHostingEnabled:      true,
		GroupSharingEnabled:            true,
		AccountSharingEnabled:          true,
		RoleBasedResourceGrantsEnabled: true,
	})
}

func mustUserActor(t testing.TB, id, version int64, roles map[int64]int64, capabilities []Capability, legacyAdmin bool) Actor {
	t.Helper()
	actor, err := newUserActor(id, userActorOptions{
		subjectAuthzVersion: version,
		roleVersions:        roles,
		capabilities:        capabilities,
		legacyAdmin:         legacyAdmin,
		authMethod:          AuthMethodJWT,
	})
	if err != nil {
		t.Fatalf("create user actor: %v", err)
	}
	return actor
}

func mustServicePrincipalActor(t testing.TB, id, version int64, roles map[int64]int64, capabilities []Capability) Actor {
	t.Helper()
	actor, err := newServicePrincipalActor(id, servicePrincipalActorOptions{
		subjectAuthzVersion: version,
		roleVersions:        roles,
		capabilities:        capabilities,
		authMethod:          AuthMethodServicePrincipal,
	})
	if err != nil {
		t.Fatalf("create service principal actor: %v", err)
	}
	return actor
}

func mustSubjectSnapshotForActor(t testing.TB, actor Actor, configuration PolicyConfiguration, currentLegacyAdmin bool) SubjectSnapshot {
	t.Helper()
	subject, ok := subjectRefFromActor(actor)
	if !ok {
		t.Fatal("actor has no durable subject")
	}
	snapshot, err := NewSubjectSnapshot(SubjectSnapshotInput{
		Subject:            subject,
		Exists:             true,
		Active:             true,
		AuthzVersion:       actor.subjectVersion(),
		RoleVersions:       actor.roleVersionsSnapshot(),
		Capabilities:       actor.capabilitiesSnapshot(),
		CurrentLegacyAdmin: currentLegacyAdmin,
		Configuration:      configuration,
	})
	if err != nil {
		t.Fatalf("create subject snapshot: %v", err)
	}
	return snapshot
}

func mustResourceRef(t testing.TB, resourceType ResourceType, id int64) ResourceRef {
	t.Helper()
	ref, err := NewResourceRef(resourceType, id)
	if err != nil {
		t.Fatalf("create resource reference: %v", err)
	}
	return ref
}

func mustResourceSnapshot(t testing.TB, input ResourceAccessSnapshotInput) ResourceAccessSnapshot {
	t.Helper()
	if input.Exists && input.Resource.Type() == ResourceTypeGroup && input.GroupAuthorizationMode == "" {
		input.GroupAuthorizationMode = GroupAuthorizationModeACL
	}
	snapshot, err := NewResourceAccessSnapshot(input)
	if err != nil {
		t.Fatalf("create resource snapshot: %v", err)
	}
	return snapshot
}

func mustUserGrant(t testing.TB, grantID int64, level AccessLevel) GrantSnapshot {
	t.Helper()
	grant, err := NewUserGrantSnapshot(grantID, level)
	if err != nil {
		t.Fatalf("create user grant: %v", err)
	}
	return grant
}

func mustRoleGrant(t testing.TB, grantID, roleID int64, level AccessLevel) GrantSnapshot {
	t.Helper()
	grant, err := NewRoleGrantSnapshot(grantID, roleID, level)
	if err != nil {
		t.Fatalf("create role grant: %v", err)
	}
	return grant
}

func int64Pointer(value int64) *int64 { return &value }

func accessLevelPointer(value AccessLevel) *AccessLevel { return &value }
