package admin

import (
	"context"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

type idempotencyStoreUnavailableMode int
type adminLegacyIdempotencyPayloadBuilder func(actorScope string) any
type adminIdempotencyAfterResponse func(result *service.IdempotencyExecuteResult)

const (
	idempotencyStoreUnavailableFailClose idempotencyStoreUnavailableMode = iota
	idempotencyStoreUnavailableFailOpen
)

func executeAdminIdempotent(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	execute func(context.Context) (any, error),
) (*service.IdempotencyExecuteResult, error) {
	return executeAdminIdempotentWithLegacyPayloads(c, scope, payload, ttl, nil, execute)
}

func executeAdminIdempotentWithLegacyPayloads(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	legacyPayloadBuilder adminLegacyIdempotencyPayloadBuilder,
	execute func(context.Context) (any, error),
) (*service.IdempotencyExecuteResult, error) {
	actorScope, legacyActorScopes, ok := adminIdempotencyActorScopes(c)
	if !ok {
		return nil, service.ErrIdempotencyActorUnavailable
	}
	c.Request = c.Request.WithContext(service.ContextWithIdempotencyLegacyActorScopes(
		c.Request.Context(),
		legacyActorScopes,
	))
	legacyRequests := make([]service.IdempotencyLegacyRequest, 0, len(legacyActorScopes))
	qualifiedLegacyRequests := make([]service.IdempotencyLegacyRequest, 0, len(legacyActorScopes))
	legacyActorScopeFingerprints := legacyActorScopes
	if legacyPayloadBuilder != nil && c.GetHeader("Idempotency-Key") != "" {
		legacyActorScopeFingerprints = nil
		for _, legacyActorScope := range legacyActorScopes {
			legacyPayload := legacyPayloadBuilder(legacyActorScope)
			legacyRequests = append(legacyRequests, service.IdempotencyLegacyRequest{
				ActorScope: legacyActorScope,
				Payload:    legacyPayload,
			})
			qualifiedLegacyRequests = append(qualifiedLegacyRequests, service.IdempotencyLegacyRequest{
				ActorScope: actorScope,
				Payload:    legacyPayload,
			})
		}
	}

	coordinator := service.DefaultIdempotencyCoordinator()
	if coordinator == nil {
		data, err := execute(c.Request.Context())
		if err != nil {
			return nil, err
		}
		return &service.IdempotencyExecuteResult{Data: data}, nil
	}

	return coordinator.Execute(c.Request.Context(), service.IdempotencyExecuteOptions{
		Scope:                   scope,
		ActorScope:              actorScope,
		LegacyActorScopes:       legacyActorScopeFingerprints,
		LegacyRequests:          legacyRequests,
		QualifiedLegacyRequests: qualifiedLegacyRequests,
		Method:                  c.Request.Method,
		Route:                   c.FullPath(),
		IdempotencyKey:          c.GetHeader("Idempotency-Key"),
		Payload:                 payload,
		RequireKey:              true,
		TTL:                     ttl,
	}, execute)
}

func adminActorScope(c *gin.Context) string {
	actorScope, _, ok := adminIdempotencyActorScopes(c)
	if !ok {
		return ""
	}
	return actorScope
}

func adminIdempotencyActorScopes(c *gin.Context) (string, []string, bool) {
	if c == nil || c.Request == nil {
		return "", nil, false
	}
	actor, ok := authz.ActorFromContext(c.Request.Context())
	if !ok {
		return "", nil, false
	}
	actorScope, ok := actor.SubjectKey()
	if !ok {
		return "", nil, false
	}
	subject, hasSubject := middleware2.GetAuthSubjectFromContext(c)
	if !hasSubject || subject.UserID <= 0 {
		return "", nil, false
	}
	if actorUserID, isUser := actor.UserID(); isUser {
		if subject.UserID != actorUserID {
			return "", nil, false
		}
		return actorScope, []string{"admin:" + strconv.FormatInt(actorUserID, 10)}, true
	}
	if _, isServicePrincipal := actor.ServicePrincipalID(); isServicePrincipal && actor.AuthMethod() == authz.AuthMethodAdminAPIKey {
		// The first-admin subject is a compatibility shim only. Its legacy name
		// may match pre-upgrade fingerprints, but never replaces the SP scope.
		return actorScope, []string{"admin:" + strconv.FormatInt(subject.UserID, 10)}, true
	}
	return "", nil, false
}

func executeAdminIdempotentJSON(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	execute func(context.Context) (any, error),
) {
	executeAdminIdempotentJSONWithMode(c, scope, payload, ttl, idempotencyStoreUnavailableFailClose, nil, execute, nil)
}

func executeAdminIdempotentJSONWithLegacyPayloads(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	legacyPayloadBuilder adminLegacyIdempotencyPayloadBuilder,
	execute func(context.Context) (any, error),
) {
	executeAdminIdempotentJSONWithMode(
		c,
		scope,
		payload,
		ttl,
		idempotencyStoreUnavailableFailClose,
		legacyPayloadBuilder,
		execute,
		nil,
	)
}

func executeAdminIdempotentJSONWithLegacyPayloadsAfterResponse(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	legacyPayloadBuilder adminLegacyIdempotencyPayloadBuilder,
	execute func(context.Context) (any, error),
	afterResponse adminIdempotencyAfterResponse,
) {
	executeAdminIdempotentJSONWithMode(
		c,
		scope,
		payload,
		ttl,
		idempotencyStoreUnavailableFailClose,
		legacyPayloadBuilder,
		execute,
		afterResponse,
	)
}

func executeAdminIdempotentJSONFailOpenOnStoreUnavailable(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	execute func(context.Context) (any, error),
) {
	executeAdminIdempotentJSONWithMode(c, scope, payload, ttl, idempotencyStoreUnavailableFailOpen, nil, execute, nil)
}

func executeAdminIdempotentJSONWithMode(
	c *gin.Context,
	scope string,
	payload any,
	ttl time.Duration,
	mode idempotencyStoreUnavailableMode,
	legacyPayloadBuilder adminLegacyIdempotencyPayloadBuilder,
	execute func(context.Context) (any, error),
	afterResponse adminIdempotencyAfterResponse,
) {
	result, err := executeAdminIdempotentWithLegacyPayloads(
		c,
		scope,
		payload,
		ttl,
		legacyPayloadBuilder,
		execute,
	)
	if err != nil {
		if infraerrors.Code(err) == infraerrors.Code(service.ErrIdempotencyStoreUnavail) {
			strategy := "fail_close"
			if mode == idempotencyStoreUnavailableFailOpen {
				strategy = "fail_open"
			}
			service.RecordIdempotencyStoreUnavailable(c.FullPath(), scope, "handler_"+strategy)
			logger.LegacyPrintf("handler.idempotency", "[Idempotency] store unavailable: method=%s route=%s scope=%s strategy=%s", c.Request.Method, c.FullPath(), scope, strategy)
			if mode == idempotencyStoreUnavailableFailOpen {
				data, fallbackErr := execute(c.Request.Context())
				if fallbackErr != nil {
					response.ErrorFrom(c, fallbackErr)
					return
				}
				c.Header("X-Idempotency-Degraded", "store-unavailable")
				response.Success(c, data)
				return
			}
		}
		if retryAfter := service.RetryAfterSecondsFromError(err); retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		response.ErrorFrom(c, err)
		return
	}
	if result != nil && result.Replayed {
		c.Header("X-Idempotency-Replayed", "true")
	}
	response.Success(c, result.Data)
	if afterResponse != nil {
		afterResponse(result)
	}
}
