package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const (
	GroupAccountMembershipDiffLimit = 500
	groupAccountDefaultPriority     = 50
	groupAccountRiskTokenTTL        = 5 * time.Minute
)

const (
	GroupAccountWarningPlatformMismatch = "platform_mismatch"
	GroupAccountWarningOAuthRequired    = "oauth_required"
	GroupAccountWarningPrivacyNotSet    = "privacy_not_set"
)

type GroupAccountListFilters struct {
	Page        int
	PageSize    int
	Search      string
	AccountType string
	Status      string
	Platform    string
}

type GroupAccountSummary struct {
	ID                     int64      `json:"id"`
	Name                   string     `json:"name"`
	Platform               string     `json:"platform"`
	Type                   string     `json:"type"`
	Status                 string     `json:"status"`
	Schedulable            bool       `json:"schedulable"`
	RateLimitedAt          *time.Time `json:"rate_limited_at,omitempty"`
	RateLimitResetAt       *time.Time `json:"rate_limit_reset_at,omitempty"`
	OverloadUntil          *time.Time `json:"overload_until,omitempty"`
	TempUnschedulableUntil *time.Time `json:"temp_unschedulable_until,omitempty"`
	GroupCount             int        `json:"group_count"`
	PolicyWarnings         []string   `json:"policy_warnings"`
}

type GroupAccountListPage struct {
	Items         []GroupAccountSummary `json:"items"`
	Total         int64                 `json:"total"`
	Page          int                   `json:"page"`
	PageSize      int                   `json:"page_size"`
	Pages         int                   `json:"pages"`
	MemberTotal   *int64                `json:"member_total,omitempty"`
	EligibleTotal *int64                `json:"eligible_total,omitempty"`
}

type GroupAccountMembershipDiffInput struct {
	AddAccountIDs         []int64 `json:"add_account_ids"`
	RemoveAccountIDs      []int64 `json:"remove_account_ids"`
	RiskConfirmationToken string  `json:"risk_confirmation_token,omitempty"`
}

type GroupAccountMembershipDiffResult struct {
	AddedAccountIDs         []int64 `json:"added_account_ids"`
	RemovedAccountIDs       []int64 `json:"removed_account_ids"`
	AlreadyMemberAccountIDs []int64 `json:"already_member_account_ids"`
	NotMemberAccountIDs     []int64 `json:"not_member_account_ids"`
	AccountCount            int64   `json:"account_count"`
	ActiveAccountCount      int64   `json:"active_account_count"`
	RateLimitedAccountCount int64   `json:"rate_limited_account_count"`
}

// GroupAccountRepositoryRecord contains the full account only inside the
// service/repository boundary. HTTP responses are built from the redacted
// GroupAccountSummary above.
type GroupAccountRepositoryRecord struct {
	Account    Account
	GroupCount int
}

type GroupAccountRepositoryPage struct {
	Items      []GroupAccountRepositoryRecord
	Total      int64
	ScopeTotal int64
	Page       int
	PageSize   int
	Pages      int
}

type GroupAccountCandidatePolicy struct {
	AllowedPlatforms                     []string
	RequireMixedSchedulingForAntigravity bool
	RequireOAuth                         bool
}

type GroupAccountMembershipSnapshot struct {
	Group             Group
	CurrentAccounts   []Account
	FinalAccounts     []Account
	AddedAccountIDs   []int64
	RemovedAccountIDs []int64
	AlreadyMemberIDs  []int64
	NotMemberIDs      []int64
}

type GroupAccountMembershipMutation struct {
	AddedAccountIDs         []int64
	RemovedAccountIDs       []int64
	AlreadyMemberAccountIDs []int64
	NotMemberAccountIDs     []int64
	AccountCount            int64
	ActiveAccountCount      int64
	RateLimitedAccountCount int64
}

type GroupAccountMembershipValidator func(GroupAccountMembershipSnapshot) error

// GroupAccountManagementRepository is kept separate from AccountRepository so
// read-only gateway repositories and their test doubles do not inherit admin
// membership-management methods.
type GroupAccountManagementRepository interface {
	ListGroupAccountMembers(ctx context.Context, groupID int64, filters GroupAccountListFilters) (*GroupAccountRepositoryPage, error)
	ListGroupAccountCandidates(ctx context.Context, groupID int64, filters GroupAccountListFilters, policy GroupAccountCandidatePolicy) (*GroupAccountRepositoryPage, error)
	ApplyGroupAccountMembershipDiff(
		ctx context.Context,
		groupID int64,
		addAccountIDs, removeAccountIDs []int64,
		priority int,
		validate GroupAccountMembershipValidator,
	) (*GroupAccountMembershipMutation, error)
}

func (s *adminServiceImpl) ListGroupAccounts(ctx context.Context, groupID int64, filters GroupAccountListFilters) (*GroupAccountListPage, error) {
	group, repo, err := s.groupAccountManagementDependencies(ctx, groupID)
	if err != nil {
		return nil, err
	}
	filters = normalizeGroupAccountListFilters(filters)
	page, err := repo.ListGroupAccountMembers(ctx, groupID, filters)
	if err != nil {
		return nil, err
	}
	return groupAccountListPageFromRepository(group, page, true), nil
}

func (s *adminServiceImpl) ListGroupAccountCandidates(ctx context.Context, groupID int64, filters GroupAccountListFilters) (*GroupAccountListPage, error) {
	group, repo, err := s.groupAccountManagementDependencies(ctx, groupID)
	if err != nil {
		return nil, err
	}
	filters = normalizeGroupAccountListFilters(filters)
	page, err := repo.ListGroupAccountCandidates(ctx, groupID, filters, candidatePolicyForGroup(group))
	if err != nil {
		return nil, err
	}
	return groupAccountListPageFromRepository(group, page, false), nil
}

func (s *adminServiceImpl) ApplyGroupAccountMembershipDiff(ctx context.Context, groupID int64, input GroupAccountMembershipDiffInput) (*GroupAccountMembershipDiffResult, error) {
	addIDs, removeIDs, err := normalizeGroupAccountMembershipDiff(input.AddAccountIDs, input.RemoveAccountIDs)
	if err != nil {
		return nil, err
	}
	_, repo, err := s.groupAccountManagementDependencies(ctx, groupID)
	if err != nil {
		return nil, err
	}

	diffDigest := groupAccountDiffDigest(groupID, addIDs, removeIDs)
	signingKey := s.groupAccountRiskSigningKey()
	mutation, err := repo.ApplyGroupAccountMembershipDiff(
		ctx,
		groupID,
		addIDs,
		removeIDs,
		groupAccountDefaultPriority,
		func(snapshot GroupAccountMembershipSnapshot) error {
			accountsByID := make(map[int64]*Account, len(snapshot.FinalAccounts))
			for i := range snapshot.FinalAccounts {
				account := &snapshot.FinalAccounts[i]
				accountsByID[account.ID] = account
			}
			for _, accountID := range snapshot.AddedAccountIDs {
				account := accountsByID[accountID]
				if account == nil {
					return infraerrors.NotFound("account_not_found", "account not found").WithMetadata(map[string]string{
						"account_id": strconv.FormatInt(accountID, 10),
					})
				}
				if err := accountGroupEligibilityError(&snapshot.Group, account); err != nil {
					return err
				}
			}

			if mixedChannelRiskInFinalSet(snapshot.FinalAccounts, snapshot.AddedAccountIDs) {
				baselineDigest := groupAccountBaselineDigest(snapshot.Group, snapshot.CurrentAccounts)
				if !validateGroupAccountRiskToken(input.RiskConfirmationToken, signingKey, groupID, diffDigest, baselineDigest) {
					token, tokenErr := issueGroupAccountRiskToken(signingKey, groupID, diffDigest, baselineDigest, time.Now().Add(groupAccountRiskTokenTTL))
					if tokenErr != nil {
						return tokenErr
					}
					return infraerrors.Conflict("mixed_channel_warning", "the final group membership contains both Anthropic and Antigravity accounts").WithMetadata(map[string]string{
						"group_id":                strconv.FormatInt(groupID, 10),
						"risk_confirmation_token": token,
					})
				}
			}
			return nil
		},
	)
	if err != nil {
		return nil, err
	}
	return &GroupAccountMembershipDiffResult{
		AddedAccountIDs:         nonNilInt64s(mutation.AddedAccountIDs),
		RemovedAccountIDs:       nonNilInt64s(mutation.RemovedAccountIDs),
		AlreadyMemberAccountIDs: nonNilInt64s(mutation.AlreadyMemberAccountIDs),
		NotMemberAccountIDs:     nonNilInt64s(mutation.NotMemberAccountIDs),
		AccountCount:            mutation.AccountCount,
		ActiveAccountCount:      mutation.ActiveAccountCount,
		RateLimitedAccountCount: mutation.RateLimitedAccountCount,
	}, nil
}

func (s *adminServiceImpl) groupAccountManagementDependencies(ctx context.Context, groupID int64) (*Group, GroupAccountManagementRepository, error) {
	if groupID <= 0 {
		return nil, nil, infraerrors.BadRequest("invalid_group_id", "invalid group ID")
	}
	group, err := s.groupRepo.GetByIDLite(ctx, groupID)
	if err != nil {
		return nil, nil, err
	}
	repo, ok := s.accountRepo.(GroupAccountManagementRepository)
	if !ok {
		return nil, nil, infraerrors.InternalServer("group_account_repository_unavailable", "group account management is not configured")
	}
	return group, repo, nil
}

func normalizeGroupAccountListFilters(filters GroupAccountListFilters) GroupAccountListFilters {
	if filters.Page <= 0 {
		filters.Page = 1
	}
	if filters.PageSize <= 0 {
		filters.PageSize = 20
	}
	if filters.PageSize > 1000 {
		filters.PageSize = 1000
	}
	filters.Search = strings.TrimSpace(filters.Search)
	filters.AccountType = strings.TrimSpace(filters.AccountType)
	filters.Status = strings.TrimSpace(filters.Status)
	filters.Platform = strings.TrimSpace(filters.Platform)
	return filters
}

func groupAccountListPageFromRepository(group *Group, page *GroupAccountRepositoryPage, members bool) *GroupAccountListPage {
	if page == nil {
		page = &GroupAccountRepositoryPage{Page: 1, PageSize: 20, Pages: 1}
	}
	items := make([]GroupAccountSummary, 0, len(page.Items))
	for i := range page.Items {
		record := &page.Items[i]
		warnings := policyWarningsForGroup(group, &record.Account)
		items = append(items, GroupAccountSummary{
			ID:                     record.Account.ID,
			Name:                   record.Account.Name,
			Platform:               record.Account.Platform,
			Type:                   record.Account.Type,
			Status:                 record.Account.Status,
			Schedulable:            record.Account.IsSchedulable(),
			RateLimitedAt:          record.Account.RateLimitedAt,
			RateLimitResetAt:       record.Account.RateLimitResetAt,
			OverloadUntil:          record.Account.OverloadUntil,
			TempUnschedulableUntil: record.Account.TempUnschedulableUntil,
			GroupCount:             record.GroupCount,
			PolicyWarnings:         warnings,
		})
	}
	result := &GroupAccountListPage{
		Items:    items,
		Total:    page.Total,
		Page:     page.Page,
		PageSize: page.PageSize,
		Pages:    page.Pages,
	}
	if members {
		result.MemberTotal = &page.ScopeTotal
	} else {
		result.EligibleTotal = &page.ScopeTotal
	}
	return result
}

func candidatePolicyForGroup(group *Group) GroupAccountCandidatePolicy {
	policy := GroupAccountCandidatePolicy{}
	if group == nil {
		return policy
	}
	switch group.Platform {
	case PlatformComposite:
		policy.AllowedPlatforms = []string{PlatformAnthropic, PlatformOpenAI, PlatformGemini, PlatformAntigravity, PlatformGrok, PlatformKimi, PlatformZhipu, PlatformDeepseek}
	case PlatformAnthropic, PlatformGemini:
		policy.AllowedPlatforms = []string{group.Platform, PlatformAntigravity}
		policy.RequireMixedSchedulingForAntigravity = true
	default:
		policy.AllowedPlatforms = []string{group.Platform}
	}
	policy.RequireOAuth = group.RequireOAuthOnly && groupSupportsOAuthOnlyFilter(group.Platform)
	return policy
}

func accountEligibilityWarning(group *Group, account *Account) string {
	if group == nil || account == nil {
		return GroupAccountWarningPlatformMismatch
	}
	compatible := false
	switch group.Platform {
	case PlatformComposite:
		compatible = isConcreteRequestPlatform(account.Platform)
	case PlatformAnthropic, PlatformGemini:
		compatible = account.Platform == group.Platform || (account.Platform == PlatformAntigravity && account.IsMixedSchedulingEnabled())
	default:
		compatible = account.Platform == group.Platform
	}
	if !compatible {
		return GroupAccountWarningPlatformMismatch
	}
	if group.RequireOAuthOnly && groupSupportsOAuthOnlyFilter(group.Platform) && account.Type == AccountTypeAPIKey {
		return GroupAccountWarningOAuthRequired
	}
	return ""
}

func accountGroupEligibilityError(group *Group, account *Account) error {
	warning := accountEligibilityWarning(group, account)
	if warning == "" {
		return nil
	}
	reason := "account_group_platform_mismatch"
	message := "account platform is not compatible with the group"
	if warning == GroupAccountWarningOAuthRequired {
		reason = "account_group_policy_violation"
		message = "the group only accepts OAuth accounts"
	}
	metadata := map[string]string{"warning": warning}
	if account != nil {
		metadata["account_id"] = strconv.FormatInt(account.ID, 10)
	}
	if group != nil {
		metadata["group_id"] = strconv.FormatInt(group.ID, 10)
	}
	return infraerrors.Conflict(reason, message).WithMetadata(metadata)
}

func policyWarningsForGroup(group *Group, account *Account) []string {
	warnings := make([]string, 0, 2)
	if warning := accountEligibilityWarning(group, account); warning != "" {
		warnings = append(warnings, warning)
	}
	if group != nil && group.RequirePrivacySet && account != nil && !account.IsPrivacySet() {
		warnings = append(warnings, GroupAccountWarningPrivacyNotSet)
	}
	return warnings
}

func normalizeGroupAccountMembershipDiff(addIDs, removeIDs []int64) ([]int64, []int64, error) {
	normalize := func(ids []int64) ([]int64, error) {
		seen := make(map[int64]struct{}, len(ids))
		out := make([]int64, 0, len(ids))
		for _, id := range ids {
			if id <= 0 {
				return nil, infraerrors.BadRequest("invalid_account_membership_diff", "account IDs must be positive")
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out, nil
	}
	add, err := normalize(addIDs)
	if err != nil {
		return nil, nil, err
	}
	remove, err := normalize(removeIDs)
	if err != nil {
		return nil, nil, err
	}
	if len(add)+len(remove) == 0 {
		return nil, nil, infraerrors.BadRequest("invalid_account_membership_diff", "membership diff cannot be empty")
	}
	if len(add)+len(remove) > GroupAccountMembershipDiffLimit {
		return nil, nil, infraerrors.BadRequest("account_membership_diff_too_large", "membership diff exceeds the 500 account limit")
	}
	addSet := make(map[int64]struct{}, len(add))
	for _, id := range add {
		addSet[id] = struct{}{}
	}
	for _, id := range remove {
		if _, exists := addSet[id]; exists {
			return nil, nil, infraerrors.BadRequest("invalid_account_membership_diff", "an account cannot be both added and removed")
		}
	}
	return add, remove, nil
}

func mixedChannelRiskInFinalSet(finalAccounts []Account, actualAddIDs []int64) bool {
	if len(actualAddIDs) == 0 {
		return false
	}
	addedRiskPlatform := false
	addSet := make(map[int64]struct{}, len(actualAddIDs))
	for _, id := range actualAddIDs {
		addSet[id] = struct{}{}
	}
	hasAnthropic := false
	hasAntigravity := false
	for i := range finalAccounts {
		platform := getAccountPlatform(finalAccounts[i].Platform)
		switch platform {
		case "Anthropic":
			hasAnthropic = true
		case "Antigravity":
			hasAntigravity = true
		}
		if _, added := addSet[finalAccounts[i].ID]; added && platform != "" {
			addedRiskPlatform = true
		}
	}
	return addedRiskPlatform && hasAnthropic && hasAntigravity
}

type groupAccountRiskTokenClaims struct {
	Version        int    `json:"v"`
	Purpose        string `json:"purpose"`
	GroupID        int64  `json:"group_id"`
	DiffDigest     string `json:"diff"`
	BaselineDigest string `json:"baseline"`
	ExpiresAt      int64  `json:"exp"`
}

const (
	groupAccountRiskPurposeMembership = "membership"
	groupAccountRiskPurposeCreate     = "create"
)

func (s *adminServiceImpl) groupAccountRiskSigningKey() []byte {
	secret := ""
	if s != nil && s.settingService != nil && s.settingService.cfg != nil {
		secret = strings.TrimSpace(s.settingService.cfg.JWT.Secret)
	}
	if secret == "" {
		// Tests and transitional bootstrap paths can lack config. The confirmation
		// is an authenticated-admin UX gate rather than an authorization boundary;
		// keep the fallback deterministic so tokens still work across instances.
		secret = "sub2api-group-account-risk-confirmation-fallback-v1"
	}
	sum := sha256.Sum256([]byte("sub2api/group-account-risk/v1\x00" + secret))
	return sum[:]
}

func issueGroupAccountRiskToken(signingKey []byte, groupID int64, diffDigest, baselineDigest string, expiresAt time.Time) (string, error) {
	return issueScopedGroupAccountRiskToken(
		signingKey,
		groupAccountRiskPurposeMembership,
		groupID,
		diffDigest,
		baselineDigest,
		expiresAt,
	)
}

func issueGroupAccountCreateRiskToken(signingKey []byte, groupID int64, requestDigest, baselineDigest string, expiresAt time.Time) (string, error) {
	return issueScopedGroupAccountRiskToken(
		signingKey,
		groupAccountRiskPurposeCreate,
		groupID,
		requestDigest,
		baselineDigest,
		expiresAt,
	)
}

func issueScopedGroupAccountRiskToken(signingKey []byte, purpose string, groupID int64, diffDigest, baselineDigest string, expiresAt time.Time) (string, error) {
	claims := groupAccountRiskTokenClaims{
		Version:        1,
		Purpose:        purpose,
		GroupID:        groupID,
		DiffDigest:     diffDigest,
		BaselineDigest: baselineDigest,
		ExpiresAt:      expiresAt.Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, signingKey)
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func validateGroupAccountRiskToken(token string, signingKey []byte, groupID int64, diffDigest, baselineDigest string) bool {
	return validateScopedGroupAccountRiskToken(
		token,
		signingKey,
		groupAccountRiskPurposeMembership,
		groupID,
		diffDigest,
		baselineDigest,
	)
}

func validateGroupAccountCreateRiskToken(token string, signingKey []byte, groupID int64, requestDigest, baselineDigest string) bool {
	return validateScopedGroupAccountRiskToken(
		token,
		signingKey,
		groupAccountRiskPurposeCreate,
		groupID,
		requestDigest,
		baselineDigest,
	)
}

func validateScopedGroupAccountRiskToken(token string, signingKey []byte, purpose string, groupID int64, diffDigest, baselineDigest string) bool {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, signingKey)
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return false
	}
	var claims groupAccountRiskTokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return false
	}
	return claims.Version == 1 &&
		claims.Purpose == purpose &&
		claims.GroupID == groupID &&
		claims.DiffDigest == diffDigest &&
		claims.BaselineDigest == baselineDigest &&
		time.Now().Unix() <= claims.ExpiresAt
}

func groupAccountCreateRequestDigest(requiredGroupID int64, groupIDs []int64, input *CreateAccountInput) string {
	normalizedGroupIDs := append([]int64(nil), groupIDs...)
	sort.Slice(normalizedGroupIDs, func(i, j int) bool { return normalizedGroupIDs[i] < normalizedGroupIDs[j] })
	payload, _ := json.Marshal(struct {
		RequiredGroupID    int64          `json:"required_group_id"`
		GroupIDs           []int64        `json:"group_ids"`
		Name               string         `json:"name"`
		Notes              *string        `json:"notes"`
		Platform           string         `json:"platform"`
		Type               string         `json:"type"`
		Credentials        map[string]any `json:"credentials"`
		Extra              map[string]any `json:"extra"`
		ProxyID            *int64         `json:"proxy_id"`
		Concurrency        int            `json:"concurrency"`
		Priority           int            `json:"priority"`
		RateMultiplier     *float64       `json:"rate_multiplier"`
		LoadFactor         *int           `json:"load_factor"`
		ExpiresAt          *int64         `json:"expires_at"`
		AutoPauseOnExpired *bool          `json:"auto_pause_on_expired"`
		ProbeEnabled       *bool          `json:"upstream_billing_probe_enabled"`
	}{
		RequiredGroupID:    requiredGroupID,
		GroupIDs:           normalizedGroupIDs,
		Name:               input.Name,
		Notes:              input.Notes,
		Platform:           input.Platform,
		Type:               input.Type,
		Credentials:        input.Credentials,
		Extra:              input.Extra,
		ProxyID:            input.ProxyID,
		Concurrency:        input.Concurrency,
		Priority:           input.Priority,
		RateMultiplier:     input.RateMultiplier,
		LoadFactor:         input.LoadFactor,
		ExpiresAt:          input.ExpiresAt,
		AutoPauseOnExpired: input.AutoPauseOnExpired,
		ProbeEnabled:       input.ProbeEnabled,
	})
	sum := sha256.Sum256(append([]byte("sub2api/group-account-create/v1\x00"), payload...))
	return hex.EncodeToString(sum[:])
}

func groupAccountCreateBaselineDigest(snapshot AccountGroupCreateSnapshot) string {
	type groupBaseline struct {
		GroupID int64  `json:"group_id"`
		Digest  string `json:"digest"`
	}
	baselines := make([]groupBaseline, 0, len(snapshot.Groups))
	for i := range snapshot.Groups {
		group := snapshot.Groups[i]
		baselines = append(baselines, groupBaseline{
			GroupID: group.ID,
			Digest:  groupAccountBaselineDigest(group, snapshot.CurrentAccountsByGroup[group.ID]),
		})
	}
	sort.Slice(baselines, func(i, j int) bool { return baselines[i].GroupID < baselines[j].GroupID })
	payload, _ := json.Marshal(baselines)
	sum := sha256.Sum256(append([]byte("sub2api/group-account-create-baseline/v1\x00"), payload...))
	return hex.EncodeToString(sum[:])
}

func groupAccountDiffDigest(groupID int64, addIDs, removeIDs []int64) string {
	payload, _ := json.Marshal(struct {
		GroupID int64   `json:"group_id"`
		Add     []int64 `json:"add"`
		Remove  []int64 `json:"remove"`
	}{GroupID: groupID, Add: addIDs, Remove: removeIDs})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func groupAccountBaselineDigest(group Group, accounts []Account) string {
	type member struct {
		ID        int64  `json:"id"`
		Platform  string `json:"platform"`
		Type      string `json:"type"`
		Mixed     bool   `json:"mixed"`
		UpdatedAt int64  `json:"updated_at"`
	}
	members := make([]member, 0, len(accounts))
	for i := range accounts {
		members = append(members, member{
			ID:        accounts[i].ID,
			Platform:  accounts[i].Platform,
			Type:      accounts[i].Type,
			Mixed:     accounts[i].IsMixedSchedulingEnabled(),
			UpdatedAt: accounts[i].UpdatedAt.UnixNano(),
		})
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	payload, _ := json.Marshal(struct {
		GroupID      int64    `json:"group_id"`
		Platform     string   `json:"platform"`
		RequireOAuth bool     `json:"require_oauth"`
		UpdatedAt    int64    `json:"updated_at"`
		Members      []member `json:"members"`
	}{
		GroupID:      group.ID,
		Platform:     group.Platform,
		RequireOAuth: group.RequireOAuthOnly,
		UpdatedAt:    group.UpdatedAt.UnixNano(),
		Members:      members,
	})
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func nonNilInt64s(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}
