package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/stretchr/testify/require"
)

type adminAccountActorRepoStub struct {
	AccountRepository
	getByIDCalls int
}

func (s *adminAccountActorRepoStub) GetByID(context.Context, int64) (*Account, error) {
	s.getByIDCalls++
	return &Account{ID: 17}, nil
}

func TestAdminAccountServiceRejectsMissingActorBeforeRepositoryAccess(t *testing.T) {
	repo := &adminAccountActorRepoStub{}
	svc := &adminServiceImpl{accountRepo: repo}

	account, err := svc.GetAccount(context.Background(), authz.Actor{}, 17)

	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, account)
	require.Zero(t, repo.getByIDCalls)
}

func TestAdminAccountPrivacyMethodsRejectMissingActorBeforeSideEffects(t *testing.T) {
	svc := &adminServiceImpl{}
	ctx := context.Background()
	actor := authz.Actor{}

	require.Empty(t, svc.EnsureOpenAIPrivacy(ctx, actor, nil))
	require.Empty(t, svc.ForceOpenAIPrivacy(ctx, actor, nil))
	require.Empty(t, svc.EnsureAntigravityPrivacy(ctx, actor, nil))
	require.Empty(t, svc.ForceAntigravityPrivacy(ctx, actor, nil))
}

func TestAdminAccountServiceAcceptsTrustedUserAndServicePrincipalActors(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		actor authz.Actor
	}{
		{name: "user", actor: adminResourceTestActor(t, authz.SubjectKindUser, 41)},
		{name: "service principal", actor: adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 73)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &adminAccountActorRepoStub{}
			svc := &adminServiceImpl{accountRepo: repo}

			account, err := svc.GetAccount(context.Background(), testCase.actor, 17)

			require.NoError(t, err)
			require.Equal(t, int64(17), account.ID)
			require.Equal(t, 1, repo.getByIDCalls)
		})
	}
}
