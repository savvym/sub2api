package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"sync"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"entgo.io/ent/dialect"
)

const roleManagementAdvisoryLockID int64 = 0x7375623261706937

var roleManagementProcessLock sync.Mutex

type roleRepository struct {
	client *dbent.Client
	sql    sqlExecutor
}

func NewRoleRepository(client *dbent.Client, sqlDB *sql.DB) service.RoleRepository {
	return &roleRepository{client: client, sql: sqlDB}
}

func (r *roleRepository) WithRoleManagementTx(ctx context.Context, fn func(txCtx context.Context) error) error {
	if r == nil || r.client == nil || fn == nil {
		return service.ErrRoleAuthorizationUnavailable
	}

	roleManagementProcessLock.Lock()
	defer roleManagementProcessLock.Unlock()

	if dbent.TxFromContext(ctx) != nil {
		if err := r.acquireRoleManagementLock(ctx); err != nil {
			return err
		}
		return fn(ctx)
	}

	tx, err := r.client.Tx(ctx)
	if errors.Is(err, dbent.ErrTxStarted) {
		if err := r.acquireRoleManagementLock(ctx); err != nil {
			return err
		}
		return fn(ctx)
	}
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	txCtx := dbent.NewTxContext(ctx, tx)
	if err := r.acquireRoleManagementLock(txCtx); err != nil {
		return err
	}
	if err := fn(txCtx); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *roleRepository) acquireRoleManagementLock(ctx context.Context) error {
	client := clientFromContext(ctx, r.client)
	if client == nil || client.Driver().Dialect() != dialect.Postgres {
		return nil
	}
	rows, err := client.QueryContext(ctx, "SELECT pg_advisory_xact_lock($1)", roleManagementAdvisoryLockID)
	if err != nil {
		return fmt.Errorf("acquire role management lock: %w", err)
	}
	return rows.Close()
}

func (r *roleRepository) GetAuthorizationModeForUpdate(ctx context.Context) (string, error) {
	client := clientFromContext(ctx, r.client)
	rows, err := client.QueryContext(ctx, `
SELECT value
FROM settings
WHERE key = $1
FOR UPDATE`, service.SettingKeyRoleAuthorizationMode)
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", err
		}
		return service.RoleAuthorizationModeLegacy, nil
	}
	var mode string
	if err := rows.Scan(&mode); err != nil {
		return "", err
	}
	if rows.Next() {
		return "", fmt.Errorf("multiple role authorization mode settings")
	}
	return mode, rows.Err()
}

func (r *roleRepository) LockRoleSubjects(ctx context.Context, userIDs []int64) (map[int64]service.RoleSubject, error) {
	client := clientFromContext(ctx, r.client)
	ids := uniqueSortedInt64s(userIDs)
	result := make(map[int64]service.RoleSubject, len(ids))
	for _, userID := range ids {
		rows, err := client.QueryContext(ctx, `
SELECT id, role, status, authz_version, deleted_at IS NOT NULL
FROM users
WHERE id = $1
FOR UPDATE`, userID)
		if err != nil {
			return nil, err
		}
		if rows.Next() {
			var subject service.RoleSubject
			if err := rows.Scan(&subject.ID, &subject.LegacyRole, &subject.Status, &subject.AuthzVersion, &subject.Deleted); err != nil {
				_ = rows.Close()
				return nil, err
			}
			result[subject.ID] = subject
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (r *roleRepository) CountActiveLegacyAdmins(ctx context.Context) (int64, error) {
	return querySingleInt64(ctx, clientFromContext(ctx, r.client), `
SELECT COUNT(*)
FROM users
WHERE role = 'admin'
  AND status = 'active'
  AND deleted_at IS NULL`)
}

func (r *roleRepository) ReconcileLegacyRole(
	ctx context.Context,
	userID int64,
	expectedRole string,
	desiredRole string,
) (service.LegacyRoleMutationResult, error) {
	var result service.LegacyRoleMutationResult
	client := clientFromContext(ctx, r.client)
	roleIDs, err := r.lockCompatibilityRoles(ctx, client)
	if err != nil {
		return result, err
	}
	bootstrapID, err := r.lockBootstrapPrincipal(ctx, client)
	if err != nil {
		return result, err
	}
	if err := lockCompatibilityAssignments(ctx, client, userID, roleIDs); err != nil {
		return result, err
	}

	desiredRoleID := roleIDs[desiredRole]
	oppositeRole := service.RoleUser
	if desiredRole == service.RoleUser {
		oppositeRole = service.RoleAdmin
	}
	oppositeRoleID := roleIDs[oppositeRole]

	desiredExists, desiredStable, err := compatibilityAssignmentState(ctx, client, userID, desiredRoleID)
	if err != nil {
		return result, err
	}
	if desiredExists && !desiredStable {
		return result, service.ErrRoleAuthorizationUnavailable.WithMetadata(map[string]string{
			"reason":  "expiring compatibility assignment",
			"user_id": fmt.Sprintf("%d", userID),
			"role":    desiredRole,
		})
	}

	deleteResult, err := client.ExecContext(ctx, `
DELETE FROM user_roles
WHERE user_id = $1
  AND role_id = $2
  AND granted_by_service_principal_id = $3`, userID, oppositeRoleID, bootstrapID)
	if err != nil {
		return result, err
	}
	deleted, err := deleteResult.RowsAffected()
	if err != nil {
		return result, err
	}

	var inserted int64
	if !desiredExists {
		insertResult, insertErr := client.ExecContext(ctx, `
INSERT INTO user_roles (
    user_id,
    role_id,
    granted_by_service_principal_id,
    created_at,
    updated_at
)
VALUES ($1, $2, $3, NOW(), NOW())
ON CONFLICT (user_id, role_id) DO NOTHING`, userID, desiredRoleID, bootstrapID)
		if insertErr != nil {
			return result, insertErr
		}
		inserted, err = insertResult.RowsAffected()
		if err != nil {
			return result, err
		}
		if inserted != 1 {
			return result, fmt.Errorf("ensure compatibility role %q for user %d: assignment was not inserted", desiredRole, userID)
		}
	}

	result.Changed = expectedRole != desiredRole || deleted > 0 || inserted > 0
	if !result.Changed {
		result.AuthzVersion, err = querySingleInt64(ctx, client, `SELECT authz_version FROM users WHERE id = $1`, userID)
		return result, err
	}

	rows, err := client.QueryContext(ctx, `
UPDATE users
SET role = $1,
    authz_version = authz_version + 1,
    updated_at = NOW()
WHERE id = $2
  AND role = $3
RETURNING authz_version, updated_at`, desiredRole, userID, expectedRole)
	if err != nil {
		return result, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return result, err
		}
		return result, service.ErrRoleMutationConflict
	}
	if err := rows.Scan(&result.AuthzVersion, &result.UpdatedAt); err != nil {
		return result, err
	}
	return result, rows.Err()
}

func (r *roleRepository) lockCompatibilityRoles(ctx context.Context, client *dbent.Client) (map[string]int64, error) {
	rows, err := client.QueryContext(ctx, `
SELECT id, code
FROM roles
WHERE code IN ('admin', 'user')
  AND is_system = TRUE
ORDER BY id
FOR NO KEY UPDATE`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	roleIDs := make(map[string]int64, 2)
	for rows.Next() {
		var id int64
		var code string
		if err := rows.Scan(&id, &code); err != nil {
			return nil, err
		}
		roleIDs[code] = id
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(roleIDs) != 2 || roleIDs[service.RoleAdmin] <= 0 || roleIDs[service.RoleUser] <= 0 {
		return nil, service.ErrRoleAuthorizationUnavailable.WithMetadata(map[string]string{"reason": "system roles missing"})
	}
	return roleIDs, nil
}

func (r *roleRepository) lockBootstrapPrincipal(ctx context.Context, client *dbent.Client) (int64, error) {
	rows, err := client.QueryContext(ctx, `
SELECT id
FROM service_principals
WHERE code = 'system_bootstrap'
  AND status = 'active'
FOR KEY SHARE`)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, service.ErrRoleAuthorizationUnavailable.WithMetadata(map[string]string{"reason": "bootstrap principal missing"})
	}
	var id int64
	if err := rows.Scan(&id); err != nil {
		return 0, err
	}
	return id, rows.Err()
}

func lockCompatibilityAssignments(ctx context.Context, client *dbent.Client, userID int64, roleIDs map[string]int64) error {
	rows, err := client.QueryContext(ctx, `
SELECT id
FROM user_roles
WHERE user_id = $1
  AND role_id IN ($2, $3)
ORDER BY role_id
FOR UPDATE`, userID, roleIDs[service.RoleAdmin], roleIDs[service.RoleUser])
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if scanErr := rows.Scan(&id); scanErr != nil {
			return scanErr
		}
	}
	return rows.Err()
}

func compatibilityAssignmentState(ctx context.Context, client *dbent.Client, userID, roleID int64) (exists bool, stable bool, err error) {
	rows, err := client.QueryContext(ctx, `
SELECT expires_at
FROM user_roles
WHERE user_id = $1
  AND role_id = $2`, userID, roleID)
	if err != nil {
		return false, false, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		return false, false, rows.Err()
	}
	var expiresAt sql.NullTime
	if err := rows.Scan(&expiresAt); err != nil {
		return false, false, err
	}
	return true, !expiresAt.Valid, rows.Err()
}

func (r *roleRepository) InspectAuthorizationReadiness(ctx context.Context, targetMode string) (service.RoleAuthorizationReadiness, error) {
	var readiness service.RoleAuthorizationReadiness
	client := clientFromContext(ctx, r.client)
	if client.Driver().Dialect() == dialect.Postgres {
		if _, err := client.ExecContext(ctx, `
LOCK TABLE users, user_roles, roles, permissions, role_permissions, service_principals, service_principal_roles IN SHARE MODE`); err != nil {
			return readiness, err
		}
	}

	type readinessCheck struct {
		code  string
		query string
		args  []any
	}
	var checks []readinessCheck
	if targetMode == service.RoleAuthorizationModeLegacy {
		checks = []readinessCheck{
			{
				code: service.RoleReadinessRBACAdminLegacyMismatch,
				query: `
SELECT COUNT(DISTINCT u.id)
FROM users AS u
JOIN user_roles AS ur ON ur.user_id = u.id
JOIN roles AS r ON r.id = ur.role_id
WHERE u.deleted_at IS NULL
  AND u.role <> 'admin'
  AND (ur.expires_at IS NULL OR ur.expires_at > NOW())
  AND (
      r.code = 'admin'
      OR EXISTS (
          SELECT 1
          FROM role_permissions AS rp
          JOIN permissions AS p ON p.id = rp.permission_id
          WHERE rp.role_id = r.id
            AND p.code IN (
                'platform.resource.manage_all',
                'platform.role.manage',
                'platform.grant.manage'
            )
      )
  )`,
			},
			{
				code: service.RoleReadinessServicePrincipalRoleUnmappable,
				query: `
SELECT COUNT(*)
FROM service_principal_roles AS spr
JOIN service_principals AS sp ON sp.id = spr.service_principal_id
WHERE sp.status = 'active'
  AND (spr.expires_at IS NULL OR spr.expires_at > NOW())`,
			},
		}
	} else {
		checks = []readinessCheck{
			{
				code:  service.RoleReadinessMigrationMissing,
				query: `SELECT 3 - COUNT(*) FROM schema_migrations WHERE filename IN ($1, $2, $3)`,
				args: []any{
					"229_resource_authorization_rbac.sql",
					"232_resource_authorization_compatibility_backfill.sql",
					"233_role_authorization_cache_invalidation.sql",
				},
			},
			{
				code:  service.RoleReadinessSystemRoleMissing,
				query: `SELECT 2 - COUNT(*) FROM roles WHERE code IN ('admin', 'user') AND is_system = TRUE`,
			},
			{
				code:  service.RoleReadinessBootstrapPrincipalMissing,
				query: `SELECT CASE WHEN COUNT(*) = 1 THEN 0 ELSE 1 END FROM service_principals WHERE code = 'system_bootstrap' AND status = 'active'`,
			},
			{
				code:  service.RoleReadinessLegacyRoleUnmappable,
				query: `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL AND role NOT IN ('admin', 'user')`,
			},
			{
				code: service.RoleReadinessCompatibilityRoleMissing,
				query: `
SELECT COUNT(*)
FROM users AS u
WHERE u.deleted_at IS NULL
  AND u.role IN ('admin', 'user')
  AND NOT EXISTS (
      SELECT 1
      FROM user_roles AS ur
      JOIN roles AS r ON r.id = ur.role_id
      WHERE ur.user_id = u.id
        AND r.code = u.role
        AND ur.expires_at IS NULL
  )`,
			},
			{
				code: service.RoleReadinessStaleBootstrapCompatibilityRole,
				query: `
SELECT COUNT(*)
FROM user_roles AS ur
JOIN users AS u ON u.id = ur.user_id
JOIN roles AS r ON r.id = ur.role_id
JOIN service_principals AS sp ON sp.id = ur.granted_by_service_principal_id
WHERE u.deleted_at IS NULL
  AND sp.code = 'system_bootstrap'
  AND r.code IN ('admin', 'user')
  AND (u.role NOT IN ('admin', 'user') OR r.code <> u.role)`,
			},
			{
				code:  service.RoleReadinessSubjectVersionInvalid,
				query: `SELECT COUNT(*) FROM users WHERE deleted_at IS NULL AND authz_version <= 0`,
			},
			{
				code:  service.RoleReadinessRoleVersionInvalid,
				query: `SELECT COUNT(*) FROM roles WHERE authz_version <= 0`,
			},
		}
	}

	for _, check := range checks {
		count, err := querySingleInt64(ctx, client, check.query, check.args...)
		if err != nil {
			return readiness, fmt.Errorf("role readiness %s: %w", check.code, err)
		}
		if count > 0 {
			readiness.Blockers = append(readiness.Blockers, service.RoleAuthorizationReadinessBlocker{
				Code:  check.code,
				Count: count,
			})
		}
	}
	return readiness, nil
}

func (r *roleRepository) SetAuthorizationMode(ctx context.Context, mode string) error {
	client := clientFromContext(ctx, r.client)
	_, err := client.ExecContext(ctx, `
INSERT INTO settings (key, value, updated_at)
VALUES ($1, $2, NOW())
ON CONFLICT (key) DO UPDATE
SET value = EXCLUDED.value,
    updated_at = EXCLUDED.updated_at`, service.SettingKeyRoleAuthorizationMode, mode)
	return err
}

func querySingleInt64(ctx context.Context, client *dbent.Client, query string, args ...any) (int64, error) {
	rows, err := client.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, sql.ErrNoRows
	}
	var value int64
	if err := rows.Scan(&value); err != nil {
		return 0, err
	}
	return value, rows.Err()
}

func uniqueSortedInt64s(values []int64) []int64 {
	set := make(map[int64]struct{}, len(values))
	for _, value := range values {
		if value > 0 {
			set[value] = struct{}{}
		}
	}
	result := make([]int64, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

// ensureUserCompatibilityRoleWithClient covers every production user-creation
// path because they all converge in userRepository.create. SQLite is used only
// by repository unit tests and does not run the SQL migration seed.
func ensureUserCompatibilityRoleWithClient(ctx context.Context, client *dbent.Client, userID int64, legacyRole string) error {
	client = clientFromContext(ctx, client)
	if client == nil || client.Driver().Dialect() != dialect.Postgres || userID <= 0 {
		return nil
	}
	desiredRole := service.RoleUser
	if legacyRole == service.RoleAdmin {
		desiredRole = service.RoleAdmin
	}

	result, err := client.ExecContext(ctx, `
INSERT INTO user_roles (
    user_id,
    role_id,
    granted_by_service_principal_id,
    created_at,
    updated_at
)
SELECT $1, r.id, sp.id, NOW(), NOW()
FROM roles AS r
CROSS JOIN service_principals AS sp
WHERE r.code = $2
  AND r.is_system = TRUE
  AND sp.code = 'system_bootstrap'
  AND sp.status = 'active'
ON CONFLICT (user_id, role_id) DO NOTHING`, userID, desiredRole)
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 1 {
		return nil
	}

	count, err := querySingleInt64(ctx, client, `
SELECT COUNT(*)
FROM user_roles AS ur
JOIN roles AS r ON r.id = ur.role_id
WHERE ur.user_id = $1
  AND r.code = $2
  AND ur.expires_at IS NULL`, userID, desiredRole)
	if err != nil {
		return err
	}
	if count != 1 {
		return service.ErrRoleAuthorizationUnavailable.WithMetadata(map[string]string{
			"reason":  "compatibility role seed missing",
			"user_id": fmt.Sprintf("%d", userID),
			"role":    desiredRole,
		})
	}
	return nil
}
