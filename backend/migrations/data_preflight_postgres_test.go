//go:build integration

package migrations_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestGroupNameDataPreflightUsesOwnerScopePostgres(t *testing.T) {
	db := newRoleCachePostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, repository.ApplyMigrations(ctx, db))

	preflight, err := os.ReadFile(filepath.Join(
		"..", "..", "openspec", "changes",
		"redesign-resource-access-control", "data-preflight.sql",
	))
	require.NoError(t, err)
	groupInventory := extractGroupNameInventoryQuery(t, string(preflight))
	defaultInventory := extractDefaultGroupNameInventoryQuery(t, string(preflight))

	_, err = db.ExecContext(ctx, `
DROP INDEX idx_groups_platform_name_unique_active;
DROP INDEX idx_groups_owner_name_unique_active`)
	require.NoError(t, err)

	base := "group-name-preflight-" + time.Now().Format("20060102150405.000000000")
	ownerOne := insertGroupNameOwnerTestUser(t, ctx, db, base+"-owner-one@example.com")
	ownerTwo := insertGroupNameOwnerTestUser(t, ctx, db, base+"-owner-two@example.com")
	insertGroup := func(name string, ownerID *int64) int64 {
		t.Helper()
		var groupID int64
		if ownerID == nil {
			require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO groups (name, platform, status)
VALUES ($1, 'openai', 'active')
RETURNING id`, name).Scan(&groupID))
			return groupID
		}
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO groups (
    name, platform, status, owner_user_id, created_by_user_id
)
VALUES ($1, 'openai', 'active', $2, $2)
RETURNING id`, name, *ownerID).Scan(&groupID))
		return groupID
	}

	platformName := base + "-platform-default"
	platformIDs := []int64{
		insertGroup(platformName, nil),
		insertGroup(strings.ToUpper(platformName), nil),
	}
	tenantName := base + "-tenant-default"
	tenantIDs := []int64{
		insertGroup(tenantName, &ownerOne),
		insertGroup(strings.ToUpper(tenantName), &ownerOne),
	}
	insertGroup(tenantName, &ownerTwo)

	crossScopeName := base + "-cross-default"
	insertGroup(crossScopeName, nil)
	insertGroup(crossScopeName, &ownerOne)
	insertGroup(crossScopeName, &ownerTwo)

	type conflictKey struct {
		ownerID int64
		owned   bool
		name    string
	}
	type conflictValue struct {
		count int
		ids   []int64
	}
	expected := map[conflictKey]conflictValue{
		{name: strings.ToLower(platformName)}: {
			count: 2,
			ids:   platformIDs,
		},
		{ownerID: ownerOne, owned: true, name: strings.ToLower(tenantName)}: {
			count: 2,
			ids:   tenantIDs,
		},
	}
	collect := func(query string) map[conflictKey]conflictValue {
		t.Helper()
		rows, queryErr := db.QueryContext(ctx, query)
		require.NoError(t, queryErr)
		defer rows.Close()
		actual := make(map[conflictKey]conflictValue)
		for rows.Next() {
			var (
				owner sql.NullInt64
				name  string
				count int
				ids   pq.Int64Array
			)
			require.NoError(t, rows.Scan(&owner, &name, &count, &ids))
			actual[conflictKey{
				ownerID: owner.Int64,
				owned:   owner.Valid,
				name:    name,
			}] = conflictValue{count: count, ids: append([]int64{}, ids...)}
		}
		require.NoError(t, rows.Err())
		return actual
	}

	require.Equal(t, expected, collect(groupInventory))
	require.Equal(t, expected, collect(defaultInventory))
}

func TestOpenAIQuotaAutoResetDataPreflightPostgres(t *testing.T) {
	db := newRoleCachePostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, repository.ApplyMigrations(ctx, db))

	preflight, err := os.ReadFile(filepath.Join(
		"..", "..", "openspec", "changes",
		"redesign-resource-access-control", "data-preflight.sql",
	))
	require.NoError(t, err)
	inventoryQuery := extractOpenAIAutoResetInventoryQuery(t, string(preflight))

	var workerPrincipalID int64
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT id
FROM service_principals
WHERE code = 'openai_quota_auto_reset_worker'`).Scan(&workerPrincipalID))

	insertRecord := func(scope, keyHash, fingerprint, status string) int64 {
		t.Helper()
		var recordID int64
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status, expires_at
)
VALUES ($1, $2, $3, $4, statement_timestamp() + INTERVAL '1 day')
RETURNING id`, scope, keyHash, fingerprint, status).Scan(&recordID))
		return recordID
	}

	const (
		accountCollisionKey = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		uniqueAccountKey    = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
		omittedAccountKey   = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	)
	restoreFence := disableOpenAIAutoResetLegacyScopeFence(t, ctx, db)
	accountCollisionA := insertRecord(
		"openai_auto_reset_credit|account:7001", accountCollisionKey,
		strings.Repeat("1", 64), "succeeded",
	)
	accountCollisionB := insertRecord(
		"openai_auto_reset_credit|account:7002", accountCollisionKey,
		strings.Repeat("2", 64), "succeeded",
	)
	malformedCollision := insertRecord(
		"openai_auto_reset_credit|account:7005", accountCollisionKey,
		"malformed-fingerprint", "processing",
	)
	uniqueAccount := insertRecord(
		"openai_auto_reset_credit|account:7003", uniqueAccountKey,
		strings.Repeat("3", 64), "processing",
	)
	omittedTerminalAccount := insertRecord(
		"openai_auto_reset_credit|account:7004", omittedAccountKey,
		strings.Repeat("4", 64), "succeeded",
	)
	restoreFence()

	const (
		creditHash = "0123456789abcdef01234567"
		cycleHash  = "89abcdef0123456789abcdef"
	)
	rawAccountID := insertOpenAIAutoResetAttemptAccount(
		t, ctx, db, "data-preflight-raw-collision", creditHash, cycleHash,
	)
	rawKey := openAIAutoResetStableKeyHash(rawAccountID, creditHash, cycleHash)
	rawFingerprint := openAIAutoResetFingerprint(
		"account:"+openAIAutoResetInt64(rawAccountID),
		rawAccountID,
		creditHash,
		cycleHash,
	)
	rawCollision := insertRecord(
		"openai_auto_reset_credit", rawKey, rawFingerprint, "processing",
	)
	targetCollision := insertRecord(
		"openai_auto_reset_credit|service_principal:"+
			openAIAutoResetInt64(workerPrincipalID),
		rawKey,
		strings.Repeat("5", 64),
		"succeeded",
	)

	type inventoryRow struct {
		status          string
		scopeKind       string
		accountIDs      []int64
		provenanceState string
	}
	rows, err := db.QueryContext(ctx, inventoryQuery)
	require.NoError(t, err)
	defer rows.Close()
	require.Equal(t, []string{
		"idempotency_record_id",
		"status",
		"scope_kind",
		"account_ids",
		"provenance_state",
	}, mustColumnNames(t, rows))

	actual := make(map[int64]inventoryRow)
	for rows.Next() {
		var recordID int64
		var row inventoryRow
		var accountIDs pq.Int64Array
		require.NoError(t, rows.Scan(
			&recordID,
			&row.status,
			&row.scopeKind,
			&accountIDs,
			&row.provenanceState,
		))
		row.accountIDs = append([]int64{}, accountIDs...)
		actual[recordID] = row
	}
	require.NoError(t, rows.Err())

	require.Equal(t, map[int64]inventoryRow{
		accountCollisionA: {
			status: "succeeded", scopeKind: "account",
			accountIDs: []int64{7001}, provenanceState: "target_scope_collision",
		},
		accountCollisionB: {
			status: "succeeded", scopeKind: "account",
			accountIDs: []int64{7002}, provenanceState: "target_scope_collision",
		},
		malformedCollision: {
			status: "processing", scopeKind: "account",
			accountIDs: []int64{7005}, provenanceState: "malformed_identity",
		},
		uniqueAccount: {
			status: "processing", scopeKind: "account",
			accountIDs: []int64{7003}, provenanceState: "resolved",
		},
		rawCollision: {
			status: "processing", scopeKind: "raw",
			accountIDs: []int64{rawAccountID}, provenanceState: "target_scope_collision",
		},
		targetCollision: {
			status: "succeeded", scopeKind: "service_principal",
			accountIDs: []int64{}, provenanceState: "target_scope_collision",
		},
	}, actual)
	require.NotContains(t, actual, omittedTerminalAccount,
		"a terminal account row without a target collision is not an in-flight candidate")
}

func TestOpenAIQuotaAutoResetTerminalRecoveryDataPreflightPostgres(t *testing.T) {
	db := newRoleCachePostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, repository.ApplyMigrations(ctx, db))

	preflight, err := os.ReadFile(filepath.Join(
		"..", "..", "openspec", "changes",
		"redesign-resource-access-control", "data-preflight.sql",
	))
	require.NoError(t, err)
	inventoryQuery := extractOpenAIAutoResetTerminalRecoveryInventoryQuery(t, string(preflight))

	var workerPrincipalID int64
	require.NoError(t, db.QueryRowContext(ctx, `
SELECT id
FROM service_principals
WHERE code = 'openai_quota_auto_reset_worker'`).Scan(&workerPrincipalID))

	type pendingAttempt struct {
		accountID    int64
		creditHash   string
		cycleHash    string
		stableKey    string
		legacy       string
		current      string
		accountScope string
		workerScope  string
	}
	newAttempt := func(name, creditDigit, cycleDigit string) pendingAttempt {
		t.Helper()
		creditHash := strings.Repeat(creditDigit, 24)
		cycleHash := strings.Repeat(cycleDigit, 24)
		accountID := insertOpenAIAutoResetAttemptAccount(
			t, ctx, db, name, creditHash, cycleHash,
		)
		return pendingAttempt{
			accountID:  accountID,
			creditHash: creditHash,
			cycleHash:  cycleHash,
			stableKey:  openAIAutoResetStableKeyHash(accountID, creditHash, cycleHash),
			legacy: openAIAutoResetFingerprint(
				"account:"+openAIAutoResetInt64(accountID),
				accountID,
				creditHash,
				cycleHash,
			),
			current: openAIAutoResetFingerprint(
				"service_principal:"+openAIAutoResetInt64(workerPrincipalID),
				accountID,
				creditHash,
				cycleHash,
			),
			accountScope: "openai_auto_reset_credit|account:" +
				openAIAutoResetInt64(accountID),
			workerScope: "openai_auto_reset_credit|service_principal:" +
				openAIAutoResetInt64(workerPrincipalID),
		}
	}
	insertTerminal := func(attempt pendingAttempt, scope, keyHash, fingerprint string, responseStatus any, body any) int64 {
		t.Helper()
		var recordID int64
		require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO idempotency_records (
    scope, idempotency_key_hash, request_fingerprint, status,
    response_status, response_body, expires_at
)
VALUES (
    $1, $2, $3, 'succeeded', $4, $5,
    statement_timestamp() + INTERVAL '1 day'
)
RETURNING id`, scope, keyHash, fingerprint, responseStatus, body).Scan(&recordID))
		return recordID
	}

	const (
		canonicalSuccess  = `{"result_code":"success","windows_reset":1,"post_process_recorded":true,"account_state_recovered":true}`
		canonicalNoCredit = `{"result_code":"no_credit","windows_reset":0}`
		redactedLegacy    = `{"code":"***","windows_reset":1}`
	)

	redactedAttempt := newAttempt("terminal-preflight-redacted", "1", "a")
	invalidAttempt := newAttempt("terminal-preflight-invalid", "2", "b")
	canonicalAccountAttempt := newAttempt("terminal-preflight-canonical-account", "3", "c")
	canonicalWorkerAttempt := newAttempt("terminal-preflight-canonical-worker", "4", "d")
	completedAttempt := newAttempt("terminal-preflight-completed", "5", "e")
	rawAttempt := newAttempt("terminal-preflight-raw", "6", "f")
	oldPrincipalAttempt := newAttempt("terminal-preflight-old-principal", "7", "8")
	fingerprintAttempt := newAttempt("terminal-preflight-fingerprint", "8", "9")
	wrongKeyAttempt := newAttempt("terminal-preflight-wrong-key", "9", "0")
	missingAttempt := newAttempt("terminal-preflight-missing-attempt", "a", "1")
	longNumericAttempt := newAttempt("terminal-preflight-long-numeric", "b", "2")
	deletedAttempt := newAttempt("terminal-preflight-deleted", "c", "3")
	shadowAttempt := newAttempt("terminal-preflight-shadow", "d", "4")
	nonOAuthAttempt := newAttempt("terminal-preflight-non-oauth", "e", "5")
	nonOpenAIAttempt := newAttempt("terminal-preflight-non-openai", "f", "6")
	malformedResettingAttempt := newAttempt(
		"terminal-preflight-malformed-resetting", "0", "7",
	)
	malformedFailedAttempt := newAttempt(
		"terminal-preflight-malformed-failed", "short", "8",
	)
	numericHashAttempt := newAttempt(
		"terminal-preflight-numeric-hash", "1", "9",
	)
	malformedStateFieldAttempt := newAttempt(
		"terminal-preflight-malformed-state-field", "2", "a",
	)
	unsafeIntegerAttempt := newAttempt(
		"terminal-preflight-unsafe-integer", "2", "b",
	)
	fractionalCountAttempt := newAttempt(
		"terminal-preflight-fractional-count", "2", "c",
	)
	unknownStatusAttempt := newAttempt(
		"terminal-preflight-unknown-status", "3", "d",
	)
	unknownFieldAttempt := newAttempt(
		"terminal-preflight-unknown-field", "4", "e",
	)
	nonObjectStateAttempt := newAttempt(
		"terminal-preflight-non-object-state", "5", "f",
	)
	missingStatusAttempt := newAttempt(
		"terminal-preflight-missing-status", "6", "0",
	)
	partialNonPendingAttempt := newAttempt(
		"terminal-preflight-partial-non-pending", "7", "1",
	)
	failedWithoutAttempt := newAttempt(
		"terminal-preflight-failed-without-attempt", "", "",
	)
	nullManagedStateAttempt := newAttempt(
		"terminal-preflight-null-managed-state", "8", "0",
	)

	var shadowParentID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO accounts (name, platform, type)
VALUES ('terminal-preflight-shadow-parent', 'openai', 'oauth')
RETURNING id`).Scan(&shadowParentID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET deleted_at = statement_timestamp()
WHERE id = $1`, deletedAttempt.accountID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET parent_account_id = $2, quota_dimension = 'spark'
WHERE id = $1`, shadowAttempt.accountID, shadowParentID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET type = 'apikey'
WHERE id = $1`, nonOAuthAttempt.accountID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET platform = 'anthropic'
WHERE id = $1`, nonOpenAIAttempt.accountID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = extra #- '{codex_auto_reset_credit_state,attempt_credit_hash}'
WHERE id = $1`, malformedResettingAttempt.accountID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = JSONB_SET(
    extra,
    '{codex_auto_reset_credit_state,attempt_credit_hash}',
    '111111111111111111111111'::JSONB
)
WHERE id = $1`, numericHashAttempt.accountID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = JSONB_SET(
    extra,
    '{codex_auto_reset_credit_state,available_count}',
    '"1"'::JSONB
)
WHERE id = $1`, malformedStateFieldAttempt.accountID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = JSONB_SET(
    extra,
    '{codex_auto_reset_credit_state,available_count}',
    '2147483648'::JSONB
)
WHERE id = $1`, unsafeIntegerAttempt.accountID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = JSONB_SET(
    extra,
    '{codex_auto_reset_credit_state,available_count}',
    '1.0'::JSONB
)
WHERE id = $1`, fractionalCountAttempt.accountID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = JSONB_SET(
    extra,
    '{codex_auto_reset_credit_state,status}',
    '"future"'::JSONB
)
WHERE id = $1`, unknownStatusAttempt.accountID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = JSONB_SET(
    extra,
    '{codex_auto_reset_credit_state,future_field}',
    'true'::JSONB
)
WHERE id = $1`, unknownFieldAttempt.accountID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = JSONB_SET(
    extra,
    '{codex_auto_reset_credit_state}',
    '"resetting"'::JSONB
)
WHERE id = $1`, nonObjectStateAttempt.accountID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = extra #- '{codex_auto_reset_credit_state,status}'
WHERE id = $1`, missingStatusAttempt.accountID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = JSONB_SET(
    extra #- '{codex_auto_reset_credit_state,attempt_cycle_hash}',
    '{codex_auto_reset_credit_state,status}',
    '"success"'::JSONB
)
WHERE id = $1`, partialNonPendingAttempt.accountID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = JSONB_SET(
    extra,
    '{codex_auto_reset_credit_state,status}',
    '"failed"'::JSONB
)
WHERE id IN ($1, $2)`, malformedFailedAttempt.accountID, failedWithoutAttempt.accountID))
	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = JSONB_SET(
    extra,
    '{codex_auto_reset_credit_state}',
    'null'::JSONB
)
WHERE id = $1`, nullManagedStateAttempt.accountID))

	restoreFence := disableOpenAIAutoResetLegacyScopeFence(t, ctx, db)
	redactedID := insertTerminal(
		redactedAttempt,
		redactedAttempt.accountScope,
		redactedAttempt.stableKey,
		redactedAttempt.legacy,
		200,
		redactedLegacy,
	)
	invalidID := insertTerminal(
		invalidAttempt,
		invalidAttempt.accountScope,
		invalidAttempt.stableKey,
		invalidAttempt.current,
		200,
		`{"result_code":`,
	)
	canonicalAccountID := insertTerminal(
		canonicalAccountAttempt,
		canonicalAccountAttempt.accountScope,
		canonicalAccountAttempt.stableKey,
		canonicalAccountAttempt.current,
		200,
		canonicalSuccess,
	)
	completedID := insertTerminal(
		completedAttempt,
		completedAttempt.accountScope,
		completedAttempt.stableKey,
		completedAttempt.legacy,
		200,
		redactedLegacy,
	)
	fingerprintMismatchID := insertTerminal(
		fingerprintAttempt,
		fingerprintAttempt.accountScope,
		fingerprintAttempt.stableKey,
		strings.Repeat("f", 64),
		200,
		canonicalNoCredit,
	)
	wrongKeyID := insertTerminal(
		wrongKeyAttempt,
		wrongKeyAttempt.accountScope,
		strings.Repeat("e", 64),
		wrongKeyAttempt.legacy,
		200,
		redactedLegacy,
	)
	longNumericID := insertTerminal(
		longNumericAttempt,
		longNumericAttempt.accountScope,
		longNumericAttempt.stableKey,
		longNumericAttempt.current,
		200,
		`{"result_code":"success","windows_reset":`+
			strings.Repeat("9", 100)+
			`,"post_process_recorded":true,"account_state_recovered":true}`,
	)
	deletedID := insertTerminal(
		deletedAttempt,
		deletedAttempt.accountScope,
		deletedAttempt.stableKey,
		deletedAttempt.current,
		200,
		canonicalSuccess,
	)
	shadowID := insertTerminal(
		shadowAttempt,
		shadowAttempt.accountScope,
		shadowAttempt.stableKey,
		shadowAttempt.current,
		200,
		canonicalSuccess,
	)
	nonOAuthID := insertTerminal(
		nonOAuthAttempt,
		nonOAuthAttempt.accountScope,
		nonOAuthAttempt.stableKey,
		nonOAuthAttempt.current,
		200,
		canonicalSuccess,
	)
	nonOpenAIID := insertTerminal(
		nonOpenAIAttempt,
		nonOpenAIAttempt.accountScope,
		nonOpenAIAttempt.stableKey,
		nonOpenAIAttempt.current,
		200,
		canonicalSuccess,
	)
	restoreFence()

	canonicalWorkerID := insertTerminal(
		canonicalWorkerAttempt,
		canonicalWorkerAttempt.workerScope,
		canonicalWorkerAttempt.stableKey,
		canonicalWorkerAttempt.legacy,
		200,
		canonicalNoCredit,
	)
	rawID := insertTerminal(
		rawAttempt,
		"openai_auto_reset_credit",
		rawAttempt.stableKey,
		rawAttempt.legacy,
		200,
		canonicalNoCredit,
	)
	oldPrincipalID := insertTerminal(
		oldPrincipalAttempt,
		"openai_auto_reset_credit|service_principal:"+
			openAIAutoResetInt64(workerPrincipalID+1000000),
		oldPrincipalAttempt.stableKey,
		oldPrincipalAttempt.legacy,
		200,
		canonicalNoCredit,
	)

	require.NoError(t, execOpenAIAutoResetStatement(ctx, db, `
UPDATE accounts
SET extra = JSONB_SET(
    extra,
    '{codex_auto_reset_credit_state,status}',
    '"success"'::JSONB
)
WHERE id = $1`, completedAttempt.accountID))

	rows, err := db.QueryContext(ctx, inventoryQuery)
	require.NoError(t, err)
	defer rows.Close()
	require.Equal(t, []string{
		"idempotency_record_id",
		"account_id",
		"response_state",
	}, mustColumnNames(t, rows))

	recordActual := make(map[int64]struct {
		accountID     int64
		responseState string
	})
	accountActual := make(map[int64]string)
	for rows.Next() {
		var recordID sql.NullInt64
		var row struct {
			accountID     int64
			responseState string
		}
		require.NoError(t, rows.Scan(&recordID, &row.accountID, &row.responseState))
		if recordID.Valid {
			require.NotContains(t, recordActual, recordID.Int64)
			recordActual[recordID.Int64] = row
			continue
		}
		require.NotContains(t, accountActual, row.accountID)
		accountActual[row.accountID] = row.responseState
	}
	require.NoError(t, rows.Err())

	require.Equal(t, map[int64]struct {
		accountID     int64
		responseState string
	}{
		redactedID: {
			accountID: redactedAttempt.accountID, responseState: "legacy_redacted_result",
		},
		invalidID: {
			accountID: invalidAttempt.accountID, responseState: "invalid_terminal_response",
		},
		rawID: {
			accountID: rawAttempt.accountID, responseState: "unreachable_scope",
		},
		oldPrincipalID: {
			accountID: oldPrincipalAttempt.accountID, responseState: "unreachable_scope",
		},
		fingerprintMismatchID: {
			accountID: fingerprintAttempt.accountID, responseState: "fingerprint_mismatch",
		},
		longNumericID: {
			accountID: longNumericAttempt.accountID, responseState: "invalid_terminal_response",
		},
	}, recordActual)
	require.Equal(t, map[int64]string{
		wrongKeyAttempt.accountID:            "missing_attempt_record",
		missingAttempt.accountID:             "missing_attempt_record",
		deletedAttempt.accountID:             "unreachable_account",
		shadowAttempt.accountID:              "unreachable_account",
		nonOAuthAttempt.accountID:            "unreachable_account",
		nonOpenAIAttempt.accountID:           "unreachable_account",
		malformedResettingAttempt.accountID:  "malformed_pending_state",
		malformedFailedAttempt.accountID:     "malformed_pending_state",
		numericHashAttempt.accountID:         "malformed_pending_state",
		malformedStateFieldAttempt.accountID: "malformed_pending_state",
		unsafeIntegerAttempt.accountID:       "malformed_pending_state",
		fractionalCountAttempt.accountID:     "malformed_pending_state",
		unknownStatusAttempt.accountID:       "malformed_pending_state",
		unknownFieldAttempt.accountID:        "malformed_pending_state",
		nonObjectStateAttempt.accountID:      "malformed_pending_state",
		missingStatusAttempt.accountID:       "malformed_pending_state",
		partialNonPendingAttempt.accountID:   "malformed_pending_state",
	}, accountActual)

	for _, omittedID := range []int64{
		canonicalAccountID,
		canonicalWorkerID,
		completedID,
		wrongKeyID,
		deletedID,
		shadowID,
		nonOAuthID,
		nonOpenAIID,
	} {
		require.NotContains(t, recordActual, omittedID)
	}
	require.NotContains(t, accountActual, failedWithoutAttempt.accountID)
	require.NotContains(t, accountActual, nullManagedStateAttempt.accountID)
}

func extractOpenAIAutoResetInventoryQuery(t *testing.T, script string) string {
	t.Helper()
	const startMarker = "WITH worker_identity AS ("
	const endMarker = "\n\n-- A succeeded idempotency row"
	start := strings.Index(script, startMarker)
	require.NotEqual(t, -1, start, "preflight inventory query start marker")
	endOffset := strings.Index(script[start:], endMarker)
	require.NotEqual(t, -1, endOffset, "preflight inventory query end marker")
	return strings.TrimSpace(script[start : start+endOffset])
}

func extractGroupNameInventoryQuery(t *testing.T, script string) string {
	t.Helper()
	const startMarker = "SELECT owner_user_id, lower(name) AS folded_name"
	const endMarker = "\n\nSELECT lower(name) AS folded_name, COUNT(*) AS active_account_count"
	start := strings.Index(script, startMarker)
	require.NotEqual(t, -1, start, "group-name inventory query start marker")
	endOffset := strings.Index(script[start:], endMarker)
	require.NotEqual(t, -1, endOffset, "group-name inventory query end marker")
	return strings.TrimSpace(script[start : start+endOffset])
}

func extractDefaultGroupNameInventoryQuery(t *testing.T, script string) string {
	t.Helper()
	const startMarker = "SELECT owner_user_id, lower(name) AS default_name"
	const endMarker = "\n\nSELECT\n  (SELECT COUNT(*) FROM users) AS users_total"
	start := strings.Index(script, startMarker)
	require.NotEqual(t, -1, start, "default group-name inventory query start marker")
	endOffset := strings.Index(script[start:], endMarker)
	require.NotEqual(t, -1, endOffset, "default group-name inventory query end marker")
	return strings.TrimSpace(script[start : start+endOffset])
}

func extractOpenAIAutoResetTerminalRecoveryInventoryQuery(t *testing.T, script string) string {
	t.Helper()
	const startMarker = "WITH terminal_worker_identity AS ("
	const endMarker = "\n\nROLLBACK;"
	start := strings.Index(script, startMarker)
	require.NotEqual(t, -1, start, "terminal recovery inventory query start marker")
	endOffset := strings.Index(script[start:], endMarker)
	require.NotEqual(t, -1, endOffset, "terminal recovery inventory query end marker")
	return strings.TrimSpace(script[start : start+endOffset])
}

func mustColumnNames(t *testing.T, rows *sql.Rows) []string {
	t.Helper()
	columns, err := rows.Columns()
	require.NoError(t, err)
	return columns
}
