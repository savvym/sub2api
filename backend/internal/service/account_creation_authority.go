package service

import (
	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/oauthflow"
)

// accountCreationAuthority is derived from a trusted Actor or server-side flow
// state. HTTP and import DTOs cannot construct it or replace account ownership.
type accountCreationAuthority struct {
	binding         oauthflow.Binding
	ownerUserID     *int64
	createdByUserID *int64
}

func newPlatformAccountCreationAuthority(actor authz.Actor) (accountCreationAuthority, error) {
	actorKey, err := adminResourceActorSubjectKey(actor)
	if err != nil {
		return accountCreationAuthority{}, err
	}
	binding, err := oauthflow.NewPlatformBinding(actorKey)
	if err != nil {
		return accountCreationAuthority{}, ErrAdminResourceActorUnavailable
	}
	authority := accountCreationAuthority{binding: binding}
	if userID, ok := actor.UserID(); ok && actor.AuthMethod() == authz.AuthMethodJWT {
		authority.createdByUserID = accountCreationInt64Pointer(userID)
	}
	return authority, nil
}

func newOwnedAccountCreationAuthority(actor authz.Actor) (accountCreationAuthority, error) {
	if !actor.Valid() || actor.AuthMethod() != authz.AuthMethodJWT {
		return accountCreationAuthority{}, ErrSelfServiceAccountActorRequired
	}
	userID, ok := actor.UserID()
	if !ok || userID <= 0 {
		return accountCreationAuthority{}, ErrSelfServiceAccountActorRequired
	}
	actorKey, ok := actor.SubjectKey()
	if !ok {
		return accountCreationAuthority{}, ErrSelfServiceAccountActorRequired
	}
	binding, err := oauthflow.NewUserBinding(actorKey, userID)
	if err != nil {
		return accountCreationAuthority{}, ErrSelfServiceAccountActorRequired
	}
	return accountCreationAuthority{
		binding:         binding,
		ownerUserID:     accountCreationInt64Pointer(userID),
		createdByUserID: accountCreationInt64Pointer(userID),
	}, nil
}

func (a accountCreationAuthority) apply(account *Account) error {
	if account == nil || !a.binding.Valid() {
		return ErrAccountNilInput
	}
	account.OwnerUserID = cloneAccountCreationInt64Pointer(a.ownerUserID)
	account.CreatedByUserID = cloneAccountCreationInt64Pointer(a.createdByUserID)
	account.PublicAccessLevel = nil
	return nil
}

func (a accountCreationAuthority) flowBinding() oauthflow.Binding {
	return a.binding
}

func (a accountCreationAuthority) ownerID() *int64 {
	return cloneAccountCreationInt64Pointer(a.ownerUserID)
}

func (a accountCreationAuthority) creatorID() *int64 {
	return cloneAccountCreationInt64Pointer(a.createdByUserID)
}

func accountCreationInt64Pointer(value int64) *int64 {
	copyValue := value
	return &copyValue
}

func cloneAccountCreationInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	return accountCreationInt64Pointer(*value)
}
