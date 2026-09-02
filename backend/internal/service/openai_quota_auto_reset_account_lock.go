package service

import "context"

// OpenAIQuotaAutoResetAccountLease owns the cross-instance advisory lock for
// one account until Release is called. LockAccountRow upgrades the lease at
// the external-effect boundary so account writers cannot invalidate the final
// eligibility check before the upstream POST returns.
type OpenAIQuotaAutoResetAccountLease interface {
	LockAccountRow(ctx context.Context) (exists bool, err error)
	Release() error
}

// OpenAIQuotaAutoResetAccountLocker serializes OpenAI quota auto-reset side
// effects for an account across all application instances.
type OpenAIQuotaAutoResetAccountLocker interface {
	TryAcquire(ctx context.Context, accountID int64) (lease OpenAIQuotaAutoResetAccountLease, acquired bool, err error)
}
