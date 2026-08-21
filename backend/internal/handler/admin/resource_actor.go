package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func adminResourceActor(c *gin.Context) (authz.Actor, bool) {
	if c == nil || c.Request == nil {
		return authz.Actor{}, false
	}
	actor, ok := authz.ActorFromContext(c.Request.Context())
	if !ok || service.ValidateAdminResourceActor(actor) != nil {
		response.ErrorFrom(c, service.ErrAdminResourceActorUnavailable)
		return authz.Actor{}, false
	}

	subject, hasSubject := servermiddleware.GetAuthSubjectFromContext(c)
	if userID, isUser := actor.UserID(); isUser {
		if !hasSubject || subject.UserID != userID {
			response.ErrorFrom(c, service.ErrAdminResourceActorUnavailable)
			return authz.Actor{}, false
		}
		return actor, true
	}
	if _, isServicePrincipal := actor.ServicePrincipalID(); isServicePrincipal {
		// The first-admin subject remains a compatibility shim for legacy
		// handlers; the Actor passed to services is always the SP.
		if !hasSubject || subject.UserID <= 0 {
			response.ErrorFrom(c, service.ErrAdminResourceActorUnavailable)
			return authz.Actor{}, false
		}
		return actor, true
	}

	response.ErrorFrom(c, service.ErrAdminResourceActorUnavailable)
	return authz.Actor{}, false
}
