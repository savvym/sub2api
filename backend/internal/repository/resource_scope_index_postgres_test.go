package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestResourceScopeIndexesAreUsedPostgres(t *testing.T) {
	db := newAuthzPolicyPostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatalf("apply migrations to temporary resource-scope database: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin resource-scope EXPLAIN transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	assertResourceScopeIndexesValid(t, ctx, tx)
	assertResourceScopeIndexesAreUsed(t, ctx, tx)
}

func assertResourceScopeIndexesValid(t *testing.T, ctx context.Context, tx *sql.Tx) {
	t.Helper()
	for _, testCase := range []struct {
		table     string
		index     string
		fragments []string
	}{
		{table: "accounts", index: "idx_accounts_owner_user_id", fragments: []string{"(owner_user_id)", "owner_user_id IS NOT NULL"}},
		{table: "accounts", index: "idx_accounts_public_access_level", fragments: []string{"(public_access_level, id)", "public_access_level IS NOT NULL", "deleted_at IS NULL"}},
		{table: "groups", index: "idx_groups_owner_user_id", fragments: []string{"(owner_user_id)", "owner_user_id IS NOT NULL"}},
		{table: "groups", index: "idx_groups_public_access_level", fragments: []string{"(public_access_level, id)", "public_access_level IS NOT NULL", "deleted_at IS NULL"}},
		{table: "account_access_grants", index: "idx_account_access_grants_grantee_user", fragments: []string{"(grantee_user_id, account_id)", "grantee_user_id IS NOT NULL"}},
		{table: "account_access_grants", index: "idx_account_access_grants_grantee_role", fragments: []string{"(grantee_role_id, account_id)", "grantee_role_id IS NOT NULL"}},
		{table: "account_access_grants", index: "idx_account_access_grants_expires_at", fragments: []string{"(expires_at, id)", "expires_at IS NOT NULL"}},
		{table: "group_access_grants", index: "idx_group_access_grants_grantee_user", fragments: []string{"(grantee_user_id, group_id)", "grantee_user_id IS NOT NULL"}},
		{table: "group_access_grants", index: "idx_group_access_grants_grantee_role", fragments: []string{"(grantee_role_id, group_id)", "grantee_role_id IS NOT NULL"}},
		{table: "group_access_grants", index: "idx_group_access_grants_expires_at", fragments: []string{"(expires_at, id)", "expires_at IS NOT NULL"}},
	} {
		var valid bool
		var definition string
		err := tx.QueryRowContext(ctx, `
SELECT index_state.indisvalid, pg_get_indexdef(index_state.indexrelid)
FROM pg_index AS index_state
JOIN pg_class AS index_relation ON index_relation.oid = index_state.indexrelid
JOIN pg_class AS table_relation ON table_relation.oid = index_state.indrelid
JOIN pg_namespace AS namespace ON namespace.oid = table_relation.relnamespace
WHERE namespace.nspname = 'public'
  AND table_relation.relname = $1
  AND index_relation.relname = $2
`, testCase.table, testCase.index).Scan(&valid, &definition)
		if err != nil {
			t.Fatalf("read scope index %s: %v", testCase.index, err)
		}
		if !valid {
			t.Fatalf("scope index %s is invalid", testCase.index)
		}
		for _, fragment := range testCase.fragments {
			if !strings.Contains(definition, fragment) {
				t.Fatalf("scope index %s definition missing %q: %s", testCase.index, fragment, definition)
			}
		}
	}
}

func assertResourceScopeIndexesAreUsed(t *testing.T, ctx context.Context, tx *sql.Tx) {
	t.Helper()
	subjectID := insertAuthzPolicyPostgresUser(t, ctx, tx, fmt.Sprintf("scope-index-subject-%d@example.test", time.Now().UnixNano()))
	otherUserID := insertAuthzPolicyPostgresUser(t, ctx, tx, fmt.Sprintf("scope-index-other-%d@example.test", time.Now().UnixNano()))
	setAuthzPolicyPostgresConfiguration(t, ctx, tx)

	prefix := fmt.Sprintf("scope-index-%d", time.Now().UnixNano())
	var subjectRoleID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO roles (code, name, description)
		VALUES ($1, $1, 'resource-scope EXPLAIN subject role')
		RETURNING id
	`, prefix+"-subject-role").Scan(&subjectRoleID); err != nil {
		t.Fatalf("insert subject role EXPLAIN fixture: %v", err)
	}
	var otherRoleID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO roles (code, name, description)
		VALUES ($1, $1, 'resource-scope EXPLAIN other role')
		RETURNING id
	`, prefix+"-other-role").Scan(&otherRoleID); err != nil {
		t.Fatalf("insert other role EXPLAIN fixture: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_roles (user_id, role_id, granted_by_user_id)
		VALUES ($1, $2, $3)
	`, subjectID, subjectRoleID, otherUserID); err != nil {
		t.Fatalf("assign subject role EXPLAIN fixture: %v", err)
	}

	platformSubjectID := insertAuthzPolicyPostgresUser(t, ctx, tx, prefix+"-platform@example.test")
	platformRoleID := insertAuthzPolicyPostgresRole(
		t,
		ctx,
		tx,
		prefix+"-platform-role",
		authz.CapabilityPlatformResourceViewAll,
	)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_roles (user_id, role_id, granted_by_user_id)
		VALUES ($1, $2, $3)
	`, platformSubjectID, platformRoleID, otherUserID); err != nil {
		t.Fatalf("assign platform role EXPLAIN fixture: %v", err)
	}
	var platformSubjectAuthzVersion, platformRoleAuthzVersion int64
	if err := tx.QueryRowContext(ctx, `SELECT authz_version FROM users WHERE id = $1`, platformSubjectID).Scan(&platformSubjectAuthzVersion); err != nil {
		t.Fatalf("read platform subject authz version: %v", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT authz_version FROM roles WHERE id = $1`, platformRoleID).Scan(&platformRoleAuthzVersion); err != nil {
		t.Fatalf("read platform role authz version: %v", err)
	}

	legacyAdminID := insertAuthzPolicyPostgresUser(t, ctx, tx, prefix+"-legacy-admin@example.test")
	if _, err := tx.ExecContext(ctx, `UPDATE users SET role = 'admin' WHERE id = $1`, legacyAdminID); err != nil {
		t.Fatalf("promote legacy admin EXPLAIN fixture: %v", err)
	}
	var legacyAdminAuthzVersion int64
	if err := tx.QueryRowContext(ctx, `SELECT authz_version FROM users WHERE id = $1`, legacyAdminID).Scan(&legacyAdminAuthzVersion); err != nil {
		t.Fatalf("read legacy admin authz version: %v", err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO accounts (
			name, platform, type, credentials, extra,
			owner_user_id, created_by_user_id, access_version
		)
		SELECT
			$1 || '-account-private-' || series.id,
			'openai', 'apikey', '{}'::jsonb, '{}'::jsonb,
			$2, $2, 1
		FROM generate_series(1, 20000) AS series(id)
	`, prefix, otherUserID); err != nil {
		t.Fatalf("insert private account EXPLAIN fixtures: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO groups (
			name, owner_user_id, created_by_user_id, access_version
		)
		SELECT
			$1 || '-group-private-' || series.id,
			$2, $2, 1
		FROM generate_series(1, 20000) AS series(id)
	`, prefix, otherUserID); err != nil {
		t.Fatalf("insert private group EXPLAIN fixtures: %v", err)
	}

	insertAuthzPolicyPostgresAccount(t, ctx, tx, prefix+"-account-owner", subjectID, "", false)
	insertAuthzPolicyPostgresAccount(t, ctx, tx, prefix+"-account-public", otherUserID, string(authz.AccessLevelViewer), false)
	accountDirectID := insertAuthzPolicyPostgresAccount(t, ctx, tx, prefix+"-account-direct", otherUserID, "", false)
	accountRoleID := insertAuthzPolicyPostgresAccount(t, ctx, tx, prefix+"-account-role", otherUserID, "", false)
	insertAuthzPolicyPostgresGroup(t, ctx, tx, prefix+"-group-owner", subjectID, "", false)
	insertAuthzPolicyPostgresGroup(t, ctx, tx, prefix+"-group-public", otherUserID, string(authz.AccessLevelViewer), false)
	groupDirectID := insertAuthzPolicyPostgresGroup(t, ctx, tx, prefix+"-group-direct", otherUserID, "", false)
	groupRoleID := insertAuthzPolicyPostgresGroup(t, ctx, tx, prefix+"-group-role", otherUserID, "", false)

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO account_access_grants (
			account_id, grantee_user_id, access_level, granted_by_user_id
		)
		SELECT id, $2, 'viewer', $2
		FROM accounts
		WHERE name LIKE $1 || '-account-private-%'
	`, prefix, otherUserID); err != nil {
		t.Fatalf("insert unrelated direct account Grant fixtures: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO account_access_grants (
			account_id, grantee_role_id, access_level, granted_by_user_id
		)
		SELECT id, $2, 'viewer', $3
		FROM accounts
		WHERE name LIKE $1 || '-account-private-%'
	`, prefix, otherRoleID, otherUserID); err != nil {
		t.Fatalf("insert unrelated role account Grant fixtures: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO group_access_grants (
			group_id, grantee_user_id, access_level, granted_by_user_id
		)
		SELECT id, $2, 'viewer', $2
		FROM groups
		WHERE name LIKE $1 || '-group-private-%'
	`, prefix, otherUserID); err != nil {
		t.Fatalf("insert unrelated direct group Grant fixtures: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO group_access_grants (
			group_id, grantee_role_id, access_level, granted_by_user_id
		)
		SELECT id, $2, 'viewer', $3
		FROM groups
		WHERE name LIKE $1 || '-group-private-%'
	`, prefix, otherRoleID, otherUserID); err != nil {
		t.Fatalf("insert unrelated role group Grant fixtures: %v", err)
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{
			query: `
				INSERT INTO account_access_grants (account_id, grantee_user_id, access_level, granted_by_user_id)
				SELECT id, $2, 'viewer', $3 FROM accounts
				WHERE name LIKE $1 || '-account-private-%' ORDER BY id LIMIT 128
			`,
			args: []any{prefix, subjectID, otherUserID},
		},
		{
			query: `
				INSERT INTO account_access_grants (account_id, grantee_role_id, access_level, granted_by_user_id)
				SELECT id, $2, 'viewer', $3 FROM accounts
				WHERE name LIKE $1 || '-account-private-%' ORDER BY id LIMIT 128
			`,
			args: []any{prefix, subjectRoleID, otherUserID},
		},
		{
			query: `
				INSERT INTO group_access_grants (group_id, grantee_user_id, access_level, granted_by_user_id)
				SELECT id, $2, 'viewer', $3 FROM groups
				WHERE name LIKE $1 || '-group-private-%' ORDER BY id LIMIT 128
			`,
			args: []any{prefix, subjectID, otherUserID},
		},
		{
			query: `
				INSERT INTO group_access_grants (group_id, grantee_role_id, access_level, granted_by_user_id)
				SELECT id, $2, 'viewer', $3 FROM groups
				WHERE name LIKE $1 || '-group-private-%' ORDER BY id LIMIT 128
			`,
			args: []any{prefix, subjectRoleID, otherUserID},
		},
	} {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("insert selective subject Grant EXPLAIN fixtures: %v", err)
		}
	}
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{
			query: `INSERT INTO account_access_grants (account_id, grantee_user_id, access_level, granted_by_user_id) VALUES ($1, $2, 'viewer', $3)`,
			args:  []any{accountDirectID, subjectID, otherUserID},
		},
		{
			query: `INSERT INTO account_access_grants (account_id, grantee_role_id, access_level, granted_by_user_id) VALUES ($1, $2, 'viewer', $3)`,
			args:  []any{accountRoleID, subjectRoleID, otherUserID},
		},
		{
			query: `INSERT INTO group_access_grants (group_id, grantee_user_id, access_level, granted_by_user_id) VALUES ($1, $2, 'viewer', $3)`,
			args:  []any{groupDirectID, subjectID, otherUserID},
		},
		{
			query: `INSERT INTO group_access_grants (group_id, grantee_role_id, access_level, granted_by_user_id) VALUES ($1, $2, 'viewer', $3)`,
			args:  []any{groupRoleID, subjectRoleID, otherUserID},
		},
	} {
		if _, err := tx.ExecContext(ctx, statement.query, statement.args...); err != nil {
			t.Fatalf("insert accessible Grant EXPLAIN fixture: %v", err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		ANALYZE accounts, groups, account_access_grants, group_access_grants,
			users, roles, user_roles, permissions, role_permissions, settings
	`); err != nil {
		t.Fatalf("analyze resource-scope EXPLAIN fixtures: %v", err)
	}

	for _, testCase := range []struct {
		name         string
		resourceType authz.ResourceType
		action       authz.Action
		table        string
		ownerIndex   string
		publicIndex  string
		directIndex  string
		roleIndex    string
	}{
		{
			name: "accounts", resourceType: authz.ResourceTypeAccount, action: authz.ActionAccountView,
			table: "accounts", ownerIndex: "idx_accounts_owner_user_id", publicIndex: "idx_accounts_public_access_level",
			directIndex: "idx_account_access_grants_grantee_user", roleIndex: "idx_account_access_grants_grantee_role",
		},
		{
			name: "groups", resourceType: authz.ResourceTypeGroup, action: authz.ActionGroupView,
			table: "groups", ownerIndex: "idx_groups_owner_user_id", publicIndex: "idx_groups_public_access_level",
			directIndex: "idx_group_access_grants_grantee_user", roleIndex: "idx_group_access_grants_grantee_role",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			explainClaims := func(t *testing.T, name string, claims fakeAccessibleScopeClaims) resourceScopeExplainNode {
				t.Helper()
				plan, err := newAuthzScopeSQLPlan(claims, testCase.resourceType, testCase.action)
				if err != nil {
					t.Fatalf("build %s scope plan: %v", name, err)
				}
				table := entsql.Table(testCase.table).As("resource")
				selector := entsql.Dialect(dialect.Postgres).Select(table.C("id")).From(table)
				predicate, args := plan.predicateSQL(authzScopeResourceColumns{
					id:                table.C("id"),
					deletedAt:         table.C("deleted_at"),
					ownerUserID:       table.C("owner_user_id"),
					publicAccessLevel: table.C("public_access_level"),
				})
				selector.Where(bindAuthzScopePredicate(predicate, args))
				query, queryArgs := selector.Query()
				if err := selector.Err(); err != nil {
					t.Fatalf("build %s EXPLAIN query: %v", name, err)
				}
				return queryResourceScopeExplainPlan(t, ctx, tx, query, queryArgs...)
			}

			claims := fakeAccessibleScopeClaims{
				valid:                   true,
				resourceType:            testCase.resourceType,
				action:                  testCase.action,
				subjectKind:             authz.SubjectKindUser,
				subjectID:               subjectID,
				subjectAuthzVersion:     1,
				roleVersions:            map[int64]int64{subjectRoleID: 1},
				roleMode:                authz.RoleAuthorizationModeRBAC,
				includeOwner:            true,
				includePublic:           true,
				includeDirectUserGrants: true,
				includeRoleGrants:       true,
				publicAccessLevels:      []authz.AccessLevel{authz.AccessLevelViewer, authz.AccessLevelConsumer},
				grantAccessLevels:       authz.AllAccessLevels(),
			}
			root := explainClaims(t, testCase.name+" sparse", claims)
			indexNames := resourceScopeExplainIndexNames(root)
			for _, indexName := range []string{testCase.ownerIndex, testCase.publicIndex, testCase.directIndex, testCase.roleIndex} {
				if _, ok := indexNames[indexName]; !ok {
					t.Fatalf("%s scope plan did not use %s; indexes=%v\nplan=%s", testCase.name, indexName, sortedResourceScopeIndexNames(indexNames), root.pretty())
				}
			}
			if resourceScopeExplainHasSequentialScan(root, testCase.table) {
				t.Fatalf("%s scope plan sequentially scanned %s despite sparse access; plan=%s", testCase.name, testCase.table, root.pretty())
			}

			for _, globalCase := range []struct {
				name   string
				claims fakeAccessibleScopeClaims
			}{
				{
					name: "legacy-admin-global",
					claims: fakeAccessibleScopeClaims{
						valid:                   true,
						resourceType:            testCase.resourceType,
						action:                  testCase.action,
						subjectKind:             authz.SubjectKindUser,
						subjectID:               legacyAdminID,
						subjectAuthzVersion:     legacyAdminAuthzVersion,
						roleVersions:            map[int64]int64{},
						roleMode:                authz.RoleAuthorizationModeLegacy,
						legacyAdminBypass:       true,
						includeOwner:            true,
						includePublic:           true,
						includeDirectUserGrants: true,
						includeRoleGrants:       true,
						publicAccessLevels:      []authz.AccessLevel{authz.AccessLevelViewer},
						grantAccessLevels:       authz.AllAccessLevels(),
					},
				},
				{
					name: "platform-capability-global",
					claims: fakeAccessibleScopeClaims{
						valid:                   true,
						resourceType:            testCase.resourceType,
						action:                  testCase.action,
						subjectKind:             authz.SubjectKindUser,
						subjectID:               platformSubjectID,
						subjectAuthzVersion:     platformSubjectAuthzVersion,
						roleVersions:            map[int64]int64{platformRoleID: platformRoleAuthzVersion},
						capabilities:            []authz.Capability{authz.CapabilityPlatformResourceViewAll},
						roleMode:                authz.RoleAuthorizationModeRBAC,
						platformCapability:      authz.CapabilityPlatformResourceViewAll,
						hasPlatformCapability:   true,
						includeOwner:            true,
						includePublic:           true,
						includeDirectUserGrants: true,
						includeRoleGrants:       true,
						publicAccessLevels:      []authz.AccessLevel{authz.AccessLevelViewer},
						grantAccessLevels:       authz.AllAccessLevels(),
					},
				},
			} {
				t.Run(globalCase.name, func(t *testing.T) {
					if _, err := tx.ExecContext(ctx, `
						UPDATE settings SET value = $1 WHERE key = 'role_authorization_mode'
					`, globalCase.claims.roleMode); err != nil {
						t.Fatalf("set %s role mode: %v", globalCase.name, err)
					}
					root := explainClaims(t, testCase.name+" "+globalCase.name, globalCase.claims)
					if count := resourceScopeExplainRelationCount(root, testCase.table); count != 1 {
						t.Fatalf("%s %s plan accessed %s %d times, want exactly the outer selector once; plan=%s", testCase.name, globalCase.name, testCase.table, count, root.pretty())
					}
				})
			}
		})
	}
}

type resourceScopeExplainNode struct {
	NodeType     string                     `json:"Node Type"`
	RelationName string                     `json:"Relation Name"`
	IndexName    string                     `json:"Index Name"`
	Plans        []resourceScopeExplainNode `json:"Plans"`
}

func queryResourceScopeExplainPlan(t *testing.T, ctx context.Context, tx *sql.Tx, query string, args ...any) resourceScopeExplainNode {
	t.Helper()
	var payload []byte
	if err := tx.QueryRowContext(ctx, `EXPLAIN (FORMAT JSON, COSTS OFF) `+query, args...).Scan(&payload); err != nil {
		t.Fatalf("EXPLAIN resource-scope query: %v\n%s", err, query)
	}
	var document []struct {
		Plan resourceScopeExplainNode `json:"Plan"`
	}
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatalf("decode resource-scope EXPLAIN JSON: %v\n%s", err, payload)
	}
	if len(document) != 1 {
		t.Fatalf("resource-scope EXPLAIN documents = %d, want 1", len(document))
	}
	return document[0].Plan
}

func resourceScopeExplainIndexNames(root resourceScopeExplainNode) map[string]struct{} {
	result := make(map[string]struct{})
	var walk func(resourceScopeExplainNode)
	walk = func(node resourceScopeExplainNode) {
		if node.IndexName != "" {
			result[node.IndexName] = struct{}{}
		}
		for _, child := range node.Plans {
			walk(child)
		}
	}
	walk(root)
	return result
}

func resourceScopeExplainHasSequentialScan(root resourceScopeExplainNode, relation string) bool {
	if root.NodeType == "Seq Scan" && root.RelationName == relation {
		return true
	}
	for _, child := range root.Plans {
		if resourceScopeExplainHasSequentialScan(child, relation) {
			return true
		}
	}
	return false
}

func resourceScopeExplainRelationCount(root resourceScopeExplainNode, relation string) int {
	count := 0
	if root.RelationName == relation {
		count++
	}
	for _, child := range root.Plans {
		count += resourceScopeExplainRelationCount(child, relation)
	}
	return count
}

func sortedResourceScopeIndexNames(indexes map[string]struct{}) []string {
	result := make([]string, 0, len(indexes))
	for index := range indexes {
		result = append(result, index)
	}
	sort.Strings(result)
	return result
}

func (n resourceScopeExplainNode) pretty() string {
	payload, err := json.MarshalIndent(n, "", "  ")
	if err != nil {
		return fmt.Sprintf("<marshal EXPLAIN plan: %v>", err)
	}
	return string(payload)
}
