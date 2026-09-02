package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/stretchr/testify/require"
)

type selfServiceGroupTxContextKey struct{}

type selfServiceGroupCallLog struct {
	calls []string
}

func (l *selfServiceGroupCallLog) add(call string) {
	if l != nil {
		l.calls = append(l.calls, call)
	}
}

type selfServiceGroupRepositoryStub struct {
	log *selfServiceGroupCallLog

	lockState   SelfServiceGroupState
	createState SelfServiceGroupState
	updateState SelfServiceGroupState
	deleteState SelfServiceGroupState

	lockActorErr error
	lockErr      error
	createErr    error
	updateErr    error
	deleteErr    error
	appendErr    error
	commitErr    error

	createInput           SelfServiceGroupCreateRecord
	updateGroupID         int64
	updateOwnerUserID     int64
	updateExpectedVersion int64
	updateName            string
	updateDescription     string
	deleteGroupID         int64
	deleteOwnerUserID     int64
	deleteExpectedVersion int64

	stagedEvents    []ResourceAuthorizationEventRecord
	committedEvents []ResourceAuthorizationEventRecord
	txCalls         int
	rolledBack      bool
	committed       bool
}

func (r *selfServiceGroupRepositoryStub) WithSerializableTx(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	r.txCalls++
	r.log.add("tx.begin")
	txCtx := context.WithValue(ctx, selfServiceGroupTxContextKey{}, true)
	if err := fn(txCtx); err != nil {
		r.rolledBack = true
		r.stagedEvents = nil
		r.log.add("tx.rollback")
		return err
	}
	if r.commitErr != nil {
		r.rolledBack = true
		r.stagedEvents = nil
		r.log.add("tx.rollback")
		return r.commitErr
	}
	r.committed = true
	r.committedEvents = append(r.committedEvents, r.stagedEvents...)
	r.stagedEvents = nil
	r.log.add("tx.commit")
	return nil
}

func (r *selfServiceGroupRepositoryStub) LockActorAuthorization(
	ctx context.Context,
	_ int64,
) error {
	assertSelfServiceGroupTxContext(ctx)
	r.log.add("lock.actor")
	return r.lockActorErr
}

func (r *selfServiceGroupRepositoryStub) LockGroup(
	ctx context.Context,
	_ int64,
) (SelfServiceGroupState, error) {
	assertSelfServiceGroupTxContext(ctx)
	r.log.add("lock.group")
	if r.lockErr != nil {
		return SelfServiceGroupState{}, r.lockErr
	}
	return r.lockState, nil
}

func (r *selfServiceGroupRepositoryStub) CreateGroup(
	ctx context.Context,
	input SelfServiceGroupCreateRecord,
) (SelfServiceGroupState, error) {
	assertSelfServiceGroupTxContext(ctx)
	r.log.add("group.create")
	r.createInput = input
	if r.createErr != nil {
		return SelfServiceGroupState{}, r.createErr
	}
	return r.createState, nil
}

func (r *selfServiceGroupRepositoryStub) UpdateGroup(
	ctx context.Context,
	groupID int64,
	ownerUserID int64,
	expectedAccessVersion int64,
	name string,
	description string,
) (SelfServiceGroupState, error) {
	assertSelfServiceGroupTxContext(ctx)
	r.log.add("group.update")
	r.updateGroupID = groupID
	r.updateOwnerUserID = ownerUserID
	r.updateExpectedVersion = expectedAccessVersion
	r.updateName = name
	r.updateDescription = description
	if r.updateErr != nil {
		return SelfServiceGroupState{}, r.updateErr
	}
	return r.updateState, nil
}

func (r *selfServiceGroupRepositoryStub) DeleteGroup(
	ctx context.Context,
	groupID int64,
	ownerUserID int64,
	expectedAccessVersion int64,
) (SelfServiceGroupState, error) {
	assertSelfServiceGroupTxContext(ctx)
	r.log.add("group.delete")
	r.deleteGroupID = groupID
	r.deleteOwnerUserID = ownerUserID
	r.deleteExpectedVersion = expectedAccessVersion
	if r.deleteErr != nil {
		return SelfServiceGroupState{}, r.deleteErr
	}
	return r.deleteState, nil
}

func (r *selfServiceGroupRepositoryStub) AppendAuthorizationEvent(
	ctx context.Context,
	event ResourceAuthorizationEventRecord,
) error {
	assertSelfServiceGroupTxContext(ctx)
	r.log.add("event.append")
	if r.appendErr != nil {
		return r.appendErr
	}
	r.stagedEvents = append(r.stagedEvents, event)
	return nil
}

func assertSelfServiceGroupTxContext(ctx context.Context) {
	if ctx == nil || ctx.Value(selfServiceGroupTxContextKey{}) != true {
		panic("self-service group operation executed outside transaction")
	}
}

type selfServiceGroupCapacityStub struct {
	log          *selfServiceGroupCallLog
	err          error
	actor        authz.Actor
	resourceType authz.ResourceType
}

func (s *selfServiceGroupCapacityStub) RequireCreateCapacity(
	ctx context.Context,
	actor authz.Actor,
	resourceType authz.ResourceType,
) (HostingCapacity, error) {
	assertSelfServiceGroupTxContext(ctx)
	s.log.add("capacity.require")
	s.actor = actor
	s.resourceType = resourceType
	if s.err != nil {
		return HostingCapacity{}, s.err
	}
	userID, _ := actor.UserID()
	return HostingCapacity{
		UserID:       userID,
		ResourceType: resourceType,
		Limit:        2,
		Usage:        1,
		Remaining:    1,
		Version:      1,
	}, nil
}

type selfServiceGroupResolverStub struct {
	log   *selfServiceGroupCallLog
	actor authz.Actor
	err   error
	calls int
}

func (s *selfServiceGroupResolverStub) ResolveUser(
	context.Context,
	int64,
	authz.AuthMethod,
) (authz.Actor, error) {
	s.calls++
	s.log.add("actor.resolve")
	return s.actor, s.err
}

func (s *selfServiceGroupResolverStub) ResolveLegacyAdminUser(
	context.Context,
	int64,
) (authz.Actor, error) {
	return authz.Actor{}, errors.New("unexpected legacy admin resolution")
}

func (s *selfServiceGroupResolverStub) ResolveServicePrincipal(
	context.Context,
	string,
	authz.AuthMethod,
) (authz.Actor, error) {
	return authz.Actor{}, errors.New("unexpected service principal resolution")
}

type selfServiceGroupPolicyStoreStub struct {
	log      *selfServiceGroupCallLog
	subject  authz.SubjectSnapshot
	resource authz.ResourceAccessSnapshot
}

func (s *selfServiceGroupPolicyStoreStub) LoadSubjectSnapshot(
	context.Context,
	authz.SubjectRef,
) (authz.SubjectSnapshot, error) {
	s.log.add("policy.subject")
	return s.subject, nil
}

func (s *selfServiceGroupPolicyStoreStub) LoadServicePrincipalSubjectSnapshotByCode(
	context.Context,
	string,
) (authz.SubjectSnapshot, error) {
	return authz.SubjectSnapshot{}, errors.New("unexpected service principal policy lookup")
}

func (s *selfServiceGroupPolicyStoreStub) LoadResourceAccessSnapshot(
	context.Context,
	authz.SubjectRef,
	authz.ResourceRef,
) (authz.ResourceAccessSnapshot, error) {
	s.log.add("policy.resource")
	return s.resource, nil
}

func TestSelfServiceGroupProductionCatalogStartsEmpty(t *testing.T) {
	actor, snapshot := selfServiceGroupActorFixture(t, 41, 1)
	store := &selfServiceGroupPolicyStoreStub{subject: snapshot}
	catalog := NewSelfServiceGroupCatalog()
	service := NewSelfServiceGroupService(
		&selfServiceGroupRepositoryStub{},
		nil,
		authz.NewPolicyService(store),
		&selfServiceGroupCapacityStub{},
		nil,
		catalog,
	)

	platforms, err := service.ListPlatforms(context.Background(), actor)
	require.NoError(t, err)
	require.Empty(t, platforms)
	require.Empty(t, catalog.List())
	_, found := catalog.Resolve("openai")
	require.False(t, found)

	created, err := service.CreateGroup(context.Background(), SelfServiceGroupCreateInput{
		Actor: actor, Name: "private", PlatformID: "openai",
	})
	require.Nil(t, created)
	require.ErrorIs(t, err, ErrSelfServiceGroupPlatformUnavailable)
	repository, ok := service.repository.(*selfServiceGroupRepositoryStub)
	require.True(t, ok)
	require.Zero(t, repository.txCalls)
}

func TestStaticSelfServiceGroupCatalogAcceptsOnlyReviewedPlatforms(t *testing.T) {
	platforms := []SelfServiceGroupPlatform{
		{ID: " openai ", Name: " OpenAI ", Platform: PlatformOpenAI},
		{ID: "anthropic", Name: "Anthropic", Platform: PlatformAnthropic},
		{ID: "gemini", Name: "Gemini", Platform: PlatformGemini},
	}
	catalog, err := NewStaticSelfServiceGroupCatalog(platforms)
	require.NoError(t, err)
	require.Len(t, catalog.List(), 3)
	resolved, found := catalog.Resolve(" openai ")
	require.True(t, found)
	require.Equal(t, SelfServiceGroupPlatform{
		ID: "openai", Name: "OpenAI", Platform: PlatformOpenAI,
	}, resolved)

	platforms[0].Name = "mutated"
	require.Equal(t, "OpenAI", catalog.List()[0].Name)

	for _, invalid := range []SelfServiceGroupPlatform{
		{ID: "grok", Name: "Grok", Platform: PlatformGrok},
		{ID: "control", Name: "bad\nname", Platform: PlatformOpenAI},
		{ID: "", Name: "OpenAI", Platform: PlatformOpenAI},
	} {
		_, err = NewStaticSelfServiceGroupCatalog([]SelfServiceGroupPlatform{invalid})
		require.Error(t, err)
	}
	_, err = NewStaticSelfServiceGroupCatalog([]SelfServiceGroupPlatform{platforms[0], platforms[0]})
	require.Error(t, err)
}

func TestSelfServiceGroupCreateUsesExactPrivateDefaultsAndTransactionOrder(t *testing.T) {
	const userID int64 = 42
	actor, _ := selfServiceGroupActorFixture(t, userID, 3)
	log := &selfServiceGroupCallLog{}
	ownerID := userID
	creatorID := userID
	createdAt := time.Date(2026, 9, 2, 2, 3, 4, 0, time.UTC)
	repo := &selfServiceGroupRepositoryStub{
		log: log,
		createState: SelfServiceGroupState{
			GroupListItem: GroupListItem{
				ID: 71, Name: "Personal OpenAI", Description: "Private group",
				Platform: PlatformOpenAI, Status: StatusActive, OwnerUserID: &ownerID,
				CreatedAt: createdAt, UpdatedAt: createdAt,
			},
			CreatedByUserID:   &creatorID,
			AccessVersion:     1,
			AuthorizationMode: selfServiceGroupAuthorizationLegacy,
			IsExclusive:       true,
		},
	}
	capacity := &selfServiceGroupCapacityStub{log: log}
	service := NewSelfServiceGroupService(
		repo, nil, nil, capacity, nil, mustSelfServiceGroupCatalog(t),
	)

	item, err := service.CreateGroup(context.Background(), SelfServiceGroupCreateInput{
		Actor: actor, Name: "  Personal OpenAI  ", Description: "  Private group\r\n ",
		PlatformID: " openai ", RequestID: " request-71 ",
	})

	require.NoError(t, err)
	require.NotNil(t, item)
	require.Equal(t, int64(71), item.ID)
	require.True(t, repo.committed)
	require.False(t, repo.rolledBack)
	require.Equal(t, []string{
		"tx.begin", "capacity.require", "group.create", "event.append", "tx.commit",
	}, log.calls)
	require.True(t, actor.SameAuthorizationState(capacity.actor))
	require.Equal(t, authz.ResourceTypeGroup, capacity.resourceType)
	require.Equal(t, SelfServiceGroupCreateRecord{
		Name: "Personal OpenAI", Description: "Private group", Platform: PlatformOpenAI,
		OwnerUserID: userID, CreatorUserID: userID,
	}, repo.createInput)

	require.Len(t, repo.committedEvents, 1)
	event := repo.committedEvents[0]
	require.Equal(t, ResourceMutationKey{ResourceType: authz.ResourceTypeGroup, ResourceID: 71}, event.Key)
	require.Equal(t, &ownerID, event.OwnerUserID)
	require.Equal(t, authz.SubjectKindUser, event.ActorKind)
	require.Equal(t, userID, event.ActorID)
	require.Equal(t, authz.AuthMethodJWT, event.AuthMethod)
	require.Equal(t, "group.created", event.EventType)
	require.Equal(t, int64(1), event.ResourceAccessVersion)
	require.Equal(t, "request-71", event.RequestID)
	require.Equal(t, []string{"configuration", "ownership"}, event.ChangedFields)
}

func TestSelfServiceGroupCreateRollsBackWhenDurableEventFails(t *testing.T) {
	actor, _ := selfServiceGroupActorFixture(t, 43, 1)
	ownerID := int64(43)
	creatorID := int64(43)
	log := &selfServiceGroupCallLog{}
	repo := &selfServiceGroupRepositoryStub{
		log: log,
		createState: SelfServiceGroupState{
			GroupListItem: GroupListItem{
				ID: 72, Name: "Personal", Platform: PlatformOpenAI,
				Status: StatusActive, OwnerUserID: &ownerID,
			},
			CreatedByUserID:   &creatorID,
			AccessVersion:     1,
			AuthorizationMode: selfServiceGroupAuthorizationLegacy,
			IsExclusive:       true,
		},
		appendErr: errors.New("authorization event unavailable"),
	}
	service := NewSelfServiceGroupService(
		repo, nil, nil, &selfServiceGroupCapacityStub{log: log}, nil,
		mustSelfServiceGroupCatalog(t),
	)

	item, err := service.CreateGroup(context.Background(), SelfServiceGroupCreateInput{
		Actor: actor, Name: "Personal", PlatformID: "openai",
	})

	require.Nil(t, item)
	require.ErrorIs(t, err, ErrSelfServiceGroupUnavailable)
	require.True(t, repo.rolledBack)
	require.False(t, repo.committed)
	require.Empty(t, repo.committedEvents)
	require.Equal(t, []string{
		"tx.begin", "capacity.require", "group.create", "event.append", "tx.rollback",
	}, log.calls)
}

func TestSelfServiceGroupMutationRejectsStaleActorBeforePolicyOrWrite(t *testing.T) {
	const userID int64 = 44
	requestActor, _ := selfServiceGroupActorFixture(t, userID, 1)
	currentActor, _ := selfServiceGroupActorFixture(t, userID, 2)
	ownerID := userID
	log := &selfServiceGroupCallLog{}
	repo := &selfServiceGroupRepositoryStub{
		log: log,
		lockState: SelfServiceGroupState{
			GroupListItem: GroupListItem{ID: 73, Name: "before", OwnerUserID: &ownerID},
			AccessVersion: 4,
		},
	}
	resolver := &selfServiceGroupResolverStub{log: log, actor: currentActor}
	service := NewSelfServiceGroupService(
		repo,
		resolver,
		authz.NewPolicyService(&selfServiceGroupPolicyStoreStub{log: log}),
		nil,
		nil,
		nil,
	)
	name := "after"

	item, err := service.UpdateGroup(context.Background(), SelfServiceGroupUpdateInput{
		Actor: requestActor, GroupID: 73, Name: &name,
	})

	require.Nil(t, item)
	require.ErrorIs(t, err, ErrSelfServiceGroupConflict)
	require.True(t, repo.rolledBack)
	require.Equal(t, []string{
		"tx.begin", "lock.actor", "lock.group", "actor.resolve", "tx.rollback",
	}, log.calls)
	require.Zero(t, repo.updateGroupID)
	require.Empty(t, repo.committedEvents)
}

func TestSelfServiceGroupMutationsConcealNonOwnedGroups(t *testing.T) {
	const userID int64 = 45
	actor, _ := selfServiceGroupActorFixture(t, userID, 1)
	otherOwnerID := userID + 1
	ownerID := userID

	states := []struct {
		name    string
		state   SelfServiceGroupState
		lockErr error
	}{
		{
			name: "other owner",
			state: SelfServiceGroupState{
				GroupListItem: GroupListItem{ID: 74, OwnerUserID: &otherOwnerID}, AccessVersion: 1,
			},
		},
		{
			name: "platform group",
			state: SelfServiceGroupState{
				GroupListItem: GroupListItem{ID: 74}, AccessVersion: 1,
			},
		},
		{
			name: "deleted group",
			state: SelfServiceGroupState{
				GroupListItem: GroupListItem{ID: 74, OwnerUserID: &ownerID},
				AccessVersion: 2, Deleted: true,
			},
		},
		{name: "missing group", lockErr: ErrGroupNotFound},
	}

	for _, state := range states {
		for _, operation := range []string{"update", "delete"} {
			t.Run(state.name+" "+operation, func(t *testing.T) {
				log := &selfServiceGroupCallLog{}
				repo := &selfServiceGroupRepositoryStub{
					log: log, lockState: state.state, lockErr: state.lockErr,
				}
				resolver := &selfServiceGroupResolverStub{log: log, actor: actor}
				service := NewSelfServiceGroupService(
					repo,
					resolver,
					authz.NewPolicyService(&selfServiceGroupPolicyStoreStub{log: log}),
					nil,
					nil,
					nil,
				)

				var err error
				if operation == "update" {
					name := "after"
					_, err = service.UpdateGroup(context.Background(), SelfServiceGroupUpdateInput{
						Actor: actor, GroupID: 74, Name: &name,
					})
				} else {
					_, err = service.DeleteGroup(context.Background(), SelfServiceGroupDeleteInput{
						Actor: actor, GroupID: 74,
					})
				}

				require.ErrorIs(t, err, ErrGroupNotFound)
				require.Equal(t, []string{
					"tx.begin", "lock.actor", "lock.group", "tx.rollback",
				}, log.calls)
				require.Zero(t, resolver.calls)
				require.Zero(t, repo.updateGroupID)
				require.Zero(t, repo.deleteGroupID)
				require.Empty(t, repo.committedEvents)
			})
		}
	}
}

func TestSelfServiceGroupUpdateNoOpSkipsWriteAndEvent(t *testing.T) {
	const (
		userID  int64 = 46
		groupID int64 = 75
	)
	actor, snapshot := selfServiceGroupActorFixture(t, userID, 5)
	ownerID := userID
	log := &selfServiceGroupCallLog{}
	locked := SelfServiceGroupState{
		GroupListItem: GroupListItem{
			ID: groupID, Name: "before", Description: "existing", Platform: PlatformOpenAI,
			Status: StatusActive, OwnerUserID: &ownerID,
		},
		AccessVersion: 7,
	}
	repo := &selfServiceGroupRepositoryStub{log: log, lockState: locked}
	resolver := &selfServiceGroupResolverStub{log: log, actor: actor}
	policyStore := selfServiceGroupOwnedPolicyStore(t, log, snapshot, ownerID, groupID, 7)
	service := NewSelfServiceGroupService(
		repo, resolver, authz.NewPolicyService(policyStore), nil, nil, nil,
	)
	name := " before "
	description := " existing "

	item, err := service.UpdateGroup(context.Background(), SelfServiceGroupUpdateInput{
		Actor: actor, GroupID: groupID, Name: &name, Description: &description,
	})

	require.NoError(t, err)
	require.Equal(t, "before", item.Name)
	require.True(t, repo.committed)
	require.Equal(t, []string{
		"tx.begin", "lock.actor", "lock.group", "actor.resolve", "policy.resource", "tx.commit",
	}, log.calls)
	require.Zero(t, repo.updateGroupID)
	require.Empty(t, repo.committedEvents)
}

func TestSelfServiceGroupUpdateAndDeleteUseLockedVersionAndAppendDurableEvent(t *testing.T) {
	const (
		userID  int64 = 47
		groupID int64 = 76
	)
	actor, snapshot := selfServiceGroupActorFixture(t, userID, 5)
	ownerID := userID

	for _, operation := range []string{"update", "delete"} {
		t.Run(operation, func(t *testing.T) {
			log := &selfServiceGroupCallLog{}
			locked := SelfServiceGroupState{
				GroupListItem: GroupListItem{
					ID: groupID, Name: "before", Description: "existing", Platform: PlatformOpenAI,
					Status: StatusActive, OwnerUserID: &ownerID,
				},
				AccessVersion: 7,
			}
			mutated := locked
			mutated.AccessVersion = 8
			if operation == "update" {
				mutated.Name = "after"
				mutated.Description = "changed"
			} else {
				mutated.Deleted = true
			}
			repo := &selfServiceGroupRepositoryStub{
				log: log, lockState: locked, updateState: mutated, deleteState: mutated,
			}
			resolver := &selfServiceGroupResolverStub{log: log, actor: actor}
			policyStore := selfServiceGroupOwnedPolicyStore(t, log, snapshot, ownerID, groupID, 7)
			service := NewSelfServiceGroupService(
				repo, resolver, authz.NewPolicyService(policyStore), nil, nil, nil,
			)

			var (
				item *GroupListItem
				err  error
			)
			if operation == "update" {
				name := " after "
				description := " changed "
				item, err = service.UpdateGroup(context.Background(), SelfServiceGroupUpdateInput{
					Actor: actor, GroupID: groupID, Name: &name, Description: &description,
					RequestID: "update-76",
				})
			} else {
				item, err = service.DeleteGroup(context.Background(), SelfServiceGroupDeleteInput{
					Actor: actor, GroupID: groupID, RequestID: "delete-76",
				})
			}

			require.NoError(t, err)
			require.NotNil(t, item)
			require.True(t, repo.committed)
			require.Equal(t, []string{
				"tx.begin", "lock.actor", "lock.group", "actor.resolve", "policy.resource",
				"group." + operation, "event.append", "tx.commit",
			}, log.calls)
			require.Len(t, repo.committedEvents, 1)
			event := repo.committedEvents[0]
			require.Equal(t, int64(8), event.ResourceAccessVersion)
			require.Equal(t, &ownerID, event.OwnerUserID)
			require.Equal(t, userID, event.ActorID)
			require.Equal(t, authz.SubjectKindUser, event.ActorKind)
			require.Equal(t, authz.AuthMethodJWT, event.AuthMethod)
			if operation == "update" {
				require.Equal(t, groupID, repo.updateGroupID)
				require.Equal(t, userID, repo.updateOwnerUserID)
				require.Equal(t, int64(7), repo.updateExpectedVersion)
				require.Equal(t, "after", repo.updateName)
				require.Equal(t, "changed", repo.updateDescription)
				require.Equal(t, "group.updated", event.EventType)
				require.Equal(t, []string{"name", "description"}, event.ChangedFields)
				require.Equal(t, "update-76", event.RequestID)
			} else {
				require.Equal(t, groupID, repo.deleteGroupID)
				require.Equal(t, userID, repo.deleteOwnerUserID)
				require.Equal(t, int64(7), repo.deleteExpectedVersion)
				require.Equal(t, "group.deleted", event.EventType)
				require.Equal(t, []string{"lifecycle"}, event.ChangedFields)
				require.Equal(t, "delete-76", event.RequestID)
			}
		})
	}
}

func TestSelfServiceGroupMutationRollsBackWhenDurableEventFails(t *testing.T) {
	const (
		userID  int64 = 48
		groupID int64 = 77
	)
	actor, snapshot := selfServiceGroupActorFixture(t, userID, 1)
	ownerID := userID
	locked := SelfServiceGroupState{
		GroupListItem: GroupListItem{
			ID: groupID, Name: "before", Platform: PlatformOpenAI,
			Status: StatusActive, OwnerUserID: &ownerID,
		},
		AccessVersion: 2,
	}
	updated := locked
	updated.Name = "after"
	updated.AccessVersion = 3
	log := &selfServiceGroupCallLog{}
	repo := &selfServiceGroupRepositoryStub{
		log: log, lockState: locked, updateState: updated,
		appendErr: errors.New("authorization event unavailable"),
	}
	resolver := &selfServiceGroupResolverStub{log: log, actor: actor}
	policyStore := selfServiceGroupOwnedPolicyStore(t, log, snapshot, ownerID, groupID, 2)
	service := NewSelfServiceGroupService(
		repo, resolver, authz.NewPolicyService(policyStore), nil, nil, nil,
	)
	name := "after"

	item, err := service.UpdateGroup(context.Background(), SelfServiceGroupUpdateInput{
		Actor: actor, GroupID: groupID, Name: &name,
	})

	require.Nil(t, item)
	require.ErrorIs(t, err, ErrSelfServiceGroupUnavailable)
	require.True(t, repo.rolledBack)
	require.False(t, repo.committed)
	require.Empty(t, repo.committedEvents)
	require.Equal(t, []string{
		"tx.begin", "lock.actor", "lock.group", "actor.resolve", "policy.resource",
		"group.update", "event.append", "tx.rollback",
	}, log.calls)
}

func mustSelfServiceGroupCatalog(t testing.TB) *SelfServiceGroupCatalog {
	t.Helper()
	catalog, err := NewStaticSelfServiceGroupCatalog([]SelfServiceGroupPlatform{{
		ID: "openai", Name: "OpenAI", Platform: PlatformOpenAI,
	}})
	require.NoError(t, err)
	return catalog
}

func selfServiceGroupOwnedPolicyStore(
	t testing.TB,
	log *selfServiceGroupCallLog,
	snapshot authz.SubjectSnapshot,
	ownerID int64,
	groupID int64,
	accessVersion int64,
) *selfServiceGroupPolicyStoreStub {
	t.Helper()
	ref, err := authz.NewResourceRef(authz.ResourceTypeGroup, groupID)
	require.NoError(t, err)
	resourceSnapshot, err := authz.NewResourceAccessSnapshot(authz.ResourceAccessSnapshotInput{
		Subject: snapshot, Resource: ref, Exists: true, OwnerUserID: &ownerID,
		AccessVersion: accessVersion, GroupAuthorizationMode: authz.GroupAuthorizationModeLegacy,
	})
	require.NoError(t, err)
	return &selfServiceGroupPolicyStoreStub{
		log: log, subject: snapshot, resource: resourceSnapshot,
	}
}

func selfServiceGroupActorFixture(
	t testing.TB,
	userID int64,
	authzVersion int64,
) (authz.Actor, authz.SubjectSnapshot) {
	t.Helper()
	subject, err := authz.NewSubjectRef(authz.SubjectKindUser, userID)
	require.NoError(t, err)
	configuration, err := authz.NewPolicyConfiguration(authz.PolicyConfigurationInput{
		RoleAuthorizationMode:        authz.RoleAuthorizationModeRBAC,
		ResourceAccessControlEnabled: true,
		SelfServiceHostingEnabled:    true,
	})
	require.NoError(t, err)
	snapshot, err := authz.NewSubjectSnapshot(authz.SubjectSnapshotInput{
		Subject: subject, Exists: true, Active: true, AuthzVersion: authzVersion,
		Capabilities:  []authz.Capability{authz.CapabilityGroupCreate},
		Configuration: configuration,
	})
	require.NoError(t, err)
	resolver := authz.NewActorResolver(&selfServiceGroupPolicyStoreStub{subject: snapshot})
	actor, err := resolver.ResolveUser(context.Background(), userID, authz.AuthMethodJWT)
	require.NoError(t, err)
	return actor, snapshot
}
