package connected

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
)

var (
	// ErrNotFound: nothing is at the path.
	ErrNotFound = errors.New("path does not exist")
	// ErrAmbiguous: a name on the path stands for more than one entry at the provider.
	ErrAmbiguous = errors.New("name is not unique at the provider")
	// ErrListingGone: the listing a page token belongs to is no longer kept.
	ErrListingGone = errors.New("listing expired")
	// ErrTooLarge: a folder has more entries than the server lists.
	ErrTooLarge = errors.New("folder has too many entries")
)

const (
	// listingFresh is how long a listing answers further questions without asking the
	// provider again.
	listingFresh = 15 * time.Second
	// snapshotIdle is how long a paged listing is kept after its last use.
	snapshotIdle = 10 * time.Minute
	// maxRows is the most entries one folder may have, and the most rows all listings
	// and paged listings in memory hold together.
	maxRows = 50000
	// parallelListings is how many folders are read from providers at once.
	parallelListings = 2

	// UnsupportedDuplicate marks a row that stands for several entries with one name.
	UnsupportedDuplicate = "duplicate"
)

// Row is an entry of a connected storage as the server hands it on.
type Row struct {
	provider.Item
	// Path is the path the row was reached by, relative to the root of the connection.
	// Name is the last element of it: for a document that exists only at the provider,
	// the provider's name plus the extension of its kind.
	Path string
	// Unsupported is set for a row the server cannot address. Such a row has no actions.
	Unsupported string
	// ViaLink tells that the row is a link or was reached through one: it is read-only.
	ViaLink bool
}

// Enterable tells whether the row is a folder the server can list.
func (r *Row) Enterable() bool {
	// A link into another drive is listed but not entered: it opens at the provider.
	return r.IsDir && r.Unsupported == "" && !(r.Link != nil && r.Link.TargetDrive != "")
}

// ReadOnly tells whether nothing may be changed at or below the row.
func (r *Row) ReadOnly() bool {
	return r.ViaLink || r.Unsupported != "" || r.Native != ""
}

// displayName is the name a provider item has in Fylvo.
func displayName(it *provider.Item) string {
	if it.Native != "" {
		// The extension of the provider's own desktop app for such a document. It keeps
		// the document apart from a real file with the same name beside it.
		return it.Name + "." + it.Native
	}
	return it.Name
}

// nativeKinds are the extensions of documents that exist only at the provider.
var nativeKinds = map[string]bool{"gdoc": true, "gsheet": true, "gslides": true, "gdraw": true}

// plainName tells whether a name can be one element of a path.
func plainName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, c := range name {
		if c < 0x20 || c == 0x7f || c == '/' || c == '\\' {
			return false
		}
	}
	return true
}

type listKey struct {
	conn   string
	gen    int
	folder string
}

// listing is the content of one folder as the provider reported it.
type listing struct {
	entries []*Row // Path and ViaLink are not set here
	byName  map[string]*Row
	omitted int
	fold    bool
	at      time.Time
}

func (l *listing) find(name string) *Row {
	if l.fold {
		name = strings.ToLower(name)
	}
	return l.byName[name]
}

type snapshot struct {
	key      listKey
	user     int
	path     string
	order    Order
	rows     []*Row
	omitted  int
	lastUsed time.Time
}

// Page is one page of a listing.
type Page struct {
	// Parent is the folder that was listed.
	Parent *Row
	Rows   []*Row
	// Next continues the listing, empty on the last page.
	Next  string
	Total int
	// Omitted is the number of entries the provider has but the listing leaves out.
	Omitted int
}

// Order of a listing.
type Order struct {
	// By is "name", "size" or "updated_at".
	By   string
	Desc bool
}

// Tree reads folders of connected storages and turns paths into items. A path is only
// what the user sees: every question to a provider names a folder or an item by its id.
type Tree struct {
	now func() time.Time
	sem chan struct{}

	mu        sync.Mutex
	listings  map[listKey]*listing
	snapshots map[string]*snapshot
}

func NewTree() *Tree {
	return &Tree{
		now:       time.Now,
		sem:       make(chan struct{}, parallelListings),
		listings:  map[listKey]*listing{},
		snapshots: map[string]*snapshot{},
	}
}

func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// add puts an item into a listing under its name in Fylvo. An item that is already
// there is counted once; another item with the same name turns the row into one that
// stands for none of them.
func (l *listing) add(it provider.Item) {
	name := displayName(&it)
	if !plainName(name) {
		l.omitted++
		return
	}
	key := name
	if l.fold {
		key = strings.ToLower(name)
	}
	if first, taken := l.byName[key]; taken {
		if first.Unsupported == "" && first.ID == it.ID {
			return
		}
		*first = Row{
			Item:        provider.Item{Name: first.Name, IsDir: first.IsDir && it.IsDir},
			Unsupported: UnsupportedDuplicate,
		}
		return
	}
	it.Name = name
	row := &Row{Item: it}
	l.byName[key] = row
	l.entries = append(l.entries, row)
}

// fresh returns the listing of a folder if one is in memory and young enough.
func (t *Tree) fresh(key listKey) *listing {
	t.mu.Lock()
	defer t.mu.Unlock()
	l := t.listings[key]
	if l != nil && t.now().Sub(l.at) > listingFresh {
		delete(t.listings, key)
		return nil
	}
	return l
}

// load returns the listing of the folder with the given id, from memory when fresh.
func (t *Tree) load(ctx context.Context, c *Conn, folderID string) (*listing, error) {
	key := listKey{conn: c.ID, gen: c.Generation, folder: folderID}
	if l := t.fresh(key); l != nil {
		return l, nil
	}

	select {
	case t.sem <- struct{}{}:
		defer func() { <-t.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// Somebody else may have read it while this call waited.
	if l := t.fresh(key); l != nil {
		return l, nil
	}

	l := &listing{byName: map[string]*Row{}, fold: c.P.CaseInsensitive()}
	seen := 0
	for page := ""; ; {
		p, err := c.P.Children(ctx, folderID, page)
		if err != nil {
			if provider.KindOf(err) == provider.KindNotFound {
				return nil, ErrNotFound
			}
			return nil, err
		}
		seen += len(p.Items)
		if seen > maxRows {
			return nil, ErrTooLarge
		}
		for _, it := range p.Items {
			l.add(it)
		}
		if p.Next == "" || p.Next == page {
			break
		}
		page = p.Next
	}
	l.at = t.now()

	t.mu.Lock()
	t.sweep()
	t.makeRoom(len(l.entries))
	t.listings[key] = l
	t.mu.Unlock()
	return l, nil
}

// sweep drops what is too old. The caller holds t.mu.
func (t *Tree) sweep() {
	now := t.now()
	for k, l := range t.listings {
		if now.Sub(l.at) > listingFresh {
			delete(t.listings, k)
		}
	}
	for id, s := range t.snapshots {
		if now.Sub(s.lastUsed) > snapshotIdle {
			delete(t.snapshots, id)
		}
	}
}

// makeRoom drops paged listings, the ones used longest ago first, until rows more fit
// under the bound. Fresh listings go by themselves within seconds. The caller holds t.mu.
func (t *Tree) makeRoom(rows int) {
	total := rows
	for _, l := range t.listings {
		total += len(l.entries)
	}
	for _, s := range t.snapshots {
		total += len(s.rows)
	}
	for total > maxRows && len(t.snapshots) > 0 {
		var oldest string
		for id, s := range t.snapshots {
			if oldest == "" || s.lastUsed.Before(t.snapshots[oldest].lastUsed) {
				oldest = id
			}
		}
		total -= len(t.snapshots[oldest].rows)
		delete(t.snapshots, oldest)
	}
}

// named asks the provider for the entry with the given name in a folder, without
// reading the whole folder. The answer follows the rules of a listing, so both ways
// always agree.
func (t *Tree) named(ctx context.Context, c *Conn, folderID, name string) (*Row, error) {
	l := &listing{byName: map[string]*Row{}, fold: c.P.CaseInsensitive()}

	// An entry that carries the name as it is.
	items, err := c.P.Named(ctx, folderID, name)
	if err != nil {
		if provider.KindOf(err) == provider.KindNotFound {
			return nil, ErrNotFound
		}
		return nil, err
	}
	for _, it := range items {
		if it.Native == "" {
			l.add(it)
		}
	}

	// A document that exists only at the provider and shows under name: its own name
	// is the part before the extension of its kind.
	if dot := strings.LastIndex(name, "."); dot > 0 && nativeKinds[name[dot+1:]] {
		items, err := c.P.Named(ctx, folderID, name[:dot])
		if err != nil && provider.KindOf(err) != provider.KindNotFound {
			return nil, err
		}
		for _, it := range items {
			if it.Native == name[dot+1:] {
				l.add(it)
			}
		}
	}

	row := l.find(name)
	switch {
	case row == nil:
		return nil, ErrNotFound
	case row.Unsupported != "":
		return nil, ErrAmbiguous
	}
	return row, nil
}

// Resolve returns the entry at path, which must have passed CheckPath.
//
// It walks down from the root, one name at a time, and looks each name up in the folder
// it has reached by that folder's id. A name that stands for several entries makes the
// path ambiguous: refused, for the provider would let any of them answer.
func (t *Tree) Resolve(ctx context.Context, c *Conn, path string) (*Row, error) {
	rootID, err := c.P.Root(ctx)
	if err != nil {
		return nil, err
	}
	row := &Row{Item: provider.Item{ID: rootID, IsDir: true}}
	if path == "" {
		return row, nil
	}

	var (
		walked  string
		viaLink bool
	)
	for _, name := range strings.Split(path, "/") {
		if !row.Enterable() {
			return nil, ErrNotFound
		}
		folderID := row.ContentID()

		var found *Row
		if l := t.fresh(listKey{conn: c.ID, gen: c.Generation, folder: folderID}); l != nil {
			found = l.find(name)
			switch {
			case found == nil:
				return nil, ErrNotFound
			case found.Unsupported != "":
				return nil, ErrAmbiguous
			}
		} else if found, err = t.named(ctx, c, folderID, name); err != nil {
			return nil, err
		}

		walked = joinPath(walked, found.Name)
		viaLink = viaLink || found.Link != nil
		next := *found
		next.Path, next.ViaLink = walked, viaLink
		row = &next
	}
	return row, nil
}

func compareInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func sortRows(rows []*Row, o Order) {
	byName := func(a, b *Row) int {
		if c := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); c != 0 {
			return c
		}
		// Names are unique within a listing, so this makes the order total.
		return strings.Compare(a.Name, b.Name)
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		// Folders first, in every order.
		if a.IsDir != b.IsDir {
			return a.IsDir
		}
		cmp := 0
		switch o.By {
		case "size":
			cmp = compareInt(a.Size, b.Size)
		case "updated_at":
			cmp = a.ModTime.Compare(b.ModTime)
		default:
			cmp = byName(a, b)
		}
		if cmp == 0 {
			// Equal keys are in name order, whatever the direction.
			return byName(a, b) < 0
		}
		if o.Desc {
			return cmp > 0
		}
		return cmp < 0
	})
}

// List returns a page of the folder at path. An empty token starts a listing; the token
// of a page continues it on exactly the rows the listing started with, for the same
// account, connection, folder and order only.
func (t *Tree) List(ctx context.Context, c *Conn, path string, o Order, size int, token string) (*Page, error) {
	if size <= 0 {
		size = 100
	}

	if token != "" {
		id, offset, ok := parseToken(token)
		if !ok {
			return nil, ErrListingGone
		}
		t.mu.Lock()
		t.sweep()
		snap := t.snapshots[id]
		if snap != nil && (snap.key.conn != c.ID || snap.key.gen != c.Generation || snap.user != c.UserID ||
			snap.path != path || snap.order != o) {
			// A token of another account, connection, folder or order.
			snap = nil
		}
		if snap != nil {
			snap.lastUsed = t.now()
		}
		t.mu.Unlock()
		if snap == nil || offset > len(snap.rows) {
			return nil, ErrListingGone
		}
		return snap.page(id, offset, size), nil
	}

	parent, err := t.Resolve(ctx, c, path)
	if err != nil {
		return nil, err
	}
	if !parent.Enterable() {
		return nil, ErrNotFound
	}
	l, err := t.load(ctx, c, parent.ContentID())
	if err != nil {
		return nil, err
	}

	viaLink := parent.ViaLink
	rows := make([]*Row, 0, len(l.entries))
	for _, e := range l.entries {
		row := *e
		row.Path = joinPath(path, row.Name)
		row.ViaLink = viaLink || row.Link != nil
		rows = append(rows, &row)
	}
	sortRows(rows, o)

	snap := &snapshot{
		key:  listKey{conn: c.ID, gen: c.Generation, folder: parent.ContentID()},
		user: c.UserID, path: path, order: o, rows: rows, omitted: l.omitted,
	}
	id := ""
	if len(rows) > size {
		// Only a listing with more than one page is kept.
		id = randomID()
		t.mu.Lock()
		t.sweep()
		t.makeRoom(len(rows))
		snap.lastUsed = t.now()
		t.snapshots[id] = snap
		t.mu.Unlock()
	}
	page := snap.page(id, 0, size)
	page.Parent = parent
	return page, nil
}

func (s *snapshot) page(id string, offset, size int) *Page {
	end := min(offset+size, len(s.rows))
	page := &Page{Rows: s.rows[offset:end], Total: len(s.rows), Omitted: s.omitted}
	if end < len(s.rows) {
		page.Next = id + "." + strconv.Itoa(end)
	}
	return page
}

func randomID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func parseToken(token string) (id string, offset int, ok bool) {
	id, rest, found := strings.Cut(token, ".")
	if !found || len(id) != 24 {
		return "", 0, false
	}
	offset, err := strconv.Atoi(rest)
	if err != nil || offset < 0 {
		return "", 0, false
	}
	return id, offset, true
}

// Changed forgets what is known about the folders with the given ids after a write
// there. Paged listings of these folders end: their next page answers ErrListingGone.
func (t *Tree) Changed(c *Conn, folderIDs ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, folder := range folderIDs {
		key := listKey{conn: c.ID, gen: c.Generation, folder: folder}
		delete(t.listings, key)
		for id, s := range t.snapshots {
			if s.key == key {
				delete(t.snapshots, id)
			}
		}
	}
}

// Forget drops everything known about a connection, in every generation.
func (t *Tree) Forget(connection string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for k := range t.listings {
		if k.conn == connection {
			delete(t.listings, k)
		}
	}
	for id, s := range t.snapshots {
		if s.key.conn == connection {
			delete(t.snapshots, id)
		}
	}
}
