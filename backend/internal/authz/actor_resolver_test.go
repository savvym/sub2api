package authz

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type actorResolverStoreStub struct {
	userSnapshot             SubjectSnapshot
	servicePrincipalSnapshot SubjectSnapshot
	userErr                  error
	servicePrincipalErr      error
	userCalls                int
	servicePrincipalCalls    int
	lastSubject              SubjectRef
	lastServicePrincipalCode string
}

func (s *actorResolverStoreStub) LoadSubjectSnapshot(_ context.Context, subject SubjectRef) (SubjectSnapshot, error) {
	s.userCalls++
	s.lastSubject = subject
	return s.userSnapshot, s.userErr
}

func (s *actorResolverStoreStub) LoadServicePrincipalSubjectSnapshotByCode(_ context.Context, code string) (SubjectSnapshot, error) {
	s.servicePrincipalCalls++
	s.lastServicePrincipalCode = code
	return s.servicePrincipalSnapshot, s.servicePrincipalErr
}

func TestActorResolverResolvesUserFromOneTrustedSnapshot(t *testing.T) {
	t.Parallel()

	subject := mustActorResolverSubject(t, SubjectKindUser, 42)
	store := &actorResolverStoreStub{userSnapshot: mustActorResolverSnapshot(t, SubjectSnapshotInput{
		Subject:            subject,
		Exists:             true,
		Active:             true,
		AuthzVersion:       7,
		RoleVersions:       map[int64]int64{9: 3, 2: 1},
		Capabilities:       []Capability{CapabilityResourceShare, CapabilityAccountCreate},
		CurrentLegacyAdmin: true,
	})}

	actor, err := NewActorResolver(store).ResolveUser(context.Background(), 42, AuthMethodJWT)
	if err != nil {
		t.Fatalf("resolve user: %v", err)
	}
	if !actor.Valid() || actor.Kind() != SubjectKindUser || actor.AuthMethod() != AuthMethodJWT {
		t.Fatalf("unexpected resolved actor: %+v", actor)
	}
	if id, ok := actor.UserID(); !ok || id != 42 {
		t.Fatalf("unexpected user identity: %d, %v", id, ok)
	}
	if actor.subjectVersion() != 7 || !actor.hasLegacyAdminBypass() {
		t.Fatalf("snapshot metadata not preserved: version=%d admin=%v", actor.subjectVersion(), actor.hasLegacyAdminBypass())
	}
	if got := actor.roleVersionsSnapshot(); !reflect.DeepEqual(got, map[int64]int64{2: 1, 9: 3}) {
		t.Fatalf("unexpected role versions: %v", got)
	}
	if !actor.hasCapabilitySnapshot(CapabilityResourceShare) || !actor.hasCapabilitySnapshot(CapabilityAccountCreate) {
		t.Fatalf("snapshot capabilities not preserved: %v", actor.capabilitiesSnapshot())
	}
	if store.userCalls != 1 || store.servicePrincipalCalls != 0 || store.lastSubject != subject {
		t.Fatalf("unexpected store calls: user=%d principal=%d subject=%+v", store.userCalls, store.servicePrincipalCalls, store.lastSubject)
	}
}

func TestActorResolverResolvesServicePrincipalByCanonicalCode(t *testing.T) {
	t.Parallel()

	subject := mustActorResolverSubject(t, SubjectKindServicePrincipal, 42)
	store := &actorResolverStoreStub{servicePrincipalSnapshot: mustActorResolverSnapshot(t, SubjectSnapshotInput{
		Subject:      subject,
		Exists:       true,
		Active:       true,
		AuthzVersion: 4,
		RoleVersions: map[int64]int64{5: 2},
		Capabilities: []Capability{CapabilityPlatformResourceViewAll},
	})}

	actor, err := NewActorResolver(store).ResolveServicePrincipal(
		context.Background(),
		"  "+AdminAPIKeyServicePrincipalCode+"  ",
		AuthMethodAdminAPIKey,
	)
	if err != nil {
		t.Fatalf("resolve service principal: %v", err)
	}
	if !actor.Valid() || actor.Kind() != SubjectKindServicePrincipal || actor.AuthMethod() != AuthMethodAdminAPIKey {
		t.Fatalf("unexpected resolved actor: %+v", actor)
	}
	if id, ok := actor.ServicePrincipalID(); !ok || id != 42 {
		t.Fatalf("unexpected service principal identity: %d, %v", id, ok)
	}
	if _, ok := actor.UserID(); ok || actor.hasLegacyAdminBypass() {
		t.Fatal("service principal inherited user-only identity state")
	}
	if actor.subjectVersion() != 4 || !actor.hasCapabilitySnapshot(CapabilityPlatformResourceViewAll) {
		t.Fatal("service principal snapshot state not preserved")
	}
	if store.servicePrincipalCalls != 1 || store.userCalls != 0 || store.lastServicePrincipalCode != AdminAPIKeyServicePrincipalCode {
		t.Fatalf("unexpected store calls: principal=%d user=%d code=%q", store.servicePrincipalCalls, store.userCalls, store.lastServicePrincipalCode)
	}
}

func TestActorResolverRejectsMissingAndInactiveSubjects(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name   string
		kind   SubjectKind
		exists bool
		active bool
	}{
		{name: "missing user", kind: SubjectKindUser},
		{name: "inactive user", kind: SubjectKindUser, exists: true},
		{name: "missing service principal", kind: SubjectKindServicePrincipal},
		{name: "inactive service principal", kind: SubjectKindServicePrincipal, exists: true},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			subject := mustActorResolverSubject(t, testCase.kind, 31)
			input := SubjectSnapshotInput{
				Subject: subject,
				Exists:  testCase.exists,
				Active:  testCase.active,
			}
			if testCase.exists {
				input.AuthzVersion = 1
			}
			snapshot := mustActorResolverSnapshot(t, input)
			store := &actorResolverStoreStub{}
			var (
				actor Actor
				err   error
			)
			if testCase.kind == SubjectKindUser {
				store.userSnapshot = snapshot
				actor, err = NewActorResolver(store).ResolveUser(context.Background(), 31, AuthMethodJWT)
			} else {
				store.servicePrincipalSnapshot = snapshot
				actor, err = NewActorResolver(store).ResolveServicePrincipal(context.Background(), "worker", AuthMethodServicePrincipal)
			}
			if !errors.Is(err, ErrActorInactive) || actor.Valid() {
				t.Fatalf("expected inactive actor failure, got actor=%+v err=%v", actor, err)
			}
		})
	}
}

func TestActorResolverRejectsInvalidAndMismatchedSnapshots(t *testing.T) {
	t.Parallel()

	user42 := mustActorResolverSubject(t, SubjectKindUser, 42)
	user43Snapshot := mustActorResolverSnapshot(t, SubjectSnapshotInput{
		Subject:      mustActorResolverSubject(t, SubjectKindUser, 43),
		Exists:       true,
		Active:       true,
		AuthzVersion: 1,
	})
	userSnapshot := mustActorResolverSnapshot(t, SubjectSnapshotInput{
		Subject:      user42,
		Exists:       true,
		Active:       true,
		AuthzVersion: 1,
	})

	for _, testCase := range []struct {
		name     string
		resolve  func(*actorResolverStoreStub) (Actor, error)
		populate func(*actorResolverStoreStub)
	}{
		{
			name: "invalid user snapshot",
			populate: func(store *actorResolverStoreStub) {
				store.userSnapshot = SubjectSnapshot{}
			},
			resolve: func(store *actorResolverStoreStub) (Actor, error) {
				return NewActorResolver(store).ResolveUser(context.Background(), 42, AuthMethodJWT)
			},
		},
		{
			name: "mismatched user snapshot",
			populate: func(store *actorResolverStoreStub) {
				store.userSnapshot = user43Snapshot
			},
			resolve: func(store *actorResolverStoreStub) (Actor, error) {
				return NewActorResolver(store).ResolveUser(context.Background(), 42, AuthMethodJWT)
			},
		},
		{
			name: "invalid service principal snapshot",
			populate: func(store *actorResolverStoreStub) {
				store.servicePrincipalSnapshot = SubjectSnapshot{}
			},
			resolve: func(store *actorResolverStoreStub) (Actor, error) {
				return NewActorResolver(store).ResolveServicePrincipal(context.Background(), "worker", AuthMethodServicePrincipal)
			},
		},
		{
			name: "user snapshot returned for service principal code",
			populate: func(store *actorResolverStoreStub) {
				store.servicePrincipalSnapshot = userSnapshot
			},
			resolve: func(store *actorResolverStoreStub) (Actor, error) {
				return NewActorResolver(store).ResolveServicePrincipal(context.Background(), "worker", AuthMethodServicePrincipal)
			},
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			store := &actorResolverStoreStub{}
			testCase.populate(store)
			actor, err := testCase.resolve(store)
			if !errors.Is(err, ErrAuthorizationUnavailable) || actor.Valid() {
				t.Fatalf("expected unavailable authorization data, got actor=%+v err=%v", actor, err)
			}
		})
	}
}

func TestActorResolverPreservesStoreFailureCause(t *testing.T) {
	t.Parallel()

	cause := errors.New("database unavailable")
	for _, testCase := range []struct {
		name    string
		resolve func(*actorResolverStoreStub) (Actor, error)
	}{
		{
			name: "user",
			resolve: func(store *actorResolverStoreStub) (Actor, error) {
				store.userErr = cause
				return NewActorResolver(store).ResolveUser(context.Background(), 42, AuthMethodJWT)
			},
		},
		{
			name: "service principal",
			resolve: func(store *actorResolverStoreStub) (Actor, error) {
				store.servicePrincipalErr = cause
				return NewActorResolver(store).ResolveServicePrincipal(context.Background(), "worker", AuthMethodServicePrincipal)
			},
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			actor, err := testCase.resolve(&actorResolverStoreStub{})
			if !errors.Is(err, ErrAuthorizationUnavailable) || !errors.Is(err, cause) || actor.Valid() {
				t.Fatalf("store failure was not preserved: actor=%+v err=%v", actor, err)
			}
		})
	}
}

func TestActorResolverRejectsWrongAuthenticationMethodBeforeStoreLookup(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name    string
		resolve func(*actorResolverStoreStub) (Actor, error)
	}{
		{
			name: "user with admin api key",
			resolve: func(store *actorResolverStoreStub) (Actor, error) {
				return NewActorResolver(store).ResolveUser(context.Background(), 42, AuthMethodAdminAPIKey)
			},
		},
		{
			name: "service principal with jwt",
			resolve: func(store *actorResolverStoreStub) (Actor, error) {
				return NewActorResolver(store).ResolveServicePrincipal(context.Background(), "worker", AuthMethodJWT)
			},
		},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			store := &actorResolverStoreStub{}
			actor, err := testCase.resolve(store)
			if !errors.Is(err, ErrInvalidActor) || actor.Valid() {
				t.Fatalf("expected invalid actor, got actor=%+v err=%v", actor, err)
			}
			if store.userCalls != 0 || store.servicePrincipalCalls != 0 {
				t.Fatalf("invalid method reached store: user=%d principal=%d", store.userCalls, store.servicePrincipalCalls)
			}
		})
	}
}

func TestActorResolverUsesCurrentSnapshotForLegacyAdminGate(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name        string
		legacyAdmin bool
		wantErr     error
	}{
		{name: "current admin allowed", legacyAdmin: true},
		{name: "non admin denied", wantErr: ErrPolicyAccessDenied},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			subject := mustActorResolverSubject(t, SubjectKindUser, 42)
			store := &actorResolverStoreStub{userSnapshot: mustActorResolverSnapshot(t, SubjectSnapshotInput{
				Subject:            subject,
				Exists:             true,
				Active:             true,
				AuthzVersion:       3,
				CurrentLegacyAdmin: testCase.legacyAdmin,
			})}

			actor, err := NewActorResolver(store).ResolveLegacyAdminUser(context.Background(), 42)
			if testCase.wantErr != nil {
				if !errors.Is(err, testCase.wantErr) || actor.Valid() {
					t.Fatalf("expected legacy admin denial, got actor=%+v err=%v", actor, err)
				}
				return
			}
			if err != nil || !actor.Valid() || !actor.hasLegacyAdminBypass() || actor.AuthMethod() != AuthMethodJWT {
				t.Fatalf("current legacy admin rejected: actor=%+v err=%v", actor, err)
			}
		})
	}
}

func mustActorResolverSubject(t testing.TB, kind SubjectKind, id int64) SubjectRef {
	t.Helper()
	subject, err := NewSubjectRef(kind, id)
	if err != nil {
		t.Fatalf("create actor resolver subject: %v", err)
	}
	return subject
}

func mustActorResolverSnapshot(t testing.TB, input SubjectSnapshotInput) SubjectSnapshot {
	t.Helper()
	input.Configuration = fullyEnabledConfiguration(t, RoleAuthorizationModeShadow)
	snapshot, err := NewSubjectSnapshot(input)
	if err != nil {
		t.Fatalf("create actor resolver snapshot: %v", err)
	}
	return snapshot
}
