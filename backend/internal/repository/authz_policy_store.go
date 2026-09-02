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
	"github.com/Wei-Shaw/sub2api/internal/config"
)

type authzPolicyQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type authzPolicyStore struct {
	client     *dbent.Client
	queryer    authzPolicyQueryer
	simpleMode bool
}

var (
	_ authz.PolicyStore        = (*authzPolicyStore)(nil)
	_ authz.ActorResolverStore = (*authzPolicyStore)(nil)
	_ authz.WorkerPolicyStore  = (*authzPolicyStore)(nil)
)

func NewAuthzPolicyStore(client *dbent.Client, cfg *config.Config) authz.PolicyStore {
	return &authzPolicyStore{
		client:     client,
		simpleMode: cfg != nil && cfg.RunMode == config.RunModeSimple,
	}
}

func NewAuthzActorResolverStore(client *dbent.Client) authz.ActorResolverStore {
	return &authzPolicyStore{client: client}
}

func newAuthzPolicyStoreWithQueryer(queryer authzPolicyQueryer) *authzPolicyStore {
	return &authzPolicyStore{queryer: queryer}
}

func (s *authzPolicyStore) LoadSubjectSnapshot(ctx context.Context, subject authz.SubjectRef) (authz.SubjectSnapshot, error) {
	if !subject.Valid() {
		return authz.SubjectSnapshot{}, authz.ErrInvalidPolicySnapshot
	}
	queryer := s.queryerForContext(ctx)
	if queryer == nil {
		return authz.SubjectSnapshot{}, fmt.Errorf("authz policy store: nil database client")
	}

	payload, err := queryAuthzPolicyJSON(ctx, queryer, buildSubjectSnapshotSQL(subject.Kind()), subject.ID())
	if err != nil {
		return authz.SubjectSnapshot{}, fmt.Errorf("load authz subject snapshot: %w", err)
	}
	document, err := decodeAuthzPolicyDocument(payload, false)
	if err != nil {
		return authz.SubjectSnapshot{}, err
	}
	s.applyRuntimeConfiguration(&document)
	return document.subjectSnapshot(subject)
}

func (s *authzPolicyStore) LoadServicePrincipalSubjectSnapshotByCode(ctx context.Context, code string) (authz.SubjectSnapshot, error) {
	code = strings.TrimSpace(code)
	if code == "" || len(code) > 64 {
		return authz.SubjectSnapshot{}, fmt.Errorf("authz policy store: invalid service principal code")
	}
	queryer := s.queryerForContext(ctx)
	if queryer == nil {
		return authz.SubjectSnapshot{}, fmt.Errorf("authz policy store: nil database client")
	}

	payload, err := queryAuthzPolicyJSON(ctx, queryer, buildServicePrincipalSubjectSnapshotByCodeSQL(), code)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return authz.SubjectSnapshot{}, fmt.Errorf("load authz service principal subject snapshot by code: %w", authz.ErrSubjectNotFound)
		}
		return authz.SubjectSnapshot{}, fmt.Errorf("load authz service principal subject snapshot by code: %w", err)
	}
	document, err := decodeAuthzPolicyDocument(payload, false)
	if err != nil {
		return authz.SubjectSnapshot{}, err
	}
	s.applyRuntimeConfiguration(&document)
	if document.SubjectID <= 0 || !document.Subject.Exists {
		return authz.SubjectSnapshot{}, authz.ErrInvalidPolicySnapshot
	}
	subject, err := authz.NewSubjectRef(authz.SubjectKindServicePrincipal, document.SubjectID)
	if err != nil {
		return authz.SubjectSnapshot{}, authz.ErrInvalidPolicySnapshot
	}
	return document.subjectSnapshot(subject)
}

func (s *authzPolicyStore) LoadResourceAccessSnapshot(ctx context.Context, subject authz.SubjectRef, resource authz.ResourceRef) (authz.ResourceAccessSnapshot, error) {
	if !subject.Valid() || !resource.Valid() {
		return authz.ResourceAccessSnapshot{}, authz.ErrInvalidPolicySnapshot
	}
	queryer := s.queryerForContext(ctx)
	if queryer == nil {
		return authz.ResourceAccessSnapshot{}, fmt.Errorf("authz policy store: nil database client")
	}

	payload, err := queryAuthzPolicyJSON(
		ctx,
		queryer,
		buildResourceSnapshotSQL(subject.Kind(), resource.Type()),
		subject.ID(),
		resource.ID(),
	)
	if err != nil {
		return authz.ResourceAccessSnapshot{}, fmt.Errorf("load authz resource snapshot: %w", err)
	}
	document, err := decodeAuthzPolicyDocument(payload, true)
	if err != nil {
		return authz.ResourceAccessSnapshot{}, err
	}
	s.applyRuntimeConfiguration(&document)
	subjectSnapshot, err := document.subjectSnapshot(subject)
	if err != nil {
		return authz.ResourceAccessSnapshot{}, err
	}
	return document.resourceSnapshot(subjectSnapshot, resource)
}

func (s *authzPolicyStore) applyRuntimeConfiguration(document *rawAuthzPolicyDocument) {
	if s == nil || document == nil || !s.simpleMode {
		return
	}
	document.Configuration.SelfServiceHostingEnabled = false
	document.Configuration.GroupSharingEnabled = false
	document.Configuration.AccountSharingEnabled = false
}

func (s *authzPolicyStore) queryerForContext(ctx context.Context) authzPolicyQueryer {
	if tx := dbent.TxFromContext(ctx); tx != nil {
		return tx.Client()
	}
	if s == nil {
		return nil
	}
	if s.client != nil {
		return s.client
	}
	return s.queryer
}

func queryAuthzPolicyJSON(ctx context.Context, queryer authzPolicyQueryer, query string, args ...any) (payload string, err error) {
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return "", err
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			payload = ""
		}
	}()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", err
		}
		return "", sql.ErrNoRows
	}
	if err := rows.Scan(&payload); err != nil {
		return "", err
	}
	if rows.Next() {
		return "", fmt.Errorf("authz policy store: query returned multiple documents")
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	return payload, nil
}

type rawAuthzPolicyDocument struct {
	SubjectID     int64                 `json:"subject_id,omitempty"`
	Subject       rawAuthzSubject       `json:"subject"`
	Configuration rawAuthzConfiguration `json:"configuration"`
	Resource      *rawAuthzResource     `json:"resource,omitempty"`
	UserGrants    []rawAuthzGrant       `json:"user_grants"`
	RoleGrants    []rawAuthzGrant       `json:"role_grants"`
}

type rawAuthzSubject struct {
	Exists             bool           `json:"exists"`
	Active             bool           `json:"active"`
	AuthzVersion       int64          `json:"authz_version"`
	CurrentLegacyAdmin bool           `json:"current_legacy_admin"`
	Roles              []rawAuthzRole `json:"roles"`
	Capabilities       []string       `json:"capabilities"`
}

type rawAuthzRole struct {
	ID      int64 `json:"id"`
	Version int64 `json:"version"`
}

type rawAuthzConfiguration struct {
	RoleAuthorizationMode          string `json:"role_authorization_mode"`
	ResourceAccessControlEnabled   bool   `json:"resource_access_control_enabled"`
	SelfServiceHostingEnabled      bool   `json:"self_service_hosting_enabled"`
	GroupSharingEnabled            bool   `json:"group_sharing_enabled"`
	AccountSharingEnabled          bool   `json:"account_sharing_enabled"`
	RoleBasedResourceGrantsEnabled bool   `json:"role_based_resource_grants_enabled"`
}

type rawAuthzResource struct {
	Exists            bool    `json:"exists"`
	Deleted           bool    `json:"deleted"`
	OwnerUserID       *int64  `json:"owner_user_id"`
	PublicAccessLevel *string `json:"public_access_level"`
	AuthorizationMode *string `json:"authorization_mode"`
	AccessVersion     int64   `json:"access_version"`
}

type rawAuthzGrant struct {
	ID          int64  `json:"id"`
	RoleID      *int64 `json:"role_id,omitempty"`
	AccessLevel string `json:"access_level"`
}

func decodeAuthzPolicyDocument(payload string, requireResource bool) (rawAuthzPolicyDocument, error) {
	var document rawAuthzPolicyDocument
	if strings.TrimSpace(payload) == "" {
		return document, fmt.Errorf("%w: empty database document", authz.ErrInvalidPolicySnapshot)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return document, fmt.Errorf("%w: decode database document: %w", authz.ErrInvalidPolicySnapshot, err)
	}
	if err := validateAuthzJSONObject(envelope["subject"], "subject", []string{
		"exists", "active", "authz_version", "current_legacy_admin", "roles", "capabilities",
	}, nil); err != nil {
		return document, err
	}
	if err := validateAuthzJSONObject(envelope["configuration"], "configuration", []string{
		"role_authorization_mode", "resource_access_control_enabled", "self_service_hosting_enabled",
		"group_sharing_enabled", "account_sharing_enabled", "role_based_resource_grants_enabled",
	}, nil); err != nil {
		return document, err
	}
	if requireResource {
		if err := validateAuthzJSONObject(envelope["resource"], "resource", []string{
			"exists", "deleted", "owner_user_id", "public_access_level", "authorization_mode", "access_version",
		}, map[string]struct{}{
			"owner_user_id": {}, "public_access_level": {}, "authorization_mode": {},
		}); err != nil {
			return document, err
		}
		for _, field := range []string{"user_grants", "role_grants"} {
			value, ok := envelope[field]
			if !ok || authzJSONNull(value) {
				return document, fmt.Errorf("%w: missing %s document", authz.ErrInvalidPolicySnapshot, field)
			}
		}
	}
	if err := json.Unmarshal([]byte(payload), &document); err != nil {
		return document, fmt.Errorf("%w: decode database document: %w", authz.ErrInvalidPolicySnapshot, err)
	}
	return document, nil
}

func validateAuthzJSONObject(raw json.RawMessage, name string, required []string, nullable map[string]struct{}) error {
	if authzJSONNull(raw) {
		return fmt.Errorf("%w: missing %s document", authz.ErrInvalidPolicySnapshot, name)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return fmt.Errorf("%w: invalid %s document", authz.ErrInvalidPolicySnapshot, name)
	}
	for _, field := range required {
		value, ok := object[field]
		if !ok {
			return fmt.Errorf("%w: missing %s.%s", authz.ErrInvalidPolicySnapshot, name, field)
		}
		if _, allowed := nullable[field]; !allowed && authzJSONNull(value) {
			return fmt.Errorf("%w: null %s.%s", authz.ErrInvalidPolicySnapshot, name, field)
		}
	}
	return nil
}

func authzJSONNull(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}

func (d rawAuthzPolicyDocument) subjectSnapshot(subject authz.SubjectRef) (authz.SubjectSnapshot, error) {
	configuration, err := authz.NewPolicyConfiguration(authz.PolicyConfigurationInput{
		RoleAuthorizationMode:          normalizeAuthzRoleMode(d.Configuration.RoleAuthorizationMode),
		ResourceAccessControlEnabled:   d.Configuration.ResourceAccessControlEnabled,
		SelfServiceHostingEnabled:      d.Configuration.SelfServiceHostingEnabled,
		GroupSharingEnabled:            d.Configuration.GroupSharingEnabled,
		AccountSharingEnabled:          d.Configuration.AccountSharingEnabled,
		RoleBasedResourceGrantsEnabled: d.Configuration.RoleBasedResourceGrantsEnabled,
	})
	if err != nil {
		return authz.SubjectSnapshot{}, err
	}

	roleVersions := make(map[int64]int64, len(d.Subject.Roles))
	for _, role := range d.Subject.Roles {
		if role.ID <= 0 || role.Version <= 0 {
			return authz.SubjectSnapshot{}, authz.ErrInvalidPolicySnapshot
		}
		if _, duplicate := roleVersions[role.ID]; duplicate {
			return authz.SubjectSnapshot{}, authz.ErrInvalidPolicySnapshot
		}
		roleVersions[role.ID] = role.Version
	}

	capabilities := make([]authz.Capability, 0, len(d.Subject.Capabilities))
	seenCapabilities := make(map[authz.Capability]struct{}, len(d.Subject.Capabilities))
	for _, rawCapability := range d.Subject.Capabilities {
		capability, ok := authz.ParseCapability(rawCapability)
		if !ok {
			return authz.SubjectSnapshot{}, authz.ErrInvalidPolicySnapshot
		}
		if _, duplicate := seenCapabilities[capability]; duplicate {
			return authz.SubjectSnapshot{}, authz.ErrInvalidPolicySnapshot
		}
		seenCapabilities[capability] = struct{}{}
		capabilities = append(capabilities, capability)
	}

	return authz.NewSubjectSnapshot(authz.SubjectSnapshotInput{
		Subject:            subject,
		Exists:             d.Subject.Exists,
		Active:             d.Subject.Active,
		AuthzVersion:       d.Subject.AuthzVersion,
		RoleVersions:       roleVersions,
		Capabilities:       capabilities,
		CurrentLegacyAdmin: d.Subject.CurrentLegacyAdmin,
		Configuration:      configuration,
	})
}

func (d rawAuthzPolicyDocument) resourceSnapshot(subject authz.SubjectSnapshot, resource authz.ResourceRef) (authz.ResourceAccessSnapshot, error) {
	if d.Resource == nil {
		return authz.ResourceAccessSnapshot{}, authz.ErrInvalidPolicySnapshot
	}
	var publicAccessLevel *authz.AccessLevel
	if d.Resource.PublicAccessLevel != nil {
		parsed, ok := authz.ParseAccessLevel(*d.Resource.PublicAccessLevel)
		if !ok || !parsed.AllowedAsPublic() {
			return authz.ResourceAccessSnapshot{}, authz.ErrInvalidPolicySnapshot
		}
		publicAccessLevel = &parsed
	}
	var groupAuthorizationMode authz.GroupAuthorizationMode
	switch resource.Type() {
	case authz.ResourceTypeGroup:
		if d.Resource.Exists {
			if d.Resource.AuthorizationMode == nil {
				return authz.ResourceAccessSnapshot{}, authz.ErrInvalidPolicySnapshot
			}
			groupAuthorizationMode = authz.GroupAuthorizationMode(*d.Resource.AuthorizationMode)
			if !groupAuthorizationMode.Valid() {
				return authz.ResourceAccessSnapshot{}, authz.ErrInvalidPolicySnapshot
			}
		} else if d.Resource.AuthorizationMode != nil {
			return authz.ResourceAccessSnapshot{}, authz.ErrInvalidPolicySnapshot
		}
	case authz.ResourceTypeAccount:
		if d.Resource.AuthorizationMode != nil {
			return authz.ResourceAccessSnapshot{}, authz.ErrInvalidPolicySnapshot
		}
	}

	userGrants := make([]authz.GrantSnapshot, 0, len(d.UserGrants))
	for _, rawGrant := range d.UserGrants {
		if rawGrant.RoleID != nil {
			return authz.ResourceAccessSnapshot{}, authz.ErrInvalidPolicySnapshot
		}
		level, ok := authz.ParseAccessLevel(rawGrant.AccessLevel)
		if !ok {
			return authz.ResourceAccessSnapshot{}, authz.ErrInvalidPolicySnapshot
		}
		grant, err := authz.NewUserGrantSnapshot(rawGrant.ID, level)
		if err != nil {
			return authz.ResourceAccessSnapshot{}, err
		}
		userGrants = append(userGrants, grant)
	}

	roleGrants := make([]authz.GrantSnapshot, 0, len(d.RoleGrants))
	for _, rawGrant := range d.RoleGrants {
		if rawGrant.RoleID == nil {
			return authz.ResourceAccessSnapshot{}, authz.ErrInvalidPolicySnapshot
		}
		level, ok := authz.ParseAccessLevel(rawGrant.AccessLevel)
		if !ok {
			return authz.ResourceAccessSnapshot{}, authz.ErrInvalidPolicySnapshot
		}
		grant, err := authz.NewRoleGrantSnapshot(rawGrant.ID, *rawGrant.RoleID, level)
		if err != nil {
			return authz.ResourceAccessSnapshot{}, err
		}
		roleGrants = append(roleGrants, grant)
	}

	return authz.NewResourceAccessSnapshot(authz.ResourceAccessSnapshotInput{
		Subject:                subject,
		Resource:               resource,
		GroupAuthorizationMode: groupAuthorizationMode,
		Exists:                 d.Resource.Exists,
		Deleted:                d.Resource.Deleted,
		OwnerUserID:            d.Resource.OwnerUserID,
		PublicAccessLevel:      publicAccessLevel,
		AccessVersion:          d.Resource.AccessVersion,
		UserGrants:             userGrants,
		RoleGrants:             roleGrants,
	})
}

func normalizeAuthzRoleMode(value string) authz.RoleAuthorizationMode {
	switch authz.RoleAuthorizationMode(value) {
	case authz.RoleAuthorizationModeShadow:
		return authz.RoleAuthorizationModeShadow
	case authz.RoleAuthorizationModeRBAC:
		return authz.RoleAuthorizationModeRBAC
	default:
		return authz.RoleAuthorizationModeLegacy
	}
}

func buildSubjectSnapshotSQL(kind authz.SubjectKind) string {
	subjectRow, activeRoles := authzSubjectCTEs(kind)
	return fmt.Sprintf(`WITH
subject_row AS (%s),
active_roles AS (%s),
current_capabilities AS (%s),
policy_configuration AS (%s)
SELECT jsonb_build_object(
	'subject', %s,
	'configuration', %s
)::text`, subjectRow, activeRoles, authzCurrentCapabilitiesCTE(kind), authzPolicyConfigurationCTE, authzSubjectJSON(kind), authzConfigurationJSON)
}

func buildServicePrincipalSubjectSnapshotByCodeSQL() string {
	return fmt.Sprintf(`WITH
subject_row AS (
	SELECT id, status, authz_version
	FROM service_principals
	WHERE code = $1
),
active_roles AS (
		SELECT r.id, r.authz_version
		FROM service_principal_roles spr
		JOIN subject_row sr ON sr.id = spr.service_principal_id
		JOIN roles r ON r.id = spr.role_id
		WHERE spr.expires_at IS NULL OR spr.expires_at > statement_timestamp()
),
current_capabilities AS (%s),
policy_configuration AS (%s)
SELECT jsonb_build_object(
		'subject_id', (SELECT id FROM subject_row),
	'subject', %s,
	'configuration', %s
	)::text
	WHERE EXISTS (SELECT 1 FROM subject_row)`,
		authzCurrentCapabilitiesCTE(authz.SubjectKindServicePrincipal),
		authzPolicyConfigurationCTE,
		authzSubjectJSON(authz.SubjectKindServicePrincipal),
		authzConfigurationJSON,
	)
}

func buildResourceSnapshotSQL(kind authz.SubjectKind, resourceType authz.ResourceType) string {
	subjectRow, activeRoles := authzSubjectCTEs(kind)
	resourceRow, userGrants, roleGrants := authzResourceCTEs(kind, resourceType)
	return fmt.Sprintf(`WITH
subject_row AS (%s),
active_roles AS (%s),
current_capabilities AS (%s),
policy_configuration AS (%s),
resource_row AS (%s),
current_user_grants AS (%s),
current_role_grants AS (%s)
SELECT jsonb_build_object(
	'subject', %s,
	'configuration', %s,
	'resource', jsonb_build_object(
		'exists', EXISTS (SELECT 1 FROM resource_row),
		'deleted', COALESCE((SELECT deleted_at IS NOT NULL FROM resource_row), FALSE),
		'owner_user_id', (SELECT owner_user_id FROM resource_row),
		'public_access_level', (SELECT public_access_level FROM resource_row),
		'authorization_mode', (SELECT authorization_mode FROM resource_row),
		'access_version', COALESCE((SELECT access_version FROM resource_row), 0)
	),
	'user_grants', COALESCE((
		SELECT jsonb_agg(jsonb_build_object(
			'id', id,
			'access_level', access_level
		) ORDER BY id)
		FROM current_user_grants
	), '[]'::jsonb),
	'role_grants', COALESCE((
		SELECT jsonb_agg(jsonb_build_object(
			'id', id,
			'role_id', role_id,
			'access_level', access_level
		) ORDER BY id, role_id)
		FROM current_role_grants
	), '[]'::jsonb)
)::text`,
		subjectRow,
		activeRoles,
		authzCurrentCapabilitiesCTE(kind),
		authzPolicyConfigurationCTE,
		resourceRow,
		userGrants,
		roleGrants,
		authzSubjectJSON(kind),
		authzConfigurationJSON,
	)
}

func authzCurrentCapabilitiesCTE(kind authz.SubjectKind) string {
	roleCapabilities := `
		SELECT p.code
		FROM active_roles ar
		JOIN role_permissions rp ON rp.role_id = ar.id
		JOIN permissions p ON p.id = rp.permission_id
	`
	if kind != authz.SubjectKindServicePrincipal {
		return roleCapabilities
	}
	return roleCapabilities + `
		UNION
		SELECT p.code
		FROM service_principal_worker_permissions spwp
		JOIN subject_row sr ON sr.id = spwp.service_principal_id
		JOIN permissions p ON p.id = spwp.permission_id
	`
}

func authzSubjectCTEs(kind authz.SubjectKind) (subjectRow, activeRoles string) {
	switch kind {
	case authz.SubjectKindUser:
		return `
			SELECT id, status, authz_version, role, deleted_at
			FROM users
			WHERE id = $1
		`, `
			SELECT r.id, r.authz_version
			FROM user_roles ur
			JOIN roles r ON r.id = ur.role_id
			WHERE ur.user_id = $1
			  AND (ur.expires_at IS NULL OR ur.expires_at > statement_timestamp())
		`
	case authz.SubjectKindServicePrincipal:
		return `
			SELECT id, status, authz_version
			FROM service_principals
			WHERE id = $1
		`, `
			SELECT r.id, r.authz_version
			FROM service_principal_roles spr
			JOIN roles r ON r.id = spr.role_id
			WHERE spr.service_principal_id = $1
			  AND (spr.expires_at IS NULL OR spr.expires_at > statement_timestamp())
		`
	default:
		return `SELECT NULL::bigint AS id WHERE FALSE`, `SELECT NULL::bigint AS id, NULL::bigint AS authz_version WHERE FALSE`
	}
}

func authzSubjectJSON(kind authz.SubjectKind) string {
	activeExpression := `(SELECT status = 'active' FROM subject_row)`
	legacyAdminExpression := `FALSE`
	if kind == authz.SubjectKindUser {
		activeExpression = `(SELECT status = 'active' AND deleted_at IS NULL FROM subject_row)`
		legacyAdminExpression = `COALESCE((SELECT role = 'admin' AND deleted_at IS NULL FROM subject_row), FALSE)`
	}
	return fmt.Sprintf(`jsonb_build_object(
		'exists', EXISTS (SELECT 1 FROM subject_row),
		'active', COALESCE(%s, FALSE),
		'authz_version', COALESCE((SELECT authz_version FROM subject_row), 0),
		'current_legacy_admin', %s,
		'roles', COALESCE((
			SELECT jsonb_agg(jsonb_build_object('id', id, 'version', authz_version) ORDER BY id)
			FROM active_roles
		), '[]'::jsonb),
		'capabilities', COALESCE((
			SELECT jsonb_agg(code ORDER BY code)
			FROM current_capabilities
		), '[]'::jsonb)
	)`, activeExpression, legacyAdminExpression)
}

func authzResourceCTEs(kind authz.SubjectKind, resourceType authz.ResourceType) (resourceRow, userGrants, roleGrants string) {
	resourceTable := "groups"
	grantTable := "group_access_grants"
	resourceIDColumn := "group_id"
	authorizationModeColumn := "authorization_mode"
	if resourceType == authz.ResourceTypeAccount {
		resourceTable = "accounts"
		grantTable = "account_access_grants"
		resourceIDColumn = "account_id"
		authorizationModeColumn = "NULL::text AS authorization_mode"
	}
	resourceRow = fmt.Sprintf(`
			SELECT owner_user_id, public_access_level, access_version, deleted_at, %s
			FROM %s
			WHERE id = $2
		`, authorizationModeColumn, resourceTable)
	if kind == authz.SubjectKindUser {
		userGrants = fmt.Sprintf(`
			SELECT id, access_level
			FROM %s
			WHERE %s = $2
			  AND grantee_user_id = $1
			  AND (expires_at IS NULL OR expires_at > statement_timestamp())
		`, grantTable, resourceIDColumn)
	} else {
		userGrants = `SELECT NULL::bigint AS id, NULL::text AS access_level WHERE FALSE`
	}
	roleGrants = fmt.Sprintf(`
		SELECT grants.id, grants.grantee_role_id AS role_id, grants.access_level
		FROM %s grants
		JOIN active_roles ar ON ar.id = grants.grantee_role_id
		WHERE grants.%s = $2
		  AND (grants.expires_at IS NULL OR grants.expires_at > statement_timestamp())
	`, grantTable, resourceIDColumn)
	return resourceRow, userGrants, roleGrants
}

const authzPolicyConfigurationCTE = `
	SELECT
		COALESCE((SELECT value = 'true' FROM settings WHERE key = 'resource_access_control_enabled'), FALSE) AS resource_access_control_enabled,
		COALESCE((SELECT value = 'true' FROM settings WHERE key = 'self_service_hosting_enabled'), FALSE) AS self_service_hosting_enabled,
		COALESCE((SELECT value = 'true' FROM settings WHERE key = 'group_sharing_enabled'), FALSE) AS group_sharing_enabled,
		COALESCE((SELECT value = 'true' FROM settings WHERE key = 'account_sharing_enabled'), FALSE) AS account_sharing_enabled,
		COALESCE((SELECT value = 'true' FROM settings WHERE key = 'role_based_resource_grants_enabled'), FALSE) AS role_based_resource_grants_enabled,
		CASE COALESCE((SELECT value FROM settings WHERE key = 'role_authorization_mode'), 'legacy')
			WHEN 'shadow' THEN 'shadow'
			WHEN 'rbac' THEN 'rbac'
			ELSE 'legacy'
		END AS role_authorization_mode
`

const authzConfigurationJSON = `jsonb_build_object(
		'resource_access_control_enabled', (SELECT resource_access_control_enabled FROM policy_configuration),
		'self_service_hosting_enabled', (SELECT self_service_hosting_enabled FROM policy_configuration),
		'group_sharing_enabled', (SELECT group_sharing_enabled FROM policy_configuration),
	'account_sharing_enabled', (SELECT account_sharing_enabled FROM policy_configuration),
		'role_based_resource_grants_enabled', (SELECT role_based_resource_grants_enabled FROM policy_configuration),
		'role_authorization_mode', (SELECT role_authorization_mode FROM policy_configuration)
	)`
