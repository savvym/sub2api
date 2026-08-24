package repository

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbgroup "github.com/Wei-Shaw/sub2api/ent/group"
	dbpredicate "github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/internal/authz"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/lib/pq"
)

type accessibleScopeClaims interface {
	Valid() bool
	ResourceType() authz.ResourceType
	Action() authz.Action
	SubjectKind() authz.SubjectKind
	SubjectID() int64
	SubjectAuthzVersion() int64
	RoleVersions() map[int64]int64
	Capabilities() []authz.Capability
	RoleMode() authz.RoleAuthorizationMode
	LegacyAdminBypass() bool
	PlatformCapabilityBypass() (authz.Capability, bool)
	IncludesOwner() bool
	IncludesPublicAccess() bool
	IncludesDirectUserGrants() bool
	IncludesRoleGrants() bool
	PublicAccessLevels() []authz.AccessLevel
	GrantAccessLevels() []authz.AccessLevel
}

type authzScopeSQLPlan struct {
	resourceType            authz.ResourceType
	subjectKind             authz.SubjectKind
	subjectID               int64
	subjectAuthzVersion     int64
	roleMode                authz.RoleAuthorizationMode
	roleVersionsJSON        string
	capabilitiesJSON        string
	legacyAdminBypass       bool
	platformCapability      authz.Capability
	hasPlatformCapability   bool
	includeOwner            bool
	includePublic           bool
	includeDirectUserGrants bool
	includeRoleGrants       bool
	publicAccessLevels      []string
	grantAccessLevels       []string
}

type authzScopeResourceColumns struct {
	id                string
	deletedAt         string
	ownerUserID       string
	publicAccessLevel string
}

func accountAccessiblePredicate(scope authz.AccessibleScope) (dbpredicate.Account, error) {
	return accountAccessiblePredicateWithClaims(scope)
}

func accountAccessiblePredicateWithClaims(scope accessibleScopeClaims) (dbpredicate.Account, error) {
	plan, err := newAuthzScopeSQLPlan(scope, authz.ResourceTypeAccount, authz.ActionAccountView)
	if err != nil {
		return dbpredicate.Account(func(selector *entsql.Selector) {
			selector.Where(entsql.False())
		}), err
	}
	return dbpredicate.Account(func(selector *entsql.Selector) {
		query, args := plan.predicateSQL(authzScopeResourceColumns{
			id:                selector.C(dbaccount.FieldID),
			deletedAt:         selector.C(dbaccount.FieldDeletedAt),
			ownerUserID:       selector.C(dbaccount.FieldOwnerUserID),
			publicAccessLevel: selector.C(dbaccount.FieldPublicAccessLevel),
		})
		selector.Where(bindAuthzScopePredicate(query, args))
	}), nil
}

func groupAccessiblePredicate(scope authz.AccessibleScope) (dbpredicate.Group, error) {
	return groupAccessiblePredicateWithClaims(scope)
}

func groupAccessiblePredicateWithClaims(scope accessibleScopeClaims) (dbpredicate.Group, error) {
	plan, err := newAuthzScopeSQLPlan(scope, authz.ResourceTypeGroup, authz.ActionGroupView)
	if err != nil {
		return dbpredicate.Group(func(selector *entsql.Selector) {
			selector.Where(entsql.False())
		}), err
	}
	return dbpredicate.Group(func(selector *entsql.Selector) {
		query, args := plan.predicateSQL(authzScopeResourceColumns{
			id:                selector.C(dbgroup.FieldID),
			deletedAt:         selector.C(dbgroup.FieldDeletedAt),
			ownerUserID:       selector.C(dbgroup.FieldOwnerUserID),
			publicAccessLevel: selector.C(dbgroup.FieldPublicAccessLevel),
		})
		selector.Where(bindAuthzScopePredicate(query, args))
	}), nil
}

// bindAuthzScopePredicate lets Ent assign dialect-specific placeholders using
// the outer selector's current argument offset. ExprP treats its SQL as raw and
// would leave predicateSQL's question-mark template unchanged on PostgreSQL.
func bindAuthzScopePredicate(query string, args []any) *entsql.Predicate {
	return entsql.P(func(builder *entsql.Builder) {
		placeholderCount := strings.Count(query, "?")
		if placeholderCount != len(args) {
			builder.AddError(fmt.Errorf(
				"authz scope predicate: placeholder count %d does not match argument count %d",
				placeholderCount,
				len(args),
			))
			return
		}
		remaining := query
		for _, arg := range args {
			prefix, suffix, _ := strings.Cut(remaining, "?")
			builder.WriteString(prefix).Arg(arg)
			remaining = suffix
		}
		builder.WriteString(remaining)
	})
}

func newAuthzScopeSQLPlan(scope accessibleScopeClaims, resourceType authz.ResourceType, expectedAction authz.Action) (authzScopeSQLPlan, error) {
	if scope == nil || !scope.Valid() || !resourceType.Valid() || !expectedAction.ValidFor(resourceType) ||
		scope.ResourceType() != resourceType || scope.Action() != expectedAction {
		return authzScopeSQLPlan{}, authz.ErrInvalidResourceRef
	}
	roleVersionsJSON, err := json.Marshal(scope.RoleVersions())
	if err != nil {
		return authzScopeSQLPlan{}, fmt.Errorf("marshal authz role versions: %w", err)
	}
	capabilities := scope.Capabilities()
	capabilityNames := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		if !capability.Valid() {
			return authzScopeSQLPlan{}, authz.ErrInvalidPolicySnapshot
		}
		capabilityNames = append(capabilityNames, string(capability))
	}
	sort.Strings(capabilityNames)
	capabilitiesJSON, err := json.Marshal(capabilityNames)
	if err != nil {
		return authzScopeSQLPlan{}, fmt.Errorf("marshal authz capabilities: %w", err)
	}
	platformCapability, hasPlatformCapability := scope.PlatformCapabilityBypass()

	return authzScopeSQLPlan{
		resourceType:            resourceType,
		subjectKind:             scope.SubjectKind(),
		subjectID:               scope.SubjectID(),
		subjectAuthzVersion:     scope.SubjectAuthzVersion(),
		roleMode:                scope.RoleMode(),
		roleVersionsJSON:        string(roleVersionsJSON),
		capabilitiesJSON:        string(capabilitiesJSON),
		legacyAdminBypass:       scope.LegacyAdminBypass(),
		platformCapability:      platformCapability,
		hasPlatformCapability:   hasPlatformCapability,
		includeOwner:            scope.IncludesOwner(),
		includePublic:           scope.IncludesPublicAccess(),
		includeDirectUserGrants: scope.IncludesDirectUserGrants(),
		includeRoleGrants:       scope.IncludesRoleGrants(),
		publicAccessLevels:      authzAccessLevelNames(scope.PublicAccessLevels()),
		grantAccessLevels:       authzAccessLevelNames(scope.GrantAccessLevels()),
	}, nil
}

func authzAccessLevelNames(levels []authz.AccessLevel) []string {
	result := make([]string, 0, len(levels))
	for _, level := range levels {
		result = append(result, string(level))
	}
	sort.Strings(result)
	return result
}

func (p authzScopeSQLPlan) predicateSQL(columns authzScopeResourceColumns) (string, []any) {
	subjectRow, activeRoles, subjectActive, legacyAdminCondition := p.subjectSQL()
	resourceTable, grantTable, resourceIDColumn, sharingFlag := p.resourceSQL()

	args := []any{p.subjectID}
	globalBypasses := make([]string, 0, 2)
	if p.legacyAdminBypass {
		globalBypasses = append(globalBypasses, legacyAdminCondition)
	}
	if p.hasPlatformCapability {
		globalBypasses = append(globalBypasses, `EXISTS (
			SELECT 1 FROM current_capabilities
			WHERE code = ?
		)`)
		args = append(args, string(p.platformCapability))
	}

	// Keep each authorization source independently indexable, then join the
	// resulting IDs back to the outer Ent query under the same snapshot checks.
	resourceSources := make([]string, 0, 4)
	// A captured global bypass supersedes sparse sources. Its current authority
	// is still revalidated below before the outer resource row is admitted.
	if len(globalBypasses) == 0 && p.includeOwner {
		resourceSources = append(resourceSources, fmt.Sprintf(`
			SELECT owner_resource.id
			FROM %s owner_resource
			WHERE owner_resource.deleted_at IS NULL
			  AND owner_resource.owner_user_id = current_subject.id
			  AND policy_configuration.resource_access_control_enabled
			  AND policy_configuration.self_service_hosting_enabled
		`, resourceTable))
	}
	if len(globalBypasses) == 0 && p.includePublic {
		resourceSources = append(resourceSources, fmt.Sprintf(`
			SELECT public_resource.id
			FROM %s public_resource
			WHERE public_resource.deleted_at IS NULL
			  AND public_resource.public_access_level = ANY(?::text[])
			  AND policy_configuration.resource_access_control_enabled
			  AND policy_configuration.self_service_hosting_enabled
			  AND policy_configuration.%s
		`, resourceTable, sharingFlag))
		args = append(args, pq.Array(p.publicAccessLevels))
	}
	if len(globalBypasses) == 0 && p.includeDirectUserGrants {
		resourceSources = append(resourceSources, fmt.Sprintf(`
			SELECT direct_grant.%s AS id
			FROM %s direct_grant
			WHERE direct_grant.grantee_user_id = current_subject.id
			  AND direct_grant.access_level = ANY(?::text[])
			  AND (direct_grant.expires_at IS NULL OR direct_grant.expires_at > statement_timestamp())
			  AND policy_configuration.resource_access_control_enabled
			  AND policy_configuration.self_service_hosting_enabled
			  AND policy_configuration.%s
		`, resourceIDColumn, grantTable, sharingFlag))
		args = append(args, pq.Array(p.grantAccessLevels))
	}
	if len(globalBypasses) == 0 && p.includeRoleGrants {
		resourceSources = append(resourceSources, fmt.Sprintf(`
			SELECT role_grant.%s AS id
			FROM %s role_grant
			JOIN active_roles ON active_roles.id = role_grant.grantee_role_id
			WHERE role_grant.access_level = ANY(?::text[])
			  AND (role_grant.expires_at IS NULL OR role_grant.expires_at > statement_timestamp())
			  AND policy_configuration.resource_access_control_enabled
			  AND policy_configuration.self_service_hosting_enabled
			  AND policy_configuration.%s
			  AND policy_configuration.role_based_resource_grants_enabled
		`, resourceIDColumn, grantTable, sharingFlag))
		args = append(args, pq.Array(p.grantAccessLevels))
	}
	args = append(args,
		p.subjectAuthzVersion,
		string(p.roleMode),
		p.roleVersionsJSON,
		p.capabilitiesJSON,
	)

	if len(globalBypasses) > 0 {
		query := fmt.Sprintf(`%s IS NULL AND EXISTS (
	WITH
	current_subject AS (%s),
	active_roles AS (%s),
	current_capabilities AS (
		SELECT DISTINCT permission.code
		FROM active_roles
		JOIN role_permissions ON role_permissions.role_id = active_roles.id
		JOIN permissions permission ON permission.id = role_permissions.permission_id
	),
	policy_configuration AS (%s)
	SELECT 1
	FROM current_subject
	CROSS JOIN policy_configuration
	WHERE %s
	  AND (%s)
	  AND current_subject.authz_version = ?
	  AND policy_configuration.role_authorization_mode = ?
	  AND COALESCE((
		SELECT jsonb_object_agg(active_roles.id::text, active_roles.authz_version)
		FROM active_roles
	  ), '{}'::jsonb) = ?::jsonb
	  AND COALESCE((
		SELECT jsonb_agg(current_capabilities.code ORDER BY current_capabilities.code)
		FROM current_capabilities
	  ), '[]'::jsonb) = ?::jsonb
	)`,
			columns.deletedAt,
			subjectRow,
			activeRoles,
			authzPolicyConfigurationCTE,
			subjectActive,
			joinSQLOr(globalBypasses),
		)
		return query, args
	}

	if len(resourceSources) == 0 {
		resourceSources = append(resourceSources, `SELECT NULL::bigint AS id WHERE FALSE`)
	}
	query := fmt.Sprintf(`%s IS NULL AND %s IN (
	WITH
	current_subject AS (%s),
	active_roles AS (%s),
	current_capabilities AS (
		SELECT DISTINCT permission.code
		FROM active_roles
		JOIN role_permissions ON role_permissions.role_id = active_roles.id
		JOIN permissions permission ON permission.id = role_permissions.permission_id
	),
	policy_configuration AS (%s)
	SELECT accessible_resource.id
	FROM current_subject
	CROSS JOIN policy_configuration
	CROSS JOIN LATERAL (
		%s
	) accessible_resource
	WHERE %s
	  AND current_subject.authz_version = ?
	  AND policy_configuration.role_authorization_mode = ?
	  AND COALESCE((
		SELECT jsonb_object_agg(active_roles.id::text, active_roles.authz_version)
		FROM active_roles
	  ), '{}'::jsonb) = ?::jsonb
	  AND COALESCE((
		SELECT jsonb_agg(current_capabilities.code ORDER BY current_capabilities.code)
		FROM current_capabilities
	  ), '[]'::jsonb) = ?::jsonb
	)`,
		columns.deletedAt,
		columns.id,
		subjectRow,
		activeRoles,
		authzPolicyConfigurationCTE,
		joinSQLUnionAll(resourceSources),
		subjectActive,
	)
	return query, args
}

func (p authzScopeSQLPlan) subjectSQL() (subjectRow, activeRoles, activeCondition, legacyAdminCondition string) {
	switch p.subjectKind {
	case authz.SubjectKindUser:
		return `
			SELECT id, status, authz_version, role, deleted_at
			FROM users
			WHERE id = ?
		`, `
			SELECT role.id, role.authz_version
			FROM user_roles assignment
			JOIN roles role ON role.id = assignment.role_id
			JOIN current_subject ON current_subject.id = assignment.user_id
			WHERE assignment.expires_at IS NULL OR assignment.expires_at > statement_timestamp()
		`, `current_subject.status = 'active' AND current_subject.deleted_at IS NULL`, `current_subject.role = 'admin'`
	case authz.SubjectKindServicePrincipal:
		return `
			SELECT id, code, status, authz_version
			FROM service_principals
			WHERE id = ?
		`, `
			SELECT role.id, role.authz_version
			FROM service_principal_roles assignment
			JOIN roles role ON role.id = assignment.role_id
			JOIN current_subject ON current_subject.id = assignment.service_principal_id
			WHERE assignment.expires_at IS NULL OR assignment.expires_at > statement_timestamp()
		`, `current_subject.status = 'active'`, fmt.Sprintf(
				`current_subject.code = %s`, pq.QuoteLiteral(authz.AdminAPIKeyServicePrincipalCode),
			)
	default:
		return `SELECT NULL::bigint AS id WHERE FALSE`, `SELECT NULL::bigint AS id, NULL::bigint AS authz_version WHERE FALSE`, `FALSE`, `FALSE`
	}
}

func (p authzScopeSQLPlan) resourceSQL() (resourceTable, grantTable, resourceIDColumn, sharingFlag string) {
	if p.resourceType == authz.ResourceTypeAccount {
		return "accounts", "account_access_grants", "account_id", "account_sharing_enabled"
	}
	return "groups", "group_access_grants", "group_id", "group_sharing_enabled"
}

func joinSQLUnionAll(branches []string) string {
	return strings.Join(branches, "\n\t\tUNION ALL\n")
}

func joinSQLOr(branches []string) string {
	return strings.Join(branches, "\n\t\tOR ")
}
