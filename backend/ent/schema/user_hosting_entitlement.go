package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// UserHostingEntitlement stores private account and group creation quotas.
// The hoster role in user_roles remains the qualification authority.
type UserHostingEntitlement struct {
	ent.Schema
}

func (UserHostingEntitlement) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "user_hosting_entitlements"},
		entsql.Checks(map[string]string{
			"user_hosting_entitlements_account_limit_nonnegative": "account_limit >= 0",
			"user_hosting_entitlements_group_limit_nonnegative":   "group_limit >= 0",
			"user_hosting_entitlements_version_positive":          "version > 0",
		}),
	}
}

func (UserHostingEntitlement) Mixin() []ent.Mixin {
	return []ent.Mixin{mixins.TimeMixin{}}
}

func (UserHostingEntitlement) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("user_id"),
		field.Int64("account_limit").
			Default(0).
			Min(0),
		field.Int64("group_limit").
			Default(0).
			Min(0),
		field.Int64("version").
			Default(1).
			Min(1),
		field.Int64("created_by_user_id"),
		field.Int64("updated_by_user_id"),
	}
}

func (UserHostingEntitlement) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("hosting_entitlement").
			Field("user_id").
			Unique().
			Required(),
		edge.From("created_by", User.Type).
			Ref("created_hosting_entitlements").
			Field("created_by_user_id").
			Unique().
			Required(),
		edge.From("updated_by", User.Type).
			Ref("updated_hosting_entitlements").
			Field("updated_by_user_id").
			Unique().
			Required(),
	}
}
