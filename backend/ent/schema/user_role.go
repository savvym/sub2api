package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// UserRole assigns one platform role to a user with auditable provenance.
type UserRole struct {
	ent.Schema
}

func (UserRole) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "user_roles"},
		entsql.Checks(map[string]string{
			"user_roles_grantor_exactly_one_check": "(CASE WHEN granted_by_user_id IS NULL THEN 0 ELSE 1 END + CASE WHEN granted_by_service_principal_id IS NULL THEN 0 ELSE 1 END) = 1",
		}),
	}
}

func (UserRole) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (UserRole) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_id"),
		field.Int64("role_id"),
		field.Int64("granted_by_user_id").
			Optional().
			Nillable(),
		field.Int64("granted_by_service_principal_id").
			Optional().
			Nillable(),
		field.Time("expires_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (UserRole) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("user", User.Type).
			Field("user_id").
			Unique().
			Required().
			StorageKey(edge.Column("user_id"), edge.Symbol("user_roles_user_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("role", Role.Type).
			Field("role_id").
			Unique().
			Required().
			StorageKey(edge.Column("role_id"), edge.Symbol("user_roles_role_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("granted_by_user", User.Type).
			Field("granted_by_user_id").
			Unique().
			StorageKey(edge.Column("granted_by_user_id"), edge.Symbol("user_roles_granted_by_user_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("granted_by_service_principal", ServicePrincipal.Type).
			Field("granted_by_service_principal_id").
			Unique().
			StorageKey(edge.Column("granted_by_service_principal_id"), edge.Symbol("user_roles_granted_by_service_principal_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Restrict)),
	}
}

func (UserRole) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user_id", "role_id").
			Unique().
			StorageKey("user_roles_user_role_key"),
		index.Fields("role_id", "user_id").
			StorageKey("idx_user_roles_role_id"),
		index.Fields("expires_at", "id").
			StorageKey("idx_user_roles_expires_at").
			Annotations(entsql.IndexWhere("expires_at IS NOT NULL")),
	}
}
