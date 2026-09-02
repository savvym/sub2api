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

const maxSelfServiceAPIKeyBytes = 16 * 1024

var (
	ErrSelfServiceAccountUnavailable = infraerrors.ServiceUnavailable(
		"SELF_SERVICE_ACCOUNT_UNAVAILABLE",
		"self-service account management is unavailable",
	)
	ErrSelfServiceAccountConflict = infraerrors.Conflict(
		"SELF_SERVICE_ACCOUNT_CONFLICT",
		"the account or authorization state changed before this request was applied",
	)
	ErrSelfServiceAccountForbidden = infraerrors.Forbidden(
		"SELF_SERVICE_ACCOUNT_FORBIDDEN",
		"self-service account management is not allowed",
	)
	ErrSelfServiceAccountActorRequired = infraerrors.Unauthorized(
		"SELF_SERVICE_ACCOUNT_ACTOR_REQUIRED",
		"an active JWT user is required",
	)
	ErrSelfServiceAccountProductUnavailable = infraerrors.BadRequest(
		"SELF_SERVICE_ACCOUNT_PRODUCT_UNAVAILABLE",
		"the selected self-service account product is unavailable",
	)
	ErrInvalidSelfServiceAccount = infraerrors.BadRequest(
		"INVALID_SELF_SERVICE_ACCOUNT",
		"invalid self-service account input",
	)
)

// SelfServiceAccountProduct is the public, non-secret description used by the
// simplified account creation flow. Production starts with an empty catalog.
type SelfServiceAccountProduct struct {
	ID          string
	Name        string
	Platform    string
	AccountType string
}

// SelfServiceAccountCatalog is an immutable server-side allowlist. A request
// selects a product ID; it never supplies platform, auth type, or endpoint
// configuration directly.
type SelfServiceAccountCatalog struct {
	products []SelfServiceAccountProduct
	byID     map[string]SelfServiceAccountProduct
}

// NewSelfServiceAccountCatalog returns the production catalog. The current
// security decision keeps every candidate product disabled.
func NewSelfServiceAccountCatalog() *SelfServiceAccountCatalog {
	catalog, _ := NewStaticSelfServiceAccountCatalog(nil)
	return catalog
}

// NewStaticSelfServiceAccountCatalog creates an explicit API-key-only catalog
// for focused tests and future reviewed composition roots.
func NewStaticSelfServiceAccountCatalog(
	products []SelfServiceAccountProduct,
) (*SelfServiceAccountCatalog, error) {
	catalog := &SelfServiceAccountCatalog{
		products: make([]SelfServiceAccountProduct, 0, len(products)),
		byID:     make(map[string]SelfServiceAccountProduct, len(products)),
	}
	for _, product := range products {
		normalized, err := normalizeSelfServiceAccountProduct(product)
		if err != nil {
			return nil, err
		}
		if _, exists := catalog.byID[normalized.ID]; exists {
			return nil, fmt.Errorf("duplicate self-service account product %q", normalized.ID)
		}
		catalog.products = append(catalog.products, normalized)
		catalog.byID[normalized.ID] = normalized
	}
	return catalog, nil
}

func normalizeSelfServiceAccountProduct(
	product SelfServiceAccountProduct,
) (SelfServiceAccountProduct, error) {
	product.ID = strings.TrimSpace(product.ID)
	product.Name = strings.TrimSpace(product.Name)
	product.Platform = strings.TrimSpace(product.Platform)
	product.AccountType = strings.TrimSpace(product.AccountType)
	if product.ID == "" || product.Name == "" || len(product.ID) > 64 || len(product.Name) > 100 ||
		product.AccountType != AccountTypeAPIKey || !selfServiceCandidatePlatform(product.Platform) {
		return SelfServiceAccountProduct{}, fmt.Errorf("invalid self-service account product")
	}
	for _, value := range []string{product.ID, product.Name, product.Platform, product.AccountType} {
		if !utf8.ValidString(value) {
			return SelfServiceAccountProduct{}, fmt.Errorf("invalid self-service account product")
		}
		for _, character := range value {
			if unicode.IsControl(character) {
				return SelfServiceAccountProduct{}, fmt.Errorf("invalid self-service account product")
			}
		}
	}
	return product, nil
}

func selfServiceCandidatePlatform(platform string) bool {
	switch platform {
	case PlatformOpenAI, PlatformAnthropic, PlatformGemini:
		return true
	default:
		return false
	}
}

func (c *SelfServiceAccountCatalog) List() []SelfServiceAccountProduct {
	if c == nil {
		return []SelfServiceAccountProduct{}
	}
	result := make([]SelfServiceAccountProduct, len(c.products))
	copy(result, c.products)
	return result
}

func (c *SelfServiceAccountCatalog) Resolve(id string) (SelfServiceAccountProduct, bool) {
	if c == nil {
		return SelfServiceAccountProduct{}, false
	}
	product, ok := c.byID[strings.TrimSpace(id)]
	return product, ok
}

type SelfServiceAccountState struct {
	AccountListItem
	AccessVersion int64
	Deleted       bool
}

type SelfServiceAccountCreateRecord struct {
	Name          string
	Platform      string
	AccountType   string
	APIKey        string
	OwnerUserID   int64
	CreatorUserID int64
}

type SelfServiceAccountRepository interface {
	WithSerializableTx(ctx context.Context, fn func(txCtx context.Context) error) error
	LockActorAuthorization(ctx context.Context, userID int64) error
	LockAccount(ctx context.Context, accountID int64) (SelfServiceAccountState, error)
	CreateAccount(ctx context.Context, input SelfServiceAccountCreateRecord) (SelfServiceAccountState, error)
	RenameAccount(
		ctx context.Context,
		accountID int64,
		ownerUserID int64,
		expectedAccessVersion int64,
		name string,
	) (SelfServiceAccountState, error)
	DeleteAccount(
		ctx context.Context,
		accountID int64,
		ownerUserID int64,
		expectedAccessVersion int64,
	) (SelfServiceAccountState, error)
	AppendAuthorizationEvent(ctx context.Context, event ResourceAuthorizationEventRecord) error
}

type SelfServiceAccountCreateInput struct {
	Actor     authz.Actor
	Name      string
	ProductID string
	APIKey    string
	RequestID string
}

type SelfServiceAccountRenameInput struct {
	Actor     authz.Actor
	AccountID int64
	Name      string
	RequestID string
}

type SelfServiceAccountDeleteInput struct {
	Actor     authz.Actor
	AccountID int64
	RequestID string
}

type SelfServiceAccountService struct {
	repository SelfServiceAccountRepository
	resolver   authz.Resolver
	policy     authz.ResourcePolicy
	capacity   HostingCapacityGuard
	reader     *ResourceReadService
	catalog    *SelfServiceAccountCatalog
}

func NewSelfServiceAccountService(
	repository SelfServiceAccountRepository,
	resolver authz.Resolver,
	policy authz.ResourcePolicy,
	capacity HostingCapacityGuard,
	reader *ResourceReadService,
	catalog *SelfServiceAccountCatalog,
) *SelfServiceAccountService {
	return &SelfServiceAccountService{
		repository: repository,
		resolver:   resolver,
		policy:     policy,
		capacity:   capacity,
		reader:     reader,
		catalog:    catalog,
	}
}

func (s *SelfServiceAccountService) ListAccounts(
	ctx context.Context,
	actor authz.Actor,
	query AccountReadQuery,
) ([]AccountListItem, *pagination.PaginationResult, error) {
	if err := validateSelfServiceAccountActor(actor); err != nil {
		return nil, nil, err
	}
	if s == nil || s.reader == nil || ctx == nil {
		return nil, nil, ErrSelfServiceAccountUnavailable
	}
	items, result, err := s.reader.ListAccounts(ctx, actor, query)
	if err != nil {
		return nil, nil, selfServiceAccountError(err)
	}
	return items, result, nil
}

func (s *SelfServiceAccountService) GetAccount(
	ctx context.Context,
	actor authz.Actor,
	accountID int64,
) (*AccountListItem, error) {
	if err := validateSelfServiceAccountActor(actor); err != nil {
		return nil, err
	}
	if accountID <= 0 {
		return nil, ErrInvalidResourceReadID
	}
	if s == nil || s.reader == nil || ctx == nil {
		return nil, ErrSelfServiceAccountUnavailable
	}
	item, err := s.reader.GetAccount(ctx, actor, accountID)
	if err != nil {
		return nil, selfServiceAccountError(err)
	}
	return item, nil
}

func (s *SelfServiceAccountService) ListProducts(
	ctx context.Context,
	actor authz.Actor,
) ([]SelfServiceAccountProduct, error) {
	if err := validateSelfServiceAccountActor(actor); err != nil {
		return nil, err
	}
	if s == nil || s.policy == nil || s.catalog == nil || ctx == nil {
		return nil, ErrSelfServiceAccountUnavailable
	}
	decision, err := s.policy.CanCreate(ctx, actor, authz.ResourceTypeAccount)
	if err != nil {
		return nil, selfServiceAccountError(err)
	}
	if !decision.Allowed() {
		return nil, selfServiceAccountDecisionError(decision, false)
	}
	return s.catalog.List(), nil
}

func (s *SelfServiceAccountService) CreateAccount(
	ctx context.Context,
	input SelfServiceAccountCreateInput,
) (*AccountListItem, error) {
	userID, err := selfServiceAccountActorUserID(input.Actor)
	if err != nil {
		return nil, err
	}
	name, err := normalizeSelfServiceAccountName(input.Name)
	if err != nil {
		return nil, err
	}
	apiKey, err := normalizeSelfServiceAPIKey(input.APIKey)
	if err != nil {
		return nil, err
	}
	if s == nil || s.repository == nil || s.capacity == nil || s.catalog == nil || ctx == nil {
		return nil, ErrSelfServiceAccountUnavailable
	}
	product, ok := s.catalog.Resolve(input.ProductID)
	if !ok {
		return nil, ErrSelfServiceAccountProductUnavailable
	}

	var created SelfServiceAccountState
	err = s.repository.WithSerializableTx(ctx, func(txCtx context.Context) error {
		if _, capacityErr := s.capacity.RequireCreateCapacity(
			txCtx,
			input.Actor,
			authz.ResourceTypeAccount,
		); capacityErr != nil {
			return capacityErr
		}
		var createErr error
		created, createErr = s.repository.CreateAccount(txCtx, SelfServiceAccountCreateRecord{
			Name:          name,
			Platform:      product.Platform,
			AccountType:   product.AccountType,
			APIKey:        apiKey,
			OwnerUserID:   userID,
			CreatorUserID: userID,
		})
		if createErr != nil {
			return createErr
		}
		if err := validateSelfServiceCreatedAccount(created, userID, product); err != nil {
			return err
		}
		return s.repository.AppendAuthorizationEvent(txCtx, ResourceAuthorizationEventRecord{
			Key: ResourceMutationKey{
				ResourceType: authz.ResourceTypeAccount,
				ResourceID:   created.ID,
			},
			OwnerUserID:           int64Pointer(userID),
			ActorKind:             authz.SubjectKindUser,
			ActorID:               userID,
			AuthMethod:            authz.AuthMethodJWT,
			EventType:             "account.created",
			ResourceAccessVersion: created.AccessVersion,
			RequestID:             boundedSelfServiceRequestID(input.RequestID),
			ChangedFields:         []string{"configuration", "credentials", "ownership"},
		})
	})
	if err != nil {
		return nil, selfServiceAccountError(err)
	}
	result := created.AccountListItem
	return &result, nil
}

func (s *SelfServiceAccountService) RenameAccount(
	ctx context.Context,
	input SelfServiceAccountRenameInput,
) (*AccountListItem, error) {
	userID, err := selfServiceAccountActorUserID(input.Actor)
	if err != nil {
		return nil, err
	}
	if input.AccountID <= 0 {
		return nil, ErrInvalidResourceReadID
	}
	name, err := normalizeSelfServiceAccountName(input.Name)
	if err != nil {
		return nil, err
	}
	if s == nil || s.repository == nil || s.resolver == nil || s.policy == nil || ctx == nil {
		return nil, ErrSelfServiceAccountUnavailable
	}

	var updated SelfServiceAccountState
	err = s.repository.WithSerializableTx(ctx, func(txCtx context.Context) error {
		locked, currentActor, lockErr := s.lockOwnedAccount(
			txCtx,
			input.Actor,
			userID,
			input.AccountID,
			authz.ActionAccountEdit,
		)
		if lockErr != nil {
			return lockErr
		}
		updated, lockErr = s.repository.RenameAccount(
			txCtx,
			input.AccountID,
			userID,
			locked.AccessVersion,
			name,
		)
		if lockErr != nil {
			return lockErr
		}
		return s.appendOwnedAccountEvent(
			txCtx,
			currentActor,
			updated,
			"account.updated",
			[]string{"name"},
			input.RequestID,
		)
	})
	if err != nil {
		return nil, selfServiceAccountError(err)
	}
	result := updated.AccountListItem
	return &result, nil
}

func (s *SelfServiceAccountService) DeleteAccount(
	ctx context.Context,
	input SelfServiceAccountDeleteInput,
) (*AccountListItem, error) {
	userID, err := selfServiceAccountActorUserID(input.Actor)
	if err != nil {
		return nil, err
	}
	if input.AccountID <= 0 {
		return nil, ErrInvalidResourceReadID
	}
	if s == nil || s.repository == nil || s.resolver == nil || s.policy == nil || ctx == nil {
		return nil, ErrSelfServiceAccountUnavailable
	}

	var deleted SelfServiceAccountState
	err = s.repository.WithSerializableTx(ctx, func(txCtx context.Context) error {
		locked, currentActor, lockErr := s.lockOwnedAccount(
			txCtx,
			input.Actor,
			userID,
			input.AccountID,
			authz.ActionAccountDelete,
		)
		if lockErr != nil {
			return lockErr
		}
		deleted, lockErr = s.repository.DeleteAccount(
			txCtx,
			input.AccountID,
			userID,
			locked.AccessVersion,
		)
		if lockErr != nil {
			return lockErr
		}
		return s.appendOwnedAccountEvent(
			txCtx,
			currentActor,
			deleted,
			"account.deleted",
			[]string{"lifecycle"},
			input.RequestID,
		)
	})
	if err != nil {
		return nil, selfServiceAccountError(err)
	}
	result := deleted.AccountListItem
	return &result, nil
}

func (s *SelfServiceAccountService) lockOwnedAccount(
	ctx context.Context,
	requestActor authz.Actor,
	userID int64,
	accountID int64,
	action authz.Action,
) (SelfServiceAccountState, authz.Actor, error) {
	if err := s.repository.LockActorAuthorization(ctx, userID); err != nil {
		return SelfServiceAccountState{}, authz.Actor{}, err
	}
	locked, err := s.repository.LockAccount(ctx, accountID)
	if err != nil {
		return SelfServiceAccountState{}, authz.Actor{}, err
	}
	if locked.Deleted || locked.OwnerUserID == nil || *locked.OwnerUserID != userID {
		return SelfServiceAccountState{}, authz.Actor{}, ErrAccountNotFound
	}
	currentActor, err := s.resolver.ResolveUser(ctx, userID, authz.AuthMethodJWT)
	if err != nil {
		return SelfServiceAccountState{}, authz.Actor{}, err
	}
	if !requestActor.SameAuthorizationState(currentActor) {
		return SelfServiceAccountState{}, authz.Actor{}, ErrSelfServiceAccountConflict
	}
	ref, err := authz.NewResourceRef(authz.ResourceTypeAccount, accountID)
	if err != nil {
		return SelfServiceAccountState{}, authz.Actor{}, ErrSelfServiceAccountUnavailable.WithCause(err)
	}
	decision, err := s.policy.Authorize(ctx, currentActor, action, ref)
	if err != nil {
		return SelfServiceAccountState{}, authz.Actor{}, err
	}
	if !decision.Allowed() {
		return SelfServiceAccountState{}, authz.Actor{}, selfServiceAccountDecisionError(decision, true)
	}
	return locked, currentActor, nil
}

func (s *SelfServiceAccountService) appendOwnedAccountEvent(
	ctx context.Context,
	actor authz.Actor,
	state SelfServiceAccountState,
	eventType string,
	changedFields []string,
	requestID string,
) error {
	actorKind, actorID, ok := actor.DurableSubject()
	if !ok || actorKind != authz.SubjectKindUser || state.OwnerUserID == nil ||
		*state.OwnerUserID != actorID || state.AccessVersion <= 0 {
		return ErrSelfServiceAccountUnavailable
	}
	return s.repository.AppendAuthorizationEvent(ctx, ResourceAuthorizationEventRecord{
		Key: ResourceMutationKey{
			ResourceType: authz.ResourceTypeAccount,
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

func validateSelfServiceCreatedAccount(
	state SelfServiceAccountState,
	ownerUserID int64,
	product SelfServiceAccountProduct,
) error {
	if state.ID <= 0 || state.Deleted || state.AccessVersion != 1 || state.OwnerUserID == nil ||
		*state.OwnerUserID != ownerUserID || state.PublicAccessLevel != nil ||
		state.Platform != product.Platform || state.Type != product.AccountType ||
		!state.CredentialConfigured {
		return ErrSelfServiceAccountUnavailable
	}
	return nil
}

func validateSelfServiceAccountActor(actor authz.Actor) error {
	_, err := selfServiceAccountActorUserID(actor)
	return err
}

func selfServiceAccountActorUserID(actor authz.Actor) (int64, error) {
	userID, ok := actor.UserID()
	if !ok || actor.AuthMethod() != authz.AuthMethodJWT {
		return 0, ErrSelfServiceAccountActorRequired
	}
	return userID, nil
}

func normalizeSelfServiceAccountName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || len([]rune(value)) > maxAccountNameRunes {
		return "", ErrInvalidSelfServiceAccount
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", ErrInvalidSelfServiceAccount
		}
	}
	return value, nil
}

func normalizeSelfServiceAPIKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || !utf8.ValidString(value) || len(value) > maxSelfServiceAPIKeyBytes {
		return "", ErrInvalidSelfServiceAccount
	}
	for _, character := range value {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return "", ErrInvalidSelfServiceAccount
		}
	}
	return value, nil
}

func boundedSelfServiceRequestID(value string) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > 64 {
		runes = runes[:64]
	}
	return string(runes)
}

func int64Pointer(value int64) *int64 {
	copyValue := value
	return &copyValue
}

func selfServiceAccountDecisionError(decision authz.Decision, conceal bool) error {
	reason := decision.DenyReason()
	class, ok := reason.Class()
	if !ok {
		return ErrSelfServiceAccountUnavailable
	}
	if conceal && class == authz.DenialClassNotFound {
		return ErrAccountNotFound
	}
	switch class {
	case authz.DenialClassForbidden:
		return ErrSelfServiceAccountForbidden.WithMetadata(map[string]string{"reason": string(reason)})
	case authz.DenialClassUnauthenticated:
		return ErrSelfServiceAccountActorRequired
	case authz.DenialClassNotFound:
		return ErrAccountNotFound
	default:
		return ErrSelfServiceAccountUnavailable.WithMetadata(map[string]string{"reason": string(reason)})
	}
}

func selfServiceAccountError(err error) error {
	if err == nil {
		return nil
	}
	var applicationErr *infraerrors.ApplicationError
	if errors.As(err, &applicationErr) {
		return err
	}
	if errors.Is(err, ErrAccountNotFound) {
		return ErrAccountNotFound
	}
	if errors.Is(err, authz.ErrFeatureDisabled) || errors.Is(err, authz.ErrPolicyAccessDenied) {
		return ErrSelfServiceAccountForbidden
	}
	if errors.Is(err, authz.ErrInvalidActor) || errors.Is(err, authz.ErrActorInactive) ||
		errors.Is(err, authz.ErrSessionInvalid) {
		return ErrSelfServiceAccountActorRequired
	}
	if errors.Is(err, authz.ErrAuthorizationUnavailable) || errors.Is(err, authz.ErrInvalidPolicySnapshot) {
		return ErrSelfServiceAccountUnavailable.WithCause(err)
	}
	return ErrSelfServiceAccountUnavailable.WithCause(err)
}
