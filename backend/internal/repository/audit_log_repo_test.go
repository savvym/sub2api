package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBuildAuditLogsWhereIncludesServicePrincipalIdentity(t *testing.T) {
	principalID := int64(41)
	where, args := buildAuditLogsWhere(&service.AuditLogFilter{
		ActorServicePrincipalID: &principalID,
		Query:                   "Admin API Key",
	})

	require.Contains(t, where, "l.actor_service_principal_id = $1")
	require.Contains(t, where, "sp.code ILIKE $2")
	require.Contains(t, where, "sp.name ILIKE $2")
	require.Equal(t, []any{principalID, "%Admin API Key%"}, args)
}

func TestAuditLogRepositoryGetHydratesServicePrincipalIdentity(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	createdAt := time.Date(2026, time.August, 20, 8, 30, 0, 0, time.UTC)
	rows := sqlmock.NewRows([]string{
		"id", "created_at", "actor_user_id", "actor_service_principal_id",
		"actor_service_principal_code", "actor_service_principal_name",
		"actor_email", "actor_role", "auth_method", "credential_masked",
		"action", "method", "path", "request_id", "client_ip", "user_agent",
		"request_body", "status_code", "latency_ms", "extra",
	}).AddRow(
		int64(9), createdAt, nil, int64(41),
		"admin_api_key", "Admin API Key", "", "", "admin_api_key", "x-api-key ab****yz",
		"admin.accounts.create", "POST", "/api/v1/admin/accounts", "req-9", "127.0.0.1", "test-agent",
		`{"name":"example"}`, 201, int64(18), `{"result":"success"}`,
	)
	mock.ExpectQuery("SELECT").WithArgs(int64(9)).WillReturnRows(rows)

	repo := NewAuditLogRepository(db)
	entry, err := repo.GetByID(context.Background(), 9)
	require.NoError(t, err)
	require.Nil(t, entry.ActorUserID)
	require.NotNil(t, entry.ActorServicePrincipalID)
	require.Equal(t, int64(41), *entry.ActorServicePrincipalID)
	require.Equal(t, "admin_api_key", entry.ActorServicePrincipalCode)
	require.Equal(t, "Admin API Key", entry.ActorServicePrincipalName)
	require.Empty(t, entry.ActorEmail)
	require.Equal(t, map[string]any{"result": "success"}, entry.Extra)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScanAuditLogRowKeepsActorlessEventsValid(t *testing.T) {
	createdAt := time.Date(2026, time.August, 20, 9, 0, 0, 0, time.UTC)
	entry, err := scanAuditLogRow(func(dest ...any) error {
		values := []any{
			int64(11), createdAt, sql.NullInt64{}, sql.NullInt64{}, "", "",
			"", "", "", "", "auth.login", "POST", "/api/v1/auth/login",
			"", "127.0.0.1", "test-agent", "", 401, int64(3), "{}",
		}
		for i := range dest {
			var assignErr error
			switch target := dest[i].(type) {
			case *int64:
				assignErr = assignAuditLogScanValue(target, values[i])
			case *time.Time:
				assignErr = assignAuditLogScanValue(target, values[i])
			case *sql.NullInt64:
				assignErr = assignAuditLogScanValue(target, values[i])
			case *string:
				assignErr = assignAuditLogScanValue(target, values[i])
			case *int:
				assignErr = assignAuditLogScanValue(target, values[i])
			default:
				assignErr = fmt.Errorf("unsupported audit log scan destination %T", target)
			}
			if assignErr != nil {
				return assignErr
			}
		}
		return nil
	})
	require.NoError(t, err)
	require.Nil(t, entry.ActorUserID)
	require.Nil(t, entry.ActorServicePrincipalID)
}

func assignAuditLogScanValue[T any](target *T, value any) error {
	typedValue, ok := value.(T)
	if !ok {
		var expected T
		return fmt.Errorf("cannot scan %T into %T", value, expected)
	}
	*target = typedValue
	return nil
}
