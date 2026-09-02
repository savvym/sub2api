//go:build unit

package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/geminicli"
	"github.com/Wei-Shaw/sub2api/internal/pkg/oauthflow"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
)

type oauthFlowOpenAIClient struct {
	exchange func(ctx context.Context, code, codeVerifier, redirectURI, proxyURL, clientID string) (*openai.TokenResponse, error)
	calls    atomic.Int32
}

func (c *oauthFlowOpenAIClient) ExchangeCode(
	ctx context.Context,
	code, codeVerifier, redirectURI, proxyURL, clientID string,
) (*openai.TokenResponse, error) {
	c.calls.Add(1)
	if c.exchange != nil {
		return c.exchange(ctx, code, codeVerifier, redirectURI, proxyURL, clientID)
	}
	return &openai.TokenResponse{AccessToken: "access-token", RefreshToken: "refresh-token", ExpiresIn: 3600}, nil
}

func (*oauthFlowOpenAIClient) RefreshToken(context.Context, string, string) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

func (*oauthFlowOpenAIClient) RefreshTokenWithClientID(context.Context, string, string, string) (*openai.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

type oauthFlowGeminiClient struct {
	calls atomic.Int32
}

func (c *oauthFlowGeminiClient) ExchangeCode(
	context.Context,
	string,
	string,
	string,
	string,
	string,
) (*geminicli.TokenResponse, error) {
	c.calls.Add(1)
	return &geminicli.TokenResponse{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		TokenType:    "Bearer",
		ExpiresIn:    3600,
	}, nil
}

func (*oauthFlowGeminiClient) RefreshToken(context.Context, string, string, string) (*geminicli.TokenResponse, error) {
	return nil, errors.New("not implemented")
}

type oauthFlowProxyRepo struct {
	ProxyRepository
	proxy *Proxy
}

func (r *oauthFlowProxyRepo) GetByID(context.Context, int64) (*Proxy, error) {
	return r.proxy, nil
}

func TestAdminOAuthFlowsBindCallbackToInitiatingActor(t *testing.T) {
	initiator := adminResourceTestActor(t, "user", 41)
	otherActor := adminResourceTestActor(t, "user", 42)
	expectedAuthority, err := newPlatformAccountCreationAuthority(initiator)
	require.NoError(t, err)
	expectedBinding := expectedAuthority.flowBinding()

	t.Run("claude", func(t *testing.T) {
		svc := NewOAuthService(nil, nil)
		defer svc.Stop()
		result, err := svc.AdminGenerateAuthURL(context.Background(), initiator, nil)
		require.NoError(t, err)
		session, ok := svc.sessionStore.Get(result.SessionID)
		require.True(t, ok)
		require.True(t, session.Binding.Equal(expectedBinding))

		_, err = svc.AdminExchangeCode(context.Background(), otherActor, &ExchangeCodeInput{SessionID: result.SessionID, Code: "code"})
		require.ErrorContains(t, err, "CLAUDE_OAUTH_ACTOR_MISMATCH")
		require.True(t, svc.sessionStore.TryConsumeSession(result.SessionID), "actor mismatch must not consume the session")
	})

	t.Run("openai", func(t *testing.T) {
		svc := NewOpenAIOAuthService(nil, nil)
		defer svc.Stop()
		result, err := svc.AdminGenerateAuthURL(context.Background(), initiator, nil, "", "openai")
		require.NoError(t, err)
		session, ok := svc.sessionStore.Get(result.SessionID)
		require.True(t, ok)
		require.True(t, session.Binding.Equal(expectedBinding))

		_, err = svc.AdminExchangeCode(context.Background(), otherActor, &OpenAIExchangeCodeInput{
			SessionID: result.SessionID,
			Code:      "code",
			State:     session.State,
		})
		require.ErrorContains(t, err, "OPENAI_OAUTH_ACTOR_MISMATCH")
		require.True(t, svc.sessionStore.TryConsumeSession(result.SessionID), "actor mismatch must not consume the session")
	})

	t.Run("grok", func(t *testing.T) {
		svc := NewGrokOAuthService(nil, nil)
		defer svc.Stop()
		result, err := svc.AdminGenerateAuthURL(context.Background(), initiator, nil, "")
		require.NoError(t, err)
		session, ok := svc.sessionStore.Get(result.SessionID)
		require.True(t, ok)
		require.True(t, session.Binding.Equal(expectedBinding))

		_, err = svc.AdminExchangeCode(context.Background(), otherActor, &GrokExchangeCodeInput{
			SessionID: result.SessionID,
			Code:      "code",
			State:     result.State,
		})
		require.ErrorContains(t, err, "GROK_OAUTH_ACTOR_MISMATCH")
		require.True(t, svc.sessionStore.TryConsumeSession(result.SessionID), "actor mismatch must not consume the session")
	})

	t.Run("gemini", func(t *testing.T) {
		cfg := &config.Config{}
		cfg.Gemini.OAuth.ClientID = "custom-client"
		cfg.Gemini.OAuth.ClientSecret = "custom-secret"
		svc := NewGeminiOAuthService(nil, nil, nil, nil, cfg)
		defer svc.Stop()
		result, err := svc.AdminGenerateAuthURL(context.Background(), initiator, nil, "", "", "ai_studio", GeminiTierAIStudioPaid)
		require.NoError(t, err)
		session, ok := svc.sessionStore.Get(result.SessionID)
		require.True(t, ok)
		require.True(t, session.Binding.Equal(expectedBinding))

		_, err = svc.AdminExchangeCode(context.Background(), otherActor, &GeminiExchangeCodeInput{
			SessionID: result.SessionID,
			Code:      "code",
			State:     result.State,
		})
		require.ErrorContains(t, err, "GEMINI_OAUTH_ACTOR_MISMATCH")
		require.True(t, svc.sessionStore.TryConsumeSession(result.SessionID), "actor mismatch must not consume the session")
	})

	t.Run("antigravity", func(t *testing.T) {
		svc := NewAntigravityOAuthService(nil)
		defer svc.Stop()
		result, err := svc.AdminGenerateAuthURL(context.Background(), initiator, nil)
		require.NoError(t, err)
		session, ok := svc.sessionStore.Get(result.SessionID)
		require.True(t, ok)
		require.True(t, session.Binding.Equal(expectedBinding))

		_, err = svc.AdminExchangeCode(context.Background(), otherActor, &AntigravityExchangeCodeInput{
			SessionID: result.SessionID,
			Code:      "code",
			State:     result.State,
		})
		require.ErrorContains(t, err, "ANTIGRAVITY_OAUTH_ACTOR_MISMATCH")
		require.True(t, svc.sessionStore.TryConsumeSession(result.SessionID), "actor mismatch must not consume the session")
	})
}

func TestOpenAIOAuthCallbackRejectsMissingOrTamperedBindingWithoutConsumption(t *testing.T) {
	actor := adminResourceTestActor(t, "user", 41)
	ownerID := int64(42)
	bindings := map[string]oauthflow.Binding{
		"missing": {},
		"tampered": {
			ActorSubjectKey: "user:41",
			OwnerKind:       oauthflow.OwnerKindUser,
			OwnerUserID:     &ownerID,
		},
	}

	for name, binding := range bindings {
		t.Run(name, func(t *testing.T) {
			client := &oauthFlowOpenAIClient{}
			svc := NewOpenAIOAuthService(nil, client)
			defer svc.Stop()
			svc.sessionStore.Set("session", &openai.OAuthSession{
				State:        "state",
				CodeVerifier: "verifier",
				RedirectURI:  openai.DefaultRedirectURI,
				Binding:      binding,
				CreatedAt:    time.Now(),
			})

			_, err := svc.AdminExchangeCode(context.Background(), actor, &OpenAIExchangeCodeInput{
				SessionID: "session",
				Code:      "code",
				State:     "state",
			})
			require.ErrorContains(t, err, "OPENAI_OAUTH_SESSION_BINDING_INVALID")
			require.Zero(t, client.calls.Load())
			require.True(t, svc.sessionStore.TryConsumeSession("session"), "invalid binding must fail before consumption")
		})
	}
}

func TestOpenAIOAuthCallbackUsesServerFlowStateAndConsumesOnce(t *testing.T) {
	actor := adminResourceTestActor(t, "user", 41)
	proxyID := int64(7)
	proxy := &Proxy{ID: proxyID, Protocol: "http", Host: "proxy.example.com", Port: 8080}
	var gotProxyURL string
	var gotRedirectURI string
	client := &oauthFlowOpenAIClient{
		exchange: func(_ context.Context, _, _, redirectURI, proxyURL, _ string) (*openai.TokenResponse, error) {
			gotProxyURL = proxyURL
			gotRedirectURI = redirectURI
			return &openai.TokenResponse{AccessToken: "access-token", ExpiresIn: 3600}, nil
		},
	}
	svc := NewOpenAIOAuthService(&oauthFlowProxyRepo{proxy: proxy}, client)
	defer svc.Stop()

	const redirectURI = "http://localhost:1455/trusted-callback"
	result, err := svc.AdminGenerateAuthURL(context.Background(), actor, &proxyID, redirectURI, "openai")
	require.NoError(t, err)
	session, ok := svc.sessionStore.Get(result.SessionID)
	require.True(t, ok)

	otherProxyID := int64(8)
	_, err = svc.AdminExchangeCode(context.Background(), actor, &OpenAIExchangeCodeInput{
		SessionID: result.SessionID,
		Code:      "code",
		State:     session.State,
		ProxyID:   &otherProxyID,
	})
	require.ErrorContains(t, err, "OPENAI_OAUTH_PROXY_MISMATCH")
	require.Zero(t, client.calls.Load())

	_, err = svc.AdminExchangeCode(context.Background(), actor, &OpenAIExchangeCodeInput{
		SessionID:   result.SessionID,
		Code:        "code",
		State:       session.State,
		RedirectURI: "http://localhost:1455/forged-callback",
	})
	require.ErrorContains(t, err, "OPENAI_OAUTH_REDIRECT_URI_MISMATCH")
	require.Zero(t, client.calls.Load())

	info, err := svc.AdminExchangeCode(context.Background(), actor, &OpenAIExchangeCodeInput{
		SessionID: result.SessionID,
		Code:      "code",
		State:     session.State,
	})
	require.NoError(t, err)
	require.Equal(t, "access-token", info.AccessToken)
	require.Equal(t, "http://proxy.example.com:8080", gotProxyURL)
	require.Equal(t, redirectURI, gotRedirectURI)
	require.Equal(t, int32(1), client.calls.Load())

	_, err = svc.AdminExchangeCode(context.Background(), actor, &OpenAIExchangeCodeInput{
		SessionID: result.SessionID,
		Code:      "replayed-code",
		State:     session.State,
	})
	require.Error(t, err)
	require.Equal(t, int32(1), client.calls.Load())
}

func TestGeminiOAuthCallbackRejectsTypeAndTierOverridesWithoutConsumption(t *testing.T) {
	actor := adminResourceTestActor(t, "user", 41)
	authority, err := newPlatformAccountCreationAuthority(actor)
	require.NoError(t, err)
	cfg := &config.Config{}
	cfg.Gemini.OAuth.ClientID = "custom-client"
	cfg.Gemini.OAuth.ClientSecret = "custom-secret"
	client := &oauthFlowGeminiClient{}
	svc := NewGeminiOAuthService(nil, client, nil, nil, cfg)
	defer svc.Stop()
	svc.sessionStore.Set("session", &geminicli.OAuthSession{
		State:        "state",
		CodeVerifier: "verifier",
		RedirectURI:  geminicli.AIStudioOAuthRedirectURI,
		TierID:       GeminiTierAIStudioPaid,
		OAuthType:    "ai_studio",
		Binding:      authority.flowBinding(),
		CreatedAt:    time.Now(),
	})

	_, err = svc.AdminExchangeCode(context.Background(), actor, &GeminiExchangeCodeInput{
		SessionID: "session",
		State:     "state",
		Code:      "code",
		OAuthType: "code_assist",
	})
	require.ErrorContains(t, err, "oauth_type does not match")
	require.Zero(t, client.calls.Load())

	_, err = svc.AdminExchangeCode(context.Background(), actor, &GeminiExchangeCodeInput{
		SessionID: "session",
		State:     "state",
		Code:      "code",
		TierID:    GeminiTierAIStudioFree,
	})
	require.ErrorContains(t, err, "tier_id does not match")
	require.Zero(t, client.calls.Load())

	info, err := svc.AdminExchangeCode(context.Background(), actor, &GeminiExchangeCodeInput{
		SessionID: "session",
		State:     "state",
		Code:      "code",
	})
	require.NoError(t, err)
	require.Equal(t, "ai_studio", info.OAuthType)
	require.Equal(t, GeminiTierAIStudioPaid, info.TierID)
	require.Equal(t, int32(1), client.calls.Load())
}

func TestOpenAIOAuthUpstreamFailureCannotBeReplayed(t *testing.T) {
	actor := adminResourceTestActor(t, "user", 41)
	upstreamErr := errors.New("upstream exchange failed")
	client := &oauthFlowOpenAIClient{
		exchange: func(context.Context, string, string, string, string, string) (*openai.TokenResponse, error) {
			return nil, upstreamErr
		},
	}
	svc := NewOpenAIOAuthService(nil, client)
	defer svc.Stop()
	result, err := svc.AdminGenerateAuthURL(context.Background(), actor, nil, "", "openai")
	require.NoError(t, err)
	session, ok := svc.sessionStore.Get(result.SessionID)
	require.True(t, ok)

	input := &OpenAIExchangeCodeInput{SessionID: result.SessionID, State: session.State, Code: "code"}
	_, err = svc.AdminExchangeCode(context.Background(), actor, input)
	require.ErrorIs(t, err, upstreamErr)
	require.Equal(t, int32(1), client.calls.Load())

	_, err = svc.AdminExchangeCode(context.Background(), actor, input)
	require.Error(t, err)
	require.Equal(t, int32(1), client.calls.Load())
}

func TestOpenAIOAuthConcurrentCallbacksReachUpstreamOnce(t *testing.T) {
	actor := adminResourceTestActor(t, "user", 41)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	client := &oauthFlowOpenAIClient{
		exchange: func(context.Context, string, string, string, string, string) (*openai.TokenResponse, error) {
			started <- struct{}{}
			<-release
			return &openai.TokenResponse{AccessToken: "access-token", ExpiresIn: 3600}, nil
		},
	}
	svc := NewOpenAIOAuthService(nil, client)
	defer svc.Stop()
	result, err := svc.AdminGenerateAuthURL(context.Background(), actor, nil, "", "openai")
	require.NoError(t, err)
	session, ok := svc.sessionStore.Get(result.SessionID)
	require.True(t, ok)

	start := make(chan struct{})
	results := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, exchangeErr := svc.AdminExchangeCode(context.Background(), actor, &OpenAIExchangeCodeInput{
				SessionID: result.SessionID,
				State:     session.State,
				Code:      "code",
			})
			results <- exchangeErr
		}()
	}
	close(start)
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("no callback reached the upstream client")
	}

	var firstErr error
	select {
	case firstErr = <-results:
	case <-time.After(2 * time.Second):
		t.Fatal("the losing callback did not fail while the winner was in flight")
	}
	require.Error(t, firstErr)
	close(release)
	secondErr := <-results
	require.NoError(t, secondErr)
	require.Equal(t, int32(1), client.calls.Load())
}
