//go:build unit

package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type duplicateAccountRepoStub struct {
	*sparkShadowRepoStub
	atomicCreateErr        error
	accountGroupsOf        map[int64][]AccountGroup
	validatedGroups        map[int64]Group
	currentAccountsByGroup map[int64][]Account
}

func newDuplicateAccountRepoStub() *duplicateAccountRepoStub {
	return &duplicateAccountRepoStub{
		sparkShadowRepoStub:    newSparkShadowRepoStub(),
		accountGroupsOf:        make(map[int64][]AccountGroup),
		validatedGroups:        make(map[int64]Group),
		currentAccountsByGroup: make(map[int64][]Account),
	}
}

func (s *duplicateAccountRepoStub) CreateWithAccountGroups(ctx context.Context, account *Account, groups []AccountGroup) error {
	if s.atomicCreateErr != nil {
		return s.atomicCreateErr
	}
	groupIDs := make([]int64, 0, len(groups))
	for _, group := range groups {
		groupIDs = append(groupIDs, group.GroupID)
	}
	account.GroupIDs = groupIDs
	if err := s.Create(ctx, account); err != nil {
		return err
	}
	clonedGroups := make([]AccountGroup, len(groups))
	copy(clonedGroups, groups)
	for i := range clonedGroups {
		clonedGroups[i].AccountID = account.ID
	}
	account.AccountGroups = clonedGroups
	s.accountGroupsOf[account.ID] = clonedGroups
	if len(groupIDs) > 0 {
		s.groupsOf[account.ID] = append([]int64(nil), groupIDs...)
	}
	stored := *account
	s.accounts[account.ID] = &stored
	s.mockAccountRepoForGemini.accountsByID[account.ID] = &stored
	return nil
}

func (s *duplicateAccountRepoStub) CreateWithValidatedAccountGroups(
	ctx context.Context,
	account *Account,
	groups []AccountGroup,
	validate AccountGroupCreateValidator,
) error {
	snapshot := AccountGroupCreateSnapshot{
		Groups:                 make([]Group, 0, len(groups)),
		CurrentAccountsByGroup: make(map[int64][]Account, len(groups)),
	}
	for _, binding := range groups {
		group, exists := s.validatedGroups[binding.GroupID]
		if !exists {
			group = Group{ID: binding.GroupID, Name: "group", Platform: account.Platform}
		}
		snapshot.Groups = append(snapshot.Groups, group)
		snapshot.CurrentAccountsByGroup[binding.GroupID] = append(
			[]Account(nil),
			s.currentAccountsByGroup[binding.GroupID]...,
		)
	}
	if validate != nil {
		if err := validate(snapshot); err != nil {
			return err
		}
	}
	return s.CreateWithAccountGroups(ctx, account, groups)
}

func (s *duplicateAccountRepoStub) FindByExtraField(_ context.Context, key string, value any) ([]Account, error) {
	wanted, ok := value.(string)
	if !ok {
		return nil, nil
	}
	var matches []Account
	for _, account := range s.accounts {
		if actual, ok := account.Extra[key].(string); ok && actual == wanted {
			matches = append(matches, *account)
		}
	}
	return matches, nil
}

func TestCreateAccountUsesAtomicAccountAndGroupPersistence(t *testing.T) {
	repo := newDuplicateAccountRepoStub()
	svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}

	account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name:                  "created in group context",
		Platform:              PlatformOpenAI,
		Type:                  AccountTypeAPIKey,
		GroupIDs:              []int64{19, 23},
		SkipDefaultGroupBind:  true,
		SkipMixedChannelCheck: true,
	})

	require.NoError(t, err)
	require.Equal(t, []int64{19, 23}, account.GroupIDs)
	require.Equal(t, []AccountGroup{
		{AccountID: account.ID, GroupID: 19, Priority: groupAccountDefaultPriority},
		{AccountID: account.ID, GroupID: 23, Priority: groupAccountDefaultPriority},
	}, repo.accountGroupsOf[account.ID])
}

func TestCreateAccountDoesNotPersistWhenAtomicGroupBindingFails(t *testing.T) {
	repo := newDuplicateAccountRepoStub()
	repo.atomicCreateErr = errors.New("bind groups")
	svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}

	account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name:                  "must roll back",
		Platform:              PlatformOpenAI,
		Type:                  AccountTypeAPIKey,
		GroupIDs:              []int64{19},
		SkipDefaultGroupBind:  true,
		SkipMixedChannelCheck: true,
	})

	require.Nil(t, account)
	require.ErrorContains(t, err, "bind groups")
	require.Empty(t, repo.accounts)
}

func TestCreateAccountRejectsIncompatibleGroupBeforePersistence(t *testing.T) {
	repo := newDuplicateAccountRepoStub()
	repo.validatedGroups[19] = Group{ID: 19, Name: "Anthropic", Platform: PlatformAnthropic}
	svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}

	account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name:                  "wrong platform",
		Platform:              PlatformOpenAI,
		Type:                  AccountTypeAPIKey,
		GroupIDs:              []int64{19},
		SkipDefaultGroupBind:  true,
		SkipMixedChannelCheck: true,
	})

	require.Nil(t, account)
	requireApplicationErrorReason(t, err, "account_group_platform_mismatch")
	require.Empty(t, repo.accounts)
}

func TestGroupContextCreateRejectsCrossPlatformAntigravityException(t *testing.T) {
	repo := newDuplicateAccountRepoStub()
	repo.validatedGroups[19] = Group{ID: 19, Name: "Anthropic", Platform: PlatformAnthropic}
	svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}

	account, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
		Name:                 "antigravity only joins as an existing account",
		Platform:             PlatformAntigravity,
		Type:                 AccountTypeOAuth,
		Extra:                map[string]any{"mixed_scheduling": true},
		RequiredGroupID:      19,
		SkipDefaultGroupBind: true,
	})

	require.Nil(t, account)
	requireApplicationErrorReason(t, err, "account_group_platform_mismatch")
	require.Empty(t, repo.accounts)
}

func TestGroupContextCreateRequiresBoundRiskTokenAndForcesPathGroup(t *testing.T) {
	repo := newDuplicateAccountRepoStub()
	repo.validatedGroups[19] = Group{
		ID:        19,
		Name:      "Anthropic",
		Platform:  PlatformAnthropic,
		UpdatedAt: time.Unix(100, 0),
	}
	repo.currentAccountsByGroup[19] = []Account{{
		ID:        7,
		Name:      "existing antigravity",
		Platform:  PlatformAntigravity,
		Type:      AccountTypeOAuth,
		UpdatedAt: time.Unix(200, 0),
	}}
	svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}
	input := &CreateAccountInput{
		Name:                  "created from group",
		Platform:              PlatformAnthropic,
		Type:                  AccountTypeOAuth,
		Credentials:           map[string]any{"access_token": "stable-token"},
		RequiredGroupID:       19,
		SkipDefaultGroupBind:  true,
		SkipMixedChannelCheck: true,
	}

	account, err := svc.CreateAccount(context.Background(), input)
	require.Nil(t, account)
	require.Equal(t, "mixed_channel_warning", infraerrors.Reason(err))
	token := infraerrors.FromError(err).Metadata["risk_confirmation_token"]
	require.NotEmpty(t, token)
	require.Empty(t, repo.accounts, "the challenge must not persist an account")

	input.RiskConfirmationToken = token
	account, err = svc.CreateAccount(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, []int64{19}, account.GroupIDs)
	require.Len(t, repo.accounts, 1)
}

func TestGroupContextCreateRejectsRiskTokenAfterRequestOrBaselineChanges(t *testing.T) {
	newFixture := func() (*duplicateAccountRepoStub, *adminServiceImpl, *CreateAccountInput, string) {
		repo := newDuplicateAccountRepoStub()
		repo.validatedGroups[19] = Group{ID: 19, Name: "Anthropic", Platform: PlatformAnthropic, UpdatedAt: time.Unix(100, 0)}
		repo.currentAccountsByGroup[19] = []Account{{ID: 7, Platform: PlatformAntigravity, Type: AccountTypeOAuth, UpdatedAt: time.Unix(200, 0)}}
		svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}
		input := &CreateAccountInput{
			Name:                 "original request",
			Platform:             PlatformAnthropic,
			Type:                 AccountTypeOAuth,
			Credentials:          map[string]any{"access_token": "stable-token"},
			RequiredGroupID:      19,
			SkipDefaultGroupBind: true,
		}
		_, err := svc.CreateAccount(context.Background(), input)
		require.Equal(t, "mixed_channel_warning", infraerrors.Reason(err))
		return repo, svc, input, infraerrors.FromError(err).Metadata["risk_confirmation_token"]
	}

	t.Run("request changed", func(t *testing.T) {
		repo, svc, input, token := newFixture()
		input.Name = "changed request"
		input.RiskConfirmationToken = token
		_, err := svc.CreateAccount(context.Background(), input)
		require.Equal(t, "mixed_channel_warning", infraerrors.Reason(err))
		require.NotEqual(t, token, infraerrors.FromError(err).Metadata["risk_confirmation_token"])
		require.Empty(t, repo.accounts)
	})

	t.Run("baseline changed", func(t *testing.T) {
		repo, svc, input, token := newFixture()
		repo.currentAccountsByGroup[19][0].UpdatedAt = time.Unix(201, 0)
		input.RiskConfirmationToken = token
		_, err := svc.CreateAccount(context.Background(), input)
		require.Equal(t, "mixed_channel_warning", infraerrors.Reason(err))
		require.NotEqual(t, token, infraerrors.FromError(err).Metadata["risk_confirmation_token"])
		require.Empty(t, repo.accounts)
	})
}

func TestDuplicateAccountCopiesConfigurationAndResetsRuntimeState(t *testing.T) {
	ctx := context.Background()
	repo := newDuplicateAccountRepoStub()
	svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}

	notes := "keep this note"
	proxyID := int64(17)
	originalProxyID := int64(11)
	rateMultiplier := 1.25
	loadFactor := 9
	expiresAt := time.Date(2027, time.March, 4, 5, 6, 7, 0, time.UTC)
	rateLimitedAt := time.Now().Add(-time.Minute)
	rateLimitResetAt := time.Now().Add(time.Hour)
	overloadUntil := time.Now().Add(2 * time.Hour)
	tempUnschedulableUntil := time.Now().Add(3 * time.Hour)
	sessionWindowStart := time.Now().Add(-2 * time.Hour)
	sessionWindowEnd := time.Now().Add(2 * time.Hour)

	source := &Account{
		Name:                  "primary",
		Notes:                 &notes,
		Platform:              PlatformAnthropic,
		Type:                  AccountTypeAPIKey,
		ProxyID:               &proxyID,
		ProxyFallbackOriginID: &originalProxyID,
		Concurrency:           6,
		Priority:              40,
		RateMultiplier:        &rateMultiplier,
		LoadFactor:            &loadFactor,
		Status:                StatusError,
		Schedulable:           true,
		ErrorMessage:          "upstream unavailable",
		ExpiresAt:             &expiresAt,
		AutoPauseOnExpired:    false,
		Credentials: map[string]any{
			"api_key": "secret",
			"nested":  map[string]any{"token": "source-token"},
		},
		Extra: map[string]any{
			"config":                          map[string]any{"region": "us-east-1"},
			"items":                           []any{map[string]any{"enabled": true}},
			"quota_limit":                     1000,
			"quota_used":                      450,
			"quota_daily_used":                25,
			"quota_daily_start":               "2026-07-15T00:00:00Z",
			"model_rate_limits":               map[string]any{"gpt-5": "2099-01-01T00:00:00Z"},
			"codex_5h_used_percent":           80,
			"codex_cli_only":                  true,
			"grok_usage_snapshot":             map[string]any{"status_code": 429},
			"openai_responses_supported":      false,
			"openai_compact_checked_at":       "2026-07-15T00:00:00Z",
			"session_window_utilization":      0.8,
			"passive_usage_sampled_at":        "2026-07-15T00:00:00Z",
			"antigravity_force_token_refresh": true,
			"antigravity_credits_overages":    map[string]any{"enabled": true},
			"crs_account_id":                  "remote-42",
			"crs_kind":                        "openai-api-key",
			"crs_synced_at":                   "2026-07-15T00:00:00Z",
		},
		GroupIDs:                []int64{7, 3},
		AccountGroups:           []AccountGroup{{GroupID: 7, Priority: 50}, {GroupID: 3, Priority: 7}},
		RateLimitedAt:           &rateLimitedAt,
		RateLimitResetAt:        &rateLimitResetAt,
		OverloadUntil:           &overloadUntil,
		TempUnschedulableUntil:  &tempUnschedulableUntil,
		TempUnschedulableReason: "maintenance",
		SessionWindowStart:      &sessionWindowStart,
		SessionWindowEnd:        &sessionWindowEnd,
		SessionWindowStatus:     "active",
	}
	source.Extra[UpstreamBillingProbeEnabledExtraKey] = true
	source.Extra[UpstreamBillingRateSyncEnabledExtraKey] = true
	source.Extra[UpstreamBillingProbeExtraKey] = map[string]any{"status": "ok"}
	require.NoError(t, repo.Create(ctx, source))

	duplicate, err := svc.DuplicateAccount(ctx, source.ID, "admin:1", "")

	require.NoError(t, err)
	require.NotEqual(t, source.ID, duplicate.ID)
	require.Equal(t, "primary (Copy)", duplicate.Name)
	require.Equal(t, source.Platform, duplicate.Platform)
	require.Equal(t, source.Type, duplicate.Type)
	require.Equal(t, source.Concurrency, duplicate.Concurrency)
	require.Equal(t, source.Priority, duplicate.Priority)
	require.Equal(t, source.AutoPauseOnExpired, duplicate.AutoPauseOnExpired)
	require.Equal(t, source.GroupIDs, duplicate.GroupIDs)
	require.Equal(t, source.Credentials, duplicate.Credentials)
	require.Equal(t, map[string]any{
		"config":         map[string]any{"region": "us-east-1"},
		"items":          []any{map[string]any{"enabled": true}},
		"quota_limit":    float64(1000),
		"codex_cli_only": true,
	}, duplicate.Extra)
	require.NotContains(t, duplicate.Extra, UpstreamBillingRateSyncEnabledExtraKey)
	require.NotNil(t, duplicate.ExpiresAt)
	require.True(t, source.ExpiresAt.Equal(*duplicate.ExpiresAt))
	require.Equal(t, source.Notes, duplicate.Notes)
	require.Equal(t, source.ProxyFallbackOriginID, duplicate.ProxyID)
	require.Equal(t, source.RateMultiplier, duplicate.RateMultiplier)
	require.Equal(t, source.LoadFactor, duplicate.LoadFactor)
	require.Equal(t, source.GroupIDs, repo.groupsOf[duplicate.ID])
	require.Equal(t, []AccountGroup{
		{AccountID: duplicate.ID, GroupID: 7, Priority: 50},
		{AccountID: duplicate.ID, GroupID: 3, Priority: 7},
	}, repo.accountGroupsOf[duplicate.ID])

	require.Equal(t, StatusActive, duplicate.Status)
	require.False(t, duplicate.Schedulable)
	require.Empty(t, duplicate.ErrorMessage)
	require.Nil(t, duplicate.LastUsedAt)
	require.Nil(t, duplicate.RateLimitedAt)
	require.Nil(t, duplicate.RateLimitResetAt)
	require.Nil(t, duplicate.OverloadUntil)
	require.Nil(t, duplicate.TempUnschedulableUntil)
	require.Empty(t, duplicate.TempUnschedulableReason)
	require.Nil(t, duplicate.SessionWindowStart)
	require.Nil(t, duplicate.SessionWindowEnd)
	require.Empty(t, duplicate.SessionWindowStatus)

	duplicate.Credentials["nested"].(map[string]any)["token"] = "changed"
	duplicate.Extra["config"].(map[string]any)["region"] = "changed"
	duplicate.Extra["items"].([]any)[0].(map[string]any)["enabled"] = false
	storedSource, getErr := repo.GetByID(ctx, source.ID)
	require.NoError(t, getErr)
	require.Equal(t, "source-token", storedSource.Credentials["nested"].(map[string]any)["token"])
	require.Equal(t, "us-east-1", storedSource.Extra["config"].(map[string]any)["region"])
	require.Equal(t, true, storedSource.Extra["items"].([]any)[0].(map[string]any)["enabled"])
	require.Equal(t, "remote-42", storedSource.Extra["crs_account_id"])
}

func TestDuplicateAccountRejectsCredentialShadow(t *testing.T) {
	ctx := context.Background()
	repo := newDuplicateAccountRepoStub()
	svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}
	parentID := int64(99)
	shadow := &Account{
		Name:            "shadow",
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		ParentAccountID: &parentID,
		QuotaDimension:  QuotaDimensionSpark,
	}
	require.NoError(t, repo.Create(ctx, shadow))

	_, err := svc.DuplicateAccount(ctx, shadow.ID, "admin:1", "")

	require.Error(t, err)
	require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
	require.Equal(t, "ACCOUNT_DUPLICATE_SHADOW_UNSUPPORTED", infraerrors.Reason(err))
	require.Len(t, repo.accounts, 1)
}

func TestDuplicateAccountRejectsRotatingOrUnknownCredentialTypes(t *testing.T) {
	for _, accountType := range []string{AccountTypeOAuth, AccountTypeSetupToken, "legacy-cookie"} {
		t.Run(accountType, func(t *testing.T) {
			ctx := context.Background()
			repo := newDuplicateAccountRepoStub()
			svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}
			source := &Account{
				Name:        "rotating-credential-account",
				Platform:    PlatformOpenAI,
				Type:        accountType,
				Credentials: map[string]any{"refresh_token": "shared-token"},
			}
			require.NoError(t, repo.Create(ctx, source))

			_, err := svc.DuplicateAccount(ctx, source.ID, "admin:1", "")

			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, infraerrors.Code(err))
			require.Equal(t, "ACCOUNT_DUPLICATE_CREDENTIAL_TYPE_UNSUPPORTED", infraerrors.Reason(err))
			require.Len(t, repo.accounts, 1)
		})
	}
}

func TestDuplicateAccountPreservesUngroupedState(t *testing.T) {
	ctx := context.Background()
	repo := newDuplicateAccountRepoStub()
	svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}
	source := &Account{
		Name:        "ungrouped",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "secret"},
		GroupIDs:    nil,
	}
	require.NoError(t, repo.Create(ctx, source))

	duplicate, err := svc.DuplicateAccount(ctx, source.ID, "admin:1", "")

	require.NoError(t, err)
	require.Empty(t, duplicate.GroupIDs)
	require.NotContains(t, repo.groupsOf, duplicate.ID)
}

func TestDuplicateAccountAtomicCreateFailureLeavesNoOrphan(t *testing.T) {
	ctx := context.Background()
	repo := newDuplicateAccountRepoStub()
	svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}
	source := &Account{
		Name:          "source",
		Platform:      PlatformAnthropic,
		Type:          AccountTypeAPIKey,
		Credentials:   map[string]any{"api_key": "secret"},
		GroupIDs:      []int64{7},
		AccountGroups: []AccountGroup{{GroupID: 7, Priority: 25}},
	}
	require.NoError(t, repo.Create(ctx, source))
	repo.atomicCreateErr = errors.New("group binding failed")

	_, err := svc.DuplicateAccount(ctx, source.ID, "admin:1", "")

	require.ErrorContains(t, err, "group binding failed")
	require.Len(t, repo.accounts, 1)
}

func TestDuplicateAccountRejectsHistoricalIncompatibleGroupWithoutPersistence(t *testing.T) {
	ctx := context.Background()
	repo := newDuplicateAccountRepoStub()
	svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}
	source := &Account{
		Name:          "historical-api-key",
		Platform:      PlatformAnthropic,
		Type:          AccountTypeAPIKey,
		Credentials:   map[string]any{"api_key": "secret"},
		GroupIDs:      []int64{7},
		AccountGroups: []AccountGroup{{GroupID: 7, Priority: 25}},
	}
	repo.validatedGroups[7] = Group{
		ID:               7,
		Name:             "oauth-only",
		Platform:         PlatformAnthropic,
		RequireOAuthOnly: true,
	}
	require.NoError(t, repo.Create(ctx, source))

	duplicate, err := svc.DuplicateAccount(ctx, source.ID, "admin:1", "")

	require.Nil(t, duplicate)
	require.Equal(t, "account_group_policy_violation", infraerrors.Reason(err))
	require.Len(t, repo.accounts, 1)
	require.Empty(t, repo.accountGroupsOf)
}

func TestDuplicateAccountNamePreservesSuffixWithinSchemaLimit(t *testing.T) {
	name := duplicateAccountName(strings.Repeat("界", 100))

	require.Equal(t, 100, utf8.RuneCountInString(name))
	require.True(t, strings.HasSuffix(name, " (Copy)"))
}

func TestDuplicateAccountReturnsExistingCopyForSameOperationKey(t *testing.T) {
	ctx := context.Background()
	repo := newDuplicateAccountRepoStub()
	svc := &adminServiceImpl{accountRepo: repo, accountDuplicateRepo: repo}
	source := &Account{
		Name:        "source",
		Platform:    PlatformAnthropic,
		Type:        AccountTypeAPIKey,
		Credentials: map[string]any{"api_key": "secret"},
	}
	require.NoError(t, repo.Create(ctx, source))

	first, err := svc.DuplicateAccount(ctx, source.ID, "admin:7", "stable-operation-key")
	require.NoError(t, err)
	second, err := svc.DuplicateAccount(ctx, source.ID, "admin:7", "stable-operation-key")
	require.NoError(t, err)
	recovered, err := svc.RecoverDuplicateAccount(ctx, source.ID, "admin:7", "stable-operation-key")
	require.NoError(t, err)
	otherAdminRecovery, err := svc.RecoverDuplicateAccount(ctx, source.ID, "admin:8", "stable-operation-key")
	require.NoError(t, err)
	otherAdminCopy, err := svc.DuplicateAccount(ctx, source.ID, "admin:8", "stable-operation-key")
	require.NoError(t, err)

	require.Equal(t, first.ID, second.ID)
	require.Equal(t, first.ID, recovered.ID)
	require.Nil(t, otherAdminRecovery, "durable recovery identity must remain scoped to the initiating admin")
	require.NotEqual(t, first.ID, otherAdminCopy.ID)
	require.Len(t, repo.accounts, 3)
	require.NotEmpty(t, first.Extra[duplicateAccountOperationIDExtraKey])
}
