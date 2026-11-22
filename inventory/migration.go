package inventory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/group"
	"github.com/cloudreve/Cloudreve/v4/ent/node"
	"github.com/cloudreve/Cloudreve/v4/ent/oauthclient"
	"github.com/cloudreve/Cloudreve/v4/ent/oauthgrant"
	"github.com/cloudreve/Cloudreve/v4/ent/schema"
	"github.com/cloudreve/Cloudreve/v4/ent/setting"
	"github.com/cloudreve/Cloudreve/v4/ent/storagepolicy"
	"github.com/cloudreve/Cloudreve/v4/inventory/debug"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/samber/lo"
)

// needMigration exams if required schema version is satisfied.
func needMigration(client *ent.Client, ctx context.Context, requiredDbVersion string) bool {
	c, _ := client.Setting.Query().Where(setting.NameEQ(DBVersionPrefix + requiredDbVersion)).Count(ctx)
	return c == 0
}

func migrate(l logging.Logger, client *ent.Client, ctx context.Context, kv cache.Driver, requiredDbVersion string) error {
	l.Info("Start initializing database schema...")
	l.Info("Creating basic table schema...")
	if err := client.Schema.Create(ctx); err != nil {
		return fmt.Errorf("Failed creating schema resources: %w", err)
	}

	migrateDefaultSettings(l, client, ctx, kv)

	if err := migrateDefaultStoragePolicy(l, client, ctx); err != nil {
		return fmt.Errorf("failed migrating default storage policy: %w", err)
	}

	if err := migrateSysGroups(l, client, ctx); err != nil {
		return fmt.Errorf("failed migrating default storage policy: %w", err)
	}

	if err := migrateOAuthClient(l, client, ctx); err != nil {
		return fmt.Errorf("failed migrating OAuth client: %w", err)
	}

	if err := applyPatches(l, client, ctx, requiredDbVersion); err != nil {
		return fmt.Errorf("failed applying schema patches: %w", err)
	}

	client.Setting.Create().SetName(DBVersionPrefix + requiredDbVersion).SetValue("installed").Save(ctx)
	return nil
}

func migrateDefaultSettings(l logging.Logger, client *ent.Client, ctx context.Context, kv cache.Driver) {
	// clean kv cache
	if err := kv.DeleteAll(); err != nil {
		l.Warning("Failed to remove all KV entries while schema migration: %s", err)
	}

	// List existing settings into a map
	existingSettings := make(map[string]struct{})
	settings, err := client.Setting.Query().All(ctx)
	if err != nil {
		l.Warning("Failed to query existing settings: %s", err)
	}

	for _, s := range settings {
		existingSettings[s.Name] = struct{}{}
	}

	l.Info("Insert default settings...")
	for k, v := range DefaultSettings {
		if _, ok := existingSettings[k]; ok {
			l.Debug("Skip inserting setting %s, already exists.", k)
			continue
		}

		if override, ok := os.LookupEnv(EnvDefaultOverwritePrefix + k); ok {
			l.Info("Override default setting %q with env value %q", k, override)
			v = override
		}

		client.Setting.Create().SetName(k).SetValue(v).SaveX(ctx)
	}
}

func migrateDefaultStoragePolicy(l logging.Logger, client *ent.Client, ctx context.Context) error {
	if _, err := client.StoragePolicy.Query().Where(storagepolicy.ID(1)).First(ctx); err == nil {
		l.Info("Default storage policy (ID=1) already exists, skip migrating.")
		return nil
	}

	l.Info("Insert default storage policy...")
	if _, err := client.StoragePolicy.Create().
		SetName("Default storage policy").
		SetType(types.PolicyTypeLocal).
		SetDirNameRule(util.DataPath("uploads/{uid}/{path}")).
		SetFileNameRule("{uid}_{randomkey8}_{originname}").
		SetSettings(&types.PolicySetting{
			ChunkSize:   25 << 20, // 25MB
			PreAllocate: true,
		}).
		Save(ctx); err != nil {
		return fmt.Errorf("failed to create default storage policy: %w", err)
	}

	return nil
}

func migrateSysGroups(l logging.Logger, client *ent.Client, ctx context.Context) error {
	if err := migrateAdminGroup(l, client, ctx); err != nil {
		return err
	}

	if err := migrateUserGroup(l, client, ctx); err != nil {
		return err
	}

	if err := migrateAnonymousGroup(l, client, ctx); err != nil {
		return err
	}

	if err := migrateMasterNode(l, client, ctx); err != nil {
		return err
	}

	return nil
}

func migrateAdminGroup(l logging.Logger, client *ent.Client, ctx context.Context) error {
	if _, err := client.Group.Query().Where(group.ID(1)).First(ctx); err == nil {
		l.Info("Default admin group (ID=1) already exists, skip migrating.")
		return nil
	}

	l.Info("Insert default admin group...")
	permissions := &boolset.BooleanSet{}
	boolset.Sets(map[types.GroupPermission]bool{
		types.GroupPermissionIsAdmin:             true,
		types.GroupPermissionShare:               true,
		types.GroupPermissionWebDAV:              true,
		types.GroupPermissionWebDAVProxy:         true,
		types.GroupPermissionArchiveDownload:     true,
		types.GroupPermissionArchiveTask:         true,
		types.GroupPermissionShareDownload:       true,
		types.GroupPermissionRemoteDownload:      true,
		types.GroupPermissionRedirectedSource:    true,
		types.GroupPermissionAdvanceDelete:       true,
		types.GroupPermissionIgnoreFileOwnership: true,
		// TODO: review default permission
	}, permissions)
	if _, err := client.Group.Create().
		SetName("Admin").
		SetStoragePoliciesID(1).
		SetMaxStorage(1 * constants.TB). // 1 TB default storage
		SetPermissions(permissions).
		SetSettings(&types.GroupSetting{
			SourceBatchSize:  1000,
			Aria2BatchSize:   50,
			MaxWalkedFiles:   DefaultMaxWalkedFiles,
			TrashRetention:   7 * 24 * 3600,
			RedirectedSource: true,
		}).
		Save(ctx); err != nil {
		return fmt.Errorf("failed to create default admin group: %w", err)
	}

	return nil
}

func migrateUserGroup(l logging.Logger, client *ent.Client, ctx context.Context) error {
	if _, err := client.Group.Query().Where(group.ID(2)).First(ctx); err == nil {
		l.Info("Default user group (ID=2) already exists, skip migrating.")
		return nil
	}

	l.Info("Insert default user group...")
	permissions := &boolset.BooleanSet{}
	boolset.Sets(map[types.GroupPermission]bool{
		types.GroupPermissionShare:            true,
		types.GroupPermissionShareDownload:    true,
		types.GroupPermissionRedirectedSource: true,
		types.GroupPermissionWebDAV:           true, // Fylvo: both users mount Fylvo as a drive
	}, permissions)
	if _, err := client.Group.Create().
		SetName("User").
		SetStoragePoliciesID(1).
		SetMaxStorage(1 * constants.GB). // 1 GB default storage
		SetPermissions(permissions).
		SetSettings(&types.GroupSetting{
			SourceBatchSize:  10,
			Aria2BatchSize:   1,
			MaxWalkedFiles:   DefaultMaxWalkedFiles,
			TrashRetention:   7 * 24 * 3600,
			RedirectedSource: true,
		}).
		Save(ctx); err != nil {
		return fmt.Errorf("failed to create default user group: %w", err)
	}

	return nil
}

func migrateAnonymousGroup(l logging.Logger, client *ent.Client, ctx context.Context) error {
	if _, err := client.Group.Query().Where(group.ID(AnonymousGroupID)).First(ctx); err == nil {
		l.Info("Default anonymous group (ID=3) already exists, skip migrating.")
		return nil
	}

	l.Info("Insert default anonymous group...")
	permissions := &boolset.BooleanSet{}
	boolset.Sets(map[types.GroupPermission]bool{
		types.GroupPermissionIsAnonymous:   true,
		types.GroupPermissionShareDownload: true,
	}, permissions)
	if _, err := client.Group.Create().
		SetName("Anonymous").
		SetPermissions(permissions).
		SetSettings(&types.GroupSetting{
			MaxWalkedFiles:   DefaultMaxWalkedFiles,
			RedirectedSource: true,
		}).
		Save(ctx); err != nil {
		return fmt.Errorf("failed to create default anonymous group: %w", err)
	}

	return nil
}

func migrateMasterNode(l logging.Logger, client *ent.Client, ctx context.Context) error {
	if _, err := client.Node.Query().Where(node.TypeEQ(node.TypeMaster)).First(ctx); err == nil {
		l.Info("Default master node already exists, skip migrating.")
		return nil
	}

	capabilities := &boolset.BooleanSet{}
	boolset.Sets(map[types.NodeCapability]bool{
		types.NodeCapabilityCreateArchive:  true,
		types.NodeCapabilityExtractArchive: true,
		types.NodeCapabilityRemoteDownload: true,
	}, capabilities)

	stm := client.Node.Create().
		SetType(node.TypeMaster).
		SetCapabilities(capabilities).
		SetName("Master").
		SetSettings(&types.NodeSetting{
			Provider: types.DownloaderProviderAria2,
		}).
		SetStatus(node.StatusActive)

	_, enableAria2 := os.LookupEnv(EnvEnableAria2)
	if enableAria2 {
		l.Info("Aria2 is override as enabled.")
		stm.SetSettings(&types.NodeSetting{
			Provider: types.DownloaderProviderAria2,
			Aria2Setting: &types.Aria2Setting{
				Server: "http://127.0.0.1:6800/jsonrpc",
			},
		})
	}

	l.Info("Insert default master node...")
	if _, err := stm.Save(ctx); err != nil {
		return fmt.Errorf("failed to create default master node: %w", err)
	}

	return nil
}

const (
	// Fylvo: clients of the Cloudreve desktop app (Microsoft Store) and the Cloudreve iOS app.
	// Fylvo does not support these apps; the clients are removed from existing installations
	// on start (see removeRetiredOAuthClients).
	OAuthClientCloudreveDesktopGUID = "393a1839-f52e-498e-9972-e77cc2241eee"
	OAuthClientCloudreveiOSGUID     = "220db97a-44a3-44f7-99b6-d767262b4daa"

	OAuthClientCLIGUID   = "6326d2af-2fef-4a99-94da-1ee8ef0ca53f"
	OAuthClientCLISecret = "yoFiNgbxvSCzK2Nm92T3TNaREh4qBjq4"
	// Fylvo: a translation key, like OAuthClientFylvoDesktopName. Installations that still carry
	// oauthClientCloudreveCLIName get it on start (see replaceCloudreveOAuthClientIcons).
	OAuthClientCLIName          = "application:oauth.cliApp"
	oauthClientCloudreveCLIName = "Cloudreve CLI"
	OAuthClientCLIRedirectURI   = "http://127.0.0.1/callback"

	// Fylvo Desktop signs in with its own client; the web app sends it back to fylvo://.
	OAuthClientFylvoDesktopGUID        = "512cd652-c028-4332-992e-d88a4406e5b2"
	OAuthClientFylvoDesktopSecret      = "ZO3QjjTTSLiRjaw8ixyryEdJmTnmhtCj"
	OAuthClientFylvoDesktopName        = "application:oauth.fylvoDesktop"
	OAuthClientFylvoDesktopRedirectURI = "/callback/fylvo-desktop"

	// OAuthClientDefaultIcon is the icon of the built-in clients. Clients created by upstream
	// Cloudreve point at oauthClientCloudreveIcon, which Fylvo no longer ships; they are moved to
	// the Fylvo icon on start (see replaceCloudreveOAuthClientIcons).
	OAuthClientDefaultIcon   = "/static/img/fylvo.svg"
	oauthClientCloudreveIcon = "/static/img/cloudreve.svg"
)

// RetiredOAuthClientGUIDs are the clients of Cloudreve apps that Fylvo does not support.
var RetiredOAuthClientGUIDs = []string{OAuthClientCloudreveDesktopGUID, OAuthClientCloudreveiOSGUID}

// IsRetiredOAuthClient reports whether a client ID belongs to a Cloudreve app that Fylvo does
// not support. Tokens issued to such a client are refused.
func IsRetiredOAuthClient(clientID string) bool {
	for _, guid := range RetiredOAuthClientGUIDs {
		if strings.EqualFold(strings.TrimSpace(clientID), guid) {
			return true
		}
	}
	return false
}

func migrateOAuthClient(l logging.Logger, client *ent.Client, ctx context.Context) error {
	if err := migrateOAuthClientCLI(l, client, ctx); err != nil {
		return err
	}

	return nil
}

// removeRetiredOAuthClients deletes the clients of Cloudreve apps that Fylvo does not support
// (RetiredOAuthClientGUIDs) and the users' grants for them, if an installation still has them.
// Those apps can then neither sign in nor refresh their tokens, and no empty entry stays under
// "connected apps". Runs on every start, like the Fylvo client ensure, and also cleans up
// grants a deleted client left behind.
func removeRetiredOAuthClients(l logging.Logger, client *ent.Client, ctx context.Context) error {
	// There are only a few OAuth clients; matching in Go catches IDs stored in another case or
	// with surrounding spaces, whatever the database collation does.
	all, err := client.OAuthClient.Query().All(schema.SkipSoftDelete(ctx))
	if err != nil {
		return fmt.Errorf("failed to list OAuth clients: %w", err)
	}
	for _, old := range all {
		if IsRetiredOAuthClient(old.GUID) {
			if err := removeOAuthClient(l, client, ctx, old); err != nil {
				return err
			}
		}
	}
	return nil
}

// replaceCloudreveOAuthClientIcons points clients that still use the removed Cloudreve icon at
// the Fylvo icon, and gives the CLI client its Fylvo name if it still has the Cloudreve one.
// Runs on every start, because the schema migration only runs when the version changes. Icons
// and names an administrator chose are kept.
func replaceCloudreveOAuthClientIcons(l logging.Logger, client *ent.Client, ctx context.Context) error {
	all, err := client.OAuthClient.Query().All(schema.SkipSoftDelete(ctx))
	if err != nil {
		return fmt.Errorf("failed to list OAuth clients: %w", err)
	}
	for _, c := range all {
		if c.GUID == OAuthClientCLIGUID && c.Name == oauthClientCloudreveCLIName {
			if err := client.OAuthClient.UpdateOneID(c.ID).SetName(OAuthClientCLIName).Exec(schema.SkipSoftDelete(ctx)); err != nil {
				return fmt.Errorf("failed to rename OAuth client %s: %w", c.GUID, err)
			}
			l.Info("Renamed OAuth client %s from %q.", c.GUID, oauthClientCloudreveCLIName)
		}
		if c.Props == nil || c.Props.Icon != oauthClientCloudreveIcon {
			continue
		}
		props := *c.Props
		props.Icon = OAuthClientDefaultIcon
		if err := client.OAuthClient.UpdateOneID(c.ID).SetProps(&props).Exec(schema.SkipSoftDelete(ctx)); err != nil {
			return fmt.Errorf("failed to update icon of OAuth client %s: %w", c.GUID, err)
		}
		l.Info("Replaced the old icon of OAuth client %s with the Fylvo icon.", c.GUID)
	}
	return nil
}

func removeOAuthClient(l logging.Logger, client *ent.Client, ctx context.Context, old *ent.OAuthClient) error {
	guid := old.GUID

	tx, err := client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	grants, err := tx.OAuthGrant.Delete().Where(oauthgrant.ClientID(old.ID)).Exec(ctx)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed to remove grants of OAuth client %s: %w", guid, err)
	}
	clients, err := tx.OAuthClient.Delete().Where(oauthclient.ID(old.ID)).Exec(ctx)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("failed to remove OAuth client %s: %w", guid, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to remove OAuth client %s: %w", guid, err)
	}
	if grants > 0 || clients > 0 {
		l.Info("Removed the OAuth client of an unsupported app (GUID=%s) and %d grant(s).", guid, grants)
	}
	return nil
}

// ensureOAuthClientFylvoDesktop creates the Fylvo Desktop client if it is missing. It runs on
// every start, because the schema migration only runs when the version changes and existing
// Fylvo installations keep their version. Administrator changes to the client are kept, and a
// soft-deleted row counts as present (its GUID is still taken by the unique index).
func ensureOAuthClientFylvoDesktop(l logging.Logger, client *ent.Client, ctx context.Context) error {
	exists := func() (bool, error) {
		n, err := client.OAuthClient.Query().Where(oauthclient.GUID(OAuthClientFylvoDesktopGUID)).Count(schema.SkipSoftDelete(ctx))
		return n > 0, err
	}
	if found, err := exists(); err != nil {
		return fmt.Errorf("failed to query Fylvo Desktop OAuth client: %w", err)
	} else if found {
		if n, err := client.OAuthClient.Query().Where(oauthclient.GUID(OAuthClientFylvoDesktopGUID)).Count(ctx); err == nil && n == 0 {
			l.Warning("The Fylvo Desktop OAuth client (GUID=%s) is deleted; Fylvo Desktop cannot sign in.", OAuthClientFylvoDesktopGUID)
		}
		return nil
	}

	l.Info("Creating OAuth client for Fylvo Desktop (GUID=%s)...", OAuthClientFylvoDesktopGUID)
	if _, err := client.OAuthClient.Create().
		SetGUID(OAuthClientFylvoDesktopGUID).
		SetSecret(OAuthClientFylvoDesktopSecret).
		SetName(OAuthClientFylvoDesktopName).
		SetRedirectUris([]string{OAuthClientFylvoDesktopRedirectURI}).
		SetScopes([]string{"profile", "email", "openid", "offline_access", "UserInfo.Write", "Workflow.Write", "Files.Write", "Shares.Write"}).
		SetProps(&types.OAuthClientProps{Icon: OAuthClientDefaultIcon, RefreshTokenTTL: 7776000}).
		SetIsEnabled(true).
		Save(ctx); err != nil {
		// Another server on the same database may be creating it right now; give its
		// transaction a moment to commit.
		for try := 0; ; try++ {
			if found, _ := exists(); found {
				return nil
			}
			if try == 5 {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		return fmt.Errorf("failed to create Fylvo Desktop OAuth client: %w", err)
	}

	return nil
}

// migrateOAuthClientCLI preserves administrator changes to an existing built-in client.
func migrateOAuthClientCLI(l logging.Logger, client *ent.Client, ctx context.Context) error {
	if _, err := client.OAuthClient.Query().Where(oauthclient.GUID(OAuthClientCLIGUID)).First(ctx); err == nil {
		l.Info("Default OAuth client (GUID=%s) already exists, skip migrating.", OAuthClientCLIGUID)
		return nil
	} else if !ent.IsNotFound(err) {
		return fmt.Errorf("failed to query default CLI OAuth client: %w", err)
	}

	if _, err := client.OAuthClient.Create().
		SetGUID(OAuthClientCLIGUID).
		SetSecret(OAuthClientCLISecret).
		SetName(OAuthClientCLIName).
		SetRedirectUris([]string{OAuthClientCLIRedirectURI}).
		SetScopes([]string{"profile", "email", "openid", "offline_access", "UserInfo.Write", "Workflow.Write", "Files.Write", "Shares.Write"}).
		SetProps(&types.OAuthClientProps{Icon: OAuthClientDefaultIcon, RefreshTokenTTL: 7776000}).
		SetIsEnabled(true).
		Save(ctx); err != nil {
		return fmt.Errorf("failed to create default CLI OAuth client: %w", err)
	}
	return nil
}

type (
	PatchFunc func(l logging.Logger, client *ent.Client, ctx context.Context) error
	Patch     struct {
		Name       string
		EndVersion string
		Func       PatchFunc
	}
)

var patches = []Patch{
	{
		Name:       "apply_default_archive_viewer",
		EndVersion: "4.7.0",
		Func: func(l logging.Logger, client *ent.Client, ctx context.Context) error {
			fileViewersSetting, err := client.Setting.Query().Where(setting.Name("file_viewers")).First(ctx)
			if err != nil {
				return fmt.Errorf("failed to query file_viewers setting: %w", err)
			}

			var fileViewers []types.ViewerGroup
			if err := json.Unmarshal([]byte(fileViewersSetting.Value), &fileViewers); err != nil {
				return fmt.Errorf("failed to unmarshal file_viewers setting: %w", err)
			}

			fileViewerExisted := false
			for _, viewer := range fileViewers[0].Viewers {
				if viewer.ID == "archive" {
					fileViewerExisted = true
					break
				}
			}

			// 2.2 If not existed, add it
			if !fileViewerExisted {
				// Found existing archive viewer default setting
				var defaultArchiveViewer types.Viewer
				for _, viewer := range defaultFileViewers[0].Viewers {
					if viewer.ID == "archive" {
						defaultArchiveViewer = viewer
						break
					}
				}

				fileViewers[0].Viewers = append(fileViewers[0].Viewers, defaultArchiveViewer)
				newFileViewersSetting, err := json.Marshal(fileViewers)
				if err != nil {
					return fmt.Errorf("failed to marshal file_viewers setting: %w", err)
				}

				if _, err := client.Setting.UpdateOne(fileViewersSetting).SetValue(string(newFileViewersSetting)).Save(ctx); err != nil {
					return fmt.Errorf("failed to update file_viewers setting: %w", err)
				}
			}

			return nil
		},
	},
	{
		Name:       "apply_default_excalidraw_viewer",
		EndVersion: "4.1.0",
		Func: func(l logging.Logger, client *ent.Client, ctx context.Context) error {
			// 1. Apply excalidraw file icons
			// 1.1 Check if it's already applied
			iconSetting, err := client.Setting.Query().Where(setting.Name("explorer_icons")).First(ctx)
			if err != nil {
				return fmt.Errorf("failed to query explorer_icons setting: %w", err)
			}

			var icons []types.FileTypeIconSetting
			if err := json.Unmarshal([]byte(iconSetting.Value), &icons); err != nil {
				return fmt.Errorf("failed to unmarshal explorer_icons setting: %w", err)
			}

			iconExisted := false
			for _, icon := range icons {
				if lo.Contains(icon.Exts, "excalidraw") {
					iconExisted = true
					break
				}
			}

			// 1.2 If not existed, add it
			if !iconExisted {
				// Found existing excalidraw icon default setting
				var defaultExcalidrawIcon types.FileTypeIconSetting
				for _, icon := range defaultIcons {
					if lo.Contains(icon.Exts, "excalidraw") {
						defaultExcalidrawIcon = icon
						break
					}
				}

				icons = append(icons, defaultExcalidrawIcon)
				newIconSetting, err := json.Marshal(icons)
				if err != nil {
					return fmt.Errorf("failed to marshal explorer_icons setting: %w", err)
				}

				if _, err := client.Setting.UpdateOne(iconSetting).SetValue(string(newIconSetting)).Save(ctx); err != nil {
					return fmt.Errorf("failed to update explorer_icons setting: %w", err)
				}
			}

			// 2. Apply default file viewers
			// 2.1 Check if it's already applied
			fileViewersSetting, err := client.Setting.Query().Where(setting.Name("file_viewers")).First(ctx)
			if err != nil {
				return fmt.Errorf("failed to query file_viewers setting: %w", err)
			}

			var fileViewers []types.ViewerGroup
			if err := json.Unmarshal([]byte(fileViewersSetting.Value), &fileViewers); err != nil {
				return fmt.Errorf("failed to unmarshal file_viewers setting: %w", err)
			}

			fileViewerExisted := false
			for _, viewer := range fileViewers[0].Viewers {
				if viewer.ID == "excalidraw" {
					fileViewerExisted = true
					break
				}
			}

			// 2.2 If not existed, add it
			if !fileViewerExisted {
				// Found existing excalidraw viewer default setting
				var defaultExcalidrawViewer types.Viewer
				for _, viewer := range defaultFileViewers[0].Viewers {
					if viewer.ID == "excalidraw" {
						defaultExcalidrawViewer = viewer
						break
					}
				}

				fileViewers[0].Viewers = append(fileViewers[0].Viewers, defaultExcalidrawViewer)
				newFileViewersSetting, err := json.Marshal(fileViewers)
				if err != nil {
					return fmt.Errorf("failed to marshal file_viewers setting: %w", err)
				}

				if _, err := client.Setting.UpdateOne(fileViewersSetting).SetValue(string(newFileViewersSetting)).Save(ctx); err != nil {
					return fmt.Errorf("failed to update file_viewers setting: %w", err)
				}
			}

			return nil
		},
	},
	{
		Name:       "apply_email_title_magic_var",
		EndVersion: "4.7.0",
		Func: func(l logging.Logger, client *ent.Client, ctx context.Context) error {
			// 1. Activate Template
			mailActivationTemplateSetting, err := client.Setting.Query().Where(setting.Name("mail_activation_template")).First(ctx)
			if err != nil {
				return fmt.Errorf("failed to query mail_activation_template setting: %w", err)
			}

			var mailActivationTemplate []struct {
				Title    string `json:"title"`
				Body     string `json:"body"`
				Language string `json:"language"`
			}
			if err := json.Unmarshal([]byte(mailActivationTemplateSetting.Value), &mailActivationTemplate); err != nil {
				return fmt.Errorf("failed to unmarshal mail_activation_template setting: %w", err)
			}

			for i, t := range mailActivationTemplate {
				mailActivationTemplate[i].Title = fmt.Sprintf("[{{ .CommonContext.SiteBasic.Name }}] %s", t.Title)
			}

			newMailActivationTemplate, err := json.Marshal(mailActivationTemplate)
			if err != nil {
				return fmt.Errorf("failed to marshal mail_activation_template setting: %w", err)
			}

			if _, err := client.Setting.UpdateOne(mailActivationTemplateSetting).SetValue(string(newMailActivationTemplate)).Save(ctx); err != nil {
				return fmt.Errorf("failed to update mail_activation_template setting: %w", err)
			}

			// 2. Reset Password Template
			mailResetTemplateSetting, err := client.Setting.Query().Where(setting.Name("mail_reset_template")).First(ctx)
			if err != nil {
				return fmt.Errorf("failed to query mail_reset_template setting: %w", err)
			}

			var mailResetTemplate []struct {
				Title    string `json:"title"`
				Body     string `json:"body"`
				Language string `json:"language"`
			}
			if err := json.Unmarshal([]byte(mailResetTemplateSetting.Value), &mailResetTemplate); err != nil {
				return fmt.Errorf("failed to unmarshal mail_reset_template setting: %w", err)
			}

			for i, t := range mailResetTemplate {
				mailResetTemplate[i].Title = fmt.Sprintf("[{{ .CommonContext.SiteBasic.Name }}] %s", t.Title)
			}

			newMailResetTemplate, err := json.Marshal(mailResetTemplate)
			if err != nil {
				return fmt.Errorf("failed to marshal mail_reset_template setting: %w", err)
			}

			if _, err := client.Setting.UpdateOne(mailResetTemplateSetting).SetValue(string(newMailResetTemplate)).Save(ctx); err != nil {
				return fmt.Errorf("failed to update mail_reset_template setting: %w", err)
			}

			return nil
		},
	},
	{
		Name:       "apply_thumb_path_magic_var",
		EndVersion: "4.10.0",
		Func: func(l logging.Logger, client *ent.Client, ctx context.Context) error {
			thumbSuffixSetting, err := client.Setting.Query().Where(setting.Name("thumb_entity_suffix")).First(ctx)
			if err != nil {
				return fmt.Errorf("failed to query thumb_entity_suffix setting: %w", err)
			}

			newThumbSuffix := fmt.Sprintf("{blob_path}/{blob_name}%s", thumbSuffixSetting.Value)
			if _, err := client.Setting.UpdateOne(thumbSuffixSetting).SetValue(newThumbSuffix).Save(ctx); err != nil {
				return fmt.Errorf("failed to update thumb_entity_suffix setting: %w", err)
			}

			return nil
		},
	},
	{
		Name:       "reset_secret_key",
		EndVersion: "4.13.0",
		Func: func(l logging.Logger, client *ent.Client, ctx context.Context) error {
			newSecretKey := util.RandStringRunesCrypto(256)
			ctx = context.WithValue(ctx, debug.SkipDbLogging{}, true)
			if err := client.Setting.Update().Where(setting.Name("secret_key")).SetValue(newSecretKey).Exec(ctx); err != nil {
				return fmt.Errorf("failed to update secret_key setting: %w", err)
			}

			return nil
		},
	},
}

func applyPatches(l logging.Logger, client *ent.Client, ctx context.Context, requiredDbVersion string) error {
	allVersionMarks, err := client.Setting.Query().Where(setting.NameHasPrefix(DBVersionPrefix)).All(ctx)
	if err != nil {
		return err
	}

	requiredDbVersion = strings.TrimSuffix(requiredDbVersion, "-pro")

	// Find the latest applied version
	var latestAppliedVersion *semver.Version
	for _, v := range allVersionMarks {
		v.Name = strings.TrimSuffix(v.Name, "-pro")
		version, err := semver.NewVersion(strings.TrimPrefix(v.Name, DBVersionPrefix))
		if err != nil {
			l.Warning("Failed to parse past version %s: %s", v.Name, err)
			continue
		}
		if latestAppliedVersion == nil || version.Compare(latestAppliedVersion) > 0 {
			latestAppliedVersion = version
		}
	}

	requiredVersion, err := semver.NewVersion(requiredDbVersion)
	if err != nil {
		return fmt.Errorf("failed to parse required version %s: %w", requiredDbVersion, err)
	}

	if latestAppliedVersion == nil || requiredVersion.Compare(requiredVersion) > 0 {
		latestAppliedVersion = requiredVersion
	}

	for _, patch := range patches {
		if latestAppliedVersion.Compare(semver.MustParse(patch.EndVersion)) < 0 {
			l.Info("Applying schema patch %s...", patch.Name)
			if err := patch.Func(l, client, ctx); err != nil {
				return err
			}
		}
	}

	return nil
}
