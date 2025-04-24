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
		field.Enum("type").Values("unspecified", "extended", "trade", "transfer", "deposit", "withdrawal"),
		field.Enum("status").Values("unspecified", "pending", "processing", "completed", "failed", "cancelled"),
		field.Int("account_id"),
		field.JSON("data", map[string]string{}).Default(map[string]string{}),
		field.Time("created_at").Immutable().Default(time.Now),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// Edges of the Transaction.
func (Transaction) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("account", Account.Type).
			Ref("transactions").
			Field("account_id").
			Unique().
			Required(),
	}
}
