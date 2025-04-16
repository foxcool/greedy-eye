package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

// Asset holds the schema definition for the Asset entity.
type Asset struct {
	ent.Schema
}

// Fields of the Asset.
func (Asset) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("uuid", uuid.UUID{}).
			Default(uuid.New),
		field.String("symbol"),
		field.String("name"),
		field.Enum("type").Values(
			"unspecified",
			"cryptocurrency",
			"stock",
			"bond",
			"commodity",
			"forex",
			"fund",
		),
		field.Strings("tags"),
		field.Time("created_at").Immutable().Default(time.Now()),
		field.Time("updated_at").Immutable().Default(time.Now()),
	}
}

// Edges of the Asset.
func (Asset) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("holdings", Holding.Type),
	}
}
