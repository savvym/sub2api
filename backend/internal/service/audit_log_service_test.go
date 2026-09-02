package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type auditLogServiceRepositoryStub struct {
	insertCalls int
	insertCtx   context.Context
	inserted    *AuditLog
	insertErr   error
}

func (r *auditLogServiceRepositoryStub) BatchInsert(context.Context, []*AuditLog) (int64, error) {
	return 0, nil
}

func (r *auditLogServiceRepositoryStub) Insert(ctx context.Context, entry *AuditLog) error {
	r.insertCalls++
	r.insertCtx = ctx
	r.inserted = entry
	return r.insertErr
}

func (r *auditLogServiceRepositoryStub) List(context.Context, *AuditLogFilter) (*AuditLogList, error) {
	return &AuditLogList{}, nil
}

func (r *auditLogServiceRepositoryStub) GetByID(context.Context, int64) (*AuditLog, error) {
	return nil, ErrAuditLogNotFound
}

func (r *auditLogServiceRepositoryStub) Count(context.Context) (int64, error) {
	return 0, nil
}

func (r *auditLogServiceRepositoryStub) TruncateAll(context.Context) error {
	return nil
}

func (r *auditLogServiceRepositoryStub) DeleteBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func TestAuditLogServiceRecordDurablePersistsSynchronously(t *testing.T) {
	repo := &auditLogServiceRepositoryStub{}
	service := NewAuditLogService(repo, nil)
	type contextKey string
	const key contextKey = "request"
	ctx := context.WithValue(context.Background(), key, "request-1")
	entry := &AuditLog{Action: AuditActionOpenAIQuotaAutoReset}
	startedAt := time.Now().UTC()

	err := service.RecordDurable(ctx, entry)

	require.NoError(t, err)
	require.Equal(t, 1, repo.insertCalls)
	require.Same(t, ctx, repo.insertCtx)
	require.Same(t, entry, repo.inserted)
	require.Equal(t, "request-1", repo.insertCtx.Value(key))
	require.False(t, entry.CreatedAt.IsZero())
	require.Equal(t, time.UTC, entry.CreatedAt.Location())
	require.False(t, entry.CreatedAt.Before(startedAt))
	require.False(t, entry.CreatedAt.After(time.Now().UTC()))
}

func TestAuditLogServiceRecordDurableFailsClosedOnInvalidInputs(t *testing.T) {
	t.Run("nil service", func(t *testing.T) {
		var service *AuditLogService
		err := service.RecordDurable(context.Background(), &AuditLog{})
		require.ErrorContains(t, err, "nil service")
	})

	t.Run("nil repository", func(t *testing.T) {
		service := NewAuditLogService(nil, nil)
		err := service.RecordDurable(context.Background(), &AuditLog{})
		require.ErrorContains(t, err, "nil repository")
	})

	t.Run("nil context", func(t *testing.T) {
		repo := &auditLogServiceRepositoryStub{}
		service := NewAuditLogService(repo, nil)
		err := service.RecordDurable(nil, &AuditLog{})
		require.ErrorContains(t, err, "nil context")
		require.Zero(t, repo.insertCalls)
	})

	t.Run("nil entry", func(t *testing.T) {
		repo := &auditLogServiceRepositoryStub{}
		service := NewAuditLogService(repo, nil)
		err := service.RecordDurable(context.Background(), nil)
		require.ErrorContains(t, err, "nil entry")
		require.Zero(t, repo.insertCalls)
	})

	t.Run("canceled context", func(t *testing.T) {
		repo := &auditLogServiceRepositoryStub{}
		service := NewAuditLogService(repo, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		entry := &AuditLog{}

		err := service.RecordDurable(ctx, entry)

		require.ErrorIs(t, err, context.Canceled)
		require.Zero(t, repo.insertCalls)
		require.True(t, entry.CreatedAt.IsZero())
	})
}

func TestAuditLogServiceRecordDurablePropagatesRepositoryError(t *testing.T) {
	wantErr := errors.New("audit insert failed")
	repo := &auditLogServiceRepositoryStub{insertErr: wantErr}
	service := NewAuditLogService(repo, nil)

	err := service.RecordDurable(context.Background(), &AuditLog{})

	require.ErrorIs(t, err, wantErr)
	require.Equal(t, 1, repo.insertCalls)
}

func TestAuditLogServicePrincipalConstants(t *testing.T) {
	require.Equal(t, "service_principal", AuditAuthMethodServicePrincipal)
	require.Equal(t, "system.openai.reset_credit.auto", AuditActionOpenAIQuotaAutoReset)
}
