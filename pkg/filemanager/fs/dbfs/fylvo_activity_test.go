package dbfs

import (
	"testing"
	"time"

	"context"
	"github.com/cloudreve/Cloudreve/v4/ent"
	entactivity "github.com/cloudreve/Cloudreve/v4/ent/activity"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/activity"
	"github.com/cloudreve/Cloudreve/v4/pkg/auth/requestinfo"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

// Fylvo: tests for the activity log entries the file system writes.

// activityLog starts the activity log writer for e and returns a function that gives all
// entries written since, oldest first.
func (e *sharedFolderEnv) activityLog() func() []*ent.Activity {
	r := activity.Start(inventory.NewActivityClient(e.client), e.logger)
	e.t.Cleanup(r.Close)
	start, err := e.client.Activity.Query().Count(e.ctx)
	require.NoError(e.t, err)

	return func() []*ent.Activity {
		r.Flush()
		all, err := e.client.Activity.Query().Order(ent.Asc(entactivity.FieldID)).All(e.ctx)
		require.NoError(e.t, err)
		return all[start:]
	}
}

func typesOf(entries []*ent.Activity) []activity.Type {
	res := make([]activity.Type, 0, len(entries))
	for _, e := range entries {
		res = append(res, activity.Type(e.Type))
	}
	return res
}

func (e *sharedFolderEnv) fileID(f fs.FileSystem, raw string) int {
	file, err := f.Get(e.ctx, e.uri(raw))
	require.NoError(e.t, err)
	return file.ID()
}

func TestActivityFileLifecycle(t *testing.T) {
	e := newSharedFolderEnv(t)
	log := e.activityLog()
	owner := e.as(e.owner)
	ctx := context.WithValue(e.ctx, requestinfo.RequestInfoCtx{}, &requestinfo.RequestInfo{IP: "192.0.2.7", UserAgent: "test-agent"})
	e.ctx = ctx

	e.upload(owner, "cloudreve://my/Private/a.txt", 5)
	id := e.fileID(owner, "cloudreve://my/Private/a.txt")
	privateID := e.fileID(owner, "cloudreve://my/Private")
	sharedID := e.fileID(owner, "cloudreve://my/Shared")

	// A new version of the same file.
	_, err := e.conditionalUpload(owner, e.uri("cloudreve://my/Private/a.txt"), 6, e.version("cloudreve://my/Private/a.txt"))
	require.NoError(t, err)

	_, _, err = owner.Rename(ctx, e.uri("cloudreve://my/Private/a.txt"), "b.txt")
	require.NoError(t, err)
	_, err = owner.MoveOrCopy(ctx, []*fs.URI{e.uri("cloudreve://my/Private/b.txt")}, e.uri("cloudreve://my/Shared"), false)
	require.NoError(t, err)
	require.NoError(t, owner.SoftDelete(ctx, e.uri("cloudreve://my/Shared/b.txt")))

	entries := log()
	require.Equal(t, []activity.Type{activity.FileUpload, activity.FileUpdate, activity.FileRename, activity.FileMove, activity.FileTrash}, typesOf(entries))
	for _, entry := range entries {
		require.Equal(t, id, entry.FileID)
		require.Equal(t, e.owner.ID, entry.UserID)
		require.Equal(t, e.owner.ID, entry.OwnerID)
		require.Equal(t, "192.0.2.7", entry.IP)
		require.Equal(t, "test-agent", entry.UserAgent)
	}

	require.Equal(t, "a.txt", entries[0].Content.Name)
	require.Equal(t, privateID, entries[0].ParentID)
	require.Equal(t, "a.txt", entries[2].Content.OldName)
	require.Equal(t, "b.txt", entries[2].Content.Name)
	require.Equal(t, sharedID, entries[3].ParentID)
	require.Equal(t, privateID, entries[3].FromParentID)
	require.Equal(t, "Private", entries[3].Content.FromFolder)
	require.Equal(t, "Shared", entries[3].Content.ToFolder)
	// The trash renames the file and drops its folder: the entry keeps both.
	require.Equal(t, "b.txt", entries[4].Content.Name)
	require.Equal(t, sharedID, entries[4].ParentID)

	// Restore from the trash and delete it for good, again from the trash.
	trashed := e.trashUri(owner, id)
	require.NoError(t, owner.Restore(ctx, trashed))
	require.NoError(t, owner.SoftDelete(ctx, e.uri("cloudreve://my/Shared/b.txt")))
	_, _, err = owner.Delete(ctx, []*fs.URI{e.trashUri(owner, id)})
	require.NoError(t, err)

	entries = log()[5:]
	require.Equal(t, []activity.Type{activity.FileRestore, activity.FileTrash, activity.FileDelete}, typesOf(entries))
	require.Equal(t, sharedID, entries[0].ParentID)
	require.Equal(t, "b.txt", entries[0].Content.Name)
	require.Equal(t, "b.txt", entries[2].Content.Name, "the real name, not the one in the trash")
}

// trashUri finds the address of a file in the trash.
func (e *sharedFolderEnv) trashUri(f fs.FileSystem, id int) *fs.URI {
	_, res, err := f.List(e.ctx, e.uri("cloudreve://trash"), fs.WithPageSize(100))
	require.NoError(e.t, err)
	for _, file := range res.Files {
		if file.ID() == id {
			return file.Uri(false)
		}
	}
	e.t.Fatalf("file %d is not in the trash", id)
	return nil
}

func TestActivityCreateAndCopy(t *testing.T) {
	e := newSharedFolderEnv(t)
	log := e.activityLog()
	owner := e.as(e.owner)

	// Folders made on the way each get an entry, the empty file too.
	_, err := owner.Create(e.ctx, e.uri("cloudreve://my/New/Deep/empty.txt"), types.FileTypeFile)
	require.NoError(t, err)
	_, err = owner.MoveOrCopy(e.ctx, []*fs.URI{e.uri("cloudreve://my/Private/secret.txt")}, e.uri("cloudreve://my/New"), true)
	require.NoError(t, err)

	entries := log()
	require.Equal(t, []activity.Type{activity.FileCreate, activity.FileCreate, activity.FileCreate, activity.FileCopy}, typesOf(entries))
	require.Equal(t, []string{"New", "Deep", "empty.txt", "secret.txt"},
		[]string{entries[0].Content.Name, entries[1].Content.Name, entries[2].Content.Name, entries[3].Content.Name})
	require.True(t, entries[0].Content.IsFolder)
	require.False(t, entries[2].Content.IsFolder)
	require.Equal(t, e.fileID(owner, "cloudreve://my/New/secret.txt"), entries[3].FileID)
	require.Equal(t, "Private", entries[3].Content.FromFolder)
}

func TestActivityLeavesOutWhatIsNoChange(t *testing.T) {
	e := newSharedFolderEnv(t)
	log := e.activityLog()
	owner := e.as(e.owner)

	// A cancelled upload: neither its start nor its cleanup.
	session, err := owner.PrepareUpload(e.ctx, &fs.UploadRequest{Props: &fs.UploadProps{
		Uri:             e.uri("cloudreve://my/Private/cancelled.txt"),
		Size:            3,
		UploadSessionID: uuid.Must(uuid.NewV4()).String(),
		ExpireAt:        time.Now().Add(time.Hour),
	}})
	require.NoError(t, err)
	_, _, err = owner.CancelUploadSession(e.ctx, e.uri("cloudreve://my/Private/cancelled.txt"), session.Props.UploadSessionID, session)
	require.NoError(t, err)

	// Nothing in a context that skips, as imports and archive extraction use.
	e.upload(owner, "cloudreve://my/Private/plain.txt", 1)
	e.ctx = activity.Skip(e.ctx)
	e.upload(owner, "cloudreve://my/Private/imported.txt", 1)

	require.Equal(t, []activity.Type{activity.FileUpload}, typesOf(log()))
}

func TestActivityOfMembersAndGuests(t *testing.T) {
	e := newSharedFolderEnv(t)
	log := e.activityLog()

	// The editor changes the shared folder: done by the editor, about the owner's file.
	e.upload(e.as(e.editor), e.shared("/from-editor.txt").String(), 2)
	entries := log()
	require.Len(t, entries, 1)
	require.Equal(t, e.editor.ID, entries[0].UserID)
	require.Equal(t, e.owner.ID, entries[0].OwnerID)
	require.Equal(t, e.fileID(e.as(e.owner), "cloudreve://my/Shared"), entries[0].ParentID)

	// A guest of an upload link: no account, but the link.
	link, at := e.uploadLink(&types.ShareProps{AllowUpload: true, HideContent: true})
	anonymous, err := e.userClient.AnonymousUser(e.ctx)
	require.NoError(t, err)
	require.NoError(t, e.tryUpload(e.as(anonymous), at("/from-guest.txt"), 2, nil))

	entries = log()[1:]
	require.Len(t, entries, 1)
	require.Equal(t, activity.FileUpload, activity.Type(entries[0].Type))
	require.Zero(t, entries[0].UserID)
	require.Equal(t, link.ID, entries[0].ShareID)
	require.Equal(t, e.owner.ID, entries[0].OwnerID)
	require.Equal(t, activity.ViaLink, entries[0].Via)
}

func TestActivitySystemDeletes(t *testing.T) {
	e := newSharedFolderEnv(t)
	log := e.activityLog()
	owner := e.as(e.owner)

	id := e.fileID(owner, "cloudreve://my/Private/secret.txt")
	require.NoError(t, owner.SoftDelete(e.ctx, e.uri("cloudreve://my/Private/secret.txt")))
	e.ctx = activity.System(e.ctx)
	_, _, err := owner.Delete(e.ctx, []*fs.URI{e.trashUri(owner, id)})
	require.NoError(t, err)

	entries := log()
	require.Equal(t, []activity.Type{activity.FileTrash, activity.FileDelete}, typesOf(entries))
	require.Zero(t, entries[1].UserID)
	require.Equal(t, activity.ViaSystem, entries[1].Via)
	require.Equal(t, "secret.txt", entries[1].Content.Name)
}
