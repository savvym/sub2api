package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/gin-gonic/gin"
)

func (s *OAuthService) AdminGenerateAuthURL(ctx context.Context, actor authz.Actor, proxyID *int64) (*GenerateAuthURLResult, error) {
	authority, err := newPlatformAccountCreationAuthority(actor)
	if err != nil {
		return nil, err
	}
	return s.generateAuthURL(ctx, authority.flowBinding(), proxyID)
}

func (s *OAuthService) AdminGenerateSetupTokenURL(ctx context.Context, actor authz.Actor, proxyID *int64) (*GenerateAuthURLResult, error) {
	authority, err := newPlatformAccountCreationAuthority(actor)
	if err != nil {
		return nil, err
	}
	return s.generateSetupTokenURL(ctx, authority.flowBinding(), proxyID)
}

func (s *OAuthService) AdminExchangeCode(ctx context.Context, actor authz.Actor, input *ExchangeCodeInput) (*TokenInfo, error) {
	authority, err := newPlatformAccountCreationAuthority(actor)
	if err != nil {
		return nil, err
	}
	return s.exchangeCode(ctx, authority.flowBinding(), input)
}

func (s *OAuthService) AdminCookieAuth(ctx context.Context, actor authz.Actor, input *CookieAuthInput) (*TokenInfo, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.CookieAuth(ctx, input)
}

func (s *OAuthService) AdminRefreshAccountToken(ctx context.Context, actor authz.Actor, account *Account) (*TokenInfo, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.RefreshAccountToken(ctx, account)
}

func (s *GeminiOAuthService) AdminRefreshAccountToken(ctx context.Context, actor authz.Actor, account *Account) (*GeminiTokenInfo, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.RefreshAccountToken(ctx, account)
}

func (s *GeminiOAuthService) AdminRefreshAccountGoogleOneTier(ctx context.Context, actor authz.Actor, account *Account) (string, map[string]any, map[string]any, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return "", nil, nil, err
	}
	return s.RefreshAccountGoogleOneTier(ctx, account)
}

func (s *AntigravityOAuthService) AdminRefreshAccountToken(ctx context.Context, actor authz.Actor, account *Account) (*AntigravityTokenInfo, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.RefreshAccountToken(ctx, account)
}

func (s *CRSSyncService) AdminSyncFromCRS(ctx context.Context, actor authz.Actor, input SyncFromCRSInput) (*SyncFromCRSResult, error) {
	authority, err := newPlatformAccountCreationAuthority(actor)
	if err != nil {
		return nil, err
	}
	return s.syncFromCRS(ctx, input, authority)
}

func (s *CRSSyncService) AdminPreviewFromCRS(ctx context.Context, actor authz.Actor, input SyncFromCRSInput) (*PreviewFromCRSResult, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.PreviewFromCRS(ctx, input)
}

func (s *AccountTestService) AdminTestAccountConnection(c *gin.Context, actor authz.Actor, accountID int64, modelID, prompt, mode string, opts ...AccountTestOptions) error {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	return s.TestAccountConnection(c, accountID, modelID, prompt, mode, opts...)
}

func (s *AccountTestService) AdminProbeOpenAIAPIKeyResponsesSupport(ctx context.Context, actor authz.Actor, accountID int64) error {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	s.ProbeOpenAIAPIKeyResponsesSupport(ctx, accountID)
	return nil
}

func (s *AccountTestService) AdminFetchUpstreamSupportedModels(ctx context.Context, actor authz.Actor, account *Account) ([]string, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.FetchUpstreamSupportedModels(ctx, account)
}

func (s *AccountTestService) AdminSyncUpstreamModelCatalog(ctx context.Context, actor authz.Actor, account *Account) (*UpstreamModelCatalog, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.SyncUpstreamModelCatalog(ctx, account)
}

func (s *RateLimitService) AdminBuildOpenAIAccountSchedulerScoreSnapshot(
	ctx context.Context,
	actor authz.Actor,
	accounts []*Account,
	loadMap map[int64]*AccountLoadInfo,
) (map[int64]OpenAIAccountSchedulerScoreSnapshot, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.BuildOpenAIAccountSchedulerScoreSnapshot(ctx, accounts, loadMap), nil
}

func (s *RateLimitService) AdminClearRateLimit(ctx context.Context, actor authz.Actor, accountID int64) error {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	return s.ClearRateLimit(ctx, accountID)
}

func (s *RateLimitService) AdminRecoverAccountState(ctx context.Context, actor authz.Actor, accountID int64, options AccountRecoveryOptions) (*SuccessfulTestRecoveryResult, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.RecoverAccountState(ctx, accountID, options)
}

func (s *RateLimitService) AdminRecoverAccountAfterSuccessfulTest(ctx context.Context, actor authz.Actor, accountID int64) (*SuccessfulTestRecoveryResult, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.RecoverAccountAfterSuccessfulTest(ctx, accountID)
}

func (s *RateLimitService) AdminClearTempUnschedulable(ctx context.Context, actor authz.Actor, accountID int64) error {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	return s.ClearTempUnschedulable(ctx, accountID)
}

func (s *RateLimitService) AdminGetTempUnschedStatus(ctx context.Context, actor authz.Actor, accountID int64) (*TempUnschedState, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GetTempUnschedStatus(ctx, accountID)
}

func (s *AccountUsageService) AdminGetUsage(ctx context.Context, actor authz.Actor, accountID int64, force ...bool) (*UsageInfo, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GetUsage(ctx, accountID, force...)
}

func (s *AccountUsageService) AdminGetUsageBatch(ctx context.Context, actor authz.Actor, accountIDs []int64, force bool) (map[int64]*UsageInfo, map[int64]string, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, nil, err
	}
	return s.GetUsageBatch(ctx, accountIDs, force)
}

func (s *AccountUsageService) AdminGetPassiveUsage(ctx context.Context, actor authz.Actor, accountID int64) (*UsageInfo, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GetPassiveUsage(ctx, accountID)
}

func (s *AccountUsageService) AdminGetTodayStats(ctx context.Context, actor authz.Actor, accountID int64) (*WindowStats, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GetTodayStats(ctx, accountID)
}

func (s *AccountUsageService) AdminGetTodayStatsBatch(ctx context.Context, actor authz.Actor, accountIDs []int64) (map[int64]*WindowStats, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GetTodayStatsBatch(ctx, accountIDs)
}

func (s *AccountUsageService) AdminGetAccountUsageStats(ctx context.Context, actor authz.Actor, accountID int64, startTime, endTime time.Time) (*usagestats.AccountUsageStatsResponse, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GetAccountUsageStats(ctx, accountID, startTime, endTime)
}

func (s *AccountUsageService) AdminGetAccountWindowStats(ctx context.Context, actor authz.Actor, accountID int64, startTime time.Time) (*usagestats.AccountStats, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	return s.GetAccountWindowStats(ctx, accountID, startTime)
}
