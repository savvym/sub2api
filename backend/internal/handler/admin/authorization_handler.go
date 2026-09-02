package admin

import (
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

// AuthorizationHandler exposes the guarded role-authorization mode workflow.
type AuthorizationHandler struct {
	roleService *service.RoleService
	totpService *service.TotpService
	userService *service.UserService
}

func NewAuthorizationHandler(
	roleService *service.RoleService,
	totpService *service.TotpService,
	userService *service.UserService,
) *AuthorizationHandler {
	return &AuthorizationHandler{
		roleService: roleService,
		totpService: totpService,
		userService: userService,
	}
}

// GetRoleAuthorizationMode returns the only supported next hop and its readiness.
func (h *AuthorizationHandler) GetRoleAuthorizationMode(c *gin.Context) {
	actorUserID, ok := authorizationActorUserID(c)
	if !ok {
		return
	}

	status, err := h.roleService.GetAuthorizationModeStatus(c.Request.Context(), actorUserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

type roleAuthorizationModeTransitionRequest struct {
	ExpectedMode string `json:"expected_mode"`
	TargetMode   string `json:"target_mode"`
}

// TransitionRoleAuthorizationMode applies a compare-and-swap mode transition.
// This endpoint always requires a recent session-bound JWT TOTP grant,
// independently of settings.
func (h *AuthorizationHandler) TransitionRoleAuthorizationMode(c *gin.Context) {
	if !middleware.EnforceSessionBoundStepUpAlways(c, h.totpService, h.userService) {
		return
	}

	actorUserID, ok := authorizationActorUserID(c)
	if !ok {
		return
	}

	var request roleAuthorizationModeTransitionRequest
	if err := decodeRoleAuthorizationModeTransitionRequest(c, &request); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	requestID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
	result, err := h.roleService.TransitionAuthorizationMode(c.Request.Context(), service.RoleAuthorizationModeTransitionInput{
		ActorUserID:  actorUserID,
		ExpectedMode: request.ExpectedMode,
		TargetMode:   request.TargetMode,
		AuditTrace: service.RoleAuthorizationModeAuditTrace{
			RequestID: requestID,
			ClientIP:  middleware.SecurityClientIP(c),
			UserAgent: c.Request.UserAgent(),
		},
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if result.Changed {
		// The service persisted the success audit atomically with the transition.
		middleware.SkipAudit(c)
	}
	response.Success(c, result)
}

func authorizationActorUserID(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		middleware.AbortWithError(c, 401, "UNAUTHORIZED", "Authorization required")
		return 0, false
	}
	return subject.UserID, true
}

func decodeRoleAuthorizationModeTransitionRequest(
	c *gin.Context,
	destination *roleAuthorizationModeTransitionRequest,
) error {
	if c.Request == nil || c.Request.Body == nil {
		return invalidRoleAuthorizationModeTransitionRequest()
	}

	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return invalidRoleAuthorizationModeTransitionRequest()
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return invalidRoleAuthorizationModeTransitionRequest()
	}

	destination.ExpectedMode = strings.TrimSpace(destination.ExpectedMode)
	destination.TargetMode = strings.TrimSpace(destination.TargetMode)
	if destination.ExpectedMode == "" || destination.TargetMode == "" {
		return invalidRoleAuthorizationModeTransitionRequest()
	}
	return nil
}

func invalidRoleAuthorizationModeTransitionRequest() error {
	return infraerrors.BadRequest(
		"INVALID_REQUEST",
		"request body must contain only non-empty expected_mode and target_mode",
	)
}
