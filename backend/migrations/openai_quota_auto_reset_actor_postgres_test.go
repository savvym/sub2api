//go:build integration

package migrations_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

const (
	openAIAutoResetWorkerCode       = "openai_quota_auto_reset_worker"
	openAIAutoResetPermissionCode   = "platform.account.openai_quota_auto_reset"
	openAIAutoResetPermissionDetail = "Query and consume OpenAI quota reset credits and recover the same account"
)

func TestOpenAIQuotaAutoResetActorMigrationPostgres(t *testing.T) {
	db := newRoleCachePostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	applyErr := repository.ApplyMigrations(ctx, db)
	if applyErr != nil {
		var postgresErr *pq.Error
		if errors.As(applyErr, &postgresErr) {
			t.Fatalf("apply migrations: %v (position=%s where=%s detail=%s)",
				applyErr, postgresErr.Position, postgresErr.Where, postgresErr.Detail)
		}
	}
	require.NoError(t, applyErr)

	migrationSQL, err := dbmigrations.FS.ReadFile("243_openai_quota_auto_reset_actor.sql")
	require.NoError(t, err)

	principalID, permissionID, initialVersion := readOpenAIAutoResetWorker(t, ctx, db)
	require.Greater(t, initialVersion, int64(1), "the initial direct grant must advance authz_version")
	assertOpenAIAutoResetWorkerTableShape(t, ctx, db)
	assertOpenAIAutoResetWorkerGrantShape(t, ctx, db, principalID, permissionID)
	assertOpenAIAutoResetAuditIndexShape(t, ctx, db)
	assertOpenAIAutoResetLegacyScopeFenceShape(t, ctx, db)
	assertOpenAIAutoResetProtectionShape(t, ctx, db)

	t.Run("exact reapply is a no-op", func(t *testing.T) {
		_, reapplyErr := db.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, reapplyErr)

		reappliedPrincipalID, reappliedPermissionID, reappliedVersion := readOpenAIAutoResetWorker(t, ctx, db)
		require.Equal(t, principalID, reappliedPrincipalID)
		require.Equal(t, permissionID, reappliedPermissionID)
		require.Equal(t, initialVersion, reappliedVersion)
		assertOpenAIAutoResetWorkerGrantShape(t, ctx, db, principalID, permissionID)
		assertOpenAIAutoResetAuditIndexShape(t, ctx, db)
		assertOpenAIAutoResetLegacyScopeFenceShape(t, ctx, db)
		assertOpenAIAutoResetProtectionShape(t, ctx, db)
	})

	t.Run("raw reapply removes legacy definer overloads and restores owner-only ACLs", func(t *testing.T) {
		roleName := fmt.Sprintf("migration_243_acl_%d", time.Now().UnixNano())
		quotedRole := pq.QuoteIdentifier(roleName)
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, "CREATE ROLE "+quotedRole))
		roleDropped := false
		t.Cleanup(func() {
			if roleDropped {
				return
			}
			_, _ = db.ExecContext(context.Background(),
				"ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM "+quotedRole)
			_, _ = db.ExecContext(context.Background(), "DROP ROLE IF EXISTS "+quotedRole)
		})

		require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
			"ALTER DEFAULT PRIVILEGES GRANT EXECUTE ON FUNCTIONS TO "+quotedRole))
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
CREATE FUNCTION reconcile_openai_quota_auto_reset_protected_attempt(
    BIGINT, TEXT, TEXT, BIGINT, INTEGER, TEXT, TIMESTAMPTZ, TIMESTAMPTZ,
    TEXT, INTEGER, JSONB
)
RETURNS VOID
LANGUAGE plpgsql
SECURITY DEFINER
AS $$ BEGIN NULL; END $$`))
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
ALTER FUNCTION reconcile_openai_quota_auto_reset_protected_attempt(
    BIGINT, TEXT, TEXT, BIGINT, TIMESTAMPTZ, TEXT, INTEGER, JSONB
) SECURITY DEFINER`))
		for _, signature := range []string{
			"reconcile_openai_quota_auto_reset_protected_attempt(bigint,text,text,bigint,timestamptz,text,integer,jsonb)",
			"discard_openai_quota_auto_reset_protected_attempt_no_effect(bigint,text,text,bigint,timestamptz,text,jsonb,boolean)",
		} {
			require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
				"GRANT EXECUTE ON FUNCTION "+signature+" TO PUBLIC, "+quotedRole))
		}

		_, reapplyErr := db.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, reapplyErr)
		assertOpenAIAutoResetProtectionShape(t, ctx, db)
		var oldDefinerExists bool
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT to_regprocedure(
    'reconcile_openai_quota_auto_reset_protected_attempt(bigint,text,text,bigint,integer,text,timestamptz,timestamptz,text,integer,jsonb)'
) IS NOT NULL`).Scan(&oldDefinerExists))
		require.False(t, oldDefinerExists)
		for _, signature := range []string{
			"reconcile_openai_quota_auto_reset_protected_attempt(bigint,text,text,bigint,timestamptz,text,integer,jsonb)",
			"discard_openai_quota_auto_reset_protected_attempt_no_effect(bigint,text,text,bigint,timestamptz,text,jsonb,boolean)",
		} {
			var roleCanExecute bool
			require.NoError(t, db.QueryRowContext(ctx,
				`SELECT has_function_privilege($1, $2, 'EXECUTE')`, roleName, signature,
			).Scan(&roleCanExecute))
			require.False(t, roleCanExecute)
		}

		require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
			"ALTER DEFAULT PRIVILEGES REVOKE EXECUTE ON FUNCTIONS FROM "+quotedRole))
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, "DROP ROLE "+quotedRole))
		roleDropped = true
	})

	t.Run("unknown reconciliation overload fails closed and rolls back business changes", func(t *testing.T) {
		const unknownSignature = "reconcile_openai_quota_auto_reset_protected_attempt(text)"
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
CREATE FUNCTION reconcile_openai_quota_auto_reset_protected_attempt(TEXT)
RETURNS VOID
LANGUAGE plpgsql
SECURITY DEFINER
AS $$ BEGIN NULL; END $$`))
		unknownDropped := false
		var candidateRecordID int64
		candidateCleaned := false
		t.Cleanup(func() {
			if !candidateCleaned && candidateRecordID > 0 {
				cleanupOpenAIAutoResetProtectedRecord(t, context.Background(), db, candidateRecordID)
			}
			if !unknownDropped {
				_, _ = db.ExecContext(context.Background(), "DROP FUNCTION IF EXISTS "+unknownSignature)
				_, _ = db.ExecContext(context.Background(), string(migrationSQL))
			}
		})

		require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
			`DELETE FROM openai_quota_auto_reset_protection_backfill WHERE completed`))
		restoreFence := disableOpenAIAutoResetLegacyScopeFence(t, ctx, db)
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    locked_until, expires_at
)
VALUES (
    'openai_auto_reset_credit|account:4500', repeat('4', 64), repeat('5', 64),
    'processing', statement_timestamp() + INTERVAL '5 minutes',
    statement_timestamp() + INTERVAL '1 day'
)
RETURNING id`).Scan(&candidateRecordID))
		restoreFence()
		versionBeforeReapply := readOpenAIAutoResetWorkerVersion(t, ctx, db, principalID)
		_, reapplyErr := db.ExecContext(ctx, string(migrationSQL))
		require.ErrorContains(t, reapplyErr,
			"unsafe OpenAI quota auto-reset reconciliation function overload")
		require.Equal(t, versionBeforeReapply,
			readOpenAIAutoResetWorkerVersion(t, ctx, db, principalID))
		var sentinelCount int
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM openai_quota_auto_reset_protection_backfill WHERE completed`,
		).Scan(&sentinelCount))
		require.Zero(t, sentinelCount,
			"the one-time sentinel inserted before overload validation must roll back")
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, candidateRecordID, false)
		var candidateScope, candidateStatus string
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT scope, status FROM idempotency_records WHERE id = $1`, candidateRecordID,
		).Scan(&candidateScope, &candidateStatus))
		require.Equal(t, "openai_auto_reset_credit|account:4500", candidateScope)
		require.Equal(t, "processing", candidateStatus)

		cleanupOpenAIAutoResetProtectedRecord(t, ctx, db, candidateRecordID)
		candidateCleaned = true
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
			"DROP FUNCTION "+unknownSignature))
		unknownDropped = true
		_, hardenedReapplyErr := db.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, hardenedReapplyErr)
		assertOpenAIAutoResetWorkerGrantShape(t, ctx, db, principalID, permissionID)
		assertOpenAIAutoResetProtectionShape(t, ctx, db)
	})

	t.Run("raw reapply repairs the expiry guard trigger", func(t *testing.T) {
		const triggerName = "idempotency_records_expiry_delete_guard"
		mutations := []struct {
			name string
			sql  string
		}{
			{name: "disabled", sql: "ALTER TABLE idempotency_records DISABLE TRIGGER " + triggerName},
			{name: "dropped", sql: "DROP TRIGGER " + triggerName + " ON idempotency_records"},
			{
				name: "wrong binding",
				sql: `
DROP TRIGGER idempotency_records_expiry_delete_guard ON idempotency_records;
CREATE TRIGGER idempotency_records_expiry_delete_guard
BEFORE DELETE ON idempotency_records
FOR EACH ROW
EXECUTE FUNCTION guard_openai_quota_auto_reset_protected_attempt()`,
			},
		}
		for _, mutation := range mutations {
			t.Run(mutation.name, func(t *testing.T) {
				require.NoError(t, execOpenAIAutoResetStatement(ctx, db, mutation.sql))
				_, reapplyErr := db.ExecContext(ctx, string(migrationSQL))
				require.NoError(t, reapplyErr)
				assertOpenAIAutoResetProtectionShape(t, ctx, db)
			})
		}
	})

	t.Run("raw reapply serializes with success and discard reconciliation", func(t *testing.T) {
		canonicalScope := fmt.Sprintf(
			"openai_auto_reset_credit|service_principal:%d", principalID,
		)

		t.Run("migration lock precedes success", func(t *testing.T) {
			const accountID = int64(4601)
			fingerprint := strings.Repeat("6", 64)
			var recordID int64
			require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    locked_until, expires_at
)
VALUES (
    $1, repeat('6', 63) || '1', $2, 'processing',
    statement_timestamp() + INTERVAL '5 minutes',
    statement_timestamp() + INTERVAL '1 day'
)
RETURNING id`, canonicalScope, fingerprint).Scan(&recordID))
			require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO openai_quota_auto_reset_protected_attempts (idempotency_record_id, account_id)
VALUES ($1, $2)`, recordID, accountID))
			requestID := "reconcile-success:" + strconv.FormatInt(recordID, 10)
			createdAt := time.Now().UTC().Truncate(time.Microsecond)
			extra := openAIAutoResetReconciliationAuditExtra(
				accountID,
				recordID,
				fingerprint,
				"reconciled_success",
				1,
				"operator-ticket:reapply-race-success",
				"migration-test",
			)

			migrationConn, migrationConnErr := db.Conn(ctx)
			require.NoError(t, migrationConnErr)
			defer migrationConn.Close()
			successConn, successConnErr := db.Conn(ctx)
			require.NoError(t, successConnErr)
			defer successConn.Close()
			var migrationPID, successPID int
			require.NoError(t, migrationConn.QueryRowContext(ctx,
				`SELECT pg_backend_pid()`,
			).Scan(&migrationPID))
			require.NoError(t, successConn.QueryRowContext(ctx,
				`SELECT pg_backend_pid()`,
			).Scan(&successPID))

			migrationTx, migrationTxErr := migrationConn.BeginTx(ctx, nil)
			require.NoError(t, migrationTxErr)
			migrationCommitted := false
			defer func() {
				if !migrationCommitted {
					_ = migrationTx.Rollback()
				}
			}()
			_, lockErr := migrationTx.ExecContext(ctx,
				`LOCK TABLE idempotency_records IN SHARE ROW EXCLUSIVE MODE`)
			require.NoError(t, lockErr)

			successResult := make(chan error, 1)
			go func() {
				_, reconcileErr := successConn.ExecContext(ctx, `
SELECT reconcile_openai_quota_auto_reset_protected_attempt(
    $1, $2, $3, $4, $5, $6, 200, $7::jsonb
)`, recordID, canonicalScope, fingerprint, accountID, createdAt, requestID, extra)
				successResult <- reconcileErr
			}()
			var blockingQueryErr error
			require.Eventually(t, func() bool {
				var isBlocked bool
				blockingQueryErr = db.QueryRowContext(ctx,
					`SELECT $1 = ANY(pg_blocking_pids($2))`, migrationPID, successPID,
				).Scan(&isBlocked)
				return blockingQueryErr == nil && isBlocked
			}, 5*time.Second, 20*time.Millisecond)
			require.NoError(t, blockingQueryErr)

			_, reapplyErr := migrationTx.ExecContext(ctx, string(migrationSQL))
			require.NoError(t, reapplyErr)
			require.NoError(t, migrationTx.Commit())
			migrationCommitted = true
			select {
			case reconcileErr := <-successResult:
				require.NoError(t, reconcileErr)
			case <-time.After(5 * time.Second):
				require.Fail(t, "success reconciliation remained blocked after migration commit")
			}

			var status string
			require.NoError(t, db.QueryRowContext(ctx,
				`SELECT status FROM idempotency_records WHERE id = $1`, recordID,
			).Scan(&status))
			require.Equal(t, "succeeded", status)
			cleanupOpenAIAutoResetProtectedRecord(t, ctx, db, recordID)
			require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
DELETE FROM audit_logs
WHERE action = 'system.openai.reset_credit.auto' AND request_id = $1`, requestID))
		})

		t.Run("discard lock precedes migration", func(t *testing.T) {
			const accountID = int64(4602)
			fingerprint := strings.Repeat("7", 64)
			var recordID int64
			require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    locked_until, expires_at
)
VALUES (
    $1, repeat('7', 63) || '2', $2, 'processing',
    statement_timestamp() + INTERVAL '5 minutes',
    statement_timestamp() + INTERVAL '1 day'
)
RETURNING id`, canonicalScope, fingerprint).Scan(&recordID))
			require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO openai_quota_auto_reset_protected_attempts (idempotency_record_id, account_id)
VALUES ($1, $2)`, recordID, accountID))
			requestID := "reconcile-no-effect:" + strconv.FormatInt(recordID, 10)
			createdAt := time.Now().UTC().Truncate(time.Microsecond)
			extra := openAIAutoResetReconciliationAuditExtra(
				accountID,
				recordID,
				fingerprint,
				"reconciled_no_effect",
				0,
				"operator-ticket:reapply-race-discard",
				"migration-test",
			)

			discardConn, discardConnErr := db.Conn(ctx)
			require.NoError(t, discardConnErr)
			defer discardConn.Close()
			migrationConn, migrationConnErr := db.Conn(ctx)
			require.NoError(t, migrationConnErr)
			defer migrationConn.Close()
			var discardPID, migrationPID int
			require.NoError(t, discardConn.QueryRowContext(ctx,
				`SELECT pg_backend_pid()`,
			).Scan(&discardPID))
			require.NoError(t, migrationConn.QueryRowContext(ctx,
				`SELECT pg_backend_pid()`,
			).Scan(&migrationPID))

			discardTx, discardTxErr := discardConn.BeginTx(ctx, nil)
			require.NoError(t, discardTxErr)
			discardCommitted := false
			defer func() {
				if !discardCommitted {
					_ = discardTx.Rollback()
				}
			}()
			_, discardErr := discardTx.ExecContext(ctx, `
SELECT discard_openai_quota_auto_reset_protected_attempt_no_effect(
    $1, $2, $3, $4, $5, $6, $7::jsonb, true
)`, recordID, canonicalScope, fingerprint, accountID, createdAt, requestID, extra)
			require.NoError(t, discardErr)

			migrationResult := make(chan error, 1)
			go func() {
				_, reapplyErr := migrationConn.ExecContext(ctx, string(migrationSQL))
				migrationResult <- reapplyErr
			}()
			var blockingQueryErr error
			require.Eventually(t, func() bool {
				var isBlocked bool
				blockingQueryErr = db.QueryRowContext(ctx,
					`SELECT $1 = ANY(pg_blocking_pids($2))`, discardPID, migrationPID,
				).Scan(&isBlocked)
				return blockingQueryErr == nil && isBlocked
			}, 5*time.Second, 20*time.Millisecond)
			require.NoError(t, blockingQueryErr)
			require.NoError(t, discardTx.Commit())
			discardCommitted = true

			select {
			case reapplyErr := <-migrationResult:
				require.NoError(t, reapplyErr)
			case <-time.After(5 * time.Second):
				require.Fail(t, "raw reapply remained blocked after discard commit")
			}
			var parentExists bool
			require.NoError(t, db.QueryRowContext(ctx,
				`SELECT EXISTS (SELECT 1 FROM idempotency_records WHERE id = $1)`, recordID,
			).Scan(&parentExists))
			require.False(t, parentExists)
			require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
DELETE FROM audit_logs
WHERE action = 'system.openai.reset_credit.auto' AND request_id = $1`, requestID))
		})
	})

	t.Run("legacy account scope writes fail closed while current scopes remain valid", func(t *testing.T) {
		const legacyScope = "openai_auto_reset_credit|account:404"
		insertLegacy := func() error {
			_, insertErr := db.ExecContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    response_status, response_body, expires_at
)
VALUES ($1, repeat('4', 64), repeat('5', 64), 'succeeded',
        200, '{}', statement_timestamp() + INTERVAL '1 day')`, legacyScope)
			return insertErr
		}
		require.ErrorContains(t, insertLegacy(), "legacy OpenAI quota auto-reset account scope is fenced")

		canonicalScope := "openai_auto_reset_credit|service_principal:" + openAIAutoResetInt64(principalID)
		var rawFenceID, canonicalID, unrelatedID int64
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    response_status, response_body, expires_at
)
VALUES ('openai_auto_reset_credit', repeat('4', 63) || '6',
        'upgrade-fence:actor-qualified:v1', 'succeeded',
        200, '{}', statement_timestamp() + INTERVAL '1 day')
RETURNING id`).Scan(&rawFenceID))
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    response_status, response_body, expires_at
)
VALUES ($1, repeat('4', 63) || '7', repeat('5', 63) || '7', 'succeeded',
        200, '{}', statement_timestamp() + INTERVAL '1 day')
RETURNING id`, canonicalScope).Scan(&canonicalID))
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    response_status, response_body, expires_at
)
VALUES ('unrelated_operation|account:404', repeat('4', 63) || '8',
        repeat('5', 63) || '8', 'succeeded',
        200, '{}', statement_timestamp() + INTERVAL '1 day')
RETURNING id`).Scan(&unrelatedID))
		t.Cleanup(func() {
			_, _ = db.ExecContext(context.Background(), `
UPDATE idempotency_records
SET expires_at = statement_timestamp() - INTERVAL '1 second'
WHERE id IN ($1, $2, $3)`, rawFenceID, canonicalID, unrelatedID)
			_, _ = db.ExecContext(context.Background(),
				`DELETE FROM idempotency_records WHERE id IN ($1, $2, $3)`,
				rawFenceID, canonicalID, unrelatedID,
			)
		})

		_, legacyUpdateErr := db.ExecContext(ctx, `
UPDATE idempotency_records
SET scope = 'openai_auto_reset_credit|account:405'
WHERE id = $1`, unrelatedID)
		require.ErrorContains(t, legacyUpdateErr, "legacy OpenAI quota auto-reset account scope is fenced")
		assertOpenAIAutoResetIdempotencyRecord(
			t,
			ctx,
			db,
			unrelatedID,
			"unrelated_operation|account:404",
			strings.Repeat("5", 63)+"8",
		)

		_, reapplyErr := db.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, reapplyErr)
		assertOpenAIAutoResetLegacyScopeFenceShape(t, ctx, db)
		require.ErrorContains(t, insertLegacy(), "legacy OpenAI quota auto-reset account scope is fenced")
	})

	t.Run("auto-reset audit request identity is unique only when non-empty", func(t *testing.T) {
		const requestID = "migration-243-audit-request"
		var firstAuditID int64
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO audit_logs (
    actor_service_principal_id, auth_method, action, method, path,
    request_id, status_code, extra
)
VALUES ($1, 'service_principal', 'system.openai.reset_credit.auto', 'SYSTEM',
        '/system/openai/accounts/1/auto-reset-credit', $2, 200, '{"account_id":1}'::jsonb)
RETURNING id`, principalID, requestID).Scan(&firstAuditID))
		t.Cleanup(func() {
			_, _ = db.ExecContext(context.Background(), `
DELETE FROM audit_logs
WHERE id = $1 OR (action = 'system.openai.reset_credit.auto' AND request_id = '')`, firstAuditID)
		})

		_, duplicateErr := db.ExecContext(ctx, `
INSERT INTO audit_logs (
    actor_service_principal_id, auth_method, action, request_id
)
VALUES ($1, 'service_principal', 'system.openai.reset_credit.auto', $2)`, principalID, requestID)
		require.Error(t, duplicateErr)

		_, emptyFirstErr := db.ExecContext(ctx, `
INSERT INTO audit_logs (
    actor_service_principal_id, auth_method, action, request_id
)
VALUES ($1, 'service_principal', 'system.openai.reset_credit.auto', '')`, principalID)
		require.NoError(t, emptyFirstErr)
		_, emptySecondErr := db.ExecContext(ctx, `
INSERT INTO audit_logs (
    actor_service_principal_id, auth_method, action, request_id
)
VALUES ($1, 'service_principal', 'system.openai.reset_credit.auto', '')`, principalID)
		require.NoError(t, emptySecondErr)
	})

	t.Run("old scopes move and ambiguous retryable outcomes freeze", func(t *testing.T) {
		type legacyRecord struct {
			id          int64
			keyHash     string
			fingerprint string
			status      string
			wantStatus  string
			accountID   string
			expired     bool
		}
		records := []legacyRecord{
			{
				keyHash:     strings.Repeat("a", 64),
				fingerprint: strings.Repeat("b", 64),
				status:      "succeeded",
				wantStatus:  "succeeded",
				accountID:   "101",
			},
			{
				keyHash:     strings.Repeat("c", 64),
				fingerprint: strings.Repeat("d", 64),
				status:      "processing",
				wantStatus:  "processing",
				accountID:   "202",
				expired:     true,
			},
			{
				keyHash:     strings.Repeat("7", 64),
				fingerprint: strings.Repeat("8", 64),
				status:      "failed_retryable",
				wantStatus:  "processing",
				accountID:   "203",
			},
		}

		// ApplyMigrations already ran 243 against an empty database. Remove only
		// the one-time test gate so this subtest exercises a real pre-migration
		// in-flight snapshot; the migration recreates it transactionally.
		_, resetBackfillErr := db.ExecContext(ctx,
			`DELETE FROM openai_quota_auto_reset_protection_backfill WHERE completed`)
		require.NoError(t, resetBackfillErr)

		restoreFence := disableOpenAIAutoResetLegacyScopeFence(t, ctx, db)
		defer restoreFence()
		for index := range records {
			record := &records[index]
			require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope,
    idempotency_key_hash,
    request_fingerprint,
    status,
    response_status,
	    response_body,
	    error_reason,
	    locked_until,
    expires_at
)
VALUES (
    'openai_auto_reset_credit|account:' || $1,
    $2,
    $3,
    $4::VARCHAR,
	    CASE WHEN $4::TEXT = 'succeeded' THEN 200 ELSE NULL END,
	    CASE WHEN $4::TEXT = 'succeeded' THEN '{"windows_reset":1}' ELSE NULL END,
	    CASE WHEN $4::TEXT = 'failed_retryable' THEN 'AMBIGUOUS_TIMEOUT' ELSE NULL END,
	    CASE WHEN $4::TEXT IN ('processing', 'failed_retryable') THEN statement_timestamp() + INTERVAL '5 minutes' ELSE NULL END,
		    CASE WHEN $5::BOOLEAN
		         THEN statement_timestamp() - INTERVAL '1 minute'
		         ELSE statement_timestamp() + INTERVAL '1 day'
		    END
		)
		RETURNING id`, record.accountID, record.keyHash, record.fingerprint, record.status, record.expired).Scan(&record.id))
		}
		restoreFence()

		var rawFenceID, rawFailedID, rawFenceFailedID int64
		var directPrincipalFailedID, otherPrincipalFailedID int64
		rawAccountID := insertOpenAIAutoResetAttemptAccount(
			t, ctx, db, "raw-provenance", strings.Repeat("a", 24), strings.Repeat("b", 24),
		)
		directPrincipalAccountID := insertOpenAIAutoResetAttemptAccount(
			t, ctx, db, "direct-principal-provenance", strings.Repeat("c", 24), strings.Repeat("d", 24),
		)
		otherPrincipalAccountID := insertOpenAIAutoResetAttemptAccount(
			t, ctx, db, "other-principal-provenance", strings.Repeat("e", 24), strings.Repeat("f", 24),
		)
		rawKeyHash := openAIAutoResetStableKeyHash(
			rawAccountID, strings.Repeat("a", 24), strings.Repeat("b", 24),
		)
		rawFingerprint := openAIAutoResetFingerprint(
			fmt.Sprintf("account:%d", rawAccountID),
			rawAccountID,
			strings.Repeat("a", 24),
			strings.Repeat("b", 24),
		)
		directPrincipalKeyHash := openAIAutoResetStableKeyHash(
			directPrincipalAccountID, strings.Repeat("c", 24), strings.Repeat("d", 24),
		)
		directPrincipalFingerprint := openAIAutoResetFingerprint(
			fmt.Sprintf("service_principal:%d", principalID),
			directPrincipalAccountID,
			strings.Repeat("c", 24),
			strings.Repeat("d", 24),
		)
		otherPrincipalKeyHash := openAIAutoResetStableKeyHash(
			otherPrincipalAccountID, strings.Repeat("e", 24), strings.Repeat("f", 24),
		)
		otherPrincipalFingerprint := openAIAutoResetFingerprint(
			fmt.Sprintf("account:%d", otherPrincipalAccountID),
			otherPrincipalAccountID,
			strings.Repeat("e", 24),
			strings.Repeat("f", 24),
		)
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status, locked_until, expires_at
)
VALUES (
    'openai_auto_reset_credit',
    repeat('e', 64),
    'upgrade-fence:actor-qualified:v1',
    'processing',
    statement_timestamp() + INTERVAL '5 minutes',
    statement_timestamp() + INTERVAL '1 day'
		)
		RETURNING id`).Scan(&rawFenceID))
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    error_reason, locked_until, expires_at
		)
		VALUES (
		    'openai_auto_reset_credit', $1, $2,
		    'failed_retryable', 'AMBIGUOUS_TIMEOUT', statement_timestamp() - INTERVAL '1 minute',
		    statement_timestamp() + INTERVAL '1 day'
		)
		RETURNING id`, rawKeyHash, rawFingerprint).Scan(&rawFailedID))
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    error_reason, locked_until, expires_at
)
VALUES (
    'openai_auto_reset_credit', repeat('0', 64), 'upgrade-fence:actor-qualified:v1',
    'failed_retryable', 'FENCE_RETRY', statement_timestamp() - INTERVAL '1 minute',
    statement_timestamp() + INTERVAL '1 day'
)
RETURNING id`).Scan(&rawFenceFailedID))
		wantScope := "openai_auto_reset_credit|service_principal:" + openAIAutoResetInt64(principalID)
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    error_reason, locked_until, expires_at
)
			VALUES (
			    $1, $2, $3,
			    'failed_retryable', 'AMBIGUOUS_TIMEOUT', statement_timestamp() - INTERVAL '1 minute',
			    statement_timestamp() + INTERVAL '1 day'
			)
			RETURNING id`, wantScope, directPrincipalKeyHash, directPrincipalFingerprint).Scan(&directPrincipalFailedID))
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    error_reason, locked_until, expires_at
)
		VALUES (
		    'openai_auto_reset_credit|service_principal:987654321',
			    $1, $2,
			    'failed_retryable', 'AMBIGUOUS_TIMEOUT', statement_timestamp() - INTERVAL '1 minute',
			    statement_timestamp() + INTERVAL '1 day'
			)
			RETURNING id`, otherPrincipalKeyHash, otherPrincipalFingerprint).Scan(&otherPrincipalFailedID))

		beforeVersion := readOpenAIAutoResetWorkerVersion(t, ctx, db, principalID)
		_, reapplyErr := db.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, reapplyErr)
		require.Equal(t, beforeVersion, readOpenAIAutoResetWorkerVersion(t, ctx, db, principalID))
		assertOpenAIAutoResetLegacyScopeFenceShape(t, ctx, db)

		for _, record := range records {
			var scope, fingerprint, status string
			var errorReason sql.NullString
			var lockedUntil sql.NullTime
			require.NoError(t, db.QueryRowContext(ctx, `
SELECT scope, request_fingerprint, status, error_reason, locked_until
FROM idempotency_records
WHERE id = $1`, record.id).Scan(&scope, &fingerprint, &status, &errorReason, &lockedUntil))
			require.Equal(t, wantScope, scope)
			require.Equal(t, record.fingerprint, fingerprint)
			require.Equal(t, record.wantStatus, status)
			if record.status == "failed_retryable" {
				require.False(t, errorReason.Valid)
				require.False(t, lockedUntil.Valid)
			}
			assertOpenAIAutoResetAttemptProtected(
				t,
				ctx,
				db,
				record.id,
				record.status != "succeeded",
			)
		}

		// These are the generic status-only statements held by an old worker
		// that claimed before migration 243. Neither is allowed to bypass the
		// new atomic audit finalizer or turn an ambiguous result retryable.
		_, oldSucceededErr := db.ExecContext(ctx, `
		UPDATE idempotency_records
		SET status = 'succeeded',
		    response_status = 200,
		    response_body = '{"windows_reset":1}',
		    error_reason = NULL,
		    locked_until = NULL,
		    expires_at = statement_timestamp() + INTERVAL '1 day',
		    updated_at = NOW()
		WHERE id = $1`, records[1].id)
		require.ErrorContains(t, oldSucceededErr,
			"protected OpenAI quota auto-reset attempt requires explicit reconciliation")

		_, oldFailedRetryableErr := db.ExecContext(ctx, `
		UPDATE idempotency_records
		SET status = 'failed_retryable',
		    error_reason = 'LATE_OLD_WORKER_TIMEOUT',
		    locked_until = statement_timestamp() + INTERVAL '1 minute',
		    expires_at = statement_timestamp() + INTERVAL '1 day',
		    updated_at = NOW()
		WHERE id = $1`, records[2].id)
		require.ErrorContains(t, oldFailedRetryableErr,
			"protected OpenAI quota auto-reset attempt requires explicit reconciliation")

		var protectedStatus string
		var protectedResponseStatus sql.NullInt64
		var protectedResponseBody, protectedErrorReason sql.NullString
		require.NoError(t, db.QueryRowContext(ctx, `
		SELECT status, response_status, response_body, error_reason
		FROM idempotency_records
		WHERE id = $1`, records[1].id).Scan(
			&protectedStatus,
			&protectedResponseStatus,
			&protectedResponseBody,
			&protectedErrorReason,
		))
		require.Equal(t, "processing", protectedStatus)
		require.False(t, protectedResponseStatus.Valid)
		require.False(t, protectedResponseBody.Valid)
		require.False(t, protectedErrorReason.Valid)
		assertOpenAIAutoResetFrozenRecord(t, ctx, db, records[2].id, wantScope)

		var auditCount int
		require.NoError(t, db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM audit_logs
		WHERE action = 'system.openai.reset_credit.auto'
		  AND path = '/system/openai/accounts/202/auto-reset-credit'`).Scan(&auditCount))
		require.Zero(t, auditCount)

		restoreExpiryGuard := disableOpenAIAutoResetExpiryDeleteGuard(t, ctx, db)
		defer restoreExpiryGuard()
		cleanupResult, cleanupErr := db.ExecContext(ctx,
			`DELETE FROM idempotency_records WHERE id = $1`, records[1].id)
		require.NoError(t, cleanupErr)
		cleanupAffected, cleanupAffectedErr := cleanupResult.RowsAffected()
		require.NoError(t, cleanupAffectedErr)
		require.Zero(t, cleanupAffected,
			"protected cleanup must skip the row without relying on the expiry guard")
		restoreExpiryGuard()
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, records[1].id, true)

		var rawScope, rawFenceFingerprint string
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT scope, request_fingerprint
FROM idempotency_records
WHERE id = $1`, rawFenceID).Scan(&rawScope, &rawFenceFingerprint))
		require.Equal(t, "openai_auto_reset_credit", rawScope)
		require.Equal(t, "upgrade-fence:actor-qualified:v1", rawFenceFingerprint)

		assertOpenAIAutoResetFrozenRecord(t, ctx, db, rawFailedID, wantScope)
		assertOpenAIAutoResetFrozenRecord(t, ctx, db, directPrincipalFailedID, wantScope)
		assertOpenAIAutoResetFrozenRecord(t, ctx, db, otherPrincipalFailedID, wantScope)
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, rawFenceID, false)
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, rawFailedID, true)
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, rawFenceFailedID, false)
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, directPrincipalFailedID, true)
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, otherPrincipalFailedID, true)
		assertOpenAIAutoResetProtectedAccount(t, ctx, db, records[1].id, 202)
		assertOpenAIAutoResetProtectedAccount(t, ctx, db, records[2].id, 203)
		assertOpenAIAutoResetProtectedAccount(t, ctx, db, rawFailedID, rawAccountID)
		assertOpenAIAutoResetProtectedAccount(
			t, ctx, db, directPrincipalFailedID, directPrincipalAccountID,
		)
		assertOpenAIAutoResetProtectedAccount(
			t, ctx, db, otherPrincipalFailedID, otherPrincipalAccountID,
		)
		var nonCanonicalProtectedCount int
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM openai_quota_auto_reset_protected_attempts AS protected
JOIN idempotency_records AS record
  ON record.id = protected.idempotency_record_id
WHERE record.scope <> $1`, wantScope).Scan(&nonCanonicalProtectedCount))
		require.Zero(t, nonCanonicalProtectedCount,
			"every protected attempt must be reachable by the reserved worker reconciliation identity")

		_, deleteRawAccountErr := db.ExecContext(ctx,
			`DELETE FROM accounts WHERE id = $1`, rawAccountID)
		require.NoError(t, deleteRawAccountErr)
		assertOpenAIAutoResetProtectedAccount(t, ctx, db, rawFailedID, rawAccountID)

		var fenceStatus string
		var fenceErrorReason string
		var fenceLockedUntil time.Time
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT status, error_reason, locked_until
FROM idempotency_records
WHERE id = $1`, rawFenceFailedID).Scan(&fenceStatus, &fenceErrorReason, &fenceLockedUntil))
		require.Equal(t, "failed_retryable", fenceStatus)
		require.Equal(t, "FENCE_RETRY", fenceErrorReason)
		require.False(t, fenceLockedUntil.IsZero())

		rawAuditCreatedAt := time.Now().UTC().Truncate(time.Microsecond)
		rawAuditRequestID := "reconcile-success:" + strconv.FormatInt(rawFailedID, 10)
		rawAuditExtra := openAIAutoResetReconciliationAuditExtra(
			rawAccountID, rawFailedID, rawFingerprint,
			"reconciled_success", 1, "operator-ticket:raw-success", "migration-test",
		)
		_, rawReconcileErr := db.ExecContext(ctx, `
SELECT reconcile_openai_quota_auto_reset_protected_attempt(
    $1, $2, $3, $4, $5, $6, $7, $8::jsonb
)`,
			rawFailedID,
			wantScope,
			rawFingerprint,
			rawAccountID,
			rawAuditCreatedAt,
			rawAuditRequestID,
			200,
			rawAuditExtra,
		)
		require.NoError(t, rawReconcileErr,
			"a raw historical attempt selected by the snapshot must be reconcilable")
		var rawReconciledScope, rawReconciledStatus, rawReconciledAuditRequestID string
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT record.scope, record.status, protected.reconciliation_audit_request_id
FROM idempotency_records AS record
JOIN openai_quota_auto_reset_protected_attempts AS protected
  ON protected.idempotency_record_id = record.id
WHERE record.id = $1`, rawFailedID).Scan(
			&rawReconciledScope,
			&rawReconciledStatus,
			&rawReconciledAuditRequestID,
		))
		require.Equal(t, wantScope, rawReconciledScope)
		require.Equal(t, "succeeded", rawReconciledStatus)
		require.Equal(t, rawAuditRequestID, rawReconciledAuditRequestID)
		restoreRawExpiryGuard := disableOpenAIAutoResetExpiryDeleteGuard(t, ctx, db)
		defer restoreRawExpiryGuard()
		rawCleanupResult, rawCleanupErr := db.ExecContext(ctx,
			`DELETE FROM idempotency_records WHERE id = $1`, rawFailedID)
		require.NoError(t, rawCleanupErr)
		rawCleanupCount, rawCleanupCountErr := rawCleanupResult.RowsAffected()
		require.NoError(t, rawCleanupCountErr)
		require.Equal(t, int64(1), rawCleanupCount)
		restoreRawExpiryGuard()
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, rawFailedID, false)

		for _, recordID := range []int64{
			records[0].id,
			records[1].id,
			records[2].id,
			rawFenceID,
			rawFenceFailedID,
			directPrincipalFailedID,
			otherPrincipalFailedID,
		} {
			cleanupOpenAIAutoResetProtectedRecord(t, ctx, db, recordID)
		}
		for _, accountID := range []int64{directPrincipalAccountID, otherPrincipalAccountID} {
			require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
				`DELETE FROM accounts WHERE id = $1`, accountID))
		}
	})

	t.Run("historical snapshot rejects malformed and semantically invalid identities atomically", func(t *testing.T) {
		type snapshotCase struct {
			name         string
			needsAccount bool
			scopeKind    string
			keyKind      string
			fingerKind   string
			wantError    string
		}
		cases := []snapshotCase{
			{
				name: "malformed stable key hash", needsAccount: true,
				scopeKind: "raw", keyKind: "malformed", fingerKind: "legacy",
				wantError: "protected attempt identity is malformed",
			},
			{
				name: "malformed request fingerprint", needsAccount: true,
				scopeKind: "raw", keyKind: "stable", fingerKind: "malformed",
				wantError: "protected attempt identity is malformed",
			},
			{
				name: "leading zero account actor", scopeKind: "leading-zero-account",
				keyKind: "unmapped", fingerKind: "opaque",
				wantError: "protected attempt identity is malformed",
			},
			{
				name: "overflow Service Principal actor", scopeKind: "overflow-principal",
				keyKind: "unmapped", fingerKind: "opaque",
				wantError: "protected attempt identity is malformed",
			},
			{
				name: "raw key has no account mapping", scopeKind: "raw",
				keyKind: "unmapped", fingerKind: "opaque",
				wantError: "account provenance is not unique",
			},
			{
				name: "raw semantic fingerprint mismatch", needsAccount: true,
				scopeKind: "raw", keyKind: "stable", fingerKind: "opaque",
				wantError: "protected marker fingerprint mismatch",
			},
			{
				name: "Service Principal semantic fingerprint mismatch", needsAccount: true,
				scopeKind: "principal", keyKind: "stable", fingerKind: "opaque",
				wantError: "protected marker fingerprint mismatch",
			},
			{
				name: "account semantic fingerprint mismatch", needsAccount: true,
				scopeKind: "account", keyKind: "stable", fingerKind: "opaque",
				wantError: "protected marker fingerprint mismatch",
			},
		}

		for index, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				creditHash := strings.Repeat(fmt.Sprintf("%x", index+1), 24)
				cycleHash := strings.Repeat("a", 24)
				var accountID int64
				if testCase.needsAccount {
					accountID = insertOpenAIAutoResetAttemptAccount(
						t, ctx, db, "snapshot-invalid-"+strconv.Itoa(index), creditHash, cycleHash,
					)
				}

				scope := "openai_auto_reset_credit"
				switch testCase.scopeKind {
				case "account":
					scope = fmt.Sprintf("openai_auto_reset_credit|account:%d", accountID)
				case "principal":
					scope = fmt.Sprintf("openai_auto_reset_credit|service_principal:%d", principalID)
				case "leading-zero-account":
					scope = "openai_auto_reset_credit|account:01"
				case "overflow-principal":
					scope = "openai_auto_reset_credit|service_principal:9223372036854775808"
				}

				keyHash := strings.Repeat("7", 64)
				if testCase.keyKind == "stable" {
					keyHash = openAIAutoResetStableKeyHash(accountID, creditHash, cycleHash)
				} else if testCase.keyKind == "malformed" {
					keyHash = "not-a-canonical-key-hash"
				}
				fingerprint := strings.Repeat("8", 64)
				if testCase.fingerKind == "legacy" {
					fingerprint = openAIAutoResetFingerprint(
						fmt.Sprintf("account:%d", accountID), accountID, creditHash, cycleHash,
					)
				} else if testCase.fingerKind == "malformed" {
					fingerprint = "not-a-canonical-request-fingerprint"
				}

				require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
					`DELETE FROM openai_quota_auto_reset_protection_backfill WHERE completed`))
				var recordID int64
				var restoreFence func()
				if testCase.scopeKind == "account" {
					restoreFence = disableOpenAIAutoResetLegacyScopeFence(t, ctx, db)
				}
				require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    error_reason, locked_until, expires_at
)
VALUES (
    $1, $2, $3, 'failed_retryable', 'AMBIGUOUS_TIMEOUT',
    statement_timestamp() + INTERVAL '5 minutes',
    statement_timestamp() + INTERVAL '1 day'
)
RETURNING id`, scope, keyHash, fingerprint).Scan(&recordID))
				if restoreFence != nil {
					restoreFence()
				}

				beforeVersion := readOpenAIAutoResetWorkerVersion(t, ctx, db, principalID)
				_, reapplyErr := db.ExecContext(ctx, string(migrationSQL))
				require.ErrorContains(t, reapplyErr, testCase.wantError)
				require.Equal(t, beforeVersion, readOpenAIAutoResetWorkerVersion(t, ctx, db, principalID))
				assertOpenAIAutoResetAttemptProtected(t, ctx, db, recordID, false)

				var sentinelCount int
				require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM openai_quota_auto_reset_protection_backfill WHERE completed`,
				).Scan(&sentinelCount))
				require.Zero(t, sentinelCount, "a rejected snapshot must roll its sentinel back")
				var storedScope, storedKey, storedFingerprint, storedStatus, storedError string
				require.NoError(t, db.QueryRowContext(ctx, `
SELECT scope, idempotency_key_hash, request_fingerprint, status, error_reason
FROM idempotency_records
WHERE id = $1`, recordID).Scan(
					&storedScope, &storedKey, &storedFingerprint, &storedStatus, &storedError,
				))
				require.Equal(t, scope, storedScope)
				require.Equal(t, keyHash, storedKey)
				require.Equal(t, fingerprint, storedFingerprint)
				require.Equal(t, "failed_retryable", storedStatus)
				require.Equal(t, "AMBIGUOUS_TIMEOUT", storedError)

				cleanupOpenAIAutoResetProtectedRecord(t, ctx, db, recordID)
				if accountID > 0 {
					require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
						`DELETE FROM accounts WHERE id = $1`, accountID))
				}
				_, restoreMigrationErr := db.ExecContext(ctx, string(migrationSQL))
				require.NoError(t, restoreMigrationErr)
			})
		}
	})

	t.Run("raw reapply validates protected markers despite the completed sentinel", func(t *testing.T) {
		creditHash := strings.Repeat("9", 24)
		cycleHash := strings.Repeat("0", 24)
		accountID := insertOpenAIAutoResetAttemptAccount(
			t, ctx, db, "reapply-semantic-mismatch", creditHash, cycleHash,
		)
		canonicalScope := fmt.Sprintf(
			"openai_auto_reset_credit|service_principal:%d", principalID,
		)
		fingerprint := strings.Repeat("f", 64)
		var recordID int64
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    locked_until, expires_at
)
VALUES (
    $1, $2, $3, 'processing', statement_timestamp() + INTERVAL '5 minutes',
    statement_timestamp() + INTERVAL '1 day'
)
RETURNING id`,
			canonicalScope,
			openAIAutoResetStableKeyHash(accountID, creditHash, cycleHash),
			fingerprint,
		).Scan(&recordID))
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO openai_quota_auto_reset_protected_attempts (idempotency_record_id, account_id)
VALUES ($1, $2)`, recordID, accountID))

		_, reapplyErr := db.ExecContext(ctx, string(migrationSQL))
		require.ErrorContains(t, reapplyErr, "protected marker fingerprint mismatch")
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, recordID, true)
		var sentinelCount int
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM openai_quota_auto_reset_protection_backfill WHERE completed`,
		).Scan(&sentinelCount))
		require.Equal(t, 1, sentinelCount)

		cleanupOpenAIAutoResetProtectedRecord(t, ctx, db, recordID)
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
			`DELETE FROM accounts WHERE id = $1`, accountID))
		_, hardenedReapplyErr := db.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, hardenedReapplyErr)

		for index, invalidScope := range []string{
			"openai_auto_reset_credit|service_principal:01",
			fmt.Sprintf("openai_auto_reset_credit|account:%d", accountID+1),
			"openai_auto_reset_credit|unknown:1",
		} {
			var invalidScopeRecordID int64
			var restoreFence func()
			if strings.Contains(invalidScope, "|account:") {
				restoreFence = disableOpenAIAutoResetLegacyScopeFence(t, ctx, db)
			}
			require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    locked_until, expires_at
)
VALUES (
    $1, $2, repeat('d', 64), 'processing', statement_timestamp() + INTERVAL '5 minutes',
    statement_timestamp() + INTERVAL '1 day'
)
RETURNING id`, invalidScope, fmt.Sprintf("%064x", 900+index)).Scan(&invalidScopeRecordID))
			if restoreFence != nil {
				restoreFence()
			}
			require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO openai_quota_auto_reset_protected_attempts (idempotency_record_id, account_id)
VALUES ($1, $2)`, invalidScopeRecordID, accountID))

			_, invalidScopeReapplyErr := db.ExecContext(ctx, string(migrationSQL))
			require.ErrorContains(t, invalidScopeReapplyErr,
				"protected marker identity is malformed")
			assertOpenAIAutoResetAttemptProtected(t, ctx, db, invalidScopeRecordID, true)
			var storedScope string
			require.NoError(t, db.QueryRowContext(ctx,
				`SELECT scope FROM idempotency_records WHERE id = $1`, invalidScopeRecordID,
			).Scan(&storedScope))
			require.Equal(t, invalidScope, storedScope)
			require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM openai_quota_auto_reset_protection_backfill WHERE completed`,
			).Scan(&sentinelCount))
			require.Equal(t, 1, sentinelCount)
			cleanupOpenAIAutoResetProtectedRecord(t, ctx, db, invalidScopeRecordID)
		}

		unresolvedShapes := []struct {
			name           string
			status         string
			responseStatus any
			responseBody   any
			errorReason    any
		}{
			{
				name: "unreconciled succeeded parent", status: "succeeded",
				responseStatus: 200, responseBody: `{"caller":"terminal"}`,
			},
			{
				name: "processing parent with response", status: "processing",
				responseStatus: 200, responseBody: `{"caller":"partial"}`,
			},
			{
				name: "processing parent with error", status: "processing",
				errorReason: "CALLER_ERROR",
			},
			{
				name: "retryable parent with response", status: "failed_retryable",
				responseStatus: 500, responseBody: `{"caller":"conflicting"}`,
				errorReason: "AMBIGUOUS_TIMEOUT",
			},
		}
		for index, shape := range unresolvedShapes {
			t.Run(shape.name, func(t *testing.T) {
				var shapeRecordID int64
				require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    response_status, response_body, error_reason, locked_until, expires_at
)
VALUES (
    $1, $2, repeat('b', 64), $3, $4, $5, $6,
    statement_timestamp() + INTERVAL '5 minutes',
    statement_timestamp() + INTERVAL '1 day'
)
RETURNING id`,
					canonicalScope,
					fmt.Sprintf("%064x", 1000+index),
					shape.status,
					shape.responseStatus,
					shape.responseBody,
					shape.errorReason,
				).Scan(&shapeRecordID))
				require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO openai_quota_auto_reset_protected_attempts (idempotency_record_id, account_id)
VALUES ($1, $2)`, shapeRecordID, accountID))

				_, shapeReapplyErr := db.ExecContext(ctx, string(migrationSQL))
				require.ErrorContains(t, shapeReapplyErr,
					"unresolved protected parent shape mismatch")
				assertOpenAIAutoResetAttemptProtected(t, ctx, db, shapeRecordID, true)
				var storedStatus string
				require.NoError(t, db.QueryRowContext(ctx,
					`SELECT status FROM idempotency_records WHERE id = $1`, shapeRecordID,
				).Scan(&storedStatus))
				require.Equal(t, shape.status, storedStatus)
				require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM openai_quota_auto_reset_protection_backfill WHERE completed`,
				).Scan(&sentinelCount))
				require.Equal(t, 1, sentinelCount)
				cleanupOpenAIAutoResetProtectedRecord(t, ctx, db, shapeRecordID)
			})
		}
	})

	t.Run("raw reapply preserves marker provenance when account state cannot prove semantics", func(t *testing.T) {
		for _, stateCase := range []string{"different state", "missing account"} {
			t.Run(stateCase, func(t *testing.T) {
				creditHash := strings.Repeat("c", 24)
				cycleHash := strings.Repeat("d", 24)
				accountID := insertOpenAIAutoResetAttemptAccount(
					t, ctx, db, "reapply-provenance-"+strings.ReplaceAll(stateCase, " ", "-"),
					creditHash, cycleHash,
				)
				canonicalScope := fmt.Sprintf(
					"openai_auto_reset_credit|service_principal:%d", principalID,
				)
				var recordID int64
				require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    locked_until, expires_at
)
VALUES (
    $1, $2, repeat('e', 64), 'processing', statement_timestamp() + INTERVAL '5 minutes',
    statement_timestamp() + INTERVAL '1 day'
)
RETURNING id`,
					canonicalScope,
					openAIAutoResetStableKeyHash(accountID, creditHash, cycleHash),
				).Scan(&recordID))
				require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO openai_quota_auto_reset_protected_attempts (idempotency_record_id, account_id)
VALUES ($1, $2)`, recordID, accountID))
				if stateCase == "different state" {
					require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = jsonb_set(
        extra,
        '{codex_auto_reset_credit_state,attempt_cycle_hash}',
        to_jsonb(repeat('f', 24))
    )
WHERE id = $1`, accountID))
				} else {
					require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
						`DELETE FROM accounts WHERE id = $1`, accountID))
				}

				_, reapplyErr := db.ExecContext(ctx, string(migrationSQL))
				require.NoError(t, reapplyErr)
				assertOpenAIAutoResetAttemptProtected(t, ctx, db, recordID, true)
				cleanupOpenAIAutoResetProtectedRecord(t, ctx, db, recordID)
				if stateCase == "different state" {
					require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
						`DELETE FROM accounts WHERE id = $1`, accountID))
				}
			})
		}
	})

	t.Run("reconciled tombstone rejects an old update blocked before commit", func(t *testing.T) {
		const (
			accountID    = int64(204)
			fingerprint  = "abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd"
			reconcileSQL = `
SELECT reconcile_openai_quota_auto_reset_protected_attempt(
    $1, $2, $3, $4, $5, $6, $7, $8::jsonb
)`
		)
		canonicalScope :=
			"openai_auto_reset_credit|service_principal:" + openAIAutoResetInt64(principalID)
		var recordID int64
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    locked_until, expires_at
)
VALUES (
    $1, repeat('a', 63) || '4', $2, 'processing',
    statement_timestamp() + INTERVAL '5 minutes',
    statement_timestamp() + INTERVAL '1 day'
)
			RETURNING id`, canonicalScope, fingerprint).Scan(&recordID))
		auditRequestID := "reconcile-success:" + strconv.FormatInt(recordID, 10)
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
			INSERT INTO openai_quota_auto_reset_protected_attempts (idempotency_record_id, account_id)
			VALUES ($1, $2)`, recordID, accountID))
		missingEvidenceExtra := fmt.Sprintf(
			`{"account_id":%d,"decision_owner":"migration-test",`+
				`"idempotency_record_id":%d,"request_fingerprint":"%s",`+
				`"result_code":"reconciled_success","windows_reset":1}`,
			accountID,
			recordID,
			fingerprint,
		)
		_, missingEvidenceErr := db.ExecContext(ctx, reconcileSQL,
			recordID,
			canonicalScope,
			fingerprint,
			accountID,
			time.Now().UTC(),
			auditRequestID,
			200,
			missingEvidenceExtra,
		)
		require.ErrorContains(t, missingEvidenceErr,
			"invalid OpenAI quota auto-reset reconciliation input")
		var missingAuditCount int
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM audit_logs
WHERE action = 'system.openai.reset_credit.auto' AND request_id = $1`, auditRequestID).Scan(
			&missingAuditCount,
		))
		require.Zero(t, missingAuditCount)
		var postMissingStatus string
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT status FROM idempotency_records WHERE id = $1`, recordID,
		).Scan(&postMissingStatus))
		require.Equal(t, "processing", postMissingStatus)
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, recordID, true)

		mismatchedAccountExtra := openAIAutoResetReconciliationAuditExtra(
			accountID+1, recordID, fingerprint,
			"reconciled_success", 1, "operator-ticket:account-mismatch", "migration-test",
		)
		_, provenanceErr := db.ExecContext(ctx, reconcileSQL,
			recordID,
			canonicalScope,
			fingerprint,
			accountID+1,
			time.Now().UTC(),
			auditRequestID,
			200,
			mismatchedAccountExtra,
		)
		require.ErrorContains(t, provenanceErr,
			"OpenAI quota auto-reset reconciliation account provenance mismatch")

		reconcileConn, reconcileConnErr := db.Conn(ctx)
		require.NoError(t, reconcileConnErr)
		defer reconcileConn.Close()
		oldWorkerConn, oldWorkerConnErr := db.Conn(ctx)
		require.NoError(t, oldWorkerConnErr)
		defer oldWorkerConn.Close()

		var reconcilePID, oldWorkerPID int
		require.NoError(t, reconcileConn.QueryRowContext(ctx,
			`SELECT pg_backend_pid()`,
		).Scan(&reconcilePID))
		require.NoError(t, oldWorkerConn.QueryRowContext(ctx,
			`SELECT pg_backend_pid()`,
		).Scan(&oldWorkerPID))

		reconcileTx, reconcileTxErr := reconcileConn.BeginTx(ctx, nil)
		require.NoError(t, reconcileTxErr)
		committed := false
		defer func() {
			if !committed {
				_ = reconcileTx.Rollback()
			}
		}()
		var lockedID int64
		require.NoError(t, reconcileTx.QueryRowContext(ctx, `
SELECT id FROM idempotency_records WHERE id = $1 FOR UPDATE`, recordID).Scan(&lockedID))
		require.Equal(t, recordID, lockedID)

		oldUpdateResult := make(chan error, 1)
		go func() {
			_, updateErr := oldWorkerConn.ExecContext(ctx, `
UPDATE idempotency_records
SET status = 'succeeded',
    response_status = 200,
    response_body = '{"windows_reset":99,"late_old_worker":true}',
    error_reason = NULL,
    locked_until = NULL,
    expires_at = statement_timestamp() + INTERVAL '1 day',
    updated_at = NOW()
WHERE id = $1`, recordID)
			oldUpdateResult <- updateErr
		}()

		var blockingQueryErr error
		require.Eventually(t, func() bool {
			var isBlocked bool
			blockingQueryErr = db.QueryRowContext(ctx,
				`SELECT $1 = ANY(pg_blocking_pids($2))`, reconcilePID, oldWorkerPID,
			).Scan(&isBlocked)
			return blockingQueryErr == nil && isBlocked
		}, 5*time.Second, 20*time.Millisecond,
			"the old worker UPDATE must wait on the reconciliation row lock")
		require.NoError(t, blockingQueryErr)
		select {
		case earlyErr := <-oldUpdateResult:
			require.Failf(t, "old worker UPDATE returned before reconciliation commit", "error: %v", earlyErr)
		default:
		}

		auditCreatedAt := time.Now().UTC().Truncate(time.Microsecond)
		auditExtra := openAIAutoResetReconciliationAuditExtra(
			accountID, recordID, fingerprint,
			"reconciled_success", 1, "operator-ticket:success", "migration-test",
		)
		_, reconcileErr := reconcileTx.ExecContext(ctx, reconcileSQL,
			recordID,
			canonicalScope,
			fingerprint,
			accountID,
			auditCreatedAt,
			auditRequestID,
			200,
			auditExtra,
		)
		require.NoError(t, reconcileErr)
		require.NoError(t, reconcileTx.Commit())
		committed = true

		select {
		case oldUpdateErr := <-oldUpdateResult:
			require.ErrorContains(t, oldUpdateErr,
				"protected OpenAI quota auto-reset attempt requires explicit reconciliation")
		case <-time.After(5 * time.Second):
			require.Fail(t, "old worker UPDATE remained blocked after reconciliation commit")
		}

		var status string
		var responseStatus int
		var storedResponseBody string
		var storedExpiresAt time.Time
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT status, response_status, response_body, expires_at
FROM idempotency_records
WHERE id = $1`, recordID).Scan(&status, &responseStatus, &storedResponseBody, &storedExpiresAt))
		require.Equal(t, "succeeded", status)
		require.Equal(t, 200, responseStatus)
		require.JSONEq(t,
			`{"account_state_recovered":false,"recovery_pending":true,`+
				`"result_code":"success","windows_reset":1}`,
			storedResponseBody,
		)
		require.WithinDuration(t, time.Now().UTC().Add(8*24*time.Hour), storedExpiresAt, 5*time.Second)

		var reconciledAt time.Time
		var storedAuditRequestID string
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT reconciled_at, reconciliation_audit_request_id
FROM openai_quota_auto_reset_protected_attempts
WHERE idempotency_record_id = $1`, recordID).Scan(&reconciledAt, &storedAuditRequestID))
		require.False(t, reconciledAt.IsZero())
		require.Equal(t, auditRequestID, storedAuditRequestID)

		_, retryErr := db.ExecContext(ctx, reconcileSQL,
			recordID,
			canonicalScope,
			fingerprint,
			accountID,
			auditCreatedAt,
			auditRequestID,
			200,
			auditExtra,
		)
		require.NoError(t, retryErr, "an exact reconciliation retry must be idempotent")

		const conflictingAuditRequestID = "11111111-1111-5111-8111-111111111111"
		_, mismatchErr := db.ExecContext(ctx, reconcileSQL,
			recordID,
			canonicalScope,
			fingerprint,
			accountID,
			auditCreatedAt,
			conflictingAuditRequestID,
			200,
			auditExtra,
		)
		require.ErrorContains(t, mismatchErr,
			"invalid OpenAI quota auto-reset reconciliation input")
		var conflictingAuditCount int
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM audit_logs
WHERE action = 'system.openai.reset_credit.auto'
  AND request_id = $1`, conflictingAuditRequestID).Scan(&conflictingAuditCount))
		require.Zero(t, conflictingAuditCount,
			"a conflicting reconciliation must roll its newly inserted audit back")

		var auditCount int
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM audit_logs
WHERE action = 'system.openai.reset_credit.auto'
  AND request_id = $1
  AND actor_service_principal_id = $2`, auditRequestID, principalID).Scan(&auditCount))
		require.Equal(t, 1, auditCount)

		_, reapplyErr := db.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, reapplyErr)
		assertOpenAIAutoResetProtectionShape(t, ctx, db)
		var reappliedAuditRequestID string
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT reconciliation_audit_request_id
FROM openai_quota_auto_reset_protected_attempts
WHERE idempotency_record_id = $1`, recordID).Scan(&reappliedAuditRequestID))
		require.Equal(t, auditRequestID, reappliedAuditRequestID)
		_, postReapplyOldUpdateErr := db.ExecContext(ctx, `
UPDATE idempotency_records
SET response_body = '{"windows_reset":100,"late_after_reapply":true}',
    updated_at = NOW()
WHERE id = $1`, recordID)
		require.ErrorContains(t, postReapplyOldUpdateErr,
			"protected OpenAI quota auto-reset attempt requires explicit reconciliation")

		restoreExpiryGuard := disableOpenAIAutoResetExpiryDeleteGuard(t, ctx, db)
		defer restoreExpiryGuard()
		restoreProtectedGuard := disableOpenAIAutoResetProtectedAttemptGuard(t, ctx, db)
		defer restoreProtectedGuard()
		_, failClosedCleanupErr := db.ExecContext(ctx,
			`DELETE FROM idempotency_records WHERE id = $1`, recordID)
		require.ErrorContains(t, failClosedCleanupErr,
			"openai_auto_reset_protected_attempts_record_id_fkey",
			"RESTRICT must retain the reconciliation tombstone if its guard is disabled")
		restoreProtectedGuard()
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, recordID, true)

		cleanupResult, cleanupErr := db.ExecContext(ctx,
			`DELETE FROM idempotency_records WHERE id = $1`, recordID)
		require.NoError(t, cleanupErr)
		cleanupCount, cleanupCountErr := cleanupResult.RowsAffected()
		require.NoError(t, cleanupCountErr)
		require.Equal(t, int64(1), cleanupCount)
		restoreExpiryGuard()
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, recordID, false)
	})

	t.Run("success reconciliation rejects null and malformed inputs then canonicalizes every result", func(t *testing.T) {
		const reconcileSQL = `
SELECT reconcile_openai_quota_auto_reset_protected_attempt(
    $1, $2, $3, $4, $5, $6, $7, $8::jsonb
)`
		canonicalScope := fmt.Sprintf(
			"openai_auto_reset_credit|service_principal:%d", principalID,
		)
		insertProtected := func(accountID int64, fingerprint string) int64 {
			var recordID int64
			require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    locked_until, expires_at
)
VALUES (
    $1, repeat('2', 64), $2, 'processing',
    statement_timestamp() + INTERVAL '5 minutes',
    statement_timestamp() + INTERVAL '1 day'
)
RETURNING id`, canonicalScope, fingerprint).Scan(&recordID))
			require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO openai_quota_auto_reset_protected_attempts (idempotency_record_id, account_id)
VALUES ($1, $2)`, recordID, accountID))
			return recordID
		}

		const invalidAccountID = int64(3101)
		invalidFingerprint := strings.Repeat("3", 64)
		invalidRecordID := insertProtected(invalidAccountID, invalidFingerprint)
		invalidRequestID := "reconcile-success:" + strconv.FormatInt(invalidRecordID, 10)
		invalidCreatedAt := time.Now().UTC().Truncate(time.Microsecond)
		validExtra := openAIAutoResetReconciliationAuditExtra(
			invalidAccountID,
			invalidRecordID,
			invalidFingerprint,
			"reconciled_success",
			1,
			"operator-ticket:input-matrix",
			"migration-test",
		)
		withoutResult := strings.Replace(
			validExtra, `"result_code":"reconciled_success",`, "", 1,
		)
		withNullResult := strings.Replace(
			validExtra, `"result_code":"reconciled_success"`, `"result_code":null`, 1,
		)
		withoutWindows := strings.Replace(validExtra, `,"windows_reset":1`, "", 1)
		withStringWindows := strings.Replace(validExtra, `"windows_reset":1`, `"windows_reset":"1"`, 1)
		withFractionWindows := strings.Replace(validExtra, `"windows_reset":1`, `"windows_reset":1.5`, 1)
		withNegativeWindows := strings.Replace(validExtra, `"windows_reset":1`, `"windows_reset":-1`, 1)
		withLargeWindows := strings.Replace(validExtra, `"windows_reset":1`, `"windows_reset":2147483648`, 1)

		type invalidInput struct {
			name        string
			recordID    any
			scope       any
			fingerprint any
			accountID   any
			createdAt   any
			requestID   any
			statusCode  any
			extra       any
		}
		baseInput := invalidInput{
			recordID: invalidRecordID, scope: canonicalScope, fingerprint: invalidFingerprint,
			accountID: invalidAccountID, createdAt: invalidCreatedAt,
			requestID: invalidRequestID, statusCode: 200, extra: validExtra,
		}
		invalidInputs := []invalidInput{
			func() invalidInput {
				value := baseInput
				value.name = "null record id"
				value.recordID = nil
				return value
			}(),
			func() invalidInput {
				value := baseInput
				value.name = "null actor scope"
				value.scope = nil
				return value
			}(),
			func() invalidInput {
				value := baseInput
				value.name = "null fingerprint"
				value.fingerprint = nil
				return value
			}(),
			func() invalidInput {
				value := baseInput
				value.name = "null account id"
				value.accountID = nil
				return value
			}(),
			func() invalidInput {
				value := baseInput
				value.name = "null audit timestamp"
				value.createdAt = nil
				return value
			}(),
			func() invalidInput {
				value := baseInput
				value.name = "null audit request id"
				value.requestID = nil
				return value
			}(),
			func() invalidInput {
				value := baseInput
				value.name = "null audit status"
				value.statusCode = nil
				return value
			}(),
			func() invalidInput {
				value := baseInput
				value.name = "null audit extra"
				value.extra = nil
				return value
			}(),
			func() invalidInput {
				value := baseInput
				value.name = "missing result code"
				value.extra = withoutResult
				return value
			}(),
			func() invalidInput {
				value := baseInput
				value.name = "JSON null result code"
				value.extra = withNullResult
				return value
			}(),
			func() invalidInput {
				value := baseInput
				value.name = "missing windows reset"
				value.extra = withoutWindows
				return value
			}(),
			func() invalidInput {
				value := baseInput
				value.name = "non-number windows reset"
				value.extra = withStringWindows
				return value
			}(),
			func() invalidInput {
				value := baseInput
				value.name = "fractional windows reset"
				value.extra = withFractionWindows
				return value
			}(),
			func() invalidInput {
				value := baseInput
				value.name = "negative windows reset"
				value.extra = withNegativeWindows
				return value
			}(),
			func() invalidInput {
				value := baseInput
				value.name = "windows reset exceeds int32"
				value.extra = withLargeWindows
				return value
			}(),
		}
		for _, input := range invalidInputs {
			t.Run(input.name, func(t *testing.T) {
				_, reconcileErr := db.ExecContext(ctx, reconcileSQL,
					input.recordID,
					input.scope,
					input.fingerprint,
					input.accountID,
					input.createdAt,
					input.requestID,
					input.statusCode,
					input.extra,
				)
				require.ErrorContains(t, reconcileErr,
					"invalid OpenAI quota auto-reset reconciliation input")
				var auditCount int
				require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM audit_logs
WHERE action = 'system.openai.reset_credit.auto' AND request_id = $1`, invalidRequestID,
				).Scan(&auditCount))
				require.Zero(t, auditCount)
				assertOpenAIAutoResetAttemptProtected(t, ctx, db, invalidRecordID, true)
				var status string
				require.NoError(t, db.QueryRowContext(ctx,
					`SELECT status FROM idempotency_records WHERE id = $1`, invalidRecordID,
				).Scan(&status))
				require.Equal(t, "processing", status)
			})
		}
		cleanupOpenAIAutoResetProtectedRecord(t, ctx, db, invalidRecordID)

		outcomes := []struct {
			resultCode      string
			windowsReset    int
			auditStatusCode int
			responseBody    string
		}{
			{
				resultCode: "reconciled_success", windowsReset: 2, auditStatusCode: 200,
				responseBody: `{"account_state_recovered":false,"recovery_pending":true,` +
					`"result_code":"success","windows_reset":2}`,
			},
			{
				resultCode: "no_credit", windowsReset: 0, auditStatusCode: 409,
				responseBody: `{"result_code":"no_credit","windows_reset":0}`,
			},
			{
				resultCode: "recovery_deferred", windowsReset: 3, auditStatusCode: 409,
				responseBody: `{"account_state_recovered":false,"recovery_deferred":true,` +
					`"recovery_pending":true,"result_code":"success","windows_reset":3}`,
			},
			{
				resultCode: "recovery_failed", windowsReset: 4, auditStatusCode: 409,
				responseBody: `{"account_state_recovered":false,"recovery_pending":true,` +
					`"result_code":"success",` +
					`"warning_code":"OPENAI_AUTO_RESET_RECONCILED_RECOVERY_FAILED",` +
					`"windows_reset":4}`,
			},
		}
		for index, outcome := range outcomes {
			t.Run(outcome.resultCode, func(t *testing.T) {
				accountID := int64(3200 + index)
				fingerprint := fmt.Sprintf("%064x", index+4)
				recordID := insertProtected(accountID, fingerprint)
				createdAt := time.Now().UTC().Truncate(time.Microsecond)
				requestID := "reconcile-success:" + strconv.FormatInt(recordID, 10)
				extra := openAIAutoResetReconciliationAuditExtra(
					accountID,
					recordID,
					fingerprint,
					outcome.resultCode,
					outcome.windowsReset,
					"operator-ticket:canonical-"+outcome.resultCode,
					"migration-test",
				)
				_, reconcileErr := db.ExecContext(ctx, reconcileSQL,
					recordID,
					canonicalScope,
					fingerprint,
					accountID,
					createdAt,
					requestID,
					outcome.auditStatusCode,
					extra,
				)
				require.NoError(t, reconcileErr)

				var status, responseBody string
				var responseStatus, auditStatus int
				var expiresAt, reconciledAt time.Time
				require.NoError(t, db.QueryRowContext(ctx, `
SELECT record.status,
       record.response_status,
       record.response_body,
       record.expires_at,
       protected.reconciled_at,
       audit.status_code
FROM idempotency_records AS record
JOIN openai_quota_auto_reset_protected_attempts AS protected
  ON protected.idempotency_record_id = record.id
JOIN audit_logs AS audit
  ON audit.action = 'system.openai.reset_credit.auto'
 AND audit.request_id = protected.reconciliation_audit_request_id
WHERE record.id = $1`, recordID).Scan(
					&status,
					&responseStatus,
					&responseBody,
					&expiresAt,
					&reconciledAt,
					&auditStatus,
				))
				require.Equal(t, "succeeded", status)
				require.Equal(t, 200, responseStatus)
				require.Equal(t, outcome.auditStatusCode, auditStatus)
				require.JSONEq(t, outcome.responseBody, responseBody)
				require.True(t, expiresAt.Equal(reconciledAt.Add(8*24*time.Hour)),
					"success expiry must be exactly reconciled_at plus eight days")

				if index == 0 {
					for _, interval := range []string{"7 days", "9 days"} {
						_, expiryErr := db.ExecContext(ctx, `
UPDATE idempotency_records
SET expires_at = $2::INTERVAL + $3::TIMESTAMPTZ,
    updated_at = NOW()
WHERE id = $1`, recordID, interval, reconciledAt)
						require.ErrorContains(t, expiryErr,
							"protected OpenAI quota auto-reset attempt requires explicit reconciliation")
					}
				}

				_, reapplyErr := db.ExecContext(ctx, string(migrationSQL))
				require.NoError(t, reapplyErr,
					"a canonical retained reconciliation outcome must survive raw reapply")
				cleanupOpenAIAutoResetProtectedRecord(t, ctx, db, recordID)
				require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
DELETE FROM audit_logs
WHERE action = 'system.openai.reset_credit.auto' AND request_id = $1`, requestID))
			})
		}
	})

	t.Run("raw reapply rejects legacy terminal decisions and rolls back hardening", func(t *testing.T) {
		canonicalScope := fmt.Sprintf(
			"openai_auto_reset_credit|service_principal:%d", principalID,
		)
		var canonicalSuccessBody string
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT jsonb_build_object(
    'account_state_recovered', false,
    'recovery_pending', true,
    'result_code', 'success',
    'windows_reset', 1
)::text`).Scan(&canonicalSuccessBody))

		for index, outcomeCase := range []struct {
			name         string
			invalidAudit bool
			wantError    string
			storedBody   string
			retention    time.Duration
		}{
			{
				name:       "caller controlled body and short retention",
				wantError:  "reconciled parent outcome mismatch",
				storedBody: `{"caller_controlled":true}`,
				retention:  24 * time.Hour,
			},
			{
				name:         "audit missing required evidence",
				invalidAudit: true,
				wantError:    "reconciled outcome audit contract mismatch",
				storedBody:   canonicalSuccessBody,
				retention:    8 * 24 * time.Hour,
			},
		} {
			t.Run(outcomeCase.name, func(t *testing.T) {
				accountID := int64(4100 + index)
				fingerprint := fmt.Sprintf("%064x", 500+index)
				var recordID int64
				require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    locked_until, expires_at
)
VALUES (
    $1, $2, $3, 'processing', statement_timestamp() + INTERVAL '5 minutes',
    statement_timestamp() + INTERVAL '1 day'
)
RETURNING id`,
					canonicalScope,
					fmt.Sprintf("%064x", 600+index),
					fingerprint,
				).Scan(&recordID))
				require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO openai_quota_auto_reset_protected_attempts (idempotency_record_id, account_id)
VALUES ($1, $2)`, recordID, accountID))

				requestID := "reconcile-success:" + strconv.FormatInt(recordID, 10)
				extra := openAIAutoResetReconciliationAuditExtra(
					accountID,
					recordID,
					fingerprint,
					"reconciled_success",
					1,
					"operator-ticket:legacy-outcome",
					"migration-test",
				)
				if outcomeCase.invalidAudit {
					extra = strings.Replace(
						extra, `"evidence_ref":"operator-ticket:legacy-outcome",`, "", 1,
					)
				}
				require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO audit_logs (
    created_at, actor_service_principal_id, auth_method, action, method,
    path, request_id, status_code, extra
)
VALUES (
    $1, $2, 'service_principal', 'system.openai.reset_credit.auto', 'SYSTEM',
    $3, $4, 200, $5::jsonb
)`,
					time.Now().UTC().Truncate(time.Microsecond),
					principalID,
					fmt.Sprintf("/system/openai/accounts/%d/auto-reset-credit", accountID),
					requestID,
					extra,
				))

				reconciledAt := time.Now().UTC().Truncate(time.Microsecond)
				restoreGuard := disableOpenAIAutoResetProtectedAttemptGuard(t, ctx, db)
				require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE openai_quota_auto_reset_protected_attempts
SET reconciled_at = $2,
    reconciliation_audit_request_id = $3
WHERE idempotency_record_id = $1`, recordID, reconciledAt, requestID))
				require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE idempotency_records
SET status = 'succeeded',
    response_status = 200,
    response_body = $2,
    error_reason = NULL,
    locked_until = NULL,
    expires_at = $3,
    updated_at = NOW()
WHERE id = $1`,
					recordID,
					outcomeCase.storedBody,
					reconciledAt.Add(outcomeCase.retention),
				))
				restoreGuard()

				_, reapplyErr := db.ExecContext(ctx, string(migrationSQL))
				require.ErrorContains(t, reapplyErr, outcomeCase.wantError)
				assertOpenAIAutoResetAttemptProtected(t, ctx, db, recordID, true)
				var storedBody string
				require.NoError(t, db.QueryRowContext(ctx,
					`SELECT response_body FROM idempotency_records WHERE id = $1`, recordID,
				).Scan(&storedBody))
				require.Equal(t, outcomeCase.storedBody, storedBody)
				var sentinelCount int
				require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM openai_quota_auto_reset_protection_backfill WHERE completed`,
				).Scan(&sentinelCount))
				require.Equal(t, 1, sentinelCount)

				cleanupOpenAIAutoResetProtectedRecord(t, ctx, db, recordID)
				require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
DELETE FROM audit_logs
WHERE action = 'system.openai.reset_credit.auto' AND request_id = $1`, requestID))
			})
		}

		for index, auditCase := range []struct {
			name      string
			requestID string
			result    string
			evidence  string
			recordID  int64
		}{
			{
				name: "no-effect audit missing evidence", requestID: "reconcile-no-effect:700001",
				result: "reconciled_no_effect", recordID: 700001,
			},
			{
				name: "no-effect namespace without result", requestID: "reconcile-no-effect:700002",
				result: "legacy_no_effect", evidence: "operator-ticket:legacy", recordID: 700002,
			},
			{
				name: "no-effect result outside namespace", requestID: "legacy-no-effect-700003",
				result: "reconciled_no_effect", evidence: "operator-ticket:legacy", recordID: 700003,
			},
			{
				name:      "no-effect namespace suffix overflows bigint",
				requestID: "reconcile-no-effect:9223372036854775808",
				result:    "reconciled_no_effect", evidence: "operator-ticket:legacy", recordID: 700004,
			},
		} {
			t.Run(auditCase.name, func(t *testing.T) {
				accountID := int64(4200 + index)
				extra := openAIAutoResetReconciliationAuditExtra(
					accountID,
					auditCase.recordID,
					strings.Repeat("a", 64),
					auditCase.result,
					0,
					auditCase.evidence,
					"migration-test",
				)
				require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO audit_logs (
    created_at, actor_service_principal_id, auth_method, action, method,
    path, request_id, status_code, extra
)
VALUES (
    $1, $2, 'service_principal', 'system.openai.reset_credit.auto', 'SYSTEM',
    $3, $4, 409, $5::jsonb
)`,
					time.Now().UTC().Truncate(time.Microsecond),
					principalID,
					fmt.Sprintf("/system/openai/accounts/%d/auto-reset-credit", accountID),
					auditCase.requestID,
					extra,
				))

				_, reapplyErr := db.ExecContext(ctx, string(migrationSQL))
				require.ErrorContains(t, reapplyErr, "no-effect audit")
				var auditCount, sentinelCount int
				require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM audit_logs
WHERE action = 'system.openai.reset_credit.auto' AND request_id = $1`, auditCase.requestID,
				).Scan(&auditCount))
				require.Equal(t, 1, auditCount)
				require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM openai_quota_auto_reset_protection_backfill WHERE completed`,
				).Scan(&sentinelCount))
				require.Equal(t, 1, sentinelCount)
				require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
DELETE FROM audit_logs
WHERE action = 'system.openai.reset_credit.auto' AND request_id = $1`, auditCase.requestID))
			})
		}

		creditHash := strings.Repeat("e", 24)
		cycleHash := strings.Repeat("f", 24)
		pendingAccountID := insertOpenAIAutoResetAttemptAccount(
			t, ctx, db, "legacy-no-effect-pending-state", creditHash, cycleHash,
		)
		const pendingRecordID = int64(700100)
		pendingFingerprint := openAIAutoResetFingerprint(
			fmt.Sprintf("service_principal:%d", principalID),
			pendingAccountID,
			creditHash,
			cycleHash,
		)
		pendingRequestID := "reconcile-no-effect:" + strconv.FormatInt(pendingRecordID, 10)
		pendingExtra := openAIAutoResetReconciliationAuditExtra(
			pendingAccountID,
			pendingRecordID,
			pendingFingerprint,
			"reconciled_no_effect",
			0,
			"operator-ticket:pending-state",
			"migration-test",
		)
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO audit_logs (
    created_at, actor_service_principal_id, auth_method, action, method,
    path, request_id, status_code, extra
)
VALUES (
    $1, $2, 'service_principal', 'system.openai.reset_credit.auto', 'SYSTEM',
    $3, $4, 409, $5::jsonb
)`,
			time.Now().UTC().Truncate(time.Microsecond),
			principalID,
			fmt.Sprintf("/system/openai/accounts/%d/auto-reset-credit", pendingAccountID),
			pendingRequestID,
			pendingExtra,
		))
		_, pendingReapplyErr := db.ExecContext(ctx, string(migrationSQL))
		require.ErrorContains(t, pendingReapplyErr,
			"no-effect audit retained pending account state")

		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = extra - 'codex_auto_reset_credit_state',
    updated_at = NOW()
WHERE id = $1`, pendingAccountID))
		_, clearedReapplyErr := db.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, clearedReapplyErr,
			"a canonical no-effect audit must pass once its exact pending state is cleared")
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
DELETE FROM audit_logs
WHERE action = 'system.openai.reset_credit.auto' AND request_id = $1`, pendingRequestID))
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
			`DELETE FROM accounts WHERE id = $1`, pendingAccountID))
	})

	t.Run("success retention keeps a tombstone while matching account recovery is pending", func(t *testing.T) {
		creditHash := strings.Repeat("4", 24)
		cycleHash := strings.Repeat("5", 24)
		accountID := insertOpenAIAutoResetAttemptAccount(
			t, ctx, db, "success-pending-recovery", creditHash, cycleHash,
		)
		canonicalScope :=
			"openai_auto_reset_credit|service_principal:" + openAIAutoResetInt64(principalID)
		fingerprint := openAIAutoResetFingerprint(
			fmt.Sprintf("service_principal:%d", principalID),
			accountID,
			creditHash,
			cycleHash,
		)
		var recordID int64
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    locked_until, expires_at
)
VALUES (
    $1, $2, $3, 'processing', statement_timestamp() + INTERVAL '5 minutes',
    statement_timestamp() + INTERVAL '1 day'
)
RETURNING id`,
			canonicalScope,
			openAIAutoResetStableKeyHash(accountID, creditHash, cycleHash),
			fingerprint,
		).Scan(&recordID))
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO openai_quota_auto_reset_protected_attempts (idempotency_record_id, account_id)
VALUES ($1, $2)`, recordID, accountID))

		auditCreatedAt := time.Now().UTC().Truncate(time.Microsecond)
		auditRequestID := "reconcile-success:" + strconv.FormatInt(recordID, 10)
		auditExtra := openAIAutoResetReconciliationAuditExtra(
			accountID, recordID, fingerprint,
			"reconciled_success", 1, "operator-ticket:pending-recovery", "migration-test",
		)
		_, reconcileErr := db.ExecContext(ctx, `
SELECT reconcile_openai_quota_auto_reset_protected_attempt(
    $1, $2, $3, $4, $5, $6, 200, $7::jsonb
)`, recordID, canonicalScope, fingerprint, accountID, auditCreatedAt, auditRequestID, auditExtra)
		require.NoError(t, reconcileErr)

		var expiresAt time.Time
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT expires_at FROM idempotency_records WHERE id = $1`, recordID,
		).Scan(&expiresAt))
		require.WithinDuration(t, time.Now().UTC().Add(8*24*time.Hour), expiresAt, 5*time.Second)

		restoreExpiryGuard := disableOpenAIAutoResetExpiryDeleteGuard(t, ctx, db)
		defer restoreExpiryGuard()
		pendingDelete, pendingDeleteErr := db.ExecContext(ctx,
			`DELETE FROM idempotency_records WHERE id = $1`, recordID)
		require.NoError(t, pendingDeleteErr)
		pendingDeleted, pendingDeletedErr := pendingDelete.RowsAffected()
		require.NoError(t, pendingDeletedErr)
		require.Zero(t, pendingDeleted,
			"matching resetting state must retain the terminal record for recovery")
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, recordID, true)

		_, terminalStateErr := db.ExecContext(ctx, `
UPDATE accounts
SET extra = jsonb_set(
        extra,
        '{codex_auto_reset_credit_state,status}',
        '"success"'::jsonb
    ),
    updated_at = NOW()
WHERE id = $1`, accountID)
		require.NoError(t, terminalStateErr)
		terminalDelete, terminalDeleteErr := db.ExecContext(ctx,
			`DELETE FROM idempotency_records WHERE id = $1`, recordID)
		require.NoError(t, terminalDeleteErr)
		terminalDeleted, terminalDeletedErr := terminalDelete.RowsAffected()
		require.NoError(t, terminalDeletedErr)
		require.Equal(t, int64(1), terminalDeleted)
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, recordID, false)
		restoreExpiryGuard()
	})

	t.Run("expiry cleanup retains unprotected terminal outcomes while account recovery is pending", func(t *testing.T) {
		const cleanupSQL = `
WITH victims AS (
    SELECT id
    FROM idempotency_records
    WHERE id = $1
      AND expires_at <= $2
    FOR UPDATE SKIP LOCKED
)
DELETE FROM idempotency_records AS record
USING victims
WHERE record.id = victims.id
  AND record.expires_at <= $2`

		for index, pendingStatus := range []string{"resetting", "failed"} {
			t.Run(pendingStatus, func(t *testing.T) {
				creditHash := strings.Repeat(strconv.Itoa(index+6), 24)
				cycleHash := strings.Repeat(strconv.Itoa(index+8), 24)
				accountID := insertOpenAIAutoResetAttemptAccount(
					t, ctx, db, "unprotected-terminal-"+pendingStatus, creditHash, cycleHash,
				)
				if pendingStatus != "resetting" {
					require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = jsonb_set(
        extra,
        '{codex_auto_reset_credit_state,status}',
        TO_JSONB($2::TEXT)
    ),
    updated_at = NOW()
WHERE id = $1`, accountID, pendingStatus))
				}

				canonicalScope :=
					"openai_auto_reset_credit|service_principal:" + openAIAutoResetInt64(principalID)
				var recordID int64
				expiredAt := time.Now().UTC().Add(-time.Minute)
				require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    response_status, response_body, expires_at
)
VALUES ($1, $2, repeat('8', 64), 'succeeded', 200, $3, $4)
RETURNING id`,
					canonicalScope,
					openAIAutoResetStableKeyHash(accountID, creditHash, cycleHash),
					`{"result_code":"success","windows_reset":1}`,
					expiredAt,
				).Scan(&recordID))

				t.Cleanup(func() {
					_, _ = db.ExecContext(context.Background(), `
UPDATE accounts
SET extra = extra - 'codex_auto_reset_credit_state',
    updated_at = NOW()
WHERE id = $1`, accountID)
					_, _ = db.ExecContext(context.Background(),
						`DELETE FROM idempotency_records WHERE id = $1`, recordID)
					_, _ = db.ExecContext(context.Background(),
						`DELETE FROM accounts WHERE id = $1`, accountID)
				})

				pendingDelete, pendingDeleteErr := db.ExecContext(
					ctx, cleanupSQL, recordID, time.Now().UTC(),
				)
				require.NoError(t, pendingDeleteErr)
				pendingDeleted, pendingDeletedErr := pendingDelete.RowsAffected()
				require.NoError(t, pendingDeletedErr)
				require.Zero(t, pendingDeleted,
					"expiry cleanup must retain terminal recovery input")

				require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = jsonb_set(
        extra,
        '{codex_auto_reset_credit_state,status}',
        '"success"'::jsonb
    ),
    updated_at = NOW()
WHERE id = $1`, accountID))
				terminalDelete, terminalDeleteErr := db.ExecContext(
					ctx, cleanupSQL, recordID, time.Now().UTC(),
				)
				require.NoError(t, terminalDeleteErr)
				terminalDeleted, terminalDeletedErr := terminalDelete.RowsAffected()
				require.NoError(t, terminalDeletedErr)
				require.Equal(t, int64(1), terminalDeleted,
					"expiry cleanup may delete the record after account recovery completes")
				require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
					`DELETE FROM accounts WHERE id = $1`, accountID))
			})
		}
	})

	t.Run("verified no-effect discard is atomic and prevents a blocked old update from reviving the record", func(t *testing.T) {
		const discardSQL = `
SELECT discard_openai_quota_auto_reset_protected_attempt_no_effect(
    $1, $2, $3, $4, $5, $6, $7::jsonb, $8
)`
		creditHash := strings.Repeat("7", 24)
		cycleHash := strings.Repeat("8", 24)
		accountID := insertOpenAIAutoResetAttemptAccount(
			t, ctx, db, "discard-matching-state", creditHash, cycleHash,
		)
		canonicalScope :=
			"openai_auto_reset_credit|service_principal:" + openAIAutoResetInt64(principalID)
		fingerprint := openAIAutoResetFingerprint(
			fmt.Sprintf("service_principal:%d", principalID),
			accountID,
			creditHash,
			cycleHash,
		)
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET platform = 'anthropic',
    status = 'disabled',
    deleted_at = statement_timestamp(),
    extra = extra || '{"unrelated":"preserve-me"}'::jsonb,
    updated_at = NOW()
WHERE id = $1`, accountID))
		var recordID int64
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    locked_until, expires_at
)
VALUES (
    $1, $2, $3, 'processing', statement_timestamp() + INTERVAL '5 minutes',
    statement_timestamp() + INTERVAL '2 days'
)
RETURNING id`,
			canonicalScope,
			openAIAutoResetStableKeyHash(accountID, creditHash, cycleHash),
			fingerprint,
		).Scan(&recordID))
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO openai_quota_auto_reset_protected_attempts (idempotency_record_id, account_id)
VALUES ($1, $2)`, recordID, accountID))

		auditCreatedAt := time.Now().UTC().Truncate(time.Microsecond)
		decisionRequestID := "reconcile-no-effect:" + strconv.FormatInt(recordID, 10)
		redeemRequestID := "11111111-1111-5111-8111-111111111111"
		auditExtra := openAIAutoResetReconciliationAuditExtra(
			accountID, recordID, fingerprint,
			"reconciled_no_effect", 0, "operator-ticket:no-effect", "migration-test",
		)
		missingEvidenceExtra := fmt.Sprintf(
			`{"account_id":%d,"decision_owner":"migration-test",`+
				`"idempotency_record_id":%d,"request_fingerprint":"%s",`+
				`"result_code":"reconciled_no_effect","windows_reset":0}`,
			accountID,
			recordID,
			fingerprint,
		)
		_, missingEvidenceErr := db.ExecContext(ctx, discardSQL,
			recordID, canonicalScope, fingerprint, accountID,
			auditCreatedAt, decisionRequestID, missingEvidenceExtra, true,
		)
		require.ErrorContains(t, missingEvidenceErr,
			"invalid OpenAI quota auto-reset no-effect discard input")
		var missingAuditCount int
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM audit_logs
WHERE action = 'system.openai.reset_credit.auto' AND request_id = $1`,
			decisionRequestID,
		).Scan(&missingAuditCount))
		require.Zero(t, missingAuditCount)
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, recordID, true)
		var missingFieldStatus string
		var missingFieldStatePresent bool
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT record.status,
       account.extra ? 'codex_auto_reset_credit_state'
FROM idempotency_records AS record
JOIN accounts AS account ON account.id = $2
WHERE record.id = $1`, recordID, accountID).Scan(
			&missingFieldStatus,
			&missingFieldStatePresent,
		))
		require.Equal(t, "processing", missingFieldStatus)
		require.True(t, missingFieldStatePresent)

		_, notDrainedErr := db.ExecContext(ctx, discardSQL,
			recordID, canonicalScope, fingerprint, accountID,
			auditCreatedAt, decisionRequestID, auditExtra, false,
		)
		require.ErrorContains(t, notDrainedErr,
			"invalid OpenAI quota auto-reset no-effect discard input")
		_, redeemIDErr := db.ExecContext(ctx, discardSQL,
			recordID, canonicalScope, fingerprint, accountID,
			auditCreatedAt, redeemRequestID, auditExtra, true,
		)
		require.ErrorContains(t, redeemIDErr,
			"invalid OpenAI quota auto-reset no-effect discard input")

		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
CREATE TABLE openai_quota_auto_reset_discard_blockers (
    idempotency_record_id BIGINT PRIMARY KEY
        REFERENCES idempotency_records(id) ON DELETE RESTRICT
)`))
		t.Cleanup(func() {
			_, _ = db.ExecContext(context.Background(),
				`DROP TABLE IF EXISTS openai_quota_auto_reset_discard_blockers`)
		})
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO openai_quota_auto_reset_discard_blockers (idempotency_record_id)
VALUES ($1)`, recordID))
		_, blockedDeleteErr := db.ExecContext(ctx, discardSQL,
			recordID, canonicalScope, fingerprint, accountID,
			auditCreatedAt, decisionRequestID, auditExtra, true,
		)
		require.ErrorContains(t, blockedDeleteErr,
			"openai_quota_auto_reset_discard_blockers",
			"a late parent-delete failure must roll the whole function statement back")

		var auditCount int
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM audit_logs
WHERE action = 'system.openai.reset_credit.auto' AND request_id = $1`,
			decisionRequestID,
		).Scan(&auditCount))
		require.Zero(t, auditCount)
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, recordID, true)
		var rolledBackStatus string
		var rolledBackExpiresAt time.Time
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT status, expires_at FROM idempotency_records WHERE id = $1`, recordID).Scan(
			&rolledBackStatus,
			&rolledBackExpiresAt,
		))
		require.Equal(t, "processing", rolledBackStatus)
		require.True(t, rolledBackExpiresAt.After(time.Now()),
			"the failed discard must not leave its forced expiry behind")
		var matchingStateStillPresent bool
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT extra ? 'codex_auto_reset_credit_state'
FROM accounts
WHERE id = $1`, accountID).Scan(&matchingStateStillPresent))
		require.True(t, matchingStateStillPresent,
			"the failed discard must roll the account state cleanup back")

		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
DELETE FROM openai_quota_auto_reset_discard_blockers
WHERE idempotency_record_id = $1`, recordID))
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
			`DROP TABLE openai_quota_auto_reset_discard_blockers`))

		discardConn, discardConnErr := db.Conn(ctx)
		require.NoError(t, discardConnErr)
		defer discardConn.Close()
		oldWorkerConn, oldWorkerConnErr := db.Conn(ctx)
		require.NoError(t, oldWorkerConnErr)
		defer oldWorkerConn.Close()
		var discardPID, oldWorkerPID int
		require.NoError(t, discardConn.QueryRowContext(ctx,
			`SELECT pg_backend_pid()`,
		).Scan(&discardPID))
		require.NoError(t, oldWorkerConn.QueryRowContext(ctx,
			`SELECT pg_backend_pid()`,
		).Scan(&oldWorkerPID))

		discardTx, discardTxErr := discardConn.BeginTx(ctx, nil)
		require.NoError(t, discardTxErr)
		committed := false
		defer func() {
			if !committed {
				_ = discardTx.Rollback()
			}
		}()
		var lockedID int64
		require.NoError(t, discardTx.QueryRowContext(ctx,
			`SELECT id FROM idempotency_records WHERE id = $1 FOR UPDATE`, recordID,
		).Scan(&lockedID))
		require.Equal(t, recordID, lockedID)

		type oldUpdateOutcome struct {
			affected int64
			err      error
		}
		oldUpdateResult := make(chan oldUpdateOutcome, 1)
		go func() {
			result, updateErr := oldWorkerConn.ExecContext(ctx, `
UPDATE idempotency_records
SET status = 'succeeded',
    response_status = 200,
    response_body = '{"windows_reset":99,"late_old_worker":true}',
    error_reason = NULL,
    locked_until = NULL,
    expires_at = statement_timestamp() + INTERVAL '1 day',
    updated_at = NOW()
WHERE id = $1`, recordID)
			if updateErr != nil {
				oldUpdateResult <- oldUpdateOutcome{err: updateErr}
				return
			}
			affected, affectedErr := result.RowsAffected()
			oldUpdateResult <- oldUpdateOutcome{affected: affected, err: affectedErr}
		}()

		var blockingQueryErr error
		require.Eventually(t, func() bool {
			var isBlocked bool
			blockingQueryErr = db.QueryRowContext(ctx,
				`SELECT $1 = ANY(pg_blocking_pids($2))`, discardPID, oldWorkerPID,
			).Scan(&isBlocked)
			return blockingQueryErr == nil && isBlocked
		}, 5*time.Second, 20*time.Millisecond,
			"the old worker UPDATE must wait on the discard row lock")
		require.NoError(t, blockingQueryErr)
		select {
		case earlyOutcome := <-oldUpdateResult:
			require.Failf(t, "old worker UPDATE returned before discard commit",
				"outcome: %+v", earlyOutcome)
		default:
		}

		_, discardErr := discardTx.ExecContext(ctx, discardSQL,
			recordID, canonicalScope, fingerprint, accountID,
			auditCreatedAt, decisionRequestID, auditExtra, true,
		)
		require.NoError(t, discardErr)
		require.NoError(t, discardTx.Commit())
		committed = true

		select {
		case oldOutcome := <-oldUpdateResult:
			require.NoError(t, oldOutcome.err)
			require.Zero(t, oldOutcome.affected,
				"a blocked old UPDATE must not revive the deleted parent")
		case <-time.After(5 * time.Second):
			require.Fail(t, "old worker UPDATE remained blocked after discard commit")
		}

		var recordExists, markerExists, matchingStateExists bool
		var unrelatedExtra, storedPlatform, storedAccountStatus string
		var storedDeletedAt time.Time
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT EXISTS (SELECT 1 FROM idempotency_records WHERE id = $1),
       EXISTS (
           SELECT 1 FROM openai_quota_auto_reset_protected_attempts
           WHERE idempotency_record_id = $1
       ),
       EXISTS (
	           SELECT 1 FROM accounts
	           WHERE id = $2 AND extra ? 'codex_auto_reset_credit_state'
	       ),
       account.extra ->> 'unrelated',
       account.platform,
       account.status,
       account.deleted_at
FROM accounts AS account
WHERE account.id = $2`, recordID, accountID).Scan(
			&recordExists,
			&markerExists,
			&matchingStateExists,
			&unrelatedExtra,
			&storedPlatform,
			&storedAccountStatus,
			&storedDeletedAt,
		))
		require.False(t, recordExists)
		require.False(t, markerExists)
		require.False(t, matchingStateExists,
			"discard must clear the matching recovery state so a new attempt can claim")
		require.Equal(t, "preserve-me", unrelatedExtra)
		require.Equal(t, "anthropic", storedPlatform)
		require.Equal(t, "disabled", storedAccountStatus)
		require.False(t, storedDeletedAt.IsZero())
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM audit_logs
WHERE action = 'system.openai.reset_credit.auto'
  AND request_id = $1
  AND status_code = 409
  AND extra = $2::jsonb`, decisionRequestID, auditExtra).Scan(&auditCount))
		require.Equal(t, 1, auditCount)

		_, exactRetryErr := db.ExecContext(ctx, discardSQL,
			recordID, canonicalScope, fingerprint, accountID,
			auditCreatedAt, decisionRequestID, auditExtra, true,
		)
		require.NoError(t, exactRetryErr,
			"an exact retry after parent deletion must verify the existing decision audit")

		var reclaimedRecordID int64
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    locked_until, expires_at
)
VALUES (
    $1, $2, $3, 'processing', statement_timestamp() + INTERVAL '5 minutes',
    statement_timestamp() + INTERVAL '8 days'
)
RETURNING id`,
			canonicalScope,
			openAIAutoResetStableKeyHash(accountID, creditHash, cycleHash),
			fingerprint,
		).Scan(&reclaimedRecordID),
			"the exact stable key must be claimable again after verified no-effect discard")
		cleanupOpenAIAutoResetProtectedRecord(t, ctx, db, reclaimedRecordID)

		_, noEffectReapplyErr := db.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, noEffectReapplyErr,
			"a canonical no-effect audit with cleared state must survive raw reapply")
		conflictingExtra := openAIAutoResetReconciliationAuditExtra(
			accountID, recordID, fingerprint,
			"reconciled_no_effect", 0, "operator-ticket:different", "migration-test",
		)
		_, conflictingRetryErr := db.ExecContext(ctx, discardSQL,
			recordID, canonicalScope, fingerprint, accountID,
			auditCreatedAt, decisionRequestID, conflictingExtra, true,
		)
		require.ErrorContains(t, conflictingRetryErr,
			"existing OpenAI quota auto-reset no-effect discard audit mismatch")

		_, normalAuditErr := db.ExecContext(ctx, `
INSERT INTO audit_logs (
    created_at, actor_service_principal_id, auth_method, action, method,
    path, request_id, status_code, extra
)
VALUES (
    $1, $2, 'service_principal', 'system.openai.reset_credit.auto', 'SYSTEM',
    $3, $4, 200, $5::jsonb
)`,
			time.Now().UTC().Truncate(time.Microsecond),
			principalID,
			fmt.Sprintf("/system/openai/accounts/%d/auto-reset-credit", accountID),
			redeemRequestID,
			fmt.Sprintf(`{"account_id":%d,"result_code":"success","windows_reset":1}`, accountID),
		)
		require.NoError(t, normalAuditErr,
			"the no-effect decision namespace must not consume the upstream redeem request id")

		newerCreditHash := strings.Repeat("a", 24)
		newerCycleHash := strings.Repeat("b", 24)
		newerAccountID := insertOpenAIAutoResetAttemptAccount(
			t, ctx, db, "discard-newer-state", newerCreditHash, newerCycleHash,
		)
		var olderRecordID int64
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    locked_until, expires_at
)
VALUES (
    $1, $2, repeat('c', 64), 'processing',
    statement_timestamp() + INTERVAL '5 minutes', statement_timestamp() + INTERVAL '1 day'
)
RETURNING id`,
			canonicalScope,
			openAIAutoResetStableKeyHash(
				newerAccountID, strings.Repeat("d", 24), strings.Repeat("e", 24),
			),
		).Scan(&olderRecordID))
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO openai_quota_auto_reset_protected_attempts (idempotency_record_id, account_id)
VALUES ($1, $2)`, olderRecordID, newerAccountID))
		newerAuditCreatedAt := time.Now().UTC().Truncate(time.Microsecond)
		newerDecisionID := "reconcile-no-effect:" + strconv.FormatInt(olderRecordID, 10)
		newerAuditExtra := openAIAutoResetReconciliationAuditExtra(
			newerAccountID, olderRecordID, strings.Repeat("c", 64),
			"reconciled_no_effect", 0, "operator-ticket:older-attempt", "migration-test",
		)
		_, newerDiscardErr := db.ExecContext(ctx, discardSQL,
			olderRecordID, canonicalScope, strings.Repeat("c", 64), newerAccountID,
			newerAuditCreatedAt, newerDecisionID, newerAuditExtra, true,
		)
		require.NoError(t, newerDiscardErr)
		var preservedCreditHash, preservedCycleHash string
		require.NoError(t, db.QueryRowContext(ctx, `
SELECT extra -> 'codex_auto_reset_credit_state' ->> 'attempt_credit_hash',
       extra -> 'codex_auto_reset_credit_state' ->> 'attempt_cycle_hash'
FROM accounts
WHERE id = $1`, newerAccountID).Scan(&preservedCreditHash, &preservedCycleHash))
		require.Equal(t, newerCreditHash, preservedCreditHash)
		require.Equal(t, newerCycleHash, preservedCycleHash)
	})

	t.Run("raw reapply leaves current retryable attempt reclaimable", func(t *testing.T) {
		currentScope := "openai_auto_reset_credit|service_principal:" + openAIAutoResetInt64(principalID)
		var currentID int64
		require.NoError(t, db.QueryRowContext(ctx, `
	INSERT INTO idempotency_records (
	    scope, idempotency_key_hash, request_fingerprint, status,
	    error_reason, locked_until, expires_at
	)
	VALUES (
	    $1, repeat('2', 63) || '9', repeat('3', 63) || '9',
	    'failed_retryable', 'CURRENT_RETRYABLE', statement_timestamp() - INTERVAL '1 minute',
	    statement_timestamp() + INTERVAL '1 day'
	)
	RETURNING id`, currentScope).Scan(&currentID))
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, currentID, false)

		_, reapplyErr := db.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, reapplyErr)
		assertOpenAIAutoResetProtectionShape(t, ctx, db)
		assertOpenAIAutoResetAttemptProtected(t, ctx, db, currentID, false)

		var status, errorReason string
		require.NoError(t, db.QueryRowContext(ctx, `
	SELECT status, error_reason
	FROM idempotency_records
	WHERE id = $1`, currentID).Scan(&status, &errorReason))
		require.Equal(t, "failed_retryable", status)
		require.Equal(t, "CURRENT_RETRYABLE", errorReason)

		reclaimResult, reclaimErr := db.ExecContext(ctx, `
	UPDATE idempotency_records
	SET status = 'processing',
	    error_reason = NULL,
	    locked_until = statement_timestamp() + INTERVAL '5 minutes',
	    expires_at = statement_timestamp() + INTERVAL '1 day',
	    updated_at = NOW()
	WHERE id = $1
	  AND status = 'failed_retryable'
	  AND locked_until <= statement_timestamp()`, currentID)
		require.NoError(t, reclaimErr)
		reclaimed, reclaimedErr := reclaimResult.RowsAffected()
		require.NoError(t, reclaimedErr)
		require.Equal(t, int64(1), reclaimed)

		_, finalizeErr := db.ExecContext(ctx, `
	UPDATE idempotency_records
	SET status = 'succeeded',
	    response_status = 200,
	    response_body = '{}',
	    locked_until = NULL,
	    expires_at = statement_timestamp() - INTERVAL '1 minute',
	    updated_at = NOW()
	WHERE id = $1 AND status = 'processing'`, currentID)
		require.NoError(t, finalizeErr, "unmarked current attempts must remain writable by the atomic finalizer")
		deleteResult, deleteErr := db.ExecContext(ctx,
			`DELETE FROM idempotency_records WHERE id = $1`, currentID)
		require.NoError(t, deleteErr)
		deleted, deletedErr := deleteResult.RowsAffected()
		require.NoError(t, deletedErr)
		require.Equal(t, int64(1), deleted)
	})

	t.Run("direct grant remove and add advances version across ABA", func(t *testing.T) {
		beforeVersion := readOpenAIAutoResetWorkerVersion(t, ctx, db, principalID)
		_, deleteErr := db.ExecContext(ctx, `
DELETE FROM service_principal_worker_permissions
WHERE service_principal_id = $1 AND permission_id = $2`, principalID, permissionID)
		require.NoError(t, deleteErr)
		require.Equal(t, beforeVersion+1, readOpenAIAutoResetWorkerVersion(t, ctx, db, principalID))

		_, insertErr := db.ExecContext(ctx, `
INSERT INTO service_principal_worker_permissions (service_principal_id, permission_id)
VALUES ($1, $2)`, principalID, permissionID)
		require.NoError(t, insertErr)
		require.Equal(t, beforeVersion+2, readOpenAIAutoResetWorkerVersion(t, ctx, db, principalID))
		assertOpenAIAutoResetWorkerGrantShape(t, ctx, db, principalID, permissionID)
	})

	t.Run("target scope uniqueness collision rolls back", func(t *testing.T) {
		const keyHash = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		oldScope := "openai_auto_reset_credit|account:303"
		newScope := "openai_auto_reset_credit|service_principal:" + openAIAutoResetInt64(principalID)
		var oldID, newID int64
		restoreFence := disableOpenAIAutoResetLegacyScopeFence(t, ctx, db)
		defer restoreFence()
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status, expires_at
)
VALUES ($1, $2, repeat('1', 64), 'processing', statement_timestamp() + INTERVAL '1 day')
RETURNING id`, oldScope, keyHash).Scan(&oldID))
		restoreFence()
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status, expires_at
)
VALUES ($1, $2, repeat('2', 64), 'succeeded', statement_timestamp() + INTERVAL '1 day')
RETURNING id`, newScope, keyHash).Scan(&newID))

		beforeVersion := readOpenAIAutoResetWorkerVersion(t, ctx, db, principalID)
		_, reapplyErr := db.ExecContext(ctx, string(migrationSQL))
		require.Error(t, reapplyErr)
		require.Equal(t, beforeVersion, readOpenAIAutoResetWorkerVersion(t, ctx, db, principalID))
		assertOpenAIAutoResetProtectionShape(t, ctx, db)
		assertOpenAIAutoResetIdempotencyRecord(t, ctx, db, oldID, oldScope, strings.Repeat("1", 64))
		assertOpenAIAutoResetIdempotencyRecord(t, ctx, db, newID, newScope, strings.Repeat("2", 64))
	})

	t.Run("expired processing Service Principal outcome is retained for reconciliation", func(t *testing.T) {
		targetScope := "openai_auto_reset_credit|service_principal:" + openAIAutoResetInt64(principalID)
		type expiryRecord struct {
			scope       string
			status      string
			key         string
			fingerprint string
			kept        bool
			id          int64
		}
		records := []expiryRecord{
			{
				scope: targetScope, status: "processing", key: strings.Repeat("3", 64),
				fingerprint: strings.Repeat("6", 64), kept: true,
			},
			{
				scope: targetScope, status: "succeeded", key: strings.Repeat("4", 64),
				fingerprint: strings.Repeat("6", 64),
			},
			{
				scope: "openai_auto_reset_credit", status: "processing", key: strings.Repeat("b", 64),
				fingerprint: strings.Repeat("7", 64), kept: true,
			},
			{
				scope: "openai_auto_reset_credit", status: "processing", key: strings.Repeat("d", 64),
				fingerprint: "upgrade-fence:actor-qualified:v1",
			},
			{
				scope: "other_expired_processing_scope", status: "processing", key: strings.Repeat("5", 64),
				fingerprint: strings.Repeat("6", 64),
			},
		}
		for index := range records {
			record := &records[index]
			require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    response_status, response_body, expires_at
)
VALUES (
	    $1, $2, $3, $4::VARCHAR,
	    CASE WHEN $4::TEXT = 'succeeded' THEN 200 ELSE NULL END,
	    CASE WHEN $4::TEXT = 'succeeded' THEN '{}' ELSE NULL END,
	    statement_timestamp() - INTERVAL '1 minute'
)
RETURNING id`, record.scope, record.key, record.fingerprint, record.status).Scan(&record.id))
			result, deleteErr := db.ExecContext(ctx, `DELETE FROM idempotency_records WHERE id = $1`, record.id)
			require.NoError(t, deleteErr)
			affected, affectedErr := result.RowsAffected()
			require.NoError(t, affectedErr)
			if record.kept {
				require.Zero(t, affected)
				var status string
				require.NoError(t, db.QueryRowContext(ctx,
					`SELECT status FROM idempotency_records WHERE id = $1`, record.id,
				).Scan(&status))
				require.Equal(t, "processing", status)
			} else {
				require.Equal(t, int64(1), affected)
			}
		}
		t.Cleanup(func() {
			for _, record := range records {
				if !record.kept {
					continue
				}
				_, _ = db.ExecContext(context.Background(), `
UPDATE idempotency_records SET status = 'failed_retryable' WHERE id = $1`, record.id)
				_, _ = db.ExecContext(context.Background(), `DELETE FROM idempotency_records WHERE id = $1`, record.id)
			}
		})
	})
}

func TestOpenAIQuotaAutoResetActorMigrationRejectsMalformedAuditIndexPostgres(t *testing.T) {
	db := newRoleCachePostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, repository.ApplyMigrations(ctx, db))

	migrationSQL, err := dbmigrations.FS.ReadFile("243_openai_quota_auto_reset_actor.sql")
	require.NoError(t, err)
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
		`DROP INDEX idx_audit_logs_openai_auto_reset_request_id`))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
CREATE INDEX idx_audit_logs_openai_auto_reset_request_id
ON audit_logs (request_id, action)
WHERE action = 'system.openai.reset_credit.auto' AND request_id <> ''`))

	_, migrationErr := db.ExecContext(ctx, string(migrationSQL))
	require.ErrorContains(t, migrationErr, "unsafe existing idx_audit_logs_openai_auto_reset_request_id index shape")

	var isUnique bool
	var firstColumn string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT index_row.indisunique,
       pg_get_indexdef(index_row.indexrelid, 1, TRUE)
FROM pg_index AS index_row
WHERE index_row.indexrelid = 'idx_audit_logs_openai_auto_reset_request_id'::regclass`).Scan(
		&isUnique,
		&firstColumn,
	))
	require.False(t, isUnique)
	require.Equal(t, "request_id", firstColumn)
}

func TestOpenAIQuotaAutoResetActorMigrationRejectsReservedCodeCollisionsPostgres(t *testing.T) {
	db := newRoleCachePostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, repository.ApplyMigrations(ctx, db))

	migrationSQL, err := dbmigrations.FS.ReadFile("243_openai_quota_auto_reset_actor.sql")
	require.NoError(t, err)
	removeOpenAIAutoResetWorkerSeed(t, ctx, db)

	var grantorID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role, status)
VALUES ('auto-reset-collision@example.test', 'not-a-real-password-hash', 'admin', 'active')
RETURNING id`).Scan(&grantorID))
	var roleID int64
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT id FROM roles WHERE code = 'platform_operator'`).Scan(&roleID))

	t.Run("active roleful collision is untouched", func(t *testing.T) {
		var collisionID int64
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO service_principals (code, name, status)
VALUES ($1, 'Pre-existing Active Principal', 'active')
RETURNING id`, openAIAutoResetWorkerCode).Scan(&collisionID))
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO service_principal_roles (service_principal_id, role_id, granted_by_user_id)
VALUES ($1, $2, $3)`, collisionID, roleID, grantorID))

		_, migrationErr := db.ExecContext(ctx, string(migrationSQL))
		require.Error(t, migrationErr)
		assertOpenAIAutoResetCollision(t, ctx, db, collisionID, "Pre-existing Active Principal", "active", 1, 0)
		assertOpenAIAutoResetReservedPermissionMissing(t, ctx, db)

		require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
			`DELETE FROM service_principal_roles WHERE service_principal_id = $1`, collisionID))
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
			`DELETE FROM service_principals WHERE id = $1`, collisionID))
	})

	t.Run("exact-looking disabled collision is not activated", func(t *testing.T) {
		var permissionID int64
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO permissions (code, description)
VALUES ($1, $2)
RETURNING id`, openAIAutoResetPermissionCode, openAIAutoResetPermissionDetail).Scan(&permissionID))
		var collisionID int64
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO service_principals (code, name, status)
VALUES ($1, 'OpenAI Quota Auto-Reset Worker', 'disabled')
RETURNING id`, openAIAutoResetWorkerCode).Scan(&collisionID))
		require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
INSERT INTO service_principal_worker_permissions (service_principal_id, permission_id)
VALUES ($1, $2)`, collisionID, permissionID))
		beforeVersion := readOpenAIAutoResetWorkerVersion(t, ctx, db, collisionID)

		_, migrationErr := db.ExecContext(ctx, string(migrationSQL))
		require.Error(t, migrationErr)
		assertOpenAIAutoResetCollision(t, ctx, db, collisionID, "OpenAI Quota Auto-Reset Worker", "disabled", 0, 1)
		require.Equal(t, beforeVersion, readOpenAIAutoResetWorkerVersion(t, ctx, db, collisionID))

		removeOpenAIAutoResetWorkerSeed(t, ctx, db)
	})

	t.Run("absence is a valid first application", func(t *testing.T) {
		_, migrationErr := db.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, migrationErr)
		principalID, permissionID, version := readOpenAIAutoResetWorker(t, ctx, db)
		require.Equal(t, int64(2), version)
		assertOpenAIAutoResetWorkerGrantShape(t, ctx, db, principalID, permissionID)
	})
}

func TestOpenAIQuotaAutoResetActorMigrationRejectsMalformedExistingTablePostgres(t *testing.T) {
	db := newRoleCachePostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, repository.ApplyMigrations(ctx, db))

	migrationSQL, err := dbmigrations.FS.ReadFile("243_openai_quota_auto_reset_actor.sql")
	require.NoError(t, err)
	removeOpenAIAutoResetWorkerSeed(t, ctx, db)
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
		`DROP TABLE service_principal_worker_permissions`))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
CREATE TABLE service_principal_worker_permissions (
    service_principal_id BIGINT NOT NULL,
    permission_id BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT service_principal_worker_permissions_pkey
        PRIMARY KEY (service_principal_id, permission_id),
    CONSTRAINT sp_worker_permissions_principal_id_fkey
        FOREIGN KEY (service_principal_id)
        REFERENCES service_principals(id) ON DELETE RESTRICT,
    CONSTRAINT sp_worker_permissions_permission_id_fkey
        FOREIGN KEY (permission_id)
        REFERENCES permissions(id) ON DELETE CASCADE
)`))

	_, migrationErr := db.ExecContext(ctx, string(migrationSQL))
	require.ErrorContains(t, migrationErr, "unsafe existing service_principal_worker_permissions table shape")
	assertOpenAIAutoResetReservedPermissionMissing(t, ctx, db)

	var principalCount int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM service_principals WHERE code = $1`, openAIAutoResetWorkerCode).Scan(&principalCount))
	require.Zero(t, principalCount)
}

func assertOpenAIAutoResetWorkerTableShape(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var columnCount int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'service_principal_worker_permissions'
  AND (
      (column_name = 'service_principal_id' AND data_type = 'bigint' AND is_nullable = 'NO')
      OR (column_name = 'permission_id' AND data_type = 'bigint' AND is_nullable = 'NO')
      OR (
          column_name = 'created_at'
          AND data_type = 'timestamp with time zone'
          AND is_nullable = 'NO'
          AND column_default = 'now()'
      )
  )`).Scan(&columnCount))
	require.Equal(t, 3, columnCount)

	type constraintShape struct {
		kind       string
		definition string
		deleteType string
	}
	rows, err := db.QueryContext(ctx, `
SELECT conname, contype::TEXT, pg_get_constraintdef(oid), confdeltype::TEXT
FROM pg_constraint
WHERE conrelid = 'service_principal_worker_permissions'::regclass
  AND contype IN ('p', 'f')
ORDER BY conname`)
	require.NoError(t, err)
	defer rows.Close()
	constraints := make(map[string]constraintShape)
	for rows.Next() {
		var name string
		var shape constraintShape
		require.NoError(t, rows.Scan(&name, &shape.kind, &shape.definition, &shape.deleteType))
		constraints[name] = shape
	}
	require.NoError(t, rows.Err())
	require.Len(t, constraints, 3)
	require.Equal(t, constraintShape{
		kind:       "p",
		definition: "PRIMARY KEY (service_principal_id, permission_id)",
		deleteType: " ",
	}, constraints["service_principal_worker_permissions_pkey"])
	require.Equal(t, "f", constraints["sp_worker_permissions_principal_id_fkey"].kind)
	require.Contains(t, constraints["sp_worker_permissions_principal_id_fkey"].definition,
		"FOREIGN KEY (service_principal_id) REFERENCES service_principals(id) ON DELETE CASCADE")
	require.Equal(t, "c", constraints["sp_worker_permissions_principal_id_fkey"].deleteType)
	require.Equal(t, "f", constraints["sp_worker_permissions_permission_id_fkey"].kind)
	require.Contains(t, constraints["sp_worker_permissions_permission_id_fkey"].definition,
		"FOREIGN KEY (permission_id) REFERENCES permissions(id) ON DELETE CASCADE")
	require.Equal(t, "c", constraints["sp_worker_permissions_permission_id_fkey"].deleteType)
}

func assertOpenAIAutoResetProtectionShape(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var protectedColumnCount, backfillColumnCount int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'openai_quota_auto_reset_protected_attempts'
	  AND (
	      (column_name = 'idempotency_record_id' AND data_type = 'bigint'
	       AND is_nullable = 'NO' AND column_default IS NULL)
	      OR (column_name = 'account_id' AND data_type = 'bigint'
	          AND is_nullable = 'NO' AND column_default IS NULL)
	      OR (column_name = 'protected_at' AND data_type = 'timestamp with time zone'
	          AND is_nullable = 'NO' AND column_default = 'now()')
	      OR (column_name = 'reconciled_at' AND data_type = 'timestamp with time zone'
	          AND is_nullable = 'YES' AND column_default IS NULL)
	      OR (column_name = 'reconciliation_audit_request_id'
	          AND data_type = 'character varying' AND character_maximum_length = 64
	          AND is_nullable = 'YES' AND column_default IS NULL)
	  )`).Scan(&protectedColumnCount))
	require.Equal(t, 5, protectedColumnCount)
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM information_schema.columns
WHERE table_schema = current_schema()
  AND table_name = 'openai_quota_auto_reset_protection_backfill'
  AND (
      (column_name = 'completed' AND data_type = 'boolean'
       AND is_nullable = 'NO' AND column_default IS NULL)
      OR (column_name = 'completed_at' AND data_type = 'timestamp with time zone'
          AND is_nullable = 'NO' AND column_default = 'now()')
  )`).Scan(&backfillColumnCount))
	require.Equal(t, 2, backfillColumnCount)

	type constraintShape struct {
		kind       string
		definition string
		deleteType string
	}
	rows, err := db.QueryContext(ctx, `
SELECT conname, contype::TEXT, pg_get_constraintdef(oid), confdeltype::TEXT
FROM pg_constraint
WHERE conrelid = 'openai_quota_auto_reset_protected_attempts'::regclass
  AND contype IN ('p', 'f')
ORDER BY conname`)
	require.NoError(t, err)
	protectedConstraints := make(map[string]constraintShape)
	for rows.Next() {
		var name string
		var shape constraintShape
		require.NoError(t, rows.Scan(&name, &shape.kind, &shape.definition, &shape.deleteType))
		protectedConstraints[name] = shape
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Len(t, protectedConstraints, 2)
	require.Equal(t, "p", protectedConstraints["openai_auto_reset_protected_attempts_pkey"].kind)
	require.Equal(t,
		"PRIMARY KEY (idempotency_record_id)",
		protectedConstraints["openai_auto_reset_protected_attempts_pkey"].definition,
	)
	require.Equal(t, "f", protectedConstraints["openai_auto_reset_protected_attempts_record_id_fkey"].kind)
	require.Contains(t,
		protectedConstraints["openai_auto_reset_protected_attempts_record_id_fkey"].definition,
		"FOREIGN KEY (idempotency_record_id) REFERENCES idempotency_records(id) ON DELETE RESTRICT",
	)
	require.Equal(t, "r", protectedConstraints["openai_auto_reset_protected_attempts_record_id_fkey"].deleteType)

	var protectedAccountCheck, protectedReconciledCheck string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT pg_get_constraintdef(oid)
FROM pg_constraint
WHERE conrelid = 'openai_quota_auto_reset_protected_attempts'::regclass
  AND conname = 'openai_auto_reset_protected_attempts_account_id_check'
  AND contype = 'c'`).Scan(&protectedAccountCheck))
	require.Equal(t, "CHECK ((account_id > 0))", protectedAccountCheck)
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT pg_get_constraintdef(oid)
FROM pg_constraint
WHERE conrelid = 'openai_quota_auto_reset_protected_attempts'::regclass
  AND conname = 'openai_auto_reset_protected_attempts_reconciled_check'
  AND contype = 'c'`).Scan(&protectedReconciledCheck))
	require.Contains(t, protectedReconciledCheck, "reconciled_at IS NULL")
	require.Contains(t, protectedReconciledCheck, "reconciliation_audit_request_id IS NULL")

	var backfillPrimaryKey, backfillCheck string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT
    MAX(pg_get_constraintdef(oid)) FILTER (
        WHERE conname = 'openai_auto_reset_protection_backfill_pkey'
    ),
    MAX(pg_get_constraintdef(oid)) FILTER (
        WHERE conname = 'openai_auto_reset_protection_backfill_singleton'
    )
FROM pg_constraint
WHERE conrelid = 'openai_quota_auto_reset_protection_backfill'::regclass`).Scan(
		&backfillPrimaryKey,
		&backfillCheck,
	))
	require.Equal(t, "PRIMARY KEY (completed)", backfillPrimaryKey)
	require.Equal(t, "CHECK (completed)", backfillCheck)

	var completedRows int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT COUNT(*) FROM openai_quota_auto_reset_protection_backfill WHERE completed`).Scan(&completedRows))
	require.Equal(t, 1, completedRows)

	var functionName, triggerDefinition, triggerEnabled string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT procedure.proname,
       pg_get_triggerdef(trigger_row.oid, TRUE),
       trigger_row.tgenabled::TEXT
FROM pg_trigger AS trigger_row
JOIN pg_proc AS procedure ON procedure.oid = trigger_row.tgfoid
WHERE trigger_row.tgrelid = 'idempotency_records'::regclass
  AND trigger_row.tgname = 'idempotency_records_openai_auto_reset_protected_attempt_guard'
  AND NOT trigger_row.tgisinternal`).Scan(
		&functionName,
		&triggerDefinition,
		&triggerEnabled,
	))
	require.Equal(t, "guard_openai_quota_auto_reset_protected_attempt", functionName)
	require.Contains(t, triggerDefinition, "BEFORE")
	require.Contains(t, triggerDefinition, "DELETE")
	require.Contains(t, triggerDefinition, "UPDATE")
	require.Contains(t, triggerDefinition, "ON idempotency_records")
	require.Equal(t, "O", triggerEnabled)

	var expiryFunctionName, expiryTriggerDefinition, expiryTriggerEnabled string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT procedure.proname,
       pg_get_triggerdef(trigger_row.oid, TRUE),
       trigger_row.tgenabled::TEXT
FROM pg_trigger AS trigger_row
JOIN pg_proc AS procedure ON procedure.oid = trigger_row.tgfoid
WHERE trigger_row.tgrelid = 'idempotency_records'::regclass
  AND trigger_row.tgname = 'idempotency_records_expiry_delete_guard'
  AND NOT trigger_row.tgisinternal`).Scan(
		&expiryFunctionName,
		&expiryTriggerDefinition,
		&expiryTriggerEnabled,
	))
	require.Equal(t, "guard_idempotency_record_expiry_delete", expiryFunctionName)
	require.Contains(t, expiryTriggerDefinition, "BEFORE DELETE ON idempotency_records")
	require.Equal(t, "O", expiryTriggerEnabled)
	require.Less(t,
		"idempotency_records_expiry_delete_guard",
		"idempotency_records_openai_auto_reset_protected_attempt_guard",
		"PostgreSQL fires same-event triggers in name order, so future expiry must short-circuit first",
	)

	var reconcileArgumentTypes, reconcileResultType string
	var publicCanExecuteReconcile, reconcileSecurityDefiner, reconcileOwnerIsCurrent bool
	var reconcileNonOwnerExecuteCount int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT pg_get_function_identity_arguments(procedure.oid),
       pg_get_function_result(procedure.oid),
       has_function_privilege('public', procedure.oid, 'EXECUTE'),
       procedure.prosecdef,
       procedure.proowner = (
           SELECT role_row.oid FROM pg_roles AS role_row WHERE role_row.rolname = CURRENT_USER
       ),
       (
           SELECT COUNT(*)
           FROM aclexplode(COALESCE(
               procedure.proacl,
               acldefault('f', procedure.proowner)
           )) AS privilege
           WHERE privilege.privilege_type = 'EXECUTE'
             AND privilege.grantee <> procedure.proowner
       )
FROM pg_proc AS procedure
WHERE procedure.proname = 'reconcile_openai_quota_auto_reset_protected_attempt'
  AND procedure.pronamespace = current_schema()::regnamespace`).Scan(
		&reconcileArgumentTypes,
		&reconcileResultType,
		&publicCanExecuteReconcile,
		&reconcileSecurityDefiner,
		&reconcileOwnerIsCurrent,
		&reconcileNonOwnerExecuteCount,
	))
	require.Equal(t,
		"p_idempotency_record_id bigint, p_actor_qualified_scope text, "+
			"p_request_fingerprint text, p_account_id bigint, "+
			"p_audit_created_at timestamp with time zone, "+
			"p_audit_request_id text, "+
			"p_audit_status_code integer, p_audit_extra jsonb",
		reconcileArgumentTypes,
	)
	require.Equal(t, "void", reconcileResultType)
	require.False(t, publicCanExecuteReconcile)
	require.False(t, reconcileSecurityDefiner)
	require.True(t, reconcileOwnerIsCurrent)
	require.Zero(t, reconcileNonOwnerExecuteCount)

	var discardArgumentTypes, discardResultType string
	var publicCanExecuteDiscard, discardSecurityDefiner, discardOwnerIsCurrent bool
	var discardNonOwnerExecuteCount int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT pg_get_function_identity_arguments(procedure.oid),
       pg_get_function_result(procedure.oid),
       has_function_privilege('public', procedure.oid, 'EXECUTE'),
       procedure.prosecdef,
       procedure.proowner = (
           SELECT role_row.oid FROM pg_roles AS role_row WHERE role_row.rolname = CURRENT_USER
       ),
       (
           SELECT COUNT(*)
           FROM aclexplode(COALESCE(
               procedure.proacl,
               acldefault('f', procedure.proowner)
           )) AS privilege
           WHERE privilege.privilege_type = 'EXECUTE'
             AND privilege.grantee <> procedure.proowner
       )
FROM pg_proc AS procedure
WHERE procedure.proname = 'discard_openai_quota_auto_reset_protected_attempt_no_effect'
  AND procedure.pronamespace = current_schema()::regnamespace`).Scan(
		&discardArgumentTypes,
		&discardResultType,
		&publicCanExecuteDiscard,
		&discardSecurityDefiner,
		&discardOwnerIsCurrent,
		&discardNonOwnerExecuteCount,
	))
	require.Equal(t,
		"p_idempotency_record_id bigint, p_actor_qualified_scope text, "+
			"p_request_fingerprint text, p_account_id bigint, "+
			"p_audit_created_at timestamp with time zone, p_audit_request_id text, "+
			"p_audit_extra jsonb, p_old_fleet_drained boolean",
		discardArgumentTypes,
	)
	require.Equal(t, "void", discardResultType)
	require.False(t, publicCanExecuteDiscard)
	require.False(t, discardSecurityDefiner)
	require.True(t, discardOwnerIsCurrent)
	require.Zero(t, discardNonOwnerExecuteCount)

	var oldExpiryOverloadExists bool
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT to_regprocedure(
    'reconcile_openai_quota_auto_reset_protected_attempt(bigint,text,text,bigint,integer,text,timestamptz,timestamptz,text,integer,jsonb)'
) IS NOT NULL`).Scan(&oldExpiryOverloadExists))
	require.False(t, oldExpiryOverloadExists)
	var oldResponseOverloadExists bool
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT to_regprocedure(
    'reconcile_openai_quota_auto_reset_protected_attempt(bigint,text,text,bigint,integer,text,timestamptz,text,integer,jsonb)'
) IS NOT NULL`).Scan(&oldResponseOverloadExists))
	require.False(t, oldResponseOverloadExists)
}

func assertOpenAIAutoResetAuditIndexShape(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var (
		tableName      string
		accessMethod   string
		isUnique       bool
		isValid        bool
		isReady        bool
		isImmediate    bool
		isExclusion    bool
		keyCount       int
		attributeCount int
		firstColumn    string
		secondColumn   string
		predicate      string
	)
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT index_row.indrelid::regclass::TEXT,
       access_method.amname,
       index_row.indisunique,
       index_row.indisvalid,
       index_row.indisready,
       index_row.indimmediate,
       index_row.indisexclusion,
       index_row.indnkeyatts,
       index_row.indnatts,
       pg_get_indexdef(index_row.indexrelid, 1, TRUE),
       pg_get_indexdef(index_row.indexrelid, 2, TRUE),
       pg_get_expr(index_row.indpred, index_row.indrelid, TRUE)
FROM pg_index AS index_row
JOIN pg_class AS index_relation
  ON index_relation.oid = index_row.indexrelid
JOIN pg_am AS access_method
  ON access_method.oid = index_relation.relam
WHERE index_row.indexrelid = 'idx_audit_logs_openai_auto_reset_request_id'::regclass`).Scan(
		&tableName,
		&accessMethod,
		&isUnique,
		&isValid,
		&isReady,
		&isImmediate,
		&isExclusion,
		&keyCount,
		&attributeCount,
		&firstColumn,
		&secondColumn,
		&predicate,
	))
	require.Equal(t, "audit_logs", tableName)
	require.Equal(t, "btree", accessMethod)
	require.True(t, isUnique)
	require.True(t, isValid)
	require.True(t, isReady)
	require.True(t, isImmediate)
	require.False(t, isExclusion)
	require.Equal(t, 2, keyCount)
	require.Equal(t, 2, attributeCount)
	require.Equal(t, "action", firstColumn)
	require.Equal(t, "request_id", secondColumn)
	require.Equal(t,
		"action::text = 'system.openai.reset_credit.auto'::text AND request_id::text <> ''::text",
		predicate,
	)
}

func assertOpenAIAutoResetLegacyScopeFenceShape(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var (
		functionName      string
		triggerDefinition string
		triggerEnabled    string
	)
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT procedure.proname,
       pg_get_triggerdef(trigger_row.oid, TRUE),
       trigger_row.tgenabled::TEXT
FROM pg_trigger AS trigger_row
JOIN pg_proc AS procedure ON procedure.oid = trigger_row.tgfoid
WHERE trigger_row.tgrelid = 'idempotency_records'::regclass
  AND trigger_row.tgname = 'idempotency_records_openai_auto_reset_account_scope_fence'
  AND NOT trigger_row.tgisinternal`).Scan(
		&functionName,
		&triggerDefinition,
		&triggerEnabled,
	))
	require.Equal(t, "reject_legacy_openai_auto_reset_account_scope", functionName)
	require.Contains(t, triggerDefinition, "BEFORE INSERT OR UPDATE OF scope ON idempotency_records")
	require.Equal(t, "O", triggerEnabled)
}

func disableOpenAIAutoResetLegacyScopeFence(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
) func() {
	t.Helper()
	const triggerName = "idempotency_records_openai_auto_reset_account_scope_fence"
	_, err := db.ExecContext(ctx, "ALTER TABLE idempotency_records DISABLE TRIGGER "+triggerName)
	require.NoError(t, err)

	restored := false
	return func() {
		if restored {
			return
		}
		restored = true
		_, restoreErr := db.ExecContext(ctx, "ALTER TABLE idempotency_records ENABLE TRIGGER "+triggerName)
		require.NoError(t, restoreErr)
	}
}

func disableOpenAIAutoResetExpiryDeleteGuard(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
) func() {
	t.Helper()
	const triggerName = "idempotency_records_expiry_delete_guard"
	_, err := db.ExecContext(ctx, "ALTER TABLE idempotency_records DISABLE TRIGGER "+triggerName)
	require.NoError(t, err)

	restored := false
	return func() {
		if restored {
			return
		}
		restored = true
		_, restoreErr := db.ExecContext(ctx, "ALTER TABLE idempotency_records ENABLE TRIGGER "+triggerName)
		require.NoError(t, restoreErr)
	}
}

func disableOpenAIAutoResetProtectedAttemptGuard(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
) func() {
	t.Helper()
	const triggerName = "idempotency_records_openai_auto_reset_protected_attempt_guard"
	_, err := db.ExecContext(ctx, "ALTER TABLE idempotency_records DISABLE TRIGGER "+triggerName)
	require.NoError(t, err)

	restored := false
	return func() {
		if restored {
			return
		}
		restored = true
		_, restoreErr := db.ExecContext(ctx, "ALTER TABLE idempotency_records ENABLE TRIGGER "+triggerName)
		require.NoError(t, restoreErr)
	}
}

func readOpenAIAutoResetWorker(t *testing.T, ctx context.Context, db *sql.DB) (int64, int64, int64) {
	t.Helper()
	var principalID, permissionID, version int64
	var principalName, principalStatus, permissionDescription string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT principal.id,
       permission.id,
       principal.authz_version,
       principal.name,
       principal.status,
       permission.description
FROM service_principals AS principal
JOIN service_principal_worker_permissions AS worker_permission
  ON worker_permission.service_principal_id = principal.id
JOIN permissions AS permission
  ON permission.id = worker_permission.permission_id
WHERE principal.code = $1
  AND permission.code = $2`, openAIAutoResetWorkerCode, openAIAutoResetPermissionCode).Scan(
		&principalID,
		&permissionID,
		&version,
		&principalName,
		&principalStatus,
		&permissionDescription,
	))
	require.Equal(t, "OpenAI Quota Auto-Reset Worker", principalName)
	require.Equal(t, "active", principalStatus)
	require.Equal(t, openAIAutoResetPermissionDetail, permissionDescription)
	return principalID, permissionID, version
}

func assertOpenAIAutoResetWorkerGrantShape(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	principalID int64,
	permissionID int64,
) {
	t.Helper()
	var roleCount, directCount, expectedDirectCount int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT
    (SELECT COUNT(*) FROM service_principal_roles WHERE service_principal_id = $1),
    (SELECT COUNT(*) FROM service_principal_worker_permissions WHERE service_principal_id = $1),
    (SELECT COUNT(*) FROM service_principal_worker_permissions
     WHERE service_principal_id = $1 AND permission_id = $2)`, principalID, permissionID).Scan(
		&roleCount,
		&directCount,
		&expectedDirectCount,
	))
	require.Zero(t, roleCount)
	require.Equal(t, 1, directCount)
	require.Equal(t, 1, expectedDirectCount)
}

func readOpenAIAutoResetWorkerVersion(t *testing.T, ctx context.Context, db *sql.DB, principalID int64) int64 {
	t.Helper()
	var version int64
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT authz_version FROM service_principals WHERE id = $1`, principalID,
	).Scan(&version))
	return version
}

func assertOpenAIAutoResetIdempotencyRecord(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	id int64,
	wantScope string,
	wantFingerprint string,
) {
	t.Helper()
	var scope, fingerprint string
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT scope, request_fingerprint FROM idempotency_records WHERE id = $1`, id).Scan(&scope, &fingerprint))
	require.Equal(t, wantScope, scope)
	require.Equal(t, wantFingerprint, fingerprint)
}

func assertOpenAIAutoResetFrozenRecord(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	id int64,
	wantScope string,
) {
	t.Helper()
	var scope, status string
	var errorReason sql.NullString
	var lockedUntil sql.NullTime
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT scope, status, error_reason, locked_until
FROM idempotency_records
WHERE id = $1`, id).Scan(&scope, &status, &errorReason, &lockedUntil))
	require.Equal(t, wantScope, scope)
	require.Equal(t, "processing", status)
	require.False(t, errorReason.Valid)
	require.False(t, lockedUntil.Valid)
}

func assertOpenAIAutoResetAttemptProtected(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	id int64,
	wantProtected bool,
) {
	t.Helper()
	var protected bool
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM openai_quota_auto_reset_protected_attempts
    WHERE idempotency_record_id = $1
)`, id).Scan(&protected))
	require.Equal(t, wantProtected, protected)
}

func assertOpenAIAutoResetProtectedAccount(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	recordID int64,
	wantAccountID int64,
) {
	t.Helper()
	var accountID int64
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT account_id
FROM openai_quota_auto_reset_protected_attempts
WHERE idempotency_record_id = $1`, recordID).Scan(&accountID))
	require.Equal(t, wantAccountID, accountID)
}

func insertOpenAIAutoResetAttemptAccount(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	name string,
	creditHash string,
	cycleHash string,
) int64 {
	t.Helper()
	var accountID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type, extra)
VALUES (
    $1,
    'openai',
    'oauth',
    jsonb_build_object(
        'codex_auto_reset_credit_state',
        jsonb_build_object(
            'status', 'resetting',
            'attempt_credit_hash', $2::TEXT,
            'attempt_cycle_hash', $3::TEXT
        )
    )
)
RETURNING id`, name, creditHash, cycleHash).Scan(&accountID))
	return accountID
}

func openAIAutoResetStableKeyHash(accountID int64, creditHash string, cycleHash string) string {
	stableKey := fmt.Sprintf("oarc:%d:%s:%s", accountID, creditHash, cycleHash)
	sum := sha256.Sum256([]byte(stableKey))
	return hex.EncodeToString(sum[:])
}

func openAIAutoResetFingerprint(actorScope string, accountID int64, creditHash string, cycleHash string) string {
	payload := fmt.Sprintf(
		`{"account_id":%d,"credit_hash":"%s","cycle_hash":"%s"}`,
		accountID,
		creditHash,
		cycleHash,
	)
	sum := sha256.Sum256([]byte(
		"POST\n/system/openai/reset-credit/auto\n" + actorScope + "\n" + payload,
	))
	return hex.EncodeToString(sum[:])
}

func cleanupOpenAIAutoResetProtectedRecord(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	recordID int64,
) {
	t.Helper()
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
DELETE FROM openai_quota_auto_reset_protected_attempts
WHERE idempotency_record_id = $1`, recordID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE idempotency_records
SET scope = 'migration_243_test_cleanup:' || id::TEXT,
    status = 'failed_retryable',
    response_status = NULL,
    response_body = NULL,
    error_reason = 'MIGRATION_TEST_CLEANUP',
    locked_until = NULL,
    expires_at = '-infinity'::TIMESTAMPTZ,
    updated_at = NOW()
WHERE id = $1`, recordID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
		`DELETE FROM idempotency_records WHERE id = $1`, recordID))
}

func openAIAutoResetReconciliationAuditExtra(
	accountID int64,
	recordID int64,
	fingerprint string,
	resultCode string,
	windowsReset int,
	evidenceRef string,
	decisionOwner string,
) string {
	return fmt.Sprintf(
		`{"account_id":%d,"decision_owner":"%s","evidence_ref":"%s",`+
			`"idempotency_record_id":%d,"request_fingerprint":"%s",`+
			`"result_code":"%s","windows_reset":%d}`,
		accountID,
		decisionOwner,
		evidenceRef,
		recordID,
		fingerprint,
		resultCode,
		windowsReset,
	)
}

func removeOpenAIAutoResetWorkerSeed(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
		`DELETE FROM service_principals WHERE code = $1`, openAIAutoResetWorkerCode))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db,
		`DELETE FROM permissions WHERE code = $1`, openAIAutoResetPermissionCode))
}

func assertOpenAIAutoResetCollision(
	t *testing.T,
	ctx context.Context,
	db *sql.DB,
	principalID int64,
	wantName string,
	wantStatus string,
	wantRoleCount int,
	wantDirectCount int,
) {
	t.Helper()
	var name, status string
	var roleCount, directCount int
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT name, status,
       (SELECT COUNT(*) FROM service_principal_roles WHERE service_principal_id = principal.id),
       (SELECT COUNT(*) FROM service_principal_worker_permissions WHERE service_principal_id = principal.id)
FROM service_principals AS principal
WHERE id = $1`, principalID).Scan(&name, &status, &roleCount, &directCount))
	require.Equal(t, wantName, name)
	require.Equal(t, wantStatus, status)
	require.Equal(t, wantRoleCount, roleCount)
	require.Equal(t, wantDirectCount, directCount)
}

func assertOpenAIAutoResetReservedPermissionMissing(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	var count int
	require.NoError(t, db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM permissions WHERE code = $1`, openAIAutoResetPermissionCode,
	).Scan(&count))
	require.Zero(t, count)
}

func execOpenAIAutoResetStatement(ctx context.Context, db *sql.DB, statement string, args ...any) error {
	_, err := db.ExecContext(ctx, statement, args...)
	return err
}

func openAIAutoResetInt64(value int64) string {
	return strconv.FormatInt(value, 10)
}
