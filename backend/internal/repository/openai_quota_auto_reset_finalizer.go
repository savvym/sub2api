package repository

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const (
	openAIAutoResetIdempotencyScopePrefix = "openai_auto_reset_credit|service_principal:"
	openAIAutoResetAuditMethod            = "SYSTEM"
	openAIAutoResetMaxOutcomeCount        = int64(math.MaxInt32)
)

var openAIAutoResetAuditExtraKeys = map[string]struct{}{
	"account_id":      {},
	"trigger_window":  {},
	"threshold_5h":    {},
	"threshold_7d":    {},
	"utilization_5h":  {},
	"utilization_7d":  {},
	"available_count": {},
	"result_code":     {},
	"windows_reset":   {},
	"error_code":      {},
}

const openAIAutoResetFinalizationTableLockSQL = "LOCK TABLE idempotency_records IN ROW EXCLUSIVE MODE"

const openAIAutoResetFinalizationLockSQL = `
SELECT scope,
       request_fingerprint,
       status,
       response_status,
       response_body,
       error_reason,
       locked_until,
       expires_at = $2
FROM idempotency_records
WHERE id = $1
FOR UPDATE`

const openAIAutoResetAuditInsertSQL = `INSERT INTO audit_logs (` + auditLogInsertColumns + `)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
ON CONFLICT (action, request_id)
WHERE action = 'system.openai.reset_credit.auto' AND request_id <> ''
DO NOTHING`

const openAIAutoResetAuditVerifySQL = `
SELECT id,
       actor_user_id,
       actor_service_principal_id,
       actor_email,
       actor_role,
       auth_method,
       credential_masked,
       action,
       method,
       path,
       request_id,
       client_ip,
       user_agent,
       request_body,
       status_code,
       latency_ms,
       extra = $3::jsonb
FROM audit_logs
WHERE action = $1
  AND request_id = $2
FOR UPDATE`

const openAIAutoResetIdempotencySucceedSQL = `
UPDATE idempotency_records
SET status = $2,
    response_status = $3,
    response_body = $4,
    error_reason = NULL,
    locked_until = NULL,
    expires_at = $5,
    updated_at = NOW()
WHERE id = $1
  AND scope = $6
  AND request_fingerprint = $7
  AND status = $8`

type openAIQuotaAutoResetFinalizer struct {
	db *sql.DB
}

type preparedOpenAIAutoResetFinalization struct {
	recordID           int64
	scope              string
	fingerprint        string
	responseStatus     int
	responseBody       string
	expiresAt          time.Time
	servicePrincipalID int64
	audit              service.AuditLog
	auditExtraJSON     string
}

// NewOpenAIQuotaAutoResetFinalizer provides the dedicated atomic finalization
// port. It intentionally remains separate from the generic idempotency store.
func NewOpenAIQuotaAutoResetFinalizer(db *sql.DB) service.OpenAIQuotaAutoResetFinalizer {
	return &openAIQuotaAutoResetFinalizer{db: db}
}

func (r *openAIQuotaAutoResetFinalizer) FinalizeOpenAIQuotaAutoReset(
	ctx context.Context,
	input *service.OpenAIQuotaAutoResetFinalization,
) error {
	if ctx == nil {
		return openAIAutoResetFinalizationInvalid("context is nil")
	}
	if r == nil || r.db == nil {
		return openAIAutoResetFinalizationInvalid("repository is not configured")
	}
	prepared, err := prepareOpenAIAutoResetFinalization(input)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin OpenAI quota auto-reset finalization: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	// Establish the writer table lock before row locks so migration SRE locks
	// cannot form a cycle with the UPDATE's later implicit lock upgrade.
	if _, err := tx.ExecContext(ctx, openAIAutoResetFinalizationTableLockSQL); err != nil {
		return fmt.Errorf("lock OpenAI quota auto-reset idempotency table: %w", err)
	}

	var (
		storedScope       string
		storedFingerprint string
		storedStatus      string
		storedResponse    sql.NullString
		storedErrorReason sql.NullString
		storedLockedUntil sql.NullTime
		storedHTTPStatus  sql.NullInt64
		expiresAtMatches  bool
	)
	if err := tx.QueryRowContext(
		ctx,
		openAIAutoResetFinalizationLockSQL,
		prepared.recordID,
		prepared.expiresAt,
	).Scan(
		&storedScope,
		&storedFingerprint,
		&storedStatus,
		&storedHTTPStatus,
		&storedResponse,
		&storedErrorReason,
		&storedLockedUntil,
		&expiresAtMatches,
	); err != nil {
		if err == sql.ErrNoRows {
			return openAIAutoResetFinalizationConflict("idempotency record does not exist")
		}
		return fmt.Errorf("lock OpenAI quota auto-reset idempotency record: %w", err)
	}

	if storedScope != prepared.scope || storedFingerprint != prepared.fingerprint || !expiresAtMatches {
		return openAIAutoResetFinalizationConflict("idempotency record identity mismatch")
	}

	switch storedStatus {
	case service.IdempotencyStatusProcessing:
		if storedHTTPStatus.Valid || storedResponse.Valid || storedErrorReason.Valid {
			return openAIAutoResetFinalizationConflict("processing record contains terminal data")
		}
		if err := insertOpenAIAutoResetAudit(ctx, tx, prepared); err != nil {
			return err
		}
		if err := verifyOpenAIAutoResetAudit(ctx, tx, prepared); err != nil {
			return err
		}

		result, err := tx.ExecContext(
			ctx,
			openAIAutoResetIdempotencySucceedSQL,
			prepared.recordID,
			service.IdempotencyStatusSucceeded,
			prepared.responseStatus,
			prepared.responseBody,
			prepared.expiresAt,
			prepared.scope,
			prepared.fingerprint,
			service.IdempotencyStatusProcessing,
		)
		if err != nil {
			return fmt.Errorf("mark OpenAI quota auto-reset idempotency record succeeded: %w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("read OpenAI quota auto-reset finalization update count: %w", err)
		}
		if affected != 1 {
			return openAIAutoResetFinalizationConflict("processing transition did not affect exactly one record")
		}

	case service.IdempotencyStatusSucceeded:
		if !storedHTTPStatus.Valid || int(storedHTTPStatus.Int64) != prepared.responseStatus ||
			!storedResponse.Valid || storedResponse.String != prepared.responseBody ||
			storedErrorReason.Valid || storedLockedUntil.Valid {
			return openAIAutoResetFinalizationConflict("succeeded record terminal response mismatch")
		}
		// A succeeded row without its exact audit is not repaired silently. Only a
		// complete prior commit may acknowledge an ambiguous commit retry.
		if err := verifyOpenAIAutoResetAudit(ctx, tx, prepared); err != nil {
			return err
		}

	default:
		return openAIAutoResetFinalizationConflict("idempotency record is not processing or succeeded")
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit OpenAI quota auto-reset finalization: %w", err)
	}
	committed = true
	return nil
}

func prepareOpenAIAutoResetFinalization(
	input *service.OpenAIQuotaAutoResetFinalization,
) (*preparedOpenAIAutoResetFinalization, error) {
	if input == nil {
		return nil, openAIAutoResetFinalizationInvalid("input is nil")
	}
	if input.AccountID <= 0 || input.IdempotencyRecordID <= 0 {
		return nil, openAIAutoResetFinalizationInvalid("account and idempotency record IDs must be positive")
	}
	if input.ResponseStatus != http.StatusOK {
		return nil, openAIAutoResetFinalizationInvalid("terminal response status must be 200")
	}
	if input.ExpiresAt.IsZero() {
		return nil, openAIAutoResetFinalizationInvalid("idempotency expiry is required")
	}
	if !validSHA256Hex(input.RequestFingerprint) {
		return nil, openAIAutoResetFinalizationInvalid("request fingerprint must be lowercase SHA-256 hex")
	}
	response, err := service.ParseOpenAIAutoResetCanonicalResponse(input.ResponseBody)
	if err != nil {
		return nil, openAIAutoResetFinalizationInvalid("terminal response body violates the canonical schema")
	}

	audit := input.Audit
	if audit.ID != 0 || audit.ActorUserID != nil || audit.ActorServicePrincipalID == nil ||
		*audit.ActorServicePrincipalID <= 0 {
		return nil, openAIAutoResetFinalizationInvalid("audit must identify exactly one Service Principal")
	}
	servicePrincipalID := *audit.ActorServicePrincipalID
	expectedScope := openAIAutoResetIdempotencyScopePrefix + strconv.FormatInt(servicePrincipalID, 10)
	if input.ActorQualifiedScope != expectedScope {
		return nil, openAIAutoResetFinalizationInvalid("actor-qualified scope does not match the audit Service Principal")
	}
	if audit.ActorServicePrincipalCode != "" || audit.ActorServicePrincipalName != "" ||
		audit.ActorEmail != "" || audit.ActorRole != "" || audit.CredentialMasked != "" ||
		audit.ClientIP != "" || audit.UserAgent != "" || audit.RequestBody != "" || audit.LatencyMs != 0 {
		return nil, openAIAutoResetFinalizationInvalid("worker audit contains unsupported request or display attribution")
	}
	if audit.AuthMethod != service.AuditAuthMethodServicePrincipal ||
		audit.Action != service.AuditActionOpenAIQuotaAutoReset || audit.Method != openAIAutoResetAuditMethod {
		return nil, openAIAutoResetFinalizationInvalid("worker audit action or authentication identity is invalid")
	}
	if audit.StatusCode != http.StatusOK && audit.StatusCode != http.StatusConflict {
		return nil, openAIAutoResetFinalizationInvalid("worker audit status must be 200 or 409")
	}
	expectedPath := fmt.Sprintf("/system/openai/accounts/%d/auto-reset-credit", input.AccountID)
	if audit.Path != expectedPath || len(audit.Path) > 512 {
		return nil, openAIAutoResetFinalizationInvalid("worker audit path does not match the account")
	}
	if !validBoundedRequestID(audit.RequestID) {
		return nil, openAIAutoResetFinalizationInvalid("worker audit request ID is required and must fit audit storage")
	}
	if err := validateOpenAIAutoResetFinalAudit(input.AccountID, &audit, response); err != nil {
		return nil, err
	}
	extraJSON, err := json.Marshal(audit.Extra)
	if err != nil {
		return nil, openAIAutoResetFinalizationInvalid("worker audit extra is not JSON serializable")
	}
	if len(extraJSON) == 0 || string(extraJSON) == "null" {
		extraJSON = []byte("{}")
	}

	audit.CreatedAt = audit.CreatedAt.UTC()
	if audit.CreatedAt.IsZero() {
		audit.CreatedAt = time.Now().UTC()
	}

	return &preparedOpenAIAutoResetFinalization{
		recordID:           input.IdempotencyRecordID,
		scope:              input.ActorQualifiedScope,
		fingerprint:        input.RequestFingerprint,
		responseStatus:     input.ResponseStatus,
		responseBody:       response.Body,
		expiresAt:          input.ExpiresAt.UTC(),
		servicePrincipalID: servicePrincipalID,
		audit:              audit,
		auditExtraJSON:     string(extraJSON),
	}, nil
}

func insertOpenAIAutoResetAudit(
	ctx context.Context,
	tx *sql.Tx,
	input *preparedOpenAIAutoResetFinalization,
) error {
	values := []any{
		input.audit.CreatedAt,
		nil,
		input.servicePrincipalID,
		input.audit.ActorEmail,
		input.audit.ActorRole,
		input.audit.AuthMethod,
		input.audit.CredentialMasked,
		input.audit.Action,
		input.audit.Method,
		input.audit.Path,
		input.audit.RequestID,
		input.audit.ClientIP,
		input.audit.UserAgent,
		input.audit.RequestBody,
		input.audit.StatusCode,
		input.audit.LatencyMs,
		input.auditExtraJSON,
	}
	result, err := tx.ExecContext(ctx, openAIAutoResetAuditInsertSQL, values...)
	if err != nil {
		return fmt.Errorf("insert OpenAI quota auto-reset audit: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("read OpenAI quota auto-reset audit insert count: %w", err)
	}
	if affected != 0 && affected != 1 {
		return openAIAutoResetFinalizationConflict("audit insert affected an unexpected number of records")
	}
	return nil
}

func verifyOpenAIAutoResetAudit(
	ctx context.Context,
	tx *sql.Tx,
	input *preparedOpenAIAutoResetFinalization,
) error {
	rows, err := tx.QueryContext(
		ctx,
		openAIAutoResetAuditVerifySQL,
		service.AuditActionOpenAIQuotaAutoReset,
		input.audit.RequestID,
		input.auditExtraJSON,
	)
	if err != nil {
		return fmt.Errorf("lock OpenAI quota auto-reset audit: %w", err)
	}
	defer func() { _ = rows.Close() }()

	count := 0
	for rows.Next() {
		count++
		var (
			id               int64
			actorUserID      sql.NullInt64
			actorPrincipalID sql.NullInt64
			actorEmail       string
			actorRole        string
			authMethod       string
			credentialMasked string
			action           string
			method           string
			path             string
			requestID        string
			clientIP         string
			userAgent        string
			requestBody      string
			statusCode       int
			latencyMs        int64
			extraMatches     bool
		)
		if err := rows.Scan(
			&id,
			&actorUserID,
			&actorPrincipalID,
			&actorEmail,
			&actorRole,
			&authMethod,
			&credentialMasked,
			&action,
			&method,
			&path,
			&requestID,
			&clientIP,
			&userAgent,
			&requestBody,
			&statusCode,
			&latencyMs,
			&extraMatches,
		); err != nil {
			return fmt.Errorf("scan OpenAI quota auto-reset audit: %w", err)
		}
		if count > 1 || id <= 0 || actorUserID.Valid || !actorPrincipalID.Valid ||
			actorPrincipalID.Int64 != input.servicePrincipalID || actorEmail != input.audit.ActorEmail ||
			actorRole != input.audit.ActorRole || authMethod != input.audit.AuthMethod ||
			credentialMasked != input.audit.CredentialMasked || action != input.audit.Action ||
			method != input.audit.Method || path != input.audit.Path || requestID != input.audit.RequestID ||
			clientIP != input.audit.ClientIP || userAgent != input.audit.UserAgent ||
			requestBody != input.audit.RequestBody || statusCode != input.audit.StatusCode ||
			latencyMs != input.audit.LatencyMs || !extraMatches {
			return openAIAutoResetFinalizationConflict("existing audit does not match the requested outcome")
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate OpenAI quota auto-reset audit: %w", err)
	}
	if count != 1 {
		return openAIAutoResetFinalizationConflict("exactly one matching audit is required")
	}
	return nil
}

func validateOpenAIAutoResetFinalAudit(
	accountID int64,
	audit *service.AuditLog,
	response service.OpenAIAutoResetCanonicalResponse,
) error {
	if audit == nil || len(audit.Extra) != len(openAIAutoResetAuditExtraKeys) {
		return openAIAutoResetFinalizationInvalid("worker audit extra does not match the canonical schema")
	}
	for key := range audit.Extra {
		if _, ok := openAIAutoResetAuditExtraKeys[key]; !ok {
			return openAIAutoResetFinalizationInvalid("worker audit extra contains an unknown field")
		}
	}
	for key := range openAIAutoResetAuditExtraKeys {
		if _, ok := audit.Extra[key]; !ok {
			return openAIAutoResetFinalizationInvalid("worker audit extra is missing a required field")
		}
	}

	storedAccountID, ok := exactPositiveInt64(audit.Extra["account_id"])
	if !ok || storedAccountID != accountID {
		return openAIAutoResetFinalizationInvalid("worker audit account identity mismatch")
	}
	triggerWindow, ok := audit.Extra["trigger_window"].(string)
	if !ok {
		return openAIAutoResetFinalizationInvalid("worker audit trigger window must be a string")
	}
	threshold5h, ok := exactFiniteNumber(audit.Extra["threshold_5h"])
	if !ok || threshold5h < 0.001 || threshold5h > 1 {
		return openAIAutoResetFinalizationInvalid("worker audit 5h threshold is invalid")
	}
	threshold7d, ok := exactFiniteNumber(audit.Extra["threshold_7d"])
	if !ok || threshold7d < 0.001 || threshold7d > 1 {
		return openAIAutoResetFinalizationInvalid("worker audit 7d threshold is invalid")
	}
	utilization5h, ok := exactFiniteNumber(audit.Extra["utilization_5h"])
	if !ok || utilization5h < 0 {
		return openAIAutoResetFinalizationInvalid("worker audit 5h utilization is invalid")
	}
	utilization7d, ok := exactFiniteNumber(audit.Extra["utilization_7d"])
	if !ok || utilization7d < 0 {
		return openAIAutoResetFinalizationInvalid("worker audit 7d utilization is invalid")
	}
	if triggerWindow != expectedOpenAIAutoResetTriggerWindow(
		utilization5h >= threshold5h,
		utilization7d >= threshold7d,
	) || triggerWindow == "" {
		return openAIAutoResetFinalizationInvalid("worker audit trigger window is inconsistent with utilization")
	}
	availableCount, ok := exactNonNegativeInt64(audit.Extra["available_count"])
	if !ok || availableCount > openAIAutoResetMaxOutcomeCount {
		return openAIAutoResetFinalizationInvalid("worker audit available count is invalid")
	}
	resultCode, ok := audit.Extra["result_code"].(string)
	if !ok {
		return openAIAutoResetFinalizationInvalid("worker audit result code must be a string")
	}
	windowsReset, ok := exactNonNegativeInt64(audit.Extra["windows_reset"])
	if !ok || windowsReset > openAIAutoResetMaxOutcomeCount || windowsReset != int64(response.WindowsReset) {
		return openAIAutoResetFinalizationInvalid("worker audit windows reset does not match the terminal response")
	}
	errorCode, ok := audit.Extra["error_code"].(string)
	if !ok || !validOpenAIAutoResetAuditCode(errorCode) {
		return openAIAutoResetFinalizationInvalid("worker audit error code is invalid")
	}

	switch resultCode {
	case "success":
		if audit.StatusCode != http.StatusOK || errorCode != "" ||
			response.ResultCode != "success" || !response.PostProcessRecorded ||
			!response.AccountStateRecovered || response.RecoveryPending ||
			response.RecoveryDeferred || response.WarningCode != "" {
			return openAIAutoResetFinalizationInvalid("worker audit success outcome does not match the terminal response")
		}
	case "no_credit":
		if audit.StatusCode != http.StatusConflict || errorCode != "NO_RESET_CREDIT" ||
			response.ResultCode != "no_credit" || response.WindowsReset != 0 {
			return openAIAutoResetFinalizationInvalid("worker audit no-credit outcome does not match the terminal response")
		}
	case "recovery_deferred":
		if audit.StatusCode != http.StatusConflict || errorCode == "" ||
			response.ResultCode != "success" || !response.RecoveryPending ||
			!response.RecoveryDeferred || response.PostProcessRecorded ||
			response.AccountStateRecovered || response.WarningCode != "" {
			return openAIAutoResetFinalizationInvalid("worker audit deferred recovery does not match the terminal response")
		}
	case "recovery_failed":
		if audit.StatusCode != http.StatusConflict || response.ResultCode != "success" ||
			!response.PostProcessRecorded || !response.RecoveryPending ||
			response.RecoveryDeferred || response.WarningCode == "" || errorCode != response.WarningCode {
			return openAIAutoResetFinalizationInvalid("worker audit failed recovery does not match the terminal response")
		}
	default:
		return openAIAutoResetFinalizationInvalid("worker audit result code is not recognized")
	}
	return nil
}

func expectedOpenAIAutoResetTriggerWindow(fiveHour, sevenDay bool) string {
	switch {
	case fiveHour && sevenDay:
		return "5h+7d"
	case fiveHour:
		return "5h"
	case sevenDay:
		return "7d"
	default:
		return ""
	}
}

func validOpenAIAutoResetAuditCode(value string) bool {
	if len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'z' ||
			char >= 'A' && char <= 'Z' ||
			char >= '0' && char <= '9' ||
			char == '_' || char == '-' || char == '.' || char == ':') {
			return false
		}
	}
	return true
}

func exactFiniteNumber(value any) (float64, bool) {
	var number float64
	switch typed := value.(type) {
	case int:
		number = float64(typed)
	case int8:
		number = float64(typed)
	case int16:
		number = float64(typed)
	case int32:
		number = float64(typed)
	case int64:
		number = float64(typed)
	case uint:
		number = float64(typed)
	case uint8:
		number = float64(typed)
	case uint16:
		number = float64(typed)
	case uint32:
		number = float64(typed)
	case uint64:
		number = float64(typed)
	case float32:
		number = float64(typed)
	case float64:
		number = typed
	case json.Number:
		parsed, err := typed.Float64()
		if err != nil {
			return 0, false
		}
		number = parsed
	default:
		return 0, false
	}
	return number, !math.IsNaN(number) && !math.IsInf(number, 0)
}

func exactNonNegativeInt64(value any) (int64, bool) {
	switch number := value.(type) {
	case int:
		if number >= 0 {
			return int64(number), true
		}
	case int8:
		if number >= 0 {
			return int64(number), true
		}
	case int16:
		if number >= 0 {
			return int64(number), true
		}
	case int32:
		if number >= 0 {
			return int64(number), true
		}
	case int64:
		if number >= 0 {
			return number, true
		}
	case uint:
		if uint64(number) <= math.MaxInt64 {
			return int64(number), true
		}
	case uint8:
		return int64(number), true
	case uint16:
		return int64(number), true
	case uint32:
		return int64(number), true
	case uint64:
		if number <= math.MaxInt64 {
			return int64(number), true
		}
	case float64:
		if number >= 0 && number <= math.MaxInt64 && number == math.Trunc(number) {
			return int64(number), true
		}
	case json.Number:
		parsed, err := number.Int64()
		if err == nil && parsed >= 0 {
			return parsed, true
		}
	}
	return 0, false
}

func openAIAutoResetFinalizationInvalid(reason string) error {
	return fmt.Errorf("%w: %s", service.ErrOpenAIQuotaAutoResetFinalizationInvalid, reason)
}

func openAIAutoResetFinalizationConflict(reason string) error {
	return fmt.Errorf("%w: %s", service.ErrOpenAIQuotaAutoResetFinalizationConflict, reason)
}

func validSHA256Hex(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validBoundedRequestID(value string) bool {
	if value == "" || len(value) > 64 || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if char < 33 || char > 126 {
			return false
		}
	}
	return true
}

func exactPositiveInt64(value any) (int64, bool) {
	switch number := value.(type) {
	case int:
		if number > 0 {
			return int64(number), true
		}
	case int32:
		if number > 0 {
			return int64(number), true
		}
	case int64:
		if number > 0 {
			return number, true
		}
	case uint:
		if uint64(number) <= math.MaxInt64 && number > 0 {
			return int64(number), true
		}
	case uint32:
		if number > 0 {
			return int64(number), true
		}
	case uint64:
		if number <= math.MaxInt64 && number > 0 {
			return int64(number), true
		}
	case float64:
		if number > 0 && number <= math.MaxInt64 && number == math.Trunc(number) {
			return int64(number), true
		}
	case json.Number:
		parsed, err := number.Int64()
		if err == nil && parsed > 0 {
			return parsed, true
		}
	}
	return 0, false
}
