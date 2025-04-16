package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Price holds the schema definition for the Price entity.
type Price struct {
	ent.Schema
}

// Fields of the Price.
func (Price) Fields() []ent.Field {
	fields := []ent.Field{
		field.UUID("uuid", uuid.UUID{}).
			Default(uuid.New),
		field.String("source_id").Unique(),
		field.String("interval"),
		field.Int64("amount"),
		field.Uint32("precision"),
		field.Int64("open").Optional(),
		field.Int64("high").Optional(),
		field.Int64("low").Optional(),
		field.Int64("close").Optional(),
		field.Int64("volume").Optional(),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
	return fields
}

// Edges of the Price.
func (Price) Edges() []ent.Edge {
	edges := []ent.Edge{
		edge.To("asset", Asset.Type),
		edge.To("base_asset", Asset.Type),
	}
	return edges
}
