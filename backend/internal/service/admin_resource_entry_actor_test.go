package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type adminResourceEntrySubscriptionRepo struct {
	UserSubscriptionRepository
	listCalls int
}

func (r *adminResourceEntrySubscriptionRepo) List(_ context.Context, params pagination.PaginationParams, _, _ *int64, _, _, _, _ string) ([]UserSubscription, *pagination.PaginationResult, error) {
	r.listCalls++
	return []UserSubscription{}, &pagination.PaginationResult{Page: params.Page, PageSize: params.PageSize}, nil
}

type adminResourceEntryAccountRepo struct {
	AccountRepository
	account  *Account
	getCalls int
}

func (r *adminResourceEntryAccountRepo) GetByID(_ context.Context, _ int64) (*Account, error) {
	r.getCalls++
	return r.account, nil
}

type adminResourceEntryProxyRepo struct {
	ProxyRepository
	listAccountCalls int
}

func (r *adminResourceEntryProxyRepo) ListAccountSummariesByProxyID(_ context.Context, proxyID int64) ([]ProxyAccountSummary, error) {
	r.listAccountCalls++
	return []ProxyAccountSummary{{ID: proxyID}}, nil
}

func TestAdminResourceEntryFacadesRejectMissingActorBeforeServiceUse(t *testing.T) {
	ctx := context.Background()
	actor := authz.Actor{}
	requireUnavailable := func(err error) {
		t.Helper()
		require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	}

	var gemini *GeminiOAuthService
	_, err := gemini.AdminGenerateAuthURL(ctx, actor, nil, "", "", "code_assist", "")
	requireUnavailable(err)
	_, err = gemini.AdminExchangeCode(ctx, actor, nil)
	requireUnavailable(err)

	var antigravity *AntigravityOAuthService
	_, err = antigravity.AdminGenerateAuthURL(ctx, actor, nil)
	requireUnavailable(err)
	_, err = antigravity.AdminExchangeCode(ctx, actor, nil)
	requireUnavailable(err)
	_, err = antigravity.AdminValidateRefreshToken(ctx, actor, "", nil)
	requireUnavailable(err)

	var quota *CNProviderQuotaService
	_, err = quota.AdminQueryUsage(ctx, actor, 1)
	requireUnavailable(err)
	var balance *CNProviderBalanceService
	_, err = balance.AdminQueryBalance(ctx, actor, 1)
	requireUnavailable(err)

	var subscriptions *SubscriptionService
	_, _, err = subscriptions.AdminList(ctx, actor, 1, 20, nil, nil, "", "", "", "")
	requireUnavailable(err)
	_, err = subscriptions.AdminGetByID(ctx, actor, 1)
	requireUnavailable(err)
	_, err = subscriptions.AdminGetSubscriptionProgress(ctx, actor, 1)
	requireUnavailable(err)
	_, err = subscriptions.AdminAssignSubscription(ctx, actor, nil)
	requireUnavailable(err)
	_, err = subscriptions.AdminBulkAssignSubscription(ctx, actor, nil)
	requireUnavailable(err)
	_, err = subscriptions.AdminExtendSubscription(ctx, actor, 1, 1)
	requireUnavailable(err)
	_, err = subscriptions.AdminResetQuota(ctx, actor, 1, true, false, false)
	requireUnavailable(err)
	requireUnavailable(subscriptions.AdminRevokeSubscription(ctx, actor, 1))
	_, err = subscriptions.AdminRestoreSubscription(ctx, actor, 1)
	requireUnavailable(err)
	_, _, err = subscriptions.AdminListGroupSubscriptions(ctx, actor, 1, 1, 20)
	requireUnavailable(err)
	_, err = subscriptions.AdminListUserSubscriptions(ctx, actor, 1)
	requireUnavailable(err)

	var adminService *adminServiceImpl
	_, _, err = adminService.ListUsers(ctx, actor, 1, 20, UserListFilters{}, "", "")
	requireUnavailable(err)
	_, err = adminService.GetUser(ctx, actor, 1)
	requireUnavailable(err)
	_, err = adminService.GetUserIncludeDeleted(ctx, actor, 1)
	requireUnavailable(err)
	_, err = adminService.CreateUser(ctx, actor, nil)
	requireUnavailable(err)
	_, err = adminService.UpdateUser(ctx, actor, 1, nil)
	requireUnavailable(err)
	requireUnavailable(adminService.DeleteUser(ctx, actor, 1))
	_, _, err = adminService.GetUserAPIKeys(ctx, actor, 1, 1, 20, "", "")
	requireUnavailable(err)
	_, err = adminService.GetUserRPMStatus(ctx, actor, 1)
	requireUnavailable(err)
	_, err = adminService.AdminUpdateAPIKeyGroupID(ctx, actor, 1, nil)
	requireUnavailable(err)
	_, err = adminService.AdminResetAPIKeyRateLimitUsage(ctx, actor, 1)
	requireUnavailable(err)
	_, err = adminService.ReplaceUserGroup(ctx, actor, 1, 2, 3)
	requireUnavailable(err)
	_, err = adminService.AdminGetProxyAccounts(ctx, actor, 1)
	requireUnavailable(err)
}

func TestAdminResourceEntryFacadesAcceptTrustedActorKinds(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		actor authz.Actor
	}{
		{name: "jwt user", actor: adminResourceTestActor(t, authz.SubjectKindUser, 41)},
		{name: "admin api key service principal", actor: adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 73)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := context.Background()

			gemini := NewGeminiOAuthService(nil, nil, nil, nil, &config.Config{})
			_, err := gemini.AdminExchangeCode(ctx, testCase.actor, &GeminiExchangeCodeInput{SessionID: "missing"})
			require.Error(t, err)
			require.NotErrorIs(t, err, ErrAdminResourceActorUnavailable)

			antigravity := NewAntigravityOAuthService(nil)
			result, err := antigravity.AdminGenerateAuthURL(ctx, testCase.actor, nil)
			require.NoError(t, err)
			require.NotNil(t, result)
			_, err = antigravity.AdminExchangeCode(ctx, testCase.actor, &AntigravityExchangeCodeInput{SessionID: "missing"})
			require.Error(t, err)
			require.NotErrorIs(t, err, ErrAdminResourceActorUnavailable)

			quotaRepo := &adminResourceEntryAccountRepo{account: paygAccount(PlatformKimi)}
			quota := NewCNProviderQuotaService(quotaRepo, nil, nil, nil)
			_, err = quota.AdminQueryUsage(ctx, testCase.actor, 9)
			require.Error(t, err)
			require.NotErrorIs(t, err, ErrAdminResourceActorUnavailable)
			require.Equal(t, 1, quotaRepo.getCalls)

			balanceRepo := &adminResourceEntryAccountRepo{account: codingAccount(PlatformKimi)}
			balance := NewCNProviderBalanceService(balanceRepo, nil, nil, nil)
			_, err = balance.AdminQueryBalance(ctx, testCase.actor, 10)
			require.Error(t, err)
			require.NotErrorIs(t, err, ErrAdminResourceActorUnavailable)
			require.Equal(t, 1, balanceRepo.getCalls)

			subscriptionRepo := &adminResourceEntrySubscriptionRepo{}
			subscriptions := NewSubscriptionService(nil, subscriptionRepo, nil, nil, nil)
			listed, resultPage, err := subscriptions.AdminList(ctx, testCase.actor, 2, 25, nil, nil, "", "", "created_at", "desc")
			require.NoError(t, err)
			require.Empty(t, listed)
			require.NotNil(t, resultPage)
			require.Equal(t, 1, subscriptionRepo.listCalls)

			proxyRepo := &adminResourceEntryProxyRepo{}
			adminService := &adminServiceImpl{proxyRepo: proxyRepo}
			accounts, err := adminService.AdminGetProxyAccounts(ctx, testCase.actor, 12)
			require.NoError(t, err)
			require.Len(t, accounts, 1)
			require.Equal(t, 1, proxyRepo.listAccountCalls)
		})
	}
}
