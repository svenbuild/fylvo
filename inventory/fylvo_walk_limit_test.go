package inventory

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/stretchr/testify/require"
)

func TestRaiseWalkLimit(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "walk-limit.db"))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	logger := logging.NewConsoleLogger(logging.LevelError)

	old, err := client.Group.Create().SetName("Old").SetPermissions(&boolset.BooleanSet{}).
		SetSettings(&types.GroupSetting{MaxWalkedFiles: 100000, TrashRetention: 30}).Save(ctx)
	require.NoError(t, err)
	chosen, err := client.Group.Create().SetName("Chosen").SetPermissions(&boolset.BooleanSet{}).
		SetSettings(&types.GroupSetting{MaxWalkedFiles: 5000}).Save(ctx)
	require.NoError(t, err)

	require.NoError(t, raiseWalkLimit(logger, client, ctx))
	require.NoError(t, raiseWalkLimit(logger, client, ctx)) // running again changes nothing

	old, err = client.Group.Get(ctx, old.ID)
	require.NoError(t, err)
	require.Equal(t, DefaultMaxWalkedFiles, old.Settings.MaxWalkedFiles)
	require.Equal(t, 30, old.Settings.TrashRetention, "other settings stay")
	chosen, err = client.Group.Get(ctx, chosen.ID)
	require.NoError(t, err)
	require.Equal(t, 5000, chosen.Settings.MaxWalkedFiles)
}
