package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIQuotaAutoResetFinalizerPostgresConcurrentSameOutcome(t *testing.T) {
	db := newAuthzPolicyPostgresTestDatabase(t)
	db.SetMaxOpenConns(8)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, ApplyMigrations(ctx, db))

	fixture := newOpenAIAutoResetFinalizerPostgresRaceFixture(t, ctx, db, "same-outcome")
	results := runOpenAIAutoResetFinalizerPostgresRace(t, ctx, db, fixture.recordID, []openAIAutoResetFinalizerPostgresRaceCase{
		{winner: "success", input: fixture.input("success")},
		{winner: "success", input: fixture.input("success")},
	})

	require.Len(t, results, 2)
	for _, result := range results {
		require.NoError(t, result.err)
	}
	responseWinner, auditWinner := assertOpenAIAutoResetFinalizerPostgresRaceCommitted(t, ctx, db, fixture)
	require.Equal(t, "success", responseWinner)
	require.Equal(t, responseWinner, auditWinner)
}

func TestOpenAIQuotaAutoResetFinalizerPostgresConcurrentDifferentOutcomes(t *testing.T) {
	db := newAuthzPolicyPostgresTestDatabase(t)
	db.SetMaxOpenConns(8)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, ApplyMigrations(ctx, db))

	fixture := newOpenAIAutoResetFinalizerPostgresRaceFixture(t, ctx, db, "different-outcomes")
	results := runOpenAIAutoResetFinalizerPostgresRace(t, ctx, db, fixture.recordID, []openAIAutoResetFinalizerPostgresRaceCase{
		{winner: "success", input: fixture.input("success")},
		{winner: "no_credit", input: fixture.input("no_credit")},
	})

	var successfulWinner string
	conflictCount := 0
	for _, result := range results {
		switch {
		case result.err == nil:
			require.Empty(t, successfulWinner, "only one competing outcome may commit")
			successfulWinner = result.winner
		case errors.Is(result.err, service.ErrOpenAIQuotaAutoResetFinalizationConflict):
			conflictCount++
		default:
			require.NoError(t, result.err, "competitor %q returned an unexpected error", result.winner)
		}
	}
	require.NotEmpty(t, successfulWinner)
	require.Equal(t, 1, conflictCount)

	responseWinner, auditWinner := assertOpenAIAutoResetFinalizerPostgresRaceCommitted(t, ctx, db, fixture)
	require.Equal(t, successfulWinner, responseWinner)
	require.Equal(t, successfulWinner, auditWinner)
}

func TestOpenAIQuotaAutoResetFinalizerPostgresQueuesBehindMigrationTableLock(t *testing.T) {
	db := newAuthzPolicyPostgresTestDatabase(t)
	db.SetMaxOpenConns(8)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, ApplyMigrations(ctx, db))

	fixture := newOpenAIAutoResetFinalizerPostgresRaceFixture(t, ctx, db, "migration-table-lock")
	migrationTx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = migrationTx.Rollback() }()
	var migrationPID int
	require.NoError(t, migrationTx.QueryRowContext(ctx, `SELECT pg_backend_pid()`).Scan(&migrationPID))
	_, err = migrationTx.ExecContext(ctx, `LOCK TABLE idempotency_records IN SHARE ROW EXCLUSIVE MODE`)
	require.NoError(t, err)

	finalizationDone := make(chan error, 1)
	go func() {
		finalizationDone <- NewOpenAIQuotaAutoResetFinalizer(db).
			FinalizeOpenAIQuotaAutoReset(ctx, fixture.input("success"))
	}()
	waitForOpenAIAutoResetFinalizerPostgresMigrationTableLockWaiter(t, ctx, db, migrationPID)
	require.NoError(t, migrationTx.Commit())

	select {
	case finalizationErr := <-finalizationDone:
		require.NoError(t, finalizationErr)
	case <-ctx.Done():
		t.Fatalf("wait for OpenAI quota auto-reset finalizer after migration lock release: %v", ctx.Err())
	}
	responseWinner, auditWinner := assertOpenAIAutoResetFinalizerPostgresRaceCommitted(t, ctx, db, fixture)
	require.Equal(t, "success", responseWinner)
	require.Equal(t, responseWinner, auditWinner)
}

func TestOpenAIQuotaAutoResetFinalizerPostgresAtomicCommitAndExactRetry(t *testing.T) {
	db := newAuthzPolicyPostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, ApplyMigrations(ctx, db))

	var principalID int64
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT id
FROM service_principals
WHERE code = 'openai_quota_auto_reset_worker'`).Scan(&principalID))

	expiresAt := time.Now().UTC().Add(8 * 24 * time.Hour).Truncate(time.Microsecond)
	scope := openAIAutoResetIdempotencyScopePrefix + strconv.FormatInt(principalID, 10)
	fingerprint := openAIAutoResetFinalizerPostgresHash("atomic-fingerprint")
	keyHash := openAIAutoResetFinalizerPostgresHash("atomic-key")
	const requestID = "auto-reset-finalizer-atomic-request"

	var recordID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status, locked_until, expires_at
)
VALUES ($1, $2, $3, 'processing', statement_timestamp() + INTERVAL '1 minute', $4)
RETURNING id`, scope, keyHash, fingerprint, expiresAt).Scan(&recordID))

	input := &service.OpenAIQuotaAutoResetFinalization{
		AccountID:           42,
		IdempotencyRecordID: recordID,
		ActorQualifiedScope: scope,
		RequestFingerprint:  fingerprint,
		ResponseStatus:      200,
		ExpiresAt:           expiresAt,
		Audit: service.AuditLog{
			ActorServicePrincipalID: &principalID,
			AuthMethod:              service.AuditAuthMethodServicePrincipal,
			Action:                  service.AuditActionOpenAIQuotaAutoReset,
			Method:                  openAIAutoResetAuditMethod,
			Path:                    "/system/openai/accounts/42/auto-reset-credit",
			RequestID:               requestID,
		},
	}
	applyOpenAIAutoResetFinalizationTestOutcome(input, "no_credit")

	finalizer := NewOpenAIQuotaAutoResetFinalizer(db)
	require.NoError(t, finalizer.FinalizeOpenAIQuotaAutoReset(ctx, input))
	require.NoError(t, finalizer.FinalizeOpenAIQuotaAutoReset(ctx, input), "exact retry must acknowledge the prior commit")

	var status string
	var responseStatus int
	var responseBody string
	var storedExpiresAt time.Time
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT status, response_status, response_body, expires_at
FROM idempotency_records
WHERE id = $1`, recordID).Scan(&status, &responseStatus, &responseBody, &storedExpiresAt))
	require.Equal(t, service.IdempotencyStatusSucceeded, status)
	require.Equal(t, 200, responseStatus)
	require.Equal(t, canonicalOpenAIAutoResetNoCreditResponseBody, responseBody)
	require.True(t, storedExpiresAt.Equal(expiresAt))

	var auditCount int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM audit_logs
WHERE action = $1 AND request_id = $2`, service.AuditActionOpenAIQuotaAutoReset, requestID).Scan(&auditCount))
	require.Equal(t, 1, auditCount)

	mismatched := *input
	mismatched.Audit = input.Audit
	applyOpenAIAutoResetFinalizationTestOutcome(&mismatched, "success")
	err := finalizer.FinalizeOpenAIQuotaAutoReset(ctx, &mismatched)
	require.ErrorIs(t, err, service.ErrOpenAIQuotaAutoResetFinalizationConflict)
}

func TestOpenAIQuotaAutoResetFinalizerPostgresAuditConflictRollsBackState(t *testing.T) {
	db := newAuthzPolicyPostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, ApplyMigrations(ctx, db))

	var principalID int64
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT id FROM service_principals WHERE code = 'openai_quota_auto_reset_worker'`).Scan(&principalID))
	expiresAt := time.Now().UTC().Add(8 * 24 * time.Hour).Truncate(time.Microsecond)
	scope := openAIAutoResetIdempotencyScopePrefix + strconv.FormatInt(principalID, 10)
	fingerprint := openAIAutoResetFinalizerPostgresHash("conflict-fingerprint")
	keyHash := openAIAutoResetFinalizerPostgresHash("conflict-key")
	const requestID = "auto-reset-finalizer-conflict-request"

	var recordID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status, locked_until, expires_at
)
VALUES ($1, $2, $3, 'processing', statement_timestamp() + INTERVAL '1 minute', $4)
RETURNING id`, scope, keyHash, fingerprint, expiresAt).Scan(&recordID))
	mismatchedAuditExtra := openAIAutoResetFinalizationTestExtraJSON(
		t,
		openAIAutoResetFinalizationTestAuditExtra(999, "success", 2, ""),
	)
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO audit_logs (
    actor_service_principal_id, auth_method, action, method, path,
    request_id, status_code, extra
)
VALUES ($1, $2, $3, 'SYSTEM', '/system/openai/accounts/999/auto-reset-credit', $4, 200,
        $5::jsonb)
RETURNING id`, principalID, service.AuditAuthMethodServicePrincipal,
		service.AuditActionOpenAIQuotaAutoReset, requestID, mismatchedAuditExtra).Scan(new(int64)))

	input := &service.OpenAIQuotaAutoResetFinalization{
		AccountID:           42,
		IdempotencyRecordID: recordID,
		ActorQualifiedScope: scope,
		RequestFingerprint:  fingerprint,
		ResponseStatus:      200,
		ExpiresAt:           expiresAt,
		Audit: service.AuditLog{
			ActorServicePrincipalID: &principalID,
			AuthMethod:              service.AuditAuthMethodServicePrincipal,
			Action:                  service.AuditActionOpenAIQuotaAutoReset,
			Method:                  openAIAutoResetAuditMethod,
			Path:                    "/system/openai/accounts/42/auto-reset-credit",
			RequestID:               requestID,
		},
	}
	applyOpenAIAutoResetFinalizationTestOutcome(input, "success")

	err := NewOpenAIQuotaAutoResetFinalizer(db).FinalizeOpenAIQuotaAutoReset(ctx, input)
	require.ErrorIs(t, err, service.ErrOpenAIQuotaAutoResetFinalizationConflict)

	var status string
	var responseBody *string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT status, response_body
FROM idempotency_records
WHERE id = $1`, recordID).Scan(&status, &responseBody))
	require.Equal(t, service.IdempotencyStatusProcessing, status)
	require.Nil(t, responseBody)
}

func openAIAutoResetFinalizerPostgresHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

type openAIAutoResetFinalizerPostgresRaceFixture struct {
	principalID int64
	recordID    int64
	scope       string
	fingerprint string
	expiresAt   time.Time
	requestID   string
}

func newOpenAIAutoResetFinalizerPostgresRaceFixture(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	label string,
) *openAIAutoResetFinalizerPostgresRaceFixture {
	t.Helper()

	fixture := &openAIAutoResetFinalizerPostgresRaceFixture{
		expiresAt: time.Now().UTC().Add(8 * 24 * time.Hour).Truncate(time.Microsecond),
		requestID: "auto-reset-finalizer-race-" + label,
	}
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT id
FROM service_principals
WHERE code = 'openai_quota_auto_reset_worker'`).Scan(&fixture.principalID))
	fixture.scope = openAIAutoResetIdempotencyScopePrefix + strconv.FormatInt(fixture.principalID, 10)
	fixture.fingerprint = openAIAutoResetFinalizerPostgresHash(label + "-fingerprint")
	keyHash := openAIAutoResetFinalizerPostgresHash(label + "-key")
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status, locked_until, expires_at
)
VALUES ($1, $2, $3, 'processing', statement_timestamp() + INTERVAL '1 minute', $4)
RETURNING id`, fixture.scope, keyHash, fixture.fingerprint, fixture.expiresAt).Scan(&fixture.recordID))
	return fixture
}

func (f *openAIAutoResetFinalizerPostgresRaceFixture) input(resultCode string) *service.OpenAIQuotaAutoResetFinalization {
	input := &service.OpenAIQuotaAutoResetFinalization{
		AccountID:           f.recordID,
		IdempotencyRecordID: f.recordID,
		ActorQualifiedScope: f.scope,
		RequestFingerprint:  f.fingerprint,
		ResponseStatus:      200,
		ExpiresAt:           f.expiresAt,
		Audit: service.AuditLog{
			ActorServicePrincipalID: &f.principalID,
			AuthMethod:              service.AuditAuthMethodServicePrincipal,
			Action:                  service.AuditActionOpenAIQuotaAutoReset,
			Method:                  openAIAutoResetAuditMethod,
			Path:                    fmt.Sprintf("/system/openai/accounts/%d/auto-reset-credit", f.recordID),
			RequestID:               f.requestID,
		},
	}
	applyOpenAIAutoResetFinalizationTestOutcome(input, resultCode)
	return input
}

type openAIAutoResetFinalizerPostgresRaceCase struct {
	winner string
	input  *service.OpenAIQuotaAutoResetFinalization
}

type openAIAutoResetFinalizerPostgresRaceResult struct {
	winner string
	err    error
}

func runOpenAIAutoResetFinalizerPostgresRace(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	recordID int64,
	cases []openAIAutoResetFinalizerPostgresRaceCase,
) []openAIAutoResetFinalizerPostgresRaceResult {
	t.Helper()

	blocker, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer func() { _ = blocker.Rollback() }()
	var lockedRecordID int64
	require.NoError(t, blocker.QueryRowContext(ctx, `
SELECT id
FROM idempotency_records
WHERE id = $1
FOR UPDATE`, recordID).Scan(&lockedRecordID))
	require.Equal(t, recordID, lockedRecordID)

	start := make(chan struct{})
	resultChannel := make(chan openAIAutoResetFinalizerPostgresRaceResult, len(cases))
	finalizer := NewOpenAIQuotaAutoResetFinalizer(db)
	for _, raceCase := range cases {
		raceCase := raceCase
		go func() {
			<-start
			resultChannel <- openAIAutoResetFinalizerPostgresRaceResult{
				winner: raceCase.winner,
				err:    finalizer.FinalizeOpenAIQuotaAutoReset(ctx, raceCase.input),
			}
		}()
	}
	close(start)
	waitForOpenAIAutoResetFinalizerPostgresLockWaiters(t, ctx, db, len(cases))
	require.NoError(t, blocker.Commit())

	results := make([]openAIAutoResetFinalizerPostgresRaceResult, 0, len(cases))
	for range cases {
		select {
		case result := <-resultChannel:
			results = append(results, result)
		case <-ctx.Done():
			t.Fatalf("collect OpenAI quota auto-reset finalizer race results: %v", ctx.Err())
		}
	}
	return results
}

func waitForOpenAIAutoResetFinalizerPostgresLockWaiters(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	want int,
) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		var waiting int
		err := db.QueryRowContext(waitCtx, `
SELECT COUNT(*)
FROM pg_stat_activity
WHERE datname = current_database()
  AND pid <> pg_backend_pid()
  AND state = 'active'
  AND wait_event_type = 'Lock'
  AND query LIKE '%FROM idempotency_records%'
  AND query LIKE '%expires_at = $2%'`).Scan(&waiting)
		require.NoError(t, err)
		if waiting >= want {
			return
		}
		select {
		case <-ticker.C:
		case <-waitCtx.Done():
			t.Fatalf("wait for %d OpenAI quota auto-reset finalizer lock waiters: %v", want, waitCtx.Err())
		}
	}
}

func waitForOpenAIAutoResetFinalizerPostgresMigrationTableLockWaiter(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	migrationPID int,
) {
	t.Helper()
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		var waiting int
		err := db.QueryRowContext(waitCtx, `
SELECT COUNT(*)
FROM pg_stat_activity AS activity
WHERE activity.datname = current_database()
  AND activity.pid <> pg_backend_pid()
  AND activity.state = 'active'
  AND activity.wait_event_type = 'Lock'
  AND activity.query LIKE '%LOCK TABLE idempotency_records IN ROW EXCLUSIVE MODE%'
  AND $1 = ANY (pg_blocking_pids(activity.pid))`, migrationPID).Scan(&waiting)
		require.NoError(t, err)
		if waiting >= 1 {
			return
		}
		select {
		case <-ticker.C:
		case <-waitCtx.Done():
			t.Fatalf("finalizer did not queue behind migration table lock held by pid %d: %v", migrationPID, waitCtx.Err())
		}
	}
}

func assertOpenAIAutoResetFinalizerPostgresRaceCommitted(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	fixture *openAIAutoResetFinalizerPostgresRaceFixture,
) (string, string) {
	t.Helper()

	var (
		status         string
		responseStatus sql.NullInt64
		responseBody   sql.NullString
		errorReason    sql.NullString
		lockedUntil    sql.NullTime
	)
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT status, response_status, response_body, error_reason, locked_until
FROM idempotency_records
WHERE id = $1`, fixture.recordID).Scan(
		&status,
		&responseStatus,
		&responseBody,
		&errorReason,
		&lockedUntil,
	))
	require.Equal(t, service.IdempotencyStatusSucceeded, status)
	require.True(t, responseStatus.Valid)
	require.Equal(t, int64(200), responseStatus.Int64)
	require.True(t, responseBody.Valid)
	require.False(t, errorReason.Valid, "committed record must not retain a terminal error")
	require.False(t, lockedUntil.Valid, "committed record must release its processing lease")

	var response struct {
		ResultCode string `json:"result_code"`
	}
	require.NoError(t, json.Unmarshal([]byte(responseBody.String), &response))
	require.Contains(t, []string{"success", "no_credit"}, response.ResultCode)

	var (
		auditCount  int
		auditWinner sql.NullString
	)
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*), MIN(extra->>'result_code')
FROM audit_logs
WHERE action = $1 AND request_id = $2`,
		service.AuditActionOpenAIQuotaAutoReset,
		fixture.requestID,
	).Scan(&auditCount, &auditWinner))
	require.Equal(t, 1, auditCount)
	require.True(t, auditWinner.Valid)
	require.NotEmpty(t, auditWinner.String)
	return response.ResultCode, auditWinner.String
}
