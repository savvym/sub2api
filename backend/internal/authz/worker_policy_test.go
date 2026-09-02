package authz

import (
	"context"
	"errors"
	"testing"
)

const (
	testOpenAIQuotaWorkerPrincipalID int64 = 71
	testOpenAIQuotaWorkerVersion     int64 = 5
	testOpenAIQuotaWorkerAccountID   int64 = 83
)

type stubWorkerPolicyStore struct {
	snapshot      WorkerAuthorizationSnapshot
	err           error
	calls         int
	lastCode      string
	lastAccountID int64
}

func (s *stubWorkerPolicyStore) LoadWorkerAuthorizationSnapshot(
	_ context.Context,
	servicePrincipalCode string,
	accountID int64,
) (WorkerAuthorizationSnapshot, error) {
	s.calls++
	s.lastCode = servicePrincipalCode
	s.lastAccountID = accountID
	return s.snapshot, s.err
}

func TestWorkerPolicyAuthorizesOpenAIQuotaAutoReset(t *testing.T) {
	actor := mustOpenAIQuotaWorkerActor(t)
	accountRef := mustResourceRef(t, ResourceTypeAccount, testOpenAIQuotaWorkerAccountID)

	t.Run("capability check", func(t *testing.T) {
		store := &stubWorkerPolicyStore{snapshot: mustOpenAIQuotaWorkerSnapshot(t, 0)}
		policy := NewWorkerPolicy(store)

		if err := policy.CheckWorkerCapability(context.Background(), actor, CapabilityPlatformAccountOpenAIQuotaAutoReset); err != nil {
			t.Fatalf("check worker capability: %v", err)
		}
		assertWorkerPolicyStoreCall(t, store, 0)
	})

	t.Run("account operate", func(t *testing.T) {
		store := &stubWorkerPolicyStore{snapshot: mustOpenAIQuotaWorkerSnapshot(t, testOpenAIQuotaWorkerAccountID)}
		policy := NewWorkerPolicy(store)

		if err := policy.AuthorizeWorker(
			context.Background(),
			actor,
			CapabilityPlatformAccountOpenAIQuotaAutoReset,
			ActionAccountOperate,
			accountRef,
		); err != nil {
			t.Fatalf("authorize worker: %v", err)
		}
		assertWorkerPolicyStoreCall(t, store, testOpenAIQuotaWorkerAccountID)
	})
}

func TestWorkerPolicyRejectsWrongCapabilityAuthMethodAndOperationBeforeStore(t *testing.T) {
	accountRef := mustResourceRef(t, ResourceTypeAccount, testOpenAIQuotaWorkerAccountID)
	groupRef := mustResourceRef(t, ResourceTypeGroup, testOpenAIQuotaWorkerAccountID)
	workerActor := mustOpenAIQuotaWorkerActor(t)
	adminAPIKeyActor, err := newServicePrincipalActor(testOpenAIQuotaWorkerPrincipalID, servicePrincipalActorOptions{
		subjectAuthzVersion: testOpenAIQuotaWorkerVersion,
		capabilities:        []Capability{CapabilityPlatformAccountOpenAIQuotaAutoReset},
		authMethod:          AuthMethodAdminAPIKey,
	})
	if err != nil {
		t.Fatalf("create admin API key actor: %v", err)
	}
	userActor := mustUserActor(
		t,
		testOpenAIQuotaWorkerPrincipalID,
		testOpenAIQuotaWorkerVersion,
		nil,
		[]Capability{CapabilityPlatformAccountOpenAIQuotaAutoReset},
		false,
	)
	systemActor, err := newSystemActor(OpenAIQuotaAutoResetServicePrincipalCode)
	if err != nil {
		t.Fatalf("create system actor: %v", err)
	}

	tests := []struct {
		name   string
		invoke func(WorkerPolicy) error
	}{
		{
			name: "different known capability",
			invoke: func(policy WorkerPolicy) error {
				return policy.CheckWorkerCapability(context.Background(), workerActor, CapabilityPlatformResourceOperateAll)
			},
		},
		{
			name: "unknown capability",
			invoke: func(policy WorkerPolicy) error {
				return policy.CheckWorkerCapability(context.Background(), workerActor, Capability("platform.account.unknown_worker"))
			},
		},
		{
			name: "admin API key auth method",
			invoke: func(policy WorkerPolicy) error {
				return policy.CheckWorkerCapability(context.Background(), adminAPIKeyActor, CapabilityPlatformAccountOpenAIQuotaAutoReset)
			},
		},
		{
			name: "user actor",
			invoke: func(policy WorkerPolicy) error {
				return policy.CheckWorkerCapability(context.Background(), userActor, CapabilityPlatformAccountOpenAIQuotaAutoReset)
			},
		},
		{
			name: "system actor",
			invoke: func(policy WorkerPolicy) error {
				return policy.CheckWorkerCapability(context.Background(), systemActor, CapabilityPlatformAccountOpenAIQuotaAutoReset)
			},
		},
		{
			name: "invalid actor",
			invoke: func(policy WorkerPolicy) error {
				return policy.CheckWorkerCapability(context.Background(), Actor{}, CapabilityPlatformAccountOpenAIQuotaAutoReset)
			},
		},
		{
			name: "account view",
			invoke: func(policy WorkerPolicy) error {
				return policy.AuthorizeWorker(context.Background(), workerActor, CapabilityPlatformAccountOpenAIQuotaAutoReset, ActionAccountView, accountRef)
			},
		},
		{
			name: "group operation",
			invoke: func(policy WorkerPolicy) error {
				return policy.AuthorizeWorker(context.Background(), workerActor, CapabilityPlatformAccountOpenAIQuotaAutoReset, ActionGroupUse, groupRef)
			},
		},
		{
			name: "account action with group reference",
			invoke: func(policy WorkerPolicy) error {
				return policy.AuthorizeWorker(context.Background(), workerActor, CapabilityPlatformAccountOpenAIQuotaAutoReset, ActionAccountOperate, groupRef)
			},
		},
		{
			name: "invalid action",
			invoke: func(policy WorkerPolicy) error {
				return policy.AuthorizeWorker(context.Background(), workerActor, CapabilityPlatformAccountOpenAIQuotaAutoReset, Action("account.reset"), accountRef)
			},
		},
		{
			name: "invalid resource reference",
			invoke: func(policy WorkerPolicy) error {
				return policy.AuthorizeWorker(context.Background(), workerActor, CapabilityPlatformAccountOpenAIQuotaAutoReset, ActionAccountOperate, ResourceRef{})
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store := &stubWorkerPolicyStore{snapshot: mustOpenAIQuotaWorkerSnapshot(t, testOpenAIQuotaWorkerAccountID)}
			err := testCase.invoke(NewWorkerPolicy(store))
			if !errors.Is(err, ErrPolicyAccessDenied) {
				t.Fatalf("error = %v, want %v", err, ErrPolicyAccessDenied)
			}
			if store.calls != 0 {
				t.Fatalf("worker store calls = %d, want 0", store.calls)
			}
		})
	}
}

func TestWorkerPolicyRequiresExactCurrentActorAndDatabaseAuthority(t *testing.T) {
	tests := []struct {
		name           string
		actor          func(testing.TB) Actor
		mutateSnapshot func(*WorkerAuthorizationSnapshotInput)
		wantErr        error
	}{
		{
			name: "service principal ID changed",
			mutateSnapshot: func(input *WorkerAuthorizationSnapshotInput) {
				input.ServicePrincipalID++
			},
			wantErr: ErrSessionInvalid,
		},
		{
			name: "service principal code changed",
			mutateSnapshot: func(input *WorkerAuthorizationSnapshotInput) {
				input.ServicePrincipalCode = "another_worker"
			},
			wantErr: ErrSessionInvalid,
		},
		{
			name: "authorization version stale",
			mutateSnapshot: func(input *WorkerAuthorizationSnapshotInput) {
				input.AuthzVersion++
			},
			wantErr: ErrSessionInvalid,
		},
		{
			name: "actor has role",
			actor: func(t testing.TB) Actor {
				return mustServicePrincipalActor(
					t,
					testOpenAIQuotaWorkerPrincipalID,
					testOpenAIQuotaWorkerVersion,
					map[int64]int64{9: 2},
					[]Capability{CapabilityPlatformAccountOpenAIQuotaAutoReset},
				)
			},
			wantErr: ErrSessionInvalid,
		},
		{
			name: "actor has no capability",
			actor: func(t testing.TB) Actor {
				return mustServicePrincipalActor(t, testOpenAIQuotaWorkerPrincipalID, testOpenAIQuotaWorkerVersion, nil, nil)
			},
			wantErr: ErrSessionInvalid,
		},
		{
			name: "actor has extra capability",
			actor: func(t testing.TB) Actor {
				return mustServicePrincipalActor(
					t,
					testOpenAIQuotaWorkerPrincipalID,
					testOpenAIQuotaWorkerVersion,
					nil,
					[]Capability{CapabilityPlatformAccountOpenAIQuotaAutoReset, CapabilityPlatformResourceViewAll},
				)
			},
			wantErr: ErrSessionInvalid,
		},
		{
			name: "service principal inactive",
			mutateSnapshot: func(input *WorkerAuthorizationSnapshotInput) {
				input.Active = false
			},
			wantErr: ErrActorInactive,
		},
		{
			name: "database has role",
			mutateSnapshot: func(input *WorkerAuthorizationSnapshotInput) {
				input.RoleCount = 1
			},
			wantErr: ErrPolicyAccessDenied,
		},
		{
			name: "database has no direct permission",
			mutateSnapshot: func(input *WorkerAuthorizationSnapshotInput) {
				input.PermissionCodes = nil
			},
			wantErr: ErrPolicyAccessDenied,
		},
		{
			name: "database has wrong direct permission",
			mutateSnapshot: func(input *WorkerAuthorizationSnapshotInput) {
				input.PermissionCodes = []string{string(CapabilityPlatformResourceOperateAll)}
			},
			wantErr: ErrPolicyAccessDenied,
		},
		{
			name: "database has extra direct permission",
			mutateSnapshot: func(input *WorkerAuthorizationSnapshotInput) {
				input.PermissionCodes = []string{
					string(CapabilityPlatformAccountOpenAIQuotaAutoReset),
					string(CapabilityPlatformResourceViewAll),
				}
			},
			wantErr: ErrPolicyAccessDenied,
		},
		{
			name: "database has unknown direct permission",
			mutateSnapshot: func(input *WorkerAuthorizationSnapshotInput) {
				input.PermissionCodes = []string{"platform.account.unknown_worker"}
			},
			wantErr: ErrPolicyAccessDenied,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			actor := mustOpenAIQuotaWorkerActor(t)
			if testCase.actor != nil {
				actor = testCase.actor(t)
			}
			input := openAIQuotaWorkerSnapshotInput(testOpenAIQuotaWorkerAccountID)
			if testCase.mutateSnapshot != nil {
				testCase.mutateSnapshot(&input)
			}
			store := &stubWorkerPolicyStore{snapshot: mustWorkerAuthorizationSnapshot(t, input)}
			policy := NewWorkerPolicy(store)
			err := policy.AuthorizeWorker(
				context.Background(),
				actor,
				CapabilityPlatformAccountOpenAIQuotaAutoReset,
				ActionAccountOperate,
				mustResourceRef(t, ResourceTypeAccount, testOpenAIQuotaWorkerAccountID),
			)
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("error = %v, want %v", err, testCase.wantErr)
			}
			assertWorkerPolicyStoreCall(t, store, testOpenAIQuotaWorkerAccountID)
		})
	}
}

func TestWorkerPolicyRequiresExistingNonDeletedAccount(t *testing.T) {
	tests := []struct {
		name           string
		mutateSnapshot func(*WorkerAuthorizationSnapshotInput)
	}{
		{
			name: "missing account",
			mutateSnapshot: func(input *WorkerAuthorizationSnapshotInput) {
				input.AccountExists = false
			},
		},
		{
			name: "deleted account",
			mutateSnapshot: func(input *WorkerAuthorizationSnapshotInput) {
				input.AccountDeleted = true
			},
		},
		{
			name: "different account",
			mutateSnapshot: func(input *WorkerAuthorizationSnapshotInput) {
				input.AccountID++
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			input := openAIQuotaWorkerSnapshotInput(testOpenAIQuotaWorkerAccountID)
			testCase.mutateSnapshot(&input)
			store := &stubWorkerPolicyStore{snapshot: mustWorkerAuthorizationSnapshot(t, input)}
			err := NewWorkerPolicy(store).AuthorizeWorker(
				context.Background(),
				mustOpenAIQuotaWorkerActor(t),
				CapabilityPlatformAccountOpenAIQuotaAutoReset,
				ActionAccountOperate,
				mustResourceRef(t, ResourceTypeAccount, testOpenAIQuotaWorkerAccountID),
			)
			if !errors.Is(err, ErrPolicyAccessDenied) {
				t.Fatalf("error = %v, want %v", err, ErrPolicyAccessDenied)
			}
			assertWorkerPolicyStoreCall(t, store, testOpenAIQuotaWorkerAccountID)
		})
	}
}

func TestWorkerPolicyFailsClosedWhenAuthorizationDataIsUnavailable(t *testing.T) {
	actor := mustOpenAIQuotaWorkerActor(t)
	capability := CapabilityPlatformAccountOpenAIQuotaAutoReset

	t.Run("nil store", func(t *testing.T) {
		err := NewWorkerPolicy(nil).CheckWorkerCapability(context.Background(), actor, capability)
		if !errors.Is(err, ErrAuthorizationUnavailable) {
			t.Fatalf("error = %v, want %v", err, ErrAuthorizationUnavailable)
		}
	})

	t.Run("nil context", func(t *testing.T) {
		store := &stubWorkerPolicyStore{snapshot: mustOpenAIQuotaWorkerSnapshot(t, 0)}
		err := NewWorkerPolicy(store).CheckWorkerCapability(nil, actor, capability) //nolint:staticcheck // Intentionally verifies the nil-context guard.
		if !errors.Is(err, ErrAuthorizationUnavailable) {
			t.Fatalf("error = %v, want %v", err, ErrAuthorizationUnavailable)
		}
		if store.calls != 0 {
			t.Fatalf("worker store calls = %d, want 0", store.calls)
		}
	})

	t.Run("store error", func(t *testing.T) {
		cause := errors.New("database unavailable")
		store := &stubWorkerPolicyStore{err: cause}
		err := NewWorkerPolicy(store).CheckWorkerCapability(context.Background(), actor, capability)
		if !errors.Is(err, ErrAuthorizationUnavailable) || !errors.Is(err, cause) {
			t.Fatalf("error = %v, want authorization unavailable preserving %v", err, cause)
		}
		assertWorkerPolicyStoreCall(t, store, 0)
	})

	t.Run("subject missing", func(t *testing.T) {
		store := &stubWorkerPolicyStore{err: ErrSubjectNotFound}
		err := NewWorkerPolicy(store).CheckWorkerCapability(context.Background(), actor, capability)
		if !errors.Is(err, ErrActorInactive) || errors.Is(err, ErrAuthorizationUnavailable) {
			t.Fatalf("error = %v, want only %v", err, ErrActorInactive)
		}
		assertWorkerPolicyStoreCall(t, store, 0)
	})

	t.Run("malformed snapshot", func(t *testing.T) {
		store := &stubWorkerPolicyStore{}
		err := NewWorkerPolicy(store).CheckWorkerCapability(context.Background(), actor, capability)
		if !errors.Is(err, ErrAuthorizationUnavailable) {
			t.Fatalf("error = %v, want %v", err, ErrAuthorizationUnavailable)
		}
		assertWorkerPolicyStoreCall(t, store, 0)
	})
}

func mustOpenAIQuotaWorkerActor(t testing.TB) Actor {
	t.Helper()
	return mustServicePrincipalActor(
		t,
		testOpenAIQuotaWorkerPrincipalID,
		testOpenAIQuotaWorkerVersion,
		nil,
		[]Capability{CapabilityPlatformAccountOpenAIQuotaAutoReset},
	)
}

func openAIQuotaWorkerSnapshotInput(accountID int64) WorkerAuthorizationSnapshotInput {
	return WorkerAuthorizationSnapshotInput{
		ServicePrincipalID:   testOpenAIQuotaWorkerPrincipalID,
		ServicePrincipalCode: OpenAIQuotaAutoResetServicePrincipalCode,
		Active:               true,
		AuthzVersion:         testOpenAIQuotaWorkerVersion,
		PermissionCodes:      []string{string(CapabilityPlatformAccountOpenAIQuotaAutoReset)},
		AccountID:            accountID,
		AccountExists:        accountID > 0,
	}
}

func mustOpenAIQuotaWorkerSnapshot(t testing.TB, accountID int64) WorkerAuthorizationSnapshot {
	t.Helper()
	return mustWorkerAuthorizationSnapshot(t, openAIQuotaWorkerSnapshotInput(accountID))
}

func mustWorkerAuthorizationSnapshot(t testing.TB, input WorkerAuthorizationSnapshotInput) WorkerAuthorizationSnapshot {
	t.Helper()
	snapshot, err := NewWorkerAuthorizationSnapshot(input)
	if err != nil {
		t.Fatalf("create worker authorization snapshot: %v", err)
	}
	return snapshot
}

func assertWorkerPolicyStoreCall(t testing.TB, store *stubWorkerPolicyStore, accountID int64) {
	t.Helper()
	if store.calls != 1 || store.lastCode != OpenAIQuotaAutoResetServicePrincipalCode || store.lastAccountID != accountID {
		t.Fatalf(
			"worker store call = (calls=%d code=%q account=%d), want (calls=1 code=%q account=%d)",
			store.calls,
			store.lastCode,
			store.lastAccountID,
			OpenAIQuotaAutoResetServicePrincipalCode,
			accountID,
		)
	}
}
