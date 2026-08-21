package repository

import (
	"context"
	"errors"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/compositemodelroute"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type compositeModelRouteRepository struct {
	client *dbent.Client
}

func NewCompositeModelRouteRepository(client *dbent.Client) service.CompositeModelRouteRepository {
	return &compositeModelRouteRepository{client: client}
}

func (r *compositeModelRouteRepository) withMutationTx(
	ctx context.Context,
	fn func(context.Context, *dbent.Client) error,
) error {
	if existing := dbent.TxFromContext(ctx); existing != nil {
		return fn(ctx, existing.Client())
	}
	tx, err := r.client.Tx(ctx)
	if errors.Is(err, dbent.ErrTxStarted) {
		return fn(ctx, r.client)
	}
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txCtx := dbent.NewTxContext(ctx, tx)
	if err := fn(txCtx, tx.Client()); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *compositeModelRouteRepository) ListByGroup(ctx context.Context, groupID int64, includeDisabled bool) ([]service.CompositeModelRoute, error) {
	q := clientFromContext(ctx, r.client).CompositeModelRoute.Query().
		Where(compositemodelroute.GroupIDEQ(groupID)).
		Order(
			dbent.Asc(compositemodelroute.FieldPriority),
			dbent.Asc(compositemodelroute.FieldID),
		)
	if !includeDisabled {
		q = q.Where(compositemodelroute.EnabledEQ(true))
	}
	rows, err := q.All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]service.CompositeModelRoute, 0, len(rows))
	for _, row := range rows {
		out = append(out, *compositeModelRouteEntityToService(row))
	}
	return out, nil
}

func (r *compositeModelRouteRepository) Create(ctx context.Context, route *service.CompositeModelRoute) error {
	if route == nil {
		return service.ErrCompositeRouteNotFound
	}
	return r.withMutationTx(ctx, func(txCtx context.Context, client *dbent.Client) error {
		created, err := client.CompositeModelRoute.Create().
			SetGroupID(route.GroupID).
			SetPublicModel(route.PublicModel).
			SetMatchType(route.MatchType).
			SetTargetPlatform(route.TargetPlatform).
			SetUpstreamModel(route.UpstreamModel).
			SetEndpoint(route.Endpoint).
			SetPriority(route.Priority).
			SetEnabled(route.Enabled).
			SetNotes(route.Notes).
			Save(txCtx)
		if err != nil {
			return translatePersistenceError(err, nil, service.ErrCompositeRouteExists)
		}
		groupID := created.GroupID
		if err := enqueueSchedulerOutbox(txCtx, client, service.SchedulerOutboxEventGroupChanged, nil, &groupID, nil); err != nil {
			return err
		}
		*route = *compositeModelRouteEntityToService(created)
		return nil
	})
}

func (r *compositeModelRouteRepository) Update(ctx context.Context, route *service.CompositeModelRoute) error {
	if route == nil {
		return service.ErrCompositeRouteNotFound
	}
	return r.withMutationTx(ctx, func(txCtx context.Context, client *dbent.Client) error {
		updated, err := client.CompositeModelRoute.UpdateOneID(route.ID).
			SetPublicModel(route.PublicModel).
			SetMatchType(route.MatchType).
			SetTargetPlatform(route.TargetPlatform).
			SetUpstreamModel(route.UpstreamModel).
			SetEndpoint(route.Endpoint).
			SetPriority(route.Priority).
			SetEnabled(route.Enabled).
			SetNotes(route.Notes).
			Save(txCtx)
		if err != nil {
			return translatePersistenceError(err, service.ErrCompositeRouteNotFound, service.ErrCompositeRouteExists)
		}
		groupID := updated.GroupID
		if err := enqueueSchedulerOutbox(txCtx, client, service.SchedulerOutboxEventGroupChanged, nil, &groupID, nil); err != nil {
			return err
		}
		*route = *compositeModelRouteEntityToService(updated)
		return nil
	})
}

func (r *compositeModelRouteRepository) Delete(ctx context.Context, id int64) error {
	return r.withMutationTx(ctx, func(txCtx context.Context, client *dbent.Client) error {
		row, err := client.CompositeModelRoute.Query().
			Where(compositemodelroute.IDEQ(id)).
			Select(compositemodelroute.FieldGroupID).
			Only(txCtx)
		if err != nil {
			return translatePersistenceError(err, service.ErrCompositeRouteNotFound, nil)
		}
		if err := client.CompositeModelRoute.DeleteOneID(id).Exec(txCtx); err != nil {
			return translatePersistenceError(err, service.ErrCompositeRouteNotFound, nil)
		}
		groupID := row.GroupID
		return enqueueSchedulerOutbox(txCtx, client, service.SchedulerOutboxEventGroupChanged, nil, &groupID, nil)
	})
}

func (r *compositeModelRouteRepository) DeleteByGroup(ctx context.Context, groupID int64) error {
	return r.withMutationTx(ctx, func(txCtx context.Context, client *dbent.Client) error {
		deleted, err := client.CompositeModelRoute.Delete().
			Where(compositemodelroute.GroupIDEQ(groupID)).
			Exec(txCtx)
		if err != nil {
			return err
		}
		if deleted == 0 {
			return nil
		}
		return enqueueSchedulerOutbox(txCtx, client, service.SchedulerOutboxEventGroupChanged, nil, &groupID, nil)
	})
}

func compositeModelRouteEntityToService(row *dbent.CompositeModelRoute) *service.CompositeModelRoute {
	if row == nil {
		return nil
	}
	return &service.CompositeModelRoute{
		ID:             row.ID,
		GroupID:        row.GroupID,
		PublicModel:    row.PublicModel,
		MatchType:      row.MatchType,
		TargetPlatform: row.TargetPlatform,
		UpstreamModel:  row.UpstreamModel,
		Endpoint:       row.Endpoint,
		Priority:       row.Priority,
		Enabled:        row.Enabled,
		Notes:          derefString(row.Notes),
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
	}
}
