package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/stretchr/testify/require"
)

type selfServiceAccountTxContextKey struct{}

type selfServiceAccountCallLog struct {
	calls []string
}

func (l *selfServiceAccountCallLog) add(call string) {
	if l != nil {
		l.calls = append(l.calls, call)
	}
}

type selfServiceAccountRepositoryStub struct {
	log *selfServiceAccountCallLog

	lockState   SelfServiceAccountState
	createState SelfServiceAccountState
	renameState SelfServiceAccountState
	deleteState SelfServiceAccountState

	lockActorErr error
	lockErr      error
	createErr    error
	renameErr    error
	deleteErr    error
	appendErr    error
	commitErr    error

	createInput           SelfServiceAccountCreateRecord
	renameAccountID       int64
	renameOwnerUserID     int64
	renameExpectedVersion int64
	renameName            string
	deleteAccountID       int64
	deleteOwnerUserID     int64
	deleteExpectedVersion int64

	stagedEvents    []ResourceAuthorizationEventRecord
	committedEvents []ResourceAuthorizationEventRecord
	txCalls         int
	rolledBack      bool
	committed       bool
}

func (r *selfServiceAccountRepositoryStub) WithSerializableTx(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	r.txCalls++
	r.log.add("tx.begin")
	txCtx := context.WithValue(ctx, selfServiceAccountTxContextKey{}, true)
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

func (r *selfServiceAccountRepositoryStub) LockActorAuthorization(ctx context.Context, _ int64) error {
	assertSelfServiceAccountTxContext(ctx)
	r.log.add("lock.actor")
	return r.lockActorErr
}

func (r *selfServiceAccountRepositoryStub) LockAccount(
	ctx context.Context,
	_ int64,
) (SelfServiceAccountState, error) {
	assertSelfServiceAccountTxContext(ctx)
	r.log.add("lock.account")
	if r.lockErr != nil {
		return SelfServiceAccountState{}, r.lockErr
	}
	return r.lockState, nil
}

func (r *selfServiceAccountRepositoryStub) CreateAccount(
	ctx context.Context,
	input SelfServiceAccountCreateRecord,
) (SelfServiceAccountState, error) {
	assertSelfServiceAccountTxContext(ctx)
	r.log.add("account.create")
	r.createInput = input
	if r.createErr != nil {
		return SelfServiceAccountState{}, r.createErr
	}
	return r.createState, nil
}

func (r *selfServiceAccountRepositoryStub) RenameAccount(
	ctx context.Context,
	accountID int64,
	ownerUserID int64,
	expectedAccessVersion int64,
	name string,
) (SelfServiceAccountState, error) {
	assertSelfServiceAccountTxContext(ctx)
	r.log.add("account.rename")
	r.renameAccountID = accountID
	r.renameOwnerUserID = ownerUserID
	r.renameExpectedVersion = expectedAccessVersion
	r.renameName = name
	if r.renameErr != nil {
		return SelfServiceAccountState{}, r.renameErr
	}
	return r.renameState, nil
}

func (r *selfServiceAccountRepositoryStub) DeleteAccount(
	ctx context.Context,
	accountID int64,
	ownerUserID int64,
	expectedAccessVersion int64,
) (SelfServiceAccountState, error) {
	assertSelfServiceAccountTxContext(ctx)
	r.log.add("account.delete")
	r.deleteAccountID = accountID
	r.deleteOwnerUserID = ownerUserID
	r.deleteExpectedVersion = expectedAccessVersion
	if r.deleteErr != nil {
		return SelfServiceAccountState{}, r.deleteErr
	}
	return r.deleteState, nil
}

func (r *selfServiceAccountRepositoryStub) AppendAuthorizationEvent(
	ctx context.Context,
	event ResourceAuthorizationEventRecord,
) error {
	assertSelfServiceAccountTxContext(ctx)
	r.log.add("event.append")
	if r.appendErr != nil {
		return r.appendErr
	}
	r.stagedEvents = append(r.stagedEvents, event)
	return nil
}

func assertSelfServiceAccountTxContext(ctx context.Context) {
	if ctx == nil || ctx.Value(selfServiceAccountTxContextKey{}) != true {
		panic("self-service account operation executed outside transaction")
	}
}

type selfServiceAccountCapacityStub struct {
	log          *selfServiceAccountCallLog
	err          error
	actor        authz.Actor
	resourceType authz.ResourceType
}

func (s *selfServiceAccountCapacityStub) RequireCreateCapacity(
	ctx context.Context,
	actor authz.Actor,
	resourceType authz.ResourceType,
) (HostingCapacity, error) {
	assertSelfServiceAccountTxContext(ctx)
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

type selfServiceAccountResolverStub struct {
	log   *selfServiceAccountCallLog
	actor authz.Actor
	err   error
	calls int
}

func (s *selfServiceAccountResolverStub) ResolveUser(
	context.Context,
	int64,
	authz.AuthMethod,
) (authz.Actor, error) {
	s.calls++
	s.log.add("actor.resolve")
	return s.actor, s.err
}

func (s *selfServiceAccountResolverStub) ResolveLegacyAdminUser(
	context.Context,
	int64,
) (authz.Actor, error) {
	return authz.Actor{}, errors.New("unexpected legacy admin resolution")
}

func (s *selfServiceAccountResolverStub) ResolveServicePrincipal(
	context.Context,
	string,
	authz.AuthMethod,
) (authz.Actor, error) {
	return authz.Actor{}, errors.New("unexpected service principal resolution")
}

type selfServiceAccountPolicyStoreStub struct {
	log      *selfServiceAccountCallLog
	subject  authz.SubjectSnapshot
	resource authz.ResourceAccessSnapshot
}

func (s *selfServiceAccountPolicyStoreStub) LoadSubjectSnapshot(
	context.Context,
	authz.SubjectRef,
) (authz.SubjectSnapshot, error) {
	s.log.add("policy.subject")
	return s.subject, nil
}

func (s *selfServiceAccountPolicyStoreStub) LoadServicePrincipalSubjectSnapshotByCode(
	context.Context,
	string,
) (authz.SubjectSnapshot, error) {
	return authz.SubjectSnapshot{}, errors.New("unexpected service principal policy lookup")
}

func (s *selfServiceAccountPolicyStoreStub) LoadResourceAccessSnapshot(
	context.Context,
	authz.SubjectRef,
	authz.ResourceRef,
) (authz.ResourceAccessSnapshot, error) {
	s.log.add("policy.resource")
	return s.resource, nil
}

func TestSelfServiceAccountProductionCatalogStartsEmpty(t *testing.T) {
	actor, snapshot := selfServiceAccountActorFixture(t, 41, 1)
	store := &selfServiceAccountPolicyStoreStub{subject: snapshot}
	catalog := NewSelfServiceAccountCatalog()
	service := NewSelfServiceAccountService(
		&selfServiceAccountRepositoryStub{},
		nil,
		authz.NewPolicyService(store),
		&selfServiceAccountCapacityStub{},
		nil,
		catalog,
	)

	products, err := service.ListProducts(context.Background(), actor)
	require.NoError(t, err)
	require.Empty(t, products)
	require.Empty(t, catalog.List())
	_, found := catalog.Resolve("openai-api-key")
	require.False(t, found)

	created, err := service.CreateAccount(context.Background(), SelfServiceAccountCreateInput{
		Actor: actor, Name: "private", ProductID: "openai-api-key", APIKey: "sk-test",
	})
	require.Nil(t, created)
	require.ErrorIs(t, err, ErrSelfServiceAccountProductUnavailable)
	require.Zero(t, service.repository.(*selfServiceAccountRepositoryStub).txCalls)
}

func TestStaticSelfServiceAccountCatalogAcceptsOnlyReviewedAPIKeyProducts(t *testing.T) {
	products := []SelfServiceAccountProduct{
		{ID: " openai-api-key ", Name: " OpenAI API Key ", Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey},
		{ID: "anthropic-api-key", Name: "Anthropic API Key", Platform: PlatformAnthropic, AccountType: AccountTypeAPIKey},
		{ID: "gemini-api-key", Name: "Gemini API Key", Platform: PlatformGemini, AccountType: AccountTypeAPIKey},
	}
	catalog, err := NewStaticSelfServiceAccountCatalog(products)
	require.NoError(t, err)
	require.Len(t, catalog.List(), 3)
	resolved, found := catalog.Resolve(" openai-api-key ")
	require.True(t, found)
	require.Equal(t, SelfServiceAccountProduct{
		ID: "openai-api-key", Name: "OpenAI API Key", Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey,
	}, resolved)

	products[0].Name = "mutated"
	require.Equal(t, "OpenAI API Key", catalog.List()[0].Name)

	for _, invalid := range []SelfServiceAccountProduct{
		{ID: "openai-oauth", Name: "OpenAI OAuth", Platform: PlatformOpenAI, AccountType: AccountTypeOAuth},
		{ID: "grok-api-key", Name: "Grok API Key", Platform: PlatformGrok, AccountType: AccountTypeAPIKey},
		{ID: "control", Name: "bad\nname", Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey},
	} {
		_, err = NewStaticSelfServiceAccountCatalog([]SelfServiceAccountProduct{invalid})
		require.Error(t, err)
	}
	_, err = NewStaticSelfServiceAccountCatalog([]SelfServiceAccountProduct{products[0], products[0]})
	require.Error(t, err)
}

func TestSelfServiceAccountCreateUsesExactPrivateDefaultsAndTransactionOrder(t *testing.T) {
	const userID int64 = 42
	actor, _ := selfServiceAccountActorFixture(t, userID, 3)
	log := &selfServiceAccountCallLog{}
	ownerID := userID
	createdAt := time.Date(2026, 9, 2, 2, 3, 4, 0, time.UTC)
	repo := &selfServiceAccountRepositoryStub{
		log: log,
		createState: SelfServiceAccountState{
			AccountListItem: AccountListItem{
				ID: 71, Name: "Personal OpenAI", Platform: PlatformOpenAI,
				Type: AccountTypeAPIKey, Status: StatusActive, CredentialConfigured: true,
				OwnerUserID: &ownerID, CreatedAt: createdAt, UpdatedAt: createdAt,
			},
			AccessVersion: 1,
		},
	}
	capacity := &selfServiceAccountCapacityStub{log: log}
	catalog := mustSelfServiceAccountCatalog(t)
	service := NewSelfServiceAccountService(repo, nil, nil, capacity, nil, catalog)

	item, err := service.CreateAccount(context.Background(), SelfServiceAccountCreateInput{
		Actor: actor, Name: "  Personal OpenAI  ", ProductID: " openai-api-key ",
		APIKey: "  sk-private  ", RequestID: " request-71 ",
	})

	require.NoError(t, err)
	require.NotNil(t, item)
	require.Equal(t, int64(71), item.ID)
	require.True(t, repo.committed)
	require.False(t, repo.rolledBack)
	require.Equal(t, []string{
		"tx.begin", "capacity.require", "account.create", "event.append", "tx.commit",
	}, log.calls)
	require.True(t, actor.SameAuthorizationState(capacity.actor))
	require.Equal(t, authz.ResourceTypeAccount, capacity.resourceType)
	require.Equal(t, SelfServiceAccountCreateRecord{
		Name: "Personal OpenAI", Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey,
		APIKey: "sk-private", OwnerUserID: userID, CreatorUserID: userID,
	}, repo.createInput)

	require.Len(t, repo.committedEvents, 1)
	event := repo.committedEvents[0]
	require.Equal(t, ResourceMutationKey{ResourceType: authz.ResourceTypeAccount, ResourceID: 71}, event.Key)
	require.Equal(t, &ownerID, event.OwnerUserID)
	require.Equal(t, authz.SubjectKindUser, event.ActorKind)
	require.Equal(t, userID, event.ActorID)
	require.Equal(t, authz.AuthMethodJWT, event.AuthMethod)
	require.Equal(t, "account.created", event.EventType)
	require.Equal(t, int64(1), event.ResourceAccessVersion)
	require.Equal(t, "request-71", event.RequestID)
	require.Equal(t, []string{"configuration", "credentials", "ownership"}, event.ChangedFields)
}

func TestSelfServiceAccountCreateRollsBackWhenDurableEventFails(t *testing.T) {
	actor, _ := selfServiceAccountActorFixture(t, 43, 1)
	ownerID := int64(43)
	eventErr := errors.New("authorization event unavailable")
	log := &selfServiceAccountCallLog{}
	repo := &selfServiceAccountRepositoryStub{
		log: log,
		createState: SelfServiceAccountState{
			AccountListItem: AccountListItem{
				ID: 72, Name: "Personal", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Status: StatusActive, CredentialConfigured: true, OwnerUserID: &ownerID,
			},
			AccessVersion: 1,
		},
		appendErr: eventErr,
	}
	service := NewSelfServiceAccountService(
		repo, nil, nil, &selfServiceAccountCapacityStub{log: log}, nil, mustSelfServiceAccountCatalog(t),
	)

	item, err := service.CreateAccount(context.Background(), SelfServiceAccountCreateInput{
		Actor: actor, Name: "Personal", ProductID: "openai-api-key", APIKey: "sk-private",
	})

	require.Nil(t, item)
	require.ErrorIs(t, err, ErrSelfServiceAccountUnavailable)
	require.True(t, repo.rolledBack)
	require.False(t, repo.committed)
	require.Empty(t, repo.committedEvents)
	require.Equal(t, []string{
		"tx.begin", "capacity.require", "account.create", "event.append", "tx.rollback",
	}, log.calls)
}

func TestSelfServiceAccountMutationRejectsStaleActorBeforePolicyOrWrite(t *testing.T) {
	const userID int64 = 44
	requestActor, _ := selfServiceAccountActorFixture(t, userID, 1)
	currentActor, _ := selfServiceAccountActorFixture(t, userID, 2)
	ownerID := userID
	log := &selfServiceAccountCallLog{}
	repo := &selfServiceAccountRepositoryStub{
		log: log,
		lockState: SelfServiceAccountState{
			AccountListItem: AccountListItem{ID: 73, Name: "before", OwnerUserID: &ownerID},
			AccessVersion:   4,
		},
	}
	resolver := &selfServiceAccountResolverStub{log: log, actor: currentActor}
	policyStore := &selfServiceAccountPolicyStoreStub{log: log}
	service := NewSelfServiceAccountService(repo, resolver, authz.NewPolicyService(policyStore), nil, nil, nil)

	item, err := service.RenameAccount(context.Background(), SelfServiceAccountRenameInput{
		Actor: requestActor, AccountID: 73, Name: "after",
	})

	require.Nil(t, item)
	require.ErrorIs(t, err, ErrSelfServiceAccountConflict)
	require.True(t, repo.rolledBack)
	require.Equal(t, []string{
		"tx.begin", "lock.actor", "lock.account", "actor.resolve", "tx.rollback",
	}, log.calls)
	require.Zero(t, repo.renameAccountID)
	require.Empty(t, repo.committedEvents)
}

func TestSelfServiceAccountMutationsConcealNonOwnedAccounts(t *testing.T) {
	const userID int64 = 45
	actor, _ := selfServiceAccountActorFixture(t, userID, 1)
	otherOwnerID := userID + 1
	ownerID := userID

	states := []struct {
		name    string
		state   SelfServiceAccountState
		lockErr error
	}{
		{
			name: "other owner",
			state: SelfServiceAccountState{
				AccountListItem: AccountListItem{ID: 74, OwnerUserID: &otherOwnerID}, AccessVersion: 1,
			},
		},
		{
			name: "platform account",
			state: SelfServiceAccountState{
				AccountListItem: AccountListItem{ID: 74}, AccessVersion: 1,
			},
		},
		{
			name: "deleted account",
			state: SelfServiceAccountState{
				AccountListItem: AccountListItem{ID: 74, OwnerUserID: &ownerID}, AccessVersion: 2, Deleted: true,
			},
		},
		{name: "missing account", lockErr: ErrAccountNotFound},
	}

	for _, state := range states {
		for _, operation := range []string{"rename", "delete"} {
			t.Run(state.name+" "+operation, func(t *testing.T) {
				log := &selfServiceAccountCallLog{}
				repo := &selfServiceAccountRepositoryStub{log: log, lockState: state.state, lockErr: state.lockErr}
				resolver := &selfServiceAccountResolverStub{log: log, actor: actor}
				service := NewSelfServiceAccountService(
					repo,
					resolver,
					authz.NewPolicyService(&selfServiceAccountPolicyStoreStub{log: log}),
					nil,
					nil,
					nil,
				)

				var err error
				if operation == "rename" {
					_, err = service.RenameAccount(context.Background(), SelfServiceAccountRenameInput{
						Actor: actor, AccountID: 74, Name: "after",
					})
				} else {
					_, err = service.DeleteAccount(context.Background(), SelfServiceAccountDeleteInput{
						Actor: actor, AccountID: 74,
					})
				}

				require.ErrorIs(t, err, ErrAccountNotFound)
				require.Equal(t, []string{
					"tx.begin", "lock.actor", "lock.account", "tx.rollback",
				}, log.calls)
				require.Zero(t, resolver.calls)
				require.Zero(t, repo.renameAccountID)
				require.Zero(t, repo.deleteAccountID)
				require.Empty(t, repo.committedEvents)
			})
		}
	}
}

func TestSelfServiceAccountRenameAndDeleteUseLockedVersionAndAppendDurableEvent(t *testing.T) {
	const (
		userID    int64 = 46
		accountID int64 = 75
	)
	actor, snapshot := selfServiceAccountActorFixture(t, userID, 5)
	ownerID := userID

	for _, operation := range []string{"rename", "delete"} {
		t.Run(operation, func(t *testing.T) {
			log := &selfServiceAccountCallLog{}
			locked := SelfServiceAccountState{
				AccountListItem: AccountListItem{
					ID: accountID, Name: "before", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
					Status: StatusActive, CredentialConfigured: true, OwnerUserID: &ownerID,
				},
				AccessVersion: 7,
			}
			mutated := locked
			mutated.AccessVersion = 8
			if operation == "rename" {
				mutated.Name = "after"
			} else {
				mutated.Deleted = true
			}
			repo := &selfServiceAccountRepositoryStub{
				log: log, lockState: locked, renameState: mutated, deleteState: mutated,
			}
			resolver := &selfServiceAccountResolverStub{log: log, actor: actor}
			ref, err := authz.NewResourceRef(authz.ResourceTypeAccount, accountID)
			require.NoError(t, err)
			resourceSnapshot, err := authz.NewResourceAccessSnapshot(authz.ResourceAccessSnapshotInput{
				Subject: snapshot, Resource: ref, Exists: true, OwnerUserID: &ownerID, AccessVersion: 7,
			})
			require.NoError(t, err)
			policyStore := &selfServiceAccountPolicyStoreStub{
				log: log, subject: snapshot, resource: resourceSnapshot,
			}
			service := NewSelfServiceAccountService(
				repo, resolver, authz.NewPolicyService(policyStore), nil, nil, nil,
			)

			var item *AccountListItem
			if operation == "rename" {
				item, err = service.RenameAccount(context.Background(), SelfServiceAccountRenameInput{
					Actor: actor, AccountID: accountID, Name: " after ", RequestID: "rename-75",
				})
			} else {
				item, err = service.DeleteAccount(context.Background(), SelfServiceAccountDeleteInput{
					Actor: actor, AccountID: accountID, RequestID: "delete-75",
				})
			}

			require.NoError(t, err)
			require.NotNil(t, item)
			require.True(t, repo.committed)
			require.Equal(t, []string{
				"tx.begin", "lock.actor", "lock.account", "actor.resolve", "policy.resource",
				"account." + operation, "event.append", "tx.commit",
			}, log.calls)
			require.Len(t, repo.committedEvents, 1)
			event := repo.committedEvents[0]
			require.Equal(t, int64(8), event.ResourceAccessVersion)
			require.Equal(t, &ownerID, event.OwnerUserID)
			require.Equal(t, userID, event.ActorID)
			require.Equal(t, authz.SubjectKindUser, event.ActorKind)
			require.Equal(t, authz.AuthMethodJWT, event.AuthMethod)
			if operation == "rename" {
				require.Equal(t, accountID, repo.renameAccountID)
				require.Equal(t, userID, repo.renameOwnerUserID)
				require.Equal(t, int64(7), repo.renameExpectedVersion)
				require.Equal(t, "after", repo.renameName)
				require.Equal(t, "account.updated", event.EventType)
				require.Equal(t, []string{"name"}, event.ChangedFields)
				require.Equal(t, "rename-75", event.RequestID)
			} else {
				require.Equal(t, accountID, repo.deleteAccountID)
				require.Equal(t, userID, repo.deleteOwnerUserID)
				require.Equal(t, int64(7), repo.deleteExpectedVersion)
				require.Equal(t, "account.deleted", event.EventType)
				require.Equal(t, []string{"lifecycle"}, event.ChangedFields)
				require.Equal(t, "delete-75", event.RequestID)
			}
		})
	}
}

func mustSelfServiceAccountCatalog(t testing.TB) *SelfServiceAccountCatalog {
	t.Helper()
	catalog, err := NewStaticSelfServiceAccountCatalog([]SelfServiceAccountProduct{{
		ID: "openai-api-key", Name: "OpenAI API Key", Platform: PlatformOpenAI, AccountType: AccountTypeAPIKey,
	}})
	require.NoError(t, err)
	return catalog
}

func selfServiceAccountActorFixture(
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
		Capabilities:  []authz.Capability{authz.CapabilityAccountCreate},
		Configuration: configuration,
	})
	require.NoError(t, err)
	resolver := authz.NewActorResolver(&selfServiceAccountPolicyStoreStub{subject: snapshot})
	actor, err := resolver.ResolveUser(context.Background(), userID, authz.AuthMethodJWT)
	require.NoError(t, err)
	return actor, snapshot
}

func TestBoundedSelfServiceRequestIDTrimsAndLimitsRunes(t *testing.T) {
	value := "  " + strings.Repeat("界", 70) + "  "
	result := boundedSelfServiceRequestID(value)
	require.Len(t, []rune(result), 64)
	require.NotContains(t, result, " ")
}
