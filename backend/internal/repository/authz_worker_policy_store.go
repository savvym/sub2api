package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/authz"
)

var _ authz.WorkerPolicyStore = (*authzPolicyStore)(nil)

func NewAuthzWorkerPolicyStore(client *dbent.Client) authz.WorkerPolicyStore {
	return &authzPolicyStore{client: client}
}

type rawWorkerAuthorizationDocument struct {
	ServicePrincipalID   int64    `json:"service_principal_id"`
	ServicePrincipalCode string   `json:"service_principal_code"`
	Active               bool     `json:"active"`
	AuthzVersion         int64    `json:"authz_version"`
	RoleCount            int      `json:"role_count"`
	PermissionCodes      []string `json:"permission_codes"`
	AccountID            int64    `json:"account_id"`
	AccountExists        bool     `json:"account_exists"`
	AccountDeleted       bool     `json:"account_deleted"`
}

func (s *authzPolicyStore) LoadWorkerAuthorizationSnapshot(
	ctx context.Context,
	servicePrincipalCode string,
	accountID int64,
) (authz.WorkerAuthorizationSnapshot, error) {
	servicePrincipalCode = strings.TrimSpace(servicePrincipalCode)
	if ctx == nil || servicePrincipalCode == "" || len(servicePrincipalCode) > 64 || accountID < 0 {
		return authz.WorkerAuthorizationSnapshot{}, authz.ErrInvalidPolicySnapshot
	}
	queryer := s.queryerForContext(ctx)
	if queryer == nil {
		return authz.WorkerAuthorizationSnapshot{}, fmt.Errorf("authz worker policy store: nil database client")
	}

	payload, err := queryAuthzPolicyJSON(
		ctx,
		queryer,
		workerAuthorizationSnapshotSQL,
		servicePrincipalCode,
		accountID,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return authz.WorkerAuthorizationSnapshot{}, authz.ErrSubjectNotFound
		}
		return authz.WorkerAuthorizationSnapshot{}, fmt.Errorf("load authz worker snapshot: %w", err)
	}

	var document rawWorkerAuthorizationDocument
	if err := json.Unmarshal([]byte(payload), &document); err != nil {
		return authz.WorkerAuthorizationSnapshot{}, fmt.Errorf("decode authz worker snapshot: %w", err)
	}
	return authz.NewWorkerAuthorizationSnapshot(authz.WorkerAuthorizationSnapshotInput{
		ServicePrincipalID:   document.ServicePrincipalID,
		ServicePrincipalCode: document.ServicePrincipalCode,
		Active:               document.Active,
		AuthzVersion:         document.AuthzVersion,
		RoleCount:            document.RoleCount,
		PermissionCodes:      document.PermissionCodes,
		AccountID:            document.AccountID,
		AccountExists:        document.AccountExists,
		AccountDeleted:       document.AccountDeleted,
	})
}

const workerAuthorizationSnapshotSQL = `
SELECT jsonb_build_object(
	'service_principal_id', service_principals.id,
	'service_principal_code', service_principals.code,
	'active', service_principals.status = 'active',
	'authz_version', service_principals.authz_version,
	'role_count', (
		SELECT COUNT(*)
		FROM service_principal_roles
		WHERE service_principal_id = service_principals.id
	),
	'permission_codes', COALESCE((
		SELECT jsonb_agg(permissions.code ORDER BY permissions.code)
		FROM service_principal_worker_permissions
		JOIN permissions
			ON permissions.id = service_principal_worker_permissions.permission_id
		WHERE service_principal_worker_permissions.service_principal_id = service_principals.id
	), '[]'::jsonb),
	'account_id', $2::bigint,
	'account_exists', CASE WHEN $2::bigint > 0 THEN EXISTS (
		SELECT 1 FROM accounts WHERE id = $2::bigint
	) ELSE FALSE END,
	'account_deleted', CASE WHEN $2::bigint > 0 THEN COALESCE((
		SELECT deleted_at IS NOT NULL FROM accounts WHERE id = $2::bigint
	), FALSE) ELSE FALSE END
)::text
FROM service_principals
WHERE code = $1
`
