package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

func (s *GeminiOAuthService) AdminGenerateAuthURL(ctx context.Context, actor authz.Actor, proxyID *int64, redirectURI, projectID, oauthType, tierID string) (*GeminiAuthURLResult, error) {
	authority, err := newPlatformAccountCreationAuthority(actor)
	if err != nil {
		return nil, err
	}
	return s.generateAuthURL(ctx, authority.flowBinding(), proxyID, redirectURI, projectID, oauthType, tierID)
}

func (s *GeminiOAuthService) AdminExchangeCode(ctx context.Context, actor authz.Actor, input *GeminiExchangeCodeInput) (*GeminiTokenInfo, error) {
	authority, err := newPlatformAccountCreationAuthority(actor)
	if err != nil {
		return nil, err
	}
	return s.exchangeCode(ctx, authority.flowBinding(), input)
}

func (s *AntigravityOAuthService) AdminGenerateAuthURL(ctx context.Context, actor authz.Actor, proxyID *int64) (*AntigravityAuthURLResult, error) {
	authority, err := newPlatformAccountCreationAuthority(actor)
	if err != nil {
		return nil, err
	}
	return s.generateAuthURL(ctx, authority.flowBinding(), proxyID)
}

func (s *AntigravityOAuthService) AdminExchangeCode(ctx context.Context, actor authz.Actor, input *AntigravityExchangeCodeInput) (*AntigravityTokenInfo, error) {
	authority, err := newPlatformAccountCreationAuthority(actor)
	if err != nil {
		return nil, err
	}
	return s.exchangeCode(ctx, authority.flowBinding(), input)
}

func (s *AntigravityOAuthService) AdminValidateRefreshToken(ctx context.Context, actor authz.Actor, refreshToken string, proxyID *int64) (*AntigravityTokenInfo, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.ValidateRefreshToken(ctx, refreshToken, proxyID)
}

func (s *CNProviderQuotaService) AdminQueryUsage(ctx context.Context, actor authz.Actor, accountID int64) (*CNProviderQuotaProbeResult, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.QueryUsage(ctx, accountID)
}

func (s *CNProviderBalanceService) AdminQueryBalance(ctx context.Context, actor authz.Actor, accountID int64) (*CNProviderBalanceResult, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.QueryBalance(ctx, accountID)
}

func (s *SubscriptionService) AdminList(ctx context.Context, actor authz.Actor, page, pageSize int, userID, groupID *int64, status, platform, sortBy, sortOrder string) ([]UserSubscription, *pagination.PaginationResult, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, nil, err
	}
	return s.List(ctx, page, pageSize, userID, groupID, status, platform, sortBy, sortOrder)
}

func (s *SubscriptionService) AdminGetByID(ctx context.Context, actor authz.Actor, id int64) (*UserSubscription, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GetByID(ctx, id)
}

func (s *SubscriptionService) AdminGetSubscriptionProgress(ctx context.Context, actor authz.Actor, subscriptionID int64) (*SubscriptionProgress, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GetSubscriptionProgress(ctx, subscriptionID)
}

func (s *SubscriptionService) AdminAssignSubscription(ctx context.Context, actor authz.Actor, input *AssignSubscriptionInput) (*UserSubscription, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.AssignSubscription(ctx, input)
}

func (s *SubscriptionService) AdminBulkAssignSubscription(ctx context.Context, actor authz.Actor, input *BulkAssignSubscriptionInput) (*BulkAssignResult, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.BulkAssignSubscription(ctx, input)
}

func (s *SubscriptionService) AdminExtendSubscription(ctx context.Context, actor authz.Actor, subscriptionID int64, days int) (*UserSubscription, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.ExtendSubscription(ctx, subscriptionID, days)
}

func (s *SubscriptionService) AdminResetQuota(ctx context.Context, actor authz.Actor, subscriptionID int64, resetDaily, resetWeekly, resetMonthly bool) (*UserSubscription, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.ResetQuota(ctx, subscriptionID, resetDaily, resetWeekly, resetMonthly)
}

func (s *SubscriptionService) AdminRevokeSubscription(ctx context.Context, actor authz.Actor, subscriptionID int64) error {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	return s.RevokeSubscription(ctx, subscriptionID)
}

func (s *SubscriptionService) AdminRestoreSubscription(ctx context.Context, actor authz.Actor, subscriptionID int64) (*UserSubscription, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.RestoreSubscription(ctx, subscriptionID)
}

func (s *SubscriptionService) AdminListUserSubscriptions(ctx context.Context, actor authz.Actor, userID int64) ([]UserSubscription, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.ListUserSubscriptions(ctx, userID)
}

func (s *adminServiceImpl) AdminGetProxyAccounts(ctx context.Context, actor authz.Actor, proxyID int64) ([]ProxyAccountSummary, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GetProxyAccounts(ctx, proxyID)
}
