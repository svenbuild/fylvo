package manager

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/dbfs"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

// Fylvo: tests for the clean-up of abandoned uploads.

// staleEnv is a small Fylvo with an owner, a member who may edit the folder "Shared" of the owner, and
// an upload link to that folder for visitors without an account.
type staleEnv struct {
	t      *testing.T
	ctx    context.Context
	dep    dependency.Dep
	client *ent.Client
	kv     cache.Driver

	owner, member, visitor *ent.User
	sharedAt, linkAt       func(path string) *fs.URI
}

func newStaleEnv(t *testing.T) *staleEnv {
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
	ctx := context.WithValue(context.Background(), dependency.DepCtx{}, dep)
	ctx = context.WithValue(ctx, logging.LoggerCtx{}, logger)

	e := &staleEnv{t: t, ctx: ctx, dep: dep, client: client, kv: kv}
	e.owner = e.newUser("owner")
	e.member = e.newUser("member")
	e.visitor, err = dep.UserClient().AnonymousUser(ctx)
	require.NoError(t, err)

	_, err = e.fs(e.owner).Create(ctx, e.uri("cloudreve://my/Shared"), types.FileTypeFolder)
	require.NoError(t, err)
	e.finish(e.beginFor(e.owner, e.uri("cloudreve://my/Shared/doc.txt"), 10, nil, time.Hour))

	shared, err := e.fs(e.owner).Get(ctx, e.uri("cloudreve://my/Shared"))
	require.NoError(t, err)
	members, err := dep.ShareClient().Upsert(ctx, &inventory.CreateShareParams{OwnerID: e.owner.ID, FileID: shared.ID(),
		Props: &types.ShareProps{MemberOnly: true, Members: []types.ShareMember{{UserID: e.member.ID, CanEdit: true}}}})
	require.NoError(t, err)
	link, err := dep.ShareClient().Upsert(ctx, &inventory.CreateShareParams{OwnerID: e.owner.ID, FileID: shared.ID(),
		Props: &types.ShareProps{AllowUpload: true}})
	require.NoError(t, err)
	e.sharedAt = func(path string) *fs.URI {
		return e.uri(fs.NewShareUri(hashid.EncodeShareID(hasher, members.ID), "") + path)
	}
	e.linkAt = func(path string) *fs.URI {
		return e.uri(fs.NewShareUri(hashid.EncodeShareID(hasher, link.ID), "") + path)
	}
	return e
}

func (e *staleEnv) newUser(name string) *ent.User {
	created, err := e.client.User.Create().SetEmail(name + "@fylvo.test").SetNick(name).
		SetStatus(user.StatusActive).SetGroupID(2).SetSettings(&types.UserSetting{}).Save(e.ctx)
	require.NoError(e.t, err)
	return e.reload(created)
}

// reload returns the user as a request would see it, with group and current storage use.
func (e *staleEnv) reload(u *ent.User) *ent.User {
	if inventory.IsAnonymousUser(u) {
		return u
	}
	loaded, err := e.dep.UserClient().GetByID(context.WithValue(e.ctx, inventory.LoadUserGroup{}, true), u.ID)
	require.NoError(e.t, err)
	return loaded
}

// fs returns a fresh file system for the user, as every request gets one.
func (e *staleEnv) fs(u *ent.User) fs.FileSystem {
	return NewFileManager(e.dep, e.reload(u)).(*manager).fs
}

func (e *staleEnv) uri(raw string) *fs.URI {
	parsed, err := fs.NewUriFromString(raw)
	require.NoError(e.t, err)
	return parsed
}

// begin starts an upload the way the web app does, without content on disk. Its session and lock
// run out after a moment, as if the uploader had left long ago.
func (e *staleEnv) begin(u *ent.User, target *fs.URI, size int64, entityType *types.EntityType) *upload {
	return e.beginFor(u, target, size, entityType, 100*time.Millisecond)
}

func (e *staleEnv) beginFor(u *ent.User, target *fs.URI, size int64, entityType *types.EntityType, ttl time.Duration) *upload {
	f := e.fs(u)
	session, err := f.PrepareUpload(e.ctx, &fs.UploadRequest{Props: &fs.UploadProps{
		Uri: target, Size: size, UploadSessionID: uuid.Must(uuid.NewV4()).String(),
		ExpireAt: time.Now().Add(ttl), EntityType: entityType,
	}})
	require.NoError(e.t, err)
	return &upload{f: f, session: session}
}

type upload struct {
	f       fs.FileSystem
	session *fs.UploadSession
}

func (e *staleEnv) finish(u *upload) {
	_, err := u.f.CompleteUpload(e.ctx, u.session)
	require.NoError(e.t, err)
}

// sweep runs the clean-up as if all uploads so far had started long ago.
func (e *staleEnv) sweep() {
	time.Sleep(150 * time.Millisecond) // the locks of the uploads run out
	_, _, err := sweepStaleUploads(e.ctx, e.dep, time.Now().Add(time.Minute))
	require.NoError(e.t, err)
}

// file returns a file of the owner, or nil if it is not there.
func (e *staleEnv) file(path string) fs.File {
	ctx := context.WithValue(e.ctx, inventory.LoadFileMetadata{}, true)
	file, err := e.fs(e.owner).Get(ctx, e.uri("cloudreve://my/Shared"+path), dbfs.WithFileEntities())
	var appErr serializer.AppError
	if errors.As(err, &appErr) && appErr.Code == serializer.CodeParentNotExist {
		return nil
	}
	require.NoError(e.t, err)
	return file
}

func (e *staleEnv) used() int64 {
	return e.reload(e.owner).Storage
}

func TestSweepRemovesAbandonedNewFiles(t *testing.T) {
	e := newStaleEnv(t)
	before := e.used()

	e.begin(e.owner, e.uri("cloudreve://my/Shared/by owner.txt"), 100, nil)
	e.begin(e.member, e.sharedAt("/by member.txt"), 200, nil)
	e.begin(e.visitor, e.linkAt("/by visitor.txt"), 300, nil)
	require.Equal(t, before+600, e.used(), "the uploads reserve the storage of the owner")
	for _, name := range []string{"by owner.txt", "by member.txt", "by visitor.txt"} {
		require.NotNil(t, e.file("/"+name), name)
	}

	e.sweep()
	for _, name := range []string{"by owner.txt", "by member.txt", "by visitor.txt"} {
		require.Nil(t, e.file("/"+name), name)
	}
	require.Equal(t, before, e.used())
	require.EqualValues(t, 10, e.file("/doc.txt").Size(), "a finished file stays")
}

func TestSweepLeavesUploadsThatCanGoOn(t *testing.T) {
	e := newStaleEnv(t)

	// Started just now: too young for the real schedule.
	e.begin(e.member, e.sharedAt("/young.txt"), 1, nil)
	time.Sleep(150 * time.Millisecond)
	CronRemoveStaleUploads(e.ctx)
	require.NotNil(t, e.file("/young.txt"))
	require.NoError(t, e.fs(e.owner).SoftDelete(e.ctx, e.uri("cloudreve://my/Shared/young.txt"))) // out of the way

	// The session is still in the cache (it can survive a restart there).
	cached := e.begin(e.member, e.sharedAt("/cached.txt"), 1, nil)
	require.NoError(t, e.kv.Set(UploadSessionCachePrefix+cached.session.Props.UploadSessionID, *cached.session, 3600))

	// A request is still writing a chunk.
	writing := e.begin(e.visitor, e.linkAt("/writing.txt"), 1, nil)
	done := TrackUploadRequest(writing.session.Props.UploadSessionID)

	// The lock of the upload is still held.
	e.beginFor(e.member, e.sharedAt("/locked.txt"), 1, nil, time.Hour)

	e.sweep()
	for _, name := range []string{"cached.txt", "writing.txt", "locked.txt"} {
		require.NotNil(t, e.file("/"+name), name)
	}

	// Once the request is over, the upload is gone the next time.
	done()
	e.sweep()
	require.Nil(t, e.file("/writing.txt"))
}

func TestSweepKeepsFinishedFileOfAnAbandonedVersion(t *testing.T) {
	e := newStaleEnv(t)
	before := e.used()
	original := e.file("/doc.txt")

	version := types.EntityTypeVersion
	e.begin(e.member, e.sharedAt("/doc.txt"), 50, &version)
	require.Equal(t, before+50, e.used())

	e.sweep()
	file := e.file("/doc.txt")
	require.NotNil(t, file)
	require.Equal(t, original.PrimaryEntityID(), file.PrimaryEntityID())
	require.Len(t, file.Entities(), 1)
	require.EqualValues(t, 10, file.Size())
	require.Empty(t, file.Metadata()[dbfs.MetadataUploadSessionID], "no longer marked as uploading")
	require.Equal(t, before, e.used())
}

func TestSweepKeepsEmptyFileThatWasThere(t *testing.T) {
	e := newStaleEnv(t)
	_, err := e.fs(e.owner).Create(e.ctx, e.uri("cloudreve://my/Shared/empty.txt"), types.FileTypeFile)
	require.NoError(t, err)

	version := types.EntityTypeVersion
	e.begin(e.member, e.sharedAt("/empty.txt"), 5, &version)

	e.sweep()
	file := e.file("/empty.txt")
	require.NotNil(t, file, "the empty file of the owner stays")
	require.Empty(t, file.Entities())
	require.Empty(t, file.Metadata()[dbfs.MetadataUploadSessionID])
}

func TestSweepRemovesFileOfARetriedAbandonedUpload(t *testing.T) {
	e := newStaleEnv(t)
	before := e.used()

	// The first upload makes the file and is abandoned; a second one to the same file too.
	e.begin(e.member, e.sharedAt("/retry.txt"), 5, nil)
	time.Sleep(150 * time.Millisecond)
	version := types.EntityTypeVersion
	e.begin(e.member, e.sharedAt("/retry.txt"), 7, &version)

	e.sweep()
	require.Nil(t, e.file("/retry.txt"), "nobody ever finished this file")
	require.Equal(t, before, e.used())
}

func TestSweepLeavesNewerUploadToTheSameFile(t *testing.T) {
	e := newStaleEnv(t)
	before := e.used()

	version := types.EntityTypeVersion
	e.begin(e.member, e.sharedAt("/doc.txt"), 50, &version)
	time.Sleep(150 * time.Millisecond)

	// The sweep finds the old placeholder; before it gets to it, a newer upload starts.
	placeholders, err := e.dep.FileClient().StaleUploads(e.ctx, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, placeholders, 1)
	newer := e.beginFor(e.owner, e.uri("cloudreve://my/Shared/doc.txt"), 70, &version, time.Hour)
	require.NoError(t, e.fs(e.owner).Unlock(e.ctx, newer.session.LockToken)) // lets the sweep take the lock
	removeStaleUpload(e.ctx, e.dep, placeholders[0], time.Now().Add(time.Minute))

	file := e.file("/doc.txt")
	require.Equal(t, newer.session.Props.UploadSessionID, file.Metadata()[dbfs.MetadataUploadSessionID], "the newer upload is still marked")
	require.Len(t, file.Entities(), 2, "finished version and newer placeholder")
	require.Equal(t, before+70, e.used(), "the old placeholder is given back once")

	// The newer upload can still be finished.
	newer.session.LockToken = ""
	e.finish(newer)
	require.EqualValues(t, 70, e.file("/doc.txt").Size())
}

func TestSweepLeavesOtherStoragePolicies(t *testing.T) {
	e := newStaleEnv(t)
	upload := e.begin(e.member, e.sharedAt("/remote.txt"), 1, nil)
	_, err := e.client.StoragePolicy.UpdateOneID(upload.session.Policy.ID).SetType(types.PolicyTypeS3).Save(e.ctx)
	require.NoError(t, err)

	e.sweep()
	require.NotNil(t, e.file("/remote.txt"))
}

func TestSweepLeavesFilesInTheTrash(t *testing.T) {
	e := newStaleEnv(t)
	_, err := e.fs(e.owner).Create(e.ctx, e.uri("cloudreve://my/Shared/folder"), types.FileTypeFolder)
	require.NoError(t, err)
	e.begin(e.member, e.sharedAt("/folder/inside.txt"), 1, nil)
	time.Sleep(150 * time.Millisecond)
	require.NoError(t, e.fs(e.owner).SoftDelete(e.ctx, e.uri("cloudreve://my/Shared/folder")))

	e.sweep()
	placeholders, err := e.dep.FileClient().StaleUploads(e.ctx, time.Now().Add(time.Minute))
	require.NoError(t, err)
	require.Len(t, placeholders, 1, "the trash takes care of it")
}

func TestUnlinkingTwiceGivesStorageBackOnce(t *testing.T) {
	e := newStaleEnv(t)
	version := types.EntityTypeVersion
	e.begin(e.member, e.sharedAt("/doc.txt"), 50, &version)
	file := e.file("/doc.txt").(*dbfs.File)

	var placeholder *ent.Entity
	for _, entity := range file.Model.Edges.Entities {
		if entity.UploadSessionID != nil {
			placeholder = entity
		}
	}
	require.NotNil(t, placeholder)

	fc := e.dep.FileClient()
	first, err := fc.UnlinkEntity(e.ctx, placeholder, file.Model, e.owner)
	require.NoError(t, err)
	second, err := fc.UnlinkEntity(e.ctx, placeholder, file.Model, e.owner)
	require.NoError(t, err)
	require.Equal(t, int64(-50), first[e.owner.ID])
	require.Zero(t, second[e.owner.ID])

	stale, err := fc.RemoveStaleEntities(e.ctx, file.Model)
	require.NoError(t, err)
	require.Zero(t, stale[file.Model.OwnerID], "the old copy of the file still lists it, the database no longer does")
}

func TestNewUploadLeavesAVersionFinishedInTheMeantime(t *testing.T) {
	e := newStaleEnv(t)
	version := types.EntityTypeVersion
	running := e.beginFor(e.member, e.sharedAt("/doc.txt"), 50, &version, time.Hour)

	// A newer upload has read the file while the first one was still running; then the first one is finished.
	seen := e.file("/doc.txt").(*dbfs.File)
	e.finish(running)
	before := e.used()

	diff, err := e.dep.FileClient().RemoveStaleEntities(e.ctx, seen.Model)
	require.NoError(t, err)
	require.Zero(t, diff[seen.Model.OwnerID])
	require.EqualValues(t, 50, e.file("/doc.txt").Size(), "the finished version stays")
	require.Equal(t, before, e.used())
}

func TestHardDeleteAfterTheSweepGivesStorageBackOnce(t *testing.T) {
	e := newStaleEnv(t)
	version := types.EntityTypeVersion
	e.begin(e.member, e.sharedAt("/doc.txt"), 50, &version)
	seen := e.file("/doc.txt").(*dbfs.File) // loaded before the sweep, still lists the placeholder
	require.Len(t, seen.Model.Edges.Entities, 2)

	e.sweep()
	_, diff, err := e.dep.FileClient().Delete(e.ctx, []*ent.File{seen.Model}, nil)
	require.NoError(t, err)
	require.Equal(t, int64(-10), diff[e.owner.ID], "only the finished version is given back here")
}

func TestCopyOfAnUnfinishedFileIsNotTakenForTheUploadsOwn(t *testing.T) {
	e := newStaleEnv(t)
	e.begin(e.owner, e.uri("cloudreve://my/Shared/half.txt"), 5, nil)
	_, err := e.fs(e.owner).MoveOrCopy(e.ctx, []*fs.URI{e.uri("cloudreve://my/Shared/half.txt")}, e.uri("cloudreve://my"), true)
	require.NoError(t, err)

	// A new version of the copy is abandoned: only that version goes, the copy stays.
	version := types.EntityTypeVersion
	e.begin(e.owner, e.uri("cloudreve://my/half.txt"), 7, &version)
	e.sweep()
	copied, err := e.fs(e.owner).Get(e.ctx, e.uri("cloudreve://my/half.txt"))
	require.NoError(t, err)
	require.NotNil(t, copied)
}
