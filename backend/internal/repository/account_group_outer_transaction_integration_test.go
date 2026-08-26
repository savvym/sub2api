//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCreateWithValidatedAccountGroupsRollsBackWithOuterTransaction(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	prefix := fmt.Sprintf("validated-create-outer-rollback-%d", time.Now().UnixNano())
	group := mustCreateGroup(t, client, &service.Group{
		Name:           prefix + "-group",
		Platform:       service.PlatformOpenAI,
		RateMultiplier: 1,
	})
	account := &service.Account{
		Name:        prefix + "-account",
		Platform:    service.PlatformOpenAI,
		Type:        service.AccountTypeAPIKey,
		Status:      service.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{"api_key": "secret"},
		Extra:       map[string]any{},
	}
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", account.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE account_id = $1 OR group_id = $2", account.ID, group.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1 OR name = $2", account.ID, account.Name)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE id = $1", group.ID)
	})

	outerTx, err := client.Tx(ctx)
	require.NoError(t, err, "begin outer transaction")
	t.Cleanup(func() { _ = outerTx.Rollback() })
	txCtx := dbent.NewTxContext(ctx, outerTx)

	validated := false
	err = repo.CreateWithValidatedAccountGroups(
		txCtx,
		account,
		[]service.AccountGroup{{GroupID: group.ID, Priority: 37}},
		func(snapshot service.AccountGroupCreateSnapshot) error {
			validated = true
			require.Len(t, snapshot.Groups, 1)
			require.Equal(t, group.ID, snapshot.Groups[0].ID)
			require.Empty(t, snapshot.CurrentAccountsByGroup[group.ID])
			return nil
		},
	)
	require.NoError(t, err)
	require.True(t, validated)
	require.NotZero(t, account.ID)

	var stagedAccountCount, stagedPriority, stagedOutboxCount int
	require.NoError(t, scanSingleRow(txCtx, outerTx.Client(),
		"SELECT COUNT(*) FROM accounts WHERE id = $1 AND name = $2",
		[]any{account.ID, account.Name},
		&stagedAccountCount,
	))
	require.Equal(t, 1, stagedAccountCount)
	require.NoError(t, scanSingleRow(txCtx, outerTx.Client(),
		"SELECT priority FROM account_groups WHERE account_id = $1 AND group_id = $2",
		[]any{account.ID, group.ID},
		&stagedPriority,
	))
	require.Equal(t, 37, stagedPriority)
	require.NoError(t, scanSingleRow(txCtx, outerTx.Client(),
		"SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1 AND account_id = $2",
		[]any{service.SchedulerOutboxEventAccountChanged, account.ID},
		&stagedOutboxCount,
	))
	require.Equal(t, 1, stagedOutboxCount)

	require.NoError(t, outerTx.Rollback(), "rollback outer transaction")

	var accountCount, bindingCount, outboxCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM accounts WHERE id = $1 OR name = $2", account.ID, account.Name,
	).Scan(&accountCount))
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM account_groups WHERE account_id = $1 AND group_id = $2", account.ID, group.ID,
	).Scan(&bindingCount))
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1 AND account_id = $2",
		service.SchedulerOutboxEventAccountChanged, account.ID,
	).Scan(&outboxCount))
	require.Zero(t, accountCount)
	require.Zero(t, bindingCount)
	require.Zero(t, outboxCount)
}

func TestApplyGroupAccountMembershipDiffRollsBackWithOuterTransaction(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	prefix := fmt.Sprintf("group-diff-outer-rollback-%d", time.Now().UnixNano())
	group := mustCreateGroup(t, client, &service.Group{
		Name:           prefix + "-group",
		Platform:       service.PlatformAnthropic,
		RateMultiplier: 1,
	})
	current := mustCreateAccount(t, client, &service.Account{
		Name:     prefix + "-current",
		Platform: service.PlatformAnthropic,
		Type:     service.AccountTypeOAuth,
	})
	added := mustCreateAccount(t, client, &service.Account{
		Name:     prefix + "-added",
		Platform: service.PlatformAnthropic,
		Type:     service.AccountTypeOAuth,
	})
	_, err := integrationDB.ExecContext(ctx,
		"INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, 23, NOW())",
		current.ID, group.ID,
	)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE group_id = $1", group.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE group_id = $1", group.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE group_id = $1", group.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id IN ($1, $2)", current.ID, added.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE id = $1", group.ID)
	})

	outerTx, err := client.Tx(ctx)
	require.NoError(t, err, "begin outer transaction")
	t.Cleanup(func() { _ = outerTx.Rollback() })
	txCtx := dbent.NewTxContext(ctx, outerTx)

	mutation, err := repo.ApplyGroupAccountMembershipDiff(
		txCtx,
		group.ID,
		[]int64{added.ID},
		[]int64{current.ID},
		59,
		nil,
	)
	require.NoError(t, err)
	require.Equal(t, []int64{added.ID}, mutation.AddedAccountIDs)
	require.Equal(t, []int64{current.ID}, mutation.RemovedAccountIDs)

	var stagedCurrentCount, stagedAddedPriority, stagedOutboxCount int
	require.NoError(t, scanSingleRow(txCtx, outerTx.Client(),
		"SELECT COUNT(*) FROM account_groups WHERE account_id = $1 AND group_id = $2",
		[]any{current.ID, group.ID},
		&stagedCurrentCount,
	))
	require.Zero(t, stagedCurrentCount)
	require.NoError(t, scanSingleRow(txCtx, outerTx.Client(),
		"SELECT priority FROM account_groups WHERE account_id = $1 AND group_id = $2",
		[]any{added.ID, group.ID},
		&stagedAddedPriority,
	))
	require.Equal(t, 59, stagedAddedPriority)
	require.NoError(t, scanSingleRow(txCtx, outerTx.Client(),
		"SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1 AND group_id = $2",
		[]any{service.SchedulerOutboxEventGroupChanged, group.ID},
		&stagedOutboxCount,
	))
	require.Equal(t, 1, stagedOutboxCount)

	require.NoError(t, outerTx.Rollback(), "rollback outer transaction")

	var persistedCurrentPriority, persistedAddedCount, outboxCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT priority FROM account_groups WHERE account_id = $1 AND group_id = $2", current.ID, group.ID,
	).Scan(&persistedCurrentPriority))
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM account_groups WHERE account_id = $1 AND group_id = $2", added.ID, group.ID,
	).Scan(&persistedAddedCount))
	require.NoError(t, integrationDB.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1 AND group_id = $2",
		service.SchedulerOutboxEventGroupChanged, group.ID,
	).Scan(&outboxCount))
	require.Equal(t, 23, persistedCurrentPriority)
	require.Zero(t, persistedAddedCount)
	require.Zero(t, outboxCount)
}
