package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ResourceAuthorizationEvent is the durable, append-only record of an
// authorization-changing command. The SQL migration enforces immutability.
type ResourceAuthorizationEvent struct {
	ent.Schema
}

func (ResourceAuthorizationEvent) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "resource_authorization_events"},
		entsql.Checks(map[string]string{
			"resource_authorization_events_resource_exactly_one_check":  "(CASE WHEN account_id IS NULL THEN 0 ELSE 1 END + CASE WHEN group_id IS NULL THEN 0 ELSE 1 END) = 1",
			"resource_authorization_events_actor_exactly_one_check":     "(CASE WHEN actor_user_id IS NULL THEN 0 ELSE 1 END + CASE WHEN actor_service_principal_id IS NULL THEN 0 ELSE 1 END) = 1",
			"resource_authorization_events_event_type_not_empty_check":  "TRIM(event_type) <> ''",
			"resource_authorization_events_access_version_positive":     "resource_access_version > 0",
			"resource_authorization_events_auth_method_not_empty_check": "TRIM(auth_method) <> ''",
		}),
	}
}

func (ResourceAuthorizationEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("account_id").
			Optional().
			Nillable(),
		field.Int64("group_id").
			Optional().
			Nillable(),
		field.Int64("actor_user_id").
			Optional().
			Nillable(),
		field.Int64("actor_service_principal_id").
			Optional().
			Nillable(),
		field.Int64("resource_owner_user_id").
			Optional().
			Nillable(),
		field.String("event_type").
			MaxLen(64).
			NotEmpty(),
		field.Int64("resource_access_version"),
		field.String("auth_method").
			MaxLen(32).
			NotEmpty().
			Default("unknown"),
		field.String("request_id").
			MaxLen(64).
			Default(""),
		field.JSON("details", map[string]any{}).
			Default(func() map[string]any { return map[string]any{} }).
			SchemaType(map[string]string{dialect.Postgres: "jsonb"}),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (ResourceAuthorizationEvent) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("account", Account.Type).
			Field("account_id").
			Unique().
			StorageKey(edge.Column("account_id"), edge.Symbol("resource_authorization_events_account_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("group", Group.Type).
			Field("group_id").
			Unique().
			StorageKey(edge.Column("group_id"), edge.Symbol("resource_authorization_events_group_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("actor_user", User.Type).
			Field("actor_user_id").
			Unique().
			StorageKey(edge.Column("actor_user_id"), edge.Symbol("resource_authorization_events_actor_user_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("actor_service_principal", ServicePrincipal.Type).
			Field("actor_service_principal_id").
			Unique().
			StorageKey(edge.Column("actor_service_principal_id"), edge.Symbol("resource_authorization_events_actor_service_principal_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("resource_owner", User.Type).
			Field("resource_owner_user_id").
			Unique().
			StorageKey(edge.Column("resource_owner_user_id"), edge.Symbol("resource_authorization_events_resource_owner_user_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Restrict)),
	}
}

func (ResourceAuthorizationEvent) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("account_id", "created_at", "id").
			StorageKey("idx_resource_authorization_events_account_created").
			Annotations(
				entsql.IndexWhere("account_id IS NOT NULL"),
				entsql.DescColumns("created_at", "id"),
			),
		index.Fields("group_id", "created_at", "id").
			StorageKey("idx_resource_authorization_events_group_created").
			Annotations(
				entsql.IndexWhere("group_id IS NOT NULL"),
				entsql.DescColumns("created_at", "id"),
			),
		index.Fields("actor_user_id", "created_at", "id").
			StorageKey("idx_resource_authorization_events_actor_user_created").
			Annotations(
				entsql.IndexWhere("actor_user_id IS NOT NULL"),
				entsql.DescColumns("created_at", "id"),
			),
		index.Fields("actor_service_principal_id", "created_at", "id").
			StorageKey("idx_resource_authorization_events_actor_sp_created").
			Annotations(
				entsql.IndexWhere("actor_service_principal_id IS NOT NULL"),
				entsql.DescColumns("created_at", "id"),
			),
		index.Fields("created_at", "id").
			StorageKey("idx_resource_authorization_events_created_at").
			Annotations(entsql.DescColumns("created_at", "id")),
	}
}
