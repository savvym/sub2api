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

// Permission is a stable platform capability code.
type Permission struct {
	ent.Schema
}

func (Permission) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "permissions"},
	}
}

func (Permission) Fields() []ent.Field {
	return []ent.Field{
		field.String("code").
			MaxLen(128).
			NotEmpty().
			Unique(),
		field.String("description").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Default(""),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (Permission) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("roles", Role.Type).
			Ref("permissions").
			Through("role_permissions", RolePermission.Type),
		edge.From("worker_service_principals", ServicePrincipal.Type).
			Ref("worker_permissions").
			Through("worker_permission_grants", ServicePrincipalWorkerPermission.Type),
	}
}
