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

// RolePermission is the edge schema for the roles-permissions relationship.
type RolePermission struct {
	ent.Schema
}

func (RolePermission) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "role_permissions"},
		field.ID("role_id", "permission_id"),
	}
}

func (RolePermission) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("role_id"),
		field.Int64("permission_id"),
		field.Time("created_at").
			Immutable().
			Default(time.Now).
			SchemaType(map[string]string{dialect.Postgres: "timestamptz"}),
	}
}

func (RolePermission) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("role", Role.Type).
			Field("role_id").
			Unique().
			Required().
			StorageKey(edge.Column("role_id"), edge.Symbol("role_permissions_role_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("permission", Permission.Type).
			Field("permission_id").
			Unique().
			Required().
			StorageKey(edge.Column("permission_id"), edge.Symbol("role_permissions_permission_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}
