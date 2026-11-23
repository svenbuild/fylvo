package explorer

import (
	"testing"

	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive/drivetest"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/stretchr/testify/require"
)

// Fylvo: the routes that change a connected storage, down to a simulated Google Drive.

// idsOf lists a folder and returns the ids of its rows by address, as a client has them.
func (e *connectedEnv) idsOf(t *testing.T, folder string) connectedIDs {
	res, err := e.list(e.alex, folder, 0, "")
	require.NoError(t, err)
	ids := connectedIDs{folder: res.Parent.ID}
	for _, f := range res.Files {
		ids[f.Path] = f.ID
	}
	return ids
}

func (e *connectedEnv) names(t *testing.T, folder string) string {
	res, err := e.list(e.alex, folder, 0, "")
	require.NoError(t, err)
	return fileNames(res.Files)
}

func TestConnectedCreate(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFile("", "notes.txt", []byte("hello"))
	e.sim.AddNative("", "Budget", "gsheet")

	folder, err := (&CreateFileService{Uri: e.root + "/Docs", Type: "folder"}).Create(e.as(e.alex))
	require.NoError(t, err)
	require.Equal(t, int(types.FileTypeFolder), folder.Type)
	require.Equal(t, e.root+"/Docs", folder.Path)
	file, err := (&CreateFileService{Uri: e.root + "/Docs/new.txt", Type: "file"}).Create(e.as(e.alex))
	require.NoError(t, err)
	require.Equal(t, "new.txt", file.Name)
	require.Zero(t, file.Size)
	// What was created is listed right away, although the folder was just read.
	require.Equal(t, "Docs|Budget.gsheet|notes.txt", e.names(t, e.root))
	require.Equal(t, "new.txt", e.names(t, e.root+"/Docs"))

	// The same again returns what is there; with err_on_conflict it is an error.
	again, err := (&CreateFileService{Uri: e.root + "/Docs", Type: "folder"}).Create(e.as(e.alex))
	require.NoError(t, err)
	require.Equal(t, folder.ID, again.ID)
	creates := e.sim.Calls(drivetest.CallCreate)
	_, err = (&CreateFileService{Uri: e.root + "/Docs", Type: "folder", ErrOnConflict: true}).Create(e.as(e.alex))
	require.Equal(t, serializer.CodeObjectExist, connectedCode(err))
	// A name that is taken by another kind of entry, or by a document of the provider.
	_, err = (&CreateFileService{Uri: e.root + "/notes.txt", Type: "folder"}).Create(e.as(e.alex))
	require.Equal(t, serializer.CodeObjectExist, connectedCode(err))
	_, err = (&CreateFileService{Uri: e.root + "/Budget.gsheet", Type: "file"}).Create(e.as(e.alex))
	require.Equal(t, serializer.CodeObjectExist, connectedCode(err))
	// Names Fylvo does not give, and places that do not exist.
	for _, name := range []string{"a:b", "star*", "tab\there"} {
		_, err = (&CreateFileService{Uri: e.root + "/" + name, Type: "file"}).Create(e.as(e.alex))
		require.Error(t, err, name)
	}
	_, err = (&CreateFileService{Uri: e.root + "/nowhere/new.txt", Type: "file"}).Create(e.as(e.alex))
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
	require.Equal(t, creates, e.sim.Calls(drivetest.CallCreate), "nothing of that reached the provider")

	// Another account cannot create in the storage of Alex.
	_, err = (&CreateFileService{Uri: e.root + "/Docs/sam.txt", Type: "file"}).Create(e.as(e.sam))
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
}

func TestConnectedRename(t *testing.T) {
	e := newConnectedEnv(t)
	id := e.sim.AddFile("", "notes.txt", []byte("hello"))
	e.sim.AddFile("", "taken.txt", []byte("x"))
	e.sim.AddNative("", "Budget", "gsheet")
	ids := e.idsOf(t, e.root)
	file := e.root + "/notes.txt"

	// Without the id of the row, or with another one, nothing is renamed.
	_, err := (&RenameFileService{Uri: file, NewName: "x.txt"}).Rename(e.as(e.alex))
	require.Equal(t, connected.ReasonChanged, connected.ReasonOf(err))
	_, err = (&RenameFileService{Uri: file, NewName: "x.txt", Ids: connectedIDs{file: ids[e.root+"/taken.txt"]}}).Rename(e.as(e.alex))
	require.Equal(t, connected.ReasonChanged, connected.ReasonOf(err))
	_, err = (&RenameFileService{Uri: file, NewName: "taken.txt", Ids: ids}).Rename(e.as(e.alex))
	require.Equal(t, serializer.CodeObjectExist, connectedCode(err))
	_, err = (&RenameFileService{Uri: file, NewName: "a/b", Ids: ids}).Rename(e.as(e.alex))
	require.Equal(t, serializer.CodeIllegalObjectName, connectedCode(err))
	_, err = (&RenameFileService{Uri: e.root + "/Budget.gsheet", NewName: "Plan", Ids: ids}).Rename(e.as(e.alex))
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err), "a document of the provider is read-only")
	require.Zero(t, e.sim.Calls(drivetest.CallUpdate))

	renamed, err := (&RenameFileService{Uri: file, NewName: "journal.txt", Ids: ids}).Rename(e.as(e.alex))
	require.NoError(t, err)
	require.Equal(t, "journal.txt", renamed.Name)
	require.Equal(t, e.root+"/journal.txt", renamed.Path)
	require.Equal(t, ids[file], renamed.ID, "the row keeps its id")
	require.Equal(t, "Budget.gsheet|journal.txt|taken.txt", e.names(t, e.root))

	// The file is replaced under its name at the provider: the id the client holds is
	// for the one that is gone, and the new one is left alone.
	ids = e.idsOf(t, e.root)
	e.sim.Trash(id)
	e.sim.AddFile("", "journal.txt", []byte("another file"))
	e.m.Tree().Forget(e.id)
	_, err = (&RenameFileService{Uri: e.root + "/journal.txt", NewName: "mine.txt", Ids: ids}).Rename(e.as(e.alex))
	require.Equal(t, connected.ReasonChanged, connected.ReasonOf(err))
	require.Equal(t, "Budget.gsheet|journal.txt|taken.txt", e.names(t, e.root))
}

func TestConnectedMove(t *testing.T) {
	e := newConnectedEnv(t)
	docs := e.sim.AddFolder("", "Docs")
	e.sim.AddFolder(docs, "Old")
	e.sim.AddFile("", "a.txt", []byte("a"))
	e.sim.AddFile("", "b.txt", []byte("b"))
	e.sim.AddFile(docs, "b.txt", []byte("taken"))
	e.sim.AddShortcut("", "Link to Docs", docs)
	ids := e.idsOf(t, e.root)
	for path, id := range e.idsOf(t, e.root+"/Docs") {
		ids[path] = id
	}
	a, b := e.root+"/a.txt", e.root+"/b.txt"

	// A copy, and anything that leaves the storage or comes into it, is not offered.
	require.Equal(t, serializer.CodeConnectedUnsupported,
		connectedCode((&MoveFileService{Uris: []string{a}, Dst: e.root + "/Docs", Copy: true, Ids: ids}).Move(e.as(e.alex))))
	require.Equal(t, serializer.CodeConnectedUnsupported,
		connectedCode((&MoveFileService{Uris: []string{a}, Dst: "cloudreve://my", Ids: ids}).Move(e.as(e.alex))))
	require.Equal(t, serializer.CodeConnectedUnsupported,
		connectedCode((&MoveFileService{Uris: []string{"cloudreve://my/x.txt"}, Dst: e.root + "/Docs"}).Move(e.as(e.alex))))
	// Not into a folder reached through a link, and not a folder into itself.
	require.Equal(t, serializer.CodeConnectedUnsupported,
		connectedCode((&MoveFileService{Uris: []string{a}, Dst: e.root + "/Link to Docs", Ids: ids}).Move(e.as(e.alex))))
	err := (&MoveFileService{Uris: []string{e.root + "/Docs"}, Dst: e.root + "/Docs/Old", Ids: ids}).Move(e.as(e.alex))
	require.Equal(t, serializer.CodeBatchOperationNotFullyCompleted, connectedCode(err))
	require.Zero(t, e.sim.Calls(drivetest.CallUpdate))

	// One of two cannot be moved, its name is taken there. The other one is, and the
	// answer says which one was not, and why. Nothing is replaced.
	err = (&MoveFileService{Uris: []string{a, b}, Dst: e.root + "/Docs", Ids: ids}).Move(e.as(e.alex))
	require.Equal(t, serializer.CodeBatchOperationNotFullyCompleted, connectedCode(err))
	failed := serializer.Err(e.ctx, err).AggregatedError.(map[string]serializer.Response)
	require.Len(t, failed, 1)
	require.Equal(t, serializer.CodeObjectExist, failed[b].Code)
	require.Equal(t, "Docs|Link to Docs|b.txt", e.names(t, e.root))
	require.Equal(t, "Old|a.txt|b.txt", e.names(t, e.root+"/Docs"))
	address, err := e.urlOf(e.alex, e.root+"/Docs/b.txt", true)
	require.NoError(t, err)
	require.Equal(t, "taken", e.fetch(address, "").Body.String())

	// Back to the root, with the folder named by the id the client has for it.
	ids = e.idsOf(t, e.root+"/Docs")
	ids[e.root] = e.idsOf(t, e.root)[e.root]
	require.NoError(t, (&MoveFileService{Uris: []string{e.root + "/Docs/a.txt"}, Dst: e.root, Ids: ids}).Move(e.as(e.alex)))
	require.Equal(t, "Docs|Link to Docs|a.txt|b.txt", e.names(t, e.root))
}

func TestConnectedDelete(t *testing.T) {
	e := newConnectedEnv(t)
	docs := e.sim.AddFolder("", "Docs")
	e.sim.AddFile(docs, "inside.txt", []byte("x"))
	e.sim.AddFile("", "a.txt", []byte("a"))
	e.sim.AddFile("", "twice.txt", []byte("1"))
	e.sim.AddFile("", "twice.txt", []byte("2"))
	ids := e.idsOf(t, e.root)
	a := e.root + "/a.txt"

	// Without ids nothing is deleted; a row that stands for two entries never is.
	err := (&DeleteFileService{Uris: []string{a}}).Delete(e.as(e.alex))
	require.Equal(t, serializer.CodeBatchOperationNotFullyCompleted, connectedCode(err))
	err = (&DeleteFileService{Uris: []string{e.root + "/twice.txt"}, Ids: ids}).Delete(e.as(e.alex))
	require.Error(t, err)
	// Another account, with the right ids, finds nothing.
	require.Error(t, (&DeleteFileService{Uris: []string{a}, Ids: ids}).Delete(e.as(e.sam)))
	// Two storages in one request.
	require.Error(t, (&DeleteFileService{Uris: []string{a, "cloudreve://aaaaaaaaaaaaaaaa@connected/x"}, Ids: ids}).Delete(e.as(e.alex)))
	require.Equal(t, "Docs|a.txt|twice.txt", e.names(t, e.root))

	// A file and a folder with what is in it: both go to the trash of the provider.
	require.NoError(t, (&DeleteFileService{Uris: []string{a, e.root + "/Docs"}, Ids: ids, SkipSoftDelete: true}).Delete(e.as(e.alex)))
	require.Equal(t, "twice.txt", e.names(t, e.root))
	// Already gone: the address no longer leads anywhere.
	err = (&DeleteFileService{Uris: []string{a}, Ids: ids}).Delete(e.as(e.alex))
	require.Equal(t, serializer.CodeBatchOperationNotFullyCompleted, connectedCode(err))
	failed := serializer.Err(e.ctx, err).AggregatedError.(map[string]serializer.Response)
	require.Equal(t, serializer.CodeParentNotExist, failed[a].Code)
}
