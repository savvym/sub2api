package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAuthorizationExpiryRepositoryClaimsDueJobsWithRecoverableLease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	expiresAt := time.Now().UTC().Add(-time.Minute)
	mock.ExpectQuery("(?s)processed_at IS NULL.*available_at <= statement_timestamp\\(\\).*FOR UPDATE SKIP LOCKED.*RETURNING").
		WithArgs("worker-a", 100, int64(30000)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "source_type", "source_id", "expires_at", "attempts"}).
			AddRow(7, service.AuthorizationExpirySourceUserRole, 11, expiresAt, 2))

	repo := NewAuthorizationExpiryRepository(db)
	jobs, err := repo.Claim(context.Background(), "worker-a", 100, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, int64(7), jobs[0].ID)
	require.Equal(t, int64(11), jobs[0].SourceID)
	require.Equal(t, 2, jobs[0].Attempts)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthorizationExpiryRepositoryRetryReleasesOwnedClaim(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectExec("(?s)UPDATE authorization_expiry_jobs.*attempts = attempts \\+ 1.*claimed_at = NULL.*WHERE id = \\$1 AND claimed_by = \\$2").
		WithArgs(int64(7), "worker-a", int64(500), "serialization failure").
		WillReturnResult(sqlmock.NewResult(0, 1))

	repo := NewAuthorizationExpiryRepository(db)
	err = repo.RetryClaimed(context.Background(), 7, "worker-a", 500*time.Millisecond, "serialization failure")
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthorizationExpiryRepositoryReleaseClaimsOnlyClearsOwnedPendingJobs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectExec("(?s)UPDATE authorization_expiry_jobs.*claimed_at = NULL.*claimed_by = NULL.*claimed_by = \\$1.*processed_at IS NULL.*id = ANY\\(\\$2::BIGINT\\[\\]\\)").
		WithArgs("worker-a", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err = NewAuthorizationExpiryRepository(db).ReleaseClaims(
		context.Background(), "worker-a", []int64{7, 8},
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthorizationExpiryRepositoryProcessLocksParentBeforeSourceAndChecksCoordinatorBeforeEarlyRelease(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	expiresAt := now.Add(time.Minute)
	job := service.AuthorizationExpiryJob{
		ID: 7, SourceType: service.AuthorizationExpirySourceAccountAccessGrant,
		SourceID: 11, ExpiresAt: expiresAt,
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT account_id FROM account_access_grants WHERE id = \\$1").
		WithArgs(job.SourceID).
		WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(13))
	mock.ExpectQuery("SELECT owner_user_id FROM accounts WHERE id = \\$1 FOR UPDATE").
		WithArgs(int64(13)).
		WillReturnRows(sqlmock.NewRows([]string{"owner_user_id"}).AddRow(17))
	mock.ExpectQuery("SELECT account_id, NULL::BIGINT, expires_at FROM account_access_grants WHERE id = \\$1 FOR UPDATE").
		WithArgs(job.SourceID).
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "owner_user_id", "expires_at"}).
			AddRow(13, nil, expiresAt))
	mock.ExpectQuery("(?s)SELECT id.*FROM authorization_expiry_jobs.*FOR UPDATE").
		WithArgs(job.ID, "worker-a", job.SourceType, job.SourceID, job.ExpiresAt).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(job.ID))
	mock.ExpectQuery("(?s)SELECT principals.id, principals.status.*FOR UPDATE OF principals").
		WithArgs(authorizationExpiryCoordinatorCode).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status"}).AddRow(19, service.StatusActive))
	mock.ExpectQuery("(?s)SELECT EXISTS.*FROM service_principal_roles").
		WithArgs(int64(19)).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectQuery("SELECT statement_timestamp\\(\\)").
		WillReturnRows(sqlmock.NewRows([]string{"statement_timestamp"}).AddRow(now))
	mock.ExpectExec("(?s)UPDATE authorization_expiry_jobs.*available_at = \\$3.*claimed_at = NULL").
		WithArgs(job.ID, "worker-a", expiresAt).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	result, err := NewAuthorizationExpiryRepository(db).ProcessClaimed(context.Background(), job, "worker-a")
	require.NoError(t, err)
	require.False(t, result.Processed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthorizationExpiryRepositoryMissingSourceFailsClosedWithoutCoordinator(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	job := service.AuthorizationExpiryJob{
		ID: 7, SourceType: service.AuthorizationExpirySourceUserRole,
		SourceID: 11, ExpiresAt: time.Now().UTC().Add(-time.Minute),
	}

	mock.ExpectBegin()
	mock.ExpectQuery("SELECT user_id FROM user_roles WHERE id = \\$1").
		WithArgs(job.SourceID).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}))
	mock.ExpectQuery("(?s)SELECT id.*FROM authorization_expiry_jobs.*FOR UPDATE").
		WithArgs(job.ID, "worker-a", job.SourceType, job.SourceID, job.ExpiresAt).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(job.ID))
	mock.ExpectQuery("(?s)SELECT principals.id, principals.status.*FOR UPDATE OF principals").
		WithArgs(authorizationExpiryCoordinatorCode).
		WillReturnRows(sqlmock.NewRows([]string{"id", "status"}))
	mock.ExpectRollback()

	result, err := NewAuthorizationExpiryRepository(db).ProcessClaimed(context.Background(), job, "worker-a")
	require.ErrorContains(t, err, "load authorization expiry coordinator")
	require.False(t, result.Processed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthorizationExpiryRequestIDIncludesExpiryGeneration(t *testing.T) {
	expiresAt := time.Date(2026, time.August, 24, 1, 2, 3, 456000000, time.UTC)
	first := authorizationExpiryRequestID(service.AuthorizationExpiryJob{ID: 7, ExpiresAt: expiresAt})
	second := authorizationExpiryRequestID(service.AuthorizationExpiryJob{ID: 7, ExpiresAt: expiresAt.Add(time.Microsecond)})

	require.Equal(t, "authorization-expiry-job:7:1787533323456000", first)
	require.NotEqual(t, first, second)
	require.LessOrEqual(t, len(first), 64)
}

func TestAuthorizationExpiryRepositoryStatsUseDatabaseStatementTime(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	oldest := now.Add(-10 * time.Second)
	mock.ExpectQuery("(?s)statement_timestamp\\(\\).*FROM service_principals.*FROM service_principal_roles.*expires_at <= statement_timestamp\\(\\).*").
		WithArgs(authorizationExpiryCoordinatorCode).
		WillReturnRows(sqlmock.NewRows([]string{
			"database_time", "coordinator_ready", "pending", "due", "oldest_due", "max_attempts", "last_error",
		}).AddRow(now, true, 8, 3, oldest, 4, "retry"))

	stats, err := NewAuthorizationExpiryRepository(db).Stats(context.Background())
	require.NoError(t, err)
	require.Equal(t, now, stats.DatabaseTime)
	require.True(t, stats.CoordinatorReady)
	require.Equal(t, int64(8), stats.Pending)
	require.Equal(t, int64(3), stats.Due)
	require.Equal(t, oldest, *stats.OldestDueExpiresAt)
	require.Equal(t, 4, stats.MaxAttempts)
	require.Equal(t, "retry", stats.LastError)
	require.NoError(t, mock.ExpectationsWereMet())
}
