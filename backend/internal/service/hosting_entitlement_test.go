//go:build unit

package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type hostingEntitlementTestTxKey struct{}
type hostingEntitlementTestSnapshotKey struct{}

type hostingEntitlementRepositoryStub struct {
	record        HostingEntitlementRecord
	capacity      HostingCapacityRecord
	lockErr       error
	readErr       error
	applyErr      error
	auditErr      error
	capacityErr   error
	snapshotErr   error
	txErr         error
	snapshotCalls int
	txCalls       int
	lockCalls     int
	readCalls     int
	applyCalls    int
	auditCalls    int
	capacityCalls int
	rolledBack    bool
	lockActorID   int64
	lockTargetID  int64
	applyInput    HostingEntitlementMutationInput
	auditTrace    HostingEntitlementAuditTrace
}

func (r *hostingEntitlementRepositoryStub) WithHostingEntitlementSnapshot(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	r.snapshotCalls++
	if r.snapshotErr != nil {
		return r.snapshotErr
	}
	return fn(context.WithValue(ctx, hostingEntitlementTestSnapshotKey{}, true))
}

func (r *hostingEntitlementRepositoryStub) WithHostingEntitlementTx(
	ctx context.Context,
	fn func(context.Context) error,
) error {
	r.txCalls++
	if r.txErr != nil {
		return r.txErr
	}
	before := r.record
	err := fn(context.WithValue(ctx, hostingEntitlementTestTxKey{}, true))
	if err != nil {
		r.record = before
		r.rolledBack = true
	}
	return err
}

func (r *hostingEntitlementRepositoryStub) LockHostingEntitlementSubjects(
	ctx context.Context,
	actorUserID int64,
	targetUserID int64,
) error {
	if ctx.Value(hostingEntitlementTestTxKey{}) != true {
		panic("hosting entitlement subjects locked outside transaction")
	}
	r.lockCalls++
	r.lockActorID = actorUserID
	r.lockTargetID = targetUserID
	return r.lockErr
}

func (r *hostingEntitlementRepositoryStub) ReadHostingEntitlement(
	ctx context.Context,
	targetUserID int64,
) (HostingEntitlementRecord, error) {
	if ctx.Value(hostingEntitlementTestTxKey{}) != true &&
		ctx.Value(hostingEntitlementTestSnapshotKey{}) != true {
		panic("hosting entitlement read outside transaction or snapshot")
	}
	r.readCalls++
	if r.readErr != nil {
		return HostingEntitlementRecord{}, r.readErr
	}
	if r.record.UserID != targetUserID {
		return HostingEntitlementRecord{}, ErrUserNotFound
	}
	return r.record, nil
}

func (r *hostingEntitlementRepositoryStub) ApplyHostingEntitlement(
	ctx context.Context,
	input HostingEntitlementMutationInput,
) (HostingEntitlementMutationResult, error) {
	if ctx.Value(hostingEntitlementTestTxKey{}) != true {
		panic("hosting entitlement mutation outside transaction")
	}
	r.applyCalls++
	r.applyInput = input
	if r.applyErr != nil {
		return HostingEntitlementMutationResult{}, r.applyErr
	}

	roleChanged := input.Current.Hoster != input.Hoster ||
		(input.Hoster && input.Current.HosterAssignmentExists && !input.Current.HosterAssignmentPermanent)
	changed := input.Current.Version == 0 || roleChanged ||
		input.Current.AccountLimit != input.AccountLimit || input.Current.GroupLimit != input.GroupLimit
	if !changed {
		return HostingEntitlementMutationResult{}, nil
	}

	r.record.Hoster = input.Hoster
	r.record.HosterAssignmentExists = input.Hoster
	r.record.HosterAssignmentPermanent = input.Hoster
	r.record.AccountLimit = input.AccountLimit
	r.record.GroupLimit = input.GroupLimit
	if r.record.Version == 0 {
		r.record.Version = 1
	} else {
		r.record.Version++
	}
	if roleChanged {
		r.record.AuthzVersion++
	}
	r.record.UpdatedByUserID = hostingEntitlementInt64Pointer(input.ActorUserID)
	if r.record.CreatedByUserID == nil {
		r.record.CreatedByUserID = hostingEntitlementInt64Pointer(input.ActorUserID)
	}
	return HostingEntitlementMutationResult{Changed: true, RoleChanged: roleChanged}, nil
}

func (r *hostingEntitlementRepositoryStub) AppendHostingEntitlementAudit(
	ctx context.Context,
	_ int64,
	_ HostingEntitlementRecord,
	_ HostingEntitlementRecord,
	trace HostingEntitlementAuditTrace,
) error {
	if ctx.Value(hostingEntitlementTestTxKey{}) != true {
		panic("hosting entitlement audit outside transaction")
	}
	r.auditCalls++
	r.auditTrace = trace
	return r.auditErr
}

func (r *hostingEntitlementRepositoryStub) LockHostingCapacity(
	ctx context.Context,
	_ int64,
	_ authz.ResourceType,
) (HostingCapacityRecord, error) {
	r.capacityCalls++
	if ctx.Value(hostingEntitlementTestTxKey{}) != true {
		return HostingCapacityRecord{}, ErrHostingEntitlementUnavailable
	}
	if r.capacityErr != nil {
		return HostingCapacityRecord{}, r.capacityErr
	}
	return r.capacity, nil
}

type hostingEntitlementResolverStub struct {
	userActor  authz.Actor
	adminActor authz.Actor
	userErr    error
	adminErr   error
	userCalls  int
	adminCalls int
}

func (r *hostingEntitlementResolverStub) ResolveUser(
	context.Context,
	int64,
	authz.AuthMethod,
) (authz.Actor, error) {
	r.userCalls++
	return r.userActor, r.userErr
}

func (r *hostingEntitlementResolverStub) ResolveLegacyAdminUser(
	context.Context,
	int64,
) (authz.Actor, error) {
	r.adminCalls++
	return r.adminActor, r.adminErr
}

func (r *hostingEntitlementResolverStub) ResolveServicePrincipal(
	context.Context,
	string,
	authz.AuthMethod,
) (authz.Actor, error) {
	return authz.Actor{}, authz.ErrInvalidActor
}

type hostingEntitlementActorStore struct {
	snapshot authz.SubjectSnapshot
}

func (s hostingEntitlementActorStore) LoadSubjectSnapshot(
	context.Context,
	authz.SubjectRef,
) (authz.SubjectSnapshot, error) {
	return s.snapshot, nil
}

func (s hostingEntitlementActorStore) LoadServicePrincipalSubjectSnapshotByCode(
	context.Context,
	string,
) (authz.SubjectSnapshot, error) {
	return s.snapshot, nil
}

func (s hostingEntitlementActorStore) LoadResourceAccessSnapshot(
	context.Context,
	authz.SubjectRef,
	authz.ResourceRef,
) (authz.ResourceAccessSnapshot, error) {
	return authz.ResourceAccessSnapshot{}, errors.New("unexpected resource authorization lookup")
}

type hostingEntitlementPolicyErrorStub struct {
	err error
}

func (s hostingEntitlementPolicyErrorStub) CheckCapability(
	context.Context,
	authz.Actor,
	authz.Capability,
) (authz.Decision, error) {
	return authz.Decision{}, s.err
}

func (s hostingEntitlementPolicyErrorStub) CanCreate(
	context.Context,
	authz.Actor,
	authz.ResourceType,
) (authz.Decision, error) {
	return authz.Decision{}, s.err
}

func (s hostingEntitlementPolicyErrorStub) Authorize(
	context.Context,
	authz.Actor,
	authz.Action,
	authz.ResourceRef,
) (authz.Decision, error) {
	return authz.Decision{}, s.err
}

func (s hostingEntitlementPolicyErrorStub) AccessibleScope(
	context.Context,
	authz.Actor,
	authz.ResourceType,
	authz.Action,
) (authz.AccessibleScope, error) {
	return authz.AccessibleScope{}, s.err
}

func TestHostingEntitlementServiceGetReturnsCompositeCapacity(t *testing.T) {
	adminActor, _ := hostingEntitlementTestUserActor(t, 41, 3, true, nil, authz.RoleAuthorizationModeLegacy, false)
	repo := &hostingEntitlementRepositoryStub{record: HostingEntitlementRecord{
		UserID:       72,
		UserActive:   true,
		Hoster:       true,
		AccountLimit: 5,
		AccountUsage: 2,
		GroupLimit:   1,
		GroupUsage:   3,
		Version:      4,
		AuthzVersion: 9,
	}}
	resolver := &hostingEntitlementResolverStub{adminActor: adminActor}
	svc := NewHostingEntitlementService(repo, resolver, nil)

	result, err := svc.Get(context.Background(), adminActor, 72)

	require.NoError(t, err)
	require.Equal(t, int64(3), result.AccountRemaining)
	require.Zero(t, result.GroupRemaining)
	require.Equal(t, int64(4), result.Version)
	require.Equal(t, 1, repo.snapshotCalls)
	require.Equal(t, 1, resolver.adminCalls)
}

func TestHostingEntitlementServiceUpdateValidatesBeforeTransaction(t *testing.T) {
	adminActor, _ := hostingEntitlementTestUserActor(t, 41, 1, true, nil, authz.RoleAuthorizationModeLegacy, false)
	servicePrincipal := adminResourceTestActor(t, authz.SubjectKindServicePrincipal, 91)
	tests := []struct {
		name   string
		input  HostingEntitlementUpdateInput
		reason string
	}{
		{name: "service principal", input: HostingEntitlementUpdateInput{Actor: servicePrincipal, TargetUserID: 2}, reason: "HOSTING_ACTOR_NOT_AUTHORIZED"},
		{name: "invalid target", input: HostingEntitlementUpdateInput{Actor: adminActor}, reason: "INVALID_USER_ID"},
		{name: "negative version", input: HostingEntitlementUpdateInput{Actor: adminActor, TargetUserID: 2, ExpectedVersion: -1}, reason: "INVALID_HOSTING_ENTITLEMENT"},
		{name: "negative account quota", input: HostingEntitlementUpdateInput{Actor: adminActor, TargetUserID: 2, AccountLimit: -1}, reason: "INVALID_HOSTING_ENTITLEMENT"},
		{name: "negative group quota", input: HostingEntitlementUpdateInput{Actor: adminActor, TargetUserID: 2, GroupLimit: -1}, reason: "INVALID_HOSTING_ENTITLEMENT"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &hostingEntitlementRepositoryStub{}
			svc := NewHostingEntitlementService(repo, &hostingEntitlementResolverStub{}, nil)

			_, err := svc.Update(context.Background(), testCase.input)

			require.Error(t, err)
			require.Equal(t, testCase.reason, infraerrors.Reason(err))
			require.Zero(t, repo.txCalls)
		})
	}
}

func TestHostingEntitlementServiceUpdateRejectsStaleActorAndCAS(t *testing.T) {
	requestActor, _ := hostingEntitlementTestUserActor(t, 41, 1, true, nil, authz.RoleAuthorizationModeLegacy, false)
	currentActor, _ := hostingEntitlementTestUserActor(t, 41, 2, true, nil, authz.RoleAuthorizationModeLegacy, false)

	t.Run("stale actor", func(t *testing.T) {
		repo := &hostingEntitlementRepositoryStub{record: HostingEntitlementRecord{UserID: 72, Version: 3}}
		svc := NewHostingEntitlementService(repo, &hostingEntitlementResolverStub{adminActor: currentActor}, nil)

		_, err := svc.Update(context.Background(), HostingEntitlementUpdateInput{
			Actor: requestActor, TargetUserID: 72, ExpectedVersion: 3,
		})

		require.ErrorIs(t, err, ErrHostingEntitlementConflict)
		require.True(t, repo.rolledBack)
		require.Zero(t, repo.applyCalls)
		require.Zero(t, repo.auditCalls)
	})

	t.Run("stale version", func(t *testing.T) {
		repo := &hostingEntitlementRepositoryStub{record: HostingEntitlementRecord{UserID: 72, Version: 3}}
		svc := NewHostingEntitlementService(repo, &hostingEntitlementResolverStub{adminActor: requestActor}, nil)

		_, err := svc.Update(context.Background(), HostingEntitlementUpdateInput{
			Actor: requestActor, TargetUserID: 72, ExpectedVersion: 2,
		})

		require.ErrorIs(t, err, ErrHostingEntitlementConflict)
		require.Equal(t, "2", infraerrors.FromError(err).Metadata["expected_version"])
		require.Equal(t, "3", infraerrors.FromError(err).Metadata["current_version"])
		require.Zero(t, repo.applyCalls)
		require.Zero(t, repo.auditCalls)
	})
}

func TestHostingEntitlementServiceUpdateNoopSkipsDurableAudit(t *testing.T) {
	adminActor, _ := hostingEntitlementTestUserActor(t, 41, 1, true, nil, authz.RoleAuthorizationModeLegacy, false)
	repo := &hostingEntitlementRepositoryStub{record: HostingEntitlementRecord{
		UserID:                    72,
		UserActive:                true,
		Hoster:                    true,
		HosterAssignmentExists:    true,
		HosterAssignmentPermanent: true,
		AccountLimit:              4,
		GroupLimit:                2,
		Version:                   7,
		AuthzVersion:              3,
	}}
	svc := NewHostingEntitlementService(repo, &hostingEntitlementResolverStub{adminActor: adminActor}, nil)

	result, err := svc.Update(context.Background(), HostingEntitlementUpdateInput{
		Actor: adminActor, TargetUserID: 72, ExpectedVersion: 7,
		Hoster: true, AccountLimit: 4, GroupLimit: 2,
	})

	require.NoError(t, err)
	require.False(t, result.Changed)
	require.Equal(t, int64(7), result.Version)
	require.Equal(t, 1, repo.applyCalls)
	require.Zero(t, repo.auditCalls)
}

func TestHostingEntitlementServiceUpdateAuditFailureRollsBackMutation(t *testing.T) {
	adminActor, _ := hostingEntitlementTestUserActor(t, 41, 1, true, nil, authz.RoleAuthorizationModeLegacy, false)
	original := HostingEntitlementRecord{UserID: 72, UserActive: true, Version: 2, AuthzVersion: 5}
	repo := &hostingEntitlementRepositoryStub{
		record:   original,
		auditErr: errors.New("audit unavailable"),
	}
	svc := NewHostingEntitlementService(repo, &hostingEntitlementResolverStub{adminActor: adminActor}, nil)

	_, err := svc.Update(context.Background(), HostingEntitlementUpdateInput{
		Actor: adminActor, TargetUserID: 72, ExpectedVersion: 2,
		Hoster: true, AccountLimit: 6, GroupLimit: 3,
		AuditTrace: HostingEntitlementAuditTrace{
			RequestID: "  " + strings.Repeat("r", 80) + "  ",
			ClientIP:  " 198.51.100.12 ",
			UserAgent: strings.Repeat("u", 600),
		},
	})

	require.ErrorIs(t, err, ErrHostingEntitlementUnavailable)
	require.True(t, repo.rolledBack)
	require.Equal(t, original, repo.record)
	require.Equal(t, 1, repo.applyCalls)
	require.Equal(t, 1, repo.auditCalls)
	require.Len(t, []rune(repo.auditTrace.RequestID), 64)
	require.Equal(t, "198.51.100.12", repo.auditTrace.ClientIP)
	require.Len(t, []rune(repo.auditTrace.UserAgent), 512)
}

func TestHostingEntitlementServiceFirstUpdateMaterializesVersionOne(t *testing.T) {
	adminActor, _ := hostingEntitlementTestUserActor(t, 41, 1, true, nil, authz.RoleAuthorizationModeLegacy, false)
	repo := &hostingEntitlementRepositoryStub{record: HostingEntitlementRecord{
		UserID: 72, UserActive: true, AuthzVersion: 1,
	}}
	svc := NewHostingEntitlementService(repo, &hostingEntitlementResolverStub{adminActor: adminActor}, nil)

	result, err := svc.Update(context.Background(), HostingEntitlementUpdateInput{
		Actor: adminActor, TargetUserID: 72, ExpectedVersion: 0,
	})

	require.NoError(t, err)
	require.True(t, result.Changed)
	require.Equal(t, int64(1), result.Version)
	require.Equal(t, int64(1), result.AuthzVersion)
	require.Equal(t, 1, repo.auditCalls)
}

func TestHostingEntitlementServiceRequireCreateCapacity(t *testing.T) {
	actor, snapshot := hostingEntitlementTestUserActor(
		t, 72, 3, false,
		[]authz.Capability{authz.CapabilityAccountCreate, authz.CapabilityGroupCreate},
		authz.RoleAuthorizationModeShadow,
		true,
	)
	allowPolicy := authz.NewPolicyService(hostingEntitlementActorStore{snapshot: snapshot})

	tests := []struct {
		name         string
		resourceType authz.ResourceType
		capacity     HostingCapacityRecord
		policy       authz.ResourcePolicy
		want         HostingCapacity
		wantErr      error
	}{
		{
			name:         "account capacity",
			resourceType: authz.ResourceTypeAccount,
			capacity: HostingCapacityRecord{
				UserID: 72, UserActive: true, Hoster: true, Version: 4,
				AccountLimit: 5, AccountUsage: 2,
			},
			policy: allowPolicy,
			want: HostingCapacity{
				UserID: 72, ResourceType: authz.ResourceTypeAccount,
				Limit: 5, Usage: 2, Remaining: 3, Version: 4,
			},
		},
		{
			name:         "group capacity",
			resourceType: authz.ResourceTypeGroup,
			capacity: HostingCapacityRecord{
				UserID: 72, UserActive: true, Hoster: true, Version: 6,
				GroupLimit: 3, GroupUsage: 2,
			},
			policy: allowPolicy,
			want: HostingCapacity{
				UserID: 72, ResourceType: authz.ResourceTypeGroup,
				Limit: 3, Usage: 2, Remaining: 1, Version: 6,
			},
		},
		{
			name:         "zero quota is exhausted",
			resourceType: authz.ResourceTypeAccount,
			capacity: HostingCapacityRecord{
				UserID: 72, UserActive: true, Hoster: true,
			},
			policy:  allowPolicy,
			wantErr: ErrHostingQuotaExceeded,
		},
		{
			name:         "lowered quota below usage is exhausted",
			resourceType: authz.ResourceTypeGroup,
			capacity: HostingCapacityRecord{
				UserID: 72, UserActive: true, Hoster: true, Version: 8,
				GroupLimit: 1, GroupUsage: 3,
			},
			policy:  allowPolicy,
			wantErr: ErrHostingQuotaExceeded,
		},
		{
			name:         "inactive user",
			resourceType: authz.ResourceTypeAccount,
			capacity: HostingCapacityRecord{
				UserID: 72, Hoster: true, AccountLimit: 2,
			},
			policy:  allowPolicy,
			wantErr: ErrHostingQualificationRequired,
		},
		{
			name:         "missing hoster qualification",
			resourceType: authz.ResourceTypeAccount,
			capacity: HostingCapacityRecord{
				UserID: 72, UserActive: true, AccountLimit: 2,
			},
			policy:  allowPolicy,
			wantErr: ErrHostingQualificationRequired,
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			repo := &hostingEntitlementRepositoryStub{capacity: testCase.capacity}
			resolver := &hostingEntitlementResolverStub{userActor: actor}
			svc := NewHostingEntitlementService(repo, resolver, testCase.policy)
			var result HostingCapacity
			err := repo.WithHostingEntitlementTx(context.Background(), func(txCtx context.Context) error {
				var capacityErr error
				result, capacityErr = svc.RequireCreateCapacity(txCtx, actor, testCase.resourceType)
				return capacityErr
			})

			if testCase.wantErr != nil {
				require.ErrorIs(t, err, testCase.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, testCase.want, result)
		})
	}
}

func TestHostingEntitlementServiceRequireCreateCapacityFailsClosed(t *testing.T) {
	actor, allowSnapshot := hostingEntitlementTestUserActor(
		t, 72, 3, false,
		[]authz.Capability{authz.CapabilityAccountCreate},
		authz.RoleAuthorizationModeShadow,
		true,
	)
	allowPolicy := authz.NewPolicyService(hostingEntitlementActorStore{snapshot: allowSnapshot})
	baseCapacity := HostingCapacityRecord{
		UserID: 72, UserActive: true, Hoster: true, Version: 1,
		AccountLimit: 2,
	}

	t.Run("transaction required", func(t *testing.T) {
		repo := &hostingEntitlementRepositoryStub{capacity: baseCapacity}
		svc := NewHostingEntitlementService(repo, &hostingEntitlementResolverStub{userActor: actor}, allowPolicy)

		_, err := svc.RequireCreateCapacity(context.Background(), actor, authz.ResourceTypeAccount)

		require.ErrorIs(t, err, ErrHostingEntitlementUnavailable)
	})

	t.Run("stale actor", func(t *testing.T) {
		currentActor, _ := hostingEntitlementTestUserActor(
			t, 72, 4, false,
			[]authz.Capability{authz.CapabilityAccountCreate},
			authz.RoleAuthorizationModeShadow,
			true,
		)
		repo := &hostingEntitlementRepositoryStub{capacity: baseCapacity}
		svc := NewHostingEntitlementService(repo, &hostingEntitlementResolverStub{userActor: currentActor}, allowPolicy)

		err := repo.WithHostingEntitlementTx(context.Background(), func(txCtx context.Context) error {
			_, capacityErr := svc.RequireCreateCapacity(txCtx, actor, authz.ResourceTypeAccount)
			return capacityErr
		})

		require.ErrorIs(t, err, ErrHostingEntitlementConflict)
	})

	t.Run("policy denial", func(t *testing.T) {
		_, denySnapshot := hostingEntitlementTestUserActor(
			t, 72, 3, false,
			[]authz.Capability{authz.CapabilityAccountCreate},
			authz.RoleAuthorizationModeShadow,
			false,
		)
		repo := &hostingEntitlementRepositoryStub{capacity: baseCapacity}
		svc := NewHostingEntitlementService(
			repo,
			&hostingEntitlementResolverStub{userActor: actor},
			authz.NewPolicyService(hostingEntitlementActorStore{snapshot: denySnapshot}),
		)

		err := repo.WithHostingEntitlementTx(context.Background(), func(txCtx context.Context) error {
			_, capacityErr := svc.RequireCreateCapacity(txCtx, actor, authz.ResourceTypeAccount)
			return capacityErr
		})

		require.ErrorIs(t, err, ErrHostingCreateForbidden)
		require.Equal(t, string(authz.DenyReasonFeatureDisabled), infraerrors.FromError(err).Metadata["reason"])
	})

	t.Run("policy dependency failure", func(t *testing.T) {
		repo := &hostingEntitlementRepositoryStub{capacity: baseCapacity}
		svc := NewHostingEntitlementService(
			repo,
			&hostingEntitlementResolverStub{userActor: actor},
			hostingEntitlementPolicyErrorStub{err: errors.New("policy store unavailable")},
		)

		err := repo.WithHostingEntitlementTx(context.Background(), func(txCtx context.Context) error {
			_, capacityErr := svc.RequireCreateCapacity(txCtx, actor, authz.ResourceTypeAccount)
			return capacityErr
		})

		require.ErrorIs(t, err, ErrHostingEntitlementUnavailable)
	})
}

func hostingEntitlementTestUserActor(
	t testing.TB,
	userID int64,
	authzVersion int64,
	legacyAdmin bool,
	capabilities []authz.Capability,
	mode authz.RoleAuthorizationMode,
	selfServiceEnabled bool,
) (authz.Actor, authz.SubjectSnapshot) {
	t.Helper()
	subject, err := authz.NewSubjectRef(authz.SubjectKindUser, userID)
	require.NoError(t, err)
	configuration, err := authz.NewPolicyConfiguration(authz.PolicyConfigurationInput{
		RoleAuthorizationMode:        mode,
		ResourceAccessControlEnabled: selfServiceEnabled,
		SelfServiceHostingEnabled:    selfServiceEnabled,
	})
	require.NoError(t, err)
	snapshot, err := authz.NewSubjectSnapshot(authz.SubjectSnapshotInput{
		Subject:            subject,
		Exists:             true,
		Active:             true,
		AuthzVersion:       authzVersion,
		Capabilities:       capabilities,
		CurrentLegacyAdmin: legacyAdmin,
		Configuration:      configuration,
	})
	require.NoError(t, err)
	resolver := authz.NewActorResolver(hostingEntitlementActorStore{snapshot: snapshot})
	if legacyAdmin {
		actor, resolveErr := resolver.ResolveLegacyAdminUser(context.Background(), userID)
		require.NoError(t, resolveErr)
		return actor, snapshot
	}
	actor, resolveErr := resolver.ResolveUser(context.Background(), userID, authz.AuthMethodJWT)
	require.NoError(t, resolveErr)
	return actor, snapshot
}

func hostingEntitlementInt64Pointer(value int64) *int64 {
	return &value
}
