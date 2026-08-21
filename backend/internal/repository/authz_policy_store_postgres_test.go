package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbgroup "github.com/Wei-Shaw/sub2api/ent/group"
	dbpredicate "github.com/Wei-Shaw/sub2api/ent/predicate"
	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/lib/pq"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

const authzPolicyPostgresAdminDSNEnv = "SUB2API_AUTHZ_POLICY_POSTGRES_ADMIN_DSN"

// Run with a PostgreSQL administrator URL whose role may create databases:
//
//	SUB2API_AUTHZ_POLICY_POSTGRES_ADMIN_DSN='postgres://user@127.0.0.1:5432/postgres?sslmode=disable' \
//	  go test ./internal/repository -run '^TestAuthzPolicyStorePostgresCTESnapshotAndExpiry$' -count=1 -v
//
// The test creates and drops an isolated database. It never runs migrations or
// fixtures against the database named in the supplied URL.
func TestAuthzPolicyStorePostgresCTESnapshotAndExpiry(t *testing.T) {
	db := newAuthzPolicyPostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatalf("apply migrations to temporary authz database: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin fixture transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL TIME ZONE 'UTC'`); err != nil {
		t.Fatalf("set fixture timezone: %v", err)
	}

	ownerID := insertAuthzPolicyPostgresUser(t, ctx, tx, "authz-owner@example.test")
	subjectID := insertAuthzPolicyPostgresUser(t, ctx, tx, "authz-subject@example.test")
	otherUserID := insertAuthzPolicyPostgresUser(t, ctx, tx, "authz-other@example.test")
	activeRoleID := insertAuthzPolicyPostgresRole(t, ctx, tx, "authz_active", authz.CapabilityAccountCreate)
	boundaryRoleID := insertAuthzPolicyPostgresRole(t, ctx, tx, "authz_boundary", authz.CapabilityGroupCreate)
	pastRoleID := insertAuthzPolicyPostgresRole(t, ctx, tx, "authz_past", authz.CapabilityResourceShare)
	unassignedRoleID := insertAuthzPolicyPostgresRole(t, ctx, tx, "authz_unassigned", authz.CapabilityPlatformResourceManageAll)

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_roles (user_id, role_id, granted_by_user_id, expires_at)
		VALUES
			($1, $2, $3, CURRENT_TIMESTAMP + INTERVAL '1 hour'),
			($1, $4, $3, CURRENT_TIMESTAMP),
			($1, $5, $3, CURRENT_TIMESTAMP - INTERVAL '1 second')
	`, subjectID, activeRoleID, ownerID, boundaryRoleID, pastRoleID); err != nil {
		t.Fatalf("insert role assignments: %v", err)
	}
	var servicePrincipalID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO service_principals (code, name, status, authz_version)
		VALUES ('authz_policy_worker', 'Authz Policy Worker', 'active', 5)
		RETURNING id
	`).Scan(&servicePrincipalID); err != nil {
		t.Fatalf("insert service principal: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO service_principal_roles (service_principal_id, role_id, granted_by_user_id, expires_at)
		VALUES
			($1, $2, $3, CURRENT_TIMESTAMP + INTERVAL '1 hour'),
			($1, $4, $3, CURRENT_TIMESTAMP),
			($1, $5, $3, CURRENT_TIMESTAMP - INTERVAL '1 second')
	`, servicePrincipalID, activeRoleID, ownerID, boundaryRoleID, pastRoleID); err != nil {
		t.Fatalf("insert service principal role assignments: %v", err)
	}
	setAuthzPolicyPostgresConfiguration(t, ctx, tx)

	var accountID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO accounts (
			name, platform, type, credentials, extra,
			owner_user_id, created_by_user_id, public_access_level, access_version
		)
		VALUES ('authz-policy-account', 'openai', 'apikey', '{}'::jsonb, '{}'::jsonb, $1, $1, 'viewer', 6)
		RETURNING id
	`, ownerID).Scan(&accountID); err != nil {
		t.Fatalf("insert account fixture: %v", err)
	}

	directGrantID := insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO account_access_grants (
			account_id, grantee_user_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'consumer', $3, CURRENT_TIMESTAMP + INTERVAL '1 hour')
		RETURNING id
	`, accountID, subjectID, ownerID)
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO account_access_grants (
			account_id, grantee_user_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'manager', $3, CURRENT_TIMESTAMP + INTERVAL '1 hour')
		RETURNING id
	`, accountID, otherUserID, ownerID)
	roleGrantID := insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO account_access_grants (
			account_id, grantee_role_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'maintainer', $3, CURRENT_TIMESTAMP + INTERVAL '1 hour')
		RETURNING id
	`, accountID, activeRoleID, ownerID)
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO account_access_grants (
			account_id, grantee_role_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'manager', $3, CURRENT_TIMESTAMP + INTERVAL '1 hour')
		RETURNING id
	`, accountID, unassignedRoleID, ownerID)
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO account_access_grants (
			account_id, grantee_role_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP)
		RETURNING id
	`, accountID, boundaryRoleID, ownerID)

	subject := mustAuthzSubjectRef(t, authz.SubjectKindUser, subjectID)
	resource := mustAuthzResourceRef(t, authz.ResourceTypeAccount, accountID)
	store := newAuthzPolicyStoreWithQueryer(tx)

	subjectSnapshot, err := store.LoadSubjectSnapshot(ctx, subject)
	if err != nil {
		t.Fatalf("load PostgreSQL subject snapshot: %v", err)
	}
	if !subjectSnapshot.Valid() || !subjectSnapshot.Exists() || !subjectSnapshot.Active() {
		t.Fatalf("unexpected PostgreSQL subject state: %+v", subjectSnapshot)
	}
	if got, want := subjectSnapshot.RoleVersions(), map[int64]int64{activeRoleID: 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("active PostgreSQL roles = %v, want %v", got, want)
	}
	if got, want := subjectSnapshot.Capabilities(), []authz.Capability{authz.CapabilityAccountCreate}; !reflect.DeepEqual(got, want) {
		t.Fatalf("active PostgreSQL capabilities = %v, want %v", got, want)
	}
	assertFullyEnabledPolicyConfiguration(t, subjectSnapshot.Configuration())
	servicePrincipalSnapshot, err := store.LoadServicePrincipalSubjectSnapshotByCode(ctx, "authz_policy_worker")
	if err != nil {
		t.Fatalf("load PostgreSQL service principal snapshot by code: %v", err)
	}
	wantServicePrincipalSubject := mustAuthzSubjectRef(t, authz.SubjectKindServicePrincipal, servicePrincipalID)
	if !servicePrincipalSnapshot.Valid() || servicePrincipalSnapshot.Subject() != wantServicePrincipalSubject ||
		!servicePrincipalSnapshot.Exists() || !servicePrincipalSnapshot.Active() || servicePrincipalSnapshot.AuthzVersion() != 5 {
		t.Fatalf("unexpected PostgreSQL service principal state: %+v", servicePrincipalSnapshot)
	}
	if got, want := servicePrincipalSnapshot.RoleVersions(), map[int64]int64{activeRoleID: 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("active PostgreSQL service principal roles = %v, want %v", got, want)
	}
	if got, want := servicePrincipalSnapshot.Capabilities(), []authz.Capability{authz.CapabilityAccountCreate}; !reflect.DeepEqual(got, want) {
		t.Fatalf("active PostgreSQL service principal capabilities = %v, want %v", got, want)
	}
	assertFullyEnabledPolicyConfiguration(t, servicePrincipalSnapshot.Configuration())
	missingServicePrincipalSnapshot, err := store.LoadServicePrincipalSubjectSnapshotByCode(ctx, "authz_policy_missing")
	if !errors.Is(err, authz.ErrSubjectNotFound) || errors.Is(err, sql.ErrNoRows) || missingServicePrincipalSnapshot.Valid() {
		t.Fatalf("missing PostgreSQL service principal result: snapshot=%+v err=%v", missingServicePrincipalSnapshot, err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE service_principals
		SET status = 'disabled'
		WHERE id = $1
	`, servicePrincipalID); err != nil {
		t.Fatalf("disable service principal: %v", err)
	}
	servicePrincipalSnapshot, err = store.LoadServicePrincipalSubjectSnapshotByCode(ctx, "authz_policy_worker")
	if err != nil {
		t.Fatalf("load disabled PostgreSQL service principal snapshot by code: %v", err)
	}
	if !servicePrincipalSnapshot.Valid() || !servicePrincipalSnapshot.Exists() || servicePrincipalSnapshot.Active() {
		t.Fatalf("unexpected disabled PostgreSQL service principal state: %+v", servicePrincipalSnapshot)
	}

	resourceSnapshot, err := store.LoadResourceAccessSnapshot(ctx, subject, resource)
	if err != nil {
		t.Fatalf("load PostgreSQL resource snapshot: %v", err)
	}
	assertAuthzPolicyPostgresResourceState(t, resourceSnapshot, resource, ownerID, directGrantID, roleGrantID, activeRoleID)
	assertAuthzPolicyPostgresEntScopes(t, ctx, tx, subjectSnapshot, ownerID, subjectID, otherUserID, activeRoleID, accountID)

	if _, err := tx.ExecContext(ctx, `
		UPDATE account_access_grants
		SET expires_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`, directGrantID); err != nil {
		t.Fatalf("move direct grant to expiry boundary: %v", err)
	}
	assertAuthzPolicyPostgresExpiryEqualsDatabaseNow(t, ctx, tx, directGrantID)
	resourceSnapshot, err = store.LoadResourceAccessSnapshot(ctx, subject, resource)
	if err != nil {
		t.Fatalf("load snapshot at direct-grant expiry boundary: %v", err)
	}
	if len(resourceSnapshot.UserGrants()) != 0 || len(resourceSnapshot.RoleGrants()) != 1 ||
		resourceSnapshot.RoleGrants()[0].GrantID() != roleGrantID {
		t.Fatalf("direct grant did not expire exactly at database time: user=%+v role=%+v", resourceSnapshot.UserGrants(), resourceSnapshot.RoleGrants())
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE account_access_grants
		SET expires_at = CURRENT_TIMESTAMP
		WHERE id = $1
	`, roleGrantID); err != nil {
		t.Fatalf("move role grant to expiry boundary: %v", err)
	}
	assertAuthzPolicyPostgresExpiryEqualsDatabaseNow(t, ctx, tx, roleGrantID)
	resourceSnapshot, err = store.LoadResourceAccessSnapshot(ctx, subject, resource)
	if err != nil {
		t.Fatalf("load snapshot at role-grant expiry boundary: %v", err)
	}
	if len(resourceSnapshot.UserGrants()) != 0 || len(resourceSnapshot.RoleGrants()) != 0 {
		t.Fatalf("grants did not expire exactly at database time: user=%+v role=%+v", resourceSnapshot.UserGrants(), resourceSnapshot.RoleGrants())
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE user_roles
		SET expires_at = CURRENT_TIMESTAMP
		WHERE user_id = $1 AND role_id = $2
	`, subjectID, activeRoleID); err != nil {
		t.Fatalf("move role assignment to expiry boundary: %v", err)
	}
	subjectSnapshot, err = store.LoadSubjectSnapshot(ctx, subject)
	if err != nil {
		t.Fatalf("load snapshot at role-assignment expiry boundary: %v", err)
	}
	if len(subjectSnapshot.RoleVersions()) != 0 || len(subjectSnapshot.Capabilities()) != 0 {
		t.Fatalf("role assignment did not expire exactly at database time: roles=%v capabilities=%v", subjectSnapshot.RoleVersions(), subjectSnapshot.Capabilities())
	}
}

func assertAuthzPolicyPostgresEntScopes(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	subjectSnapshot authz.SubjectSnapshot,
	ownerID int64,
	subjectID int64,
	otherUserID int64,
	activeRoleID int64,
	existingAccountID int64,
) {
	t.Helper()
	viewer := string(authz.AccessLevelViewer)
	ownerAccountID := insertAuthzPolicyPostgresAccount(t, ctx, tx, "authz-scope-account-owner", subjectID, "", false)
	publicAccountID := insertAuthzPolicyPostgresAccount(t, ctx, tx, "authz-scope-account-public", otherUserID, viewer, false)
	directAccountID := insertAuthzPolicyPostgresAccount(t, ctx, tx, "authz-scope-account-direct", otherUserID, "", false)
	roleAccountID := insertAuthzPolicyPostgresAccount(t, ctx, tx, "authz-scope-account-role", otherUserID, "", false)
	_ = insertAuthzPolicyPostgresAccount(t, ctx, tx, "authz-scope-account-private", otherUserID, "", false)
	expiredDirectAccountID := insertAuthzPolicyPostgresAccount(t, ctx, tx, "authz-scope-account-expired-direct", otherUserID, "", false)
	expiredRoleAccountID := insertAuthzPolicyPostgresAccount(t, ctx, tx, "authz-scope-account-expired-role", otherUserID, "", false)
	_ = insertAuthzPolicyPostgresAccount(t, ctx, tx, "authz-scope-account-deleted-public", otherUserID, viewer, true)

	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO account_access_grants (
			account_id, grantee_user_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP + INTERVAL '1 hour')
		RETURNING id
	`, directAccountID, subjectID, ownerID)
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO account_access_grants (
			account_id, grantee_role_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP + INTERVAL '1 hour')
		RETURNING id
	`, roleAccountID, activeRoleID, ownerID)
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO account_access_grants (
			account_id, grantee_user_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP)
		RETURNING id
	`, expiredDirectAccountID, subjectID, ownerID)
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO account_access_grants (
			account_id, grantee_role_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP)
		RETURNING id
	`, expiredRoleAccountID, activeRoleID, ownerID)

	ownerGroupID := insertAuthzPolicyPostgresGroup(t, ctx, tx, "authz-scope-group-owner", subjectID, "", false)
	publicGroupID := insertAuthzPolicyPostgresGroup(t, ctx, tx, "authz-scope-group-public", otherUserID, viewer, false)
	directGroupID := insertAuthzPolicyPostgresGroup(t, ctx, tx, "authz-scope-group-direct", otherUserID, "", false)
	roleGroupID := insertAuthzPolicyPostgresGroup(t, ctx, tx, "authz-scope-group-role", otherUserID, "", false)
	_ = insertAuthzPolicyPostgresGroup(t, ctx, tx, "authz-scope-group-private", otherUserID, "", false)
	expiredDirectGroupID := insertAuthzPolicyPostgresGroup(t, ctx, tx, "authz-scope-group-expired-direct", otherUserID, "", false)
	expiredRoleGroupID := insertAuthzPolicyPostgresGroup(t, ctx, tx, "authz-scope-group-expired-role", otherUserID, "", false)
	_ = insertAuthzPolicyPostgresGroup(t, ctx, tx, "authz-scope-group-deleted-public", otherUserID, viewer, true)

	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO group_access_grants (
			group_id, grantee_user_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP + INTERVAL '1 hour')
		RETURNING id
	`, directGroupID, subjectID, ownerID)
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO group_access_grants (
			group_id, grantee_role_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP + INTERVAL '1 hour')
		RETURNING id
	`, roleGroupID, activeRoleID, ownerID)
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO group_access_grants (
			group_id, grantee_user_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP)
		RETURNING id
	`, expiredDirectGroupID, subjectID, ownerID)
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO group_access_grants (
			group_id, grantee_role_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP)
		RETURNING id
	`, expiredRoleGroupID, activeRoleID, ownerID)

	recorder := &authzPolicyPostgresQueryRecorder{delegate: tx}
	driver := entsql.NewDriver(dialect.Postgres, entsql.Conn{ExecQuerier: recorder})
	client := dbent.NewClient(dbent.Driver(driver))

	accountClaims := newAuthzPolicyPostgresViewClaims(subjectSnapshot, authz.ResourceTypeAccount, authz.ActionAccountView)
	accountPlan, err := newAuthzScopeSQLPlan(accountClaims, authz.ResourceTypeAccount, authz.ActionAccountView)
	if err != nil {
		t.Fatalf("build PostgreSQL account scope plan: %v", err)
	}
	accountPredicate := dbpredicate.Account(func(selector *entsql.Selector) {
		query, args := accountPlan.predicateSQL(authzScopeResourceColumns{
			id:                selector.C(dbaccount.FieldID),
			deletedAt:         selector.C(dbaccount.FieldDeletedAt),
			ownerUserID:       selector.C(dbaccount.FieldOwnerUserID),
			publicAccessLevel: selector.C(dbaccount.FieldPublicAccessLevel),
		})
		selector.Where(bindAuthzScopePredicate(query, args))
	})
	accountIDs, err := client.Account.Query().
		Where(accountPredicate).
		Order(dbaccount.ByID()).
		IDs(ctx)
	if err != nil {
		t.Fatalf("execute PostgreSQL account Ent scope: %v\n%s", err, recorder.lastQuery())
	}
	wantAccountIDs := sortedAuthzPolicyPostgresIDs(existingAccountID, ownerAccountID, publicAccountID, directAccountID, roleAccountID)
	if !reflect.DeepEqual(accountIDs, wantAccountIDs) {
		t.Fatalf("PostgreSQL account scope IDs = %v, want %v", accountIDs, wantAccountIDs)
	}
	accountCount, err := client.Account.Query().Where(accountPredicate).Count(ctx)
	if err != nil {
		t.Fatalf("count PostgreSQL account Ent scope: %v", err)
	}
	if accountCount != len(wantAccountIDs) {
		t.Fatalf("PostgreSQL account scope count = %d, want %d", accountCount, len(wantAccountIDs))
	}
	accountPage, err := client.Account.Query().
		Where(accountPredicate).
		Order(dbaccount.ByID()).
		Offset(1).
		Limit(2).
		IDs(ctx)
	if err != nil {
		t.Fatalf("page PostgreSQL account Ent scope: %v", err)
	}
	if want := wantAccountIDs[1:3]; !reflect.DeepEqual(accountPage, want) {
		t.Fatalf("PostgreSQL account scope page = %v, want %v", accountPage, want)
	}

	groupClaims := newAuthzPolicyPostgresViewClaims(subjectSnapshot, authz.ResourceTypeGroup, authz.ActionGroupView)
	groupPlan, err := newAuthzScopeSQLPlan(groupClaims, authz.ResourceTypeGroup, authz.ActionGroupView)
	if err != nil {
		t.Fatalf("build PostgreSQL group scope plan: %v", err)
	}
	groupPredicate := dbpredicate.Group(func(selector *entsql.Selector) {
		query, args := groupPlan.predicateSQL(authzScopeResourceColumns{
			id:                selector.C(dbgroup.FieldID),
			deletedAt:         selector.C(dbgroup.FieldDeletedAt),
			ownerUserID:       selector.C(dbgroup.FieldOwnerUserID),
			publicAccessLevel: selector.C(dbgroup.FieldPublicAccessLevel),
		})
		selector.Where(bindAuthzScopePredicate(query, args))
	})
	groupIDs, err := client.Group.Query().
		Where(groupPredicate).
		Order(dbgroup.ByID()).
		IDs(ctx)
	if err != nil {
		t.Fatalf("execute PostgreSQL group Ent scope: %v", err)
	}
	wantGroupIDs := sortedAuthzPolicyPostgresIDs(ownerGroupID, publicGroupID, directGroupID, roleGroupID)
	if !reflect.DeepEqual(groupIDs, wantGroupIDs) {
		t.Fatalf("PostgreSQL group scope IDs = %v, want %v", groupIDs, wantGroupIDs)
	}
	groupCount, err := client.Group.Query().Where(groupPredicate).Count(ctx)
	if err != nil {
		t.Fatalf("count PostgreSQL group Ent scope: %v", err)
	}
	if groupCount != len(wantGroupIDs) {
		t.Fatalf("PostgreSQL group scope count = %d, want %d", groupCount, len(wantGroupIDs))
	}
	groupPage, err := client.Group.Query().
		Where(groupPredicate).
		Order(dbgroup.ByID()).
		Offset(1).
		Limit(2).
		IDs(ctx)
	if err != nil {
		t.Fatalf("page PostgreSQL group Ent scope: %v", err)
	}
	if want := wantGroupIDs[1:3]; !reflect.DeepEqual(groupPage, want) {
		t.Fatalf("PostgreSQL group scope page = %v, want %v", groupPage, want)
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE users
		SET authz_version = authz_version + 1
		WHERE id = $1
	`, subjectID); err != nil {
		t.Fatalf("make PostgreSQL scope subject version stale: %v", err)
	}
	staleAccountCount, err := client.Account.Query().Where(accountPredicate).Count(ctx)
	if err != nil {
		t.Fatalf("count stale PostgreSQL account scope: %v", err)
	}
	staleGroupCount, err := client.Group.Query().Where(groupPredicate).Count(ctx)
	if err != nil {
		t.Fatalf("count stale PostgreSQL group scope: %v", err)
	}
	if staleAccountCount != 0 || staleGroupCount != 0 {
		t.Fatalf("stale PostgreSQL scope remained usable: accounts=%d groups=%d", staleAccountCount, staleGroupCount)
	}

	if len(recorder.queries) != 8 {
		t.Fatalf("Ent scope query count = %d, want 8", len(recorder.queries))
	}
	for _, index := range []int{0, 1, 2, 6} {
		assertAuthzPolicyPostgresEntQuery(t, recorder.queries[index], "accounts", "account_access_grants")
	}
	for _, index := range []int{3, 4, 5, 7} {
		assertAuthzPolicyPostgresEntQuery(t, recorder.queries[index], "groups", "group_access_grants")
	}
}

type authzPolicyPostgresViewClaims struct {
	resourceType       authz.ResourceType
	action             authz.Action
	subject            authz.SubjectRef
	authzVersion       int64
	roleVersions       map[int64]int64
	capabilities       []authz.Capability
	roleMode           authz.RoleAuthorizationMode
	publicAccessLevels []authz.AccessLevel
	grantAccessLevels  []authz.AccessLevel
}

func newAuthzPolicyPostgresViewClaims(
	snapshot authz.SubjectSnapshot,
	resourceType authz.ResourceType,
	action authz.Action,
) authzPolicyPostgresViewClaims {
	return authzPolicyPostgresViewClaims{
		resourceType:       resourceType,
		action:             action,
		subject:            snapshot.Subject(),
		authzVersion:       snapshot.AuthzVersion(),
		roleVersions:       snapshot.RoleVersions(),
		capabilities:       snapshot.Capabilities(),
		roleMode:           snapshot.Configuration().RoleMode(),
		publicAccessLevels: []authz.AccessLevel{authz.AccessLevelViewer, authz.AccessLevelConsumer},
		grantAccessLevels:  authz.AllAccessLevels(),
	}
}

func (c authzPolicyPostgresViewClaims) Valid() bool                           { return true }
func (c authzPolicyPostgresViewClaims) ResourceType() authz.ResourceType      { return c.resourceType }
func (c authzPolicyPostgresViewClaims) Action() authz.Action                  { return c.action }
func (c authzPolicyPostgresViewClaims) SubjectKind() authz.SubjectKind        { return c.subject.Kind() }
func (c authzPolicyPostgresViewClaims) SubjectID() int64                      { return c.subject.ID() }
func (c authzPolicyPostgresViewClaims) SubjectAuthzVersion() int64            { return c.authzVersion }
func (c authzPolicyPostgresViewClaims) RoleMode() authz.RoleAuthorizationMode { return c.roleMode }
func (c authzPolicyPostgresViewClaims) LegacyAdminBypass() bool               { return false }
func (c authzPolicyPostgresViewClaims) IncludesOwner() bool                   { return true }
func (c authzPolicyPostgresViewClaims) IncludesPublicAccess() bool            { return true }
func (c authzPolicyPostgresViewClaims) IncludesDirectUserGrants() bool        { return true }
func (c authzPolicyPostgresViewClaims) IncludesRoleGrants() bool              { return true }
func (c authzPolicyPostgresViewClaims) PlatformCapabilityBypass() (authz.Capability, bool) {
	return "", false
}
func (c authzPolicyPostgresViewClaims) RoleVersions() map[int64]int64 {
	return cloneAuthzPolicyPostgresRoleVersions(c.roleVersions)
}
func (c authzPolicyPostgresViewClaims) Capabilities() []authz.Capability {
	return append([]authz.Capability(nil), c.capabilities...)
}
func (c authzPolicyPostgresViewClaims) PublicAccessLevels() []authz.AccessLevel {
	return append([]authz.AccessLevel(nil), c.publicAccessLevels...)
}
func (c authzPolicyPostgresViewClaims) GrantAccessLevels() []authz.AccessLevel {
	return append([]authz.AccessLevel(nil), c.grantAccessLevels...)
}

type authzPolicyPostgresRecordedQuery struct {
	query string
	args  []any
}

type authzPolicyPostgresQueryRecorder struct {
	delegate *sql.Tx
	queries  []authzPolicyPostgresRecordedQuery
}

func (r *authzPolicyPostgresQueryRecorder) lastQuery() string {
	if len(r.queries) == 0 {
		return "<no query recorded>"
	}
	return r.queries[len(r.queries)-1].query
}

func (r *authzPolicyPostgresQueryRecorder) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return r.delegate.ExecContext(ctx, query, args...)
}

func (r *authzPolicyPostgresQueryRecorder) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	r.queries = append(r.queries, authzPolicyPostgresRecordedQuery{
		query: query,
		args:  append([]any(nil), args...),
	})
	return r.delegate.QueryContext(ctx, query, args...)
}

func insertAuthzPolicyPostgresAccount(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	name string,
	ownerID int64,
	publicAccessLevel string,
	deleted bool,
) int64 {
	t.Helper()
	var publicValue any
	if publicAccessLevel != "" {
		publicValue = publicAccessLevel
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO accounts (
			name, platform, type, credentials, extra,
			owner_user_id, created_by_user_id, public_access_level, access_version, deleted_at
		)
		VALUES (
			$1, 'openai', 'apikey', '{}'::jsonb, '{}'::jsonb,
			$2, $2, $3, 1, CASE WHEN $4::boolean THEN CURRENT_TIMESTAMP ELSE NULL END
		)
		RETURNING id
	`, name, ownerID, publicValue, deleted).Scan(&id); err != nil {
		t.Fatalf("insert PostgreSQL account %s: %v", name, err)
	}
	return id
}

func insertAuthzPolicyPostgresGroup(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	name string,
	ownerID int64,
	publicAccessLevel string,
	deleted bool,
) int64 {
	t.Helper()
	var publicValue any
	if publicAccessLevel != "" {
		publicValue = publicAccessLevel
	}
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO groups (
			name, owner_user_id, created_by_user_id, public_access_level, access_version, deleted_at
		)
		VALUES (
			$1, $2, $2, $3, 1, CASE WHEN $4::boolean THEN CURRENT_TIMESTAMP ELSE NULL END
		)
		RETURNING id
	`, name, ownerID, publicValue, deleted).Scan(&id); err != nil {
		t.Fatalf("insert PostgreSQL group %s: %v", name, err)
	}
	return id
}

func sortedAuthzPolicyPostgresIDs(ids ...int64) []int64 {
	result := append([]int64(nil), ids...)
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func cloneAuthzPolicyPostgresRoleVersions(source map[int64]int64) map[int64]int64 {
	result := make(map[int64]int64, len(source))
	for roleID, version := range source {
		result[roleID] = version
	}
	return result
}

func assertAuthzPolicyPostgresEntQuery(
	t *testing.T,
	recorded authzPolicyPostgresRecordedQuery,
	resourceTable string,
	grantTable string,
) {
	t.Helper()
	if strings.Contains(recorded.query, "?") {
		t.Fatalf("Ent did not rewrite question-mark placeholders:\n%s", recorded.query)
	}
	for _, fragment := range []string{
		`FROM "` + resourceTable + `"`,
		"WITH",
		"current_subject AS",
		"FROM " + grantTable + " direct_grant",
		"FROM " + grantTable + " role_grant",
		"expires_at > CURRENT_TIMESTAMP",
	} {
		if !strings.Contains(recorded.query, fragment) {
			t.Fatalf("Ent scope SQL missing %q:\n%s", fragment, recorded.query)
		}
	}
	if len(recorded.args) != 8 {
		t.Fatalf("Ent scope SQL args = %d, want 8: %#v", len(recorded.args), recorded.args)
	}
	placeholderPattern := regexp.MustCompile(`\$([0-9]+)`)
	maxPlaceholder := 0
	for _, match := range placeholderPattern.FindAllStringSubmatch(recorded.query, -1) {
		value, err := strconv.Atoi(match[1])
		if err != nil {
			t.Fatalf("parse Ent placeholder %q: %v", match[0], err)
		}
		if value > maxPlaceholder {
			maxPlaceholder = value
		}
	}
	if maxPlaceholder != len(recorded.args) {
		t.Fatalf("Ent max placeholder = $%d, args = %d:\n%s", maxPlaceholder, len(recorded.args), recorded.query)
	}
}

func newAuthzPolicyPostgresTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	adminDSN := strings.TrimSpace(os.Getenv(authzPolicyPostgresAdminDSNEnv))
	if adminDSN == "" {
		t.Skipf("set %s to run the isolated PostgreSQL authz test", authzPolicyPostgresAdminDSNEnv)
	}
	parsed, err := url.Parse(adminDSN)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" {
		t.Fatalf("%s must be a PostgreSQL URL", authzPolicyPostgresAdminDSNEnv)
	}

	adminDB, err := sql.Open("postgres", adminDSN)
	if err != nil {
		t.Fatalf("open PostgreSQL administrator connection: %v", err)
	}
	t.Cleanup(func() { _ = adminDB.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := adminDB.PingContext(ctx); err != nil {
		t.Fatalf("ping PostgreSQL administrator connection: %v", err)
	}

	databaseName := fmt.Sprintf("sub2api_authz_policy_it_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := adminDB.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(databaseName)); err != nil {
		t.Fatalf("create isolated PostgreSQL database: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cleanupCancel()
		if _, dropErr := adminDB.ExecContext(cleanupCtx, "DROP DATABASE "+pq.QuoteIdentifier(databaseName)+" WITH (FORCE)"); dropErr != nil {
			t.Errorf("drop isolated PostgreSQL database %s: %v", databaseName, dropErr)
		}
	})

	parsed.Path = "/" + databaseName
	parsed.RawPath = ""
	testDB, err := sql.Open("postgres", parsed.String())
	if err != nil {
		t.Fatalf("open isolated PostgreSQL database: %v", err)
	}
	t.Cleanup(func() { _ = testDB.Close() })
	if err := testDB.PingContext(ctx); err != nil {
		t.Fatalf("ping isolated PostgreSQL database: %v", err)
	}
	return testDB
}

func insertAuthzPolicyPostgresUser(t *testing.T, ctx context.Context, tx *sql.Tx, email string) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash, role, status, username)
		VALUES ($1, 'not-a-real-password-hash', 'user', 'active', $1)
		RETURNING id
	`, email).Scan(&id); err != nil {
		t.Fatalf("insert PostgreSQL user %s: %v", email, err)
	}
	return id
}

func insertAuthzPolicyPostgresRole(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	code string,
	capability authz.Capability,
) int64 {
	t.Helper()
	var roleID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO roles (code, name, description)
		VALUES ($1, $1, 'authz policy integration fixture')
		RETURNING id
	`, code).Scan(&roleID); err != nil {
		t.Fatalf("insert PostgreSQL role %s: %v", code, err)
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO role_permissions (role_id, permission_id)
		SELECT $1, id FROM permissions WHERE code = $2
	`, roleID, capability)
	if err != nil {
		t.Fatalf("attach PostgreSQL permission %s: %v", capability, err)
	}
	if affected, err := result.RowsAffected(); err != nil || affected != 1 {
		t.Fatalf("attach PostgreSQL permission %s affected %d rows: %v", capability, affected, err)
	}
	return roleID
}

func insertAuthzPolicyPostgresGrant(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	query string,
	args ...any,
) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRowContext(ctx, query, args...).Scan(&id); err != nil {
		t.Fatalf("insert PostgreSQL grant: %v", err)
	}
	return id
}

func setAuthzPolicyPostgresConfiguration(t *testing.T, ctx context.Context, tx *sql.Tx) {
	t.Helper()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value)
		VALUES
			('resource_access_control_enabled', 'true'),
			('self_service_hosting_enabled', 'true'),
			('group_sharing_enabled', 'true'),
			('account_sharing_enabled', 'true'),
			('role_based_resource_grants_enabled', 'true'),
			('role_authorization_mode', 'rbac')
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value
	`); err != nil {
		t.Fatalf("set PostgreSQL policy configuration: %v", err)
	}
}

func assertAuthzPolicyPostgresResourceState(
	t *testing.T,
	snapshot authz.ResourceAccessSnapshot,
	resource authz.ResourceRef,
	ownerID int64,
	directGrantID int64,
	roleGrantID int64,
	roleID int64,
) {
	t.Helper()
	if !snapshot.Valid() || snapshot.Resource() != resource || !snapshot.Exists() || snapshot.Deleted() || snapshot.AccessVersion() != 6 {
		t.Fatalf("unexpected PostgreSQL resource state: %+v", snapshot)
	}
	if got, ok := snapshot.OwnerUserID(); !ok || got != ownerID {
		t.Fatalf("PostgreSQL owner = (%d, %t), want (%d, true)", got, ok, ownerID)
	}
	if got, ok := snapshot.PublicAccessLevel(); !ok || got != authz.AccessLevelViewer {
		t.Fatalf("PostgreSQL public access = (%q, %t), want (%q, true)", got, ok, authz.AccessLevelViewer)
	}
	userGrants := snapshot.UserGrants()
	if len(userGrants) != 1 || userGrants[0].GrantID() != directGrantID || userGrants[0].AccessLevel() != authz.AccessLevelConsumer {
		t.Fatalf("unexpected PostgreSQL direct grants: %+v", userGrants)
	}
	roleGrants := snapshot.RoleGrants()
	if len(roleGrants) != 1 || roleGrants[0].GrantID() != roleGrantID || roleGrants[0].AccessLevel() != authz.AccessLevelMaintainer {
		t.Fatalf("unexpected PostgreSQL role grants: %+v", roleGrants)
	}
	if got, ok := roleGrants[0].RoleID(); !ok || got != roleID {
		t.Fatalf("PostgreSQL role grant role = (%d, %t), want (%d, true)", got, ok, roleID)
	}
}

func assertAuthzPolicyPostgresExpiryEqualsDatabaseNow(t *testing.T, ctx context.Context, tx *sql.Tx, grantID int64) {
	t.Helper()
	var equal bool
	if err := tx.QueryRowContext(ctx, `
		SELECT expires_at = CURRENT_TIMESTAMP
		FROM account_access_grants
		WHERE id = $1
	`, grantID).Scan(&equal); err != nil {
		t.Fatalf("read PostgreSQL grant expiry boundary: %v", err)
	}
	if !equal {
		t.Fatalf("grant %d is not exactly at the database CURRENT_TIMESTAMP boundary", grantID)
	}
}
