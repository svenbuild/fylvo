package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
)

// ConnectedStorage is a storage at another provider that an account connected to Fylvo
// (see pkg/connected). It has no soft delete on purpose: a disconnected storage must leave
// no token behind. It has no foreign keys; the rows of an account are removed with it.
type ConnectedStorage struct {
	ent.Schema
}

// Fields of the ConnectedStorage.
func (ConnectedStorage) Fields() []ent.Field {
	return []ent.Field{
		field.Time("created_at").
			Immutable().
			Default(time.Now),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
		field.Int("user_id"),
		// The id in addresses, routes and the sidebar. Random, never reused.
		field.String("public_id").
			Immutable(),
		field.String("provider").
			Immutable(),
		field.String("name"),
		// Shown as "connected as".
		field.String("account"),
		// The provider's own, unchanging id of the account and its drive.
		field.String("account_key").
			Immutable(),
		// The token of the provider account, sealed (pkg/connected.Sealer).
		field.Bytes("secret").
			Sensitive(),
		field.JSON("props", &types.ConnectedStorageProps{}).
			Optional(),
		// Raised with every new token. Whatever was issued for an older generation
		// (download grants, upload sessions, page tokens) stops working.
		field.Int("generation").
			Default(1),
		field.String("status").
			Default("ok"),
		field.String("status_message").
			Optional(),
		field.Time("last_used_at").
			Optional().
			Nillable(),
	}
}

// Indexes of the ConnectedStorage.
func (ConnectedStorage) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("public_id").
			Unique(),
		// A provider account is connected to a Fylvo account at most once.
		index.Fields("user_id", "provider", "account_key").
			Unique(),
	}
}
