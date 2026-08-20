package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

type resourceReadPolicyCall struct {
	resourceType authz.ResourceType
	action       authz.Action
}

type resourceReadPolicyStub struct {
	scope authz.AccessibleScope
	err   error
	calls []resourceReadPolicyCall
}

func (s *resourceReadPolicyStub) CheckCapability(context.Context, authz.Actor, authz.Capability) (authz.Decision, error) {
	return authz.Decision{}, errors.New("unexpected CheckCapability call")
}

func (s *resourceReadPolicyStub) CanCreate(context.Context, authz.Actor, authz.ResourceType) (authz.Decision, error) {
	return authz.Decision{}, errors.New("unexpected CanCreate call")
}

func (s *resourceReadPolicyStub) Authorize(context.Context, authz.Actor, authz.Action, authz.ResourceRef) (authz.Decision, error) {
	return authz.Decision{}, errors.New("unexpected Authorize call")
}

func (s *resourceReadPolicyStub) AccessibleScope(_ context.Context, _ authz.Actor, resourceType authz.ResourceType, action authz.Action) (authz.AccessibleScope, error) {
	s.calls = append(s.calls, resourceReadPolicyCall{resourceType: resourceType, action: action})
	return s.scope, s.err
}

type scopedAccountReaderStub struct {
	listItems  []AccountListItem
	listResult *pagination.PaginationResult
	listErr    error
	getItem    *AccountListItem
	getErr     error
	listCalls  int
	getCalls   int
	lastScope  authz.AccessibleScope
	lastQuery  AccountReadQuery
	lastID     int64
}

func (s *scopedAccountReaderStub) ListAccessibleAccounts(_ context.Context, scope authz.AccessibleScope, query AccountReadQuery) ([]AccountListItem, *pagination.PaginationResult, error) {
	s.listCalls++
	s.lastScope = scope
	s.lastQuery = query
	return s.listItems, s.listResult, s.listErr
}

func (s *scopedAccountReaderStub) GetAccessibleAccount(_ context.Context, scope authz.AccessibleScope, id int64) (*AccountListItem, error) {
	s.getCalls++
	s.lastScope = scope
	s.lastID = id
	return s.getItem, s.getErr
}

type scopedGroupReaderStub struct {
	listItems  []GroupListItem
	listResult *pagination.PaginationResult
	listErr    error
	getItem    *GroupListItem
	getErr     error
	listCalls  int
	getCalls   int
	lastScope  authz.AccessibleScope
	lastQuery  GroupReadQuery
	lastID     int64
}

func (s *scopedGroupReaderStub) ListAccessibleGroups(_ context.Context, scope authz.AccessibleScope, query GroupReadQuery) ([]GroupListItem, *pagination.PaginationResult, error) {
	s.listCalls++
	s.lastScope = scope
	s.lastQuery = query
	return s.listItems, s.listResult, s.listErr
}

func (s *scopedGroupReaderStub) GetAccessibleGroup(_ context.Context, scope authz.AccessibleScope, id int64) (*GroupListItem, error) {
	s.getCalls++
	s.lastScope = scope
	s.lastID = id
	return s.getItem, s.getErr
}

func TestResourceReadServiceListsWithExactViewScopes(t *testing.T) {
	tests := []struct {
		name         string
		resourceType authz.ResourceType
		action       authz.Action
		run          func(*ResourceReadService) error
		assertQuery  func(*testing.T, *scopedAccountReaderStub, *scopedGroupReaderStub)
	}{
		{
			name:         "accounts",
			resourceType: authz.ResourceTypeAccount,
			action:       authz.ActionAccountView,
			run: func(service *ResourceReadService) error {
				_, _, err := service.ListAccounts(context.Background(), authz.Actor{}, AccountReadQuery{
					Pagination:  pagination.PaginationParams{SortBy: " PLATFORM ", SortOrder: " DESC "},
					Platform:    " openai ",
					AccountType: " apikey ",
					Status:      " active ",
					Search:      " needle ",
				})
				return err
			},
			assertQuery: func(t *testing.T, accounts *scopedAccountReaderStub, _ *scopedGroupReaderStub) {
				t.Helper()
				if accounts.listCalls != 1 {
					t.Fatalf("account list calls = %d, want 1", accounts.listCalls)
				}
				got := accounts.lastQuery
				if got.Platform != "openai" || got.AccountType != "apikey" || got.Status != "active" || got.Search != "needle" {
					t.Fatalf("account query was not normalized: %#v", got)
				}
				if got.Pagination.Page != 1 || got.Pagination.PageSize != 20 || got.Pagination.SortBy != "platform" || got.Pagination.SortOrder != "desc" {
					t.Fatalf("account pagination was not normalized: %#v", got.Pagination)
				}
			},
		},
		{
			name:         "groups",
			resourceType: authz.ResourceTypeGroup,
			action:       authz.ActionGroupView,
			run: func(service *ResourceReadService) error {
				_, _, err := service.ListGroups(context.Background(), authz.Actor{}, GroupReadQuery{
					Pagination: pagination.PaginationParams{Page: 2, PageSize: 5, SortBy: " NAME ", SortOrder: " ASC "},
					Platform:   " anthropic ",
					Status:     " active ",
					Search:     " shared ",
				})
				return err
			},
			assertQuery: func(t *testing.T, _ *scopedAccountReaderStub, groups *scopedGroupReaderStub) {
				t.Helper()
				if groups.listCalls != 1 {
					t.Fatalf("group list calls = %d, want 1", groups.listCalls)
				}
				got := groups.lastQuery
				if got.Platform != "anthropic" || got.Status != "active" || got.Search != "shared" {
					t.Fatalf("group query was not normalized: %#v", got)
				}
				if got.Pagination.Page != 2 || got.Pagination.PageSize != 5 || got.Pagination.SortBy != "name" || got.Pagination.SortOrder != "asc" {
					t.Fatalf("group pagination was not normalized: %#v", got.Pagination)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := &resourceReadPolicyStub{}
			accounts := &scopedAccountReaderStub{listResult: &pagination.PaginationResult{Total: 1, Page: 1, PageSize: 20, Pages: 1}}
			groups := &scopedGroupReaderStub{listResult: &pagination.PaginationResult{Total: 1, Page: 1, PageSize: 20, Pages: 1}}
			service := NewResourceReadService(policy, accounts, groups)
			if err := test.run(service); err != nil {
				t.Fatalf("list resource: %v", err)
			}
			if len(policy.calls) != 1 {
				t.Fatalf("policy calls = %d, want 1", len(policy.calls))
			}
			if policy.calls[0].resourceType != test.resourceType || policy.calls[0].action != test.action {
				t.Fatalf("policy scope call = %#v, want %s/%s", policy.calls[0], test.resourceType, test.action)
			}
			test.assertQuery(t, accounts, groups)
		})
	}
}

func TestResourceReadServiceGetsWithExactViewScopes(t *testing.T) {
	policy := &resourceReadPolicyStub{}
	accounts := &scopedAccountReaderStub{getItem: &AccountListItem{ID: 41}}
	groups := &scopedGroupReaderStub{getItem: &GroupListItem{ID: 52}}
	service := NewResourceReadService(policy, accounts, groups)

	account, err := service.GetAccount(context.Background(), authz.Actor{}, 41)
	if err != nil || account.ID != 41 {
		t.Fatalf("get account = %#v, %v", account, err)
	}
	group, err := service.GetGroup(context.Background(), authz.Actor{}, 52)
	if err != nil || group.ID != 52 {
		t.Fatalf("get group = %#v, %v", group, err)
	}

	if len(policy.calls) != 2 ||
		policy.calls[0] != (resourceReadPolicyCall{resourceType: authz.ResourceTypeAccount, action: authz.ActionAccountView}) ||
		policy.calls[1] != (resourceReadPolicyCall{resourceType: authz.ResourceTypeGroup, action: authz.ActionGroupView}) {
		t.Fatalf("policy calls = %#v", policy.calls)
	}
	if accounts.getCalls != 1 || accounts.lastID != 41 || groups.getCalls != 1 || groups.lastID != 52 {
		t.Fatalf("reader get calls: accounts=%d/%d groups=%d/%d", accounts.getCalls, accounts.lastID, groups.getCalls, groups.lastID)
	}
}

func TestResourceReadServicePropagatesPolicyAndRepositoryErrors(t *testing.T) {
	policyFailure := errors.New("policy unavailable")
	policy := &resourceReadPolicyStub{err: policyFailure}
	accounts := &scopedAccountReaderStub{listResult: &pagination.PaginationResult{}}
	groups := &scopedGroupReaderStub{listResult: &pagination.PaginationResult{}}
	service := NewResourceReadService(policy, accounts, groups)

	if _, _, err := service.ListAccounts(context.Background(), authz.Actor{}, AccountReadQuery{}); !errors.Is(err, policyFailure) {
		t.Fatalf("account policy error = %v", err)
	}
	if accounts.listCalls != 0 {
		t.Fatal("account reader was called after policy failure")
	}

	repositoryFailure := errors.New("repository unavailable")
	policy.err = nil
	groups.getErr = repositoryFailure
	if _, err := service.GetGroup(context.Background(), authz.Actor{}, 7); !errors.Is(err, repositoryFailure) {
		t.Fatalf("group repository error = %v", err)
	}
}

func TestResourceReadServiceRejectsInvalidInputsBeforePolicyOrRepository(t *testing.T) {
	policy := &resourceReadPolicyStub{}
	accounts := &scopedAccountReaderStub{listResult: &pagination.PaginationResult{}}
	groups := &scopedGroupReaderStub{listResult: &pagination.PaginationResult{}}
	service := NewResourceReadService(policy, accounts, groups)

	invalidCalls := []func() error{
		func() error {
			_, err := service.GetAccount(context.Background(), authz.Actor{}, 0)
			return err
		},
		func() error {
			_, err := service.GetGroup(context.Background(), authz.Actor{}, -1)
			return err
		},
		func() error {
			_, _, err := service.ListAccounts(context.Background(), authz.Actor{}, AccountReadQuery{Search: strings.Repeat("x", 101)})
			return err
		},
		func() error {
			_, _, err := service.ListGroups(context.Background(), authz.Actor{}, GroupReadQuery{Search: "private\x00group"})
			return err
		},
		func() error {
			_, _, err := service.ListAccounts(context.Background(), authz.Actor{}, AccountReadQuery{Pagination: pagination.PaginationParams{SortBy: "credentials"}})
			return err
		},
		func() error {
			_, _, err := service.ListGroups(context.Background(), authz.Actor{}, GroupReadQuery{Pagination: pagination.PaginationParams{SortBy: "account_count"}})
			return err
		},
		func() error {
			_, _, err := service.ListAccounts(context.Background(), authz.Actor{}, AccountReadQuery{
				Pagination: pagination.PaginationParams{Page: int(^uint(0) >> 1), PageSize: 2},
			})
			return err
		},
	}

	for index, call := range invalidCalls {
		if err := call(); err == nil || infraerrors.Code(err) != 400 {
			t.Fatalf("invalid call %d error = %v", index, err)
		}
	}
	if len(policy.calls) != 0 || accounts.listCalls != 0 || accounts.getCalls != 0 || groups.listCalls != 0 || groups.getCalls != 0 {
		t.Fatalf("invalid input reached dependencies: policy=%d accounts=%d/%d groups=%d/%d", len(policy.calls), accounts.listCalls, accounts.getCalls, groups.listCalls, groups.getCalls)
	}
}

func TestResourceReadServiceNilDependenciesFailClosed(t *testing.T) {
	policy := &resourceReadPolicyStub{}
	accounts := &scopedAccountReaderStub{listResult: &pagination.PaginationResult{}}
	groups := &scopedGroupReaderStub{listResult: &pagination.PaginationResult{}}

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "nil receiver", run: func() error {
			var service *ResourceReadService
			_, _, err := service.ListAccounts(context.Background(), authz.Actor{}, AccountReadQuery{})
			return err
		}},
		{name: "nil policy", run: func() error {
			_, _, err := NewResourceReadService(nil, accounts, groups).ListGroups(context.Background(), authz.Actor{}, GroupReadQuery{})
			return err
		}},
		{name: "nil account reader", run: func() error {
			_, err := NewResourceReadService(policy, nil, groups).GetAccount(context.Background(), authz.Actor{}, 1)
			return err
		}},
		{name: "nil group reader", run: func() error {
			_, err := NewResourceReadService(policy, accounts, nil).GetGroup(context.Background(), authz.Actor{}, 1)
			return err
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); !errors.Is(err, ErrResourceReadUnavailable) || infraerrors.Code(err) != 503 {
				t.Fatalf("error = %v, want resource read unavailable", err)
			}
		})
	}
}

func TestResourceReadServiceRejectsMalformedRepositoryResults(t *testing.T) {
	policy := &resourceReadPolicyStub{}
	accounts := &scopedAccountReaderStub{getItem: &AccountListItem{ID: 9}}
	groups := &scopedGroupReaderStub{listResult: nil}
	service := NewResourceReadService(policy, accounts, groups)

	if _, err := service.GetAccount(context.Background(), authz.Actor{}, 8); !errors.Is(err, ErrResourceReadUnavailable) {
		t.Fatalf("mismatched account result error = %v", err)
	}
	if _, _, err := service.ListGroups(context.Background(), authz.Actor{}, GroupReadQuery{}); !errors.Is(err, ErrResourceReadUnavailable) {
		t.Fatalf("nil pagination error = %v", err)
	}
}

func TestResourceReadProjectionsExcludeSensitiveAndAggregateFields(t *testing.T) {
	assertFieldsAbsent(t, reflect.TypeOf(AccountListItem{}), []string{
		"Credentials", "CredentialsStatus", "Extra", "Proxy", "ProxyID", "ProxyFallbackOriginID",
		"ErrorMessage", "Schedulable", "Priority", "Concurrency", "RateMultiplier", "QuotaLimit",
		"QuotaUsed", "GroupIDs", "Groups", "AccountGroups", "ParentAccountID",
	})
	assertFieldsAbsent(t, reflect.TypeOf(GroupListItem{}), []string{
		"AccountCount", "ActiveAccountCount", "RateLimitedAccountCount", "AccountGroups",
		"RateMultiplier", "ProfitControlEnabled", "ModelPricing", "ModelRouting", "FallbackGroupID",
	})
}

func assertFieldsAbsent(t *testing.T, typ reflect.Type, forbidden []string) {
	t.Helper()
	for _, field := range forbidden {
		if _, ok := typ.FieldByName(field); ok {
			t.Errorf("%s unexpectedly exposes %s", typ.Name(), field)
		}
	}
}
