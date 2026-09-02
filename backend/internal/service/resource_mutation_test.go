package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type resourceMutationSQLStateTestError struct {
	state string
}

func (e resourceMutationSQLStateTestError) Error() string {
	return "transaction failed with SQLSTATE " + e.state
}

func (e resourceMutationSQLStateTestError) SQLState() string {
	return e.state
}

type resourceMutationRepositoryStub struct {
	states           map[ResourceMutationKey]ResourceMutationState
	txBeginErr       error
	txCommitErr      error
	lockActorErr     error
	lockResourcesErr error
	incrementErr     error
	appendErr        error
	calls            []string
	events           []ResourceAuthorizationEventRecord
	incremented      []ResourceMutationKey
	lockedActor      authz.SubjectKind
	lockedID         int64
	inTx             bool
	rolledBack       bool
}

func (s *resourceMutationRepositoryStub) WithSerializableTx(ctx context.Context, fn func(context.Context) error) error {
	s.calls = append(s.calls, "tx")
	if s.txBeginErr != nil {
		return s.txBeginErr
	}
	s.inTx = true
	defer func() { s.inTx = false }()
	if err := fn(ctx); err != nil {
		s.rolledBack = true
		return err
	}
	return s.txCommitErr
}

func (s *resourceMutationRepositoryStub) LockActorAuthorization(_ context.Context, kind authz.SubjectKind, id int64) error {
	s.calls = append(s.calls, "actor")
	s.lockedActor, s.lockedID = kind, id
	return s.lockActorErr
}

func (s *resourceMutationRepositoryStub) LockResources(_ context.Context, keys []ResourceMutationKey) (map[ResourceMutationKey]ResourceMutationState, error) {
	s.calls = append(s.calls, "resources")
	if s.lockResourcesErr != nil {
		return nil, s.lockResourcesErr
	}
	result := make(map[ResourceMutationKey]ResourceMutationState, len(keys))
	for _, key := range keys {
		if state, ok := s.states[key]; ok {
			result[key] = state
		}
	}
	return result, nil
}

func (s *resourceMutationRepositoryStub) IncrementAccessVersions(_ context.Context, keys []ResourceMutationKey) (map[ResourceMutationKey]ResourceMutationState, error) {
	s.calls = append(s.calls, "versions")
	if s.incrementErr != nil {
		return nil, s.incrementErr
	}
	s.incremented = append([]ResourceMutationKey(nil), keys...)
	result := make(map[ResourceMutationKey]ResourceMutationState, len(keys))
	for _, key := range keys {
		state := s.states[key]
		state.AccessVersion++
		s.states[key] = state
		result[key] = state
	}
	return result, nil
}

func (s *resourceMutationRepositoryStub) AppendAuthorizationEvents(_ context.Context, events []ResourceAuthorizationEventRecord) error {
	s.calls = append(s.calls, "audit")
	s.events = append([]ResourceAuthorizationEventRecord(nil), events...)
	return s.appendErr
}

type resourceMutationResolverStub struct {
	actor authz.Actor
	err   error
}

func (s resourceMutationResolverStub) ResolveUser(context.Context, int64, authz.AuthMethod) (authz.Actor, error) {
	return s.actor, s.err
}

func (s resourceMutationResolverStub) ResolveLegacyAdminUser(context.Context, int64) (authz.Actor, error) {
	return s.actor, s.err
}

func (s resourceMutationResolverStub) ResolveServicePrincipal(context.Context, string, authz.AuthMethod) (authz.Actor, error) {
	return s.actor, s.err
}

type resourceMutationPolicyStoreStub struct {
	subject   authz.SubjectSnapshot
	resource  authz.ResourceAccessSnapshot
	resources map[authz.ResourceRef]authz.ResourceAccessSnapshot
}

func TestNewAdminServiceFailsClosedWithoutResourceMutationCoordinator(t *testing.T) {
	adminService := NewAdminService(
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
	)

	created, err := adminService.CreateAccount(
		context.Background(),
		adminResourceUserTestActor(t),
		&CreateAccountInput{SkipDefaultGroupBind: true},
	)

	require.Nil(t, created)
	require.ErrorIs(t, err, ErrResourceMutationUnavailable)
}

func (s resourceMutationPolicyStoreStub) LoadSubjectSnapshot(context.Context, authz.SubjectRef) (authz.SubjectSnapshot, error) {
	return s.subject, nil
}

func (s resourceMutationPolicyStoreStub) LoadResourceAccessSnapshot(_ context.Context, _ authz.SubjectRef, ref authz.ResourceRef) (authz.ResourceAccessSnapshot, error) {
	if s.resources != nil {
		return s.resources[ref], nil
	}
	return s.resource, nil
}

func (s resourceMutationPolicyStoreStub) LoadServicePrincipalSubjectSnapshotByCode(context.Context, string) (authz.SubjectSnapshot, error) {
	return s.subject, nil
}

func TestResourceMutationCoordinatorCommitsTrustedActorMutationVersionAuditAndAfterCommit(t *testing.T) {
	const credentialValue = "sk-sensitive-credential-value"
	for _, testCase := range []struct {
		name       string
		actor      authz.Actor
		kind       authz.SubjectKind
		actorID    int64
		authMethod authz.AuthMethod
	}{
		{
			name:       "jwt user",
			actor:      adminResourceTestActor(t, authz.SubjectKindUser, 41),
			kind:       authz.SubjectKindUser,
			actorID:    41,
			authMethod: authz.AuthMethodJWT,
		},
		{
			name:       "admin api key service principal",
			actor:      adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 73),
			kind:       authz.SubjectKindServicePrincipal,
			actorID:    73,
			authMethod: authz.AuthMethodAdminAPIKey,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			actor := testCase.actor
			ref, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, true)
			key := ResourceMutationKeyFromRef(ref)
			repo := &resourceMutationRepositoryStub{states: map[ResourceMutationKey]ResourceMutationState{
				key: {Key: key, AccessVersion: 3},
			}}
			coordinator := NewResourceMutationCoordinator(
				repo,
				resourceMutationResolverStub{actor: actor},
				authz.NewPolicyService(resourceMutationPolicyStoreStub{subject: subjectSnapshot, resource: resourceSnapshot}),
			)
			ctx := WithResourceMutationAuditTrace(context.Background(), ResourceMutationAuditTrace{
				RequestID:   "request-17",
				RequestBody: `{"credentials":{"api_key":"` + credentialValue + `"}}`,
			})
			mutateCalls := 0
			afterCommitCalls := 0
			afterCommitRanInTx := false
			auditMarkedBeforeCallback := false
			err := coordinator.Execute(ctx, actor, ResourceMutationCommand{Targets: []ResourceMutationTarget{{
				Ref:                   ref,
				Action:                authz.ActionAccountEdit,
				ExpectedAccessVersion: 3,
				Mutates:               true,
				EventType:             "account.updated",
				ChangedFields:         []string{"credentials", "configuration", "credentials"},
			}}}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
				mutateCalls++
				afterResourceMutationCommit(txCtx, func() {
					afterCommitCalls++
					afterCommitRanInTx = repo.inTx
					auditMarkedBeforeCallback = ResourceMutationAuditCommitted(ctx)
					repo.calls = append(repo.calls, "after_commit")
				})
				require.Zero(t, afterCommitCalls)
				return nil, nil
			})

			require.NoError(t, err)
			require.Equal(t, 1, mutateCalls)
			require.Equal(t, 1, afterCommitCalls)
			require.False(t, afterCommitRanInTx)
			require.True(t, auditMarkedBeforeCallback)
			require.True(t, ResourceMutationAuditCommitted(ctx))
			require.Equal(t, []string{"tx", "actor", "resources", "versions", "audit", "after_commit"}, repo.calls)
			require.Equal(t, testCase.kind, repo.lockedActor)
			require.Equal(t, testCase.actorID, repo.lockedID)
			require.Equal(t, []ResourceMutationKey{key}, repo.incremented)
			require.Len(t, repo.events, 1)

			event := repo.events[0]
			require.Equal(t, testCase.kind, event.ActorKind)
			require.Equal(t, testCase.actorID, event.ActorID)
			require.Equal(t, testCase.authMethod, event.AuthMethod)
			require.Equal(t, "request-17", event.RequestID)
			require.EqualValues(t, 4, event.ResourceAccessVersion)
			require.Equal(t, []string{"configuration", "credentials"}, event.ChangedFields)

			encodedEvent, encodeErr := json.Marshal(event)
			require.NoError(t, encodeErr)
			require.NotContains(t, string(encodedEvent), credentialValue)
			details, detailsErr := json.Marshal(map[string]any{
				"changed_fields": event.ChangedFields,
				"result":         "success",
			})
			require.NoError(t, detailsErr)
			require.JSONEq(t, `{"changed_fields":["configuration","credentials"],"result":"success"}`, string(details))
		})
	}
}

func TestResourceMutationCoordinatorContainsAfterCommitPanicsAndContinues(t *testing.T) {
	actor := adminResourceUserTestActor(t)
	ref, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, true)
	key := ResourceMutationKeyFromRef(ref)
	repo := &resourceMutationRepositoryStub{states: map[ResourceMutationKey]ResourceMutationState{
		key: {Key: key, AccessVersion: 3},
	}}
	coordinator := NewResourceMutationCoordinator(
		repo,
		resourceMutationResolverStub{actor: actor},
		authz.NewPolicyService(resourceMutationPolicyStoreStub{subject: subjectSnapshot, resource: resourceSnapshot}),
	)
	ctx := WithResourceMutationAuditTrace(context.Background(), ResourceMutationAuditTrace{RequestID: "callback-panic"})
	callbacks := make([]string, 0, 2)

	require.NotPanics(t, func() {
		err := coordinator.Execute(ctx, actor, ResourceMutationCommand{Targets: []ResourceMutationTarget{{
			Ref: ref, Action: authz.ActionAccountEdit, ExpectedAccessVersion: 3, Mutates: true,
			EventType: "account.updated", ChangedFields: []string{"configuration"},
		}}}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
			afterResourceMutationCommit(txCtx, func() { callbacks = append(callbacks, "before") })
			afterResourceMutationCommit(txCtx, func() { panic("callback failure") })
			afterResourceMutationCommit(txCtx, func() { callbacks = append(callbacks, "after") })
			return nil, nil
		})
		require.NoError(t, err)
	})

	require.Equal(t, []string{"before", "after"}, callbacks)
	require.True(t, ResourceMutationAuditCommitted(ctx))
}

func TestResourceMutationCoordinatorRejectsChangedOrInactiveActorBeforeMutation(t *testing.T) {
	actor := adminResourceTestActor(t, authz.SubjectKindUser, 41)
	changedActor := resourceMutationTestActor(t, authz.SubjectKindUser, 41, 2)

	for _, testCase := range []struct {
		name     string
		resolver resourceMutationResolverStub
	}{
		{name: "authorization snapshot changed", resolver: resourceMutationResolverStub{actor: changedActor}},
		{name: "actor inactive", resolver: resourceMutationResolverStub{err: authz.ErrActorInactive}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ref, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, true)
			key := ResourceMutationKeyFromRef(ref)
			repo := &resourceMutationRepositoryStub{states: map[ResourceMutationKey]ResourceMutationState{
				key: {Key: key, AccessVersion: 3},
			}}
			coordinator := NewResourceMutationCoordinator(
				repo,
				testCase.resolver,
				authz.NewPolicyService(resourceMutationPolicyStoreStub{subject: subjectSnapshot, resource: resourceSnapshot}),
			)
			mutateCalls := 0
			err := coordinator.Execute(context.Background(), actor, ResourceMutationCommand{Targets: []ResourceMutationTarget{{
				Ref: ref, Action: authz.ActionAccountEdit, Mutates: true,
				EventType: "account.updated", ChangedFields: []string{"configuration"},
			}}}, func(context.Context) ([]CreatedResourceMutation, error) {
				mutateCalls++
				return nil, nil
			})

			require.ErrorIs(t, err, ErrResourceMutationConflict)
			require.Zero(t, mutateCalls)
			require.Empty(t, repo.incremented)
			require.Empty(t, repo.events)
			require.Equal(t, []string{"tx", "actor"}, repo.calls)
		})
	}
}

func TestResourceMutationCoordinatorRejectsAccessVersionConflictWithoutWrites(t *testing.T) {
	actor := adminResourceUserTestActor(t)
	ref, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, true)
	key := ResourceMutationKeyFromRef(ref)
	repo := &resourceMutationRepositoryStub{states: map[ResourceMutationKey]ResourceMutationState{
		key: {Key: key, AccessVersion: 4},
	}}
	coordinator := NewResourceMutationCoordinator(
		repo,
		resourceMutationResolverStub{actor: actor},
		authz.NewPolicyService(resourceMutationPolicyStoreStub{subject: subjectSnapshot, resource: resourceSnapshot}),
	)
	mutateCalls := 0
	err := coordinator.Execute(context.Background(), actor, ResourceMutationCommand{Targets: []ResourceMutationTarget{{
		Ref: ref, Action: authz.ActionAccountEdit, ExpectedAccessVersion: 3, Mutates: true,
		EventType: "account.updated", ChangedFields: []string{"configuration"},
	}}}, func(context.Context) ([]CreatedResourceMutation, error) {
		mutateCalls++
		return nil, nil
	})

	require.ErrorIs(t, err, ErrResourceMutationConflict)
	require.Zero(t, mutateCalls)
	require.Empty(t, repo.incremented)
	require.Empty(t, repo.events)
	require.Equal(t, []string{"tx", "actor", "resources"}, repo.calls)
}

func TestResourceMutationCoordinatorRejectsForbiddenPolicyWithoutWrites(t *testing.T) {
	actor := adminResourceUserTestActor(t)
	ref, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixturesWithLegacyAdmin(t, actor, true, false)
	key := ResourceMutationKeyFromRef(ref)
	repo := &resourceMutationRepositoryStub{states: map[ResourceMutationKey]ResourceMutationState{
		key: {Key: key, AccessVersion: 3},
	}}
	coordinator := NewResourceMutationCoordinator(
		repo,
		resourceMutationResolverStub{actor: actor},
		authz.NewPolicyService(resourceMutationPolicyStoreStub{subject: subjectSnapshot, resource: resourceSnapshot}),
	)
	mutateCalls := 0
	err := coordinator.Execute(context.Background(), actor, ResourceMutationCommand{Targets: []ResourceMutationTarget{{
		Ref: ref, Action: authz.ActionAccountEdit, Mutates: true,
		EventType: "account.updated", ChangedFields: []string{"configuration"},
	}}}, func(context.Context) ([]CreatedResourceMutation, error) {
		mutateCalls++
		return nil, nil
	})

	require.ErrorIs(t, err, ErrResourceMutationForbidden)
	require.Zero(t, mutateCalls)
	require.Empty(t, repo.incremented)
	require.Empty(t, repo.events)
	require.Equal(t, []string{"tx", "actor", "resources"}, repo.calls)
}

func TestResourceMutationCoordinatorRejectsInvisibleBatchWithoutSideEffects(t *testing.T) {
	actor := adminResourceUserTestActor(t)
	ref, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, false)
	key := ResourceMutationKeyFromRef(ref)
	repo := &resourceMutationRepositoryStub{states: map[ResourceMutationKey]ResourceMutationState{
		key: {Key: key, AccessVersion: 3},
	}}
	coordinator := NewResourceMutationCoordinator(
		repo,
		resourceMutationResolverStub{actor: actor},
		authz.NewPolicyService(resourceMutationPolicyStoreStub{subject: subjectSnapshot, resource: resourceSnapshot}),
	)
	mutated := false
	err := coordinator.Execute(context.Background(), actor, ResourceMutationCommand{Targets: []ResourceMutationTarget{{
		Ref: ref, Action: authz.ActionAccountEdit, Mutates: true,
		EventType: "account.updated", ChangedFields: []string{"configuration"},
	}}}, func(context.Context) ([]CreatedResourceMutation, error) {
		mutated = true
		return nil, nil
	})

	require.ErrorIs(t, err, ErrAccountNotFound)
	require.False(t, mutated)
	require.Empty(t, repo.incremented)
	require.Empty(t, repo.events)
	require.Equal(t, []string{"tx", "actor", "resources"}, repo.calls)
}

func TestResourceMutationCoordinatorAuditFailureDoesNotMarkCommitOrRunCallbacks(t *testing.T) {
	actor := adminResourceUserTestActor(t)
	ref, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, true)
	key := ResourceMutationKeyFromRef(ref)
	auditErr := errors.New("durable audit unavailable")
	repo := &resourceMutationRepositoryStub{
		states:    map[ResourceMutationKey]ResourceMutationState{key: {Key: key, AccessVersion: 3}},
		appendErr: auditErr,
	}
	coordinator := NewResourceMutationCoordinator(
		repo,
		resourceMutationResolverStub{actor: actor},
		authz.NewPolicyService(resourceMutationPolicyStoreStub{subject: subjectSnapshot, resource: resourceSnapshot}),
	)
	ctx := WithResourceMutationAuditTrace(context.Background(), ResourceMutationAuditTrace{RequestID: "failed"})
	afterCommit := false
	err := coordinator.Execute(ctx, actor, ResourceMutationCommand{Targets: []ResourceMutationTarget{{
		Ref: ref, Action: authz.ActionAccountEdit, Mutates: true,
		EventType: "account.updated", ChangedFields: []string{"configuration"},
	}}}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		afterResourceMutationCommit(txCtx, func() { afterCommit = true })
		return nil, nil
	})

	require.ErrorIs(t, err, auditErr)
	require.ErrorIs(t, err, ErrResourceMutationUnavailable)
	require.False(t, afterCommit)
	require.False(t, ResourceMutationAuditCommitted(ctx))
}

func TestResourceMutationCoordinatorMapsRepositoryInfrastructureFailuresToUnavailable(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		configure func(*resourceMutationRepositoryStub, error)
	}{
		{
			name: "transaction begin",
			configure: func(repo *resourceMutationRepositoryStub, failure error) {
				repo.txBeginErr = failure
			},
		},
		{
			name: "transaction commit",
			configure: func(repo *resourceMutationRepositoryStub, failure error) {
				repo.txCommitErr = failure
			},
		},
		{
			name: "resource lock",
			configure: func(repo *resourceMutationRepositoryStub, failure error) {
				repo.lockResourcesErr = failure
			},
		},
		{
			name: "access version increment",
			configure: func(repo *resourceMutationRepositoryStub, failure error) {
				repo.incrementErr = failure
			},
		},
		{
			name: "authorization event append",
			configure: func(repo *resourceMutationRepositoryStub, failure error) {
				repo.appendErr = failure
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			actor := adminResourceUserTestActor(t)
			ref, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, true)
			key := ResourceMutationKeyFromRef(ref)
			failure := errors.New(testCase.name + " failed")
			repo := &resourceMutationRepositoryStub{states: map[ResourceMutationKey]ResourceMutationState{
				key: {Key: key, AccessVersion: 3},
			}}
			testCase.configure(repo, failure)
			coordinator := NewResourceMutationCoordinator(
				repo,
				resourceMutationResolverStub{actor: actor},
				authz.NewPolicyService(resourceMutationPolicyStoreStub{subject: subjectSnapshot, resource: resourceSnapshot}),
			)
			ctx := WithResourceMutationAuditTrace(context.Background(), ResourceMutationAuditTrace{RequestID: "failed"})
			afterCommit := false
			err := coordinator.Execute(ctx, actor, ResourceMutationCommand{Targets: []ResourceMutationTarget{{
				Ref: ref, Action: authz.ActionAccountEdit, ExpectedAccessVersion: 3, Mutates: true,
				EventType: "account.updated", ChangedFields: []string{"configuration"},
			}}}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
				afterResourceMutationCommit(txCtx, func() { afterCommit = true })
				return nil, nil
			})

			require.ErrorIs(t, err, ErrResourceMutationUnavailable)
			require.ErrorIs(t, err, failure)
			statusCode, body := infraerrors.ToHTTP(err)
			require.Equal(t, http.StatusServiceUnavailable, statusCode)
			require.Equal(t, ErrResourceMutationUnavailable.Reason, body.Reason)
			require.False(t, afterCommit)
			require.False(t, ResourceMutationAuditCommitted(ctx))
		})
	}
}

func TestResourceMutationCoordinatorPreservesCommandAndStableServiceErrors(t *testing.T) {
	actor := adminResourceUserTestActor(t)
	ref, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, true)
	key := ResourceMutationKeyFromRef(ref)
	plainDomainErr := errors.New("group input validation failed")

	for _, testCase := range []struct {
		name        string
		commandErr  error
		transaction bool
	}{
		{name: "plain command error", commandErr: plainDomainErr},
		{name: "application command error", commandErr: ErrGroupExists},
		{name: "transaction service error", commandErr: ErrGroupExists, transaction: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &resourceMutationRepositoryStub{states: map[ResourceMutationKey]ResourceMutationState{
				key: {Key: key, AccessVersion: 3},
			}}
			if testCase.transaction {
				repo.txBeginErr = testCase.commandErr
			}
			coordinator := NewResourceMutationCoordinator(
				repo,
				resourceMutationResolverStub{actor: actor},
				authz.NewPolicyService(resourceMutationPolicyStoreStub{subject: subjectSnapshot, resource: resourceSnapshot}),
			)
			err := coordinator.Execute(context.Background(), actor, ResourceMutationCommand{Targets: []ResourceMutationTarget{{
				Ref: ref, Action: authz.ActionAccountEdit, Mutates: true,
				EventType: "account.updated", ChangedFields: []string{"configuration"},
			}}}, func(context.Context) ([]CreatedResourceMutation, error) {
				return nil, testCase.commandErr
			})

			require.ErrorIs(t, err, testCase.commandErr)
			if testCase.commandErr == plainDomainErr {
				require.NotErrorIs(t, err, ErrResourceMutationUnavailable)
			}
		})
	}
}

func TestResourceMutationCoordinatorMapsCommandSQLFailures(t *testing.T) {
	actor := adminResourceUserTestActor(t)
	ref, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, true)
	key := ResourceMutationKeyFromRef(ref)

	for _, testCase := range []struct {
		name       string
		sqlState   string
		wantStatus int
		wantError  error
	}{
		{name: "constraint or outbox failure", sqlState: "23514", wantStatus: http.StatusServiceUnavailable, wantError: ErrResourceMutationUnavailable},
		{name: "serialization failure", sqlState: "40001", wantStatus: http.StatusConflict, wantError: ErrResourceMutationConflict},
		{name: "deadlock", sqlState: "40P01", wantStatus: http.StatusConflict, wantError: ErrResourceMutationConflict},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &resourceMutationRepositoryStub{states: map[ResourceMutationKey]ResourceMutationState{
				key: {Key: key, AccessVersion: 3},
			}}
			coordinator := NewResourceMutationCoordinator(
				repo,
				resourceMutationResolverStub{actor: actor},
				authz.NewPolicyService(resourceMutationPolicyStoreStub{subject: subjectSnapshot, resource: resourceSnapshot}),
			)
			failure := resourceMutationSQLStateTestError{state: testCase.sqlState}
			err := coordinator.Execute(context.Background(), actor, ResourceMutationCommand{Targets: []ResourceMutationTarget{{
				Ref: ref, Action: authz.ActionAccountEdit, Mutates: true,
				EventType: "account.updated", ChangedFields: []string{"configuration"},
			}}}, func(context.Context) ([]CreatedResourceMutation, error) {
				return nil, failure
			})

			require.ErrorIs(t, err, testCase.wantError)
			require.ErrorIs(t, err, failure)
			statusCode, _ := infraerrors.ToHTTP(err)
			require.Equal(t, testCase.wantStatus, statusCode)
		})
	}
}

func TestResourceMutationCoordinatorMapsSerializationAndDeadlockToConflict(t *testing.T) {
	for _, sqlState := range []string{"40001", "40P01"} {
		t.Run(sqlState, func(t *testing.T) {
			actor := adminResourceUserTestActor(t)
			_, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, true)
			repo := &resourceMutationRepositoryStub{
				states:     make(map[ResourceMutationKey]ResourceMutationState),
				txBeginErr: resourceMutationSQLStateTestError{state: sqlState},
			}
			coordinator := NewResourceMutationCoordinator(
				repo,
				resourceMutationResolverStub{actor: actor},
				authz.NewPolicyService(resourceMutationPolicyStoreStub{subject: subjectSnapshot, resource: resourceSnapshot}),
			)
			err := coordinator.Execute(context.Background(), actor, ResourceMutationCommand{}, func(context.Context) ([]CreatedResourceMutation, error) {
				return nil, nil
			})

			require.ErrorIs(t, err, ErrResourceMutationConflict)
			statusCode, _ := infraerrors.ToHTTP(err)
			require.Equal(t, http.StatusConflict, statusCode)
		})
	}
}

func TestResourceMutationCoordinatorNoopDoesNotVersionAuditOrRunCallbacks(t *testing.T) {
	actor := adminResourceUserTestActor(t)
	ref, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, true)
	key := ResourceMutationKeyFromRef(ref)
	repo := &resourceMutationRepositoryStub{states: map[ResourceMutationKey]ResourceMutationState{
		key: {Key: key, AccessVersion: 3},
	}}
	coordinator := NewResourceMutationCoordinator(
		repo,
		resourceMutationResolverStub{actor: actor},
		authz.NewPolicyService(resourceMutationPolicyStoreStub{subject: subjectSnapshot, resource: resourceSnapshot}),
	)
	ctx := WithResourceMutationAuditTrace(context.Background(), ResourceMutationAuditTrace{RequestID: "duplicate-replay"})
	afterCommit := false
	err := coordinator.Execute(ctx, actor, ResourceMutationCommand{
		CreateResourceTypes: []authz.ResourceType{authz.ResourceTypeGroup},
		Targets: []ResourceMutationTarget{{
			Ref: ref, Action: authz.ActionAccountView, ExpectedAccessVersion: 3,
		}},
	}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		afterResourceMutationCommit(txCtx, func() { afterCommit = true })
		return nil, errResourceMutationNoop
	})

	require.NoError(t, err)
	require.EqualValues(t, 3, repo.states[key].AccessVersion)
	require.Empty(t, repo.incremented)
	require.Empty(t, repo.events)
	require.Equal(t, []string{"tx", "actor", "resources"}, repo.calls)
	require.True(t, repo.rolledBack)
	require.False(t, afterCommit)
	require.False(t, ResourceMutationAuditCommitted(ctx))
}

func TestResourceMutationCoordinatorEmptyMutationIsNoop(t *testing.T) {
	actor := adminResourceUserTestActor(t)
	_, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, true)
	repo := &resourceMutationRepositoryStub{states: make(map[ResourceMutationKey]ResourceMutationState)}
	coordinator := NewResourceMutationCoordinator(
		repo,
		resourceMutationResolverStub{actor: actor},
		authz.NewPolicyService(resourceMutationPolicyStoreStub{subject: subjectSnapshot, resource: resourceSnapshot}),
	)
	ctx := WithResourceMutationAuditTrace(context.Background(), ResourceMutationAuditTrace{RequestID: "empty-mutation"})
	afterCommit := false
	err := coordinator.Execute(ctx, actor, ResourceMutationCommand{}, func(txCtx context.Context) ([]CreatedResourceMutation, error) {
		afterResourceMutationCommit(txCtx, func() { afterCommit = true })
		return nil, nil
	})

	require.NoError(t, err)
	require.Empty(t, repo.incremented)
	require.Empty(t, repo.events)
	require.Equal(t, []string{"tx", "actor", "resources"}, repo.calls)
	require.True(t, repo.rolledBack)
	require.False(t, afterCommit)
	require.False(t, ResourceMutationAuditCommitted(ctx))
}

func TestResourceMutationCoordinatorDoesNotVersionAuthorizationOnlyTargets(t *testing.T) {
	actor := adminResourceUserTestActor(t)
	mutatedRef, subjectSnapshot, mutatedSnapshot := resourceMutationPolicyFixtures(t, actor, true)
	authorizationOnlyRef, err := authz.NewResourceRef(authz.ResourceTypeGroup, 8)
	require.NoError(t, err)
	authorizationOnlySnapshot, err := authz.NewResourceAccessSnapshot(authz.ResourceAccessSnapshotInput{
		Subject:                subjectSnapshot,
		Resource:               authorizationOnlyRef,
		GroupAuthorizationMode: authz.GroupAuthorizationModeLegacy,
		Exists:                 true,
		AccessVersion:          9,
	})
	require.NoError(t, err)
	mutatedKey := ResourceMutationKeyFromRef(mutatedRef)
	authorizationOnlyKey := ResourceMutationKeyFromRef(authorizationOnlyRef)
	repo := &resourceMutationRepositoryStub{states: map[ResourceMutationKey]ResourceMutationState{
		mutatedKey:           {Key: mutatedKey, AccessVersion: 3},
		authorizationOnlyKey: {Key: authorizationOnlyKey, AccessVersion: 9},
	}}
	coordinator := NewResourceMutationCoordinator(
		repo,
		resourceMutationResolverStub{actor: actor},
		authz.NewPolicyService(resourceMutationPolicyStoreStub{
			subject: subjectSnapshot,
			resources: map[authz.ResourceRef]authz.ResourceAccessSnapshot{
				mutatedRef:           mutatedSnapshot,
				authorizationOnlyRef: authorizationOnlySnapshot,
			},
		}),
	)
	err = coordinator.Execute(context.Background(), actor, ResourceMutationCommand{Targets: []ResourceMutationTarget{
		{
			Ref: mutatedRef, Action: authz.ActionAccountEdit, ExpectedAccessVersion: 3, Mutates: true,
			EventType: "account.updated", ChangedFields: []string{"configuration"},
		},
		{
			Ref: authorizationOnlyRef, Action: authz.ActionGroupView, ExpectedAccessVersion: 9,
		},
	}}, func(context.Context) ([]CreatedResourceMutation, error) {
		return nil, nil
	})

	require.NoError(t, err)
	require.Equal(t, []ResourceMutationKey{mutatedKey}, repo.incremented)
	require.EqualValues(t, 4, repo.states[mutatedKey].AccessVersion)
	require.EqualValues(t, 9, repo.states[authorizationOnlyKey].AccessVersion)
	require.Len(t, repo.events, 1)
	require.Equal(t, mutatedKey, repo.events[0].Key)
}

func TestResourceMutationCoordinatorBlocksOnlyAccessExpansionWhenPropagationIsDegraded(t *testing.T) {
	actor := adminResourceUserTestActor(t)
	_, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, true)
	repo := &resourceMutationRepositoryStub{states: make(map[ResourceMutationKey]ResourceMutationState)}
	coordinator := NewResourceMutationCoordinator(
		repo,
		resourceMutationResolverStub{actor: actor},
		authz.NewPolicyService(resourceMutationPolicyStoreStub{subject: subjectSnapshot, resource: resourceSnapshot}),
	)
	coordinator.propagationGuard = newAuthorizationPropagationGuard(
		authorizationPropagationStatsStub{err: errors.New("stats unavailable")},
		authorizationPropagationWorkerStub{name: "auth_cache_invalidation", running: true},
		authorizationPropagationWorkerStub{name: "scheduler_outbox", running: true},
		authorizationPropagationWorkerStub{name: "authorization_expiry", running: true},
	)

	mutateCalls := 0
	err := coordinator.Execute(context.Background(), actor, ResourceMutationCommand{
		ExpandsAccess: true,
	}, func(context.Context) ([]CreatedResourceMutation, error) {
		mutateCalls++
		return nil, nil
	})
	require.ErrorIs(t, err, ErrAuthorizationPropagationDegraded)
	require.Zero(t, mutateCalls)
	require.Empty(t, repo.calls)

	err = coordinator.Execute(context.Background(), actor, ResourceMutationCommand{}, func(context.Context) ([]CreatedResourceMutation, error) {
		mutateCalls++
		return nil, errResourceMutationNoop
	})
	require.NoError(t, err)
	require.Equal(t, 1, mutateCalls)
	require.Equal(t, []string{"tx", "actor", "resources"}, repo.calls)
}

func TestResourceMutationCoordinatorExpansionFailsClosedWithoutPropagationGuard(t *testing.T) {
	actor := adminResourceUserTestActor(t)
	_, subjectSnapshot, resourceSnapshot := resourceMutationPolicyFixtures(t, actor, true)
	coordinator := NewResourceMutationCoordinator(
		&resourceMutationRepositoryStub{states: make(map[ResourceMutationKey]ResourceMutationState)},
		resourceMutationResolverStub{actor: actor},
		authz.NewPolicyService(resourceMutationPolicyStoreStub{subject: subjectSnapshot, resource: resourceSnapshot}),
	)

	err := coordinator.Execute(context.Background(), actor, ResourceMutationCommand{ExpandsAccess: true}, func(context.Context) ([]CreatedResourceMutation, error) {
		return nil, nil
	})
	require.ErrorIs(t, err, ErrAuthorizationPropagationDegraded)
}

func TestResourceMutationAuditTraceIsBounded(t *testing.T) {
	ctx := WithResourceMutationAuditTrace(context.Background(), ResourceMutationAuditTrace{
		Method:      strings.Repeat("m", 20),
		Path:        strings.Repeat("p", 600),
		RequestID:   strings.Repeat("r", 80),
		ClientIP:    strings.Repeat("i", 80),
		UserAgent:   strings.Repeat("u", 600),
		RequestBody: strings.Repeat("b", auditRequestBodyMaxBytes+100),
	})
	state, ok := ctx.Value(resourceMutationAuditContextKey{}).(*resourceMutationAuditContext)
	require.True(t, ok)
	require.Len(t, state.trace.Method, 16)
	require.Len(t, state.trace.Path, 512)
	require.Len(t, state.trace.RequestID, 64)
	require.Len(t, state.trace.ClientIP, 64)
	require.Len(t, state.trace.UserAgent, 512)
	require.Len(t, state.trace.RequestBody, auditRequestBodyMaxBytes)
}

func resourceMutationPolicyFixtures(
	t testing.TB,
	actor authz.Actor,
	resourceExists bool,
) (authz.ResourceRef, authz.SubjectSnapshot, authz.ResourceAccessSnapshot) {
	t.Helper()
	kind, _, ok := actor.DurableSubject()
	require.True(t, ok)
	return resourceMutationPolicyFixturesWithLegacyAdmin(
		t,
		actor,
		resourceExists,
		kind == authz.SubjectKindUser,
	)
}

func resourceMutationPolicyFixturesWithLegacyAdmin(
	t testing.TB,
	actor authz.Actor,
	resourceExists bool,
	currentLegacyAdmin bool,
) (authz.ResourceRef, authz.SubjectSnapshot, authz.ResourceAccessSnapshot) {
	t.Helper()
	kind, id, ok := actor.DurableSubject()
	require.True(t, ok)
	subject, err := authz.NewSubjectRef(kind, id)
	require.NoError(t, err)
	configuration, err := authz.NewPolicyConfiguration(authz.PolicyConfigurationInput{
		RoleAuthorizationMode: authz.RoleAuthorizationModeLegacy,
	})
	require.NoError(t, err)
	subjectSnapshot, err := authz.NewSubjectSnapshot(authz.SubjectSnapshotInput{
		Subject:            subject,
		Exists:             true,
		Active:             true,
		AuthzVersion:       1,
		CurrentLegacyAdmin: currentLegacyAdmin,
		Configuration:      configuration,
	})
	require.NoError(t, err)
	ref, err := authz.NewResourceRef(authz.ResourceTypeAccount, 7)
	require.NoError(t, err)
	accessVersion := int64(0)
	if resourceExists {
		accessVersion = 3
	}
	resourceSnapshot, err := authz.NewResourceAccessSnapshot(authz.ResourceAccessSnapshotInput{
		Subject:       subjectSnapshot,
		Resource:      ref,
		Exists:        resourceExists,
		AccessVersion: accessVersion,
	})
	require.NoError(t, err)
	return ref, subjectSnapshot, resourceSnapshot
}

func resourceMutationTestActor(
	t testing.TB,
	kind authz.SubjectKind,
	id int64,
	authzVersion int64,
) authz.Actor {
	t.Helper()
	subject, err := authz.NewSubjectRef(kind, id)
	require.NoError(t, err)
	configuration, err := authz.NewPolicyConfiguration(authz.PolicyConfigurationInput{
		RoleAuthorizationMode: authz.RoleAuthorizationModeLegacy,
	})
	require.NoError(t, err)
	snapshot, err := authz.NewSubjectSnapshot(authz.SubjectSnapshotInput{
		Subject:            subject,
		Exists:             true,
		Active:             true,
		AuthzVersion:       authzVersion,
		CurrentLegacyAdmin: kind == authz.SubjectKindUser,
		Configuration:      configuration,
	})
	require.NoError(t, err)
	resolver := authz.NewActorResolver(resourceMutationPolicyStoreStub{subject: snapshot})
	if kind == authz.SubjectKindServicePrincipal {
		actor, resolveErr := resolver.ResolveServicePrincipal(
			context.Background(),
			authz.AdminAPIKeyServicePrincipalCode,
			authz.AuthMethodAdminAPIKey,
		)
		require.NoError(t, resolveErr)
		return actor
	}
	actor, resolveErr := resolver.ResolveLegacyAdminUser(context.Background(), id)
	require.NoError(t, resolveErr)
	return actor
}
