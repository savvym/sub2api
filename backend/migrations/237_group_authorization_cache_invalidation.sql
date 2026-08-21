-- Keep durable API-key cache invalidation aligned with every persisted Group
-- field consumed by the authorization snapshot. The trigger was installed by
-- migration 184; replacing its function preserves the existing trigger while
-- making each outbox insert part of the Group update transaction.

CREATE OR REPLACE FUNCTION enqueue_group_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_group_id BIGINT;
BEGIN
    target_group_id := OLD.id;
    IF TG_OP = 'UPDATE'
       -- Resource authorization state.
       AND OLD.owner_user_id IS NOT DISTINCT FROM NEW.owner_user_id
       AND OLD.public_access_level IS NOT DISTINCT FROM NEW.public_access_level
       AND OLD.access_version IS NOT DISTINCT FROM NEW.access_version
       AND OLD.authorization_mode IS NOT DISTINCT FROM NEW.authorization_mode
       -- Group and subscription eligibility.
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.is_exclusive IS NOT DISTINCT FROM NEW.is_exclusive
       AND OLD.platform IS NOT DISTINCT FROM NEW.platform
       AND OLD.subscription_type IS NOT DISTINCT FROM NEW.subscription_type
       AND OLD.require_oauth_only IS NOT DISTINCT FROM NEW.require_oauth_only
       AND OLD.require_privacy_set IS NOT DISTINCT FROM NEW.require_privacy_set
       AND OLD.daily_limit_usd IS NOT DISTINCT FROM NEW.daily_limit_usd
       AND OLD.weekly_limit_usd IS NOT DISTINCT FROM NEW.weekly_limit_usd
       AND OLD.monthly_limit_usd IS NOT DISTINCT FROM NEW.monthly_limit_usd
       -- Pricing and media capabilities persisted in APIKeyAuthGroupSnapshot.
       AND OLD.rate_multiplier IS NOT DISTINCT FROM NEW.rate_multiplier
       AND OLD.allow_image_generation IS NOT DISTINCT FROM NEW.allow_image_generation
       AND OLD.allow_batch_image_generation IS NOT DISTINCT FROM NEW.allow_batch_image_generation
       AND OLD.image_rate_independent IS NOT DISTINCT FROM NEW.image_rate_independent
       AND OLD.image_rate_multiplier IS NOT DISTINCT FROM NEW.image_rate_multiplier
       AND OLD.image_price_1k IS NOT DISTINCT FROM NEW.image_price_1k
       AND OLD.image_price_2k IS NOT DISTINCT FROM NEW.image_price_2k
       AND OLD.image_price_4k IS NOT DISTINCT FROM NEW.image_price_4k
       AND OLD.video_rate_independent IS NOT DISTINCT FROM NEW.video_rate_independent
       AND OLD.video_rate_multiplier IS NOT DISTINCT FROM NEW.video_rate_multiplier
       AND OLD.video_price_480p IS NOT DISTINCT FROM NEW.video_price_480p
       AND OLD.video_price_720p IS NOT DISTINCT FROM NEW.video_price_720p
       AND OLD.video_price_1080p IS NOT DISTINCT FROM NEW.video_price_1080p
       AND OLD.video_model_prices IS NOT DISTINCT FROM NEW.video_model_prices
       AND OLD.web_search_price_per_call IS NOT DISTINCT FROM NEW.web_search_price_per_call
       AND OLD.search_price_per_1k IS NOT DISTINCT FROM NEW.search_price_per_1k
       AND OLD.audio_realtime_price_per_min IS NOT DISTINCT FROM NEW.audio_realtime_price_per_min
       AND OLD.audio_tts_price_per_million_chars IS NOT DISTINCT FROM NEW.audio_tts_price_per_million_chars
       AND OLD.audio_stt_price_per_hour IS NOT DISTINCT FROM NEW.audio_stt_price_per_hour
       AND OLD.long_context_pricing_enabled IS NOT DISTINCT FROM NEW.long_context_pricing_enabled
       AND OLD.model_pricing IS NOT DISTINCT FROM NEW.model_pricing
       AND OLD.peak_rate_enabled IS NOT DISTINCT FROM NEW.peak_rate_enabled
       AND OLD.peak_start IS NOT DISTINCT FROM NEW.peak_start
       AND OLD.peak_end IS NOT DISTINCT FROM NEW.peak_end
       AND OLD.peak_rate_multiplier IS NOT DISTINCT FROM NEW.peak_rate_multiplier
       AND OLD.profit_control_enabled IS NOT DISTINCT FROM NEW.profit_control_enabled
       AND OLD.profit_min_margin IS NOT DISTINCT FROM NEW.profit_min_margin
       AND OLD.profit_safety_buffer IS NOT DISTINCT FROM NEW.profit_safety_buffer
       -- Request admission, routing, and model policy.
       AND OLD.claude_code_only IS NOT DISTINCT FROM NEW.claude_code_only
       AND OLD.fallback_group_id IS NOT DISTINCT FROM NEW.fallback_group_id
       AND OLD.fallback_group_id_on_invalid_request IS NOT DISTINCT FROM NEW.fallback_group_id_on_invalid_request
       AND OLD.model_routing IS NOT DISTINCT FROM NEW.model_routing
       AND OLD.model_routing_enabled IS NOT DISTINCT FROM NEW.model_routing_enabled
       AND OLD.mcp_xml_inject IS NOT DISTINCT FROM NEW.mcp_xml_inject
       AND OLD.supported_model_scopes IS NOT DISTINCT FROM NEW.supported_model_scopes
       AND OLD.allow_messages_dispatch IS NOT DISTINCT FROM NEW.allow_messages_dispatch
       AND OLD.allow_live IS NOT DISTINCT FROM NEW.allow_live
       AND OLD.default_mapped_model IS NOT DISTINCT FROM NEW.default_mapped_model
       AND OLD.messages_dispatch_model_config IS NOT DISTINCT FROM NEW.messages_dispatch_model_config
       AND OLD.models_list_config IS NOT DISTINCT FROM NEW.models_list_config
       AND OLD.rpm_limit IS NOT DISTINCT FROM NEW.rpm_limit
       AND OLD.max_reasoning_effort IS NOT DISTINCT FROM NEW.max_reasoning_effort
       AND OLD.reasoning_effort_mappings IS NOT DISTINCT FROM NEW.reasoning_effort_mappings
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at THEN
        RETURN NEW;
    END IF;

    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    SELECT encode(sha256(convert_to(k.key, 'UTF8')), 'hex')
    FROM api_keys AS k
    WHERE k.group_id = target_group_id
      AND k.deleted_at IS NULL
      AND k.key <> '';
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;
