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

// AccountAccessGrant grants a fixed access level to exactly one user or role.
type AccountAccessGrant struct {
	ent.Schema
}

func (AccountAccessGrant) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "account_access_grants"},
		entsql.Checks(map[string]string{
			"account_access_grants_grantee_exactly_one_check": "(CASE WHEN grantee_user_id IS NULL THEN 0 ELSE 1 END + CASE WHEN grantee_role_id IS NULL THEN 0 ELSE 1 END) = 1",
			"account_access_grants_grantor_exactly_one_check": "(CASE WHEN granted_by_user_id IS NULL THEN 0 ELSE 1 END + CASE WHEN granted_by_service_principal_id IS NULL THEN 0 ELSE 1 END) = 1",
			"account_access_grants_access_level_check":        "access_level IN ('viewer', 'consumer', 'maintainer', 'manager')",
		}),
	}
}

func (AccountAccessGrant) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
	}
}

func (AccountAccessGrant) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("account_id"),
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

func (AccountAccessGrant) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("account", Account.Type).
			Field("account_id").
			Unique().
			Required().
			StorageKey(edge.Column("account_id"), edge.Symbol("account_access_grants_account_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("grantee_user", User.Type).
			Field("grantee_user_id").
			Unique().
			StorageKey(edge.Column("grantee_user_id"), edge.Symbol("account_access_grants_grantee_user_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("grantee_role", Role.Type).
			Field("grantee_role_id").
			Unique().
			StorageKey(edge.Column("grantee_role_id"), edge.Symbol("account_access_grants_grantee_role_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("granted_by_user", User.Type).
			Field("granted_by_user_id").
			Unique().
			StorageKey(edge.Column("granted_by_user_id"), edge.Symbol("account_access_grants_granted_by_user_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Restrict)),
		edge.To("granted_by_service_principal", ServicePrincipal.Type).
			Field("granted_by_service_principal_id").
			Unique().
			StorageKey(edge.Column("granted_by_service_principal_id"), edge.Symbol("account_access_grants_granted_by_service_principal_id_fkey")).
			Annotations(entsql.OnDelete(entsql.Restrict)),
	}
}

func (AccountAccessGrant) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("account_id", "grantee_user_id").
			Unique().
			StorageKey("account_access_grants_account_user_key").
			Annotations(entsql.IndexWhere("grantee_user_id IS NOT NULL")),
		index.Fields("account_id", "grantee_role_id").
			Unique().
			StorageKey("account_access_grants_account_role_key").
			Annotations(entsql.IndexWhere("grantee_role_id IS NOT NULL")),
		index.Fields("grantee_user_id", "account_id").
			StorageKey("idx_account_access_grants_grantee_user").
			Annotations(entsql.IndexWhere("grantee_user_id IS NOT NULL")),
		index.Fields("grantee_role_id", "account_id").
			StorageKey("idx_account_access_grants_grantee_role").
			Annotations(entsql.IndexWhere("grantee_role_id IS NOT NULL")),
		index.Fields("expires_at", "id").
			StorageKey("idx_account_access_grants_expires_at").
			Annotations(entsql.IndexWhere("expires_at IS NOT NULL")),
	}
}
