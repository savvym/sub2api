//go:build integration

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type selfServiceGroupPostgresFixture struct {
	client      *dbent.Client
	repository  service.SelfServiceGroupRepository
	userID      int64
	requestBase string
}

func TestSelfServiceGroupRepositoryCreatesPrivateGroupWithSafeDefaultsOutboxAndEventPostgres(t *testing.T) {
	fixture := newSelfServiceGroupPostgresFixture(t)
	requestID := fixture.requestBase + "-create"
	var created service.SelfServiceGroupState

	err := fixture.repository.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		var createErr error
		created, createErr = fixture.repository.CreateGroup(txCtx, service.SelfServiceGroupCreateRecord{
			Name:          "Personal OpenAI",
			Description:   "Private group",
			Platform:      service.PlatformOpenAI,
			OwnerUserID:   fixture.userID,
			CreatorUserID: fixture.userID,
		})
		if createErr != nil {
			return createErr
		}
		return fixture.repository.AppendAuthorizationEvent(
			txCtx,
			selfServiceGroupPostgresEvent(
				created,
				fixture.userID,
				"group.created",
				requestID,
				[]string{"configuration", "ownership"},
			),
		)
	})

	require.NoError(t, err)
	require.Positive(t, created.ID)
	require.Equal(t, int64(1), created.AccessVersion)
	require.False(t, created.Deleted)
	require.True(t, created.IsExclusive)
	require.Equal(t, &fixture.userID, created.OwnerUserID)
	require.Equal(t, &fixture.userID, created.CreatedByUserID)
	require.Nil(t, created.PublicAccessLevel)
	require.Equal(t, "legacy", created.AuthorizationMode)

	var (
		name                            string
		description                     sql.NullString
		platform                        string
		rateMultiplier                  float64
		peakRateEnabled                 bool
		peakStart                       string
		peakEnd                         string
		peakRateMultiplier              float64
		isExclusive                     bool
		status                          string
		subscriptionType                string
		dailyLimit                      sql.NullFloat64
		weeklyLimit                     sql.NullFloat64
		monthlyLimit                    sql.NullFloat64
		defaultValidityDays             int
		allowImageGeneration            bool
		allowBatchImageGeneration       bool
		imageRateIndependent            bool
		imageRateMultiplier             float64
		batchImageDiscountMultiplier    float64
		batchImageHoldMultiplier        float64
		videoRateIndependent            bool
		videoRateMultiplier             float64
		longContextPricingEnabled       bool
		modelPricing                    sql.NullString
		claudeCodeOnly                  bool
		fallbackGroupID                 sql.NullInt64
		fallbackGroupOnInvalidRequestID sql.NullInt64
		modelRoutingEnabled             bool
		mcpXMLInject                    bool
		sortOrder                       int
		allowMessagesDispatch           bool
		allowLive                       bool
		requireOAuthOnly                bool
		requirePrivacySet               bool
		defaultMappedModel              string
		rpmLimit                        int
		maxReasoningEffort              string
		ownerUserID                     sql.NullInt64
		createdByUserID                 sql.NullInt64
		publicAccessLevel               sql.NullString
		accessVersion                   int64
		authorizationMode               string
		profitControlEnabled            bool
		profitMinMargin                 float64
		profitSafetyBuffer              float64
		deletedAt                       sql.NullTime
	)
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
SELECT
    name,
    description,
    platform,
    rate_multiplier,
    peak_rate_enabled,
    peak_start,
    peak_end,
    peak_rate_multiplier,
    is_exclusive,
    status,
    subscription_type,
    daily_limit_usd,
    weekly_limit_usd,
    monthly_limit_usd,
    default_validity_days,
    allow_image_generation,
    allow_batch_image_generation,
    image_rate_independent,
    image_rate_multiplier,
    batch_image_discount_multiplier,
    batch_image_hold_multiplier,
    video_rate_independent,
    video_rate_multiplier,
    long_context_pricing_enabled,
    model_pricing::text,
    claude_code_only,
    fallback_group_id,
    fallback_group_id_on_invalid_request,
    model_routing_enabled,
    mcp_xml_inject,
    sort_order,
    allow_messages_dispatch,
    allow_live,
    require_oauth_only,
    require_privacy_set,
    default_mapped_model,
    rpm_limit,
    max_reasoning_effort,
    owner_user_id,
    created_by_user_id,
    public_access_level,
    access_version,
    authorization_mode,
    profit_control_enabled,
    profit_min_margin,
    profit_safety_buffer,
    deleted_at
FROM groups
WHERE id = $1`, created.ID).Scan(
		&name,
		&description,
		&platform,
		&rateMultiplier,
		&peakRateEnabled,
		&peakStart,
		&peakEnd,
		&peakRateMultiplier,
		&isExclusive,
		&status,
		&subscriptionType,
		&dailyLimit,
		&weeklyLimit,
		&monthlyLimit,
		&defaultValidityDays,
		&allowImageGeneration,
		&allowBatchImageGeneration,
		&imageRateIndependent,
		&imageRateMultiplier,
		&batchImageDiscountMultiplier,
		&batchImageHoldMultiplier,
		&videoRateIndependent,
		&videoRateMultiplier,
		&longContextPricingEnabled,
		&modelPricing,
		&claudeCodeOnly,
		&fallbackGroupID,
		&fallbackGroupOnInvalidRequestID,
		&modelRoutingEnabled,
		&mcpXMLInject,
		&sortOrder,
		&allowMessagesDispatch,
		&allowLive,
		&requireOAuthOnly,
		&requirePrivacySet,
		&defaultMappedModel,
		&rpmLimit,
		&maxReasoningEffort,
		&ownerUserID,
		&createdByUserID,
		&publicAccessLevel,
		&accessVersion,
		&authorizationMode,
		&profitControlEnabled,
		&profitMinMargin,
		&profitSafetyBuffer,
		&deletedAt,
	))
	require.Equal(t, "Personal OpenAI", name)
	require.Equal(t, sql.NullString{String: "Private group", Valid: true}, description)
	require.Equal(t, service.PlatformOpenAI, platform)
	require.Equal(t, 1.0, rateMultiplier)
	require.False(t, peakRateEnabled)
	require.Empty(t, peakStart)
	require.Empty(t, peakEnd)
	require.Equal(t, 1.0, peakRateMultiplier)
	require.True(t, isExclusive)
	require.Equal(t, service.StatusActive, status)
	require.Equal(t, service.SubscriptionTypeStandard, subscriptionType)
	require.False(t, dailyLimit.Valid)
	require.False(t, weeklyLimit.Valid)
	require.False(t, monthlyLimit.Valid)
	require.Equal(t, 30, defaultValidityDays)
	require.False(t, allowImageGeneration)
	require.False(t, allowBatchImageGeneration)
	require.False(t, imageRateIndependent)
	require.Equal(t, 1.0, imageRateMultiplier)
	require.Equal(t, 0.5, batchImageDiscountMultiplier)
	require.Equal(t, 0.6, batchImageHoldMultiplier)
	require.False(t, videoRateIndependent)
	require.Equal(t, 1.0, videoRateMultiplier)
	require.True(t, longContextPricingEnabled)
	require.False(t, modelPricing.Valid)
	require.False(t, claudeCodeOnly)
	require.False(t, fallbackGroupID.Valid)
	require.False(t, fallbackGroupOnInvalidRequestID.Valid)
	require.False(t, modelRoutingEnabled)
	require.True(t, mcpXMLInject)
	require.Zero(t, sortOrder)
	require.False(t, allowMessagesDispatch)
	require.False(t, allowLive)
	require.False(t, requireOAuthOnly)
	require.False(t, requirePrivacySet)
	require.Empty(t, defaultMappedModel)
	require.Zero(t, rpmLimit)
	require.Empty(t, maxReasoningEffort)
	require.Equal(t, sql.NullInt64{Int64: fixture.userID, Valid: true}, ownerUserID)
	require.Equal(t, sql.NullInt64{Int64: fixture.userID, Valid: true}, createdByUserID)
	require.False(t, publicAccessLevel.Valid)
	require.Equal(t, int64(1), accessVersion)
	require.Equal(t, "legacy", authorizationMode)
	require.False(t, profitControlEnabled)
	require.Zero(t, profitMinMargin)
	require.Zero(t, profitSafetyBuffer)
	require.False(t, deletedAt.Valid)

	require.Equal(t, 1, selfServiceGroupPostgresCount(t, `
SELECT COUNT(*)
FROM scheduler_outbox
WHERE event_type = $1 AND group_id = $2`, service.SchedulerOutboxEventGroupChanged, created.ID))

	var (
		eventOwnerUserID   sql.NullInt64
		eventActorUserID   sql.NullInt64
		eventActorSPID     sql.NullInt64
		authMethod         string
		eventType          string
		eventAccessVersion int64
		eventRequestID     string
		detailsJSON        string
	)
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
SELECT
    resource_owner_user_id,
    actor_user_id,
    actor_service_principal_id,
    auth_method,
    event_type,
    resource_access_version,
    request_id,
    details::text
FROM resource_authorization_events
WHERE group_id = $1 AND request_id = $2`, created.ID, requestID).Scan(
		&eventOwnerUserID,
		&eventActorUserID,
		&eventActorSPID,
		&authMethod,
		&eventType,
		&eventAccessVersion,
		&eventRequestID,
		&detailsJSON,
	))
	require.Equal(t, sql.NullInt64{Int64: fixture.userID, Valid: true}, eventOwnerUserID)
	require.Equal(t, sql.NullInt64{Int64: fixture.userID, Valid: true}, eventActorUserID)
	require.False(t, eventActorSPID.Valid)
	require.Equal(t, string(authz.AuthMethodJWT), authMethod)
	require.Equal(t, "group.created", eventType)
	require.Equal(t, int64(1), eventAccessVersion)
	require.Equal(t, requestID, eventRequestID)
	require.JSONEq(t, `{"changed_fields":["configuration","ownership"],"result":"success"}`, detailsJSON)
}

func TestSelfServiceGroupRepositoryEventFailureRollsBackGroupAndOutboxPostgres(t *testing.T) {
	fixture := newSelfServiceGroupPostgresFixture(t)
	requestID := fixture.requestBase + "-event-failure"
	groupName := fmt.Sprintf("self-service-group-event-rollback-%d", time.Now().UnixNano())
	var created service.SelfServiceGroupState

	err := fixture.repository.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		var createErr error
		created, createErr = fixture.repository.CreateGroup(txCtx, service.SelfServiceGroupCreateRecord{
			Name: groupName, Platform: service.PlatformAnthropic,
			OwnerUserID: fixture.userID, CreatorUserID: fixture.userID,
		})
		if createErr != nil {
			return createErr
		}
		event := selfServiceGroupPostgresEvent(
			created, fixture.userID, "group.created", requestID, []string{"configuration"},
		)
		event.ActorID = math.MaxInt64
		return fixture.repository.AppendAuthorizationEvent(txCtx, event)
	})

	require.Error(t, err)
	require.Positive(t, created.ID)
	require.Zero(t, selfServiceGroupPostgresCount(t,
		"SELECT COUNT(*) FROM groups WHERE id = $1", created.ID))
	require.Zero(t, selfServiceGroupPostgresCount(t,
		"SELECT COUNT(*) FROM groups WHERE name = $1", groupName))
	require.Zero(t, selfServiceGroupPostgresCount(t,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE group_id = $1", created.ID))
	require.Zero(t, selfServiceGroupPostgresCount(t,
		"SELECT COUNT(*) FROM resource_authorization_events WHERE request_id = $1", requestID))
}

func TestSelfServiceGroupRepositoryOutboxFailureRollsBackGroupPostgres(t *testing.T) {
	fixture := newSelfServiceGroupPostgresFixture(t)
	requestID := fixture.requestBase + "-outbox-failure"
	groupName := fmt.Sprintf("self-service-group-outbox-rollback-%d", time.Now().UnixNano())
	installSelfServiceGroupOutboxFailureTrigger(t, groupName)

	err := fixture.repository.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		created, createErr := fixture.repository.CreateGroup(txCtx, service.SelfServiceGroupCreateRecord{
			Name: groupName, Platform: service.PlatformGemini,
			OwnerUserID: fixture.userID, CreatorUserID: fixture.userID,
		})
		if createErr != nil {
			return createErr
		}
		return fixture.repository.AppendAuthorizationEvent(
			txCtx,
			selfServiceGroupPostgresEvent(
				created, fixture.userID, "group.created", requestID, nil,
			),
		)
	})

	require.ErrorContains(t, err, "forced self-service group outbox failure")
	require.Zero(t, selfServiceGroupPostgresCount(t,
		"SELECT COUNT(*) FROM groups WHERE name = $1", groupName))
	require.Zero(t, selfServiceGroupPostgresCount(t, `
SELECT COUNT(*)
FROM scheduler_outbox AS outbox
JOIN groups AS group_row ON group_row.id = outbox.group_id
WHERE group_row.name = $1`, groupName))
	require.Zero(t, selfServiceGroupPostgresCount(t,
		"SELECT COUNT(*) FROM resource_authorization_events WHERE request_id = $1", requestID))
}

func TestSelfServiceGroupRepositoryUpdateAndSoftDeleteIncrementVersionPostgres(t *testing.T) {
	fixture := newSelfServiceGroupPostgresFixture(t)
	created := createSelfServiceGroupPostgres(t, fixture, "before")

	_, err := integrationDB.ExecContext(context.Background(),
		"DELETE FROM scheduler_outbox WHERE group_id = $1", created.ID)
	require.NoError(t, err)

	var updated service.SelfServiceGroupState
	err = fixture.repository.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		if lockErr := fixture.repository.LockActorAuthorization(txCtx, fixture.userID); lockErr != nil {
			return lockErr
		}
		locked, lockErr := fixture.repository.LockGroup(txCtx, created.ID)
		if lockErr != nil {
			return lockErr
		}
		var updateErr error
		updated, updateErr = fixture.repository.UpdateGroup(
			txCtx, created.ID, fixture.userID, locked.AccessVersion, "after", "changed",
		)
		if updateErr != nil {
			return updateErr
		}
		return fixture.repository.AppendAuthorizationEvent(
			txCtx,
			selfServiceGroupPostgresEvent(
				updated,
				fixture.userID,
				"group.updated",
				fixture.requestBase+"-update",
				[]string{"name", "description"},
			),
		)
	})
	require.NoError(t, err)
	require.Equal(t, int64(2), updated.AccessVersion)
	require.Equal(t, "after", updated.Name)
	require.Equal(t, "changed", updated.Description)
	require.False(t, updated.Deleted)
	require.Equal(t, 1, selfServiceGroupPostgresCount(t,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE group_id = $1", created.ID))
	require.Equal(t, 1, selfServiceGroupPostgresCount(t, `
SELECT COUNT(*)
FROM resource_authorization_events
WHERE group_id = $1 AND request_id = $2 AND resource_access_version = 2`,
		created.ID, fixture.requestBase+"-update"))

	_, err = integrationDB.ExecContext(context.Background(),
		"DELETE FROM scheduler_outbox WHERE group_id = $1", created.ID)
	require.NoError(t, err)

	var deleted service.SelfServiceGroupState
	err = fixture.repository.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		if lockErr := fixture.repository.LockActorAuthorization(txCtx, fixture.userID); lockErr != nil {
			return lockErr
		}
		locked, lockErr := fixture.repository.LockGroup(txCtx, created.ID)
		if lockErr != nil {
			return lockErr
		}
		var deleteErr error
		deleted, deleteErr = fixture.repository.DeleteGroup(
			txCtx, created.ID, fixture.userID, locked.AccessVersion,
		)
		if deleteErr != nil {
			return deleteErr
		}
		return fixture.repository.AppendAuthorizationEvent(
			txCtx,
			selfServiceGroupPostgresEvent(
				deleted,
				fixture.userID,
				"group.deleted",
				fixture.requestBase+"-delete",
				[]string{"lifecycle"},
			),
		)
	})
	require.NoError(t, err)
	require.Equal(t, int64(3), deleted.AccessVersion)
	require.True(t, deleted.Deleted)

	var (
		persistedName        string
		persistedDescription sql.NullString
		persistedVersion     int64
		deletedAt            sql.NullTime
	)
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
SELECT name, description, access_version, deleted_at
FROM groups
WHERE id = $1`, created.ID).Scan(
		&persistedName, &persistedDescription, &persistedVersion, &deletedAt,
	))
	require.Equal(t, "after", persistedName)
	require.Equal(t, sql.NullString{String: "changed", Valid: true}, persistedDescription)
	require.Equal(t, int64(3), persistedVersion)
	require.True(t, deletedAt.Valid)
	require.Equal(t, 1, selfServiceGroupPostgresCount(t,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE group_id = $1", created.ID))
	require.Equal(t, 1, selfServiceGroupPostgresCount(t, `
SELECT COUNT(*)
FROM resource_authorization_events
WHERE group_id = $1 AND request_id = $2 AND resource_access_version = 3`,
		created.ID, fixture.requestBase+"-delete"))
}

func TestSelfServiceGroupRepositoryRejectsDeleteWhileGroupHasAccountPostgres(t *testing.T) {
	fixture := newSelfServiceGroupPostgresFixture(t)
	created := createSelfServiceGroupPostgres(t, fixture, "referenced")

	var accountID int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
INSERT INTO accounts (name, platform, type, credentials, status)
VALUES ($1, 'openai', 'apikey', '{}'::jsonb, 'active')
RETURNING id`, fixture.requestBase+"-account").Scan(&accountID))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(),
			"DELETE FROM account_groups WHERE account_id = $1", accountID)
		_, _ = integrationDB.ExecContext(context.Background(),
			"DELETE FROM accounts WHERE id = $1", accountID)
	})
	_, err := integrationDB.ExecContext(context.Background(), `
INSERT INTO account_groups (account_id, group_id)
VALUES ($1, $2)`, accountID, created.ID)
	require.NoError(t, err)

	err = fixture.repository.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		if lockErr := fixture.repository.LockActorAuthorization(txCtx, fixture.userID); lockErr != nil {
			return lockErr
		}
		locked, lockErr := fixture.repository.LockGroup(txCtx, created.ID)
		if lockErr != nil {
			return lockErr
		}
		_, deleteErr := fixture.repository.DeleteGroup(
			txCtx, created.ID, fixture.userID, locked.AccessVersion,
		)
		return deleteErr
	})
	require.ErrorIs(t, err, service.ErrSelfServiceGroupNotEmpty)

	var deletedAt sql.NullTime
	require.NoError(t, integrationDB.QueryRowContext(context.Background(),
		"SELECT deleted_at FROM groups WHERE id = $1", created.ID).Scan(&deletedAt))
	require.False(t, deletedAt.Valid)
}

func TestSelfServiceGroupBlockingReferencesCoverRuntimeConfigurationPostgres(t *testing.T) {
	testCases := []struct {
		name  string
		setup func(t *testing.T, fixture *selfServiceGroupPostgresFixture, groupID int64)
	}{
		{
			name: "channel monitor",
			setup: func(t *testing.T, _ *selfServiceGroupPostgresFixture, groupID int64) {
				var previous pq.Int64Array
				require.NoError(t, integrationDB.QueryRowContext(context.Background(),
					"SELECT group_ids FROM channel_monitor_v2_config WHERE id = 1").Scan(&previous))
				t.Cleanup(func() {
					_, err := integrationDB.ExecContext(context.Background(),
						"UPDATE channel_monitor_v2_config SET group_ids = $1 WHERE id = 1",
						pq.Array([]int64(previous)))
					require.NoError(t, err)
				})
				_, err := integrationDB.ExecContext(context.Background(),
					"UPDATE channel_monitor_v2_config SET group_ids = $1 WHERE id = 1",
					pq.Array([]int64{groupID}))
				require.NoError(t, err)
			},
		},
		{
			name: "account stats pricing rule",
			setup: func(t *testing.T, fixture *selfServiceGroupPostgresFixture, groupID int64) {
				var channelID int64
				require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
INSERT INTO channels (name, description, status)
VALUES ($1, '', 'active')
RETURNING id`, fixture.requestBase+"-pricing").Scan(&channelID))
				t.Cleanup(func() {
					_, err := integrationDB.ExecContext(context.Background(),
						"DELETE FROM channels WHERE id = $1", channelID)
					require.NoError(t, err)
				})
				_, err := integrationDB.ExecContext(context.Background(), `
INSERT INTO channel_account_stats_pricing_rules (channel_id, name, group_ids)
VALUES ($1, 'private group pricing', $2)`, channelID, pq.Array([]int64{groupID}))
				require.NoError(t, err)
			},
		},
		{
			name: "content moderation config",
			setup: func(t *testing.T, _ *selfServiceGroupPostgresFixture, groupID int64) {
				setSelfServiceGroupSettingReference(t, "content_moderation_config",
					fmt.Sprintf(`{"group_ids":[%d]}`, groupID))
			},
		},
		{
			name: "prompt audit config",
			setup: func(t *testing.T, _ *selfServiceGroupPostgresFixture, groupID int64) {
				setSelfServiceGroupSettingReference(t, "prompt_audit_config",
					fmt.Sprintf(`{"group_ids":[%d]}`, groupID))
			},
		},
		{
			name: "default subscription config",
			setup: func(t *testing.T, _ *selfServiceGroupPostgresFixture, groupID int64) {
				setSelfServiceGroupSettingReference(t, "default_subscriptions",
					fmt.Sprintf(`[{"group_id":%d,"validity_days":30}]`, groupID))
			},
		},
		{
			name: "auth source subscription config",
			setup: func(t *testing.T, _ *selfServiceGroupPostgresFixture, groupID int64) {
				setSelfServiceGroupSettingReference(t, "auth_source_default_email_subscriptions",
					fmt.Sprintf(`[{"group_id":%d,"validity_days":30}]`, groupID))
			},
		},
		{
			name: "unarchived announcement",
			setup: func(t *testing.T, fixture *selfServiceGroupPostgresFixture, groupID int64) {
				insertSelfServiceGroupAnnouncementReference(
					t, fixture, groupID, "active",
				)
			},
		},
		{
			name: "pending subscription payment",
			setup: func(t *testing.T, fixture *selfServiceGroupPostgresFixture, groupID int64) {
				insertSelfServiceGroupPaymentOrderReference(
					t, fixture, groupID, "PENDING",
				)
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newSelfServiceGroupPostgresFixture(t)
			created := createSelfServiceGroupPostgres(t, fixture, "runtime-reference")
			referenced, err := selfServiceGroupHasBlockingReferences(
				context.Background(), fixture.client, created.ID,
			)
			require.NoError(t, err)
			require.False(t, referenced)

			testCase.setup(t, fixture, created.ID)
			referenced, err = selfServiceGroupHasBlockingReferences(
				context.Background(), fixture.client, created.ID,
			)
			require.NoError(t, err)
			require.True(t, referenced)
		})
	}
}

func TestSelfServiceGroupBlockingReferencesIgnoreHistoricalConfigurationPostgres(t *testing.T) {
	fixture := newSelfServiceGroupPostgresFixture(t)
	created := createSelfServiceGroupPostgres(t, fixture, "historical-reference")
	insertSelfServiceGroupAnnouncementReference(t, fixture, created.ID, "archived")
	insertSelfServiceGroupPaymentOrderReference(t, fixture, created.ID, "COMPLETED")

	referenced, err := selfServiceGroupHasBlockingReferences(
		context.Background(), fixture.client, created.ID,
	)
	require.NoError(t, err)
	require.False(t, referenced)
}

func newSelfServiceGroupPostgresFixture(t *testing.T) *selfServiceGroupPostgresFixture {
	t.Helper()
	suffix := time.Now().UnixNano()
	client := testEntClient(t)
	user := mustCreateUser(t, client, &service.User{
		Email: fmt.Sprintf("self-service-group-%d@example.com", suffix),
	})
	fixture := &selfServiceGroupPostgresFixture{
		client:      client,
		repository:  NewSelfServiceGroupRepository(client),
		userID:      user.ID,
		requestBase: fmt.Sprintf("ssg-%d", suffix),
	}
	t.Cleanup(func() { cleanupSelfServiceGroupPostgresFixture(t, fixture) })
	return fixture
}

func createSelfServiceGroupPostgres(
	t testing.TB,
	fixture *selfServiceGroupPostgresFixture,
	name string,
) service.SelfServiceGroupState {
	t.Helper()
	var created service.SelfServiceGroupState
	err := fixture.repository.WithSerializableTx(context.Background(), func(txCtx context.Context) error {
		var createErr error
		created, createErr = fixture.repository.CreateGroup(txCtx, service.SelfServiceGroupCreateRecord{
			Name: name, Platform: service.PlatformOpenAI,
			OwnerUserID: fixture.userID, CreatorUserID: fixture.userID,
		})
		if createErr != nil {
			return createErr
		}
		return fixture.repository.AppendAuthorizationEvent(
			txCtx,
			selfServiceGroupPostgresEvent(
				created,
				fixture.userID,
				"group.created",
				fixture.requestBase+"-create-"+name,
				[]string{"configuration"},
			),
		)
	})
	require.NoError(t, err)
	return created
}

func selfServiceGroupPostgresEvent(
	state service.SelfServiceGroupState,
	userID int64,
	eventType string,
	requestID string,
	changedFields []string,
) service.ResourceAuthorizationEventRecord {
	ownerUserID := userID
	return service.ResourceAuthorizationEventRecord{
		Key: service.ResourceMutationKey{
			ResourceType: authz.ResourceTypeGroup,
			ResourceID:   state.ID,
		},
		OwnerUserID:           &ownerUserID,
		ActorKind:             authz.SubjectKindUser,
		ActorID:               userID,
		AuthMethod:            authz.AuthMethodJWT,
		EventType:             eventType,
		ResourceAccessVersion: state.AccessVersion,
		RequestID:             requestID,
		ChangedFields:         append([]string(nil), changedFields...),
	}
}

func installSelfServiceGroupOutboxFailureTrigger(t *testing.T, groupName string) {
	t.Helper()
	suffix := time.Now().UnixNano()
	functionName := fmt.Sprintf("fail_self_service_group_outbox_%d", suffix)
	triggerName := fmt.Sprintf("fail_self_service_group_outbox_trigger_%d", suffix)
	_, err := integrationDB.ExecContext(context.Background(), fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF NEW.event_type = %s
       AND EXISTS (
           SELECT 1
           FROM groups
           WHERE id = NEW.group_id
             AND name = %s
       ) THEN
        RAISE EXCEPTION 'forced self-service group outbox failure';
    END IF;
    RETURN NEW;
END;
$$`,
		functionName,
		pq.QuoteLiteral(service.SchedulerOutboxEventGroupChanged),
		pq.QuoteLiteral(groupName),
	))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON scheduler_outbox", triggerName,
		))
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
			"DROP FUNCTION IF EXISTS %s()", functionName,
		))
	})
	_, err = integrationDB.ExecContext(context.Background(), fmt.Sprintf(
		"CREATE TRIGGER %s BEFORE INSERT ON scheduler_outbox FOR EACH ROW EXECUTE FUNCTION %s()",
		triggerName,
		functionName,
	))
	require.NoError(t, err)
}

func cleanupSelfServiceGroupPostgresFixture(
	t testing.TB,
	fixture *selfServiceGroupPostgresFixture,
) {
	t.Helper()
	ctx := context.Background()
	_, err := integrationDB.ExecContext(ctx, `
DELETE FROM scheduler_outbox
WHERE group_id IN (
    SELECT id
    FROM groups
    WHERE owner_user_id = $1
)`, fixture.userID)
	require.NoError(t, err)

	tx, err := integrationDB.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "SET LOCAL session_replication_role = replica")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `
DELETE FROM resource_authorization_events
WHERE actor_user_id = $1 OR resource_owner_user_id = $1`, fixture.userID)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())

	_, err = integrationDB.ExecContext(ctx, "DELETE FROM groups WHERE owner_user_id = $1", fixture.userID)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, "DELETE FROM users WHERE id = $1", fixture.userID)
	require.NoError(t, err)
}

func setSelfServiceGroupSettingReference(t *testing.T, key, value string) {
	t.Helper()
	var previous string
	err := integrationDB.QueryRowContext(context.Background(),
		"SELECT value FROM settings WHERE key = $1", key).Scan(&previous)
	existed := err == nil
	require.True(t, existed || errors.Is(err, sql.ErrNoRows))
	t.Cleanup(func() {
		if existed {
			_, restoreErr := integrationDB.ExecContext(context.Background(), `
UPDATE settings
SET value = $2, updated_at = statement_timestamp()
WHERE key = $1`, key, previous)
			require.NoError(t, restoreErr)
			return
		}
		_, deleteErr := integrationDB.ExecContext(context.Background(),
			"DELETE FROM settings WHERE key = $1", key)
		require.NoError(t, deleteErr)
	})
	_, err = integrationDB.ExecContext(context.Background(), `
INSERT INTO settings (key, value, updated_at)
VALUES ($1, $2, statement_timestamp())
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`, key, value)
	require.NoError(t, err)
}

func insertSelfServiceGroupAnnouncementReference(
	t *testing.T,
	fixture *selfServiceGroupPostgresFixture,
	groupID int64,
	status string,
) {
	t.Helper()
	var announcementID int64
	targeting := fmt.Sprintf(
		`{"any_of":[{"all_of":[{"type":"subscription","operator":"in","group_ids":[%d]}]}]}`,
		groupID,
	)
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
INSERT INTO announcements (title, content, status, targeting, created_by, updated_by)
VALUES ($1, 'body', $2, $3::jsonb, $4, $4)
RETURNING id`, fixture.requestBase+"-announcement-"+status, status, targeting, fixture.userID).Scan(
		&announcementID,
	))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(),
			"DELETE FROM announcements WHERE id = $1", announcementID)
		require.NoError(t, err)
	})
}

func insertSelfServiceGroupPaymentOrderReference(
	t *testing.T,
	fixture *selfServiceGroupPostgresFixture,
	groupID int64,
	status string,
) {
	t.Helper()
	var orderID int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
INSERT INTO payment_orders (
    user_id,
    user_email,
    user_name,
    amount,
    pay_amount,
    recharge_code,
    out_trade_no,
    payment_type,
    payment_trade_no,
    order_type,
    subscription_group_id,
    subscription_days,
    status,
    expires_at,
    client_ip,
    src_host
) VALUES (
    $1, $2, 'self-service group test', 1, 1, $3, $4, 'test', '',
    'subscription', $5, 30, $6, statement_timestamp() + INTERVAL '1 day', '', ''
)
RETURNING id`,
		fixture.userID,
		fixture.requestBase+"@example.com",
		fixture.requestBase+"-recharge-"+status,
		fixture.requestBase+"-trade-"+status,
		groupID,
		status,
	).Scan(&orderID))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(),
			"DELETE FROM payment_orders WHERE id = $1", orderID)
		require.NoError(t, err)
	})
}

func selfServiceGroupPostgresCount(t testing.TB, query string, args ...any) int {
	t.Helper()
	var count int
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), query, args...).Scan(&count))
	return count
}
