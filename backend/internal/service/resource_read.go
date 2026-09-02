package service

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

const (
	resourceReadDefaultPageSize = 20
	resourceReadMaxPageSize     = 1000
	resourceReadMaxFilterLength = 100
)

var (
	ErrInvalidResourceReadID = infraerrors.BadRequest(
		"INVALID_RESOURCE_READ_ID",
		"resource id must be positive",
	)
	ErrInvalidResourceReadQuery = infraerrors.BadRequest(
		"INVALID_RESOURCE_READ_QUERY",
		"invalid resource read query",
	)
	ErrResourceReadUnavailable = infraerrors.ServiceUnavailable(
		"RESOURCE_READ_UNAVAILABLE",
		"resource read service is unavailable",
	)
)

// AccountListItem is the shared-safe account projection. Deliberately absent
// fields include credentials, extra, proxy details, errors, quotas, scheduler
// state, parent-account metadata, and account/group relationships.
type AccountListItem struct {
	ID                   int64
	Name                 string
	Platform             string
	Type                 string
	Status               string
	CredentialConfigured bool
	OwnerUserID          *int64
	PublicAccessLevel    *authz.AccessLevel
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// GroupListItem is the shared-safe group projection. Account topology, account
// counts, pricing, profit controls, fallback routing, and model routing are not
// part of this read model.
type GroupListItem struct {
	ID                int64
	Name              string
	Description       string
	Platform          string
	Status            string
	OwnerUserID       *int64
	PublicAccessLevel *authz.AccessLevel
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type AccountReadQuery struct {
	Pagination  pagination.PaginationParams
	Platform    string
	AccountType string
	Status      string
	Search      string
}

// Normalize returns the only query shape repositories may execute. Keeping
// this method public lets repository implementations revalidate direct calls.
func (q AccountReadQuery) Normalize() (AccountReadQuery, error) {
	var err error
	q.Platform, err = normalizeResourceReadFilter("platform", q.Platform)
	if err != nil {
		return AccountReadQuery{}, err
	}
	q.AccountType, err = normalizeResourceReadFilter("account type", q.AccountType)
	if err != nil {
		return AccountReadQuery{}, err
	}
	q.Status, err = normalizeResourceReadFilter("status", q.Status)
	if err != nil {
		return AccountReadQuery{}, err
	}
	q.Search, err = normalizeResourceReadFilter("search", q.Search)
	if err != nil {
		return AccountReadQuery{}, err
	}
	q.Pagination, err = normalizeResourceReadPagination(
		q.Pagination,
		"name",
		map[string]struct{}{
			"name":       {},
			"id":         {},
			"platform":   {},
			"type":       {},
			"status":     {},
			"created_at": {},
			"updated_at": {},
		},
	)
	if err != nil {
		return AccountReadQuery{}, err
	}
	return q, nil
}

type GroupReadQuery struct {
	Pagination pagination.PaginationParams
	Platform   string
	Status     string
	Search     string
}

// Normalize intentionally rejects account_count. An unscoped group count
// reveals accounts the actor cannot view, while a correctly scoped count needs
// a separate account.view scope and is outside this minimal read projection.
func (q GroupReadQuery) Normalize() (GroupReadQuery, error) {
	var err error
	q.Platform, err = normalizeResourceReadFilter("platform", q.Platform)
	if err != nil {
		return GroupReadQuery{}, err
	}
	q.Status, err = normalizeResourceReadFilter("status", q.Status)
	if err != nil {
		return GroupReadQuery{}, err
	}
	q.Search, err = normalizeResourceReadFilter("search", q.Search)
	if err != nil {
		return GroupReadQuery{}, err
	}
	if strings.EqualFold(strings.TrimSpace(q.Pagination.SortBy), "account_count") {
		return GroupReadQuery{}, fmt.Errorf(
			"%w: account_count is not available for scoped group reads",
			ErrInvalidResourceReadQuery,
		)
	}
	q.Pagination, err = normalizeResourceReadPagination(
		q.Pagination,
		"name",
		map[string]struct{}{
			"name":       {},
			"id":         {},
			"platform":   {},
			"status":     {},
			"created_at": {},
			"updated_at": {},
		},
	)
	if err != nil {
		return GroupReadQuery{}, err
	}
	return q, nil
}

type ScopedAccountReader interface {
	ListAccessibleAccounts(ctx context.Context, scope authz.AccessibleScope, query AccountReadQuery) ([]AccountListItem, *pagination.PaginationResult, error)
	GetAccessibleAccount(ctx context.Context, scope authz.AccessibleScope, id int64) (*AccountListItem, error)
}

type ScopedGroupReader interface {
	ListAccessibleGroups(ctx context.Context, scope authz.AccessibleScope, query GroupReadQuery) ([]GroupListItem, *pagination.PaginationResult, error)
	GetAccessibleGroup(ctx context.Context, scope authz.AccessibleScope, id int64) (*GroupListItem, error)
}

type ResourceReadService struct {
	policy   authz.ResourcePolicy
	accounts ScopedAccountReader
	groups   ScopedGroupReader
}

func NewResourceReadService(policy authz.ResourcePolicy, accounts ScopedAccountReader, groups ScopedGroupReader) *ResourceReadService {
	return &ResourceReadService{policy: policy, accounts: accounts, groups: groups}
}

func (s *ResourceReadService) ListAccounts(ctx context.Context, actor authz.Actor, query AccountReadQuery) ([]AccountListItem, *pagination.PaginationResult, error) {
	if s == nil || s.policy == nil || s.accounts == nil {
		return nil, nil, ErrResourceReadUnavailable
	}
	normalized, err := query.Normalize()
	if err != nil {
		return nil, nil, err
	}
	scope, err := s.policy.AccessibleScope(ctx, actor, authz.ResourceTypeAccount, authz.ActionAccountView)
	if err != nil {
		return nil, nil, err
	}
	items, result, err := s.accounts.ListAccessibleAccounts(ctx, scope, normalized)
	if err != nil {
		return nil, nil, err
	}
	if result == nil {
		return nil, nil, fmt.Errorf("%w: account reader returned nil pagination", ErrResourceReadUnavailable)
	}
	return items, result, nil
}

func (s *ResourceReadService) GetAccount(ctx context.Context, actor authz.Actor, id int64) (*AccountListItem, error) {
	if s == nil || s.policy == nil || s.accounts == nil {
		return nil, ErrResourceReadUnavailable
	}
	if id <= 0 {
		return nil, ErrInvalidResourceReadID
	}
	scope, err := s.policy.AccessibleScope(ctx, actor, authz.ResourceTypeAccount, authz.ActionAccountView)
	if err != nil {
		return nil, err
	}
	item, err := s.accounts.GetAccessibleAccount(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	if item == nil || item.ID != id {
		return nil, fmt.Errorf("%w: account reader returned an invalid result", ErrResourceReadUnavailable)
	}
	return item, nil
}

func (s *ResourceReadService) ListGroups(ctx context.Context, actor authz.Actor, query GroupReadQuery) ([]GroupListItem, *pagination.PaginationResult, error) {
	if s == nil || s.policy == nil || s.groups == nil {
		return nil, nil, ErrResourceReadUnavailable
	}
	normalized, err := query.Normalize()
	if err != nil {
		return nil, nil, err
	}
	scope, err := s.policy.AccessibleScope(ctx, actor, authz.ResourceTypeGroup, authz.ActionGroupView)
	if err != nil {
		return nil, nil, err
	}
	items, result, err := s.groups.ListAccessibleGroups(ctx, scope, normalized)
	if err != nil {
		return nil, nil, err
	}
	if result == nil {
		return nil, nil, fmt.Errorf("%w: group reader returned nil pagination", ErrResourceReadUnavailable)
	}
	return items, result, nil
}

func (s *ResourceReadService) GetGroup(ctx context.Context, actor authz.Actor, id int64) (*GroupListItem, error) {
	if s == nil || s.policy == nil || s.groups == nil {
		return nil, ErrResourceReadUnavailable
	}
	if id <= 0 {
		return nil, ErrInvalidResourceReadID
	}
	scope, err := s.policy.AccessibleScope(ctx, actor, authz.ResourceTypeGroup, authz.ActionGroupView)
	if err != nil {
		return nil, err
	}
	item, err := s.groups.GetAccessibleGroup(ctx, scope, id)
	if err != nil {
		return nil, err
	}
	if item == nil || item.ID != id {
		return nil, fmt.Errorf("%w: group reader returned an invalid result", ErrResourceReadUnavailable)
	}
	return item, nil
}

func normalizeResourceReadFilter(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || len(value) > resourceReadMaxFilterLength {
		return "", fmt.Errorf("%w: invalid %s", ErrInvalidResourceReadQuery, name)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", fmt.Errorf("%w: invalid %s", ErrInvalidResourceReadQuery, name)
		}
	}
	return value, nil
}

func normalizeResourceReadPagination(params pagination.PaginationParams, defaultSort string, allowedSorts map[string]struct{}) (pagination.PaginationParams, error) {
	if params.Page < 0 || params.PageSize < 0 || params.PageSize > resourceReadMaxPageSize {
		return pagination.PaginationParams{}, fmt.Errorf("%w: invalid pagination", ErrInvalidResourceReadQuery)
	}
	if params.Page == 0 {
		params.Page = 1
	}
	if params.PageSize == 0 {
		params.PageSize = resourceReadDefaultPageSize
	}
	maxInt := int(^uint(0) >> 1)
	if params.Page-1 > maxInt/params.PageSize {
		return pagination.PaginationParams{}, fmt.Errorf("%w: pagination offset overflows", ErrInvalidResourceReadQuery)
	}

	sortBy := strings.ToLower(strings.TrimSpace(params.SortBy))
	if sortBy == "" {
		sortBy = defaultSort
	}
	if _, ok := allowedSorts[sortBy]; !ok {
		return pagination.PaginationParams{}, fmt.Errorf("%w: unsupported sort field", ErrInvalidResourceReadQuery)
	}
	params.SortBy = sortBy

	sortOrder := strings.ToLower(strings.TrimSpace(params.SortOrder))
	if sortOrder == "" {
		sortOrder = pagination.SortOrderAsc
	}
	if sortOrder != pagination.SortOrderAsc && sortOrder != pagination.SortOrderDesc {
		return pagination.PaginationParams{}, fmt.Errorf("%w: unsupported sort order", ErrInvalidResourceReadQuery)
	}
	params.SortOrder = sortOrder
	return params, nil
}
