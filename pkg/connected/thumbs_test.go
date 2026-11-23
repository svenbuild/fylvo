package connected

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/stretchr/testify/require"
)

// Previews of files in connected storages: what is kept, and how the provider is asked.

const thumbConn = "aaaaaaaaaaaaaaaa"

// testPicture is a small PNG; different shades give different bytes.
func testPicture(shade uint8) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for i := range img.Pix {
		img.Pix[i] = shade
	}
	img.Set(0, 0, color.RGBA{shade, 0, 0, 255})
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		panic(err)
	}
	return out.Bytes()
}

// previews is a provider that hands out previews and counts how often it is asked.
type previews struct {
	provider.Provider
	calls   atomic.Int32
	answer  func(item *provider.Item, size int) ([]byte, error)
	entered chan struct{}
	release chan struct{}
}

func (p *previews) Thumbnail(ctx context.Context, item *provider.Item, size int) (*provider.Content, error) {
	p.calls.Add(1)
	if p.entered != nil {
		p.entered <- struct{}{}
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	data, err := p.answer(item, size)
	if err != nil {
		return nil, err
	}
	return &provider.Content{Body: io.NopCloser(bytes.NewReader(data)), Length: int64(len(data))}, nil
}

func thumbFixture(t *testing.T) (*Manager, *previews, *Conn, string) {
	dir := filepath.Join(t.TempDir(), "thumbs")
	p := &previews{answer: func(*provider.Item, int) ([]byte, error) { return testPicture(1), nil }}
	return &Manager{thumbs: newThumbs(dir)}, p, &Conn{ID: thumbConn, P: p}, dir
}

func kept(t *testing.T, dir string) []string {
	t.Helper()
	var names []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			names = append(names, filepath.ToSlash(rel))
		}
		return nil
	})
	return names
}

func TestThumbnailIsFetchedOnceAndKept(t *testing.T) {
	m, p, conn, dir := thumbFixture(t)
	ctx := context.Background()
	item := &provider.Item{ID: "file-1", Version: "r:1"}

	require.Empty(t, kept(t, dir), "nothing is made before the first preview")
	data, err := m.Thumbnail(ctx, conn, item, 400)
	require.NoError(t, err)
	require.Equal(t, testPicture(1), data)
	// The second time it comes from disk.
	data, err = m.Thumbnail(ctx, conn, item, 400)
	require.NoError(t, err)
	require.Equal(t, testPicture(1), data)
	require.EqualValues(t, 1, p.calls.Load())

	names := kept(t, dir)
	require.Len(t, names, 1)
	require.True(t, strings.HasPrefix(names[0], thumbConn+"/"), names[0])
	require.NotContains(t, names[0], "file-1", "the name on disk tells nothing")

	// Another version, another size, another item: each its own preview.
	for _, other := range []struct {
		item *provider.Item
		size int
	}{{&provider.Item{ID: "file-1", Version: "r:2"}, 400}, {item, 200}, {&provider.Item{ID: "file-2", Version: "r:1"}, 400}} {
		_, err := m.Thumbnail(ctx, conn, other.item, other.size)
		require.NoError(t, err)
	}
	require.EqualValues(t, 4, p.calls.Load())
	require.Len(t, kept(t, dir), 4)

	// A server that starts on the same folder finds them.
	restarted := &Manager{thumbs: newThumbs(dir)}
	data, err = restarted.Thumbnail(ctx, conn, item, 400)
	require.NoError(t, err)
	require.Equal(t, testPicture(1), data)
	require.EqualValues(t, 4, p.calls.Load())

	// An item whose state the provider does not name has no preview to tie to it, and
	// neither has anything of a provider that hands none out.
	_, err = m.Thumbnail(ctx, conn, &provider.Item{ID: "file-3"}, 400)
	require.ErrorIs(t, err, ErrNoThumbnail)
	var plain struct{ provider.Provider }
	_, err = m.Thumbnail(ctx, &Conn{ID: thumbConn, P: plain}, item, 100)
	require.ErrorIs(t, err, ErrNoThumbnail)
	require.EqualValues(t, 4, p.calls.Load())
}

// What is no usable preview is not served and not kept, and the provider is not asked
// again for that version for a while.
func TestThumbnailThatIsNone(t *testing.T) {
	for name, answer := range map[string]func(*provider.Item, int) ([]byte, error){
		"the provider has none":   func(*provider.Item, int) ([]byte, error) { return nil, &provider.Error{Kind: provider.KindNotFound} },
		"not for this account":    func(*provider.Item, int) ([]byte, error) { return nil, &provider.Error{Kind: provider.KindForbidden} },
		"it lies somewhere else":  func(*provider.Item, int) ([]byte, error) { return nil, &provider.Error{Kind: provider.KindUnsafe} },
		"a page instead":          func(*provider.Item, int) ([]byte, error) { return []byte("<html><body>Sign in</body></html>"), nil },
		"a picture of other kind": func(*provider.Item, int) ([]byte, error) { return []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"), nil },
		"nothing":                 func(*provider.Item, int) ([]byte, error) { return nil, nil },
		"too big": func(*provider.Item, int) ([]byte, error) {
			return append(testPicture(1), make([]byte, thumbMaxBytes)...), nil
		},
	} {
		m, p, conn, dir := thumbFixture(t)
		p.answer = answer
		item := &provider.Item{ID: "file-1", Version: "r:1"}
		require.False(t, m.NoThumbnail(conn, item, 400), name)
		for i := 0; i < 3; i++ {
			_, err := m.Thumbnail(context.Background(), conn, item, 400)
			require.ErrorIs(t, err, ErrNoThumbnail, name)
		}
		require.EqualValues(t, 1, p.calls.Load(), name)
		require.True(t, m.NoThumbnail(conn, item, 400), name)
		require.Empty(t, kept(t, dir), name)

		// A new version is asked for again, and so is the old one after a while.
		require.False(t, m.NoThumbnail(conn, &provider.Item{ID: "file-1", Version: "r:2"}, 400), name)
		now := time.Now().Add(thumbMissLife + time.Minute)
		m.thumbs.now = func() time.Time { return now }
		require.False(t, m.NoThumbnail(conn, item, 400), name)
	}
}

// Trouble that may pass is not remembered as "none".
func TestThumbnailTroubleIsNotRemembered(t *testing.T) {
	m, p, conn, dir := thumbFixture(t)
	item := &provider.Item{ID: "file-1", Version: "r:1"}
	for _, kind := range []provider.Kind{provider.KindBusy, provider.KindStale, provider.KindReconnect} {
		p.answer = func(*provider.Item, int) ([]byte, error) { return nil, &provider.Error{Kind: kind} }
		_, err := m.Thumbnail(context.Background(), conn, item, 400)
		require.Equal(t, kind, provider.KindOf(err))
		require.False(t, m.NoThumbnail(conn, item, 400))
	}
	require.Empty(t, kept(t, dir))
	p.answer = func(*provider.Item, int) ([]byte, error) { return testPicture(2), nil }
	data, err := m.Thumbnail(context.Background(), conn, item, 400)
	require.NoError(t, err)
	require.Equal(t, testPicture(2), data)
}

// Many clients that ask for the same preview at once make one fetch.
func TestThumbnailOneFetchForMany(t *testing.T) {
	m, p, conn, _ := thumbFixture(t)
	p.entered, p.release = make(chan struct{}, 1), make(chan struct{})
	item := &provider.Item{ID: "file-1", Version: "r:1"}

	var wg sync.WaitGroup
	results := make([][]byte, 6)
	errs := make([]error, 6)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = m.Thumbnail(context.Background(), conn, item, 400)
		}()
	}
	<-p.entered
	// Give the others time to arrive; they wait for the one that is under way.
	time.Sleep(100 * time.Millisecond)
	close(p.release)
	wg.Wait()
	for i := range results {
		require.NoError(t, errs[i])
		require.Equal(t, testPicture(1), results[i])
	}
	require.EqualValues(t, 1, p.calls.Load())
}

// The client that fetched goes away in the middle: those who waited for the same preview
// are not sent away with it. One of them fetches.
func TestThumbnailWaitersSurviveTheFetcher(t *testing.T) {
	m, p, conn, _ := thumbFixture(t)
	p.entered, p.release = make(chan struct{}, 2), make(chan struct{})
	item := &provider.Item{ID: "file-1", Version: "r:1"}

	leaving, leave := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() {
		_, err := m.Thumbnail(leaving, conn, item, 400)
		first <- err
	}()
	<-p.entered
	type result struct {
		data []byte
		err  error
	}
	second := make(chan result, 1)
	go func() {
		data, err := m.Thumbnail(context.Background(), conn, item, 400)
		second <- result{data, err}
	}()
	time.Sleep(50 * time.Millisecond)
	leave()
	require.ErrorIs(t, <-first, context.Canceled)
	<-p.entered
	close(p.release)
	got := <-second
	require.NoError(t, got.err)
	require.Equal(t, testPicture(1), got.data)
	require.EqualValues(t, 2, p.calls.Load())
	require.False(t, m.NoThumbnail(conn, item, 400))
}

// A provider that stalls: the fetch runs into its limit, and those who waited for the
// same preview are told to come back. They do not each wait their own turn at it.
func TestThumbnailWaitersDoNotQueueBehindAStall(t *testing.T) {
	m, p, conn, _ := thumbFixture(t)
	p.entered, p.release = make(chan struct{}, 1), make(chan struct{})
	p.answer = func(*provider.Item, int) ([]byte, error) { return nil, context.DeadlineExceeded }
	item := &provider.Item{ID: "file-1", Version: "r:1"}

	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		go func() {
			_, err := m.Thumbnail(context.Background(), conn, item, 400)
			errs <- err
		}()
	}
	<-p.entered
	time.Sleep(100 * time.Millisecond)
	close(p.release)
	for i := 0; i < 4; i++ {
		require.ErrorIs(t, <-errs, context.DeadlineExceeded)
	}
	require.EqualValues(t, 1, p.calls.Load())
	require.False(t, m.NoThumbnail(conn, item, 400))
}

// A preview that arrives while somebody is about to ask for it is not fetched twice:
// what is kept and what is under way are looked at in one step.
func TestThumbnailIsNotFetchedBesideTheCache(t *testing.T) {
	m, p, conn, _ := thumbFixture(t)
	item := &provider.Item{ID: "file-1", Version: "r:1"}
	var wg sync.WaitGroup
	errs := make(chan error, 200)
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Thumbnail(context.Background(), conn, item, 400)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, p.calls.Load())
}

// A part of a picture, where the whole was asked for, is no preview.
func TestThumbnailRefusesAPart(t *testing.T) {
	m, _, conn, dir := thumbFixture(t)
	conn.P = partial{}
	item := &provider.Item{ID: "file-1", Version: "r:1"}
	_, err := m.Thumbnail(context.Background(), conn, item, 400)
	require.Equal(t, provider.KindBusy, provider.KindOf(err))
	require.Empty(t, kept(t, dir))
	require.False(t, m.NoThumbnail(conn, item, 400))
}

type partial struct{ provider.Provider }

func (partial) Thumbnail(context.Context, *provider.Item, int) (*provider.Content, error) {
	picture := testPicture(1)
	return &provider.Content{Body: io.NopCloser(bytes.NewReader(picture)), Length: int64(len(picture)), Partial: true}, nil
}

// Rows that get no preview: a link, and what was reached through one.
func TestHasThumbnail(t *testing.T) {
	file := provider.Item{ID: "f", Version: "r:1", Preview: true}
	require.True(t, HasThumbnail(&Row{Item: file}))
	for name, row := range map[string]*Row{
		"through a link":   {Item: file, ViaLink: true},
		"a link":           {Item: provider.Item{ID: "f", Version: "r:1", Preview: true, Link: &provider.Link{TargetID: "t"}}, ViaLink: true},
		"a folder":         {Item: provider.Item{ID: "f", IsDir: true, Preview: true}},
		"no version":       {Item: provider.Item{ID: "f", Preview: true}},
		"none at provider": {Item: provider.Item{ID: "f", Version: "r:1"}},
		"unsupported":      {Item: file, Unsupported: "duplicate"},
	} {
		require.False(t, HasThumbnail(row), name)
	}
}

// What is known as "none" stays bounded without forgetting everything at once.
func TestThumbMissesAreBounded(t *testing.T) {
	c := newThumbs("")
	now := time.Unix(1800000000, 0)
	c.now = func() time.Time { return now }
	for i := 0; i < maxThumbMisses; i++ {
		c.miss("old-" + strconv.Itoa(i))
	}
	now = now.Add(thumbMissLife / 2)
	// Full. The next one makes room, and the recent ones are not all lost for it.
	for i := 0; i < 100; i++ {
		c.miss("new-" + strconv.Itoa(i))
	}
	require.LessOrEqual(t, len(c.misses), maxThumbMisses)
	require.Greater(t, len(c.misses), maxThumbMisses/2)
	for i := 0; i < 100; i++ {
		require.True(t, c.missing("new-"+strconv.Itoa(i)))
	}
	// Once the old ones ran out they are the ones that go.
	now = now.Add(thumbMissLife/2 + time.Minute)
	for i := 0; i < maxThumbMisses; i++ {
		c.miss("fill-" + strconv.Itoa(i))
	}
	for k := range c.misses {
		require.False(t, strings.HasPrefix(k, "old-"), k)
	}
}

// Only so many previews are fetched from providers at once. A further one waits a
// little and is then told to come back; nothing is remembered against it.
func TestThumbnailFetchesAreBounded(t *testing.T) {
	m, p, conn, _ := thumbFixture(t)
	p.entered, p.release = make(chan struct{}, maxThumbFetches), make(chan struct{})
	wait := thumbWait
	thumbWait = 50 * time.Millisecond
	defer func() { thumbWait = wait }()

	var wg sync.WaitGroup
	running := make(chan error, maxThumbFetches)
	for i := 0; i < maxThumbFetches; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := m.Thumbnail(context.Background(), conn, &provider.Item{ID: "busy-" + string(rune('a'+i)), Version: "r:1"}, 400)
			running <- err
		}()
		<-p.entered
	}
	late := &provider.Item{ID: "late", Version: "r:1"}
	_, err := m.Thumbnail(context.Background(), conn, late, 400)
	require.ErrorIs(t, err, ErrBusy)
	require.False(t, m.NoThumbnail(conn, late, 400))
	require.EqualValues(t, maxThumbFetches, p.calls.Load())

	// Who gives up waiting frees nothing and blocks nothing.
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = m.Thumbnail(gone, conn, late, 400)
	require.ErrorIs(t, err, context.Canceled)

	close(p.release)
	wg.Wait()
	close(running)
	for err := range running {
		require.NoError(t, err)
	}
	p.entered = nil
	_, err = m.Thumbnail(context.Background(), conn, late, 400)
	require.NoError(t, err)
}

// The previews on disk stay within their bound: the ones used longest ago go first.
func TestThumbCacheIsBounded(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "thumbs")
	c := newThumbs(dir)
	picture := testPicture(1)
	c.limit = int64(len(sealedPicture(picture))) * 10
	now := time.Unix(1800000000, 0)
	c.now = func() time.Time { return now }

	name := func(i int) string { return "p" + string(rune('a'+i)) + thumbExt }
	for i := 0; i < 10; i++ {
		now = now.Add(time.Second)
		c.put(thumbConn, name(i), picture)
	}
	require.Len(t, kept(t, dir), 10)
	// The first one is used again, so the second is the oldest now.
	now = now.Add(time.Second)
	_, ok := c.get(thumbConn, name(0))
	require.True(t, ok)
	now = now.Add(time.Second)
	c.put(thumbConn, name(10), picture)

	left := kept(t, dir)
	require.LessOrEqual(t, len(left), 9, "room is made for more than one")
	require.Contains(t, left, thumbConn+"/"+name(0))
	require.Contains(t, left, thumbConn+"/"+name(10))
	require.NotContains(t, left, thumbConn+"/"+name(1))
	require.LessOrEqual(t, c.total, c.limit)
	_, ok = c.get(thumbConn, name(1))
	require.False(t, ok)

	// A picture bigger than the whole bound is not kept at all.
	c.put(thumbConn, "big"+thumbExt, make([]byte, c.limit+1))
	require.NotContains(t, kept(t, dir), thumbConn+"/big"+thumbExt)

	// Started on a folder that holds more than the bound allows, it tidies up.
	smaller := newThumbs(dir)
	smaller.limit = int64(len(sealedPicture(picture))) * 3
	_, _ = smaller.get(thumbConn, name(0))
	require.LessOrEqual(t, len(kept(t, dir)), 3)
}

// When a preview was last used is written to disk now and then, so a restart still
// knows which ones were used longest ago.
func TestThumbCacheRemembersUseOverARestart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "thumbs")
	c := newThumbs(dir)
	picture := testPicture(1)
	now := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	c.now = func() time.Time { return now }
	for _, name := range []string{"a", "b", "c"} {
		c.put(thumbConn, name+thumbExt, picture)
		old := now
		require.NoError(t, os.Chtimes(filepath.Join(dir, thumbConn, name+thumbExt), old, old))
		now = now.Add(time.Minute)
	}
	// "a" is read a day later; a read a moment after that writes nothing again.
	now = now.Add(24 * time.Hour)
	_, ok := c.get(thumbConn, "a"+thumbExt)
	require.True(t, ok)
	info, err := os.Stat(filepath.Join(dir, thumbConn, "a"+thumbExt))
	require.NoError(t, err)
	require.WithinDuration(t, now, info.ModTime(), 2*time.Second)
	stamp := info.ModTime()
	now = now.Add(time.Minute)
	_, ok = c.get(thumbConn, "a"+thumbExt)
	require.True(t, ok)
	info, err = os.Stat(filepath.Join(dir, thumbConn, "a"+thumbExt))
	require.NoError(t, err)
	require.Equal(t, stamp, info.ModTime())

	// After a restart with room for two, the one read last is among those that stay.
	restarted := newThumbs(dir)
	restarted.limit = int64(len(sealedPicture(picture))) * 2
	_, ok = restarted.get(thumbConn, "a"+thumbExt)
	require.True(t, ok)
	require.Len(t, kept(t, dir), 1, "shrunk to nine tenths of two")
}

// A start removes what a connection left behind that ended while the server was down
// or going down. When the connections cannot be read, nothing is removed.
func TestThumbCacheStartTidiesUp(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "thumbs")
	first := newThumbs(dir)
	picture := testPicture(1)
	ended, lives := "cccccccccccccccc", "dddddddddddddddd"
	first.put(ended, "x"+thumbExt, picture)
	first.put(lives, "x"+thumbExt, picture)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "notes"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes", "mine.txt"), []byte("x"), 0o600))

	unsure := newThumbs(dir)
	unsure.live = func() ([]string, error) { return nil, errors.New("the database is not there yet") }
	_, ok := unsure.get(ended, "x"+thumbExt)
	require.True(t, ok)
	require.Len(t, kept(t, dir), 3)

	started := newThumbs(dir)
	started.live = func() ([]string, error) { return []string{lives}, nil }
	_, ok = started.get(lives, "x"+thumbExt)
	require.True(t, ok)
	_, ok = started.get(ended, "x"+thumbExt)
	require.False(t, ok)
	require.ElementsMatch(t, []string{lives + "/x" + thumbExt, "notes/mine.txt"}, kept(t, dir))
	require.Equal(t, int64(len(sealedPicture(picture))), started.total)

	// Switched off, what was kept before goes; a folder that is not ours stays.
	t.Setenv(EnvThumbCache, "0")
	off := newThumbs(dir)
	require.Empty(t, off.dir)
	require.Equal(t, []string{"notes/mine.txt"}, kept(t, dir))
}

// The switch for the size of the cache: megabytes, and 0 keeps nothing on disk.
func TestThumbCacheSwitch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "thumbs")
	require.EqualValues(t, thumbCacheDefault, newThumbs(dir).limit)
	t.Setenv(EnvThumbCache, "16")
	require.EqualValues(t, 16<<20, newThumbs(dir).limit)
	for _, ignored := range []string{"-1", "many", ""} {
		t.Setenv(EnvThumbCache, ignored)
		require.EqualValues(t, thumbCacheDefault, newThumbs(dir).limit, ignored)
	}

	t.Setenv(EnvThumbCache, "0")
	p := &previews{answer: func(*provider.Item, int) ([]byte, error) { return testPicture(1), nil }}
	m, conn := &Manager{thumbs: newThumbs(dir)}, &Conn{ID: thumbConn, P: p}
	for i := 0; i < 2; i++ {
		data, err := m.Thumbnail(context.Background(), conn, &provider.Item{ID: "file-1", Version: "r:1"}, 400)
		require.NoError(t, err)
		require.Equal(t, testPicture(1), data)
	}
	require.EqualValues(t, 2, p.calls.Load(), "nothing is kept, so the provider is asked each time")
	require.Empty(t, kept(t, dir))
}

// What a crash or a full disk left behind is never taken for a preview.
func TestThumbCacheServesNothingHalf(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "thumbs")
	conn := filepath.Join(dir, thumbConn)
	require.NoError(t, os.MkdirAll(conn, 0o700))
	picture := testPicture(1)
	whole := sealedPicture(picture)
	// A file that was being written, one cut short at its real name, a picture that
	// was never checked, something that is no picture though its checksum fits, and a
	// folder that is no connection.
	require.NoError(t, os.WriteFile(filepath.Join(conn, thumbTemp+"123456"), whole[:10], 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(conn, "cut"+thumbExt), whole[:len(whole)-5], 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(conn, "bare"+thumbExt), picture, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(conn, "page"+thumbExt), sealedPicture([]byte("<html>")), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(conn, "good"+thumbExt), whole, 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "not-a-connection"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "not-a-connection", "x"+thumbExt), picture, 0o600))

	c := newThumbs(dir)
	data, ok := c.get(thumbConn, "good"+thumbExt)
	require.True(t, ok)
	require.Equal(t, picture, data)
	for _, bad := range []string{thumbTemp + "123456", "cut" + thumbExt, "bare" + thumbExt, "page" + thumbExt, "missing" + thumbExt} {
		_, ok := c.get(thumbConn, bad)
		require.False(t, ok, bad)
	}
	left := kept(t, dir)
	require.ElementsMatch(t, []string{thumbConn + "/good" + thumbExt, "not-a-connection/x" + thumbExt}, left)

	// A preview changed on disk behind the server's back is dropped, not served: one
	// byte of the same length is enough.
	changed := append([]byte(nil), whole...)
	changed[len(picture)/2] ^= 1
	require.NoError(t, os.WriteFile(filepath.Join(conn, "good"+thumbExt), changed, 0o600))
	_, ok = c.get(thumbConn, "good"+thumbExt)
	require.False(t, ok)

	// A folder that cannot be written to keeps nothing, and that is all.
	blocked := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(blocked, nil, 0o600))
	none := newThumbs(blocked)
	none.put(thumbConn, "x"+thumbExt, picture)
	_, ok = none.get(thumbConn, "x"+thumbExt)
	require.False(t, ok)
}

// The previews of a connection go with it, and nothing is kept for it afterwards, also
// not by a fetch that was under way when it ended.
func TestThumbCacheEndsWithTheConnection(t *testing.T) {
	m, p, conn, dir := thumbFixture(t)
	ctx := context.Background()
	other := &Conn{ID: "bbbbbbbbbbbbbbbb", P: p}
	item := &provider.Item{ID: "file-1", Version: "r:1"}
	for _, c := range []*Conn{conn, other} {
		_, err := m.Thumbnail(ctx, c, item, 400)
		require.NoError(t, err)
	}
	p.answer = func(*provider.Item, int) ([]byte, error) { return nil, &provider.Error{Kind: provider.KindNotFound} }
	none := &provider.Item{ID: "file-2", Version: "r:1"}
	_, err := m.Thumbnail(ctx, conn, none, 400)
	require.ErrorIs(t, err, ErrNoThumbnail)
	require.Len(t, kept(t, dir), 2)

	// A fetch for the connection is at the provider when the connection ends.
	p.answer = func(*provider.Item, int) ([]byte, error) { return testPicture(3), nil }
	p.entered, p.release = make(chan struct{}, 1), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := m.Thumbnail(ctx, conn, &provider.Item{ID: "file-3", Version: "r:1"}, 400)
		done <- err
	}()
	<-p.entered
	m.thumbs.drop(thumbConn)
	close(p.release)
	require.NoError(t, <-done)

	left := kept(t, dir)
	require.Len(t, left, 1)
	require.True(t, strings.HasPrefix(left[0], other.ID+"/"))
	_, err = os.Stat(filepath.Join(dir, thumbConn))
	require.True(t, errors.Is(err, os.ErrNotExist), "the folder of the connection is gone")
	require.False(t, m.NoThumbnail(conn, none, 400), "what was known of it is forgotten")
	require.Equal(t, m.thumbs.files[other.ID+"/"+thumbName(item, 400)].size, m.thumbs.total)
}

// A grant sealed for an address a client keeps is the same every time, opens like any
// other, and is different for anything else.
func TestSealStable(t *testing.T) {
	ctx := context.Background()
	s := testSealer(1)
	plain, where := []byte(`{"k":"t","u":7}`), Context("grant")

	first, err := s.SealStable(ctx, PurposeGrant, where, plain)
	require.NoError(t, err)
	second, err := s.SealStable(ctx, PurposeGrant, where, plain)
	require.NoError(t, err)
	require.Equal(t, first, second)
	opened, err := s.Open(ctx, PurposeGrant, where, first)
	require.NoError(t, err)
	require.Equal(t, plain, opened)
	require.NotContains(t, string(first), `"u":7`)
	// It is marked as its own kind and has a key of its own: taken for an ordinary
	// seal it does not open, and an ordinary one does not open as this kind.
	require.EqualValues(t, sealStable, first[0])
	asOrdinary := append([]byte{sealVersion}, first[1:]...)
	_, err = s.Open(ctx, PurposeGrant, where, asOrdinary)
	require.ErrorIs(t, err, ErrSealed)

	// The ordinary seal is new every time.
	random, err := s.Seal(ctx, PurposeGrant, where, plain)
	require.NoError(t, err)
	require.NotEqual(t, first, random)
	require.EqualValues(t, sealVersion, random[0])
	asStable := append([]byte{sealStable}, random[1:]...)
	_, err = s.Open(ctx, PurposeGrant, where, asStable)
	require.ErrorIs(t, err, ErrSealed)
	opened, err = s.Open(ctx, PurposeGrant, where, random)
	require.NoError(t, err)
	require.Equal(t, plain, opened)

	// Another value, another place, another purpose, another key: another nonce and
	// another result.
	nonce := func(sealed []byte) []byte { return sealed[1 : 1+nonceSize] }
	for name, other := range map[string]func() ([]byte, error){
		"value":   func() ([]byte, error) { return s.SealStable(ctx, PurposeGrant, where, []byte(`{"k":"t","u":8}`)) },
		"context": func() ([]byte, error) { return s.SealStable(ctx, PurposeGrant, Context("other"), plain) },
		"purpose": func() ([]byte, error) { return s.SealStable(ctx, PurposeToken, where, plain) },
		"key":     func() ([]byte, error) { return testSealer(2).SealStable(ctx, PurposeGrant, where, plain) },
		// The same bytes cut differently between place and value.
		"split": func() ([]byte, error) { return s.SealStable(ctx, PurposeGrant, append(where, plain[:3]...), plain[3:]) },
	} {
		sealed, err := other()
		require.NoError(t, err, name)
		require.NotEqual(t, nonce(first), nonce(sealed), name)
		require.NotEqual(t, first, sealed, name)
	}
	// It opens only where it was sealed for.
	_, err = s.Open(ctx, PurposeToken, where, first)
	require.ErrorIs(t, err, ErrSealed)
	_, err = s.Open(ctx, PurposeGrant, Context("other"), first)
	require.ErrorIs(t, err, ErrSealed)
}
