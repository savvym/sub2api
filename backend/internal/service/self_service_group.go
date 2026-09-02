package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
)

const (
	maxSelfServiceGroupDescriptionRunes = 2000
	selfServiceGroupAuthorizationLegacy = "legacy"
)

var (
	ErrSelfServiceGroupUnavailable = infraerrors.ServiceUnavailable(
		"SELF_SERVICE_GROUP_UNAVAILABLE",
		"self-service group management is unavailable",
	)
	ErrSelfServiceGroupConflict = infraerrors.Conflict(
		"SELF_SERVICE_GROUP_CONFLICT",
		"the group or authorization state changed before this request was applied",
	)
	ErrSelfServiceGroupForbidden = infraerrors.Forbidden(
		"SELF_SERVICE_GROUP_FORBIDDEN",
		"self-service group management is not allowed",
	)
	ErrSelfServiceGroupActorRequired = infraerrors.Unauthorized(
		"SELF_SERVICE_GROUP_ACTOR_REQUIRED",
		"an active JWT user is required",
	)
	ErrSelfServiceGroupPlatformUnavailable = infraerrors.BadRequest(
		"SELF_SERVICE_GROUP_PLATFORM_UNAVAILABLE",
		"the selected self-service group platform is unavailable",
	)
	ErrSelfServiceGroupNotEmpty = infraerrors.Conflict(
		"SELF_SERVICE_GROUP_NOT_EMPTY",
		"the group still has resource references and cannot be deleted",
	)
	ErrInvalidSelfServiceGroup = infraerrors.BadRequest(
		"INVALID_SELF_SERVICE_GROUP",
		"invalid self-service group input",
	)
)

// SelfServiceGroupPlatform is the public server-owned platform choice exposed
// by the restricted group creation flow. Production starts with no choices.
type SelfServiceGroupPlatform struct {
	ID       string
	Name     string
	Platform string
}

// SelfServiceGroupCatalog prevents callers from supplying a platform string
// directly. Only reviewed static entries can be selected by ID.
type SelfServiceGroupCatalog struct {
	platforms []SelfServiceGroupPlatform
	byID      map[string]SelfServiceGroupPlatform
}

func NewSelfServiceGroupCatalog() *SelfServiceGroupCatalog {
	catalog, _ := NewStaticSelfServiceGroupCatalog(nil)
	return catalog
}

func NewStaticSelfServiceGroupCatalog(
	platforms []SelfServiceGroupPlatform,
) (*SelfServiceGroupCatalog, error) {
	catalog := &SelfServiceGroupCatalog{
		platforms: make([]SelfServiceGroupPlatform, 0, len(platforms)),
		byID:      make(map[string]SelfServiceGroupPlatform, len(platforms)),
	}
	for _, platform := range platforms {
		normalized, err := normalizeSelfServiceGroupPlatform(platform)
		if err != nil {
			return nil, err
		}
		if _, exists := catalog.byID[normalized.ID]; exists {
			return nil, fmt.Errorf("duplicate self-service group platform %q", normalized.ID)
		}
		catalog.platforms = append(catalog.platforms, normalized)
		catalog.byID[normalized.ID] = normalized
	}
	return catalog, nil
}

func normalizeSelfServiceGroupPlatform(
	platform SelfServiceGroupPlatform,
) (SelfServiceGroupPlatform, error) {
	platform.ID = strings.TrimSpace(platform.ID)
	platform.Name = strings.TrimSpace(platform.Name)
	platform.Platform = strings.TrimSpace(platform.Platform)
	if platform.ID == "" || platform.Name == "" || len(platform.ID) > 64 ||
		len([]rune(platform.Name)) > maxGroupNameRunes ||
		!selfServiceCandidatePlatform(platform.Platform) {
		return SelfServiceGroupPlatform{}, fmt.Errorf("invalid self-service group platform")
	}
	for _, value := range []string{platform.ID, platform.Name, platform.Platform} {
		if !utf8.ValidString(value) {
			return SelfServiceGroupPlatform{}, fmt.Errorf("invalid self-service group platform")
		}
		for _, character := range value {
			if unicode.IsControl(character) {
				return SelfServiceGroupPlatform{}, fmt.Errorf("invalid self-service group platform")
			}
		}
	}
	return platform, nil
}

func (c *SelfServiceGroupCatalog) List() []SelfServiceGroupPlatform {
	if c == nil {
		return []SelfServiceGroupPlatform{}
	}
	result := make([]SelfServiceGroupPlatform, len(c.platforms))
	copy(result, c.platforms)
	return result
}

func (c *SelfServiceGroupCatalog) Resolve(id string) (SelfServiceGroupPlatform, bool) {
	if c == nil {
		return SelfServiceGroupPlatform{}, false
	}
	platform, ok := c.byID[strings.TrimSpace(id)]
	return platform, ok
}

type SelfServiceGroupState struct {
	GroupListItem
	CreatedByUserID   *int64
	AccessVersion     int64
	AuthorizationMode string
	IsExclusive       bool
	Deleted           bool
}

type SelfServiceGroupCreateRecord struct {
	Name          string
	Description   string
	Platform      string
	OwnerUserID   int64
	CreatorUserID int64
}

type SelfServiceGroupRepository interface {
	WithSerializableTx(ctx context.Context, fn func(txCtx context.Context) error) error
	LockActorAuthorization(ctx context.Context, userID int64) error
	LockGroup(ctx context.Context, groupID int64) (SelfServiceGroupState, error)
	CreateGroup(ctx context.Context, input SelfServiceGroupCreateRecord) (SelfServiceGroupState, error)
	UpdateGroup(
		ctx context.Context,
		groupID int64,
		ownerUserID int64,
		expectedAccessVersion int64,
		name string,
		description string,
	) (SelfServiceGroupState, error)
	DeleteGroup(
		ctx context.Context,
		groupID int64,
		ownerUserID int64,
		expectedAccessVersion int64,
	) (SelfServiceGroupState, error)
	AppendAuthorizationEvent(ctx context.Context, event ResourceAuthorizationEventRecord) error
}

type SelfServiceGroupCreateInput struct {
	Actor       authz.Actor
	Name        string
	Description string
	PlatformID  string
	RequestID   string
}

type SelfServiceGroupUpdateInput struct {
	Actor       authz.Actor
	GroupID     int64
	Name        *string
	Description *string
	RequestID   string
}

type SelfServiceGroupDeleteInput struct {
	Actor     authz.Actor
	GroupID   int64
	RequestID string
}

type SelfServiceGroupService struct {
	repository SelfServiceGroupRepository
	resolver   authz.Resolver
	policy     authz.ResourcePolicy
	capacity   HostingCapacityGuard
	reader     *ResourceReadService
	catalog    *SelfServiceGroupCatalog
}

func NewSelfServiceGroupService(
	repository SelfServiceGroupRepository,
	resolver authz.Resolver,
	policy authz.ResourcePolicy,
	capacity HostingCapacityGuard,
	reader *ResourceReadService,
	catalog *SelfServiceGroupCatalog,
) *SelfServiceGroupService {
	return &SelfServiceGroupService{
		repository: repository,
		resolver:   resolver,
		policy:     policy,
		capacity:   capacity,
		reader:     reader,
		catalog:    catalog,
	}
}

func (s *SelfServiceGroupService) ListGroups(
	ctx context.Context,
	actor authz.Actor,
	query GroupReadQuery,
) ([]GroupListItem, *pagination.PaginationResult, error) {
	if err := validateSelfServiceGroupActor(actor); err != nil {
		return nil, nil, err
	}
	if s == nil || s.reader == nil || ctx == nil {
		return nil, nil, ErrSelfServiceGroupUnavailable
	}
	items, result, err := s.reader.ListGroups(ctx, actor, query)
	if err != nil {
		return nil, nil, selfServiceGroupError(err)
	}
	return items, result, nil
}

func (s *SelfServiceGroupService) GetGroup(
	ctx context.Context,
	actor authz.Actor,
	groupID int64,
) (*GroupListItem, error) {
	if err := validateSelfServiceGroupActor(actor); err != nil {
		return nil, err
	}
	if groupID <= 0 {
		return nil, ErrInvalidResourceReadID
	}
	if s == nil || s.reader == nil || ctx == nil {
		return nil, ErrSelfServiceGroupUnavailable
	}
	item, err := s.reader.GetGroup(ctx, actor, groupID)
	if err != nil {
		return nil, selfServiceGroupError(err)
	}
	return item, nil
}

func (s *SelfServiceGroupService) ListPlatforms(
	ctx context.Context,
	actor authz.Actor,
) ([]SelfServiceGroupPlatform, error) {
	if err := validateSelfServiceGroupActor(actor); err != nil {
		return nil, err
	}
	if s == nil || s.policy == nil || s.catalog == nil || ctx == nil {
		return nil, ErrSelfServiceGroupUnavailable
	}
	decision, err := s.policy.CanCreate(ctx, actor, authz.ResourceTypeGroup)
	if err != nil {
		return nil, selfServiceGroupError(err)
	}
	if !decision.Allowed() {
		return nil, selfServiceGroupDecisionError(decision, false)
	}
	return s.catalog.List(), nil
}

func (s *SelfServiceGroupService) CreateGroup(
	ctx context.Context,
	input SelfServiceGroupCreateInput,
) (*GroupListItem, error) {
	userID, err := selfServiceGroupActorUserID(input.Actor)
	if err != nil {
		return nil, err
	}
	name, err := normalizeSelfServiceGroupName(input.Name)
	if err != nil {
		return nil, err
	}
	description, err := normalizeSelfServiceGroupDescription(input.Description)
	if err != nil {
		return nil, err
	}
	if s == nil || s.repository == nil || s.capacity == nil || s.catalog == nil || ctx == nil {
		return nil, ErrSelfServiceGroupUnavailable
	}
	platform, ok := s.catalog.Resolve(input.PlatformID)
	if !ok {
		return nil, ErrSelfServiceGroupPlatformUnavailable
	}

	var created SelfServiceGroupState
	err = s.repository.WithSerializableTx(ctx, func(txCtx context.Context) error {
		if _, capacityErr := s.capacity.RequireCreateCapacity(
			txCtx,
			input.Actor,
			authz.ResourceTypeGroup,
		); capacityErr != nil {
			return capacityErr
		}
		var createErr error
		created, createErr = s.repository.CreateGroup(txCtx, SelfServiceGroupCreateRecord{
			Name:          name,
			Description:   description,
			Platform:      platform.Platform,
			OwnerUserID:   userID,
			CreatorUserID: userID,
		})
		if createErr != nil {
			return createErr
		}
		if err := validateSelfServiceCreatedGroup(created, userID, platform); err != nil {
			return err
		}
		return s.repository.AppendAuthorizationEvent(txCtx, ResourceAuthorizationEventRecord{
			Key: ResourceMutationKey{
				ResourceType: authz.ResourceTypeGroup,
				ResourceID:   created.ID,
			},
			OwnerUserID:           int64Pointer(userID),
			ActorKind:             authz.SubjectKindUser,
			ActorID:               userID,
			AuthMethod:            authz.AuthMethodJWT,
			EventType:             "group.created",
			ResourceAccessVersion: created.AccessVersion,
			RequestID:             boundedSelfServiceRequestID(input.RequestID),
			ChangedFields:         []string{"configuration", "ownership"},
		})
	})
	if err != nil {
		return nil, selfServiceGroupError(err)
	}
	result := created.GroupListItem
	return &result, nil
}

func (s *SelfServiceGroupService) UpdateGroup(
	ctx context.Context,
	input SelfServiceGroupUpdateInput,
) (*GroupListItem, error) {
	userID, err := selfServiceGroupActorUserID(input.Actor)
	if err != nil {
		return nil, err
	}
	if input.GroupID <= 0 {
		return nil, ErrInvalidResourceReadID
	}
	if input.Name == nil && input.Description == nil {
		return nil, ErrInvalidSelfServiceGroup
	}
	var normalizedName *string
	if input.Name != nil {
		value, normalizeErr := normalizeSelfServiceGroupName(*input.Name)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		normalizedName = &value
	}
	var normalizedDescription *string
	if input.Description != nil {
		value, normalizeErr := normalizeSelfServiceGroupDescription(*input.Description)
		if normalizeErr != nil {
			return nil, normalizeErr
		}
		normalizedDescription = &value
	}
	if s == nil || s.repository == nil || s.resolver == nil || s.policy == nil || ctx == nil {
		return nil, ErrSelfServiceGroupUnavailable
	}

	var updated SelfServiceGroupState
	err = s.repository.WithSerializableTx(ctx, func(txCtx context.Context) error {
		locked, currentActor, lockErr := s.lockOwnedGroup(
			txCtx,
			input.Actor,
			userID,
			input.GroupID,
			authz.ActionGroupEdit,
		)
		if lockErr != nil {
			return lockErr
		}
		name := locked.Name
		description := locked.Description
		changedFields := make([]string, 0, 2)
		if normalizedName != nil {
			name = *normalizedName
			if name != locked.Name {
				changedFields = append(changedFields, "name")
			}
		}
		if normalizedDescription != nil {
			description = *normalizedDescription
			if description != locked.Description {
				changedFields = append(changedFields, "description")
			}
		}
		if len(changedFields) == 0 {
			updated = locked
			return nil
		}
		updated, lockErr = s.repository.UpdateGroup(
			txCtx,
			input.GroupID,
			userID,
			locked.AccessVersion,
			name,
			description,
		)
		if lockErr != nil {
			return lockErr
		}
		return s.appendOwnedGroupEvent(
			txCtx,
			currentActor,
			updated,
			"group.updated",
			changedFields,
			input.RequestID,
		)
	})
	if err != nil {
		return nil, selfServiceGroupError(err)
	}
	result := updated.GroupListItem
	return &result, nil
}

func (s *SelfServiceGroupService) DeleteGroup(
	ctx context.Context,
	input SelfServiceGroupDeleteInput,
) (*GroupListItem, error) {
	userID, err := selfServiceGroupActorUserID(input.Actor)
	if err != nil {
		return nil, err
	}
	if input.GroupID <= 0 {
		return nil, ErrInvalidResourceReadID
	}
	if s == nil || s.repository == nil || s.resolver == nil || s.policy == nil || ctx == nil {
		return nil, ErrSelfServiceGroupUnavailable
	}

	var deleted SelfServiceGroupState
	err = s.repository.WithSerializableTx(ctx, func(txCtx context.Context) error {
		locked, currentActor, lockErr := s.lockOwnedGroup(
			txCtx,
			input.Actor,
			userID,
			input.GroupID,
			authz.ActionGroupDelete,
		)
		if lockErr != nil {
			return lockErr
		}
		deleted, lockErr = s.repository.DeleteGroup(
			txCtx,
			input.GroupID,
			userID,
			locked.AccessVersion,
		)
		if lockErr != nil {
			return lockErr
		}
		return s.appendOwnedGroupEvent(
			txCtx,
			currentActor,
			deleted,
			"group.deleted",
			[]string{"lifecycle"},
			input.RequestID,
		)
	})
	if err != nil {
		return nil, selfServiceGroupError(err)
	}
	result := deleted.GroupListItem
	return &result, nil
}

func (s *SelfServiceGroupService) lockOwnedGroup(
	ctx context.Context,
	requestActor authz.Actor,
	userID int64,
	groupID int64,
	action authz.Action,
) (SelfServiceGroupState, authz.Actor, error) {
	if err := s.repository.LockActorAuthorization(ctx, userID); err != nil {
		return SelfServiceGroupState{}, authz.Actor{}, err
	}
	locked, err := s.repository.LockGroup(ctx, groupID)
	if err != nil {
		return SelfServiceGroupState{}, authz.Actor{}, err
	}
	if locked.Deleted || locked.OwnerUserID == nil || *locked.OwnerUserID != userID {
		return SelfServiceGroupState{}, authz.Actor{}, ErrGroupNotFound
	}
	currentActor, err := s.resolver.ResolveUser(ctx, userID, authz.AuthMethodJWT)
	if err != nil {
		return SelfServiceGroupState{}, authz.Actor{}, err
	}
	if !requestActor.SameAuthorizationState(currentActor) {
		return SelfServiceGroupState{}, authz.Actor{}, ErrSelfServiceGroupConflict
	}
	ref, err := authz.NewResourceRef(authz.ResourceTypeGroup, groupID)
	if err != nil {
		return SelfServiceGroupState{}, authz.Actor{}, ErrSelfServiceGroupUnavailable.WithCause(err)
	}
	decision, err := s.policy.Authorize(ctx, currentActor, action, ref)
	if err != nil {
		return SelfServiceGroupState{}, authz.Actor{}, err
	}
	if !decision.Allowed() {
		return SelfServiceGroupState{}, authz.Actor{}, selfServiceGroupDecisionError(decision, true)
	}
	return locked, currentActor, nil
}

func (s *SelfServiceGroupService) appendOwnedGroupEvent(
	ctx context.Context,
	actor authz.Actor,
	state SelfServiceGroupState,
	eventType string,
	changedFields []string,
	requestID string,
) error {
	actorKind, actorID, ok := actor.DurableSubject()
	if !ok || actorKind != authz.SubjectKindUser || state.OwnerUserID == nil ||
		*state.OwnerUserID != actorID || state.AccessVersion <= 0 {
		return ErrSelfServiceGroupUnavailable
	}
	return s.repository.AppendAuthorizationEvent(ctx, ResourceAuthorizationEventRecord{
		Key: ResourceMutationKey{
			ResourceType: authz.ResourceTypeGroup,
			ResourceID:   state.ID,
		},
		OwnerUserID:           int64Pointer(actorID),
		ActorKind:             actorKind,
		ActorID:               actorID,
		AuthMethod:            authz.AuthMethodJWT,
		EventType:             eventType,
		ResourceAccessVersion: state.AccessVersion,
		RequestID:             boundedSelfServiceRequestID(requestID),
		ChangedFields:         append([]string(nil), changedFields...),
	})
}

func validateSelfServiceCreatedGroup(
	state SelfServiceGroupState,
	ownerUserID int64,
	platform SelfServiceGroupPlatform,
) error {
	if state.ID <= 0 || state.Deleted || state.AccessVersion != 1 ||
		state.OwnerUserID == nil || *state.OwnerUserID != ownerUserID ||
		state.CreatedByUserID == nil || *state.CreatedByUserID != ownerUserID ||
		state.PublicAccessLevel != nil || state.Platform != platform.Platform ||
		state.Status != StatusActive || !state.IsExclusive ||
		state.AuthorizationMode != selfServiceGroupAuthorizationLegacy {
		return ErrSelfServiceGroupUnavailable
	}
	return nil
}

func validateSelfServiceGroupActor(actor authz.Actor) error {
	_, err := selfServiceGroupActorUserID(actor)
	return err
}

func selfServiceGroupActorUserID(actor authz.Actor) (int64, error) {
	userID, ok := actor.UserID()
	if !ok || actor.AuthMethod() != authz.AuthMethodJWT {
		return 0, ErrSelfServiceGroupActorRequired
	}
	return userID, nil
}

func normalizeSelfServiceGroupName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || len([]rune(value)) > maxGroupNameRunes {
		return "", ErrInvalidSelfServiceGroup
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", ErrInvalidSelfServiceGroup
		}
	}
	return value, nil
}

func normalizeSelfServiceGroupDescription(value string) (string, error) {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) || len([]rune(value)) > maxSelfServiceGroupDescriptionRunes {
		return "", ErrInvalidSelfServiceGroup
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\n' && character != '\t' {
			return "", ErrInvalidSelfServiceGroup
		}
	}
	return value, nil
}

func selfServiceGroupDecisionError(decision authz.Decision, conceal bool) error {
	reason := decision.DenyReason()
	class, ok := reason.Class()
	if !ok {
		return ErrSelfServiceGroupUnavailable
	}
	if conceal && class == authz.DenialClassNotFound {
		return ErrGroupNotFound
	}
	switch class {
	case authz.DenialClassForbidden:
		return ErrSelfServiceGroupForbidden.WithMetadata(map[string]string{"reason": string(reason)})
	case authz.DenialClassUnauthenticated:
		return ErrSelfServiceGroupActorRequired
	case authz.DenialClassNotFound:
		return ErrGroupNotFound
	default:
		return ErrSelfServiceGroupUnavailable.WithMetadata(map[string]string{"reason": string(reason)})
	}
}

func selfServiceGroupError(err error) error {
	if err == nil {
		return nil
	}
	var applicationErr *infraerrors.ApplicationError
	if errors.As(err, &applicationErr) {
		return err
	}
	if errors.Is(err, ErrGroupNotFound) {
		return ErrGroupNotFound
	}
	if errors.Is(err, authz.ErrFeatureDisabled) || errors.Is(err, authz.ErrPolicyAccessDenied) {
		return ErrSelfServiceGroupForbidden
	}
	if errors.Is(err, authz.ErrInvalidActor) || errors.Is(err, authz.ErrActorInactive) ||
		errors.Is(err, authz.ErrSessionInvalid) {
		return ErrSelfServiceGroupActorRequired
	}
	if errors.Is(err, authz.ErrAuthorizationUnavailable) || errors.Is(err, authz.ErrInvalidPolicySnapshot) {
		return ErrSelfServiceGroupUnavailable.WithCause(err)
	}
	return ErrSelfServiceGroupUnavailable.WithCause(err)
}
