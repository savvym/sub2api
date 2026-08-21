package migrations

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupAuthorizationCacheInvalidationMigrationContract(t *testing.T) {
	content, err := FS.ReadFile("237_group_authorization_cache_invalidation.sql")
	require.NoError(t, err)

	compact := strings.Join(strings.Fields(string(content)), " ")
	require.Contains(t, compact, "CREATE OR REPLACE FUNCTION enqueue_group_auth_cache_invalidation()")

	for _, column := range []string{
		"owner_user_id",
		"public_access_level",
		"access_version",
		"authorization_mode",
		"status",
		"is_exclusive",
		"platform",
		"subscription_type",
		"require_oauth_only",
		"require_privacy_set",
		"daily_limit_usd",
		"weekly_limit_usd",
		"monthly_limit_usd",
		"rate_multiplier",
		"allow_image_generation",
		"allow_batch_image_generation",
		"image_rate_independent",
		"image_rate_multiplier",
		"image_price_1k",
		"image_price_2k",
		"image_price_4k",
		"video_rate_independent",
		"video_rate_multiplier",
		"video_price_480p",
		"video_price_720p",
		"video_price_1080p",
		"video_model_prices",
		"web_search_price_per_call",
		"search_price_per_1k",
		"audio_realtime_price_per_min",
		"audio_tts_price_per_million_chars",
		"audio_stt_price_per_hour",
		"long_context_pricing_enabled",
		"model_pricing",
		"peak_rate_enabled",
		"peak_start",
		"peak_end",
		"peak_rate_multiplier",
		"profit_control_enabled",
		"profit_min_margin",
		"profit_safety_buffer",
		"claude_code_only",
		"fallback_group_id",
		"fallback_group_id_on_invalid_request",
		"model_routing",
		"model_routing_enabled",
		"mcp_xml_inject",
		"supported_model_scopes",
		"allow_messages_dispatch",
		"allow_live",
		"default_mapped_model",
		"messages_dispatch_model_config",
		"models_list_config",
		"rpm_limit",
		"max_reasoning_effort",
		"reasoning_effort_mappings",
		"deleted_at",
	} {
		require.Contains(t, compact,
			"OLD."+column+" IS NOT DISTINCT FROM NEW."+column,
			"security-relevant Group column %s must be monitored", column,
		)
	}

	require.Equal(t, 1, strings.Count(compact, "INSERT INTO auth_cache_invalidation_outbox"))
	require.Contains(t, compact, "SELECT encode(sha256(convert_to(k.key, 'UTF8')), 'hex')")
	require.Contains(t, compact, "WHERE k.group_id = target_group_id")
	require.Contains(t, compact, "AND k.deleted_at IS NULL")
	require.Contains(t, compact, "AND k.key <> ''")

	for _, cosmeticColumn := range []string{"name", "description", "sort_order", "created_by_user_id"} {
		require.NotContains(t, compact, "OLD."+cosmeticColumn+" IS NOT DISTINCT FROM NEW."+cosmeticColumn)
	}
	require.NotContains(t, compact, "VALUES (k.key)")
	require.NotContains(t, compact, "DROP TRIGGER")
	require.NotContains(t, compact, "CREATE TRIGGER")
}
