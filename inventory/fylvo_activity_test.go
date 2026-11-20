package inventory

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/schema"
	"github.com/cloudreve/Cloudreve/v4/ent/setting"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/stretchr/testify/require"
)

// An installation from before the activity log: its database version stays 4.19.1, so the
// schema migration does not run again. The table and the setting come on the next start.
func TestActivityLogComesToExistingInstallations(t *testing.T) {
	logger := logging.NewConsoleLogger(logging.LevelError)
	kv := cache.NewMemoStore("", logger)
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	t.Cleanup(func() { _ = client.Close() })
	client, err := InitializeDBClient(logger, client, kv, "4.19.1")
	require.NoError(t, err)

	// As it was before.
	_, err = client.ExecContext(ctx, "DROP TABLE activities")
	require.NoError(t, err)
	_, err = client.Setting.Delete().Where(setting.Name(ActivityRetentionSetting)).Exec(schema.SkipSoftDelete(ctx))
	require.NoError(t, err)
	files, err := client.File.Query().Count(ctx)
	require.NoError(t, err)

	client, err = InitializeDBClient(logger, client, kv, "4.19.1")
	require.NoError(t, err)
	_, err = client.Activity.Query().Count(ctx)
	require.NoError(t, err, "the table is there")
	value, err := NewSettingClient(client, kv).Get(ctx, ActivityRetentionSetting)
	require.NoError(t, err)
	require.Equal(t, ActivityRetentionDefault, value)
	after, err := client.File.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, files, after, "other tables are not touched")

	// A changed retention is kept on the next start.
	require.NoError(t, NewSettingClient(client, kv).Set(ctx, map[string]string{ActivityRetentionSetting: "30"}))
	client, err = InitializeDBClient(logger, client, kv, "4.19.1")
	require.NoError(t, err)
	value, err = NewSettingClient(client, kv).Get(ctx, ActivityRetentionSetting)
	require.NoError(t, err)
	require.Equal(t, "30", value)
}

func TestAfterCommitWaitsForTheOutermostTransaction(t *testing.T) {
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	t.Cleanup(func() { _ = client.Close() })
	ctx := context.Background()
	ran := 0

	AfterCommit(ctx, func() { ran++ })
	require.Equal(t, 1, ran, "no transaction: at once")

	sc := NewSettingClient(client, nil)
	_, outer, outerCtx, err := WithTx(ctx, sc)
	require.NoError(t, err)
	_, inner, innerCtx, err := WithTx(outerCtx, sc)
	require.NoError(t, err)
	AfterCommit(innerCtx, func() { ran++ })
	require.NoError(t, Commit(inner))
	require.Equal(t, 1, ran, "the inner commit is none")
	require.NoError(t, Commit(outer))
	require.Equal(t, 2, ran)

	AfterCommit(outerCtx, func() { ran++ })
	require.Equal(t, 3, ran, "after the commit: at once")

	_, tx, txCtx, err := WithTx(ctx, sc)
	require.NoError(t, err)
	AfterCommit(txCtx, func() { ran++ })
	require.NoError(t, Rollback(tx))
	AfterCommit(txCtx, func() { ran++ })
	require.Equal(t, 3, ran, "rolled back: never")
}
