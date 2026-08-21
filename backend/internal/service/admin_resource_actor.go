package service

import (
	"github.com/Wei-Shaw/sub2api/internal/authz"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

var ErrAdminResourceActorUnavailable = infraerrors.ServiceUnavailable(
	"AUTHORIZATION_UNAVAILABLE",
	"authorization data unavailable",
)

// ValidateAdminResourceActor enforces the authenticated subject kinds accepted
// by the legacy admin resource facade. Current authorization is still checked
// by the admin HTTP gate until the transactional authorization slice lands.
func ValidateAdminResourceActor(actor authz.Actor) error {
	if !actor.Valid() {
		return ErrAdminResourceActorUnavailable
	}
	if _, ok := actor.UserID(); ok && actor.AuthMethod() == authz.AuthMethodJWT {
		return nil
	}
	if _, ok := actor.ServicePrincipalID(); ok && actor.AuthMethod() == authz.AuthMethodAdminAPIKey {
		return nil
	}
	return ErrAdminResourceActorUnavailable
}

func adminResourceActorSubjectKey(actor authz.Actor) (string, error) {
	if err := ValidateAdminResourceActor(actor); err != nil {
		return "", err
	}
	key, ok := actor.SubjectKey()
	if !ok {
		return "", ErrAdminResourceActorUnavailable
	}
	return key, nil
}
