package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/stretchr/testify/require"
)

func TestAdminAccountSupportServicesRejectMissingActorBeforeServiceUse(t *testing.T) {
	ctx := context.Background()
	actor := authz.Actor{}

	var oauthService *OAuthService
	result, err := oauthService.AdminGenerateAuthURL(ctx, actor, nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, result)
	result, err = oauthService.AdminGenerateSetupTokenURL(ctx, actor, nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, result)
	token, err := oauthService.AdminExchangeCode(ctx, actor, nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, token)
	token, err = oauthService.AdminCookieAuth(ctx, actor, nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, token)
	token, err = oauthService.AdminRefreshAccountToken(ctx, actor, nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, token)

	var geminiOAuthService *GeminiOAuthService
	geminiToken, err := geminiOAuthService.AdminRefreshAccountToken(ctx, actor, nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, geminiToken)
	tierID, tierExtra, tierCredentials, err := geminiOAuthService.AdminRefreshAccountGoogleOneTier(ctx, actor, nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Empty(t, tierID)
	require.Nil(t, tierExtra)
	require.Nil(t, tierCredentials)

	var antigravityOAuthService *AntigravityOAuthService
	antigravityToken, err := antigravityOAuthService.AdminRefreshAccountToken(ctx, actor, nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, antigravityToken)

	var crsService *CRSSyncService
	syncResult, err := crsService.AdminSyncFromCRS(ctx, actor, SyncFromCRSInput{})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, syncResult)
	previewResult, err := crsService.AdminPreviewFromCRS(ctx, actor, SyncFromCRSInput{})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, previewResult)

	var testService *AccountTestService
	require.ErrorIs(t, testService.AdminTestAccountConnection(nil, actor, 1, "", "", ""), ErrAdminResourceActorUnavailable)
	require.ErrorIs(t, testService.AdminProbeOpenAIAPIKeyResponsesSupport(ctx, actor, 1), ErrAdminResourceActorUnavailable)
	models, err := testService.AdminFetchUpstreamSupportedModels(ctx, actor, nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, models)

	var rateLimitService *RateLimitService
	scores, err := rateLimitService.AdminBuildOpenAIAccountSchedulerScoreSnapshot(ctx, actor, nil, nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, scores)
	require.ErrorIs(t, rateLimitService.AdminClearRateLimit(ctx, actor, 1), ErrAdminResourceActorUnavailable)
	recovery, err := rateLimitService.AdminRecoverAccountState(ctx, actor, 1, AccountRecoveryOptions{})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, recovery)
	recovery, err = rateLimitService.AdminRecoverAccountAfterSuccessfulTest(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, recovery)
	require.ErrorIs(t, rateLimitService.AdminClearTempUnschedulable(ctx, actor, 1), ErrAdminResourceActorUnavailable)
	tempState, err := rateLimitService.AdminGetTempUnschedStatus(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, tempState)

	var usageService *AccountUsageService
	usage, err := usageService.AdminGetUsage(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, usage)
	usageByAccount, errorsByAccount, err := usageService.AdminGetUsageBatch(ctx, actor, []int64{1}, false)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, usageByAccount)
	require.Nil(t, errorsByAccount)
	usage, err = usageService.AdminGetPassiveUsage(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, usage)
	today, err := usageService.AdminGetTodayStats(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, today)
	todayByAccount, err := usageService.AdminGetTodayStatsBatch(ctx, actor, []int64{1})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, todayByAccount)
	accountUsage, err := usageService.AdminGetAccountUsageStats(ctx, actor, 1, time.Time{}, time.Time{})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, accountUsage)
	windowStats, err := usageService.AdminGetAccountWindowStats(ctx, actor, 1, time.Time{})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, windowStats)
}
