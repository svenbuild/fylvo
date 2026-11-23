package explorer

import (
	"net/http"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/connectedtest"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/stretchr/testify/require"
)

// Fylvo: the answers of the routes that change a connected storage, one of each kind a
// client has to handle. See connectedtest.Examples for how to get them as files.
func TestConnectedWriteExamples(t *testing.T) {
	e := newConnectedEnv(t)
	x := connectedtest.NewExamples(t)
	docs := e.sim.AddFolder("", "Docs")
	e.sim.AddFile(docs, "taken.txt", []byte("taken"))
	e.sim.AddFile("", "notes.txt", []byte("hello"))
	e.sim.AddFile("", "taken.txt", []byte("root"))
	e.sim.AddNative("", "Budget", "gsheet")

	// What a folder that takes changes looks like: capability, storage policy, not read-only.
	root, err := e.list(e.alex, e.root, 0, "")
	require.NoError(t, err)
	x.Add("write-list-root", root, nil)
	ids := e.idsOf(t, e.root)

	folder, err := (&CreateFileService{Uri: e.root + "/Work", Type: "folder"}).Create(e.as(e.alex))
	x.Add("write-create-folder", folder, err)
	require.NoError(t, err)
	file, err := (&CreateFileService{Uri: e.root + "/Work/todo.txt", Type: "file"}).Create(e.as(e.alex))
	x.Add("write-create-file", file, err)
	require.NoError(t, err)

	renamed, err := (&RenameFileService{Uri: e.root + "/notes.txt", NewName: "journal.txt", Ids: ids}).Rename(e.as(e.alex))
	x.Add("write-rename", renamed, err)
	require.NoError(t, err)

	// A batch in which one of two entries cannot be moved: the other one is.
	ids = e.idsOf(t, e.root)
	err = (&MoveFileService{Uris: []string{e.root + "/journal.txt", e.root + "/taken.txt"}, Dst: e.root + "/Docs", Ids: ids}).Move(e.as(e.alex))
	require.Equal(t, serializer.CodeBatchOperationNotFullyCompleted, connectedCode(err))
	x.Add("write-move-partly-failed", nil, err)

	ids = e.idsOf(t, e.root+"/Docs")
	err = (&DeleteFileService{Uris: []string{e.root + "/Docs/journal.txt"}, Ids: ids}).Delete(e.as(e.alex))
	require.NoError(t, err)
	x.Add("write-delete", nil, nil)

	// An upload: the session, the answer to a chunk that is not the last, the state in
	// between, the answer to the last chunk, the state at the end.
	data := uploadPayload(connected.UploadChunkSize + 1000)
	session, err := (&CreateUploadSessionService{Uri: e.root + "/Work/big.bin", Size: int64(len(data))}).Create(e.as(e.alex))
	x.Add("upload-session", session, err)
	require.NoError(t, err)
	first, err := (&UploadService{ID: session.SessionID, Index: 0}).ConnectedUploadChunk(e.withBody(e.alex, http.MethodPost, data[:connected.UploadChunkSize]))
	require.NoError(t, err)
	require.Nil(t, first)
	x.Add("upload-chunk", nil, nil)
	state, err := ConnectedUploadState(e.as(e.alex), session.SessionID)
	x.Add("upload-state-receiving", state, err)
	require.NoError(t, err)
	last, err := (&UploadService{ID: session.SessionID, Index: 1}).ConnectedUploadChunk(e.withBody(e.alex, http.MethodPost, data[connected.UploadChunkSize:]))
	x.Add("upload-last-chunk", last, err)
	require.NoError(t, err)
	state, err = ConnectedUploadState(e.as(e.alex), session.SessionID)
	x.Add("upload-state-done", state, err)
	require.NoError(t, err)

	// A save from an editor, and one that names a version the file no longer has.
	saved, err := (&FileUpdateService{Uri: e.root + "/Work/todo.txt", Previous: file.PrimaryEntity}).PutContent(e.withBody(e.alex, http.MethodPut, []byte("buy bread")), nil)
	x.Add("write-save", saved, err)
	require.NoError(t, err)
	_, err = (&FileUpdateService{Uri: e.root + "/Work/todo.txt", Previous: file.PrimaryEntity}).PutContent(e.withBody(e.alex, http.MethodPut, []byte("buy apples")), nil)
	require.Equal(t, serializer.CodeStaleVersion, connectedCode(err))
	x.Add("error-write-stale-version", nil, err)

	// What a client can run into.
	_, err = (&RenameFileService{Uri: e.root + "/taken.txt", NewName: "x.txt"}).Rename(e.as(e.alex))
	require.Equal(t, connected.ReasonChanged, connected.ReasonOf(err))
	x.Add("error-write-changed", nil, err)

	_, err = (&CreateFileService{Uri: e.root + "/Work", Type: "folder", ErrOnConflict: true}).Create(e.as(e.alex))
	require.Equal(t, serializer.CodeObjectExist, connectedCode(err))
	x.Add("error-write-name-taken", nil, err)

	_, err = (&CreateFileService{Uri: e.root + "/report.", Type: "file"}).Create(e.as(e.alex))
	require.Equal(t, serializer.CodeIllegalObjectName, connectedCode(err))
	x.Add("error-write-illegal-name", nil, err)

	ids = e.idsOf(t, e.root)
	_, err = (&RenameFileService{Uri: e.root + "/Budget.gsheet", NewName: "Plan", Ids: ids}).Rename(e.as(e.alex))
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err))
	x.Add("error-write-read-only", nil, err)

	_, err = (&FileUpdateService{Uri: e.root + "/gone.txt", PreviousRequired: true}).PutContent(e.withBody(e.alex, http.MethodPut, []byte("x")), nil)
	require.Equal(t, serializer.CodeUploadTargetGone, connectedCode(err))
	x.Add("error-write-target-gone", nil, err)

	_, err = ConnectedUploadState(e.as(e.alex), "cu-unknown")
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
	x.Add("error-upload-session-not-found", nil, err)

	// These two are shown as they are answered; making a provider lose an answer is the
	// business of the tests in pkg/connected.
	x.Add("error-upload-outcome-unknown", nil, connected.ErrOutcomeUnknown)
	x.Add("error-upload-send-chunk-again", nil, connected.ErrSendAgain)
}
