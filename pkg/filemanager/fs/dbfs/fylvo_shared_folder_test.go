package dbfs

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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

	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	t.Cleanup(func() { _ = client.Close() })
	client, err := inventory.InitializeDBClient(logger, client, kv, "4.19.1")
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
		settings:    setting.NewProvider(setting.NewDbSettingStore(inventory.NewSettingClient(client, kv), nil)),
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
		"copy out of the folder": {notSupported, func(f fs.FileSystem) error {
			_, err := f.MoveOrCopy(e.ctx, []*fs.URI{e.shared("/doc.txt")}, e.uri("cloudreve://my/own"), true)
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
