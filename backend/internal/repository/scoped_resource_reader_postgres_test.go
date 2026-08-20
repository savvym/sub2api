package repository

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestScopedResourceReaderPostgres(t *testing.T) {
	db := newAuthzPolicyPostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatalf("apply migrations to temporary scoped-reader database: %v", err)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin scoped-reader fixture transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL TIME ZONE 'UTC'`); err != nil {
		t.Fatalf("set scoped-reader fixture timezone: %v", err)
	}

	grantorID := insertAuthzPolicyPostgresUser(t, ctx, tx, "scoped-reader-grantor@example.test")
	subjectID := insertAuthzPolicyPostgresUser(t, ctx, tx, "scoped-reader-subject@example.test")
	otherUserID := insertAuthzPolicyPostgresUser(t, ctx, tx, "scoped-reader-other@example.test")
	roleID := insertAuthzPolicyPostgresRole(t, ctx, tx, "scoped_reader_role", authz.CapabilityAccountCreate)
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO user_roles (user_id, role_id, granted_by_user_id, expires_at)
		VALUES ($1, $2, $3, CURRENT_TIMESTAMP + INTERVAL '1 hour')
	`, subjectID, roleID, grantorID); err != nil {
		t.Fatalf("assign scoped-reader role: %v", err)
	}
	setAuthzPolicyPostgresConfiguration(t, ctx, tx)

	accounts := insertScopedResourceReaderAccountFixtures(
		t, ctx, tx, grantorID, subjectID, otherUserID, roleID,
	)
	groups := insertScopedResourceReaderGroupFixtures(
		t, ctx, tx, grantorID, subjectID, otherUserID, roleID,
	)

	subject := mustAuthzSubjectRef(t, authz.SubjectKindUser, subjectID)
	snapshot, err := newAuthzPolicyStoreWithQueryer(tx).LoadSubjectSnapshot(ctx, subject)
	if err != nil {
		t.Fatalf("load scoped-reader subject snapshot: %v", err)
	}
	if !snapshot.Valid() || !snapshot.Exists() || !snapshot.Active() {
		t.Fatalf("unexpected scoped-reader subject snapshot: %+v", snapshot)
	}

	recorder := &authzPolicyPostgresQueryRecorder{delegate: tx}
	driver := entsql.NewDriver(dialect.Postgres, entsql.Conn{ExecQuerier: recorder})
	client := dbent.NewClient(dbent.Driver(driver))
	reader := NewScopedResourceReader(client)
	accountClaims := newAuthzPolicyPostgresViewClaims(
		snapshot,
		authz.ResourceTypeAccount,
		authz.ActionAccountView,
	)
	groupClaims := newAuthzPolicyPostgresViewClaims(
		snapshot,
		authz.ResourceTypeGroup,
		authz.ActionGroupView,
	)

	t.Run("accounts", func(t *testing.T) {
		assertScopedResourceReaderAccountBehavior(t, ctx, reader, recorder, accountClaims, accounts)
	})
	t.Run("groups", func(t *testing.T) {
		assertScopedResourceReaderGroupBehavior(t, ctx, reader, recorder, groupClaims, groups)
	})

	if _, err := tx.ExecContext(ctx, `
		UPDATE users
		SET authz_version = authz_version + 1
		WHERE id = $1
	`, subjectID); err != nil {
		t.Fatalf("make scoped-reader subject snapshot stale: %v", err)
	}

	accountItems, accountPage, err := reader.listAccessibleAccounts(ctx, accountClaims, service.AccountReadQuery{
		Pagination: pagination.PaginationParams{Page: 1, PageSize: 10, SortBy: "id", SortOrder: "asc"},
	})
	if err != nil {
		t.Fatalf("list accounts with stale subject claims: %v", err)
	}
	if len(accountItems) != 0 || accountPage == nil || accountPage.Total != 0 {
		t.Fatalf("stale account scope remained usable: items=%+v page=%+v", accountItems, accountPage)
	}

	groupItems, groupPage, err := reader.listAccessibleGroups(ctx, groupClaims, service.GroupReadQuery{
		Pagination: pagination.PaginationParams{Page: 1, PageSize: 10, SortBy: "id", SortOrder: "asc"},
	})
	if err != nil {
		t.Fatalf("list groups with stale subject claims: %v", err)
	}
	if len(groupItems) != 0 || groupPage == nil || groupPage.Total != 0 {
		t.Fatalf("stale group scope remained usable: items=%+v page=%+v", groupItems, groupPage)
	}
}

type scopedResourceReaderFixture struct {
	owner         int64
	public        int64
	direct        int64
	role          int64
	private       int64
	expiredDirect int64
	expiredRole   int64
}

func insertScopedResourceReaderAccountFixtures(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	grantorID int64,
	subjectID int64,
	otherUserID int64,
	roleID int64,
) scopedResourceReaderFixture {
	t.Helper()
	viewer := string(authz.AccessLevelViewer)
	fixture := scopedResourceReaderFixture{
		owner:         insertAuthzPolicyPostgresAccount(t, ctx, tx, "scoped-reader-account-a", subjectID, "", false),
		public:        insertAuthzPolicyPostgresAccount(t, ctx, tx, "scoped-reader-account-a", otherUserID, viewer, false),
		direct:        insertAuthzPolicyPostgresAccount(t, ctx, tx, "scoped-reader-account-b", otherUserID, "", false),
		role:          insertAuthzPolicyPostgresAccount(t, ctx, tx, "scoped-reader-account-c", otherUserID, "", false),
		private:       insertAuthzPolicyPostgresAccount(t, ctx, tx, "scoped-reader-account-a", otherUserID, "", false),
		expiredDirect: insertAuthzPolicyPostgresAccount(t, ctx, tx, "scoped-reader-account-a", otherUserID, "", false),
		expiredRole:   insertAuthzPolicyPostgresAccount(t, ctx, tx, "scoped-reader-account-a", otherUserID, "", false),
	}
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO account_access_grants (
			account_id, grantee_user_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP + INTERVAL '1 hour')
		RETURNING id
	`, fixture.direct, subjectID, grantorID)
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO account_access_grants (
			account_id, grantee_role_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP + INTERVAL '1 hour')
		RETURNING id
	`, fixture.role, roleID, grantorID)
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO account_access_grants (
			account_id, grantee_user_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP)
		RETURNING id
	`, fixture.expiredDirect, subjectID, grantorID)
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO account_access_grants (
			account_id, grantee_role_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP)
		RETURNING id
	`, fixture.expiredRole, roleID, grantorID)
	return fixture
}

func insertScopedResourceReaderGroupFixtures(
	t *testing.T,
	ctx context.Context,
	tx *sql.Tx,
	grantorID int64,
	subjectID int64,
	otherUserID int64,
	roleID int64,
) scopedResourceReaderFixture {
	t.Helper()
	viewer := string(authz.AccessLevelViewer)
	fixture := scopedResourceReaderFixture{
		owner:         insertAuthzPolicyPostgresGroup(t, ctx, tx, "scoped-reader-group-a-owner", subjectID, "", false),
		public:        insertAuthzPolicyPostgresGroup(t, ctx, tx, "scoped-reader-group-a-public", otherUserID, viewer, false),
		direct:        insertAuthzPolicyPostgresGroup(t, ctx, tx, "scoped-reader-group-b", otherUserID, "", false),
		role:          insertAuthzPolicyPostgresGroup(t, ctx, tx, "scoped-reader-group-c", otherUserID, "", false),
		private:       insertAuthzPolicyPostgresGroup(t, ctx, tx, "scoped-reader-group-a-private", otherUserID, "", false),
		expiredDirect: insertAuthzPolicyPostgresGroup(t, ctx, tx, "scoped-reader-group-a-expired-direct", otherUserID, "", false),
		expiredRole:   insertAuthzPolicyPostgresGroup(t, ctx, tx, "scoped-reader-group-a-expired-role", otherUserID, "", false),
	}
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO group_access_grants (
			group_id, grantee_user_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP + INTERVAL '1 hour')
		RETURNING id
	`, fixture.direct, subjectID, grantorID)
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO group_access_grants (
			group_id, grantee_role_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP + INTERVAL '1 hour')
		RETURNING id
	`, fixture.role, roleID, grantorID)
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO group_access_grants (
			group_id, grantee_user_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP)
		RETURNING id
	`, fixture.expiredDirect, subjectID, grantorID)
	_ = insertAuthzPolicyPostgresGrant(t, ctx, tx, `
		INSERT INTO group_access_grants (
			group_id, grantee_role_id, access_level, granted_by_user_id, expires_at
		)
		VALUES ($1, $2, 'viewer', $3, CURRENT_TIMESTAMP)
		RETURNING id
	`, fixture.expiredRole, roleID, grantorID)
	return fixture
}

func assertScopedResourceReaderAccountBehavior(
	t *testing.T,
	ctx context.Context,
	reader *scopedResourceReader,
	recorder *authzPolicyPostgresQueryRecorder,
	claims authzPolicyPostgresViewClaims,
	fixture scopedResourceReaderFixture,
) {
	t.Helper()

	filtered, filteredPage, err := reader.listAccessibleAccounts(ctx, claims, service.AccountReadQuery{
		Pagination: pagination.PaginationParams{Page: 1, PageSize: 10, SortBy: "id", SortOrder: "asc"},
		Search:     "scoped-reader-account-a",
	})
	if err != nil {
		t.Fatalf("list filtered accounts: %v", err)
	}
	assertScopedResourceReaderAccountIDs(t, filtered, []int64{fixture.owner, fixture.public})
	assertScopedResourceReaderPage(t, filteredPage, 2, 1, 10, 1)

	recorder.queries = nil
	firstPage, firstPageResult, err := reader.listAccessibleAccounts(ctx, claims, service.AccountReadQuery{
		Pagination: pagination.PaginationParams{Page: 1, PageSize: 2, SortBy: "name", SortOrder: "asc"},
	})
	if err != nil {
		t.Fatalf("list first account page: %v", err)
	}
	assertScopedResourceReaderAccountIDs(t, firstPage, []int64{fixture.owner, fixture.public})
	assertScopedResourceReaderPage(t, firstPageResult, 4, 1, 2, 2)
	accountPageQuery := scopedResourceReaderPageQuery(t, recorder)
	assertScopedResourceReaderStableOrder(t, accountPageQuery.query, "name", "id")
	assertScopedResourceReaderNarrowPageQuery(t, accountPageQuery.query, []string{
		"id", "name", "platform", "type", "status", "owner_user_id", "public_access_level", "created_at", "updated_at",
	}, []string{
		"credentials", "extra", "proxy_id", "account_count", "active_account_count", "rate_limited_account_count",
	})

	secondPage, secondPageResult, err := reader.listAccessibleAccounts(ctx, claims, service.AccountReadQuery{
		Pagination: pagination.PaginationParams{Page: 2, PageSize: 2, SortBy: "name", SortOrder: "asc"},
	})
	if err != nil {
		t.Fatalf("list second account page: %v", err)
	}
	assertScopedResourceReaderAccountIDs(t, secondPage, []int64{fixture.direct, fixture.role})
	assertScopedResourceReaderPage(t, secondPageResult, 4, 2, 2, 2)

	visible, err := reader.getAccessibleAccount(ctx, claims, fixture.role)
	if err != nil || visible == nil || visible.ID != fixture.role {
		t.Fatalf("get role-granted account = %+v, %v", visible, err)
	}
	for _, hiddenID := range []int64{fixture.private, fixture.expiredDirect, fixture.expiredRole} {
		if _, err := reader.getAccessibleAccount(ctx, claims, hiddenID); !errors.Is(err, service.ErrAccountNotFound) {
			t.Fatalf("get inaccessible account %d error = %v, want account not found", hiddenID, err)
		}
	}
}

func assertScopedResourceReaderGroupBehavior(
	t *testing.T,
	ctx context.Context,
	reader *scopedResourceReader,
	recorder *authzPolicyPostgresQueryRecorder,
	claims authzPolicyPostgresViewClaims,
	fixture scopedResourceReaderFixture,
) {
	t.Helper()

	filtered, filteredPage, err := reader.listAccessibleGroups(ctx, claims, service.GroupReadQuery{
		Pagination: pagination.PaginationParams{Page: 1, PageSize: 10, SortBy: "id", SortOrder: "asc"},
		Search:     "scoped-reader-group-a",
	})
	if err != nil {
		t.Fatalf("list filtered groups: %v", err)
	}
	assertScopedResourceReaderGroupIDs(t, filtered, []int64{fixture.owner, fixture.public})
	assertScopedResourceReaderPage(t, filteredPage, 2, 1, 10, 1)

	recorder.queries = nil
	firstPage, firstPageResult, err := reader.listAccessibleGroups(ctx, claims, service.GroupReadQuery{
		Pagination: pagination.PaginationParams{Page: 1, PageSize: 2, SortBy: "status", SortOrder: "asc"},
	})
	if err != nil {
		t.Fatalf("list first group page: %v", err)
	}
	assertScopedResourceReaderGroupIDs(t, firstPage, []int64{fixture.owner, fixture.public})
	assertScopedResourceReaderPage(t, firstPageResult, 4, 1, 2, 2)
	groupPageQuery := scopedResourceReaderPageQuery(t, recorder)
	assertScopedResourceReaderStableOrder(t, groupPageQuery.query, "status", "id")
	assertScopedResourceReaderNarrowPageQuery(t, groupPageQuery.query, []string{
		"id", "name", "description", "platform", "status", "owner_user_id", "public_access_level", "created_at", "updated_at",
	}, []string{
		"credentials", "extra", "proxy_id", "account_count", "active_account_count", "rate_limited_account_count",
	})

	secondPage, secondPageResult, err := reader.listAccessibleGroups(ctx, claims, service.GroupReadQuery{
		Pagination: pagination.PaginationParams{Page: 2, PageSize: 2, SortBy: "status", SortOrder: "asc"},
	})
	if err != nil {
		t.Fatalf("list second group page: %v", err)
	}
	assertScopedResourceReaderGroupIDs(t, secondPage, []int64{fixture.direct, fixture.role})
	assertScopedResourceReaderPage(t, secondPageResult, 4, 2, 2, 2)

	visible, err := reader.getAccessibleGroup(ctx, claims, fixture.direct)
	if err != nil || visible == nil || visible.ID != fixture.direct {
		t.Fatalf("get directly granted group = %+v, %v", visible, err)
	}
	for _, hiddenID := range []int64{fixture.private, fixture.expiredDirect, fixture.expiredRole} {
		if _, err := reader.getAccessibleGroup(ctx, claims, hiddenID); !errors.Is(err, service.ErrGroupNotFound) {
			t.Fatalf("get inaccessible group %d error = %v, want group not found", hiddenID, err)
		}
	}
}

func scopedResourceReaderPageQuery(t *testing.T, recorder *authzPolicyPostgresQueryRecorder) authzPolicyPostgresRecordedQuery {
	t.Helper()
	if len(recorder.queries) != 2 {
		t.Fatalf("scoped list query count = %d, want count and page queries", len(recorder.queries))
	}
	return recorder.queries[1]
}

func assertScopedResourceReaderNarrowPageQuery(t *testing.T, query string, required, forbidden []string) {
	t.Helper()
	lowerQuery := strings.ToLower(query)
	fromIndex := strings.Index(lowerQuery, " from ")
	if fromIndex < 0 {
		t.Fatalf("page query has no FROM clause:\n%s", query)
	}
	selectClause := lowerQuery[:fromIndex]
	if strings.Contains(selectClause, "*") {
		t.Fatalf("page query uses a wildcard projection:\n%s", query)
	}
	for _, field := range required {
		if !strings.Contains(selectClause, `"`+field+`"`) {
			t.Fatalf("page query projection is missing %q:\n%s", field, query)
		}
	}
	for _, field := range forbidden {
		if strings.Contains(selectClause, `"`+field+`"`) {
			t.Fatalf("page query projection exposes forbidden field %q:\n%s", field, query)
		}
	}
}

func assertScopedResourceReaderStableOrder(t *testing.T, query, primary, tieBreaker string) {
	t.Helper()
	lowerQuery := strings.ToLower(query)
	orderIndex := strings.LastIndex(lowerQuery, " order by ")
	if orderIndex < 0 {
		t.Fatalf("page query has no ORDER BY clause:\n%s", query)
	}
	orderClause := lowerQuery[orderIndex:]
	primaryIndex := strings.Index(orderClause, `"`+primary+`"`)
	tieBreakerIndex := strings.Index(orderClause, `"`+tieBreaker+`"`)
	if primaryIndex < 0 || tieBreakerIndex <= primaryIndex {
		t.Fatalf("page query order is not stable on %s then %s:\n%s", primary, tieBreaker, query)
	}
}

func assertScopedResourceReaderAccountIDs(t *testing.T, items []service.AccountListItem, want []int64) {
	t.Helper()
	got := make([]int64, 0, len(items))
	for _, item := range items {
		got = append(got, item.ID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("account IDs = %v, want %v", got, want)
	}
}

func assertScopedResourceReaderGroupIDs(t *testing.T, items []service.GroupListItem, want []int64) {
	t.Helper()
	got := make([]int64, 0, len(items))
	for _, item := range items {
		got = append(got, item.ID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("group IDs = %v, want %v", got, want)
	}
}

func assertScopedResourceReaderPage(
	t *testing.T,
	result *pagination.PaginationResult,
	total int64,
	page int,
	pageSize int,
	pages int,
) {
	t.Helper()
	want := &pagination.PaginationResult{Total: total, Page: page, PageSize: pageSize, Pages: pages}
	if !reflect.DeepEqual(result, want) {
		t.Fatalf("pagination = %+v, want %+v", result, want)
	}
}
