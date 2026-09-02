package service

import (
	"context"
	"errors"
	"slices"
	"sort"

	"github.com/Wei-Shaw/sub2api/internal/authz"
)

type groupMutationTargetSet map[ResourceMutationKey]ResourceMutationTarget

func (s *adminServiceImpl) CreateGroup(ctx context.Context, actor authz.Actor, input *CreateGroupInput) (*Group, error) {
	if s == nil || s.resourceMutations == nil {
		return s.createGroupInResourceTx(ctx, actor, input)
	}
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	if input == nil {
		return nil, errors.New("group input is required")
	}
	work := cloneCreateGroupInput(input)
	targets := make(groupMutationTargetSet)
	sourceIDs := uniqueSortedResourceIDs(work.CopyAccountsFromGroupIDs)
	sourceAccountIDs, err := s.addGroupCopyTargets(ctx, targets, sourceIDs, work.RequireOAuthOnly, NormalizeGroupPlatform(work.Platform))
	if err != nil {
		return nil, err
	}
	if err := s.addGroupReferenceTargets(ctx, targets, groupReferenceIDs(work.FallbackGroupID, work.FallbackGroupIDOnInvalidRequest)); err != nil {
		return nil, err
	}
	if err := s.addAccountReferenceTargets(ctx, targets, groupModelRoutingAccountIDs(work.ModelRouting)); err != nil {
		return nil, err
	}

	var created *Group
	err = s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{
		CreateResourceTypes: []authz.ResourceType{authz.ResourceTypeGroup},
		Targets:             targets.list(),
	}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		if err := s.checkGroupCopySnapshot(txCtx, sourceIDs, sourceAccountIDs, work.RequireOAuthOnly, NormalizeGroupPlatform(work.Platform)); err != nil {
			return nil, err
		}
		var createErr error
		created, createErr = s.createGroupInResourceTx(txCtx, actor, &work)
		if createErr != nil {
			return nil, createErr
		}
		fields := []string{"configuration"}
		if len(sourceIDs) > 0 {
			fields = append(fields, "account_groups")
		}
		return []CreatedResourceMutation{createdGroupMutation(created, "group.created", fields)}, nil
	})
	return created, err
}

func (s *adminServiceImpl) DuplicateGroup(ctx context.Context, actor authz.Actor, id int64, operationKey string) (*Group, error) {
	if s == nil || s.resourceMutations == nil {
		return s.duplicateGroupInResourceTx(ctx, actor, id, operationKey)
	}
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	if existing, err := s.RecoverDuplicateGroup(ctx, actor, id, operationKey); err != nil || existing != nil {
		return existing, err
	}
	source, err := s.groupRepo.GetByIDLite(ctx, id)
	if err != nil {
		return nil, err
	}
	targets := make(groupMutationTargetSet)
	if err := targets.addGroup(source, authz.ActionGroupView, false, "", nil); err != nil {
		return nil, err
	}
	sourceIDs := []int64{id}
	sourceAccountIDs, err := s.addGroupCopyTargets(ctx, targets, sourceIDs, source.RequireOAuthOnly, source.Platform)
	if err != nil {
		return nil, err
	}
	if err := s.addGroupReferenceTargets(ctx, targets, groupReferenceIDs(source.FallbackGroupID, source.FallbackGroupIDOnInvalidRequest)); err != nil {
		return nil, err
	}
	if err := s.addAccountReferenceTargets(ctx, targets, groupModelRoutingAccountIDs(source.ModelRouting)); err != nil {
		return nil, err
	}

	var duplicate *Group
	err = s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{
		CreateResourceTypes: []authz.ResourceType{authz.ResourceTypeGroup},
		Targets:             targets.list(),
	}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		if existing, recoverErr := s.RecoverDuplicateGroup(txCtx, actor, id, operationKey); recoverErr != nil {
			return nil, recoverErr
		} else if existing != nil {
			duplicate = existing
			return nil, errResourceMutationNoop
		}
		if err := s.checkGroupCopySnapshot(txCtx, sourceIDs, sourceAccountIDs, source.RequireOAuthOnly, source.Platform); err != nil {
			return nil, err
		}
		var duplicateErr error
		duplicate, duplicateErr = s.duplicateGroupInResourceTx(txCtx, actor, id, operationKey)
		if duplicateErr != nil {
			return nil, duplicateErr
		}
		return []CreatedResourceMutation{createdGroupMutation(
			duplicate, "group.duplicated", []string{"configuration", "account_groups"},
		)}, nil
	})
	return duplicate, err
}

func (s *adminServiceImpl) UpdateGroup(ctx context.Context, actor authz.Actor, id int64, input *UpdateGroupInput) (*Group, error) {
	if s == nil || s.resourceMutations == nil {
		return s.updateGroupInResourceTx(ctx, actor, id, input)
	}
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	if input == nil {
		return nil, errors.New("group input is required")
	}
	work := cloneUpdateGroupInput(input)
	current, err := s.groupRepo.GetByIDLite(ctx, id)
	if err != nil {
		return nil, err
	}
	targets := make(groupMutationTargetSet)
	fields := []string{"configuration"}
	if len(work.CopyAccountsFromGroupIDs) > 0 {
		fields = append(fields, "account_groups")
	}
	if err := targets.addGroup(current, authz.ActionGroupEdit, true, "group.updated", fields); err != nil {
		return nil, err
	}

	sourceIDs := uniqueSortedResourceIDs(work.CopyAccountsFromGroupIDs)
	finalPlatform := current.Platform
	if work.Platform != "" {
		finalPlatform = work.Platform
	}
	finalRequireOAuthOnly := current.RequireOAuthOnly
	if work.RequireOAuthOnly != nil {
		finalRequireOAuthOnly = *work.RequireOAuthOnly
	}
	var sourceAccountIDs []int64
	var currentAccountIDs []int64
	if len(sourceIDs) > 0 {
		sourceAccountIDs, err = s.addGroupCopyTargets(ctx, targets, sourceIDs, finalRequireOAuthOnly, finalPlatform)
		if err != nil {
			return nil, err
		}
		var loadErr error
		currentAccountIDs, loadErr = s.groupRepo.GetAccountIDsByGroupIDs(ctx, []int64{id})
		if loadErr != nil {
			return nil, loadErr
		}
		if err := s.addAccountMutationTargets(ctx, targets, currentAccountIDs); err != nil {
			return nil, err
		}
	}
	if err := s.addGroupReferenceTargets(ctx, targets, finalGroupReferenceIDs(current, &work)); err != nil {
		return nil, err
	}
	if err := s.addAccountReferenceTargets(ctx, targets, groupModelRoutingAccountIDs(finalGroupModelRouting(current, &work))); err != nil {
		return nil, err
	}

	var updated *Group
	err = s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{Targets: targets.list()}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		if len(sourceIDs) > 0 {
			if err := s.checkGroupAccountSnapshot(txCtx, []int64{id}, currentAccountIDs); err != nil {
				return nil, err
			}
			if err := s.checkGroupCopySnapshot(txCtx, sourceIDs, sourceAccountIDs, finalRequireOAuthOnly, finalPlatform); err != nil {
				return nil, err
			}
		}
		var updateErr error
		updated, updateErr = s.updateGroupInResourceTx(txCtx, actor, id, &work)
		return nil, updateErr
	})
	if err == nil && updated != nil {
		updated.AccessVersion = current.AccessVersion + 1
	}
	return updated, err
}

func (s *adminServiceImpl) DeleteGroup(ctx context.Context, actor authz.Actor, id int64) error {
	if s == nil || s.resourceMutations == nil {
		return s.deleteGroupInResourceTx(ctx, actor, id)
	}
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	group, err := s.groupRepo.GetByIDLite(ctx, id)
	if err != nil {
		return err
	}
	accountIDs, err := s.groupRepo.GetAccountIDsByGroupIDs(ctx, []int64{id})
	if err != nil {
		return err
	}
	targets := make(groupMutationTargetSet)
	if err := targets.addGroup(group, authz.ActionGroupDelete, true, "group.deleted", []string{"configuration", "account_groups"}); err != nil {
		return err
	}
	if err := s.addAccountMutationTargets(ctx, targets, accountIDs); err != nil {
		return err
	}
	return s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{Targets: targets.list()}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		if err := s.checkGroupAccountSnapshot(txCtx, []int64{id}, accountIDs); err != nil {
			return nil, err
		}
		return nil, s.deleteGroupInResourceTx(txCtx, actor, id)
	})
}

func (s *adminServiceImpl) CreateCompositeRoute(ctx context.Context, actor authz.Actor, groupID int64, input CompositeRouteInput) (*CompositeModelRoute, error) {
	if s == nil || s.resourceMutations == nil {
		return s.createCompositeRouteInResourceTx(ctx, actor, groupID, input)
	}
	var route *CompositeModelRoute
	err := s.executeGroupResourceMutation(ctx, actor, groupID, authz.ActionGroupEdit, "group.composite_route_created", []string{"composite_routes"}, func(txCtx context.Context) error {
		var createErr error
		route, createErr = s.createCompositeRouteInResourceTx(txCtx, actor, groupID, input)
		return createErr
	})
	return route, err
}

func (s *adminServiceImpl) UpdateCompositeRoute(ctx context.Context, actor authz.Actor, groupID, routeID int64, input CompositeRouteInput) (*CompositeModelRoute, error) {
	if s == nil || s.resourceMutations == nil {
		return s.updateCompositeRouteInResourceTx(ctx, actor, groupID, routeID, input)
	}
	var route *CompositeModelRoute
	err := s.executeGroupResourceMutation(ctx, actor, groupID, authz.ActionGroupEdit, "group.composite_route_updated", []string{"composite_routes"}, func(txCtx context.Context) error {
		var updateErr error
		route, updateErr = s.updateCompositeRouteInResourceTx(txCtx, actor, groupID, routeID, input)
		return updateErr
	})
	return route, err
}

func (s *adminServiceImpl) DeleteCompositeRoute(ctx context.Context, actor authz.Actor, groupID, routeID int64) error {
	if s == nil || s.resourceMutations == nil {
		return s.deleteCompositeRouteInResourceTx(ctx, actor, groupID, routeID)
	}
	return s.executeGroupResourceMutation(ctx, actor, groupID, authz.ActionGroupEdit, "group.composite_route_deleted", []string{"composite_routes"}, func(txCtx context.Context) error {
		return s.deleteCompositeRouteInResourceTx(txCtx, actor, groupID, routeID)
	})
}

func (s *adminServiceImpl) ClearGroupRateMultipliers(ctx context.Context, actor authz.Actor, groupID int64) error {
	if s == nil || s.resourceMutations == nil {
		return s.clearGroupRateMultipliersInResourceTx(ctx, actor, groupID)
	}
	return s.executeGroupResourceMutation(ctx, actor, groupID, authz.ActionGroupEdit, "group.rate_multipliers_cleared", []string{"rate_multipliers"}, func(txCtx context.Context) error {
		return s.clearGroupRateMultipliersInResourceTx(txCtx, actor, groupID)
	})
}

func (s *adminServiceImpl) BatchSetGroupRateMultipliers(ctx context.Context, actor authz.Actor, groupID int64, entries []GroupRateMultiplierInput) error {
	if s == nil || s.resourceMutations == nil {
		return s.batchSetGroupRateMultipliersInResourceTx(ctx, actor, groupID, entries)
	}
	work := append([]GroupRateMultiplierInput(nil), entries...)
	return s.executeGroupResourceMutation(ctx, actor, groupID, authz.ActionGroupEdit, "group.rate_multipliers_updated", []string{"rate_multipliers"}, func(txCtx context.Context) error {
		return s.batchSetGroupRateMultipliersInResourceTx(txCtx, actor, groupID, work)
	})
}

func (s *adminServiceImpl) ClearGroupRPMOverrides(ctx context.Context, actor authz.Actor, groupID int64) error {
	if s == nil || s.resourceMutations == nil {
		return s.clearGroupRPMOverridesInResourceTx(ctx, actor, groupID)
	}
	return s.executeGroupResourceMutation(ctx, actor, groupID, authz.ActionGroupEdit, "group.rpm_overrides_cleared", []string{"rpm_overrides"}, func(txCtx context.Context) error {
		return s.clearGroupRPMOverridesInResourceTx(txCtx, actor, groupID)
	})
}

func (s *adminServiceImpl) BatchSetGroupRPMOverrides(ctx context.Context, actor authz.Actor, groupID int64, entries []GroupRPMOverrideInput) error {
	if s == nil || s.resourceMutations == nil {
		return s.batchSetGroupRPMOverridesInResourceTx(ctx, actor, groupID, entries)
	}
	work := append([]GroupRPMOverrideInput(nil), entries...)
	return s.executeGroupResourceMutation(ctx, actor, groupID, authz.ActionGroupEdit, "group.rpm_overrides_updated", []string{"rpm_overrides"}, func(txCtx context.Context) error {
		return s.batchSetGroupRPMOverridesInResourceTx(txCtx, actor, groupID, work)
	})
}

func (s *adminServiceImpl) UpdateGroupSortOrders(ctx context.Context, actor authz.Actor, updates []GroupSortOrderUpdate) error {
	if s == nil || s.resourceMutations == nil {
		return s.updateGroupSortOrdersInResourceTx(ctx, actor, updates)
	}
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	work := append([]GroupSortOrderUpdate(nil), updates...)
	ids := make([]int64, 0, len(work))
	for _, update := range work {
		ids = append(ids, update.ID)
	}
	targets, err := s.groupResourceMutationTargets(ctx, ids, authz.ActionGroupEdit, true, "group.sort_order_updated", []string{"sort_order"})
	if err != nil {
		return err
	}
	return s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{Targets: targets}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		return nil, s.updateGroupSortOrdersInResourceTx(txCtx, actor, work)
	})
}

func (s *adminServiceImpl) AdminUpdateAPIKeyGroupID(ctx context.Context, actor authz.Actor, keyID int64, groupID *int64) (*AdminUpdateAPIKeyGroupIDResult, error) {
	if s == nil || s.resourceMutations == nil || groupID == nil {
		return s.adminUpdateAPIKeyGroupIDInResourceTx(ctx, actor, keyID, groupID)
	}
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	apiKey, err := s.apiKeyRepo.GetByID(ctx, keyID)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, 2)
	if apiKey.GroupID != nil {
		ids = append(ids, *apiKey.GroupID)
	}
	if *groupID > 0 {
		ids = append(ids, *groupID)
	}
	expectedOldGroupID := cloneResourceMutationInt64Pointer(apiKey.GroupID)
	targets, err := s.groupResourceMutationTargets(ctx, ids, authz.ActionGroupUse, true, "group.api_key_bindings_changed", []string{"api_key_bindings"})
	if err != nil {
		return nil, err
	}
	var result *AdminUpdateAPIKeyGroupIDResult
	err = s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{Targets: targets}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		current, loadErr := s.apiKeyRepo.GetByID(txCtx, keyID)
		if loadErr != nil {
			return nil, loadErr
		}
		if !equalResourceMutationInt64Pointers(current.GroupID, expectedOldGroupID) {
			return nil, ErrResourceMutationConflict
		}
		var updateErr error
		result, updateErr = s.adminUpdateAPIKeyGroupIDInResourceTx(txCtx, actor, keyID, groupID)
		return nil, updateErr
	})
	return result, err
}

func equalResourceMutationInt64Pointers(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (s *adminServiceImpl) ReplaceUserGroup(ctx context.Context, actor authz.Actor, userID, oldGroupID, newGroupID int64) (*ReplaceUserGroupResult, error) {
	if s == nil || s.resourceMutations == nil {
		return s.replaceUserGroupInResourceTx(ctx, actor, userID, oldGroupID, newGroupID)
	}
	if err := ValidateAdminResourceActor(actor); err != nil {
		return nil, err
	}
	targets, err := s.groupResourceMutationTargets(
		ctx, []int64{oldGroupID, newGroupID}, authz.ActionGroupManageAccess, true,
		"group.user_bindings_changed", []string{"api_key_bindings", "user_allowed_groups"},
	)
	if err != nil {
		return nil, err
	}
	var result *ReplaceUserGroupResult
	err = s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{Targets: targets}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		var replaceErr error
		result, replaceErr = s.replaceUserGroupInResourceTx(txCtx, actor, userID, oldGroupID, newGroupID)
		return nil, replaceErr
	})
	return result, err
}

func (s *adminServiceImpl) executeGroupResourceMutation(
	ctx context.Context,
	actor authz.Actor,
	groupID int64,
	action authz.Action,
	eventType string,
	changedFields []string,
	mutate func(context.Context) error,
) error {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	targets, err := s.groupResourceMutationTargets(ctx, []int64{groupID}, action, true, eventType, changedFields)
	if err != nil {
		return err
	}
	return s.resourceMutations.Execute(ctx, actor, ResourceMutationCommand{Targets: targets}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		return nil, mutate(txCtx)
	})
}

func (s *adminServiceImpl) addGroupCopyTargets(
	ctx context.Context,
	targets groupMutationTargetSet,
	sourceIDs []int64,
	requireOAuthOnly bool,
	platform string,
) ([]int64, error) {
	for _, id := range sourceIDs {
		group, err := s.groupRepo.GetByIDLite(ctx, id)
		if err != nil {
			return nil, err
		}
		if err := targets.addGroup(group, authz.ActionGroupView, false, "", nil); err != nil {
			return nil, err
		}
	}
	accountIDs, err := s.groupRepo.GetAccountIDsByGroupIDs(ctx, sourceIDs)
	if err != nil {
		return nil, err
	}
	effectiveIDs, err := s.filterGroupCopyAccountIDs(ctx, accountIDs, requireOAuthOnly, platform)
	if err != nil {
		return nil, err
	}
	if err := s.addAccountMutationTargets(ctx, targets, effectiveIDs); err != nil {
		return nil, err
	}
	return effectiveIDs, nil
}

func (s *adminServiceImpl) checkGroupCopySnapshot(
	ctx context.Context,
	sourceIDs, expectedAccountIDs []int64,
	requireOAuthOnly bool,
	platform string,
) error {
	actual, err := s.groupRepo.GetAccountIDsByGroupIDs(ctx, sourceIDs)
	if err != nil {
		return err
	}
	actual, err = s.filterGroupCopyAccountIDs(ctx, actual, requireOAuthOnly, platform)
	if err != nil {
		return err
	}
	if !slices.Equal(uniqueSortedResourceIDs(expectedAccountIDs), uniqueSortedResourceIDs(actual)) {
		return ErrResourceMutationConflict
	}
	return nil
}

func (s *adminServiceImpl) checkGroupAccountSnapshot(ctx context.Context, groupIDs, expectedAccountIDs []int64) error {
	actual, err := s.groupRepo.GetAccountIDsByGroupIDs(ctx, groupIDs)
	if err != nil {
		return err
	}
	if !slices.Equal(uniqueSortedResourceIDs(expectedAccountIDs), uniqueSortedResourceIDs(actual)) {
		return ErrResourceMutationConflict
	}
	return nil
}

func (s *adminServiceImpl) filterGroupCopyAccountIDs(ctx context.Context, ids []int64, requireOAuthOnly bool, platform string) ([]int64, error) {
	ids = uniqueSortedResourceIDs(ids)
	if !requireOAuthOnly || !groupSupportsOAuthOnlyFilter(platform) || len(ids) == 0 {
		return ids, nil
	}
	accounts, err := s.loadAccountsForResourceMutation(ctx, ids)
	if err != nil {
		return nil, err
	}
	filtered := make([]int64, 0, len(accounts))
	for _, account := range accounts {
		if account.Type != AccountTypeAPIKey {
			filtered = append(filtered, account.ID)
		}
	}
	return filtered, nil
}

func (s *adminServiceImpl) addGroupReferenceTargets(ctx context.Context, targets groupMutationTargetSet, ids []int64) error {
	for _, id := range uniqueSortedResourceIDs(ids) {
		group, err := s.groupRepo.GetByIDLite(ctx, id)
		if err != nil {
			return err
		}
		if err := targets.addGroup(group, authz.ActionGroupUse, false, "", nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *adminServiceImpl) addAccountReferenceTargets(ctx context.Context, targets groupMutationTargetSet, ids []int64) error {
	accounts, err := s.loadAccountsForResourceMutation(ctx, ids)
	if err != nil {
		return err
	}
	for _, account := range accounts {
		if err := targets.addAccount(account, authz.ActionAccountUse, false, "", nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *adminServiceImpl) addAccountMutationTargets(ctx context.Context, targets groupMutationTargetSet, ids []int64) error {
	accounts, err := s.loadAccountsForResourceMutation(ctx, ids)
	if err != nil {
		return err
	}
	for _, account := range accounts {
		if err := targets.addAccount(account, authz.ActionAccountUse, true, "account.group_links_changed", []string{"account_groups"}); err != nil {
			return err
		}
	}
	return nil
}

func (targets groupMutationTargetSet) addGroup(group *Group, action authz.Action, mutates bool, eventType string, fields []string) error {
	if group == nil || group.AccessVersion <= 0 {
		return ErrResourceMutationUnavailable
	}
	ref, err := authz.NewResourceRef(authz.ResourceTypeGroup, group.ID)
	if err != nil {
		return ErrResourceMutationUnavailable.WithCause(err)
	}
	return targets.add(ResourceMutationTarget{Ref: ref, Action: action, ExpectedAccessVersion: group.AccessVersion, Mutates: mutates, EventType: eventType, ChangedFields: fields})
}

func (targets groupMutationTargetSet) addAccount(account *Account, action authz.Action, mutates bool, eventType string, fields []string) error {
	if account == nil || account.AccessVersion <= 0 {
		return ErrResourceMutationUnavailable
	}
	return targets.add(accountResourceMutationTarget(account, action, mutates, eventType, fields))
}

func (targets groupMutationTargetSet) add(target ResourceMutationTarget) error {
	key := ResourceMutationKeyFromRef(target.Ref)
	if !key.Valid() {
		return ErrResourceMutationUnavailable
	}
	existing, found := targets[key]
	if !found {
		target.ChangedFields = append([]string(nil), target.ChangedFields...)
		targets[key] = target
		return nil
	}
	if existing.ExpectedAccessVersion != target.ExpectedAccessVersion {
		return ErrResourceMutationConflict
	}
	if resourceMutationActionRank(target.Action) > resourceMutationActionRank(existing.Action) {
		existing.Action = target.Action
	}
	if target.Mutates {
		if !existing.Mutates {
			existing.EventType = target.EventType
		}
		existing.Mutates = true
		existing.ChangedFields = append(existing.ChangedFields, target.ChangedFields...)
	}
	targets[key] = existing
	return nil
}

func (targets groupMutationTargetSet) list() []ResourceMutationTarget {
	result := make([]ResourceMutationTarget, 0, len(targets))
	for _, target := range targets {
		result = append(result, target)
	}
	sort.Slice(result, func(i, j int) bool {
		left, right := ResourceMutationKeyFromRef(result[i].Ref), ResourceMutationKeyFromRef(result[j].Ref)
		if left.ResourceType != right.ResourceType {
			return left.ResourceType < right.ResourceType
		}
		return left.ResourceID < right.ResourceID
	})
	return result
}

func resourceMutationActionRank(action authz.Action) int {
	switch action {
	case authz.ActionGroupView, authz.ActionAccountView:
		return 1
	case authz.ActionGroupUse, authz.ActionAccountUse:
		return 2
	case authz.ActionAccountOperate:
		return 3
	case authz.ActionGroupEdit, authz.ActionAccountEdit:
		return 4
	case authz.ActionGroupManageAccess, authz.ActionAccountManageAccess:
		return 5
	case authz.ActionGroupDelete, authz.ActionAccountDelete:
		return 6
	case authz.ActionGroupTransfer, authz.ActionAccountTransfer:
		return 7
	default:
		return 0
	}
}

func createdGroupMutation(group *Group, eventType string, fields []string) CreatedResourceMutation {
	if group == nil {
		return CreatedResourceMutation{}
	}
	ref, _ := authz.NewResourceRef(authz.ResourceTypeGroup, group.ID)
	return CreatedResourceMutation{
		Ref:           ref,
		OwnerUserID:   cloneResourceMutationInt64Pointer(group.OwnerUserID),
		AccessVersion: group.AccessVersion,
		EventType:     eventType,
		ChangedFields: append([]string(nil), fields...),
	}
}

func groupReferenceIDs(values ...*int64) []int64 {
	ids := make([]int64, 0, len(values))
	for _, value := range values {
		if value != nil && *value > 0 {
			ids = append(ids, *value)
		}
	}
	return uniqueSortedResourceIDs(ids)
}

func finalGroupReferenceIDs(current *Group, input *UpdateGroupInput) []int64 {
	fallback := current.FallbackGroupID
	if input.FallbackGroupID != nil {
		fallback = input.FallbackGroupID
	}
	fallbackInvalid := current.FallbackGroupIDOnInvalidRequest
	if input.FallbackGroupIDOnInvalidRequest != nil {
		fallbackInvalid = input.FallbackGroupIDOnInvalidRequest
	}
	return groupReferenceIDs(fallback, fallbackInvalid)
}

func groupModelRoutingAccountIDs(routing map[string][]int64) []int64 {
	var ids []int64
	for _, routeIDs := range routing {
		ids = append(ids, routeIDs...)
	}
	return uniqueSortedResourceIDs(ids)
}

func finalGroupModelRouting(current *Group, input *UpdateGroupInput) map[string][]int64 {
	if input.ModelRouting != nil {
		return input.ModelRouting
	}
	return current.ModelRouting
}

func cloneCreateGroupInput(input *CreateGroupInput) CreateGroupInput {
	cloned := *input
	cloned.ModelPricing = append([]ChannelModelPricing(nil), input.ModelPricing...)
	cloned.VideoModelPrices = cloneGroupVideoModelPrices(input.VideoModelPrices)
	cloned.ModelRouting = cloneGroupModelRouting(input.ModelRouting)
	cloned.SupportedModelScopes = append([]string(nil), input.SupportedModelScopes...)
	cloned.MessagesDispatchModelConfig = cloneGroupMessagesDispatchModelConfig(input.MessagesDispatchModelConfig)
	cloned.ModelsListConfig.Models = append([]string(nil), input.ModelsListConfig.Models...)
	cloned.ReasoningEffortMappings = append([]ReasoningEffortMapping(nil), input.ReasoningEffortMappings...)
	cloned.CopyAccountsFromGroupIDs = append([]int64(nil), input.CopyAccountsFromGroupIDs...)
	return cloned
}

func cloneUpdateGroupInput(input *UpdateGroupInput) UpdateGroupInput {
	cloned := *input
	if input.ModelPricing != nil {
		pricing := append([]ChannelModelPricing(nil), (*input.ModelPricing)...)
		cloned.ModelPricing = &pricing
	}
	cloned.VideoModelPrices = cloneGroupVideoModelPrices(input.VideoModelPrices)
	cloned.ModelRouting = cloneGroupModelRouting(input.ModelRouting)
	if input.SupportedModelScopes != nil {
		values := append([]string(nil), (*input.SupportedModelScopes)...)
		cloned.SupportedModelScopes = &values
	}
	if input.MessagesDispatchModelConfig != nil {
		value := cloneGroupMessagesDispatchModelConfig(*input.MessagesDispatchModelConfig)
		cloned.MessagesDispatchModelConfig = &value
	}
	if input.ModelsListConfig != nil {
		value := *input.ModelsListConfig
		value.Models = append([]string(nil), input.ModelsListConfig.Models...)
		cloned.ModelsListConfig = &value
	}
	if input.ReasoningEffortMappings != nil {
		values := append([]ReasoningEffortMapping(nil), (*input.ReasoningEffortMappings)...)
		cloned.ReasoningEffortMappings = &values
	}
	cloned.CopyAccountsFromGroupIDs = append([]int64(nil), input.CopyAccountsFromGroupIDs...)
	return cloned
}
