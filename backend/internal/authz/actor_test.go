package authz

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestUserActorCopiesTrustedAuthorizationState(t *testing.T) {
	t.Parallel()

	roleVersions := map[int64]int64{9: 3, 2: 1}
	capabilities := []Capability{CapabilityResourceShare, CapabilityAccountCreate}
	actor, err := newUserActor(42, userActorOptions{
		subjectAuthzVersion: 7,
		roleVersions:        roleVersions,
		capabilities:        capabilities,
		legacyAdmin:         true,
		authMethod:          AuthMethodJWT,
	})
	if err != nil {
		t.Fatalf("create user actor: %v", err)
	}

	roleVersions[9] = 999
	capabilities[0] = Capability("modified")
	if !actor.Valid() || actor.Kind() != SubjectKindUser || !actor.hasLegacyAdminBypass() {
		t.Fatal("valid user actor rejected")
	}
	if userID, ok := actor.UserID(); !ok || userID != 42 {
		t.Fatalf("unexpected user subject: %d, %v", userID, ok)
	}
	if _, ok := actor.ServicePrincipalID(); ok {
		t.Fatal("user actor exposed service principal identity")
	}
	if got := actor.roleVersionsSnapshot()[9]; got != 3 {
		t.Fatalf("actor role state mutated through input: %d", got)
	}
	if got := actor.roleIDsSnapshot(); !reflect.DeepEqual(got, []int64{2, 9}) {
		t.Fatalf("unexpected sorted role ids: %v", got)
	}
	if !actor.hasCapabilitySnapshot(CapabilityResourceShare) || actor.hasCapabilitySnapshot(Capability("unknown")) {
		t.Fatal("capability lookup did not fail closed")
	}
}

func TestServicePrincipalActorIsDistinctFromUser(t *testing.T) {
	t.Parallel()

	actor, err := newServicePrincipalActor(11, servicePrincipalActorOptions{
		subjectAuthzVersion: 2,
		roleVersions:        map[int64]int64{4: 1},
		capabilities:        []Capability{CapabilityPlatformResourceViewAll},
		authMethod:          AuthMethodAdminAPIKey,
	})
	if err != nil {
		t.Fatalf("create service principal actor: %v", err)
	}
	if !actor.Valid() || actor.Kind() != SubjectKindServicePrincipal || actor.hasLegacyAdminBypass() {
		t.Fatal("service principal actor identity is invalid")
	}
	if servicePrincipalID, ok := actor.ServicePrincipalID(); !ok || servicePrincipalID != 11 {
		t.Fatalf("unexpected service principal subject: %d, %v", servicePrincipalID, ok)
	}
	if _, ok := actor.UserID(); ok {
		t.Fatal("service principal actor exposed user identity")
	}
}

func TestInvalidActorInputsFailClosed(t *testing.T) {
	t.Parallel()

	validUserOptions := userActorOptions{subjectAuthzVersion: 1, authMethod: AuthMethodJWT}
	for _, testCase := range []struct {
		name    string
		userID  int64
		options userActorOptions
	}{
		{name: "missing user", userID: 0, options: validUserOptions},
		{name: "missing version", userID: 1, options: userActorOptions{authMethod: AuthMethodJWT}},
		{name: "wrong auth method", userID: 1, options: userActorOptions{subjectAuthzVersion: 1, authMethod: AuthMethodSystem}},
		{name: "bad role id", userID: 1, options: userActorOptions{subjectAuthzVersion: 1, roleVersions: map[int64]int64{0: 1}, authMethod: AuthMethodJWT}},
		{name: "bad role version", userID: 1, options: userActorOptions{subjectAuthzVersion: 1, roleVersions: map[int64]int64{1: 0}, authMethod: AuthMethodJWT}},
		{name: "unknown capability", userID: 1, options: userActorOptions{subjectAuthzVersion: 1, capabilities: []Capability{"unknown"}, authMethod: AuthMethodJWT}},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			actor, err := newUserActor(testCase.userID, testCase.options)
			if err == nil || actor.Valid() {
				t.Fatalf("invalid user actor accepted: %+v", testCase)
			}
		})
	}

	if (Actor{}).Valid() {
		t.Fatal("zero actor is valid")
	}
}

func TestSystemActorCannotBeForgedByJSON(t *testing.T) {
	t.Parallel()

	var actor Actor
	if err := json.Unmarshal([]byte(`{"kind":"system","systemCode":"http","authMethod":"system"}`), &actor); err != nil {
		t.Fatalf("unmarshal actor: %v", err)
	}
	if actor.Valid() || actor.Kind() == SubjectKindSystem {
		t.Fatal("HTTP-shaped JSON forged a system actor")
	}

	systemActor, err := newSystemActor("scheduler")
	if err != nil || !systemActor.Valid() || systemActor.Kind() != SubjectKindSystem {
		t.Fatalf("trusted system actor rejected: %v", err)
	}
	if code, ok := systemActor.SystemCode(); !ok || code != "scheduler" {
		t.Fatalf("unexpected system actor code: %q, %v", code, ok)
	}
	if _, _, ok := systemActor.DurableSubject(); ok {
		t.Fatal("process-local system actor exposed a durable subject")
	}
}

func TestDurableSubjectUsesExactlyOnePersistedIdentity(t *testing.T) {
	t.Parallel()

	user, err := newUserActor(42, userActorOptions{subjectAuthzVersion: 1, authMethod: AuthMethodJWT})
	if err != nil {
		t.Fatalf("create user actor: %v", err)
	}
	if kind, id, ok := user.DurableSubject(); !ok || kind != SubjectKindUser || id != 42 {
		t.Fatalf("unexpected durable user: %q, %d, %v", kind, id, ok)
	}

	principal, err := newServicePrincipalActor(7, servicePrincipalActorOptions{
		subjectAuthzVersion: 1,
		authMethod:          AuthMethodServicePrincipal,
	})
	if err != nil {
		t.Fatalf("create service principal actor: %v", err)
	}
	if kind, id, ok := principal.DurableSubject(); !ok || kind != SubjectKindServicePrincipal || id != 7 {
		t.Fatalf("unexpected durable service principal: %q, %d, %v", kind, id, ok)
	}
}
