package connected

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive/drivetest"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/graph"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/graph/graphtest"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/stretchr/testify/require"
)

// testChunk is the chunk size of these tests: the smallest multiple of the granularity
// of both providers (256 KiB and 320 KiB).
const testChunk = 1280 * 1024

const testToken = `{"access_token":"access-1","token_type":"Bearer","refresh_token":"refresh-1","expiry":"2099-01-01T00:00:00Z"}`

// simulator is what both simulated providers offer for setting up a test.
type simulator interface {
	AddFolder(parentID, name string) string
	AddFile(parentID, name string, content []byte) string
	SetContent(id string, content []byte)
	Rename(id, name string)
}

// hooked lets a test answer requests to the provider itself.
type hooked struct {
	base http.RoundTripper
	hook func(r *http.Request) (*http.Response, error)
}

func (h *hooked) RoundTrip(r *http.Request) (*http.Response, error) {
	if h.hook != nil {
		if res, err := h.hook(r); res != nil || err != nil {
			return res, err
		}
	}
	return h.base.RoundTrip(r)
}

// uploadEnv is a connection of account 7 to a simulated provider.
type uploadEnv struct {
	t    *testing.T
	f    *managerFixture
	kind string
	sim  simulator
	d    *drivetest.Server
	g    *graphtest.Server
	http *hooked
	id   string
	ctx  context.Context
	conn *Conn
}

func newUploadEnv(t *testing.T, kind string) *uploadEnv {
	before := chunkSize
	chunkSize = testChunk
	t.Cleanup(func() { chunkSize = before })

	e := &uploadEnv{t: t, f: newManagerFixture(t), kind: kind}
	// Nothing goes to a real provider, also not when the test disconnects.
	e.f.specs[kind].Revoke = nil
	switch kind {
	case ProviderDrive:
		e.d = drivetest.New(t)
		e.d.CheckRanges(true)
		e.sim = e.d
		e.http = &hooked{base: http.DefaultTransport}
		client := &http.Client{Transport: e.http, CheckRedirect: e.d.Client().CheckRedirect}
		e.f.specs[kind].NewProvider = func(_ *http.Client, auth provider.Auth, _ map[string]string) provider.Provider {
			return drive.New(drive.Options{HTTP: client, Auth: auth, BaseURL: e.d.URL()})
		}
	default:
		e.g = graphtest.New(t)
		e.sim = e.g
		e.http = &hooked{base: http.DefaultTransport}
		client := &http.Client{Transport: e.http}
		e.f.specs[kind].NewProvider = func(_ *http.Client, auth provider.Auth, _ map[string]string) provider.Provider {
			return graph.New(graph.Options{HTTP: client, Auth: auth, DriveID: e.g.DriveID(), BaseURL: e.g.URL()})
		}
	}
	storage, err := e.f.Create(context.Background(), 7, kind, &Identity{Key: "account-1", Label: "user@example.com"}, testToken)
	require.NoError(t, err)
	e.id = storage.ID
	e.conn, e.ctx = e.use(7, e.id)
	return e
}

// use admits a request of an account to a connection.
func (e *uploadEnv) use(userID int, id string) (*Conn, context.Context) {
	c, ctx, done, err := e.f.Use(context.Background(), userID, id)
	require.NoError(e.t, err)
	e.t.Cleanup(done)
	return c, ctx
}

func (e *uploadEnv) breakNext(keep int64) {
	if e.d != nil {
		e.d.BreakNextRange(keep)
	} else {
		e.g.BreakNextFragment(keep)
	}
}

func (e *uploadEnv) loseNextAnswer() {
	if e.d != nil {
		e.d.LoseNextAnswer()
	} else {
		e.g.LoseNextUploadReceipt()
	}
}

func (e *uploadEnv) forgetSessions() {
	if e.d != nil {
		e.d.ForgetSessions()
	} else {
		e.g.ForgetUploads()
	}
}

func (e *uploadEnv) fill() {
	if e.d != nil {
		e.d.SetFull(true)
	} else {
		e.g.FillStorage()
	}
}

// ranges counts the requests that passed bytes on (or asked or cancelled at Graph,
// whose session address takes all of them).
func (e *uploadEnv) ranges() int {
	if e.d != nil {
		return e.d.Calls(drivetest.CallRange)
	}
	return e.g.Calls("upload")
}

func payload(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i/4099)
	}
	return b
}

func (e *uploadEnv) start(path string, size int, r UploadRequest) (*UploadInfo, error) {
	r.Path, r.Size = path, int64(size)
	return e.f.StartUpload(e.ctx, e.conn, r)
}

// chunk sends chunk index of data.
func (e *uploadEnv) chunk(id string, index int, data []byte) (*Row, error) {
	from := min(index*testChunk, len(data))
	to := min(from+testChunk, len(data))
	return e.f.UploadChunk(e.ctx, e.conn, id, index, bytes.NewReader(data[from:to]), int64(to-from))
}

// send sends all chunks of data from index on and returns what the last one answered.
func (e *uploadEnv) send(id string, from int, data []byte) (*Row, error) {
	chunks := max(1, (len(data)+testChunk-1)/testChunk)
	for i := from; i < chunks; i++ {
		row, err := e.chunk(id, i, data)
		if err != nil || i == chunks-1 {
			return row, err
		}
		require.Nil(e.t, row)
	}
	return nil, nil
}

func (e *uploadEnv) upload(path string, data []byte, r UploadRequest) (*Row, error) {
	info, err := e.start(path, len(data), r)
	if err != nil {
		return nil, err
	}
	return e.send(info.ID, 0, data)
}

// content reads a file back from the provider.
func (e *uploadEnv) content(path string) []byte {
	row, err := e.f.Tree().Resolve(e.ctx, e.conn, path)
	require.NoError(e.t, err)
	got, err := e.conn.P.Open(e.ctx, &row.Item, "")
	require.NoError(e.t, err)
	defer got.Body.Close()
	b, err := io.ReadAll(got.Body)
	require.NoError(e.t, err)
	return b
}

func (e *uploadEnv) status(id string) *UploadStatus {
	st, err := e.f.UploadStatus(context.Background(), 7, id)
	require.NoError(e.t, err)
	return st
}

func forBoth(t *testing.T, test func(t *testing.T, e *uploadEnv)) {
	for _, kind := range []string{ProviderDrive, ProviderOneDrive} {
		t.Run(kind, func(t *testing.T) { test(t, newUploadEnv(t, kind)) })
	}
}

func TestUploadSizes(t *testing.T) {
	for _, tc := range []struct {
		name string
		size int
	}{
		{"several whole chunks", 3 * testChunk},
		{"last chunk shorter", 2*testChunk + 12345},
		{"last chunk only the tail", testChunk + 1000},
		{"one small chunk", 1000},
		{"one whole chunk", testChunk},
		{"one byte", 1},
		{"empty", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forBoth(t, func(t *testing.T, e *uploadEnv) {
				folder := e.sim.AddFolder("", "Docs")
				e.sim.AddFile(folder, "other.txt", []byte("x"))
				// A listing in memory, which the upload has to end.
				page, err := e.f.Tree().List(e.ctx, e.conn, "Docs", Order{}, 10, "")
				require.NoError(t, err)
				require.Len(t, page.Rows, 1)

				data := payload(tc.size)
				info, err := e.start("Docs/new.bin", tc.size, UploadRequest{})
				require.NoError(t, err)
				require.True(t, IsUploadID(info.ID))
				require.Equal(t, int64(testChunk), info.ChunkSize)
				require.Equal(t, UploadReceiving, e.status(info.ID).State)

				row, err := e.send(info.ID, 0, data)
				require.NoError(t, err)
				require.NotNil(t, row)
				require.Equal(t, "new.bin", row.Name)
				require.Equal(t, "Docs/new.bin", row.Path)
				require.Equal(t, int64(tc.size), row.Size)
				require.NotEmpty(t, row.Version)

				st := e.status(info.ID)
				require.Equal(t, UploadDone, st.State)
				require.Equal(t, int64(tc.size), st.Received)
				require.Equal(t, row, st.Row)
				require.NoError(t, st.Err)
				require.Equal(t, data, e.content("Docs/new.bin"))

				page, err = e.f.Tree().List(e.ctx, e.conn, "Docs", Order{}, 10, "")
				require.NoError(t, err)
				require.Equal(t, "new.bin|other.txt", rowNames(page.Rows))
			})
		})
	}
}

func TestUploadChunkOrder(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		data := payload(2*testChunk + 500)
		info, err := e.start("a.bin", len(data), UploadRequest{})
		require.NoError(t, err)

		// Index and length must fit the upload exactly.
		_, err = e.f.UploadChunk(e.ctx, e.conn, info.ID, 0, bytes.NewReader(data[:10]), 10)
		require.Equal(t, serializer.CodeInvalidContentLength, codeOf(err))
		_, err = e.f.UploadChunk(e.ctx, e.conn, info.ID, 3, bytes.NewReader(nil), 0)
		require.Equal(t, serializer.CodeInvalidContentLength, codeOf(err))
		_, err = e.f.UploadChunk(e.ctx, e.conn, info.ID, -1, bytes.NewReader(nil), 0)
		require.Equal(t, serializer.CodeInvalidContentLength, codeOf(err))

		_, err = e.chunk(info.ID, 0, data)
		require.NoError(t, err)
		sent := e.ranges()
		// A repeated chunk is acknowledged and not passed on again.
		row, err := e.chunk(info.ID, 0, data)
		require.NoError(t, err)
		require.Nil(t, row)
		require.Equal(t, sent, e.ranges())
		// One that skips a chunk is refused.
		_, err = e.chunk(info.ID, 2, data)
		require.ErrorIs(t, err, ErrChunkOrder)
		require.Equal(t, sent, e.ranges())

		row, err = e.send(info.ID, 1, data)
		require.NoError(t, err)
		require.NotNil(t, row)
		sent = e.ranges()
		// The last chunk again gets the same answer, the others are acknowledged.
		again, err := e.chunk(info.ID, 2, data)
		require.NoError(t, err)
		require.Equal(t, row, again)
		again, err = e.chunk(info.ID, 0, data)
		require.NoError(t, err)
		require.Nil(t, again)
		require.Equal(t, sent, e.ranges())
		require.Equal(t, data, e.content("a.bin"))
	})
}

func TestUploadBrokenChunk(t *testing.T) {
	for _, tc := range []struct {
		name string
		// keep is how much of the broken range arrives.
		keep  int64
		chunk int
	}{
		{"after one byte", 1, 1},
		{"after a few bytes", 1000, 1},
		{"after one step of Drive", 256 * 1024, 1},
		{"after one step of OneDrive", 320 * 1024, 1},
		{"in the first chunk", 4321, 0},
		// A break in the tail is another matter: see the tests of the tail below.
	} {
		t.Run(tc.name, func(t *testing.T) {
			forBoth(t, func(t *testing.T, e *uploadEnv) {
				data := payload(2*testChunk + 1000)
				info, err := e.start("a.bin", len(data), UploadRequest{})
				require.NoError(t, err)
				for i := 0; i < tc.chunk; i++ {
					_, err := e.chunk(info.ID, i, data)
					require.NoError(t, err)
				}

				e.breakNext(tc.keep)
				_, err = e.chunk(info.ID, tc.chunk, data)
				require.ErrorIs(t, err, ErrSendAgain)
				require.Equal(t, UploadReceiving, e.status(info.ID).State)

				// The same chunk again: only what the provider lacks goes on.
				row, err := e.send(info.ID, tc.chunk, data)
				require.NoError(t, err)
				require.NotNil(t, row)
				require.Equal(t, int64(len(data)), row.Size)
				require.Equal(t, UploadDone, e.status(info.ID).State)
				require.Equal(t, data, e.content("a.bin"))
			})
		})
	}
}

func TestUploadLostReceipt(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		data := payload(testChunk + 1000)
		info, err := e.start("a.bin", len(data), UploadRequest{})
		require.NoError(t, err)
		_, err = e.chunk(info.ID, 0, data)
		require.NoError(t, err)

		// The answer to the last range gets lost, and the session cannot tell either.
		e.loseNextAnswer()
		if e.d != nil {
			// Drive's session would tell; here it does not answer.
			e.http.hook = func(r *http.Request) (*http.Response, error) {
				if strings.HasPrefix(r.Header.Get("Content-Range"), "bytes */") {
					return nil, errors.New("no answer")
				}
				return nil, nil
			}
		}
		_, err = e.chunk(info.ID, 1, data)
		require.Equal(t, serializer.CodeConnectedOutcomeUnknown, codeOf(err))
		e.http.hook = nil

		st := e.status(info.ID)
		require.Equal(t, UploadUnknown, st.State)
		require.Equal(t, serializer.CodeConnectedOutcomeUnknown, codeOf(st.Err))
		require.Nil(t, st.Row)

		// Nothing is sent again: not by a repetition, not by a cancel.
		sent := e.ranges()
		_, err = e.chunk(info.ID, 1, data)
		require.Equal(t, serializer.CodeConnectedOutcomeUnknown, codeOf(err))
		require.Equal(t, serializer.CodeConnectedOutcomeUnknown, codeOf(e.f.CancelUpload(e.ctx, e.conn, info.ID)))
		require.Equal(t, sent, e.ranges())
		require.Equal(t, UploadUnknown, e.status(info.ID).State)
	})
}

func TestUploadLostReceiptDriveTells(t *testing.T) {
	// Drive's session answers with the stored file when it is asked after a lost receipt.
	e := newUploadEnv(t, ProviderDrive)
	data := payload(testChunk + 1000)
	info, err := e.start("a.bin", len(data), UploadRequest{})
	require.NoError(t, err)
	_, err = e.chunk(info.ID, 0, data)
	require.NoError(t, err)
	e.loseNextAnswer()
	row, err := e.chunk(info.ID, 1, data)
	require.NoError(t, err)
	require.Equal(t, int64(len(data)), row.Size)
	require.Equal(t, UploadDone, e.status(info.ID).State)
	require.Equal(t, 1, e.d.Calls(drivetest.CallStatus))
}

func TestUploadRefusedByTheProvider(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault func(e *uploadEnv)
		code  int
	}{
		{"session forgotten", (*uploadEnv).forgetSessions, serializer.CodeUploadSessionExpired},
		{"storage full", (*uploadEnv).fill, serializer.CodeConnectedFull},
	} {
		t.Run(tc.name, func(t *testing.T) {
			forBoth(t, func(t *testing.T, e *uploadEnv) {
				data := payload(2*testChunk + 1000)
				info, err := e.start("a.bin", len(data), UploadRequest{})
				require.NoError(t, err)
				_, err = e.chunk(info.ID, 0, data)
				require.NoError(t, err)

				tc.fault(e)
				_, err = e.chunk(info.ID, 1, data)
				require.Equal(t, tc.code, codeOf(err))
				st := e.status(info.ID)
				require.Equal(t, UploadFailed, st.State)
				require.Equal(t, tc.code, codeOf(st.Err))
				// The upload is over; the client may start a new one.
				_, err = e.chunk(info.ID, 1, data)
				require.Equal(t, tc.code, codeOf(err))
				_, err = e.f.Tree().Resolve(e.ctx, e.conn, "a.bin")
				require.ErrorIs(t, err, ErrNotFound)
			})
		})
	}
}

func TestUploadNames(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		e.sim.AddFile("", "taken.txt", []byte("x"))
		_, err := e.start("taken.txt", 10, UploadRequest{})
		require.Equal(t, serializer.CodeObjectExist, codeOf(err))
		_, err = e.start("taken.txt", 0, UploadRequest{})
		require.Equal(t, serializer.CodeObjectExist, codeOf(err))
		_, err = e.start("Taken.txt", 10, UploadRequest{})
		if e.d == nil {
			// OneDrive ignores case.
			require.Equal(t, serializer.CodeNameCaseTaken, codeOf(err))
		} else {
			require.NoError(t, err)
		}
		for _, name := range []string{"a:b", "star*", "q?.txt"} {
			_, err = e.start(name, 10, UploadRequest{})
			require.Equal(t, serializer.CodeIllegalObjectName, codeOf(err), name)
		}
		_, err = e.start("nowhere/a.txt", 10, UploadRequest{})
		require.ErrorIs(t, err, ErrNotFound)
		_, err = e.start("", 10, UploadRequest{})
		require.Error(t, err)
	})
}

func TestUploadNameTakenMeanwhile(t *testing.T) {
	for _, size := range []int{testChunk + 1000, 0} {
		t.Run(fmt.Sprintf("size %d", size), func(t *testing.T) {
			forBoth(t, func(t *testing.T, e *uploadEnv) {
				data := payload(size)
				info, err := e.start("a.bin", size, UploadRequest{})
				require.NoError(t, err)
				if size > 0 {
					_, err = e.chunk(info.ID, 0, data)
					require.NoError(t, err)
				}
				first := e.sim.AddFile("", "a.bin", []byte("first"))

				// Drive is looked at before the tail is sent; OneDrive refuses the tail.
				sent := e.ranges()
				_, err = e.send(info.ID, max(0, (size-1)/testChunk), data)
				require.Equal(t, serializer.CodeObjectExist, codeOf(err))
				if e.d != nil {
					require.Equal(t, sent, e.ranges(), "the tail was not sent")
				}
				require.Equal(t, UploadFailed, e.status(info.ID).State)

				row, err := e.f.Tree().Resolve(e.ctx, e.conn, "a.bin")
				require.NoError(t, err)
				require.Equal(t, first, row.ID)
				require.Equal(t, []byte("first"), e.content("a.bin"))
			})
		})
	}
}

func TestUploadReplace(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		id := e.sim.AddFile("", "notes.txt", []byte("version one"))
		row, err := e.f.Tree().Resolve(e.ctx, e.conn, "notes.txt")
		require.NoError(t, err)
		first := VersionID(e.id, row)

		// Without leave to replace the name is taken.
		data := payload(testChunk + 7)
		_, err = e.start("notes.txt", len(data), UploadRequest{})
		require.Equal(t, serializer.CodeObjectExist, codeOf(err))

		// A stale version is refused at the start.
		_, err = e.start("notes.txt", len(data), UploadRequest{Replace: true, Previous: "v0000"})
		require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
		// Emptying a file is not offered.
		_, err = e.start("notes.txt", 0, UploadRequest{Replace: true})
		require.ErrorIs(t, err, ErrUnsupported)

		replaced, err := e.upload("notes.txt", data, UploadRequest{Replace: true, Previous: first})
		require.NoError(t, err)
		require.Equal(t, id, replaced.ID)
		require.NotEqual(t, first, VersionID(e.id, replaced))
		require.Equal(t, data, e.content("notes.txt"))

		// The version the client saw is gone now.
		_, err = e.start("notes.txt", 5, UploadRequest{Replace: true, Previous: first})
		require.Equal(t, serializer.CodeStaleVersion, codeOf(err))

		// previous_required without a file there.
		_, err = e.start("missing.txt", 5, UploadRequest{Replace: true, PreviousRequired: true})
		require.Equal(t, serializer.CodeUploadTargetGone, codeOf(err))
		// Without it, a new file.
		created, err := e.upload("missing.txt", []byte("hello"), UploadRequest{Replace: true, Previous: first})
		require.NoError(t, err)
		require.NotEqual(t, id, created.ID)
	})
}

func TestUploadReplaceChangedMeanwhile(t *testing.T) {
	for _, change := range []string{"content", "renamed away", "other file in its place"} {
		t.Run(change, func(t *testing.T) {
			forBoth(t, func(t *testing.T, e *uploadEnv) {
				id := e.sim.AddFile("", "notes.txt", []byte("version one"))
				data := payload(testChunk + 7)
				info, err := e.start("notes.txt", len(data), UploadRequest{Replace: true})
				require.NoError(t, err)
				_, err = e.chunk(info.ID, 0, data)
				require.NoError(t, err)

				switch change {
				case "content":
					e.sim.SetContent(id, []byte("written elsewhere"))
				case "renamed away":
					e.sim.Rename(id, "elsewhere.txt")
				default:
					e.sim.Rename(id, "elsewhere.txt")
					e.sim.AddFile("", "notes.txt", []byte("new"))
				}
				sent := e.ranges()
				_, err = e.chunk(info.ID, 1, data)
				require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
				require.Equal(t, UploadFailed, e.status(info.ID).State)
				if e.d != nil {
					require.Equal(t, sent, e.ranges(), "the tail was not sent")
				}
				if change == "content" {
					require.Equal(t, []byte("written elsewhere"), e.content("notes.txt"))
				}
			})
		})
	}
}

// Decision 11: a change between the look before the commit and the commit itself. OneDrive
// refuses the tail by the eTag the session was opened with; Drive takes no condition, and
// the later save wins.
func TestUploadReplaceChangedRightBeforeTheTail(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		id := e.sim.AddFile("", "notes.txt", []byte("version one"))
		data := payload(testChunk + 7)
		info, err := e.start("notes.txt", len(data), UploadRequest{Replace: true})
		require.NoError(t, err)
		_, err = e.chunk(info.ID, 0, data)
		require.NoError(t, err)

		e.http.hook = func(r *http.Request) (*http.Response, error) {
			var first, last, total int64
			if n, _ := fmt.Sscanf(r.Header.Get("Content-Range"), "bytes %d-%d/%d", &first, &last, &total); n == 3 && last == total-1 {
				e.http.hook = nil
				e.sim.SetContent(id, []byte("written elsewhere"))
			}
			return nil, nil
		}
		row, err := e.chunk(info.ID, 1, data)
		if e.d != nil {
			require.NoError(t, err)
			require.Equal(t, id, row.ID)
			require.Equal(t, data, e.content("notes.txt"))
			return
		}
		require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
		require.Equal(t, UploadFailed, e.status(info.ID).State)
		require.Equal(t, []byte("written elsewhere"), e.content("notes.txt"))
	})
}

func TestUploadCannotReplaceLinksAndDocuments(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		if e.d != nil {
			target := e.d.AddFile("", "target.txt", []byte("x"))
			e.d.AddShortcut("", "link.txt", target)
			e.d.AddNative("", "Budget", "gsheet")
			_, err := e.start("Budget.gsheet", 5, UploadRequest{Replace: true})
			require.ErrorIs(t, err, ErrUnsupported)
		} else {
			e.g.AddRemote("", "link.txt", false, []byte("x"))
		}
		_, err := e.start("link.txt", 5, UploadRequest{Replace: true})
		require.ErrorIs(t, err, ErrUnsupported)
		folder := e.sim.AddFolder("", "Folder")
		e.sim.AddFile(folder, "inside.txt", []byte("x"))
		_, err = e.start("Folder", 5, UploadRequest{Replace: true})
		require.Equal(t, serializer.CodeObjectExist, codeOf(err))
	})
}

func TestUploadCancel(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		data := payload(testChunk + 1000)
		info, err := e.start("a.bin", len(data), UploadRequest{})
		require.NoError(t, err)
		_, err = e.chunk(info.ID, 0, data)
		require.NoError(t, err)

		require.NoError(t, e.f.CancelUpload(e.ctx, e.conn, info.ID))
		if e.d != nil {
			require.Equal(t, 1, e.d.Calls(drivetest.CallCancel))
		}
		st := e.status(info.ID)
		require.Equal(t, UploadFailed, st.State)
		require.ErrorIs(t, st.Err, ErrUploadCancelled)
		_, err = e.chunk(info.ID, 1, data)
		require.ErrorIs(t, err, ErrUploadCancelled)
		require.NoError(t, e.f.CancelUpload(e.ctx, e.conn, info.ID))
		_, err = e.f.Tree().Resolve(e.ctx, e.conn, "a.bin")
		require.ErrorIs(t, err, ErrNotFound)

		// After the end a cancel touches nothing.
		info, err = e.start("b.bin", len(data), UploadRequest{})
		require.NoError(t, err)
		row, err := e.send(info.ID, 0, data)
		require.NoError(t, err)
		sent := e.ranges()
		require.NoError(t, e.f.CancelUpload(e.ctx, e.conn, info.ID))
		require.Equal(t, sent, e.ranges())
		require.Equal(t, UploadDone, e.status(info.ID).State)
		require.Equal(t, row, e.status(info.ID).Row)
		require.Equal(t, data, e.content("b.bin"))
	})
}

func TestUploadBelongsToItsAccount(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	data := payload(1000)
	info, err := e.start("a.bin", len(data), UploadRequest{})
	require.NoError(t, err)

	other, err := e.f.Create(context.Background(), 8, ProviderDrive, &Identity{Key: "account-2", Label: "user@example.com"}, testToken)
	require.NoError(t, err)
	conn, ctx := e.use(8, other.ID)

	for _, id := range []string{info.ID, "cu-unknown"} {
		_, err = e.f.UploadStatus(context.Background(), 8, id)
		require.Equal(t, serializer.CodeParentNotExist, codeOf(err))
		_, err = e.f.UploadConnection(8, id)
		require.Equal(t, serializer.CodeParentNotExist, codeOf(err))
		_, err = e.f.UploadChunk(ctx, conn, id, 0, bytes.NewReader(data), int64(len(data)))
		require.Equal(t, serializer.CodeParentNotExist, codeOf(err))
		require.Equal(t, serializer.CodeParentNotExist, codeOf(e.f.CancelUpload(ctx, conn, id)))
	}
	_, err = e.f.UploadStatus(context.Background(), 7, "cu-unknown")
	require.Equal(t, serializer.CodeParentNotExist, codeOf(err))

	// Untouched by all of that.
	require.Equal(t, UploadReceiving, e.status(info.ID).State)
	row, err := e.send(info.ID, 0, data)
	require.NoError(t, err)
	require.NotNil(t, row)
}

func TestUploadEndsWithTheConnection(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		data := payload(testChunk + 1000)
		info, err := e.start("a.bin", len(data), UploadRequest{})
		require.NoError(t, err)
		_, err = e.chunk(info.ID, 0, data)
		require.NoError(t, err)

		require.NoError(t, e.f.Disconnect(context.Background(), 7, e.id))
		// The session ended with its connection, and the state route no longer tells
		// of it: the storage is none of the account's any more.
		state, _, endErr := e.f.uploads.byID[info.ID].outcome()
		require.Equal(t, UploadFailed, state)
		require.ErrorIs(t, endErr, ErrUploadGone)
		_, err = e.f.UploadStatus(context.Background(), 7, info.ID)
		require.ErrorIs(t, err, ErrNoStorage)
		_, err = e.chunk(info.ID, 1, data)
		require.ErrorIs(t, err, ErrUploadGone)
		if e.d != nil {
			require.Zero(t, e.d.Calls(drivetest.CallCancel))
		}
	})
}

func TestUploadPlacesForChunks(t *testing.T) {
	e := newUploadEnv(t, ProviderOneDrive)
	before := uploadWait
	uploadWait = time.Millisecond
	t.Cleanup(func() { uploadWait = before })

	data := payload(testChunk + 1000)
	info, err := e.start("a.bin", len(data), UploadRequest{})
	require.NoError(t, err)

	// Both places are taken by other requests.
	e.f.uploads.sem <- struct{}{}
	e.f.uploads.sem <- struct{}{}
	_, err = e.chunk(info.ID, 0, data)
	require.ErrorIs(t, err, ErrBusy)
	require.Zero(t, e.ranges())
	require.Equal(t, UploadReceiving, e.status(info.ID).State)

	<-e.f.uploads.sem
	row, err := e.send(info.ID, 0, data)
	require.NoError(t, err)
	require.NotNil(t, row)
	require.Len(t, e.f.uploads.sem, 1, "the place was given back")
}

func TestUploadSessionsExpire(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	now := time.Now()
	e.f.uploads.now = func() time.Time { return now }

	idle, err := e.start("idle.bin", 1000, UploadRequest{})
	require.NoError(t, err)
	done, err := e.upload("done.bin", payload(1000), UploadRequest{})
	require.NoError(t, err)
	require.NotNil(t, done)

	// The outcome is told for an hour, an idle session lasts a day.
	now = now.Add(59 * time.Minute)
	sessions := len(e.f.uploads.byID)
	_, err = e.f.UploadStatus(context.Background(), 7, idle.ID)
	require.NoError(t, err)
	require.Len(t, e.f.uploads.byID, sessions)
	now = now.Add(2 * time.Minute)
	_, err = e.f.UploadStatus(context.Background(), 7, idle.ID)
	require.NoError(t, err)
	require.Len(t, e.f.uploads.byID, sessions-1, "the ended session is gone")

	require.Zero(t, e.d.Calls(drivetest.CallCancel))
	now = now.Add(uploadIdle)
	// An idle session ends as failed and is told as such for an hour, like any other.
	require.Equal(t, UploadFailed, e.status(idle.ID).State)
	// The provider is told in the background, not while a request waits.
	require.Eventually(t, func() bool { return e.d.Calls(drivetest.CallCancel) == 1 }, 5*time.Second, 10*time.Millisecond,
		"cancelled at the provider")
}

func TestUploadReceiptMustMatch(t *testing.T) {
	m := &Manager{uploads: newUploads()}
	sum := func(s string) string {
		h := md5.Sum([]byte(s))
		return hex.EncodeToString(h[:])
	}
	for _, tc := range []struct {
		name    string
		replace string
		item    provider.Item
		state   string
	}{
		{"matches", "", provider.Item{ID: "f1", Size: 3, Hash: sum("abc")}, UploadDone},
		{"replace matches", "f1", provider.Item{ID: "f1", Size: 3, Hash: sum("abc")}, UploadDone},
		{"other size", "", provider.Item{ID: "f1", Size: 4, Hash: sum("abc")}, UploadUnknown},
		{"other checksum", "", provider.Item{ID: "f1", Size: 3, Hash: sum("abd")}, UploadUnknown},
		{"no checksum", "", provider.Item{ID: "f1", Size: 3}, UploadUnknown},
		{"other item replaced", "f2", provider.Item{ID: "f1", Size: 3, Hash: sum("abc")}, UploadUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := &upload{size: 3, path: "a.txt", replace: tc.replace, sum: md5.New(), state: UploadReceiving}
			u.sum.Write([]byte("abc"))
			row, err := m.finish(u, &tc.item, true)
			require.Equal(t, tc.state, u.state)
			if tc.state == UploadDone {
				require.NoError(t, err)
				require.Equal(t, "a.txt", row.Path)
			} else {
				require.ErrorIs(t, err, ErrOutcomeUnknown)
				require.Nil(t, row)
			}
		})
	}
}

// A provider that refuses to open the session for a replace: the client is told why, and
// there is no session to send into.
func TestUploadReplaceRefusedAtTheStart(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		e.sim.AddFile("", "notes.txt", []byte("version one"))
		status := http.StatusPreconditionFailed
		e.http.hook = func(r *http.Request) (*http.Response, error) {
			if strings.Contains(r.URL.Path, "createUploadSession") || r.URL.Query().Get("uploadType") == "resumable" {
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("{}")), Request: r}, nil
			}
			return nil, nil
		}
		info, err := e.start("notes.txt", testChunk, UploadRequest{Replace: true})
		require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
		require.Nil(t, info)

		status = http.StatusForbidden
		info, err = e.start("notes.txt", testChunk, UploadRequest{Replace: true})
		require.Error(t, err)
		require.Nil(t, info)

		// And the same for a new file.
		info, err = e.start("new.bin", testChunk, UploadRequest{})
		require.Error(t, err)
		require.Nil(t, info)
		require.Empty(t, e.f.uploads.byID, "no session was kept")
	})
}

// Two accounts of Fylvo that connected the same account at a provider share one lock for
// changes, so they cannot both find a name free. Another provider account has its own.
func TestChangesOfOneProviderAccountShareALock(t *testing.T) {
	f := newManagerFixture(t)
	first := &Conn{ID: "a", AccountKey: "drive|account-1"}
	second := &Conn{ID: "b", AccountKey: "drive|account-1"}
	other := &Conn{ID: "c", AccountKey: "drive|account-2"}

	done := f.writing(first)
	lock, ok := f.writes.Load(second.AccountKey)
	require.True(t, ok)
	require.False(t, lock.(*sync.Mutex).TryLock(), "the second connection has to wait")
	f.writing(other)()
	done()
	f.writing(second)()
}

func TestNewNames(t *testing.T) {
	for _, name := range []string{"a", "report 2026.pdf", "ä ö ü", strings.Repeat("a", 255)} {
		require.NoError(t, checkNewName(name), name)
	}
	for _, name := range []string{"", ".", "..", "a/b", "a:b", "what?", " report", "report ", "report.", strings.Repeat("a", 256)} {
		require.ErrorIs(t, checkNewName(name), ErrIllegalName, "%q", name)
	}
}

// isLastRange tells whether a request to the provider carries the last range of an upload.
func isLastRange(r *http.Request, size int) bool {
	return r.Method == http.MethodPut && strings.HasSuffix(strings.SplitN(r.Header.Get("Content-Range"), "/", 2)[0], fmt.Sprintf("-%d", size-1))
}

// The whole tail left this server and no answer came. Whatever the provider's session
// says a moment later, the file may be on its way into the storage: the outcome is
// unknown, and nothing is sent a second time.
func TestUploadTailHandedOverWithoutAnAnswer(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		data := payload(testChunk + 1000)
		info, err := e.start("a.bin", len(data), UploadRequest{})
		require.NoError(t, err)
		_, err = e.chunk(info.ID, 0, data)
		require.NoError(t, err)

		// The provider takes every byte of the tail and then the connection breaks. Its
		// session still says "not complete" when it is asked.
		e.http.hook = func(r *http.Request) (*http.Response, error) {
			if isLastRange(r, len(data)) {
				_, _ = io.Copy(io.Discard, r.Body)
				return nil, errors.New("connection reset")
			}
			return nil, nil
		}
		_, err = e.chunk(info.ID, 1, data)
		require.Equal(t, serializer.CodeConnectedOutcomeUnknown, codeOf(err))
		e.http.hook = nil
		require.Equal(t, UploadUnknown, e.status(info.ID).State)
		sent := e.ranges()
		_, err = e.chunk(info.ID, 1, data)
		require.Equal(t, serializer.CodeConnectedOutcomeUnknown, codeOf(err))
		require.Equal(t, sent, e.ranges(), "nothing was sent again")
	})
}

// Not one byte of the tail left: nothing can have been stored, so the last chunk is
// simply sent again.
func TestUploadTailNotHandedOver(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		data := payload(testChunk + 1000)
		info, err := e.start("a.bin", len(data), UploadRequest{})
		require.NoError(t, err)
		_, err = e.chunk(info.ID, 0, data)
		require.NoError(t, err)

		e.http.hook = func(r *http.Request) (*http.Response, error) {
			if isLastRange(r, len(data)) {
				return nil, errors.New("no connection")
			}
			return nil, nil
		}
		_, err = e.chunk(info.ID, 1, data)
		require.Equal(t, serializer.CodeConnectedBusy, codeOf(err))
		e.http.hook = nil
		require.Equal(t, UploadReceiving, e.status(info.ID).State)

		row, err := e.chunk(info.ID, 1, data)
		require.NoError(t, err)
		require.NotNil(t, row)
		require.Equal(t, data, e.content("a.bin"))
	})
}

// The folder of an upload is renamed while the upload runs. The file does not end up
// under an address other than the one the upload was started for.
func TestUploadFolderRenamedMeanwhile(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		e.sim.AddFolder("", "A")
		data := payload(testChunk + 1000)
		info, err := e.start("A/file.bin", len(data), UploadRequest{})
		require.NoError(t, err)
		_, err = e.chunk(info.ID, 0, data)
		require.NoError(t, err)

		folder, err := e.f.Tree().Resolve(e.ctx, e.conn, "A")
		require.NoError(t, err)
		_, err = e.f.RenameEntry(e.ctx, e.conn, Source{Path: "A", ID: FileID(e.id, folder)}, "B")
		require.NoError(t, err)

		_, err = e.chunk(info.ID, 1, data)
		require.Equal(t, serializer.CodeParentNotExist, codeOf(err))
		require.Equal(t, UploadFailed, e.status(info.ID).State)
		_, err = e.f.Tree().ResolveNow(e.ctx, e.conn, "B/file.bin")
		require.ErrorIs(t, err, ErrNotFound, "nothing was stored in the renamed folder")
	})
}

// One account cannot pile up open uploads without end.
func TestUploadSessionsPerAccount(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	for i := 0; i < maxSessions; i++ {
		_, err := e.start(fmt.Sprintf("empty %d.txt", i), 0, UploadRequest{})
		require.NoError(t, err)
	}
	_, err := e.start("one too many.txt", 0, UploadRequest{})
	require.Equal(t, serializer.CodeConnectedBusy, codeOf(err))
}

// A provider that says "go on" for bytes that never left this server is not believed:
// it is asked what it has, and the chunk is sent again.
func TestUploadGoOnForBytesNeverSent(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		data := payload(2*testChunk + 1000)
		info, err := e.start("a.bin", len(data), UploadRequest{})
		require.NoError(t, err)

		e.http.hook = func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPut || !strings.HasPrefix(r.Header.Get("Content-Range"), "bytes 0-") {
				return nil, nil
			}
			_, _ = io.CopyN(io.Discard, r.Body, 100)
			if e.d != nil {
				return &http.Response{StatusCode: http.StatusPermanentRedirect, Body: http.NoBody,
					Header: http.Header{"Range": {fmt.Sprintf("bytes=0-%d", testChunk-1)}}}, nil
			}
			return &http.Response{StatusCode: http.StatusAccepted, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(fmt.Sprintf(`{"nextExpectedRanges":["%d-"]}`, testChunk)))}, nil
		}
		_, err = e.chunk(info.ID, 0, data)
		require.Equal(t, serializer.CodeConnectedBusy, codeOf(err))
		e.http.hook = nil
		require.Zero(t, e.status(info.ID).Received)

		row, err := e.send(info.ID, 0, data)
		require.NoError(t, err)
		require.NotNil(t, row)
		require.Equal(t, data, e.content("a.bin"))
	})
}

// A receipt from Drive that cannot be for this upload leaves the outcome unknown: one
// that comes before the whole tail left this server, and one that does not say what
// kind of entry was stored.
func TestUploadDriveReceiptThatProvesNothing(t *testing.T) {
	for _, tc := range []struct {
		name string
		read int64
		mime string
	}{
		{"before the tail left", 100, `"mimeType":"application/octet-stream",`},
		{"without a kind", 1 << 20, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newUploadEnv(t, ProviderDrive)
			data := payload(testChunk + 1000)
			info, err := e.start("a.bin", len(data), UploadRequest{})
			require.NoError(t, err)
			_, err = e.chunk(info.ID, 0, data)
			require.NoError(t, err)

			sum := md5.Sum(data)
			e.http.hook = func(r *http.Request) (*http.Response, error) {
				if !isLastRange(r, len(data)) {
					return nil, nil
				}
				_, _ = io.CopyN(io.Discard, r.Body, tc.read)
				receipt := fmt.Sprintf(`{"id":"forged","name":"a.bin",%s"size":"%d","md5Checksum":"%s"}`, tc.mime, len(data), hex.EncodeToString(sum[:]))
				return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
					Body: io.NopCloser(strings.NewReader(receipt))}, nil
			}
			_, err = e.chunk(info.ID, 1, data)
			require.Equal(t, serializer.CodeConnectedOutcomeUnknown, codeOf(err))
			require.Equal(t, UploadUnknown, e.status(info.ID).State)
		})
	}
}

// A database that does not answer is told as that, not as a session that is gone: a
// client waiting for the outcome of an upload asks again.
func TestUploadStatusWhenTheDatabaseFails(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	info, err := e.start("a.bin", 1000, UploadRequest{})
	require.NoError(t, err)
	require.NoError(t, e.f.db.Close())
	_, err = e.f.UploadStatus(context.Background(), 7, info.ID)
	require.Equal(t, serializer.CodeDBError, codeOf(err))
}

// An empty file is created with a request to the provider like any other: it waits for
// one of the places.
func TestUploadEmptyFileTakesAPlace(t *testing.T) {
	e := newUploadEnv(t, ProviderOneDrive)
	before := uploadWait
	uploadWait = time.Millisecond
	t.Cleanup(func() { uploadWait = before })

	info, err := e.start("empty.txt", 0, UploadRequest{})
	require.NoError(t, err)
	e.f.uploads.sem <- struct{}{}
	e.f.uploads.sem <- struct{}{}
	_, err = e.chunk(info.ID, 0, nil)
	require.ErrorIs(t, err, ErrBusy)
	require.Equal(t, UploadReceiving, e.status(info.ID).State)

	<-e.f.uploads.sem
	row, err := e.chunk(info.ID, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, row)
}

// A change the provider did not answer may still be carried out there. Until that is
// settled its name stays closed, so a second request cannot make a second entry with it.
func TestNameStaysClosedAfterAnUnclearChange(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	answer := func(status int) {
		e.http.hook = func(r *http.Request) (*http.Response, error) {
			if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/files") {
				return nil, nil
			}
			if status == 0 {
				return nil, errors.New("connection reset")
			}
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: http.NoBody}, nil
		}
	}

	// A clear no closes nothing.
	answer(http.StatusBadRequest)
	_, err := e.f.CreateEntry(e.ctx, e.conn, "Plain", true, true)
	require.Error(t, err)
	e.http.hook = nil
	_, err = e.f.CreateEntry(e.ctx, e.conn, "Plain", true, true)
	require.NoError(t, err)

	// No answer at all: the name is closed, in every spelling, and only this name.
	answer(0)
	_, err = e.f.CreateEntry(e.ctx, e.conn, "Work", true, true)
	require.Error(t, err)
	e.http.hook = nil
	_, err = e.f.CreateEntry(e.ctx, e.conn, "Work", true, true)
	require.ErrorIs(t, err, ErrBusy)
	_, err = e.f.CreateEntry(e.ctx, e.conn, "work", false, true)
	require.ErrorIs(t, err, ErrBusy)
	_, err = e.start("WORK", 1000, UploadRequest{})
	require.ErrorIs(t, err, ErrBusy)
	_, err = e.f.CreateEntry(e.ctx, e.conn, "Other", true, true)
	require.NoError(t, err)

	// After a while it is open again.
	now := e.f.uploads.now
	e.f.uploads.now = func() time.Time { return now().Add(unsettledFor + time.Second) }
	_, err = e.f.CreateEntry(e.ctx, e.conn, "Work", true, true)
	require.NoError(t, err)
}

// "Slow down" to the last range of an upload is a clear no: the last chunk is sent
// again, and the upload does not end as unknown.
func TestUploadLastRangeToldToSlowDown(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		data := payload(testChunk + 1000)
		info, err := e.start("a.bin", len(data), UploadRequest{})
		require.NoError(t, err)
		_, err = e.chunk(info.ID, 0, data)
		require.NoError(t, err)

		e.http.hook = func(r *http.Request) (*http.Response, error) {
			if !isLastRange(r, len(data)) {
				return nil, nil
			}
			_, _ = io.Copy(io.Discard, r.Body)
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: http.Header{}, Body: http.NoBody}, nil
		}
		_, err = e.chunk(info.ID, 1, data)
		require.Equal(t, serializer.CodeConnectedBusy, codeOf(err))
		e.http.hook = nil
		require.Equal(t, UploadReceiving, e.status(info.ID).State)

		row, err := e.chunk(info.ID, 1, data)
		require.NoError(t, err)
		require.NotNil(t, row)
		require.Equal(t, data, e.content("a.bin"))
	})
}

// "Create folder X" where X is a link to a folder does not answer with the link as if
// it were the folder that was asked for.
func TestCreateWhereALinkHasTheName(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	target := e.d.AddFolder("", "Target")
	e.d.AddShortcut("", "Team", target)
	_, err := e.f.CreateEntry(e.ctx, e.conn, "Team", true, false)
	require.ErrorIs(t, err, ErrNameTaken)
}

// A receipt the provider tells when it is asked is believed no more than one it answers
// with: not for a file whose end never left this server.
func TestUploadReceiptByAskingAfterAShortTail(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	data := payload(testChunk + 1000)
	info, err := e.start("a.bin", len(data), UploadRequest{})
	require.NoError(t, err)
	_, err = e.chunk(info.ID, 0, data)
	require.NoError(t, err)

	sum := md5.Sum(data)
	e.http.hook = func(r *http.Request) (*http.Response, error) {
		switch {
		case isLastRange(r, len(data)):
			// A part of the tail is read, then the provider fails.
			_, _ = io.CopyN(io.Discard, r.Body, 100)
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: http.NoBody}, nil
		case r.Method == http.MethodPut && strings.HasPrefix(r.Header.Get("Content-Range"), "bytes */"):
			receipt := fmt.Sprintf(`{"id":"forged","name":"a.bin","mimeType":"application/octet-stream","size":"%d","md5Checksum":"%s"}`, len(data), hex.EncodeToString(sum[:]))
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(receipt))}, nil
		}
		return nil, nil
	}
	_, err = e.chunk(info.ID, 1, data)
	require.Equal(t, serializer.CodeConnectedOutcomeUnknown, codeOf(err))
	require.Equal(t, UploadUnknown, e.status(info.ID).State)
}

// Changes are switched off while an upload is open: nothing of it goes on to the
// provider any more, not a range, not the last one, and no cancel either.
func TestUploadStopsWhenChangesAreSwitchedOff(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	data := payload(2*testChunk + 1000)
	info, err := e.start("a.bin", len(data), UploadRequest{})
	require.NoError(t, err)
	_, err = e.chunk(info.ID, 0, data)
	require.NoError(t, err)

	t.Setenv(EnvWrite, "")
	sent := e.ranges()
	for index := 1; index <= 2; index++ {
		_, err = e.chunk(info.ID, index, data)
		require.ErrorIs(t, err, ErrUnsupported)
	}
	require.NoError(t, e.f.CancelUpload(e.ctx, e.conn, info.ID))
	require.Equal(t, UploadFailed, e.status(info.ID).State)
	require.Equal(t, sent, e.ranges(), "no range was sent")
	require.Zero(t, e.d.Calls(drivetest.CallCancel), "the provider was not told")
	_, err = e.f.Tree().ResolveNow(e.ctx, e.conn, "a.bin")
	require.ErrorIs(t, err, ErrNotFound)
}

// A clear "no" of the provider to an empty file leaves nothing open: the upload can be
// tried again, and the name is not closed.
func TestUploadEmptyFileClearlyRefused(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	info, err := e.start("empty.txt", 0, UploadRequest{})
	require.NoError(t, err)
	e.http.hook = func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/files") {
			return &http.Response{StatusCode: http.StatusBadRequest, Header: http.Header{}, Body: http.NoBody}, nil
		}
		return nil, nil
	}
	_, err = e.chunk(info.ID, 0, nil)
	require.Error(t, err)
	require.NotEqual(t, serializer.CodeConnectedOutcomeUnknown, codeOf(err))
	require.Equal(t, UploadReceiving, e.status(info.ID).State)
	e.http.hook = nil

	row, err := e.chunk(info.ID, 0, nil)
	require.NoError(t, err)
	require.NotNil(t, row)
}

// Two uploads are open for one new name. The first ends with an open outcome: the second
// does not send its end while that name is closed, at either provider.
func TestUploadSecondCommitWaitsForAnOpenOutcome(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		data := payload(testChunk + 1000)
		first, err := e.start("a.bin", len(data), UploadRequest{})
		require.NoError(t, err)
		second, err := e.start("a.bin", len(data), UploadRequest{})
		require.NoError(t, err)
		for _, id := range []string{first.ID, second.ID} {
			_, err = e.chunk(id, 0, data)
			require.NoError(t, err)
		}

		// The whole tail of the first leaves, and no answer comes back.
		e.http.hook = func(r *http.Request) (*http.Response, error) {
			if isLastRange(r, len(data)) {
				_, _ = io.Copy(io.Discard, r.Body)
				return nil, errors.New("connection reset")
			}
			return nil, nil
		}
		_, err = e.chunk(first.ID, 1, data)
		require.Equal(t, serializer.CodeConnectedOutcomeUnknown, codeOf(err))
		e.http.hook = nil

		sent := e.ranges()
		_, err = e.chunk(second.ID, 1, data)
		require.Equal(t, serializer.CodeConnectedBusy, codeOf(err))
		require.Equal(t, UploadReceiving, e.status(second.ID).State)
		require.Equal(t, sent, e.ranges(), "the second upload sent nothing")
	})
}

// What a save can replace.
func TestReplaceable(t *testing.T) {
	file := func(change func(*Row)) *Row {
		row := &Row{Item: provider.Item{ID: "f1", Name: "a.txt", Version: "v1", ETag: "e1"}}
		change(row)
		return row
	}
	for _, kind := range []string{ProviderDrive, ProviderOneDrive} {
		c := &Conn{Provider: kind}
		require.True(t, c.Replaceable(file(func(*Row) {})), kind)
		require.False(t, c.Replaceable(file(func(r *Row) { r.IsDir = true })), kind)
		require.False(t, c.Replaceable(file(func(r *Row) { r.Version = "" })), kind)
		require.False(t, c.Replaceable(file(func(r *Row) { r.Native = "gdoc" })), kind)
		require.False(t, c.Replaceable(file(func(r *Row) { r.ViaLink = true })), kind)
		require.False(t, c.Replaceable(file(func(r *Row) { r.Unsupported = UnsupportedDuplicate })), kind)
	}
	// Without its tag a file at OneDrive cannot be replaced; Drive has no such tag.
	require.False(t, (&Conn{Provider: ProviderOneDrive}).Replaceable(file(func(r *Row) { r.ETag = "" })))
	require.True(t, (&Conn{Provider: ProviderDrive}).Replaceable(file(func(r *Row) { r.ETag = "" })))
}

// A call that never left this server (no token could be had) closes no name: the
// provider was not asked anything.
func TestNameStaysOpenWhenNothingWasSent(t *testing.T) {
	require.True(t, unclear(errors.New("connection reset")))
	require.True(t, unclear(&provider.Error{Kind: provider.KindBusy}))
	require.True(t, unclear(&provider.Error{Kind: provider.KindOther, Status: 503}))
	require.False(t, unclear(&provider.Error{Kind: provider.KindOther, Status: 400}))
	require.False(t, unclear(&provider.Error{Kind: provider.KindBusy, Status: 429}))
	require.False(t, unclear(nil))

	// The provider's client marks what it never sent.
	auth := failingAuth{err: &provider.Error{Kind: provider.KindBusy}}
	call := provider.Caller{HTTP: http.DefaultClient, Auth: auth, AllowHTTP: true}
	_, err := call.Do(context.Background(), http.MethodPost, "http://localhost:1/x", nil, nil)
	require.Error(t, err)
	require.False(t, provider.Sent(err))
	require.Equal(t, provider.KindBusy, provider.KindOf(err), "it is still the error it was")
	require.False(t, unclear(err))
	_, err = call.Stream(context.Background(), http.MethodPut, "http://localhost:1/x", nil, nil, 0, true)
	require.False(t, provider.Sent(err))
}

type failingAuth struct{ err error }

func (f failingAuth) Token(context.Context, string) (string, error) { return "", f.err }

// An upload of a new file whose outcome becomes open in the middle (the provider says
// "stored" to a range that is not the last) closes its name like one at the commit.
func TestUploadOpenOutcomeInTheMiddleClosesTheName(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	data := payload(2*testChunk + 1000)
	info, err := e.start("a.bin", len(data), UploadRequest{})
	require.NoError(t, err)
	e.http.hook = func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodPut && strings.HasPrefix(r.Header.Get("Content-Range"), "bytes 0-") {
			_, _ = io.Copy(io.Discard, r.Body)
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"id":"x"}`))}, nil
		}
		return nil, nil
	}
	_, err = e.chunk(info.ID, 0, data)
	require.Equal(t, serializer.CodeConnectedOutcomeUnknown, codeOf(err))
	e.http.hook = nil

	_, err = e.start("A.bin", 1000, UploadRequest{})
	require.ErrorIs(t, err, ErrBusy)
	_, err = e.f.CreateEntry(e.ctx, e.conn, "a.bin", false, true)
	require.ErrorIs(t, err, ErrBusy)
}

// renewalFails is an Auth whose first token the provider refuses and which then cannot
// get a new one.
type renewalFails struct{ asked int }

func (r *renewalFails) Token(_ context.Context, rejected string) (string, error) {
	r.asked++
	if rejected != "" {
		return "", &provider.Error{Kind: provider.KindBusy, Status: http.StatusServiceUnavailable}
	}
	return "old-token", nil
}

// The provider refuses the token (401) and no new one can be had: the call was not
// carried out, whatever the error of the renewal looks like.
func TestRefusedTokenIsAClearNo(t *testing.T) {
	refuses := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	server := httptest.NewServer(refuses)
	defer server.Close()

	auth := &renewalFails{}
	call := provider.Caller{HTTP: server.Client(), Auth: auth, AllowHTTP: true}
	_, err := call.Do(context.Background(), http.MethodPost, server.URL+"/files", nil, []byte("{}"))
	require.Error(t, err)
	require.Equal(t, 2, auth.asked)
	require.False(t, provider.Sent(err))
	require.False(t, unclear(err))

	_, err = call.Stream(context.Background(), http.MethodPut, server.URL+"/upload", nil, strings.NewReader("x"), 1, true)
	require.Error(t, err)
	require.False(t, provider.Sent(err))
	require.False(t, unclear(err))
}

// A receipt that the provider tells when it is asked is no count of bytes: also one with
// a size that would fit as "so much has arrived" ends the upload as unknown.
func TestUploadReceiptIsNoOffset(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	data := payload(testChunk + 1000)
	info, err := e.start("a.bin", len(data), UploadRequest{})
	require.NoError(t, err)
	_, err = e.chunk(info.ID, 0, data)
	require.NoError(t, err)

	e.http.hook = func(r *http.Request) (*http.Response, error) {
		switch {
		case isLastRange(r, len(data)):
			_, _ = io.CopyN(io.Discard, r.Body, 100)
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: http.NoBody}, nil
		case r.Method == http.MethodPut && strings.HasPrefix(r.Header.Get("Content-Range"), "bytes */"):
			// A stored file as large as what had arrived before the failure.
			receipt := fmt.Sprintf(`{"id":"forged","name":"a.bin","mimeType":"application/octet-stream","size":"%d","md5Checksum":"00"}`, testChunk+100)
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(strings.NewReader(receipt))}, nil
		}
		return nil, nil
	}
	_, err = e.chunk(info.ID, 1, data)
	require.Equal(t, serializer.CodeConnectedOutcomeUnknown, codeOf(err))
	require.Equal(t, UploadUnknown, e.status(info.ID).State)
	e.http.hook = nil
	_, err = e.start("a.bin", 1000, UploadRequest{})
	require.ErrorIs(t, err, ErrBusy, "the name is closed")
}

// The state of an upload names the kind of provider, so the file it tells carries the
// same capability as in a listing.
func TestUploadStatusNamesTheProvider(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		info, err := e.start("a.bin", 1000, UploadRequest{})
		require.NoError(t, err)
		require.Equal(t, e.kind, e.status(info.ID).Provider)
	})
}
