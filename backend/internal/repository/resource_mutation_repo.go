package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type resourceMutationRepository struct {
	client *dbent.Client
}

func NewResourceMutationRepository(client *dbent.Client) service.ResourceMutationRepository {
	return &resourceMutationRepository{client: client}
}

func (r *resourceMutationRepository) WithSerializableTx(
	ctx context.Context,
	fn func(txCtx context.Context) error,
) error {
	if r == nil || r.client == nil || ctx == nil || fn == nil {
		return service.ErrResourceMutationUnavailable
	}
	if dbent.TxFromContext(ctx) != nil {
		return service.ErrResourceMutationUnavailable
	}
	tx, err := r.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return translateResourceMutationTxError(err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := fn(txCtx); err != nil {
		return translateResourceMutationTxError(err)
	}
	return translateResourceMutationTxError(tx.Commit())
}

func translateResourceMutationTxError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pq.Error
	if errors.As(err, &pgErr) && (pgErr.Code == "40001" || pgErr.Code == "40P01") {
		return service.ErrResourceMutationConflict.WithCause(err)
	}
	return err
}

func (r *resourceMutationRepository) LockActorAuthorization(
	ctx context.Context,
	kind authz.SubjectKind,
	id int64,
) error {
	if id <= 0 {
		return service.ErrResourceMutationUnavailable
	}
	client := clientFromContext(ctx, r.client)
	var subjectQuery, roleQuery string
	switch kind {
	case authz.SubjectKindUser:
		subjectQuery = `SELECT id FROM users WHERE id = $1 FOR UPDATE`
		roleQuery = `
SELECT r.id
FROM user_roles ur
JOIN roles r ON r.id = ur.role_id
WHERE ur.user_id = $1
ORDER BY r.id
FOR UPDATE OF ur, r`
	case authz.SubjectKindServicePrincipal:
		subjectQuery = `SELECT id FROM service_principals WHERE id = $1 FOR UPDATE`
		roleQuery = `
SELECT r.id
FROM service_principal_roles spr
JOIN roles r ON r.id = spr.role_id
WHERE spr.service_principal_id = $1
ORDER BY r.id
FOR UPDATE OF spr, r`
	default:
		return service.ErrResourceMutationUnavailable
	}
	if err := consumeResourceMutationLockRows(ctx, client, subjectQuery, id); err != nil {
		return fmt.Errorf("lock resource mutation subject: %w", err)
	}
	if err := consumeResourceMutationLockRows(ctx, client, roleQuery, id); err != nil {
		return fmt.Errorf("lock resource mutation roles: %w", err)
	}
	return nil
}

func consumeResourceMutationLockRows(
	ctx context.Context,
	queryer interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	},
	query string,
	args ...any,
) error {
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var ignored int64
		if err := rows.Scan(&ignored); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (r *resourceMutationRepository) LockResources(
	ctx context.Context,
	keys []service.ResourceMutationKey,
) (map[service.ResourceMutationKey]service.ResourceMutationState, error) {
	client := clientFromContext(ctx, r.client)
	keys = normalizedResourceMutationKeys(keys)
	states := make(map[service.ResourceMutationKey]service.ResourceMutationState, len(keys))
	for _, key := range keys {
		table, err := resourceMutationTable(key.ResourceType)
		if err != nil {
			return nil, err
		}
		rows, err := client.QueryContext(ctx, fmt.Sprintf(`
SELECT owner_user_id, access_version, deleted_at IS NOT NULL
FROM %s
WHERE id = $1
FOR UPDATE`, table), key.ResourceID)
		if err != nil {
			return nil, err
		}
		var state service.ResourceMutationState
		if rows.Next() {
			state.Key = key
			if err := rows.Scan(&state.OwnerUserID, &state.AccessVersion, &state.Deleted); err != nil {
				_ = rows.Close()
				return nil, err
			}
			states[key] = state
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return states, nil
}

func (r *resourceMutationRepository) IncrementAccessVersions(
	ctx context.Context,
	keys []service.ResourceMutationKey,
) (map[service.ResourceMutationKey]service.ResourceMutationState, error) {
	client := clientFromContext(ctx, r.client)
	keys = normalizedResourceMutationKeys(keys)
	states := make(map[service.ResourceMutationKey]service.ResourceMutationState, len(keys))
	for _, key := range keys {
		table, err := resourceMutationTable(key.ResourceType)
		if err != nil {
			return nil, err
		}
		rows, err := client.QueryContext(ctx, fmt.Sprintf(`
UPDATE %s
SET access_version = access_version + 1,
    updated_at = CURRENT_TIMESTAMP
WHERE id = $1
RETURNING owner_user_id, access_version, deleted_at IS NOT NULL`, table), key.ResourceID)
		if err != nil {
			return nil, err
		}
		if !rows.Next() {
			rowErr := rows.Err()
			_ = rows.Close()
			if rowErr != nil {
				return nil, rowErr
			}
			return nil, service.ErrResourceMutationConflict
		}
		state := service.ResourceMutationState{Key: key}
		if err := rows.Scan(&state.OwnerUserID, &state.AccessVersion, &state.Deleted); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if rows.Next() {
			_ = rows.Close()
			return nil, fmt.Errorf("resource mutation version update returned multiple rows")
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
		states[key] = state
	}
	return states, nil
}

func (r *resourceMutationRepository) AppendAuthorizationEvents(
	ctx context.Context,
	events []service.ResourceAuthorizationEventRecord,
) error {
	return appendResourceAuthorizationEvents(ctx, clientFromContext(ctx, r.client), events)
}

func appendResourceAuthorizationEvents(
	ctx context.Context,
	client *dbent.Client,
	events []service.ResourceAuthorizationEventRecord,
) error {
	if client == nil {
		return service.ErrResourceMutationUnavailable
	}
	for _, event := range events {
		if !event.Key.Valid() || event.ActorID <= 0 || event.ResourceAccessVersion <= 0 {
			return service.ErrResourceMutationUnavailable
		}
		var accountID, groupID, actorUserID, actorServicePrincipalID *int64
		resourceID := event.Key.ResourceID
		switch event.Key.ResourceType {
		case authz.ResourceTypeAccount:
			accountID = &resourceID
		case authz.ResourceTypeGroup:
			groupID = &resourceID
		default:
			return service.ErrResourceMutationUnavailable
		}
		actorID := event.ActorID
		switch event.ActorKind {
		case authz.SubjectKindUser:
			actorUserID = &actorID
		case authz.SubjectKindServicePrincipal:
			actorServicePrincipalID = &actorID
		default:
			return service.ErrResourceMutationUnavailable
		}
		details, err := json.Marshal(map[string]any{
			"changed_fields": event.ChangedFields,
			"result":         "success",
		})
		if err != nil {
			return err
		}
		if _, err := client.ExecContext(ctx, `
INSERT INTO resource_authorization_events (
    account_id,
    group_id,
    resource_owner_user_id,
    actor_user_id,
    actor_service_principal_id,
    auth_method,
    event_type,
    resource_access_version,
    request_id,
    details
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10::jsonb)`,
			accountID,
			groupID,
			event.OwnerUserID,
			actorUserID,
			actorServicePrincipalID,
			string(event.AuthMethod),
			event.EventType,
			event.ResourceAccessVersion,
			event.RequestID,
			string(details),
		); err != nil {
			return err
		}
	}
	return nil
}

func resourceMutationTable(resourceType authz.ResourceType) (string, error) {
	switch resourceType {
	case authz.ResourceTypeAccount:
		return "accounts", nil
	case authz.ResourceTypeGroup:
		return "groups", nil
	default:
		return "", service.ErrResourceMutationUnavailable
	}
}

func normalizedResourceMutationKeys(keys []service.ResourceMutationKey) []service.ResourceMutationKey {
	set := make(map[service.ResourceMutationKey]struct{}, len(keys))
	for _, key := range keys {
		if key.Valid() {
			set[key] = struct{}{}
		}
	}
	result := make([]service.ResourceMutationKey, 0, len(set))
	for key := range set {
		result = append(result, key)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ResourceType != result[j].ResourceType {
			return result[i].ResourceType < result[j].ResourceType
		}
		return result[i].ResourceID < result[j].ResourceID
	})
	return result
}
