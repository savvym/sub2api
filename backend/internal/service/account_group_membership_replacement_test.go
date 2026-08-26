package service

import (
	"context"
	"testing"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type accountGroupReplacementRepoStub struct {
	snapshot        AccountGroupMembershipReplacementSnapshot
	err             error
	called          bool
	accountID       int64
	desiredGroupIDs []int64
	defaultPriority int
}

func (s *accountGroupReplacementRepoStub) ReplaceAccountGroupMemberships(
	_ context.Context,
	accountID int64,
	desiredGroupIDs []int64,
	defaultPriority int,
	validate AccountGroupMembershipReplacementValidator,
) (*AccountGroupMembershipReplacement, error) {
	s.called = true
	s.accountID = accountID
	s.desiredGroupIDs = append([]int64(nil), desiredGroupIDs...)
	s.defaultPriority = defaultPriority
	if s.err != nil {
		return nil, s.err
	}
	if validate != nil {
		if err := validate(s.snapshot); err != nil {
			return nil, err
		}
	}
	return &AccountGroupMembershipReplacement{
		CurrentGroupIDs: append([]int64(nil), s.snapshot.CurrentGroupIDs...),
		DesiredGroupIDs: append([]int64(nil), s.snapshot.DesiredGroupIDs...),
		AddedGroupIDs:   append([]int64(nil), s.snapshot.AddedGroupIDs...),
		RemovedGroupIDs: append([]int64(nil), s.snapshot.RemovedGroupIDs...),
	}, nil
}

func TestReplaceAccountGroupMembershipsValidatesEffectiveAdditions(t *testing.T) {
	repo := &accountGroupReplacementRepoStub{snapshot: AccountGroupMembershipReplacementSnapshot{
		Account:         Account{ID: 7, Platform: PlatformGemini, Type: AccountTypeOAuth},
		GroupsByID:      map[int64]Group{10: {ID: 10, Platform: PlatformOpenAI}},
		DesiredGroupIDs: []int64{10},
		AddedGroupIDs:   []int64{10},
		FinalAccountsByGroup: map[int64][]Account{
			10: {{ID: 7, Platform: PlatformGemini, Type: AccountTypeOAuth}},
		},
	}}
	svc := &adminServiceImpl{accountGroupRepo: repo}

	err := svc.replaceAccountGroupMemberships(context.Background(), 7, []int64{10}, nil, nil, false)

	require.Error(t, err)
	require.Equal(t, "account_group_platform_mismatch", infraerrors.Reason(err))
	require.True(t, repo.called)
	require.Equal(t, groupAccountDefaultPriority, repo.defaultPriority)
}

func TestReplaceAccountGroupMembershipsEnforcesOAuthOnly(t *testing.T) {
	repo := &accountGroupReplacementRepoStub{snapshot: AccountGroupMembershipReplacementSnapshot{
		Account: Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		GroupsByID: map[int64]Group{
			10: {ID: 10, Platform: PlatformOpenAI, RequireOAuthOnly: true},
		},
		AddedGroupIDs: []int64{10},
	}}
	svc := &adminServiceImpl{accountGroupRepo: repo}

	err := svc.replaceAccountGroupMemberships(context.Background(), 7, []int64{10}, nil, nil, false)

	require.Error(t, err)
	require.Equal(t, "account_group_policy_violation", infraerrors.Reason(err))
}

func TestReplaceAccountGroupMembershipsChecksLockedFinalSetForMixedChannels(t *testing.T) {
	repo := &accountGroupReplacementRepoStub{snapshot: AccountGroupMembershipReplacementSnapshot{
		Account:       Account{ID: 7, Platform: PlatformAntigravity, Type: AccountTypeOAuth},
		GroupsByID:    map[int64]Group{10: {ID: 10, Name: "mixed", Platform: PlatformComposite}},
		AddedGroupIDs: []int64{10},
		FinalAccountsByGroup: map[int64][]Account{
			10: {
				{ID: 7, Platform: PlatformAntigravity},
				{ID: 8, Platform: PlatformAnthropic},
			},
		},
	}}
	svc := &adminServiceImpl{accountGroupRepo: repo}

	err := svc.replaceAccountGroupMemberships(context.Background(), 7, []int64{10}, nil, nil, false)

	var mixedErr *MixedChannelError
	require.ErrorAs(t, err, &mixedErr)
	require.Equal(t, int64(10), mixedErr.GroupID)
}

func TestReplaceAccountGroupMembershipsDoesNotRevalidateExistingBindings(t *testing.T) {
	repo := &accountGroupReplacementRepoStub{snapshot: AccountGroupMembershipReplacementSnapshot{
		Account:         Account{ID: 7, Platform: PlatformGemini, Type: AccountTypeAPIKey},
		GroupsByID:      map[int64]Group{10: {ID: 10, Platform: PlatformOpenAI, RequireOAuthOnly: true}},
		CurrentGroupIDs: []int64{10},
		DesiredGroupIDs: []int64{10},
		FinalAccountsByGroup: map[int64][]Account{
			10: {
				{ID: 7, Platform: PlatformAntigravity},
				{ID: 8, Platform: PlatformAnthropic},
			},
		},
	}}
	svc := &adminServiceImpl{accountGroupRepo: repo}

	err := svc.replaceAccountGroupMemberships(context.Background(), 7, []int64{10}, nil, nil, false)

	require.NoError(t, err)
}

func TestReplaceAccountGroupMembershipsHonorsConfirmedMixedChannelRisk(t *testing.T) {
	repo := &accountGroupReplacementRepoStub{snapshot: AccountGroupMembershipReplacementSnapshot{
		Account:       Account{ID: 7, Platform: PlatformAntigravity, Type: AccountTypeOAuth},
		GroupsByID:    map[int64]Group{10: {ID: 10, Platform: PlatformComposite}},
		AddedGroupIDs: []int64{10},
		FinalAccountsByGroup: map[int64][]Account{
			10: {
				{ID: 7, Platform: PlatformAntigravity},
				{ID: 8, Platform: PlatformAnthropic},
			},
		},
	}}
	svc := &adminServiceImpl{accountGroupRepo: repo}

	err := svc.replaceAccountGroupMemberships(context.Background(), 7, []int64{10}, nil, nil, true)

	require.NoError(t, err)
}

func TestReplaceAccountGroupMembershipsAcceptsMatchingExpectedBaseline(t *testing.T) {
	expectedGroupIDs := []int64{20, 10, 20}
	repo := &accountGroupReplacementRepoStub{snapshot: AccountGroupMembershipReplacementSnapshot{
		Account:         Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		GroupsByID:      map[int64]Group{10: {ID: 10}, 20: {ID: 20}},
		CurrentGroupIDs: []int64{10, 20},
		DesiredGroupIDs: []int64{20},
		RemovedGroupIDs: []int64{10},
	}}
	svc := &adminServiceImpl{accountGroupRepo: repo}

	err := svc.replaceAccountGroupMemberships(
		context.Background(),
		7,
		[]int64{20},
		&expectedGroupIDs,
		nil,
		false,
	)

	require.NoError(t, err)
	require.Equal(t, []int64{20}, repo.desiredGroupIDs)
}

func TestReplaceAccountGroupMembershipsRejectsStaleExpectedBaseline(t *testing.T) {
	expectedGroupIDs := []int64{10}
	repo := &accountGroupReplacementRepoStub{snapshot: AccountGroupMembershipReplacementSnapshot{
		Account:         Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		GroupsByID:      map[int64]Group{10: {ID: 10}, 20: {ID: 20}},
		CurrentGroupIDs: []int64{10, 20},
		DesiredGroupIDs: []int64{20},
		RemovedGroupIDs: []int64{10},
	}}
	svc := &adminServiceImpl{accountGroupRepo: repo}

	err := svc.replaceAccountGroupMemberships(
		context.Background(),
		7,
		[]int64{20},
		&expectedGroupIDs,
		nil,
		false,
	)

	require.Error(t, err)
	require.Equal(t, "account_group_membership_stale", infraerrors.Reason(err))
	require.Equal(t, "7", infraerrors.FromError(err).Metadata["account_id"])
}

func TestReplaceAccountGroupMembershipsUsesPendingAccountFieldsForLockedEligibility(t *testing.T) {
	tests := []struct {
		name           string
		pendingAccount Account
		wantReason     string
	}{
		{
			name:           "platform changed before adding group",
			pendingAccount: Account{ID: 7, Platform: PlatformGemini, Type: AccountTypeOAuth},
			wantReason:     "account_group_platform_mismatch",
		},
		{
			name:           "type changed before adding oauth-only group",
			pendingAccount: Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
			wantReason:     "account_group_policy_violation",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &accountGroupReplacementRepoStub{snapshot: AccountGroupMembershipReplacementSnapshot{
				Account:         Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth},
				GroupsByID:      map[int64]Group{10: {ID: 10, Platform: PlatformOpenAI, RequireOAuthOnly: true}},
				DesiredGroupIDs: []int64{10},
				AddedGroupIDs:   []int64{10},
			}}
			svc := &adminServiceImpl{accountGroupRepo: repo}

			err := svc.replaceAccountGroupMemberships(
				context.Background(),
				7,
				[]int64{10},
				nil,
				&test.pendingAccount,
				false,
			)

			require.Error(t, err)
			require.Equal(t, test.wantReason, infraerrors.Reason(err))
		})
	}
}

func TestReplaceAccountGroupMembershipsFailsClosedWithoutRepository(t *testing.T) {
	err := (&adminServiceImpl{}).replaceAccountGroupMemberships(context.Background(), 7, []int64{10}, nil, nil, false)

	require.Error(t, err)
	require.Equal(t, "account_group_membership_repository_unavailable", infraerrors.Reason(err))
}
