package repository

import (
	"context"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

const openAIAutoResetRecoveryCandidateMaxPageSize = 1000

var _ service.OpenAIAutoResetRecoveryCandidatePager = (*accountRepository)(nil)

// ListOpenAIAutoResetRecoveryCandidatePage returns accounts whose managed state
// may represent an external-effecting attempt. It intentionally includes
// malformed identities so the service can fail them closed instead of silently
// skipping them. Account status, schedulability, and the auto-reset toggle are
// not eligibility conditions: once an upstream effect may have happened, local
// recovery must converge first.
func (r *accountRepository) ListOpenAIAutoResetRecoveryCandidatePage(
	ctx context.Context,
	options service.OpenAIAutoResetRecoveryCandidatePageOptions,
) (*service.OpenAIAutoResetRecoveryCandidatePage, error) {
	if r == nil || r.sql == nil {
		return nil, errors.New("account repository SQL executor not configured")
	}
	if options.AfterID < 0 {
		return nil, errors.New("OpenAI auto-reset recovery candidate cursor cannot be negative")
	}
	if options.Limit <= 0 || options.Limit > openAIAutoResetRecoveryCandidateMaxPageSize {
		return nil, errors.New("OpenAI auto-reset recovery candidate page limit must be between 1 and 1000")
	}

	rows, err := r.sql.QueryContext(ctx, `
		SELECT id
		FROM accounts
		WHERE deleted_at IS NULL
			AND platform = 'openai'
			AND type = 'oauth'
			AND parent_account_id IS NULL
			AND id > $1
			AND (
				extra -> 'codex_auto_reset_credit_state' ->> 'status' = 'resetting'
				OR (
					extra -> 'codex_auto_reset_credit_state' ->> 'status' = 'failed'
					AND (
						COALESCE(extra -> 'codex_auto_reset_credit_state' ->> 'attempt_credit_hash', '') <> ''
						OR COALESCE(extra -> 'codex_auto_reset_credit_state' ->> 'attempt_cycle_hash', '') <> ''
					)
				)
			)
		ORDER BY id ASC
		LIMIT $2
	`, options.AfterID, options.Limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	ids := make([]int64, 0, options.Limit)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	page := &service.OpenAIAutoResetRecoveryCandidatePage{
		AccountIDs: ids,
		HasMore:    len(ids) == options.Limit,
	}
	if len(ids) > 0 {
		page.NextAfterID = ids[len(ids)-1]
	}
	return page, nil
}
