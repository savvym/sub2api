//go:build integration

package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/authz"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

var hostingEntitlementIntegrationSequence uint64

type hostingEntitlementIntegrationFixture struct {
	repo       service.HostingEntitlementRepository
	service    *service.HostingEntitlementService
	resolver   authz.Resolver
	admin      *service.User
	target     *service.User
	adminActor authz.Actor
	cacheKey   string
	requestID  string
}

func TestHostingEntitlementRepositorySchemaPostgres(t *testing.T) {
	tx := testTx(t)

	requireTable(t, tx, "user_hosting_entitlements")
	requireColumn(t, tx, "user_hosting_entitlements", "user_id", "bigint", 0, false)
	requireColumn(t, tx, "user_hosting_entitlements", "account_limit", "bigint", 0, false)
	requireColumn(t, tx, "user_hosting_entitlements", "group_limit", "bigint", 0, false)
	requireColumn(t, tx, "user_hosting_entitlements", "version", "bigint", 0, false)
	requireColumnDefaultContains(t, tx, "user_hosting_entitlements", "account_limit", "0")
	requireColumnDefaultContains(t, tx, "user_hosting_entitlements", "group_limit", "0")
	requireColumnDefaultContains(t, tx, "user_hosting_entitlements", "version", "1")
	requirePartialUniqueIndexDefinition(
		t,
		tx,
		"user_hosting_entitlements",
		"user_hosting_entitlements_user_id_key",
		"user_id",
	)
	requireForeignKeyOnDelete(t, tx, "user_hosting_entitlements", "user_id", "users", "CASCADE")
	requireForeignKeyOnDelete(t, tx, "user_hosting_entitlements", "created_by_user_id", "users", "RESTRICT")
	requireForeignKeyOnDelete(t, tx, "user_hosting_entitlements", "updated_by_user_id", "users", "RESTRICT")
	for _, constraint := range []string{
		"user_hosting_entitlements_user_id_key",
		"user_hosting_entitlements_user_id_fkey",
		"user_hosting_entitlements_created_by_user_id_fkey",
		"user_hosting_entitlements_updated_by_user_id_fkey",
		"user_hosting_entitlements_account_limit_nonnegative",
		"user_hosting_entitlements_group_limit_nonnegative",
		"user_hosting_entitlements_version_positive",
	} {
		requireConstraintValidated(t, tx, "user_hosting_entitlements", constraint)
	}
	requireConstraintDefinitionContains(
		t,
		tx,
		"user_hosting_entitlements",
		"user_hosting_entitlements_account_limit_nonnegative",
		"account_limit",
		">= 0",
	)
	requireConstraintDefinitionContains(
		t,
		tx,
		"user_hosting_entitlements",
		"user_hosting_entitlements_group_limit_nonnegative",
		"group_limit",
		">= 0",
	)
	requireConstraintDefinitionContains(
		t,
		tx,
		"user_hosting_entitlements",
		"user_hosting_entitlements_version_positive",
		"version",
		"> 0",
	)
}

func TestHostingEntitlementRepositoryGrantQuotaRevokeCASAndOutboxPostgres(t *testing.T) {
	fixture := newHostingEntitlementIntegrationFixture(t, true)
	ctx := context.Background()

	initial, err := fixture.service.Get(ctx, fixture.adminActor, fixture.target.ID)
	require.NoError(t, err)
	require.False(t, initial.Hoster)
	require.Zero(t, initial.AccountLimit)
	require.Zero(t, initial.GroupLimit)
	require.Zero(t, initial.Version)
	require.Nil(t, initial.CreatedByUserID)

	initialAuthzVersion := hostingEntitlementIntegrationUserAuthzVersion(t, fixture.target.ID)
	grantRequestID := fixture.requestID + "-grant"
	granted, err := fixture.service.Update(ctx, service.HostingEntitlementUpdateInput{
		Actor:           fixture.adminActor,
		TargetUserID:    fixture.target.ID,
		ExpectedVersion: 0,
		Hoster:          true,
		AccountLimit:    3,
		GroupLimit:      2,
		AuditTrace: service.HostingEntitlementAuditTrace{
			RequestID: grantRequestID,
			ClientIP:  "198.51.100.31",
			UserAgent: "hosting-entitlement-integration/1.0",
		},
	})
	require.NoError(t, err)
	require.True(t, granted.Changed)
	require.True(t, granted.Hoster)
	require.Equal(t, int64(1), granted.Version)
	require.Equal(t, initialAuthzVersion+1, granted.AuthzVersion)
	require.Equal(t, int64(3), granted.AccountLimit)
	require.Equal(t, int64(2), granted.GroupLimit)
	require.Equal(t, fixture.admin.ID, requireHostingEntitlementIntegrationInt64(t, granted.CreatedByUserID))
	require.Equal(t, fixture.admin.ID, requireHostingEntitlementIntegrationInt64(t, granted.UpdatedByUserID))
	requireHostingEntitlementIntegrationRoleAssignment(t, fixture.target.ID, fixture.admin.ID, true)
	require.Equal(t, int64(1), hostingEntitlementIntegrationOutboxCount(t, fixture.cacheKey))
	requireHostingEntitlementIntegrationAudit(t, fixture, grantRequestID, false, true, 0, 1)

	quotaRequestID := fixture.requestID + "-quota"
	quotaOnly, err := fixture.service.Update(ctx, service.HostingEntitlementUpdateInput{
		Actor:           fixture.adminActor,
		TargetUserID:    fixture.target.ID,
		ExpectedVersion: 1,
		Hoster:          true,
		AccountLimit:    5,
		GroupLimit:      4,
		AuditTrace:      service.HostingEntitlementAuditTrace{RequestID: quotaRequestID},
	})
	require.NoError(t, err)
	require.True(t, quotaOnly.Changed)
	require.Equal(t, int64(2), quotaOnly.Version)
	require.Equal(t, granted.AuthzVersion, quotaOnly.AuthzVersion)
	require.Equal(t, int64(1), hostingEntitlementIntegrationOutboxCount(t, fixture.cacheKey))
	requireHostingEntitlementIntegrationAudit(t, fixture, quotaRequestID, true, true, 1, 2)

	noOp, err := fixture.service.Update(ctx, service.HostingEntitlementUpdateInput{
		Actor:           fixture.adminActor,
		TargetUserID:    fixture.target.ID,
		ExpectedVersion: 2,
		Hoster:          true,
		AccountLimit:    5,
		GroupLimit:      4,
		AuditTrace:      service.HostingEntitlementAuditTrace{RequestID: fixture.requestID + "-noop"},
	})
	require.NoError(t, err)
	require.False(t, noOp.Changed)
	require.Equal(t, int64(2), noOp.Version)
	require.Equal(t, int64(2), hostingEntitlementIntegrationAuditCount(t, fixture.admin.ID))

	revokeRequestID := fixture.requestID + "-revoke"
	revoked, err := fixture.service.Update(ctx, service.HostingEntitlementUpdateInput{
		Actor:           fixture.adminActor,
		TargetUserID:    fixture.target.ID,
		ExpectedVersion: 2,
		Hoster:          false,
		AccountLimit:    5,
		GroupLimit:      4,
		AuditTrace:      service.HostingEntitlementAuditTrace{RequestID: revokeRequestID},
	})
	require.NoError(t, err)
	require.True(t, revoked.Changed)
	require.False(t, revoked.Hoster)
	require.Equal(t, int64(3), revoked.Version)
	require.Equal(t, granted.AuthzVersion+1, revoked.AuthzVersion)
	requireHostingEntitlementIntegrationRoleAssignment(t, fixture.target.ID, 0, false)
	require.Equal(t, int64(2), hostingEntitlementIntegrationOutboxCount(t, fixture.cacheKey))
	requireHostingEntitlementIntegrationAudit(t, fixture, revokeRequestID, true, false, 2, 3)

	_, err = fixture.service.Update(ctx, service.HostingEntitlementUpdateInput{
		Actor:           fixture.adminActor,
		TargetUserID:    fixture.target.ID,
		ExpectedVersion: 2,
		Hoster:          true,
		AccountLimit:    7,
		GroupLimit:      7,
	})
	require.ErrorIs(t, err, service.ErrHostingEntitlementConflict)

	final, err := fixture.service.Get(ctx, fixture.adminActor, fixture.target.ID)
	require.NoError(t, err)
	require.Equal(t, revoked.HostingEntitlement, final)
}

func TestHostingEntitlementRepositoryAuditFailureRollsBackEverythingPostgres(t *testing.T) {
	fixture := newHostingEntitlementIntegrationFixture(t, true)
	requestID := fixture.requestID + "-forced-audit-failure"
	installHostingEntitlementIntegrationAuditFailure(t, requestID)
	initialAuthzVersion := hostingEntitlementIntegrationUserAuthzVersion(t, fixture.target.ID)

	_, err := fixture.service.Update(context.Background(), service.HostingEntitlementUpdateInput{
		Actor:           fixture.adminActor,
		TargetUserID:    fixture.target.ID,
		ExpectedVersion: 0,
		Hoster:          true,
		AccountLimit:    2,
		GroupLimit:      1,
		AuditTrace:      service.HostingEntitlementAuditTrace{RequestID: requestID},
	})

	require.ErrorIs(t, err, service.ErrHostingEntitlementUnavailable)
	require.Zero(t, hostingEntitlementIntegrationEntitlementCount(t, fixture.target.ID))
	requireHostingEntitlementIntegrationRoleAssignment(t, fixture.target.ID, 0, false)
	require.Equal(t, initialAuthzVersion, hostingEntitlementIntegrationUserAuthzVersion(t, fixture.target.ID))
	require.Zero(t, hostingEntitlementIntegrationOutboxCount(t, fixture.cacheKey))
	require.Zero(t, hostingEntitlementIntegrationAuditCount(t, fixture.admin.ID))
}

func TestHostingEntitlementRepositoryConcurrentCASAllowsOneWinnerPostgres(t *testing.T) {
	fixture := newHostingEntitlementIntegrationFixture(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	initial, err := fixture.service.Update(ctx, service.HostingEntitlementUpdateInput{
		Actor:           fixture.adminActor,
		TargetUserID:    fixture.target.ID,
		ExpectedVersion: 0,
		AccountLimit:    1,
		GroupLimit:      1,
		AuditTrace:      service.HostingEntitlementAuditTrace{RequestID: fixture.requestID + "-initial"},
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), initial.Version)

	type outcome struct {
		result service.HostingEntitlementUpdateResult
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var wg sync.WaitGroup
	for index, limit := range []int64{2, 3} {
		index, limit := index, limit
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, updateErr := fixture.service.Update(ctx, service.HostingEntitlementUpdateInput{
				Actor:           fixture.adminActor,
				TargetUserID:    fixture.target.ID,
				ExpectedVersion: 1,
				AccountLimit:    limit,
				GroupLimit:      limit,
				AuditTrace: service.HostingEntitlementAuditTrace{
					RequestID: fmt.Sprintf("%s-cas-%d", fixture.requestID, index),
				},
			})
			outcomes <- outcome{result: result, err: updateErr}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)

	var successes, conflicts int
	for result := range outcomes {
		switch {
		case result.err == nil:
			successes++
			require.True(t, result.result.Changed)
			require.Equal(t, int64(2), result.result.Version)
		case errors.Is(result.err, service.ErrHostingEntitlementConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent CAS result: %+v", result)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)

	final, err := fixture.service.Get(ctx, fixture.adminActor, fixture.target.ID)
	require.NoError(t, err)
	require.Equal(t, int64(2), final.Version)
	require.Contains(t, []int64{2, 3}, final.AccountLimit)
	require.Equal(t, final.AccountLimit, final.GroupLimit)
	require.Equal(t, int64(1), hostingEntitlementIntegrationAuditCountByPrefix(t, fixture.requestID+"-cas-"))
}

func TestHostingEntitlementRepositoryConcurrentInitialCASAllowsOneWinnerPostgres(t *testing.T) {
	fixture := newHostingEntitlementIntegrationFixture(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	type outcome struct {
		result service.HostingEntitlementUpdateResult
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var wg sync.WaitGroup
	for index, limit := range []int64{2, 3} {
		index, limit := index, limit
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			result, updateErr := fixture.service.Update(ctx, service.HostingEntitlementUpdateInput{
				Actor:           fixture.adminActor,
				TargetUserID:    fixture.target.ID,
				ExpectedVersion: 0,
				AccountLimit:    limit,
				GroupLimit:      limit,
				AuditTrace: service.HostingEntitlementAuditTrace{
					RequestID: fmt.Sprintf("%s-initial-cas-%d", fixture.requestID, index),
				},
			})
			outcomes <- outcome{result: result, err: updateErr}
		}()
	}
	close(start)
	wg.Wait()
	close(outcomes)

	var successes, conflicts int
	for result := range outcomes {
		switch {
		case result.err == nil:
			successes++
			require.True(t, result.result.Changed)
			require.Equal(t, int64(1), result.result.Version)
		case errors.Is(result.err, service.ErrHostingEntitlementConflict):
			conflicts++
		default:
			t.Fatalf("unexpected concurrent initial CAS result: %+v", result)
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)

	final, err := fixture.service.Get(ctx, fixture.adminActor, fixture.target.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), final.Version)
	require.Contains(t, []int64{2, 3}, final.AccountLimit)
	require.Equal(t, final.AccountLimit, final.GroupLimit)
	require.Equal(t, int64(1), hostingEntitlementIntegrationAuditCountByPrefix(t, fixture.requestID+"-initial-cas-"))
}

func TestHostingEntitlementRepositoryAllowsQuotaReductionBelowUsageAndBlocksCreationPostgres(t *testing.T) {
	fixture := newHostingEntitlementIntegrationFixture(t, false)
	ctx := context.Background()

	granted, err := fixture.service.Update(ctx, service.HostingEntitlementUpdateInput{
		Actor:           fixture.adminActor,
		TargetUserID:    fixture.target.ID,
		ExpectedVersion: 0,
		Hoster:          true,
		AccountLimit:    3,
		GroupLimit:      1,
		AuditTrace:      service.HostingEntitlementAuditTrace{RequestID: fixture.requestID + "-grant"},
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), granted.Version)

	for index := 0; index < 2; index++ {
		hostingEntitlementIntegrationInsertResource(
			t,
			context.Background(),
			fixture.target.ID,
			authz.ResourceTypeAccount,
			fmt.Sprintf("%s-existing-%d", fixture.requestID, index),
		)
	}

	lowered, err := fixture.service.Update(ctx, service.HostingEntitlementUpdateInput{
		Actor:           fixture.adminActor,
		TargetUserID:    fixture.target.ID,
		ExpectedVersion: 1,
		Hoster:          true,
		AccountLimit:    1,
		GroupLimit:      1,
		AuditTrace:      service.HostingEntitlementAuditTrace{RequestID: fixture.requestID + "-lower"},
	})
	require.NoError(t, err)
	require.True(t, lowered.Changed)
	require.Equal(t, int64(2), lowered.Version)
	require.Equal(t, int64(2), lowered.AccountUsage)
	require.Zero(t, lowered.AccountRemaining)

	targetActor, err := fixture.resolver.ResolveUser(ctx, fixture.target.ID, authz.AuthMethodJWT)
	require.NoError(t, err)
	capacityService := service.NewHostingEntitlementService(
		fixture.repo,
		fixture.resolver,
		hostingEntitlementIntegrationShadowPolicy(t, fixture.target.ID),
	)

	_, err = capacityService.RequireCreateCapacity(ctx, targetActor, authz.ResourceTypeAccount)
	require.ErrorIs(t, err, service.ErrHostingEntitlementUnavailable)

	err = fixture.repo.WithHostingEntitlementTx(ctx, func(txCtx context.Context) error {
		_, capacityErr := capacityService.RequireCreateCapacity(txCtx, targetActor, authz.ResourceTypeAccount)
		return capacityErr
	})
	require.ErrorIs(t, err, service.ErrHostingQuotaExceeded)
}

func TestHostingEntitlementRepositoryCapacityRequiresSerializableTransactionPostgres(t *testing.T) {
	fixture := newHostingEntitlementIntegrationFixture(t, false)
	ctx := context.Background()

	_, err := fixture.service.Update(ctx, service.HostingEntitlementUpdateInput{
		Actor:           fixture.adminActor,
		TargetUserID:    fixture.target.ID,
		ExpectedVersion: 0,
		Hoster:          true,
		AccountLimit:    1,
		AuditTrace:      service.HostingEntitlementAuditTrace{RequestID: fixture.requestID + "-grant"},
	})
	require.NoError(t, err)
	targetActor, err := fixture.resolver.ResolveUser(ctx, fixture.target.ID, authz.AuthMethodJWT)
	require.NoError(t, err)
	capacityService := service.NewHostingEntitlementService(
		fixture.repo,
		fixture.resolver,
		hostingEntitlementIntegrationShadowPolicy(t, fixture.target.ID),
	)

	tx, err := integrationEntClient.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)

	_, err = capacityService.RequireCreateCapacity(txCtx, targetActor, authz.ResourceTypeAccount)

	require.ErrorIs(t, err, service.ErrHostingEntitlementUnavailable)
	require.Equal(t, hostingEntitlementSerializableIsolationReason, infraerrors.FromError(err).Metadata["reason"])
}

func TestHostingEntitlementRepositoryConcurrentCapacityNeverOverallocatesPostgres(t *testing.T) {
	for _, resourceType := range []authz.ResourceType{authz.ResourceTypeAccount, authz.ResourceTypeGroup} {
		resourceType := resourceType
		t.Run(string(resourceType), func(t *testing.T) {
			fixture := newHostingEntitlementIntegrationFixture(t, false)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			input := service.HostingEntitlementUpdateInput{
				Actor:           fixture.adminActor,
				TargetUserID:    fixture.target.ID,
				ExpectedVersion: 0,
				Hoster:          true,
				AuditTrace:      service.HostingEntitlementAuditTrace{RequestID: fixture.requestID + "-grant"},
			}
			if resourceType == authz.ResourceTypeAccount {
				input.AccountLimit = 1
			} else {
				input.GroupLimit = 1
			}
			_, err := fixture.service.Update(ctx, input)
			require.NoError(t, err)

			targetActor, err := fixture.resolver.ResolveUser(ctx, fixture.target.ID, authz.AuthMethodJWT)
			require.NoError(t, err)
			capacityService := service.NewHostingEntitlementService(
				fixture.repo,
				fixture.resolver,
				hostingEntitlementIntegrationShadowPolicy(t, fixture.target.ID),
			)

			start := make(chan struct{})
			outcomes := make(chan error, 2)
			var wg sync.WaitGroup
			for index := 0; index < 2; index++ {
				index := index
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					outcomes <- fixture.repo.WithHostingEntitlementTx(ctx, func(txCtx context.Context) error {
						if _, capacityErr := capacityService.RequireCreateCapacity(txCtx, targetActor, resourceType); capacityErr != nil {
							return capacityErr
						}
						return hostingEntitlementIntegrationInsertResourceError(
							txCtx,
							fixture.target.ID,
							resourceType,
							fmt.Sprintf("%s-concurrent-%d", fixture.requestID, index),
						)
					})
				}()
			}
			close(start)
			wg.Wait()
			close(outcomes)

			var successes, rejected int
			for outcome := range outcomes {
				switch {
				case outcome == nil:
					successes++
				case errors.Is(outcome, service.ErrHostingQuotaExceeded),
					errors.Is(outcome, service.ErrHostingEntitlementConflict):
					rejected++
				default:
					t.Fatalf("unexpected capacity outcome: %v", outcome)
				}
			}
			require.Equal(t, 1, successes)
			require.Equal(t, 1, rejected)
			require.Equal(t, int64(1), hostingEntitlementIntegrationOwnedResourceCount(t, fixture.target.ID, resourceType))
		})
	}
}

type hostingEntitlementIntegrationPolicyStore struct {
	snapshot authz.SubjectSnapshot
}

func (s hostingEntitlementIntegrationPolicyStore) LoadSubjectSnapshot(
	context.Context,
	authz.SubjectRef,
) (authz.SubjectSnapshot, error) {
	return s.snapshot, nil
}

func (s hostingEntitlementIntegrationPolicyStore) LoadResourceAccessSnapshot(
	context.Context,
	authz.SubjectRef,
	authz.ResourceRef,
) (authz.ResourceAccessSnapshot, error) {
	return authz.ResourceAccessSnapshot{}, errors.New("unexpected resource snapshot lookup")
}

func newHostingEntitlementIntegrationFixture(
	t *testing.T,
	withAPIKey bool,
) *hostingEntitlementIntegrationFixture {
	t.Helper()
	suffix := nextHostingEntitlementIntegrationSuffix()
	requestID := fmt.Sprintf("hosting-entitlement-%d", suffix)
	cacheKey := ""
	if withAPIKey {
		keyValue := fmt.Sprintf("sk-hosting-entitlement-%d", suffix)
		hash := sha256.Sum256([]byte(keyValue))
		cacheKey = hex.EncodeToString(hash[:])
		t.Cleanup(func() {
			_, err := integrationDB.ExecContext(context.Background(), `
DELETE FROM auth_cache_invalidation_outbox
WHERE cache_key = $1`, cacheKey)
			require.NoError(t, err)
		})

		userRepo := NewUserRepository(integrationEntClient, integrationDB)
		admin := createRoleIntegrationUser(t, userRepo, service.RoleAdmin, "hosting-entitlement-admin")
		target := createRoleIntegrationUser(t, userRepo, service.RoleUser, "hosting-entitlement-target")
		registerHostingEntitlementIntegrationCleanup(t, admin.ID, target.ID)
		require.NoError(t, NewAPIKeyRepository(integrationEntClient, integrationDB).Create(
			context.Background(),
			&service.APIKey{
				UserID: target.ID,
				Key:    keyValue,
				Name:   "hosting entitlement invalidation",
				Status: service.StatusActive,
			},
		))
		_, err := integrationDB.ExecContext(context.Background(), `
DELETE FROM auth_cache_invalidation_outbox
WHERE cache_key = $1`, cacheKey)
		require.NoError(t, err)
		return buildHostingEntitlementIntegrationFixture(t, admin, target, cacheKey, requestID)
	}

	userRepo := NewUserRepository(integrationEntClient, integrationDB)
	admin := createRoleIntegrationUser(t, userRepo, service.RoleAdmin, "hosting-entitlement-admin")
	target := createRoleIntegrationUser(t, userRepo, service.RoleUser, "hosting-entitlement-target")
	registerHostingEntitlementIntegrationCleanup(t, admin.ID, target.ID)
	return buildHostingEntitlementIntegrationFixture(t, admin, target, cacheKey, requestID)
}

func buildHostingEntitlementIntegrationFixture(
	t *testing.T,
	admin *service.User,
	target *service.User,
	cacheKey string,
	requestID string,
) *hostingEntitlementIntegrationFixture {
	t.Helper()
	resolver := authz.NewActorResolver(NewAuthzActorResolverStore(integrationEntClient))
	adminActor, err := resolver.ResolveLegacyAdminUser(context.Background(), admin.ID)
	require.NoError(t, err)
	repo := NewHostingEntitlementRepository(integrationEntClient)
	return &hostingEntitlementIntegrationFixture{
		repo:       repo,
		service:    service.NewHostingEntitlementService(repo, resolver, nil),
		resolver:   resolver,
		admin:      admin,
		target:     target,
		adminActor: adminActor,
		cacheKey:   cacheKey,
		requestID:  requestID,
	}
}

func registerHostingEntitlementIntegrationCleanup(t *testing.T, actorUserID, targetUserID int64) {
	t.Helper()
	t.Cleanup(func() {
		ctx := context.Background()
		_, err := integrationDB.ExecContext(ctx, `
DELETE FROM accounts
WHERE owner_user_id = $1`, targetUserID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `
DELETE FROM groups
WHERE owner_user_id = $1`, targetUserID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(ctx, `
DELETE FROM audit_logs
WHERE actor_user_id = $1
  AND action = $2`, actorUserID, service.AuditActionHostingEntitlementUpdate)
		require.NoError(t, err)
	})
}

func hostingEntitlementIntegrationShadowPolicy(t *testing.T, userID int64) authz.ResourcePolicy {
	t.Helper()
	subject, err := authz.NewSubjectRef(authz.SubjectKindUser, userID)
	require.NoError(t, err)
	baseSnapshot, err := newAuthzPolicyStoreWithQueryer(integrationDB).LoadSubjectSnapshot(context.Background(), subject)
	require.NoError(t, err)
	configuration, err := authz.NewPolicyConfiguration(authz.PolicyConfigurationInput{
		RoleAuthorizationMode:        authz.RoleAuthorizationModeShadow,
		ResourceAccessControlEnabled: true,
		SelfServiceHostingEnabled:    true,
	})
	require.NoError(t, err)
	snapshot, err := authz.NewSubjectSnapshot(authz.SubjectSnapshotInput{
		Subject:            subject,
		Exists:             baseSnapshot.Exists(),
		Active:             baseSnapshot.Active(),
		AuthzVersion:       baseSnapshot.AuthzVersion(),
		RoleVersions:       baseSnapshot.RoleVersions(),
		Capabilities:       baseSnapshot.Capabilities(),
		CurrentLegacyAdmin: baseSnapshot.CurrentLegacyAdmin(),
		Configuration:      configuration,
	})
	require.NoError(t, err)
	return authz.NewPolicyService(hostingEntitlementIntegrationPolicyStore{snapshot: snapshot})
}

func installHostingEntitlementIntegrationAuditFailure(t *testing.T, requestID string) {
	t.Helper()
	suffix := nextHostingEntitlementIntegrationSuffix()
	functionName := fmt.Sprintf("hosting_entitlement_audit_failure_%d", suffix)
	triggerName := fmt.Sprintf("hosting_entitlement_audit_failure_%d", suffix)
	_, err := integrationDB.ExecContext(context.Background(), fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger AS $trigger$
BEGIN
    IF NEW.action = %s AND NEW.request_id = %s THEN
        RAISE EXCEPTION 'forced hosting entitlement audit failure';
    END IF;
    RETURN NEW;
END;
$trigger$ LANGUAGE plpgsql;

CREATE TRIGGER %s
BEFORE INSERT ON audit_logs
FOR EACH ROW EXECUTE FUNCTION %s();`,
		pq.QuoteIdentifier(functionName),
		pq.QuoteLiteral(service.AuditActionHostingEntitlementUpdate),
		pq.QuoteLiteral(requestID),
		pq.QuoteIdentifier(triggerName),
		pq.QuoteIdentifier(functionName),
	))
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx := context.Background()
		_, cleanupErr := integrationDB.ExecContext(ctx, fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON audit_logs",
			pq.QuoteIdentifier(triggerName),
		))
		require.NoError(t, cleanupErr)
		_, cleanupErr = integrationDB.ExecContext(ctx, fmt.Sprintf(
			"DROP FUNCTION IF EXISTS %s()",
			pq.QuoteIdentifier(functionName),
		))
		require.NoError(t, cleanupErr)
	})
}

func requireHostingEntitlementIntegrationAudit(
	t *testing.T,
	fixture *hostingEntitlementIntegrationFixture,
	requestID string,
	previousHoster bool,
	currentHoster bool,
	previousVersion int64,
	currentVersion int64,
) {
	t.Helper()
	var (
		authMethod string
		method     string
		path       string
		statusCode int
		extra      []byte
	)
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
SELECT auth_method, method, path, status_code, extra
FROM audit_logs
WHERE actor_user_id = $1
  AND action = $2
  AND request_id = $3`, fixture.admin.ID, service.AuditActionHostingEntitlementUpdate, requestID).Scan(
		&authMethod,
		&method,
		&path,
		&statusCode,
		&extra,
	))
	require.Equal(t, service.AuditAuthMethodJWT, authMethod)
	require.Equal(t, httpMethodPut, method)
	require.Equal(t, hostingEntitlementAuditPath, path)
	require.Equal(t, 200, statusCode)

	var document struct {
		TargetUserID int64 `json:"target_user_id"`
		Previous     struct {
			Hoster  bool  `json:"hoster"`
			Version int64 `json:"version"`
		} `json:"previous"`
		Current struct {
			Hoster  bool  `json:"hoster"`
			Version int64 `json:"version"`
		} `json:"current"`
	}
	require.NoError(t, json.Unmarshal(extra, &document))
	require.Equal(t, fixture.target.ID, document.TargetUserID)
	require.Equal(t, previousHoster, document.Previous.Hoster)
	require.Equal(t, currentHoster, document.Current.Hoster)
	require.Equal(t, previousVersion, document.Previous.Version)
	require.Equal(t, currentVersion, document.Current.Version)
}

const httpMethodPut = "PUT"

func requireHostingEntitlementIntegrationRoleAssignment(
	t *testing.T,
	userID int64,
	grantorUserID int64,
	want bool,
) {
	t.Helper()
	var (
		count      int64
		gotGrantor sql.NullInt64
		serviceID  sql.NullInt64
		expiresAt  sql.NullTime
	)
	err := integrationDB.QueryRowContext(context.Background(), `
SELECT COUNT(*), MAX(ur.granted_by_user_id), MAX(ur.granted_by_service_principal_id), MAX(ur.expires_at)
FROM user_roles AS ur
JOIN roles AS r ON r.id = ur.role_id
WHERE ur.user_id = $1
  AND r.code = 'hoster'
  AND r.is_system = TRUE`, userID).Scan(&count, &gotGrantor, &serviceID, &expiresAt)
	require.NoError(t, err)
	if !want {
		require.Zero(t, count)
		return
	}
	require.Equal(t, int64(1), count)
	require.True(t, gotGrantor.Valid)
	require.Equal(t, grantorUserID, gotGrantor.Int64)
	require.False(t, serviceID.Valid)
	require.False(t, expiresAt.Valid)
}

func hostingEntitlementIntegrationInsertResource(
	t *testing.T,
	ctx context.Context,
	userID int64,
	resourceType authz.ResourceType,
	name string,
) {
	t.Helper()
	require.NoError(t, hostingEntitlementIntegrationInsertResourceError(ctx, userID, resourceType, name))
}

func hostingEntitlementIntegrationInsertResourceError(
	ctx context.Context,
	userID int64,
	resourceType authz.ResourceType,
	name string,
) error {
	client := clientFromContext(ctx, integrationEntClient)
	switch resourceType {
	case authz.ResourceTypeAccount:
		_, err := client.ExecContext(ctx, `
INSERT INTO accounts (
    name, platform, type, status, owner_user_id, created_by_user_id
)
VALUES ($1, 'openai', 'api_key', 'active', $2, $2)`, name, userID)
		return err
	case authz.ResourceTypeGroup:
		_, err := client.ExecContext(ctx, `
INSERT INTO groups (
    name, rate_multiplier, is_exclusive, status, owner_user_id, created_by_user_id
)
VALUES ($1, 1, FALSE, 'active', $2, $2)`, name, userID)
		return err
	default:
		return fmt.Errorf("unsupported resource type %q", resourceType)
	}
}

func hostingEntitlementIntegrationOwnedResourceCount(
	t *testing.T,
	userID int64,
	resourceType authz.ResourceType,
) int64 {
	t.Helper()
	table := "accounts"
	if resourceType == authz.ResourceTypeGroup {
		table = "groups"
	}
	var count int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), fmt.Sprintf(`
SELECT COUNT(*)
FROM %s
WHERE owner_user_id = $1
  AND deleted_at IS NULL`, pq.QuoteIdentifier(table)), userID).Scan(&count))
	return count
}

func hostingEntitlementIntegrationUserAuthzVersion(t *testing.T, userID int64) int64 {
	t.Helper()
	var version int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
SELECT authz_version
FROM users
WHERE id = $1`, userID).Scan(&version))
	return version
}

func hostingEntitlementIntegrationOutboxCount(t *testing.T, cacheKey string) int64 {
	t.Helper()
	if cacheKey == "" {
		return 0
	}
	var count int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
SELECT COUNT(*)
FROM auth_cache_invalidation_outbox
WHERE cache_key = $1`, cacheKey).Scan(&count))
	return count
}

func hostingEntitlementIntegrationEntitlementCount(t *testing.T, userID int64) int64 {
	t.Helper()
	var count int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
SELECT COUNT(*)
FROM user_hosting_entitlements
WHERE user_id = $1`, userID).Scan(&count))
	return count
}

func hostingEntitlementIntegrationAuditCount(t *testing.T, actorUserID int64) int64 {
	t.Helper()
	var count int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
SELECT COUNT(*)
FROM audit_logs
WHERE actor_user_id = $1
  AND action = $2`, actorUserID, service.AuditActionHostingEntitlementUpdate).Scan(&count))
	return count
}

func hostingEntitlementIntegrationAuditCountByPrefix(t *testing.T, prefix string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
SELECT COUNT(*)
FROM audit_logs
WHERE action = $1
  AND request_id LIKE $2`, service.AuditActionHostingEntitlementUpdate, prefix+"%").Scan(&count))
	return count
}

func requireHostingEntitlementIntegrationInt64(t *testing.T, value *int64) int64 {
	t.Helper()
	require.NotNil(t, value)
	return *value
}

func nextHostingEntitlementIntegrationSuffix() int64 {
	sequence := atomic.AddUint64(&hostingEntitlementIntegrationSequence, 1)
	return time.Now().UnixNano() + int64(sequence)
}
