//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestOpenAIQuotaAutoResetFinalizerIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	principalID := openAIAutoResetFinalizerIntegrationPrincipalID(t, ctx)
	finalizer := NewOpenAIQuotaAutoResetFinalizer(integrationDB)

	t.Run("atomic commit and exact retry", func(t *testing.T) {
		fixture := newOpenAIAutoResetFinalizerIntegrationFixture(t, ctx, principalID, "commit")

		require.NoError(t, finalizer.FinalizeOpenAIQuotaAutoReset(ctx, fixture.input))
		assertOpenAIAutoResetFinalizerIntegrationSucceeded(t, ctx, fixture)
		assertOpenAIAutoResetFinalizerIntegrationAudit(t, ctx, fixture, 1)

		require.NoError(t, finalizer.FinalizeOpenAIQuotaAutoReset(ctx, fixture.input))
		assertOpenAIAutoResetFinalizerIntegrationSucceeded(t, ctx, fixture)
		assertOpenAIAutoResetFinalizerIntegrationAudit(t, ctx, fixture, 1)
	})

	t.Run("audit insert failure rolls back", func(t *testing.T) {
		fixture := newOpenAIAutoResetFinalizerIntegrationFixture(t, ctx, principalID, "audit-failure")
		installOpenAIAutoResetFinalizerAuditFailureTrigger(t, ctx, fixture.input.Audit.RequestID)

		err := finalizer.FinalizeOpenAIQuotaAutoReset(ctx, fixture.input)
		require.ErrorContains(t, err, "forced OpenAI auto-reset audit insert failure")
		assertOpenAIAutoResetFinalizerIntegrationProcessing(t, ctx, fixture)
		assertOpenAIAutoResetFinalizerIntegrationAudit(t, ctx, fixture, 0)
	})

	t.Run("idempotency update failure rolls back inserted audit", func(t *testing.T) {
		fixture := newOpenAIAutoResetFinalizerIntegrationFixture(t, ctx, principalID, "update-failure")
		installOpenAIAutoResetFinalizerUpdateFailureTrigger(t, ctx, fixture.recordID)

		err := finalizer.FinalizeOpenAIQuotaAutoReset(ctx, fixture.input)
		require.ErrorContains(t, err, "forced OpenAI auto-reset idempotency update failure")
		assertOpenAIAutoResetFinalizerIntegrationProcessing(t, ctx, fixture)
		assertOpenAIAutoResetFinalizerIntegrationAudit(t, ctx, fixture, 0)
	})

	t.Run("mismatched terminal outcome conflicts", func(t *testing.T) {
		fixture := newOpenAIAutoResetFinalizerIntegrationFixture(t, ctx, principalID, "mismatch")
		require.NoError(t, finalizer.FinalizeOpenAIQuotaAutoReset(ctx, fixture.input))

		responseMismatch := *fixture.input
		responseMismatch.Audit = fixture.input.Audit
		applyOpenAIAutoResetFinalizationTestOutcome(&responseMismatch, "no_credit")
		err := finalizer.FinalizeOpenAIQuotaAutoReset(ctx, &responseMismatch)
		require.ErrorIs(t, err, service.ErrOpenAIQuotaAutoResetFinalizationConflict)

		auditMismatch := *fixture.input
		auditMismatch.Audit = fixture.input.Audit
		auditMismatch.Audit.Extra = openAIAutoResetFinalizationTestAuditExtra(
			fixture.input.AccountID,
			"success",
			2,
			"",
		)
		auditMismatch.Audit.Extra["available_count"] = 5
		err = finalizer.FinalizeOpenAIQuotaAutoReset(ctx, &auditMismatch)
		require.ErrorIs(t, err, service.ErrOpenAIQuotaAutoResetFinalizationConflict)

		assertOpenAIAutoResetFinalizerIntegrationSucceeded(t, ctx, fixture)
		assertOpenAIAutoResetFinalizerIntegrationAudit(t, ctx, fixture, 1)
	})
}

type openAIAutoResetFinalizerIntegrationFixture struct {
	recordID int64
	keyHash  string
	input    *service.OpenAIQuotaAutoResetFinalization
}

func newOpenAIAutoResetFinalizerIntegrationFixture(
	t *testing.T,
	ctx context.Context,
	principalID int64,
	label string,
) *openAIAutoResetFinalizerIntegrationFixture {
	t.Helper()

	uniqueID := strings.ReplaceAll(uuid.NewString(), "-", "")
	scope := openAIAutoResetIdempotencyScopePrefix + strconv.FormatInt(principalID, 10)
	keyHash := openAIAutoResetFinalizerIntegrationHash(label + ":key:" + uniqueID)
	fingerprint := openAIAutoResetFinalizerIntegrationHash(label + ":fingerprint:" + uniqueID)
	expiresAt := time.Now().UTC().Add(8 * 24 * time.Hour).Truncate(time.Microsecond)

	var recordID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope,
    idempotency_key_hash,
    request_fingerprint,
    status,
    locked_until,
    expires_at
)
VALUES ($1, $2, $3, $4, statement_timestamp() + INTERVAL '1 minute', $5)
RETURNING id`,
		scope,
		keyHash,
		fingerprint,
		service.IdempotencyStatusProcessing,
		expiresAt,
	).Scan(&recordID))

	requestID := "oarc-it-" + uniqueID
	input := &service.OpenAIQuotaAutoResetFinalization{
		AccountID:           recordID,
		IdempotencyRecordID: recordID,
		ActorQualifiedScope: scope,
		RequestFingerprint:  fingerprint,
		ResponseStatus:      http.StatusOK,
		ExpiresAt:           expiresAt,
		Audit: service.AuditLog{
			CreatedAt:               time.Now().UTC().Truncate(time.Microsecond),
			ActorServicePrincipalID: &principalID,
			AuthMethod:              service.AuditAuthMethodServicePrincipal,
			Action:                  service.AuditActionOpenAIQuotaAutoReset,
			Method:                  openAIAutoResetAuditMethod,
			Path:                    fmt.Sprintf("/system/openai/accounts/%d/auto-reset-credit", recordID),
			RequestID:               requestID,
		},
	}
	applyOpenAIAutoResetFinalizationTestOutcome(input, "success")
	fixture := &openAIAutoResetFinalizerIntegrationFixture{
		recordID: recordID,
		keyHash:  keyHash,
		input:    input,
	}

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()

		if _, err := integrationDB.ExecContext(cleanupCtx, `
DELETE FROM audit_logs
WHERE action = $1 AND request_id = $2`,
			service.AuditActionOpenAIQuotaAutoReset,
			requestID,
		); err != nil {
			t.Errorf("clean up OpenAI auto-reset audit fixture: %v", err)
		}
		if _, err := integrationDB.ExecContext(cleanupCtx, `
UPDATE idempotency_records
SET status = $2,
    expires_at = statement_timestamp() - INTERVAL '1 second'
WHERE id = $1`, recordID, service.IdempotencyStatusFailedRetryable); err != nil {
			t.Errorf("expire OpenAI auto-reset idempotency fixture: %v", err)
		}
		if _, err := integrationDB.ExecContext(cleanupCtx, `
DELETE FROM idempotency_records
WHERE id = $1`, recordID); err != nil {
			t.Errorf("clean up OpenAI auto-reset idempotency fixture: %v", err)
		}
	})

	return fixture
}

func openAIAutoResetFinalizerIntegrationPrincipalID(t *testing.T, ctx context.Context) int64 {
	t.Helper()
	var principalID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT id
FROM service_principals
WHERE code = $1`, "openai_quota_auto_reset_worker").Scan(&principalID))
	require.Positive(t, principalID)
	return principalID
}

func openAIAutoResetFinalizerIntegrationHash(value string) string {
	return service.HashIdempotencyKey(value)
}

func assertOpenAIAutoResetFinalizerIntegrationSucceeded(
	t *testing.T,
	ctx context.Context,
	fixture *openAIAutoResetFinalizerIntegrationFixture,
) {
	t.Helper()
	var (
		status         string
		responseStatus sql.NullInt64
		responseBody   sql.NullString
		errorReason    sql.NullString
		lockedUntil    sql.NullTime
		expiresAt      time.Time
	)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT status, response_status, response_body, error_reason, locked_until, expires_at
FROM idempotency_records
WHERE id = $1 AND idempotency_key_hash = $2`, fixture.recordID, fixture.keyHash).Scan(
		&status,
		&responseStatus,
		&responseBody,
		&errorReason,
		&lockedUntil,
		&expiresAt,
	))
	require.Equal(t, service.IdempotencyStatusSucceeded, status)
	require.True(t, responseStatus.Valid)
	require.EqualValues(t, http.StatusOK, responseStatus.Int64)
	require.True(t, responseBody.Valid)
	require.Equal(t, canonicalOpenAIAutoResetSuccessResponseBody, responseBody.String)
	require.False(t, errorReason.Valid)
	require.False(t, lockedUntil.Valid)
	require.True(t, expiresAt.Equal(fixture.input.ExpiresAt))
}

func assertOpenAIAutoResetFinalizerIntegrationProcessing(
	t *testing.T,
	ctx context.Context,
	fixture *openAIAutoResetFinalizerIntegrationFixture,
) {
	t.Helper()
	var (
		status         string
		responseStatus sql.NullInt64
		responseBody   sql.NullString
		errorReason    sql.NullString
		lockedUntil    sql.NullTime
	)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT status, response_status, response_body, error_reason, locked_until
FROM idempotency_records
WHERE id = $1 AND idempotency_key_hash = $2`, fixture.recordID, fixture.keyHash).Scan(
		&status,
		&responseStatus,
		&responseBody,
		&errorReason,
		&lockedUntil,
	))
	require.Equal(t, service.IdempotencyStatusProcessing, status)
	require.False(t, responseStatus.Valid)
	require.False(t, responseBody.Valid)
	require.False(t, errorReason.Valid)
	require.True(t, lockedUntil.Valid)
}

func assertOpenAIAutoResetFinalizerIntegrationAudit(
	t *testing.T,
	ctx context.Context,
	fixture *openAIAutoResetFinalizerIntegrationFixture,
	wantCount int,
) {
	t.Helper()
	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT COUNT(*)
FROM audit_logs
WHERE action = $1 AND request_id = $2`,
		service.AuditActionOpenAIQuotaAutoReset,
		fixture.input.Audit.RequestID,
	).Scan(&count))
	require.Equal(t, wantCount, count)
	if wantCount == 0 {
		return
	}

	var (
		actorUserID      sql.NullInt64
		actorPrincipalID sql.NullInt64
		authMethod       string
		method           string
		path             string
		statusCode       int
		accountID        int64
		resultCode       string
		extraJSON        string
	)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
SELECT actor_user_id,
       actor_service_principal_id,
       auth_method,
       method,
       path,
	       status_code,
	       (extra->>'account_id')::BIGINT,
	       extra->>'result_code',
	       extra::text
FROM audit_logs
WHERE action = $1 AND request_id = $2`,
		service.AuditActionOpenAIQuotaAutoReset,
		fixture.input.Audit.RequestID,
	).Scan(
		&actorUserID,
		&actorPrincipalID,
		&authMethod,
		&method,
		&path,
		&statusCode,
		&accountID,
		&resultCode,
		&extraJSON,
	))
	require.False(t, actorUserID.Valid)
	require.True(t, actorPrincipalID.Valid)
	require.Equal(t, *fixture.input.Audit.ActorServicePrincipalID, actorPrincipalID.Int64)
	require.Equal(t, service.AuditAuthMethodServicePrincipal, authMethod)
	require.Equal(t, openAIAutoResetAuditMethod, method)
	require.Equal(t, fixture.input.Audit.Path, path)
	require.Equal(t, fixture.input.Audit.StatusCode, statusCode)
	require.Equal(t, fixture.input.AccountID, accountID)
	require.Equal(t, "success", resultCode)
	require.JSONEq(t, openAIAutoResetFinalizationTestExtraJSON(t, fixture.input.Audit.Extra), extraJSON)
}

func installOpenAIAutoResetFinalizerAuditFailureTrigger(
	t *testing.T,
	ctx context.Context,
	requestID string,
) {
	t.Helper()
	installOpenAIAutoResetFinalizerFailureTrigger(
		t,
		ctx,
		"audit_logs",
		"INSERT",
		fmt.Sprintf(
			"NEW.action = %s AND NEW.request_id = %s",
			pq.QuoteLiteral(service.AuditActionOpenAIQuotaAutoReset),
			pq.QuoteLiteral(requestID),
		),
		"forced OpenAI auto-reset audit insert failure",
	)
}

func installOpenAIAutoResetFinalizerUpdateFailureTrigger(
	t *testing.T,
	ctx context.Context,
	recordID int64,
) {
	t.Helper()
	installOpenAIAutoResetFinalizerFailureTrigger(
		t,
		ctx,
		"idempotency_records",
		"UPDATE",
		fmt.Sprintf(
			"OLD.id = %d AND NEW.status = %s",
			recordID,
			pq.QuoteLiteral(service.IdempotencyStatusSucceeded),
		),
		"forced OpenAI auto-reset idempotency update failure",
	)
}

func installOpenAIAutoResetFinalizerFailureTrigger(
	t *testing.T,
	ctx context.Context,
	table string,
	operation string,
	when string,
	message string,
) {
	t.Helper()
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	functionName := "oarc_finalizer_it_fn_" + suffix
	triggerName := "oarc_finalizer_it_trigger_" + suffix
	quotedFunction := pq.QuoteIdentifier(functionName)
	quotedTrigger := pq.QuoteIdentifier(triggerName)
	quotedTable := pq.QuoteIdentifier(table)

	_, err := integrationDB.ExecContext(ctx, fmt.Sprintf(`
CREATE FUNCTION %s()
RETURNS trigger
LANGUAGE plpgsql
AS $trigger$
BEGIN
    RAISE EXCEPTION %s;
END;
$trigger$`, quotedFunction, pq.QuoteLiteral(message)))
	require.NoError(t, err)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, err := integrationDB.ExecContext(cleanupCtx, fmt.Sprintf(
			"DROP TRIGGER IF EXISTS %s ON %s",
			quotedTrigger,
			quotedTable,
		)); err != nil {
			t.Errorf("drop OpenAI auto-reset failure trigger: %v", err)
		}
		if _, err := integrationDB.ExecContext(cleanupCtx, fmt.Sprintf(
			"DROP FUNCTION IF EXISTS %s()",
			quotedFunction,
		)); err != nil {
			t.Errorf("drop OpenAI auto-reset failure function: %v", err)
		}
	})

	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`
CREATE TRIGGER %s
BEFORE %s ON %s
FOR EACH ROW
WHEN (%s)
EXECUTE FUNCTION %s()`,
		quotedTrigger,
		operation,
		quotedTable,
		when,
		quotedFunction,
	))
	require.NoError(t, err)
}
