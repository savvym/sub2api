package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/authz"
)

func TestAuthzPolicyStoreLoadsUserSubjectSnapshotWithOneStatement(t *testing.T) {
	store, mock := newAuthzPolicyStoreSQLMock(t)
	subject := mustAuthzSubjectRef(t, authz.SubjectKindUser, 41)
	payload := mustAuthzPolicyJSON(t, rawAuthzPolicyDocument{
		Subject: rawAuthzSubject{
			Exists:             true,
			Active:             true,
			AuthzVersion:       8,
			CurrentLegacyAdmin: true,
			Roles: []rawAuthzRole{
				{ID: 3, Version: 2},
				{ID: 9, Version: 5},
			},
			Capabilities: []string{
				string(authz.CapabilityResourceShare),
				string(authz.CapabilityAccountCreate),
			},
		},
		Configuration: fullyEnabledRawAuthzConfiguration(),
	})
	mock.ExpectQuery(buildSubjectSnapshotSQL(authz.SubjectKindUser)).
		WithArgs(subject.ID()).
		WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(payload)).
		RowsWillBeClosed()

	snapshot, err := store.LoadSubjectSnapshot(context.Background(), subject)
	if err != nil {
		t.Fatalf("load user subject snapshot: %v", err)
	}
	if !snapshot.Valid() || snapshot.Subject() != subject || !snapshot.Exists() || !snapshot.Active() {
		t.Fatalf("unexpected user subject state: %+v", snapshot)
	}
	if snapshot.AuthzVersion() != 8 || !snapshot.CurrentLegacyAdmin() {
		t.Fatalf("unexpected user subject versions or legacy role: %+v", snapshot)
	}
	if got, want := snapshot.RoleVersions(), map[int64]int64{3: 2, 9: 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("role versions = %v, want %v", got, want)
	}
	if got, want := snapshot.Capabilities(), []authz.Capability{
		authz.CapabilityAccountCreate,
		authz.CapabilityResourceShare,
	}; !reflect.DeepEqual(got, want) {
		t.Fatalf("capabilities = %v, want %v", got, want)
	}
	assertFullyEnabledPolicyConfiguration(t, snapshot.Configuration())
	assertAuthzPolicyStoreExpectations(t, mock)
}

func TestAuthzPolicyStoreLoadsServicePrincipalSubjectSnapshotWithOneStatement(t *testing.T) {
	store, mock := newAuthzPolicyStoreSQLMock(t)
	subject := mustAuthzSubjectRef(t, authz.SubjectKindServicePrincipal, 52)
	payload := mustAuthzPolicyJSON(t, rawAuthzPolicyDocument{
		Subject: rawAuthzSubject{
			Exists:       true,
			Active:       true,
			AuthzVersion: 4,
			Roles:        []rawAuthzRole{{ID: 12, Version: 7}},
			Capabilities: []string{string(authz.CapabilityGroupCreate)},
		},
		Configuration: fullyEnabledRawAuthzConfiguration(),
	})
	mock.ExpectQuery(buildSubjectSnapshotSQL(authz.SubjectKindServicePrincipal)).
		WithArgs(subject.ID()).
		WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(payload)).
		RowsWillBeClosed()

	snapshot, err := store.LoadSubjectSnapshot(context.Background(), subject)
	if err != nil {
		t.Fatalf("load service principal subject snapshot: %v", err)
	}
	if !snapshot.Valid() || snapshot.Subject() != subject || !snapshot.Exists() || !snapshot.Active() {
		t.Fatalf("unexpected service principal subject state: %+v", snapshot)
	}
	if snapshot.AuthzVersion() != 4 || snapshot.CurrentLegacyAdmin() {
		t.Fatalf("unexpected service principal versions or legacy role: %+v", snapshot)
	}
	if got, want := snapshot.RoleVersions(), map[int64]int64{12: 7}; !reflect.DeepEqual(got, want) {
		t.Fatalf("role versions = %v, want %v", got, want)
	}
	if got, want := snapshot.Capabilities(), []authz.Capability{authz.CapabilityGroupCreate}; !reflect.DeepEqual(got, want) {
		t.Fatalf("capabilities = %v, want %v", got, want)
	}
	assertFullyEnabledPolicyConfiguration(t, snapshot.Configuration())
	assertAuthzPolicyStoreExpectations(t, mock)
}

func TestAuthzPolicyStoreBuildsAccountResourceSnapshotWithDirectAndRoleGrants(t *testing.T) {
	store, mock := newAuthzPolicyStoreSQLMock(t)
	subject := mustAuthzSubjectRef(t, authz.SubjectKindUser, 61)
	resource := mustAuthzResourceRef(t, authz.ResourceTypeAccount, 71)
	ownerID := int64(81)
	publicLevel := string(authz.AccessLevelViewer)
	roleID := int64(17)
	payload := mustAuthzPolicyJSON(t, rawAuthzPolicyDocument{
		Subject: rawAuthzSubject{
			Exists:       true,
			Active:       true,
			AuthzVersion: 9,
			Roles:        []rawAuthzRole{{ID: roleID, Version: 3}},
		},
		Configuration: fullyEnabledRawAuthzConfiguration(),
		Resource: &rawAuthzResource{
			Exists:            true,
			OwnerUserID:       &ownerID,
			PublicAccessLevel: &publicLevel,
			AccessVersion:     6,
		},
		UserGrants: []rawAuthzGrant{
			{ID: 101, AccessLevel: string(authz.AccessLevelConsumer)},
		},
		RoleGrants: []rawAuthzGrant{
			{ID: 102, RoleID: &roleID, AccessLevel: string(authz.AccessLevelMaintainer)},
		},
	})
	mock.ExpectQuery(buildResourceSnapshotSQL(authz.SubjectKindUser, authz.ResourceTypeAccount)).
		WithArgs(subject.ID(), resource.ID()).
		WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(payload)).
		RowsWillBeClosed()

	snapshot, err := store.LoadResourceAccessSnapshot(context.Background(), subject, resource)
	if err != nil {
		t.Fatalf("load account resource snapshot: %v", err)
	}
	if !snapshot.Valid() || snapshot.Resource() != resource || !snapshot.Exists() || snapshot.Deleted() {
		t.Fatalf("unexpected account resource state: %+v", snapshot)
	}
	if got, ok := snapshot.OwnerUserID(); !ok || got != ownerID {
		t.Fatalf("owner = (%d, %t), want (%d, true)", got, ok, ownerID)
	}
	if got, ok := snapshot.PublicAccessLevel(); !ok || got != authz.AccessLevelViewer {
		t.Fatalf("public access = (%q, %t), want (%q, true)", got, ok, authz.AccessLevelViewer)
	}
	if snapshot.AccessVersion() != 6 {
		t.Fatalf("access version = %d, want 6", snapshot.AccessVersion())
	}
	userGrants := snapshot.UserGrants()
	if len(userGrants) != 1 || userGrants[0].Source() != authz.MatchSourceUserGrant ||
		userGrants[0].GrantID() != 101 || userGrants[0].AccessLevel() != authz.AccessLevelConsumer {
		t.Fatalf("unexpected direct grants: %+v", userGrants)
	}
	roleGrants := snapshot.RoleGrants()
	if len(roleGrants) != 1 || roleGrants[0].Source() != authz.MatchSourceRoleGrant ||
		roleGrants[0].GrantID() != 102 || roleGrants[0].AccessLevel() != authz.AccessLevelMaintainer {
		t.Fatalf("unexpected role grants: %+v", roleGrants)
	}
	if got, ok := roleGrants[0].RoleID(); !ok || got != roleID {
		t.Fatalf("role grant role = (%d, %t), want (%d, true)", got, ok, roleID)
	}
	assertAuthzPolicyStoreExpectations(t, mock)
}

func TestAuthzPolicyStoreBuildsServicePrincipalGroupSnapshotWithRoleGrantOnly(t *testing.T) {
	store, mock := newAuthzPolicyStoreSQLMock(t)
	subject := mustAuthzSubjectRef(t, authz.SubjectKindServicePrincipal, 62)
	resource := mustAuthzResourceRef(t, authz.ResourceTypeGroup, 72)
	ownerID := int64(82)
	roleID := int64(18)
	groupMode := string(authz.GroupAuthorizationModeACL)
	payload := mustAuthzPolicyJSON(t, rawAuthzPolicyDocument{
		Subject: rawAuthzSubject{
			Exists:       true,
			Active:       true,
			AuthzVersion: 10,
			Roles:        []rawAuthzRole{{ID: roleID, Version: 4}},
		},
		Configuration: fullyEnabledRawAuthzConfiguration(),
		Resource: &rawAuthzResource{
			Exists:            true,
			OwnerUserID:       &ownerID,
			AuthorizationMode: &groupMode,
			AccessVersion:     7,
		},
		RoleGrants: []rawAuthzGrant{
			{ID: 103, RoleID: &roleID, AccessLevel: string(authz.AccessLevelConsumer)},
		},
	})
	mock.ExpectQuery(buildResourceSnapshotSQL(authz.SubjectKindServicePrincipal, authz.ResourceTypeGroup)).
		WithArgs(subject.ID(), resource.ID()).
		WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(payload)).
		RowsWillBeClosed()

	snapshot, err := store.LoadResourceAccessSnapshot(context.Background(), subject, resource)
	if err != nil {
		t.Fatalf("load service principal group snapshot: %v", err)
	}
	if !snapshot.Valid() || snapshot.Resource() != resource || !snapshot.Exists() || snapshot.Deleted() {
		t.Fatalf("unexpected service principal resource state: %+v", snapshot)
	}
	if len(snapshot.UserGrants()) != 0 {
		t.Fatalf("service principal received direct user grants: %+v", snapshot.UserGrants())
	}
	roleGrants := snapshot.RoleGrants()
	if len(roleGrants) != 1 || roleGrants[0].GrantID() != 103 || roleGrants[0].AccessLevel() != authz.AccessLevelConsumer {
		t.Fatalf("unexpected service principal role grants: %+v", roleGrants)
	}
	if got, ok := roleGrants[0].RoleID(); !ok || got != roleID {
		t.Fatalf("role grant role = (%d, %t), want (%d, true)", got, ok, roleID)
	}
	assertAuthzPolicyStoreExpectations(t, mock)
}

func TestAuthzPolicyStoreRejectsInvalidSubjectDocuments(t *testing.T) {
	subject := mustAuthzSubjectRef(t, authz.SubjectKindUser, 91)
	validDocument := rawAuthzPolicyDocument{
		Subject: rawAuthzSubject{
			Exists:       true,
			Active:       true,
			AuthzVersion: 1,
		},
		Configuration: fullyEnabledRawAuthzConfiguration(),
	}
	unknownCapabilityDocument := validDocument
	unknownCapabilityDocument.Subject.Capabilities = []string{"platform.root"}

	tests := []struct {
		name    string
		payload string
	}{
		{name: "empty", payload: ""},
		{name: "malformed JSON", payload: `{"subject":`},
		{name: "empty object", payload: `{}`},
		{name: "null document", payload: `null`},
		{name: "null subject", payload: `{"subject":null,"configuration":{}}`},
		{name: "null configuration", payload: `{"subject":{},"configuration":null}`},
		{name: "unknown capability", payload: mustAuthzPolicyJSON(t, unknownCapabilityDocument)},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store, mock := newAuthzPolicyStoreSQLMock(t)
			mock.ExpectQuery(buildSubjectSnapshotSQL(authz.SubjectKindUser)).
				WithArgs(subject.ID()).
				WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(testCase.payload)).
				RowsWillBeClosed()

			snapshot, err := store.LoadSubjectSnapshot(context.Background(), subject)
			if !errors.Is(err, authz.ErrInvalidPolicySnapshot) || snapshot.Valid() {
				t.Fatalf("invalid subject document accepted: snapshot=%+v err=%v", snapshot, err)
			}
			assertAuthzPolicyStoreExpectations(t, mock)
		})
	}
}

func TestDecodeAuthzPolicyDocumentRejectsIncompleteNestedObjects(t *testing.T) {
	t.Parallel()

	subjectPayload := mustAuthzPolicyJSON(t, validRawAuthzSubjectDocument(authz.SubjectKindUser))
	resourcePayload := mustAuthzPolicyJSON(t, validRawAuthzResourceDocument(authz.SubjectKindUser, authz.ResourceTypeAccount))
	tests := []struct {
		name            string
		payload         string
		requireResource bool
		mutate          func(map[string]any)
	}{
		{
			name:    "empty subject",
			payload: subjectPayload,
			mutate: func(document map[string]any) {
				document["subject"] = map[string]any{}
			},
		},
		{
			name:    "null required subject field",
			payload: subjectPayload,
			mutate: func(document map[string]any) {
				document["subject"].(map[string]any)["active"] = nil
			},
		},
		{
			name:    "empty configuration",
			payload: subjectPayload,
			mutate: func(document map[string]any) {
				document["configuration"] = map[string]any{}
			},
		},
		{
			name:            "empty resource",
			payload:         resourcePayload,
			requireResource: true,
			mutate: func(document map[string]any) {
				document["resource"] = map[string]any{}
			},
		},
		{
			name:            "null required resource field",
			payload:         resourcePayload,
			requireResource: true,
			mutate: func(document map[string]any) {
				document["resource"].(map[string]any)["exists"] = nil
			},
		},
		{
			name:            "missing grant collection",
			payload:         resourcePayload,
			requireResource: true,
			mutate: func(document map[string]any) {
				delete(document, "user_grants")
			},
		},
	}

	for _, testCase := range tests {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			payload := mutateAuthzPolicyJSON(t, testCase.payload, testCase.mutate)
			document, err := decodeAuthzPolicyDocument(payload, testCase.requireResource)
			if !errors.Is(err, authz.ErrInvalidPolicySnapshot) || document.Subject.Exists {
				t.Fatalf("incomplete document accepted: document=%+v err=%v", document, err)
			}
		})
	}
}

func TestAuthzPolicyStoreRejectsInvalidResourceAccessLevels(t *testing.T) {
	subject := mustAuthzSubjectRef(t, authz.SubjectKindUser, 92)
	resource := mustAuthzResourceRef(t, authz.ResourceTypeAccount, 93)

	tests := []struct {
		name   string
		mutate func(*rawAuthzPolicyDocument)
	}{
		{
			name: "unknown public access level",
			mutate: func(document *rawAuthzPolicyDocument) {
				unknown := "root"
				document.Resource.PublicAccessLevel = &unknown
			},
		},
		{
			name: "known but forbidden public access level",
			mutate: func(document *rawAuthzPolicyDocument) {
				manager := string(authz.AccessLevelManager)
				document.Resource.PublicAccessLevel = &manager
			},
		},
		{
			name: "unknown direct grant access level",
			mutate: func(document *rawAuthzPolicyDocument) {
				document.UserGrants[0].AccessLevel = "root"
			},
		},
		{
			name: "unknown role grant access level",
			mutate: func(document *rawAuthzPolicyDocument) {
				document.RoleGrants[0].AccessLevel = "root"
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			document := validRawAuthzResourceDocument(authz.SubjectKindUser, authz.ResourceTypeAccount)
			testCase.mutate(&document)
			store, mock := newAuthzPolicyStoreSQLMock(t)
			mock.ExpectQuery(buildResourceSnapshotSQL(authz.SubjectKindUser, authz.ResourceTypeAccount)).
				WithArgs(subject.ID(), resource.ID()).
				WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(mustAuthzPolicyJSON(t, document))).
				RowsWillBeClosed()

			snapshot, err := store.LoadResourceAccessSnapshot(context.Background(), subject, resource)
			if !errors.Is(err, authz.ErrInvalidPolicySnapshot) || snapshot.Valid() {
				t.Fatalf("invalid resource document accepted: snapshot=%+v err=%v", snapshot, err)
			}
			assertAuthzPolicyStoreExpectations(t, mock)
		})
	}
}

func TestAuthzPolicyStoreRejectsServicePrincipalUserState(t *testing.T) {
	tests := []struct {
		name     string
		resource bool
	}{
		{name: "legacy administrator"},
		{name: "direct user grant", resource: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			subject := mustAuthzSubjectRef(t, authz.SubjectKindServicePrincipal, 94)
			store, mock := newAuthzPolicyStoreSQLMock(t)
			if !testCase.resource {
				document := validRawAuthzSubjectDocument(authz.SubjectKindServicePrincipal)
				document.Subject.CurrentLegacyAdmin = true
				mock.ExpectQuery(buildSubjectSnapshotSQL(authz.SubjectKindServicePrincipal)).
					WithArgs(subject.ID()).
					WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(mustAuthzPolicyJSON(t, document))).
					RowsWillBeClosed()
				snapshot, err := store.LoadSubjectSnapshot(context.Background(), subject)
				if !errors.Is(err, authz.ErrInvalidPolicySnapshot) || snapshot.Valid() {
					t.Fatalf("service principal legacy administrator accepted: snapshot=%+v err=%v", snapshot, err)
				}
			} else {
				resource := mustAuthzResourceRef(t, authz.ResourceTypeGroup, 95)
				document := validRawAuthzResourceDocument(authz.SubjectKindServicePrincipal, authz.ResourceTypeGroup)
				document.UserGrants = []rawAuthzGrant{{ID: 33, AccessLevel: string(authz.AccessLevelViewer)}}
				mock.ExpectQuery(buildResourceSnapshotSQL(authz.SubjectKindServicePrincipal, authz.ResourceTypeGroup)).
					WithArgs(subject.ID(), resource.ID()).
					WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(mustAuthzPolicyJSON(t, document))).
					RowsWillBeClosed()
				snapshot, err := store.LoadResourceAccessSnapshot(context.Background(), subject, resource)
				if !errors.Is(err, authz.ErrInvalidPolicySnapshot) || snapshot.Valid() {
					t.Fatalf("service principal direct user grant accepted: snapshot=%+v err=%v", snapshot, err)
				}
			}
			assertAuthzPolicyStoreExpectations(t, mock)
		})
	}
}

func TestAuthzPolicyStoreRejectsNilQueryer(t *testing.T) {
	subject := mustAuthzSubjectRef(t, authz.SubjectKindUser, 111)
	resource := mustAuthzResourceRef(t, authz.ResourceTypeGroup, 112)
	stores := []struct {
		name  string
		store authz.PolicyStore
	}{
		{name: "nil test queryer", store: newAuthzPolicyStoreWithQueryer(nil)},
		{name: "nil production client", store: NewAuthzPolicyStore(nil)},
	}

	for _, testStore := range stores {
		t.Run(testStore.name+" subject", func(t *testing.T) {
			snapshot, err := testStore.store.LoadSubjectSnapshot(context.Background(), subject)
			if err == nil || snapshot.Valid() || !strings.Contains(err.Error(), "nil database client") {
				t.Fatalf("nil queryer subject result: snapshot=%+v err=%v", snapshot, err)
			}
		})
		t.Run(testStore.name+" resource", func(t *testing.T) {
			snapshot, err := testStore.store.LoadResourceAccessSnapshot(context.Background(), subject, resource)
			if err == nil || snapshot.Valid() || !strings.Contains(err.Error(), "nil database client") {
				t.Fatalf("nil queryer resource result: snapshot=%+v err=%v", snapshot, err)
			}
		})
	}
}

func TestAuthzPolicyStorePropagatesSQLAndRowErrors(t *testing.T) {
	subject := mustAuthzSubjectRef(t, authz.SubjectKindUser, 121)
	query := buildSubjectSnapshotSQL(authz.SubjectKindUser)
	validPayload := mustAuthzPolicyJSON(t, validRawAuthzSubjectDocument(authz.SubjectKindUser))

	tests := []struct {
		name       string
		rows       *sqlmock.Rows
		queryError error
		wantCause  error
		wantNoRows bool
	}{
		{
			name:       "query error",
			queryError: errors.New("query unavailable"),
		},
		{
			name:       "no rows",
			rows:       sqlmock.NewRows([]string{"document"}),
			wantNoRows: true,
		},
		{
			name:      "row error",
			rows:      sqlmock.NewRows([]string{"document"}).AddRow(validPayload).AddRow(validPayload),
			wantCause: errors.New("row unavailable"),
		},
		{
			name: "scan error",
			rows: sqlmock.NewRows([]string{"document", "unexpected"}).AddRow(validPayload, "extra"),
		},
		{
			name: "multiple documents",
			rows: sqlmock.NewRows([]string{"document"}).AddRow(validPayload).AddRow(validPayload),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store, mock := newAuthzPolicyStoreSQLMock(t)
			expectation := mock.ExpectQuery(query).WithArgs(subject.ID())
			switch {
			case testCase.queryError != nil:
				expectation.WillReturnError(testCase.queryError)
			case testCase.wantCause != nil:
				expectation.WillReturnRows(testCase.rows.RowError(1, testCase.wantCause)).RowsWillBeClosed()
			default:
				expectation.WillReturnRows(testCase.rows).RowsWillBeClosed()
			}

			snapshot, err := store.LoadSubjectSnapshot(context.Background(), subject)
			if err == nil || snapshot.Valid() {
				t.Fatalf("SQL failure accepted: snapshot=%+v err=%v", snapshot, err)
			}
			if testCase.queryError != nil && !errors.Is(err, testCase.queryError) {
				t.Fatalf("query cause not preserved: %v", err)
			}
			if testCase.wantCause != nil && !errors.Is(err, testCase.wantCause) {
				t.Fatalf("row cause not preserved: %v", err)
			}
			if testCase.wantNoRows && !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("no-rows cause not preserved: %v", err)
			}
			assertAuthzPolicyStoreExpectations(t, mock)
		})
	}
}

func TestAuthzPolicyStoreSQLUsesStrictDatabaseExpiryBoundary(t *testing.T) {
	strictExpiry := regexp.MustCompile(`expires_at\s*>\s*CURRENT_TIMESTAMP`)
	inclusiveExpiry := regexp.MustCompile(`expires_at\s*>=\s*CURRENT_TIMESTAMP`)

	subjectTests := []struct {
		name string
		kind authz.SubjectKind
	}{
		{name: "user", kind: authz.SubjectKindUser},
		{name: "service principal", kind: authz.SubjectKindServicePrincipal},
	}
	for _, testCase := range subjectTests {
		t.Run("subject "+testCase.name, func(t *testing.T) {
			query := buildSubjectSnapshotSQL(testCase.kind)
			assertStrictAuthzExpiryPredicates(t, query, strictExpiry, inclusiveExpiry, 1)
		})
	}

	resourceTests := []struct {
		name          string
		kind          authz.SubjectKind
		resourceType  authz.ResourceType
		wantPredicate int
	}{
		{name: "user account", kind: authz.SubjectKindUser, resourceType: authz.ResourceTypeAccount, wantPredicate: 3},
		{name: "user group", kind: authz.SubjectKindUser, resourceType: authz.ResourceTypeGroup, wantPredicate: 3},
		{name: "service principal account", kind: authz.SubjectKindServicePrincipal, resourceType: authz.ResourceTypeAccount, wantPredicate: 2},
		{name: "service principal group", kind: authz.SubjectKindServicePrincipal, resourceType: authz.ResourceTypeGroup, wantPredicate: 2},
	}
	for _, testCase := range resourceTests {
		t.Run("resource "+testCase.name, func(t *testing.T) {
			query := buildResourceSnapshotSQL(testCase.kind, testCase.resourceType)
			assertStrictAuthzExpiryPredicates(t, query, strictExpiry, inclusiveExpiry, testCase.wantPredicate)
			if testCase.kind == authz.SubjectKindUser {
				if got := strings.Count(query, "grantee_user_id = $1"); got != 1 {
					t.Fatalf("direct grants must be filtered once to the supplied user, got %d predicates:\n%s", got, query)
				}
			} else if strings.Contains(query, "grantee_user_id = $1") {
				t.Fatalf("service principal query must not load direct user grants:\n%s", query)
			}
		})
	}
}

func newAuthzPolicyStoreSQLMock(t *testing.T) (*authzPolicyStore, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatalf("create SQL mock: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	return newAuthzPolicyStoreWithQueryer(db), mock
}

func mustAuthzSubjectRef(t *testing.T, kind authz.SubjectKind, id int64) authz.SubjectRef {
	t.Helper()
	ref, err := authz.NewSubjectRef(kind, id)
	if err != nil {
		t.Fatalf("create subject reference: %v", err)
	}
	return ref
}

func mustAuthzResourceRef(t *testing.T, resourceType authz.ResourceType, id int64) authz.ResourceRef {
	t.Helper()
	ref, err := authz.NewResourceRef(resourceType, id)
	if err != nil {
		t.Fatalf("create resource reference: %v", err)
	}
	return ref
}

func mustAuthzPolicyJSON(t *testing.T, document rawAuthzPolicyDocument) string {
	t.Helper()
	if document.Subject.Roles == nil {
		document.Subject.Roles = []rawAuthzRole{}
	}
	if document.Subject.Capabilities == nil {
		document.Subject.Capabilities = []string{}
	}
	if document.Resource != nil {
		if document.UserGrants == nil {
			document.UserGrants = []rawAuthzGrant{}
		}
		if document.RoleGrants == nil {
			document.RoleGrants = []rawAuthzGrant{}
		}
	}
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode policy document: %v", err)
	}
	return string(payload)
}

func mutateAuthzPolicyJSON(t *testing.T, payload string, mutate func(map[string]any)) string {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(payload), &document); err != nil {
		t.Fatalf("decode policy JSON for mutation: %v", err)
	}
	mutate(document)
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode mutated policy JSON: %v", err)
	}
	return string(encoded)
}

func validRawAuthzSubjectDocument(kind authz.SubjectKind) rawAuthzPolicyDocument {
	document := rawAuthzPolicyDocument{
		Subject: rawAuthzSubject{
			Exists:       true,
			Active:       true,
			AuthzVersion: 1,
			Roles:        []rawAuthzRole{{ID: 7, Version: 2}},
		},
		Configuration: fullyEnabledRawAuthzConfiguration(),
	}
	if kind == authz.SubjectKindUser {
		document.Subject.CurrentLegacyAdmin = false
	}
	return document
}

func validRawAuthzResourceDocument(kind authz.SubjectKind, resourceType authz.ResourceType) rawAuthzPolicyDocument {
	document := validRawAuthzSubjectDocument(kind)
	ownerID := int64(4)
	viewer := string(authz.AccessLevelViewer)
	roleID := int64(7)
	document.Resource = &rawAuthzResource{
		Exists:            true,
		OwnerUserID:       &ownerID,
		PublicAccessLevel: &viewer,
		AccessVersion:     3,
	}
	if resourceType == authz.ResourceTypeGroup {
		mode := string(authz.GroupAuthorizationModeACL)
		document.Resource.AuthorizationMode = &mode
	}
	document.RoleGrants = []rawAuthzGrant{
		{ID: 12, RoleID: &roleID, AccessLevel: string(authz.AccessLevelMaintainer)},
	}
	if kind == authz.SubjectKindUser {
		document.UserGrants = []rawAuthzGrant{
			{ID: 11, AccessLevel: string(authz.AccessLevelConsumer)},
		}
	}
	return document
}

func fullyEnabledRawAuthzConfiguration() rawAuthzConfiguration {
	return rawAuthzConfiguration{
		RoleAuthorizationMode:          string(authz.RoleAuthorizationModeRBAC),
		ResourceAccessControlEnabled:   true,
		SelfServiceHostingEnabled:      true,
		GroupSharingEnabled:            true,
		AccountSharingEnabled:          true,
		RoleBasedResourceGrantsEnabled: true,
	}
}

func assertFullyEnabledPolicyConfiguration(t *testing.T, configuration authz.PolicyConfiguration) {
	t.Helper()
	if !configuration.Valid() || configuration.RoleMode() != authz.RoleAuthorizationModeRBAC ||
		!configuration.ResourceAccessControlEnabled() || !configuration.SelfServiceHostingEnabled() ||
		!configuration.SharingEnabled(authz.ResourceTypeGroup) ||
		!configuration.SharingEnabled(authz.ResourceTypeAccount) ||
		!configuration.RoleBasedResourceGrantsEnabled() {
		t.Fatalf("unexpected policy configuration: %+v", configuration)
	}
}

func assertAuthzPolicyStoreExpectations(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("SQL expectations: %v", err)
	}
}

func assertStrictAuthzExpiryPredicates(
	t *testing.T,
	query string,
	strictExpiry *regexp.Regexp,
	inclusiveExpiry *regexp.Regexp,
	want int,
) {
	t.Helper()
	if inclusiveExpiry.MatchString(query) {
		t.Fatalf("query uses inclusive expiry boundary:\n%s", query)
	}
	if got := len(strictExpiry.FindAllStringIndex(query, -1)); got != want {
		t.Fatalf("strict expiry predicates = %d, want %d:\n%s", got, want, query)
	}
}
