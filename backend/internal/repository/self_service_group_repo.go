package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const (
	selfServiceGroupMaxNameRunes        = 100
	selfServiceGroupMaxDescriptionRunes = 2000
)

type selfServiceGroupRepository struct {
	client *dbent.Client
}

func NewSelfServiceGroupRepository(client *dbent.Client) service.SelfServiceGroupRepository {
	return &selfServiceGroupRepository{client: client}
}

func (r *selfServiceGroupRepository) WithSerializableTx(
	ctx context.Context,
	fn func(txCtx context.Context) error,
) error {
	if r == nil || r.client == nil || ctx == nil || fn == nil || dbent.TxFromContext(ctx) != nil {
		return service.ErrSelfServiceGroupUnavailable
	}
	tx, err := r.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return translateSelfServiceGroupTxError(err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := fn(txCtx); err != nil {
		return translateSelfServiceGroupTxError(err)
	}
	return translateSelfServiceGroupTxError(tx.Commit())
}

func translateSelfServiceGroupTxError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pq.Error
	if errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01") {
		return service.ErrSelfServiceGroupConflict.WithCause(err)
	}
	return err
}

func (r *selfServiceGroupRepository) LockActorAuthorization(
	ctx context.Context,
	userID int64,
) error {
	if r == nil || r.client == nil || ctx == nil || dbent.TxFromContext(ctx) == nil || userID <= 0 {
		return service.ErrSelfServiceGroupUnavailable
	}
	client := clientFromContext(ctx, r.client)
	if err := consumeResourceMutationLockRows(
		ctx,
		client,
		`SELECT id FROM users WHERE id = $1 FOR UPDATE`,
		userID,
	); err != nil {
		return fmt.Errorf("lock self-service group actor: %w", err)
	}
	if err := consumeResourceMutationLockRows(ctx, client, `
SELECT r.id
FROM user_roles AS ur
JOIN roles AS r ON r.id = ur.role_id
WHERE ur.user_id = $1
ORDER BY r.id, ur.id
FOR UPDATE OF ur, r`, userID); err != nil {
		return fmt.Errorf("lock self-service group actor roles: %w", err)
	}
	return nil
}

func (r *selfServiceGroupRepository) LockGroup(
	ctx context.Context,
	groupID int64,
) (service.SelfServiceGroupState, error) {
	if r == nil || r.client == nil || ctx == nil || dbent.TxFromContext(ctx) == nil || groupID <= 0 {
		return service.SelfServiceGroupState{}, service.ErrSelfServiceGroupUnavailable
	}
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, `
SELECT
    id,
    name,
    COALESCE(description, ''),
    platform,
    status,
    owner_user_id,
    created_by_user_id,
    public_access_level,
    access_version,
    authorization_mode,
    is_exclusive,
    deleted_at IS NOT NULL,
    created_at,
    updated_at
FROM groups
WHERE id = $1
FOR UPDATE`, groupID)
	if err != nil {
		return service.SelfServiceGroupState{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return service.SelfServiceGroupState{}, err
		}
		return service.SelfServiceGroupState{}, service.ErrGroupNotFound
	}
	state, err := scanSelfServiceGroupState(rows)
	if err != nil {
		return service.SelfServiceGroupState{}, err
	}
	if rows.Next() {
		return service.SelfServiceGroupState{}, fmt.Errorf("self-service group lock returned multiple rows")
	}
	if err := rows.Err(); err != nil {
		return service.SelfServiceGroupState{}, err
	}
	return state, nil
}

func (r *selfServiceGroupRepository) CreateGroup(
	ctx context.Context,
	input service.SelfServiceGroupCreateRecord,
) (service.SelfServiceGroupState, error) {
	if r == nil || r.client == nil || ctx == nil || dbent.TxFromContext(ctx) == nil ||
		!validSelfServiceGroupCreateRecord(input) {
		return service.SelfServiceGroupState{}, service.ErrSelfServiceGroupUnavailable
	}
	return createSelfServiceGroupRecord(ctx, clientFromContext(ctx, r.client), input)
}

func createSelfServiceGroupRecord(
	ctx context.Context,
	client *dbent.Client,
	input service.SelfServiceGroupCreateRecord,
) (service.SelfServiceGroupState, error) {
	if ctx == nil || client == nil || dbent.TxFromContext(ctx) == nil ||
		!validSelfServiceGroupCreateRecord(input) {
		return service.SelfServiceGroupState{}, service.ErrSelfServiceGroupUnavailable
	}
	rows, err := client.QueryContext(ctx, `
INSERT INTO groups (
    name,
    description,
    platform,
    is_exclusive,
    status,
    owner_user_id,
    created_by_user_id,
    public_access_level,
    access_version,
    authorization_mode
) VALUES ($1, NULLIF($2, ''), $3, TRUE, $4, $5, $6, NULL, 1, 'legacy')
RETURNING
    id,
    name,
    COALESCE(description, ''),
    platform,
    status,
    owner_user_id,
    created_by_user_id,
    public_access_level,
    access_version,
    authorization_mode,
    is_exclusive,
    FALSE,
    created_at,
    updated_at`,
		input.Name,
		input.Description,
		input.Platform,
		service.StatusActive,
		input.OwnerUserID,
		input.CreatorUserID,
	)
	if err != nil {
		return service.SelfServiceGroupState{}, translatePersistenceError(err, nil, service.ErrGroupExists)
	}
	state, err := consumeSelfServiceGroupMutation(rows)
	if err != nil {
		return service.SelfServiceGroupState{}, err
	}
	if err := enqueueSchedulerOutbox(
		ctx,
		client,
		service.SchedulerOutboxEventGroupChanged,
		nil,
		&state.ID,
		nil,
	); err != nil {
		return service.SelfServiceGroupState{}, err
	}
	return state, nil
}

func validSelfServiceGroupCreateRecord(input service.SelfServiceGroupCreateRecord) bool {
	if input.OwnerUserID <= 0 || input.CreatorUserID != input.OwnerUserID ||
		input.Name == "" || !selfServiceGroupCandidatePlatform(input.Platform) ||
		!utf8.ValidString(input.Name) || !utf8.ValidString(input.Description) ||
		strings.TrimSpace(input.Name) != input.Name ||
		strings.TrimSpace(input.Description) != input.Description ||
		len([]rune(input.Name)) > selfServiceGroupMaxNameRunes ||
		len([]rune(input.Description)) > selfServiceGroupMaxDescriptionRunes {
		return false
	}
	for _, character := range input.Name {
		if unicode.IsControl(character) {
			return false
		}
	}
	for _, character := range input.Description {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return false
		}
	}
	return true
}

func selfServiceGroupCandidatePlatform(platform string) bool {
	switch platform {
	case service.PlatformOpenAI, service.PlatformAnthropic, service.PlatformGemini:
		return true
	default:
		return false
	}
}

func (r *selfServiceGroupRepository) UpdateGroup(
	ctx context.Context,
	groupID int64,
	ownerUserID int64,
	expectedAccessVersion int64,
	name string,
	description string,
) (service.SelfServiceGroupState, error) {
	if r == nil || r.client == nil || ctx == nil || dbent.TxFromContext(ctx) == nil ||
		groupID <= 0 || ownerUserID <= 0 || expectedAccessVersion <= 0 ||
		!validSelfServiceGroupMutableFields(name, description) {
		return service.SelfServiceGroupState{}, service.ErrSelfServiceGroupUnavailable
	}
	client := clientFromContext(ctx, r.client)
	rows, err := client.QueryContext(ctx, `
UPDATE groups
SET name = $1,
    description = NULLIF($2, ''),
    access_version = access_version + 1,
    updated_at = statement_timestamp()
WHERE id = $3
  AND owner_user_id = $4
  AND access_version = $5
  AND deleted_at IS NULL
RETURNING
    id,
    name,
    COALESCE(description, ''),
    platform,
    status,
    owner_user_id,
    created_by_user_id,
    public_access_level,
    access_version,
    authorization_mode,
    is_exclusive,
    FALSE,
    created_at,
    updated_at`, name, description, groupID, ownerUserID, expectedAccessVersion)
	if err != nil {
		return service.SelfServiceGroupState{}, translatePersistenceError(err, nil, service.ErrGroupExists)
	}
	state, err := consumeSelfServiceGroupMutation(rows)
	if err != nil {
		return service.SelfServiceGroupState{}, err
	}
	if err := enqueueSchedulerOutbox(
		ctx,
		client,
		service.SchedulerOutboxEventGroupChanged,
		nil,
		&groupID,
		nil,
	); err != nil {
		return service.SelfServiceGroupState{}, err
	}
	return state, nil
}

func validSelfServiceGroupMutableFields(name, description string) bool {
	return validSelfServiceGroupCreateRecord(service.SelfServiceGroupCreateRecord{
		Name:          name,
		Description:   description,
		Platform:      service.PlatformOpenAI,
		OwnerUserID:   1,
		CreatorUserID: 1,
	})
}

func (r *selfServiceGroupRepository) DeleteGroup(
	ctx context.Context,
	groupID int64,
	ownerUserID int64,
	expectedAccessVersion int64,
) (service.SelfServiceGroupState, error) {
	if r == nil || r.client == nil || ctx == nil || dbent.TxFromContext(ctx) == nil ||
		groupID <= 0 || ownerUserID <= 0 || expectedAccessVersion <= 0 {
		return service.SelfServiceGroupState{}, service.ErrSelfServiceGroupUnavailable
	}
	client := clientFromContext(ctx, r.client)
	referenced, err := selfServiceGroupHasBlockingReferences(ctx, client, groupID)
	if err != nil {
		return service.SelfServiceGroupState{}, err
	}
	if referenced {
		return service.SelfServiceGroupState{}, service.ErrSelfServiceGroupNotEmpty
	}
	rows, err := client.QueryContext(ctx, `
UPDATE groups
SET access_version = access_version + 1,
    deleted_at = statement_timestamp(),
    updated_at = statement_timestamp()
WHERE id = $1
  AND owner_user_id = $2
  AND access_version = $3
  AND deleted_at IS NULL
RETURNING
    id,
    name,
    COALESCE(description, ''),
    platform,
    status,
    owner_user_id,
    created_by_user_id,
    public_access_level,
    access_version,
    authorization_mode,
    is_exclusive,
    TRUE,
    created_at,
    updated_at`, groupID, ownerUserID, expectedAccessVersion)
	if err != nil {
		return service.SelfServiceGroupState{}, err
	}
	state, err := consumeSelfServiceGroupMutation(rows)
	if err != nil {
		return service.SelfServiceGroupState{}, err
	}
	if err := enqueueSchedulerOutbox(
		ctx,
		client,
		service.SchedulerOutboxEventGroupChanged,
		nil,
		&groupID,
		nil,
	); err != nil {
		return service.SelfServiceGroupState{}, err
	}
	return state, nil
}

func selfServiceGroupHasBlockingReferences(
	ctx context.Context,
	client *dbent.Client,
	groupID int64,
) (bool, error) {
	rows, err := client.QueryContext(ctx, `
SELECT EXISTS (
    SELECT 1 FROM account_groups WHERE group_id = $1
    UNION ALL
    SELECT 1 FROM api_keys WHERE group_id = $1 AND deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM user_subscriptions WHERE group_id = $1 AND deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM user_allowed_groups WHERE group_id = $1
    UNION ALL
    SELECT 1 FROM user_group_rate_multipliers WHERE group_id = $1
    UNION ALL
    SELECT 1 FROM channel_groups WHERE group_id = $1
    UNION ALL
    SELECT 1 FROM composite_model_routes WHERE group_id = $1 AND deleted_at IS NULL
    UNION ALL
    SELECT 1 FROM group_access_grants WHERE group_id = $1
    UNION ALL
    SELECT 1 FROM groups
    WHERE id <> $1
      AND deleted_at IS NULL
      AND (fallback_group_id = $1 OR fallback_group_id_on_invalid_request = $1)
    UNION ALL
    SELECT 1 FROM redeem_codes WHERE group_id = $1
    UNION ALL
    SELECT 1 FROM subscription_plans WHERE group_id = $1
    UNION ALL
    SELECT 1 FROM channel_monitor_v2_config WHERE $1 = ANY(group_ids)
    UNION ALL
    SELECT 1 FROM channel_account_stats_pricing_rules WHERE $1 = ANY(group_ids)
    UNION ALL
    SELECT 1 FROM settings
    WHERE key IN ('content_moderation_config', 'prompt_audit_config')
      AND jsonb_path_exists(
          value::jsonb,
          '$.group_ids[*] ? (@ == $group_id)',
          jsonb_build_object('group_id', $1)
      )
    UNION ALL
    SELECT 1 FROM settings
    WHERE (
        key = 'default_subscriptions'
        OR key LIKE 'auth!_source!_default!_%!_subscriptions' ESCAPE '!'
    )
      AND jsonb_path_exists(
          value::jsonb,
          '$[*] ? (@.group_id == $group_id)',
          jsonb_build_object('group_id', $1)
      )
    UNION ALL
    SELECT 1 FROM announcements
    WHERE status IN ('draft', 'active')
      AND jsonb_path_exists(
          targeting,
          '$.any_of[*].all_of[*] ? (@.type == "subscription" && @.group_ids[*] == $group_id)',
          jsonb_build_object('group_id', $1)
      )
    UNION ALL
    SELECT 1 FROM payment_orders
    WHERE order_type = 'subscription'
      AND subscription_group_id = $1
      AND status IN ('PENDING', 'PAID', 'RECHARGING', 'FAILED')
)`, groupID)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, err
		}
		return false, service.ErrSelfServiceGroupUnavailable
	}
	var referenced bool
	if err := rows.Scan(&referenced); err != nil {
		return false, err
	}
	if rows.Next() {
		return false, fmt.Errorf("self-service group reference check returned multiple rows")
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return referenced, nil
}

func consumeSelfServiceGroupMutation(
	rows *sql.Rows,
) (service.SelfServiceGroupState, error) {
	if rows == nil {
		return service.SelfServiceGroupState{}, service.ErrSelfServiceGroupUnavailable
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return service.SelfServiceGroupState{}, err
		}
		return service.SelfServiceGroupState{}, service.ErrSelfServiceGroupConflict
	}
	state, err := scanSelfServiceGroupState(rows)
	if err != nil {
		return service.SelfServiceGroupState{}, err
	}
	if rows.Next() {
		return service.SelfServiceGroupState{}, fmt.Errorf("self-service group mutation returned multiple rows")
	}
	if err := rows.Err(); err != nil {
		return service.SelfServiceGroupState{}, err
	}
	return state, nil
}

type selfServiceGroupScanner interface {
	Scan(dest ...any) error
}

func scanSelfServiceGroupState(
	scanner selfServiceGroupScanner,
) (service.SelfServiceGroupState, error) {
	var (
		state             service.SelfServiceGroupState
		publicAccessLevel *string
	)
	if err := scanner.Scan(
		&state.ID,
		&state.Name,
		&state.Description,
		&state.Platform,
		&state.Status,
		&state.OwnerUserID,
		&state.CreatedByUserID,
		&publicAccessLevel,
		&state.AccessVersion,
		&state.AuthorizationMode,
		&state.IsExclusive,
		&state.Deleted,
		&state.CreatedAt,
		&state.UpdatedAt,
	); err != nil {
		return service.SelfServiceGroupState{}, err
	}
	if publicAccessLevel != nil {
		level, ok := authz.ParseAccessLevel(*publicAccessLevel)
		if !ok || !level.AllowedAsPublic() {
			return service.SelfServiceGroupState{}, authz.ErrInvalidPolicySnapshot
		}
		state.PublicAccessLevel = &level
	}
	if state.ID <= 0 || state.Name == "" || state.Platform == "" || state.Status == "" ||
		state.AccessVersion <= 0 || state.AuthorizationMode == "" ||
		(state.OwnerUserID != nil && *state.OwnerUserID <= 0) ||
		(state.CreatedByUserID != nil && *state.CreatedByUserID <= 0) {
		return service.SelfServiceGroupState{}, authz.ErrInvalidPolicySnapshot
	}
	return state, nil
}

func (r *selfServiceGroupRepository) AppendAuthorizationEvent(
	ctx context.Context,
	event service.ResourceAuthorizationEventRecord,
) error {
	if r == nil || r.client == nil || ctx == nil || dbent.TxFromContext(ctx) == nil {
		return service.ErrSelfServiceGroupUnavailable
	}
	return appendResourceAuthorizationEvents(
		ctx,
		clientFromContext(ctx, r.client),
		[]service.ResourceAuthorizationEventRecord{event},
	)
}
