package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestResourceAccessControlCrossTenantFullStackPostgres(t *testing.T) {
	db := newAuthzPolicyPostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatalf("apply migrations to cross-tenant database: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin cross-tenant fixture transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	grantorID := insertAuthzPolicyPostgresUser(t, ctx, tx, "cross-tenant-grantor@example.test")
	tenantAID := insertAuthzPolicyPostgresUser(t, ctx, tx, "cross-tenant-a@example.test")
	tenantBID := insertAuthzPolicyPostgresUser(t, ctx, tx, "cross-tenant-b@example.test")
	roleID := insertAuthzPolicyPostgresRole(t, ctx, tx, "cross_tenant_reader", authz.CapabilityAccountCreate)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_roles (user_id, role_id, granted_by_user_id, expires_at)
		VALUES ($1, $2, $3, statement_timestamp() + INTERVAL '1 hour')
	`, tenantAID, roleID, grantorID); err != nil {
		t.Fatalf("assign cross-tenant reader role: %v", err)
	}

	var servicePrincipalID int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO service_principals (code, name, status)
		VALUES ('cross_tenant_reader', 'Cross-tenant reader', 'active')
		RETURNING id
	`).Scan(&servicePrincipalID); err != nil {
		t.Fatalf("insert cross-tenant service principal: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO service_principal_roles (service_principal_id, role_id, granted_by_user_id, expires_at)
		VALUES ($1, $2, $3, statement_timestamp() + INTERVAL '1 hour')
	`, servicePrincipalID, roleID, grantorID); err != nil {
		t.Fatalf("assign service-principal reader role: %v", err)
	}
	setAuthzPolicyPostgresConfiguration(t, ctx, tx)

	accounts := insertCrossTenantAccountFixtures(t, ctx, tx, tenantAID, tenantBID, grantorID, roleID)
	groups := insertCrossTenantGroupFixtures(t, ctx, tx, tenantAID, tenantBID, grantorID, roleID)

	driver := entsql.NewDriver(dialect.Postgres, entsql.Conn{ExecQuerier: tx})
	client := dbent.NewClient(dbent.Driver(driver))
	store := newAuthzPolicyStoreWithQueryer(tx)
	resolver := authz.NewActorResolver(store)
	policy := authz.NewPolicyService(store)
	reader := NewScopedResourceReader(client)
	readService := service.NewResourceReadService(policy, reader, reader)

	tenantA, err := resolver.ResolveUser(ctx, tenantAID, authz.AuthMethodJWT)
	if err != nil {
		t.Fatalf("resolve tenant A actor: %v", err)
	}
	servicePrincipal, err := resolver.ResolveServicePrincipal(ctx, "cross_tenant_reader", authz.AuthMethodServicePrincipal)
	if err != nil {
		t.Fatalf("resolve cross-tenant service principal: %v", err)
	}

	t.Run("user list search sort pagination and IDOR stay tenant scoped", func(t *testing.T) {
		assertCrossTenantAccountReadMatrix(
			t,
			ctx,
			readService,
			tenantA,
			accounts,
			[]int64{accounts.owner, accounts.public, accounts.direct, accounts.role},
			"user",
		)
		assertCrossTenantGroupReadMatrix(
			t,
			ctx,
			readService,
			tenantA,
			groups,
			[]int64{groups.owner, groups.public, groups.direct, groups.role},
			"user",
		)
	})

	t.Run("service principal only receives its role-grant branch", func(t *testing.T) {
		assertCrossTenantAccountReadMatrix(
			t,
			ctx,
			readService,
			servicePrincipal,
			accounts,
			[]int64{accounts.role},
			"service principal",
		)
		assertCrossTenantGroupReadMatrix(
			t,
			ctx,
			readService,
			servicePrincipal,
			groups,
			[]int64{groups.role},
			"service principal",
		)
	})

	t.Run("admin API key legacy bypass rechecks its fixed active principal", func(t *testing.T) {
		var adminAPIKeyPrincipalID int64
		var adminAPIKeyStatus string
		var adminAPIKeyRoleCount int
		if err := tx.QueryRowContext(ctx, `
			SELECT principal.id, principal.status, COUNT(assignment.role_id)
			FROM service_principals principal
			LEFT JOIN service_principal_roles assignment
			  ON assignment.service_principal_id = principal.id
			WHERE principal.code = $1
			GROUP BY principal.id, principal.status
		`, authz.AdminAPIKeyServicePrincipalCode).Scan(
			&adminAPIKeyPrincipalID,
			&adminAPIKeyStatus,
			&adminAPIKeyRoleCount,
		); err != nil {
			t.Fatalf("load migrated admin API-key principal: %v", err)
		}
		if adminAPIKeyPrincipalID <= 0 || adminAPIKeyStatus != "active" || adminAPIKeyRoleCount != 0 {
			t.Fatalf(
				"admin API-key principal = id=%d status=%q roles=%d, want active roleless principal",
				adminAPIKeyPrincipalID,
				adminAPIKeyStatus,
				adminAPIKeyRoleCount,
			)
		}

		defer func() {
			if _, restoreErr := tx.ExecContext(ctx, `
				UPDATE service_principals
				SET code = $2, status = 'active'
				WHERE id = $1
			`, adminAPIKeyPrincipalID, authz.AdminAPIKeyServicePrincipalCode); restoreErr != nil {
				t.Errorf("restore admin API-key principal: %v", restoreErr)
			}
			if _, restoreErr := tx.ExecContext(ctx, `
				UPDATE settings SET value = 'rbac' WHERE key = 'role_authorization_mode'
			`); restoreErr != nil {
				t.Errorf("restore role authority after admin API-key checks: %v", restoreErr)
			}
		}()

		for _, mode := range []string{"legacy", "shadow"} {
			if _, err := tx.ExecContext(ctx, `
				UPDATE settings SET value = $1 WHERE key = 'role_authorization_mode'
			`, mode); err != nil {
				t.Fatalf("switch admin API-key fixture to %s role authority: %v", mode, err)
			}
			adminAPIKey, resolveErr := resolver.ResolveServicePrincipal(
				ctx,
				authz.AdminAPIKeyServicePrincipalCode,
				authz.AuthMethodAdminAPIKey,
			)
			if resolveErr != nil {
				t.Fatalf("resolve admin API-key principal in %s mode: %v", mode, resolveErr)
			}
			accountScope, scopeErr := policy.AccessibleScope(
				ctx,
				adminAPIKey,
				authz.ResourceTypeAccount,
				authz.ActionAccountView,
			)
			if scopeErr != nil {
				t.Fatalf("create admin API-key account scope in %s mode: %v", mode, scopeErr)
			}
			groupScope, scopeErr := policy.AccessibleScope(
				ctx,
				adminAPIKey,
				authz.ResourceTypeGroup,
				authz.ActionGroupView,
			)
			if scopeErr != nil {
				t.Fatalf("create admin API-key group scope in %s mode: %v", mode, scopeErr)
			}

			assertCrossTenantAdminAPIKeyRead(t, ctx, readService, adminAPIKey, accounts, groups, mode)

			if _, err := tx.ExecContext(ctx, `
				UPDATE service_principals SET status = 'disabled' WHERE id = $1
			`, adminAPIKeyPrincipalID); err != nil {
				t.Fatalf("disable admin API-key principal in %s mode: %v", mode, err)
			}
			assertOldScopeIDs(t, ctx, reader, accountScope, groupScope, nil, nil)

			if _, err := tx.ExecContext(ctx, `
				UPDATE service_principals SET status = 'active' WHERE id = $1
			`, adminAPIKeyPrincipalID); err != nil {
				t.Fatalf("reactivate admin API-key principal in %s mode: %v", mode, err)
			}
			if _, err := tx.ExecContext(ctx, `
				UPDATE service_principals SET code = $2 WHERE id = $1
			`, adminAPIKeyPrincipalID, "admin_api_key_temporarily_renamed"); err != nil {
				t.Fatalf("rename admin API-key principal in %s mode: %v", mode, err)
			}
			assertOldScopeIDs(t, ctx, reader, accountScope, groupScope, nil, nil)
			if _, err := tx.ExecContext(ctx, `
				UPDATE service_principals SET code = $2 WHERE id = $1
			`, adminAPIKeyPrincipalID, authz.AdminAPIKeyServicePrincipalCode); err != nil {
				t.Fatalf("restore admin API-key principal code in %s mode: %v", mode, err)
			}
		}
	})

	t.Run("simple mode closes user scope but preserves legacy admin governance", func(t *testing.T) {
		assertCrossTenantRawPolicyFlagsTrue(t, ctx, tx)

		simpleStore := &authzPolicyStore{queryer: tx, simpleMode: true}
		simpleResolver := authz.NewActorResolver(simpleStore)
		simplePolicy := authz.NewPolicyService(simpleStore)
		simpleReadService := service.NewResourceReadService(simplePolicy, reader, reader)

		simpleTenantA, resolveErr := simpleResolver.ResolveUser(ctx, tenantAID, authz.AuthMethodJWT)
		if resolveErr != nil {
			t.Fatalf("resolve SIMPLE Mode tenant actor: %v", resolveErr)
		}
		if _, scopeErr := simplePolicy.AccessibleScope(
			ctx,
			simpleTenantA,
			authz.ResourceTypeAccount,
			authz.ActionAccountView,
		); !errors.Is(scopeErr, authz.ErrFeatureDisabled) {
			t.Fatalf("SIMPLE Mode user scope error = %v, want feature disabled", scopeErr)
		}
		if items, page, readErr := simpleReadService.ListAccounts(ctx, simpleTenantA, service.AccountReadQuery{}); !errors.Is(readErr, authz.ErrFeatureDisabled) || items != nil || page != nil {
			t.Fatalf("SIMPLE Mode user read = items=%+v page=%+v err=%v", items, page, readErr)
		}
		if _, scopeErr := simplePolicy.AccessibleScope(
			ctx,
			simpleTenantA,
			authz.ResourceTypeGroup,
			authz.ActionGroupView,
		); !errors.Is(scopeErr, authz.ErrFeatureDisabled) {
			t.Fatalf("SIMPLE Mode user group scope error = %v, want feature disabled", scopeErr)
		}
		if items, page, readErr := simpleReadService.ListGroups(ctx, simpleTenantA, service.GroupReadQuery{}); !errors.Is(readErr, authz.ErrFeatureDisabled) || items != nil || page != nil {
			t.Fatalf("SIMPLE Mode user group read = items=%+v page=%+v err=%v", items, page, readErr)
		}

		for _, testCase := range []struct {
			name         string
			resourceType authz.ResourceType
			action       authz.Action
			resourceID   int64
		}{
			{name: "account", resourceType: authz.ResourceTypeAccount, action: authz.ActionAccountView, resourceID: accounts.owner},
			{name: "group", resourceType: authz.ResourceTypeGroup, action: authz.ActionGroupView, resourceID: groups.owner},
		} {
			ref, refErr := authz.NewResourceRef(testCase.resourceType, testCase.resourceID)
			if refErr != nil {
				t.Fatalf("create SIMPLE Mode %s resource reference: %v", testCase.name, refErr)
			}
			decision, authorizeErr := simplePolicy.Authorize(ctx, simpleTenantA, testCase.action, ref)
			if authorizeErr != nil || decision.Allowed() || decision.DenyReason() != authz.DenyReasonFeatureDisabled {
				t.Fatalf(
					"SIMPLE Mode user %s authorization = allowed=%t reason=%q err=%v, want feature-disabled denial",
					testCase.name,
					decision.Allowed(),
					decision.DenyReason(),
					authorizeErr,
				)
			}
		}

		var adminID int64
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO users (email, password_hash, role, status, username)
			VALUES ('cross-tenant-simple-admin@example.test', 'not-a-real-hash', 'admin', 'active', 'cross-tenant-simple-admin')
			RETURNING id
		`).Scan(&adminID); err != nil {
			t.Fatalf("insert SIMPLE Mode legacy admin: %v", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE settings SET value = 'legacy' WHERE key = 'role_authorization_mode'`); err != nil {
			t.Fatalf("switch SIMPLE Mode fixture to legacy role authority: %v", err)
		}
		legacyAdmin, resolveErr := simpleResolver.ResolveLegacyAdminUser(ctx, adminID)
		if resolveErr != nil {
			t.Fatalf("resolve SIMPLE Mode legacy admin: %v", resolveErr)
		}
		visible, readErr := simpleReadService.GetAccount(ctx, legacyAdmin, accounts.private)
		if readErr != nil || visible == nil || visible.ID != accounts.private {
			t.Fatalf("SIMPLE Mode legacy admin private read = item=%+v err=%v", visible, readErr)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE settings SET value = 'rbac' WHERE key = 'role_authorization_mode'`); err != nil {
			t.Fatalf("restore cross-tenant fixture to rbac role authority: %v", err)
		}
	})

	t.Run("old scopes recheck flags roles and subject state", func(t *testing.T) {
		accountScope, scopeErr := policy.AccessibleScope(ctx, tenantA, authz.ResourceTypeAccount, authz.ActionAccountView)
		if scopeErr != nil {
			t.Fatalf("create account scope: %v", scopeErr)
		}
		groupScope, scopeErr := policy.AccessibleScope(ctx, tenantA, authz.ResourceTypeGroup, authz.ActionGroupView)
		if scopeErr != nil {
			t.Fatalf("create group scope: %v", scopeErr)
		}

		setCrossTenantPolicyFlags(t, ctx, tx, false, false, true)
		assertOldScopeIDs(t, ctx, reader, accountScope, groupScope, []int64{accounts.owner}, []int64{groups.owner})

		setCrossTenantPolicyFlags(t, ctx, tx, true, true, false)
		assertOldScopeIDs(
			t,
			ctx,
			reader,
			accountScope,
			groupScope,
			[]int64{accounts.owner, accounts.public, accounts.direct},
			[]int64{groups.owner, groups.public, groups.direct},
		)

		setCrossTenantPolicyFlags(t, ctx, tx, true, true, true)
		if _, err := tx.ExecContext(ctx, `UPDATE roles SET authz_version = authz_version + 1 WHERE id = $1`, roleID); err != nil {
			t.Fatalf("advance reader role version: %v", err)
		}
		assertOldScopeIDs(t, ctx, reader, accountScope, groupScope, nil, nil)
		if _, _, err := readService.ListAccounts(ctx, tenantA, service.AccountReadQuery{}); !errors.Is(err, authz.ErrSessionInvalid) {
			t.Fatalf("stale actor account read error = %v, want session invalid", err)
		}

		freshTenantA, err := resolver.ResolveUser(ctx, tenantAID, authz.AuthMethodJWT)
		if err != nil {
			t.Fatalf("resolve tenant A after role version change: %v", err)
		}
		freshAccountScope, err := policy.AccessibleScope(ctx, freshTenantA, authz.ResourceTypeAccount, authz.ActionAccountView)
		if err != nil {
			t.Fatalf("create fresh account scope: %v", err)
		}
		freshGroupScope, err := policy.AccessibleScope(ctx, freshTenantA, authz.ResourceTypeGroup, authz.ActionGroupView)
		if err != nil {
			t.Fatalf("create fresh group scope: %v", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET status = 'disabled' WHERE id = $1`, tenantAID); err != nil {
			t.Fatalf("disable tenant A: %v", err)
		}
		assertOldScopeIDs(t, ctx, reader, freshAccountScope, freshGroupScope, nil, nil)
		if _, _, err := readService.ListAccounts(ctx, freshTenantA, service.AccountReadQuery{}); !errors.Is(err, authz.ErrActorInactive) {
			t.Fatalf("disabled actor account read error = %v, want actor inactive", err)
		}
	})
}

type crossTenantResourceFixtures struct {
	owner    int64
	private  int64
	platform int64
	public   int64
	direct   int64
	role     int64
}

func insertCrossTenantAccountFixtures(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	tenantAID int64,
	tenantBID int64,
	grantorID int64,
	roleID int64,
) crossTenantResourceFixtures {
	t.Helper()
	fixtures := crossTenantResourceFixtures{
		owner:    insertCrossTenantAccount(t, ctx, tx, "cross-account-a-owner", &tenantAID, nil),
		private:  insertCrossTenantAccount(t, ctx, tx, "cross-account-b-private-secret", &tenantBID, nil),
		platform: insertCrossTenantAccount(t, ctx, tx, "cross-account-platform-private", nil, nil),
		public:   insertCrossTenantAccount(t, ctx, tx, "cross-account-b-public", &tenantBID, accessLevelPointer(authz.AccessLevelViewer)),
		direct:   insertCrossTenantAccount(t, ctx, tx, "cross-account-b-direct", &tenantBID, nil),
		role:     insertCrossTenantAccount(t, ctx, tx, "cross-account-b-role", &tenantBID, nil),
	}
	insertCrossTenantGrant(t, ctx, tx, "account_access_grants", "account_id", fixtures.direct, tenantAID, 0, grantorID)
	insertCrossTenantGrant(t, ctx, tx, "account_access_grants", "account_id", fixtures.role, 0, roleID, grantorID)
	return fixtures
}

func insertCrossTenantGroupFixtures(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	tenantAID int64,
	tenantBID int64,
	grantorID int64,
	roleID int64,
) crossTenantResourceFixtures {
	t.Helper()
	fixtures := crossTenantResourceFixtures{
		owner:    insertCrossTenantGroup(t, ctx, tx, "cross-group-a-owner", &tenantAID, nil),
		private:  insertCrossTenantGroup(t, ctx, tx, "cross-group-b-private-secret", &tenantBID, nil),
		platform: insertCrossTenantGroup(t, ctx, tx, "cross-group-platform-private", nil, nil),
		public:   insertCrossTenantGroup(t, ctx, tx, "cross-group-b-public", &tenantBID, accessLevelPointer(authz.AccessLevelViewer)),
		direct:   insertCrossTenantGroup(t, ctx, tx, "cross-group-b-direct", &tenantBID, nil),
		role:     insertCrossTenantGroup(t, ctx, tx, "cross-group-b-role", &tenantBID, nil),
	}
	insertCrossTenantGrant(t, ctx, tx, "group_access_grants", "group_id", fixtures.direct, tenantAID, 0, grantorID)
	insertCrossTenantGrant(t, ctx, tx, "group_access_grants", "group_id", fixtures.role, 0, roleID, grantorID)
	return fixtures
}

func insertCrossTenantAccount(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	name string,
	ownerID *int64,
	publicLevel *authz.AccessLevel,
) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO accounts (
			name, platform, type, credentials, extra, status,
			owner_user_id, created_by_user_id, public_access_level
		)
		VALUES ($1, 'openai', 'apikey', '{}'::jsonb, '{}'::jsonb, 'active', $2, $2, $3)
		RETURNING id
	`, name, ownerID, publicLevel).Scan(&id); err != nil {
		t.Fatalf("insert cross-tenant account %s: %v", name, err)
	}
	return id
}

func insertCrossTenantGroup(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	name string,
	ownerID *int64,
	publicLevel *authz.AccessLevel,
) int64 {
	t.Helper()
	var id int64
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO groups (
			name, description, platform, status, rate_multiplier,
			owner_user_id, created_by_user_id, public_access_level
		)
		VALUES ($1, $2, 'openai', 'active', 1, $3, $3, $4)
		RETURNING id
	`, name, name, ownerID, publicLevel).Scan(&id); err != nil {
		t.Fatalf("insert cross-tenant group %s: %v", name, err)
	}
	return id
}

func insertCrossTenantGrant(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	table string,
	resourceColumn string,
	resourceID int64,
	userID int64,
	roleID int64,
	grantorID int64,
) {
	t.Helper()
	granteeColumn := "grantee_user_id"
	granteeID := userID
	if roleID > 0 {
		granteeColumn = "grantee_role_id"
		granteeID = roleID
	}
	query := fmt.Sprintf(`
		INSERT INTO %s (%s, %s, access_level, granted_by_user_id, expires_at)
		VALUES ($1, $2, 'viewer', $3, statement_timestamp() + INTERVAL '1 hour')
	`, table, resourceColumn, granteeColumn)
	if _, err := tx.ExecContext(ctx, query, resourceID, granteeID, grantorID); err != nil {
		t.Fatalf("insert cross-tenant grant into %s: %v", table, err)
	}
}

func assertCrossTenantAccountReadMatrix(
	t *testing.T,
	ctx context.Context,
	readService *service.ResourceReadService,
	actor authz.Actor,
	fixtures crossTenantResourceFixtures,
	want []int64,
	actorLabel string,
) {
	t.Helper()
	for _, sortBy := range []string{"id", "name", "platform", "type", "status", "created_at", "updated_at"} {
		for _, order := range []string{"asc", "desc"} {
			got := make([]int64, 0, len(want))
			for page := 1; page <= len(want)+1; page++ {
				items, result, err := readService.ListAccounts(ctx, actor, service.AccountReadQuery{
					Pagination: pagination.PaginationParams{Page: page, PageSize: 1, SortBy: sortBy, SortOrder: order},
				})
				if err != nil {
					t.Fatalf("list %s accounts sort=%s order=%s page=%d: %v", actorLabel, sortBy, order, page, err)
				}
				if result == nil || result.Total != int64(len(want)) {
					t.Fatalf("%s account pagination sort=%s order=%s page=%d = %+v, want total %d", actorLabel, sortBy, order, page, result, len(want))
				}
				for _, id := range accountListItemIDs(items) {
					if id == fixtures.private || id == fixtures.platform {
						t.Fatalf("%s account isolation leaked id %d for sort=%s order=%s page=%d", actorLabel, id, sortBy, order, page)
					}
					got = append(got, id)
				}
			}
			assertCrossTenantIDs(t, got, want)
		}
	}

	items, result, err := readService.ListAccounts(ctx, actor, service.AccountReadQuery{
		Search:     "cross-account-",
		Pagination: pagination.PaginationParams{Page: 1, PageSize: 20, SortBy: "id", SortOrder: "asc"},
	})
	if err != nil {
		t.Fatalf("search visible %s accounts: %v", actorLabel, err)
	}
	assertCrossTenantIDs(t, accountListItemIDs(items), want)
	if result == nil || result.Total != int64(len(want)) {
		t.Fatalf("visible %s account search page = %+v, want total %d", actorLabel, result, len(want))
	}
	visibleIDs := make(map[int64]struct{}, len(want))
	for _, id := range want {
		visibleIDs[id] = struct{}{}
		item, readErr := readService.GetAccount(ctx, actor, id)
		if readErr != nil || item == nil || item.ID != id {
			t.Fatalf("visible %s account %d read = item=%+v err=%v", actorLabel, id, item, readErr)
		}
	}

	items, result, err = readService.ListAccounts(ctx, actor, service.AccountReadQuery{
		Search:     "cross-account-b-private-secret",
		Pagination: pagination.PaginationParams{Page: 1, PageSize: 20, SortBy: "id", SortOrder: "asc"},
	})
	if err != nil || len(items) != 0 || result == nil || result.Total != 0 {
		t.Fatalf("private %s account search = items=%+v page=%+v err=%v, want empty", actorLabel, items, result, err)
	}
	_, missingErr := readService.GetAccount(ctx, actor, fixtures.platform+1_000_000)
	if !errors.Is(missingErr, service.ErrAccountNotFound) {
		t.Fatalf("missing %s account error = %v, want account not found", actorLabel, missingErr)
	}
	for _, id := range crossTenantFixtureIDs(fixtures) {
		if _, visible := visibleIDs[id]; visible {
			continue
		}
		_, deniedErr := readService.GetAccount(ctx, actor, id)
		if !errors.Is(deniedErr, service.ErrAccountNotFound) || deniedErr.Error() != missingErr.Error() {
			t.Fatalf("%s account IDOR errors differ for id %d: denied=%v missing=%v", actorLabel, id, deniedErr, missingErr)
		}
	}
}

func assertCrossTenantGroupReadMatrix(
	t *testing.T,
	ctx context.Context,
	readService *service.ResourceReadService,
	actor authz.Actor,
	fixtures crossTenantResourceFixtures,
	want []int64,
	actorLabel string,
) {
	t.Helper()
	for _, sortBy := range []string{"id", "name", "platform", "status", "created_at", "updated_at"} {
		for _, order := range []string{"asc", "desc"} {
			got := make([]int64, 0, len(want))
			for page := 1; page <= len(want)+1; page++ {
				items, result, err := readService.ListGroups(ctx, actor, service.GroupReadQuery{
					Pagination: pagination.PaginationParams{Page: page, PageSize: 1, SortBy: sortBy, SortOrder: order},
				})
				if err != nil {
					t.Fatalf("list %s groups sort=%s order=%s page=%d: %v", actorLabel, sortBy, order, page, err)
				}
				if result == nil || result.Total != int64(len(want)) {
					t.Fatalf("%s group pagination sort=%s order=%s page=%d = %+v, want total %d", actorLabel, sortBy, order, page, result, len(want))
				}
				for _, id := range groupListItemIDs(items) {
					if id == fixtures.private || id == fixtures.platform {
						t.Fatalf("%s group isolation leaked id %d for sort=%s order=%s page=%d", actorLabel, id, sortBy, order, page)
					}
					got = append(got, id)
				}
			}
			assertCrossTenantIDs(t, got, want)
		}
	}

	items, result, err := readService.ListGroups(ctx, actor, service.GroupReadQuery{
		Search:     "cross-group-",
		Pagination: pagination.PaginationParams{Page: 1, PageSize: 20, SortBy: "id", SortOrder: "asc"},
	})
	if err != nil {
		t.Fatalf("search visible %s groups: %v", actorLabel, err)
	}
	assertCrossTenantIDs(t, groupListItemIDs(items), want)
	if result == nil || result.Total != int64(len(want)) {
		t.Fatalf("visible %s group search page = %+v, want total %d", actorLabel, result, len(want))
	}
	visibleIDs := make(map[int64]struct{}, len(want))
	for _, id := range want {
		visibleIDs[id] = struct{}{}
		item, readErr := readService.GetGroup(ctx, actor, id)
		if readErr != nil || item == nil || item.ID != id {
			t.Fatalf("visible %s group %d read = item=%+v err=%v", actorLabel, id, item, readErr)
		}
	}

	items, result, err = readService.ListGroups(ctx, actor, service.GroupReadQuery{
		Search:     "cross-group-b-private-secret",
		Pagination: pagination.PaginationParams{Page: 1, PageSize: 20, SortBy: "id", SortOrder: "asc"},
	})
	if err != nil || len(items) != 0 || result == nil || result.Total != 0 {
		t.Fatalf("private %s group search = items=%+v page=%+v err=%v, want empty", actorLabel, items, result, err)
	}
	_, missingErr := readService.GetGroup(ctx, actor, fixtures.platform+1_000_000)
	if !errors.Is(missingErr, service.ErrGroupNotFound) {
		t.Fatalf("missing %s group error = %v, want group not found", actorLabel, missingErr)
	}
	for _, id := range crossTenantFixtureIDs(fixtures) {
		if _, visible := visibleIDs[id]; visible {
			continue
		}
		_, deniedErr := readService.GetGroup(ctx, actor, id)
		if !errors.Is(deniedErr, service.ErrGroupNotFound) || deniedErr.Error() != missingErr.Error() {
			t.Fatalf("%s group IDOR errors differ for id %d: denied=%v missing=%v", actorLabel, id, deniedErr, missingErr)
		}
	}
}

func assertOldScopeIDs(
	t *testing.T,
	ctx context.Context,
	reader *scopedResourceReader,
	accountScope authz.AccessibleScope,
	groupScope authz.AccessibleScope,
	wantAccounts []int64,
	wantGroups []int64,
) {
	t.Helper()
	accountItems, accountPage, err := reader.ListAccessibleAccounts(ctx, accountScope, service.AccountReadQuery{
		Pagination: pagination.PaginationParams{Page: 1, PageSize: 20, SortBy: "id", SortOrder: "asc"},
	})
	if err != nil {
		t.Fatalf("list accounts with old scope: %v", err)
	}
	assertCrossTenantIDs(t, accountListItemIDs(accountItems), wantAccounts)
	if accountPage == nil || accountPage.Total != int64(len(wantAccounts)) {
		t.Fatalf("old account scope page = %+v, want total %d", accountPage, len(wantAccounts))
	}

	groupItems, groupPage, err := reader.ListAccessibleGroups(ctx, groupScope, service.GroupReadQuery{
		Pagination: pagination.PaginationParams{Page: 1, PageSize: 20, SortBy: "id", SortOrder: "asc"},
	})
	if err != nil {
		t.Fatalf("list groups with old scope: %v", err)
	}
	assertCrossTenantIDs(t, groupListItemIDs(groupItems), wantGroups)
	if groupPage == nil || groupPage.Total != int64(len(wantGroups)) {
		t.Fatalf("old group scope page = %+v, want total %d", groupPage, len(wantGroups))
	}
}

func assertCrossTenantAdminAPIKeyRead(
	t *testing.T,
	ctx context.Context,
	readService *service.ResourceReadService,
	actor authz.Actor,
	accounts crossTenantResourceFixtures,
	groups crossTenantResourceFixtures,
	mode string,
) {
	t.Helper()
	accountItems, accountPage, err := readService.ListAccounts(ctx, actor, service.AccountReadQuery{
		Search:     "cross-account-",
		Pagination: pagination.PaginationParams{Page: 1, PageSize: 20, SortBy: "id", SortOrder: "asc"},
	})
	if err != nil {
		t.Fatalf("list admin API-key accounts in %s mode: %v", mode, err)
	}
	assertCrossTenantIDs(t, accountListItemIDs(accountItems), crossTenantFixtureIDs(accounts))
	if accountPage == nil || accountPage.Total != 6 {
		t.Fatalf("admin API-key account page in %s mode = %+v, want total 6", mode, accountPage)
	}
	for _, id := range []int64{accounts.private, accounts.platform} {
		item, readErr := readService.GetAccount(ctx, actor, id)
		if readErr != nil || item == nil || item.ID != id {
			t.Fatalf("admin API-key account %d read in %s mode = item=%+v err=%v", id, mode, item, readErr)
		}
	}

	groupItems, groupPage, err := readService.ListGroups(ctx, actor, service.GroupReadQuery{
		Search:     "cross-group-",
		Pagination: pagination.PaginationParams{Page: 1, PageSize: 20, SortBy: "id", SortOrder: "asc"},
	})
	if err != nil {
		t.Fatalf("list admin API-key groups in %s mode: %v", mode, err)
	}
	assertCrossTenantIDs(t, groupListItemIDs(groupItems), crossTenantFixtureIDs(groups))
	if groupPage == nil || groupPage.Total != 6 {
		t.Fatalf("admin API-key group page in %s mode = %+v, want total 6", mode, groupPage)
	}
	for _, id := range []int64{groups.private, groups.platform} {
		item, readErr := readService.GetGroup(ctx, actor, id)
		if readErr != nil || item == nil || item.ID != id {
			t.Fatalf("admin API-key group %d read in %s mode = item=%+v err=%v", id, mode, item, readErr)
		}
	}
}

func crossTenantFixtureIDs(fixtures crossTenantResourceFixtures) []int64 {
	return []int64{
		fixtures.owner,
		fixtures.private,
		fixtures.platform,
		fixtures.public,
		fixtures.direct,
		fixtures.role,
	}
}

func assertCrossTenantRawPolicyFlagsTrue(t *testing.T, ctx context.Context, tx *sql.Tx) {
	t.Helper()
	for _, key := range []string{
		"resource_access_control_enabled",
		"self_service_hosting_enabled",
		"group_sharing_enabled",
		"account_sharing_enabled",
		"role_based_resource_grants_enabled",
	} {
		var value string
		if err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&value); err != nil {
			t.Fatalf("load raw SIMPLE Mode flag %s: %v", key, err)
		}
		if value != "true" {
			t.Fatalf("raw SIMPLE Mode flag %s = %q, want true", key, value)
		}
	}
}

func setCrossTenantPolicyFlags(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	accountSharing bool,
	groupSharing bool,
	roleGrants bool,
) {
	t.Helper()
	for key, enabled := range map[string]bool{
		"account_sharing_enabled":            accountSharing,
		"group_sharing_enabled":              groupSharing,
		"role_based_resource_grants_enabled": roleGrants,
	} {
		if _, err := tx.ExecContext(ctx, `UPDATE settings SET value = $2 WHERE key = $1`, key, fmt.Sprintf("%t", enabled)); err != nil {
			t.Fatalf("set %s=%t: %v", key, enabled, err)
		}
	}
}

func accountListItemIDs(items []service.AccountListItem) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func groupListItemIDs(items []service.GroupListItem) []int64 {
	ids := make([]int64, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	return ids
}

func assertCrossTenantIDs(t *testing.T, got []int64, want []int64) {
	t.Helper()
	sort.Slice(got, func(i, j int) bool { return got[i] < got[j] })
	sort.Slice(want, func(i, j int) bool { return want[i] < want[j] })
	if len(got) != len(want) {
		t.Fatalf("resource IDs = %v, want %v", got, want)
	}
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("resource IDs = %v, want %v", got, want)
		}
	}
}

func accessLevelPointer(level authz.AccessLevel) *authz.AccessLevel {
	return &level
}
