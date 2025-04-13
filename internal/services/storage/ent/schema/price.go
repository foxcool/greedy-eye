package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/shopspring/decimal"
)

// Price holds the schema definition for the Price entity.
type Price struct {
	ent.Schema
}

// DecimalSchemaType is the schema type for prices and amounts.
var DecimalSchemaType = map[string]string{
	// 38 before the decimal point and 18 after the decimal point.
	dialect.MySQL:    "decimal(38,18)",
	dialect.Postgres: "numeric(38,18)",
}

// Fields of the Price.
func (Price) Fields() []ent.Field {
	return []ent.Field{
		field.String("source"),
		field.Float("last_price").
			GoType(decimal.Decimal{}).
			SchemaType(DecimalSchemaType),
		field.Float("ask").
			GoType(decimal.Decimal{}).
			SchemaType(DecimalSchemaType),
		field.Float("bid").
			GoType(decimal.Decimal{}).
			SchemaType(DecimalSchemaType),
		field.Time("time"),
	}
}

// Edges of the Price.
func (Price) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("base_asset", Asset.Type).Unique(),
		edge.To("quote_asset", Asset.Type).Unique(),
	}
}
