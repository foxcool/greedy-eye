package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Portfolio holds the schema definition for the Portfolio entity.
type Portfolio struct {
	ent.Schema
}

// Fields of the Portfolio.
func (Portfolio) Fields() []ent.Field {
	fields := []ent.Field{
		field.UUID("uuid", uuid.UUID{}).
			Default(uuid.New),
		field.Time("created_at").Immutable().Default(time.Now),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
		field.Int("user_id"),
		field.String("name"),
		field.String("description").Optional(),
		field.JSON("data", map[string]any{}).Default(map[string]any{}),
	}
	return fields
}

// Edges of the Portfolio.
func (Portfolio) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("portfolios").
			Field("user_id").
			Unique().
			Required(),
		edge.To("holdings", Holding.Type),
	}
}
