package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/shopspring/decimal"
)

// Holding holds the schema definition for the Holding entity.
type Holding struct {
	ent.Schema
}

// Fields of the Holding.
func (Holding) Fields() []ent.Field {
	return []ent.Field{
		field.Float("amount").
			GoType(decimal.Decimal{}).
			SchemaType(DecimalSchemaType),
	}
}

// Edges of the Holding.
func (Holding) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("asset", Asset.Type).
			Unique(),
		edge.From("portfolio", Portfolio.Type).
			Ref("holdings").
			Unique(),
		edge.From("account", Account.Type).
			Ref("holdings").
			Unique(),
	}
}
