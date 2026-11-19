package inventory

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/oauthclient"
	"github.com/cloudreve/Cloudreve/v4/ent/schema"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/stretchr/testify/require"
)

func TestBuiltinCLIOAuthMigration(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "migration.db"))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	logger := logging.NewConsoleLogger(logging.LevelError)

	require.NoError(t, migrateOAuthClient(logger, client, ctx))
	desktop, err := client.OAuthClient.Query().Where(oauthclient.GUID(OAuthClientDesktopGUID)).Only(ctx)
	require.NoError(t, err)
	cli, err := client.OAuthClient.Query().Where(oauthclient.GUID(OAuthClientCLIGUID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "Cloudreve CLI", cli.Name)
	require.Equal(t, OAuthClientCLISecret, cli.Secret)
	require.Equal(t, []string{"http://127.0.0.1/callback"}, cli.RedirectUris)
	require.Equal(t, desktop.Scopes, cli.Scopes)
	require.Equal(t, desktop.Props, cli.Props)
	require.Equal(t, int64(7776000), cli.Props.RefreshTokenTTL)
	require.True(t, cli.IsEnabled)

	_, err = client.OAuthClient.UpdateOne(cli).SetIsEnabled(false).SetName("Disabled by administrator").SetSecret("administrator-secret").Save(ctx)
	require.NoError(t, err)
	require.NoError(t, migrateOAuthClient(logger, client, ctx))
	preserved, err := client.OAuthClient.Get(ctx, cli.ID)
	require.NoError(t, err)
	require.False(t, preserved.IsEnabled)
	require.Equal(t, "Disabled by administrator", preserved.Name)
	require.Equal(t, "administrator-secret", preserved.Secret)
	count, err := client.OAuthClient.Query().Where(oauthclient.GUID(OAuthClientCLIGUID)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestEnsureFylvoDesktopOAuthClient(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	logger := logging.NewConsoleLogger(logging.LevelError)

	// An existing installation already has the Cloudreve desktop client.
	require.NoError(t, migrateOAuthClient(logger, client, ctx))
	require.NoError(t, ensureOAuthClientFylvoDesktop(logger, client, ctx))

	fylvo, err := client.OAuthClient.Query().Where(oauthclient.GUID(OAuthClientFylvoDesktopGUID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"/callback/fylvo-desktop"}, fylvo.RedirectUris)
	require.Equal(t, "/static/img/fylvo.svg", fylvo.Props.Icon)
	require.True(t, fylvo.IsEnabled)

	// The Store app client stays as it is.
	desktop, err := client.OAuthClient.Query().Where(oauthclient.GUID(OAuthClientDesktopGUID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"/callback/desktop"}, desktop.RedirectUris)
	require.Equal(t, desktop.Scopes, fylvo.Scopes)

	// Running again keeps administrator changes and creates no duplicate.
	_, err = client.OAuthClient.UpdateOne(fylvo).SetIsEnabled(false).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, ensureOAuthClientFylvoDesktop(logger, client, ctx))
	count, err := client.OAuthClient.Query().Where(oauthclient.GUID(OAuthClientFylvoDesktopGUID)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	preserved, err := client.OAuthClient.Get(ctx, fylvo.ID)
	require.NoError(t, err)
	require.False(t, preserved.IsEnabled)
}

func TestEnsureFylvoDesktopOAuthClientKeepsSoftDeletedRow(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo-deleted.db"))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	logger := logging.NewConsoleLogger(logging.LevelError)

	require.NoError(t, ensureOAuthClientFylvoDesktop(logger, client, ctx))
	_, err := client.OAuthClient.Delete().Where(oauthclient.GUID(OAuthClientFylvoDesktopGUID)).Exec(ctx)
	require.NoError(t, err)
	visible, err := client.OAuthClient.Query().Where(oauthclient.GUID(OAuthClientFylvoDesktopGUID)).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, visible)
	kept, err := client.OAuthClient.Query().Where(oauthclient.GUID(OAuthClientFylvoDesktopGUID)).Count(schema.SkipSoftDelete(ctx))
	require.NoError(t, err)
	require.Equal(t, 1, kept, "the delete must be a soft delete for this test to mean anything")

	// A soft-deleted row still holds the GUID; starting again must not fail on the unique index.
	require.NoError(t, ensureOAuthClientFylvoDesktop(logger, client, ctx))
}
