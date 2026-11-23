package connected

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
)

// Previews of files in connected storages.
//
// Fylvo makes none itself: that would pull whole photos and films over the line. It
// takes the ready-made preview of the provider, only when a client asks for the one of
// a row it shows, never ahead and never in bulk. What it fetched it keeps on disk, by
// connection, item, version and size, so the provider is asked once per version.

const (
	// thumbMaxBytes is the most one preview may weigh. A bigger answer is no preview.
	thumbMaxBytes = 2 << 20
	// thumbCacheDefault is how much disk the kept previews of all connections may take.
	thumbCacheDefault = 256 << 20
	// maxThumbFetches is how many previews are fetched from providers at once on this
	// server; a further one waits thumbWait, then the client is told to come back.
	maxThumbFetches = 2
	// thumbFetchTime bounds one fetch at a provider.
	thumbFetchTime = 30 * time.Second
	// thumbLiveTime bounds the look at the connections there are, at the start.
	thumbLiveTime = 5 * time.Second
	// thumbMissLife is how long "the provider has none" is remembered for a version,
	// maxThumbMisses how many such notes are kept.
	thumbMissLife  = 6 * time.Hour
	maxThumbMisses = 20000
)

// EnvThumbCache sets, in megabytes, how much disk the kept previews may take. "0" keeps
// none: every preview is then fetched from the provider when it is asked for.
const EnvThumbCache = "FYLVO_CONNECTED_THUMB_CACHE_MB"

// ErrNoThumbnail: the provider has no preview of this version of the file.
var ErrNoThumbnail = errors.New("the provider has no preview of this file")

var thumbFetches = make(chan struct{}, maxThumbFetches)

// thumbWait is how long a fetch waits for one of the places.
var thumbWait = 10 * time.Second

// thumbFile is one kept preview.
type thumbFile struct {
	size int64
	// used is when it was last asked for, saved what the disk says about that.
	used, saved time.Time
}

// thumbFlight is a fetch that is under way, for those who ask for the same preview
// meanwhile.
type thumbFlight struct {
	done chan struct{}
	data []byte
	err  error
}

// thumbs keeps previews on disk, within a bound, and remembers which versions have none.
type thumbs struct {
	// dir is where the previews are kept: one folder per connection. "" keeps none.
	dir   string
	limit int64
	now   func() time.Time

	mu     sync.Mutex
	loaded bool
	// files are the kept previews by "<connection>/<name>".
	files map[string]*thumbFile
	total int64
	// misses are the versions the provider has no preview of, until when that counts.
	misses map[string]time.Time
	// gone are connections that ended: nothing is kept for them any more.
	gone    map[string]bool
	flights map[string]*thumbFlight
	// live returns the connections there are, for the start: the folder of one that is
	// no more (it ended while the server was down or going down) is removed. nil, or an
	// error, removes nothing.
	live func() ([]string, error)
}

func newThumbs(dir string) *thumbs {
	limit := int64(thumbCacheDefault)
	if mb, err := strconv.Atoi(os.Getenv(EnvThumbCache)); err == nil && mb >= 0 {
		limit = int64(mb) << 20
	}
	if limit == 0 && dir != "" {
		// Nothing is kept. What an earlier start kept goes.
		conns, _ := os.ReadDir(dir)
		for _, conn := range conns {
			if conn.IsDir() && validPublicID(conn.Name()) {
				_ = os.RemoveAll(filepath.Join(dir, conn.Name()))
			}
		}
		dir = ""
	}
	return &thumbs{
		dir: dir, limit: limit, now: time.Now,
		files: map[string]*thumbFile{}, misses: map[string]time.Time{}, gone: map[string]bool{}, flights: map[string]*thumbFlight{},
	}
}

const (
	thumbExt = ".img"
	// thumbTemp starts the name of a preview that is being written. One that is left
	// over after a crash is removed at the next start and never served.
	thumbTemp = "new-"
)

// thumbName is the name a preview is kept under: it tells the item, the state of its
// content and the size of the picture, and nothing readable.
func thumbName(item *provider.Item, size int) string {
	sum := sha256.Sum256(Context(item.ID, item.Version, strconv.Itoa(size)))
	return hex.EncodeToString(sum[:16]) + thumbExt
}

// load reads what is on disk, once. Half-written files are removed. The caller holds t.mu.
func (t *thumbs) load() {
	if t.loaded || t.dir == "" {
		return
	}
	t.loaded = true
	var known map[string]bool
	if t.live != nil {
		if ids, err := t.live(); err == nil {
			known = map[string]bool{}
			for _, id := range ids {
				known[id] = true
			}
		}
	}
	conns, _ := os.ReadDir(t.dir)
	for _, conn := range conns {
		if !conn.IsDir() || !validPublicID(conn.Name()) {
			continue
		}
		if known != nil && !known[conn.Name()] {
			// The connection ended and its folder was left behind.
			_ = os.RemoveAll(filepath.Join(t.dir, conn.Name()))
			continue
		}
		entries, _ := os.ReadDir(filepath.Join(t.dir, conn.Name()))
		for _, e := range entries {
			path := filepath.Join(t.dir, conn.Name(), e.Name())
			info, err := e.Info()
			if err != nil || !info.Mode().IsRegular() || !strings.HasSuffix(e.Name(), thumbExt) || strings.HasPrefix(e.Name(), thumbTemp) {
				_ = os.Remove(path)
				continue
			}
			t.files[conn.Name()+"/"+e.Name()] = &thumbFile{size: info.Size(), used: info.ModTime(), saved: info.ModTime()}
			t.total += info.Size()
		}
	}
	t.shrink()
}

// shrink removes the previews used longest ago until the bound holds again, and some
// more, so it does not run for every new one. The caller holds t.mu.
func (t *thumbs) shrink() {
	if t.total <= t.limit {
		return
	}
	keys := make([]string, 0, len(t.files))
	for k := range t.files {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return t.files[keys[i]].used.Before(t.files[keys[j]].used) })
	for _, k := range keys {
		if t.total <= t.limit/10*9 {
			break
		}
		t.remove(k)
	}
}

// remove deletes one kept preview. A file that cannot be deleted stays counted: it
// still takes its room. The caller holds t.mu.
func (t *thumbs) remove(key string) bool {
	if err := os.Remove(filepath.Join(t.dir, filepath.FromSlash(key))); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false
	}
	if f := t.files[key]; f != nil {
		t.total -= f.size
		delete(t.files, key)
	}
	return true
}

// get returns a kept preview.
func (t *thumbs) get(conn, name string) ([]byte, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.kept(conn, name)
}

// thumbUsedStep is how far the time a kept preview was last used may lag on disk. It
// is written there, so the order survives a restart, but not for every read.
const thumbUsedStep = time.Hour

// kept returns a kept preview. The caller holds t.mu.
func (t *thumbs) kept(conn, name string) ([]byte, bool) {
	t.load()
	key := conn + "/" + name
	f := t.files[key]
	if f == nil {
		return nil, false
	}
	path := filepath.Join(t.dir, conn, name)
	stored, err := os.ReadFile(path)
	data, whole := unsealed(stored)
	if err != nil || int64(len(stored)) != f.size || !whole || !isPicture(data) {
		// Gone or damaged: as if it had never been kept.
		t.remove(key)
		return nil, false
	}
	if now := t.now(); now.After(f.used) {
		f.used = now
		if now.Sub(f.saved) >= thumbUsedStep && os.Chtimes(path, now, now) == nil {
			f.saved = now
		}
	}
	return data, true
}

// A kept preview is the picture followed by its SHA-256, so a file that is not whole,
// whatever cut or changed it, is told from one that is.

func sealedPicture(data []byte) []byte {
	sum := sha256.Sum256(data)
	return append(append(make([]byte, 0, len(data)+len(sum)), data...), sum[:]...)
}

func unsealed(stored []byte) (data []byte, whole bool) {
	if len(stored) < sha256.Size {
		return nil, false
	}
	data = stored[:len(stored)-sha256.Size]
	sum := sha256.Sum256(data)
	return data, bytes.Equal(sum[:], stored[len(data):])
}

// put keeps a preview. It is written beside its place and moved there when it is
// complete, so a full disk or a crash leaves nothing that could be taken for a preview.
// When it cannot be kept it is not: the caller has the picture anyway.
func (t *thumbs) put(conn, name string, data []byte) {
	if t.dir == "" || !validPublicID(conn) || int64(len(data)) > t.limit {
		return
	}
	// What a start finds on disk is looked at before anything new is written there.
	t.mu.Lock()
	t.load()
	t.mu.Unlock()
	stored := sealedPicture(data)
	dir := filepath.Join(t.dir, conn)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	temp, err := os.CreateTemp(dir, thumbTemp+"*")
	if err != nil {
		return
	}
	_, err = temp.Write(stored)
	if closeErr := temp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(temp.Name())
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()
	key := conn + "/" + name
	if t.gone[conn] || os.Rename(temp.Name(), filepath.Join(dir, name)) != nil {
		_ = os.Remove(temp.Name())
		if t.gone[conn] {
			_ = os.Remove(dir)
		}
		return
	}
	if old := t.files[key]; old != nil {
		t.total -= old.size
	}
	now := t.now()
	t.files[key] = &thumbFile{size: int64(len(stored)), used: now, saved: now}
	t.total += int64(len(stored))
	t.shrink()
	if t.total > t.limit {
		// No room could be made (older ones could not be deleted): this one is not
		// kept either, so the bound holds.
		t.remove(key)
	}
}

// drop removes everything kept for a connection that ended, and keeps nothing more for it.
func (t *thumbs) drop(conn string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.gone[conn] = true
	prefix := conn + "/"
	for key, f := range t.files {
		if strings.HasPrefix(key, prefix) {
			t.total -= f.size
			delete(t.files, key)
		}
	}
	for key := range t.misses {
		if strings.HasPrefix(key, prefix) {
			delete(t.misses, key)
		}
	}
	if t.dir != "" && validPublicID(conn) {
		_ = os.RemoveAll(filepath.Join(t.dir, conn))
	}
}

// missing tells whether the provider is known to have no preview of this version.
func (t *thumbs) missing(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.missed(key)
}

// missed is missing for a caller that holds t.mu.
func (t *thumbs) missed(key string) bool {
	until, ok := t.misses[key]
	if ok && t.now().After(until) {
		delete(t.misses, key)
		ok = false
	}
	return ok
}

func (t *thumbs) miss(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if conn, _, _ := strings.Cut(key, "/"); t.gone[conn] {
		// The connection ended while the provider was asked: nothing more is kept for it.
		return
	}
	now := t.now()
	if len(t.misses) >= maxThumbMisses {
		// Those that ran out go first. If that is not enough some others go, whichever:
		// they are cheap to learn again.
		for k, until := range t.misses {
			if now.After(until) {
				delete(t.misses, k)
			}
		}
		for k := range t.misses {
			if len(t.misses) < maxThumbMisses/10*9 {
				break
			}
			delete(t.misses, k)
		}
	}
	t.misses[key] = now.Add(thumbMissLife)
}

// isPicture tells whether data is a picture of a kind every client shows.
func isPicture(data []byte) bool {
	kind := http.DetectContentType(data)
	return kind == "image/jpeg" || kind == "image/png"
}

// TidyThumbnails looks at what is kept on disk: what a crash left half-written and the
// folders of connections that are no more are removed, and the bound is applied. It
// runs once; the server calls it at its start, so none of that waits for somebody to
// load a preview.
func (m *Manager) TidyThumbnails() {
	m.thumbs.mu.Lock()
	defer m.thumbs.mu.Unlock()
	m.thumbs.load()
}

// HasThumbnail tells whether a row can have a preview: a file of the storage itself
// (no link, and nothing reached through one), in a state the provider names, of which
// the provider says it has a picture.
func HasThumbnail(row *Row) bool {
	return !row.IsDir && row.Unsupported == "" && row.Link == nil && !row.ViaLink && row.Version != "" && row.Preview
}

// NoThumbnail tells whether the provider is known to have no preview of the item in the
// given size, from an earlier try.
func (m *Manager) NoThumbnail(c *Conn, item *provider.Item, size int) bool {
	return m.thumbs.missing(c.ID + "/" + thumbName(item, size))
}

// Thumbnail returns the preview of an item of a connection in the given size, a JPEG or
// PNG picture. It comes from disk when it was fetched before, else from the provider.
// ErrNoThumbnail when the provider has none (which is remembered for a while), ErrBusy
// when too many are being fetched right now.
func (m *Manager) Thumbnail(ctx context.Context, c *Conn, item *provider.Item, size int) ([]byte, error) {
	source, ok := c.P.(provider.Thumbnailer)
	if !ok || item.Version == "" {
		return nil, ErrNoThumbnail
	}
	t, name := m.thumbs, thumbName(item, size)
	key := c.ID + "/" + name

	// One fetch per preview: who asks for the same one meanwhile waits for its result.
	// What is kept, what is known to be missing and what is under way are looked at in
	// one step, so nobody starts a second fetch for a picture that just arrived.
	for {
		t.mu.Lock()
		if data, ok := t.kept(c.ID, name); ok {
			t.mu.Unlock()
			return data, nil
		}
		if t.missed(key) {
			t.mu.Unlock()
			return nil, ErrNoThumbnail
		}
		running := t.flights[key]
		if running == nil {
			break
		}
		t.mu.Unlock()
		select {
		case <-running.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		if !errors.Is(running.err, context.Canceled) {
			// Also when the provider took too long: nobody queues up behind a provider
			// that stalls, each for a wait of his own.
			return running.data, running.err
		}
		// The one who fetched gave up (its client went away). That says nothing to
		// those who waited: the next of them fetches.
	}
	flight := &thumbFlight{done: make(chan struct{})}
	t.flights[key] = flight
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		delete(t.flights, key)
		t.mu.Unlock()
		close(flight.done)
	}()

	flight.data, flight.err = m.fetchThumbnail(ctx, source, c, item, size, name)
	return flight.data, flight.err
}

func (m *Manager) fetchThumbnail(ctx context.Context, source provider.Thumbnailer, c *Conn, item *provider.Item, size int, name string) ([]byte, error) {
	wait := time.NewTimer(thumbWait)
	defer wait.Stop()
	select {
	case thumbFetches <- struct{}{}:
		defer func() { <-thumbFetches }()
	case <-wait.C:
		return nil, ErrBusy
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithTimeout(ctx, thumbFetchTime)
	defer cancel()
	none := func() ([]byte, error) {
		m.thumbs.miss(c.ID + "/" + name)
		return nil, ErrNoThumbnail
	}
	content, err := source.Thumbnail(ctx, item, size)
	if err != nil {
		var failure *provider.Error
		switch {
		case provider.KindOf(err) == provider.KindNotFound, provider.KindOf(err) == provider.KindForbidden,
			provider.KindOf(err) == provider.KindUnsafe:
			// None, none for this account, or one that lies where nothing is fetched from.
			return none()
		case errors.As(err, &failure) && failure.Kind == provider.KindOther && failure.Status == http.StatusUnauthorized:
			// A plain "no" from the host a redirect led to, where no token was sent:
			// about this picture, and the same the next time. (A call with the token
			// never ends this way: there a 401 is answered by a new token.)
			return none()
		}
		return nil, err
	}
	defer content.Body.Close()
	if content.Partial {
		// A part of something, though the whole was asked for: not a picture to keep.
		return nil, &provider.Error{Kind: provider.KindBusy}
	}
	data, err := io.ReadAll(io.LimitReader(content.Body, thumbMaxBytes+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &provider.Error{Kind: provider.KindBusy}
	}
	if len(data) > thumbMaxBytes || !isPicture(data) {
		return none()
	}
	if ctx.Err() == nil {
		m.thumbs.put(c.ID, name, data)
	}
	return data, nil
}
