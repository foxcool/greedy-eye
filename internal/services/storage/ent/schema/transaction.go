package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Transaction holds the schema definition for the Transaction entity.
type Transaction struct {
	ent.Schema
}

// Fields of the Transaction.
func (Transaction) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("uuid", uuid.UUID{}).
			Default(uuid.New),
		field.Int64("amount"),
		field.Int64("fee"),
		field.Uint32("precision"),
		field.Enum("type").Values("unspecified", "buy", "sell", "transfer", "deposit", "withdrawal"),
		field.Enum("status").Values("unspecified", "pending", "completed", "failed", "cancelled"),
		field.Time("created_at").Default(time.Now),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		field.JSON("metadata", map[string]string{}).Default(map[string]string{}),
	}
}

// Edges of the Transaction.
func (Transaction) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("portfolio", Portfolio.Type),
		edge.To("account", Account.Type),
		edge.To("asset", Asset.Type).Required(),
		edge.To("fee_asset", Asset.Type).Required(),
	}
}
