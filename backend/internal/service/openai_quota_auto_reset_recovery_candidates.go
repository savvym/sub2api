package service

import "context"

// OpenAIAutoResetRecoveryCandidatePageOptions describes one bounded,
// cursor-stable scan of accounts whose external-effecting attempt still needs
// local recovery. Mutable scheduling eligibility is intentionally absent.
type OpenAIAutoResetRecoveryCandidatePageOptions struct {
	AfterID int64
	Limit   int
}

// OpenAIAutoResetRecoveryCandidatePage carries raw account IDs so the scanner
// can enqueue recovery without hydrating credentials or other account data.
type OpenAIAutoResetRecoveryCandidatePage struct {
	AccountIDs  []int64
	NextAfterID int64
	HasMore     bool
}

// OpenAIAutoResetRecoveryCandidatePager is narrower than AccountRepository so
// only the auto-reset scanner depends on the PostgreSQL recovery query.
type OpenAIAutoResetRecoveryCandidatePager interface {
	ListOpenAIAutoResetRecoveryCandidatePage(
		ctx context.Context,
		options OpenAIAutoResetRecoveryCandidatePageOptions,
	) (*OpenAIAutoResetRecoveryCandidatePage, error)
}
