package inventory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/oauthclient"
	"github.com/cloudreve/Cloudreve/v4/ent/schema"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/stretchr/testify/require"
)

func TestBuiltinCLIOAuthMigration(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "migration.db"))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	logger := logging.NewConsoleLogger(logging.LevelError)

	require.NoError(t, migrateOAuthClient(logger, client, ctx))
	require.NoError(t, ensureOAuthClientFylvoDesktop(logger, client, ctx))
	desktop, err := client.OAuthClient.Query().Where(oauthclient.GUID(OAuthClientFylvoDesktopGUID)).Only(ctx)
	require.NoError(t, err)
	cli, err := client.OAuthClient.Query().Where(oauthclient.GUID(OAuthClientCLIGUID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, "Cloudreve CLI", cli.Name)
	require.Equal(t, OAuthClientCLISecret, cli.Secret)
	require.Equal(t, []string{"http://127.0.0.1/callback"}, cli.RedirectUris)
	require.Equal(t, desktop.Scopes, cli.Scopes)
	require.Equal(t, desktop.Props.RefreshTokenTTL, cli.Props.RefreshTokenTTL)
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

	require.NoError(t, migrateOAuthClient(logger, client, ctx))
	require.NoError(t, ensureOAuthClientFylvoDesktop(logger, client, ctx))

	fylvo, err := client.OAuthClient.Query().Where(oauthclient.GUID(OAuthClientFylvoDesktopGUID)).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"/callback/fylvo-desktop"}, fylvo.RedirectUris)
	require.Equal(t, "/static/img/fylvo.svg", fylvo.Props.Icon)
	require.True(t, fylvo.IsEnabled)


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

func TestRemoveRetiredOAuthClients(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "retired.db"))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	logger := logging.NewConsoleLogger(logging.LevelError)

	// New installations never get the clients of the Cloudreve apps.
	require.NoError(t, migrateOAuthClient(logger, client, ctx))
	for _, guid := range RetiredOAuthClientGUIDs {
		count, err := client.OAuthClient.Query().Where(oauthclient.GUID(guid)).Count(schema.SkipSoftDelete(ctx))
		require.NoError(t, err)
		require.Zero(t, count, guid)
	}

	// Existing installations have them from the upstream migration, and users who connected the
	// apps have grants for them. All are removed on start; the CLI client stays.
	group, err := client.Group.Create().SetName("Users").SetPermissions(&boolset.BooleanSet{}).Save(ctx)
	require.NoError(t, err)
	u, err := client.User.Create().SetEmail("a@fylvo.test").SetNick("a").SetGroup(group).SetSettings(&types.UserSetting{}).Save(ctx)
	require.NoError(t, err)
	u2, err := client.User.Create().SetEmail("b@fylvo.test").SetNick("b").SetGroup(group).SetSettings(&types.UserSetting{}).Save(ctx)
	require.NoError(t, err)
	var olds []int
	for _, guid := range RetiredOAuthClientGUIDs {
		old, err := client.OAuthClient.Create().
			SetGUID(guid).
			SetSecret("upstream").
			SetName("upstream").
			SetRedirectUris([]string{"/callback/upstream"}).
			SetScopes([]string{"openid"}).
			SetIsEnabled(true).
			Save(ctx)
		require.NoError(t, err)
		olds = append(olds, old.ID)
		_, err = client.OAuthGrant.Create().SetUserID(u.ID).SetClientID(old.ID).SetScopes([]string{"openid"}).Save(ctx)
		require.NoError(t, err)
	}

	require.NoError(t, removeRetiredOAuthClients(logger, client, ctx))
	for _, guid := range RetiredOAuthClientGUIDs {
		count, err := client.OAuthClient.Query().Where(oauthclient.GUID(guid)).Count(ctx)
		require.NoError(t, err)
		require.Zero(t, count, guid)
	}
	grants, err := NewOAuthClientClient(client, conf.SQLiteDB).GetGrantsByUserID(ctx, u.ID)
	require.NoError(t, err)
	require.Empty(t, grants, "no empty entry may stay under connected apps")
	cli, err := client.OAuthClient.Query().Where(oauthclient.GUID(OAuthClientCLIGUID)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, cli, "the CLI client stays")

	// Grants left behind by an earlier version (client already deleted) are cleaned up too.
	for _, id := range olds {
		_, err = client.OAuthGrant.Create().SetUserID(u2.ID).SetClientID(id).SetScopes([]string{"openid"}).Save(ctx)
		require.NoError(t, err)
	}
	require.NoError(t, removeRetiredOAuthClients(logger, client, ctx))
	grants, err = NewOAuthClientClient(client, conf.SQLiteDB).GetGrantsByUserID(ctx, u2.ID)
	require.NoError(t, err)
	require.Empty(t, grants)

	// Running again is a no-op.
	require.NoError(t, removeRetiredOAuthClients(logger, client, ctx))
}

func TestIsRetiredOAuthClient(t *testing.T) {
	require.True(t, IsRetiredOAuthClient(OAuthClientCloudreveDesktopGUID))
	require.True(t, IsRetiredOAuthClient(strings.ToUpper(OAuthClientCloudreveiOSGUID)))
	require.False(t, IsRetiredOAuthClient(OAuthClientFylvoDesktopGUID))
	require.False(t, IsRetiredOAuthClient(OAuthClientCLIGUID))
	require.False(t, IsRetiredOAuthClient(""))
}

func TestRetiredOAuthClientLookupFails(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "retired-lookup.db"))
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	// Even a row created by hand with a retired client ID cannot be used to sign in.
	for _, guid := range RetiredOAuthClientGUIDs {
		_, err := client.OAuthClient.Create().
			SetGUID(guid).
			SetSecret("by-hand").
			SetName("by-hand").
			SetRedirectUris([]string{"/callback/by-hand"}).
			SetScopes([]string{"openid"}).
			SetIsEnabled(true).
			Save(ctx)
		require.NoError(t, err)
		clients := NewOAuthClientClient(client, conf.SQLiteDB)
		_, err = clients.GetByGUID(ctx, guid)
		require.True(t, ent.IsNotFound(err), guid)
		_, err = clients.GetByGUIDWithGrants(ctx, strings.ToUpper(guid), 1)
		require.True(t, ent.IsNotFound(err), guid)
	}
}
