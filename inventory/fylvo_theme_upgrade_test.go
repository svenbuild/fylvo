package inventory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/setting"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/stretchr/testify/require"
)

func TestUpgradeThemesReplacesOnlyOldDefaults(t *testing.T) {
	ctx := context.Background()
	logger := logging.NewConsoleLogger(logging.LevelError)
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "themes.db"))
	t.Cleanup(func() { _ = client.Close() })

	var old []json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(fylvoOldThemes), &old))
	read := func() string {
		s, err := client.Setting.Query().Where(setting.Name("theme_options")).Only(ctx)
		require.NoError(t, err)
		return s.Value
	}

	// An old default, written with different spacing: replaced.
	require.NoError(t, client.Setting.Create().SetName("theme_options").SetValue(" "+string(old[0])+" ").Exec(ctx))
	require.NoError(t, upgradeThemes(logger, client, ctx))
	require.Equal(t, fylvoTheme, read())

	var themes map[string]any
	require.NoError(t, json.Unmarshal([]byte(read()), &themes))
	require.Len(t, themes, 6)
	require.Contains(t, themes, "#7484F4")

	// A list the administrator changed: kept.
	custom := `{"#123456":{"light":{}}}`
	require.NoError(t, client.Setting.Update().Where(setting.Name("theme_options")).SetValue(custom).Exec(ctx))
	require.NoError(t, upgradeThemes(logger, client, ctx))
	require.Equal(t, custom, read())
}
