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

// ServicePrincipalRole assigns a platform role to a service principal.
type ServicePrincipalRole struct {
	ent.Schema
}

func (ServicePrincipalRole) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "service_principal_roles"},
	}
}

func (ServicePrincipalRole) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (ServicePrincipalRole) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("service_principal_id"),
		field.Int64("role_id"),
		field.Int64("granted_by_user_id"),
		field.Time("expires_at").
			Optional().
			Nillable().
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (ServicePrincipalRole) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("service_principal", ServicePrincipal.Type).
			Field("service_principal_id").
			Unique().
			Required().
			StorageKey(edge.Column("service_principal_id"), edge.Symbol("service_principal_roles_service_principal_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("role", Role.Type).
			Field("role_id").
			Unique().
			Required().
			StorageKey(edge.Column("role_id"), edge.Symbol("service_principal_roles_role_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("granted_by_user", User.Type).
			Field("granted_by_user_id").
			Unique().
			Required().
			StorageKey(edge.Column("granted_by_user_id"), edge.Symbol("service_principal_roles_granted_by_user_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Restrict)),
	}
}

func (ServicePrincipalRole) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("service_principal_id", "role_id").
			Unique().
			StorageKey("service_principal_roles_principal_role_key"),
		index.Fields("role_id", "service_principal_id").
			StorageKey("idx_service_principal_roles_role_id"),
		index.Fields("expires_at", "id").
			StorageKey("idx_service_principal_roles_expires_at").
			Annotations(entsql.IndexWhere("expires_at IS NOT NULL")),
	}
}
