//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestListOpenAIAutoResetRecoveryCandidatePageBypassesMutableEligibilityWithoutSkippingAfterConvergence(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	repo := newAccountRepositoryWithSQL(tx.Client(), tx, nil)
	_, err := tx.ExecContext(ctx, `
		UPDATE accounts
		SET extra = extra - 'codex_auto_reset_credit_state'
	`)
	require.NoError(t, err)

	insert := func(
		name, platform, accountType, status string,
		schedulable bool,
		parentAccountID *int64,
		stateStatus, creditHash, cycleHash string,
	) int64 {
		t.Helper()
		extra := map[string]any{
			service.OpenAIAutoResetCreditEnabledExtraKey: false,
		}
		if stateStatus != "" {
			extra[service.OpenAIAutoResetCreditStateExtraKey] = map[string]any{
				"status":              stateStatus,
				"attempt_credit_hash": creditHash,
				"attempt_cycle_hash":  cycleHash,
			}
		}
		rawExtra, marshalErr := json.Marshal(extra)
		require.NoError(t, marshalErr)
		var id int64
		err := scanSingleRow(ctx, tx, `
			INSERT INTO accounts (
				name, platform, type, status, schedulable, parent_account_id, extra
			)
			VALUES ($1, $2, $3, $4, $5, $6, $7::jsonb)
			RETURNING id
		`, []any{name, platform, accountType, status, schedulable, parentAccountID, string(rawExtra)}, &id)
		require.NoError(t, err)
		return id
	}

	creditHash := "111111111111111111111111"
	cycleHash := "222222222222222222222222"
	firstID := insert(
		"auto-reset-recovery-disabled", service.PlatformOpenAI, service.AccountTypeOAuth,
		service.StatusDisabled, false, nil, service.OpenAIAutoResetStatusResetting, creditHash, cycleHash,
	)
	secondID := insert(
		"auto-reset-recovery-error", service.PlatformOpenAI, service.AccountTypeOAuth,
		service.StatusError, true, nil, service.OpenAIAutoResetStatusFailed, creditHash, cycleHash,
	)
	thirdID := insert(
		"auto-reset-recovery-unschedulable", service.PlatformOpenAI, service.AccountTypeOAuth,
		service.StatusActive, false, nil, service.OpenAIAutoResetStatusResetting, creditHash, cycleHash,
	)

	_ = insert(
		"auto-reset-recovery-terminal", service.PlatformOpenAI, service.AccountTypeOAuth,
		service.StatusActive, true, nil, service.OpenAIAutoResetStatusSuccess, creditHash, cycleHash,
	)
	invalidHashID := insert(
		"auto-reset-recovery-invalid-hash", service.PlatformOpenAI, service.AccountTypeOAuth,
		service.StatusActive, true, nil, service.OpenAIAutoResetStatusResetting, "short", cycleHash,
	)
	_ = insert(
		"auto-reset-recovery-pre-effect-failed", service.PlatformOpenAI, service.AccountTypeOAuth,
		service.StatusDisabled, false, nil, service.OpenAIAutoResetStatusFailed, "", "",
	)
	_ = insert(
		"auto-reset-recovery-api-key", service.PlatformOpenAI, service.AccountTypeAPIKey,
		service.StatusActive, true, nil, service.OpenAIAutoResetStatusResetting, creditHash, cycleHash,
	)
	shadowParentID := firstID
	_ = insert(
		"auto-reset-recovery-shadow", service.PlatformOpenAI, service.AccountTypeOAuth,
		service.StatusActive, true, &shadowParentID, service.OpenAIAutoResetStatusResetting, creditHash, cycleHash,
	)
	deletedID := insert(
		"auto-reset-recovery-deleted", service.PlatformOpenAI, service.AccountTypeOAuth,
		service.StatusActive, true, nil, service.OpenAIAutoResetStatusResetting, creditHash, cycleHash,
	)
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET deleted_at = NOW() WHERE id = $1`, deletedID)
	require.NoError(t, err)

	firstPage, err := repo.ListOpenAIAutoResetRecoveryCandidatePage(
		ctx,
		service.OpenAIAutoResetRecoveryCandidatePageOptions{Limit: 2},
	)
	require.NoError(t, err)
	require.Equal(t, []int64{firstID, secondID}, firstPage.AccountIDs)
	require.Equal(t, secondID, firstPage.NextAfterID)
	require.True(t, firstPage.HasMore)

	// Recovery removes the first page from the predicate before the next query.
	// Keyset pagination must still advance to the remaining candidate.
	_, err = tx.ExecContext(ctx, fmt.Sprintf(`
		UPDATE accounts
		SET extra = jsonb_set(
			extra,
			'{%s,status}',
			'"success"'::jsonb
		)
		WHERE id IN ($1, $2)
	`, service.OpenAIAutoResetCreditStateExtraKey), firstID, secondID)
	require.NoError(t, err)

	secondPage, err := repo.ListOpenAIAutoResetRecoveryCandidatePage(
		ctx,
		service.OpenAIAutoResetRecoveryCandidatePageOptions{
			AfterID: firstPage.NextAfterID,
			Limit:   2,
		},
	)
	require.NoError(t, err)
	require.Equal(t, []int64{thirdID, invalidHashID}, secondPage.AccountIDs)
	require.Equal(t, invalidHashID, secondPage.NextAfterID)
	require.True(t, secondPage.HasMore)

	finalPage, err := repo.ListOpenAIAutoResetRecoveryCandidatePage(
		ctx,
		service.OpenAIAutoResetRecoveryCandidatePageOptions{
			AfterID: secondPage.NextAfterID,
			Limit:   2,
		},
	)
	require.NoError(t, err)
	require.Empty(t, finalPage.AccountIDs)
	require.False(t, finalPage.HasMore)
}
