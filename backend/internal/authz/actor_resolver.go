package authz

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const AdminAPIKeyServicePrincipalCode = "admin_api_key"

// ActorResolverStore supplies one database snapshot for every resolved
// identity. The Service Principal lookup must resolve code, status, versions,
// roles, and capabilities in one statement or transaction.
type ActorResolverStore interface {
	LoadSubjectSnapshot(ctx context.Context, subject SubjectRef) (SubjectSnapshot, error)
	LoadServicePrincipalSubjectSnapshotByCode(ctx context.Context, code string) (SubjectSnapshot, error)
}

// Resolver is the trusted bridge from an authenticated identity to an opaque
// authorization Actor.
type Resolver interface {
	ResolveUser(ctx context.Context, userID int64, method AuthMethod) (Actor, error)
	ResolveLegacyAdminUser(ctx context.Context, userID int64) (Actor, error)
	ResolveServicePrincipal(ctx context.Context, code string, method AuthMethod) (Actor, error)
}

type ActorResolver struct {
	store ActorResolverStore
}

func NewActorResolver(store ActorResolverStore) Resolver {
	return &ActorResolver{store: store}
}

func (r *ActorResolver) ResolveUser(ctx context.Context, userID int64, method AuthMethod) (Actor, error) {
	if !method.validFor(SubjectKindUser) {
		return Actor{}, ErrInvalidActor
	}
	subject, err := NewSubjectRef(SubjectKindUser, userID)
	if err != nil {
		return Actor{}, ErrInvalidActor
	}
	if r == nil || r.store == nil || ctx == nil {
		return Actor{}, fmt.Errorf("%w: actor resolver is not configured", ErrAuthorizationUnavailable)
	}

	snapshot, err := r.store.LoadSubjectSnapshot(ctx, subject)
	if err != nil {
		if errors.Is(err, ErrSubjectNotFound) {
			return Actor{}, ErrActorInactive
		}
		return Actor{}, fmt.Errorf("%w: load user subject: %w", ErrAuthorizationUnavailable, err)
	}
	return userActorFromSnapshot(subject, snapshot, method)
}

// ResolveLegacyAdminUser resolves the current database snapshot and uses its
// legacy role as the sole admin gate. This avoids authorizing from the earlier
// compatibility User lookup performed by authentication middleware.
func (r *ActorResolver) ResolveLegacyAdminUser(ctx context.Context, userID int64) (Actor, error) {
	actor, err := r.ResolveUser(ctx, userID, AuthMethodJWT)
	if err != nil {
		return Actor{}, err
	}
	if !actor.hasLegacyAdminBypass() {
		return Actor{}, ErrPolicyAccessDenied
	}
	return actor, nil
}

func (r *ActorResolver) ResolveServicePrincipal(ctx context.Context, code string, method AuthMethod) (Actor, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 64 || !method.validFor(SubjectKindServicePrincipal) ||
		(method == AuthMethodAdminAPIKey && code != AdminAPIKeyServicePrincipalCode) {
		return Actor{}, ErrInvalidActor
	}
	if r == nil || r.store == nil || ctx == nil {
		return Actor{}, fmt.Errorf("%w: actor resolver is not configured", ErrAuthorizationUnavailable)
	}

	snapshot, err := r.store.LoadServicePrincipalSubjectSnapshotByCode(ctx, code)
	if err != nil {
		if errors.Is(err, ErrSubjectNotFound) {
			return Actor{}, ErrActorInactive
		}
		return Actor{}, fmt.Errorf("%w: load service principal subject: %w", ErrAuthorizationUnavailable, err)
	}
	if !snapshot.Valid() || snapshot.Subject().Kind() != SubjectKindServicePrincipal {
		return Actor{}, fmt.Errorf("%w: invalid service principal snapshot", ErrAuthorizationUnavailable)
	}
	if method == AuthMethodAdminAPIKey &&
		(len(snapshot.RoleVersions()) != 0 || len(snapshot.Capabilities()) != 0) {
		return Actor{}, fmt.Errorf("%w: admin API key service principal must not have roles or capabilities", ErrAuthorizationUnavailable)
	}
	if !snapshot.Exists() || !snapshot.Active() {
		return Actor{}, ErrActorInactive
	}

	actor, err := newServicePrincipalActor(snapshot.Subject().ID(), servicePrincipalActorOptions{
		subjectAuthzVersion: snapshot.AuthzVersion(),
		roleVersions:        snapshot.RoleVersions(),
		capabilities:        snapshot.Capabilities(),
		authMethod:          method,
	})
	if err != nil {
		return Actor{}, fmt.Errorf("%w: construct service principal actor: %v", ErrAuthorizationUnavailable, err)
	}
	return actor, nil
}

func userActorFromSnapshot(subject SubjectRef, snapshot SubjectSnapshot, method AuthMethod) (Actor, error) {
	if !snapshot.Valid() || snapshot.Subject() != subject {
		return Actor{}, fmt.Errorf("%w: invalid user subject snapshot", ErrAuthorizationUnavailable)
	}
	if !snapshot.Exists() || !snapshot.Active() {
		return Actor{}, ErrActorInactive
	}
	actor, err := newUserActor(subject.ID(), userActorOptions{
		subjectAuthzVersion: snapshot.AuthzVersion(),
		roleVersions:        snapshot.RoleVersions(),
		capabilities:        snapshot.Capabilities(),
		legacyAdmin:         snapshot.CurrentLegacyAdmin(),
		authMethod:          method,
	})
	if err != nil {
		return Actor{}, fmt.Errorf("%w: construct user actor: %v", ErrAuthorizationUnavailable, err)
	}
	return actor, nil
}
