package dto

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func TestResourceAccountFromServiceUsesStrictWhitelist(t *testing.T) {
	ownerID := int64(42)
	level := authz.AccessLevelConsumer
	createdAt := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	updatedAt := createdAt.Add(time.Hour)

	got := ResourceAccountFromService(&service.AccountListItem{
		ID:                7,
		Name:              "shared-account",
		Platform:          "openai",
		Type:              "oauth",
		Status:            "active",
		OwnerUserID:       &ownerID,
		PublicAccessLevel: &level,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
	}, ownerID)

	require.Equal(t, &ResourceAccount{
		ID:                7,
		Name:              "shared-account",
		Platform:          "openai",
		Type:              "oauth",
		Status:            "active",
		OwnedByMe:         true,
		PublicAccessLevel: accessLevelPointer(authz.AccessLevelConsumer),
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
	}, got)
	assertResourceReadJSONKeys(t, got, []string{
		"created_at",
		"id",
		"name",
		"owned_by_me",
		"platform",
		"public_access_level",
		"status",
		"type",
		"updated_at",
	})
}

func TestResourceGroupFromServiceUsesStrictWhitelist(t *testing.T) {
	ownerID := int64(8)
	level := authz.AccessLevelViewer
	createdAt := time.Date(2026, 8, 20, 2, 3, 4, 0, time.UTC)
	updatedAt := createdAt.Add(2 * time.Hour)

	got := ResourceGroupFromService(&service.GroupListItem{
		ID:                11,
		Name:              "shared-group",
		Description:       "safe summary",
		Platform:          "anthropic",
		Status:            "active",
		OwnerUserID:       &ownerID,
		PublicAccessLevel: &level,
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
	}, ownerID+1)

	require.Equal(t, &ResourceGroup{
		ID:                11,
		Name:              "shared-group",
		Description:       "safe summary",
		Platform:          "anthropic",
		Status:            "active",
		OwnedByMe:         false,
		PublicAccessLevel: accessLevelPointer(authz.AccessLevelViewer),
		CreatedAt:         createdAt,
		UpdatedAt:         updatedAt,
	}, got)
	assertResourceReadJSONKeys(t, got, []string{
		"created_at",
		"description",
		"id",
		"name",
		"owned_by_me",
		"platform",
		"public_access_level",
		"status",
		"updated_at",
	})
}

func TestResourceReadMappersComputeOwnershipWithoutExposingOwnerID(t *testing.T) {
	ownerID := int64(55)

	tests := []struct {
		name     string
		ownerID  *int64
		viewerID int64
		want     bool
	}{
		{name: "owner", ownerID: &ownerID, viewerID: ownerID, want: true},
		{name: "different user", ownerID: &ownerID, viewerID: ownerID + 1, want: false},
		{name: "platform resource", ownerID: nil, viewerID: ownerID, want: false},
		{name: "invalid viewer", ownerID: &ownerID, viewerID: 0, want: false},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			account := ResourceAccountFromService(&service.AccountListItem{OwnerUserID: testCase.ownerID}, testCase.viewerID)
			group := ResourceGroupFromService(&service.GroupListItem{OwnerUserID: testCase.ownerID}, testCase.viewerID)
			require.Equal(t, testCase.want, account.OwnedByMe)
			require.Equal(t, testCase.want, group.OwnedByMe)
			assertForbiddenResourceReadKeys(t, account)
			assertForbiddenResourceReadKeys(t, group)
		})
	}
}

func TestResourceReadDTOReflectionRejectsSensitiveFields(t *testing.T) {
	assertResourceReadTypeKeys(t, reflect.TypeOf(ResourceAccount{}), []string{
		"created_at",
		"id",
		"name",
		"owned_by_me",
		"platform",
		"public_access_level",
		"status",
		"type",
		"updated_at",
	})
	assertResourceReadTypeKeys(t, reflect.TypeOf(ResourceGroup{}), []string{
		"created_at",
		"description",
		"id",
		"name",
		"owned_by_me",
		"platform",
		"public_access_level",
		"status",
		"updated_at",
	})
}

func TestResourceReadMappersHandleNil(t *testing.T) {
	require.Nil(t, ResourceAccountFromService(nil, 1))
	require.Nil(t, ResourceGroupFromService(nil, 1))
}

func assertResourceReadJSONKeys(t *testing.T, value any, want []string) {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)

	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &document))
	got := make([]string, 0, len(document))
	for key := range document {
		got = append(got, key)
	}
	sort.Strings(got)
	require.Equal(t, want, got)
	assertForbiddenResourceReadKeys(t, value)
}

func assertResourceReadTypeKeys(t *testing.T, typ reflect.Type, want []string) {
	t.Helper()
	got := make([]string, 0, typ.NumField())
	for index := 0; index < typ.NumField(); index++ {
		field := typ.Field(index)
		key := strings.SplitN(field.Tag.Get("json"), ",", 2)[0]
		if key != "" && key != "-" {
			got = append(got, key)
		}
	}
	sort.Strings(got)
	require.Equal(t, want, got)
}

func assertForbiddenResourceReadKeys(t *testing.T, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)

	var document map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &document))
	for _, key := range forbiddenResourceReadKeys {
		require.NotContains(t, document, key, "resource DTO must not expose %q", key)
	}
}

func accessLevelPointer(level authz.AccessLevel) *authz.AccessLevel {
	return &level
}

var forbiddenResourceReadKeys = []string{
	"access_version",
	"account_count",
	"account_groups",
	"active_account_count",
	"authorization_mode",
	"concurrency",
	"created_by_user_id",
	"credentials",
	"credentials_status",
	"error_message",
	"expires_at",
	"extra",
	"fallback_group_id",
	"group_ids",
	"groups",
	"last_used_at",
	"load_factor",
	"model_pricing",
	"model_routing",
	"owner_user_id",
	"parent_account_id",
	"pricing",
	"priority",
	"profit_control_enabled",
	"proxy",
	"proxy_fallback_origin_id",
	"proxy_id",
	"quota_dimension",
	"rate_limit_reset_at",
	"rate_limited_account_count",
	"rate_limited_at",
	"rate_multiplier",
	"routing",
	"schedulable",
	"subscription_type",
	"temp_unschedulable_reason",
	"temp_unschedulable_until",
}
