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

// GroupAccessGrant grants a fixed access level to exactly one user or role.
type GroupAccessGrant struct {
	ent.Schema
}

func (GroupAccessGrant) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "group_access_grants"},
		entsql.Checks(map[string]string{
			"group_access_grants_grantee_exactly_one_check": "(CASE WHEN grantee_user_id IS NULL THEN 0 ELSE 1 END + CASE WHEN grantee_role_id IS NULL THEN 0 ELSE 1 END) = 1",
			"group_access_grants_grantor_exactly_one_check": "(CASE WHEN granted_by_user_id IS NULL THEN 0 ELSE 1 END + CASE WHEN granted_by_service_principal_id IS NULL THEN 0 ELSE 1 END) = 1",
			"group_access_grants_access_level_check":        "access_level IN ('viewer', 'consumer', 'maintainer', 'manager')",
		}),
	}
}

func (GroupAccessGrant) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (GroupAccessGrant) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("group_id"),
		field.Int64("grantee_user_id").
			Optional().
			Nillable(),
		field.Int64("grantee_role_id").
			Optional().
			Nillable(),
		field.String("access_level").
			MaxLen(20).
			NotEmpty(),
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

func (GroupAccessGrant) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("group", Group.Type).
			Field("group_id").
			Unique().
			Required().
			StorageKey(edge.Column("group_id"), edge.Symbol("group_access_grants_group_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("grantee_user", User.Type).
			Field("grantee_user_id").
			Unique().
			StorageKey(edge.Column("grantee_user_id"), edge.Symbol("group_access_grants_grantee_user_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("grantee_role", Role.Type).
			Field("grantee_role_id").
			Unique().
			StorageKey(edge.Column("grantee_role_id"), edge.Symbol("group_access_grants_grantee_role_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("granted_by_user", User.Type).
			Field("granted_by_user_id").
			Unique().
			StorageKey(edge.Column("granted_by_user_id"), edge.Symbol("group_access_grants_granted_by_user_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("granted_by_service_principal", ServicePrincipal.Type).
			Field("granted_by_service_principal_id").
			Unique().
			StorageKey(edge.Column("granted_by_service_principal_id"), edge.Symbol("group_access_grants_granted_by_service_principal_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Restrict)),
	}
}

func (GroupAccessGrant) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("group_id", "grantee_user_id").
			Unique().
			StorageKey("group_access_grants_group_user_key").
			Annotations(entsql.IndexWhere("grantee_user_id IS NOT NULL")),
		index.Fields("group_id", "grantee_role_id").
			Unique().
			StorageKey("group_access_grants_group_role_key").
			Annotations(entsql.IndexWhere("grantee_role_id IS NOT NULL")),
		index.Fields("grantee_user_id", "group_id").
			StorageKey("idx_group_access_grants_grantee_user").
			Annotations(entsql.IndexWhere("grantee_user_id IS NOT NULL")),
		index.Fields("grantee_role_id", "group_id").
			StorageKey("idx_group_access_grants_grantee_role").
			Annotations(entsql.IndexWhere("grantee_role_id IS NOT NULL")),
		index.Fields("expires_at", "id").
			StorageKey("idx_group_access_grants_expires_at").
			Annotations(entsql.IndexWhere("expires_at IS NOT NULL")),
	}
}
