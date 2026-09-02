//go:build unit

package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type shadowSetTOCTOUAccountRepo struct {
	*sparkShadowRepoStub
	mutationRepo         *resourceMutationRepositoryStub
	parentID             int64
	authorizedShadowIDs  []int64
	transactionShadowIDs []int64
	listShadowCalls      int
	updateCalls          int
	bulkUpdateCalls      int
	deleteCalls          int
	revertCalls          int
}

func TestCreateShadowVersionsAndAuditsParentRelationship(t *testing.T) {
	ctx := context.Background()
	repo := newSparkShadowRepoStub()
	parent := &Account{
		Name:          "parent",
		Platform:      PlatformOpenAI,
		Type:          AccountTypeOAuth,
		Status:        StatusActive,
		Credentials:   map[string]any{"access_token": "parent-token"},
		AccessVersion: 3,
	}
	require.NoError(t, repo.Create(ctx, parent))

	actor := adminResourceUserTestActor(t)
	_, subjectSnapshot, _ := resourceMutationPolicyFixtures(t, actor, true)
	parentRef, err := authz.NewResourceRef(authz.ResourceTypeAccount, parent.ID)
	require.NoError(t, err)
	resourceSnapshot, err := authz.NewResourceAccessSnapshot(authz.ResourceAccessSnapshotInput{
		Subject:       subjectSnapshot,
		Resource:      parentRef,
		Exists:        true,
		AccessVersion: parent.AccessVersion,
	})
	require.NoError(t, err)
	parentKey := ResourceMutationKeyFromRef(parentRef)
	mutationRepo := &resourceMutationRepositoryStub{states: map[ResourceMutationKey]ResourceMutationState{
		parentKey: {Key: parentKey, AccessVersion: parent.AccessVersion},
	}}
	coordinator := NewResourceMutationCoordinator(
		mutationRepo,
		resourceMutationResolverStub{actor: actor},
		authz.NewPolicyService(resourceMutationPolicyStoreStub{
			subject:  subjectSnapshot,
			resource: resourceSnapshot,
		}),
	)
	service := &adminServiceImpl{
		accountRepo:       repo,
		groupRepo:         &sparkShadowGroupRepoStub{},
		resourceMutations: coordinator,
	}

	shadow, err := service.CreateShadow(ctx, actor, parent.ID, ShadowOptions{Name: "shadow"})

	require.NoError(t, err)
	require.NotNil(t, shadow)
	require.Equal(t, []ResourceMutationKey{parentKey}, mutationRepo.incremented)
	require.Len(t, mutationRepo.events, 2)
	require.Equal(t, "account.shadow_links_changed", mutationRepo.events[0].EventType)
	require.Equal(t, []string{"shadow_accounts"}, mutationRepo.events[0].ChangedFields)
	require.EqualValues(t, 4, mutationRepo.events[0].ResourceAccessVersion)
	require.Equal(t, "account.shadow_created", mutationRepo.events[1].EventType)
	require.Equal(t, shadow.ID, mutationRepo.events[1].Key.ResourceID)
}

func (r *shadowSetTOCTOUAccountRepo) ListShadowsByParent(ctx context.Context, parentID int64) ([]*Account, error) {
	if parentID != r.parentID {
		return r.sparkShadowRepoStub.ListShadowsByParent(ctx, parentID)
	}
	r.listShadowCalls++
	ids := r.authorizedShadowIDs
	if r.mutationRepo != nil && r.mutationRepo.inTx {
		ids = r.transactionShadowIDs
	}
	result := make([]*Account, 0, len(ids))
	for _, id := range ids {
		account := r.accounts[id]
		if account == nil {
			continue
		}
		copy := *account
		result = append(result, &copy)
	}
	return result, nil
}

func (r *shadowSetTOCTOUAccountRepo) Update(ctx context.Context, account *Account) error {
	r.updateCalls++
	return r.sparkShadowRepoStub.Update(ctx, account)
}

func (r *shadowSetTOCTOUAccountRepo) BulkUpdate(context.Context, []int64, AccountBulkUpdate) (int64, error) {
	r.bulkUpdateCalls++
	return 0, nil
}

func (r *shadowSetTOCTOUAccountRepo) Delete(ctx context.Context, id int64) error {
	r.deleteCalls++
	return r.sparkShadowRepoStub.Delete(ctx, id)
}

func (r *shadowSetTOCTOUAccountRepo) RevertProxyFallback(context.Context, int64) error {
	r.revertCalls++
	return nil
}

func TestAccountShadowSetChangeInsideResourceTransactionRejectsBeforeWrites(t *testing.T) {
	tests := []struct {
		name string
		run  func(context.Context, *adminServiceImpl, authz.Actor, int64) error
	}{
		{
			name: "update proxy",
			run: func(ctx context.Context, service *adminServiceImpl, actor authz.Actor, parentID int64) error {
				proxyID := int64(41)
				_, err := service.UpdateAccount(ctx, actor, parentID, &UpdateAccountInput{ProxyID: &proxyID})
				return err
			},
		},
		{
			name: "bulk update proxy",
			run: func(ctx context.Context, service *adminServiceImpl, actor authz.Actor, parentID int64) error {
				proxyID := int64(42)
				_, err := service.BulkUpdateAccounts(ctx, actor, &BulkUpdateAccountsInput{
					AccountIDs: []int64{parentID},
					ProxyID:    &proxyID,
				})
				return err
			},
		},
		{
			name: "delete",
			run: func(ctx context.Context, service *adminServiceImpl, actor authz.Actor, parentID int64) error {
				return service.DeleteAccount(ctx, actor, parentID)
			},
		},
		{
			name: "batch delete",
			run: func(ctx context.Context, service *adminServiceImpl, actor authz.Actor, parentID int64) error {
				return service.BatchDeleteAccounts(ctx, actor, []int64{parentID})
			},
		},
		{
			name: "revert proxy fallback",
			run: func(ctx context.Context, service *adminServiceImpl, actor authz.Actor, parentID int64) error {
				return service.RevertAccountProxyFallback(ctx, actor, parentID)
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			service, repo, mutationRepo, actor, parentID := newAccountShadowSetTOCTOUFixture(t)
			before := make(map[int64]Account, len(repo.accounts))
			for id, account := range repo.accounts {
				before[id] = *account
			}

			err := testCase.run(context.Background(), service, actor, parentID)

			require.ErrorIs(t, err, ErrResourceMutationConflict)
			require.Equal(t, http.StatusConflict, infraerrors.Code(err))
			require.Equal(t, "RESOURCE_AUTHORIZATION_CHANGED", infraerrors.Reason(err))
			require.True(t, mutationRepo.rolledBack)
			require.Empty(t, mutationRepo.incremented)
			require.Empty(t, mutationRepo.events)
			require.Equal(t, 2, repo.listShadowCalls, "must enumerate once before and once inside the transaction")
			require.Zero(t, repo.updateCalls)
			require.Zero(t, repo.bulkUpdateCalls)
			require.Zero(t, repo.deleteCalls)
			require.Zero(t, repo.revertCalls)
			require.Len(t, repo.accounts, len(before))
			for id, expected := range before {
				require.Equal(t, expected, *repo.accounts[id], "account %d changed before shadow-set validation", id)
			}
		})
	}
}

func newAccountShadowSetTOCTOUFixture(
	t testing.TB,
) (*adminServiceImpl, *shadowSetTOCTOUAccountRepo, *resourceMutationRepositoryStub, authz.Actor, int64) {
	t.Helper()
	ctx := context.Background()
	baseRepo := newSparkShadowRepoStub()
	oldProxyID := int64(7)
	originProxyID := int64(3)
	parent := &Account{
		Name:                  "parent",
		Platform:              PlatformOpenAI,
		Type:                  AccountTypeOAuth,
		Status:                StatusActive,
		ProxyID:               &oldProxyID,
		ProxyFallbackOriginID: &originProxyID,
		Credentials:           map[string]any{"access_token": "parent-token"},
		AccessVersion:         3,
	}
	require.NoError(t, baseRepo.Create(ctx, parent))
	parentID := parent.ID
	lockedShadow := &Account{
		Name:            "locked-shadow",
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		Status:          StatusActive,
		QuotaDimension:  QuotaDimensionSpark,
		ParentAccountID: &parentID,
		ProxyID:         &oldProxyID,
		AccessVersion:   5,
	}
	require.NoError(t, baseRepo.Create(ctx, lockedShadow))
	newShadow := &Account{
		Name:            "concurrent-shadow",
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		Status:          StatusActive,
		QuotaDimension:  QuotaDimensionSpark,
		ParentAccountID: &parentID,
		ProxyID:         &oldProxyID,
		AccessVersion:   1,
	}
	require.NoError(t, baseRepo.Create(ctx, newShadow))

	actor := adminResourceUserTestActor(t)
	_, subjectSnapshot, _ := resourceMutationPolicyFixtures(t, actor, true)
	states := make(map[ResourceMutationKey]ResourceMutationState)
	resources := make(map[authz.ResourceRef]authz.ResourceAccessSnapshot)
	for _, account := range []*Account{parent, lockedShadow} {
		ref, err := authz.NewResourceRef(authz.ResourceTypeAccount, account.ID)
		require.NoError(t, err)
		key := ResourceMutationKeyFromRef(ref)
		states[key] = ResourceMutationState{Key: key, AccessVersion: account.AccessVersion}
		snapshot, err := authz.NewResourceAccessSnapshot(authz.ResourceAccessSnapshotInput{
			Subject:       subjectSnapshot,
			Resource:      ref,
			Exists:        true,
			AccessVersion: account.AccessVersion,
		})
		require.NoError(t, err)
		resources[ref] = snapshot
	}
	mutationRepo := &resourceMutationRepositoryStub{states: states}
	repo := &shadowSetTOCTOUAccountRepo{
		sparkShadowRepoStub:  baseRepo,
		mutationRepo:         mutationRepo,
		parentID:             parentID,
		authorizedShadowIDs:  []int64{lockedShadow.ID},
		transactionShadowIDs: []int64{lockedShadow.ID, newShadow.ID},
	}
	coordinator := NewResourceMutationCoordinator(
		mutationRepo,
		resourceMutationResolverStub{actor: actor},
		authz.NewPolicyService(resourceMutationPolicyStoreStub{
			subject:   subjectSnapshot,
			resources: resources,
		}),
	)
	return &adminServiceImpl{accountRepo: repo, resourceMutations: coordinator}, repo, mutationRepo, actor, parentID
}
