package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type ollamaCloudUsageSessionRequest struct {
	Session string `json:"session" binding:"required"`
}

type ollamaCloudUsageAutoRefreshRequest struct {
	Enabled *bool `json:"enabled" binding:"required"`
}

func (h *AccountHandler) GetOllamaCloudUsageSettings(c *gin.Context) {
	actor, ok := adminResourceActor(c)
	if !ok {
		return
	}

	if h.ollamaCloudUsage == nil {
		response.ErrorFrom(c, service.ErrOllamaCloudUsageUnavailable)
		return
	}
	settings, err := h.ollamaCloudUsage.AdminGetSettings(c.Request.Context(), actor)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *AccountHandler) UpdateOllamaCloudUsageSettings(c *gin.Context) {
	actor, ok := adminResourceActor(c)
	if !ok {
		return
	}

	if h.ollamaCloudUsage == nil {
		response.ErrorFrom(c, service.ErrOllamaCloudUsageUnavailable)
		return
	}
	var req service.OllamaCloudUsageSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := h.ollamaCloudUsage.AdminUpdateSettings(c.Request.Context(), actor, &req); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	settings, err := h.ollamaCloudUsage.AdminGetSettings(c.Request.Context(), actor)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *AccountHandler) GetOllamaCloudUsage(c *gin.Context) {
	actor, ok := adminResourceActor(c)
	if !ok {
		return
	}

	if !h.requireOllamaCloudUsage(c) {
		return
	}
	accountID, ok := ollamaCloudUsageAccountID(c)
	if !ok {
		return
	}
	state, err := h.ollamaCloudUsage.AdminGetState(c.Request.Context(), actor, accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}

func (h *AccountHandler) SaveOllamaCloudUsageSession(c *gin.Context) {
	actor, ok := adminResourceActor(c)
	if !ok {
		return
	}

	if !h.requireOllamaCloudUsage(c) {
		return
	}
	accountID, ok := ollamaCloudUsageAccountID(c)
	if !ok {
		return
	}
	var req ollamaCloudUsageSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	state, err := h.ollamaCloudUsage.AdminSaveSession(c.Request.Context(), actor, accountID, req.Session)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}

func (h *AccountHandler) DeleteOllamaCloudUsageSession(c *gin.Context) {
	actor, ok := adminResourceActor(c)
	if !ok {
		return
	}

	if !h.requireOllamaCloudUsage(c) {
		return
	}
	accountID, ok := ollamaCloudUsageAccountID(c)
	if !ok {
		return
	}
	state, err := h.ollamaCloudUsage.AdminDeleteSession(c.Request.Context(), actor, accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}

func (h *AccountHandler) SetOllamaCloudUsageAutoRefresh(c *gin.Context) {
	actor, ok := adminResourceActor(c)
	if !ok {
		return
	}

	if !h.requireOllamaCloudUsage(c) {
		return
	}
	accountID, ok := ollamaCloudUsageAccountID(c)
	if !ok {
		return
	}
	var req ollamaCloudUsageAutoRefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	state, err := h.ollamaCloudUsage.AdminSetAutoRefresh(c.Request.Context(), actor, accountID, *req.Enabled)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}

func (h *AccountHandler) RefreshOllamaCloudUsage(c *gin.Context) {
	actor, ok := adminResourceActor(c)
	if !ok {
		return
	}

	if !h.requireOllamaCloudUsage(c) {
		return
	}
	accountID, ok := ollamaCloudUsageAccountID(c)
	if !ok {
		return
	}
	state, err := h.ollamaCloudUsage.AdminRefresh(c.Request.Context(), actor, accountID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, state)
}

func (h *AccountHandler) requireOllamaCloudUsage(c *gin.Context) bool {
	if h != nil && h.ollamaCloudUsage != nil {
		return true
	}
	response.ErrorFrom(c, service.ErrOllamaCloudUsageUnavailable)
	return false
}

func ollamaCloudUsageAccountID(c *gin.Context) (int64, bool) {
	if c == nil {
		return 0, false
	}
	accountID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || accountID <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return accountID, true
}
