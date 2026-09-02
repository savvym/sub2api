//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/stretchr/testify/require"
)

type accountOwnershipCaptureRepo struct {
	AccountRepository
	created *Account
}

func (r *accountOwnershipCaptureRepo) Create(_ context.Context, account *Account) error {
	account.ID = 1
	copyAccount := *account
	r.created = &copyAccount
	return nil
}

func TestAccountCreationAuthorityOverwritesUntrustedOwnership(t *testing.T) {
	forgedOwnerID := int64(999)
	forgedCreatorID := int64(998)
	forgedAccess := "public"

	for _, testCase := range []struct {
		name            string
		actor           authz.Actor
		wantCreatorUser *int64
	}{
		{name: "jwt admin", actor: adminResourceTestActor(t, authz.SubjectKindUser, 41), wantCreatorUser: accountCreationInt64Pointer(41)},
		{name: "service principal", actor: adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 73)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			authority, err := newPlatformAccountCreationAuthority(testCase.actor)
			require.NoError(t, err)
			account := &Account{
				OwnerUserID:       &forgedOwnerID,
				CreatedByUserID:   &forgedCreatorID,
				PublicAccessLevel: &forgedAccess,
			}

			require.NoError(t, authority.apply(account))
			require.Nil(t, account.OwnerUserID)
			require.Equal(t, testCase.wantCreatorUser, account.CreatedByUserID)
			require.Nil(t, account.PublicAccessLevel)
		})
	}
}

func TestOwnedAccountCreationAuthorityBindsJWTUser(t *testing.T) {
	actor := adminResourceTestActor(t, authz.SubjectKindUser, 41)
	authority, err := newOwnedAccountCreationAuthority(actor)
	require.NoError(t, err)

	forgedOwnerID := int64(999)
	forgedCreatorID := int64(998)
	forgedAccess := "public"
	account := &Account{
		OwnerUserID:       &forgedOwnerID,
		CreatedByUserID:   &forgedCreatorID,
		PublicAccessLevel: &forgedAccess,
	}
	require.NoError(t, authority.apply(account))
	require.Equal(t, int64(41), *account.OwnerUserID)
	require.Equal(t, int64(41), *account.CreatedByUserID)
	require.Nil(t, account.PublicAccessLevel)

	_, err = newOwnedAccountCreationAuthority(adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 73))
	require.ErrorIs(t, err, ErrSelfServiceAccountActorRequired)
}

func TestAccountCreationWriteSinksApplyPlatformAuthority(t *testing.T) {
	for _, actorCase := range []struct {
		name            string
		actor           authz.Actor
		wantCreatorUser *int64
	}{
		{name: "jwt admin", actor: adminResourceTestActor(t, authz.SubjectKindUser, 41), wantCreatorUser: accountCreationInt64Pointer(41)},
		{name: "service principal", actor: adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 73)},
	} {
		t.Run(actorCase.name, func(t *testing.T) {
			t.Run("account service", func(t *testing.T) {
				repo := &accountOwnershipCaptureRepo{}
				svc := NewAccountService(repo, nil)
				created, err := svc.AdminCreate(context.Background(), actorCase.actor, CreateAccountRequest{
					Name:        "base account",
					Platform:    PlatformAnthropic,
					Type:        AccountTypeAPIKey,
					Credentials: map[string]any{"api_key": "secret"},
				})
				require.NoError(t, err)
				require.Equal(t, repo.created, created)
				require.Nil(t, created.OwnerUserID)
				require.Equal(t, actorCase.wantCreatorUser, created.CreatedByUserID)
				require.Nil(t, created.PublicAccessLevel)
			})

			t.Run("admin create used by oauth import and batch", func(t *testing.T) {
				repo := &accountOwnershipCaptureRepo{}
				svc := &adminServiceImpl{accountRepo: repo}
				created, err := svc.CreateAccount(context.Background(), actorCase.actor, &CreateAccountInput{
					Name:                  "admin account",
					Platform:              PlatformAnthropic,
					Type:                  AccountTypeAPIKey,
					Credentials:           map[string]any{"api_key": "secret"},
					SkipDefaultGroupBind:  true,
					SkipMixedChannelCheck: true,
				})
				require.NoError(t, err)
				require.Equal(t, repo.created, created)
				require.Nil(t, created.OwnerUserID)
				require.Equal(t, actorCase.wantCreatorUser, created.CreatedByUserID)
				require.Nil(t, created.PublicAccessLevel)
			})
		})
	}
}
