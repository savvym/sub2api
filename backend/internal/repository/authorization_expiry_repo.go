package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

const authorizationExpiryCoordinatorCode = "authorization_expiry_coordinator"

type authorizationExpiryRepository struct {
	db *sql.DB
}

func NewAuthorizationExpiryRepository(db *sql.DB) service.AuthorizationExpiryRepository {
	return &authorizationExpiryRepository{db: db}
}

func (r *authorizationExpiryRepository) Claim(
	ctx context.Context,
	workerID string,
	limit int,
	lease time.Duration,
) ([]service.AuthorizationExpiryJob, error) {
	if r == nil || r.db == nil || ctx == nil || workerID == "" {
		return nil, errors.New("authorization expiry repository is unavailable")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	leaseMilliseconds := lease.Milliseconds()
	if leaseMilliseconds < 1 {
		leaseMilliseconds = (30 * time.Second).Milliseconds()
	}
	rows, err := r.db.QueryContext(ctx, `
WITH candidates AS (
    SELECT id
    FROM authorization_expiry_jobs
    WHERE processed_at IS NULL
      AND available_at <= statement_timestamp()
      AND (
          claimed_at IS NULL
          OR claimed_at <= statement_timestamp() - ($3 * INTERVAL '1 millisecond')
      )
    ORDER BY available_at, id
    LIMIT $2
    FOR UPDATE SKIP LOCKED
)
UPDATE authorization_expiry_jobs AS jobs
SET claimed_at = statement_timestamp(),
    claimed_by = $1,
    updated_at = statement_timestamp()
FROM candidates
WHERE jobs.id = candidates.id
RETURNING jobs.id, jobs.source_type, jobs.source_id, jobs.expires_at, jobs.attempts
`, workerID, limit, leaseMilliseconds)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	jobs := make([]service.AuthorizationExpiryJob, 0, limit)
	for rows.Next() {
		var job service.AuthorizationExpiryJob
		if err := rows.Scan(&job.ID, &job.SourceType, &job.SourceID, &job.ExpiresAt, &job.Attempts); err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return jobs, nil
}

func (r *authorizationExpiryRepository) ProcessClaimed(
	ctx context.Context,
	job service.AuthorizationExpiryJob,
	workerID string,
) (service.AuthorizationExpiryResult, error) {
	if r == nil || r.db == nil || ctx == nil || job.ID <= 0 || job.SourceID <= 0 || workerID == "" {
		return service.AuthorizationExpiryResult{}, errors.New("invalid authorization expiry claim")
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return service.AuthorizationExpiryResult{}, err
	}
	defer func() { _ = tx.Rollback() }()

	// Management writes lock the parent before its role/grant; expiry must use
	// the same parent -> source -> trigger-owned job order to avoid deadlocks.
	parentID, sourceObserved, err := readAuthorizationExpiryParentID(ctx, tx, job.SourceType, job.SourceID)
	if err != nil {
		return service.AuthorizationExpiryResult{}, err
	}
	var (
		source authorizationExpirySource
		found  bool
	)
	if sourceObserved {
		ownerUserID, parentFound, lockErr := lockAuthorizationExpiryParent(ctx, tx, job.SourceType, parentID)
		if lockErr != nil {
			return service.AuthorizationExpiryResult{}, lockErr
		}
		if !parentFound {
			return service.AuthorizationExpiryResult{}, fmt.Errorf(
				"authorization expiry job %d parent %d disappeared", job.ID, parentID,
			)
		}
		source, found, err = lockAuthorizationExpirySource(ctx, tx, job.SourceType, job.SourceID)
		if err != nil {
			return service.AuthorizationExpiryResult{}, err
		}
		if found && source.SubjectOrResourceID != parentID {
			return service.AuthorizationExpiryResult{}, fmt.Errorf(
				"authorization expiry job %d source parent changed from %d to %d",
				job.ID, parentID, source.SubjectOrResourceID,
			)
		}
		if found {
			source.OwnerUserID = ownerUserID
		}
	}
	locked, err := lockAuthorizationExpiryJob(ctx, tx, job, workerID)
	if err != nil {
		return service.AuthorizationExpiryResult{}, err
	}
	if !locked {
		if err := tx.Commit(); err != nil {
			return service.AuthorizationExpiryResult{}, err
		}
		return service.AuthorizationExpiryResult{}, nil
	}
	coordinatorID, err := loadAuthorizationExpiryCoordinator(ctx, tx)
	if err != nil {
		return service.AuthorizationExpiryResult{}, err
	}
	if !found {
		result, err := tx.ExecContext(ctx, `
UPDATE authorization_expiry_jobs
SET processed_at = statement_timestamp(),
    claimed_at = NULL,
    claimed_by = NULL,
    last_error = '',
    updated_at = statement_timestamp()
WHERE id = $1 AND claimed_by = $2 AND processed_at IS NULL
`, job.ID, workerID)
		if err != nil {
			return service.AuthorizationExpiryResult{}, err
		}
		if err := requireOneAuthorizationExpiryRow(result, job.ID, "finish missing source"); err != nil {
			return service.AuthorizationExpiryResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return service.AuthorizationExpiryResult{}, err
		}
		return service.AuthorizationExpiryResult{Processed: true, SourceMissing: true}, nil
	}
	if !source.ExpiresAt.Equal(job.ExpiresAt) {
		return service.AuthorizationExpiryResult{}, fmt.Errorf("authorization expiry job %d source timestamp changed", job.ID)
	}

	var databaseTime time.Time
	if err := tx.QueryRowContext(ctx, "SELECT statement_timestamp()").Scan(&databaseTime); err != nil {
		return service.AuthorizationExpiryResult{}, err
	}
	if source.ExpiresAt.After(databaseTime) {
		result, err := tx.ExecContext(ctx, `
UPDATE authorization_expiry_jobs
SET available_at = $3,
    claimed_at = NULL,
    claimed_by = NULL,
    updated_at = statement_timestamp()
WHERE id = $1 AND claimed_by = $2 AND processed_at IS NULL
`, job.ID, workerID, source.ExpiresAt)
		if err != nil {
			return service.AuthorizationExpiryResult{}, err
		}
		if err := requireOneAuthorizationExpiryRow(result, job.ID, "release early claim"); err != nil {
			return service.AuthorizationExpiryResult{}, err
		}
		if err := tx.Commit(); err != nil {
			return service.AuthorizationExpiryResult{}, err
		}
		return service.AuthorizationExpiryResult{}, nil
	}

	if err := applyAuthorizationExpiry(ctx, tx, job, source, coordinatorID); err != nil {
		return service.AuthorizationExpiryResult{}, err
	}
	result, err := tx.ExecContext(ctx, `
UPDATE authorization_expiry_jobs
SET processed_at = statement_timestamp(),
    claimed_at = NULL,
    claimed_by = NULL,
    last_error = '',
    updated_at = statement_timestamp()
WHERE id = $1
  AND claimed_by = $2
  AND processed_at IS NULL
  AND source_type = $3
  AND source_id = $4
  AND expires_at = $5
`, job.ID, workerID, job.SourceType, job.SourceID, job.ExpiresAt)
	if err != nil {
		return service.AuthorizationExpiryResult{}, err
	}
	if err := requireOneAuthorizationExpiryRow(result, job.ID, "finish"); err != nil {
		return service.AuthorizationExpiryResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return service.AuthorizationExpiryResult{}, err
	}
	return service.AuthorizationExpiryResult{Processed: true}, nil
}

type authorizationExpirySource struct {
	SubjectOrResourceID int64
	OwnerUserID         *int64
	ExpiresAt           time.Time
}

func readAuthorizationExpiryParentID(
	ctx context.Context,
	tx *sql.Tx,
	sourceType string,
	sourceID int64,
) (int64, bool, error) {
	var query string
	switch sourceType {
	case service.AuthorizationExpirySourceUserRole:
		query = `SELECT user_id FROM user_roles WHERE id = $1`
	case service.AuthorizationExpirySourceServicePrincipalRole:
		query = `SELECT service_principal_id FROM service_principal_roles WHERE id = $1`
	case service.AuthorizationExpirySourceAccountAccessGrant:
		query = `SELECT account_id FROM account_access_grants WHERE id = $1`
	case service.AuthorizationExpirySourceGroupAccessGrant:
		query = `SELECT group_id FROM group_access_grants WHERE id = $1`
	default:
		return 0, false, fmt.Errorf("unsupported authorization expiry source type %q", sourceType)
	}
	var parentID int64
	err := tx.QueryRowContext(ctx, query, sourceID).Scan(&parentID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return parentID, true, nil
}

func lockAuthorizationExpiryParent(
	ctx context.Context,
	tx *sql.Tx,
	sourceType string,
	parentID int64,
) (*int64, bool, error) {
	var query string
	switch sourceType {
	case service.AuthorizationExpirySourceUserRole:
		query = `SELECT NULL::BIGINT FROM users WHERE id = $1 FOR UPDATE`
	case service.AuthorizationExpirySourceServicePrincipalRole:
		query = `SELECT NULL::BIGINT FROM service_principals WHERE id = $1 FOR UPDATE`
	case service.AuthorizationExpirySourceAccountAccessGrant:
		query = `SELECT owner_user_id FROM accounts WHERE id = $1 FOR UPDATE`
	case service.AuthorizationExpirySourceGroupAccessGrant:
		query = `SELECT owner_user_id FROM groups WHERE id = $1 FOR UPDATE`
	default:
		return nil, false, fmt.Errorf("unsupported authorization expiry source type %q", sourceType)
	}
	var owner sql.NullInt64
	err := tx.QueryRowContext(ctx, query, parentID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !owner.Valid {
		return nil, true, nil
	}
	ownerUserID := owner.Int64
	return &ownerUserID, true, nil
}

func lockAuthorizationExpirySource(
	ctx context.Context,
	tx *sql.Tx,
	sourceType string,
	sourceID int64,
) (authorizationExpirySource, bool, error) {
	var query string
	switch sourceType {
	case service.AuthorizationExpirySourceUserRole:
		query = `SELECT user_id, NULL::BIGINT, expires_at FROM user_roles WHERE id = $1 FOR UPDATE`
	case service.AuthorizationExpirySourceServicePrincipalRole:
		query = `SELECT service_principal_id, NULL::BIGINT, expires_at FROM service_principal_roles WHERE id = $1 FOR UPDATE`
	case service.AuthorizationExpirySourceAccountAccessGrant:
		query = `SELECT account_id, NULL::BIGINT, expires_at FROM account_access_grants WHERE id = $1 FOR UPDATE`
	case service.AuthorizationExpirySourceGroupAccessGrant:
		query = `SELECT group_id, NULL::BIGINT, expires_at FROM group_access_grants WHERE id = $1 FOR UPDATE`
	default:
		return authorizationExpirySource{}, false, fmt.Errorf("unsupported authorization expiry source type %q", sourceType)
	}
	var (
		source    authorizationExpirySource
		owner     sql.NullInt64
		expiresAt sql.NullTime
	)
	err := tx.QueryRowContext(ctx, query, sourceID).Scan(&source.SubjectOrResourceID, &owner, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return authorizationExpirySource{}, false, nil
	}
	if err != nil {
		return authorizationExpirySource{}, false, err
	}
	if !expiresAt.Valid {
		return authorizationExpirySource{}, false, nil
	}
	if owner.Valid {
		value := owner.Int64
		source.OwnerUserID = &value
	}
	source.ExpiresAt = expiresAt.Time
	return source, true, nil
}

func lockAuthorizationExpiryJob(
	ctx context.Context,
	tx *sql.Tx,
	job service.AuthorizationExpiryJob,
	workerID string,
) (bool, error) {
	var ignored int64
	err := tx.QueryRowContext(ctx, `
SELECT id
FROM authorization_expiry_jobs
WHERE id = $1
  AND claimed_by = $2
  AND processed_at IS NULL
  AND source_type = $3
  AND source_id = $4
  AND expires_at = $5
FOR UPDATE
`, job.ID, workerID, job.SourceType, job.SourceID, job.ExpiresAt).Scan(&ignored)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func loadAuthorizationExpiryCoordinator(ctx context.Context, tx *sql.Tx) (int64, error) {
	var (
		id     int64
		status string
	)
	// FOR UPDATE serializes both status updates and the FK key-share lock taken
	// by a concurrent service_principal_roles insert with this readiness check.
	err := tx.QueryRowContext(ctx, `
SELECT principals.id, principals.status
FROM service_principals AS principals
WHERE principals.code = $1
FOR UPDATE OF principals
`, authorizationExpiryCoordinatorCode).Scan(&id, &status)
	if err != nil {
		return 0, fmt.Errorf("load authorization expiry coordinator: %w", err)
	}
	if status != service.StatusActive {
		return 0, fmt.Errorf("authorization expiry coordinator is not an active identity-only principal")
	}
	var hasRole bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM service_principal_roles
    WHERE service_principal_id = $1
)
`, id).Scan(&hasRole); err != nil {
		return 0, fmt.Errorf("load authorization expiry coordinator roles: %w", err)
	}
	if hasRole {
		return 0, fmt.Errorf("authorization expiry coordinator is not an active identity-only principal")
	}
	return id, nil
}

func applyAuthorizationExpiry(
	ctx context.Context,
	tx *sql.Tx,
	job service.AuthorizationExpiryJob,
	source authorizationExpirySource,
	coordinatorID int64,
) error {
	switch job.SourceType {
	case service.AuthorizationExpirySourceUserRole:
		var version int64
		if err := tx.QueryRowContext(ctx, `
UPDATE users
SET authz_version = authz_version + 1,
    updated_at = statement_timestamp()
WHERE id = $1
RETURNING authz_version
`, source.SubjectOrResourceID).Scan(&version); err != nil {
			return fmt.Errorf("version user after role expiry: %w", err)
		}
		return appendAuthorizationExpiryAudit(ctx, tx, coordinatorID, job, version)
	case service.AuthorizationExpirySourceServicePrincipalRole:
		var version int64
		if err := tx.QueryRowContext(ctx, `
UPDATE service_principals
SET authz_version = authz_version + 1,
    updated_at = statement_timestamp()
WHERE id = $1
RETURNING authz_version
`, source.SubjectOrResourceID).Scan(&version); err != nil {
			return fmt.Errorf("version service principal after role expiry: %w", err)
		}
		return appendAuthorizationExpiryAudit(ctx, tx, coordinatorID, job, version)
	case service.AuthorizationExpirySourceAccountAccessGrant:
		version, ownerUserID, err := incrementExpiredGrantResourceVersion(ctx, tx, "accounts", source.SubjectOrResourceID)
		if err != nil {
			return err
		}
		if ownerUserID == nil {
			ownerUserID = source.OwnerUserID
		}
		if err := appendAuthorizationExpiryResourceEvent(
			ctx, tx, coordinatorID, job, source.SubjectOrResourceID, nil, ownerUserID, version,
		); err != nil {
			return err
		}
		accountID := source.SubjectOrResourceID
		return enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, map[string]any{
			"reason": "authorization_expiry", "source_id": job.SourceID, "expires_at": job.ExpiresAt,
		})
	case service.AuthorizationExpirySourceGroupAccessGrant:
		version, ownerUserID, err := incrementExpiredGrantResourceVersion(ctx, tx, "groups", source.SubjectOrResourceID)
		if err != nil {
			return err
		}
		if ownerUserID == nil {
			ownerUserID = source.OwnerUserID
		}
		if err := appendAuthorizationExpiryResourceEvent(
			ctx, tx, coordinatorID, job, nil, source.SubjectOrResourceID, ownerUserID, version,
		); err != nil {
			return err
		}
		groupID := source.SubjectOrResourceID
		return enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventGroupChanged, nil, &groupID, map[string]any{
			"reason": "authorization_expiry", "source_id": job.SourceID, "expires_at": job.ExpiresAt,
		})
	default:
		return fmt.Errorf("unsupported authorization expiry source type %q", job.SourceType)
	}
}

func appendAuthorizationExpiryAudit(
	ctx context.Context,
	tx *sql.Tx,
	coordinatorID int64,
	job service.AuthorizationExpiryJob,
	version int64,
) error {
	action := "authorization." + job.SourceType + ".expired"
	_, err := tx.ExecContext(ctx, `
INSERT INTO audit_logs (
    actor_service_principal_id,
    auth_method,
    action,
    method,
    path,
    request_id,
    status_code,
    extra
)
VALUES (
    $1, $2, $3, 'WORKER', '/authorization/expiry', $4, 200,
    jsonb_build_object(
        'source_type', $5::TEXT,
        'source_id', $6::BIGINT,
        'expires_at', $7::TIMESTAMPTZ,
        'authz_version', $8::BIGINT
    )
)
`, coordinatorID, string(authz.AuthMethodServicePrincipal), action,
		authorizationExpiryRequestID(job), job.SourceType, job.SourceID, job.ExpiresAt, version)
	return err
}

func incrementExpiredGrantResourceVersion(
	ctx context.Context,
	tx *sql.Tx,
	table string,
	id int64,
) (int64, *int64, error) {
	query := fmt.Sprintf(`
UPDATE %s
SET access_version = access_version + 1,
    updated_at = statement_timestamp()
WHERE id = $1
RETURNING access_version, owner_user_id
`, table)
	var (
		version int64
		owner   sql.NullInt64
	)
	if err := tx.QueryRowContext(ctx, query, id).Scan(&version, &owner); err != nil {
		return 0, nil, fmt.Errorf("version %s after access grant expiry: %w", table, err)
	}
	if !owner.Valid {
		return version, nil, nil
	}
	ownerID := owner.Int64
	return version, &ownerID, nil
}

func appendAuthorizationExpiryResourceEvent(
	ctx context.Context,
	tx *sql.Tx,
	coordinatorID int64,
	job service.AuthorizationExpiryJob,
	accountID any,
	groupID any,
	ownerUserID *int64,
	version int64,
) error {
	_, err := tx.ExecContext(ctx, `
INSERT INTO resource_authorization_events (
    account_id,
    group_id,
    resource_owner_user_id,
    actor_service_principal_id,
    auth_method,
    event_type,
    resource_access_version,
    request_id,
    details
)
VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8,
    jsonb_build_object(
        'source_type', $9::TEXT,
        'source_id', $10::BIGINT,
        'expires_at', $11::TIMESTAMPTZ,
        'result', 'expired'
    )
)
`, accountID, groupID, ownerUserID, coordinatorID, string(authz.AuthMethodServicePrincipal),
		job.SourceType+".expired", version, authorizationExpiryRequestID(job),
		job.SourceType, job.SourceID, job.ExpiresAt)
	return err
}

func authorizationExpiryRequestID(job service.AuthorizationExpiryJob) string {
	return fmt.Sprintf("authorization-expiry-job:%d:%d", job.ID, job.ExpiresAt.UTC().UnixMicro())
}

func requireOneAuthorizationExpiryRow(result sql.Result, jobID int64, operation string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("authorization expiry job %d cannot %s", jobID, operation)
	}
	return nil
}

func (r *authorizationExpiryRepository) RetryClaimed(
	ctx context.Context,
	id int64,
	workerID string,
	delay time.Duration,
	lastError string,
) error {
	if r == nil || r.db == nil || id <= 0 || workerID == "" {
		return errors.New("invalid authorization expiry retry")
	}
	if delay < 0 {
		delay = 0
	}
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	result, err := r.db.ExecContext(ctx, `
UPDATE authorization_expiry_jobs
SET attempts = attempts + 1,
    available_at = GREATEST(expires_at, statement_timestamp() + ($3 * INTERVAL '1 millisecond')),
    last_error = LEFT($4, 1024),
    claimed_at = NULL,
    claimed_by = NULL,
    updated_at = statement_timestamp()
WHERE id = $1 AND claimed_by = $2 AND processed_at IS NULL
`, id, workerID, delay.Milliseconds(), lastError)
	if err != nil {
		return err
	}
	return requireOneAuthorizationExpiryRow(result, id, "retry")
}

func (r *authorizationExpiryRepository) ReleaseClaims(
	ctx context.Context,
	workerID string,
	jobIDs []int64,
) error {
	if r == nil || r.db == nil || ctx == nil || workerID == "" {
		return errors.New("invalid authorization expiry claim release")
	}
	if len(jobIDs) == 0 {
		return nil
	}
	for _, jobID := range jobIDs {
		if jobID <= 0 {
			return errors.New("invalid authorization expiry claim release")
		}
	}
	_, err := r.db.ExecContext(ctx, `
UPDATE authorization_expiry_jobs
SET claimed_at = NULL,
    claimed_by = NULL,
    updated_at = statement_timestamp()
WHERE claimed_by = $1
  AND processed_at IS NULL
  AND id = ANY($2::BIGINT[])
`, workerID, pq.Array(jobIDs))
	return err
}

func (r *authorizationExpiryRepository) Stats(ctx context.Context) (service.AuthorizationExpiryStats, error) {
	if r == nil || r.db == nil {
		return service.AuthorizationExpiryStats{}, errors.New("authorization expiry repository is unavailable")
	}
	var (
		stats     service.AuthorizationExpiryStats
		oldestDue sql.NullTime
		lastError sql.NullString
	)
	err := r.db.QueryRowContext(ctx, `
SELECT
    statement_timestamp(),
    EXISTS (
        SELECT 1
        FROM service_principals AS principals
        WHERE principals.code = $1
          AND principals.status = 'active'
          AND NOT EXISTS (
              SELECT 1
              FROM service_principal_roles AS assignments
              WHERE assignments.service_principal_id = principals.id
          )
    ),
    COUNT(*) FILTER (WHERE processed_at IS NULL),
    COUNT(*) FILTER (
        WHERE processed_at IS NULL AND expires_at <= statement_timestamp()
    ),
    MIN(expires_at) FILTER (
        WHERE processed_at IS NULL AND expires_at <= statement_timestamp()
    ),
    COALESCE(MAX(attempts) FILTER (WHERE processed_at IS NULL), 0),
    (
        SELECT last_error
        FROM authorization_expiry_jobs
        WHERE processed_at IS NULL AND last_error <> ''
        ORDER BY updated_at DESC, id DESC
        LIMIT 1
    )
FROM authorization_expiry_jobs
`, authorizationExpiryCoordinatorCode).Scan(
		&stats.DatabaseTime,
		&stats.CoordinatorReady,
		&stats.Pending,
		&stats.Due,
		&oldestDue,
		&stats.MaxAttempts,
		&lastError,
	)
	if err != nil {
		return stats, err
	}
	if oldestDue.Valid {
		value := oldestDue.Time
		stats.OldestDueExpiresAt = &value
	}
	if lastError.Valid {
		stats.LastError = lastError.String
	}
	return stats, nil
}
