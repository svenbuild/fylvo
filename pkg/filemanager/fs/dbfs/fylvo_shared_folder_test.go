package dbfs

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/eventhub"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/lock"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

// Fylvo: tests for shared folders. Who may read and change what in a folder that is shared
// with named members.

// sharedFolderEnv is a small Fylvo with four users and one folder of the owner that is
// shared with the editor (may edit) and the reader (may read).
type sharedFolderEnv struct {
	t      *testing.T
	ctx    context.Context
	client *ent.Client
	hasher hashid.Encoder

	fileClient  inventory.FileClient
	shareClient inventory.ShareClient
	userClient  inventory.UserClient
	policies    inventory.StoragePolicyClient
	settings    setting.Provider
	locks       lock.LockSystem
	kv          cache.Driver
	events      eventhub.EventHub
	logger      logging.Logger

	owner, editor, reader, outsider *ent.User
	share                           *ent.Share
	shareID                         string
}

func newSharedFolderEnv(t *testing.T) *sharedFolderEnv {
	logger := logging.NewConsoleLogger(logging.LevelError)
	ctx := context.WithValue(context.Background(), logging.LoggerCtx{}, logger)
	kv := cache.NewMemoStore("", logger)

	// One connection, as the server runs SQLite (inventory/client.go): a query outside of an
	// open transaction then hangs here as it would there.
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	client := enttest.NewClient(t, enttest.WithOptions(ent.Driver(entsql.OpenDB(dialect.SQLite, db))))
	t.Cleanup(func() { _ = client.Close() })
	client, err = inventory.InitializeDBClient(logger, client, kv, "4.19.1")
	require.NoError(t, err)

	hasher, err := hashid.New("fylvo-test")
	require.NoError(t, err)

	e := &sharedFolderEnv{
		t:           t,
		ctx:         ctx,
		client:      client,
		hasher:      hasher,
		fileClient:  inventory.NewFileClient(client, conf.SQLiteDB, hasher),
		shareClient: inventory.NewShareClient(client, conf.SQLiteDB, hasher),
		userClient:  inventory.NewUserClient(client),
		policies:    inventory.NewStoragePolicyClient(client, kv),
		settings:    setting.NewProvider(setting.NewKvSettingStore(kv, setting.NewDbSettingStore(inventory.NewSettingClient(client, kv), nil))),
		locks:       lock.NewMemLS(hasher, logger),
		kv:          kv,
		logger:      logger,
	}
	e.events = eventhub.NewEventHub(e.userClient, inventory.NewFsEventClient(client, conf.SQLiteDB), e.settings)

	e.owner = e.newUser("owner")
	e.editor = e.newUser("editor")
	e.reader = e.newUser("reader")
	e.outsider = e.newUser("outsider")

	// The folder of the owner: Shared/sub, Shared/doc.txt and, next to it, Private/secret.txt.
	owner := e.as(e.owner)
	e.mkdir(owner, "cloudreve://my/Shared/sub")
	e.upload(owner, "cloudreve://my/Shared/doc.txt", 10)
	e.mkdir(owner, "cloudreve://my/Private")
	e.upload(owner, "cloudreve://my/Private/secret.txt", 10)

	shared, err := owner.Get(ctx, e.uri("cloudreve://my/Shared"))
	require.NoError(t, err)
	e.share, err = e.shareClient.Upsert(ctx, &inventory.CreateShareParams{
		OwnerID: e.owner.ID,
		FileID:  shared.ID(),
		Props: &types.ShareProps{
			MemberOnly: true,
			Members: []types.ShareMember{
				{UserID: e.editor.ID, CanEdit: true},
				{UserID: e.reader.ID},
			},
		},
	})
	require.NoError(t, err)
	e.shareID = hashid.EncodeShareID(hasher, e.share.ID)
	return e
}

func (e *sharedFolderEnv) newUser(name string) *ent.User {
	created, err := e.client.User.Create().
		SetEmail(name + "@fylvo.test").
		SetNick(name).
		SetStatus(user.StatusActive).
		SetGroupID(2).
		SetSettings(&types.UserSetting{}).
		Save(e.ctx)
	require.NoError(e.t, err)
	return e.reload(created)
}

// reload returns the user as a request would see it, with group and current storage use.
func (e *sharedFolderEnv) reload(u *ent.User) *ent.User {
	if inventory.IsAnonymousUser(u) {
		return u
	}

	loaded, err := e.userClient.GetByID(context.WithValue(e.ctx, inventory.LoadUserGroup{}, true), u.ID)
	require.NoError(e.t, err)
	return loaded
}

// as returns a fresh file system for the user, as every request gets one.
func (e *sharedFolderEnv) as(u *ent.User) fs.FileSystem {
	return NewDatabaseFS(e.reload(u), e.fileClient, e.shareClient, e.logger, e.locks, e.settings, e.policies,
		e.hasher, e.userClient, e.kv, e.kv, inventory.NewDirectLinkClient(e.client, conf.SQLiteDB, e.hasher), nil, e.events)
}

func (e *sharedFolderEnv) uri(raw string) *fs.URI {
	parsed, err := fs.NewUriFromString(raw)
	require.NoError(e.t, err)
	return parsed
}

// shared returns the address of a path inside the shared folder, as members see it.
func (e *sharedFolderEnv) shared(path string) *fs.URI {
	return e.uri(fs.NewShareUri(e.shareID, "") + path)
}

func (e *sharedFolderEnv) mkdir(f fs.FileSystem, raw string) {
	_, err := f.Create(e.ctx, e.uri(raw), types.FileTypeFolder)
	require.NoError(e.t, err)
}

// tryUpload creates a file the way an upload does, without any content on disk.
func (e *sharedFolderEnv) tryUpload(f fs.FileSystem, target *fs.URI, size int64, metadata map[string]string) error {
	session, err := f.PrepareUpload(e.ctx, &fs.UploadRequest{
		Props: &fs.UploadProps{
			Uri:             target,
			Size:            size,
			UploadSessionID: uuid.Must(uuid.NewV4()).String(),
			ExpireAt:        time.Now().Add(time.Hour),
			Metadata:        metadata,
		},
	})
	if err != nil {
		return err
	}

	_, err = f.CompleteUpload(e.ctx, session)
	return err
}

func (e *sharedFolderEnv) upload(f fs.FileSystem, raw string, size int64) {
	require.NoError(e.t, e.tryUpload(f, e.uri(raw), size, nil))
}

func (e *sharedFolderEnv) names(f fs.FileSystem, target *fs.URI) ([]string, error) {
	_, res, err := f.List(e.ctx, target, fs.WithPageSize(100))
	if err != nil {
		return nil, err
	}

	names := make([]string, 0, len(res.Files))
	for _, file := range res.Files {
		names = append(names, file.Name())
	}
	return names, nil
}

func TestSharedFolderIsOpenToMembersOnly(t *testing.T) {
	e := newSharedFolderEnv(t)

	for _, member := range []*ent.User{e.owner, e.editor, e.reader} {
		names, err := e.names(e.as(member), e.shared(""))
		require.NoError(t, err, member.Nick)
		require.ElementsMatch(t, []string{"sub", "doc.txt"}, names, member.Nick)

		_, err = e.as(member).Get(e.ctx, e.shared("/doc.txt"), WithRequiredCapabilities(NavigatorCapabilityDownloadFile))
		require.NoError(t, err, member.Nick)
	}

	anonymous, err := e.userClient.AnonymousUser(e.ctx)
	require.NoError(t, err)
	for _, stranger := range []*ent.User{e.outsider, anonymous} {
		_, err := e.names(e.as(stranger), e.shared(""))
		require.ErrorIs(t, err, ErrShareNotFound, stranger.Nick)

		// Not through any of the ways that read a file either.
		f := e.as(stranger)
		_, err = f.Get(e.ctx, e.shared("/doc.txt"), WithRequiredCapabilities(NavigatorCapabilityDownloadFile))
		require.Error(t, err, stranger.Nick)
		require.Error(t, f.CheckCapability(e.ctx, e.shared("/doc.txt"), WithRequiredCapabilities(NavigatorCapabilityGenerateThumb)), stranger.Nick)
		require.Error(t, f.Walk(e.ctx, e.shared(""), 5, func(fs.File, int) error { return nil }), stranger.Nick)
	}
}

func TestSharedFolderDoesNotReachOutside(t *testing.T) {
	e := newSharedFolderEnv(t)

	for _, member := range []*ent.User{e.editor, e.reader} {
		f := e.as(member)
		_, err := f.Get(e.ctx, e.shared("/../Private/secret.txt"))
		require.Error(t, err, member.Nick)
		_, err = f.Get(e.ctx, e.uri("cloudreve://my/Private/secret.txt"))
		require.Error(t, err, member.Nick)
		_, err = f.Get(e.ctx, e.uri("cloudreve://"+hashid.EncodeUserID(e.hasher, e.owner.ID)+"@my/Private/secret.txt"))
		require.Error(t, err, member.Nick)
	}
}

func TestRemovedMemberLosesAccessAtOnce(t *testing.T) {
	e := newSharedFolderEnv(t)

	// The clients send a hint so that the server keeps what it has resolved between
	// requests. Access to a shared folder must not be kept that way.
	hinted := context.WithValue(e.ctx, ContextHintCtxKey{}, uuid.Must(uuid.NewV4()))
	f := e.as(e.editor)
	_, _, err := f.List(hinted, e.shared(""), fs.WithPageSize(100))
	require.NoError(t, err)
	f.Recycle()

	_, err = e.client.Share.UpdateOne(e.share).SetProps(&types.ShareProps{MemberOnly: true}).Save(e.ctx)
	require.NoError(t, err)

	_, _, err = e.as(e.editor).List(hinted, e.shared(""), fs.WithPageSize(100))
	require.ErrorIs(t, err, ErrShareNotFound)
	// A share without members stays closed, it does not turn into a link.
	_, err = e.names(e.as(e.outsider), e.shared(""))
	require.ErrorIs(t, err, ErrShareNotFound)
}

func TestEditorWorksInSharedFolder(t *testing.T) {
	e := newSharedFolderEnv(t)
	editor := e.as(e.editor)

	_, err := editor.Create(e.ctx, e.shared("/new folder"), types.FileTypeFolder)
	require.NoError(t, err)
	require.NoError(t, e.tryUpload(e.as(e.editor), e.shared("/new folder/report.txt"), 100, nil))
	// A new version of a file that is there already.
	version := types.EntityTypeVersion
	session, err := e.as(e.editor).PrepareUpload(e.ctx, &fs.UploadRequest{Props: &fs.UploadProps{
		Uri: e.shared("/doc.txt"), Size: 20, UploadSessionID: uuid.Must(uuid.NewV4()).String(),
		ExpireAt: time.Now().Add(time.Hour), EntityType: &version,
	}})
	require.NoError(t, err)
	_, err = e.as(e.editor).CompleteUpload(e.ctx, session)
	require.NoError(t, err)

	_, _, err = e.as(e.editor).Rename(e.ctx, e.shared("/new folder/report.txt"), "final.txt")
	require.NoError(t, err)
	_, err = e.as(e.editor).MoveOrCopy(e.ctx, []*fs.URI{e.shared("/new folder/final.txt")}, e.shared("/sub"), false)
	require.NoError(t, err)
	_, err = e.as(e.editor).MoveOrCopy(e.ctx, []*fs.URI{e.shared("/sub/final.txt")}, e.shared("/new folder"), true)
	require.NoError(t, err)

	// Everything in the folder belongs to the owner and counts against the owner's storage.
	created, err := e.as(e.owner).Get(e.ctx, e.uri("cloudreve://my/Shared/sub/final.txt"))
	require.NoError(t, err)
	require.Equal(t, e.owner.ID, created.OwnerID())
	require.Zero(t, e.reload(e.editor).Storage)
	// secret.txt, the new version of doc.txt (the old one is not kept), report.txt and its copy.
	require.Equal(t, int64(10+20+100+100), e.reload(e.owner).Storage)

	// What the editor deletes lands in the trash of the owner, who can bring it back.
	require.NoError(t, e.as(e.editor).SoftDelete(e.ctx, e.shared("/sub/final.txt")))
	names, err := e.names(e.as(e.editor), e.shared("/sub"))
	require.NoError(t, err)
	require.Empty(t, names)
	trash, err := e.as(e.owner).AllFilesInTrashBin(e.ctx, fs.WithPageSize(100))
	require.NoError(t, err)
	require.Len(t, trash.Files, 1)
	require.NoError(t, e.as(e.owner).Restore(e.ctx, trash.Files[0].Uri(false)))
	names, err = e.names(e.as(e.owner), e.uri("cloudreve://my/Shared/sub"))
	require.NoError(t, err)
	require.Equal(t, []string{"final.txt"}, names)
}

// refusal is an attempt that must fail, and the reason it must fail for.
type refusal struct {
	reason string
	try    func(f fs.FileSystem) error
}

// The reasons, as the errors word them.
const (
	ownerOnly    = "Only owner or administrator can perform this action"
	notSupported = "Not supported action"
	noAccess     = "Permission denied"
	noShare      = "Shared file does not exist"
)

func TestEditorIsHeldToTheSharedFolder(t *testing.T) {
	e := newSharedFolderEnv(t)
	e.mkdir(e.as(e.editor), "cloudreve://my/own")
	e.upload(e.as(e.editor), "cloudreve://my/own/mine.txt", 5)

	refused := map[string]refusal{
		"delete for good": {ownerOnly, func(f fs.FileSystem) error {
			_, _, err := f.Delete(e.ctx, []*fs.URI{e.shared("/doc.txt")})
			return err
		}},
		"manage versions": {ownerOnly, func(f fs.FileSystem) error {
			_, err := f.VersionControl(e.ctx, e.shared("/doc.txt"), 1, true)
			return err
		}},
		"change metadata": {notSupported, func(f fs.FileSystem) error {
			return f.PatchMetadata(e.ctx, []*fs.URI{e.shared("/doc.txt")}, fs.MetadataPatch{Key: "customize:icon_color", Value: "#fff"})
		}},
		"change props": {ownerOnly, func(f fs.FileSystem) error {
			return f.PatchProps(e.ctx, e.shared("/sub"), &types.FileProps{View: &types.ExplorerView{View: "list"}}, false)
		}},
		"create with metadata": {ownerOnly, func(f fs.FileSystem) error {
			_, err := f.Create(e.ctx, e.shared("/tagged"), types.FileTypeFolder, fs.WithMetadata(map[string]string{"customize:icon_color": "#fff"}))
			return err
		}},
		"plant a shortcut": {ownerOnly, func(f fs.FileSystem) error {
			_, err := f.Create(e.ctx, e.shared("/shortcut"), types.FileTypeFolder, WithSymbolicLink(),
				fs.WithMetadata(map[string]string{MetadataSharedRedirect: fs.NewShareUri(e.shareID, "")}))
			return err
		}},
		"upload with metadata": {ownerOnly, func(f fs.FileSystem) error {
			return e.tryUpload(f, e.shared("/tagged.txt"), 1, map[string]string{"customize:icon_color": "#fff"})
		}},
		"rename the shared folder itself": {notSupported, func(f fs.FileSystem) error {
			_, _, err := f.Rename(e.ctx, e.shared(""), "renamed")
			return err
		}},
		"delete the shared folder itself": {notSupported, func(f fs.FileSystem) error {
			return f.SoftDelete(e.ctx, e.shared(""))
		}},
		"move out of the folder": {notSupported, func(f fs.FileSystem) error {
			_, err := f.MoveOrCopy(e.ctx, []*fs.URI{e.shared("/doc.txt")}, e.uri("cloudreve://my/own"), false)
			return err
		}},
		"move into the folder": {notSupported, func(f fs.FileSystem) error {
			_, err := f.MoveOrCopy(e.ctx, []*fs.URI{e.uri("cloudreve://my/own/mine.txt")}, e.shared("/sub"), false)
			return err
		}},
		"write next to the folder": {noAccess, func(f fs.FileSystem) error {
			owner := hashid.EncodeUserID(e.hasher, e.owner.ID)
			return e.tryUpload(f, e.uri("cloudreve://"+owner+"@my/Private/planted.txt"), 1, nil)
		}},
		"restore, which a share does not offer": {notSupported, func(f fs.FileSystem) error {
			return f.Restore(e.ctx, e.shared("/doc.txt"))
		}},
	}

	for name, attempt := range refused {
		require.ErrorContains(t, attempt.try(e.as(e.editor)), attempt.reason, name)
	}

	// Nothing of it happened.
	names, err := e.names(e.as(e.owner), e.uri("cloudreve://my/Shared"))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"sub", "doc.txt"}, names)
	names, err = e.names(e.as(e.owner), e.uri("cloudreve://my/Private"))
	require.NoError(t, err)
	require.Equal(t, []string{"secret.txt"}, names)
	names, err = e.names(e.as(e.editor), e.uri("cloudreve://my/own"))
	require.NoError(t, err)
	require.Equal(t, []string{"mine.txt"}, names)
}

func TestReaderCannotChangeAnything(t *testing.T) {
	e := newSharedFolderEnv(t)

	refused := map[string]refusal{
		"create a folder": {notSupported, func(f fs.FileSystem) error {
			_, err := f.Create(e.ctx, e.shared("/new"), types.FileTypeFolder)
			return err
		}},
		"upload a file": {notSupported, func(f fs.FileSystem) error {
			return e.tryUpload(f, e.shared("/new.txt"), 1, nil)
		}},
		"check an upload": {notSupported, func(f fs.FileSystem) error {
			return f.PreValidateUpload(e.ctx, e.shared(""), fs.PreValidateFile{Name: "new.txt", Size: 1})
		}},
		"overwrite a file": {notSupported, func(f fs.FileSystem) error {
			version := types.EntityTypeVersion
			_, err := f.PrepareUpload(e.ctx, &fs.UploadRequest{Props: &fs.UploadProps{
				Uri: e.shared("/doc.txt"), Size: 1, UploadSessionID: uuid.Must(uuid.NewV4()).String(),
				ExpireAt: time.Now().Add(time.Hour), EntityType: &version,
			}})
			return err
		}},
		"rename": {notSupported, func(f fs.FileSystem) error {
			_, _, err := f.Rename(e.ctx, e.shared("/doc.txt"), "other.txt")
			return err
		}},
		"move": {ownerOnly, func(f fs.FileSystem) error {
			_, err := f.MoveOrCopy(e.ctx, []*fs.URI{e.shared("/doc.txt")}, e.shared("/sub"), false)
			return err
		}},
		"copy": {ownerOnly, func(f fs.FileSystem) error {
			_, err := f.MoveOrCopy(e.ctx, []*fs.URI{e.shared("/doc.txt")}, e.shared("/sub"), true)
			return err
		}},
		"delete": {notSupported, func(f fs.FileSystem) error {
			return f.SoftDelete(e.ctx, e.shared("/doc.txt"))
		}},
		"delete for good": {notSupported, func(f fs.FileSystem) error {
			_, _, err := f.Delete(e.ctx, []*fs.URI{e.shared("/doc.txt")})
			return err
		}},
		"lock": {ownerOnly, func(f fs.FileSystem) error {
			_, err := f.Lock(e.ctx, time.Minute, e.reader, true, lock.Application{Type: "viewer"}, e.shared("/doc.txt"), "token")
			return err
		}},
	}

	for _, stranger := range []*ent.User{e.reader, e.outsider} {
		for name, attempt := range refused {
			reason := attempt.reason
			if stranger == e.outsider {
				reason = noShare
			}
			require.ErrorContains(t, attempt.try(e.as(stranger)), reason, stranger.Nick+": "+name)
		}
	}

	names, err := e.names(e.as(e.owner), e.uri("cloudreve://my/Shared"))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"sub", "doc.txt"}, names)

	// Reading everything below the folder in one go is fine, WebDAV and ZIP download need it.
	walked := 0
	require.NoError(t, e.as(e.reader).Walk(e.ctx, e.shared(""), 5, func(fs.File, int) error {
		walked++
		return nil
	}))
	require.Equal(t, 3, walked)
}

func TestUploadCannotBeFinishedAfterLosingTheRight(t *testing.T) {
	e := newSharedFolderEnv(t)

	session, err := e.as(e.editor).PrepareUpload(e.ctx, &fs.UploadRequest{Props: &fs.UploadProps{
		Uri: e.shared("/late.txt"), Size: 1, UploadSessionID: uuid.Must(uuid.NewV4()).String(), ExpireAt: time.Now().Add(time.Hour),
	}})
	require.NoError(t, err)

	_, err = e.client.Share.UpdateOne(e.share).SetProps(&types.ShareProps{
		MemberOnly: true,
		Members:    []types.ShareMember{{UserID: e.editor.ID}},
	}).Save(e.ctx)
	require.NoError(t, err)

	_, err = e.as(e.editor).CompleteUpload(e.ctx, session)
	require.ErrorContains(t, err, ownerOnly)
}

func TestCancelledUploadTakesOnlyItsOwnFile(t *testing.T) {
	e := newSharedFolderEnv(t)
	begin := func(target *fs.URI, entityType *types.EntityType) *fs.UploadSession {
		session, err := e.as(e.editor).PrepareUpload(e.ctx, &fs.UploadRequest{Props: &fs.UploadProps{
			Uri: target, Size: 1, UploadSessionID: uuid.Must(uuid.NewV4()).String(), ExpireAt: time.Now().Add(time.Hour), EntityType: entityType,
		}})
		require.NoError(t, err)
		return session
	}
	cancel := func(target *fs.URI, session *fs.UploadSession) {
		_, _, err := e.as(e.editor).CancelUploadSession(e.ctx, target, session.Props.UploadSessionID, session)
		require.NoError(t, err)
	}

	// An empty file of the owner holds nothing. A new version an editor starts to upload is
	// then all there is to it, and cancelling that upload must leave the file where it is.
	_, err := e.as(e.owner).Create(e.ctx, e.uri("cloudreve://my/Shared/empty.txt"), types.FileTypeFile)
	require.NoError(t, err)
	version := types.EntityTypeVersion
	cancel(e.shared("/empty.txt"), begin(e.shared("/empty.txt"), &version))

	// A file that the upload created goes with it.
	cancel(e.shared("/fresh.txt"), begin(e.shared("/fresh.txt"), nil))

	names, err := e.names(e.as(e.owner), e.uri("cloudreve://my/Shared"))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"sub", "doc.txt", "empty.txt"}, names)
}

func TestCancelledUploadLeavesAFileThatTookItsPlace(t *testing.T) {
	e := newSharedFolderEnv(t)
	session, err := e.as(e.editor).PrepareUpload(e.ctx, &fs.UploadRequest{Props: &fs.UploadProps{
		Uri: e.shared("/fresh.txt"), Size: 1, UploadSessionID: uuid.Must(uuid.NewV4()).String(), ExpireAt: time.Now().Add(time.Hour),
	}})
	require.NoError(t, err)

	// The lock of the upload runs out, and the owner puts a file of their own at that place.
	require.NoError(t, e.as(e.owner).Unlock(e.ctx, session.LockToken))
	_, _, err = e.as(e.owner).Delete(e.ctx, []*fs.URI{e.uri("cloudreve://my/Shared/fresh.txt")})
	require.NoError(t, err)
	e.upload(e.as(e.owner), "cloudreve://my/Shared/fresh.txt", 5)

	// Cleaning up the failed upload leaves that file alone.
	_, _, _ = e.as(e.editor).CancelUploadSession(e.ctx, e.shared("/fresh.txt"), session.Props.UploadSessionID, session)
	file, err := e.as(e.owner).Get(e.ctx, e.uri("cloudreve://my/Shared/fresh.txt"))
	require.NoError(t, err)
	require.EqualValues(t, 5, file.Size())
}

func TestSingleFileShareShowsOnlyThatFile(t *testing.T) {
	e := newSharedFolderEnv(t)
	doc, err := e.as(e.owner).Get(e.ctx, e.uri("cloudreve://my/Shared/doc.txt"))
	require.NoError(t, err)
	link, err := e.shareClient.Upsert(e.ctx, &inventory.CreateShareParams{OwnerID: e.owner.ID, FileID: doc.ID()})
	require.NoError(t, err)
	base := fs.NewShareUri(hashid.EncodeShareID(e.hasher, link.ID), "")
	e.upload(e.as(e.owner), "cloudreve://my/Shared/sub/hidden.txt", 1)

	visitor := e.as(e.outsider)
	_, err = visitor.Get(e.ctx, e.uri(base+"/doc.txt"))
	require.NoError(t, err)

	// The folder that holds the file is not shared: nothing next to or below the file.
	_, err = e.as(e.outsider).Get(e.ctx, e.uri(base+"/sub/hidden.txt"))
	require.Error(t, err)
	_, err = e.as(e.outsider).Get(e.ctx, e.uri(base+"/sub"))
	require.Error(t, err)
	require.Error(t, e.tryUpload(e.as(e.outsider), e.uri(base+"/planted.txt"), 1, nil))

	// A member list on a share of a single file never gives the right to edit.
	_, err = e.client.Share.UpdateOne(link).SetProps(&types.ShareProps{
		MemberOnly: true,
		Members:    []types.ShareMember{{UserID: e.editor.ID, CanEdit: true}},
	}).Save(e.ctx)
	require.NoError(t, err)
	require.Error(t, e.tryUpload(e.as(e.editor), e.uri(base+"/planted.txt"), 1, nil))
	_, _, err = e.as(e.editor).Rename(e.ctx, e.uri(base+"/doc.txt"), "renamed.txt")
	require.Error(t, err)
}

func TestSharedFolderIsListedForMembers(t *testing.T) {
	e := newSharedFolderEnv(t)
	sharedWithMe := e.uri("cloudreve://shared_with_me")

	for _, member := range []*ent.User{e.editor, e.reader} {
		_, res, err := e.as(member).List(e.ctx, sharedWithMe, fs.WithPageSize(100))
		require.NoError(t, err, member.Nick)
		require.Len(t, res.Files, 1, member.Nick)
		require.Equal(t, "Shared", res.Files[0].Name())
		require.True(t, res.Files[0].IsSymbolic())
		require.Equal(t, fs.NewShareUri(e.shareID, ""), res.Files[0].Metadata()[MetadataSharedRedirect])
	}

	for _, other := range []*ent.User{e.owner, e.outsider} {
		names, err := e.names(e.as(other), sharedWithMe)
		require.NoError(t, err, other.Nick)
		require.Empty(t, names, other.Nick)
	}

	// A folder in the trash of its owner is not offered any more.
	require.NoError(t, e.as(e.owner).SoftDelete(e.ctx, e.uri("cloudreve://my/Shared")))
	names, err := e.names(e.as(e.editor), sharedWithMe)
	require.NoError(t, err)
	require.Empty(t, names)
	_, err = e.names(e.as(e.editor), e.shared(""))
	require.Error(t, err)
}

func TestMemberSharesStayOutOfProfiles(t *testing.T) {
	e := newSharedFolderEnv(t)
	doc, err := e.as(e.owner).Get(e.ctx, e.uri("cloudreve://my/Shared/doc.txt"))
	require.NoError(t, err)
	link, err := e.shareClient.Upsert(e.ctx, &inventory.CreateShareParams{OwnerID: e.owner.ID, FileID: doc.ID()})
	require.NoError(t, err)

	for _, publicOnly := range []bool{true, false} {
		res, err := e.shareClient.List(e.ctx, &inventory.ListShareArgs{
			PaginationArgs: &inventory.PaginationArgs{PageSize: 50},
			UserID:         e.owner.ID,
			PublicOnly:     publicOnly,
			HideMemberOnly: true,
		})
		require.NoError(t, err)
		require.Len(t, res.Shares, 1)
		require.Equal(t, link.ID, res.Shares[0].ID)
	}
}

// uploadLink creates a link to the shared folder of the owner with the given props and
// returns the address visitors use.
func (e *sharedFolderEnv) uploadLink(props *types.ShareProps) (*ent.Share, func(path string) *fs.URI) {
	shared, err := e.as(e.owner).Get(e.ctx, e.uri("cloudreve://my/Shared"))
	require.NoError(e.t, err)
	link, err := e.shareClient.Upsert(e.ctx, &inventory.CreateShareParams{OwnerID: e.owner.ID, FileID: shared.ID(), Props: props})
	require.NoError(e.t, err)
	base := fs.NewShareUri(hashid.EncodeShareID(e.hasher, link.ID), "")
	return link, func(path string) *fs.URI { return e.uri(base + path) }
}

func (e *sharedFolderEnv) visitors() []*ent.User {
	anonymous, err := e.userClient.AnonymousUser(e.ctx)
	require.NoError(e.t, err)
	return []*ent.User{e.outsider, anonymous}
}

func TestPlainLinkTakesNoUploads(t *testing.T) {
	e := newSharedFolderEnv(t)
	_, at := e.uploadLink(nil)

	for _, visitor := range e.visitors() {
		require.ErrorContains(t, e.tryUpload(e.as(visitor), at("/new.txt"), 1, nil), notSupported, visitor.Nick)
		_, err := e.as(visitor).Create(e.ctx, at("/new"), types.FileTypeFolder)
		require.ErrorContains(t, err, notSupported, visitor.Nick)
	}
}

func TestUploadLinkLetsVisitorsAddButNotChange(t *testing.T) {
	e := newSharedFolderEnv(t)
	link, at := e.uploadLink(&types.ShareProps{AllowUpload: true})

	for i, visitor := range e.visitors() {
		folder := fmt.Sprintf("/from visitor %d", i)
		_, err := e.as(visitor).Create(e.ctx, at(folder), types.FileTypeFolder)
		require.NoError(t, err, visitor.Nick)
		require.NoError(t, e.tryUpload(e.as(visitor), at(folder+"/photo.jpg"), 100, nil), visitor.Nick)

		// A name that is taken: the upload goes next to the file, whether the visitor asks
		// for a new file or for a new version of the one that is there.
		require.NoError(t, e.tryUpload(e.as(visitor), at("/doc.txt"), 7, nil), visitor.Nick)
		version := types.EntityTypeVersion
		session, err := e.as(visitor).PrepareUpload(e.ctx, &fs.UploadRequest{Props: &fs.UploadProps{
			Uri: at("/doc.txt"), Size: 7, UploadSessionID: uuid.Must(uuid.NewV4()).String(),
			ExpireAt: time.Now().Add(time.Hour), EntityType: &version,
		}})
		require.NoError(t, err, visitor.Nick)
		require.NotEqual(t, "doc.txt", session.Props.Uri.Name(), visitor.Nick)
		_, err = e.as(visitor).CompleteUpload(e.ctx, session)
		require.NoError(t, err, visitor.Nick)

		refused := map[string]refusal{
			"rename": {notSupported, func(f fs.FileSystem) error {
				_, _, err := f.Rename(e.ctx, at("/doc.txt"), "other.txt")
				return err
			}},
			"delete": {notSupported, func(f fs.FileSystem) error {
				return f.SoftDelete(e.ctx, at("/doc.txt"))
			}},
			"delete for good": {ownerOnly, func(f fs.FileSystem) error {
				_, _, err := f.Delete(e.ctx, []*fs.URI{at("/doc.txt")})
				return err
			}},
			"move": {ownerOnly, func(f fs.FileSystem) error {
				_, err := f.MoveOrCopy(e.ctx, []*fs.URI{at("/doc.txt")}, at("/sub"), false)
				return err
			}},
			"copy": {ownerOnly, func(f fs.FileSystem) error {
				_, err := f.MoveOrCopy(e.ctx, []*fs.URI{at("/doc.txt")}, at("/sub"), true)
				return err
			}},
			"lock": {ownerOnly, func(f fs.FileSystem) error {
				_, err := f.Lock(e.ctx, time.Minute, visitor, true, lock.Application{Type: "viewer"}, at("/doc.txt"), "token")
				return err
			}},
			"upload with metadata": {ownerOnly, func(f fs.FileSystem) error {
				return e.tryUpload(f, at("/tagged.txt"), 1, map[string]string{"customize:icon_color": "#fff"})
			}},
		}
		for name, attempt := range refused {
			require.ErrorContains(t, attempt.try(e.as(visitor)), attempt.reason, visitor.Nick+": "+name)
		}

		// A path cannot climb out of the folder: this lands in a new folder inside it.
		require.NoError(t, e.tryUpload(e.as(visitor), at("/../Private/planted.txt"), 1, nil), visitor.Nick)
	}

	// The file of the owner is as it was, the uploads lie next to it and belong to the owner.
	doc, err := e.as(e.owner).Get(e.ctx, e.uri("cloudreve://my/Shared/doc.txt"))
	require.NoError(t, err)
	require.Equal(t, int64(10), doc.Size())
	names, err := e.names(e.as(e.owner), e.uri("cloudreve://my/Shared"))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"sub", "doc.txt", "doc (1).txt", "doc (2).txt", "doc (3).txt", "doc (4).txt",
		"from visitor 0", "from visitor 1", "Private"}, names)
	added, err := e.as(e.owner).Get(e.ctx, e.uri("cloudreve://my/Shared/doc (1).txt"))
	require.NoError(t, err)
	require.Equal(t, e.owner.ID, added.OwnerID())
	names, err = e.names(e.as(e.owner), e.uri("cloudreve://my/Private"))
	require.NoError(t, err)
	require.Equal(t, []string{"secret.txt"}, names)

	// What came in is counted on the link and charged to the owner.
	stored, err := e.client.Share.Get(e.ctx, link.ID)
	require.NoError(t, err)
	require.Equal(t, int64(2*(100+7+7+1)), stored.Props.Uploaded)
	require.Equal(t, int64(10+10+2*(100+7+7+1)), e.reload(e.owner).Storage)
	require.Zero(t, e.reload(e.outsider).Storage)
}

func TestDropBoxTakesUploadsAndShowsNothing(t *testing.T) {
	e := newSharedFolderEnv(t)
	_, at := e.uploadLink(&types.ShareProps{AllowUpload: true, HideContent: true})

	for _, visitor := range e.visitors() {
		require.NoError(t, e.tryUpload(e.as(visitor), at("/doc.txt"), 3, nil), visitor.Nick)

		for _, folder := range []string{"", "/sub"} {
			names, err := e.names(e.as(visitor), at(folder))
			require.NoError(t, err, visitor.Nick)
			require.Empty(t, names, visitor.Nick)
		}

		f := e.as(visitor)
		for name, capability := range map[string]NavigatorCapability{
			"download": NavigatorCapabilityDownloadFile, "details": NavigatorCapabilityInfo, "preview": NavigatorCapabilityGenerateThumb,
		} {
			_, err := f.Get(e.ctx, at("/doc.txt"), WithRequiredCapabilities(capability))
			require.ErrorContains(t, err, notSupported, visitor.Nick+": "+name)
		}
		require.Error(t, f.Walk(e.ctx, at(""), 5, func(fs.File, int) error { return nil }), visitor.Nick)
	}

	// The owner still sees everything through the same link.
	names, err := e.names(e.as(e.owner), at(""))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"sub", "doc.txt", "doc (1).txt", "doc (2).txt"}, names)
}

func TestUploadLinkStopsAtItsLimit(t *testing.T) {
	e := newSharedFolderEnv(t)
	link, at := e.uploadLink(&types.ShareProps{AllowUpload: true, UploadLimit: 150})

	require.NoError(t, e.tryUpload(e.as(e.outsider), at("/first.bin"), 100, nil))
	require.ErrorContains(t, e.tryUpload(e.as(e.outsider), at("/second.bin"), 100, nil), "upload limit of this link")
	require.NoError(t, e.tryUpload(e.as(e.outsider), at("/third.bin"), 50, nil))

	stored, err := e.client.Share.Get(e.ctx, link.ID)
	require.NoError(t, err)
	require.Equal(t, int64(150), stored.Props.Uploaded)
	// The owner and the members of the folder are not held to the limit of a link.
	require.NoError(t, e.tryUpload(e.as(e.owner), e.uri("cloudreve://my/Shared/own.bin"), 100, nil))
	require.NoError(t, e.tryUpload(e.as(e.editor), e.shared("/member.bin"), 100, nil))
}

func TestDropBoxAnswersNothingAboutAFile(t *testing.T) {
	e := newSharedFolderEnv(t)
	_, at := e.uploadLink(&types.ShareProps{AllowUpload: true, HideContent: true})

	for _, visitor := range e.visitors() {
		// Listing the address of a file would answer with the file as the listed place.
		_, _, err := e.as(visitor).List(e.ctx, at("/doc.txt"), fs.WithPageSize(10))
		require.Error(t, err, visitor.Nick)
		_, _, err = e.as(visitor).List(e.ctx, at("/no such file.txt"), fs.WithPageSize(10))
		require.Error(t, err, visitor.Nick)
	}
}

func TestLinkSetToHideItsFolderHidesItAtOnce(t *testing.T) {
	e := newSharedFolderEnv(t)
	link, at := e.uploadLink(nil)

	// The visitor's client sends a hint, so the server keeps the opened share for some minutes.
	hinted := context.WithValue(e.ctx, ContextHintCtxKey{}, uuid.Must(uuid.NewV4()))
	open := func() ([]string, error) {
		f := e.as(e.outsider)
		defer f.Recycle()
		_, res, err := f.List(hinted, at(""), fs.WithPageSize(100))
		if err != nil {
			return nil, err
		}
		names := make([]string, 0, len(res.Files))
		for _, file := range res.Files {
			names = append(names, file.Name())
		}
		return names, nil
	}

	for i := 0; i < 2; i++ {
		names, err := open()
		require.NoError(t, err)
		require.ElementsMatch(t, []string{"sub", "doc.txt"}, names)
	}

	_, err := e.client.Share.UpdateOne(link).SetProps(&types.ShareProps{AllowUpload: true, HideContent: true}).Save(e.ctx)
	require.NoError(t, err)
	names, err := open()
	require.NoError(t, err)
	require.Empty(t, names)
	f := e.as(e.outsider)
	_, err = f.Get(hinted, at("/doc.txt"), WithRequiredCapabilities(NavigatorCapabilityDownloadFile))
	require.ErrorContains(t, err, notSupported)

	// And a deleted link is gone at once, too.
	require.NoError(t, e.shareClient.Delete(e.ctx, link.ID))
	_, err = open()
	require.ErrorContains(t, err, noShare)
}

func TestTakenNameAsLongAsNamesMayBe(t *testing.T) {
	e := newSharedFolderEnv(t)
	_, at := e.uploadLink(&types.ShareProps{AllowUpload: true})
	long := "/" + strings.Repeat("a", 251) + ".txt"

	require.NoError(t, e.tryUpload(e.as(e.owner), e.uri("cloudreve://my/Shared"+long), 1, nil))
	require.NoError(t, e.tryUpload(e.as(e.outsider), at(long), 1, nil))
	require.NoError(t, e.tryUpload(e.as(e.outsider), at(long), 1, nil))
	names, err := e.names(e.as(e.owner), e.uri("cloudreve://my/Shared"))
	require.NoError(t, err)
	require.Len(t, names, 5)
}

func TestMemberReleasesOnlyLocksItMayChange(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner, editor, reader := e.as(e.owner), e.as(e.editor), e.as(e.reader)

	// A shared folder below a folder of the owner that is not shared: Outer/Inner.
	e.mkdir(owner, "cloudreve://my/Outer/Inner")
	e.upload(owner, "cloudreve://my/Outer/Inner/doc.txt", 10)
	inner, err := owner.Get(e.ctx, e.uri("cloudreve://my/Outer/Inner"))
	require.NoError(t, err)
	share, err := e.shareClient.Upsert(e.ctx, &inventory.CreateShareParams{
		OwnerID: e.owner.ID,
		FileID:  inner.ID(),
		Props: &types.ShareProps{
			MemberOnly: true,
			Members:    []types.ShareMember{{UserID: e.editor.ID, CanEdit: true}, {UserID: e.reader.ID}},
		},
	})
	require.NoError(t, err)
	innerUri := func(path string) *fs.URI {
		return e.uri(fs.NewShareUri(hashid.EncodeShareID(e.hasher, share.ID), "") + path)
	}

	// The owner locks the folder above the shared one, as a WebDAV client does.
	ownerLock, err := owner.Lock(e.ctx, -1, e.owner, false, fs.LockApp(fs.ApplicationDAV), e.uri("cloudreve://my/Outer"), "")
	require.NoError(t, err)
	ownerToken := ownerLock.LastToken()

	// The office editor of a member runs into that lock and learns neither its token nor where it lies.
	_, err = editor.Lock(e.ctx, time.Minute, e.editor, true, fs.LockApp(fs.ApplicationViewer), innerUri("/doc.txt"), "office-token")
	var conflicts lock.ConflictError
	require.ErrorAs(t, err, &conflicts)
	require.Empty(t, conflicts[0].Token)
	require.Empty(t, conflicts[0].Path)

	// Even with the token, a member releases no lock that only covers the shared folder from above.
	doc, err := editor.Get(e.ctx, innerUri("/doc.txt"))
	require.NoError(t, err)
	root, err := editor.Get(e.ctx, innerUri(""))
	require.NoError(t, err)
	require.Error(t, editor.UnlockAt(e.ctx, doc, doc.Uri(false), ownerToken))
	require.Error(t, editor.UnlockAt(e.ctx, root, root.Uri(false), ownerToken))
	_, err = editor.Lock(e.ctx, time.Minute, e.editor, true, fs.LockApp(fs.ApplicationViewer), innerUri("/doc.txt"), "")
	require.ErrorIs(t, err, lock.ErrLocked, "the lock of the owner is still there")

	// The owner releases it among the own files.
	myRoot, err := owner.Get(e.ctx, e.uri("cloudreve://my"))
	require.NoError(t, err)
	require.NoError(t, owner.UnlockAt(e.ctx, myRoot, myRoot.Uri(false), ownerToken))

	// A lock on a file in the shared folder: an editor releases it, a reader does not.
	memberLock, err := editor.Lock(e.ctx, time.Minute, e.editor, true, fs.LockApp(fs.ApplicationViewer), innerUri("/doc.txt"), "")
	require.NoError(t, err)
	readerDoc, err := reader.Get(e.ctx, innerUri("/doc.txt"))
	require.NoError(t, err)
	require.ErrorIs(t, reader.UnlockAt(e.ctx, readerDoc, readerDoc.Uri(false), memberLock.LastToken()), fs.ErrOwnerOnly)
	require.NoError(t, editor.UnlockAt(e.ctx, doc, doc.Uri(false), memberLock.LastToken()))

	// The token of a lock that lies elsewhere is unknown here, also to the owner.
	otherLock, err := owner.Lock(e.ctx, time.Minute, e.owner, true, fs.LockApp(fs.ApplicationDAV), e.uri("cloudreve://my/Shared/doc.txt"), "")
	require.NoError(t, err)
	ownerDoc, err := owner.Get(e.ctx, e.uri("cloudreve://my/Outer/Inner/doc.txt"))
	require.NoError(t, err)
	require.ErrorIs(t, owner.UnlockAt(e.ctx, ownerDoc, ownerDoc.Uri(false), otherLock.LastToken()), lock.ErrNoSuchLock)
}

func TestCopyBetweenOwnFilesAndSharedFolder(t *testing.T) {
	e := newSharedFolderEnv(t)
	editor := e.as(e.editor)
	e.upload(editor, "cloudreve://my/own/mine.txt", 5)
	e.upload(editor, "cloudreve://my/own/dir/inner.txt", 3)
	require.NoError(t, editor.PatchMetadata(e.ctx, []*fs.URI{e.uri("cloudreve://my/own/mine.txt")},
		fs.MetadataPatch{Key: "customize:icon_color", Value: "#fff"},
		fs.MetadataPatch{Key: MetadataSharedRedirect, Value: fs.NewShareUri(e.shareID, "")}))
	storage := func(u *ent.User) int64 { return e.reload(u).Storage }
	ownerBefore, editorBefore := storage(e.owner), storage(e.editor)

	// Into the shared folder: the copy belongs to the owner and counts for the owner.
	_, err := editor.MoveOrCopy(e.ctx, []*fs.URI{e.uri("cloudreve://my/own/mine.txt"), e.uri("cloudreve://my/own/dir")}, e.shared("/sub"), true)
	require.NoError(t, err)
	copied, err := e.as(e.owner).Get(e.ctx, e.uri("cloudreve://my/Shared/sub/mine.txt"), WithFilePublicMetadata())
	require.NoError(t, err)
	require.Equal(t, e.owner.ID, copied.OwnerID())
	require.Equal(t, "#fff", copied.Metadata()["customize:icon_color"])
	require.NotContains(t, copied.Metadata(), MetadataSharedRedirect, "no shortcut target is planted in the tree of the owner")
	inner, err := e.as(e.owner).Get(e.ctx, e.uri("cloudreve://my/Shared/sub/dir/inner.txt"))
	require.NoError(t, err)
	require.Equal(t, e.owner.ID, inner.OwnerID())
	require.Equal(t, ownerBefore+8, storage(e.owner))
	require.Equal(t, editorBefore, storage(e.editor))

	// Out of the shared folder: the copy belongs to the editor and counts for the editor. It
	// takes the current version only, older versions stay with the owner.
	_, err = e.client.User.UpdateOne(e.owner).SetSettings(&types.UserSetting{VersionRetention: true}).Save(e.ctx)
	require.NoError(t, err)
	version := types.EntityTypeVersion
	editor = e.as(e.editor)
	session, err := editor.PrepareUpload(e.ctx, &fs.UploadRequest{Props: &fs.UploadProps{
		Uri: e.shared("/doc.txt"), Size: 10, UploadSessionID: uuid.Must(uuid.NewV4()).String(), ExpireAt: time.Now().Add(time.Hour), EntityType: &version,
	}})
	require.NoError(t, err)
	_, err = editor.CompleteUpload(e.ctx, session)
	require.NoError(t, err)
	ownerBefore = storage(e.owner)
	versions, err := e.as(e.owner).Get(e.ctx, e.uri("cloudreve://my/Shared/doc.txt"), WithFileEntities())
	require.NoError(t, err)
	require.Len(t, versions.Entities(), 2, "the owner keeps versions")
	_, err = editor.MoveOrCopy(e.ctx, []*fs.URI{e.shared("/doc.txt")}, e.uri("cloudreve://my/own"), true)
	require.NoError(t, err)
	out, err := editor.Get(e.ctx, e.uri("cloudreve://my/own/doc.txt"), WithFileEntities())
	require.NoError(t, err)
	require.Equal(t, e.editor.ID, out.OwnerID())
	require.Len(t, out.Entities(), 1)
	require.Equal(t, editorBefore+10, storage(e.editor))

	// The copies stay when the original goes, and the original when a copy goes.
	_, _, err = e.as(e.owner).Delete(e.ctx, []*fs.URI{e.uri("cloudreve://my/Shared/doc.txt")})
	require.NoError(t, err)
	_, err = editor.Get(e.ctx, e.uri("cloudreve://my/own/doc.txt"), WithFileEntities())
	require.NoError(t, err)
	require.Equal(t, editorBefore+10, storage(e.editor))

	// A folder with a shortcut in it is not copied into the tree of somebody else.
	_, err = editor.Create(e.ctx, e.uri("cloudreve://my/own/links/shortcut"), types.FileTypeFolder, WithSymbolicLink(),
		fs.WithMetadata(map[string]string{MetadataSharedRedirect: fs.NewShareUri(e.shareID, "")}))
	require.NoError(t, err)
	_, err = editor.MoveOrCopy(e.ctx, []*fs.URI{e.uri("cloudreve://my/own/links")}, e.shared(""), true)
	require.ErrorContains(t, err, ownerOnly)

	// A reader copies neither in nor out, a visitor of an upload link neither.
	e.upload(e.as(e.reader), "cloudreve://my/mine.txt", 1)
	_, err = e.as(e.reader).MoveOrCopy(e.ctx, []*fs.URI{e.shared("/sub/mine.txt")}, e.uri("cloudreve://my"), true)
	require.ErrorContains(t, err, ownerOnly)
	_, err = e.as(e.reader).MoveOrCopy(e.ctx, []*fs.URI{e.uri("cloudreve://my/mine.txt")}, e.shared(""), true)
	require.ErrorContains(t, err, ownerOnly)
	_, at := e.uploadLink(&types.ShareProps{AllowUpload: true})
	e.upload(e.as(e.outsider), "cloudreve://my/mine.txt", 1)
	_, err = e.as(e.outsider).MoveOrCopy(e.ctx, []*fs.URI{at("/sub/mine.txt")}, e.uri("cloudreve://my"), true)
	require.ErrorContains(t, err, ownerOnly)
	_, err = e.as(e.outsider).MoveOrCopy(e.ctx, []*fs.URI{e.uri("cloudreve://my/mine.txt")}, at(""), true)
	require.ErrorContains(t, err, ownerOnly)

	names, err := e.names(e.as(e.owner), e.uri("cloudreve://my/Shared"))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"sub"}, names)
}

func TestCancelledVersionKeepsTheThumbnailSetting(t *testing.T) {
	e := newSharedFolderEnv(t)
	owner := e.as(e.owner)
	require.NoError(t, owner.PatchMetadata(e.ctx, []*fs.URI{e.uri("cloudreve://my/Shared/doc.txt")},
		fs.MetadataPatch{Key: ThumbDisabledKey, Value: ""}))

	// An editor starts a new version of the file and the upload fails.
	version := types.EntityTypeVersion
	session, err := e.as(e.editor).PrepareUpload(e.ctx, &fs.UploadRequest{Props: &fs.UploadProps{
		Uri: e.shared("/doc.txt"), Size: 1, UploadSessionID: uuid.Must(uuid.NewV4()).String(), ExpireAt: time.Now().Add(time.Hour), EntityType: &version,
	}})
	require.NoError(t, err)
	_, _, err = e.as(e.editor).CancelUploadSession(e.ctx, e.shared("/doc.txt"), session.Props.UploadSessionID, session)
	require.NoError(t, err)

	// The file keeps its setting: no new version came, so nothing changed for its thumbnails.
	file, err := owner.Get(e.ctx, e.uri("cloudreve://my/Shared/doc.txt"), WithFileEntities(), WithFilePublicMetadata())
	require.NoError(t, err)
	require.Contains(t, file.Metadata(), ThumbDisabledKey)
	require.Len(t, file.Entities(), 1)
}
