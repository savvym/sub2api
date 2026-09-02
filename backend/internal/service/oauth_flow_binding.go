package service

import (
	"net/http"
	"strings"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/oauthflow"
)

func validateOAuthFlowBinding(session, current oauthflow.Binding, codePrefix string) error {
	codePrefix = strings.TrimSpace(codePrefix)
	if !session.Valid() {
		return infraerrors.New(http.StatusBadRequest, codePrefix+"_SESSION_BINDING_INVALID", "oauth session binding is missing or invalid")
	}
	if !session.Equal(current) {
		return infraerrors.New(http.StatusBadRequest, codePrefix+"_ACTOR_MISMATCH", "oauth session does not belong to the current actor")
	}
	return nil
}

func validateOAuthProxyBinding(requested, session *int64, codePrefix string) error {
	if requested == nil {
		return nil
	}
	if session == nil || *requested != *session {
		return infraerrors.New(http.StatusBadRequest, strings.TrimSpace(codePrefix)+"_PROXY_MISMATCH", "proxy_id does not match the OAuth session")
	}
	return nil
}

func oauthSessionAlreadyUsed(codePrefix string) error {
	return infraerrors.New(http.StatusBadRequest, strings.TrimSpace(codePrefix)+"_SESSION_ALREADY_USED", "oauth session has already been used")
}

func cloneOAuthFlowInt64Pointer(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}
