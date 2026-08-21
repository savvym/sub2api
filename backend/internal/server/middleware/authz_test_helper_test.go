//go:build unit

package middleware

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type middlewareActorResolverStore struct {
	userSnapshots             map[int64]authz.SubjectSnapshot
	servicePrincipalSnapshots map[string]authz.SubjectSnapshot
	userErr                   error
	servicePrincipalErr       error
	userCalls                 int
	servicePrincipalCalls     int
}

func newMiddlewareActorResolver(
	t testing.TB,
	users map[int64]*service.User,
) (authz.Resolver, *middlewareActorResolverStore) {
	t.Helper()
	store := &middlewareActorResolverStore{
		userSnapshots:             make(map[int64]authz.SubjectSnapshot),
		servicePrincipalSnapshots: make(map[string]authz.SubjectSnapshot),
	}
	for id, user := range users {
		store.userSnapshots[id] = mustMiddlewareSubjectSnapshot(
			t,
			authz.SubjectKindUser,
			id,
			true,
			user.IsActive(),
			user.IsAdmin(),
		)
	}
	return authz.NewActorResolver(store), store
}

func (s *middlewareActorResolverStore) LoadSubjectSnapshot(_ context.Context, subject authz.SubjectRef) (authz.SubjectSnapshot, error) {
	s.userCalls++
	if s.userErr != nil {
		return authz.SubjectSnapshot{}, s.userErr
	}
	return s.userSnapshots[subject.ID()], nil
}

func (s *middlewareActorResolverStore) LoadServicePrincipalSubjectSnapshotByCode(_ context.Context, code string) (authz.SubjectSnapshot, error) {
	s.servicePrincipalCalls++
	if s.servicePrincipalErr != nil {
		return authz.SubjectSnapshot{}, s.servicePrincipalErr
	}
	return s.servicePrincipalSnapshots[code], nil
}

func (s *middlewareActorResolverStore) setServicePrincipal(
	t testing.TB,
	code string,
	id int64,
	exists bool,
	active bool,
) {
	t.Helper()
	if !exists {
		delete(s.servicePrincipalSnapshots, code)
		s.servicePrincipalErr = authz.ErrSubjectNotFound
		return
	}
	s.servicePrincipalErr = nil
	s.servicePrincipalSnapshots[code] = mustMiddlewareSubjectSnapshot(
		t,
		authz.SubjectKindServicePrincipal,
		id,
		exists,
		active,
		false,
	)
}

func mustMiddlewareSubjectSnapshot(
	t testing.TB,
	kind authz.SubjectKind,
	id int64,
	exists bool,
	active bool,
	legacyAdmin bool,
) authz.SubjectSnapshot {
	t.Helper()
	subject, err := authz.NewSubjectRef(kind, id)
	require.NoError(t, err)
	configuration, err := authz.NewPolicyConfiguration(authz.PolicyConfigurationInput{
		RoleAuthorizationMode: authz.RoleAuthorizationModeLegacy,
	})
	require.NoError(t, err)

	input := authz.SubjectSnapshotInput{
		Subject:            subject,
		Exists:             exists,
		Active:             active,
		CurrentLegacyAdmin: legacyAdmin,
		Configuration:      configuration,
	}
	if exists {
		input.AuthzVersion = 1
	}
	snapshot, err := authz.NewSubjectSnapshot(input)
	require.NoError(t, err)
	return snapshot
}
