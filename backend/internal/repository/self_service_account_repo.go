package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const (
	selfServiceAccountDefaultConcurrency = 1
	selfServiceAccountDefaultPriority    = 50
)

type selfServiceAccountRepository struct {
	client *dbent.Client
}

func NewSelfServiceAccountRepository(client *dbent.Client) service.SelfServiceAccountRepository {
	return &selfServiceAccountRepository{client: client}
}

func (r *selfServiceAccountRepository) WithSerializableTx(
	ctx context.Context,
	fn func(txCtx context.Context) error,
) error {
	if r == nil || r.client == nil || ctx == nil || fn == nil || dbent.TxFromContext(ctx) != nil {
		return service.ErrSelfServiceAccountUnavailable
	}
	tx, err := r.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return translateSelfServiceAccountTxError(err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := fn(txCtx); err != nil {
		return translateSelfServiceAccountTxError(err)
	}
	return translateSelfServiceAccountTxError(tx.Commit())
}

func translateSelfServiceAccountTxError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pq.Error
	if errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01") {
		return service.ErrSelfServiceAccountConflict.WithCause(err)
	}
	return err
}

func (r *selfServiceAccountRepository) LockActorAuthorization(
	ctx context.Context,
	userID int64,
) error {
	if r == nil || r.client == nil || ctx == nil || dbent.TxFromContext(ctx) == nil || userID <= 0 {
		return service.ErrSelfServiceAccountUnavailable
	}
	client := clientFromContext(ctx, r.client)
	if err := consumeResourceMutationLockRows(
		ctx,
		client,
		`SELECT id FROM users WHERE id = $1 FOR UPDATE`,
		userID,
	); err != nil {
		return fmt.Errorf("lock self-service account actor: %w", err)
	}
	if err := consumeResourceMutationLockRows(ctx, client, `
SELECT r.id
FROM user_roles AS ur
JOIN roles AS r ON r.id = ur.role_id
WHERE ur.user_id = $1
ORDER BY r.id, ur.id
FOR UPDATE OF ur, r`, userID); err != nil {
		return fmt.Errorf("lock self-service account actor roles: %w", err)
	}
	return nil
}

func (r *selfServiceAccountRepository) LockAccount(
	ctx context.Context,
	accountID int64,
) (service.SelfServiceAccountState, error) {
	if r == nil || r.client == nil || ctx == nil || dbent.TxFromContext(ctx) == nil || accountID <= 0 {
		return service.SelfServiceAccountState{}, service.ErrSelfServiceAccountUnavailable
	}
	rows, err := clientFromContext(ctx, r.client).QueryContext(ctx, `
SELECT
    id,
    name,
    platform,
    type,
    status,
    COALESCE(credentials <> '{}'::jsonb, FALSE),
    owner_user_id,
    public_access_level,
    access_version,
    deleted_at IS NOT NULL,
    created_at,
    updated_at
FROM accounts
WHERE id = $1
FOR UPDATE`, accountID)
	if err != nil {
		return service.SelfServiceAccountState{}, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return service.SelfServiceAccountState{}, err
		}
		return service.SelfServiceAccountState{}, service.ErrAccountNotFound
	}
	state, err := scanSelfServiceAccountState(rows)
	if err != nil {
		return service.SelfServiceAccountState{}, err
	}
	if rows.Next() {
		return service.SelfServiceAccountState{}, fmt.Errorf("self-service account lock returned multiple rows")
	}
	if err := rows.Err(); err != nil {
		return service.SelfServiceAccountState{}, err
	}
	return state, nil
}

func (r *selfServiceAccountRepository) CreateAccount(
	ctx context.Context,
	input service.SelfServiceAccountCreateRecord,
) (service.SelfServiceAccountState, error) {
	if r == nil || r.client == nil || ctx == nil || dbent.TxFromContext(ctx) == nil ||
		!validSelfServiceAccountCreateRecord(input) {
		return service.SelfServiceAccountState{}, service.ErrSelfServiceAccountUnavailable
	}
	ownerUserID := input.OwnerUserID
	creatorUserID := input.CreatorUserID
	account := &service.Account{
		Name:               input.Name,
		Platform:           input.Platform,
		Type:               input.AccountType,
		Credentials:        map[string]any{"api_key": input.APIKey},
		Extra:              map[string]any{},
		Concurrency:        selfServiceAccountDefaultConcurrency,
		Priority:           selfServiceAccountDefaultPriority,
		Status:             service.StatusActive,
		AutoPauseOnExpired: true,
		OwnerUserID:        &ownerUserID,
		CreatedByUserID:    &creatorUserID,
		AccessVersion:      1,
		Schedulable:        false,
	}
	client := clientFromContext(ctx, r.client)
	if err := createAccountRecord(ctx, client, account); err != nil {
		return service.SelfServiceAccountState{}, err
	}
	if err := enqueueSchedulerOutbox(
		ctx,
		client,
		service.SchedulerOutboxEventAccountChanged,
		&account.ID,
		nil,
		nil,
	); err != nil {
		return service.SelfServiceAccountState{}, err
	}
	return service.SelfServiceAccountState{
		AccountListItem: service.AccountListItem{
			ID:                   account.ID,
			Name:                 account.Name,
			Platform:             account.Platform,
			Type:                 account.Type,
			Status:               account.Status,
			CredentialConfigured: true,
			OwnerUserID:          cloneSelfServiceInt64Pointer(account.OwnerUserID),
			CreatedAt:            account.CreatedAt,
			UpdatedAt:            account.UpdatedAt,
		},
		AccessVersion: account.AccessVersion,
	}, nil
}

func validSelfServiceAccountCreateRecord(input service.SelfServiceAccountCreateRecord) bool {
	if input.OwnerUserID <= 0 || input.CreatorUserID != input.OwnerUserID ||
		input.Name == "" || input.AccountType != service.AccountTypeAPIKey ||
		input.APIKey == "" || !selfServiceAccountCandidatePlatform(input.Platform) ||
		!utf8.ValidString(input.Name) || !utf8.ValidString(input.APIKey) ||
		strings.TrimSpace(input.Name) != input.Name || strings.TrimSpace(input.APIKey) != input.APIKey {
		return false
	}
	for _, character := range input.Name {
		if unicode.IsControl(character) {
			return false
		}
	}
	for _, character := range input.APIKey {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func selfServiceAccountCandidatePlatform(platform string) bool {
	switch platform {
	case service.PlatformOpenAI, service.PlatformAnthropic, service.PlatformGemini:
		return true
	default:
		return false
	}
}

func (r *selfServiceAccountRepository) RenameAccount(
	ctx context.Context,
	accountID int64,
	ownerUserID int64,
	expectedAccessVersion int64,
	name string,
) (service.SelfServiceAccountState, error) {
	if r == nil || r.client == nil || ctx == nil || dbent.TxFromContext(ctx) == nil ||
		accountID <= 0 || ownerUserID <= 0 || expectedAccessVersion <= 0 || name == "" {
		return service.SelfServiceAccountState{}, service.ErrSelfServiceAccountUnavailable
	}
	client := clientFromContext(ctx, r.client)
	rows, err := client.QueryContext(ctx, `
UPDATE accounts
SET name = $1,
    access_version = access_version + 1,
    updated_at = statement_timestamp()
WHERE id = $2
  AND owner_user_id = $3
  AND access_version = $4
  AND deleted_at IS NULL
RETURNING
    id,
    name,
    platform,
    type,
    status,
    COALESCE(credentials <> '{}'::jsonb, FALSE),
    owner_user_id,
    public_access_level,
    access_version,
    FALSE,
    created_at,
    updated_at`, name, accountID, ownerUserID, expectedAccessVersion)
	if err != nil {
		return service.SelfServiceAccountState{}, err
	}
	state, err := consumeSelfServiceAccountMutation(rows)
	if err != nil {
		return service.SelfServiceAccountState{}, err
	}
	if err := enqueueSchedulerOutbox(
		ctx,
		client,
		service.SchedulerOutboxEventAccountChanged,
		&accountID,
		nil,
		nil,
	); err != nil {
		return service.SelfServiceAccountState{}, err
	}
	return state, nil
}

func (r *selfServiceAccountRepository) DeleteAccount(
	ctx context.Context,
	accountID int64,
	ownerUserID int64,
	expectedAccessVersion int64,
) (service.SelfServiceAccountState, error) {
	if r == nil || r.client == nil || ctx == nil || dbent.TxFromContext(ctx) == nil ||
		accountID <= 0 || ownerUserID <= 0 || expectedAccessVersion <= 0 {
		return service.SelfServiceAccountState{}, service.ErrSelfServiceAccountUnavailable
	}
	client := clientFromContext(ctx, r.client)
	groupIDs, err := listSelfServiceAccountGroupIDs(ctx, client, accountID)
	if err != nil {
		return service.SelfServiceAccountState{}, err
	}
	if _, err := client.ExecContext(ctx, `DELETE FROM account_groups WHERE account_id = $1`, accountID); err != nil {
		return service.SelfServiceAccountState{}, err
	}
	if _, err := client.ExecContext(ctx, `DELETE FROM scheduled_test_plans WHERE account_id = $1`, accountID); err != nil {
		return service.SelfServiceAccountState{}, err
	}
	rows, err := client.QueryContext(ctx, `
UPDATE accounts
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
    platform,
    type,
    status,
    COALESCE(credentials <> '{}'::jsonb, FALSE),
    owner_user_id,
    public_access_level,
    access_version,
    TRUE,
    created_at,
    updated_at`, accountID, ownerUserID, expectedAccessVersion)
	if err != nil {
		return service.SelfServiceAccountState{}, err
	}
	state, err := consumeSelfServiceAccountMutation(rows)
	if err != nil {
		return service.SelfServiceAccountState{}, err
	}
	if err := enqueueSchedulerOutbox(
		ctx,
		client,
		service.SchedulerOutboxEventAccountChanged,
		&accountID,
		nil,
		buildSchedulerGroupPayload(groupIDs),
	); err != nil {
		return service.SelfServiceAccountState{}, err
	}
	return state, nil
}

func listSelfServiceAccountGroupIDs(
	ctx context.Context,
	client *dbent.Client,
	accountID int64,
) ([]int64, error) {
	rows, err := client.QueryContext(ctx, `
SELECT group_id
FROM account_groups
WHERE account_id = $1
ORDER BY group_id`, accountID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	groupIDs := make([]int64, 0)
	for rows.Next() {
		var groupID int64
		if err := rows.Scan(&groupID); err != nil {
			return nil, err
		}
		if groupID > 0 {
			groupIDs = append(groupIDs, groupID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(groupIDs, func(i, j int) bool { return groupIDs[i] < groupIDs[j] })
	return groupIDs, nil
}

func consumeSelfServiceAccountMutation(
	rows *sql.Rows,
) (service.SelfServiceAccountState, error) {
	if rows == nil {
		return service.SelfServiceAccountState{}, service.ErrSelfServiceAccountUnavailable
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return service.SelfServiceAccountState{}, err
		}
		return service.SelfServiceAccountState{}, service.ErrSelfServiceAccountConflict
	}
	state, err := scanSelfServiceAccountState(rows)
	if err != nil {
		return service.SelfServiceAccountState{}, err
	}
	if rows.Next() {
		return service.SelfServiceAccountState{}, fmt.Errorf("self-service account mutation returned multiple rows")
	}
	if err := rows.Err(); err != nil {
		return service.SelfServiceAccountState{}, err
	}
	return state, nil
}

type selfServiceAccountScanner interface {
	Scan(dest ...any) error
}

func scanSelfServiceAccountState(
	scanner selfServiceAccountScanner,
) (service.SelfServiceAccountState, error) {
	var (
		state             service.SelfServiceAccountState
		publicAccessLevel *string
	)
	if err := scanner.Scan(
		&state.ID,
		&state.Name,
		&state.Platform,
		&state.Type,
		&state.Status,
		&state.CredentialConfigured,
		&state.OwnerUserID,
		&publicAccessLevel,
		&state.AccessVersion,
		&state.Deleted,
		&state.CreatedAt,
		&state.UpdatedAt,
	); err != nil {
		return service.SelfServiceAccountState{}, err
	}
	if publicAccessLevel != nil {
		level, ok := authz.ParseAccessLevel(*publicAccessLevel)
		if !ok || !level.AllowedAsPublic() {
			return service.SelfServiceAccountState{}, authz.ErrInvalidPolicySnapshot
		}
		state.PublicAccessLevel = &level
	}
	if state.ID <= 0 || state.Name == "" || state.Platform == "" || state.Type == "" ||
		state.Status == "" || state.AccessVersion <= 0 ||
		(state.OwnerUserID != nil && *state.OwnerUserID <= 0) {
		return service.SelfServiceAccountState{}, authz.ErrInvalidPolicySnapshot
	}
	return state, nil
}

func (r *selfServiceAccountRepository) AppendAuthorizationEvent(
	ctx context.Context,
	event service.ResourceAuthorizationEventRecord,
) error {
	if r == nil || r.client == nil || ctx == nil || dbent.TxFromContext(ctx) == nil {
		return service.ErrSelfServiceAccountUnavailable
	}
	return appendResourceAuthorizationEvents(
		ctx,
		clientFromContext(ctx, r.client),
		[]service.ResourceAuthorizationEventRecord{event},
	)
}

func cloneSelfServiceInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}
