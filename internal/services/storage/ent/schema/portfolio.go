package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
)

// Portfolio holds the schema definition for the Portfolio entity.
type Portfolio struct {
	ent.Schema
}

// Fields of the Portfolio.
func (Portfolio) Fields() []ent.Field {
	return nil
}

// Edges of the Portfolio.
func (Portfolio) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("owners", User.Type).
			Ref("portfolios"),
		edge.To("holdings", Holding.Type),
		edge.From("tags", Tag.Type).
			Ref("portfolios"),
	}
}
