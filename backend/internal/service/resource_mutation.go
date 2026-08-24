package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var (
	ErrResourceMutationUnavailable = infraerrors.ServiceUnavailable(
		"RESOURCE_MUTATION_AUTHORIZATION_UNAVAILABLE",
		"resource authorization data is unavailable",
	)
	ErrResourceMutationConflict = infraerrors.Conflict(
		"RESOURCE_AUTHORIZATION_CHANGED",
		"resource authorization changed; retry the command",
	)
	ErrResourceMutationForbidden = infraerrors.New(
		http.StatusForbidden,
		"RESOURCE_MUTATION_FORBIDDEN",
		"resource mutation is not allowed",
	)
	errResourceMutationNoop = errors.New("resource mutation is a no-op")
)

// ResourceMutationKey is the comparable database identity used while locking,
// versioning, and auditing one Account or Group.
type ResourceMutationKey struct {
	ResourceType authz.ResourceType
	ResourceID   int64
}

func ResourceMutationKeyFromRef(ref authz.ResourceRef) ResourceMutationKey {
	return ResourceMutationKey{ResourceType: ref.Type(), ResourceID: ref.ID()}
}

func (k ResourceMutationKey) Valid() bool {
	return k.ResourceType.Valid() && k.ResourceID > 0
}

func (k ResourceMutationKey) Ref() (authz.ResourceRef, error) {
	return authz.NewResourceRef(k.ResourceType, k.ResourceID)
}

// ResourceMutationState is read from the locked row. Deleted rows remain
// addressable because Account and Group deletion is soft-delete based.
type ResourceMutationState struct {
	Key           ResourceMutationKey
	OwnerUserID   *int64
	AccessVersion int64
	Deleted       bool
}

type ResourceAuthorizationEventRecord struct {
	Key                   ResourceMutationKey
	OwnerUserID           *int64
	ActorKind             authz.SubjectKind
	ActorID               int64
	AuthMethod            authz.AuthMethod
	EventType             string
	ResourceAccessVersion int64
	RequestID             string
	ChangedFields         []string
}

// ResourceMutationAuditTrace is the bounded, already-redacted request context
// captured by the audit middleware before a command starts.
type ResourceMutationAuditTrace struct {
	Method      string
	Path        string
	RequestID   string
	ClientIP    string
	UserAgent   string
	RequestBody string
}

type resourceMutationAuditContextKey struct{}

type resourceMutationAuditContext struct {
	trace     ResourceMutationAuditTrace
	committed atomic.Bool
}

func WithResourceMutationAuditTrace(ctx context.Context, trace ResourceMutationAuditTrace) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	state := &resourceMutationAuditContext{trace: boundedResourceMutationAuditTrace(trace)}
	return context.WithValue(ctx, resourceMutationAuditContextKey{}, state)
}

func ResourceMutationAuditCommitted(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	state, _ := ctx.Value(resourceMutationAuditContextKey{}).(*resourceMutationAuditContext)
	return state != nil && state.committed.Load()
}

func markResourceMutationAuditCommitted(ctx context.Context) {
	if ctx == nil {
		return
	}
	state, _ := ctx.Value(resourceMutationAuditContextKey{}).(*resourceMutationAuditContext)
	if state != nil {
		state.committed.Store(true)
	}
}

func boundedResourceMutationAuditTrace(trace ResourceMutationAuditTrace) ResourceMutationAuditTrace {
	trace.Method = truncateResourceMutationRunes(strings.ToUpper(strings.TrimSpace(trace.Method)), 16)
	trace.Path = truncateResourceMutationRunes(strings.TrimSpace(trace.Path), 512)
	trace.RequestID = truncateResourceMutationRunes(strings.TrimSpace(trace.RequestID), 64)
	trace.ClientIP = truncateResourceMutationRunes(strings.TrimSpace(trace.ClientIP), 64)
	trace.UserAgent = truncateResourceMutationRunes(strings.TrimSpace(trace.UserAgent), 512)
	if len(trace.RequestBody) > auditRequestBodyMaxBytes {
		trace.RequestBody = trace.RequestBody[:auditRequestBodyMaxBytes]
	}
	return trace
}

func truncateResourceMutationRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// ResourceMutationRepository owns the PostgreSQL transaction primitives. All
// methods are required to use a caller-owned transaction from ctx.
type ResourceMutationRepository interface {
	WithSerializableTx(ctx context.Context, fn func(txCtx context.Context) error) error
	LockActorAuthorization(ctx context.Context, kind authz.SubjectKind, id int64) error
	LockResources(ctx context.Context, keys []ResourceMutationKey) (map[ResourceMutationKey]ResourceMutationState, error)
	IncrementAccessVersions(ctx context.Context, keys []ResourceMutationKey) (map[ResourceMutationKey]ResourceMutationState, error)
	AppendAuthorizationEvents(ctx context.Context, events []ResourceAuthorizationEventRecord) error
}

type ResourceMutationTarget struct {
	Ref                   authz.ResourceRef
	Action                authz.Action
	ExpectedAccessVersion int64
	Mutates               bool
	EventType             string
	ChangedFields         []string
}

type CreatedResourceMutation struct {
	Ref           authz.ResourceRef
	OwnerUserID   *int64
	AccessVersion int64
	EventType     string
	ChangedFields []string
}

type ResourceMutationCommand struct {
	CreateResourceTypes []authz.ResourceType
	Targets             []ResourceMutationTarget
	// ExpandsAccess marks commands that can add or reactivate an authorization
	// path. Restrictive and revocation commands leave this false so degraded
	// propagation can never prevent access from being reduced.
	ExpandsAccess bool
}

type ResourceMutationCoordinator struct {
	repository       ResourceMutationRepository
	resolver         authz.Resolver
	policy           authz.ResourcePolicy
	propagationGuard *AuthorizationPropagationGuard
}

func NewResourceMutationCoordinator(
	repository ResourceMutationRepository,
	resolver authz.Resolver,
	policy authz.ResourcePolicy,
) *ResourceMutationCoordinator {
	return &ResourceMutationCoordinator{repository: repository, resolver: resolver, policy: policy}
}

func ProvideResourceMutationCoordinator(
	repository ResourceMutationRepository,
	resolver authz.Resolver,
	policy authz.ResourcePolicy,
	propagationGuard *AuthorizationPropagationGuard,
) *ResourceMutationCoordinator {
	coordinator := NewResourceMutationCoordinator(repository, resolver, policy)
	coordinator.propagationGuard = propagationGuard
	return coordinator
}

type resourceMutationRuntime struct {
	afterCommit []func()
	noOp        bool
}

type resourceMutationCommandError struct {
	cause error
}

func (e *resourceMutationCommandError) Error() string {
	return e.cause.Error()
}

func (e *resourceMutationCommandError) Unwrap() error {
	return e.cause
}

type resourceMutationRuntimeContextKey struct{}

// afterResourceMutationCommit keeps cache/network acceleration outside the
// durable transaction. Without a coordinator it preserves the legacy timing,
// which is useful for narrow service unit tests built with direct struct literals.
func afterResourceMutationCommit(ctx context.Context, fn func()) {
	if fn == nil {
		return
	}
	if runtime, ok := ctx.Value(resourceMutationRuntimeContextKey{}).(*resourceMutationRuntime); ok && runtime != nil {
		runtime.afterCommit = append(runtime.afterCommit, fn)
		return
	}
	fn()
}

func (c *ResourceMutationCoordinator) Execute(
	ctx context.Context,
	actor authz.Actor,
	command ResourceMutationCommand,
	mutate func(txCtx context.Context) ([]CreatedResourceMutation, error),
) error {
	if c == nil || c.repository == nil || c.resolver == nil || c.policy == nil || mutate == nil || ctx == nil {
		return ErrResourceMutationUnavailable
	}
	if err := ValidateAdminResourceActor(actor); err != nil {
		return err
	}
	targets, err := normalizeResourceMutationCommand(command)
	if err != nil {
		return err
	}
	if command.ExpandsAccess {
		if c.propagationGuard == nil {
			return ErrAuthorizationPropagationDegraded
		}
		if err := c.propagationGuard.RequireExpansion(ctx); err != nil {
			return err
		}
	}

	runtime := &resourceMutationRuntime{}
	err = c.repository.WithSerializableTx(ctx, func(txCtx context.Context) error {
		txCtx = context.WithValue(txCtx, resourceMutationRuntimeContextKey{}, runtime)
		kind, actorID, ok := actor.DurableSubject()
		if !ok {
			return ErrResourceMutationUnavailable
		}
		if err := c.repository.LockActorAuthorization(txCtx, kind, actorID); err != nil {
			return ErrResourceMutationUnavailable.WithCause(err)
		}
		currentActor, err := c.resolveCurrentActor(txCtx, actor)
		if err != nil {
			if errors.Is(err, authz.ErrActorInactive) || errors.Is(err, authz.ErrPolicyAccessDenied) {
				return ErrResourceMutationConflict.WithCause(err)
			}
			return ErrResourceMutationUnavailable.WithCause(err)
		}
		if !actor.SameAuthorizationState(currentActor) {
			return ErrResourceMutationConflict
		}

		for _, resourceType := range command.CreateResourceTypes {
			decision, policyErr := c.policy.CanCreate(txCtx, currentActor, resourceType)
			if err := resourceMutationDecisionError(decision, policyErr, resourceType); err != nil {
				return err
			}
		}

		keys := make([]ResourceMutationKey, 0, len(targets))
		for _, target := range targets {
			keys = append(keys, ResourceMutationKeyFromRef(target.Ref))
		}
		states, err := c.repository.LockResources(txCtx, keys)
		if err != nil {
			return resourceMutationRepositoryError(err)
		}
		for _, target := range targets {
			key := ResourceMutationKeyFromRef(target.Ref)
			state, found := states[key]
			if !found {
				return resourceMutationNotFound(target.Ref.Type())
			}
			if target.ExpectedAccessVersion > 0 && state.AccessVersion != target.ExpectedAccessVersion {
				return ErrResourceMutationConflict
			}
			decision, policyErr := c.policy.Authorize(txCtx, currentActor, target.Action, target.Ref)
			if err := resourceMutationDecisionError(decision, policyErr, target.Ref.Type()); err != nil {
				return err
			}
		}

		created, err := mutate(txCtx)
		if errors.Is(err, errResourceMutationNoop) {
			runtime.afterCommit = nil
			runtime.noOp = true
			return errResourceMutationNoop
		}
		if err != nil {
			return &resourceMutationCommandError{cause: err}
		}
		created, err = normalizeCreatedResourceMutations(created, command.CreateResourceTypes)
		if err != nil {
			return err
		}

		mutatedKeys := make([]ResourceMutationKey, 0, len(targets))
		for _, target := range targets {
			if target.Mutates {
				mutatedKeys = append(mutatedKeys, ResourceMutationKeyFromRef(target.Ref))
			}
		}
		if len(mutatedKeys) == 0 && len(created) == 0 {
			runtime.afterCommit = nil
			runtime.noOp = true
			return errResourceMutationNoop
		}
		postStates, err := c.repository.IncrementAccessVersions(txCtx, mutatedKeys)
		if err != nil {
			return resourceMutationRepositoryError(err)
		}

		events := make([]ResourceAuthorizationEventRecord, 0, len(mutatedKeys)+len(created))
		requestID := resourceMutationRequestID(txCtx)
		for _, target := range targets {
			if !target.Mutates {
				continue
			}
			key := ResourceMutationKeyFromRef(target.Ref)
			state, ok := postStates[key]
			if !ok {
				return ErrResourceMutationConflict
			}
			events = append(events, newResourceAuthorizationEventRecord(
				state, currentActor, target.EventType, requestID, target.ChangedFields,
			))
		}
		for _, item := range created {
			state := ResourceMutationState{
				Key:           ResourceMutationKeyFromRef(item.Ref),
				OwnerUserID:   cloneResourceMutationInt64Pointer(item.OwnerUserID),
				AccessVersion: item.AccessVersion,
			}
			events = append(events, newResourceAuthorizationEventRecord(
				state, currentActor, item.EventType, requestID, item.ChangedFields,
			))
		}
		if err := c.repository.AppendAuthorizationEvents(txCtx, events); err != nil {
			return resourceMutationRepositoryError(err)
		}
		return nil
	})
	if err != nil {
		if runtime.noOp && errors.Is(err, errResourceMutationNoop) {
			return nil
		}
		return resourceMutationTransactionError(err)
	}
	if runtime.noOp {
		return nil
	}
	markResourceMutationAuditCommitted(ctx)
	for _, fn := range runtime.afterCommit {
		runResourceMutationAfterCommit(fn)
	}
	return nil
}

func runResourceMutationAfterCommit(fn func()) {
	defer func() {
		if recover() != nil {
			slog.Error("resource_mutation.after_commit_callback_panic")
		}
	}()
	fn()
}

func resourceMutationRepositoryError(err error) error {
	if err == nil {
		return nil
	}
	if resourceMutationSerializationFailure(err) {
		return ErrResourceMutationConflict.WithCause(err)
	}
	var applicationErr *infraerrors.ApplicationError
	if errors.As(err, &applicationErr) {
		return err
	}
	return ErrResourceMutationUnavailable.WithCause(err)
}

func resourceMutationTransactionError(err error) error {
	if err == nil {
		return nil
	}
	var commandErr *resourceMutationCommandError
	if errors.As(err, &commandErr) {
		if resourceMutationSQLFailure(commandErr.cause) {
			return resourceMutationRepositoryError(commandErr.cause)
		}
		return commandErr.cause
	}
	return resourceMutationRepositoryError(err)
}

func resourceMutationSQLFailure(err error) bool {
	var sqlStateErr interface{ SQLState() string }
	return errors.As(err, &sqlStateErr) && strings.TrimSpace(sqlStateErr.SQLState()) != ""
}

func resourceMutationSerializationFailure(err error) bool {
	var sqlStateErr interface{ SQLState() string }
	if !errors.As(err, &sqlStateErr) {
		return false
	}
	switch sqlStateErr.SQLState() {
	case "40001", "40P01":
		return true
	default:
		return false
	}
}

func (c *ResourceMutationCoordinator) resolveCurrentActor(ctx context.Context, actor authz.Actor) (authz.Actor, error) {
	if userID, ok := actor.UserID(); ok && actor.AuthMethod() == authz.AuthMethodJWT {
		return c.resolver.ResolveLegacyAdminUser(ctx, userID)
	}
	if _, ok := actor.ServicePrincipalID(); ok && actor.AuthMethod() == authz.AuthMethodAdminAPIKey {
		return c.resolver.ResolveServicePrincipal(
			ctx,
			authz.AdminAPIKeyServicePrincipalCode,
			authz.AuthMethodAdminAPIKey,
		)
	}
	return authz.Actor{}, authz.ErrInvalidActor
}

func normalizeResourceMutationCommand(command ResourceMutationCommand) ([]ResourceMutationTarget, error) {
	seenCreate := make(map[authz.ResourceType]struct{}, len(command.CreateResourceTypes))
	for _, resourceType := range command.CreateResourceTypes {
		if !resourceType.Valid() {
			return nil, ErrResourceMutationUnavailable
		}
		if _, duplicate := seenCreate[resourceType]; duplicate {
			return nil, ErrResourceMutationUnavailable
		}
		seenCreate[resourceType] = struct{}{}
	}

	type targetIdentity struct {
		key                   ResourceMutationKey
		action                authz.Action
		expectedAccessVersion int64
		mutates               bool
		eventType             string
	}

	targets := make([]ResourceMutationTarget, 0, len(command.Targets))
	seen := make(map[targetIdentity]int, len(command.Targets))
	for _, requested := range command.Targets {
		target := requested
		key := ResourceMutationKeyFromRef(target.Ref)
		if !target.Ref.Valid() || !target.Action.ValidFor(target.Ref.Type()) || target.ExpectedAccessVersion < 0 {
			return nil, ErrResourceMutationUnavailable
		}
		if target.Mutates {
			if err := validateResourceMutationEvent(target.EventType, target.ChangedFields); err != nil {
				return nil, err
			}
		} else if target.EventType != "" || len(target.ChangedFields) != 0 {
			return nil, ErrResourceMutationUnavailable
		}
		target.EventType = strings.TrimSpace(target.EventType)
		target.ChangedFields = normalizeChangedFields(target.ChangedFields)
		identity := targetIdentity{
			key:                   key,
			action:                target.Action,
			expectedAccessVersion: target.ExpectedAccessVersion,
			mutates:               target.Mutates,
			eventType:             target.EventType,
		}
		if index, duplicate := seen[identity]; duplicate {
			targets[index].ChangedFields = normalizeChangedFields(append(
				targets[index].ChangedFields,
				target.ChangedFields...,
			))
			continue
		}
		seen[identity] = len(targets)
		targets = append(targets, target)
	}
	sort.SliceStable(targets, func(i, j int) bool {
		left, right := ResourceMutationKeyFromRef(targets[i].Ref), ResourceMutationKeyFromRef(targets[j].Ref)
		if left.ResourceType != right.ResourceType {
			return left.ResourceType < right.ResourceType
		}
		return left.ResourceID < right.ResourceID
	})
	return targets, nil
}

func normalizeCreatedResourceMutations(
	created []CreatedResourceMutation,
	allowedTypes []authz.ResourceType,
) ([]CreatedResourceMutation, error) {
	allowed := make(map[authz.ResourceType]struct{}, len(allowedTypes))
	for _, resourceType := range allowedTypes {
		allowed[resourceType] = struct{}{}
	}
	seen := make(map[ResourceMutationKey]struct{}, len(created))
	for index := range created {
		item := &created[index]
		if !item.Ref.Valid() || item.AccessVersion <= 0 {
			return nil, ErrResourceMutationUnavailable
		}
		if _, ok := allowed[item.Ref.Type()]; !ok {
			return nil, ErrResourceMutationUnavailable
		}
		key := ResourceMutationKeyFromRef(item.Ref)
		if _, duplicate := seen[key]; duplicate {
			return nil, ErrResourceMutationUnavailable
		}
		seen[key] = struct{}{}
		if err := validateResourceMutationEvent(item.EventType, item.ChangedFields); err != nil {
			return nil, err
		}
		item.OwnerUserID = cloneResourceMutationInt64Pointer(item.OwnerUserID)
		item.ChangedFields = normalizeChangedFields(item.ChangedFields)
	}
	return created, nil
}

func validateResourceMutationEvent(eventType string, changedFields []string) error {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" || len(eventType) > 64 || len(changedFields) == 0 {
		return ErrResourceMutationUnavailable
	}
	for _, field := range changedFields {
		field = strings.TrimSpace(field)
		if field == "" || len(field) > 64 {
			return ErrResourceMutationUnavailable
		}
		for _, character := range field {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') &&
				character != '_' && character != '.' && character != '-' {
				return ErrResourceMutationUnavailable
			}
		}
	}
	return nil
}

func normalizeChangedFields(fields []string) []string {
	set := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		set[strings.TrimSpace(field)] = struct{}{}
	}
	result := make([]string, 0, len(set))
	for field := range set {
		result = append(result, field)
	}
	sort.Strings(result)
	return result
}

func newResourceAuthorizationEventRecord(
	state ResourceMutationState,
	actor authz.Actor,
	eventType string,
	requestID string,
	changedFields []string,
) ResourceAuthorizationEventRecord {
	kind, actorID, _ := actor.DurableSubject()
	return ResourceAuthorizationEventRecord{
		Key:                   state.Key,
		OwnerUserID:           cloneResourceMutationInt64Pointer(state.OwnerUserID),
		ActorKind:             kind,
		ActorID:               actorID,
		AuthMethod:            actor.AuthMethod(),
		EventType:             strings.TrimSpace(eventType),
		ResourceAccessVersion: state.AccessVersion,
		RequestID:             requestID,
		ChangedFields:         normalizeChangedFields(changedFields),
	}
}

func resourceMutationDecisionError(
	decision authz.Decision,
	policyErr error,
	resourceType authz.ResourceType,
) error {
	if policyErr != nil {
		if errors.Is(policyErr, authz.ErrActorInactive) || errors.Is(policyErr, authz.ErrSessionInvalid) {
			return ErrResourceMutationConflict.WithCause(policyErr)
		}
		return ErrResourceMutationUnavailable.WithCause(policyErr)
	}
	if decision.Allowed() {
		return nil
	}
	switch class, _ := decision.DenialClass(); class {
	case authz.DenialClassNotFound:
		return resourceMutationNotFound(resourceType)
	case authz.DenialClassUnauthenticated:
		return ErrResourceMutationConflict
	case authz.DenialClassForbidden:
		return ErrResourceMutationForbidden
	default:
		return ErrResourceMutationUnavailable
	}
}

func resourceMutationNotFound(resourceType authz.ResourceType) error {
	if resourceType == authz.ResourceTypeAccount {
		return ErrAccountNotFound
	}
	if resourceType == authz.ResourceTypeGroup {
		return ErrGroupNotFound
	}
	return ErrResourceMutationUnavailable
}

func resourceMutationRequestID(ctx context.Context) string {
	if state, _ := ctx.Value(resourceMutationAuditContextKey{}).(*resourceMutationAuditContext); state != nil {
		return state.trace.RequestID
	}
	requestID, _ := ctx.Value(ctxkey.RequestID).(string)
	requestID = strings.TrimSpace(requestID)
	if len(requestID) > 64 {
		requestID = requestID[:64]
	}
	return requestID
}

func cloneResourceMutationInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func setPlatformResourceCreator(target **int64, actor authz.Actor) {
	if target == nil {
		return
	}
	*target = nil
	if userID, ok := actor.UserID(); ok && actor.AuthMethod() == authz.AuthMethodJWT {
		*target = &userID
	}
}
