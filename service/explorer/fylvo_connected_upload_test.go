package explorer

import (
	"bytes"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive/drivetest"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/dbfs"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Fylvo: uploads into a connected storage, from the routes down to a simulated Google
// Drive.

// withBody returns a request of u that carries body with its length.
func (e *connectedEnv) withBody(u *ent.User, method string, body []byte) *gin.Context {
	c, _ := e.request(u, method, "/")
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
	c.Request.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return c
}

// sendChunks sends the chunks of data and returns the answer to the last one.
func (e *connectedEnv) sendChunks(u *ent.User, id string, data []byte) (*FileResponse, error) {
	for index := 0; ; index++ {
		from := index * connected.UploadChunkSize
		to := min(from+connected.UploadChunkSize, len(data))
		res, err := (&UploadService{ID: id, Index: index}).ConnectedUploadChunk(e.withBody(u, http.MethodPost, data[from:to]))
		if err != nil || to == len(data) {
			return res, err
		}
		require.Nil(e.t, res)
	}
}

func (e *connectedEnv) download(uri string) []byte {
	address, err := e.urlOf(e.alex, uri, true)
	require.NoError(e.t, err)
	rec := e.fetch(address, "")
	require.Equal(e.t, http.StatusOK, rec.Code)
	return rec.Body.Bytes()
}

func uploadPayload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*13 + i/7001)
	}
	return b
}

func TestConnectedUpload(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.CheckRanges(true)
	e.sim.AddFolder("", "Docs")

	// The folder tells that it takes uploads, and how.
	list, err := e.list(e.alex, e.root+"/Docs", 0, "")
	require.NoError(t, err)
	require.True(t, list.Props.Capability.Enabled(int(dbfs.NavigatorCapabilityUploadFile)))
	root, err := e.list(e.alex, e.root, 0, "")
	require.NoError(t, err)
	policy := &StoragePolicy{ID: "connected", Name: root.Parent.Name, Type: types.PolicyTypeLocal, Relay: true}
	require.Equal(t, policy, list.StoragePolicy)
	require.Zero(t, list.StoragePolicy.MaxSize)
	require.Zero(t, list.StoragePolicy.ChunkConcurrency)

	data := uploadPayload(connected.UploadChunkSize + 5000)
	uri := e.root + "/Docs/big.bin"
	session, err := (&CreateUploadSessionService{Uri: uri, Size: int64(len(data))}).Create(e.as(e.alex))
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(session.SessionID, "cu-"))
	require.Equal(t, int64(connected.UploadChunkSize), session.ChunkSize)
	require.Greater(t, session.Expires, time.Now().Unix())
	require.Equal(t, uri, session.Uri)
	require.Equal(t, policy, session.StoragePolicy)

	// Another account knows nothing of the session.
	_, err = (&UploadService{ID: session.SessionID, Index: 0}).ConnectedUploadChunk(e.withBody(e.sam, http.MethodPost, data[:connected.UploadChunkSize]))
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
	_, err = ConnectedUploadState(e.as(e.sam), session.SessionID)
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))

	file, err := e.sendChunks(e.alex, session.SessionID, data)
	require.NoError(t, err)
	require.NotNil(t, file)
	require.Equal(t, "big.bin", file.Name)
	require.Equal(t, uri, file.Path)
	require.Equal(t, int64(len(data)), file.Size)
	require.NotEmpty(t, file.PrimaryEntity)
	require.Equal(t, 2, e.sim.Calls(drivetest.CallRange))

	// The file is what the folder lists now, in exactly this version.
	info, err := e.info(e.alex, uri)
	require.NoError(t, err)
	require.Equal(t, file.ID, info.ID)
	require.Equal(t, file.PrimaryEntity, info.PrimaryEntity)
	require.Equal(t, data, e.download(uri))

	state, err := ConnectedUploadState(e.as(e.alex), session.SessionID)
	require.NoError(t, err)
	require.Equal(t, connected.UploadDone, state.State)
	require.Equal(t, int64(len(data)), state.Received)
	require.Equal(t, file, state.File)
	require.Zero(t, state.Code)
	_, err = ConnectedUploadState(e.as(e.alex), "cu-unknown")
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))

	// The last chunk again gets the same answer.
	again, err := (&UploadService{ID: session.SessionID, Index: 1}).ConnectedUploadChunk(e.withBody(e.alex, http.MethodPost, data[connected.UploadChunkSize:]))
	require.NoError(t, err)
	require.Equal(t, file, again)
	require.Equal(t, 2, e.sim.Calls(drivetest.CallRange))

	// A new version of it, naming the version it replaces.
	newer := uploadPayload(3000)
	session, err = (&CreateUploadSessionService{Uri: uri, Size: int64(len(newer)), EntityType: "version", Previous: file.PrimaryEntity}).Create(e.as(e.alex))
	require.NoError(t, err)
	replaced, err := e.sendChunks(e.alex, session.SessionID, newer)
	require.NoError(t, err)
	require.Equal(t, file.ID, replaced.ID)
	require.NotEqual(t, file.PrimaryEntity, replaced.PrimaryEntity)
	require.Equal(t, newer, e.download(uri))
	_, err = (&CreateUploadSessionService{Uri: uri, Size: 5, EntityType: "version", Previous: file.PrimaryEntity}).Create(e.as(e.alex))
	require.Equal(t, serializer.CodeStaleVersion, connectedCode(err))
	_, err = (&CreateUploadSessionService{Uri: uri, Size: 5}).Create(e.as(e.alex))
	require.Equal(t, serializer.CodeObjectExist, connectedCode(err))
	_, err = (&CreateUploadSessionService{Uri: e.root + "/Docs/gone.bin", Size: 5, EntityType: "version", PreviousRequired: true}).Create(e.as(e.alex))
	require.Equal(t, serializer.CodeUploadTargetGone, connectedCode(err))
	_, err = (&CreateUploadSessionService{Uri: e.root + "/Docs/live.jpg", Size: 5, EntityType: "live_photo"}).Create(e.as(e.alex))
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err))

	// Nobody else can start an upload there.
	_, err = (&CreateUploadSessionService{Uri: e.root + "/Docs/sam.bin", Size: 5}).Create(e.as(e.sam))
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
}

func TestConnectedUploadCancel(t *testing.T) {
	e := newConnectedEnv(t)
	uri := e.root + "/a.bin"
	session, err := (&CreateUploadSessionService{Uri: uri, Size: 10}).Create(e.as(e.alex))
	require.NoError(t, err)

	// Only by the account it belongs to.
	cancel := &DeleteUploadSessionService{ID: session.SessionID, Uri: uri}
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(cancel.Delete(e.as(e.sam))))
	require.NoError(t, cancel.Delete(e.as(e.alex)))
	require.Equal(t, 1, e.sim.Calls(drivetest.CallCancel))

	state, err := ConnectedUploadState(e.as(e.alex), session.SessionID)
	require.NoError(t, err)
	require.Equal(t, connected.UploadFailed, state.State)
	require.Equal(t, serializer.CodeUploadSessionExpired, state.Code)
	require.Nil(t, state.File)
	_, err = (&UploadService{ID: session.SessionID}).ConnectedUploadChunk(e.withBody(e.alex, http.MethodPost, []byte("0123456789")))
	require.Equal(t, serializer.CodeUploadSessionExpired, connectedCode(err))
	require.Equal(t, "", e.names(t, e.root))

	// A session id of a connected storage with an address elsewhere, and the other way.
	require.Error(t, (&DeleteUploadSessionService{ID: session.SessionID, Uri: "cloudreve://my/a.bin"}).Delete(e.as(e.alex)))
	require.Error(t, (&DeleteUploadSessionService{ID: "abc", Uri: uri}).Delete(e.as(e.alex)))
}

func TestConnectedPutContent(t *testing.T) {
	e := newConnectedEnv(t)
	uri := e.root + "/notes.txt"
	put := func(body []byte, previous string) (*FileResponse, error) {
		return (&FileUpdateService{Uri: uri, Previous: previous}).PutContent(e.withBody(e.alex, http.MethodPut, body), nil)
	}

	created, err := put([]byte("first text"), "")
	require.NoError(t, err)
	require.Equal(t, "notes.txt", created.Name)
	require.Equal(t, int64(10), created.Size)
	require.Equal(t, []byte("first text"), e.download(uri))

	saved, err := put([]byte("second text"), created.PrimaryEntity)
	require.NoError(t, err)
	require.Equal(t, created.ID, saved.ID)
	require.NotEqual(t, created.PrimaryEntity, saved.PrimaryEntity)
	require.Equal(t, []byte("second text"), e.download(uri))

	// The editor read a version that is no longer there: nothing is written.
	ranges := e.sim.Calls(drivetest.CallRange)
	_, err = put([]byte("third text"), created.PrimaryEntity)
	require.Equal(t, serializer.CodeStaleVersion, connectedCode(err))
	require.Equal(t, ranges, e.sim.Calls(drivetest.CallRange))
	require.Equal(t, []byte("second text"), e.download(uri))

	_, err = (&FileUpdateService{Uri: e.root + "/gone.txt", PreviousRequired: true}).PutContent(e.withBody(e.alex, http.MethodPut, []byte("x")), nil)
	require.Equal(t, serializer.CodeUploadTargetGone, connectedCode(err))
	_, err = (&FileUpdateService{Uri: uri}).PutContent(e.withBody(e.sam, http.MethodPut, []byte("x")), nil)
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))

	// A body that ends early: the upload is cancelled, nothing is stored.
	c := e.withBody(e.alex, http.MethodPut, []byte("short"))
	c.Request.Header.Set("Content-Length", "50")
	c.Request.Body = io.NopCloser(bytes.NewReader([]byte("short")))
	_, err = (&FileUpdateService{Uri: e.root + "/cut.txt"}).PutContent(c, nil)
	require.Error(t, err)
	require.Equal(t, "notes.txt", e.names(t, e.root))
}

func TestOrdinaryUploadsKeepTheirPath(t *testing.T) {
	e := newConnectedEnv(t)
	session, err := (&CreateUploadSessionService{Uri: "cloudreve://my/plain.txt", Size: 5}).Create(e.as(e.alex))
	require.NoError(t, err)
	require.False(t, connected.IsUploadID(session.SessionID))
	require.NotEqual(t, "connected", session.StoragePolicy.ID)
	// The session is one of the file manager, not of a connected storage.
	_, err = connected.For(e.dep).UploadConnection(e.alex.ID, session.SessionID)
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
	require.NoError(t, (&DeleteUploadSessionService{ID: session.SessionID, Uri: "cloudreve://my/plain.txt"}).Delete(e.as(e.alex)))
}
