package authz

import (
	"context"
	"testing"
)

func TestActorContextRoundTripAndInvalidActorShadow(t *testing.T) {
	t.Parallel()

	actor := mustUserActor(t, 42, 1, nil, nil, false)
	parent := ContextWithActor(context.Background(), actor)
	resolved, ok := ActorFromContext(parent)
	resolvedKey, resolvedKeyOK := resolved.SubjectKey()
	actorKey, actorKeyOK := actor.SubjectKey()
	if !ok || !resolvedKeyOK || !actorKeyOK || resolvedKey != actorKey {
		t.Fatalf("valid actor did not round trip through context: actor=%+v ok=%v", resolved, ok)
	}

	child := ContextWithActor(parent, Actor{})
	if resolved, ok = ActorFromContext(child); ok || resolved.Valid() {
		t.Fatalf("invalid child actor fell back to parent: actor=%+v ok=%v", resolved, ok)
	}
	if resolved, ok = ActorFromContext(parent); !ok || !resolved.Valid() {
		t.Fatal("shadowing child mutated parent context")
	}
}

func TestActorContextHandlesNilAndMissingContext(t *testing.T) {
	t.Parallel()

	if actor, ok := ActorFromContext(nil); ok || actor.Valid() {
		t.Fatalf("nil context returned actor: %+v", actor)
	}
	if actor, ok := ActorFromContext(context.Background()); ok || actor.Valid() {
		t.Fatalf("empty context returned actor: %+v", actor)
	}

	want := mustUserActor(t, 7, 1, nil, nil, false)
	ctx := ContextWithActor(nil, want)
	got, ok := ActorFromContext(ctx)
	if !ok {
		t.Fatal("ContextWithActor did not normalize nil context")
	}
	gotKey, gotOK := got.SubjectKey()
	wantKey, wantOK := want.SubjectKey()
	if !gotOK || !wantOK || gotKey != wantKey {
		t.Fatalf("unexpected actor from normalized context: got=%q want=%q", gotKey, wantKey)
	}
}

func TestActorSubjectKeySeparatesPersistedIdentityNamespaces(t *testing.T) {
	t.Parallel()

	user := mustUserActor(t, 42, 1, nil, nil, false)
	principal := mustServicePrincipalActor(t, 42, 1, nil, nil)

	userKey, userOK := user.SubjectKey()
	principalKey, principalOK := principal.SubjectKey()
	if !userOK || userKey != "user:42" {
		t.Fatalf("unexpected user subject key: %q, %v", userKey, userOK)
	}
	if !principalOK || principalKey != "service_principal:42" {
		t.Fatalf("unexpected service principal subject key: %q, %v", principalKey, principalOK)
	}
	if userKey == principalKey {
		t.Fatal("user and service principal numeric IDs share one namespace")
	}

	system, err := newSystemActor("scheduler")
	if err != nil {
		t.Fatalf("create system actor: %v", err)
	}
	if key, ok := system.SubjectKey(); ok || key != "" {
		t.Fatalf("process-local system actor received durable subject key: %q, %v", key, ok)
	}
	if key, ok := (Actor{}).SubjectKey(); ok || key != "" {
		t.Fatalf("invalid actor received subject key: %q, %v", key, ok)
	}
}
