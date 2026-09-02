package repository

import (
	"context"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type captureOpenAIAutoResetRecoveryQueryMatcher struct {
	actual *string
}

func (m captureOpenAIAutoResetRecoveryQueryMatcher) Match(_, actual string) error {
	*m.actual = actual
	return nil
}

func TestListOpenAIAutoResetRecoveryCandidatePageUsesBoundedKeysetQuery(t *testing.T) {
	var capturedSQL string
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(
		captureOpenAIAutoResetRecoveryQueryMatcher{actual: &capturedSQL},
	))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	mock.ExpectQuery("recovery candidates").
		WithArgs(int64(10), 2).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(int64(11)).AddRow(int64(12)))
	repo := newAccountRepositoryWithSQL(nil, db, nil)

	page, err := repo.ListOpenAIAutoResetRecoveryCandidatePage(
		context.Background(),
		service.OpenAIAutoResetRecoveryCandidatePageOptions{AfterID: 10, Limit: 2},
	)

	require.NoError(t, err)
	require.Equal(t, []int64{11, 12}, page.AccountIDs)
	require.Equal(t, int64(12), page.NextAfterID)
	require.True(t, page.HasMore)
	require.NoError(t, mock.ExpectationsWereMet())

	normalizedSQL := strings.Join(strings.Fields(capturedSQL), " ")
	for _, required := range []string{
		"deleted_at IS NULL",
		"platform = 'openai'",
		"type = 'oauth'",
		"parent_account_id IS NULL",
		"id > $1",
		"= 'resetting'",
		"= 'failed'",
		"attempt_credit_hash",
		"attempt_cycle_hash",
		"ORDER BY id ASC",
		"LIMIT $2",
	} {
		require.Contains(t, normalizedSQL, required)
	}
	require.NotContains(t, normalizedSQL, "schedulable")
	require.NotContains(t, normalizedSQL, "status = 'active'")
	require.NotContains(t, normalizedSQL, service.OpenAIAutoResetCreditEnabledExtraKey)
	require.NotContains(t, normalizedSQL, "~ '^[0-9a-f]{24}$'",
		"malformed pending identities must be surfaced to the fail-closed service path")
}

func TestListOpenAIAutoResetRecoveryCandidatePageRejectsUnboundedOptions(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	repo := newAccountRepositoryWithSQL(nil, db, nil)
	tests := []service.OpenAIAutoResetRecoveryCandidatePageOptions{
		{AfterID: -1, Limit: 1},
		{Limit: 0},
		{Limit: openAIAutoResetRecoveryCandidateMaxPageSize + 1},
	}
	for _, options := range tests {
		_, err = repo.ListOpenAIAutoResetRecoveryCandidatePage(context.Background(), options)
		require.Error(t, err)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}
