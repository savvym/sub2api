package repository

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/authz"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestResourceMutationConcurrentSameVersionExactlyOneCommitsPostgres(t *testing.T) {
	db := newAuthzPolicyPostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatalf("apply migrations to resource-mutation TOCTOU database: %v", err)
	}

	adminIDs := make([]int64, 2)
	for index := range adminIDs {
		email := fmt.Sprintf("resource-mutation-race-admin-%d@example.test", index+1)
		username := fmt.Sprintf("resource-mutation-race-admin-%d", index+1)
		if err := db.QueryRowContext(ctx, `
			INSERT INTO users (email, password_hash, role, status, username)
			VALUES ($1, 'not-a-real-hash', 'admin', 'active', $2)
			RETURNING id
		`, email, username).Scan(&adminIDs[index]); err != nil {
			t.Fatalf("insert mutation race admin %d: %v", index+1, err)
		}
	}
	var accountID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO accounts (
			name, platform, type, credentials, extra, status,
			owner_user_id, created_by_user_id, access_version
		)
		VALUES ('resource-mutation-race-original', 'openai', 'apikey', '{}'::jsonb, '{}'::jsonb, 'active', $1, $1, 1)
		RETURNING id
		`, adminIDs[0]).Scan(&accountID); err != nil {
		t.Fatalf("insert mutation race account: %v", err)
	}

	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer func() { _ = client.Close() }()
	store := &authzPolicyStore{client: client}
	resolver := authz.NewActorResolver(store)
	actors := make([]authz.Actor, len(adminIDs))
	for index, adminID := range adminIDs {
		actor, resolveErr := resolver.ResolveLegacyAdminUser(ctx, adminID)
		if resolveErr != nil {
			t.Fatalf("resolve mutation race admin %d: %v", index+1, resolveErr)
		}
		actors[index] = actor
	}
	ref, err := authz.NewResourceRef(authz.ResourceTypeAccount, accountID)
	if err != nil {
		t.Fatalf("build mutation race resource ref: %v", err)
	}

	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	repository := &resourceMutationActorLockBarrierRepository{
		ResourceMutationRepository: NewResourceMutationRepository(client),
		arrived:                    arrived,
		release:                    release,
	}
	coordinator := service.NewResourceMutationCoordinator(
		repository,
		resolver,
		authz.NewPolicyService(store),
	)
	command := service.ResourceMutationCommand{Targets: []service.ResourceMutationTarget{{
		Ref:                   ref,
		Action:                authz.ActionAccountEdit,
		ExpectedAccessVersion: 1,
		Mutates:               true,
		EventType:             "account.updated",
		ChangedFields:         []string{"name"},
	}}}

	type mutationResult struct {
		name string
		err  error
	}
	results := make(chan mutationResult, 2)
	var mutationAttempts atomic.Int32
	for index := 1; index <= 2; index++ {
		name := fmt.Sprintf("resource-mutation-race-writer-%d", index)
		requestID := fmt.Sprintf("resource-mutation-race-request-%d", index)
		actor := actors[index-1]
		go func() {
			requestCtx := service.WithResourceMutationAuditTrace(ctx, service.ResourceMutationAuditTrace{
				Method:    "PUT",
				Path:      fmt.Sprintf("/accounts/%d", accountID),
				RequestID: requestID,
			})
			executeErr := coordinator.Execute(requestCtx, actor, command, func(txCtx context.Context) ([]service.CreatedResourceMutation, error) {
				mutationAttempts.Add(1)
				queryer := clientFromContext(txCtx, client)
				if _, updateErr := queryer.ExecContext(txCtx, `
					UPDATE accounts
					SET name = $2, updated_at = statement_timestamp()
					WHERE id = $1
				`, accountID, name); updateErr != nil {
					return nil, updateErr
				}
				return nil, enqueueSchedulerOutbox(
					txCtx,
					queryer,
					service.SchedulerOutboxEventAccountChanged,
					&accountID,
					nil,
					nil,
				)
			})
			results <- mutationResult{name: name, err: executeErr}
		}()
	}

	for index := 0; index < 2; index++ {
		select {
		case <-arrived:
		case <-ctx.Done():
			t.Fatalf("wait for concurrent mutation %d to reach actor lock: %v", index+1, ctx.Err())
		}
	}
	close(release)

	var winner string
	conflicts := 0
	for index := 0; index < 2; index++ {
		select {
		case result := <-results:
			switch {
			case result.err == nil:
				if winner != "" {
					t.Fatalf("both same-version mutations committed: first=%s second=%s", winner, result.name)
				}
				winner = result.name
			case errors.Is(result.err, service.ErrResourceMutationConflict):
				conflicts++
			default:
				t.Fatalf("mutation %s returned unexpected error: %v", result.name, result.err)
			}
		case <-ctx.Done():
			t.Fatalf("wait for concurrent mutation result %d: %v", index+1, ctx.Err())
		}
	}
	if winner == "" || conflicts != 1 {
		t.Fatalf("same-version mutation results: winner=%q conflicts=%d", winner, conflicts)
	}
	// PostgreSQL may detect the serialization loser while locking the resource,
	// during its write, or at commit. The loser's closure can therefore run even
	// though all of its durable effects are rolled back.
	if attempts := mutationAttempts.Load(); attempts < 1 || attempts > 2 {
		t.Fatalf("transaction mutation attempts = %d, want 1 or 2", attempts)
	}

	var persistedName string
	var accessVersion int64
	if err := db.QueryRowContext(ctx, `
		SELECT name, access_version
		FROM accounts
		WHERE id = $1
	`, accountID).Scan(&persistedName, &accessVersion); err != nil {
		t.Fatalf("read mutation race result: %v", err)
	}
	if persistedName != winner || accessVersion != 2 {
		t.Fatalf("mutation race state = name %q version %d, want %q version 2", persistedName, accessVersion, winner)
	}

	var eventCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM resource_authorization_events
		WHERE account_id = $1 AND event_type = 'account.updated'
	`, accountID).Scan(&eventCount); err != nil {
		t.Fatalf("count mutation race authorization events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("mutation race authorization events = %d, want 1", eventCount)
	}

	var outboxCount int
	if err := db.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM scheduler_outbox
		WHERE account_id = $1 AND event_type = 'account_changed'
	`, accountID).Scan(&outboxCount); err != nil {
		t.Fatalf("count mutation race scheduler events: %v", err)
	}
	if outboxCount != 1 {
		t.Fatalf("mutation race scheduler events = %d, want 1", outboxCount)
	}
}

func TestAdminServiceClearAccountErrorRunsCallbackAfterCommitPostgres(t *testing.T) {
	db := newAuthzPolicyPostgresTestDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := ApplyMigrations(ctx, db); err != nil {
		t.Fatalf("apply migrations to after-commit database: %v", err)
	}

	var adminID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash, role, status, username)
		VALUES ('resource-mutation-after-commit-admin@example.test', 'not-a-real-hash', 'admin', 'active', 'resource-mutation-after-commit-admin')
		RETURNING id
	`).Scan(&adminID); err != nil {
		t.Fatalf("insert after-commit admin: %v", err)
	}
	var accountID int64
	if err := db.QueryRowContext(ctx, `
		INSERT INTO accounts (
			name, platform, type, credentials, extra, status, error_message,
			owner_user_id, created_by_user_id, access_version
		)
		VALUES (
			'resource-mutation-after-commit', 'openai', 'apikey', '{}'::jsonb, '{}'::jsonb,
			'error', 'transient failure', $1, $1, 1
		)
		RETURNING id
	`, adminID).Scan(&accountID); err != nil {
		t.Fatalf("insert after-commit account: %v", err)
	}

	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer func() { _ = client.Close() }()
	store := &authzPolicyStore{client: client}
	resolver := authz.NewActorResolver(store)
	actor, err := resolver.ResolveLegacyAdminUser(ctx, adminID)
	if err != nil {
		t.Fatalf("resolve after-commit admin: %v", err)
	}
	coordinator := service.NewResourceMutationCoordinator(
		NewResourceMutationRepository(client),
		resolver,
		authz.NewPolicyService(store),
	)
	accountRepo := NewAdminAccountRepository(client, db, nil)

	requestCtx := service.WithResourceMutationAuditTrace(ctx, service.ResourceMutationAuditTrace{
		Method:    "POST",
		Path:      fmt.Sprintf("/accounts/%d/clear-error", accountID),
		RequestID: "resource-mutation-after-commit-request",
	})
	observations := make(chan resourceMutationAfterCommitObservation, 2)
	var callbackCalls atomic.Int32
	blocker := &resourceMutationAfterCommitBlocker{onClear: func(clearedAccountID int64) {
		callbackCalls.Add(1)
		callbackCtx, callbackCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer callbackCancel()
		observation := resourceMutationAfterCommitObservation{
			accountID:      clearedAccountID,
			auditCommitted: service.ResourceMutationAuditCommitted(requestCtx),
		}
		observation.err = db.QueryRowContext(callbackCtx, `
			SELECT status, access_version
			FROM accounts
			WHERE id = $1
		`, clearedAccountID).Scan(&observation.status, &observation.accessVersion)
		observations <- observation
	}}
	adminService := service.NewAdminService(
		nil,
		nil,
		nil,
		accountRepo,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		client,
		nil,
		nil,
		nil,
		nil,
		blocker,
		nil,
		nil,
		nil,
		nil,
		coordinator,
	)

	updated, err := adminService.ClearAccountError(requestCtx, actor, accountID)
	if err != nil {
		t.Fatalf("clear account error through production admin service: %v", err)
	}
	if updated == nil || updated.Status != service.StatusActive || updated.AccessVersion != 2 {
		t.Fatalf("cleared account = %#v, want active access_version 2", updated)
	}
	if callbackCalls.Load() != 1 {
		t.Fatalf("after-commit callback calls = %d, want 1", callbackCalls.Load())
	}
	select {
	case observation := <-observations:
		if observation.err != nil {
			t.Fatalf("read committed account from after-commit callback: %v", observation.err)
		}
		if observation.accountID != accountID || observation.status != service.StatusActive || observation.accessVersion != 2 {
			t.Fatalf(
				"after-commit observation = account %d status %q version %d, want account %d status %q version 2",
				observation.accountID,
				observation.status,
				observation.accessVersion,
				accountID,
				service.StatusActive,
			)
		}
		if !observation.auditCommitted {
			t.Fatal("resource mutation audit was not marked committed before callback")
		}
	case <-ctx.Done():
		t.Fatalf("wait for after-commit observation: %v", ctx.Err())
	}
}

type resourceMutationAfterCommitObservation struct {
	accountID      int64
	status         string
	accessVersion  int64
	auditCommitted bool
	err            error
}

type resourceMutationAfterCommitBlocker struct {
	onClear func(accountID int64)
}

func (*resourceMutationAfterCommitBlocker) BlockAccountScheduling(*service.Account, time.Time, string) {
}

func (b *resourceMutationAfterCommitBlocker) ClearAccountSchedulingBlock(accountID int64) {
	if b != nil && b.onClear != nil {
		b.onClear(accountID)
	}
}

type resourceMutationActorLockBarrierRepository struct {
	service.ResourceMutationRepository
	arrived chan<- struct{}
	release <-chan struct{}
}

func (r *resourceMutationActorLockBarrierRepository) LockActorAuthorization(
	ctx context.Context,
	kind authz.SubjectKind,
	id int64,
) error {
	if err := r.ResourceMutationRepository.LockActorAuthorization(ctx, kind, id); err != nil {
		return err
	}
	select {
	case r.arrived <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-r.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
