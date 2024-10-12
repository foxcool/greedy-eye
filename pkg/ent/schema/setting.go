package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// Setting holds the schema definition for the Setting entity.
type Setting struct {
	ent.Schema
}

// Fields of the Setting.
func (Setting) Fields() []ent.Field {
	return []ent.Field{
		// Setting or external service name.
		field.String("name"),
		// Setting value.
		field.String("value").Sensitive(),
		// External service ID or wallet etc.
		field.String("external_id"),
	}
}

// Edges of the Setting.
func (Setting) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("user", User.Type).
			Ref("settings").
			Unique(),
	}
}
