package admin

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Fylvo: the settings of connected storages hold the sealed secrets of the OAuth clients.
// The generic settings API of the admin area neither returns nor writes them.
func TestConnectedSettingsAreKeptFromTheGenericAPI(t *testing.T) {
	logger := logging.NewConsoleLogger(logging.LevelError)
	kv := cache.NewMemoStore("", logger)
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	t.Cleanup(func() { _ = client.Close() })
	client, err := inventory.InitializeDBClient(logger, client, kv, "4.19.1")
	require.NoError(t, err)
	hasher, err := hashid.New("fylvo-test")
	require.NoError(t, err)
	dep := dependency.NewDependency(
		dependency.WithLogger(logger),
		dependency.WithConfigPath(filepath.Join(t.TempDir(), "conf.ini")),
		dependency.WithDbClient(client),
		dependency.WithKV(kv),
		dependency.WithHashIDEncoder(hasher),
	)
	ctx := context.Background()

	request := func() *gin.Context {
		c, engine := gin.CreateTestContext(httptest.NewRecorder())
		engine.ContextWithFallback = true
		c.Request = httptest.NewRequest("POST", "/api/v4/admin/settings", nil)
		util.WithValue(c, dependency.DepCtx{}, dep)
		return c
	}

	secretName := inventory.ConnectedClientSecretSetting("drive")
	idName := inventory.ConnectedClientIDSetting("drive")
	require.NoError(t, dep.SettingClient().Set(ctx, map[string]string{secretName: "sealed-value", idName: "client-id"}))

	got, err := (&GetSettingService{Keys: []string{"siteName", secretName, idName, "FYLVO_CONNECTED_DRIVE_CLIENT_SECRET"}}).GetSetting(request())
	require.NoError(t, err)
	require.Contains(t, got, "siteName")
	require.NotContains(t, got, secretName)
	require.NotContains(t, got, idName)
	for _, value := range got {
		require.NotEqual(t, "sealed-value", value)
	}

	for _, name := range []string{secretName, idName, "FYLVO_CONNECTED_DRIVE_CLIENT_SECRET", "fylvo_connected_groups_set"} {
		_, err = (&SetSettingService{Settings: map[string]string{"siteName": "Changed", name: "overwritten"}}).SetSetting(request())
		require.Error(t, err, name)
	}
	stored, err := dep.SettingClient().Gets(ctx, []string{secretName, idName, "siteName"})
	require.NoError(t, err)
	require.Equal(t, "sealed-value", stored[secretName])
	require.Equal(t, "client-id", stored[idName])
	require.NotEqual(t, "Changed", stored["siteName"], "a refused request changes nothing at all")

	// Other settings are written as before.
	_, err = (&SetSettingService{Settings: map[string]string{"siteName": "Changed"}}).SetSetting(request())
	require.NoError(t, err)
}
