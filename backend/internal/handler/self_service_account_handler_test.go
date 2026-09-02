package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type selfServiceAccountHandlerRepositoryStub struct {
	txCalls     int
	createCalls int
	createInput service.SelfServiceAccountCreateRecord
	events      []service.ResourceAuthorizationEventRecord
	createdAt   time.Time
}

func (r *selfServiceAccountHandlerRepositoryStub) WithSerializableTx(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	r.txCalls++
	return fn(ctx)
}

func (r *selfServiceAccountHandlerRepositoryStub) LockActorAuthorization(context.Context, int64) error {
	return errors.New("unexpected actor lock")
}

func (r *selfServiceAccountHandlerRepositoryStub) LockAccount(
	context.Context,
	int64,
) (service.SelfServiceAccountState, error) {
	return service.SelfServiceAccountState{}, errors.New("unexpected account lock")
}

func (r *selfServiceAccountHandlerRepositoryStub) CreateAccount(
	_ context.Context,
	input service.SelfServiceAccountCreateRecord,
) (service.SelfServiceAccountState, error) {
	r.createCalls++
	r.createInput = input
	ownerID := input.OwnerUserID
	return service.SelfServiceAccountState{
		AccountListItem: service.AccountListItem{
			ID:                   81,
			Name:                 input.Name,
			Platform:             input.Platform,
			Type:                 input.AccountType,
			Status:               service.StatusActive,
			CredentialConfigured: true,
			OwnerUserID:          &ownerID,
			CreatedAt:            r.createdAt,
			UpdatedAt:            r.createdAt,
		},
		AccessVersion: 1,
	}, nil
}

func (r *selfServiceAccountHandlerRepositoryStub) RenameAccount(
	context.Context,
	int64,
	int64,
	int64,
	string,
) (service.SelfServiceAccountState, error) {
	return service.SelfServiceAccountState{}, errors.New("unexpected rename")
}

func (r *selfServiceAccountHandlerRepositoryStub) DeleteAccount(
	context.Context,
	int64,
	int64,
	int64,
) (service.SelfServiceAccountState, error) {
	return service.SelfServiceAccountState{}, errors.New("unexpected delete")
}

func (r *selfServiceAccountHandlerRepositoryStub) AppendAuthorizationEvent(
	_ context.Context,
	event service.ResourceAuthorizationEventRecord,
) error {
	r.events = append(r.events, event)
	return nil
}

type selfServiceAccountHandlerCapacityStub struct {
	calls int
}

func (s *selfServiceAccountHandlerCapacityStub) RequireCreateCapacity(
	_ context.Context,
	actor authz.Actor,
	resourceType authz.ResourceType,
) (service.HostingCapacity, error) {
	s.calls++
	userID, _ := actor.UserID()
	return service.HostingCapacity{
		UserID: userID, ResourceType: resourceType, Limit: 1, Remaining: 1, Version: 1,
	}, nil
}

type selfServiceAccountHandlerActorStore struct {
	snapshot authz.SubjectSnapshot
}

func (s selfServiceAccountHandlerActorStore) LoadSubjectSnapshot(
	context.Context,
	authz.SubjectRef,
) (authz.SubjectSnapshot, error) {
	return s.snapshot, nil
}

func (s selfServiceAccountHandlerActorStore) LoadServicePrincipalSubjectSnapshotByCode(
	context.Context,
	string,
) (authz.SubjectSnapshot, error) {
	return authz.SubjectSnapshot{}, errors.New("unexpected service principal lookup")
}

func TestSelfServiceAccountCreateRejectsNonExactJSONBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "unknown field",
			body: `{"name":"personal","product_id":"openai-api-key","api_key":"sk-test","credentials":{}}`,
		},
		{
			name: "missing field",
			body: `{"name":"personal","product_id":"openai-api-key"}`,
		},
		{
			name: "null document",
			body: `null`,
		},
		{
			name: "null field",
			body: `{"name":"personal","product_id":"openai-api-key","api_key":null}`,
		},
		{
			name: "trailing json",
			body: `{"name":"personal","product_id":"openai-api-key","api_key":"sk-test"}{}`,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			handler, actor, repo, capacity := newSelfServiceAccountHandlerTestHarness(t, 51)
			recorder, ctx := selfServiceAccountHandlerContext(
				t,
				http.MethodPost,
				"/api/v1/accounts",
				testCase.body,
				actor,
				51,
			)

			handler.Create(ctx)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			payload := decodeSelfServiceAccountHandlerResponse(t, recorder)
			require.Equal(t, "INVALID_REQUEST", payload["reason"])
			require.Zero(t, repo.txCalls)
			require.Zero(t, repo.createCalls)
			require.Zero(t, capacity.calls)
		})
	}
}

func TestSelfServiceAccountCreateRejectsActorAndAuthSubjectMismatch(t *testing.T) {
	handler, actor, repo, capacity := newSelfServiceAccountHandlerTestHarness(t, 52)
	recorder, ctx := selfServiceAccountHandlerContext(
		t,
		http.MethodPost,
		"/api/v1/accounts",
		`{"name":"personal","product_id":"openai-api-key","api_key":"sk-test"}`,
		actor,
		53,
	)

	handler.Create(ctx)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	payload := decodeSelfServiceAccountHandlerResponse(t, recorder)
	require.Equal(t, "SELF_SERVICE_ACCOUNT_ACTOR_REQUIRED", payload["reason"])
	require.Zero(t, repo.txCalls)
	require.Zero(t, repo.createCalls)
	require.Zero(t, capacity.calls)
}

func TestSelfServiceAccountCreateResponseUsesStrictPublicProjection(t *testing.T) {
	const apiKey = "sk-never-return-this"
	handler, actor, repo, capacity := newSelfServiceAccountHandlerTestHarness(t, 54)
	recorder, ctx := selfServiceAccountHandlerContext(
		t,
		http.MethodPost,
		"/api/v1/accounts",
		`{"name":"personal","product_id":"openai-api-key","api_key":"`+apiKey+`"}`,
		actor,
		54,
	)

	handler.Create(ctx)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, 1, repo.txCalls)
	require.Equal(t, 1, repo.createCalls)
	require.Equal(t, 1, capacity.calls)
	require.Len(t, repo.events, 1)
	require.Equal(t, apiKey, repo.createInput.APIKey)

	payload := decodeSelfServiceAccountHandlerResponse(t, recorder)
	data, ok := payload["data"].(map[string]any)
	require.True(t, ok)
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	require.Equal(t, []string{
		"created_at",
		"credential_configured",
		"id",
		"name",
		"owned_by_me",
		"platform",
		"public_access_level",
		"status",
		"type",
		"updated_at",
	}, keys)
	require.Equal(t, true, data["credential_configured"])
	require.Equal(t, true, data["owned_by_me"])
	require.Nil(t, data["public_access_level"])

	responseBody := recorder.Body.String()
	require.NotContains(t, responseBody, apiKey)
	for _, forbidden := range []string{
		"api_key", "credentials", "owner_user_id", "created_by_user_id", "extra",
		"proxy", "schedulable", "access_version", "account_groups",
	} {
		require.NotContains(t, responseBody, forbidden)
	}
}

func TestParseSelfServiceAccountQueryRejectsUnknownAndRepeatedParameters(t *testing.T) {
	for _, values := range []url.Values{
		{"owner_user_id": {"1"}},
		{"page": {"1", "2"}},
		{"page_size": {"1001"}},
	} {
		_, err := parseSelfServiceAccountQuery(values)
		require.ErrorIs(t, err, service.ErrInvalidResourceReadQuery)
	}
}

func TestParseSelfServiceAccountQueryAcceptsClientTimezoneParameter(t *testing.T) {
	query, err := parseSelfServiceAccountQuery(url.Values{
		"page":     {"2"},
		"timezone": {"Asia/Shanghai"},
	})
	require.NoError(t, err)
	require.Equal(t, 2, query.Pagination.Page)
}

func newSelfServiceAccountHandlerTestHarness(
	t testing.TB,
	userID int64,
) (*SelfServiceAccountHandler, authz.Actor, *selfServiceAccountHandlerRepositoryStub, *selfServiceAccountHandlerCapacityStub) {
	t.Helper()
	actor := selfServiceAccountHandlerActor(t, userID)
	repo := &selfServiceAccountHandlerRepositoryStub{
		createdAt: time.Date(2026, 9, 2, 5, 6, 7, 0, time.UTC),
	}
	capacity := &selfServiceAccountHandlerCapacityStub{}
	catalog, err := service.NewStaticSelfServiceAccountCatalog([]service.SelfServiceAccountProduct{{
		ID: "openai-api-key", Name: "OpenAI API Key", Platform: service.PlatformOpenAI,
		AccountType: service.AccountTypeAPIKey,
	}})
	require.NoError(t, err)
	accountService := service.NewSelfServiceAccountService(repo, nil, nil, capacity, nil, catalog)
	return NewSelfServiceAccountHandler(accountService), actor, repo, capacity
}

func selfServiceAccountHandlerActor(t testing.TB, userID int64) authz.Actor {
	t.Helper()
	subject, err := authz.NewSubjectRef(authz.SubjectKindUser, userID)
	require.NoError(t, err)
	configuration, err := authz.NewPolicyConfiguration(authz.PolicyConfigurationInput{
		RoleAuthorizationMode:        authz.RoleAuthorizationModeRBAC,
		ResourceAccessControlEnabled: true,
		SelfServiceHostingEnabled:    true,
	})
	require.NoError(t, err)
	snapshot, err := authz.NewSubjectSnapshot(authz.SubjectSnapshotInput{
		Subject: subject, Exists: true, Active: true, AuthzVersion: 1,
		Capabilities:  []authz.Capability{authz.CapabilityAccountCreate},
		Configuration: configuration,
	})
	require.NoError(t, err)
	actor, err := authz.NewActorResolver(selfServiceAccountHandlerActorStore{snapshot: snapshot}).ResolveUser(
		context.Background(),
		userID,
		authz.AuthMethodJWT,
	)
	require.NoError(t, err)
	return actor
}

func selfServiceAccountHandlerContext(
	t testing.TB,
	method string,
	path string,
	body string,
	actor authz.Actor,
	authSubjectUserID int64,
) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(authz.ContextWithActor(request.Context(), actor))
	ctx.Request = request
	ctx.Set(string(servermiddleware.ContextKeyUser), servermiddleware.AuthSubject{UserID: authSubjectUserID})
	return recorder, ctx
}

func decodeSelfServiceAccountHandlerResponse(
	t testing.TB,
	recorder *httptest.ResponseRecorder,
) map[string]any {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	return payload
}
