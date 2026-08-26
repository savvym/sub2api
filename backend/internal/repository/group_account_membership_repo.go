package repository

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	dbaccount "github.com/Wei-Shaw/sub2api/ent/account"
	dbaccountgroup "github.com/Wei-Shaw/sub2api/ent/accountgroup"
	dbgroup "github.com/Wei-Shaw/sub2api/ent/group"
	dbpredicate "github.com/Wei-Shaw/sub2api/ent/predicate"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"

	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/sqljson"
)

const maxAccountGroupMembershipReplaceAttempts = 3

var (
	_ service.GroupAccountManagementRepository            = (*accountRepository)(nil)
	_ service.AccountGroupMembershipReplacementRepository = (*accountRepository)(nil)
)

func (r *accountRepository) ListGroupAccountMembers(ctx context.Context, groupID int64, filters service.GroupAccountListFilters) (*service.GroupAccountRepositoryPage, error) {
	base := r.client.Account.Query().Where(
		dbaccount.HasAccountGroupsWith(dbaccountgroup.GroupIDEQ(groupID)),
	)
	return r.listGroupAccountPage(ctx, base, filters)
}

func (r *accountRepository) ListGroupAccountCandidates(
	ctx context.Context,
	groupID int64,
	filters service.GroupAccountListFilters,
	policy service.GroupAccountCandidatePolicy,
) (*service.GroupAccountRepositoryPage, error) {
	base := r.client.Account.Query().Where(
		dbaccount.Not(dbaccount.HasAccountGroupsWith(dbaccountgroup.GroupIDEQ(groupID))),
		groupAccountCandidatePredicate(policy),
	)
	if policy.RequireOAuth {
		base = base.Where(dbaccount.TypeNEQ(service.AccountTypeAPIKey))
	}
	return r.listGroupAccountPage(ctx, base, filters)
}

func (r *accountRepository) listGroupAccountPage(
	ctx context.Context,
	base *dbent.AccountQuery,
	filters service.GroupAccountListFilters,
) (*service.GroupAccountRepositoryPage, error) {
	scopeTotal, err := base.Clone().Count(ctx)
	if err != nil {
		return nil, err
	}
	filtered := applyGroupAccountListFilters(base, filters)
	total, err := filtered.Clone().Count(ctx)
	if err != nil {
		return nil, err
	}
	entities, err := filtered.
		Offset((filters.Page-1)*filters.PageSize).
		Limit(filters.PageSize).
		Order(dbent.Asc(dbaccount.FieldName), dbent.Asc(dbaccount.FieldID)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	accounts, err := r.accountsToService(ctx, entities)
	if err != nil {
		return nil, err
	}
	items := make([]service.GroupAccountRepositoryRecord, 0, len(accounts))
	for i := range accounts {
		items = append(items, service.GroupAccountRepositoryRecord{
			Account:    accounts[i],
			GroupCount: len(accounts[i].GroupIDs),
		})
	}
	pages := int(math.Ceil(float64(total) / float64(filters.PageSize)))
	if pages < 1 {
		pages = 1
	}
	return &service.GroupAccountRepositoryPage{
		Items:      items,
		Total:      int64(total),
		ScopeTotal: int64(scopeTotal),
		Page:       filters.Page,
		PageSize:   filters.PageSize,
		Pages:      pages,
	}, nil
}

func applyGroupAccountListFilters(query *dbent.AccountQuery, filters service.GroupAccountListFilters) *dbent.AccountQuery {
	if filters.Search != "" {
		predicates := []dbpredicate.Account{dbaccount.NameContainsFold(filters.Search)}
		if id, err := strconv.ParseInt(filters.Search, 10, 64); err == nil && id > 0 {
			predicates = append(predicates, dbaccount.IDEQ(id))
		}
		query = query.Where(dbaccount.Or(predicates...))
	}
	if filters.AccountType != "" {
		query = query.Where(dbaccount.TypeEQ(filters.AccountType))
	}
	if filters.Status != "" {
		query = query.Where(dbaccount.StatusEQ(filters.Status))
	}
	if filters.Platform != "" {
		query = query.Where(dbaccount.PlatformEQ(filters.Platform))
	}
	return query
}

func groupAccountCandidatePredicate(policy service.GroupAccountCandidatePolicy) dbpredicate.Account {
	if len(policy.AllowedPlatforms) == 0 {
		return dbaccount.IDLT(0)
	}
	if !policy.RequireMixedSchedulingForAntigravity {
		return dbaccount.PlatformIn(policy.AllowedPlatforms...)
	}
	nonAntigravity := make([]string, 0, len(policy.AllowedPlatforms))
	for _, platform := range policy.AllowedPlatforms {
		if platform != service.PlatformAntigravity {
			nonAntigravity = append(nonAntigravity, platform)
		}
	}
	predicates := make([]dbpredicate.Account, 0, 2)
	if len(nonAntigravity) > 0 {
		predicates = append(predicates, dbaccount.PlatformIn(nonAntigravity...))
	}
	predicates = append(predicates, dbaccount.And(
		dbaccount.PlatformEQ(service.PlatformAntigravity),
		dbpredicate.Account(func(selector *entsql.Selector) {
			selector.Where(sqljson.ValueEQ(dbaccount.FieldExtra, true, sqljson.Path("mixed_scheduling")))
		}),
	))
	return dbaccount.Or(predicates...)
}

// ReplaceAccountGroupMemberships applies the account's complete desired group
// set with incremental writes. Group rows are the serialization point shared
// with group-scoped membership updates, so existing relation metadata is left
// untouched and validation observes a locked final membership snapshot.
func (r *accountRepository) ReplaceAccountGroupMemberships(
	ctx context.Context,
	accountID int64,
	desiredGroupIDs []int64,
	defaultPriority int,
	validate service.AccountGroupMembershipReplacementValidator,
) (*service.AccountGroupMembershipReplacement, error) {
	if accountID <= 0 {
		return nil, service.ErrAccountNotFound
	}
	desiredGroupIDs, err := normalizeDesiredAccountGroupIDs(desiredGroupIDs)
	if err != nil {
		return nil, err
	}

	for attempt := 0; attempt < maxAccountGroupMembershipReplaceAttempts; attempt++ {
		result, retry, err := r.replaceAccountGroupMembershipsAttempt(
			ctx,
			accountID,
			desiredGroupIDs,
			defaultPriority,
			validate,
		)
		if err != nil {
			return nil, err
		}
		if !retry {
			return result, nil
		}
	}

	return nil, infraerrors.Conflict(
		"account_group_membership_concurrent_change",
		"account group memberships changed concurrently; retry the request",
	)
}

func (r *accountRepository) replaceAccountGroupMembershipsAttempt(
	ctx context.Context,
	accountID int64,
	desiredGroupIDs []int64,
	defaultPriority int,
	validate service.AccountGroupMembershipReplacementValidator,
) (*service.AccountGroupMembershipReplacement, bool, error) {
	if contextTx := dbent.TxFromContext(ctx); contextTx != nil {
		txClient := contextTx.Client()
		currentHint, err := loadAccountGroupIDsForReplacement(ctx, txClient, accountID)
		if err != nil {
			return nil, false, err
		}
		result, retry, err := replaceAccountGroupMembershipsInTx(
			ctx,
			txClient,
			accountID,
			desiredGroupIDs,
			mergeSortedUniqueIDs(currentHint, desiredGroupIDs),
			defaultPriority,
			validate,
		)
		if retry && err == nil {
			return nil, false, infraerrors.Conflict(
				"account_group_membership_concurrent_change",
				"account group memberships changed inside an existing transaction; retry the request",
			)
		}
		return result, false, err
	}

	currentHint, err := loadAccountGroupIDsForReplacement(ctx, r.client, accountID)
	if err != nil {
		return nil, false, err
	}
	lockedGroupIDs := mergeSortedUniqueIDs(currentHint, desiredGroupIDs)

	tx, err := r.client.Tx(ctx)
	if err != nil {
		if !errors.Is(err, dbent.ErrTxStarted) {
			return nil, false, err
		}
		result, retry, replaceErr := replaceAccountGroupMembershipsInTx(
			ctx,
			r.client,
			accountID,
			desiredGroupIDs,
			lockedGroupIDs,
			defaultPriority,
			validate,
		)
		if retry && replaceErr == nil {
			return nil, false, infraerrors.Conflict(
				"account_group_membership_concurrent_change",
				"account group memberships changed inside an existing transaction; retry the request",
			)
		}
		return result, false, replaceErr
	}
	defer func() { _ = tx.Rollback() }()

	result, retry, err := replaceAccountGroupMembershipsInTx(
		ctx,
		tx.Client(),
		accountID,
		desiredGroupIDs,
		lockedGroupIDs,
		defaultPriority,
		validate,
	)
	if err != nil || retry {
		return nil, retry, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return result, false, nil
}

func replaceAccountGroupMembershipsInTx(
	ctx context.Context,
	client *dbent.Client,
	accountID int64,
	desiredGroupIDs, lockedGroupIDs []int64,
	defaultPriority int,
	validate service.AccountGroupMembershipReplacementValidator,
) (*service.AccountGroupMembershipReplacement, bool, error) {
	if err := lockGroupsForAccountMembership(ctx, client, lockedGroupIDs); err != nil {
		return nil, false, err
	}

	groupsByID := make(map[int64]service.Group, len(lockedGroupIDs))
	if len(lockedGroupIDs) > 0 {
		groupEntities, err := client.Group.Query().Where(dbgroup.IDIn(lockedGroupIDs...)).All(ctx)
		if err != nil {
			return nil, false, err
		}
		for _, entity := range groupEntities {
			group := groupEntityToService(entity)
			if group != nil {
				groupsByID[group.ID] = *group
			}
		}
	}
	for _, groupID := range desiredGroupIDs {
		if _, exists := groupsByID[groupID]; !exists {
			return nil, false, service.ErrGroupNotFound
		}
	}

	memberIDsByGroup, memberIDs, err := lockAccountMembershipRowsForGroups(ctx, client, lockedGroupIDs)
	if err != nil {
		return nil, false, err
	}
	accountIDsToLock := mergeSortedUniqueIDs(memberIDs, []int64{accountID})
	if err := lockAccountsForGroupMembership(ctx, client, accountIDsToLock); err != nil {
		return nil, false, err
	}

	accountEntities, err := client.Account.Query().Where(dbaccount.IDIn(accountIDsToLock...)).All(ctx)
	if err != nil {
		return nil, false, err
	}
	accountsByID := make(map[int64]service.Account, len(accountEntities))
	for _, entity := range accountEntities {
		account := accountEntityToService(entity)
		if account != nil {
			accountsByID[account.ID] = *account
		}
	}
	target, exists := accountsByID[accountID]
	if !exists {
		return nil, false, service.ErrAccountNotFound
	}

	currentGroupIDs, err := loadAccountGroupIDsForReplacement(ctx, client, accountID)
	if err != nil {
		return nil, false, err
	}
	if containsIDOutsideSet(currentGroupIDs, lockedGroupIDs) {
		// A group-scoped writer committed after the initial hint was read. Do not
		// acquire that new group lock while holding account locks; restarting keeps
		// the global group -> relation -> account order intact.
		return nil, true, nil
	}

	addedGroupIDs, removedGroupIDs := diffSortedIDs(currentGroupIDs, desiredGroupIDs)
	currentAccountsByGroup := make(map[int64][]service.Account, len(lockedGroupIDs))
	finalAccountsByGroup := make(map[int64][]service.Account, len(lockedGroupIDs))
	desiredSet := int64Set(desiredGroupIDs)
	for _, groupID := range lockedGroupIDs {
		currentAccounts := make([]service.Account, 0, len(memberIDsByGroup[groupID]))
		finalAccounts := make([]service.Account, 0, len(memberIDsByGroup[groupID])+1)
		targetPresent := false
		for _, memberID := range memberIDsByGroup[groupID] {
			account, ok := accountsByID[memberID]
			if !ok {
				continue
			}
			currentAccounts = append(currentAccounts, account)
			if memberID == accountID {
				targetPresent = true
				if _, keep := desiredSet[groupID]; !keep {
					continue
				}
			}
			finalAccounts = append(finalAccounts, account)
		}
		if _, shouldBelong := desiredSet[groupID]; shouldBelong && !targetPresent {
			finalAccounts = append(finalAccounts, target)
		}
		sort.Slice(currentAccounts, func(i, j int) bool { return currentAccounts[i].ID < currentAccounts[j].ID })
		sort.Slice(finalAccounts, func(i, j int) bool { return finalAccounts[i].ID < finalAccounts[j].ID })
		currentAccountsByGroup[groupID] = currentAccounts
		finalAccountsByGroup[groupID] = finalAccounts
	}

	if validate != nil {
		if err := validate(service.AccountGroupMembershipReplacementSnapshot{
			Account:                target,
			GroupsByID:             groupsByID,
			CurrentGroupIDs:        append([]int64(nil), currentGroupIDs...),
			DesiredGroupIDs:        append([]int64(nil), desiredGroupIDs...),
			AddedGroupIDs:          append([]int64(nil), addedGroupIDs...),
			RemovedGroupIDs:        append([]int64(nil), removedGroupIDs...),
			CurrentAccountsByGroup: currentAccountsByGroup,
			FinalAccountsByGroup:   finalAccountsByGroup,
		}); err != nil {
			return nil, false, err
		}
	}

	if len(removedGroupIDs) > 0 {
		if _, err := client.ExecContext(ctx,
			"DELETE FROM account_groups WHERE account_id = $1 AND group_id = ANY($2::bigint[])",
			accountID,
			pq.Array(removedGroupIDs),
		); err != nil {
			return nil, false, err
		}
	}
	if len(addedGroupIDs) > 0 {
		if _, err := client.ExecContext(ctx, `
			INSERT INTO account_groups (account_id, group_id, priority, created_at)
			SELECT $1, unnest($2::bigint[]), $3, NOW()
			ON CONFLICT (account_id, group_id) DO NOTHING`, accountID, pq.Array(addedGroupIDs), defaultPriority); err != nil {
			return nil, false, err
		}
	}
	if len(addedGroupIDs)+len(removedGroupIDs) > 0 {
		payload := buildSchedulerGroupPayload(mergeSortedUniqueIDs(currentGroupIDs, desiredGroupIDs))
		if err := enqueueSchedulerOutbox(ctx, client, service.SchedulerOutboxEventAccountGroupsChanged, &accountID, nil, payload); err != nil {
			return nil, false, err
		}
	}

	return &service.AccountGroupMembershipReplacement{
		CurrentGroupIDs: append([]int64(nil), currentGroupIDs...),
		DesiredGroupIDs: append([]int64(nil), desiredGroupIDs...),
		AddedGroupIDs:   append([]int64(nil), addedGroupIDs...),
		RemovedGroupIDs: append([]int64(nil), removedGroupIDs...),
	}, false, nil
}

func (r *accountRepository) ApplyGroupAccountMembershipDiff(
	ctx context.Context,
	groupID int64,
	addAccountIDs, removeAccountIDs []int64,
	priority int,
	validate service.GroupAccountMembershipValidator,
) (*service.GroupAccountMembershipMutation, error) {
	txClient := r.client
	var tx *dbent.Tx
	if contextTx := dbent.TxFromContext(ctx); contextTx != nil {
		txClient = contextTx.Client()
	} else {
		var err error
		tx, err = r.client.Tx(ctx)
		if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
			return nil, err
		}
		if tx != nil {
			defer func() { _ = tx.Rollback() }()
			txClient = tx.Client()
		}
	}

	if err := lockGroupForAccountMembership(ctx, txClient, groupID); err != nil {
		return nil, err
	}
	groupEntity, err := txClient.Group.Query().Where(dbgroup.IDEQ(groupID)).Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrGroupNotFound, nil)
	}
	group := groupEntityToService(groupEntity)

	currentIDs, err := lockGroupAccountMembershipRows(ctx, txClient, groupID)
	if err != nil {
		return nil, err
	}
	requestedIDs := mergeSortedUniqueIDs(addAccountIDs, removeAccountIDs)
	lockIDs := mergeSortedUniqueIDs(currentIDs, requestedIDs)
	if err := lockAccountsForGroupMembership(ctx, txClient, lockIDs); err != nil {
		return nil, err
	}

	accountEntities, err := txClient.Account.Query().Where(dbaccount.IDIn(lockIDs...)).All(ctx)
	if err != nil {
		return nil, err
	}
	accountsByID := make(map[int64]service.Account, len(accountEntities))
	for _, entity := range accountEntities {
		account := accountEntityToService(entity)
		if account != nil {
			accountsByID[account.ID] = *account
		}
	}
	missingIDs := make([]int64, 0)
	for _, id := range requestedIDs {
		if _, exists := accountsByID[id]; !exists {
			missingIDs = append(missingIDs, id)
		}
	}
	if len(missingIDs) > 0 {
		return nil, infraerrors.NotFound("account_not_found", "one or more accounts do not exist").WithMetadata(map[string]string{
			"account_ids": joinInt64s(missingIDs),
		})
	}

	currentSet := make(map[int64]struct{}, len(currentIDs))
	currentAccounts := make([]service.Account, 0, len(currentIDs))
	for _, id := range currentIDs {
		account, exists := accountsByID[id]
		if !exists {
			continue
		}
		currentSet[id] = struct{}{}
		currentAccounts = append(currentAccounts, account)
	}
	addedIDs := make([]int64, 0, len(addAccountIDs))
	alreadyMemberIDs := make([]int64, 0)
	for _, id := range addAccountIDs {
		if _, exists := currentSet[id]; exists {
			alreadyMemberIDs = append(alreadyMemberIDs, id)
			continue
		}
		addedIDs = append(addedIDs, id)
	}
	removedIDs := make([]int64, 0, len(removeAccountIDs))
	notMemberIDs := make([]int64, 0)
	for _, id := range removeAccountIDs {
		if _, exists := currentSet[id]; !exists {
			notMemberIDs = append(notMemberIDs, id)
			continue
		}
		removedIDs = append(removedIDs, id)
	}

	finalSet := make(map[int64]service.Account, len(currentAccounts)+len(addedIDs))
	for i := range currentAccounts {
		finalSet[currentAccounts[i].ID] = currentAccounts[i]
	}
	for _, id := range removedIDs {
		delete(finalSet, id)
	}
	for _, id := range addedIDs {
		finalSet[id] = accountsByID[id]
	}
	finalIDs := make([]int64, 0, len(finalSet))
	for id := range finalSet {
		finalIDs = append(finalIDs, id)
	}
	sort.Slice(finalIDs, func(i, j int) bool { return finalIDs[i] < finalIDs[j] })
	finalAccounts := make([]service.Account, 0, len(finalIDs))
	for _, id := range finalIDs {
		finalAccounts = append(finalAccounts, finalSet[id])
	}
	if validate != nil {
		if err := validate(service.GroupAccountMembershipSnapshot{
			Group:             *group,
			CurrentAccounts:   currentAccounts,
			FinalAccounts:     finalAccounts,
			AddedAccountIDs:   addedIDs,
			RemovedAccountIDs: removedIDs,
			AlreadyMemberIDs:  alreadyMemberIDs,
			NotMemberIDs:      notMemberIDs,
		}); err != nil {
			return nil, err
		}
	}

	if len(removedIDs) > 0 {
		if _, err := txClient.ExecContext(ctx,
			"DELETE FROM account_groups WHERE group_id = $1 AND account_id = ANY($2::bigint[])",
			groupID,
			pq.Array(removedIDs),
		); err != nil {
			return nil, err
		}
	}
	if len(addedIDs) > 0 {
		if _, err := txClient.ExecContext(ctx, `
			INSERT INTO account_groups (account_id, group_id, priority, created_at)
			SELECT unnest($1::bigint[]), $2, $3, NOW()
			ON CONFLICT (account_id, group_id) DO NOTHING`, pq.Array(addedIDs), groupID, priority); err != nil {
			return nil, err
		}
	}
	if len(addedIDs)+len(removedIDs) > 0 {
		if err := enqueueSchedulerOutbox(ctx, txClient, service.SchedulerOutboxEventGroupChanged, nil, &groupID, nil); err != nil {
			return nil, err
		}
	}
	stats, err := readGroupAccountMembershipStats(ctx, txClient, groupID)
	if err != nil {
		return nil, err
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
	}
	return &service.GroupAccountMembershipMutation{
		AddedAccountIDs:         addedIDs,
		RemovedAccountIDs:       removedIDs,
		AlreadyMemberAccountIDs: alreadyMemberIDs,
		NotMemberAccountIDs:     notMemberIDs,
		AccountCount:            stats.Total,
		ActiveAccountCount:      stats.Active,
		RateLimitedAccountCount: stats.RateLimited,
	}, nil
}

func lockGroupForAccountMembership(ctx context.Context, client *dbent.Client, groupID int64) error {
	rows, err := client.QueryContext(ctx, "SELECT id FROM groups WHERE id = $1 AND deleted_at IS NULL FOR UPDATE", groupID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return service.ErrGroupNotFound
	}
	var lockedID int64
	return rows.Scan(&lockedID)
}

func lockGroupsForAccountMembership(ctx context.Context, client *dbent.Client, groupIDs []int64) error {
	if len(groupIDs) == 0 {
		return nil
	}
	// Do not filter soft-deleted rows here. A stale relation to a soft-deleted
	// group must still be removable; desired groups are checked through the
	// soft-delete-aware Ent query after all raw rows have been locked.
	rows, err := client.QueryContext(ctx,
		"SELECT id FROM groups WHERE id = ANY($1::bigint[]) ORDER BY id FOR UPDATE",
		pq.Array(groupIDs),
	)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
	}
	return rows.Err()
}

func lockGroupAccountMembershipRows(ctx context.Context, client *dbent.Client, groupID int64) ([]int64, error) {
	rows, err := client.QueryContext(ctx, "SELECT account_id FROM account_groups WHERE group_id = $1 ORDER BY account_id FOR UPDATE", groupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func lockAccountMembershipRowsForGroups(
	ctx context.Context,
	client *dbent.Client,
	groupIDs []int64,
) (map[int64][]int64, []int64, error) {
	byGroup := make(map[int64][]int64, len(groupIDs))
	if len(groupIDs) == 0 {
		return byGroup, nil, nil
	}
	rows, err := client.QueryContext(ctx, `
		SELECT group_id, account_id
		FROM account_groups
		WHERE group_id = ANY($1::bigint[])
		ORDER BY group_id, account_id
		FOR UPDATE`, pq.Array(groupIDs))
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = rows.Close() }()
	allAccountIDs := make([]int64, 0)
	for rows.Next() {
		var groupID, accountID int64
		if err := rows.Scan(&groupID, &accountID); err != nil {
			return nil, nil, err
		}
		byGroup[groupID] = append(byGroup[groupID], accountID)
		allAccountIDs = append(allAccountIDs, accountID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return byGroup, allAccountIDs, nil
}

func lockAccountsForGroupMembership(ctx context.Context, client *dbent.Client, accountIDs []int64) error {
	if len(accountIDs) == 0 {
		return nil
	}
	rows, err := client.QueryContext(ctx, "SELECT id FROM accounts WHERE id = ANY($1::bigint[]) ORDER BY id FOR UPDATE", pq.Array(accountIDs))
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return err
		}
	}
	return rows.Err()
}

func readGroupAccountMembershipStats(ctx context.Context, client *dbent.Client, groupID int64) (groupAccountCounts, error) {
	var result groupAccountCounts
	err := scanSingleRow(ctx, client,
		fmt.Sprintf(`SELECT
			COUNT(*) FILTER (WHERE a.deleted_at IS NULL),
			COUNT(*) FILTER (WHERE %s),
			COUNT(*) FILTER (WHERE %s)
		FROM account_groups ag
		JOIN accounts a ON a.id = ag.account_id
		WHERE ag.group_id = $1`, groupAccountAvailableSQL, groupAccountTemporarilyLimitedSQL),
		[]any{groupID},
		&result.Total,
		&result.Active,
		&result.RateLimited,
	)
	return result, err
}

func normalizeDesiredAccountGroupIDs(groupIDs []int64) ([]int64, error) {
	for _, groupID := range groupIDs {
		if groupID <= 0 {
			return nil, infraerrors.BadRequest("invalid_group_id", "group IDs must be positive")
		}
	}
	return mergeSortedUniqueIDs(groupIDs), nil
}

func loadAccountGroupIDsForReplacement(ctx context.Context, client *dbent.Client, accountID int64) ([]int64, error) {
	rows, err := client.QueryContext(ctx,
		"SELECT group_id FROM account_groups WHERE account_id = $1 ORDER BY group_id",
		accountID,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	groupIDs := make([]int64, 0)
	for rows.Next() {
		var groupID int64
		if err := rows.Scan(&groupID); err != nil {
			return nil, err
		}
		groupIDs = append(groupIDs, groupID)
	}
	return groupIDs, rows.Err()
}

func containsIDOutsideSet(ids, allowed []int64) bool {
	allowedSet := int64Set(allowed)
	for _, id := range ids {
		if _, exists := allowedSet[id]; !exists {
			return true
		}
	}
	return false
}

func diffSortedIDs(current, desired []int64) (added, removed []int64) {
	currentSet := int64Set(current)
	desiredSet := int64Set(desired)
	for _, id := range desired {
		if _, exists := currentSet[id]; !exists {
			added = append(added, id)
		}
	}
	for _, id := range current {
		if _, exists := desiredSet[id]; !exists {
			removed = append(removed, id)
		}
	}
	return added, removed
}

func int64Set(ids []int64) map[int64]struct{} {
	set := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		set[id] = struct{}{}
	}
	return set
}

func mergeSortedUniqueIDs(groups ...[]int64) []int64 {
	seen := make(map[int64]struct{})
	for _, ids := range groups {
		for _, id := range ids {
			if id > 0 {
				seen[id] = struct{}{}
			}
		}
	}
	out := make([]int64, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func joinInt64s(ids []int64) string {
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	return strings.Join(parts, ",")
}
