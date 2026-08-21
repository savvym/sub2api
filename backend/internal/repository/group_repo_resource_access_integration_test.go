//go:build integration

package repository

import (
	"context"
	"testing"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupRepositoryPersistsResourceAccessFieldsAndDefaults(t *testing.T) {
	tx := testEntTx(t)
	repo := newGroupRepositoryWithSQL(tx.Client(), tx)
	defaultGroup := &service.Group{
		Name:             "resource-access-defaults",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}
	require.NoError(t, repo.Create(context.Background(), defaultGroup))
	require.Nil(t, defaultGroup.OwnerUserID)
	require.Nil(t, defaultGroup.CreatedByUserID)
	require.Nil(t, defaultGroup.PublicAccessLevel)
	require.EqualValues(t, 1, defaultGroup.AccessVersion)
	require.Equal(t, "legacy", defaultGroup.AuthorizationMode)

	owner := mustCreateUser(t, tx.Client(), &service.User{Email: "group-resource-owner@example.com"})
	creator := mustCreateUser(t, tx.Client(), &service.User{Email: "group-resource-creator@example.com"})
	ownerUserID := owner.ID
	createdByUserID := creator.ID
	publicAccessLevel := "viewer"
	groupIn := &service.Group{
		Name:              "resource-access-fields",
		Platform:          service.PlatformAnthropic,
		RateMultiplier:    1,
		Status:            service.StatusActive,
		SubscriptionType:  service.SubscriptionTypeStandard,
		OwnerUserID:       &ownerUserID,
		CreatedByUserID:   &createdByUserID,
		PublicAccessLevel: &publicAccessLevel,
		AccessVersion:     4,
		AuthorizationMode: "shadow",
	}

	require.NoError(t, repo.Create(context.Background(), groupIn))
	require.EqualValues(t, 4, groupIn.AccessVersion)
	require.Equal(t, "shadow", groupIn.AuthorizationMode)

	got, err := repo.GetByIDLite(context.Background(), groupIn.ID)
	require.NoError(t, err)
	require.Equal(t, &ownerUserID, got.OwnerUserID)
	require.Equal(t, &createdByUserID, got.CreatedByUserID)
	require.Equal(t, &publicAccessLevel, got.PublicAccessLevel)
	require.EqualValues(t, 4, got.AccessVersion)
	require.Equal(t, "shadow", got.AuthorizationMode)

	groupIn.OwnerUserID = nil
	groupIn.CreatedByUserID = nil
	groupIn.PublicAccessLevel = nil
	groupIn.AccessVersion = 0
	groupIn.AuthorizationMode = ""
	require.NoError(t, repo.Update(context.Background(), groupIn))

	got, err = repo.GetByIDLite(context.Background(), groupIn.ID)
	require.NoError(t, err)
	require.Nil(t, got.OwnerUserID)
	require.Nil(t, got.CreatedByUserID)
	require.Nil(t, got.PublicAccessLevel)
	require.EqualValues(t, 1, got.AccessVersion)
	require.Equal(t, "legacy", got.AuthorizationMode)
}

func TestGroupRepositoryCreateJoinsOuterTransaction(t *testing.T) {
	tx := testEntTx(t)
	txCtx := dbent.NewTxContext(context.Background(), tx)
	repo := newGroupRepositoryWithSQL(integrationEntClient, integrationDB)
	groupIn := &service.Group{
		Name:             "resource-access-outer-tx",
		Platform:         service.PlatformAnthropic,
		RateMultiplier:   1,
		Status:           service.StatusActive,
		SubscriptionType: service.SubscriptionTypeStandard,
	}

	require.NoError(t, repo.Create(txCtx, groupIn))
	got, err := repo.GetByIDLite(txCtx, groupIn.ID)
	require.NoError(t, err)
	require.Equal(t, groupIn.ID, got.ID)

	require.NoError(t, tx.Rollback())
	_, err = integrationEntClient.Group.Get(context.Background(), groupIn.ID)
	require.True(t, dbent.IsNotFound(err), "repository must leave outer transaction ownership to its caller")
}
