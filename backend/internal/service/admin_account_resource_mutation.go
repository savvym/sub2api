package service

import (
	"context"
	"maps"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/authz"
)

func (s *adminServiceImpl) CreateAccount(
	ctx context.Context,
	actor authz.Actor,
	input *CreateAccountInput,
) (*Account, error) {
	if s == nil || s.resourceMutations == nil {
		return s.createAccountInResourceTx(ctx, actor, input)
	}
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	if input == nil {
		return nil, ErrAccountNilInput
	}
	work := cloneCreateAccountInput(input)
	groupIDs, err := s.resolveCreateAccountGroupIDs(ctx, &work)
	if err != nil {
		return nil, err
	}
	work.GroupIDs = groupIDs
	work.SkipDefaultGroupBind = true
	groupTargets, err := s.groupResourceMutationTargets(
		ctx, groupIDs, authz.ActionGroupEdit, true, "group.account_links_changed", []string{"account_groups"},
	)
	if err != nil {
		return nil, err
	}

	var created *Account
	err = s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{
		CreateResourceTypes: []authz.ResourceType{authz.ResourceTypeAccount},
		Targets:             groupTargets,
	}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		var createErr error
		created, createErr = s.createAccountInResourceTx(txCtx, actor, &work)
		if createErr != nil {
			return nil, createErr
		}
		return []CreatedResourceMutation{createdAccountMutation(
			created, "account.created", []string{"configuration", "credentials", "account_groups"},
		)}, nil
	})
	return created, err
}

func (s *adminServiceImpl) DuplicateAccount(
	ctx context.Context,
	actor authz.Actor,
	id int64,
	operationKey string,
) (*Account, error) {
	if s == nil || s.resourceMutations == nil {
		return s.duplicateAccountInResourceTx(ctx, actor, id, operationKey)
	}
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	if existing, err := s.RecoverDuplicateAccount(ctx, actor, id, operationKey); err != nil || existing != nil {
		return existing, err
	}
	source, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	targets := []ResourceMutationTarget{accountResourceMutationTarget(
		source, authz.ActionAccountView, false, "", nil,
	)}
	groupTargets, err := s.groupResourceMutationTargets(
		ctx, source.GroupIDs, authz.ActionGroupEdit, true, "group.account_links_changed", []string{"account_groups"},
	)
	if err != nil {
		return nil, err
	}
	targets = append(targets, groupTargets...)

	var duplicate *Account
	err = s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{
		CreateResourceTypes: []authz.ResourceType{authz.ResourceTypeAccount},
		Targets:             targets,
	}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		if existing, recoverErr := s.RecoverDuplicateAccount(txCtx, actor, id, operationKey); recoverErr != nil {
			return nil, recoverErr
		} else if existing != nil {
			duplicate = existing
			return nil, errResourceMutationNoop
		}
		var duplicateErr error
		duplicate, duplicateErr = s.duplicateAccountInResourceTx(txCtx, actor, id, operationKey)
		if duplicateErr != nil {
			return nil, duplicateErr
		}
		return []CreatedResourceMutation{createdAccountMutation(
			duplicate, "account.duplicated", []string{"configuration", "credentials", "account_groups"},
		)}, nil
	})
	return duplicate, err
}

func (s *adminServiceImpl) UpdateAccount(
	ctx context.Context,
	actor authz.Actor,
	id int64,
	input *UpdateAccountInput,
) (*Account, error) {
	if s == nil || s.resourceMutations == nil {
		return s.updateAccountInResourceTx(ctx, actor, id, input)
	}
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	targets := []ResourceMutationTarget{accountResourceMutationTarget(
		account, authz.ActionAccountEdit, true, "account.updated", accountUpdateFieldCategories(input),
	)}
	var authorizedShadowIDs []int64
	if input != nil && input.ProxyID != nil {
		shadows, err := s.accountRepo.ListShadowsByParent(ctx, id)
		if err != nil {
			return nil, err
		}
		authorizedShadowIDs = resourceMutationShadowIDs(shadows)
		for _, shadow := range shadows {
			targets = append(targets, accountResourceMutationTarget(
				shadow, authz.ActionAccountEdit, true, "account.proxy_inherited", []string{"proxy"},
			))
		}
	}
	if input != nil && input.GroupIDs != nil {
		groupIDs := append([]int64(nil), account.GroupIDs...)
		groupIDs = append(groupIDs, (*input.GroupIDs)...)
		groupTargets, err := s.groupResourceMutationTargets(
			ctx, groupIDs, authz.ActionGroupEdit, true, "group.account_links_changed", []string{"account_groups"},
		)
		if err != nil {
			return nil, err
		}
		targets = append(targets, groupTargets...)
	}

	var updated *Account
	err = s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{Targets: targets}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		if input != nil && input.ProxyID != nil {
			if validateErr := s.validateResourceMutationShadowSet(txCtx, id, authorizedShadowIDs); validateErr != nil {
				return nil, validateErr
			}
		}
		var updateErr error
		updated, updateErr = s.updateAccountInResourceTx(txCtx, actor, id, input)
		return nil, updateErr
	})
	if err == nil && updated != nil {
		updated.AccessVersion = account.AccessVersion + 1
	}
	return updated, err
}

func (s *adminServiceImpl) UpdateAccountExtra(
	ctx context.Context,
	actor authz.Actor,
	id int64,
	updates map[string]any,
) error {
	if s == nil || s.resourceMutations == nil {
		return s.updateAccountExtraInResourceTx(ctx, actor, id, updates)
	}
	work := sanitizedCodexFingerprintExtraUpdates(updates)
	delete(work, UpstreamBillingProbeEnabledExtraKey)
	delete(work, UpstreamBillingRateSyncEnabledExtraKey)
	delete(work, UpstreamBillingProbeExtraKey)
	delete(work, OllamaCloudUsageSessionExtraKey)
	delete(work, OllamaCloudUsageAutoRefreshExtraKey)
	delete(work, OllamaCloudUsageSnapshotExtraKey)
	return s.executeAccountResourceMutation(
		ctx, actor, id, authz.ActionAccountEdit, "account.extra_updated", []string{"extra"},
		func(txCtx context.Context) error {
			if len(work) == 0 {
				return errResourceMutationNoop
			}
			return s.updateAccountExtraInResourceTx(txCtx, actor, id, work)
		},
	)
}

func (s *adminServiceImpl) BulkUpdateAccounts(
	ctx context.Context,
	actor authz.Actor,
	input *BulkUpdateAccountsInput,
) (*BulkUpdateAccountsResult, error) {
	if s == nil || s.resourceMutations == nil {
		return s.bulkUpdateAccountsInResourceTx(ctx, actor, input)
	}
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	if input == nil {
		return nil, ErrAccountNilInput
	}
	work := cloneBulkUpdateAccountsInput(input)
	if len(work.AccountIDs) == 0 && work.Filters != nil {
		ids, err := s.resolveBulkUpdateTargetIDs(ctx, actor, work.Filters)
		if err != nil {
			return nil, err
		}
		work.AccountIDs = ids
	}
	work.AccountIDs = uniqueSortedResourceIDs(work.AccountIDs)
	accounts, err := s.loadAccountsForResourceMutation(ctx, work.AccountIDs)
	if err != nil {
		return nil, err
	}
	targets := make([]ResourceMutationTarget, 0, len(accounts))
	groupIDs := make([]int64, 0)
	authorizedShadowIDsByParent := make(map[int64][]int64)
	for _, account := range accounts {
		targets = append(targets, accountResourceMutationTarget(
			account, authz.ActionAccountEdit, true, "account.bulk_updated", []string{"configuration", "credentials", "extra", "account_groups"},
		))
		if work.GroupIDs != nil {
			groupIDs = append(groupIDs, account.GroupIDs...)
		}
		if work.ProxyID != nil {
			shadows, err := s.accountRepo.ListShadowsByParent(ctx, account.ID)
			if err != nil {
				return nil, err
			}
			authorizedShadowIDsByParent[account.ID] = resourceMutationShadowIDs(shadows)
			for _, shadow := range shadows {
				targets = append(targets, accountResourceMutationTarget(
					shadow, authz.ActionAccountEdit, true, "account.proxy_inherited", []string{"proxy"},
				))
			}
		}
	}
	if work.GroupIDs != nil {
		groupIDs = append(groupIDs, (*work.GroupIDs)...)
		groupTargets, err := s.groupResourceMutationTargets(
			ctx, groupIDs, authz.ActionGroupEdit, true, "group.account_links_changed", []string{"account_groups"},
		)
		if err != nil {
			return nil, err
		}
		targets = append(targets, groupTargets...)
	}

	var result *BulkUpdateAccountsResult
	err = s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{Targets: targets}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		if work.ProxyID != nil {
			if validateErr := s.validateResourceMutationShadowSets(txCtx, authorizedShadowIDsByParent); validateErr != nil {
				return nil, validateErr
			}
		}
		var updateErr error
		result, updateErr = s.bulkUpdateAccountsInResourceTx(txCtx, actor, &work)
		return nil, updateErr
	})
	return result, err
}

func (s *adminServiceImpl) DeleteAccount(ctx context.Context, actor authz.Actor, id int64) error {
	if s == nil || s.resourceMutations == nil {
		return s.deleteAccountInResourceTx(ctx, actor, id)
	}
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	accounts := []*Account{account}
	shadows, err := s.accountRepo.ListShadowsByParent(ctx, id)
	if err != nil {
		return err
	}
	authorizedShadowIDs := resourceMutationShadowIDs(shadows)
	accounts = append(accounts, shadows...)
	targets := make([]ResourceMutationTarget, 0, len(accounts))
	groupIDs := make([]int64, 0)
	for _, item := range accounts {
		targets = append(targets, accountResourceMutationTarget(
			item, authz.ActionAccountDelete, true, "account.deleted", []string{"lifecycle", "account_groups"},
		))
		groupIDs = append(groupIDs, item.GroupIDs...)
	}
	groupTargets, err := s.groupResourceMutationTargets(
		ctx, groupIDs, authz.ActionGroupEdit, true, "group.account_links_changed", []string{"account_groups"},
	)
	if err != nil {
		return err
	}
	targets = append(targets, groupTargets...)
	return s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{Targets: targets}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		if validateErr := s.validateResourceMutationShadowSet(txCtx, id, authorizedShadowIDs); validateErr != nil {
			return nil, validateErr
		}
		return nil, s.deleteAccountInResourceTx(txCtx, actor, id)
	})
}

func (s *adminServiceImpl) BatchDeleteAccounts(ctx context.Context, actor authz.Actor, ids []int64) error {
	if s == nil || s.resourceMutations == nil {
		return ErrResourceMutationUnavailable
	}
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}

	ids = uniqueSortedResourceIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	selected, err := s.loadAccountsForResourceMutation(ctx, ids)
	if err != nil {
		return err
	}
	rootIDs := batchDeleteAccountRootIDs(selected)

	accountsByID := make(map[int64]*Account, len(selected))
	for _, account := range selected {
		accountsByID[account.ID] = account
	}
	authorizedShadowIDsByParent := make(map[int64][]int64, len(rootIDs))
	for _, rootID := range rootIDs {
		shadows, err := s.accountRepo.ListShadowsByParent(ctx, rootID)
		if err != nil {
			return err
		}
		authorizedShadowIDsByParent[rootID] = resourceMutationShadowIDs(shadows)
		for _, shadow := range shadows {
			if shadow != nil {
				accountsByID[shadow.ID] = shadow
			}
		}
	}

	accountIDs := make([]int64, 0, len(accountsByID))
	for accountID := range accountsByID {
		accountIDs = append(accountIDs, accountID)
	}
	sort.Slice(accountIDs, func(i, j int) bool { return accountIDs[i] < accountIDs[j] })
	targets := make([]ResourceMutationTarget, 0, len(accountIDs))
	groupIDs := make([]int64, 0)
	for _, accountID := range accountIDs {
		account := accountsByID[accountID]
		targets = append(targets, accountResourceMutationTarget(
			account, authz.ActionAccountDelete, true, "account.deleted", []string{"lifecycle", "account_groups"},
		))
		groupIDs = append(groupIDs, account.GroupIDs...)
	}
	groupTargets, err := s.groupResourceMutationTargets(
		ctx, groupIDs, authz.ActionGroupEdit, true, "group.account_links_changed", []string{"account_groups"},
	)
	if err != nil {
		return err
	}
	targets = append(targets, groupTargets...)

	return s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{Targets: targets}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		if validateErr := s.validateResourceMutationShadowSets(txCtx, authorizedShadowIDsByParent); validateErr != nil {
			return nil, validateErr
		}
		for _, rootID := range rootIDs {
			if err := s.deleteAccountInResourceTx(txCtx, actor, rootID); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
}

func (s *adminServiceImpl) ClearAccountError(ctx context.Context, actor authz.Actor, id int64) (*Account, error) {
	if s == nil || s.resourceMutations == nil {
		return s.clearAccountErrorInResourceTx(ctx, actor, id)
	}
	var account *Account
	err := s.executeAccountResourceMutation(
		ctx, actor, id, authz.ActionAccountOperate, "account.runtime_state_cleared", []string{"runtime_state"},
		func(txCtx context.Context) error {
			var mutationErr error
			account, mutationErr = s.clearAccountErrorInResourceTx(txCtx, actor, id)
			return mutationErr
		},
	)
	if err == nil && account != nil {
		account.AccessVersion++
	}
	return account, err
}

func (s *adminServiceImpl) SetAccountError(ctx context.Context, actor authz.Actor, id int64, errorMsg string) error {
	if s == nil || s.resourceMutations == nil {
		return s.setAccountErrorInResourceTx(ctx, actor, id, errorMsg)
	}
	return s.executeAccountResourceMutation(
		ctx, actor, id, authz.ActionAccountOperate, "account.runtime_state_updated", []string{"runtime_state"},
		func(txCtx context.Context) error { return s.setAccountErrorInResourceTx(txCtx, actor, id, errorMsg) },
	)
}

func (s *adminServiceImpl) SetAccountSchedulable(ctx context.Context, actor authz.Actor, id int64, schedulable bool) (*Account, error) {
	if s == nil || s.resourceMutations == nil {
		return s.setAccountSchedulableInResourceTx(ctx, actor, id, schedulable)
	}
	var account *Account
	err := s.executeAccountResourceMutation(
		ctx, actor, id, authz.ActionAccountOperate, "account.schedulable_updated", []string{"schedulable"},
		func(txCtx context.Context) error {
			var mutationErr error
			account, mutationErr = s.setAccountSchedulableInResourceTx(txCtx, actor, id, schedulable)
			return mutationErr
		},
	)
	if err == nil && account != nil {
		account.AccessVersion++
	}
	return account, err
}

func (s *adminServiceImpl) RevertAccountProxyFallback(ctx context.Context, actor authz.Actor, id int64) error {
	if s == nil || s.resourceMutations == nil {
		return s.revertAccountProxyFallbackInResourceTx(ctx, actor, id)
	}
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	targets := []ResourceMutationTarget{accountResourceMutationTarget(
		account, authz.ActionAccountOperate, true, "account.proxy_fallback_reverted", []string{"proxy"},
	)}
	shadows, err := s.accountRepo.ListShadowsByParent(ctx, id)
	if err != nil {
		return err
	}
	authorizedShadowIDs := resourceMutationShadowIDs(shadows)
	for _, shadow := range shadows {
		targets = append(targets, accountResourceMutationTarget(
			shadow, authz.ActionAccountEdit, true, "account.proxy_inherited", []string{"proxy"},
		))
	}
	return s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{Targets: targets}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		if validateErr := s.validateResourceMutationShadowSet(txCtx, id, authorizedShadowIDs); validateErr != nil {
			return nil, validateErr
		}
		return nil, s.revertAccountProxyFallbackInResourceTx(txCtx, actor, id)
	})
}

func (s *adminServiceImpl) CreateShadow(
	ctx context.Context,
	actor authz.Actor,
	parentID int64,
	opts ShadowOptions,
) (*Account, error) {
	if s == nil || s.resourceMutations == nil {
		return s.createShadowInResourceTx(ctx, actor, parentID, opts)
	}
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	parent, err := s.accountRepo.GetByID(ctx, parentID)
	if err != nil {
		return nil, err
	}
	work := opts
	work.GroupIDs, err = s.resolveShadowGroupIDs(ctx, parent, opts.GroupIDs)
	if err != nil {
		return nil, err
	}
	targets := []ResourceMutationTarget{accountResourceMutationTarget(
		parent, authz.ActionAccountView, true, "account.shadow_links_changed", []string{"shadow_accounts"},
	)}
	groupTargets, err := s.groupResourceMutationTargets(
		ctx, work.GroupIDs, authz.ActionGroupEdit, true, "group.account_links_changed", []string{"account_groups"},
	)
	if err != nil {
		return nil, err
	}
	targets = append(targets, groupTargets...)
	var shadow *Account
	err = s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{
		CreateResourceTypes: []authz.ResourceType{authz.ResourceTypeAccount},
		Targets:             targets,
	}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		var createErr error
		shadow, createErr = s.createShadowInResourceTx(txCtx, actor, parentID, work)
		if createErr != nil {
			return nil, createErr
		}
		return []CreatedResourceMutation{createdAccountMutation(
			shadow, "account.shadow_created", []string{"configuration", "parent_account", "account_groups"},
		)}, nil
	})
	return shadow, err
}

func (s *adminServiceImpl) ResetAccountQuota(ctx context.Context, actor authz.Actor, id int64) error {
	if s == nil || s.resourceMutations == nil {
		return s.resetAccountQuotaInResourceTx(ctx, actor, id)
	}
	return s.executeAccountResourceMutation(
		ctx, actor, id, authz.ActionAccountOperate, "account.quota_reset", []string{"quota"},
		func(txCtx context.Context) error { return s.resetAccountQuotaInResourceTx(txCtx, actor, id) },
	)
}

func (s *adminServiceImpl) executeAccountResourceMutation(
	ctx context.Context,
	actor authz.Actor,
	id int64,
	action authz.Action,
	eventType string,
	changedFields []string,
	mutate func(context.Context) error,
) error {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	account, err := s.accountRepo.GetByID(ctx, id)
	if err != nil {
		return err
	}
	target := accountResourceMutationTarget(account, action, true, eventType, changedFields)
	return s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{
		Targets: []ResourceMutationTarget{target},
	}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		return nil, mutate(txCtx)
	})
}

func (s *adminServiceImpl) resolveCreateAccountGroupIDs(ctx context.Context, input *CreateAccountInput) ([]int64, error) {
	groupIDs := uniqueSortedResourceIDs(input.GroupIDs)
	if len(groupIDs) != 0 || input.SkipDefaultGroupBind {
		return groupIDs, nil
	}
	groups, err := s.groupRepo.ListActiveByPlatform(ctx, input.Platform)
	if err != nil {
		// Preserve the existing create behavior: failure to find the optional
		// default group does not reject an otherwise valid platform account.
		return nil, nil
	}
	defaultName := input.Platform + "-default"
	for index := range groups {
		if groups[index].Name == defaultName {
			return []int64{groups[index].ID}, nil
		}
	}
	return nil, nil
}

func (s *adminServiceImpl) resolveShadowGroupIDs(
	ctx context.Context,
	parent *Account,
	requested []int64,
) ([]int64, error) {
	if len(requested) > 0 {
		return uniqueSortedResourceIDs(requested), nil
	}
	if parent != nil && len(parent.GroupIDs) > 0 {
		return uniqueSortedResourceIDs(parent.GroupIDs), nil
	}
	groups, err := s.groupRepo.ListActiveByPlatform(ctx, PlatformOpenAI)
	if err != nil {
		return nil, nil
	}
	for index := range groups {
		if groups[index].Name == PlatformOpenAI+"-default" {
			return []int64{groups[index].ID}, nil
		}
	}
	return nil, nil
}

func (s *adminServiceImpl) groupResourceMutationTargets(
	ctx context.Context,
	ids []int64,
	action authz.Action,
	mutates bool,
	eventType string,
	changedFields []string,
) ([]ResourceMutationTarget, error) {
	ids = uniqueSortedResourceIDs(ids)
	targets := make([]ResourceMutationTarget, 0, len(ids))
	for _, id := range ids {
		group, err := s.groupRepo.GetByIDLite(ctx, id)
		if err != nil {
			return nil, err
		}
		if group.AccessVersion <= 0 {
			return nil, ErrResourceMutationUnavailable
		}
		ref, err := authz.NewResourceRef(authz.ResourceTypeGroup, group.ID)
		if err != nil {
			return nil, ErrResourceMutationUnavailable.WithCause(err)
		}
		targets = append(targets, ResourceMutationTarget{
			Ref:                   ref,
			Action:                action,
			ExpectedAccessVersion: group.AccessVersion,
			Mutates:               mutates,
			EventType:             eventType,
			ChangedFields:         append([]string(nil), changedFields...),
		})
	}
	return targets, nil
}

func (s *adminServiceImpl) loadAccountsForResourceMutation(ctx context.Context, ids []int64) ([]*Account, error) {
	ids = uniqueSortedResourceIDs(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	accounts, err := s.accountRepo.GetByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*Account, len(accounts))
	for _, account := range accounts {
		if account != nil {
			byID[account.ID] = account
		}
	}
	result := make([]*Account, 0, len(ids))
	for _, id := range ids {
		account := byID[id]
		if account == nil {
			return nil, ErrAccountNotFound
		}
		result = append(result, account)
	}
	return result, nil
}

func batchDeleteAccountRootIDs(accounts []*Account) []int64 {
	selected := make(map[int64]*Account, len(accounts))
	for _, account := range accounts {
		if account != nil && account.ID > 0 {
			selected[account.ID] = account
		}
	}

	roots := make(map[int64]struct{}, len(selected))
	for _, account := range selected {
		rootID := account.ID
		visited := map[int64]struct{}{account.ID: {}}
		for {
			current := selected[rootID]
			if current == nil || current.ParentAccountID == nil {
				break
			}
			parentID := *current.ParentAccountID
			if _, ok := selected[parentID]; !ok {
				break
			}
			if _, cyclic := visited[parentID]; cyclic {
				for id := range visited {
					if id < rootID {
						rootID = id
					}
				}
				break
			}
			visited[parentID] = struct{}{}
			rootID = parentID
		}
		roots[rootID] = struct{}{}
	}

	rootIDs := make([]int64, 0, len(roots))
	for rootID := range roots {
		rootIDs = append(rootIDs, rootID)
	}
	sort.Slice(rootIDs, func(i, j int) bool { return rootIDs[i] < rootIDs[j] })
	return rootIDs
}

func (s *adminServiceImpl) validateResourceMutationShadowSets(
	ctx context.Context,
	authorizedByParent map[int64][]int64,
) error {
	parentIDs := make([]int64, 0, len(authorizedByParent))
	for parentID := range authorizedByParent {
		parentIDs = append(parentIDs, parentID)
	}
	sort.Slice(parentIDs, func(i, j int) bool { return parentIDs[i] < parentIDs[j] })
	for _, parentID := range parentIDs {
		if err := s.validateResourceMutationShadowSet(ctx, parentID, authorizedByParent[parentID]); err != nil {
			return err
		}
	}
	return nil
}

func (s *adminServiceImpl) validateResourceMutationShadowSet(
	ctx context.Context,
	parentID int64,
	authorizedIDs []int64,
) error {
	shadows, err := s.accountRepo.ListShadowsByParent(ctx, parentID)
	if err != nil {
		return ErrResourceMutationUnavailable.WithCause(err)
	}
	actualIDs := resourceMutationShadowIDs(shadows)
	authorizedIDs = uniqueSortedResourceIDs(authorizedIDs)
	if len(actualIDs) != len(authorizedIDs) {
		return ErrResourceMutationConflict
	}
	for index := range actualIDs {
		if actualIDs[index] != authorizedIDs[index] {
			return ErrResourceMutationConflict
		}
	}
	return nil
}

func resourceMutationShadowIDs(shadows []*Account) []int64 {
	ids := make([]int64, 0, len(shadows))
	for _, shadow := range shadows {
		if shadow != nil {
			ids = append(ids, shadow.ID)
		}
	}
	return uniqueSortedResourceIDs(ids)
}

func accountResourceMutationTarget(
	account *Account,
	action authz.Action,
	mutates bool,
	eventType string,
	changedFields []string,
) ResourceMutationTarget {
	if account == nil || account.AccessVersion <= 0 {
		return ResourceMutationTarget{}
	}
	ref, _ := authz.NewResourceRef(authz.ResourceTypeAccount, account.ID)
	return ResourceMutationTarget{
		Ref:                   ref,
		Action:                action,
		ExpectedAccessVersion: account.AccessVersion,
		Mutates:               mutates,
		EventType:             eventType,
		ChangedFields:         append([]string(nil), changedFields...),
	}
}

func createdAccountMutation(account *Account, eventType string, fields []string) CreatedResourceMutation {
	if account == nil {
		return CreatedResourceMutation{}
	}
	ref, _ := authz.NewResourceRef(authz.ResourceTypeAccount, account.ID)
	return CreatedResourceMutation{
		Ref:           ref,
		OwnerUserID:   cloneResourceMutationInt64Pointer(account.OwnerUserID),
		AccessVersion: account.AccessVersion,
		EventType:     eventType,
		ChangedFields: append([]string(nil), fields...),
	}
}

func accountUpdateFieldCategories(input *UpdateAccountInput) []string {
	fields := []string{"configuration"}
	if input == nil {
		return fields
	}
	if len(input.Credentials) > 0 {
		fields = append(fields, "credentials")
	}
	if input.Extra != nil {
		fields = append(fields, "extra")
	}
	if input.GroupIDs != nil {
		fields = append(fields, "account_groups")
	}
	return fields
}

func cloneCreateAccountInput(input *CreateAccountInput) CreateAccountInput {
	cloned := *input
	cloned.GroupIDs = append([]int64(nil), input.GroupIDs...)
	cloned.Credentials = maps.Clone(input.Credentials)
	cloned.Extra = maps.Clone(input.Extra)
	return cloned
}

func cloneBulkUpdateAccountsInput(input *BulkUpdateAccountsInput) BulkUpdateAccountsInput {
	cloned := *input
	cloned.AccountIDs = append([]int64(nil), input.AccountIDs...)
	cloned.Credentials = maps.Clone(input.Credentials)
	cloned.Extra = maps.Clone(input.Extra)
	if input.GroupIDs != nil {
		ids := append([]int64(nil), (*input.GroupIDs)...)
		cloned.GroupIDs = &ids
	}
	if input.Filters != nil {
		filters := *input.Filters
		cloned.Filters = &filters
	}
	return cloned
}

func uniqueSortedResourceIDs(ids []int64) []int64 {
	set := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id > 0 {
			set[id] = struct{}{}
		}
	}
	result := make([]int64, 0, len(set))
	for id := range set {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
