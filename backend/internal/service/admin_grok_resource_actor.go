package service

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/authz"
)

func (s *GrokOAuthService) AdminGenerateAuthURL(ctx context.Context, actor authz.Actor, proxyID *int64, redirectURI string) (*GrokAuthURLResult, error) {
	authority, err := newPlatformAccountCreationAuthority(actor)
	if err != nil {
		return nil, err
	}
	return s.generateAuthURL(ctx, authority.flowBinding(), proxyID, redirectURI)
}

func (s *GrokOAuthService) AdminExchangeCode(ctx context.Context, actor authz.Actor, input *GrokExchangeCodeInput) (*GrokTokenInfo, error) {
	authority, err := newPlatformAccountCreationAuthority(actor)
	if err != nil {
		return nil, err
	}
	return s.exchangeCode(ctx, authority.flowBinding(), input)
}

func (s *GrokOAuthService) AdminRefreshToken(ctx context.Context, actor authz.Actor, refreshToken, proxyURL, clientID string) (*GrokTokenInfo, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.RefreshToken(ctx, refreshToken, proxyURL, clientID)
}

func (s *GrokOAuthService) AdminValidateSSOToken(ctx context.Context, actor authz.Actor, ssoToken string, proxyID *int64) (*GrokTokenInfo, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.ValidateSSOToken(ctx, ssoToken, proxyID)
}

func (s *GrokOAuthService) AdminConvertFromSSO(ctx context.Context, actor authz.Actor, ssoToken string, proxyID *int64) (*GrokTokenInfo, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.ConvertFromSSO(ctx, ssoToken, proxyID)
}

func (s *GrokOAuthService) AdminAuthorizePassword(ctx context.Context, actor authz.Actor, email, password string, proxyID *int64) (*GrokTokenInfo, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.AuthorizePassword(ctx, email, password, proxyID)
}

func (s *GrokOAuthService) AdminRefreshAccountToken(ctx context.Context, actor authz.Actor, account *Account) (*GrokTokenInfo, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.RefreshAccountToken(ctx, account)
}

func (s *GrokQuotaService) AdminQueryQuota(ctx context.Context, actor authz.Actor, accountID int64) (*GrokQuotaProbeResult, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.QueryQuota(ctx, accountID)
}

func (s *GrokQuotaService) AdminResetQuota(ctx context.Context, actor authz.Actor, accountID int64) (*GrokQuotaResetResult, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.ResetQuota(ctx, accountID)
}

func (s *TokenRefreshService) AdminReconcileGrokOAuth(ctx context.Context, actor authz.Actor, input GrokOAuthReconcileInput) (*GrokOAuthReconcileResult, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.ReconcileGrokOAuth(ctx, input)
}
