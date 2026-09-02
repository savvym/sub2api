package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/pagination"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type SelfServiceGroupHandler struct {
	service *service.SelfServiceGroupService
}

func NewSelfServiceGroupHandler(service *service.SelfServiceGroupService) *SelfServiceGroupHandler {
	return &SelfServiceGroupHandler{service: service}
}

func (h *SelfServiceGroupHandler) List(c *gin.Context) {
	actor, userID, ok := selfServiceGroupActor(c)
	if !ok {
		return
	}
	query, err := parseSelfServiceGroupQuery(c.Request.URL.Query())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items, result, err := h.service.ListGroups(c.Request.Context(), actor, query)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	resources := make([]dto.ResourceGroup, 0, len(items))
	for index := range items {
		resource := dto.ResourceGroupFromService(&items[index], userID)
		if resource == nil {
			response.ErrorFrom(c, service.ErrSelfServiceGroupUnavailable)
			return
		}
		resources = append(resources, *resource)
	}
	response.PaginatedWithResult(c, resources, &response.PaginationResult{
		Total:    result.Total,
		Page:     result.Page,
		PageSize: result.PageSize,
		Pages:    result.Pages,
	})
}

func (h *SelfServiceGroupHandler) Get(c *gin.Context) {
	actor, userID, ok := selfServiceGroupActor(c)
	if !ok {
		return
	}
	groupID, err := parseSelfServiceGroupID(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	item, err := h.service.GetGroup(c.Request.Context(), actor, groupID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.ResourceGroupFromService(item, userID))
}

type selfServiceGroupPlatformResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

func (h *SelfServiceGroupHandler) Platforms(c *gin.Context) {
	actor, _, ok := selfServiceGroupActor(c)
	if !ok {
		return
	}
	platforms, err := h.service.ListPlatforms(c.Request.Context(), actor)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	result := make([]selfServiceGroupPlatformResponse, 0, len(platforms))
	for _, platform := range platforms {
		result = append(result, selfServiceGroupPlatformResponse{
			ID:       platform.ID,
			Name:     platform.Name,
			Platform: platform.Platform,
		})
	}
	response.Success(c, result)
}

type selfServiceGroupStringField struct {
	Present bool
	Value   string
}

func (f *selfServiceGroupStringField) UnmarshalJSON(data []byte) error {
	if f == nil || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return errors.New("self-service group string fields must not be null")
	}
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	f.Present = true
	f.Value = value
	return nil
}

type selfServiceGroupCreateRequest struct {
	Name        selfServiceGroupStringField `json:"name"`
	Description selfServiceGroupStringField `json:"description"`
	PlatformID  selfServiceGroupStringField `json:"platform_id"`
}

func (h *SelfServiceGroupHandler) Create(c *gin.Context) {
	actor, userID, ok := selfServiceGroupActor(c)
	if !ok {
		return
	}
	var request selfServiceGroupCreateRequest
	if err := decodeSelfServiceGroupJSON(c, &request); err != nil ||
		!request.Name.Present || !request.PlatformID.Present {
		response.ErrorFrom(c, invalidSelfServiceGroupCreateRequest())
		return
	}
	item, err := h.service.CreateGroup(c.Request.Context(), service.SelfServiceGroupCreateInput{
		Actor:       actor,
		Name:        request.Name.Value,
		Description: request.Description.Value,
		PlatformID:  request.PlatformID.Value,
		RequestID:   selfServiceGroupRequestID(c),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, dto.ResourceGroupFromService(item, userID))
}

type selfServiceGroupUpdateRequest struct {
	Name        selfServiceGroupStringField `json:"name"`
	Description selfServiceGroupStringField `json:"description"`
}

func (h *SelfServiceGroupHandler) Update(c *gin.Context) {
	actor, userID, ok := selfServiceGroupActor(c)
	if !ok {
		return
	}
	groupID, err := parseSelfServiceGroupID(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var request selfServiceGroupUpdateRequest
	if err := decodeSelfServiceGroupJSON(c, &request); err != nil ||
		(!request.Name.Present && !request.Description.Present) {
		response.ErrorFrom(c, invalidSelfServiceGroupUpdateRequest())
		return
	}
	var name *string
	if request.Name.Present {
		name = &request.Name.Value
	}
	var description *string
	if request.Description.Present {
		description = &request.Description.Value
	}
	item, err := h.service.UpdateGroup(c.Request.Context(), service.SelfServiceGroupUpdateInput{
		Actor:       actor,
		GroupID:     groupID,
		Name:        name,
		Description: description,
		RequestID:   selfServiceGroupRequestID(c),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.ResourceGroupFromService(item, userID))
}

func (h *SelfServiceGroupHandler) Delete(c *gin.Context) {
	actor, userID, ok := selfServiceGroupActor(c)
	if !ok {
		return
	}
	groupID, err := parseSelfServiceGroupID(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	item, err := h.service.DeleteGroup(c.Request.Context(), service.SelfServiceGroupDeleteInput{
		Actor: actor, GroupID: groupID, RequestID: selfServiceGroupRequestID(c),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.ResourceGroupFromService(item, userID))
}

func selfServiceGroupActor(c *gin.Context) (authz.Actor, int64, bool) {
	if c == nil || c.Request == nil {
		return authz.Actor{}, 0, false
	}
	actor, ok := authz.ActorFromContext(c.Request.Context())
	if !ok || actor.AuthMethod() != authz.AuthMethodJWT {
		response.ErrorFrom(c, service.ErrSelfServiceGroupActorRequired)
		return authz.Actor{}, 0, false
	}
	userID, isUser := actor.UserID()
	subject, hasSubject := servermiddleware.GetAuthSubjectFromContext(c)
	if !isUser || !hasSubject || subject.UserID != userID {
		response.ErrorFrom(c, service.ErrSelfServiceGroupActorRequired)
		return authz.Actor{}, 0, false
	}
	return actor, userID, true
}

func parseSelfServiceGroupID(c *gin.Context) (int64, error) {
	value := strings.TrimSpace(c.Param("id"))
	groupID, err := strconv.ParseInt(value, 10, 64)
	if err != nil || groupID <= 0 {
		return 0, infraerrors.BadRequest("INVALID_GROUP_ID", "group id must be a positive integer")
	}
	return groupID, nil
}

func parseSelfServiceGroupQuery(values url.Values) (service.GroupReadQuery, error) {
	allowed := map[string]struct{}{
		"page": {}, "page_size": {}, "platform": {}, "status": {},
		"search": {}, "sort_by": {}, "sort_order": {}, "timezone": {},
	}
	for key, entries := range values {
		if _, ok := allowed[key]; !ok || len(entries) != 1 {
			return service.GroupReadQuery{}, service.ErrInvalidResourceReadQuery
		}
	}
	page, err := parseSelfServiceGroupPositiveQueryInt(values.Get("page"), 1, 0)
	if err != nil {
		return service.GroupReadQuery{}, err
	}
	pageSize, err := parseSelfServiceGroupPositiveQueryInt(values.Get("page_size"), 20, 1000)
	if err != nil {
		return service.GroupReadQuery{}, err
	}
	return service.GroupReadQuery{
		Pagination: pagination.PaginationParams{
			Page: page, PageSize: pageSize,
			SortBy: values.Get("sort_by"), SortOrder: values.Get("sort_order"),
		},
		Platform: values.Get("platform"),
		Status:   values.Get("status"),
		Search:   values.Get("search"),
	}, nil
}

func parseSelfServiceGroupPositiveQueryInt(value string, fallback, maximum int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 || (maximum > 0 && parsed > maximum) {
		return 0, service.ErrInvalidResourceReadQuery
	}
	return parsed, nil
}

func decodeSelfServiceGroupJSON(c *gin.Context, destination any) error {
	if c == nil || c.Request == nil || c.Request.Body == nil || destination == nil {
		return io.ErrUnexpectedEOF
	}
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func invalidSelfServiceGroupCreateRequest() error {
	return infraerrors.BadRequest(
		"INVALID_REQUEST",
		"request body must contain only name, optional description, and platform_id",
	)
}

func invalidSelfServiceGroupUpdateRequest() error {
	return infraerrors.BadRequest(
		"INVALID_REQUEST",
		"request body must contain at least one of name or description",
	)
}

func selfServiceGroupRequestID(c *gin.Context) string {
	requestID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
	return requestID
}
