package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type adminChannelActorRepoStub struct {
	ChannelRepository
	listCalls         int
	getByIDCalls      int
	existsByNameCalls int
	getGroupIDsCalls  int
	deleteCalls       int
	channels          []Channel
	page              *pagination.PaginationResult
}

func (r *adminChannelActorRepoStub) List(
	context.Context,
	pagination.PaginationParams,
	string,
	string,
) ([]Channel, *pagination.PaginationResult, error) {
	r.listCalls++
	return r.channels, r.page, nil
}

func (r *adminChannelActorRepoStub) GetByID(context.Context, int64) (*Channel, error) {
	r.getByIDCalls++
	return &Channel{ID: 7, Name: "channel"}, nil
}

func (r *adminChannelActorRepoStub) ExistsByName(context.Context, string) (bool, error) {
	r.existsByNameCalls++
	return false, nil
}

func (r *adminChannelActorRepoStub) GetGroupIDs(context.Context, int64) ([]int64, error) {
	r.getGroupIDsCalls++
	return nil, nil
}

func (r *adminChannelActorRepoStub) Delete(context.Context, int64) error {
	r.deleteCalls++
	return nil
}

func (r *adminChannelActorRepoStub) totalCalls() int {
	return r.listCalls + r.getByIDCalls + r.existsByNameCalls + r.getGroupIDsCalls + r.deleteCalls
}

func TestAdminChannelFacadesRejectMissingActorBeforeRepositoryAccess(t *testing.T) {
	repo := &adminChannelActorRepoStub{}
	svc := NewChannelService(repo, nil, nil, nil)
	ctx := context.Background()
	actor := authz.Actor{}

	channels, resultPage, err := svc.AdminListChannels(ctx, actor, pagination.PaginationParams{}, "", "")
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, channels)
	require.Nil(t, resultPage)

	channel, err := svc.AdminGetChannel(ctx, actor, 7)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, channel)

	channel, err = svc.AdminCreateChannel(ctx, actor, &CreateChannelInput{Name: "new channel"})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, channel)

	channel, err = svc.AdminUpdateChannel(ctx, actor, 7, &UpdateChannelInput{Name: "renamed channel"})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, channel)

	err = svc.AdminDeleteChannel(ctx, actor, 7)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Zero(t, repo.totalCalls())
}

func TestAdminChannelFacadesAcceptTrustedActorKinds(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		actor authz.Actor
	}{
		{name: "jwt user", actor: adminResourceTestActor(t, authz.SubjectKindUser, 41)},
		{name: "admin api key service principal", actor: adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 73)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			expectedChannels := []Channel{{ID: 7, Name: "channel"}}
			expectedPage := &pagination.PaginationResult{Page: 2, PageSize: 25}
			repo := &adminChannelActorRepoStub{channels: expectedChannels, page: expectedPage}
			svc := NewChannelService(repo, nil, nil, nil)

			channels, resultPage, err := svc.AdminListChannels(
				context.Background(),
				testCase.actor,
				pagination.PaginationParams{Page: 2, PageSize: 25},
				StatusActive,
				"channel",
			)

			require.NoError(t, err)
			require.Equal(t, expectedChannels, channels)
			require.Same(t, expectedPage, resultPage)
			require.Equal(t, 1, repo.listCalls)
		})
	}
}
