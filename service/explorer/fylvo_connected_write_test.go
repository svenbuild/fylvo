package explorer

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive/drivetest"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/dbfs"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/stretchr/testify/require"
)

// Fylvo: the routes that change a connected storage, down to a simulated Google Drive.

// The tests of this package run on an installation whose operator switched changes on.
func init() { _ = os.Setenv(connected.EnvWrite, "1") }

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
	// The server listed the folder a moment ago. A change does not go by that listing.
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

// An entry is named once in a request: with two results for it nobody could tell which
// of the two addresses was acted on.
func TestConnectedBatchNamesAnEntryOnce(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFile("", "a.txt", []byte("a"))
	ids := e.idsOf(t, e.root)
	a := e.root + "/a.txt"
	ids[a+"/"] = ids[a]

	err := (&DeleteFileService{Uris: []string{a, a + "/"}, Ids: ids}).Delete(e.as(e.alex))
	require.Equal(t, serializer.CodeParamErr, connectedCode(err))
	require.Equal(t, "a.txt", e.names(t, e.root), "nothing was deleted")
}

// A request moves or deletes a bounded number of entries: each one costs calls to the
// provider, and nobody waits for an answer that takes many minutes.
func TestConnectedBatchSize(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFile("", "a.txt", []byte("a"))
	e.sim.AddFolder("", "Docs")
	ids := e.idsOf(t, e.root)
	// As many different addresses as fit, and one more.
	uris := []string{e.root + "/a.txt"}
	for i := 1; i <= connected.MaxBatch; i++ {
		uris = append(uris, fmt.Sprintf("%s/missing %d.txt", e.root, i))
	}
	err := (&DeleteFileService{Uris: uris, Ids: ids}).Delete(e.as(e.alex))
	require.Equal(t, serializer.CodeParamErr, connectedCode(err))
	err = (&MoveFileService{Uris: uris, Dst: e.root + "/Docs", Ids: ids}).Move(e.as(e.alex))
	require.Equal(t, serializer.CodeParamErr, connectedCode(err))
	require.Equal(t, "Docs|a.txt", e.names(t, e.root), "nothing was touched")

	// With one address less the request is taken: the one entry that exists is
	// deleted, the others are told as not found, each by its address.
	err = (&DeleteFileService{Uris: uris[:connected.MaxBatch], Ids: ids}).Delete(e.as(e.alex))
	require.Equal(t, serializer.CodeBatchOperationNotFullyCompleted, connectedCode(err))
	require.Equal(t, "Docs", e.names(t, e.root))
}

// An installation whose operator did not switch changes on only reads connected
// storages: nothing in a listing offers a change, and every route that changes
// something refuses before a provider is asked.
func TestConnectedOnlyReadsUnlessSwitchedOn(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFolder("", "Docs")
	e.sim.AddFile("", "notes.txt", []byte("hello"))
	ids := e.idsOf(t, e.root)

	changing := []dbfs.NavigatorCapability{
		dbfs.NavigatorCapabilityCreateFile, dbfs.NavigatorCapabilityRenameFile,
		dbfs.NavigatorCapabilityUploadFile, dbfs.NavigatorCapabilityDeleteFile,
	}
	offered := func(res *ListResponse) (bits []string) {
		rows := append([]FileResponse{}, res.Files...)
		rows = append(rows, FileResponse{Name: "(folder)", Capability: res.Props.Capability})
		for _, row := range rows {
			for _, bit := range changing {
				if row.Capability.Enabled(int(bit)) {
					bits = append(bits, fmt.Sprintf("%s:%d", row.Name, bit))
				}
			}
		}
		return bits
	}
	bit := func(name string, capability dbfs.NavigatorCapability) string {
		return fmt.Sprintf("%s:%d", name, capability)
	}

	// Switched on: a folder takes new entries; a file can be renamed, deleted and saved over.
	res, err := e.list(e.alex, e.root, 0, "")
	require.NoError(t, err)
	require.False(t, res.Connected.ReadOnly)
	require.ElementsMatch(t, []string{
		bit("(folder)", dbfs.NavigatorCapabilityCreateFile), bit("(folder)", dbfs.NavigatorCapabilityUploadFile),
		bit("Docs", dbfs.NavigatorCapabilityCreateFile), bit("Docs", dbfs.NavigatorCapabilityUploadFile),
		bit("Docs", dbfs.NavigatorCapabilityRenameFile), bit("Docs", dbfs.NavigatorCapabilityDeleteFile),
		bit("notes.txt", dbfs.NavigatorCapabilityUploadFile),
		bit("notes.txt", dbfs.NavigatorCapabilityRenameFile), bit("notes.txt", dbfs.NavigatorCapabilityDeleteFile),
	}, offered(res))

	t.Setenv(connected.EnvWrite, "")
	res, err = e.list(e.alex, e.root, 0, "")
	require.NoError(t, err)
	require.True(t, res.Connected.ReadOnly)
	require.Empty(t, offered(res))

	calls := func() int {
		return e.sim.Calls(drivetest.CallCreate) + e.sim.Calls(drivetest.CallUpdate) + e.sim.Calls(drivetest.CallStart)
	}
	before := calls()
	unsupported := func(err error) {
		t.Helper()
		require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err))
	}
	_, err = (&CreateFileService{Uri: e.root + "/New", Type: "folder"}).Create(e.as(e.alex))
	unsupported(err)
	_, err = (&RenameFileService{Uri: e.root + "/notes.txt", NewName: "n.txt", Ids: ids}).Rename(e.as(e.alex))
	unsupported(err)
	unsupported((&MoveFileService{Uris: []string{e.root + "/notes.txt"}, Dst: e.root + "/Docs", Ids: ids}).Move(e.as(e.alex)))
	unsupported((&DeleteFileService{Uris: []string{e.root + "/notes.txt"}, Ids: ids}).Delete(e.as(e.alex)))
	_, err = (&CreateUploadSessionService{Uri: e.root + "/up.bin", Size: 10}).Create(e.as(e.alex))
	unsupported(err)
	_, err = (&FileUpdateService{Uri: e.root + "/notes.txt"}).PutContent(e.withBody(e.alex, "PUT", []byte("other")), nil)
	unsupported(err)
	require.Equal(t, before, calls(), "nothing of that reached the provider")
	require.Equal(t, "Docs|notes.txt", e.names(t, e.root))
	// Reading goes on as before.
	require.Equal(t, []byte("hello"), e.download(e.root+"/notes.txt"))
}

// The key of an id names an address, however the client spells it: escaped in "ids" and
// plain in "uri", or the other way round, is the same entry.
func TestConnectedIdsBySpellingOfTheAddress(t *testing.T) {
	e := newConnectedEnv(t)
	folder := e.sim.AddFolder("", "Trial x")
	e.sim.AddFile(folder, "note one.txt", []byte("a"))
	e.sim.AddFile(folder, "note two.txt", []byte("b"))
	plain, escaped := e.root+"/Trial x", e.root+"/Trial%20x"

	var one, two string
	for key, id := range e.idsOf(t, plain) {
		switch {
		case strings.HasSuffix(key, "one.txt"):
			one = id
		case strings.HasSuffix(key, "two.txt"):
			two = id
		}
	}
	require.NotEmpty(t, one)
	require.NotEmpty(t, two)

	// The address plain, its key escaped.
	_, err := (&RenameFileService{Uri: plain + "/note one.txt", NewName: "first.txt",
		Ids: connectedIDs{escaped + "/note%20one.txt": one}}).Rename(e.as(e.alex))
	require.NoError(t, err)
	// The address escaped, its key plain.
	require.NoError(t, (&DeleteFileService{Uris: []string{escaped + "/note%20two.txt"},
		Ids: connectedIDs{plain + "/note two.txt": two}}).Delete(e.as(e.alex)))
	require.Equal(t, "first.txt", e.names(t, plain))

	// Two keys for one address that name different rows say nothing: no id, no change.
	_, err = (&RenameFileService{Uri: plain + "/first.txt", NewName: "x.txt",
		Ids: connectedIDs{plain + "/first.txt": one, escaped + "/first.txt": two}}).Rename(e.as(e.alex))
	require.Equal(t, connected.ReasonChanged, connected.ReasonOf(err))
	// The id of another row is refused as before.
	_, err = (&RenameFileService{Uri: plain + "/first.txt", NewName: "x.txt",
		Ids: connectedIDs{escaped + "/first.txt": two}}).Rename(e.as(e.alex))
	require.Equal(t, connected.ReasonChanged, connected.ReasonOf(err))
	require.Equal(t, "first.txt", e.names(t, plain))
}

// Ids that contradict each other are no ids: a change that names such an address is
// refused, also for the folder a move goes into, whose id may otherwise be left out.
func TestConnectedIdsThatContradictEachOther(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFolder("", "Trial x")
	e.sim.AddFile("", "note.txt", []byte("a"))
	plain, escaped := e.root+"/Trial x", e.root+"/Trial%20x"
	ids := e.idsOf(t, e.root)
	// A listing spells the address of the folder one way; which, does not matter here.
	note, folder := ids[e.root+"/note.txt"], ids[plain]+ids[escaped]
	require.NotEmpty(t, note)
	require.NotEmpty(t, folder)

	err := (&MoveFileService{Uris: []string{e.root + "/note.txt"}, Dst: plain,
		Ids: connectedIDs{e.root + "/note.txt": note, plain: folder, escaped: "another-id"}}).Move(e.as(e.alex))
	require.Equal(t, connected.ReasonChanged, connected.ReasonOf(err))
	require.Equal(t, "Trial x|note.txt", e.names(t, e.root), "nothing was moved")
	require.NoError(t, (&DeleteFileService{Uris: []string{e.root + "/note.txt"},
		Ids: connectedIDs{e.root + "/note.txt": note, e.root + "/note%2Etxt": note}}).Delete(e.as(e.alex)),
		"the same id twice contradicts nothing")

	// Without an id for the folder the move goes through, as before.
	e.sim.AddFile("", "second.txt", []byte("b"))
	ids = e.idsOf(t, e.root)
	require.NoError(t, (&MoveFileService{Uris: []string{e.root + "/second.txt"}, Dst: escaped,
		Ids: connectedIDs{e.root + "/second.txt": ids[e.root+"/second.txt"]}}).Move(e.as(e.alex)))
	require.Equal(t, "second.txt", e.names(t, plain))
}

// A save whose length cannot be read is refused like any other request with a bad
// parameter, before anything is opened.
func TestConnectedSaveWithABadLength(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFile("", "notes.txt", []byte("hello"))
	c := e.withBody(e.alex, "PUT", []byte("other"))
	c.Request.ContentLength = -1
	c.Request.Header.Del("Content-Length")
	c.Request.Header.Set("X-Expected-Entity-Length", "not a number")
	_, err := (&FileUpdateService{Uri: e.root + "/notes.txt"}).PutContent(c, nil)
	require.Equal(t, serializer.CodeParamErr, connectedCode(err))
	require.Equal(t, []byte("hello"), e.download(e.root+"/notes.txt"))
}

// Where the provider takes names in any case for one, so do the keys of "ids".
func TestConnectedIdsFollowTheCaseRuleOfTheProvider(t *testing.T) {
	uri := func(raw string) *fs.URI {
		parsed, ok := connectedURI(raw)
		require.True(t, ok, raw)
		return parsed
	}
	root := "cloudreve://abcdefghijklmnop@connected"
	ids := connectedIDs{root + "/Docs": "current", root + "/docs": "stale"}

	// Drive: two names, two addresses, each with its id.
	exact := ids.byAddress(false)
	id, err := exact.of(uri(root + "/Docs"))
	require.NoError(t, err)
	require.Equal(t, "current", id)
	id, err = exact.of(uri(root + "/DOCS"))
	require.NoError(t, err)
	require.Empty(t, id)

	// OneDrive: one address with two ids that contradict each other, however it is asked for.
	folded := ids.byAddress(true)
	for _, spelling := range []string{"/Docs", "/docs", "/DOCS"} {
		_, err = folded.of(uri(root + spelling))
		require.Equal(t, connected.ReasonChanged, connected.ReasonOf(err), spelling)
	}
	same := connectedIDs{root + "/Docs": "current"}.byAddress(true)
	id, err = same.of(uri(root + "/docs"))
	require.NoError(t, err)
	require.Equal(t, "current", id)
}

// The case rule is the provider's: by capitals, so the two small forms of one capital
// are one address as well.
func TestConnectedIdsFoldLikeTheProvider(t *testing.T) {
	root := "cloudreve://abcdefghijklmnop@connected"
	big, _ := connectedURI(root + "/\u03a3")
	ids := connectedIDs{root + "/\u03a3": "current", root + "/\u03c2": "stale"}
	_, err := ids.byAddress(true).of(big)
	require.Equal(t, connected.ReasonChanged, connected.ReasonOf(err))
	id, err := ids.byAddress(false).of(big)
	require.NoError(t, err)
	require.Equal(t, "current", id)
}

// An address that is meant for a connected storage and is none (an empty name at its
// end) is refused like any other bad address.
func TestConnectedSaveToAnAddressThatIsNone(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddFile("", "notes.txt", []byte("hello"))
	_, err := (&FileUpdateService{Uri: e.root + "/notes.txt//"}).PutContent(e.withBody(e.alex, "PUT", []byte("other")), nil)
	require.Error(t, err)
	require.Equal(t, []byte("hello"), e.download(e.root+"/notes.txt"))
}

// A new Office document is a real, empty one from its first moment, not a file without
// bytes: the provider's own Office opens it, also when nobody ever saved to it.
func TestConnectedCreateOfficeDocument(t *testing.T) {
	e := newConnectedEnv(t)
	e.withOffice(t, false)
	create := func(path string, errOnConflict bool) (*FileResponse, error) {
		return (&CreateFileService{Uri: e.root + path, Type: "file", ErrOnConflict: errOnConflict}).Create(e.as(e.alex))
	}

	for path, main := range map[string]string{
		"/New.docx": "word/document.xml", "/Budget.xlsx": "xl/workbook.xml", "/Talk.PPTX": "ppt/presentation.xml",
	} {
		created, err := create(path, true)
		require.NoError(t, err, path)
		require.Positive(t, created.Size, path)
		content := e.download(e.root + path)
		require.EqualValues(t, created.Size, len(content))
		z, err := zip.NewReader(bytes.NewReader(content), int64(len(content)))
		require.NoError(t, err, path)
		var names []string
		for _, f := range z.File {
			names = append(names, f.Name)
		}
		require.Contains(t, names, main, path)
	}
	// Fylvo hands out a window that edits for it, like for any document. (What the
	// Office server makes of the file is not part of this test.)
	session, err := e.officeSession(t, "/New.docx", types.ViewerActionEdit)
	require.NoError(t, err)
	require.True(t, *session.Edit)

	// Created again it is the file that is there, as it is: nothing is replaced. Asked
	// for an error, it is the error of a taken name.
	before := e.download(e.root + "/New.docx")
	id := e.sim.Calls(drivetest.CallStart)
	again, err := create("/New.docx", false)
	require.NoError(t, err)
	require.EqualValues(t, len(before), again.Size)
	require.Equal(t, before, e.download(e.root+"/New.docx"))
	require.Equal(t, id, e.sim.Calls(drivetest.CallStart), "nothing was uploaded for a name that is taken")
	_, err = create("/New.docx", true)
	require.Equal(t, serializer.CodeObjectExist, connectedCode(err))
	// A folder is a folder, whatever its name ends in, and a folder or a link under
	// the name is not a document: an error, and nothing is stored.
	folder, err := (&CreateFileService{Uri: e.root + "/Folder.docx", Type: "folder"}).Create(e.as(e.alex))
	require.NoError(t, err)
	require.Equal(t, int(types.FileTypeFolder), folder.Type)
	_, err = create("/Folder.docx", false)
	require.Equal(t, serializer.CodeObjectExist, connectedCode(err))
	e.sim.AddShortcut("", "Link.docx", e.sim.AddFile("", "target.txt", []byte("x")))
	e.m.Tree().Forget(e.id)
	uploads := e.sim.Calls(drivetest.CallStart)
	for _, errOnConflict := range []bool{false, true} {
		_, err = create("/Link.docx", errOnConflict)
		require.Equal(t, serializer.CodeObjectExist, connectedCode(err))
	}
	require.Equal(t, uploads, e.sim.Calls(drivetest.CallStart))

	// Every other kind of file is created empty, as before, and a folder is a folder.
	notes, err := create("/notes.txt", true)
	require.NoError(t, err)
	require.Zero(t, notes.Size)
	old, err := create("/letter.doc", true)
	require.NoError(t, err)
	require.Zero(t, old.Size)

	// The rules of every change hold: a name Fylvo does not give, and an installation
	// that only reads.
	// What is refused is refused in the order it is for an empty file: the name before
	// anything about the folder or about what is there.
	for _, path := range []string{"/a:b.docx", "/missing folder/a:b.docx"} {
		for _, kind := range []string{path, strings.TrimSuffix(path, ".docx") + ".txt"} {
			_, err = create(kind, true)
			require.Equal(t, serializer.CodeIllegalObjectName, connectedCode(err), kind)
		}
	}
	e.sim.AddFile("", "x:y.docx", []byte("made at the provider"))
	e.m.Tree().Forget(e.id)
	_, err = create("/x:y.docx", true)
	require.Equal(t, serializer.CodeIllegalObjectName, connectedCode(err))
	_, err = create("/missing folder/new.docx", true)
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
	t.Setenv(connected.EnvWrite, "")
	for _, path := range []string{"/Later.docx", "/a:b.docx", "/Later.txt"} {
		_, err = create(path, true)
		require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err), path)
	}
}
