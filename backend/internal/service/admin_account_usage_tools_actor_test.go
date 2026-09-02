package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/stretchr/testify/require"
)

func TestAdminUpstreamBillingProbeRejectsMissingActorBeforeServiceUse(t *testing.T) {
	ctx := context.Background()
	svc := &UpstreamBillingProbeService{}
	actor := authz.Actor{}

	settings, err := svc.AdminGetSettings(ctx, actor)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, settings)
	require.ErrorIs(t, svc.AdminUpdateSettings(ctx, actor, &UpstreamBillingProbeSettings{}), ErrAdminResourceActorUnavailable)
	snapshot, err := svc.AdminProbeAccount(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, snapshot)
	results, err := svc.AdminProbeAccounts(ctx, actor, []int64{1})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, results)
	require.ErrorIs(t, svc.AdminSetAccountEnabled(ctx, actor, 1, true), ErrAdminResourceActorUnavailable)
}

func TestAdminOllamaCloudUsageRejectsMissingActorBeforeServiceUse(t *testing.T) {
	ctx := context.Background()
	svc := &OllamaCloudUsageService{}
	actor := authz.Actor{}

	settings, err := svc.AdminGetSettings(ctx, actor)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, settings)
	require.ErrorIs(t, svc.AdminResolveAccounts(ctx, actor, []*Account{{ID: 1}}), ErrAdminResourceActorUnavailable)
	require.ErrorIs(t, svc.AdminUpdateSettings(ctx, actor, &OllamaCloudUsageSettings{}), ErrAdminResourceActorUnavailable)
	state, err := svc.AdminGetState(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, state)
	state, err = svc.AdminSaveSession(ctx, actor, 1, "session")
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, state)
	state, err = svc.AdminDeleteSession(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, state)
	state, err = svc.AdminSetAutoRefresh(ctx, actor, 1, true)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, state)
	state, err = svc.AdminRefresh(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, state)
}

func TestAdminUsageToolSettingsAcceptTrustedActors(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		actor authz.Actor
	}{
		{name: "user", actor: adminResourceTestActor(t, authz.SubjectKindUser, 41)},
		{name: "service principal", actor: adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 73)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			probeSettings, err := (&UpstreamBillingProbeService{}).AdminGetSettings(context.Background(), testCase.actor)
			require.NoError(t, err)
			require.NotNil(t, probeSettings)

			usageSettings, err := (&OllamaCloudUsageService{}).AdminGetSettings(context.Background(), testCase.actor)
			require.NoError(t, err)
			require.NotNil(t, usageSettings)
		})
	}
}

func TestAdminOpenAIResourceServicesRejectMissingActorBeforeServiceUse(t *testing.T) {
	ctx := context.Background()
	actor := authz.Actor{}

	var oauthService *OpenAIOAuthService
	authURL, err := oauthService.AdminGenerateAuthURL(ctx, actor, nil, "", PlatformOpenAI)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, authURL)
	tokenInfo, err := oauthService.AdminExchangeCode(ctx, actor, &OpenAIExchangeCodeInput{})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, tokenInfo)
	tokenInfo, err = oauthService.AdminRefreshTokenWithClientID(ctx, actor, "token", "", "")
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, tokenInfo)
	tokenInfo, err = oauthService.AdminRefreshAccountToken(ctx, actor, &Account{ID: 1})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, tokenInfo)
	tokenInfo, err = oauthService.AdminValidateCodexPersonalAccessToken(ctx, actor, "token", "")
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, tokenInfo)

	var quotaService *OpenAIQuotaService
	usage, err := quotaService.AdminQueryUsage(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, usage)
	require.ErrorIs(t, quotaService.AdminCacheResetCreditsSnapshot(ctx, actor, 1, nil), ErrAdminResourceActorUnavailable)
	reset, err := quotaService.AdminResetCredit(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, reset)
}

func TestAdminGrokResourceServicesRejectMissingActorBeforeServiceUse(t *testing.T) {
	ctx := context.Background()
	actor := authz.Actor{}

	var oauthService *GrokOAuthService
	authURL, err := oauthService.AdminGenerateAuthURL(ctx, actor, nil, "")
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, authURL)
	tokenInfo, err := oauthService.AdminExchangeCode(ctx, actor, &GrokExchangeCodeInput{})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, tokenInfo)
	tokenInfo, err = oauthService.AdminRefreshToken(ctx, actor, "token", "", "")
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, tokenInfo)
	tokenInfo, err = oauthService.AdminValidateSSOToken(ctx, actor, "token", nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, tokenInfo)
	tokenInfo, err = oauthService.AdminConvertFromSSO(ctx, actor, "token", nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, tokenInfo)
	tokenInfo, err = oauthService.AdminAuthorizePassword(ctx, actor, "email", "password", nil)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, tokenInfo)
	tokenInfo, err = oauthService.AdminRefreshAccountToken(ctx, actor, &Account{ID: 1})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, tokenInfo)

	var quotaService *GrokQuotaService
	quota, err := quotaService.AdminQueryQuota(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, quota)
	reset, err := quotaService.AdminResetQuota(ctx, actor, 1)
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, reset)

	var reconciler *TokenRefreshService
	result, err := reconciler.AdminReconcileGrokOAuth(ctx, actor, GrokOAuthReconcileInput{})
	require.ErrorIs(t, err, ErrAdminResourceActorUnavailable)
	require.Nil(t, result)
}
