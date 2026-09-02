package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/authz"
)

func TestAuthzWorkerPolicyStoreLoadsSnapshotWithOneStatement(t *testing.T) {
	store, mock := newAuthzPolicyStoreSQLMock(t)
	document := validRawWorkerAuthorizationDocument(47)
	mock.ExpectQuery(workerAuthorizationSnapshotSQL).
		WithArgs(authz.OpenAIQuotaAutoResetServicePrincipalCode, int64(47)).
		WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(mustWorkerAuthorizationJSON(t, document))).
		RowsWillBeClosed()

	got, err := store.LoadWorkerAuthorizationSnapshot(
		context.Background(),
		authz.OpenAIQuotaAutoResetServicePrincipalCode,
		47,
	)
	if err != nil {
		t.Fatalf("load worker authorization snapshot: %v", err)
	}
	want := mustRepositoryWorkerAuthorizationSnapshot(t, document)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot = %#v, want %#v", got, want)
	}
	assertAuthzPolicyStoreExpectations(t, mock)
}

func TestAuthzWorkerPolicyStorePreservesRawAdditionalAndUnknownPermissions(t *testing.T) {
	tests := []struct {
		name            string
		permissionCodes []string
	}{
		{
			name: "additional known permission",
			permissionCodes: []string{
				string(authz.CapabilityPlatformAccountOpenAIQuotaAutoReset),
				string(authz.CapabilityPlatformResourceOperateAll),
			},
		},
		{
			name: "unknown permission",
			permissionCodes: []string{
				string(authz.CapabilityPlatformAccountOpenAIQuotaAutoReset),
				"platform.account.future_worker_permission",
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store, mock := newAuthzPolicyStoreSQLMock(t)
			document := validRawWorkerAuthorizationDocument(0)
			document.PermissionCodes = testCase.permissionCodes
			mock.ExpectQuery(workerAuthorizationSnapshotSQL).
				WithArgs(authz.OpenAIQuotaAutoResetServicePrincipalCode, int64(0)).
				WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(mustWorkerAuthorizationJSON(t, document))).
				RowsWillBeClosed()

			got, err := store.LoadWorkerAuthorizationSnapshot(
				context.Background(),
				authz.OpenAIQuotaAutoResetServicePrincipalCode,
				0,
			)
			if err != nil {
				t.Fatalf("load worker authorization snapshot: %v", err)
			}
			want := mustRepositoryWorkerAuthorizationSnapshot(t, document)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("raw permission snapshot = %#v, want %#v", got, want)
			}
			assertAuthzPolicyStoreExpectations(t, mock)
		})
	}
}

func TestAuthzWorkerPolicyStorePreservesAccountState(t *testing.T) {
	tests := []struct {
		name      string
		accountID int64
		exists    bool
		deleted   bool
	}{
		{name: "capability-only snapshot", accountID: 0},
		{name: "existing account", accountID: 91, exists: true},
		{name: "missing account", accountID: 92},
		{name: "deleted account", accountID: 93, exists: true, deleted: true},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store, mock := newAuthzPolicyStoreSQLMock(t)
			document := validRawWorkerAuthorizationDocument(testCase.accountID)
			document.AccountExists = testCase.exists
			document.AccountDeleted = testCase.deleted
			mock.ExpectQuery(workerAuthorizationSnapshotSQL).
				WithArgs(authz.OpenAIQuotaAutoResetServicePrincipalCode, testCase.accountID).
				WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(mustWorkerAuthorizationJSON(t, document))).
				RowsWillBeClosed()

			got, err := store.LoadWorkerAuthorizationSnapshot(
				context.Background(),
				authz.OpenAIQuotaAutoResetServicePrincipalCode,
				testCase.accountID,
			)
			if err != nil {
				t.Fatalf("load worker authorization snapshot: %v", err)
			}
			want := mustRepositoryWorkerAuthorizationSnapshot(t, document)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("account snapshot = %#v, want %#v", got, want)
			}
			assertAuthzPolicyStoreExpectations(t, mock)
		})
	}
}

func TestAuthzWorkerPolicyStoreMapsMissingPrincipal(t *testing.T) {
	store, mock := newAuthzPolicyStoreSQLMock(t)
	mock.ExpectQuery(workerAuthorizationSnapshotSQL).
		WithArgs(authz.OpenAIQuotaAutoResetServicePrincipalCode, int64(0)).
		WillReturnRows(sqlmock.NewRows([]string{"document"})).
		RowsWillBeClosed()

	snapshot, err := store.LoadWorkerAuthorizationSnapshot(
		context.Background(),
		authz.OpenAIQuotaAutoResetServicePrincipalCode,
		0,
	)
	if !errors.Is(err, authz.ErrSubjectNotFound) || errors.Is(err, sql.ErrNoRows) ||
		!reflect.DeepEqual(snapshot, authz.WorkerAuthorizationSnapshot{}) {
		t.Fatalf("missing principal result: snapshot=%#v err=%v", snapshot, err)
	}
	assertAuthzPolicyStoreExpectations(t, mock)
}

func TestAuthzWorkerPolicyStoreValidatesInputBeforeQuery(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		code      string
		accountID int64
	}{
		{name: "nil context", code: authz.OpenAIQuotaAutoResetServicePrincipalCode},
		{name: "empty code", ctx: context.Background()},
		{name: "whitespace code", ctx: context.Background(), code: "   "},
		{name: "code too long", ctx: context.Background(), code: strings.Repeat("w", 65)},
		{name: "negative account ID", ctx: context.Background(), code: authz.OpenAIQuotaAutoResetServicePrincipalCode, accountID: -1},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store, mock := newAuthzPolicyStoreSQLMock(t)
			snapshot, err := store.LoadWorkerAuthorizationSnapshot(testCase.ctx, testCase.code, testCase.accountID)
			if !errors.Is(err, authz.ErrInvalidPolicySnapshot) || !reflect.DeepEqual(snapshot, authz.WorkerAuthorizationSnapshot{}) {
				t.Fatalf("invalid input result: snapshot=%#v err=%v", snapshot, err)
			}
			assertAuthzPolicyStoreExpectations(t, mock)
		})
	}
}

func TestAuthzWorkerPolicyStoreTrimsCodeAndRejectsNilDatabase(t *testing.T) {
	t.Run("trim code", func(t *testing.T) {
		store, mock := newAuthzPolicyStoreSQLMock(t)
		document := validRawWorkerAuthorizationDocument(0)
		mock.ExpectQuery(workerAuthorizationSnapshotSQL).
			WithArgs(authz.OpenAIQuotaAutoResetServicePrincipalCode, int64(0)).
			WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(mustWorkerAuthorizationJSON(t, document))).
			RowsWillBeClosed()

		_, err := store.LoadWorkerAuthorizationSnapshot(
			context.Background(),
			"  "+authz.OpenAIQuotaAutoResetServicePrincipalCode+"  ",
			0,
		)
		if err != nil {
			t.Fatalf("load worker snapshot with padded code: %v", err)
		}
		assertAuthzPolicyStoreExpectations(t, mock)
	})

	t.Run("nil database", func(t *testing.T) {
		store := newAuthzPolicyStoreWithQueryer(nil)
		snapshot, err := store.LoadWorkerAuthorizationSnapshot(
			context.Background(),
			authz.OpenAIQuotaAutoResetServicePrincipalCode,
			0,
		)
		if err == nil || !strings.Contains(err.Error(), "nil database client") ||
			!reflect.DeepEqual(snapshot, authz.WorkerAuthorizationSnapshot{}) {
			t.Fatalf("nil database result: snapshot=%#v err=%v", snapshot, err)
		}
	})
}

func TestAuthzWorkerPolicyStoreRejectsMalformedDatabaseDocument(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{name: "malformed JSON", payload: `{"service_principal_id":`},
		{
			name: "invalid semantic snapshot",
			payload: mustWorkerAuthorizationJSON(t, rawWorkerAuthorizationDocument{
				ServicePrincipalID:   7,
				ServicePrincipalCode: authz.OpenAIQuotaAutoResetServicePrincipalCode,
				Active:               true,
				AuthzVersion:         1,
				RoleCount:            -1,
				PermissionCodes:      []string{string(authz.CapabilityPlatformAccountOpenAIQuotaAutoReset)},
			}),
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store, mock := newAuthzPolicyStoreSQLMock(t)
			mock.ExpectQuery(workerAuthorizationSnapshotSQL).
				WithArgs(authz.OpenAIQuotaAutoResetServicePrincipalCode, int64(0)).
				WillReturnRows(sqlmock.NewRows([]string{"document"}).AddRow(testCase.payload)).
				RowsWillBeClosed()

			snapshot, err := store.LoadWorkerAuthorizationSnapshot(
				context.Background(),
				authz.OpenAIQuotaAutoResetServicePrincipalCode,
				0,
			)
			if err == nil || !reflect.DeepEqual(snapshot, authz.WorkerAuthorizationSnapshot{}) {
				t.Fatalf("malformed document result: snapshot=%#v err=%v", snapshot, err)
			}
			assertAuthzPolicyStoreExpectations(t, mock)
		})
	}
}

func validRawWorkerAuthorizationDocument(accountID int64) rawWorkerAuthorizationDocument {
	return rawWorkerAuthorizationDocument{
		ServicePrincipalID:   37,
		ServicePrincipalCode: authz.OpenAIQuotaAutoResetServicePrincipalCode,
		Active:               true,
		AuthzVersion:         4,
		PermissionCodes:      []string{string(authz.CapabilityPlatformAccountOpenAIQuotaAutoReset)},
		AccountID:            accountID,
		AccountExists:        accountID > 0,
	}
}

func mustWorkerAuthorizationJSON(t testing.TB, document rawWorkerAuthorizationDocument) string {
	t.Helper()
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("encode worker authorization document: %v", err)
	}
	return string(payload)
}

func mustRepositoryWorkerAuthorizationSnapshot(
	t testing.TB,
	document rawWorkerAuthorizationDocument,
) authz.WorkerAuthorizationSnapshot {
	t.Helper()
	snapshot, err := authz.NewWorkerAuthorizationSnapshot(authz.WorkerAuthorizationSnapshotInput{
		ServicePrincipalID:   document.ServicePrincipalID,
		ServicePrincipalCode: document.ServicePrincipalCode,
		Active:               document.Active,
		AuthzVersion:         document.AuthzVersion,
		RoleCount:            document.RoleCount,
		PermissionCodes:      document.PermissionCodes,
		AccountID:            document.AccountID,
		AccountExists:        document.AccountExists,
		AccountDeleted:       document.AccountDeleted,
	})
	if err != nil {
		t.Fatalf("create expected worker authorization snapshot: %v", err)
	}
	return snapshot
}
