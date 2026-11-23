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

// listingWait is how long a listing waits for one of the parallelListings places.
var listingWait = 10 * time.Second

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
	// parallelWalks is how many walks below a folder run at once (see walk.go).
	parallelWalks = 2
	// maxPages is how many pages the provider may need for one folder. A real folder
	// under maxRows stays far below it; a provider that keeps sending pages does not.
	maxPages = 2000

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

// Replaceable tells whether a save can replace the content of a row: a file with bytes
// of its own whose state the provider tells. At OneDrive that includes its tag, without
// which a replace cannot be tied to the state it was started on.
func (c *Conn) Replaceable(row *Row) bool {
	if row.IsDir || row.ReadOnly() || row.Version == "" {
		return false
	}
	return c.Provider != ProviderOneDrive || row.ETag != ""
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

// nativeKinds are the extensions of entries that exist only at the provider.
var nativeKinds = func() map[string]bool {
	kinds := map[string]bool{}
	for _, kind := range provider.NativeKinds {
		kinds[kind] = true
	}
	return kinds
}()

// nativeKindOf tells whether a name ends in the extension of an entry that exists only
// at the provider, and which. Where names are told apart without regard to case, so is
// the extension.
func nativeKindOf(name string, fold bool) (string, bool) {
	dot := strings.LastIndex(name, ".")
	if dot <= 0 {
		return "", false
	}
	kind := name[dot+1:]
	if fold {
		kind = strings.ToLower(kind)
	}
	return kind, nativeKinds[kind]
}

// hasNativeKind tells whether a name could be taken for an entry that exists only at the
// provider, in any spelling.
func hasNativeKind(name string) bool {
	_, ok := nativeKindOf(name, true)
	return ok
}

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
	// ids are the entries seen while the listing is read.
	ids     map[string]bool
	omitted int
	fold    bool
	at      time.Time
	// account is the account at the provider the folder belongs to (Conn.AccountKey).
	account string
}

func newListing(fold bool) *listing {
	return &listing{byName: map[string]*Row{}, ids: map[string]bool{}, fold: fold}
}

func (l *listing) find(name string) *Row {
	if l.fold {
		name = Fold(name)
	}
	return l.byName[name]
}

type snapshot struct {
	key     listKey
	account string
	user    int
	path    string
	order   Order
	parent  *Row
	// rows are the entries of the listing in the order of the snapshot. They belong
	// to the listing and are not changed: a page gets copies with its paths.
	rows     []*Row
	viaLink  bool
	omitted  int
	lastUsed time.Time

	// deep marks the snapshot of a walk below the folder (see walk.go): its rows carry
	// their own paths. scan is what the walk kept, folders are the ids of all folders
	// it read, incomplete and links what it has to say about itself.
	deep       bool
	scan       string
	folders    map[string]bool
	incomplete bool
	links      int
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
	// Deep marks a page of a walk below the folder. Incomplete tells that the walk did
	// not see everything, Links how many links it met and did not enter.
	Deep       bool
	Incomplete bool
	Links      int
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
	// walks has a place for every walk that may run at once.
	walks chan struct{}

	mu        sync.Mutex
	listings  map[listKey]*listing
	snapshots map[string]*snapshot
	// reads are the folders being read right now.
	reads map[*reading]struct{}
}

// reading is a read of a folder under way. A write to the folder marks it: what was
// read may be from before the write, so it is not kept for anybody who asks later.
type reading struct {
	key     listKey
	account string
	changed bool
}

// read notes that a folder is being read from now on. The returned function ends that.
func (t *Tree) read(key listKey, account string) (*reading, func()) {
	r := &reading{key: key, account: account}
	t.mu.Lock()
	t.reads[r] = struct{}{}
	t.mu.Unlock()
	return r, func() {
		t.mu.Lock()
		delete(t.reads, r)
		t.mu.Unlock()
	}
}

func NewTree() *Tree {
	return &Tree{
		now:       time.Now,
		sem:       make(chan struct{}, parallelListings),
		walks:     make(chan struct{}, parallelWalks),
		listings:  map[listKey]*listing{},
		snapshots: map[string]*snapshot{},
		reads:     map[*reading]struct{}{},
	}
}

func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// add puts an item into a listing under its name in Fylvo. An item that is already
// there is counted once, also when it shows up a second time under another name (it was
// renamed while the provider sent its pages); another item with the same name turns the
// row into one that stands for none of them.
func (l *listing) add(it provider.Item) {
	if l.ids[it.ID] {
		return
	}
	l.ids[it.ID] = true
	// The provider's own name decides: "." does not become a name by the extension a
	// document gets here.
	name := displayName(&it)
	if !plainName(it.Name) || !plainName(name) {
		l.omitted++
		return
	}
	key := name
	if l.fold {
		key = Fold(name)
	}
	if first, taken := l.byName[key]; taken {
		// The row stands for none of them. It keeps a time to show: the newest.
		newest := first.ModTime
		if it.ModTime.After(newest) {
			newest = it.ModTime
		}
		*first = Row{
			Item:        provider.Item{Name: first.Name, IsDir: first.IsDir && it.IsDir, ModTime: newest},
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

	wait := time.NewTimer(listingWait)
	defer wait.Stop()
	select {
	case t.sem <- struct{}{}:
		defer func() { <-t.sem }()
	case <-wait.C:
		return nil, &provider.Error{Kind: provider.KindBusy}
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	// Somebody else may have read it while this call waited.
	if l := t.fresh(key); l != nil {
		return l, nil
	}

	r, done := t.read(key, c.AccountKey)
	defer done()
	l := newListing(c.P.CaseInsensitive())
	for page, pages := "", 0; ; pages++ {
		p, err := c.P.Children(ctx, folderID, page)
		if err != nil {
			if provider.KindOf(err) == provider.KindNotFound {
				return nil, ErrNotFound
			}
			return nil, err
		}
		for _, it := range p.Items {
			l.add(it)
		}
		// Counted by entry: one that shows up twice while the folder changes is one.
		if len(l.ids) > maxRows {
			return nil, ErrTooLarge
		}
		if p.Next == "" {
			break
		}
		if p.Next == page || pages >= maxPages {
			// A provider that goes in circles has not told the whole folder.
			return nil, &provider.Error{Kind: provider.KindIncomplete}
		}
		page = p.Next
	}
	l.ids = nil
	l.at = t.now()
	l.account = c.AccountKey

	t.mu.Lock()
	defer t.mu.Unlock()
	// The connection can have ended while the provider answered. What ended it marked
	// it before it cleared this memory, so nothing of it comes back here.
	if c.ended() {
		return nil, context.Canceled
	}
	if r.changed {
		// Written to while it was read: good for this call, which began before the
		// write, and for nobody after it.
		return l, nil
	}
	t.sweep()
	t.makeRoom(len(l.entries))
	t.listings[key] = l
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

// makeRoom drops what is in memory until rows more fit under the bound: first the fresh
// listings, oldest first, which only save a question to the provider, then the paged
// listings, the ones used longest ago first. The caller holds t.mu.
func (t *Tree) makeRoom(rows int) {
	// Every listing counts one more than its rows: also an empty one takes room.
	total := rows + 1
	for _, l := range t.listings {
		total += len(l.entries) + 1
	}
	for _, s := range t.snapshots {
		total += len(s.rows)
	}
	for total > maxRows && len(t.listings) > 0 {
		var (
			oldest listKey
			found  bool
		)
		for k, l := range t.listings {
			if !found || l.at.Before(t.listings[oldest].at) {
				oldest, found = k, true
			}
		}
		total -= len(t.listings[oldest].entries) + 1
		delete(t.listings, oldest)
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
	l := newListing(c.P.CaseInsensitive())

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
	if kind, ok := nativeKindOf(name, l.fold); ok {
		items, err := c.P.Named(ctx, folderID, name[:len(name)-len(kind)-1])
		if err != nil && provider.KindOf(err) != provider.KindNotFound {
			return nil, err
		}
		for _, it := range items {
			if it.Native == kind {
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
	return t.resolve(ctx, c, path, true)
}

// ResolveNow is Resolve without the listings in memory: every name on the path is looked
// up at the provider. For changes, which must act on what is there now.
func (t *Tree) ResolveNow(ctx context.Context, c *Conn, path string) (*Row, error) {
	return t.resolve(ctx, c, path, false)
}

func (t *Tree) resolve(ctx context.Context, c *Conn, path string, cached bool) (*Row, error) {
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
		var l *listing
		if cached {
			l = t.fresh(listKey{conn: c.ID, gen: c.Generation, folder: folderID})
		}
		if l != nil {
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

		// A row from a listing in memory: the provider may need to be told again what
		// it takes to reach the entry.
		c.P.Known(&found.Item)
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
		snap, id, offset := t.snapshot(c, path, o, token)
		if snap == nil || snap.deep {
			// Also the token of a walk: a listing of one folder is not continued by it.
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
	key := listKey{conn: c.ID, gen: c.Generation, folder: parent.ContentID()}
	r, done := t.read(key, c.AccountKey)
	defer done()
	l, err := t.load(ctx, c, parent.ContentID())
	if err != nil {
		return nil, err
	}

	// Only the order is made anew for this request. The rows stay the listing's.
	rows := make([]*Row, len(l.entries))
	copy(rows, l.entries)
	sortRows(rows, o)

	snap := &snapshot{
		key: key, account: c.AccountKey,
		user: c.UserID, path: path, order: o, parent: parent, rows: rows, viaLink: parent.ViaLink,
		omitted: l.omitted,
	}
	id := ""
	if len(rows) > size {
		// Only a listing with more than one page is kept.
		id = randomID()
		t.mu.Lock()
		if c.ended() {
			t.mu.Unlock()
			return nil, context.Canceled
		}
		// A write to the folder since this began ended the listing before its first
		// page: it is not kept, and its next page answers ErrListingGone.
		if !r.changed {
			t.sweep()
			t.makeRoom(len(rows))
			snap.lastUsed = t.now()
			t.snapshots[id] = snap
		}
		t.mu.Unlock()
	}
	if c.ended() {
		return nil, context.Canceled
	}
	return snap.page(id, 0, size), nil
}

// snapshot returns the paged listing a token continues, for the account, connection,
// folder and order it was made for; nil for any other, and for a token that is no
// longer good.
func (t *Tree) snapshot(c *Conn, path string, o Order, token string) (snap *snapshot, id string, offset int) {
	id, offset, ok := parseToken(token)
	if !ok {
		return nil, "", 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sweep()
	snap = t.snapshots[id]
	if snap == nil || snap.key.conn != c.ID || snap.key.gen != c.Generation || snap.user != c.UserID ||
		snap.path != path || snap.order != o || offset > len(snap.rows) {
		// A token of another account, connection, folder or order.
		return nil, "", 0
	}
	snap.lastUsed = t.now()
	return snap, id, offset
}

func (s *snapshot) page(id string, offset, size int) *Page {
	end := min(offset+size, len(s.rows))
	rows := make([]*Row, 0, end-offset)
	for _, e := range s.rows[offset:end] {
		row := *e
		if !s.deep {
			row.Path = joinPath(s.path, row.Name)
			row.ViaLink = s.viaLink || row.Link != nil
		}
		rows = append(rows, &row)
	}
	page := &Page{
		Parent: s.parent, Rows: rows, Total: len(s.rows), Omitted: s.omitted,
		Deep: s.deep, Incomplete: s.incomplete, Links: s.links,
	}
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
// That goes for every connection to the same account at the provider: it is the same
// folder, whoever in Fylvo looks at it.
func (t *Tree) Changed(c *Conn, folderIDs ...string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, folder := range folderIDs {
		same := func(key listKey, account string) bool {
			if key.folder != folder {
				return false
			}
			return (key.conn == c.ID && key.gen == c.Generation) || (c.AccountKey != "" && account == c.AccountKey)
		}
		for key, l := range t.listings {
			if same(key, l.account) {
				delete(t.listings, key)
			}
		}
		for id, s := range t.snapshots {
			// A walk ends with a write to any folder it read.
			below := s.deep && s.folders[folder] &&
				((s.key.conn == c.ID && s.key.gen == c.Generation) || (c.AccountKey != "" && s.account == c.AccountKey))
			if same(s.key, s.account) || below {
				delete(t.snapshots, id)
			}
		}
		for r := range t.reads {
			if same(r.key, r.account) {
				r.changed = true
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
