package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Role groups platform capabilities for users and service principals.
type Role struct {
	ent.Schema
}

func (Role) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "roles"},
		entsql.Checks(map[string]string{
			"roles_authz_version_positive": "authz_version > 0",
		}),
	}
}

func (Role) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (Role) Fields() []ent.Field {
	return []ent.Field{
		field.String("code").
			MaxLen(64).
			NotEmpty().
			Unique(),
		field.String("name").
			MaxLen(100).
			NotEmpty(),
		field.String("description").
			SchemaType(map[string]string{dialect.Postgres: "text"}).
			Default(""),
		field.Bool("is_system").
			Default(false),
		field.Int64("authz_version").
			Default(1),
	}
}

func (Role) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("permissions", Permission.Type).
			Through("role_permissions", RolePermission.Type),
		edge.From("users", User.Type).
			Ref("authorization_roles").
			Through("user_roles", UserRole.Type),
		edge.From("service_principals", ServicePrincipal.Type).
			Ref("roles").
			Through("service_principal_roles", ServicePrincipalRole.Type),
	}
}
