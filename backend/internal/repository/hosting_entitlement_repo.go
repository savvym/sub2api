package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const (
	hostingEntitlementAuditMethod                 = "PUT"
	hostingEntitlementAuditPath                   = "/api/v1/admin/authorization/hosting-entitlements/:user_id"
	hostingEntitlementUserUniqueConstraint        = "user_hosting_entitlements_user_id_key"
	hostingEntitlementUserRoleUniqueConstraint    = "user_roles_user_role_key"
	hostingEntitlementSerializableIsolation       = "serializable"
	hostingEntitlementSerializableIsolationReason = "serializable transaction required"
)

type hostingEntitlementRepository struct {
	client *dbent.Client
}

func NewHostingEntitlementRepository(client *dbent.Client) service.HostingEntitlementRepository {
	return &hostingEntitlementRepository{client: client}
}

func (r *hostingEntitlementRepository) WithHostingEntitlementSnapshot(
	ctx context.Context,
	fn func(snapshotCtx context.Context) error,
) error {
	if r == nil || r.client == nil || ctx == nil || fn == nil {
		return service.ErrHostingEntitlementUnavailable
	}
	if dbent.TxFromContext(ctx) != nil {
		return fn(ctx)
	}

	tx, err := r.client.BeginTx(ctx, &sql.TxOptions{
		Isolation: sql.LevelRepeatableRead,
		ReadOnly:  true,
	})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := fn(txCtx); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *hostingEntitlementRepository) WithHostingEntitlementTx(
	ctx context.Context,
	fn func(txCtx context.Context) error,
) error {
	if r == nil || r.client == nil || ctx == nil || fn == nil {
		return service.ErrHostingEntitlementUnavailable
	}
	if dbent.TxFromContext(ctx) != nil {
		return translateHostingEntitlementTxError(fn(ctx))
	}

	tx, err := r.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return translateHostingEntitlementTxError(err)
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := fn(txCtx); err != nil {
		return translateHostingEntitlementTxError(err)
	}
	return translateHostingEntitlementTxError(tx.Commit())
}

func translateHostingEntitlementTxError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pq.Error
	if errors.As(err, &pgErr) {
		if pgErr.Code == "40001" || pgErr.Code == "40P01" ||
			(pgErr.Code == "23505" && hostingEntitlementCASConstraint(pgErr.Constraint)) {
			return service.ErrHostingEntitlementConflict.WithCause(err)
		}
	}
	return err
}

func hostingEntitlementCASConstraint(constraint string) bool {
	return constraint == hostingEntitlementUserUniqueConstraint ||
		constraint == hostingEntitlementUserRoleUniqueConstraint
}

func (r *hostingEntitlementRepository) LockHostingEntitlementSubjects(
	ctx context.Context,
	actorUserID int64,
	targetUserID int64,
) error {
	if dbent.TxFromContext(ctx) == nil || actorUserID <= 0 || targetUserID <= 0 {
		return service.ErrHostingEntitlementUnavailable
	}
	client := clientFromContext(ctx, r.client)
	for _, userID := range uniqueSortedInt64s([]int64{actorUserID, targetUserID}) {
		if err := consumeHostingLockRows(ctx, client,
			`SELECT id FROM users WHERE id = $1 ORDER BY id FOR UPDATE`, userID); err != nil {
			return fmt.Errorf("lock hosting entitlement user %d: %w", userID, err)
		}
	}

	if err := consumeHostingLockRows(ctx, client, `
SELECT ur.id
FROM user_roles AS ur
JOIN roles AS r ON r.id = ur.role_id
WHERE ur.user_id = $1
ORDER BY r.id, ur.id
FOR UPDATE OF ur, r`, actorUserID); err != nil {
		return fmt.Errorf("lock hosting entitlement actor roles: %w", err)
	}

	hosterRoleID, err := queryHostingRoleID(ctx, client, true)
	if err != nil {
		return err
	}
	if err := consumeHostingLockRows(ctx, client, `
SELECT id
FROM user_roles
WHERE user_id = $1
  AND role_id = $2
FOR UPDATE`, targetUserID, hosterRoleID); err != nil {
		return fmt.Errorf("lock hoster assignment: %w", err)
	}
	if err := consumeHostingLockRows(ctx, client, `
SELECT id
FROM user_hosting_entitlements
WHERE user_id = $1
FOR UPDATE`, targetUserID); err != nil {
		return fmt.Errorf("lock hosting entitlement row: %w", err)
	}
	return nil
}

func (r *hostingEntitlementRepository) ReadHostingEntitlement(
	ctx context.Context,
	targetUserID int64,
) (service.HostingEntitlementRecord, error) {
	var record service.HostingEntitlementRecord
	if r == nil || r.client == nil || ctx == nil || targetUserID <= 0 {
		return record, service.ErrHostingEntitlementUnavailable
	}
	client := clientFromContext(ctx, r.client)
	rows, err := client.QueryContext(ctx, `
SELECT
    u.id,
    u.status = 'active',
    u.authz_version,
    COALESCE(hoster.assignment_exists, FALSE),
    COALESCE(hoster.permanent, FALSE),
    COALESCE(hoster.effective, FALSE),
    COALESCE(entitlement.account_limit, 0),
    COALESCE(entitlement.group_limit, 0),
    COALESCE(entitlement.version, 0),
    entitlement.created_by_user_id,
    entitlement.updated_by_user_id,
    entitlement.created_at,
    entitlement.updated_at,
    (SELECT COUNT(*) FROM accounts WHERE owner_user_id = u.id AND deleted_at IS NULL),
    (SELECT COUNT(*) FROM groups WHERE owner_user_id = u.id AND deleted_at IS NULL)
FROM users AS u
LEFT JOIN LATERAL (
    SELECT
        TRUE AS assignment_exists,
        ur.expires_at IS NULL AS permanent,
        ur.expires_at IS NULL OR ur.expires_at > statement_timestamp() AS effective
    FROM user_roles AS ur
    JOIN roles AS r ON r.id = ur.role_id
    WHERE ur.user_id = u.id
      AND r.code = 'hoster'
      AND r.is_system = TRUE
    LIMIT 1
) AS hoster ON TRUE
LEFT JOIN user_hosting_entitlements AS entitlement
    ON entitlement.user_id = u.id
WHERE u.id = $1
  AND u.deleted_at IS NULL`, targetUserID)
	if err != nil {
		return record, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return record, err
		}
		return record, service.ErrUserNotFound
	}

	var (
		createdBy sql.NullInt64
		updatedBy sql.NullInt64
		createdAt sql.NullTime
		updatedAt sql.NullTime
	)
	if err := rows.Scan(
		&record.UserID,
		&record.UserActive,
		&record.AuthzVersion,
		&record.HosterAssignmentExists,
		&record.HosterAssignmentPermanent,
		&record.Hoster,
		&record.AccountLimit,
		&record.GroupLimit,
		&record.Version,
		&createdBy,
		&updatedBy,
		&createdAt,
		&updatedAt,
		&record.AccountUsage,
		&record.GroupUsage,
	); err != nil {
		return service.HostingEntitlementRecord{}, err
	}
	if rows.Next() {
		return service.HostingEntitlementRecord{}, fmt.Errorf("hosting entitlement query returned multiple rows")
	}
	if err := rows.Err(); err != nil {
		return service.HostingEntitlementRecord{}, err
	}
	record.CreatedByUserID = nullableHostingInt64(createdBy)
	record.UpdatedByUserID = nullableHostingInt64(updatedBy)
	record.CreatedAt = nullableHostingTime(createdAt)
	record.UpdatedAt = nullableHostingTime(updatedAt)
	return record, nil
}

func (r *hostingEntitlementRepository) ApplyHostingEntitlement(
	ctx context.Context,
	input service.HostingEntitlementMutationInput,
) (service.HostingEntitlementMutationResult, error) {
	var result service.HostingEntitlementMutationResult
	if dbent.TxFromContext(ctx) == nil || input.ActorUserID <= 0 || input.TargetUserID <= 0 ||
		input.Current.UserID != input.TargetUserID || input.Current.Version < 0 ||
		input.AccountLimit < 0 || input.GroupLimit < 0 {
		return result, service.ErrHostingEntitlementUnavailable
	}
	client := clientFromContext(ctx, r.client)
	hosterRoleID, err := queryHostingRoleID(ctx, client, false)
	if err != nil {
		return result, err
	}

	roleChanged := false
	if input.Hoster {
		switch {
		case !input.Current.HosterAssignmentExists:
			mutation, insertErr := client.ExecContext(ctx, `
INSERT INTO user_roles (
    user_id,
    role_id,
    granted_by_user_id,
    created_at,
    updated_at
)
VALUES ($1, $2, $3, NOW(), NOW())`, input.TargetUserID, hosterRoleID, input.ActorUserID)
			if insertErr != nil {
				return result, insertErr
			}
			roleChanged, err = exactlyOneHostingMutation(mutation, "insert hoster assignment")
			if err != nil {
				return result, err
			}
		case !input.Current.HosterAssignmentPermanent:
			mutation, updateErr := client.ExecContext(ctx, `
UPDATE user_roles
SET granted_by_user_id = $1,
    granted_by_service_principal_id = NULL,
    expires_at = NULL,
    updated_at = NOW()
WHERE user_id = $2
  AND role_id = $3`, input.ActorUserID, input.TargetUserID, hosterRoleID)
			if updateErr != nil {
				return result, updateErr
			}
			roleChanged, err = exactlyOneHostingMutation(mutation, "stabilize hoster assignment")
			if err != nil {
				return result, err
			}
		}
	} else if input.Current.HosterAssignmentExists {
		mutation, deleteErr := client.ExecContext(ctx, `
DELETE FROM user_roles
WHERE user_id = $1
  AND role_id = $2`, input.TargetUserID, hosterRoleID)
		if deleteErr != nil {
			return result, deleteErr
		}
		roleChanged, err = exactlyOneHostingMutation(mutation, "delete hoster assignment")
		if err != nil {
			return result, err
		}
	}

	entitlementChanged := input.Current.Version == 0 ||
		input.Current.AccountLimit != input.AccountLimit ||
		input.Current.GroupLimit != input.GroupLimit ||
		roleChanged
	if !entitlementChanged {
		return result, nil
	}

	if input.Current.Version == 0 {
		mutation, insertErr := client.ExecContext(ctx, `
INSERT INTO user_hosting_entitlements (
    user_id,
    account_limit,
    group_limit,
    version,
    created_by_user_id,
    updated_by_user_id,
    created_at,
    updated_at
)
VALUES ($1, $2, $3, 1, $4, $4, NOW(), NOW())`,
			input.TargetUserID,
			input.AccountLimit,
			input.GroupLimit,
			input.ActorUserID,
		)
		if insertErr != nil {
			return result, insertErr
		}
		if _, err := exactlyOneHostingMutation(mutation, "insert hosting entitlement"); err != nil {
			return result, err
		}
	} else {
		mutation, updateErr := client.ExecContext(ctx, `
UPDATE user_hosting_entitlements
SET account_limit = $1,
    group_limit = $2,
    version = version + 1,
    updated_by_user_id = $3,
    updated_at = NOW()
WHERE user_id = $4
  AND version = $5`,
			input.AccountLimit,
			input.GroupLimit,
			input.ActorUserID,
			input.TargetUserID,
			input.Current.Version,
		)
		if updateErr != nil {
			return result, updateErr
		}
		updated, rowsErr := mutation.RowsAffected()
		if rowsErr != nil {
			return result, rowsErr
		}
		if updated != 1 {
			return result, service.ErrHostingEntitlementConflict
		}
	}

	if roleChanged {
		mutation, updateErr := client.ExecContext(ctx, `
UPDATE users
SET authz_version = authz_version + 1,
    updated_at = NOW()
WHERE id = $1
  AND deleted_at IS NULL`, input.TargetUserID)
		if updateErr != nil {
			return result, updateErr
		}
		if _, err := exactlyOneHostingMutation(mutation, "increment hoster authorization version"); err != nil {
			return result, service.ErrHostingEntitlementConflict.WithCause(err)
		}
	}

	result.Changed = true
	result.RoleChanged = roleChanged
	return result, nil
}

func (r *hostingEntitlementRepository) AppendHostingEntitlementAudit(
	ctx context.Context,
	actorUserID int64,
	previous service.HostingEntitlementRecord,
	current service.HostingEntitlementRecord,
	trace service.HostingEntitlementAuditTrace,
) error {
	if dbent.TxFromContext(ctx) == nil || actorUserID <= 0 || current.UserID <= 0 || previous.UserID != current.UserID {
		return service.ErrHostingEntitlementUnavailable
	}
	extra, err := json.Marshal(struct {
		TargetUserID int64                        `json:"target_user_id"`
		Previous     hostingEntitlementAuditState `json:"previous"`
		Current      hostingEntitlementAuditState `json:"current"`
	}{
		TargetUserID: current.UserID,
		Previous:     newHostingEntitlementAuditState(previous),
		Current:      newHostingEntitlementAuditState(current),
	})
	if err != nil {
		return fmt.Errorf("encode hosting entitlement audit: %w", err)
	}

	client := clientFromContext(ctx, r.client)
	mutation, err := client.ExecContext(ctx, `
INSERT INTO audit_logs (
    actor_user_id,
    actor_email,
    actor_role,
    auth_method,
    action,
    method,
    path,
    request_id,
    client_ip,
    user_agent,
    status_code,
    extra
)
SELECT
    u.id,
    LEFT(u.email, 255),
    'admin',
    $1,
    $2,
    $3,
    $4,
    LEFT($5, 64),
    LEFT($6, 64),
    LEFT($7, 512),
    200,
    $8::jsonb
FROM users AS u
WHERE u.id = $9
  AND u.role = 'admin'
  AND u.status = 'active'
  AND u.deleted_at IS NULL`,
		service.AuditAuthMethodJWT,
		service.AuditActionHostingEntitlementUpdate,
		hostingEntitlementAuditMethod,
		hostingEntitlementAuditPath,
		trace.RequestID,
		trace.ClientIP,
		trace.UserAgent,
		string(extra),
		actorUserID,
	)
	if err != nil {
		return err
	}
	inserted, err := mutation.RowsAffected()
	if err != nil {
		return err
	}
	if inserted != 1 {
		return service.ErrHostingActorNotAuthorized
	}
	return nil
}

type hostingEntitlementAuditState struct {
	Hoster       bool  `json:"hoster"`
	AccountLimit int64 `json:"account_limit"`
	GroupLimit   int64 `json:"group_limit"`
	Version      int64 `json:"version"`
	AuthzVersion int64 `json:"authz_version"`
}

func newHostingEntitlementAuditState(record service.HostingEntitlementRecord) hostingEntitlementAuditState {
	return hostingEntitlementAuditState{
		Hoster:       record.Hoster,
		AccountLimit: record.AccountLimit,
		GroupLimit:   record.GroupLimit,
		Version:      record.Version,
		AuthzVersion: record.AuthzVersion,
	}
}

func (r *hostingEntitlementRepository) LockHostingCapacity(
	ctx context.Context,
	userID int64,
	resourceType authz.ResourceType,
) (service.HostingCapacityRecord, error) {
	var result service.HostingCapacityRecord
	if r == nil || r.client == nil || ctx == nil || dbent.TxFromContext(ctx) == nil || userID <= 0 ||
		(resourceType != authz.ResourceTypeAccount && resourceType != authz.ResourceTypeGroup) {
		return result, service.ErrHostingEntitlementUnavailable
	}
	client := clientFromContext(ctx, r.client)
	if err := requireSerializableHostingCapacityTx(ctx, client); err != nil {
		return result, err
	}
	if err := consumeHostingLockRows(ctx, client,
		`SELECT id FROM users WHERE id = $1 ORDER BY id FOR UPDATE`, userID); err != nil {
		return result, fmt.Errorf("lock hosting capacity user: %w", err)
	}
	if err := consumeHostingLockRows(ctx, client, `
SELECT ur.id
FROM user_roles AS ur
JOIN roles AS r ON r.id = ur.role_id
WHERE ur.user_id = $1
ORDER BY r.id, ur.id
FOR UPDATE OF ur, r`, userID); err != nil {
		return result, fmt.Errorf("lock hosting capacity roles: %w", err)
	}
	if _, err := queryHostingRoleID(ctx, client, true); err != nil {
		return result, err
	}
	if err := consumeHostingLockRows(ctx, client, `
SELECT id
FROM user_hosting_entitlements
WHERE user_id = $1
FOR UPDATE`, userID); err != nil {
		return result, fmt.Errorf("lock hosting capacity entitlement: %w", err)
	}

	record, err := r.ReadHostingEntitlement(ctx, userID)
	if err != nil {
		return result, err
	}
	return service.HostingCapacityRecord{
		UserID:       record.UserID,
		UserActive:   record.UserActive,
		Hoster:       record.Hoster,
		Version:      record.Version,
		AccountLimit: record.AccountLimit,
		AccountUsage: record.AccountUsage,
		GroupLimit:   record.GroupLimit,
		GroupUsage:   record.GroupUsage,
	}, nil
}

func requireSerializableHostingCapacityTx(ctx context.Context, client *dbent.Client) error {
	if ctx == nil || client == nil {
		return service.ErrHostingEntitlementUnavailable
	}
	rows, err := client.QueryContext(ctx, `SHOW transaction_isolation`)
	if err != nil {
		return fmt.Errorf("read hosting capacity transaction isolation: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return service.ErrHostingEntitlementUnavailable.WithMetadata(map[string]string{
			"reason": hostingEntitlementSerializableIsolationReason,
		})
	}
	var isolation string
	if err := rows.Scan(&isolation); err != nil {
		return err
	}
	if rows.Next() {
		return fmt.Errorf("hosting capacity transaction isolation returned multiple rows")
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if strings.EqualFold(strings.TrimSpace(isolation), hostingEntitlementSerializableIsolation) {
		return nil
	}
	return service.ErrHostingEntitlementUnavailable.WithMetadata(map[string]string{
		"reason":    hostingEntitlementSerializableIsolationReason,
		"isolation": strings.TrimSpace(isolation),
	})
}

func queryHostingRoleID(ctx context.Context, client *dbent.Client, forUpdate bool) (int64, error) {
	query := `
SELECT id
FROM roles
WHERE code = 'hoster'
  AND is_system = TRUE`
	if forUpdate {
		query += " FOR UPDATE"
	}
	rows, err := client.QueryContext(ctx, query)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, service.ErrHostingEntitlementUnavailable.WithMetadata(map[string]string{
			"reason": "hoster role missing",
		})
	}
	var roleID int64
	if err := rows.Scan(&roleID); err != nil {
		return 0, err
	}
	if rows.Next() {
		return 0, service.ErrHostingEntitlementUnavailable.WithMetadata(map[string]string{
			"reason": "multiple hoster roles",
		})
	}
	return roleID, rows.Err()
}

func consumeHostingLockRows(
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

func exactlyOneHostingMutation(result sql.Result, operation string) (bool, error) {
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if rows != 1 {
		return false, fmt.Errorf("%s affected %d rows", operation, rows)
	}
	return true, nil
}

func nullableHostingInt64(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	copyValue := value.Int64
	return &copyValue
}

func nullableHostingTime(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	copyValue := value.Time
	return &copyValue
}
