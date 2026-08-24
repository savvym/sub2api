package repository

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

type fakeAccessibleScopeClaims struct {
	valid                   bool
	resourceType            authz.ResourceType
	action                  authz.Action
	subjectKind             authz.SubjectKind
	subjectID               int64
	subjectAuthzVersion     int64
	roleVersions            map[int64]int64
	capabilities            []authz.Capability
	roleMode                authz.RoleAuthorizationMode
	legacyAdminBypass       bool
	platformCapability      authz.Capability
	hasPlatformCapability   bool
	includeOwner            bool
	includePublic           bool
	includeDirectUserGrants bool
	includeRoleGrants       bool
	publicAccessLevels      []authz.AccessLevel
	grantAccessLevels       []authz.AccessLevel
}

func (f fakeAccessibleScopeClaims) Valid() bool                           { return f.valid }
func (f fakeAccessibleScopeClaims) ResourceType() authz.ResourceType      { return f.resourceType }
func (f fakeAccessibleScopeClaims) Action() authz.Action                  { return f.action }
func (f fakeAccessibleScopeClaims) SubjectKind() authz.SubjectKind        { return f.subjectKind }
func (f fakeAccessibleScopeClaims) SubjectID() int64                      { return f.subjectID }
func (f fakeAccessibleScopeClaims) SubjectAuthzVersion() int64            { return f.subjectAuthzVersion }
func (f fakeAccessibleScopeClaims) RoleMode() authz.RoleAuthorizationMode { return f.roleMode }
func (f fakeAccessibleScopeClaims) LegacyAdminBypass() bool               { return f.legacyAdminBypass }
func (f fakeAccessibleScopeClaims) IncludesOwner() bool                   { return f.includeOwner }
func (f fakeAccessibleScopeClaims) IncludesPublicAccess() bool            { return f.includePublic }
func (f fakeAccessibleScopeClaims) IncludesDirectUserGrants() bool        { return f.includeDirectUserGrants }
func (f fakeAccessibleScopeClaims) IncludesRoleGrants() bool              { return f.includeRoleGrants }

func (f fakeAccessibleScopeClaims) RoleVersions() map[int64]int64 {
	result := make(map[int64]int64, len(f.roleVersions))
	for roleID, version := range f.roleVersions {
		result[roleID] = version
	}
	return result
}

func (f fakeAccessibleScopeClaims) Capabilities() []authz.Capability {
	return append([]authz.Capability(nil), f.capabilities...)
}

func (f fakeAccessibleScopeClaims) PlatformCapabilityBypass() (authz.Capability, bool) {
	return f.platformCapability, f.hasPlatformCapability
}

func (f fakeAccessibleScopeClaims) PublicAccessLevels() []authz.AccessLevel {
	return append([]authz.AccessLevel(nil), f.publicAccessLevels...)
}

func (f fakeAccessibleScopeClaims) GrantAccessLevels() []authz.AccessLevel {
	return append([]authz.AccessLevel(nil), f.grantAccessLevels...)
}

func TestAuthzScopeSQLPlanRevalidatesUserAndEveryAccountAccessSource(t *testing.T) {
	t.Parallel()

	scope := fakeAccessibleScopeClaims{
		valid:                   true,
		resourceType:            authz.ResourceTypeAccount,
		action:                  authz.ActionAccountView,
		subjectKind:             authz.SubjectKindUser,
		subjectID:               42,
		subjectAuthzVersion:     7,
		roleVersions:            map[int64]int64{9: 3, 2: 5},
		capabilities:            []authz.Capability{authz.CapabilityPlatformResourceViewAll, authz.CapabilityAPIKeyCreate},
		roleMode:                authz.RoleAuthorizationModeRBAC,
		platformCapability:      authz.CapabilityPlatformResourceViewAll,
		hasPlatformCapability:   true,
		includeOwner:            true,
		includePublic:           true,
		includeDirectUserGrants: true,
		includeRoleGrants:       true,
		publicAccessLevels:      []authz.AccessLevel{authz.AccessLevelViewer, authz.AccessLevelConsumer},
		grantAccessLevels:       authz.AllAccessLevels(),
	}
	plan, err := newAuthzScopeSQLPlan(scope, authz.ResourceTypeAccount, authz.ActionAccountView)
	if err != nil {
		t.Fatalf("newAuthzScopeSQLPlan() error = %v", err)
	}
	query, args := plan.predicateSQL(authzScopeResourceColumns{
		id:                `account_row.id`,
		deletedAt:         `account_row.deleted_at`,
		ownerUserID:       `account_row.owner_user_id`,
		publicAccessLevel: `account_row.public_access_level`,
	})

	for _, fragment := range []string{
		`account_row.deleted_at IS NULL`,
		`FROM users`,
		`current_subject.status = 'active' AND current_subject.deleted_at IS NULL`,
		`assignment.expires_at > statement_timestamp()`,
		`jsonb_object_agg(active_roles.id::text, active_roles.authz_version)`,
		`jsonb_agg(current_capabilities.code ORDER BY current_capabilities.code)`,
		`policy_configuration.role_authorization_mode = ?`,
		`WHERE code = ?`,
		`account_row.owner_user_id = current_subject.id`,
		`policy_configuration.account_sharing_enabled`,
		`FROM account_access_grants direct_grant`,
		`direct_grant.grantee_user_id = current_subject.id`,
		`direct_grant.expires_at > statement_timestamp()`,
		`FROM account_access_grants role_grant`,
		`JOIN active_roles ON active_roles.id = role_grant.grantee_role_id`,
		`role_grant.expires_at > statement_timestamp()`,
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("scope SQL missing %q\n%s", fragment, query)
		}
	}
	if strings.Contains(query, ">= statement_timestamp()") {
		t.Fatalf("scope SQL accepted an expires_at equality boundary\n%s", query)
	}
	if strings.Contains(query, "expires_at > CURRENT_TIMESTAMP") {
		t.Fatalf("scope SQL must use per-statement time, not transaction start\n%s", query)
	}
	if len(args) != 9 {
		t.Fatalf("scope SQL args = %d, want 9: %#v", len(args), args)
	}
	if args[0] != int64(42) || args[1] != int64(7) || args[2] != string(authz.RoleAuthorizationModeRBAC) {
		t.Fatalf("scope freshness args = %#v", args[:3])
	}
	if args[3] != `{"2":5,"9":3}` {
		t.Fatalf("role version snapshot = %v", args[3])
	}
	if args[4] != `["api_key.create","platform.resource.view_all"]` {
		t.Fatalf("capability snapshot = %v", args[4])
	}
	if args[5] != string(authz.CapabilityPlatformResourceViewAll) {
		t.Fatalf("platform bypass capability = %v", args[5])
	}
}

func TestAuthzScopeSQLPlanKeepsServicePrincipalToRoleAndPlatformSources(t *testing.T) {
	t.Parallel()

	scope := fakeAccessibleScopeClaims{
		valid:                 true,
		resourceType:          authz.ResourceTypeGroup,
		action:                authz.ActionGroupView,
		subjectKind:           authz.SubjectKindServicePrincipal,
		subjectID:             17,
		subjectAuthzVersion:   2,
		roleVersions:          map[int64]int64{8: 1},
		capabilities:          []authz.Capability{authz.CapabilityPlatformResourceManageAll},
		roleMode:              authz.RoleAuthorizationModeRBAC,
		platformCapability:    authz.CapabilityPlatformResourceManageAll,
		hasPlatformCapability: true,
		includeRoleGrants:     true,
		grantAccessLevels:     []authz.AccessLevel{authz.AccessLevelConsumer, authz.AccessLevelMaintainer, authz.AccessLevelManager},
	}
	plan, err := newAuthzScopeSQLPlan(scope, authz.ResourceTypeGroup, authz.ActionGroupView)
	if err != nil {
		t.Fatalf("newAuthzScopeSQLPlan() error = %v", err)
	}
	query, _ := plan.predicateSQL(authzScopeResourceColumns{
		id:                `group_row.id`,
		deletedAt:         `group_row.deleted_at`,
		ownerUserID:       `group_row.owner_user_id`,
		publicAccessLevel: `group_row.public_access_level`,
	})

	for _, fragment := range []string{
		`FROM service_principals`,
		`FROM service_principal_roles assignment`,
		`assignment.expires_at > statement_timestamp()`,
		`FROM group_access_grants role_grant`,
		`role_grant.expires_at > statement_timestamp()`,
		`policy_configuration.group_sharing_enabled`,
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("service-principal scope SQL missing %q\n%s", fragment, query)
		}
	}
	for _, forbidden := range []string{
		`owner_user_id = current_subject.id`,
		`direct_grant`,
		`public_access_level = ANY`,
	} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("service-principal scope SQL included %q\n%s", forbidden, query)
		}
	}
}

func TestAuthzScopeSQLPlanSharingOffLeavesOnlyOwner(t *testing.T) {
	t.Parallel()

	scope := fakeAccessibleScopeClaims{
		valid:               true,
		resourceType:        authz.ResourceTypeGroup,
		action:              authz.ActionGroupView,
		subjectKind:         authz.SubjectKindUser,
		subjectID:           3,
		subjectAuthzVersion: 1,
		roleVersions:        map[int64]int64{},
		roleMode:            authz.RoleAuthorizationModeRBAC,
		includeOwner:        true,
	}
	plan, err := newAuthzScopeSQLPlan(scope, authz.ResourceTypeGroup, authz.ActionGroupView)
	if err != nil {
		t.Fatalf("newAuthzScopeSQLPlan() error = %v", err)
	}
	query, args := plan.predicateSQL(authzScopeResourceColumns{
		id:                `group_row.id`,
		deletedAt:         `group_row.deleted_at`,
		ownerUserID:       `group_row.owner_user_id`,
		publicAccessLevel: `group_row.public_access_level`,
	})
	if !strings.Contains(query, `group_row.owner_user_id = current_subject.id`) {
		t.Fatalf("owner branch missing\n%s", query)
	}
	for _, forbidden := range []string{"policy_configuration.group_sharing_enabled", "direct_grant", "role_grant", "public_access_level = ANY"} {
		if strings.Contains(query, forbidden) {
			t.Fatalf("sharing-disabled plan included %q\n%s", forbidden, query)
		}
	}
	if len(args) != 5 {
		t.Fatalf("owner-only args = %d, want 5", len(args))
	}
}

func TestAuthzScopeSQLPlanLegacyAdminRevalidatesCurrentLegacyRole(t *testing.T) {
	t.Parallel()

	scope := fakeAccessibleScopeClaims{
		valid:               true,
		resourceType:        authz.ResourceTypeAccount,
		action:              authz.ActionAccountView,
		subjectKind:         authz.SubjectKindUser,
		subjectID:           1,
		subjectAuthzVersion: 4,
		roleVersions:        map[int64]int64{1: 2},
		roleMode:            authz.RoleAuthorizationModeLegacy,
		legacyAdminBypass:   true,
	}
	plan, err := newAuthzScopeSQLPlan(scope, authz.ResourceTypeAccount, authz.ActionAccountView)
	if err != nil {
		t.Fatalf("newAuthzScopeSQLPlan() error = %v", err)
	}
	query, _ := plan.predicateSQL(authzScopeResourceColumns{
		id:                `account_row.id`,
		deletedAt:         `account_row.deleted_at`,
		ownerUserID:       `account_row.owner_user_id`,
		publicAccessLevel: `account_row.public_access_level`,
	})
	if !strings.Contains(query, `current_subject.role = 'admin'`) {
		t.Fatalf("legacy admin scope did not revalidate users.role\n%s", query)
	}
}

func TestAuthzScopeSQLPlanRejectsInvalidOrMismatchedScope(t *testing.T) {
	t.Parallel()

	if _, err := newAuthzScopeSQLPlan(fakeAccessibleScopeClaims{}, authz.ResourceTypeAccount, authz.ActionAccountView); err == nil {
		t.Fatal("invalid scope was accepted")
	}
	validGroup := fakeAccessibleScopeClaims{
		valid:               true,
		resourceType:        authz.ResourceTypeGroup,
		action:              authz.ActionGroupView,
		subjectKind:         authz.SubjectKindUser,
		subjectID:           1,
		subjectAuthzVersion: 1,
		roleVersions:        map[int64]int64{},
		roleMode:            authz.RoleAuthorizationModeRBAC,
		includeOwner:        true,
	}
	if _, err := newAuthzScopeSQLPlan(validGroup, authz.ResourceTypeAccount, authz.ActionAccountView); err == nil {
		t.Fatal("group scope was accepted for account predicate")
	}
	if _, err := newAuthzScopeSQLPlan(validGroup, authz.ResourceTypeGroup, authz.ActionGroupEdit); err == nil {
		t.Fatal("group.view scope was accepted for group.edit predicate")
	}
	if predicate, err := accountAccessiblePredicate(authz.AccessibleScope{}); err == nil || predicate == nil {
		t.Fatalf("zero concrete scope did not return a deny-all predicate and error: %v", err)
	}
}

func TestBindAuthzScopePredicateParticipatesInOuterPostgresNumbering(t *testing.T) {
	t.Parallel()

	selector := entsql.Dialect(dialect.Postgres).
		Select("*").
		From(entsql.Table("resources"))
	selector.Where(entsql.EQ("tenant_id", int64(9)))
	selector.Where(bindAuthzScopePredicate(
		`subject_id = ? AND access_level = ANY(?::text[])`,
		[]any{int64(42), "{viewer,consumer}"},
	))
	selector.Where(entsql.EQ("status", "active"))

	query, args := selector.Query()
	if err := selector.Err(); err != nil {
		t.Fatalf("build combined PostgreSQL predicate: %v", err)
	}
	for _, fragment := range []string{
		`"tenant_id" = $1`,
		`subject_id = $2`,
		`access_level = ANY($3::text[])`,
		`"status" = $4`,
	} {
		if !strings.Contains(query, fragment) {
			t.Fatalf("combined predicate missing %q\n%s", fragment, query)
		}
	}
	if strings.Contains(query, "?") {
		t.Fatalf("combined PostgreSQL predicate retained question-mark placeholders\n%s", query)
	}
	if want := []any{int64(9), int64(42), "{viewer,consumer}", "active"}; !reflect.DeepEqual(args, want) {
		t.Fatalf("combined predicate args = %#v, want %#v", args, want)
	}
}

func TestBindAuthzScopePredicateRejectsPlaceholderMismatch(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name  string
		query string
		args  []any
	}{
		{name: "missing argument", query: `a = ? AND b = ?`, args: []any{1}},
		{name: "extra argument", query: `a = ?`, args: []any{1, 2}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			selector := entsql.Dialect(dialect.Postgres).
				Select("*").
				From(entsql.Table("resources")).
				Where(bindAuthzScopePredicate(testCase.query, testCase.args))
			_, _ = selector.Query()
			if err := selector.Err(); err == nil || !strings.Contains(err.Error(), "placeholder count") {
				t.Fatalf("placeholder mismatch error = %v", err)
			}
		})
	}
}
