//go:build integration

package repository

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupAccountMembershipRepositoryListAndApplyDiff(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newAccountRepositoryWithSQL(client, tx, nil)

	target := mustCreateGroup(t, client, &service.Group{Name: "membership-target", Platform: service.PlatformAnthropic, RateMultiplier: 1})
	other := mustCreateGroup(t, client, &service.Group{Name: "membership-other", Platform: service.PlatformAnthropic, RateMultiplier: 1})
	current := mustCreateAccount(t, client, &service.Account{Name: "membership-current", Platform: service.PlatformAnthropic})
	candidate := mustCreateAccount(t, client, &service.Account{Name: "membership-candidate", Platform: service.PlatformAnthropic})
	already := mustCreateAccount(t, client, &service.Account{Name: "membership-already", Platform: service.PlatformAnthropic})
	notMember := mustCreateAccount(t, client, &service.Account{Name: "membership-not-member", Platform: service.PlatformAnthropic})

	for _, binding := range []struct {
		accountID int64
		groupID   int64
		priority  int
	}{
		{current.ID, target.ID, 9},
		{current.ID, other.ID, 17},
		{candidate.ID, other.ID, 23},
		{already.ID, target.ID, 31},
	} {
		_, err := tx.ExecContext(ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, $3, NOW())", binding.accountID, binding.groupID, binding.priority)
		require.NoError(t, err)
	}

	members, err := repo.ListGroupAccountMembers(ctx, target.ID, service.GroupAccountListFilters{Page: 1, PageSize: 20})
	require.NoError(t, err)
	require.EqualValues(t, 2, members.Total)
	require.EqualValues(t, 2, members.ScopeTotal)
	require.Equal(t, []int64{already.ID, current.ID}, sortedRecordIDs(members.Items))

	_, err = tx.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE group_id = $1", target.ID)
	require.NoError(t, err)
	validated := false
	mutation, err := repo.ApplyGroupAccountMembershipDiff(ctx, target.ID, []int64{candidate.ID, already.ID}, []int64{current.ID, notMember.ID}, 50, func(snapshot service.GroupAccountMembershipSnapshot) error {
		validated = true
		require.Equal(t, target.ID, snapshot.Group.ID)
		require.Equal(t, []int64{candidate.ID}, snapshot.AddedAccountIDs)
		require.Equal(t, []int64{current.ID}, snapshot.RemovedAccountIDs)
		require.Equal(t, []int64{already.ID}, snapshot.AlreadyMemberIDs)
		require.Equal(t, []int64{notMember.ID}, snapshot.NotMemberIDs)
		return nil
	})
	require.NoError(t, err)
	require.True(t, validated)
	require.Equal(t, []int64{candidate.ID}, mutation.AddedAccountIDs)
	require.Equal(t, []int64{current.ID}, mutation.RemovedAccountIDs)
	require.EqualValues(t, 2, mutation.AccountCount)
	require.EqualValues(t, 2, mutation.ActiveAccountCount)

	assertMembershipPriority(t, tx, candidate.ID, target.ID, 50)
	assertMembershipPriority(t, tx, already.ID, target.ID, 31)
	assertMembershipPriority(t, tx, candidate.ID, other.ID, 23)
	assertMembershipPriority(t, tx, current.ID, other.ID, 17)
	var removedCount int
	require.NoError(t, scanSingleRow(ctx, tx, "SELECT COUNT(*) FROM account_groups WHERE account_id = $1 AND group_id = $2", []any{current.ID, target.ID}, &removedCount))
	require.Zero(t, removedCount)
	var outboxCount int
	require.NoError(t, scanSingleRow(ctx, tx, "SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1 AND group_id = $2", []any{service.SchedulerOutboxEventGroupChanged, target.ID}, &outboxCount))
	require.Equal(t, 1, outboxCount)
}

func TestReplaceAccountGroupMembershipsPreservesIntersectionMetadata(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newAccountRepositoryWithSQL(client, tx, nil)

	keep := mustCreateGroup(t, client, &service.Group{Name: "replace-keep", Platform: service.PlatformAnthropic, RateMultiplier: 1})
	remove := mustCreateGroup(t, client, &service.Group{Name: "replace-remove", Platform: service.PlatformAnthropic, RateMultiplier: 1})
	add := mustCreateGroup(t, client, &service.Group{Name: "replace-add", Platform: service.PlatformAnthropic, RateMultiplier: 1})
	target := mustCreateAccount(t, client, &service.Account{Name: "replace-target", Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth})
	peer := mustCreateAccount(t, client, &service.Account{Name: "replace-peer", Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth})

	keepCreatedAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	_, err := tx.ExecContext(ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, $3, $4)", target.ID, keep.ID, 9, keepCreatedAt)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, $3, NOW())", target.ID, remove.ID, 17)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, $3, NOW())", peer.ID, add.ID, 23)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", target.ID)
	require.NoError(t, err)

	validated := false
	replacement, err := repo.ReplaceAccountGroupMemberships(ctx, target.ID, []int64{add.ID, keep.ID, add.ID}, 50, func(snapshot service.AccountGroupMembershipReplacementSnapshot) error {
		validated = true
		require.Equal(t, target.ID, snapshot.Account.ID)
		require.ElementsMatch(t, []int64{keep.ID, remove.ID}, snapshot.CurrentGroupIDs)
		require.ElementsMatch(t, []int64{keep.ID, add.ID}, snapshot.DesiredGroupIDs)
		require.Equal(t, []int64{add.ID}, snapshot.AddedGroupIDs)
		require.Equal(t, []int64{remove.ID}, snapshot.RemovedGroupIDs)
		require.Contains(t, snapshot.GroupsByID, keep.ID)
		require.Contains(t, snapshot.GroupsByID, add.ID)
		require.Equal(t, []int64{target.ID}, sortedAccountIDs(snapshot.CurrentAccountsByGroup[keep.ID]))
		require.Equal(t, []int64{peer.ID}, sortedAccountIDs(snapshot.CurrentAccountsByGroup[add.ID]))
		require.Equal(t, sortedInt64s([]int64{target.ID, peer.ID}), sortedAccountIDs(snapshot.FinalAccountsByGroup[add.ID]))
		require.Empty(t, snapshot.FinalAccountsByGroup[remove.ID])
		return nil
	})
	require.NoError(t, err)
	require.True(t, validated)
	require.ElementsMatch(t, []int64{keep.ID, remove.ID}, replacement.CurrentGroupIDs)
	require.ElementsMatch(t, []int64{keep.ID, add.ID}, replacement.DesiredGroupIDs)
	require.Equal(t, []int64{add.ID}, replacement.AddedGroupIDs)
	require.Equal(t, []int64{remove.ID}, replacement.RemovedGroupIDs)

	assertMembershipPriority(t, tx, target.ID, keep.ID, 9)
	assertMembershipPriority(t, tx, target.ID, add.ID, 50)
	var keptCreatedAt time.Time
	require.NoError(t, scanSingleRow(ctx, tx,
		"SELECT created_at FROM account_groups WHERE account_id = $1 AND group_id = $2",
		[]any{target.ID, keep.ID},
		&keptCreatedAt,
	))
	require.True(t, keepCreatedAt.Equal(keptCreatedAt), "unchanged membership created_at must be preserved")
	var removedCount int
	require.NoError(t, scanSingleRow(ctx, tx,
		"SELECT COUNT(*) FROM account_groups WHERE account_id = $1 AND group_id = $2",
		[]any{target.ID, remove.ID},
		&removedCount,
	))
	require.Zero(t, removedCount)

	var outboxPayload string
	require.NoError(t, scanSingleRow(ctx, tx,
		"SELECT payload::text FROM scheduler_outbox WHERE event_type = $1 AND account_id = $2",
		[]any{service.SchedulerOutboxEventAccountGroupsChanged, target.ID},
		&outboxPayload,
	))
	affectedGroupIDs := sortedInt64s([]int64{keep.ID, remove.ID, add.ID})
	require.JSONEq(t, fmt.Sprintf(`{"group_ids":[%d,%d,%d]}`, affectedGroupIDs[0], affectedGroupIDs[1], affectedGroupIDs[2]), outboxPayload)
}

func TestReplaceAccountGroupMembershipsNoopDoesNotRewriteOrEnqueue(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newAccountRepositoryWithSQL(client, tx, nil)
	group := mustCreateGroup(t, client, &service.Group{Name: "replace-noop", Platform: service.PlatformOpenAI, RateMultiplier: 1})
	target := mustCreateAccount(t, client, &service.Account{Name: "replace-noop-target", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth})
	createdAt := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Microsecond)
	_, err := tx.ExecContext(ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, 37, $3)", target.ID, group.ID, createdAt)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", target.ID)
	require.NoError(t, err)

	replacement, err := repo.ReplaceAccountGroupMemberships(ctx, target.ID, []int64{group.ID, group.ID}, 50, nil)
	require.NoError(t, err)
	require.Empty(t, replacement.AddedGroupIDs)
	require.Empty(t, replacement.RemovedGroupIDs)
	assertMembershipPriority(t, tx, target.ID, group.ID, 37)
	var keptCreatedAt time.Time
	require.NoError(t, scanSingleRow(ctx, tx,
		"SELECT created_at FROM account_groups WHERE account_id = $1 AND group_id = $2",
		[]any{target.ID, group.ID},
		&keptCreatedAt,
	))
	require.True(t, createdAt.Equal(keptCreatedAt))
	var outboxCount int
	require.NoError(t, scanSingleRow(ctx, tx,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1 AND account_id = $2",
		[]any{service.SchedulerOutboxEventAccountGroupsChanged, target.ID},
		&outboxCount,
	))
	require.Zero(t, outboxCount)
}

func TestReplaceAccountGroupMembershipsValidatorFailureRollsBackDiff(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newAccountRepositoryWithSQL(client, tx, nil)
	current := mustCreateGroup(t, client, &service.Group{Name: "replace-validator-current", Platform: service.PlatformAnthropic, RateMultiplier: 1})
	added := mustCreateGroup(t, client, &service.Group{Name: "replace-validator-added", Platform: service.PlatformAnthropic, RateMultiplier: 1})
	target := mustCreateAccount(t, client, &service.Account{Name: "replace-validator-target", Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth})
	_, err := tx.ExecContext(ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, 29, NOW())", target.ID, current.ID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1", target.ID)
	require.NoError(t, err)

	validationErr := fmt.Errorf("locked validation failed")
	_, err = repo.ReplaceAccountGroupMemberships(ctx, target.ID, []int64{added.ID}, 50, func(service.AccountGroupMembershipReplacementSnapshot) error {
		return validationErr
	})
	require.ErrorIs(t, err, validationErr)
	assertMembershipPriority(t, tx, target.ID, current.ID, 29)
	assertNoMembership(t, tx, target.ID, added.ID)
	var outboxCount int
	require.NoError(t, scanSingleRow(ctx, tx,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1 AND account_id = $2",
		[]any{service.SchedulerOutboxEventAccountGroupsChanged, target.ID},
		&outboxCount,
	))
	require.Zero(t, outboxCount)
}

func TestReplaceAccountGroupMembershipsRemovesSoftDeletedCurrentGroup(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newAccountRepositoryWithSQL(client, tx, nil)
	deletedGroup := mustCreateGroup(t, client, &service.Group{Name: "replace-soft-deleted", Platform: service.PlatformOpenAI, RateMultiplier: 1})
	target := mustCreateAccount(t, client, &service.Account{Name: "replace-soft-deleted-target", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth})
	_, err := tx.ExecContext(ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, 41, NOW())", target.ID, deletedGroup.ID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "UPDATE groups SET deleted_at = NOW() WHERE id = $1", deletedGroup.ID)
	require.NoError(t, err)

	loaded, err := repo.GetByID(ctx, target.ID)
	require.NoError(t, err)
	require.Empty(t, loaded.GroupIDs)
	require.Empty(t, loaded.AccountGroups)
	require.Empty(t, loaded.Groups)
	var residualMembershipCount int
	require.NoError(t, scanSingleRow(ctx, tx,
		"SELECT COUNT(*) FROM account_groups WHERE account_id = $1 AND group_id = $2",
		[]any{target.ID, deletedGroup.ID},
		&residualMembershipCount,
	))
	require.Equal(t, 1, residualMembershipCount, "account projection must not delete the soft-deleted group's residual membership")

	replacement, err := repo.ReplaceAccountGroupMemberships(ctx, target.ID, nil, 50, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{deletedGroup.ID}, replacement.RemovedGroupIDs)
	assertNoMembership(t, tx, target.ID, deletedGroup.ID)
	var outboxCount int
	require.NoError(t, scanSingleRow(ctx, tx,
		"SELECT COUNT(*) FROM scheduler_outbox WHERE event_type = $1 AND account_id = $2",
		[]any{service.SchedulerOutboxEventAccountGroupsChanged, target.ID},
		&outboxCount,
	))
	require.Equal(t, 1, outboxCount, "clearing the final membership must refresh removed scheduler buckets")

	_, err = repo.ReplaceAccountGroupMemberships(ctx, target.ID, []int64{deletedGroup.ID}, 50, nil)
	require.ErrorIs(t, err, service.ErrGroupNotFound)
}

func TestReplaceAccountGroupMembershipsDetectsExpandedLockSet(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	lockedGroup := mustCreateGroup(t, client, &service.Group{Name: "replace-locked-set", Platform: service.PlatformOpenAI, RateMultiplier: 1})
	lateGroup := mustCreateGroup(t, client, &service.Group{Name: "replace-late-set", Platform: service.PlatformOpenAI, RateMultiplier: 1})
	target := mustCreateAccount(t, client, &service.Account{Name: "replace-expanded-target", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth})
	for _, groupID := range []int64{lockedGroup.ID, lateGroup.ID} {
		_, err := tx.ExecContext(ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, 11, NOW())", target.ID, groupID)
		require.NoError(t, err)
	}

	result, retry, err := replaceAccountGroupMembershipsInTx(
		ctx,
		client,
		target.ID,
		[]int64{lockedGroup.ID},
		[]int64{lockedGroup.ID},
		50,
		nil,
	)
	require.NoError(t, err)
	require.Nil(t, result)
	require.True(t, retry)
	assertMembershipPriority(t, tx, target.ID, lockedGroup.ID, 11)
	assertMembershipPriority(t, tx, target.ID, lateGroup.ID, 11)
}

func TestGroupAccountMembershipRepositoryCandidateEligibilityAndSoftDelete(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newAccountRepositoryWithSQL(client, tx, nil)
	target := mustCreateGroup(t, client, &service.Group{Name: "candidate-target", Platform: service.PlatformAnthropic, RateMultiplier: 1})
	prefix := fmt.Sprintf("candidate-eligibility-%d-", target.ID)
	eligibleSame := mustCreateAccount(t, client, &service.Account{Name: prefix + "same", Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth})
	eligibleMixed := mustCreateAccount(t, client, &service.Account{Name: prefix + "mixed", Platform: service.PlatformAntigravity, Type: service.AccountTypeOAuth, Extra: map[string]any{"mixed_scheduling": true}})
	ineligiblePlain := mustCreateAccount(t, client, &service.Account{Name: prefix + "plain", Platform: service.PlatformAntigravity, Type: service.AccountTypeOAuth})
	ineligibleAPIKey := mustCreateAccount(t, client, &service.Account{Name: prefix + "apikey", Platform: service.PlatformAnthropic, Type: service.AccountTypeAPIKey})
	bound := mustCreateAccount(t, client, &service.Account{Name: prefix + "bound", Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth})
	deleted := mustCreateAccount(t, client, &service.Account{Name: prefix + "deleted", Platform: service.PlatformAnthropic, Type: service.AccountTypeOAuth})
	_, err := tx.ExecContext(ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, 50, NOW())", bound.ID, target.ID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, "UPDATE accounts SET deleted_at = NOW() WHERE id = $1", deleted.ID)
	require.NoError(t, err)

	page, err := repo.ListGroupAccountCandidates(ctx, target.ID, service.GroupAccountListFilters{Page: 1, PageSize: 20, Search: prefix}, service.GroupAccountCandidatePolicy{
		AllowedPlatforms:                     []string{service.PlatformAnthropic, service.PlatformAntigravity},
		RequireMixedSchedulingForAntigravity: true,
		RequireOAuth:                         true,
	})
	require.NoError(t, err)
	require.EqualValues(t, 2, page.Total)
	require.Equal(t, []int64{eligibleSame.ID, eligibleMixed.ID}, sortedRecordIDs(page.Items))
	require.NotContains(t, sortedRecordIDs(page.Items), ineligiblePlain.ID)
	require.NotContains(t, sortedRecordIDs(page.Items), ineligibleAPIKey.ID)
	require.NotContains(t, sortedRecordIDs(page.Items), bound.ID)

	_, err = repo.ApplyGroupAccountMembershipDiff(ctx, target.ID, []int64{deleted.ID}, nil, 50, nil)
	require.Equal(t, "account_not_found", infraReason(err))
	var bindingCount int
	require.NoError(t, scanSingleRow(ctx, tx, "SELECT COUNT(*) FROM account_groups WHERE account_id = $1 AND group_id = $2", []any{deleted.ID, target.ID}, &bindingCount))
	require.Zero(t, bindingCount)
}

func TestGroupAccountMembershipRepositoryOutboxFailureRollsBackDiff(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	groupName := fmt.Sprintf("membership-atomic-%d", suffix)
	group, err := integrationEntClient.Group.Create().SetName(groupName).SetPlatform(service.PlatformAnthropic).SetRateMultiplier(1).Save(ctx)
	require.NoError(t, err)
	current, err := integrationEntClient.Account.Create().SetName(groupName + "-current").SetPlatform(service.PlatformAnthropic).SetType(service.AccountTypeOAuth).Save(ctx)
	require.NoError(t, err)
	added, err := integrationEntClient.Account.Create().SetName(groupName + "-added").SetPlatform(service.PlatformAnthropic).SetType(service.AccountTypeOAuth).Save(ctx)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, 12, NOW())", current.ID, group.ID)
	require.NoError(t, err)

	functionName := fmt.Sprintf("fail_membership_outbox_%d", suffix)
	triggerName := fmt.Sprintf("fail_membership_outbox_trigger_%d", suffix)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger AS $$
		BEGIN
			IF NEW.group_id = %d THEN
				RAISE EXCEPTION 'forced membership outbox failure';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`, functionName, group.ID))
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT ON scheduler_outbox FOR EACH ROW EXECUTE FUNCTION %s()", triggerName, functionName))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON scheduler_outbox", triggerName))
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName))
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE group_id = $1", group.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE group_id = $1", group.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id IN ($1, $2)", current.ID, added.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE id = $1", group.ID)
	})

	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	_, err = repo.ApplyGroupAccountMembershipDiff(ctx, group.ID, []int64{added.ID}, []int64{current.ID}, 50, nil)
	require.ErrorContains(t, err, "forced membership outbox failure")
	assertMembershipPriority(t, integrationDB, current.ID, group.ID, 12)
	var addedCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM account_groups WHERE account_id = $1 AND group_id = $2", added.ID, group.ID).Scan(&addedCount))
	require.Zero(t, addedCount)
}

func TestReplaceAccountGroupMembershipsOutboxFailureRollsBackDiff(t *testing.T) {
	ctx := context.Background()
	suffix := time.Now().UnixNano()
	prefix := fmt.Sprintf("replace-account-outbox-%d", suffix)
	current, err := integrationEntClient.Group.Create().SetName(prefix + "-current").SetPlatform(service.PlatformOpenAI).SetRateMultiplier(1).Save(ctx)
	require.NoError(t, err)
	added, err := integrationEntClient.Group.Create().SetName(prefix + "-added").SetPlatform(service.PlatformOpenAI).SetRateMultiplier(1).Save(ctx)
	require.NoError(t, err)
	target, err := integrationEntClient.Account.Create().SetName(prefix + "-target").SetPlatform(service.PlatformOpenAI).SetType(service.AccountTypeOAuth).Save(ctx)
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, 12, NOW())", target.ID, current.ID)
	require.NoError(t, err)

	functionName := fmt.Sprintf("fail_account_membership_outbox_%d", suffix)
	triggerName := fmt.Sprintf("fail_account_membership_outbox_trigger_%d", suffix)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`
		CREATE FUNCTION %s() RETURNS trigger AS $$
		BEGIN
			IF NEW.event_type = '%s' AND NEW.account_id = %d THEN
				RAISE EXCEPTION 'forced account membership outbox failure';
			END IF;
			RETURN NEW;
		END;
		$$ LANGUAGE plpgsql`, functionName, service.SchedulerOutboxEventAccountGroupsChanged, target.ID))
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT ON scheduler_outbox FOR EACH ROW EXECUTE FUNCTION %s()", triggerName, functionName))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON scheduler_outbox", triggerName))
		_, _ = integrationDB.ExecContext(context.Background(), fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName))
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", target.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE account_id = $1", target.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", target.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE id IN ($1, $2)", current.ID, added.ID)
	})

	repo := newAccountRepositoryWithSQL(integrationEntClient, integrationDB, nil)
	_, err = repo.ReplaceAccountGroupMemberships(ctx, target.ID, []int64{added.ID}, 50, nil)
	require.ErrorContains(t, err, "forced account membership outbox failure")
	assertMembershipPriority(t, integrationDB, target.ID, current.ID, 12)
	assertNoMembership(t, integrationDB, target.ID, added.ID)
}

func TestAccountMembershipAndFieldsRollbackWithOuterTransaction(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	repo := newAccountRepositoryWithSQL(client, integrationDB, nil)
	suffix := time.Now().UnixNano()
	prefix := fmt.Sprintf("account-membership-outer-rollback-%d", suffix)
	current := mustCreateGroup(t, client, &service.Group{Name: prefix + "-current", Platform: service.PlatformOpenAI, RateMultiplier: 1})
	desired := mustCreateGroup(t, client, &service.Group{Name: prefix + "-desired", Platform: service.PlatformOpenAI, RateMultiplier: 1})
	target := mustCreateAccount(t, client, &service.Account{Name: prefix + "-original", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth})
	_, err := integrationDB.ExecContext(ctx, "INSERT INTO account_groups (account_id, group_id, priority, created_at) VALUES ($1, $2, 23, NOW())", target.ID, current.ID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM scheduler_outbox WHERE account_id = $1", target.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM account_groups WHERE account_id = $1", target.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM accounts WHERE id = $1", target.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM groups WHERE id IN ($1, $2)", current.ID, desired.ID)
	})

	tx, err := client.Tx(ctx)
	require.NoError(t, err, "begin outer transaction")
	txCtx := dbent.NewTxContext(ctx, tx)

	_, err = repo.ReplaceAccountGroupMemberships(txCtx, target.ID, []int64{desired.ID}, 50, nil)
	require.NoError(t, err)
	pending := *target
	pending.Name = prefix + "-updated"
	pending.GroupIDs = []int64{desired.ID}
	require.NoError(t, repo.Update(txCtx, &pending))

	var stagedName string
	require.NoError(t, scanSingleRow(txCtx, tx.Client(), "SELECT name FROM accounts WHERE id = $1", []any{target.ID}, &stagedName))
	require.Equal(t, pending.Name, stagedName)
	assertNoMembership(t, tx.Client(), target.ID, current.ID)
	assertMembershipPriority(t, tx.Client(), target.ID, desired.ID, 50)

	require.NoError(t, tx.Rollback(), "rollback outer transaction to simulate a later failure")

	var persistedName string
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT name FROM accounts WHERE id = $1", target.ID).Scan(&persistedName))
	require.Equal(t, target.Name, persistedName)
	assertMembershipPriority(t, integrationDB, target.ID, current.ID, 23)
	assertNoMembership(t, integrationDB, target.ID, desired.ID)
	var outboxCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT COUNT(*) FROM scheduler_outbox WHERE account_id = $1", target.ID).Scan(&outboxCount))
	require.Zero(t, outboxCount)
}

func sortedRecordIDs(records []service.GroupAccountRepositoryRecord) []int64 {
	ids := make([]int64, 0, len(records))
	for i := range records {
		ids = append(ids, records[i].Account.ID)
	}
	for i := 0; i < len(ids); i++ {
		for j := i + 1; j < len(ids); j++ {
			if ids[j] < ids[i] {
				ids[i], ids[j] = ids[j], ids[i]
			}
		}
	}
	return ids
}

func assertMembershipPriority(t *testing.T, db sqlExecutor, accountID, groupID int64, expected int) {
	t.Helper()
	var priority int
	require.NoError(t, scanSingleRow(context.Background(), db, "SELECT priority FROM account_groups WHERE account_id = $1 AND group_id = $2", []any{accountID, groupID}, &priority))
	require.Equal(t, expected, priority)
}

func assertNoMembership(t *testing.T, db sqlExecutor, accountID, groupID int64) {
	t.Helper()
	var count int
	require.NoError(t, scanSingleRow(context.Background(), db,
		"SELECT COUNT(*) FROM account_groups WHERE account_id = $1 AND group_id = $2",
		[]any{accountID, groupID},
		&count,
	))
	require.Zero(t, count)
}

func sortedAccountIDs(accounts []service.Account) []int64 {
	ids := make([]int64, 0, len(accounts))
	for i := range accounts {
		ids = append(ids, accounts[i].ID)
	}
	return sortedInt64s(ids)
}

func sortedInt64s(ids []int64) []int64 {
	out := append([]int64(nil), ids...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func infraReason(err error) string {
	return infraerrors.Reason(err)
}
