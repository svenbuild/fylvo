package connected

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
)

// A listing of everything below a folder, and the search that is built on it.
//
// A provider has no call for it that follows the rules of a listing (names that stand
// for several entries, links, entries without bytes). So the server walks the tree
// itself, folder by folder, through the same listings and under the same bounds as
// when a client opens them one by one, and only when a client asks for it. The result
// is one snapshot, paged like any listing. What it could not see it says: a walk that
// ran into a bound, or met a folder it could not read whole, is marked incomplete.

const (
	// walkRows is the most rows one walk looks at. A folder is read whole before its
	// rows are counted, as for a plain listing, so a walk reads at most this many rows
	// and one folder more (which has its own bound, maxRows).
	walkRows = 20000
)

// walkTime is how long one walk may take.
var walkTime = 60 * time.Second

// Scan says what a walk below a folder keeps of what it sees.
type Scan struct {
	// Names are parts of a name. A row is kept when its name has all of them, or with
	// Any one of them. None keeps every row.
	Names []string
	Any   bool
	// Fold compares names without regard to case.
	Fold bool
	// Kind is "file" or "folder" to keep only those, "" for both.
	Kind string
}

// key is the scan as one text, for telling two scans apart.
func (s Scan) key() string {
	parts := append([]string{s.Kind, map[bool]string{true: "fold"}[s.Fold], map[bool]string{true: "any"}[s.Any]}, s.Names...)
	return string(Context(parts...))
}

// keeps tells whether a row is one the scan asks for.
func (s Scan) keeps(row *Row) bool {
	switch s.Kind {
	case "file":
		if row.IsDir {
			return false
		}
	case "folder":
		if !row.IsDir {
			return false
		}
	}
	if len(s.Names) == 0 {
		return true
	}
	name := row.Name
	if s.Fold {
		name = Fold(name)
	}
	for _, part := range s.Names {
		if s.Fold {
			part = Fold(part)
		}
		if has := strings.Contains(name, part); has == s.Any {
			// With Any the first hit decides, without it the first miss.
			return has
		}
	}
	return !s.Any
}

// Walk returns a page of everything below the folder at path that the scan keeps. An
// empty token starts a walk; the token of a page continues it on exactly the rows the
// walk found, for the same account, connection, folder, scan and order only.
//
// Links are listed and never entered: they can lead back up. A walk is incomplete, and
// says so in its pages, when it ran into its bound of rows or of time, when a folder
// below could not be read whole (it is too large, it went away, the provider did not
// tell all of it), or when a folder has entries that are left out or that share a name.
func (t *Tree) Walk(ctx context.Context, c *Conn, path string, scan Scan, o Order, size int, token string) (*Page, error) {
	if size <= 0 {
		size = 100
	}
	if token != "" {
		snap, id, offset := t.snapshot(c, path, o, token)
		if snap == nil || !snap.deep || snap.scan != scan.key() {
			return nil, ErrListingGone
		}
		return snap.page(id, offset, size), nil
	}

	// A walk holds its rows while it runs and asks the provider folder by folder. Only
	// parallelWalks of them run at once, on the whole server: that bounds the memory
	// they take together. The others wait for a place as long as a listing waits, and
	// one whose client gave up frees its place at its next folder.
	wait := time.NewTimer(listingWait)
	defer wait.Stop()
	select {
	case t.walks <- struct{}{}:
		defer func() { <-t.walks }()
	case <-wait.C:
		return nil, &provider.Error{Kind: provider.KindBusy}
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// The time of a walk runs from here: the way to its folder is part of it.
	asked := ctx
	ctx, cancel := context.WithTimeout(ctx, walkTime)
	defer cancel()
	// late tells that the walk ran out of its own time, while its client still waits.
	// A provider call that was cut off by it may name its error as it likes.
	late := func() bool {
		return errors.Is(ctx.Err(), context.DeadlineExceeded) && asked.Err() == nil
	}

	parent, err := t.Resolve(ctx, c, path)
	if err != nil {
		return nil, err
	}
	if !parent.Enterable() {
		return nil, ErrNotFound
	}

	snap := &snapshot{
		key:     listKey{conn: c.ID, gen: c.Generation, folder: parent.ContentID()},
		account: c.AccountKey, user: c.UserID, path: path, order: o, parent: parent, viaLink: parent.ViaLink,
		deep: true, scan: scan.key(), folders: map[string]bool{},
	}
	// Every folder that is read is marked while the walk runs, so a change in any of
	// them is noticed and the walk is not kept for further pages.
	var (
		reads  []*reading
		unread []func()
	)
	defer func() {
		for _, done := range unread {
			done()
		}
	}()

	type folder struct {
		id, path string
		viaLink  bool
		// item is the entry the folder was reached as.
		item *provider.Item
	}
	queue := []folder{{id: parent.ContentID(), path: path, viaLink: parent.ViaLink, item: &parent.Item}}
	// full is set when a bound of the walk is reached: it ends there. What it could
	// not see of a single folder only marks it incomplete, and it goes on.
	seen, full := 0, false
	for len(queue) > 0 && !full {
		f := queue[0]
		queue = queue[1:]
		if snap.folders[f.id] {
			// Reached a second time by another way: listed once.
			continue
		}
		snap.folders[f.id] = true
		r, done := t.read(listKey{conn: c.ID, gen: c.Generation, folder: f.id}, c.AccountKey)
		reads, unread = append(reads, r), append(unread, done)

		l, err := t.load(ctx, c, f.id, f.item)
		if err == nil {
			// A provider that does not look at its caller's patience is asked here.
			err = ctx.Err()
		}
		if err != nil {
			if provider.KindOf(err) == provider.KindReconnect {
				// The connection has to be renewed. That is the answer, also when the
				// time of the walk ran out in the same moment.
				return nil, err
			}
			if gone := asked.Err(); gone != nil {
				// The client left. Whatever the provider said about this folder, there
				// is nobody to answer.
				return nil, gone
			}
			below := f.id != parent.ContentID() && (errors.Is(err, ErrNotFound) || errors.Is(err, ErrTooLarge) ||
				provider.KindOf(err) == provider.KindIncomplete || provider.KindOf(err) == provider.KindForbidden)
			if (late() || below) && c.ended() {
				// What is left out below is only left out of a connection that is still there.
				return nil, context.Canceled
			}
			if late() && f.id != parent.ContentID() {
				// Out of time: what was found is the answer, marked as not all.
				snap.incomplete, snap.missed, full = true, true, true
				break
			}
			if below {
				// A folder below that cannot be read whole, or not at all: the rest is
				// still worth having.
				snap.incomplete, snap.missed = true, true
				continue
			}
			// The folder asked for itself (also when all the time went into it: nothing
			// was found yet), or trouble that is not about one folder (the provider is
			// busy, the client is gone): no answer, and the error is the provider's own.
			return nil, err
		}
		if l.omitted > 0 {
			snap.omitted += l.omitted
			snap.incomplete, snap.missed = true, true
		}
		for _, e := range l.entries {
			if seen++; seen > walkRows {
				snap.incomplete, snap.missed, full = true, true, true
				break
			}
			row := *e
			row.Path = joinPath(f.path, row.Name)
			row.ViaLink = f.viaLink || row.Link != nil
			if row.Link != nil {
				snap.links++
			}
			if row.Unsupported != "" {
				// A name that stands for several entries: what is behind it stays unseen.
				snap.incomplete = true
			}
			if row.Enterable() && row.Link == nil {
				queue = append(queue, folder{id: row.ContentID(), path: row.Path, viaLink: row.ViaLink, item: &e.Item})
			}
			if scan.keeps(&row) {
				snap.rows = append(snap.rows, &row)
			}
		}
	}
	// Rows of different folders can be equal in every key of the order. Their paths
	// decide then, so the same walk is in the same order every time.
	sort.Slice(snap.rows, func(i, j int) bool { return snap.rows[i].Path < snap.rows[j].Path })
	sortRows(snap.rows, o)

	t.mu.Lock()
	if c.ended() {
		t.mu.Unlock()
		return nil, context.Canceled
	}
	// A write through Fylvo to a folder the walk read, while it ran. An entry that was
	// moved from a folder read later into one read before is in neither listing, so
	// the walk says that it may not have seen everything. It is not kept either: it
	// was ended before its first page, and its next page answers ErrListingGone.
	changed := false
	for _, r := range reads {
		changed = changed || r.changed
	}
	if changed {
		snap.incomplete, snap.missed = true, true
	}
	id := ""
	if len(snap.rows) > size {
		id = randomID()
		if !changed {
			t.sweep()
			t.makeRoom(snap.weight())
			snap.lastUsed = t.now()
			t.snapshots[id] = snap
		}
	}
	t.mu.Unlock()
	return snap.page(id, 0, size), nil
}
