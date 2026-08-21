package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/authz"
)

func (s *OpenAIOAuthService) AdminGenerateAuthURL(ctx context.Context, actor authz.Actor, proxyID *int64, redirectURI, platform string) (*OpenAIAuthURLResult, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GenerateAuthURL(ctx, proxyID, redirectURI, platform)
}

func (s *OpenAIOAuthService) AdminExchangeCode(ctx context.Context, actor authz.Actor, input *OpenAIExchangeCodeInput) (*OpenAITokenInfo, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.ExchangeCode(ctx, input)
}

func (s *OpenAIOAuthService) AdminRefreshTokenWithClientID(ctx context.Context, actor authz.Actor, refreshToken, proxyURL, clientID string) (*OpenAITokenInfo, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.RefreshTokenWithClientID(ctx, refreshToken, proxyURL, clientID)
}

func (s *OpenAIOAuthService) AdminRefreshAccountToken(ctx context.Context, actor authz.Actor, account *Account) (*OpenAITokenInfo, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.RefreshAccountToken(ctx, account)
}

func (s *OpenAIOAuthService) AdminValidateCodexPersonalAccessToken(ctx context.Context, actor authz.Actor, accessToken, proxyURL string) (*OpenAITokenInfo, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.ValidateCodexPersonalAccessToken(ctx, accessToken, proxyURL)
}

func (s *OpenAIQuotaService) AdminQueryUsage(ctx context.Context, actor authz.Actor, accountID int64) (*OpenAIQuotaUsage, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.QueryUsage(ctx, accountID)
}

func (s *OpenAIQuotaService) AdminCacheResetCreditsSnapshot(ctx context.Context, actor authz.Actor, accountID int64, credits *OpenAIRateLimitResetCredits) error {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	return s.CacheResetCreditsSnapshot(ctx, accountID, credits)
}

func (s *OpenAIQuotaService) AdminResetCredit(ctx context.Context, actor authz.Actor, accountID int64) (*OpenAIQuotaResetResult, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.ResetCredit(ctx, accountID)
}
