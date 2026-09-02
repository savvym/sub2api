package admin

import (
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// HostingEntitlementHandler exposes the guarded hoster qualification and quota
// administration workflow.
type HostingEntitlementHandler struct {
	service     *service.HostingEntitlementService
	totpService *service.TotpService
	userService *service.UserService
}

func NewHostingEntitlementHandler(
	hostingService *service.HostingEntitlementService,
	totpService *service.TotpService,
	userService *service.UserService,
) *HostingEntitlementHandler {
	return &HostingEntitlementHandler{
		service:     hostingService,
		totpService: totpService,
		userService: userService,
	}
}

// Get returns one user's current hoster role, quotas and live usage.
func (h *HostingEntitlementHandler) Get(c *gin.Context) {
	actor, ok := adminResourceActor(c)
	if !ok {
		return
	}
	targetUserID, err := parseHostingEntitlementUserID(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	result, err := h.service.Get(c.Request.Context(), actor, targetUserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

type hostingEntitlementUpdateRequest struct {
	ExpectedVersion *int64 `json:"expected_version"`
	Hoster          *bool  `json:"hoster"`
	AccountLimit    *int64 `json:"account_limit"`
	GroupLimit      *int64 `json:"group_limit"`
}

// Update atomically applies hoster qualification and both resource quotas.
// It always requires a recent session-bound JWT TOTP grant.
func (h *HostingEntitlementHandler) Update(c *gin.Context) {
	actor, ok := adminResourceActor(c)
	if !ok {
		return
	}
	if !middleware.EnforceSessionBoundStepUpAlways(c, h.totpService, h.userService) {
		return
	}
	targetUserID, err := parseHostingEntitlementUserID(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}

	var request hostingEntitlementUpdateRequest
	if err := decodeHostingEntitlementUpdateRequest(c, &request); err != nil {
		response.ErrorFrom(c, err)
		return
	}

	requestID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
	result, err := h.service.Update(c.Request.Context(), service.HostingEntitlementUpdateInput{
		Actor:           actor,
		TargetUserID:    targetUserID,
		ExpectedVersion: *request.ExpectedVersion,
		Hoster:          *request.Hoster,
		AccountLimit:    *request.AccountLimit,
		GroupLimit:      *request.GroupLimit,
		AuditTrace: service.HostingEntitlementAuditTrace{
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
		middleware.SkipAudit(c)
	}
	response.Success(c, result)
}

func parseHostingEntitlementUserID(c *gin.Context) (int64, error) {
	value := strings.TrimSpace(c.Param("user_id"))
	userID, err := strconv.ParseInt(value, 10, 64)
	if err != nil || userID <= 0 {
		return 0, infraerrors.BadRequest("INVALID_USER_ID", "user_id must be a positive integer")
	}
	return userID, nil
}

func decodeHostingEntitlementUpdateRequest(
	c *gin.Context,
	destination *hostingEntitlementUpdateRequest,
) error {
	if c == nil || c.Request == nil || c.Request.Body == nil || destination == nil {
		return invalidHostingEntitlementUpdateRequest()
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return invalidHostingEntitlementUpdateRequest()
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return invalidHostingEntitlementUpdateRequest()
	}
	if destination.ExpectedVersion == nil || destination.Hoster == nil ||
		destination.AccountLimit == nil || destination.GroupLimit == nil ||
		*destination.ExpectedVersion < 0 || *destination.AccountLimit < 0 || *destination.GroupLimit < 0 {
		return invalidHostingEntitlementUpdateRequest()
	}
	return nil
}

func invalidHostingEntitlementUpdateRequest() error {
	return infraerrors.BadRequest(
		"INVALID_REQUEST",
		"request body must contain only nonnegative expected_version, account_limit, group_limit, and hoster",
	)
}
