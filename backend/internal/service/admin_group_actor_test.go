package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/stretchr/testify/require"
)

type adminGroupActorStore struct {
	snapshot authz.SubjectSnapshot
}

type adminGroupActorRepoStub struct {
	GroupRepository
	group *Group
}

func (s *adminGroupActorRepoStub) GetByID(context.Context, int64) (*Group, error) {
	return s.group, nil
}

func (s adminGroupActorStore) LoadSubjectSnapshot(context.Context, authz.SubjectRef) (authz.SubjectSnapshot, error) {
	return s.snapshot, nil
}

func (s adminGroupActorStore) LoadServicePrincipalSubjectSnapshotByCode(context.Context, string) (authz.SubjectSnapshot, error) {
	return s.snapshot, nil
}

func adminResourceTestActor(t testing.TB, kind authz.SubjectKind, id int64) authz.Actor {
	t.Helper()
	subject, err := authz.NewSubjectRef(kind, id)
	require.NoError(t, err)
	configuration, err := authz.NewPolicyConfiguration(authz.PolicyConfigurationInput{
		RoleAuthorizationMode: authz.RoleAuthorizationModeLegacy,
	})
	require.NoError(t, err)
	snapshot, err := authz.NewSubjectSnapshot(authz.SubjectSnapshotInput{
		Subject:            subject,
		Exists:             true,
		Active:             true,
		AuthzVersion:       1,
		CurrentLegacyAdmin: kind == authz.SubjectKindUser,
		Configuration:      configuration,
	})
	require.NoError(t, err)

	resolver := authz.NewActorResolver(adminGroupActorStore{snapshot: snapshot})
	if kind == authz.SubjectKindServicePrincipal {
		actor, resolveErr := resolver.ResolveServicePrincipal(
			context.Background(),
			authz.AdminAPIKeyServicePrincipalCode,
			authz.AuthMethodAdminAPIKey,
		)
		require.NoError(t, resolveErr)
		return actor
	}
	actor, resolveErr := resolver.ResolveUser(context.Background(), id, authz.AuthMethodJWT)
	require.NoError(t, resolveErr)
	return actor
}

func adminResourceUserTestActor(t testing.TB) authz.Actor {
	t.Helper()
	return adminResourceTestActor(t, authz.SubjectKindUser, 1)
}

func TestAdminGroupServiceRejectsMissingActorBeforeRepositoryAccess(t *testing.T) {
	svc := &adminServiceImpl{}
	group, err := svc.GetGroup(context.Background(), authz.Actor{}, 7)

	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, group)
}

func TestAdminGroupServiceAcceptsTrustedUserAndServicePrincipalActors(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		actor authz.Actor
	}{
		{name: "user", actor: adminResourceTestActor(t, authz.SubjectKindUser, 41)},
		{name: "service principal", actor: adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 73)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			expected := &Group{ID: 7, Name: "group"}
			svc := &adminServiceImpl{groupRepo: &adminGroupActorRepoStub{group: expected}}

			group, err := svc.GetGroup(context.Background(), testCase.actor, expected.ID)

			require.NoError(t, err)
			require.Same(t, expected, group)
		})
	}
}
