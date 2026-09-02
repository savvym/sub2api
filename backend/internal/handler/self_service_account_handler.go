package handler

import (
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

type SelfServiceAccountHandler struct {
	service *service.SelfServiceAccountService
}

func NewSelfServiceAccountHandler(service *service.SelfServiceAccountService) *SelfServiceAccountHandler {
	return &SelfServiceAccountHandler{service: service}
}

func (h *SelfServiceAccountHandler) List(c *gin.Context) {
	actor, userID, ok := selfServiceAccountActor(c)
	if !ok {
		return
	}
	query, err := parseSelfServiceAccountQuery(c.Request.URL.Query())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	items, result, err := h.service.ListAccounts(c.Request.Context(), actor, query)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	resources := make([]dto.ResourceAccount, 0, len(items))
	for index := range items {
		resource := dto.ResourceAccountFromService(&items[index], userID)
		if resource == nil {
			response.ErrorFrom(c, service.ErrSelfServiceAccountUnavailable)
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

func (h *SelfServiceAccountHandler) Get(c *gin.Context) {
	actor, userID, ok := selfServiceAccountActor(c)
	if !ok {
		return
	}
	accountID, err := parseSelfServiceAccountID(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	item, err := h.service.GetAccount(c.Request.Context(), actor, accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.ResourceAccountFromService(item, userID))
}

type selfServiceAccountProductResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Type     string `json:"type"`
}

func (h *SelfServiceAccountHandler) Products(c *gin.Context) {
	actor, _, ok := selfServiceAccountActor(c)
	if !ok {
		return
	}
	products, err := h.service.ListProducts(c.Request.Context(), actor)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	result := make([]selfServiceAccountProductResponse, 0, len(products))
	for _, product := range products {
		result = append(result, selfServiceAccountProductResponse{
			ID:       product.ID,
			Name:     product.Name,
			Platform: product.Platform,
			Type:     product.AccountType,
		})
	}
	response.Success(c, result)
}

type selfServiceAccountCreateRequest struct {
	Name      *string `json:"name"`
	ProductID *string `json:"product_id"`
	APIKey    *string `json:"api_key"`
}

func (h *SelfServiceAccountHandler) Create(c *gin.Context) {
	actor, userID, ok := selfServiceAccountActor(c)
	if !ok {
		return
	}
	var request selfServiceAccountCreateRequest
	if err := decodeSelfServiceAccountJSON(c, &request); err != nil ||
		request.Name == nil || request.ProductID == nil || request.APIKey == nil {
		response.ErrorFrom(c, invalidSelfServiceAccountCreateRequest())
		return
	}
	item, err := h.service.CreateAccount(c.Request.Context(), service.SelfServiceAccountCreateInput{
		Actor:     actor,
		Name:      *request.Name,
		ProductID: *request.ProductID,
		APIKey:    *request.APIKey,
		RequestID: selfServiceAccountRequestID(c),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Created(c, dto.ResourceAccountFromService(item, userID))
}

type selfServiceAccountRenameRequest struct {
	Name *string `json:"name"`
}

func (h *SelfServiceAccountHandler) Rename(c *gin.Context) {
	actor, userID, ok := selfServiceAccountActor(c)
	if !ok {
		return
	}
	accountID, err := parseSelfServiceAccountID(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var request selfServiceAccountRenameRequest
	if err := decodeSelfServiceAccountJSON(c, &request); err != nil || request.Name == nil {
		response.ErrorFrom(c, invalidSelfServiceAccountRenameRequest())
		return
	}
	item, err := h.service.RenameAccount(c.Request.Context(), service.SelfServiceAccountRenameInput{
		Actor:     actor,
		AccountID: accountID,
		Name:      *request.Name,
		RequestID: selfServiceAccountRequestID(c),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.ResourceAccountFromService(item, userID))
}

func (h *SelfServiceAccountHandler) Delete(c *gin.Context) {
	actor, userID, ok := selfServiceAccountActor(c)
	if !ok {
		return
	}
	accountID, err := parseSelfServiceAccountID(c)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	item, err := h.service.DeleteAccount(c.Request.Context(), service.SelfServiceAccountDeleteInput{
		Actor:     actor,
		AccountID: accountID,
		RequestID: selfServiceAccountRequestID(c),
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, dto.ResourceAccountFromService(item, userID))
}

func selfServiceAccountActor(c *gin.Context) (authz.Actor, int64, bool) {
	if c == nil || c.Request == nil {
		return authz.Actor{}, 0, false
	}
	actor, ok := authz.ActorFromContext(c.Request.Context())
	if !ok || actor.AuthMethod() != authz.AuthMethodJWT {
		response.ErrorFrom(c, service.ErrSelfServiceAccountActorRequired)
		return authz.Actor{}, 0, false
	}
	userID, isUser := actor.UserID()
	subject, hasSubject := servermiddleware.GetAuthSubjectFromContext(c)
	if !isUser || !hasSubject || subject.UserID != userID {
		response.ErrorFrom(c, service.ErrSelfServiceAccountActorRequired)
		return authz.Actor{}, 0, false
	}
	return actor, userID, true
}

func parseSelfServiceAccountID(c *gin.Context) (int64, error) {
	value := strings.TrimSpace(c.Param("id"))
	accountID, err := strconv.ParseInt(value, 10, 64)
	if err != nil || accountID <= 0 {
		return 0, infraerrors.BadRequest("INVALID_ACCOUNT_ID", "account id must be a positive integer")
	}
	return accountID, nil
}

func parseSelfServiceAccountQuery(values url.Values) (service.AccountReadQuery, error) {
	allowed := map[string]struct{}{
		"page": {}, "page_size": {}, "platform": {}, "type": {}, "status": {},
		"search": {}, "sort_by": {}, "sort_order": {}, "timezone": {},
	}
	for key, entries := range values {
		if _, ok := allowed[key]; !ok || len(entries) != 1 {
			return service.AccountReadQuery{}, service.ErrInvalidResourceReadQuery
		}
	}
	page, err := parseSelfServicePositiveQueryInt(values.Get("page"), 1, 0)
	if err != nil {
		return service.AccountReadQuery{}, err
	}
	pageSize, err := parseSelfServicePositiveQueryInt(values.Get("page_size"), 20, 1000)
	if err != nil {
		return service.AccountReadQuery{}, err
	}
	return service.AccountReadQuery{
		Pagination: pagination.PaginationParams{
			Page:      page,
			PageSize:  pageSize,
			SortBy:    values.Get("sort_by"),
			SortOrder: values.Get("sort_order"),
		},
		Platform:    values.Get("platform"),
		AccountType: values.Get("type"),
		Status:      values.Get("status"),
		Search:      values.Get("search"),
	}, nil
}

func parseSelfServicePositiveQueryInt(value string, fallback, maximum int) (int, error) {
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 || (maximum > 0 && parsed > maximum) {
		return 0, service.ErrInvalidResourceReadQuery
	}
	return parsed, nil
}

func decodeSelfServiceAccountJSON(c *gin.Context, destination any) error {
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

func invalidSelfServiceAccountCreateRequest() error {
	return infraerrors.BadRequest(
		"INVALID_REQUEST",
		"request body must contain only name, product_id, and api_key",
	)
}

func invalidSelfServiceAccountRenameRequest() error {
	return infraerrors.BadRequest(
		"INVALID_REQUEST",
		"request body must contain only name",
	)
}

func selfServiceAccountRequestID(c *gin.Context) string {
	requestID, _ := c.Request.Context().Value(ctxkey.RequestID).(string)
	return requestID
}
