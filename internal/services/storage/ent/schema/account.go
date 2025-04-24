package schema

import (
	"time"

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
		field.Time("created_at").Immutable().Default(time.Now),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		field.Int("user_id"),
		field.String("name"),
		field.String("description").Optional(),
		field.Enum("type").Values("unspecified", "wallet", "exchange", "bank", "broker"),
		field.JSON("data", map[string]string{}).Default(map[string]string{}),
	}
}

// Edges of the Account.
func (Account) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("accounts").
			Field("user_id").
			Unique().
			Required(),
		edge.To("holdings", Holding.Type),
		edge.To("transactions", Transaction.Type),
	}
}
