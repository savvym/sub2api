package service

import (
	"context"
	"errors"
	"time"
)

var (
	ErrOpenAIQuotaAutoResetFinalizationInvalid  = errors.New("OpenAI quota auto-reset finalization input is invalid")
	ErrOpenAIQuotaAutoResetFinalizationConflict = errors.New("OpenAI quota auto-reset finalization state conflicts with the requested outcome")
)

// OpenAIQuotaAutoResetFinalization contains the complete identity of one
// successful auto-reset outcome. The repository validates and canonicalizes
// ResponseBody before it is compared with or persisted to durable storage.
type OpenAIQuotaAutoResetFinalization struct {
	AccountID           int64
	IdempotencyRecordID int64
	ActorQualifiedScope string
	RequestFingerprint  string
	ResponseStatus      int
	ResponseBody        string
	ExpiresAt           time.Time
	Audit               AuditLog
}

// OpenAIQuotaAutoResetFinalizer atomically commits the terminal idempotency
// response and its mandatory Service Principal audit record.
type OpenAIQuotaAutoResetFinalizer interface {
	FinalizeOpenAIQuotaAutoReset(ctx context.Context, input *OpenAIQuotaAutoResetFinalization) error
}
