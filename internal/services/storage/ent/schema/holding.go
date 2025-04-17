package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Holding holds the schema definition for the Holding entity.
type Holding struct {
	ent.Schema
}

// Fields of the Holding.

func (Holding) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("uuid", uuid.UUID{}).
			Default(uuid.New),
		field.Int("asset_id"),
		field.Int("portfolio_id"),
		field.Int("account_id"),
		field.Int64("amount"),
		field.Uint32("precision"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the Holding.
func (Holding) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("asset", Asset.Type).
			Ref("holdings").
			Field("asset_id").
			Unique().
			Required(),
		edge.From("portfolio", Portfolio.Type).
			Ref("holdings").
			Field("portfolio_id").
			Unique().
			Required(),
		edge.From("account", Account.Type).
			Ref("holdings").
			Field("account_id").
			Unique().
			Required(),
	}
}
