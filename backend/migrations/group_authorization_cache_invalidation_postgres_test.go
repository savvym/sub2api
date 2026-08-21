//go:build integration

package migrations_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/repository"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestGroupAuthorizationCacheInvalidationPostgres(t *testing.T) {
	db := newRoleCachePostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	require.NoError(t, repository.ApplyMigrations(ctx, db))

	migrationSQL, err := dbmigrations.FS.ReadFile("237_group_authorization_cache_invalidation.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err, "migration 237 must be safe to reapply")

	var userID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO users (email, password_hash, role, status)
VALUES ('group-auth-cache@example.test', 'not-a-real-password-hash', 'user', 'active')
RETURNING id`).Scan(&userID))

	var targetGroupID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO groups (name, rate_multiplier, is_exclusive, status)
VALUES ('group-auth-cache-target', 1, FALSE, 'active')
RETURNING id`).Scan(&targetGroupID))

	var groupID int64
	require.NoError(t, db.QueryRowContext(ctx, `
INSERT INTO groups (name, rate_multiplier, is_exclusive, status)
VALUES ('group-auth-cache-subject', 1, FALSE, 'active')
RETURNING id`).Scan(&groupID))

	const plaintextKey = "sk-group-authorization-cache-invalidation"
	_, err = db.ExecContext(ctx, `
INSERT INTO api_keys (user_id, group_id, key, name, status)
VALUES ($1, $2, $3, 'group-auth-cache-key', 'active')`, userID, groupID, plaintextKey)
	require.NoError(t, err)
	require.NoError(t, clearGroupAuthorizationCacheOutbox(ctx, db))

	updates := []struct {
		name  string
		query string
		args  []any
	}{
		{name: "owner", query: `UPDATE groups SET owner_user_id = $2 WHERE id = $1`, args: []any{groupID, userID}},
		{name: "public access", query: `UPDATE groups SET public_access_level = 'consumer' WHERE id = $1`, args: []any{groupID}},
		{name: "access version", query: `UPDATE groups SET access_version = access_version + 1 WHERE id = $1`, args: []any{groupID}},
		{name: "authorization mode", query: `UPDATE groups SET authorization_mode = 'shadow' WHERE id = $1`, args: []any{groupID}},
		{name: "OAuth account eligibility", query: `UPDATE groups SET require_oauth_only = TRUE WHERE id = $1`, args: []any{groupID}},
		{name: "privacy account eligibility", query: `UPDATE groups SET require_privacy_set = TRUE WHERE id = $1`, args: []any{groupID}},
		{name: "subscription limit", query: `UPDATE groups SET daily_limit_usd = 12.5 WHERE id = $1`, args: []any{groupID}},
		{name: "batch image permission", query: `UPDATE groups SET allow_batch_image_generation = TRUE WHERE id = $1`, args: []any{groupID}},
		{name: "image pricing policy", query: `UPDATE groups SET image_rate_independent = TRUE WHERE id = $1`, args: []any{groupID}},
		{name: "video model prices", query: `UPDATE groups SET video_model_prices = $2::jsonb WHERE id = $1`, args: []any{groupID, `{"grok-video":{"720p":0.1}}`}},
		{name: "long context pricing", query: `UPDATE groups SET long_context_pricing_enabled = NOT long_context_pricing_enabled WHERE id = $1`, args: []any{groupID}},
		{name: "model pricing", query: `UPDATE groups SET model_pricing = $2::jsonb WHERE id = $1`, args: []any{groupID, `[{"model":"gpt-test"}]`}},
		{name: "client restriction", query: `UPDATE groups SET claude_code_only = TRUE WHERE id = $1`, args: []any{groupID}},
		{name: "fallback", query: `UPDATE groups SET fallback_group_id = $2 WHERE id = $1`, args: []any{groupID, targetGroupID}},
		{name: "model routing", query: `UPDATE groups SET model_routing = $2::jsonb WHERE id = $1`, args: []any{groupID, `{"claude-3":[1]}`}},
		{name: "MCP policy", query: `UPDATE groups SET mcp_xml_inject = FALSE WHERE id = $1`, args: []any{groupID}},
		{name: "supported model scopes", query: `UPDATE groups SET supported_model_scopes = $2::jsonb WHERE id = $1`, args: []any{groupID, `["claude"]`}},
		{name: "messages dispatch", query: `UPDATE groups SET allow_messages_dispatch = TRUE WHERE id = $1`, args: []any{groupID}},
		{name: "live permission", query: `UPDATE groups SET allow_live = TRUE WHERE id = $1`, args: []any{groupID}},
		{name: "mapped model", query: `UPDATE groups SET default_mapped_model = 'gpt-test' WHERE id = $1`, args: []any{groupID}},
		{name: "messages dispatch policy", query: `UPDATE groups SET messages_dispatch_model_config = $2::jsonb WHERE id = $1`, args: []any{groupID, `{"default_model":"gpt-test"}`}},
		{name: "models list policy", query: `UPDATE groups SET models_list_config = $2::jsonb WHERE id = $1`, args: []any{groupID, `{"enabled":true}`}},
		{name: "RPM limit", query: `UPDATE groups SET rpm_limit = 60 WHERE id = $1`, args: []any{groupID}},
		{name: "reasoning ceiling", query: `UPDATE groups SET max_reasoning_effort = 'medium' WHERE id = $1`, args: []any{groupID}},
		{name: "reasoning mappings", query: `UPDATE groups SET reasoning_effort_mappings = $2::jsonb WHERE id = $1`, args: []any{groupID, `[{"from":"high","to":"medium"}]`}},
	}

	for _, update := range updates {
		t.Run(update.name, func(t *testing.T) {
			require.NoError(t, clearGroupAuthorizationCacheOutbox(ctx, db))
			_, updateErr := db.ExecContext(ctx, update.query, update.args...)
			require.NoError(t, updateErr)
			assertGroupAuthorizationCacheOutbox(t, ctx, db, plaintextKey)
		})
	}

	t.Run("cosmetic update stays silent", func(t *testing.T) {
		require.NoError(t, clearGroupAuthorizationCacheOutbox(ctx, db))
		_, updateErr := db.ExecContext(ctx, `
UPDATE groups
SET name = name || '-renamed',
    description = 'cosmetic',
    sort_order = sort_order + 1,
    created_by_user_id = $2
WHERE id = $1`, groupID, userID)
		require.NoError(t, updateErr)

		var count int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM auth_cache_invalidation_outbox`,
		).Scan(&count))
		require.Zero(t, count)
	})

	t.Run("outbox rolls back with group mutation", func(t *testing.T) {
		require.NoError(t, clearGroupAuthorizationCacheOutbox(ctx, db))
		var beforeVersion int64
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT access_version FROM groups WHERE id = $1`, groupID,
		).Scan(&beforeVersion))

		tx, txErr := db.BeginTx(ctx, nil)
		require.NoError(t, txErr)
		_, txErr = tx.ExecContext(ctx,
			`UPDATE groups SET access_version = access_version + 1 WHERE id = $1`, groupID,
		)
		require.NoError(t, txErr)
		assertGroupAuthorizationCacheOutbox(t, ctx, tx, plaintextKey)
		require.NoError(t, tx.Rollback())

		var outboxCount int
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM auth_cache_invalidation_outbox`,
		).Scan(&outboxCount))
		require.Zero(t, outboxCount)

		var afterVersion int64
		require.NoError(t, db.QueryRowContext(ctx,
			`SELECT access_version FROM groups WHERE id = $1`, groupID,
		).Scan(&afterVersion))
		require.Equal(t, beforeVersion, afterVersion)
	})
}

type groupAuthorizationCacheOutboxQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func assertGroupAuthorizationCacheOutbox(
	t *testing.T,
	ctx context.Context,
	queryer groupAuthorizationCacheOutboxQueryer,
	plaintextKey string,
) {
	t.Helper()

	rows, err := queryer.QueryContext(ctx, `
SELECT cache_key
FROM auth_cache_invalidation_outbox
ORDER BY id`)
	require.NoError(t, err)
	defer rows.Close()

	var actual []string
	for rows.Next() {
		var cacheKey string
		require.NoError(t, rows.Scan(&cacheKey))
		actual = append(actual, cacheKey)
		require.NotEqual(t, plaintextKey, cacheKey)
		require.NotContains(t, cacheKey, plaintextKey)
	}
	require.NoError(t, rows.Err())

	sum := sha256.Sum256([]byte(plaintextKey))
	require.Equal(t, []string{hex.EncodeToString(sum[:])}, actual)
}

type groupAuthorizationCacheOutboxExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func clearGroupAuthorizationCacheOutbox(
	ctx context.Context,
	execer groupAuthorizationCacheOutboxExecer,
) error {
	_, err := execer.ExecContext(ctx, `TRUNCATE auth_cache_invalidation_outbox`)
	return err
}
