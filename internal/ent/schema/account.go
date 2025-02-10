package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
)

// Account holds the schema definition for the Account entity.
type Account struct {
	ent.Schema
}

// Fields of the Account.
func (Account) Fields() []ent.Field {
	return nil
}

// Edges of the Account.
func (Account) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("owner", User.Type).
			Ref("accounts").
			Unique(),
		edge.To("holdings", Holding.Type),
	}
}
