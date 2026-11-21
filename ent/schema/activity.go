package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
)

// Activity is one entry of the Fylvo activity log: a file change, a share change or a sign-in.
// It has no foreign keys on purpose: entries outlive the files, shares and accounts they name.
type Activity struct {
	ent.Schema
}

// Fields of the Activity.
func (Activity) Fields() []ent.Field {
	return []ent.Field{
		field.Time("created_at").
			Immutable().
			Default(time.Now),
		field.Int("type"),
		// Who did it. Empty for the system and for guests of a share link.
		field.Int("user_id").Optional(),
		// Whose file or account it is about.
		field.Int("owner_id").Optional(),
		field.Int("file_id").Optional(),
		field.Int("parent_id").Optional(),
		// The folder a file left (move, restore).
		field.Int("from_parent_id").Optional(),
		field.Int("share_id").Optional(),
		field.String("ip").Optional(),
		field.String("user_agent").Optional(),
		field.String("via").Optional(),
		field.JSON("content", &types.ActivityContent{}).Optional(),
	}
}

// Indexes of the Activity.
func (Activity) Indexes() []ent.Index {
	return []ent.Index{
		// One column each: SQLite keeps the row ID in every index, so the lists (newest ID
		// first) read them backwards and stop after one page.
		index.Fields("file_id"),
		index.Fields("parent_id"),
		index.Fields("from_parent_id"),
		index.Fields("user_id"),
		index.Fields("owner_id"),
		index.Fields("created_at"),
	}
}
