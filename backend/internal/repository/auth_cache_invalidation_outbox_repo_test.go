package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
	"time"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestAuthCacheInvalidationOutboxRepository_ClaimUsesLeaseAndSkipLocked(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	created := time.Now().UTC()
	mock.ExpectQuery("(?s)claimed_at < statement_timestamp\\(\\) - .*ORDER BY delivery_stage ASC, available_at ASC, id ASC.*FOR UPDATE SKIP LOCKED.*RETURNING").
		WithArgs("worker-a", 100, int64(30)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "cache_key", "attempts", "delivery_stage", "created_at"}).
			AddRow(int64(4), strings.Repeat("a", 64), 2, 1, created))

	repo := NewAuthCacheInvalidationOutboxRepository(db)
	events, err := repo.Claim(context.Background(), "worker-a", 100, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, int64(4), events[0].ID)
	require.Equal(t, 1, events[0].Stage)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthCacheInvalidationOutboxRepository_ClaimIsBoundedByDefault(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("(?s)FROM auth_cache_invalidation_outbox.*LIMIT \\$2.*SKIP LOCKED").
		WithArgs("worker", 100, int64(30)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "cache_key", "attempts", "delivery_stage", "created_at"}))
	repo := NewAuthCacheInvalidationOutboxRepository(db)
	_, err = repo.Claim(context.Background(), "worker", 0, 0)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthCacheInvalidationOutboxRepository_ClaimOwnershipTransitions(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := NewAuthCacheInvalidationOutboxRepository(db)

	const scheduleSecondPassSQL = `
		UPDATE auth_cache_invalidation_outbox
		SET delivery_stage = 1,
			available_at = statement_timestamp() + ($3 * INTERVAL '1 millisecond'),
			last_error = NULL,
			claimed_at = NULL,
			claimed_by = NULL
		WHERE id = $1 AND claimed_by = $2 AND delivery_stage = 0
	`
	secondPassDelay := time.Minute
	mock.ExpectExec(regexp.QuoteMeta(scheduleSecondPassSQL)).
		WithArgs(int64(1), "worker", secondPassDelay.Milliseconds()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.ScheduleSecondPass(context.Background(), 1, "worker", secondPassDelay))

	const retrySQL = `
		UPDATE auth_cache_invalidation_outbox
		SET attempts = attempts + 1,
			available_at = statement_timestamp() + ($3 * INTERVAL '1 millisecond'),
			last_error = $4,
			claimed_at = NULL,
			claimed_by = NULL
		WHERE id = $1 AND claimed_by = $2
	`
	retryDelay := 2 * time.Minute
	mock.ExpectExec(regexp.QuoteMeta(retrySQL)).
		WithArgs(int64(2), "worker", retryDelay.Milliseconds(), "publish failed").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.RetryClaimed(context.Background(), 2, "worker", retryDelay, "publish failed"))

	mock.ExpectExec("DELETE FROM auth_cache_invalidation_outbox").
		WithArgs(int64(3), "worker").
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.DeleteClaimed(context.Background(), 3, "worker"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthCacheInvalidationOutboxRepository_RejectsLostClaim(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectExec("DELETE FROM auth_cache_invalidation_outbox").
		WithArgs(int64(3), "old-worker").
		WillReturnResult(sqlmock.NewResult(0, 0))
	repo := NewAuthCacheInvalidationOutboxRepository(db)
	err = repo.DeleteClaimed(context.Background(), 3, "old-worker")
	require.ErrorContains(t, err, "no longer owned")
}

func TestAuthCacheInvalidationOutboxRepository_ReleasesOnlyOwnedClaims(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	mock.ExpectExec("(?s)UPDATE auth_cache_invalidation_outbox.*claimed_at = NULL.*claimed_by = NULL.*WHERE claimed_by = \\$1.*id = ANY\\(\\$2::BIGINT\\[\\]\\)").
		WithArgs("worker-a", sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 2))

	err = NewAuthCacheInvalidationOutboxRepository(db).ReleaseClaims(
		context.Background(), "worker-a", []int64{7, 8},
	)
	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthCacheInvalidationOutboxRepository_StatsExposeDurableLagAndFailures(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	oldest := time.Now().UTC().Add(-time.Minute)
	stage0Oldest := oldest.Add(15 * time.Second)
	stage1Oldest := oldest.Add(30 * time.Second)
	mock.ExpectQuery("(?s)COUNT\\(\\*\\) FILTER \\(WHERE delivery_stage = 0\\).*COUNT\\(\\*\\) FILTER \\(WHERE delivery_stage = 1\\)").
		WillReturnRows(sqlmock.NewRows([]string{
			"count", "min", "max", "last_error",
			"stage0_count", "stage0_min", "stage0_max", "stage0_last_error",
			"stage1_count", "stage1_min", "stage1_max", "stage1_last_error",
		}).AddRow(
			5, oldest, 7, "redis down",
			2, stage0Oldest, 3, "stage0 retry",
			3, stage1Oldest, 7, "stage1 retry",
		))
	repo := NewAuthCacheInvalidationOutboxRepository(db)
	stats, err := repo.Stats(context.Background())
	require.NoError(t, err)
	require.Equal(t, int64(5), stats.Pending)
	require.Equal(t, 7, stats.MaxAttempts)
	require.Equal(t, "redis down", stats.LastError)
	require.Equal(t, oldest, *stats.OldestCreatedAt)
	require.Equal(t, int64(2), stats.Stage0.Pending)
	require.Equal(t, stage0Oldest, *stats.Stage0.OldestCreatedAt)
	require.Equal(t, 3, stats.Stage0.MaxAttempts)
	require.Equal(t, "stage0 retry", stats.Stage0.LastError)
	require.Equal(t, int64(3), stats.Stage1.Pending)
	require.Equal(t, stage1Oldest, *stats.Stage1.OldestCreatedAt)
	require.Equal(t, 7, stats.Stage1.MaxAttempts)
	require.Equal(t, "stage1 retry", stats.Stage1.LastError)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthCacheInvalidationOutboxRepository_StatsHandleEmptyStages(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	mock.ExpectQuery("(?s)FROM auth_cache_invalidation_outbox").
		WillReturnRows(sqlmock.NewRows([]string{
			"count", "min", "max", "last_error",
			"stage0_count", "stage0_min", "stage0_max", "stage0_last_error",
			"stage1_count", "stage1_min", "stage1_max", "stage1_last_error",
		}).AddRow(0, nil, 0, nil, 0, nil, 0, nil, 0, nil, 0, nil))

	repo := NewAuthCacheInvalidationOutboxRepository(db)
	stats, err := repo.Stats(context.Background())
	require.NoError(t, err)
	require.Zero(t, stats.Pending)
	require.Nil(t, stats.OldestCreatedAt)
	require.Nil(t, stats.Stage0.OldestCreatedAt)
	require.Nil(t, stats.Stage1.OldestCreatedAt)
	require.Empty(t, stats.LastError)
	require.Empty(t, stats.Stage0.LastError)
	require.Empty(t, stats.Stage1.LastError)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAuthCacheInvalidationMigration_SecurityCoverageAndNoPlaintextPayload(t *testing.T) {
	content, err := migrations.FS.ReadFile("184_auth_cache_invalidation_outbox.sql")
	require.NoError(t, err)
	sqlText := string(content)
	for _, required := range []string{
		"encode(sha256(convert_to(raw_key, 'UTF8')), 'hex')",
		"OLD.key", "OLD.status", "OLD.deleted_at", "OLD.user_id", "OLD.group_id",
		"OLD.ip_whitelist", "OLD.ip_blacklist", "OLD.expires_at",
		"trg_users_auth_cache_invalidation", "trg_groups_auth_cache_invalidation",
		"trg_user_allowed_groups_auth_cache_invalidation", "FOR EACH ROW",
		"delivery_stage", "claimed_at", "available_at",
	} {
		require.Contains(t, sqlText, required)
	}
	require.NotContains(t, sqlText, "quota_used IS DISTINCT")
	require.NotContains(t, sqlText, "last_used_at IS DISTINCT")

	plaintext := "sk-plaintext-must-not-be-stored"
	sum := sha256.Sum256([]byte(plaintext))
	require.Len(t, hex.EncodeToString(sum[:]), 64)
	require.NotContains(t, sqlText, plaintext)
}
