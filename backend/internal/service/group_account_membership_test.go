package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type groupAccountGroupRepoStub struct {
	GroupRepository
	group *Group
}

func (s *groupAccountGroupRepoStub) GetByIDLite(context.Context, int64) (*Group, error) {
	if s.group == nil {
		return nil, ErrGroupNotFound
	}
	copy := *s.group
	return &copy, nil
}

type groupAccountRepoStub struct {
	AccountRepository
	memberPage    *GroupAccountRepositoryPage
	candidatePage *GroupAccountRepositoryPage
	snapshot      GroupAccountMembershipSnapshot
	mutation      *GroupAccountMembershipMutation
	applyCalls    int
	lastPolicy    GroupAccountCandidatePolicy
}

func (s *groupAccountRepoStub) ListGroupAccountMembers(context.Context, int64, GroupAccountListFilters) (*GroupAccountRepositoryPage, error) {
	return s.memberPage, nil
}

func (s *groupAccountRepoStub) ListGroupAccountCandidates(_ context.Context, _ int64, _ GroupAccountListFilters, policy GroupAccountCandidatePolicy) (*GroupAccountRepositoryPage, error) {
	s.lastPolicy = policy
	return s.candidatePage, nil
}

func (s *groupAccountRepoStub) ApplyGroupAccountMembershipDiff(
	_ context.Context,
	_ int64,
	_, _ []int64,
	_ int,
	validate GroupAccountMembershipValidator,
) (*GroupAccountMembershipMutation, error) {
	s.applyCalls++
	if err := validate(s.snapshot); err != nil {
		return nil, err
	}
	return s.mutation, nil
}

func TestNormalizeGroupAccountMembershipDiff(t *testing.T) {
	add, remove, err := normalizeGroupAccountMembershipDiff([]int64{3, 1, 3}, []int64{8, 7, 8})
	require.NoError(t, err)
	require.Equal(t, []int64{1, 3}, add)
	require.Equal(t, []int64{7, 8}, remove)

	_, _, err = normalizeGroupAccountMembershipDiff([]int64{1}, []int64{1})
	require.Equal(t, "invalid_account_membership_diff", infraerrors.Reason(err))
	_, _, err = normalizeGroupAccountMembershipDiff(nil, nil)
	require.Equal(t, "invalid_account_membership_diff", infraerrors.Reason(err))
	_, _, err = normalizeGroupAccountMembershipDiff([]int64{0}, nil)
	require.Equal(t, "invalid_account_membership_diff", infraerrors.Reason(err))

	tooMany := make([]int64, GroupAccountMembershipDiffLimit+1)
	for i := range tooMany {
		tooMany[i] = int64(i + 1)
	}
	_, _, err = normalizeGroupAccountMembershipDiff(tooMany, nil)
	require.Equal(t, "account_membership_diff_too_large", infraerrors.Reason(err))
}

func TestAccountEligibilityWarningMatrix(t *testing.T) {
	tests := []struct {
		name    string
		group   Group
		account Account
		want    string
	}{
		{name: "same platform", group: Group{Platform: PlatformOpenAI}, account: Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}},
		{name: "normal mismatch", group: Group{Platform: PlatformOpenAI}, account: Account{Platform: PlatformGemini}, want: GroupAccountWarningPlatformMismatch},
		{name: "anthropic accepts mixed antigravity", group: Group{Platform: PlatformAnthropic}, account: Account{Platform: PlatformAntigravity, Extra: map[string]any{"mixed_scheduling": true}}},
		{name: "anthropic rejects plain antigravity", group: Group{Platform: PlatformAnthropic}, account: Account{Platform: PlatformAntigravity}, want: GroupAccountWarningPlatformMismatch},
		{name: "gemini accepts mixed antigravity", group: Group{Platform: PlatformGemini}, account: Account{Platform: PlatformAntigravity, Extra: map[string]any{"mixed_scheduling": true}}},
		{name: "antigravity does not require mixed flag", group: Group{Platform: PlatformAntigravity}, account: Account{Platform: PlatformAntigravity}},
		{name: "composite accepts concrete", group: Group{Platform: PlatformComposite}, account: Account{Platform: PlatformDeepseek}},
		{name: "composite rejects unsupported", group: Group{Platform: PlatformComposite}, account: Account{Platform: PlatformKiro}, want: GroupAccountWarningPlatformMismatch},
		{name: "oauth only rejects api key", group: Group{Platform: PlatformOpenAI, RequireOAuthOnly: true}, account: Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, want: GroupAccountWarningOAuthRequired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, accountEligibilityWarning(&test.group, &test.account))
		})
	}
}

func TestMixedChannelRiskUsesFinalMembershipAndEffectiveAdds(t *testing.T) {
	anthropic := Account{ID: 1, Platform: PlatformAnthropic}
	antigravity := Account{ID: 2, Platform: PlatformAntigravity}
	openAI := Account{ID: 3, Platform: PlatformOpenAI}

	require.True(t, mixedChannelRiskInFinalSet([]Account{anthropic, antigravity}, []int64{2}))
	require.True(t, mixedChannelRiskInFinalSet([]Account{anthropic, antigravity}, []int64{1, 2}))
	require.False(t, mixedChannelRiskInFinalSet([]Account{antigravity}, []int64{2}), "removing the last conflict in the same diff must clear the risk")
	require.False(t, mixedChannelRiskInFinalSet([]Account{anthropic, antigravity}, nil), "remove-only and no-op diffs must not require confirmation")
	require.False(t, mixedChannelRiskInFinalSet([]Account{anthropic, antigravity, openAI}, []int64{3}), "adding an unrelated platform must not re-challenge an existing mixed group")
}

func TestGroupAccountRiskTokenBindsDiffBaselineAndExpiry(t *testing.T) {
	key := []byte("stable-test-signing-key")
	token, err := issueGroupAccountRiskToken(key, 7, "diff-a", "base-a", time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.True(t, validateGroupAccountRiskToken(token, key, 7, "diff-a", "base-a"))
	require.False(t, validateGroupAccountRiskToken(token, key, 8, "diff-a", "base-a"))
	require.False(t, validateGroupAccountRiskToken(token, key, 7, "diff-b", "base-a"))
	require.False(t, validateGroupAccountRiskToken(token, key, 7, "diff-a", "base-b"))
	require.False(t, validateGroupAccountRiskToken(token+"x", key, 7, "diff-a", "base-a"))

	expired, err := issueGroupAccountRiskToken(key, 7, "diff-a", "base-a", time.Now().Add(-time.Second))
	require.NoError(t, err)
	require.False(t, validateGroupAccountRiskToken(expired, key, 7, "diff-a", "base-a"))
}

func TestApplyGroupAccountMembershipDiffChallengesThenAcceptsBoundToken(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	group := &Group{ID: 7, Name: "anthropic", Platform: PlatformAnthropic, UpdatedAt: now}
	current := Account{ID: 1, Platform: PlatformAnthropic, Type: AccountTypeOAuth, UpdatedAt: now}
	added := Account{ID: 2, Platform: PlatformAntigravity, Type: AccountTypeOAuth, Extra: map[string]any{"mixed_scheduling": true}, UpdatedAt: now}
	repo := &groupAccountRepoStub{
		snapshot: GroupAccountMembershipSnapshot{
			Group:             *group,
			CurrentAccounts:   []Account{current},
			FinalAccounts:     []Account{current, added},
			AddedAccountIDs:   []int64{2},
			RemovedAccountIDs: []int64{},
		},
		mutation: &GroupAccountMembershipMutation{AddedAccountIDs: []int64{2}, AccountCount: 2, ActiveAccountCount: 2},
	}
	svc := &adminServiceImpl{
		groupRepo:   &groupAccountGroupRepoStub{group: group},
		accountRepo: repo,
		settingService: &SettingService{cfg: &config.Config{JWT: config.JWTConfig{
			Secret: strings.Repeat("s", 32),
		}}},
	}

	input := GroupAccountMembershipDiffInput{AddAccountIDs: []int64{2}}
	_, err := svc.ApplyGroupAccountMembershipDiff(context.Background(), group.ID, input)
	require.Equal(t, "mixed_channel_warning", infraerrors.Reason(err))
	token := infraerrors.FromError(err).Metadata["risk_confirmation_token"]
	require.NotEmpty(t, token)

	input.RiskConfirmationToken = token
	result, err := svc.ApplyGroupAccountMembershipDiff(context.Background(), group.ID, input)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, result.AddedAccountIDs)
	require.Equal(t, int64(2), result.AccountCount)

	changed := current
	changed.UpdatedAt = now.Add(time.Second)
	repo.snapshot.CurrentAccounts = []Account{changed}
	repo.snapshot.FinalAccounts = []Account{changed, added}
	_, err = svc.ApplyGroupAccountMembershipDiff(context.Background(), group.ID, input)
	require.Equal(t, "mixed_channel_warning", infraerrors.Reason(err))
	require.NotEqual(t, token, infraerrors.FromError(err).Metadata["risk_confirmation_token"])
}

func TestGroupAccountListPageIncludesZeroScopeTotal(t *testing.T) {
	page := groupAccountListPageFromRepository(&Group{Platform: PlatformOpenAI}, &GroupAccountRepositoryPage{
		Items: []GroupAccountRepositoryRecord{}, Page: 1, PageSize: 20, Pages: 1,
	}, true)
	payload, err := json.Marshal(page)
	require.NoError(t, err)
	require.JSONEq(t, `{"items":[],"total":0,"page":1,"page_size":20,"pages":1,"member_total":0}`, string(payload))
}

func TestGroupAccountListPageUsesEffectiveSchedulingState(t *testing.T) {
	now := time.Now()
	resetAt := now.Add(time.Minute)
	overloadUntil := now.Add(2 * time.Minute)
	page := groupAccountListPageFromRepository(&Group{Platform: PlatformOpenAI}, &GroupAccountRepositoryPage{
		Items: []GroupAccountRepositoryRecord{{Account: Account{
			ID: 1, Name: "limited", Platform: PlatformOpenAI, Type: AccountTypeOAuth,
			Status: StatusActive, Schedulable: true, RateLimitResetAt: &resetAt, OverloadUntil: &overloadUntil,
		}}},
		Total: 1, ScopeTotal: 1, Page: 1, PageSize: 20, Pages: 1,
	}, true)

	require.Len(t, page.Items, 1)
	require.False(t, page.Items[0].Schedulable)
	require.Equal(t, resetAt, *page.Items[0].RateLimitResetAt)
	require.Equal(t, overloadUntil, *page.Items[0].OverloadUntil)
}
