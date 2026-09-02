package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// ServicePrincipalWorkerPermission is a migration-owned, mode-independent
// capability for a built-in worker. It is not a user-manageable RBAC grant.
type ServicePrincipalWorkerPermission struct {
	ent.Schema
}

func (ServicePrincipalWorkerPermission) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "service_principal_worker_permissions"},
		field.ID("service_principal_id", "permission_id"),
	}
}

func (ServicePrincipalWorkerPermission) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("service_principal_id"),
		field.Int64("permission_id"),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (ServicePrincipalWorkerPermission) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("service_principal", ServicePrincipal.Type).
			Field("service_principal_id").
			Unique().
			Required().
			StorageKey(edge.Column("service_principal_id"), edge.Symbol("sp_worker_permissions_principal_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("permission", Permission.Type).
			Field("permission_id").
			Unique().
			Required().
			StorageKey(edge.Column("permission_id"), edge.Symbol("sp_worker_permissions_permission_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}
