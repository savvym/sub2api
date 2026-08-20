package dto

import (
	"time"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

// ResourceAccount is the HTTP projection for an account visible to a regular
// user. It intentionally contains no credentials, runtime state, proxy data,
// owner identifier, or resource relationships.
type ResourceAccount struct {
	ID                int64              `json:"id"`
	Name              string             `json:"name"`
	Platform          string             `json:"platform"`
	Type              string             `json:"type"`
	Status            string             `json:"status"`
	OwnedByMe         bool               `json:"owned_by_me"`
	PublicAccessLevel *authz.AccessLevel `json:"public_access_level"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

// ResourceGroup is the HTTP projection for a group visible to a regular user.
// Account topology, counts, pricing, and routing remain repository-internal.
type ResourceGroup struct {
	ID                int64              `json:"id"`
	Name              string             `json:"name"`
	Description       string             `json:"description"`
	Platform          string             `json:"platform"`
	Status            string             `json:"status"`
	OwnedByMe         bool               `json:"owned_by_me"`
	PublicAccessLevel *authz.AccessLevel `json:"public_access_level"`
	CreatedAt         time.Time          `json:"created_at"`
	UpdatedAt         time.Time          `json:"updated_at"`
}

func ResourceAccountFromService(item *service.AccountListItem, viewerUserID int64) *ResourceAccount {
	if item == nil {
		return nil
	}
	return &ResourceAccount{
		ID:                item.ID,
		Name:              item.Name,
		Platform:          item.Platform,
		Type:              item.Type,
		Status:            item.Status,
		OwnedByMe:         resourceOwnedByViewer(item.OwnerUserID, viewerUserID),
		PublicAccessLevel: copyResourceAccessLevel(item.PublicAccessLevel),
		CreatedAt:         item.CreatedAt,
		UpdatedAt:         item.UpdatedAt,
	}
}

func ResourceGroupFromService(item *service.GroupListItem, viewerUserID int64) *ResourceGroup {
	if item == nil {
		return nil
	}
	return &ResourceGroup{
		ID:                item.ID,
		Name:              item.Name,
		Description:       item.Description,
		Platform:          item.Platform,
		Status:            item.Status,
		OwnedByMe:         resourceOwnedByViewer(item.OwnerUserID, viewerUserID),
		PublicAccessLevel: copyResourceAccessLevel(item.PublicAccessLevel),
		CreatedAt:         item.CreatedAt,
		UpdatedAt:         item.UpdatedAt,
	}
}

func resourceOwnedByViewer(ownerUserID *int64, viewerUserID int64) bool {
	return viewerUserID > 0 && ownerUserID != nil && *ownerUserID == viewerUserID
}

func copyResourceAccessLevel(level *authz.AccessLevel) *authz.AccessLevel {
	if level == nil {
		return nil
	}
	copied := *level
	return &copied
}
