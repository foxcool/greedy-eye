package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
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
		field.Time("created_at").Immutable().Default(time.Now()),
		field.Time("updated_at").Default(time.Now()).UpdateDefault(time.Now),
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
	}
}

// Edges of the Asset.
func (Asset) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("holdings", Holding.Type),
		edge.To("prices", Price.Type),
		edge.To("prices_base", Price.Type),
		edge.To("transactions", Transaction.Type),
	}
}

// Indexes of the Asset.
func (Asset) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("tags").
			Annotations(
				entsql.IndexTypes(map[string]string{
					dialect.Postgres: "GIN",
				}),
			),
	}
}
