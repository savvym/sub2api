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

type selfServiceGroupHandlerRepositoryStub struct {
	txCalls     int
	createCalls int
	createInput service.SelfServiceGroupCreateRecord
	events      []service.ResourceAuthorizationEventRecord
	createdAt   time.Time
}

func (r *selfServiceGroupHandlerRepositoryStub) WithSerializableTx(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	r.txCalls++
	return fn(ctx)
}

func (r *selfServiceGroupHandlerRepositoryStub) LockActorAuthorization(context.Context, int64) error {
	return errors.New("unexpected actor lock")
}

func (r *selfServiceGroupHandlerRepositoryStub) LockGroup(
	context.Context,
	int64,
) (service.SelfServiceGroupState, error) {
	return service.SelfServiceGroupState{}, errors.New("unexpected group lock")
}

func (r *selfServiceGroupHandlerRepositoryStub) CreateGroup(
	_ context.Context,
	input service.SelfServiceGroupCreateRecord,
) (service.SelfServiceGroupState, error) {
	r.createCalls++
	r.createInput = input
	ownerID := input.OwnerUserID
	creatorID := input.CreatorUserID
	return service.SelfServiceGroupState{
		GroupListItem: service.GroupListItem{
			ID: 81, Name: input.Name, Description: input.Description,
			Platform: input.Platform, Status: service.StatusActive,
			OwnerUserID: &ownerID, CreatedAt: r.createdAt, UpdatedAt: r.createdAt,
		},
		CreatedByUserID:   &creatorID,
		AccessVersion:     1,
		AuthorizationMode: "legacy",
		IsExclusive:       true,
	}, nil
}

func (r *selfServiceGroupHandlerRepositoryStub) UpdateGroup(
	context.Context,
	int64,
	int64,
	int64,
	string,
	string,
) (service.SelfServiceGroupState, error) {
	return service.SelfServiceGroupState{}, errors.New("unexpected update")
}

func (r *selfServiceGroupHandlerRepositoryStub) DeleteGroup(
	context.Context,
	int64,
	int64,
	int64,
) (service.SelfServiceGroupState, error) {
	return service.SelfServiceGroupState{}, errors.New("unexpected delete")
}

func (r *selfServiceGroupHandlerRepositoryStub) AppendAuthorizationEvent(
	_ context.Context,
	event service.ResourceAuthorizationEventRecord,
) error {
	r.events = append(r.events, event)
	return nil
}

type selfServiceGroupHandlerCapacityStub struct {
	calls int
}

func (s *selfServiceGroupHandlerCapacityStub) RequireCreateCapacity(
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

type selfServiceGroupHandlerActorStore struct {
	snapshot authz.SubjectSnapshot
}

func (s selfServiceGroupHandlerActorStore) LoadSubjectSnapshot(
	context.Context,
	authz.SubjectRef,
) (authz.SubjectSnapshot, error) {
	return s.snapshot, nil
}

func (s selfServiceGroupHandlerActorStore) LoadServicePrincipalSubjectSnapshotByCode(
	context.Context,
	string,
) (authz.SubjectSnapshot, error) {
	return authz.SubjectSnapshot{}, errors.New("unexpected service principal lookup")
}

func TestSelfServiceGroupCreateRejectsNonExactJSONBodies(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "administrator pricing field",
			body: `{"name":"personal","platform_id":"openai","rate_multiplier":0.1}`,
		},
		{
			name: "administrator routing field",
			body: `{"name":"personal","platform_id":"openai","fallback_group_id":1}`,
		},
		{
			name: "missing platform",
			body: `{"name":"personal"}`,
		},
		{
			name: "null required field",
			body: `{"name":null,"platform_id":"openai"}`,
		},
		{
			name: "null optional field",
			body: `{"name":"personal","description":null,"platform_id":"openai"}`,
		},
		{
			name: "trailing json",
			body: `{"name":"personal","platform_id":"openai"}{}`,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			handler, actor, repo, capacity := newSelfServiceGroupHandlerTestHarness(t, 51)
			recorder, ctx := selfServiceGroupHandlerContext(
				t,
				http.MethodPost,
				"/api/v1/groups",
				testCase.body,
				actor,
				51,
			)

			handler.Create(ctx)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			payload := decodeSelfServiceGroupHandlerResponse(t, recorder)
			require.Equal(t, "INVALID_REQUEST", payload["reason"])
			require.Zero(t, repo.txCalls)
			require.Zero(t, repo.createCalls)
			require.Zero(t, capacity.calls)
		})
	}
}

func TestSelfServiceGroupUpdateRejectsPlatformPolicyAndNullFields(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"name":null}`,
		`{"description":null}`,
		`{"platform_id":"gemini"}`,
		`{"subscription_type":"standard"}`,
		`{"name":"renamed","model_routing_enabled":true}`,
	} {
		handler, actor, repo, capacity := newSelfServiceGroupHandlerTestHarness(t, 52)
		recorder, ctx := selfServiceGroupHandlerContext(
			t, http.MethodPatch, "/api/v1/groups/81", body, actor, 52,
		)
		ctx.Params = gin.Params{{Key: "id", Value: "81"}}

		handler.Update(ctx)

		require.Equal(t, http.StatusBadRequest, recorder.Code, body)
		payload := decodeSelfServiceGroupHandlerResponse(t, recorder)
		require.Equal(t, "INVALID_REQUEST", payload["reason"], body)
		require.Zero(t, repo.txCalls)
		require.Zero(t, repo.createCalls)
		require.Zero(t, capacity.calls)
	}
}

func TestSelfServiceGroupCreateRejectsActorAndAuthSubjectMismatch(t *testing.T) {
	handler, actor, repo, capacity := newSelfServiceGroupHandlerTestHarness(t, 53)
	recorder, ctx := selfServiceGroupHandlerContext(
		t,
		http.MethodPost,
		"/api/v1/groups",
		`{"name":"personal","platform_id":"openai"}`,
		actor,
		54,
	)

	handler.Create(ctx)

	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	payload := decodeSelfServiceGroupHandlerResponse(t, recorder)
	require.Equal(t, "SELF_SERVICE_GROUP_ACTOR_REQUIRED", payload["reason"])
	require.Zero(t, repo.txCalls)
	require.Zero(t, repo.createCalls)
	require.Zero(t, capacity.calls)
}

func TestSelfServiceGroupCreateResponseUsesStrictPublicProjection(t *testing.T) {
	handler, actor, repo, capacity := newSelfServiceGroupHandlerTestHarness(t, 55)
	recorder, ctx := selfServiceGroupHandlerContext(
		t,
		http.MethodPost,
		"/api/v1/groups",
		`{"name":"personal","description":"private group","platform_id":"openai"}`,
		actor,
		55,
	)

	handler.Create(ctx)

	require.Equal(t, http.StatusCreated, recorder.Code)
	require.Equal(t, 1, repo.txCalls)
	require.Equal(t, 1, repo.createCalls)
	require.Equal(t, 1, capacity.calls)
	require.Len(t, repo.events, 1)
	require.Equal(t, "private group", repo.createInput.Description)

	payload := decodeSelfServiceGroupHandlerResponse(t, recorder)
	data, ok := payload["data"].(map[string]any)
	require.True(t, ok)
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	require.Equal(t, []string{
		"created_at",
		"description",
		"id",
		"name",
		"owned_by_me",
		"platform",
		"public_access_level",
		"status",
		"updated_at",
	}, keys)
	require.Equal(t, true, data["owned_by_me"])
	require.Nil(t, data["public_access_level"])

	responseBody := recorder.Body.String()
	for _, forbidden := range []string{
		"owner_user_id", "created_by_user_id", "access_version", "authorization_mode",
		"rate_multiplier", "subscription_type", "daily_limit_usd", "fallback_group_id",
		"model_routing", "model_pricing", "profit_control", "account_count", "accounts",
	} {
		require.NotContains(t, responseBody, forbidden)
	}
}

func TestParseSelfServiceGroupQueryRejectsUnknownRepeatedAndAccountCountSort(t *testing.T) {
	for _, values := range []url.Values{
		{"owner_user_id": {"1"}},
		{"page": {"1", "2"}},
		{"page_size": {"1001"}},
	} {
		_, err := parseSelfServiceGroupQuery(values)
		require.ErrorIs(t, err, service.ErrInvalidResourceReadQuery)
	}

	query, err := parseSelfServiceGroupQuery(url.Values{"sort_by": {"account_count"}})
	require.NoError(t, err)
	_, err = query.Normalize()
	require.ErrorIs(t, err, service.ErrInvalidResourceReadQuery)
}

func TestParseSelfServiceGroupQueryAcceptsClientTimezoneParameter(t *testing.T) {
	query, err := parseSelfServiceGroupQuery(url.Values{
		"page": {"2"}, "timezone": {"Asia/Shanghai"},
	})
	require.NoError(t, err)
	require.Equal(t, 2, query.Pagination.Page)
}

func newSelfServiceGroupHandlerTestHarness(
	t testing.TB,
	userID int64,
) (*SelfServiceGroupHandler, authz.Actor, *selfServiceGroupHandlerRepositoryStub, *selfServiceGroupHandlerCapacityStub) {
	t.Helper()
	actor := selfServiceGroupHandlerActor(t, userID)
	repo := &selfServiceGroupHandlerRepositoryStub{
		createdAt: time.Date(2026, 9, 2, 5, 6, 7, 0, time.UTC),
	}
	capacity := &selfServiceGroupHandlerCapacityStub{}
	catalog, err := service.NewStaticSelfServiceGroupCatalog([]service.SelfServiceGroupPlatform{{
		ID: "openai", Name: "OpenAI", Platform: service.PlatformOpenAI,
	}})
	require.NoError(t, err)
	groupService := service.NewSelfServiceGroupService(repo, nil, nil, capacity, nil, catalog)
	return NewSelfServiceGroupHandler(groupService), actor, repo, capacity
}

func selfServiceGroupHandlerActor(t testing.TB, userID int64) authz.Actor {
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
		Capabilities:  []authz.Capability{authz.CapabilityGroupCreate},
		Configuration: configuration,
	})
	require.NoError(t, err)
	actor, err := authz.NewActorResolver(selfServiceGroupHandlerActorStore{snapshot: snapshot}).ResolveUser(
		context.Background(), userID, authz.AuthMethodJWT,
	)
	require.NoError(t, err)
	return actor
}

func selfServiceGroupHandlerContext(
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

func decodeSelfServiceGroupHandlerResponse(
	t testing.TB,
	recorder *httptest.ResponseRecorder,
) map[string]any {
	t.Helper()
	var payload map[string]any
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &payload))
	return payload
}
