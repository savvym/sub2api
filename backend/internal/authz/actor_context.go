package authz

import "context"

type actorContextKey struct{}

// ContextWithActor attaches a resolved Actor to a request context. Invalid
// actors are still stored so they shadow any parent Actor and fail closed when
// read back.
func ContextWithActor(ctx context.Context, actor Actor) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, actorContextKey{}, actor)
}

// ActorFromContext returns only a valid Actor produced by ActorResolver.
func ActorFromContext(ctx context.Context) (Actor, bool) {
	if ctx == nil {
		return Actor{}, false
	}
	actor, ok := ctx.Value(actorContextKey{}).(Actor)
	if !ok || !actor.Valid() {
		return Actor{}, false
	}
	return actor, true
}
