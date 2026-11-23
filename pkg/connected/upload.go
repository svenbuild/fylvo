package connected

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
)

// Uploads into a connected storage.
//
// Nothing is spooled: each chunk a client sends is passed straight on into an upload
// session at the provider. The last bytes of the file (at most maxTail) are held back in
// memory, and the provider cannot store the file without them. So until the commit
// nothing at the provider changes, and an upload can always be cancelled safely. The
// commit runs under the lock of the connection, after one more look at the provider,
// and is one short request. Sessions live in memory only: after a restart they are gone,
// and the provider's unfinished session expires by itself.

const (
	// UploadChunkSize is the size of the chunks a client sends. It is a whole multiple
	// of the granularity of every provider, so a chunk is passed on as it is.
	UploadChunkSize = 10 << 20
	// maxTail is the most an upload holds in memory: the tail kept for the commit, or
	// what is left over after a range when the provider counts in other steps.
	maxTail = 640 << 10
	// uploadIdle is how long a session may go without a request before it is cancelled.
	uploadIdle = 24 * time.Hour
	// uploadKept is how long the state route tells the outcome of an ended session.
	uploadKept = time.Hour
	// parallelUploads is how many chunk requests pass bytes on to providers at once.
	parallelUploads = 2
	// maxSessions is how many uploads one account can have open at once.
	maxSessions = 50
	// uploadPrefix starts the id of every upload session of a connected storage.
	uploadPrefix = "cu-"
	// callTimeout bounds a call to the provider that has to finish although the client
	// went away.
	callTimeout = time.Minute
)

var (
	// uploadWait is how long a chunk request waits for one of the parallelUploads places.
	uploadWait = 10 * time.Second
	// uploadStall is how long a range may go without a byte moving before it ends,
	// uploadRange how long it may take in all.
	uploadStall = 2 * time.Minute
	uploadRange = 30 * time.Minute
	// chunkSize is UploadChunkSize; tests make it smaller.
	chunkSize int64 = UploadChunkSize
)

// States of an upload, as the state route reports them.
const (
	UploadReceiving = "receiving"
	UploadDone      = "done"
	UploadFailed    = "failed"
	UploadUnknown   = "unknown"
)

var (
	// ErrStale: the file is not in the state the client names or the upload started with.
	ErrStale = serializer.NewError(serializer.CodeStaleVersion, "The file is not the version the client names", nil)
	// ErrTargetGone: the client asked to replace a file, and there is none.
	ErrTargetGone = serializer.NewError(serializer.CodeUploadTargetGone, "There is no file to replace", nil)
	// ErrOutcomeUnknown: the last range was sent, but the provider did not confirm it.
	ErrOutcomeUnknown = serializer.NewError(serializer.CodeConnectedOutcomeUnknown, "The outcome of the upload is not known", nil)
	// ErrUploadGone: the session ended before its last range: at the provider, by a
	// disconnect, or for lack of activity. Nothing was stored.
	ErrUploadGone = serializer.NewError(serializer.CodeUploadSessionExpired, "The upload session ended", nil)
	// ErrUploadCancelled: the client cancelled the upload. Nothing was stored.
	ErrUploadCancelled = serializer.NewError(serializer.CodeUploadSessionExpired, "The upload was cancelled", nil)
	// ErrSendAgain: the chunk did not get through completely; the same chunk is to be
	// sent again, and only what the provider lacks is passed on.
	ErrSendAgain = serializer.NewError(serializer.CodeConnectedBusy, "The chunk did not get through, send it again", nil)
	// ErrChunkOrder: chunks are taken in order only.
	ErrChunkOrder = serializer.NewError(serializer.CodeParamErr, "Chunks must be sent in order", nil)
	// ErrChunkLength: the index or the length of a chunk does not fit the upload.
	ErrChunkLength = serializer.NewError(serializer.CodeInvalidContentLength, "Invalid chunk index or length", nil)
)

// UploadRequest is what a client asks to upload.
type UploadRequest struct {
	Path string
	Size int64
	// Replace lets the upload give new content to the file at Path, if there is one.
	// Without it a taken name is an error.
	Replace bool
	// Previous is the version (VersionID) of the file the client last saw. Given, the
	// file is replaced only in exactly that version.
	Previous string
	// PreviousRequired: an error instead of a new file when there is none to replace.
	PreviousRequired bool
}

// UploadInfo is a started upload.
type UploadInfo struct {
	ID        string
	ChunkSize int64
	// Expires is when the session ends unless a chunk arrives before.
	Expires time.Time
}

// UploadStatus is where an upload stands.
type UploadStatus struct {
	State string
	// Received is the number of bytes taken from the client.
	Received int64
	// Conn is the connection the upload goes into, Generation the one it was started in,
	// Provider its kind.
	Conn       string
	Generation int
	Provider   string
	// Row is the stored file, once the upload is done.
	Row *Row
	// Err says why an upload failed or what is unknown about it.
	Err error
}

// IsUploadID tells whether a session id is one of an upload into a connected storage.
func IsUploadID(id string) bool {
	return strings.HasPrefix(id, uploadPrefix)
}

// upload is one upload session.
type upload struct {
	id         string
	user       int
	conn       string
	generation int
	live       *liveConn
	// account is the account at the provider (Conn.AccountKey), provider its kind.
	account, provider string

	path, name string
	folderID   string
	size       int64
	// replace and version are the item replaced and its version when the upload
	// started; replace is empty for a new file.
	replace, version string
	// up is the session at the provider, nil for an empty file.
	up provider.Upload

	// work lets one request at a time pass bytes on, commit or cancel. It guards the
	// fields below it.
	work sync.Mutex
	// next is the number of bytes the provider has. sentTo is the end of the last range
	// handed to it, whether it confirmed that range or not: it cannot have more.
	next   int64
	sentTo int64
	// held are the bytes after next that were taken from the client but not passed on.
	held []byte
	// sum is the checksum of the first hashedTo bytes of the file, nil where the
	// provider's receipt carries no checksum it can be compared with. hashedTo is the
	// end of the last chunk taken: a chunk is counted once, however often it is sent.
	sum      hash.Hash
	hashedTo int64
	// recheck: a range went wrong and the provider could not be asked what it has.
	recheck bool
	// closed: the connection ended; the upload ends once no request runs for it.
	closed atomic.Bool

	// mu guards what the state route reads, so it never waits for a transfer.
	mu       sync.Mutex
	state    string
	received int64
	row      *Row
	err      error
	touched  time.Time
	ended    time.Time
}

// uploads holds the upload sessions of all connections.
type uploads struct {
	now func() time.Time
	sem chan struct{}

	mu   sync.Mutex
	byID map[string]*upload
	// tidy is the timer of the next clean-up, nil while there is no session.
	tidy *time.Timer
}

// tidyEvery is how often the sessions are looked through for ones to end.
const tidyEvery = 10 * time.Minute

// tidyUp ends what ran out, also on a server nobody asks anything, and goes on as long
// as there are sessions.
func (s *uploads) tidyUp() {
	go cancelAll(s.sweep())
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.byID) == 0 {
		s.tidy = nil
		return
	}
	s.tidy = time.AfterFunc(tidyEvery, s.tidyUp)
}

func newUploads() *uploads {
	return &uploads{now: time.Now, sem: make(chan struct{}, parallelUploads), byID: map[string]*upload{}}
}

// find returns the session with the given id if it belongs to the account. One of
// another account is not found, like one that does not exist.
func (s *uploads) find(userID int, id string) (*upload, error) {
	// The provider is told in the background: no request waits for another one's leftovers.
	go cancelAll(s.sweep())

	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.byID[id]
	if u == nil || u.user != userID {
		return nil, ErrNoStorage
	}
	return u, nil
}

// add keeps a new session. It refuses one of a connection that ended meanwhile: what
// ended it has called close already, or calls it after this.
func (s *uploads) add(u *upload, c *Conn) bool {
	go cancelAll(s.sweep())

	s.mu.Lock()
	defer s.mu.Unlock()
	if c.ended() {
		return false
	}
	open := 0
	for _, other := range s.byID {
		if other.user == u.user {
			if state, _, _ := other.outcome(); state == UploadReceiving {
				open++
			}
		}
	}
	if open >= maxSessions {
		return false
	}
	s.byID[u.id] = u
	if s.tidy == nil {
		s.tidy = time.AfterFunc(tidyEvery, s.tidyUp)
	}
	return true
}

// sweep drops the sessions whose outcome is old, and ends the ones nobody used for too
// long, which it returns: they still have to be cancelled at the provider, which the
// caller does without holding s.mu.
func (s *uploads) sweep() (expired []*upload) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for id, u := range s.byID {
		u.mu.Lock()
		state, ended, touched := u.state, u.ended, u.touched
		u.mu.Unlock()
		switch {
		case state != UploadReceiving && now.Sub(ended) > uploadKept:
			delete(s.byID, id)
		case state == UploadReceiving && now.Sub(touched) > uploadIdle && u.work.TryLock():
			// A session a request works on is not idle, whatever its time says. With
			// its lock held it is looked at again: a request can have ended or used
			// it since. It ends here and stays, like every ended one, for the state
			// route to tell.
			u.mu.Lock()
			idle := u.state == UploadReceiving && now.Sub(u.touched) > uploadIdle
			if idle {
				u.state, u.err, u.ended = UploadFailed, ErrUploadGone, now
			}
			u.mu.Unlock()
			if !idle {
				u.work.Unlock()
				continue
			}
			expired = append(expired, u)
		}
	}
	return expired
}

// cancelAll ends sessions that were taken out of use, whose work locks the caller holds.
func cancelAll(expired []*upload) {
	// They have their outcome. The locks go back first: a request for one of them must
	// not wait while a provider that does not answer is told about the others.
	for _, u := range expired {
		u.held = nil
		u.work.Unlock()
	}
	for _, u := range expired {
		// Not where the installation only reads: the session at the provider runs out
		// by itself, and without the tail held here it stores nothing.
		if u.up != nil && Writes() {
			ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
			_ = u.up.Cancel(ctx)
			cancel()
		}
	}
}

// close ends the sessions of a connection that ended. A session a request works on
// ends when that request is over. Nothing is sent to the provider: the token is gone,
// and without the held tail the provider stores nothing; its session expires by itself.
func (s *uploads) close(publicID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for _, u := range s.byID {
		if u.conn != publicID {
			continue
		}
		u.closed.Store(true)
		if u.work.TryLock() {
			u.end(UploadFailed, nil, ErrUploadGone, now)
			u.held = nil
			u.work.Unlock()
		}
	}
}

// acquire takes one of the places for chunk requests, waiting a short while for one.
func (s *uploads) acquire(ctx context.Context) (release func(), err error) {
	timer := time.NewTimer(uploadWait)
	defer timer.Stop()
	select {
	case s.sem <- struct{}{}:
		return func() { <-s.sem }, nil
	case <-timer.C:
		return nil, ErrBusy
	case <-ctx.Done():
		return nil, ErrBusy
	}
}

func (s *uploads) touch(u *upload) {
	now := s.now()
	u.mu.Lock()
	u.touched = now
	u.mu.Unlock()
}

// end gives a session that is still receiving its outcome. The first outcome stays.
func (u *upload) end(state string, row *Row, err error, now time.Time) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.state != UploadReceiving {
		return
	}
	u.state, u.row, u.err, u.ended = state, row, err, now
	if state == UploadDone {
		u.received = u.size
	}
}

// release lets go of the bytes an ended upload still holds. The caller holds u.work.
func (u *upload) release() {
	if state, _, _ := u.outcome(); state != UploadReceiving {
		u.held = nil
	}
}

func (u *upload) outcome() (state string, row *Row, err error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.state, u.row, u.err
}

// noteReceived updates what the state route reports. The caller holds u.work.
func (u *upload) noteReceived() {
	u.mu.Lock()
	u.received = u.next + int64(len(u.held))
	u.mu.Unlock()
}

// detached is for calls whose answer matters even when the client went away: they end
// with the connection, or after callTimeout.
func detached(ctx context.Context, live *liveConn) (context.Context, context.CancelFunc) {
	if live == nil {
		return context.WithTimeout(context.WithoutCancel(ctx), callTimeout)
	}
	// A child of the connection's own context: it ends at the very moment the
	// connection does, not a moment later.
	return context.WithTimeout(live.ctx, callTimeout)
}

// refused tells whether an error of a provider means it refused a write and stored
// nothing.
func refused(err error) bool {
	switch provider.KindOf(err) {
	case provider.KindExists, provider.KindStale, provider.KindFull, provider.KindForbidden,
		provider.KindDailyLimit, provider.KindNotFound:
		return true
	}
	return false
}

// uploadError is the error the API reports for an error of a provider during an upload.
func uploadError(ctx context.Context, err error) error {
	switch provider.KindOf(err) {
	case provider.KindExists:
		return ErrNameTaken
	case provider.KindStale:
		return ErrStale
	case provider.KindNotFound:
		return ErrUploadGone
	}
	return Error(ctx, err)
}

// StartUpload opens an upload of a file at the given path. A new file needs a free name
// that follows the name rules; replacing a file needs r.Replace, and with r.Previous
// the file in exactly that version. The session at the provider is opened right away,
// on the state the checks saw.
func (m *Manager) StartUpload(ctx context.Context, c *Conn, r UploadRequest) (*UploadInfo, error) {
	w, err := writer(c)
	if err != nil {
		return nil, err
	}
	if r.Path == "" {
		return nil, ErrNameTaken
	}
	parentPath, name := ParentOf(r.Path)
	defer m.writing(c)()

	parent, err := m.folder(ctx, c, parentPath)
	if err != nil {
		return nil, err
	}
	u := &upload{
		user: c.UserID, conn: c.ID, generation: c.Generation, live: c.live, account: c.AccountKey, provider: c.Provider,
		path: r.Path, name: name, folderID: parent.ContentID(), size: r.Size, state: UploadReceiving,
	}
	if c.Provider == ProviderDrive {
		// Drive's receipt carries the MD5 of what it stored.
		u.sum = md5.New()
	}

	existing, err := m.tree.named(ctx, c, u.folderID, name)
	switch {
	case errors.Is(err, ErrNotFound):
		if r.PreviousRequired {
			return nil, ErrTargetGone
		}
		if err := checkNewName(name); err != nil {
			return nil, err
		}
		// Not while a change about this name may still be on its way at the provider.
		if err := m.settled(c.AccountKey, u.folderID, name); err != nil {
			return nil, err
		}
		err = nil
		if r.Size > 0 {
			u.up, err = w.NewUpload(ctx, u.folderID, name, r.Size)
		}
	case errors.Is(err, ErrAmbiguous) && r.Replace:
		return nil, ItemError(ReasonAmbiguous)
	case errors.Is(err, ErrAmbiguous):
		return nil, ErrNameTaken
	case err != nil:
		return nil, err
	case existing.Name != name:
		return nil, ErrCaseTaken
	case !r.Replace || existing.IsDir:
		return nil, ErrNameTaken
	case existing.Link != nil || existing.Native != "":
		// A link or a document without bytes of its own has no content to replace.
		return nil, ErrUnsupported
	default:
		// The state of the file right now, which the commit compares with again.
		item, getErr := c.P.Get(ctx, existing.ID)
		if getErr != nil {
			return nil, Error(ctx, getErr)
		}
		if r.Previous != "" && VersionID(c.ID, rowOf(item, r.Path)) != r.Previous {
			return nil, ErrStale
		}
		if r.Size == 0 || item.Version == "" {
			// The providers' interface has no way to empty a file. And a file whose
			// state the provider does not tell cannot be replaced safely: nothing
			// would show that it changed meanwhile.
			return nil, ErrUnsupported
		}
		u.replace, u.version = item.ID, item.Version
		u.up, err = w.ReplaceUpload(ctx, item, r.Size)
	}
	if provider.KindOf(err) == provider.KindStale {
		return nil, ErrStale
	}
	if err != nil {
		return nil, Error(ctx, err)
	}
	if u.up == nil && r.Size > 0 {
		// A provider that opened nothing and said nothing: no session to send into.
		return nil, Error(ctx, &provider.Error{Kind: provider.KindOther})
	}
	if u.up != nil {
		if g := u.up.Granularity(); g <= 0 || g > maxTail || chunkSize%g != 0 {
			_ = u.up.Cancel(ctx)
			return nil, ErrUnsupported
		}
	}

	u.id = uploadPrefix + randomID() + randomID()
	u.touched = m.uploads.now()
	if !m.uploads.add(u, c) {
		// The connection ended meanwhile, or the account has too many uploads open.
		if u.up != nil {
			_ = u.up.Cancel(ctx)
		}
		return nil, ErrBusy
	}
	return &UploadInfo{ID: u.id, ChunkSize: chunkSize, Expires: u.touched.Add(uploadIdle)}, nil
}

// UploadConnection returns the connection an upload of the account goes into.
func (m *Manager) UploadConnection(userID int, id string) (string, error) {
	u, err := m.uploads.find(userID, id)
	if err != nil {
		return "", err
	}
	return u.conn, nil
}

// session returns an upload of the connection a request was admitted to.
func (m *Manager) session(c *Conn, id string) (*upload, error) {
	u, err := m.uploads.find(c.UserID, id)
	if err != nil {
		return nil, err
	}
	if u.conn != c.ID || u.generation != c.Generation {
		return nil, ErrNoStorage
	}
	return u, nil
}

// UploadChunk takes chunk index of an upload: length bytes from body. Chunks come in
// order; a chunk taken before is acknowledged without being sent again. The chunk that
// completes the upload returns the stored file, and so does every repetition of it.
func (m *Manager) UploadChunk(ctx context.Context, c *Conn, id string, index int, body io.Reader, length int64) (*Row, error) {
	u, err := m.session(c, id)
	if err != nil {
		return nil, err
	}
	if _, err := writer(c); err != nil {
		// Also for an upload that was opened while changes were still switched on.
		return nil, err
	}
	chunks := max(1, (u.size+chunkSize-1)/chunkSize)
	start := int64(index) * chunkSize
	if index < 0 || int64(index) >= chunks || length != min(chunkSize, u.size-start) {
		return nil, ErrChunkLength
	}
	end := start + length

	// A second try of a chunk waits here for the first, and then finds it taken.
	u.work.Lock()
	defer func() {
		if u.closed.Load() {
			u.end(UploadFailed, nil, ErrUploadGone, m.uploads.now())
		}
		u.release()
		m.uploads.touch(u)
		u.work.Unlock()
		// The connection can have ended in the moment between the look above and
		// the unlock, when nobody could end the session: it is ended here.
		if u.closed.Load() && u.work.TryLock() {
			u.end(UploadFailed, nil, ErrUploadGone, m.uploads.now())
			u.held = nil
			u.work.Unlock()
		}
	}()
	m.uploads.touch(u)

	if state, row, err := u.outcome(); state != UploadReceiving {
		if state == UploadDone && end < u.size {
			return nil, nil
		}
		return row, err
	}
	if u.closed.Load() {
		return nil, ErrUploadGone
	}
	// All bytes are here when a commit is to be tried again.
	if !(end == u.size && u.hashedTo == u.size) {
		if end <= u.hashedTo {
			return nil, nil
		}
		if start != u.hashedTo {
			return nil, ErrChunkOrder
		}
	}

	if u.hashedTo < end {
		// One of the few places is held while bytes are passed on, and given back
		// before the commit: that one waits for the lock of the account, which can
		// take a while, and holds at most the tail.
		release, err := m.uploads.acquire(ctx)
		if err != nil {
			return nil, err
		}
		err = m.receive(ctx, u, body, start, length)
		release()
		if err != nil {
			return nil, err
		}
	}
	if end < u.size {
		return nil, nil
	}
	return m.commit(ctx, c, u)
}

// receive takes the chunk at start. It reads past what the provider has of it already,
// passes on what it lacks in ranges the provider takes, and holds the rest, which for
// the last chunk is the tail for the commit. The caller holds u.work.
func (m *Manager) receive(ctx context.Context, u *upload, body io.Reader, start, length int64) error {
	if u.recheck {
		if err := m.reconcile(ctx, u); err != nil {
			return err
		}
	}
	end := start + length

	// The checksum is counted from a copy, which is kept only when the chunk is taken.
	var sum hash.Hash
	in := body
	if u.sum != nil {
		cloner, ok := u.sum.(hash.Cloner)
		if !ok {
			return Error(ctx, errors.New("checksum cannot be copied"))
		}
		cloned, err := cloner.Clone()
		if err != nil {
			return Error(ctx, err)
		}
		sum = cloned
		in = io.TeeReader(body, sum)
	}

	accepted := u.next + int64(len(u.held))
	if skip := accepted - start; skip > 0 {
		// The provider has this part since a try that broke off.
		if _, err := io.CopyN(io.Discard, in, skip); err != nil {
			return ErrSendAgain
		}
	}
	available := int64(len(u.held)) + end - max(start, accepted)

	// Every range but the last is a multiple of the granularity; the last one, at
	// least one byte, is left for the commit.
	g := u.up.Granularity()
	send := available / g * g
	if end == u.size {
		send = (available - 1) / g * g
	}
	held := u.held
	if send > 0 {
		// What is held is less than one step of the granularity, so it all goes first.
		// The range ends when no byte moved for too long: a provider that stops
		// reading, or a client that stops sending, must not hold the session and a place.
		sendCtx, stop := context.WithCancel(ctx)
		watch := time.AfterFunc(uploadStall, stop)
		r := &cutReader{
			r:     io.MultiReader(bytes.NewReader(u.held), io.LimitReader(in, send-int64(len(u.held)))),
			moved: func() { watch.Reset(uploadStall) },
		}
		// However it moves, a range is over after uploadRange: a client that sends a
		// byte now and then must not keep a place for good.
		limit := time.AfterFunc(uploadRange, stop)
		_, err := u.up.Send(sendCtx, u.next, r, send)
		r.cut()
		// The provider cannot have more than what was handed to the network, and of a
		// range it refused outright it has nothing.
		if !refusedOutright(err) {
			u.sentTo = max(u.sentTo, u.next+r.handed())
		}
		watch.Stop()
		limit.Stop()
		stop()
		if err == nil && r.handed() < send {
			// The provider says it has bytes that never left this server. What it has
			// is open: it is asked.
			err = &provider.Error{Kind: provider.KindOther}
		}
		if err != nil {
			return m.sendFailed(ctx, u, err)
		}
		u.next += send
		u.held, held = nil, nil
		u.noteReceived()
	}

	rest := available - send - int64(len(held))
	next := make([]byte, len(held), len(held)+int(rest))
	copy(next, held)
	next = next[:len(held)+int(rest)]
	if _, err := io.ReadFull(in, next[len(held):]); err != nil {
		// The client's body ended early. What the provider has is known; the chunk is
		// sent again.
		return ErrSendAgain
	}
	u.held, u.sum, u.hashedTo = next, sum, end
	u.noteReceived()
	return nil
}

// cutReader lends a body to a request and takes it back. An HTTP transport may still
// read a body for a moment after the request returned; after cut it reads nothing more.
type cutReader struct {
	mu   sync.Mutex
	r    io.Reader
	done bool
	// read counts the bytes handed over. moved, if set, is told about each read.
	read  int64
	moved func()
}

func (c *cutReader) Read(p []byte) (int, error) {
	n, err := c.readLocked(p)
	if c.moved != nil && n > 0 {
		c.moved()
	}
	return n, err
}

// handed returns how many bytes were read from it.
func (c *cutReader) handed() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.read
}

func (c *cutReader) readLocked(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return 0, io.ErrUnexpectedEOF
	}
	// Counted under the lock of the read: after cut, the count is what was handed over.
	n, err := c.r.Read(p)
	c.read += int64(n)
	return n, err
}

func (c *cutReader) cut() {
	c.mu.Lock()
	c.done = true
	c.mu.Unlock()
}

// refusedOutright tells whether the provider said a plain "no" to a range, or was never
// asked: whatever was read on the way, it took nothing of it.
func refusedOutright(err error) bool {
	return err != nil && (provider.KindOf(err) == provider.KindRetry || !provider.Sent(err) || refused(err))
}

// sendFailed handles a range the provider did not take. The caller holds u.work.
func (m *Manager) sendFailed(ctx context.Context, u *upload, err error) error {
	switch {
	case refused(err):
		return m.fail(ctx, u, uploadError(ctx, err))
	case provider.KindOf(err) == provider.KindReconnect || provider.KindOf(err) == provider.KindSetup:
		// Refused before any byte was taken.
		return Error(ctx, err)
	case provider.KindOf(err) == provider.KindRetry:
		// Refused before any byte was taken, and the reason is gone.
		return ErrSendAgain
	case !provider.Sent(err):
		// Not carried out: no token could be had, or the provider refused the one it
		// was shown. Nothing was taken.
		return Error(ctx, err)
	case provider.KindOf(err) == provider.KindOpen:
		m.endUnknown(u)
		u.held = nil
		return ErrOutcomeUnknown
	}
	// Open what arrived: the provider is asked, and the client sends the chunk again.
	u.recheck = true
	if err := m.reconcile(ctx, u); err != nil {
		return err
	}
	return ErrSendAgain
}

// reconcile asks the provider how much of the upload it has and continues from there.
// The caller holds u.work.
func (m *Manager) reconcile(ctx context.Context, u *upload) error {
	callCtx, cancel := detached(ctx, u.live)
	defer cancel()
	n, done, err := u.up.State(callCtx)
	switch {
	case provider.KindOf(err) == provider.KindNotFound:
		// The provider dropped the session. It stored nothing: the tail is here.
		return m.fail(ctx, u, ErrUploadGone)
	case provider.KindOf(err) == provider.KindOpen:
		// The provider says "stored" and does not show what: nothing about it is certain.
		m.endUnknown(u)
		return ErrOutcomeUnknown
	case err != nil:
		// Still open; asked again with the next chunk.
		return Error(ctx, err)
	case done != nil || n >= u.size:
		// Complete without the bytes held here: nothing about it is certain.
		m.endUnknown(u)
		return ErrOutcomeUnknown
	case n < u.next || n > u.sentTo:
		// Less than it confirmed before, or more than it was sent.
		return m.fail(ctx, u, Error(ctx, &provider.Error{Kind: provider.KindOther}))
	}
	u.moveTo(n)
	u.recheck = false
	return nil
}

// moveTo continues the upload from n, the bytes the provider has, which is at least
// what it confirmed before. Held bytes it now has are dropped. The caller holds u.work.
func (u *upload) moveTo(n int64) {
	if n < u.next+int64(len(u.held)) {
		u.held = u.held[n-u.next:]
	} else {
		u.held = nil
	}
	u.next = n
	u.noteReceived()
}

// endUnknown ends an upload whose outcome is open. A new file may be in the storage or
// on its way there, so its name stays closed for a while, like after any change with an
// open outcome, wherever in the upload it became open. The caller holds u.work.
func (m *Manager) endUnknown(u *upload) {
	u.end(UploadUnknown, nil, ErrOutcomeUnknown, m.uploads.now())
	if u.replace == "" {
		m.unsettle(u.account, u.folderID, u.name)
	}
	// The folder may have a new file or new content now: what is in memory of it goes.
	m.tree.Changed(&Conn{ID: u.conn, Generation: u.generation, AccountKey: u.account}, u.folderID)
}

// fail ends an upload before its commit and cancels it at the provider. Nothing was
// stored. The caller holds u.work.
func (m *Manager) fail(ctx context.Context, u *upload, err error) error {
	u.end(UploadFailed, nil, err, m.uploads.now())
	u.held = nil
	if u.up != nil && Writes() {
		callCtx, cancel := detached(ctx, u.live)
		defer cancel()
		_ = u.up.Cancel(callCtx)
	}
	return err
}

// commit stores the file: under the lock of the connection it looks at the provider
// once more and sends the tail. The caller holds u.work.
func (m *Manager) commit(ctx context.Context, c *Conn, u *upload) (*Row, error) {
	w, err := writer(c)
	if err != nil {
		return nil, err
	}
	defer m.writing(c)()
	defer m.tree.Changed(c, u.folderID)
	// The wait for the lock can have been long. A storage that was disconnected or
	// connected again meanwhile gets nothing: the tail never leaves this server.
	if c.ended() || u.closed.Load() {
		return nil, m.fail(ctx, u, ErrUploadGone)
	}
	// The commit is a request to the provider like any other: it takes one of the
	// places, which was given back while this waited for the lock. It takes it before
	// the last look at the provider, so nothing comes between that look and the write.
	release, err := m.uploads.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := m.unchanged(ctx, c, u); err != nil {
		return nil, err
	}

	callCtx, cancel := detached(ctx, u.live)
	defer cancel()
	if callCtx.Err() != nil {
		return nil, m.fail(ctx, u, ErrUploadGone)
	}
	if u.up == nil {
		item, err := w.CreateEmpty(callCtx, u.folderID, u.name)
		switch {
		case err == nil:
			return m.finish(u, item, false)
		case provider.KindOf(err) == provider.KindNotFound:
			// The folder is gone. Nothing was created.
			return nil, m.fail(ctx, u, ErrNoStorage)
		case refused(err):
			return nil, m.fail(ctx, u, uploadError(ctx, err))
		case provider.KindOf(err) == provider.KindReconnect || provider.KindOf(err) == provider.KindSetup:
			// Refused before anything was asked of the storage: it can be tried again.
			return nil, Error(ctx, err)
		case provider.KindOf(err) == provider.KindRetry:
			// Refused before anything was done, for a reason that is gone.
			return nil, ErrSendAgain
		case !unclear(err):
			// A clear "no" of the provider: nothing was created, and the upload stays
			// open for another try.
			return nil, Error(ctx, err)
		}
		m.endUnknown(u)
		return nil, ErrOutcomeUnknown
	}

	tail := &cutReader{r: bytes.NewReader(u.held)}
	item, err := u.up.Send(callCtx, u.next, tail, int64(len(u.held)))
	tail.cut()
	left := tail.handed()
	if !refusedOutright(err) {
		u.sentTo = max(u.sentTo, u.next+left)
	}
	switch {
	case err == nil && item != nil && left < int64(len(u.held)):
		// A receipt for a file whose end never left this server. Whatever was stored,
		// it is not known to be this upload.
		m.endUnknown(u)
		u.held = nil
		return nil, ErrOutcomeUnknown
	case err == nil && item != nil:
		return m.finish(u, item, true)
	case refused(err):
		return nil, m.fail(ctx, u, uploadError(ctx, err))
	case provider.KindOf(err) == provider.KindReconnect || provider.KindOf(err) == provider.KindSetup:
		// Refused before the tail was taken: the commit can be tried again.
		return nil, Error(ctx, err)
	case provider.KindOf(err) == provider.KindRetry:
		// Refused before the tail was taken, for a reason that is gone.
		return nil, ErrSendAgain
	case err != nil && !provider.Sent(err):
		// Not carried out, however much of the tail was read on the way: the provider
		// refused the token, or none could be had. The commit can be tried again.
		return nil, Error(ctx, err)
	case provider.KindOf(err) == provider.KindOpen:
		m.endUnknown(u)
		u.held = nil
		return nil, ErrOutcomeUnknown
	case err != nil && left == 0:
		// Not one byte of the tail left this server: nothing can have been stored.
		// The last chunk can simply be sent again.
		return nil, ErrSendAgain
	}

	// No receipt. The session at the provider may still tell; it is asked with time of
	// its own, since the send can have used up all of its.
	probeCtx, cancelProbe := detached(ctx, u.live)
	defer cancelProbe()
	n, done, err := u.up.State(probeCtx)
	switch {
	case err == nil && done != nil && left == int64(len(u.held)):
		return m.finish(u, done, true)
	case err == nil && done == nil && n >= u.next && n <= u.sentTo && n < u.size && left < int64(len(u.held)):
		// Not all of the tail left this server, so nothing was stored. What is missing
		// is sent with the next try of the last chunk.
		u.moveTo(n)
		return nil, ErrSendAgain
	case provider.KindOf(err) == provider.KindNotFound && left < int64(len(u.held)):
		// Not all of the tail left, and the provider no longer knows the session:
		// nothing was stored, and nothing will be.
		return nil, m.fail(ctx, u, ErrUploadGone)
	}
	// All of the tail was handed over and no receipt came, or the provider tells a
	// receipt for a file whose end never left this server: the provider may be storing
	// a file this very moment, whatever its session says. Nothing is sent again.
	m.endUnknown(u)
	return nil, ErrOutcomeUnknown
}

// unchanged looks at the provider right before the commit: a replaced file must still
// be the item and the version the upload started with, and the name of a new file must
// still be free. A change ends the upload; an error of the provider leaves it open for
// another try. The caller holds the lock of the connection.
func (m *Manager) unchanged(ctx context.Context, c *Conn, u *upload) error {
	// The folder at the path must still be the one the upload started in: renamed or
	// replaced meanwhile, the file would end up somewhere else than its address says.
	parentPath, _ := ParentOf(u.path)
	parent, err := m.folder(ctx, c, parentPath)
	switch {
	case errors.Is(err, ErrNotFound) || errors.Is(err, ErrAmbiguous) || errors.Is(err, ErrUnsupported):
		return m.fail(ctx, u, ErrNoStorage)
	case err != nil:
		return err
	case parent.ContentID() != u.folderID:
		return m.fail(ctx, u, ErrNoStorage)
	}

	if u.replace == "" {
		// Not while another change about this name may still be on its way.
		if err := m.settled(c.AccountKey, u.folderID, u.name); err != nil {
			return err
		}
		if c.P.CaseInsensitive() && !hasNativeKind(u.name) {
			// Such a provider keeps names unique itself and refuses the commit. Not
			// the names Fylvo makes up for entries without bytes: those it does not know.
			return nil
		}
		err := m.nameFree(ctx, c, u.folderID, u.name, "")
		if errors.Is(err, ErrNameTaken) || errors.Is(err, ErrCaseTaken) {
			return m.fail(ctx, u, err)
		}
		return err
	}

	row, err := m.tree.named(ctx, c, u.folderID, u.name)
	switch {
	case errors.Is(err, ErrNotFound) || errors.Is(err, ErrAmbiguous):
		return m.fail(ctx, u, ErrStale)
	case err != nil:
		return err
	case row.ID != u.replace:
		return m.fail(ctx, u, ErrStale)
	}
	item, err := c.P.Get(ctx, u.replace)
	switch {
	case provider.KindOf(err) == provider.KindNotFound:
		return m.fail(ctx, u, ErrStale)
	case err != nil:
		return err
	case item.Version != u.version:
		return m.fail(ctx, u, ErrStale)
	}
	return nil
}

// finish checks the provider's receipt and ends the upload with it. A receipt that does
// not show exactly this upload leaves its outcome unknown. The caller holds u.work.
func (m *Manager) finish(u *upload, item *provider.Item, checksum bool) (*Row, error) {
	ok := item.Size == u.size && (u.replace == "" || item.ID == u.replace)
	if checksum && u.sum != nil {
		ok = ok && item.Hash == hex.EncodeToString(u.sum.Sum(nil))
	}
	if !ok {
		m.endUnknown(u)
		return nil, ErrOutcomeUnknown
	}
	row := rowOf(item, u.path)
	u.end(UploadDone, row, nil, m.uploads.now())
	return row, nil
}

// UploadStatus tells where an upload of the account stands.
func (m *Manager) UploadStatus(ctx context.Context, userID int, id string) (*UploadStatus, error) {
	u, err := m.uploads.find(userID, id)
	if err != nil {
		return nil, err
	}
	// Only while the storage is connected as it was when the upload started. It may
	// need a new sign-in by now: the outcome of an upload is told all the same, it is
	// what a client that lost an answer is waiting for.
	row, err := m.row(ctx, userID, u.conn)
	if err != nil {
		// Also when the database did not answer: that is told as what it is, so a
		// client waiting for an outcome asks again.
		return nil, err
	}
	if row.Generation != u.generation {
		return nil, ErrNoStorage
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	return &UploadStatus{State: u.state, Received: u.received, Conn: u.conn, Generation: u.generation, Provider: u.provider, Row: u.row, Err: u.err}, nil
}

// CancelUpload ends an upload. It waits for a chunk or a commit under way. Before the
// commit the session is cancelled at the provider; nothing was stored. After a commit
// it changes nothing: done or failed is no error, unknown stays unknown.
func (m *Manager) CancelUpload(ctx context.Context, c *Conn, id string) error {
	u, err := m.session(c, id)
	if err != nil {
		return err
	}
	u.work.Lock()
	defer u.work.Unlock()

	switch state, _, _ := u.outcome(); state {
	case UploadDone, UploadFailed:
		return nil
	case UploadUnknown:
		return ErrOutcomeUnknown
	}
	// The provider cannot store the file without the tail held here, so the upload
	// is cancelled whatever the provider answers.
	_ = m.fail(ctx, u, ErrUploadCancelled)
	return nil
}
