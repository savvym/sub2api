package service

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var errOpenAIAutoResetUpstreamIdentityChanged = errors.New("OpenAI quota auto-reset upstream identity changed before the external reset")

type openAIQuotaRequestAuthIdentity struct {
	fingerprint [sha256.Size]byte
	taskID      string
}

type openAIQuotaAutoResetUpstreamIdentity struct {
	credentialAccountID   int64
	chatGPTAccountID      string
	fedRAMP               bool
	proxyConfigured       bool
	proxyID               int64
	proxyURLFingerprint   [sha256.Size]byte
	credentialFingerprint [sha256.Size]byte
	auth                  openAIQuotaRequestAuthIdentity
}

type openAIQuotaAutoResetRequestIdentityContextKey struct{}

type openAIQuotaAutoResetRequestIdentity struct {
	identity        openAIQuotaAutoResetUpstreamIdentity
	allowTaskChange bool
}

func withOpenAIQuotaAutoResetRequestIdentity(
	ctx context.Context,
	identity openAIQuotaAutoResetUpstreamIdentity,
	allowTaskChange bool,
) context.Context {
	return context.WithValue(ctx, openAIQuotaAutoResetRequestIdentityContextKey{}, openAIQuotaAutoResetRequestIdentity{
		identity:        identity,
		allowTaskChange: allowTaskChange,
	})
}

func openAIQuotaAutoResetRequestIdentityFromContext(ctx context.Context) (openAIQuotaAutoResetRequestIdentity, bool) {
	if ctx == nil {
		return openAIQuotaAutoResetRequestIdentity{}, false
	}
	identity, ok := ctx.Value(openAIQuotaAutoResetRequestIdentityContextKey{}).(openAIQuotaAutoResetRequestIdentity)
	return identity, ok
}

func openAIQuotaBearerAuthIdentity(accessToken string) openAIQuotaRequestAuthIdentity {
	return openAIQuotaRequestAuthIdentity{fingerprint: sha256.Sum256([]byte("bearer\x00" + strings.TrimSpace(accessToken)))}
}

func openAIQuotaAgentAuthIdentity(key agentIdentityKey) openAIQuotaRequestAuthIdentity {
	publicKey := key.privateKey.Public().(ed25519.PublicKey)
	material := append([]byte("agent\x00"+key.runtimeID+"\x00"), publicKey...)
	return openAIQuotaRequestAuthIdentity{
		fingerprint: sha256.Sum256(material),
		taskID:      strings.TrimSpace(key.taskID),
	}
}

func openAIQuotaAuthIdentityFromAccount(account *Account) (openAIQuotaRequestAuthIdentity, error) {
	if account == nil {
		return openAIQuotaRequestAuthIdentity{}, errors.New("account is nil")
	}
	if account.IsOpenAIAgentIdentity() {
		key, err := agentIdentityKeyFromAccount(account)
		if err != nil {
			return openAIQuotaRequestAuthIdentity{}, err
		}
		return openAIQuotaAgentAuthIdentity(key), nil
	}
	accessToken := strings.TrimSpace(account.GetOpenAIAccessToken())
	if accessToken == "" {
		return openAIQuotaRequestAuthIdentity{}, errors.New("stored OpenAI access token is empty")
	}
	return openAIQuotaBearerAuthIdentity(accessToken), nil
}

func openAIQuotaCredentialFingerprint(account *Account) ([sha256.Size]byte, error) {
	if account == nil {
		return [sha256.Size]byte{}, errors.New("account is nil")
	}
	keys := []string{
		openAIAuthModeCredentialKey,
		openAIAuthModeLegacyCredentialKey,
		"access_token",
		"refresh_token",
		"chatgpt_account_id",
		"organization_id",
		"chatgpt_account_is_fedramp",
		"agent_runtime_id",
		"agent_private_key",
	}
	values := make(map[string]any, len(keys))
	for _, key := range keys {
		if value, ok := account.Credentials[key]; ok {
			values[key] = value
		}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return [sha256.Size]byte{}, fmt.Errorf("encode OpenAI quota credential identity: %w", err)
	}
	return sha256.Sum256(encoded), nil
}

func openAIQuotaChatGPTAccountID(account *Account) string {
	if account == nil {
		return ""
	}
	accountID := strings.TrimSpace(account.GetCredential("chatgpt_account_id"))
	if accountID == "" {
		accountID = strings.TrimSpace(account.GetCredential("organization_id"))
	}
	return accountID
}

func openAIQuotaAutoResetIdentityFromAccount(
	account *Account,
	auth openAIQuotaRequestAuthIdentity,
) (openAIQuotaAutoResetUpstreamIdentity, error) {
	if account == nil {
		return openAIQuotaAutoResetUpstreamIdentity{}, errors.New("account is nil")
	}
	credentialFingerprint, err := openAIQuotaCredentialFingerprint(account)
	if err != nil {
		return openAIQuotaAutoResetUpstreamIdentity{}, err
	}
	identity := openAIQuotaAutoResetUpstreamIdentity{
		credentialAccountID:   account.ID,
		chatGPTAccountID:      openAIQuotaChatGPTAccountID(account),
		fedRAMP:               account.IsChatGPTAccountFedRAMP(),
		credentialFingerprint: credentialFingerprint,
		auth:                  auth,
	}
	if account.ProxyID != nil {
		if account.Proxy == nil || account.Proxy.ID != *account.ProxyID {
			return openAIQuotaAutoResetUpstreamIdentity{}, errors.New("configured proxy is unavailable")
		}
		identity.proxyConfigured = true
		identity.proxyID = *account.ProxyID
		identity.proxyURLFingerprint = sha256.Sum256([]byte(account.Proxy.URL()))
	}
	return identity, nil
}

func (s *OpenAIQuotaService) captureOpenAIQuotaAutoResetIdentity(
	ctx context.Context,
	accountID int64,
	accessToken string,
	chatGPTAccountID string,
	proxyURL string,
	fedRAMP bool,
	auth openAIQuotaRequestAuthIdentity,
) (openAIQuotaAutoResetUpstreamIdentity, error) {
	if s == nil || s.accountRepo == nil {
		return openAIQuotaAutoResetUpstreamIdentity{}, errors.New("account repository is unavailable")
	}
	account, err := s.accountRepo.GetByID(ctx, accountID)
	if err != nil || account == nil {
		return openAIQuotaAutoResetUpstreamIdentity{}, errors.New("account is unavailable")
	}
	if account.IsShadow() {
		account, err = resolveCredentialAccount(ctx, s.accountRepo, account)
		if err != nil || account == nil {
			return openAIQuotaAutoResetUpstreamIdentity{}, errors.New("credential account is unavailable")
		}
	}
	currentAuth, err := openAIQuotaAuthIdentityFromAccount(account)
	if err != nil {
		return openAIQuotaAutoResetUpstreamIdentity{}, err
	}
	if currentAuth != auth {
		return openAIQuotaAutoResetUpstreamIdentity{}, errOpenAIAutoResetUpstreamIdentityChanged
	}
	identity, err := openAIQuotaAutoResetIdentityFromAccount(account, auth)
	if err != nil {
		return openAIQuotaAutoResetUpstreamIdentity{}, err
	}
	if identity.chatGPTAccountID != strings.TrimSpace(chatGPTAccountID) || identity.fedRAMP != fedRAMP {
		return openAIQuotaAutoResetUpstreamIdentity{}, errOpenAIAutoResetUpstreamIdentityChanged
	}
	requestProxyFingerprint := sha256.Sum256([]byte(strings.TrimSpace(proxyURL)))
	if identity.proxyConfigured != (strings.TrimSpace(proxyURL) != "") ||
		(identity.proxyConfigured && identity.proxyURLFingerprint != requestProxyFingerprint) {
		return openAIQuotaAutoResetUpstreamIdentity{}, errOpenAIAutoResetUpstreamIdentityChanged
	}
	if !account.IsOpenAIAgentIdentity() && openAIQuotaBearerAuthIdentity(accessToken) != auth {
		return openAIQuotaAutoResetUpstreamIdentity{}, errOpenAIAutoResetUpstreamIdentityChanged
	}
	return identity, nil
}

func openAIQuotaAutoResetIdentitiesMatch(
	expected openAIQuotaAutoResetUpstreamIdentity,
	actual openAIQuotaAutoResetUpstreamIdentity,
	allowTaskChange bool,
) bool {
	expectedTaskID := expected.auth.taskID
	actualTaskID := actual.auth.taskID
	expected.auth.taskID = ""
	actual.auth.taskID = ""
	if expected != actual {
		return false
	}
	return allowTaskChange || expectedTaskID == actualTaskID
}
