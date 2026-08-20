package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// ServicePrincipal is a non-human actor used by trusted platform automation.
type ServicePrincipal struct {
	ent.Schema
}

func (ServicePrincipal) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "service_principals"},
		entsql.Checks(map[string]string{
			"service_principals_status_check":           "status IN ('active', 'disabled')",
			"service_principals_authz_version_positive": "authz_version > 0",
		}),
	}
}

func (ServicePrincipal) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (ServicePrincipal) Fields() []ent.Field {
	return []ent.Field{
		field.String("code").
			MaxLen(64).
			NotEmpty().
			Unique(),
		field.String("name").
			MaxLen(100).
			NotEmpty(),
		field.String("status").
			MaxLen(20).
			Default("active"),
		field.Int64("authz_version").
			Default(1),
	}
}

func (ServicePrincipal) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("roles", Role.Type).
			Through("service_principal_roles", ServicePrincipalRole.Type),
	}
}
