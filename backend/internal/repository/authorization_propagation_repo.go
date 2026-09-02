package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type authorizationPropagationStatsRepository struct {
	db *sql.DB
}

func NewAuthorizationPropagationStatsRepository(db *sql.DB) service.AuthorizationPropagationStatsRepository {
	return &authorizationPropagationStatsRepository{db: db}
}

func (r *authorizationPropagationStatsRepository) Snapshot(
	ctx context.Context,
) (service.AuthorizationPropagationStats, error) {
	if r == nil || r.db == nil || ctx == nil {
		return service.AuthorizationPropagationStats{}, errors.New("authorization propagation stats repository is unavailable")
	}
	var (
		stats                service.AuthorizationPropagationStats
		authPrimaryOldest    sql.NullTime
		authSafetyPassOldest sql.NullTime
		schedulerOldest      sql.NullTime
		expiryOldest         sql.NullTime
	)
	err := r.db.QueryRowContext(ctx, `
WITH clock AS MATERIALIZED (
    SELECT statement_timestamp() AS now
)
SELECT
    clock.now,
    (SELECT COUNT(*) FROM auth_cache_invalidation_outbox WHERE delivery_stage = 0),
    (
        SELECT COUNT(*)
        FROM auth_cache_invalidation_outbox
        WHERE delivery_stage = 0 AND available_at <= clock.now
    ),
    (SELECT MIN(created_at) FROM auth_cache_invalidation_outbox WHERE delivery_stage = 0),
    (SELECT COUNT(*) FROM auth_cache_invalidation_outbox WHERE delivery_stage = 1),
    (
        SELECT COUNT(*)
        FROM auth_cache_invalidation_outbox
        WHERE delivery_stage = 1 AND available_at <= clock.now
    ),
    (
        SELECT MIN(created_at)
        FROM auth_cache_invalidation_outbox
        WHERE delivery_stage = 1 AND available_at <= clock.now
    ),
    (SELECT COUNT(*) FROM scheduler_outbox),
    (SELECT COUNT(*) FROM scheduler_outbox WHERE next_attempt_at <= clock.now),
    (SELECT MIN(created_at) FROM scheduler_outbox),
    (
        SELECT COUNT(*)
        FROM authorization_expiry_jobs
        WHERE processed_at IS NULL
    ),
    (
        SELECT COUNT(*)
        FROM authorization_expiry_jobs
        WHERE processed_at IS NULL AND available_at <= clock.now
    ),
    (
        SELECT MIN(expires_at)
        FROM authorization_expiry_jobs
        WHERE processed_at IS NULL AND expires_at <= clock.now
    ),
    EXISTS (
        SELECT 1
        FROM service_principals AS coordinator
        WHERE coordinator.code = $1
          AND coordinator.status = 'active'
          AND NOT EXISTS (
              SELECT 1
              FROM service_principal_roles AS coordinator_role
              WHERE coordinator_role.service_principal_id = coordinator.id
          )
    )
FROM clock
`, authorizationExpiryCoordinatorCode).Scan(
		&stats.DatabaseTime,
		&stats.AuthPrimary.Pending,
		&stats.AuthPrimary.Ready,
		&authPrimaryOldest,
		&stats.AuthSafetyPass.Pending,
		&stats.AuthSafetyPass.Ready,
		&authSafetyPassOldest,
		&stats.Scheduler.Pending,
		&stats.Scheduler.Ready,
		&schedulerOldest,
		&stats.Expiry.Pending,
		&stats.Expiry.Ready,
		&expiryOldest,
		&stats.ExpiryCoordinatorReady,
	)
	if err != nil {
		return stats, err
	}
	setAuthorizationPropagationOldest(&stats.AuthPrimary, authPrimaryOldest)
	setAuthorizationPropagationOldest(&stats.AuthSafetyPass, authSafetyPassOldest)
	setAuthorizationPropagationOldest(&stats.Scheduler, schedulerOldest)
	setAuthorizationPropagationOldest(&stats.Expiry, expiryOldest)
	return stats, nil
}

func setAuthorizationPropagationOldest(
	stats *service.AuthorizationPropagationQueueStats,
	value sql.NullTime,
) {
	if stats == nil || !value.Valid {
		return
	}
	timestamp := value.Time
	stats.OldestRelevantAt = &timestamp
}
