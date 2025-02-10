package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
)

// Asset holds the schema definition for the Asset entity.
type Asset struct {
	ent.Schema
}

// Fields of the Asset.
func (Asset) Fields() []ent.Field {
	return nil
}

// Edges of the Asset.
func (Asset) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("holdings", Holding.Type).
			Ref("asset"),
		edge.From("tags", Tag.Type).
			Ref("assets"),
	}
}
