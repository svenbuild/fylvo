package connected

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/stretchr/testify/require"
)

// openOffice opens a session for a file of the test connection.
func (e *uploadEnv) openOffice(path string, edit bool) (*OfficeSession, error) {
	return e.f.OpenOffice(e.ctx, e.conn, OfficeRequest{
		Path: path, ViewerID: "office", Edit: edit, Fingerprint: "fp", TTL: time.Hour, MaxSize: 4 << 20,
	})
}

// readOffice reads the document of a session the way the Office server gets it.
func (e *uploadEnv) readOffice(s *OfficeSession) ([]byte, bool, error) {
	leave, err := e.f.OfficeEnter(e.ctx, s)
	if err != nil {
		return nil, false, err
	}
	defer leave()
	var out bytes.Buffer
	begun := false
	err = e.f.OfficeRead(e.ctx, e.conn, s, 4<<20, func(*provider.Item) { begun = true }, &out)
	return out.Bytes(), begun, err
}

func (e *uploadEnv) saveOffice(s *OfficeSession, data []byte) (*Row, error) {
	leave, err := e.f.OfficeEnter(e.ctx, s)
	if err != nil {
		return nil, err
	}
	defer leave()
	return e.f.OfficeSave(e.ctx, e.conn, s, bytes.NewReader(data), int64(len(data)))
}

// isItemLook tells whether a request asks a provider for the state of one item.
func isItemLook(r *http.Request, id string) bool {
	return r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/"+id) && r.URL.Query().Get("alt") == ""
}

// isContent tells whether a request asks a provider for the bytes of a file.
func isContent(r *http.Request) bool {
	return r.Method == http.MethodGet && (r.URL.Query().Get("alt") == "media" || strings.HasSuffix(r.URL.Path, "/content"))
}

// Open, read, save, save again: every save replaces exactly the version the editor has.
func TestOfficeEditAndSave(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		first := payload(200000)
		id := e.sim.AddFile("", "letter.docx", first)
		s, err := e.openOffice("letter.docx", true)
		require.NoError(t, err)
		require.True(t, s.Edit)
		require.True(t, strings.HasPrefix(s.ID, officePrefix))
		require.True(t, strings.HasPrefix(s.Token, s.ID+"."))

		// Before the document was read there is nothing a save could be a newer state of.
		_, err = e.saveOffice(s, payload(1000))
		require.Equal(t, serializer.CodeStaleVersion, codeOf(err))

		got, _, err := e.readOffice(s)
		require.NoError(t, err)
		require.Equal(t, first, got)

		second := payload(300000)
		row, err := e.saveOffice(s, second)
		require.NoError(t, err)
		require.Equal(t, id, row.ID, "the same file, not a new one")
		require.Equal(t, second, e.content("letter.docx"))
		third := payload(testChunk + 5000)
		_, err = e.saveOffice(s, third)
		require.NoError(t, err, "the session follows its own saves")
		require.Equal(t, third, e.content("letter.docx"))

		info, edit, err := e.f.OfficeInfo(e.ctx, e.conn, s)
		require.NoError(t, err)
		require.True(t, edit)
		require.Equal(t, int64(len(third)), info.Size)
	})
}

// The file changes at the provider while it is open in the editor: the save is a
// conflict, and stays one until the document is read again.
func TestOfficeConflict(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		id := e.sim.AddFile("", "letter.docx", []byte("version one"))
		s, err := e.openOffice("letter.docx", true)
		require.NoError(t, err)
		_, _, err = e.readOffice(s)
		require.NoError(t, err)

		e.sim.SetContent(id, []byte("changed by somebody else"))
		_, err = e.saveOffice(s, []byte("my edit"))
		require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
		_, err = e.saveOffice(s, []byte("my edit"))
		require.Equal(t, serializer.CodeStaleVersion, codeOf(err), "asking again changes nothing")
		require.Equal(t, []byte("changed by somebody else"), e.content("letter.docx"))

		// A second window on the same file is a session of its own, and it conflicts with
		// what the first one saved.
		other, err := e.openOffice("letter.docx", true)
		require.NoError(t, err)
		got, _, err := e.readOffice(other)
		require.NoError(t, err)
		require.Equal(t, []byte("changed by somebody else"), got)
		_, err = e.saveOffice(other, []byte("the other window"))
		require.NoError(t, err)
		_, err = e.saveOffice(s, []byte("my edit"))
		require.Equal(t, serializer.CodeStaleVersion, codeOf(err))

		// Read again, the editor has what is there and can save.
		_, _, err = e.readOffice(s)
		require.NoError(t, err)
		_, err = e.saveOffice(s, []byte("my edit on top"))
		require.NoError(t, err)
		require.Equal(t, []byte("my edit on top"), e.content("letter.docx"))
	})
}

// Another file under the name of the one that was opened is not the document of the
// session: nothing is written to it.
func TestOfficeFileReplacedUnderItsName(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		id := e.sim.AddFile("", "letter.docx", []byte("the old one"))
		s, err := e.openOffice("letter.docx", true)
		require.NoError(t, err)
		_, _, err = e.readOffice(s)
		require.NoError(t, err)

		e.sim.Rename(id, "old letter.docx")
		e.sim.AddFile("", "letter.docx", []byte("another file"))
		_, err = e.saveOffice(s, []byte("my edit"))
		require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
		require.Equal(t, []byte("another file"), e.content("letter.docx"))
		require.Equal(t, []byte("the old one"), e.content("old letter.docx"))
	})
}

// Bytes that are not those of the version the provider names are not handed out whole,
// and the session does not take them for what it works on.
func TestOfficeReadChecksTheBytes(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		data := payload(3 * officeTail)
		e.sim.AddFile("", "letter.docx", data)
		s, err := e.openOffice("letter.docx", true)
		require.NoError(t, err)

		// The provider serves other bytes of the same length.
		e.http.hook = func(r *http.Request) (*http.Response, error) {
			if !isContent(r) {
				return nil, nil
			}
			other := bytes.Repeat([]byte{'x'}, len(data))
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, ContentLength: int64(len(other)),
				Body: io.NopCloser(bytes.NewReader(other))}, nil
		}
		got, begun, err := e.readOffice(s)
		require.ErrorIs(t, err, ErrOfficeBroken)
		require.True(t, begun)
		require.Len(t, got, len(data)-officeTail, "the end was held back")
		e.http.hook = nil
		_, err = e.saveOffice(s, []byte("an edit of bytes nobody checked"))
		require.Equal(t, serializer.CodeStaleVersion, codeOf(err))

		// The provider serves fewer bytes than the file has.
		e.http.hook = func(r *http.Request) (*http.Response, error) {
			if !isContent(r) {
				return nil, nil
			}
			short := data[:len(data)-10]
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, ContentLength: int64(len(short)),
				Body: io.NopCloser(bytes.NewReader(short))}, nil
		}
		got, _, err = e.readOffice(s)
		require.ErrorIs(t, err, ErrOfficeBroken)
		require.Less(t, len(got), len(data)-10)
		e.http.hook = nil

		// A file larger than may be edited is refused before a byte is read.
		var out bytes.Buffer
		err = e.f.OfficeRead(e.ctx, e.conn, s, 1000, func(*provider.Item) { t.Fatal("nothing begins") }, &out)
		require.ErrorIs(t, err, ErrOfficeTooLarge)
		require.Zero(t, out.Len())
	})
}

// What cannot be opened, and what can only be read.
func TestOfficeWhatCanBeOpened(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	folder := e.d.AddFolder("", "Shared")
	inside := e.d.AddFile(folder, "inside.docx", []byte("inside"))
	e.d.AddFile("", "plain.docx", []byte("plain"))
	e.d.AddNative("", "Budget", "gsheet")
	e.d.AddShortcut("", "Link to file.docx", inside)
	e.d.AddShortcut("", "Link to folder", folder)

	for _, path := range []string{"", "Shared", "Budget.gsheet", "Link to file.docx", "Link to folder"} {
		_, err := e.openOffice(path, true)
		require.Equal(t, serializer.CodeConnectedUnsupported, codeOf(err), path)
	}
	_, err := e.openOffice("missing.docx", true)
	require.Equal(t, serializer.CodeParentNotExist, codeOf(err))
	_, err = e.f.OpenOffice(e.ctx, e.conn, OfficeRequest{Path: "plain.docx", Edit: true, TTL: time.Hour, MaxSize: 3})
	require.ErrorIs(t, err, ErrOfficeTooLarge)

	// Through a link everything is read-only: the session reads and cannot save.
	s, err := e.openOffice("Link to folder/inside.docx", true)
	require.NoError(t, err)
	require.False(t, s.Edit)
	got, _, err := e.readOffice(s)
	require.NoError(t, err)
	require.Equal(t, []byte("inside"), got)
	_, err = e.saveOffice(s, []byte("x"))
	require.ErrorIs(t, err, ErrUnsupported)

	// A session opened for viewing does not save either.
	view, err := e.openOffice("plain.docx", false)
	require.NoError(t, err)
	_, _, err = e.readOffice(view)
	require.NoError(t, err)
	_, err = e.saveOffice(view, []byte("x"))
	require.ErrorIs(t, err, ErrUnsupported)

	// Where the installation only reads connected storages, no session saves.
	t.Setenv(EnvWrite, "")
	off, err := e.openOffice("plain.docx", true)
	require.NoError(t, err)
	require.False(t, off.Edit)
}

// A session is found by its token only, for as long as it lasts and the storage stays
// connected as it was.
func TestOfficeSessionsEnd(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	e.d.AddFile("", "plain.docx", []byte("plain"))
	s, err := e.openOffice("plain.docx", true)
	require.NoError(t, err)

	found, err := e.f.Office(s.ID, s.Token)
	require.NoError(t, err)
	require.Same(t, s, found)
	for _, token := range []string{"", s.ID, s.ID + ".", s.Token + "x", s.Token[:len(s.Token)-1]} {
		_, err = e.f.Office(s.ID, token)
		require.ErrorIs(t, err, ErrNoStorage, token)
	}
	_, err = e.f.Office("co-unknown", s.Token)
	require.ErrorIs(t, err, ErrNoStorage)

	// It runs out.
	now := e.f.offices.now
	e.f.offices.now = func() time.Time { return now().Add(2 * time.Hour) }
	_, err = e.f.Office(s.ID, s.Token)
	require.ErrorIs(t, err, ErrNoStorage)
	e.f.offices.now = now

	// An account has only so many. One more makes the one used longest ago give way,
	// unless it is running a call right now; then the next one does.
	clock := now()
	e.f.offices.now = func() time.Time { clock = clock.Add(time.Second); return clock }
	sessions := []*OfficeSession{s}
	for i := 1; i < maxOfficeSessions; i++ {
		next, err := e.openOffice("plain.docx", false)
		require.NoError(t, err)
		sessions = append(sessions, next)
	}
	busy, err := e.f.OfficeEnter(e.ctx, sessions[0])
	require.NoError(t, err)
	_, err = e.openOffice("plain.docx", false)
	require.NoError(t, err)
	require.Len(t, e.f.offices.byID, maxOfficeSessions)
	_, err = e.f.Office(sessions[0].ID, sessions[0].Token)
	require.NoError(t, err, "the one that runs a call stays")
	_, err = e.f.Office(sessions[1].ID, sessions[1].Token)
	require.ErrorIs(t, err, ErrNoStorage, "the oldest idle one gave way")
	_, err = e.f.OfficeEnter(e.ctx, sessions[1])
	require.ErrorIs(t, err, ErrOfficeGone)
	busy()

	// A call waits for its turn only as long as its request lasts, and finds a session
	// that ended meanwhile ended.
	hold, err := e.f.OfficeEnter(e.ctx, sessions[2])
	require.NoError(t, err)
	gone, cancel := context.WithCancel(e.ctx)
	cancel()
	_, err = e.f.OfficeEnter(gone, sessions[2])
	require.ErrorIs(t, err, ErrBusy)
	waited := make(chan error, 1)
	go func() {
		_, err := e.f.OfficeEnter(e.ctx, sessions[2])
		waited <- err
	}()
	e.f.CloseOffice(sessions[2])
	hold()
	require.ErrorIs(t, <-waited, ErrOfficeGone)

	// Sessions that ran out make room.
	e.f.offices.now = func() time.Time { return now().Add(2 * time.Hour) }
	fresh, err := e.openOffice("plain.docx", true)
	require.NoError(t, err)
	require.Len(t, e.f.offices.byID, 1)

	// It ends with the connection.
	_, _, err = e.readOffice(fresh)
	require.NoError(t, err)
	require.NoError(t, e.f.Disconnect(context.Background(), 7, e.id))
	_, err = e.f.Office(fresh.ID, fresh.Token)
	require.ErrorIs(t, err, ErrNoStorage)
	_, err = e.saveOffice(fresh, []byte("after the end"))
	require.ErrorIs(t, err, ErrOfficeGone)
}

// A conflict stays one until the document is read again, also when a later save would
// find the version the editor once had. And something else under the name of the file (a
// folder here) is a conflict too, not an error of another kind.
func TestOfficeConflictStays(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		id := e.sim.AddFile("", "letter.docx", []byte("version one"))
		s, err := e.openOffice("letter.docx", true)
		require.NoError(t, err)
		_, _, err = e.readOffice(s)
		require.NoError(t, err)
		had := s.version

		e.sim.SetContent(id, []byte("changed by somebody else"))
		_, err = e.saveOffice(s, []byte("my edit"))
		require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
		require.NotEmpty(t, had)
		// The session forgot the version it had: whatever the file looks like later,
		// also like that version again, nothing is saved before a new read.
		require.Empty(t, s.version)
		sent := e.ranges()
		_, err = e.saveOffice(s, []byte("my edit"))
		require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
		require.Equal(t, sent, e.ranges(), "the provider was not even asked to take it")

		_, _, err = e.readOffice(s)
		require.NoError(t, err)
		e.sim.Rename(id, "old letter.docx")
		e.sim.AddFolder("", "letter.docx")
		_, err = e.saveOffice(s, []byte("my edit"))
		require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
		require.Equal(t, []byte("changed by somebody else"), e.content("old letter.docx"))
	})
}

// The file changes at the provider while its bytes are on their way: the second look
// finds another version, and the read is cut off before its end.
func TestOfficeReadSecondLook(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		data := payload(3 * officeTail)
		id := e.sim.AddFile("", "letter.docx", data)
		s, err := e.openOffice("letter.docx", true)
		require.NoError(t, err)

		redirected := false
		e.http.hook = func(r *http.Request) (*http.Response, error) {
			if !isContent(r) && !redirected {
				return nil, nil
			}
			// The provider hands out the bytes of the old version, all of them, and
			// then the file changes: before the second look is answered.
			res, err := e.http.base.RoundTrip(r)
			if err != nil || res.StatusCode != http.StatusOK {
				// OneDrive sends the reader on to another address for the bytes.
				redirected = err == nil && res.StatusCode >= 300 && res.StatusCode < 400
				return res, err
			}
			redirected = false
			defer res.Body.Close()
			old, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			e.sim.SetContent(id, []byte("changed in that moment"))
			res.Body = io.NopCloser(bytes.NewReader(old))
			return res, nil
		}
		got, begun, err := e.readOffice(s)
		require.ErrorIs(t, err, ErrOfficeBroken)
		require.True(t, begun)
		require.Equal(t, data[:len(data)-officeTail], got, "the old version, without its end")
		e.http.hook = nil
		require.Empty(t, s.version)

		got, _, err = e.readOffice(s)
		require.NoError(t, err)
		require.Equal(t, []byte("changed in that moment"), got)
	})
}

// Without a checksum from the provider nothing would show that the bytes are the ones
// of the version: such a file is not opened.
func TestOfficeNeedsAChecksum(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	id := e.sim.AddFile("", "letter.docx", []byte("a letter"))
	s, err := e.openOffice("letter.docx", true)
	require.NoError(t, err)

	// From now on the provider tells the state of the file without its checksum.
	e.http.hook = func(r *http.Request) (*http.Response, error) {
		if !isItemLook(r, id) {
			return nil, nil
		}
		res, err := e.http.base.RoundTrip(r)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		var item map[string]any
		require.NoError(t, json.NewDecoder(res.Body).Decode(&item))
		delete(item, "md5Checksum")
		body, _ := json.Marshal(item)
		res.Body, res.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
		res.Header.Del("Content-Length")
		return res, nil
	}
	_, err = e.openOffice("letter.docx", true)
	require.Equal(t, serializer.CodeConnectedUnsupported, codeOf(err))
	var out bytes.Buffer
	err = e.f.OfficeRead(e.ctx, e.conn, s, 4<<20, func(*provider.Item) { t.Fatal("nothing begins") }, &out)
	require.Equal(t, serializer.CodeConnectedUnsupported, codeOf(err))
	require.Zero(t, out.Len())
}

// The session keeps the address in the provider's own spelling, so a save finds the file
// also when the client spelled it in another case (where case does not count).
func TestOfficeKeepsTheSpellingOfTheProvider(t *testing.T) {
	e := newUploadEnv(t, ProviderOneDrive)
	folder := e.sim.AddFolder("", "Docs")
	e.sim.AddFile(folder, "Letter.docx", []byte("version one"))
	s, err := e.openOffice("docs/LETTER.DOCX", true)
	require.NoError(t, err)
	require.Equal(t, "Docs/Letter.docx", s.Path)
	require.True(t, s.Edit)
	_, _, err = e.readOffice(s)
	require.NoError(t, err)
	_, err = e.saveOffice(s, []byte("version two"))
	require.NoError(t, err)
	require.Equal(t, []byte("version two"), e.content("Docs/Letter.docx"))
}

// A name that stands for several entries has no session, and says so like everything
// else that cannot be opened.
func TestOfficeDuplicateName(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	e.sim.AddFile("", "twice.docx", []byte("one"))
	e.sim.AddFile("", "twice.docx", []byte("two"))
	_, err := e.openOffice("twice.docx", true)
	require.Equal(t, serializer.CodeConnectedUnsupported, codeOf(err))
}

// A save names the item it is about, and the upload decides under the lock for changes:
// another item at the address is a conflict, the item under another spelling of its name
// was renamed and is gone from the address.
func TestUploadReplaceNamesItsItem(t *testing.T) {
	e := newUploadEnv(t, ProviderOneDrive)
	id := e.sim.AddFile("", "Letter.docx", []byte("version one"))
	other := e.sim.AddFile("", "other.docx", []byte("another"))

	_, err := e.start("Letter.docx", 100, UploadRequest{Replace: true, PreviousRequired: true, Item: other})
	require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
	e.sim.Rename(id, "letter.docx")
	_, err = e.start("Letter.docx", 100, UploadRequest{Replace: true, PreviousRequired: true, Item: id})
	require.Equal(t, serializer.CodeUploadTargetGone, codeOf(err))
	_, err = e.start("gone.docx", 100, UploadRequest{Replace: true, PreviousRequired: true, Item: id})
	require.Equal(t, serializer.CodeUploadTargetGone, codeOf(err))
	info, err := e.start("letter.docx", 100, UploadRequest{Replace: true, PreviousRequired: true, Item: id})
	require.NoError(t, err)
	require.NoError(t, e.f.CancelUpload(e.ctx, e.conn, info.ID))

	// Several entries where the one item was (possible at Drive).
	d := newUploadEnv(t, ProviderDrive)
	first := d.sim.AddFile("", "twice.docx", []byte("one"))
	d.sim.AddFile("", "twice.docx", []byte("two"))
	_, err = d.start("twice.docx", 100, UploadRequest{Replace: true, PreviousRequired: true, Item: first})
	require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
}

// failingFlush takes every byte and then cannot send them on.
type failingFlush struct{ bytes.Buffer }

func (f *failingFlush) Flush() error { return errors.New("connection reset") }

// Bytes that could not be sent on were not read by anybody: the session does not take
// them for what its editor works on.
func TestOfficeReadThatWasNotSent(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	e.sim.AddFile("", "letter.docx", []byte("a small letter"))
	s, err := e.openOffice("letter.docx", true)
	require.NoError(t, err)
	leave, err := e.f.OfficeEnter(e.ctx, s)
	require.NoError(t, err)
	out := &failingFlush{}
	err = e.f.OfficeRead(e.ctx, e.conn, s, 4<<20, func(*provider.Item) {}, out)
	leave()
	require.ErrorIs(t, err, ErrOfficeBroken)
	require.Equal(t, "a small letter", out.String(), "it had been written")
	require.Empty(t, s.version)
	_, err = e.saveOffice(s, []byte("an edit of bytes nobody got"))
	require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
}

// A provider that is a moment late with the checksum of a file does not make the editor
// fail on everything: the file is still told, a save still goes through. Only a read
// waits for the checksum.
func TestOfficeWithoutAChecksumForAMoment(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	id := e.sim.AddFile("", "letter.docx", []byte("version one"))
	s, err := e.openOffice("letter.docx", true)
	require.NoError(t, err)
	_, _, err = e.readOffice(s)
	require.NoError(t, err)

	e.http.hook = func(r *http.Request) (*http.Response, error) {
		if !isItemLook(r, id) {
			return nil, nil
		}
		res, err := e.http.base.RoundTrip(r)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		var item map[string]any
		require.NoError(t, json.NewDecoder(res.Body).Decode(&item))
		delete(item, "md5Checksum")
		body, _ := json.Marshal(item)
		res.Body, res.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
		res.Header.Del("Content-Length")
		return res, nil
	}
	item, edit, err := e.f.OfficeInfo(e.ctx, e.conn, s)
	require.NoError(t, err)
	require.True(t, edit)
	require.Equal(t, "letter.docx", item.Name)
	_, _, err = e.readOffice(s)
	require.Equal(t, serializer.CodeConnectedUnsupported, codeOf(err))
}

// The address of a save that names its item has to lead to it until the commit: a
// folder that was moved away, or the item under another spelling of its name, is a
// conflict then, so the editor reads again before anything is stored.
func TestUploadPinnedToItsAddress(t *testing.T) {
	e := newUploadEnv(t, ProviderOneDrive)
	folder := e.sim.AddFolder("", "Docs")
	id := e.sim.AddFile(folder, "Letter.docx", []byte("version one"))
	data := payload(testChunk + 1000)
	begin := func() *UploadInfo {
		info, err := e.start("Docs/Letter.docx", len(data), UploadRequest{Replace: true, PreviousRequired: true, Item: id})
		require.NoError(t, err)
		_, err = e.chunk(info.ID, 0, data)
		require.NoError(t, err)
		return info
	}

	info := begin()
	e.sim.Rename(id, "letter.docx")
	_, err := e.chunk(info.ID, 1, data)
	require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
	e.sim.Rename(id, "Letter.docx")

	info = begin()
	e.sim.Rename(folder, "Elsewhere")
	_, err = e.chunk(info.ID, 1, data)
	require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
	require.Equal(t, []byte("version one"), e.content("Elsewhere/Letter.docx"))
}

// While the provider is a moment late with the checksum, a save still goes through, and
// stores what the editor sent.
func TestOfficeSaveWithoutAChecksumForAMoment(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	id := e.sim.AddFile("", "letter.docx", []byte("version one"))
	s, err := e.openOffice("letter.docx", true)
	require.NoError(t, err)
	_, _, err = e.readOffice(s)
	require.NoError(t, err)
	e.http.hook = func(r *http.Request) (*http.Response, error) {
		if !isItemLook(r, id) {
			return nil, nil
		}
		res, err := e.http.base.RoundTrip(r)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		var item map[string]any
		require.NoError(t, json.NewDecoder(res.Body).Decode(&item))
		delete(item, "md5Checksum")
		body, _ := json.Marshal(item)
		res.Body, res.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
		res.Header.Del("Content-Length")
		return res, nil
	}
	_, edit, err := e.f.OfficeInfo(e.ctx, e.conn, s)
	require.NoError(t, err)
	require.True(t, edit)
	_, err = e.saveOffice(s, []byte("version two"))
	require.NoError(t, err)
	e.http.hook = nil
	require.Equal(t, []byte("version two"), e.content("letter.docx"))
}

// The way to the item of a save has to be what it was: a second folder of the name
// beside the first (possible at Drive) is a conflict at the start as at the commit, and
// a folder that got another spelling (where case does not count) means the address is
// no longer the item's.
func TestUploadPinnedByTheWayToIt(t *testing.T) {
	d := newUploadEnv(t, ProviderDrive)
	folder := d.sim.AddFolder("", "Docs")
	id := d.sim.AddFile(folder, "Letter.docx", []byte("version one"))
	d.sim.AddFolder("", "Docs")
	_, err := d.start("Docs/Letter.docx", 100, UploadRequest{Replace: true, PreviousRequired: true, Item: id})
	require.Equal(t, serializer.CodeStaleVersion, codeOf(err))

	e := newUploadEnv(t, ProviderOneDrive)
	folder = e.sim.AddFolder("", "Docs")
	id = e.sim.AddFile(folder, "Letter.docx", []byte("version one"))
	data := payload(testChunk + 1000)
	info, err := e.start("Docs/Letter.docx", len(data), UploadRequest{Replace: true, PreviousRequired: true, Item: id})
	require.NoError(t, err)
	_, err = e.chunk(info.ID, 0, data)
	require.NoError(t, err)
	e.sim.Rename(folder, "docs")
	_, err = e.chunk(info.ID, 1, data)
	require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
	_, err = e.start("Docs/Letter.docx", 100, UploadRequest{Replace: true, PreviousRequired: true, Item: id})
	require.Equal(t, serializer.CodeUploadTargetGone, codeOf(err))
	require.Equal(t, []byte("version one"), e.content("docs/Letter.docx"))
}

// The way to the item changes while its save is on its way: a second folder of the name
// appears (Drive), or the item is renamed in the very moment of the commit's last look.
// Both are conflicts, and the old content stays.
func TestUploadPinnedAtTheCommit(t *testing.T) {
	d := newUploadEnv(t, ProviderDrive)
	folder := d.sim.AddFolder("", "Docs")
	id := d.sim.AddFile(folder, "Letter.docx", []byte("version one"))
	data := payload(testChunk + 1000)
	begin := func() *UploadInfo {
		info, err := d.start("Docs/Letter.docx", len(data), UploadRequest{Replace: true, PreviousRequired: true, Item: id})
		require.NoError(t, err)
		_, err = d.chunk(info.ID, 0, data)
		require.NoError(t, err)
		return info
	}

	// Renamed when the commit looks at the item itself, after it found it by its name.
	info := begin()
	d.http.hook = func(r *http.Request) (*http.Response, error) {
		if isItemLook(r, id) {
			d.sim.Rename(id, "Letter (1).docx")
		}
		return nil, nil
	}
	_, err := d.chunk(info.ID, 1, data)
	d.http.hook = nil
	require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
	require.Equal(t, []byte("version one"), d.content("Docs/Letter (1).docx"))
	d.sim.Rename(id, "Letter.docx")

	// A second folder of the name beside the first.
	info = begin()
	d.sim.AddFolder("", "Docs")
	_, err = d.chunk(info.ID, 1, data)
	require.Equal(t, serializer.CodeStaleVersion, codeOf(err))
}

// A file that tells neither version nor checksum for a moment is still told to the
// editor, as one that cannot be saved over right now; it is not an error.
func TestOfficeInfoWithoutAVersionForAMoment(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	id := e.sim.AddFile("", "letter.docx", []byte("version one"))
	s, err := e.openOffice("letter.docx", true)
	require.NoError(t, err)
	e.http.hook = func(r *http.Request) (*http.Response, error) {
		if !isItemLook(r, id) {
			return nil, nil
		}
		res, err := e.http.base.RoundTrip(r)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()
		var item map[string]any
		require.NoError(t, json.NewDecoder(res.Body).Decode(&item))
		for _, field := range []string{"md5Checksum", "headRevisionId", "version"} {
			delete(item, field)
		}
		body, _ := json.Marshal(item)
		res.Body, res.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
		res.Header.Del("Content-Length")
		return res, nil
	}
	item, edit, err := e.f.OfficeInfo(e.ctx, e.conn, s)
	require.NoError(t, err)
	require.Equal(t, "letter.docx", item.Name)
	require.False(t, edit)
	_, _, err = e.readOffice(s)
	require.Equal(t, serializer.CodeConnectedUnsupported, codeOf(err))
}
