package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Account holds the schema definition for the Account entity.
type Account struct {
	ent.Schema
}

// Fields of the Account.
func (Account) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("uuid", uuid.UUID{}).
			Default(uuid.New),
		field.String("name"),
		field.String("description").Optional(),
		field.Enum("type").Values("unspecified", "wallet", "exchange", "bank", "broker"),
		field.JSON("data", map[string]string{}),
		field.Time("created_at"),
		field.Time("updated_at"),
	}
}

// Edges of the Account.
func (Account) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("accounts").
			Unique(),
		edge.To("holdings", Holding.Type).Required(),
	}
}
