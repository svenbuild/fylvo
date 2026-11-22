package inventory

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/group"
	"github.com/cloudreve/Cloudreve/v4/ent/schema"
	"github.com/cloudreve/Cloudreve/v4/ent/setting"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/stretchr/testify/require"
)

func connectedTestDB(t *testing.T) (*ent.Client, cache.Driver, logging.Logger) {
	logger := logging.NewConsoleLogger(logging.LevelError)
	kv := cache.NewMemoStore("", logger)
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	t.Cleanup(func() { _ = client.Close() })
	client, err := InitializeDBClient(logger, client, kv, "4.19.1")
	require.NoError(t, err)
	return client, kv, logger
}

func mayConnect(t *testing.T, client *ent.Client, id int) bool {
	g, err := client.Group.Query().Where(group.ID(id)).First(context.Background())
	require.NoError(t, err)
	return g.Permissions.Enabled(int(types.GroupPermissionConnectedStorage))
}

// An installation from before connected storages: the table comes on the next start, and
// the groups that exist may use it. Later choices of the operator are kept.
func TestConnectedStoragesComeToExistingInstallations(t *testing.T) {
	client, kv, logger := connectedTestDB(t)
	ctx := context.Background()

	groups, err := client.Group.Query().All(ctx)
	require.NoError(t, err)
	var anonymous, member int
	for _, g := range groups {
		if g.Permissions.Enabled(int(types.GroupPermissionIsAnonymous)) {
			anonymous = g.ID
		} else if !g.Permissions.Enabled(int(types.GroupPermissionIsAdmin)) {
			member = g.ID
		}
	}
	require.NotZero(t, anonymous)
	require.NotZero(t, member)

	// As it was before.
	_, err = client.ExecContext(ctx, "DROP TABLE connected_storages")
	require.NoError(t, err)
	_, err = client.Setting.Delete().Where(setting.Name(connectedGroupsSetting)).Exec(schema.SkipSoftDelete(ctx))
	require.NoError(t, err)
	for _, g := range groups {
		permissions := make(boolset.BooleanSet, len(*g.Permissions))
		copy(permissions, *g.Permissions)
		boolset.Set(types.GroupPermissionConnectedStorage, false, &permissions)
		require.NoError(t, client.Group.Update().Where(group.ID(g.ID)).SetPermissions(&permissions).Exec(ctx))
	}
	files, err := client.File.Query().Count(ctx)
	require.NoError(t, err)

	client, err = InitializeDBClient(logger, client, kv, "4.19.1")
	require.NoError(t, err)
	_, err = client.ConnectedStorage.Query().Count(ctx)
	require.NoError(t, err, "the table is there")
	require.True(t, mayConnect(t, client, member))
	require.False(t, mayConnect(t, client, anonymous), "guests never connect storages")
	after, err := client.File.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, files, after, "other tables are not touched")

	// The operator takes the permission away: the next start leaves it away.
	g, err := client.Group.Query().Where(group.ID(member)).First(ctx)
	require.NoError(t, err)
	permissions := make(boolset.BooleanSet, len(*g.Permissions))
	copy(permissions, *g.Permissions)
	boolset.Set(types.GroupPermissionConnectedStorage, false, &permissions)
	require.NoError(t, client.Group.Update().Where(group.ID(member)).SetPermissions(&permissions).Exec(ctx))
	client, err = InitializeDBClient(logger, client, kv, "4.19.1")
	require.NoError(t, err)
	require.False(t, mayConnect(t, client, member))
}

func TestConnectedStorageClient(t *testing.T) {
	db, _, _ := connectedTestDB(t)
	ctx := context.Background()
	c := NewConnectedStorageClient(db)

	row, err := c.Create(ctx, &CreateConnectedStorageArgs{
		UserID: 7, PublicID: "aaaaaaaaaaaaaaaa", Provider: "drive", Name: "Google Drive",
		Account: "user@example.com", AccountKey: "acct-1", Secret: []byte("sealed-1"),
		Props: &types.ConnectedStorageProps{Scopes: "drive"},
	})
	require.NoError(t, err)
	require.Equal(t, 1, row.Generation)
	require.Equal(t, ConnectedStatusOK, row.Status)

	// The same provider account a second time for the same user is refused.
	_, err = c.Create(ctx, &CreateConnectedStorageArgs{
		UserID: 7, PublicID: "bbbbbbbbbbbbbbbb", Provider: "drive", Name: "Again",
		Account: "user@example.com", AccountKey: "acct-1", Secret: []byte("x"),
	})
	require.Error(t, err)
	// Another user may connect the same provider account.
	other, err := c.Create(ctx, &CreateConnectedStorageArgs{
		UserID: 8, PublicID: "cccccccccccccccc", Provider: "drive", Name: "Shared account",
		Account: "user@example.com", AccountKey: "acct-1", Secret: []byte("sealed-other"),
	})
	require.NoError(t, err)
	n, err := c.CountByAccount(ctx, "drive", "acct-1")
	require.NoError(t, err)
	require.Equal(t, 2, n)

	// A connection is found only by its owner.
	_, err = c.Get(ctx, 8, "aaaaaaaaaaaaaaaa")
	require.True(t, ent.IsNotFound(err))
	got, err := c.Get(ctx, 7, "aaaaaaaaaaaaaaaa")
	require.NoError(t, err)
	require.Equal(t, row.ID, got.ID)
	require.Equal(t, []byte("sealed-1"), got.Secret)

	// A refreshed token is stored for the generation it belongs to, and only for that one.
	ok, err := c.UpdateSecret(ctx, row.ID, 1, []byte("sealed-2"))
	require.NoError(t, err)
	require.True(t, ok)

	renewed, ok, err := c.Renew(ctx, row.ID, 1, &RenewConnectedStorageArgs{Account: "user@example.com", Secret: []byte("sealed-3")})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 2, renewed.Generation)

	ok, err = c.UpdateSecret(ctx, row.ID, 1, []byte("late-refresh"))
	require.NoError(t, err)
	require.False(t, ok, "a late refresh of the old generation must not overwrite the new token")
	_, ok, err = c.Renew(ctx, row.ID, 1, &RenewConnectedStorageArgs{Secret: []byte("second-renew")})
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = c.UpdateState(ctx, row.ID, 1, ConnectedStatusReconnect, "late", nil)
	require.NoError(t, err)
	require.False(t, ok)
	got, err = c.Get(ctx, 7, "aaaaaaaaaaaaaaaa")
	require.NoError(t, err)
	require.Equal(t, []byte("sealed-3"), got.Secret)
	require.Equal(t, ConnectedStatusOK, got.Status)

	ok, err = c.UpdateState(ctx, row.ID, 2, ConnectedStatusReconnect, "refused", &types.ConnectedStorageProps{StatusFailures: 2})
	require.NoError(t, err)
	require.True(t, ok)
	got, _ = c.Get(ctx, 7, "aaaaaaaaaaaaaaaa")
	require.Equal(t, ConnectedStatusReconnect, got.Status)
	require.Equal(t, "refused", got.StatusMessage)

	renamed, err := c.Rename(ctx, row.ID, "Photos")
	require.NoError(t, err)
	require.Equal(t, "Photos", renamed.Name)
	require.NoError(t, c.Touch(ctx, row.ID))

	// Deleting removes the row for good, token included.
	require.NoError(t, c.Delete(ctx, row.ID))
	_, err = c.Get(ctx, 7, "aaaaaaaaaaaaaaaa")
	require.True(t, ent.IsNotFound(err))
	var left int
	rows, err := db.QueryContext(ctx, "SELECT COUNT(*) FROM connected_storages WHERE user_id = 7")
	require.NoError(t, err)
	require.True(t, rows.Next())
	require.NoError(t, rows.Scan(&left))
	require.NoError(t, rows.Close())
	require.Zero(t, left, "nothing of a disconnected storage stays in the table")

	removed, err := c.DeleteByUser(ctx, 8)
	require.NoError(t, err)
	require.Len(t, removed, 1)
	require.Equal(t, other.ID, removed[0].ID)
	list, err := c.ListByUser(ctx, 8)
	require.NoError(t, err)
	require.Empty(t, list)
}
