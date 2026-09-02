//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/imroc/req/v3"
	"github.com/stretchr/testify/require"
)

func TestAdminService_EnsureOpenAIPrivacy_RetriesNonSuccessModes(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{PrivacyModeFailed, PrivacyModeCFBlocked} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			actor := adminResourceUserTestActor(t)
			ref, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, true)
			key := ResourceMutationKeyFromRef(ref)
			mutationRepo := &resourceMutationRepositoryStub{
				states: map[ResourceMutationKey]ResourceMutationState{
					key: {Key: key, AccessVersion: 3},
				},
			}
			account := &Account{
				ID:            ref.ID(),
				AccessVersion: 3,
				Platform:      PlatformOpenAI,
				Type:          AccountTypeOAuth,
				Credentials: map[string]any{
					"access_token": "token-1",
				},
				Extra: map[string]any{
					"privacy_mode": mode,
				},
			}
			accountRepo := &mockAccountRepoForGemini{
				accountsByID: map[int64]*Account{account.ID: account},
			}
			privacyCalls := 0
			svc := &adminServiceImpl{
				accountRepo: accountRepo,
				privacyClientFactory: func(proxyURL string) (*req.Client, error) {
					privacyCalls++
					return nil, errors.New("factory failed")
				},
				resourceMutations: NewResourceMutationCoordinator(
					mutationRepo,
					resourceMutationResolverStub{actor: actor},
					authz.NewPolicyService(resourceMutationPolicyStoreStub{
						subject:  subjectSnapshot,
						resource: resourceSnapshot,
					}),
				),
			}

			got := svc.EnsureOpenAIPrivacy(context.Background(), actor, account)

			require.Equal(t, PrivacyModeFailed, got)
			require.Equal(t, 1, privacyCalls)
			require.Equal(t, 1, accountRepo.updateExtraCalls)
			require.Equal(t, account.ID, accountRepo.updateExtraID)
			require.Equal(t, map[string]any{"privacy_mode": PrivacyModeFailed}, accountRepo.updateExtraUpdates)
			require.Equal(t, []ResourceMutationKey{key}, mutationRepo.incremented)
			require.Len(t, mutationRepo.events, 1)
			require.Equal(t, "account.extra_updated", mutationRepo.events[0].EventType)
		})
	}
}

func TestAdminService_UpdateAccountExtraFilteredNoopDoesNotVersionOrAudit(t *testing.T) {
	t.Parallel()

	actor := adminResourceUserTestActor(t)
	ref, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, true)
	key := ResourceMutationKeyFromRef(ref)
	mutationRepo := &resourceMutationRepositoryStub{
		states: map[ResourceMutationKey]ResourceMutationState{
			key: {Key: key, AccessVersion: 3},
		},
	}
	account := &Account{ID: ref.ID(), AccessVersion: 3}
	accountRepo := &mockAccountRepoForGemini{
		accountsByID: map[int64]*Account{account.ID: account},
	}
	svc := &adminServiceImpl{
		accountRepo: accountRepo,
		resourceMutations: NewResourceMutationCoordinator(
			mutationRepo,
			resourceMutationResolverStub{actor: actor},
			authz.NewPolicyService(resourceMutationPolicyStoreStub{
				subject:  subjectSnapshot,
				resource: resourceSnapshot,
			}),
		),
	}

	err := svc.UpdateAccountExtra(context.Background(), actor, account.ID, map[string]any{
		codexFingerprintSeedExtraKey:           testCodexFingerprintSeed,
		UpstreamBillingProbeEnabledExtraKey:    true,
		UpstreamBillingRateSyncEnabledExtraKey: true,
		UpstreamBillingProbeExtraKey:           map[string]any{"status": "ok"},
		OllamaCloudUsageSessionExtraKey:        "session",
		OllamaCloudUsageAutoRefreshExtraKey:    true,
		OllamaCloudUsageSnapshotExtraKey:       map[string]any{"status": "ok"},
	})

	require.NoError(t, err)
	require.Zero(t, accountRepo.updateExtraCalls)
	require.Empty(t, mutationRepo.incremented)
	require.Empty(t, mutationRepo.events)
}

func TestAdminService_UpdateAccountExtraSanitizedEmptyIsResourceMutationNoop(t *testing.T) {
	actor := adminResourceUserTestActor(t)
	ref, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, true)
	key := ResourceMutationKeyFromRef(ref)
	mutationRepo := &resourceMutationRepositoryStub{
		states: map[ResourceMutationKey]ResourceMutationState{
			key: {Key: key, AccessVersion: 3},
		},
	}
	account := &Account{ID: ref.ID(), AccessVersion: 3}
	accountRepo := &mockAccountRepoForGemini{
		accountsByID: map[int64]*Account{account.ID: account},
	}
	svc := &adminServiceImpl{
		accountRepo: accountRepo,
		resourceMutations: NewResourceMutationCoordinator(
			mutationRepo,
			resourceMutationResolverStub{actor: actor},
			authz.NewPolicyService(resourceMutationPolicyStoreStub{
				subject:  subjectSnapshot,
				resource: resourceSnapshot,
			}),
		),
	}
	ctx := WithResourceMutationAuditTrace(context.Background(), ResourceMutationAuditTrace{RequestID: "empty-extra-update"})

	err := svc.UpdateAccountExtra(ctx, actor, account.ID, map[string]any{
		UpstreamBillingProbeEnabledExtraKey: true,
		OllamaCloudUsageSnapshotExtraKey:    map[string]any{"secret": "not-persisted"},
	})

	require.NoError(t, err)
	require.Zero(t, accountRepo.updateExtraCalls)
	require.Empty(t, mutationRepo.incremented)
	require.Empty(t, mutationRepo.events)
	require.True(t, mutationRepo.rolledBack)
	require.False(t, ResourceMutationAuditCommitted(ctx))
}

func TestTokenRefreshService_ensureOpenAIPrivacy_RetriesNonSuccessModes(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		TokenRefresh: config.TokenRefreshConfig{
			MaxRetries:          1,
			RetryBackoffSeconds: 0,
		},
	}

	for _, mode := range []string{PrivacyModeFailed, PrivacyModeCFBlocked} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			service := NewTokenRefreshService(&tokenRefreshAccountRepo{}, nil, nil, nil, nil, nil, nil, cfg, nil)
			privacyCalls := 0
			service.SetPrivacyDeps(func(proxyURL string) (*req.Client, error) {
				privacyCalls++
				return nil, errors.New("factory failed")
			}, nil)

			account := &Account{
				ID:       202,
				Platform: PlatformOpenAI,
				Type:     AccountTypeOAuth,
				Credentials: map[string]any{
					"access_token": "token-2",
				},
				Extra: map[string]any{
					"privacy_mode": mode,
				},
			}

			service.ensureOpenAIPrivacy(context.Background(), account)

			require.Equal(t, 1, privacyCalls)
		})
	}
}
