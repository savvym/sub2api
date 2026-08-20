package repository

import (
	"context"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbgroup "github.com/Wei-Shaw/sub2api/ent/group"
	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/service"

	entsql "entgo.io/ent/dialect/sql"
)

var (
	_ service.ScopedAccountReader = (*scopedResourceReader)(nil)
	_ service.ScopedGroupReader   = (*scopedResourceReader)(nil)
)

// scopedResourceReader is deliberately separate from the broad admin
// repositories. Its queries never hydrate credentials, account internals, or
// group routing and pricing configuration.
type scopedResourceReader struct {
	client *dbent.Client
}

func NewScopedResourceReader(client *dbent.Client) *scopedResourceReader {
	return &scopedResourceReader{client: client}
}

func (r *scopedResourceReader) ListAccessibleAccounts(
	ctx context.Context,
	scope authz.AccessibleScope,
	query service.AccountReadQuery,
) ([]service.AccountListItem, *pagination.PaginationResult, error) {
	return r.listAccessibleAccounts(ctx, scope, query)
}

func (r *scopedResourceReader) listAccessibleAccounts(
	ctx context.Context,
	scope accessibleScopeClaims,
	query service.AccountReadQuery,
) ([]service.AccountListItem, *pagination.PaginationResult, error) {
	if r == nil || r.client == nil {
		return nil, nil, service.ErrResourceReadUnavailable
	}
	normalized, err := query.Normalize()
	if err != nil {
		return nil, nil, err
	}
	predicate, err := accountAccessiblePredicateWithClaims(scope)
	if err != nil {
		return nil, nil, err
	}

	base := r.client.Account.Query().Where(predicate)
	if normalized.Platform != "" {
		base = base.Where(dbaccount.PlatformEQ(normalized.Platform))
	}
	if normalized.AccountType != "" {
		base = base.Where(dbaccount.TypeEQ(normalized.AccountType))
	}
	if normalized.Status != "" {
		base = base.Where(dbaccount.StatusEQ(normalized.Status))
	}
	if normalized.Search != "" {
		base = base.Where(dbaccount.NameContainsFold(normalized.Search))
	}

	total, err := base.Clone().Count(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows := make([]scopedAccountRow, 0, normalized.Pagination.Limit())
	pageQuery := base.
		Offset(normalized.Pagination.Offset()).
		Limit(normalized.Pagination.Limit())
	for _, order := range scopedAccountOrder(normalized.Pagination) {
		pageQuery = pageQuery.Order(order)
	}
	err = pageQuery.
		Select(scopedAccountColumns...).
		Scan(ctx, &rows)
	if err != nil {
		return nil, nil, err
	}
	items, err := scopedAccountRowsToService(rows)
	if err != nil {
		return nil, nil, err
	}
	return items, paginationResultFromTotal(int64(total), normalized.Pagination), nil
}

func (r *scopedResourceReader) GetAccessibleAccount(
	ctx context.Context,
	scope authz.AccessibleScope,
	id int64,
) (*service.AccountListItem, error) {
	return r.getAccessibleAccount(ctx, scope, id)
}

func (r *scopedResourceReader) getAccessibleAccount(
	ctx context.Context,
	scope accessibleScopeClaims,
	id int64,
) (*service.AccountListItem, error) {
	if r == nil || r.client == nil {
		return nil, service.ErrResourceReadUnavailable
	}
	if id <= 0 {
		return nil, service.ErrInvalidResourceReadID
	}
	predicate, err := accountAccessiblePredicateWithClaims(scope)
	if err != nil {
		return nil, err
	}
	var rows []scopedAccountRow
	err = r.client.Account.Query().
		Where(predicate, dbaccount.IDEQ(id)).
		Limit(1).
		Select(scopedAccountColumns...).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, service.ErrAccountNotFound
	}
	items, err := scopedAccountRowsToService(rows)
	if err != nil {
		return nil, err
	}
	return &items[0], nil
}

func (r *scopedResourceReader) ListAccessibleGroups(
	ctx context.Context,
	scope authz.AccessibleScope,
	query service.GroupReadQuery,
) ([]service.GroupListItem, *pagination.PaginationResult, error) {
	return r.listAccessibleGroups(ctx, scope, query)
}

func (r *scopedResourceReader) listAccessibleGroups(
	ctx context.Context,
	scope accessibleScopeClaims,
	query service.GroupReadQuery,
) ([]service.GroupListItem, *pagination.PaginationResult, error) {
	if r == nil || r.client == nil {
		return nil, nil, service.ErrResourceReadUnavailable
	}
	normalized, err := query.Normalize()
	if err != nil {
		return nil, nil, err
	}
	predicate, err := groupAccessiblePredicateWithClaims(scope)
	if err != nil {
		return nil, nil, err
	}

	base := r.client.Group.Query().Where(predicate)
	if normalized.Platform != "" {
		base = base.Where(dbgroup.PlatformEQ(normalized.Platform))
	}
	if normalized.Status != "" {
		base = base.Where(dbgroup.StatusEQ(normalized.Status))
	}
	if normalized.Search != "" {
		base = base.Where(dbgroup.Or(
			dbgroup.NameContainsFold(normalized.Search),
			dbgroup.DescriptionContainsFold(normalized.Search),
		))
	}

	total, err := base.Clone().Count(ctx)
	if err != nil {
		return nil, nil, err
	}
	rows := make([]scopedGroupRow, 0, normalized.Pagination.Limit())
	pageQuery := base.
		Offset(normalized.Pagination.Offset()).
		Limit(normalized.Pagination.Limit())
	for _, order := range scopedGroupOrder(normalized.Pagination) {
		pageQuery = pageQuery.Order(order)
	}
	err = pageQuery.
		Select(scopedGroupColumns...).
		Scan(ctx, &rows)
	if err != nil {
		return nil, nil, err
	}
	items, err := scopedGroupRowsToService(rows)
	if err != nil {
		return nil, nil, err
	}
	return items, paginationResultFromTotal(int64(total), normalized.Pagination), nil
}

func (r *scopedResourceReader) GetAccessibleGroup(
	ctx context.Context,
	scope authz.AccessibleScope,
	id int64,
) (*service.GroupListItem, error) {
	return r.getAccessibleGroup(ctx, scope, id)
}

func (r *scopedResourceReader) getAccessibleGroup(
	ctx context.Context,
	scope accessibleScopeClaims,
	id int64,
) (*service.GroupListItem, error) {
	if r == nil || r.client == nil {
		return nil, service.ErrResourceReadUnavailable
	}
	if id <= 0 {
		return nil, service.ErrInvalidResourceReadID
	}
	predicate, err := groupAccessiblePredicateWithClaims(scope)
	if err != nil {
		return nil, err
	}
	var rows []scopedGroupRow
	err = r.client.Group.Query().
		Where(predicate, dbgroup.IDEQ(id)).
		Limit(1).
		Select(scopedGroupColumns...).
		Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, service.ErrGroupNotFound
	}
	items, err := scopedGroupRowsToService(rows)
	if err != nil {
		return nil, err
	}
	return &items[0], nil
}

var scopedAccountColumns = []string{
	dbaccount.FieldID,
	dbaccount.FieldName,
	dbaccount.FieldPlatform,
	dbaccount.FieldType,
	dbaccount.FieldStatus,
	dbaccount.FieldOwnerUserID,
	dbaccount.FieldPublicAccessLevel,
	dbaccount.FieldCreatedAt,
	dbaccount.FieldUpdatedAt,
}

type scopedAccountRow struct {
	ID                int64     `sql:"id"`
	Name              string    `sql:"name"`
	Platform          string    `sql:"platform"`
	Type              string    `sql:"type"`
	Status            string    `sql:"status"`
	OwnerUserID       *int64    `sql:"owner_user_id"`
	PublicAccessLevel *string   `sql:"public_access_level"`
	CreatedAt         time.Time `sql:"created_at"`
	UpdatedAt         time.Time `sql:"updated_at"`
}

var scopedGroupColumns = []string{
	dbgroup.FieldID,
	dbgroup.FieldName,
	dbgroup.FieldDescription,
	dbgroup.FieldPlatform,
	dbgroup.FieldStatus,
	dbgroup.FieldOwnerUserID,
	dbgroup.FieldPublicAccessLevel,
	dbgroup.FieldCreatedAt,
	dbgroup.FieldUpdatedAt,
}

type scopedGroupRow struct {
	ID                int64     `sql:"id"`
	Name              string    `sql:"name"`
	Description       *string   `sql:"description"`
	Platform          string    `sql:"platform"`
	Status            string    `sql:"status"`
	OwnerUserID       *int64    `sql:"owner_user_id"`
	PublicAccessLevel *string   `sql:"public_access_level"`
	CreatedAt         time.Time `sql:"created_at"`
	UpdatedAt         time.Time `sql:"updated_at"`
}

func scopedAccountRowsToService(rows []scopedAccountRow) ([]service.AccountListItem, error) {
	items := make([]service.AccountListItem, 0, len(rows))
	for _, row := range rows {
		level, err := scopedPublicAccessLevel(row.PublicAccessLevel)
		if err != nil {
			return nil, err
		}
		if row.ID <= 0 || row.Name == "" || row.Platform == "" || row.Type == "" || row.Status == "" ||
			(row.OwnerUserID != nil && *row.OwnerUserID <= 0) {
			return nil, authz.ErrInvalidPolicySnapshot
		}
		items = append(items, service.AccountListItem{
			ID:                row.ID,
			Name:              row.Name,
			Platform:          row.Platform,
			Type:              row.Type,
			Status:            row.Status,
			OwnerUserID:       row.OwnerUserID,
			PublicAccessLevel: level,
			CreatedAt:         row.CreatedAt,
			UpdatedAt:         row.UpdatedAt,
		})
	}
	return items, nil
}

func scopedGroupRowsToService(rows []scopedGroupRow) ([]service.GroupListItem, error) {
	items := make([]service.GroupListItem, 0, len(rows))
	for _, row := range rows {
		level, err := scopedPublicAccessLevel(row.PublicAccessLevel)
		if err != nil {
			return nil, err
		}
		if row.ID <= 0 || row.Name == "" || row.Platform == "" || row.Status == "" ||
			(row.OwnerUserID != nil && *row.OwnerUserID <= 0) {
			return nil, authz.ErrInvalidPolicySnapshot
		}
		description := ""
		if row.Description != nil {
			description = *row.Description
		}
		items = append(items, service.GroupListItem{
			ID:                row.ID,
			Name:              row.Name,
			Description:       description,
			Platform:          row.Platform,
			Status:            row.Status,
			OwnerUserID:       row.OwnerUserID,
			PublicAccessLevel: level,
			CreatedAt:         row.CreatedAt,
			UpdatedAt:         row.UpdatedAt,
		})
	}
	return items, nil
}

func scopedPublicAccessLevel(raw *string) (*authz.AccessLevel, error) {
	if raw == nil {
		return nil, nil
	}
	level, ok := authz.ParseAccessLevel(*raw)
	if !ok || !level.AllowedAsPublic() {
		return nil, authz.ErrInvalidPolicySnapshot
	}
	return &level, nil
}

func scopedAccountOrder(params pagination.PaginationParams) []func(*entsql.Selector) {
	fields := map[string]string{
		"name":       dbaccount.FieldName,
		"id":         dbaccount.FieldID,
		"platform":   dbaccount.FieldPlatform,
		"type":       dbaccount.FieldType,
		"status":     dbaccount.FieldStatus,
		"created_at": dbaccount.FieldCreatedAt,
		"updated_at": dbaccount.FieldUpdatedAt,
	}
	return scopedResourceOrder(fields[params.SortBy], dbaccount.FieldID, params.SortOrder)
}

func scopedGroupOrder(params pagination.PaginationParams) []func(*entsql.Selector) {
	fields := map[string]string{
		"name":       dbgroup.FieldName,
		"id":         dbgroup.FieldID,
		"platform":   dbgroup.FieldPlatform,
		"status":     dbgroup.FieldStatus,
		"created_at": dbgroup.FieldCreatedAt,
		"updated_at": dbgroup.FieldUpdatedAt,
	}
	return scopedResourceOrder(fields[params.SortBy], dbgroup.FieldID, params.SortOrder)
}

func scopedResourceOrder(field, idField, direction string) []func(*entsql.Selector) {
	field = strings.TrimSpace(field)
	if field == "" {
		field = idField
	}
	order := dbent.Asc
	if direction == pagination.SortOrderDesc {
		order = dbent.Desc
	}
	if field == idField {
		return []func(*entsql.Selector){order(field)}
	}
	return []func(*entsql.Selector){order(field), order(idField)}
}
