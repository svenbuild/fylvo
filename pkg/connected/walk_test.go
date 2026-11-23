package connected

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/stretchr/testify/require"
)

// Everything below a folder: the walk the server makes itself (see walk.go).

func rowPaths(rows []*Row) string {
	paths := make([]string, 0, len(rows))
	for _, r := range rows {
		paths = append(paths, r.Path)
	}
	return strings.Join(paths, "|")
}

// walkAll reads every page of a walk.
func walkAll(t *testing.T, tree *Tree, c *Conn, path string, scan Scan, size int) (rows []*Row, last *Page) {
	t.Helper()
	token := ""
	for {
		page, err := tree.Walk(context.Background(), c, path, scan, Order{By: "name"}, size, token)
		require.NoError(t, err)
		require.True(t, page.Deep)
		rows = append(rows, page.Rows...)
		if last = page; page.Next == "" {
			return rows, last
		}
		token = page.Next
	}
}

func walkTree() (*memProvider, map[string]string) {
	p := newMem(false)
	ids := map[string]string{}
	ids["Docs"] = p.dir("root", "Docs")
	ids["Work"] = p.dir(ids["Docs"], "Work")
	ids["Deep"] = p.dir(ids["Work"], "Deep")
	ids["Empty"] = p.dir("root", "Empty")
	p.file("root", "readme.txt", 1)
	p.file(ids["Docs"], "Report.pdf", 5)
	p.file(ids["Docs"], "notes.txt", 2)
	p.file(ids["Work"], "report-draft.txt", 3)
	p.file(ids["Deep"], "leaf.txt", 4)
	return p, ids
}

func TestWalkListsEverythingBelow(t *testing.T) {
	p, ids := walkTree()
	tree, c := NewTree(), memConn(p)

	rows, last := walkAll(t, tree, c, "", Scan{}, 100)
	require.Equal(t, "Docs/Work/Deep|Docs|Empty|Docs/Work|Docs/Work/Deep/leaf.txt|Docs/notes.txt|readme.txt|Docs/Work/report-draft.txt|Docs/Report.pdf", rowPaths(rows))
	require.False(t, last.Incomplete)
	require.Zero(t, last.Links)
	require.Equal(t, 9, last.Total)
	require.Equal(t, "root", last.Parent.ID)
	for _, r := range rows {
		require.False(t, r.ViaLink, r.Path)
	}

	// Below a folder further down, with the paths as they are from the root.
	rows, _ = walkAll(t, tree, c, "Docs/Work", Scan{}, 100)
	require.Equal(t, "Docs/Work/Deep|Docs/Work/Deep/leaf.txt|Docs/Work/report-draft.txt", rowPaths(rows))
	// A folder with nothing in it, and what is no folder.
	rows, last = walkAll(t, tree, c, "Empty", Scan{}, 100)
	require.Empty(t, rows)
	require.False(t, last.Incomplete)
	_, err := tree.Walk(context.Background(), c, "readme.txt", Scan{}, Order{By: "name"}, 100, "")
	require.ErrorIs(t, err, ErrNotFound)
	_, err = tree.Walk(context.Background(), c, "Missing", Scan{}, Order{By: "name"}, 100, "")
	require.ErrorIs(t, err, ErrNotFound)

	// Every folder was asked of the provider once: the walk goes through the listings
	// a client would open one by one, and what is fresh is not read again.
	for name, id := range ids {
		require.Equal(t, 1, p.lists[id], name)
	}
	require.Equal(t, 1, p.lists["root"])
}

func TestWalkSearch(t *testing.T) {
	p, _ := walkTree()
	tree, c := NewTree(), memConn(p)
	found := func(scan Scan) string {
		rows, _ := walkAll(t, tree, c, "", scan, 100)
		return rowPaths(rows)
	}

	require.Equal(t, "Docs/Work/report-draft.txt", found(Scan{Names: []string{"report"}}))
	require.Equal(t, "Docs/Work/report-draft.txt|Docs/Report.pdf", found(Scan{Names: []string{"report"}, Fold: true}))
	require.Equal(t, "Docs/Work/report-draft.txt|Docs/Report.pdf", found(Scan{Names: []string{"REPORT"}, Fold: true}))
	// All the parts, or with Any one of them.
	require.Equal(t, "Docs/Work/report-draft.txt", found(Scan{Names: []string{"report", "draft"}}))
	require.Empty(t, found(Scan{Names: []string{"report", "leaf"}}))
	require.Equal(t, "Docs/Work/Deep/leaf.txt|Docs/Work/report-draft.txt", found(Scan{Names: []string{"report", "leaf"}, Any: true}))
	// By kind, alone and together with a name. A folder is found by its name too.
	require.Equal(t, "Docs/Work/Deep|Docs|Empty|Docs/Work", found(Scan{Kind: "folder"}))
	require.Equal(t, "Docs/Work/Deep/leaf.txt|Docs/notes.txt|readme.txt|Docs/Work/report-draft.txt|Docs/Report.pdf", found(Scan{Kind: "file"}))
	require.Equal(t, "Docs/Work/Deep", found(Scan{Names: []string{"ee"}, Kind: "folder"}))
	require.Equal(t, "Docs/Work/Deep|Docs", found(Scan{Names: []string{"D"}}))
	// What is not kept is still walked through: the leaf lies below folders that do
	// not match.
	require.Equal(t, "Docs/Work/Deep/leaf.txt", found(Scan{Names: []string{"leaf"}}))
	require.Empty(t, found(Scan{Names: []string{"nothing like it"}}))
}

// The pages of a walk are those of the walk that was started, for the same account,
// connection, folder, scan and order only, and no page of a plain listing.
func TestWalkTokens(t *testing.T) {
	p, ids := walkTree()
	tree, c, ctx := NewTree(), memConn(p), context.Background()
	scan, order := Scan{Kind: "file"}, Order{By: "name"}

	first, err := tree.Walk(ctx, c, "", scan, order, 2, "")
	require.NoError(t, err)
	require.NotEmpty(t, first.Next)
	require.Equal(t, 5, first.Total)
	// The provider changes: the walk that was started is not disturbed.
	p.file(ids["Docs"], "0-new.txt", 1)
	all := []string{rowPaths(first.Rows)}
	for page := first; page.Next != ""; {
		page, err = tree.Walk(ctx, c, "", scan, order, 2, page.Next)
		require.NoError(t, err)
		all = append(all, rowPaths(page.Rows))
	}
	require.Equal(t, "Docs/Work/Deep/leaf.txt|Docs/notes.txt|readme.txt|Docs/Work/report-draft.txt|Docs/Report.pdf", strings.Join(all, "|"))

	for name, wrong := range map[string]func() (*Page, error){
		"another scan":    func() (*Page, error) { return tree.Walk(ctx, c, "", Scan{}, order, 2, first.Next) },
		"another order":   func() (*Page, error) { return tree.Walk(ctx, c, "", scan, Order{By: "size"}, 2, first.Next) },
		"another folder":  func() (*Page, error) { return tree.Walk(ctx, c, "Docs", scan, order, 2, first.Next) },
		"a plain listing": func() (*Page, error) { return tree.List(ctx, c, "", order, 2, first.Next) },
		"another account": func() (*Page, error) {
			o := *c
			o.UserID = 2
			return tree.Walk(ctx, &o, "", scan, order, 2, first.Next)
		},
		"another connection": func() (*Page, error) {
			o := *c
			o.ID = "other"
			return tree.Walk(ctx, &o, "", scan, order, 2, first.Next)
		},
		"connected again": func() (*Page, error) {
			o := *c
			o.Generation = 2
			return tree.Walk(ctx, &o, "", scan, order, 2, first.Next)
		},
		"made up": func() (*Page, error) { return tree.Walk(ctx, c, "", scan, order, 2, "x.2") },
	} {
		_, err := wrong()
		require.ErrorIs(t, err, ErrListingGone, name)
	}
	// The other way round: the token of a plain listing continues no walk.
	plain, err := tree.List(ctx, c, "Docs", order, 1, "")
	require.NoError(t, err)
	require.NotEmpty(t, plain.Next)
	_, err = tree.Walk(ctx, c, "Docs", Scan{}, order, 1, plain.Next)
	require.ErrorIs(t, err, ErrListingGone)

	// A write to any folder the walk read ends it: its next page is gone.
	again, err := tree.Walk(ctx, c, "", scan, order, 2, "")
	require.NoError(t, err)
	tree.Changed(c, ids["Deep"])
	_, err = tree.Walk(ctx, c, "", scan, order, 2, again.Next)
	require.ErrorIs(t, err, ErrListingGone)
	// A write elsewhere does not.
	again, err = tree.Walk(ctx, c, "Docs/Work", Scan{}, order, 1, "")
	require.NoError(t, err)
	tree.Changed(c, ids["Empty"], "root")
	_, err = tree.Walk(ctx, c, "Docs/Work", Scan{}, order, 1, again.Next)
	require.NoError(t, err)
}

// Links are listed and counted, and never entered: they can lead back up.
func TestWalkStopsAtLinks(t *testing.T) {
	p := newMem(false)
	docs := p.dir("root", "Docs")
	p.file(docs, "a.txt", 1)
	// A shortcut that leads back to the root, one to a folder, one to a file, and a
	// folder in another drive.
	p.add(docs, provider.Item{Name: "up", IsDir: true, Link: &provider.Link{TargetID: "root"}})
	p.add("root", provider.Item{Name: "to docs", IsDir: true, Link: &provider.Link{TargetID: docs}})
	p.add("root", provider.Item{Name: "to a", Link: &provider.Link{TargetID: "x"}})
	p.add("root", provider.Item{Name: "elsewhere", IsDir: true, Link: &provider.Link{TargetID: "y", TargetDrive: "other"}})

	tree, c := NewTree(), memConn(p)
	rows, last := walkAll(t, tree, c, "", Scan{}, 100)
	require.Equal(t, "Docs|elsewhere|to docs|Docs/up|Docs/a.txt|to a", rowPaths(rows))
	require.Equal(t, 4, last.Links)
	require.False(t, last.Incomplete, "a link that is not entered does not make the walk incomplete")
	for _, r := range rows {
		require.Equal(t, r.Link != nil, r.ViaLink, r.Path)
	}
	require.Equal(t, 1, p.lists["root"])
	require.Equal(t, 1, p.lists[docs])

	// Started below a link, everything is "through a link", as in a listing there.
	rows, _ = walkAll(t, tree, c, "to docs", Scan{}, 100)
	require.Equal(t, "to docs/up|to docs/a.txt", rowPaths(rows))
	for _, r := range rows {
		require.True(t, r.ViaLink, r.Path)
	}
}

// What the walk could not see it says.
func TestWalkSaysWhenItIsIncomplete(t *testing.T) {
	ctx := context.Background()
	order := Order{By: "name"}

	t.Run("a name that stands for two entries", func(t *testing.T) {
		p := newMem(false)
		docs := p.dir("root", "Docs")
		p.dir(docs, "Twice")
		twice := p.dir(docs, "Twice")
		p.file(twice, "hidden.txt", 1)
		p.file(docs, "seen.txt", 1)
		// The walk goes on with everything else: folders beside it and below.
		later := p.dir("root", "Later")
		p.file(p.dir(later, "Below"), "leaf.txt", 1)
		p.file(p.dir(docs, "Sub"), "more.txt", 1)
		tree, c := NewTree(), memConn(p)
		rows, last := walkAll(t, tree, c, "", Scan{}, 100)
		require.Equal(t, "Later/Below|Docs|Later|Docs/Sub|Docs/Twice|Later/Below/leaf.txt|Docs/Sub/more.txt|Docs/seen.txt", rowPaths(rows))
		require.True(t, last.Incomplete)
		require.Zero(t, p.lists[twice], "what is behind the name is not entered")
	})

	t.Run("names that cannot be part of an address", func(t *testing.T) {
		p := newMem(false)
		docs := p.dir("root", "Docs")
		p.file(docs, "either/or.txt", 1)
		p.file(docs, "fine.txt", 1)
		p.file(p.dir(docs, "Sub"), "more.txt", 1)
		tree, c := NewTree(), memConn(p)
		rows, last := walkAll(t, tree, c, "", Scan{}, 100)
		require.Equal(t, "Docs|Docs/Sub|Docs/fine.txt|Docs/Sub/more.txt", rowPaths(rows))
		require.True(t, last.Incomplete)
		require.Equal(t, 1, last.Omitted)
	})

	t.Run("a folder below that is gone or too large", func(t *testing.T) {
		p := newMem(false)
		gone := p.dir("root", "Gone")
		p.file("root", "a.txt", 1)
		delete(p.children, gone)
		p.file(p.dir("root", "Zeta"), "z.txt", 1)
		tree, c := NewTree(), memConn(p)
		rows, last := walkAll(t, tree, c, "", Scan{}, 100)
		require.Equal(t, "Gone|Zeta|a.txt|Zeta/z.txt", rowPaths(rows))
		require.True(t, last.Incomplete)
	})

	t.Run("the bound of rows", func(t *testing.T) {
		p := newMem(false)
		for d := 0; d < 3; d++ {
			dir := p.dir("root", fmt.Sprintf("d%d", d))
			for i := 0; i < walkRows/2; i++ {
				p.add(dir, provider.Item{Name: fmt.Sprintf("f%05d", i), ID: fmt.Sprintf("d%d-%d", d, i), Version: "v"})
			}
		}
		tree, c := NewTree(), memConn(p)
		page, err := tree.Walk(ctx, c, "", Scan{Kind: "folder"}, order, 10, "")
		require.NoError(t, err)
		require.True(t, page.Incomplete)
		require.Equal(t, "d0|d1|d2", rowPaths(page.Rows))
		// Within the bound it is complete.
		page, err = tree.Walk(ctx, c, "d0", Scan{}, order, 10, "")
		require.NoError(t, err)
		require.False(t, page.Incomplete)
		require.Equal(t, walkRows/2, page.Total)
	})

	t.Run("the bound of time", func(t *testing.T) {
		p := newMem(false)
		slow := p.dir("root", "Slow")
		p.file(slow, "late.txt", 1)
		p.file("root", "a.txt", 1)
		tree, c := NewTree(), memConn(p)
		limit := walkTime
		walkTime = 50 * time.Millisecond
		defer func() { walkTime = limit }()
		stalled := &stalling{memProvider: p, at: slow}
		c.P = stalled
		page, err := tree.Walk(ctx, c, "", Scan{}, order, 10, "")
		require.NoError(t, err)
		require.True(t, page.Incomplete)
		require.Equal(t, "Slow|a.txt", rowPaths(page.Rows))
	})

	t.Run("only so many walks at once", func(t *testing.T) {
		p := newMem(false)
		for i := 0; i < 3; i++ {
			p.file("root", fmt.Sprintf("f%d.txt", i), 1)
		}
		tree, c := NewTree(), memConn(p)
		wait := listingWait
		listingWait = 20 * time.Millisecond
		defer func() { listingWait = wait }()
		for i := 0; i < parallelWalks; i++ {
			tree.walks <- struct{}{}
		}
		_, err := tree.Walk(ctx, c, "", Scan{}, order, 2, "")
		require.Equal(t, provider.KindBusy, provider.KindOf(err))
		// A place that is free again is taken and given back, also by a walk that fails.
		<-tree.walks
		first, err := tree.Walk(ctx, c, "", Scan{}, order, 2, "")
		require.NoError(t, err)
		_, err = tree.Walk(ctx, c, "Missing", Scan{}, order, 2, "")
		require.Error(t, err)
		require.Len(t, tree.walks, parallelWalks-1)
		// The pages of a walk that is done need no place.
		tree.walks <- struct{}{}
		second, err := tree.Walk(ctx, c, "", Scan{}, order, 2, first.Next)
		require.NoError(t, err)
		require.Len(t, second.Rows, 1)
	})

	t.Run("trouble that is not about one folder is no answer", func(t *testing.T) {
		p := newMem(false)
		busy := p.dir("root", "Busy")
		p.file("root", "a.txt", 1)
		tree, c := NewTree(), memConn(p)
		c.P = &failing{memProvider: p, at: busy, err: &provider.Error{Kind: provider.KindBusy}}
		_, err := tree.Walk(ctx, c, "", Scan{}, order, 10, "")
		require.Equal(t, provider.KindBusy, provider.KindOf(err))
		c.P = &failing{memProvider: p, at: busy, err: &provider.Error{Kind: provider.KindReconnect}}
		_, err = tree.Walk(ctx, c, "", Scan{}, order, 10, "")
		require.Equal(t, provider.KindReconnect, provider.KindOf(err))
		// The folder that was asked for itself must be readable.
		c.P = &failing{memProvider: p, at: "root", err: &provider.Error{Kind: provider.KindIncomplete}}
		tree = NewTree()
		_, err = tree.Walk(ctx, c, "", Scan{}, order, 10, "")
		require.Equal(t, provider.KindIncomplete, provider.KindOf(err))
		// Who gave up waiting gets nothing kept.
		gone, cancel := context.WithCancel(ctx)
		cancel()
		_, err = NewTree().Walk(gone, memConn(p), "", Scan{}, order, 10, "")
		require.True(t, errors.Is(err, context.Canceled))
	})
}

// stalling does not answer for one folder until the caller gives up.
type stalling struct {
	*memProvider
	at string
}

func (s *stalling) Children(ctx context.Context, folderID, page string) (*provider.Page, error) {
	if folderID == s.at {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return s.memProvider.Children(ctx, folderID, page)
}

// failing answers one folder with an error.
type failing struct {
	*memProvider
	at  string
	err error
}

func (f *failing) Children(ctx context.Context, folderID, page string) (*provider.Page, error) {
	if folderID == f.at {
		return nil, f.err
	}
	return f.memProvider.Children(ctx, folderID, page)
}

// Two scans are the same scan only when they keep the same rows.
func TestScanKey(t *testing.T) {
	base := Scan{Names: []string{"a", "b"}, Fold: true, Kind: "file"}
	require.Equal(t, base.key(), Scan{Names: []string{"a", "b"}, Fold: true, Kind: "file"}.key())
	for name, other := range map[string]Scan{
		"names":      {Names: []string{"a"}, Fold: true, Kind: "file"},
		"split":      {Names: []string{"ab"}, Fold: true, Kind: "file"},
		"fold":       {Names: []string{"a", "b"}, Kind: "file"},
		"any":        {Names: []string{"a", "b"}, Fold: true, Any: true, Kind: "file"},
		"kind":       {Names: []string{"a", "b"}, Fold: true, Kind: "folder"},
		"everything": {},
	} {
		require.NotEqual(t, base.key(), other.key(), name)
	}
}
