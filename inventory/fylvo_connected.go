package inventory

import (
	"context"
	"fmt"
	"strings"
	"time"

	entsqlschema "entgo.io/ent/dialect/sql/schema"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/connectedstorage"
	"github.com/cloudreve/Cloudreve/v4/ent/group"
	entmigrate "github.com/cloudreve/Cloudreve/v4/ent/migrate"
	"github.com/cloudreve/Cloudreve/v4/ent/schema"
	"github.com/cloudreve/Cloudreve/v4/ent/setting"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
)

// Fylvo connected storages, see pkg/connected.

const (
	ConnectedStatusOK        = "ok"
	ConnectedStatusReconnect = "reconnect"

	// ConnectedSettingPrefix starts the names of the settings of connected storages: the
	// OAuth clients of the installation at the providers. The generic settings API neither
	// returns nor writes them, see IsProtectedSetting.
	ConnectedSettingPrefix = "fylvo_connected_"

	// connectedGroupsSetting marks that existing groups got the permission once.
	connectedGroupsSetting = ConnectedSettingPrefix + "groups_set"
)

// ConnectedProviders are the providers a storage can be connected from.
var ConnectedProviders = []string{"drive", "onedrive"}

// ConnectedClientIDSetting names the setting with the client id of a provider.
func ConnectedClientIDSetting(provider string) string {
	return ConnectedSettingPrefix + provider + "_client_id"
}

// ConnectedClientSecretSetting names the setting with the sealed client secret of a provider.
func ConnectedClientSecretSetting(provider string) string {
	return ConnectedSettingPrefix + provider + "_client_secret"
}

// IsProtectedSetting tells whether a setting is kept away from the generic settings API of
// the admin area: it is neither returned nor written there.
func IsProtectedSetting(name string) bool {
	name = strings.ToLower(name)
	if _, ok := RedactedSettings[name]; ok {
		return true
	}
	return strings.HasPrefix(name, ConnectedSettingPrefix)
}

type (
	ConnectedStorageClient interface {
		TxOperator
		// Create stores a new connection with generation 1.
		Create(ctx context.Context, args *CreateConnectedStorageArgs) (*ent.ConnectedStorage, error)
		// Get returns the connection of a user by its public id. A connection of another
		// user is not found.
		Get(ctx context.Context, userID int, publicID string) (*ent.ConnectedStorage, error)
		// ListByUser returns the connections of a user, oldest first.
		ListByUser(ctx context.Context, userID int) ([]*ent.ConnectedStorage, error)
		// Rename changes the name shown to the user.
		Rename(ctx context.Context, id int, name string) (*ent.ConnectedStorage, error)
		// Renew stores a new token after the account signed in again and raises the
		// generation. It reports false when the row no longer has the given generation.
		Renew(ctx context.Context, id, generation int, args *RenewConnectedStorageArgs) (*ent.ConnectedStorage, bool, error)
		// UpdateSecret stores a refreshed token. It reports false, and changes nothing,
		// when the row no longer has the given generation.
		UpdateSecret(ctx context.Context, id, generation int, secret []byte) (bool, error)
		// UpdateState stores status and props under the same condition.
		UpdateState(ctx context.Context, id, generation int, status, message string, props *types.ConnectedStorageProps) (bool, error)
		// Touch notes that the connection was used.
		Touch(ctx context.Context, id int) error
		// Delete removes a connection for good.
		Delete(ctx context.Context, id int) error
		// DeleteByUser removes all connections of a user and returns what they were.
		DeleteByUser(ctx context.Context, userID int) ([]*ent.ConnectedStorage, error)
		// CountByAccount counts the connections to one account of a provider, over all users.
		CountByAccount(ctx context.Context, provider, accountKey string) (int, error)
	}

	CreateConnectedStorageArgs struct {
		UserID     int
		PublicID   string
		Provider   string
		Name       string
		Account    string
		AccountKey string
		Secret     []byte
		Props      *types.ConnectedStorageProps
	}

	RenewConnectedStorageArgs struct {
		Account string
		Secret  []byte
		Props   *types.ConnectedStorageProps
	}
)

func NewConnectedStorageClient(client *ent.Client) ConnectedStorageClient {
	return &connectedStorageClient{client: client}
}

type connectedStorageClient struct {
	client *ent.Client
}

func (c *connectedStorageClient) SetClient(newClient *ent.Client) TxOperator {
	return &connectedStorageClient{client: newClient}
}

func (c *connectedStorageClient) GetClient() *ent.Client {
	return c.client
}

func (c *connectedStorageClient) Create(ctx context.Context, args *CreateConnectedStorageArgs) (*ent.ConnectedStorage, error) {
	return c.client.ConnectedStorage.Create().
		SetUserID(args.UserID).
		SetPublicID(args.PublicID).
		SetProvider(args.Provider).
		SetName(args.Name).
		SetAccount(args.Account).
		SetAccountKey(args.AccountKey).
		SetSecret(args.Secret).
		SetProps(args.Props).
		SetStatus(ConnectedStatusOK).
		Save(ctx)
}

func (c *connectedStorageClient) Get(ctx context.Context, userID int, publicID string) (*ent.ConnectedStorage, error) {
	return c.client.ConnectedStorage.Query().
		Where(connectedstorage.UserID(userID), connectedstorage.PublicID(publicID)).
		First(ctx)
}

func (c *connectedStorageClient) ListByUser(ctx context.Context, userID int) ([]*ent.ConnectedStorage, error) {
	return c.client.ConnectedStorage.Query().
		Where(connectedstorage.UserID(userID)).
		Order(connectedstorage.ByID()).
		All(ctx)
}

func (c *connectedStorageClient) Rename(ctx context.Context, id int, name string) (*ent.ConnectedStorage, error) {
	return c.client.ConnectedStorage.UpdateOneID(id).SetName(name).Save(ctx)
}

func (c *connectedStorageClient) Renew(ctx context.Context, id, generation int, args *RenewConnectedStorageArgs) (*ent.ConnectedStorage, bool, error) {
	n, err := c.client.ConnectedStorage.Update().
		Where(connectedstorage.ID(id), connectedstorage.Generation(generation)).
		SetSecret(args.Secret).
		SetAccount(args.Account).
		SetProps(args.Props).
		SetGeneration(generation + 1).
		SetStatus(ConnectedStatusOK).
		ClearStatusMessage().
		Save(ctx)
	if err != nil || n == 0 {
		return nil, false, err
	}

	row, err := c.client.ConnectedStorage.Get(ctx, id)
	return row, err == nil, err
}

func (c *connectedStorageClient) UpdateSecret(ctx context.Context, id, generation int, secret []byte) (bool, error) {
	n, err := c.client.ConnectedStorage.Update().
		Where(connectedstorage.ID(id), connectedstorage.Generation(generation)).
		SetSecret(secret).
		Save(ctx)
	return n > 0, err
}

func (c *connectedStorageClient) UpdateState(ctx context.Context, id, generation int, status, message string, props *types.ConnectedStorageProps) (bool, error) {
	update := c.client.ConnectedStorage.Update().
		Where(connectedstorage.ID(id), connectedstorage.Generation(generation)).
		SetStatus(status).
		SetProps(props)
	if message == "" {
		update.ClearStatusMessage()
	} else {
		update.SetStatusMessage(message)
	}

	n, err := update.Save(ctx)
	return n > 0, err
}

func (c *connectedStorageClient) Touch(ctx context.Context, id int) error {
	return c.client.ConnectedStorage.Update().
		Where(connectedstorage.ID(id)).
		SetLastUsedAt(time.Now()).
		Exec(ctx)
}

func (c *connectedStorageClient) Delete(ctx context.Context, id int) error {
	_, err := c.client.ConnectedStorage.Delete().Where(connectedstorage.ID(id)).Exec(ctx)
	return err
}

func (c *connectedStorageClient) DeleteByUser(ctx context.Context, userID int) ([]*ent.ConnectedStorage, error) {
	rows, err := c.ListByUser(ctx, userID)
	if err != nil || len(rows) == 0 {
		return nil, err
	}

	if _, err := c.client.ConnectedStorage.Delete().Where(connectedstorage.UserID(userID)).Exec(ctx); err != nil {
		return nil, err
	}

	return rows, nil
}

func (c *connectedStorageClient) CountByAccount(ctx context.Context, provider, accountKey string) (int, error) {
	return c.client.ConnectedStorage.Query().
		Where(connectedstorage.Provider(provider), connectedstorage.AccountKey(accountKey)).
		Count(ctx)
}

// ensureConnectedStorages creates the table of connected storages when it is missing and,
// once, lets the existing groups use them. It runs on every start, like ensureActivityLog.
// Only this one table and the permission bit are touched.
func ensureConnectedStorages(l logging.Logger, client *ent.Client, ctx context.Context) error {
	if err := entmigrate.Create(ctx, client.Schema, []*entsqlschema.Table{entmigrate.ConnectedStoragesTable}); err != nil {
		return fmt.Errorf("failed to create the table of connected storages: %w", err)
	}

	// The settings of the providers start empty. They exist as rows so they can be updated.
	for _, provider := range ConnectedProviders {
		for _, name := range []string{ConnectedClientIDSetting(provider), ConnectedClientSecretSetting(provider)} {
			n, err := client.Setting.Query().Where(setting.Name(name)).Count(schema.SkipSoftDelete(ctx))
			if err != nil {
				return fmt.Errorf("failed to query setting %q: %w", name, err)
			}
			if n == 0 {
				if err := client.Setting.Create().SetName(name).SetValue("").Exec(ctx); err != nil {
					return fmt.Errorf("failed to add setting %q: %w", name, err)
				}
			}
		}
	}

	// A soft-deleted row still takes the name (unique index): it counts as present.
	n, err := client.Setting.Query().Where(setting.Name(connectedGroupsSetting)).Count(schema.SkipSoftDelete(ctx))
	if err != nil {
		return fmt.Errorf("failed to query the marker of connected storages: %w", err)
	}
	if n > 0 {
		return nil
	}

	groups, err := client.Group.Query().All(ctx)
	if err != nil {
		return fmt.Errorf("failed to list groups: %w", err)
	}
	for _, g := range groups {
		if g.Permissions == nil || g.Permissions.Enabled(int(types.GroupPermissionIsAnonymous)) ||
			g.Permissions.Enabled(int(types.GroupPermissionConnectedStorage)) {
			continue
		}

		permissions := make(boolset.BooleanSet, len(*g.Permissions))
		copy(permissions, *g.Permissions)
		boolset.Set(types.GroupPermissionConnectedStorage, true, &permissions)
		if err := client.Group.Update().Where(group.ID(g.ID)).SetPermissions(&permissions).Exec(ctx); err != nil {
			return fmt.Errorf("failed to update group %d: %w", g.ID, err)
		}
		l.Info("Group %q may use connected storages.", g.Name)
	}

	return client.Setting.Create().SetName(connectedGroupsSetting).SetValue("1").Exec(ctx)
}
