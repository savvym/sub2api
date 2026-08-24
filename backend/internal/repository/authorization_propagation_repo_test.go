package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestAuthorizationPropagationStatsRepositorySeparatesPrimarySafetyAndExpiryLag(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	authPrimary := now.Add(-4 * time.Second)
	authSafety := now.Add(-2 * time.Second)
	scheduler := now.Add(-6 * time.Second)
	expiry := now.Add(-8 * time.Second)
	mock.ExpectQuery("(?s)WITH clock AS MATERIALIZED.*delivery_stage = 0 AND available_at <= clock.now.*SELECT MIN\\(created_at\\).*delivery_stage = 1 AND available_at <= clock.now.*scheduler_outbox WHERE next_attempt_at <= clock.now.*authorization_expiry_jobs.*processed_at IS NULL AND available_at <= clock.now.*service_principals AS coordinator.*coordinator.code = \\$1.*coordinator.status = 'active'.*NOT EXISTS.*service_principal_roles AS coordinator_role").
		WithArgs(authorizationExpiryCoordinatorCode).
		WillReturnRows(sqlmock.NewRows([]string{
			"now",
			"auth_primary_pending", "auth_primary_ready", "auth_primary_oldest",
			"auth_safety_pending", "auth_safety_ready", "auth_safety_oldest",
			"scheduler_pending", "scheduler_ready", "scheduler_oldest",
			"expiry_pending", "expiry_ready", "expiry_oldest",
			"expiry_coordinator_ready",
		}).AddRow(
			now,
			3, 3, authPrimary,
			9, 2, authSafety,
			4, 4, scheduler,
			12, 5, expiry,
			true,
		))

	repo := NewAuthorizationPropagationStatsRepository(db)
	stats, err := repo.Snapshot(context.Background())
	require.NoError(t, err)
	require.Equal(t, now, stats.DatabaseTime)
	require.Equal(t, int64(3), stats.AuthPrimary.Pending)
	require.Equal(t, authPrimary, *stats.AuthPrimary.OldestRelevantAt)
	require.Equal(t, int64(9), stats.AuthSafetyPass.Pending)
	require.Equal(t, int64(2), stats.AuthSafetyPass.Ready)
	require.Equal(t, authSafety, *stats.AuthSafetyPass.OldestRelevantAt)
	require.Equal(t, scheduler, *stats.Scheduler.OldestRelevantAt)
	require.Equal(t, int64(12), stats.Expiry.Pending)
	require.Equal(t, int64(5), stats.Expiry.Ready)
	require.Equal(t, expiry, *stats.Expiry.OldestRelevantAt)
	require.True(t, stats.ExpiryCoordinatorReady)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthorizationPropagationStatsRepositoryHandlesEmptyQueues(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	now := time.Now().UTC()
	mock.ExpectQuery("(?s)FROM clock").WithArgs(authorizationExpiryCoordinatorCode).WillReturnRows(sqlmock.NewRows([]string{
		"now",
		"auth_primary_pending", "auth_primary_ready", "auth_primary_oldest",
		"auth_safety_pending", "auth_safety_ready", "auth_safety_oldest",
		"scheduler_pending", "scheduler_ready", "scheduler_oldest",
		"expiry_pending", "expiry_ready", "expiry_oldest",
		"expiry_coordinator_ready",
	}).AddRow(now, 0, 0, nil, 0, 0, nil, 0, 0, nil, 0, 0, nil, false))

	stats, err := NewAuthorizationPropagationStatsRepository(db).Snapshot(context.Background())
	require.NoError(t, err)
	require.Nil(t, stats.AuthPrimary.OldestRelevantAt)
	require.Nil(t, stats.AuthSafetyPass.OldestRelevantAt)
	require.Nil(t, stats.Scheduler.OldestRelevantAt)
	require.Nil(t, stats.Expiry.OldestRelevantAt)
	require.False(t, stats.ExpiryCoordinatorReady)
	require.NoError(t, mock.ExpectationsWereMet())
}
