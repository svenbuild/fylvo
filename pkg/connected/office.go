package connected

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/graph"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
)

// Files of a connected storage in an Office server.
//
// A session is for one file, named by its item id, for one account and for one connection
// as it was connected then. The address the Office server is given carries the session,
// so every editor window is a document of its own there: the bytes it works on came
// through this session, and the session knows which version of the file they are. A save
// replaces the file only in that version. Anything else is a conflict, which the Office
// server puts to the user; the session then stays in conflict until the document is read
// again.
//
// The calls of one session run one after the other. A call first enters the session
// (OfficeEnter), is then checked by its route (account, password, group, viewer,
// connection), and only then reads or saves: so nothing that was allowed when a call
// arrived is done after it stopped being allowed while the call waited.

const (
	officePrefix = "co-"
	// maxOfficeSessions is how many sessions one account can have open. One more makes
	// the one that was used longest ago give way.
	maxOfficeSessions = 50
	// officeTail is the end of a file that is handed out only after all of it was read
	// and found to be the version it was taken for. A document without its end cannot
	// be opened, so nothing unchecked is ever worked on.
	officeTail = 64 * 1024
	// OfficeReadTime is how long handing out one document may take in all.
	OfficeReadTime = 30 * time.Minute
)

var (
	// ErrOfficeTooLarge: the file is larger than what may be edited online.
	ErrOfficeTooLarge = serializer.NewError(serializer.CodeFileTooLarge, "The file is too large to be opened in an editor", nil)
	// ErrOfficeBroken: the read of a document ended after its first bytes were handed
	// out. What was handed out is not a whole document; the answer has to be cut off.
	ErrOfficeBroken = errors.New("the read of the document broke off")
	// ErrOfficeGone: the session ended (it ran out, its connection ended, or it gave way
	// to a newer one) while a call was waiting or running.
	ErrOfficeGone = errors.New("the office session ended")
)

// OfficeRequest asks for a session.
type OfficeRequest struct {
	// Path of the file in the connection.
	Path string
	// ViewerID is the editor the session is for.
	ViewerID string
	// Edit asks for a session that may save. A file that cannot be changed gets one
	// that may only read.
	Edit bool
	// Fingerprint is inventory.PasswordFingerprint of the account now.
	Fingerprint string
	// TTL is how long the session lasts.
	TTL time.Duration
	// MaxSize is the largest file that may be opened.
	MaxSize int64
}

// OfficeSession is one editor window on one file.
type OfficeSession struct {
	ID string
	// Token is what the Office server shows with every call: the id and a secret.
	Token       string
	User        int
	Fingerprint string
	Conn        string
	Generation  int
	ViewerID    string
	// Edit: the session may save.
	Edit    bool
	Expires time.Time
	// Path is the address of the file when the session was opened, in the provider's
	// own spelling of the names; Name is its name then.
	Path, Name string

	itemID, resourceKey string
	// folderID is the id of the folder the file was in when the session was opened,
	// for a session that edits. A copy beside the file goes into that folder or nowhere.
	folderID string
	// exits are the last saves of closing windows that went into a copy, by the
	// checksum of their content, with the time they were answered with. The same last
	// save sent again gets that answer and is stored nowhere, for as long as the
	// session lives: its answer may not have arrived, and it must never go over the
	// file, also not after the window read the file again.
	exits map[[sha256.Size]byte]time.Time
	// beside is the copy that holds what this session could not save since it last
	// read the document, besidePath its address, besideSum the checksum of what this
	// session put there. A further save that is refused goes into that copy, not into
	// one more.
	beside     *Row
	besidePath string
	besideSum  [sha256.Size]byte
	// copy tells whoever asks from outside the session (OfficeConflict) what beside
	// tells inside it: set while the session cannot save and its last state is in a copy.
	// unkept is set while the session cannot save and what it wanted to save could not
	// be kept either.
	copy   atomic.Pointer[OfficeCopy]
	unkept atomic.Bool
	// read tells that the file was read through this session at least once. Only then
	// is there something of a user in what it saves.
	read bool
	// work is taken by the call that runs (OfficeEnter). It guards version.
	work chan struct{}
	// version (VersionID) is that of the bytes last handed out, or of the last save
	// that is known to be stored. Empty before the first read and after a conflict:
	// then nothing can be saved until the document is read again.
	version string
	// used is when a call last entered (unix nanoseconds).
	used atomic.Int64
	// closed: the session ended.
	closed atomic.Bool
}

// OfficeCopy is the copy that holds what an editor window could not save.
type OfficeCopy struct {
	// Conn and Provider are those of the storage, Row is the copy with its path.
	Conn, Provider string
	Row            *Row
}

// OfficeStanding is how an editor window stands, for whoever asks from outside it.
type OfficeStanding struct {
	// Fingerprint is that of the password the window was opened under.
	Fingerprint string
	// Copy is set while the window cannot save and what it wanted to save is in a copy.
	Copy *OfficeCopy
	// Unkept: the window cannot save, and what it wanted to save could not be kept.
	Unkept bool
}

// forgetBeside lets go of the copy of the session: the next state that is refused gets
// a new one.
func (s *OfficeSession) forgetBeside() {
	s.beside = nil
	s.copy.Store(nil)
}

// offices holds the Office sessions of all connections.
type offices struct {
	now func() time.Time

	mu   sync.Mutex
	byID map[string]*OfficeSession
}

func newOffices() *offices {
	return &offices{now: time.Now, byID: map[string]*OfficeSession{}}
}

// add keeps a new session, unless the connection ended meanwhile. Sessions that ran out
// are dropped on the way. An account that has as many as it may have loses the one that
// was used longest ago and runs no call; only if every one of them is busy is the new
// one refused.
func (o *offices) add(s *OfficeSession, c *Conn) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := o.now()
	var own []*OfficeSession
	for id, other := range o.byID {
		switch {
		case !now.Before(other.Expires):
			other.closed.Store(true)
			delete(o.byID, id)
		case other.User == s.User:
			own = append(own, other)
		}
	}
	if c.ended() {
		return false
	}
	for len(own) >= maxOfficeSessions {
		// The one used longest ago that runs no call. Its turn is taken and kept until
		// it is closed, so no call gets in between the look and the end.
		oldest := -1
		for i, other := range own {
			select {
			case other.work <- struct{}{}:
			default:
				continue
			}
			// With its turn held nobody uses it: its time is what it is.
			if oldest >= 0 && other.used.Load() >= own[oldest].used.Load() {
				<-other.work
				continue
			}
			if oldest >= 0 {
				<-own[oldest].work
			}
			oldest = i
		}
		if oldest < 0 {
			return false
		}
		own[oldest].closed.Store(true)
		delete(o.byID, own[oldest].ID)
		<-own[oldest].work
		own = append(own[:oldest], own[oldest+1:]...)
	}
	s.used.Store(now.UnixNano())
	o.byID[s.ID] = s
	return true
}

// close ends the sessions of a connection that ended.
func (o *offices) close(publicID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for id, s := range o.byID {
		if s.Conn == publicID {
			s.closed.Store(true)
			delete(o.byID, id)
		}
	}
}

// OpenOffice opens a session for the file at a path. Only a file with bytes of its own
// can be opened, and only one whose state and checksum the provider tells: no folder, no
// document that exists only at the provider, no link, no name that stands for several
// entries.
func (m *Manager) OpenOffice(ctx context.Context, c *Conn, r OfficeRequest) (*OfficeSession, error) {
	if r.Path == "" {
		return nil, ErrUnsupported
	}
	row, err := m.tree.ResolveNow(ctx, c, r.Path)
	if errors.Is(err, ErrAmbiguous) {
		return nil, ErrUnsupported
	}
	if err != nil {
		return nil, Error(ctx, err)
	}
	if row.IsDir || row.Native != "" || row.Unsupported != "" || row.Link != nil {
		return nil, ErrUnsupported
	}
	// What the item is right now decides, not what a listing said of it.
	item, err := m.officeLook(ctx, c, row.ID, row.ResourceKey, true)
	if err != nil {
		return nil, err
	}
	if item.Size > r.MaxSize {
		return nil, ErrOfficeTooLarge
	}
	if item.Name != row.Name {
		// Renamed in the moment between the two looks: the caller chose the editor by
		// a name the file no longer has. It asks again.
		return nil, ErrBusy
	}
	// Saving needs what every change needs, the switch of the installation included.
	_, writeErr := writer(c)
	fresh := &Row{Item: *item, Path: row.Path, ViaLink: row.ViaLink}

	s := &OfficeSession{
		ID:   officePrefix + randomID() + randomID(),
		User: c.UserID, Fingerprint: r.Fingerprint, Conn: c.ID, Generation: c.Generation,
		ViewerID: r.ViewerID, Edit: r.Edit && writeErr == nil && c.Replaceable(fresh),
		Expires: m.offices.now().Add(r.TTL),
		Path:    row.Path, Name: row.Name,
		itemID: row.ID, resourceKey: row.ResourceKey,
		work: make(chan struct{}, 1),
	}
	s.Token = s.ID + "." + randomID() + randomID() + randomID() + randomID()
	if s.Edit {
		// The folder as it is now, by its id: a path can come to mean another folder.
		// It is the folder of this session only if the file is in it: between the look
		// at the file and this one another folder can have taken the path.
		folderPath, _ := ParentOf(row.Path)
		folder, err := m.tree.ResolveNow(ctx, c, folderPath)
		if err != nil {
			return nil, Error(ctx, err)
		}
		inside, err := m.tree.named(ctx, c, folder.ContentID(), row.Name)
		switch {
		case errors.Is(err, ErrNotFound), errors.Is(err, ErrAmbiguous), err == nil && inside.ID != row.ID:
			// Moved in that moment. The caller asks again.
			return nil, ErrBusy
		case err != nil:
			return nil, Error(ctx, err)
		}
		s.folderID = folder.ContentID()
	}
	if !m.offices.add(s, c) {
		return nil, ErrBusy
	}
	return s, nil
}

// CloseOffice ends a session.
func (m *Manager) CloseOffice(s *OfficeSession) {
	m.offices.mu.Lock()
	defer m.offices.mu.Unlock()
	s.closed.Store(true)
	delete(m.offices.byID, s.ID)
}

// Office returns the session a token is for. An unknown session, one that ran out and a
// wrong token all look the same.
func (m *Manager) Office(id, token string) (*OfficeSession, error) {
	m.offices.mu.Lock()
	s := m.offices.byID[id]
	now := m.offices.now()
	m.offices.mu.Unlock()
	if s == nil || !now.Before(s.Expires) || subtle.ConstantTimeCompare([]byte(token), []byte(s.Token)) != 1 {
		return nil, ErrNoStorage
	}
	return s, nil
}

// OfficeConflict tells how an editor window of the account stands: whether it can no
// longer save, because its file changed elsewhere, and where what it wanted to save is
// kept. It asks no provider and waits for no call that runs in the session. A session
// that is not there (any more), or not the account's, answers ErrNoStorage.
func (m *Manager) OfficeConflict(userID int, id string) (*OfficeStanding, error) {
	m.offices.mu.Lock()
	s := m.offices.byID[id]
	now := m.offices.now()
	m.offices.mu.Unlock()
	if s == nil || s.User != userID || s.closed.Load() || !now.Before(s.Expires) {
		return nil, ErrNoStorage
	}
	return &OfficeStanding{Fingerprint: s.Fingerprint, Copy: s.copy.Load(), Unkept: s.unkept.Load()}, nil
}

// OfficeNotKept notes that a save of the session was refused because its file changed,
// and that what it wanted to save could not be kept as a copy. The caller entered the
// session.
func (m *Manager) OfficeNotKept(s *OfficeSession) {
	if s.copy.Load() == nil {
		s.unkept.Store(true)
	}
}

// OfficeEnter lets a call into its session: one at a time, the others wait, for as long
// as their request lasts. The caller checks what the call needs after it got in, and
// calls leave when the call is over. A session that ended while the call waited answers
// ErrOfficeGone.
func (m *Manager) OfficeEnter(ctx context.Context, s *OfficeSession) (leave func(), err error) {
	select {
	case s.work <- struct{}{}:
	default:
		// Somebody is in. Wait, but not longer than the request lasts.
		select {
		case s.work <- struct{}{}:
		case <-ctx.Done():
			return nil, ErrBusy
		}
	}
	now := m.offices.now()
	if s.closed.Load() || !now.Before(s.Expires) {
		<-s.work
		return nil, ErrOfficeGone
	}
	s.used.Store(now.UnixNano())
	var once sync.Once
	return func() { once.Do(func() { <-s.work }) }, nil
}

// officeLook is the item with the given id as it is now, if it is a file an Office
// session can be about: one with bytes of its own. For a read (toRead) it also needs a
// version and a checksum, without which nothing would show that the bytes are the ones
// meant. A provider can be a moment late with those for a file it just stored; that
// must not turn every other call about the file into an error.
func (m *Manager) officeLook(ctx context.Context, c *Conn, id, resourceKey string, toRead bool) (*provider.Item, error) {
	c.P.Known(&provider.Item{ID: id, ResourceKey: resourceKey})
	item, err := c.P.Get(ctx, id)
	if err != nil {
		return nil, Error(ctx, err)
	}
	if item.IsDir || item.Native != "" || item.Link != nil {
		return nil, ErrUnsupported
	}
	// Without a version or a checksum a file can still be told (it then cannot be saved
	// over), but not read: nothing would show which bytes those are.
	if toRead && (item.Version == "" || item.Hash == "") {
		return nil, ErrUnsupported
	}
	return item, nil
}

// officeItem is the file of a session as it is now at the provider. The caller entered
// the session.
func (m *Manager) officeItem(ctx context.Context, c *Conn, s *OfficeSession, toRead bool) (*provider.Item, error) {
	if s.closed.Load() || s.Conn != c.ID || s.Generation != c.Generation {
		return nil, ErrOfficeGone
	}
	return m.officeLook(ctx, c, s.itemID, s.resourceKey, toRead)
}

// OfficeInfo returns the file of a session as it is now, and whether the session may
// save to it right now: it was opened for that, the installation still changes
// connected storages, and the file as it is can be replaced. The caller entered the
// session.
func (m *Manager) OfficeInfo(ctx context.Context, c *Conn, s *OfficeSession) (item *provider.Item, edit bool, err error) {
	item, err = m.officeItem(ctx, c, s, false)
	if err != nil {
		return nil, false, err
	}
	_, writeErr := writer(c)
	return item, s.Edit && writeErr == nil && c.Replaceable(&Row{Item: *item}), nil
}

// checksum counts the checksum a provider reports for a file, in the provider's form.
func checksum(kind string) (hash.Hash, func(hash.Hash) string) {
	if kind == ProviderOneDrive {
		return graph.NewQuickXor(), func(h hash.Hash) string { return h.(*graph.QuickXor).Base64() }
	}
	return md5.New(), func(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }
}

// OfficeRead hands the bytes of the session's file to w, and exactly the bytes of one
// version: the file is read whole, its length and checksum are compared with what the
// provider reports for the version, and the provider is asked once more whether that is
// still the version. Only then the end of the file is handed out, and only when that
// went out is the version noted as the one this session works on. begin is called with
// the file before the first byte. After begin, an error means w got a part of a
// document: the caller has to cut its answer off (ErrOfficeBroken). The caller entered
// the session.
func (m *Manager) OfficeRead(ctx context.Context, c *Conn, s *OfficeSession, limit int64, begin func(*provider.Item), w io.Writer) error {
	// The whole read has one limit, from its first look at the file to its last byte:
	// nobody keeps a place for good by taking a document byte by byte.
	ctx, stop := context.WithTimeout(ctx, OfficeReadTime)
	defer stop()
	item, err := m.officeItem(ctx, c, s, true)
	if err != nil {
		return err
	}
	if item.Size > limit {
		return ErrOfficeTooLarge
	}
	release, err := AcquireStream(ctx)
	if err != nil {
		return ErrBusy
	}
	defer release()

	// The read also ends when the provider stands still for too long. The watch runs
	// only while the provider is read, and is wound up again with every read; a client
	// that does not take the bytes is the business of w.
	watch := time.AfterFunc(uploadStall, stop)
	defer watch.Stop()
	content, err := c.P.Open(ctx, item, "")
	watch.Stop()
	if err != nil {
		return Error(ctx, err)
	}
	defer content.Body.Close()

	sum, text := checksum(c.Provider)
	// One byte more than the file has would show that it is not the file it was taken for.
	in := io.TeeReader(io.LimitReader(content.Body, item.Size+1), sum)

	begin(item)
	head := item.Size - min(item.Size, officeTail)
	buf := make([]byte, 32*1024)
	// read reads once from the provider, with the watch running for that read alone.
	read := func(p []byte) (int, error) {
		if ctx.Err() != nil {
			return 0, ctx.Err()
		}
		watch.Reset(uploadStall)
		defer watch.Stop()
		return in.Read(p)
	}
	for left := head; left > 0; {
		n, err := read(buf[:min(int64(len(buf)), left)])
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return ErrOfficeBroken
			}
			left -= int64(n)
		}
		if err != nil && left > 0 {
			return ErrOfficeBroken
		}
	}
	// The end, and one byte more if the provider has one: then it is not this file.
	tail := make([]byte, 0, item.Size-head+1)
	for len(tail) < cap(tail) {
		n, err := read(tail[len(tail):cap(tail)])
		tail = tail[:len(tail)+n]
		if err == io.EOF {
			break
		}
		if err != nil {
			return ErrOfficeBroken
		}
	}
	if int64(len(tail)) != item.Size-head {
		return ErrOfficeBroken
	}
	if text(sum) != item.Hash {
		return ErrOfficeBroken
	}
	after, err := c.P.Get(ctx, s.itemID)
	if err != nil || after.Version != item.Version {
		return ErrOfficeBroken
	}
	if _, err := w.Write(tail); err != nil {
		return ErrOfficeBroken
	}
	// Out of this server before the session takes it for read.
	if flusher, ok := w.(interface{ Flush() error }); ok {
		if err := flusher.Flush(); err != nil {
			return ErrOfficeBroken
		}
	}
	s.version = VersionID(c.ID, &Row{Item: *item})
	s.read = true
	// The editor starts again from what is stored. What it cannot save from here on
	// is another state, and gets a copy of its own.
	s.forgetBeside()
	s.unkept.Store(false)
	return nil
}

// OfficeSave stores new content for the session's file, from one body of the given size.
// The file is replaced only if the address the session was opened at still leads to its
// item, in the version this session last read or saved. Otherwise the answer is
// ErrStale, and stays so until the document is read again. That is also the answer for
// a file that is no longer at its address (renamed, moved, deleted): for the editor all
// of these are "the document changed in storage", which it can tell its user. The other
// errors are those of an upload (ErrOutcomeUnknown included). The caller entered the
// session.
func (m *Manager) OfficeSave(ctx context.Context, c *Conn, s *OfficeSession, body io.Reader, size int64) (*Row, error) {
	if !s.Edit {
		return nil, ErrUnsupported
	}
	if s.closed.Load() || s.Conn != c.ID || s.Generation != c.Generation {
		return nil, ErrOfficeGone
	}
	conflict := func() (*Row, error) {
		// A conflict stays one, also when the file should later look like the version
		// the editor had: only a new read tells what the editor works on.
		s.version = ""
		return nil, ErrStale
	}
	if s.version == "" {
		// Nothing was read through this session since it was opened or since its last
		// conflict: there is no version the content could be a newer state of.
		return conflict()
	}
	// The address must still lead to the item of this session. The upload looks, under
	// the lock for changes: something else under the name (another file, a folder, a
	// link, several entries) is a conflict, nothing there or the item under another
	// spelling of its name is "gone".
	row, err := m.Save(ctx, c, UploadRequest{
		Path: s.Path, Size: size, Replace: true, Previous: s.version, PreviousRequired: true, Item: s.itemID,
	}, body)
	var app serializer.AppError
	if errors.As(err, &app) && (app.Code == serializer.CodeStaleVersion || app.Code == serializer.CodeUploadTargetGone ||
		app.Code == serializer.CodeParentNotExist) {
		// Also when the folder of the file is no longer at its path.
		return conflict()
	}
	if errors.As(err, &app) && app.Code == serializer.CodeUploadSessionExpired {
		// The provider lost the upload in its middle. That is a conflict when the file
		// is the reason (it was deleted or changed after the look at it), and a failed
		// save like any other when the file is as it was.
		item, lookErr := m.officeItem(ctx, c, s, false)
		var gone serializer.AppError
		if (errors.As(lookErr, &gone) && gone.Code == serializer.CodeParentNotExist) ||
			(lookErr == nil && VersionID(c.ID, &Row{Item: *item}) != s.version) {
			return conflict()
		}
	}
	if err != nil {
		// Also after an unknown outcome the version stays what it was: if the save did
		// arrive, the next one finds another version and is a conflict; if it did not,
		// the next one goes through.
		return nil, err
	}
	s.version = VersionID(c.ID, row)
	return row, nil
}

const (
	// officeCopies is how many numbered names are tried for a copy beside a file,
	// before it gets a name with the time in it.
	officeCopies = 20
	// officeKeepTime is how long storing a copy may take.
	officeKeepTime = 2 * time.Minute
)

// OfficeKeepBeside stores content as a file beside the file of the session, in the
// folder the session was opened in: "letter (word).docx", or with a number when that
// name is taken. It is for what an editor wanted to save when its file had changed:
// the file is not replaced, and what the user wrote is not lost, whatever becomes of
// the window. The copy carries the name the file has now. The folder is the one the
// file was in, by its id: if another folder has its path by now, nothing is put there.
// body is read from its start for every try; sum is the SHA-256 of it.
//
// Until the session reads the document again there is one copy: content that is
// refused later takes the place of the earlier one in it, and the very content the
// copy holds is not stored again. That goes for the copy as this session left it only;
// one that somebody changed, renamed, moved or removed is theirs, and a new one is
// made. exit marks the last save of a closing window (see OfficeExitKept). The caller
// entered the session.
func (m *Manager) OfficeKeepBeside(ctx context.Context, c *Conn, s *OfficeSession, body io.ReadSeeker, size int64, sum [sha256.Size]byte, word string, exit bool) (*Row, error) {
	if !s.Edit || s.folderID == "" || !s.read {
		// Also for a session nothing was read through: what it sends is not a newer
		// state of anything its user saw.
		return nil, ErrUnsupported
	}
	if s.closed.Load() || s.Conn != c.ID || s.Generation != c.Generation {
		return nil, ErrOfficeGone
	}
	// The Office server may hang up while the copy is on its way (it does not wait for
	// ever, and a window that closes does not either). What it sent whole is stored
	// all the same: not as long as the request lasts, but for a time of its own, and
	// as long as the connection to the storage lasts.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), officeKeepTime)
	defer cancel()
	if c.live != nil {
		defer context.AfterFunc(c.live.ctx, cancel)()
	}
	stored := func(row *Row, path string) (*Row, error) {
		s.beside, s.besidePath, s.besideSum = row, path, sum
		at := *row
		at.Path = path
		s.copy.Store(&OfficeCopy{Conn: c.ID, Provider: c.Provider, Row: &at})
		s.unkept.Store(false)
		if exit {
			if s.exits == nil {
				s.exits = map[[sha256.Size]byte]time.Time{}
			}
			s.exits[sum] = row.ModTime
		}
		return row, nil
	}
	if s.beside != nil && VersionID(c.ID, s.beside) == "" {
		// Without a marker for its state nothing would show that the copy is still as
		// this session left it: a new one.
		s.forgetBeside()
	}
	if s.beside != nil && sum == s.besideSum {
		// The very content the copy holds, if the copy is still there as it was left:
		// the same item in the same version, under its name in the folder of the file.
		_, name := ParentOf(s.besidePath)
		if row, err := m.tree.named(ctx, c, s.folderID, name); err == nil && row.ID == s.beside.ID &&
			VersionID(c.ID, row) == VersionID(c.ID, s.beside) {
			return stored(s.beside, s.besidePath)
		}
		s.forgetBeside()
	}
	if s.beside != nil {
		// A state of the same document, newer than the one the copy holds: it takes
		// its place there, if the copy is still the item, in the version and in the
		// folder this session left it.
		if _, err := body.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		row, err := m.Save(ctx, c, UploadRequest{
			Path: s.besidePath, Size: size, Replace: true, Previous: VersionID(c.ID, s.beside),
			PreviousRequired: true, Item: s.beside.ID, Folder: s.folderID,
		}, body)
		if err == nil {
			return stored(row, s.besidePath)
		}
		// It did not go there, for whatever reason (the copy is somebody else's by
		// now, it went away in the middle, the provider lost the upload): a new copy.
		// At worst the same state is beside the file twice.
		s.forgetBeside()
	}

	folder, name := ParentOf(s.Path)
	if item, err := m.officeItem(ctx, c, s, false); err == nil {
		// The name the file has now, if the file is still there.
		name = item.Name
	}
	base, ext := name, ""
	if dot := strings.LastIndex(name, "."); dot >= 0 {
		base, ext = name[:dot], name[dot:]
	}
	// The file can carry a name Fylvo gives to nothing new (Drive allows more). The
	// copy gets one that keeps the rules, as close to it as they allow. A file that is
	// such a copy itself gets a further copy of the file it came from, not a copy of
	// a copy.
	base, ext = unmarked(legalNamePart(base), word), legalNamePart(ext)
	var err error
	for n := 1; n <= officeCopies+1; n++ {
		mark := " (" + word + ")"
		switch {
		case n > officeCopies:
			// All the numbered names are taken. One that nothing else carries.
			mark = " (" + word + " " + m.offices.now().UTC().Format("2006-01-02 15.04.05") + ")"
		case n > 1:
			mark = " (" + word + " " + strconv.Itoa(n) + ")"
		}
		path := besideName(base, mark, ext)
		if err = m.settled(c.AccountKey, s.folderID, path); err != nil {
			// Closed for a while after a change there whose outcome stayed open: the
			// next name.
			continue
		}
		if folder != "" {
			path = folder + "/" + path
		}
		if _, err = body.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		var row *Row
		row, err = m.Save(ctx, c, UploadRequest{Path: path, Size: size, Folder: s.folderID}, body)
		var app serializer.AppError
		if err == nil {
			return stored(row, path)
		}
		taken := errors.As(err, &app) && (app.Code == serializer.CodeObjectExist || app.Code == serializer.CodeNameCaseTaken)
		if !taken && m.settled(c.AccountKey, s.folderID, besideName(base, mark, ext)) == nil {
			// Also a provider that is busy: the next name would fare no better.
			return nil, err
		}
		// The name is taken, or was closed in the moment the copy was tried: the next one.
	}
	return nil, err
}

// OfficeExitKept tells whether content with the given checksum was the last save of a
// closing window of this session and went into a copy, and the time that save was
// answered with. The caller entered the session.
func (m *Manager) OfficeExitKept(s *OfficeSession, sum [sha256.Size]byte) (time.Time, bool) {
	when, ok := s.exits[sum]
	return when, ok
}

// unmarked takes the mark of a copy off the first part of a name: "letter (word)" and
// "letter (word 2)" are "letter", and a name that is the mark alone is "".
func unmarked(base, word string) string {
	open := strings.LastIndex(base, "(")
	if open < 0 || !strings.HasSuffix(base, ")") || (open > 0 && base[open-1] != ' ') {
		return base
	}
	inner := base[open+1 : len(base)-1]
	if number, numbered := strings.CutPrefix(inner, word+" "); numbered {
		if n, err := strconv.Atoi(number); err != nil || n < 2 || strconv.Itoa(n) != number {
			return base
		}
	} else if inner != word {
		return base
	}
	return strings.TrimRight(base[:open], " ")
}

// legalNamePart makes a part of a name one that a new entry may carry: the characters
// no new name may have become "_", and nothing is left that is not plain text.
func legalNamePart(part string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(`:*?"<>|/\`, r) || r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, part)
}

// besideName puts a mark between the name of a file and its extension. A name that
// would get too long gives up the end of its first part, never the mark or the
// extension.
func besideName(base, mark, ext string) string {
	if len(mark)+len(ext) >= maxNameLength {
		// What follows the last dot is too long to be kept whole: it is no extension
		// worth keeping, and gives way like the rest of the name.
		base, ext = base+ext, ""
	}
	// A name may not start with a space or end with a space or a period.
	base, ext = strings.TrimLeft(base, " "), strings.TrimRight(ext, " .")
	for len(base)+len(mark)+len(ext) > maxNameLength && base != "" {
		_, last := utf8.DecodeLastRuneInString(base)
		base = base[:len(base)-last]
	}
	base = strings.TrimRight(base, " ")
	if base == "" {
		// A file whose name is its extension alone (".docx"): the mark is the name.
		mark = strings.TrimLeft(mark, " ")
	}
	return base + mark + ext
}

// Save runs a whole upload from one body: the start, every chunk in its order, the
// commit. The body cannot be read a second time, so unless the outcome is open a failed
// upload is ended here, and nothing was stored.
func (m *Manager) Save(ctx context.Context, c *Conn, r UploadRequest, body io.Reader) (*Row, error) {
	info, err := m.StartUpload(ctx, c, r)
	if err != nil {
		return nil, Error(ctx, err)
	}
	for index := 0; ; index++ {
		length := min(info.ChunkSize, r.Size-int64(index)*info.ChunkSize)
		row, err := m.UploadChunk(ctx, c, info.ID, index, io.LimitReader(body, length), length)
		if err != nil {
			if !errors.Is(err, ErrOutcomeUnknown) {
				_ = m.CancelUpload(ctx, c, info.ID)
			}
			return nil, err
		}
		if row != nil {
			return row, nil
		}
	}
}
