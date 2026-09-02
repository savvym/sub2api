package repository

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

type scopedReaderQueryRecorder struct {
	mu      sync.Mutex
	queries []string
}

func (r *scopedReaderQueryRecorder) Match(_, actual string) error {
	if r == nil {
		return fmt.Errorf("scoped reader query recorder is nil")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, actual)
	return nil
}

func (r *scopedReaderQueryRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.queries...)
}

func newScopedReaderTestHarness(t *testing.T) (*scopedResourceReader, sqlmock.Sqlmock, *scopedReaderQueryRecorder) {
	t.Helper()
	recorder := &scopedReaderQueryRecorder{}
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(recorder))
	require.NoError(t, err)
	driver := entsql.OpenDB(dialect.Postgres, db)
	client := dbent.NewClient(dbent.Driver(driver))
	t.Cleanup(func() { _ = client.Close() })
	return NewScopedResourceReader(client), mock, recorder
}

func validScopedAccountClaims() fakeAccessibleScopeClaims {
	return fakeAccessibleScopeClaims{
		valid:               true,
		resourceType:        authz.ResourceTypeAccount,
		action:              authz.ActionAccountView,
		subjectKind:         authz.SubjectKindUser,
		subjectID:           42,
		subjectAuthzVersion: 7,
		roleVersions:        map[int64]int64{},
		roleMode:            authz.RoleAuthorizationModeRBAC,
		includeOwner:        true,
	}
}

func validScopedGroupClaims() fakeAccessibleScopeClaims {
	return fakeAccessibleScopeClaims{
		valid:               true,
		resourceType:        authz.ResourceTypeGroup,
		action:              authz.ActionGroupView,
		subjectKind:         authz.SubjectKindUser,
		subjectID:           42,
		subjectAuthzVersion: 7,
		roleVersions:        map[int64]int64{},
		roleMode:            authz.RoleAuthorizationModeRBAC,
		includeOwner:        true,
	}
}

func TestScopedResourceReaderListAccountsScopesCountAndPageBeforeNormalizedFilters(t *testing.T) {
	reader, mock, recorder := newScopedReaderTestHarness(t)
	now := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	viewer := string(authz.AccessLevelViewer)

	mock.ExpectQuery("scoped account count").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(3))
	mock.ExpectQuery("scoped account page").
		WillReturnRows(sqlmock.NewRows(scopedAccountResultColumns).
			AddRow(int64(11), "alpha", "openai", "oauth", "active", int64(42), nil, now, now.Add(time.Minute), true).
			AddRow(int64(12), "beta", "openai", "oauth", "active", int64(99), viewer, now, now.Add(2*time.Minute), false))

	items, result, err := reader.listAccessibleAccounts(context.Background(), validScopedAccountClaims(), service.AccountReadQuery{
		Pagination: pagination.PaginationParams{
			Page:      2,
			PageSize:  2,
			SortBy:    " PLATFORM ",
			SortOrder: " DESC ",
		},
		Platform:    " openai ",
		AccountType: " oauth ",
		Status:      " active ",
		Search:      " alpha ",
	})
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, int64(11), items[0].ID)
	require.True(t, items[0].CredentialConfigured)
	require.Nil(t, items[0].PublicAccessLevel)
	require.False(t, items[1].CredentialConfigured)
	require.NotNil(t, items[1].PublicAccessLevel)
	require.Equal(t, authz.AccessLevelViewer, *items[1].PublicAccessLevel)
	require.Equal(t, int64(3), result.Total)
	require.Equal(t, 2, result.Page)
	require.Equal(t, 2, result.PageSize)
	require.Equal(t, 2, result.Pages)
	require.NoError(t, mock.ExpectationsWereMet())

	queries := recorder.snapshot()
	require.Len(t, queries, 2, "Count must execute before the page query")
	countSQL := compactScopedReaderSQL(queries[0])
	pageSQL := compactScopedReaderSQL(queries[1])
	require.Contains(t, countSQL, "SELECT COUNT(")
	requireScopedReaderScopeBeforeFilter(t, countSQL, `"accounts"."platform" =`)
	requireScopedReaderScopeBeforeFilter(t, pageSQL, `"accounts"."platform" =`)
	for _, query := range []string{countSQL, pageSQL} {
		require.Contains(t, query, "current_subject AS")
		require.Contains(t, query, "policy_configuration AS")
		require.Contains(t, query, `"accounts"."type" =`)
		require.Contains(t, query, `"accounts"."status" =`)
		require.Contains(t, query, `"accounts"."name" ILIKE`)
	}
	require.Contains(t, pageSQL, `ORDER BY "accounts"."platform" DESC, "accounts"."id" DESC`)
	require.Contains(t, pageSQL, "LIMIT 2 OFFSET 2")
	requireScopedAccountProjection(t, pageSQL)
}

func TestScopedResourceReaderListGroupsScopesCountAndPageBeforeNormalizedFilters(t *testing.T) {
	reader, mock, recorder := newScopedReaderTestHarness(t)
	now := time.Date(2026, 8, 20, 11, 0, 0, 0, time.UTC)
	description := "shared group"
	consumer := string(authz.AccessLevelConsumer)

	mock.ExpectQuery("scoped group count").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery("scoped group page").
		WillReturnRows(sqlmock.NewRows(scopedGroupColumns).
			AddRow(int64(21), "shared", description, "anthropic", "active", int64(99), consumer, now, now.Add(time.Minute)))

	items, result, err := reader.listAccessibleGroups(context.Background(), validScopedGroupClaims(), service.GroupReadQuery{
		Pagination: pagination.PaginationParams{
			Page:      1,
			PageSize:  3,
			SortBy:    " CREATED_AT ",
			SortOrder: " ASC ",
		},
		Platform: " anthropic ",
		Status:   " active ",
		Search:   " shared ",
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, description, items[0].Description)
	require.NotNil(t, items[0].PublicAccessLevel)
	require.Equal(t, authz.AccessLevelConsumer, *items[0].PublicAccessLevel)
	require.Equal(t, int64(1), result.Total)
	require.Equal(t, 1, result.Page)
	require.Equal(t, 3, result.PageSize)
	require.Equal(t, 1, result.Pages)
	require.NoError(t, mock.ExpectationsWereMet())

	queries := recorder.snapshot()
	require.Len(t, queries, 2, "Count must execute before the page query")
	countSQL := compactScopedReaderSQL(queries[0])
	pageSQL := compactScopedReaderSQL(queries[1])
	require.Contains(t, countSQL, "SELECT COUNT(")
	requireScopedReaderScopeBeforeFilter(t, countSQL, `"groups"."platform" =`)
	requireScopedReaderScopeBeforeFilter(t, pageSQL, `"groups"."platform" =`)
	for _, query := range []string{countSQL, pageSQL} {
		require.Contains(t, query, "current_subject AS")
		require.Contains(t, query, "policy_configuration AS")
		require.Contains(t, query, `"groups"."status" =`)
		require.Contains(t, query, `"groups"."name" ILIKE`)
		require.Contains(t, query, `"groups"."description" ILIKE`)
	}
	require.Contains(t, pageSQL, `ORDER BY "groups"."created_at" ASC, "groups"."id" ASC`)
	require.Contains(t, pageSQL, "LIMIT 3 OFFSET 0")
	requireScopedGroupProjection(t, pageSQL)
}

func TestScopedResourceReaderGetUsesNarrowScopeAndUnifiesInvisibleAndMissing(t *testing.T) {
	t.Run("account", func(t *testing.T) {
		reader, mock, recorder := newScopedReaderTestHarness(t)
		for range 2 {
			mock.ExpectQuery("scoped account detail").
				WillReturnRows(sqlmock.NewRows(scopedAccountResultColumns))
		}

		invisible, err := reader.getAccessibleAccount(context.Background(), validScopedAccountClaims(), 71)
		require.Nil(t, invisible)
		require.ErrorIs(t, err, service.ErrAccountNotFound)
		missing, err := reader.getAccessibleAccount(context.Background(), validScopedAccountClaims(), 72)
		require.Nil(t, missing)
		require.ErrorIs(t, err, service.ErrAccountNotFound)
		require.NoError(t, mock.ExpectationsWereMet())

		queries := recorder.snapshot()
		require.Len(t, queries, 2)
		for _, query := range queries {
			normalized := compactScopedReaderSQL(query)
			require.Contains(t, normalized, "current_subject AS")
			require.Contains(t, normalized, `"accounts"."id" =`)
			requireScopedAccountProjection(t, normalized)
		}
	})

	t.Run("group", func(t *testing.T) {
		reader, mock, recorder := newScopedReaderTestHarness(t)
		for range 2 {
			mock.ExpectQuery("scoped group detail").
				WillReturnRows(sqlmock.NewRows(scopedGroupColumns))
		}

		invisible, err := reader.getAccessibleGroup(context.Background(), validScopedGroupClaims(), 81)
		require.Nil(t, invisible)
		require.ErrorIs(t, err, service.ErrGroupNotFound)
		missing, err := reader.getAccessibleGroup(context.Background(), validScopedGroupClaims(), 82)
		require.Nil(t, missing)
		require.ErrorIs(t, err, service.ErrGroupNotFound)
		require.NoError(t, mock.ExpectationsWereMet())

		queries := recorder.snapshot()
		require.Len(t, queries, 2)
		for _, query := range queries {
			normalized := compactScopedReaderSQL(query)
			require.Contains(t, normalized, "current_subject AS")
			require.Contains(t, normalized, `"groups"."id" =`)
			requireScopedGroupProjection(t, normalized)
		}
	})
}

func TestScopedResourceReaderFailsClosedBeforeDatabaseAccess(t *testing.T) {
	ctx := context.Background()
	accountScope := validScopedAccountClaims()
	groupScope := validScopedGroupClaims()

	t.Run("nil reader and nil client", func(t *testing.T) {
		var nilReader *scopedResourceReader
		_, _, err := nilReader.listAccessibleAccounts(ctx, accountScope, service.AccountReadQuery{})
		require.ErrorIs(t, err, service.ErrResourceReadUnavailable)
		_, err = nilReader.getAccessibleGroup(ctx, groupScope, 1)
		require.ErrorIs(t, err, service.ErrResourceReadUnavailable)

		emptyReader := &scopedResourceReader{}
		_, _, err = emptyReader.listAccessibleGroups(ctx, groupScope, service.GroupReadQuery{})
		require.ErrorIs(t, err, service.ErrResourceReadUnavailable)
		_, err = emptyReader.getAccessibleAccount(ctx, accountScope, 1)
		require.ErrorIs(t, err, service.ErrResourceReadUnavailable)
	})

	t.Run("invalid query scope and id", func(t *testing.T) {
		reader, mock, recorder := newScopedReaderTestHarness(t)

		_, _, err := reader.listAccessibleAccounts(ctx, accountScope, service.AccountReadQuery{
			Pagination: pagination.PaginationParams{SortBy: "credentials"},
		})
		require.ErrorIs(t, err, service.ErrInvalidResourceReadQuery)
		_, _, err = reader.listAccessibleGroups(ctx, groupScope, service.GroupReadQuery{Search: "private\x00group"})
		require.ErrorIs(t, err, service.ErrInvalidResourceReadQuery)
		_, _, err = reader.listAccessibleGroups(ctx, groupScope, service.GroupReadQuery{
			Pagination: pagination.PaginationParams{SortBy: "account_count"},
		})
		require.ErrorIs(t, err, service.ErrInvalidResourceReadQuery)
		_, _, err = reader.listAccessibleAccounts(ctx, accountScope, service.AccountReadQuery{
			Pagination: pagination.PaginationParams{Page: int(^uint(0) >> 1), PageSize: 2},
		})
		require.ErrorIs(t, err, service.ErrInvalidResourceReadQuery)

		_, _, err = reader.listAccessibleAccounts(ctx, fakeAccessibleScopeClaims{}, service.AccountReadQuery{})
		require.ErrorIs(t, err, authz.ErrInvalidResourceRef)
		_, err = reader.getAccessibleGroup(ctx, fakeAccessibleScopeClaims{}, 1)
		require.ErrorIs(t, err, authz.ErrInvalidResourceRef)

		var nilScope accessibleScopeClaims
		_, _, err = reader.listAccessibleGroups(ctx, nilScope, service.GroupReadQuery{})
		require.ErrorIs(t, err, authz.ErrInvalidResourceRef)
		_, err = reader.getAccessibleAccount(ctx, accountScope, 0)
		require.ErrorIs(t, err, service.ErrInvalidResourceReadID)
		_, err = reader.getAccessibleGroup(ctx, groupScope, -1)
		require.ErrorIs(t, err, service.ErrInvalidResourceReadID)

		require.Empty(t, recorder.snapshot(), "invalid input must fail before issuing SQL")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestScopedResourceRowMappersRejectInvalidPublicAccessLevel(t *testing.T) {
	manager := string(authz.AccessLevelManager)
	unknown := "superuser"

	_, err := scopedAccountRowsToService([]scopedAccountRow{{PublicAccessLevel: &manager}})
	require.ErrorIs(t, err, authz.ErrInvalidPolicySnapshot)
	_, err = scopedGroupRowsToService([]scopedGroupRow{{PublicAccessLevel: &unknown}})
	require.ErrorIs(t, err, authz.ErrInvalidPolicySnapshot)

	viewer := string(authz.AccessLevelViewer)
	level, err := scopedPublicAccessLevel(&viewer)
	require.NoError(t, err)
	require.NotNil(t, level)
	require.Equal(t, authz.AccessLevelViewer, *level)
	level, err = scopedPublicAccessLevel(nil)
	require.NoError(t, err)
	require.Nil(t, level)
}

func requireScopedReaderScopeBeforeFilter(t *testing.T, query, filter string) {
	t.Helper()
	scopeIndex := strings.Index(query, "current_subject AS")
	filterIndex := strings.Index(query, filter)
	require.NotEqual(t, -1, scopeIndex, "scope CTE missing from query: %s", query)
	require.NotEqual(t, -1, filterIndex, "normalized filter missing from query: %s", query)
	require.Less(t, scopeIndex, filterIndex, "scope predicate must precede business filters: %s", query)
}

func requireScopedAccountProjection(t *testing.T, query string) {
	t.Helper()
	requireScopedReaderProjection(t, query, []string{
		`"id"`, `"name"`, `"platform"`, `"type"`, `"status"`,
		`"owner_user_id"`, `"public_access_level"`, `"created_at"`, `"updated_at"`,
		`COALESCE("accounts"."credentials" <> '{}'::jsonb, FALSE) AS "credential_configured"`,
	}, []string{
		`"accounts"."extra"`, "proxy_id", "proxy_fallback_origin_id", "notes", "error_message",
		"concurrency", "priority", "rate_multiplier", "load_factor", "schedulable", "rate_limited_at",
		"rate_limit_reset_at", "overload_until", "temp_unschedulable", "session_window", "parent_account_id",
		"quota_dimension", "account_groups", "group_ids", "account_count",
	})
	require.Equal(t, 1, strings.Count(query, `"accounts"."credentials"`), "credentials may only appear in the configured boolean expression")
}

func requireScopedGroupProjection(t *testing.T, query string) {
	t.Helper()
	requireScopedReaderProjection(t, query, []string{
		`"id"`, `"name"`, `"description"`, `"platform"`, `"status"`,
		`"owner_user_id"`, `"public_access_level"`, `"created_at"`, `"updated_at"`,
	}, []string{
		"account_count", "active_account_count", "rate_limited_account_count", "account_groups", "accounts",
		"subscription_price", "rate_multiplier", "profit_rate", "model_routing", "fallback_group_id",
		"sort_order", "rpm_limit", "max_reasoning_effort",
	})
}

func requireScopedReaderProjection(t *testing.T, query string, required, forbidden []string) {
	t.Helper()
	selectClause, _, found := strings.Cut(query, " FROM ")
	require.True(t, found, "unexpected projection SQL: %s", query)
	require.Equal(t, len(required)-1, countScopedProjectionSeparators(selectClause), "projection column count changed: %s", selectClause)
	for _, field := range required {
		require.Contains(t, selectClause, field)
	}
	for _, field := range forbidden {
		require.NotContains(t, selectClause, field)
	}
}

func countScopedProjectionSeparators(query string) int {
	depth := 0
	inSingleQuote := false
	inDoubleQuote := false
	count := 0
	for _, character := range query {
		switch character {
		case '\'':
			if !inDoubleQuote {
				inSingleQuote = !inSingleQuote
			}
		case '"':
			if !inSingleQuote {
				inDoubleQuote = !inDoubleQuote
			}
		case '(':
			if !inSingleQuote && !inDoubleQuote {
				depth++
			}
		case ')':
			if !inSingleQuote && !inDoubleQuote && depth > 0 {
				depth--
			}
		case ',':
			if !inSingleQuote && !inDoubleQuote && depth == 0 {
				count++
			}
		}
	}
	return count
}

func compactScopedReaderSQL(query string) string {
	return strings.Join(strings.Fields(query), " ")
}
