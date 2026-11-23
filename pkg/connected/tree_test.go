package connected

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
)

// memProvider is a provider in memory. Like Google Drive it can hold several entries
// with one name in a folder, unless fold is set, which makes it behave like OneDrive.
type memProvider struct {
	provider.Provider
	mu       sync.Mutex
	children map[string][]provider.Item
	fold     bool
	pageSize int
	lists    map[string]int
	named    int
	nextID   int
}

func newMem(fold bool) *memProvider {
	return &memProvider{children: map[string][]provider.Item{"root": nil}, fold: fold, lists: map[string]int{}}
}

// add puts an entry into the folder with the given id and returns it.
func (p *memProvider) add(folder string, it provider.Item) provider.Item {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.nextID++
	if it.ID == "" {
		it.ID = fmt.Sprintf("item%d", p.nextID)
	}
	if it.ModTime.IsZero() {
		it.ModTime = time.Unix(1700000000+int64(p.nextID), 0)
	}
	if !it.IsDir && it.Native == "" && it.Link == nil && it.Version == "" {
		it.Version = fmt.Sprintf("v%d", p.nextID)
	}
	p.children[folder] = append(p.children[folder], it)
	if it.IsDir && it.Link == nil {
		if _, ok := p.children[it.ID]; !ok {
			p.children[it.ID] = nil
		}
	}
	return it
}

func (p *memProvider) dir(folder, name string) string {
	return p.add(folder, provider.Item{Name: name, IsDir: true}).ID
}

func (p *memProvider) file(folder, name string, size int64) provider.Item {
	return p.add(folder, provider.Item{Name: name, Size: size})
}

func (p *memProvider) Root(ctx context.Context) (string, error) { return "root", nil }
func (p *memProvider) CaseInsensitive() bool                    { return p.fold }

func (p *memProvider) Children(ctx context.Context, folderID, page string) (*provider.Page, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lists[folderID]++
	items, ok := p.children[folderID]
	if !ok {
		return nil, &provider.Error{Kind: provider.KindNotFound}
	}
	if p.pageSize == 0 {
		return &provider.Page{Items: append([]provider.Item(nil), items...)}, nil
	}
	start := 0
	fmt.Sscanf(page, "%d", &start)
	end := min(start+p.pageSize, len(items))
	res := &provider.Page{Items: append([]provider.Item(nil), items[start:end]...)}
	if end < len(items) {
		res.Next = fmt.Sprint(end)
	}
	return res, nil
}

func (p *memProvider) Named(ctx context.Context, folderID, name string) ([]provider.Item, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.named++
	var res []provider.Item
	for _, it := range p.children[folderID] {
		if it.Name == name || (p.fold && strings.EqualFold(it.Name, name)) {
			res = append(res, it)
		}
	}
	return res, nil
}

func memConn(p provider.Provider) *Conn {
	return &Conn{ID: "abc", UserID: 1, Provider: ProviderDrive, Generation: 1, P: p}
}

func rowNames(rows []*Row) string {
	names := make([]string, 0, len(rows))
	for _, r := range rows {
		names = append(names, r.Name)
	}
	return strings.Join(names, "|")
}

func TestListSortAndPages(t *testing.T) {
	p := newMem(false)
	p.pageSize = 2 // the provider's own pages are joined
	p.file("root", "b.txt", 30)
	p.dir("root", "Zeta")
	p.file("root", "a.txt", 10)
	p.dir("root", "alpha")
	p.file("root", "C.txt", 20)
	tree, c, ctx := NewTree(), memConn(p), context.Background()

	page, err := tree.List(ctx, c, "", Order{By: "name"}, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := rowNames(page.Rows); got != "alpha|Zeta|a.txt|b.txt|C.txt" || page.Next != "" || page.Total != 5 {
		t.Fatalf("by name: %s next %q total %d", got, page.Next, page.Total)
	}
	if page.Parent == nil || page.Parent.ID != "root" {
		t.Fatalf("parent: %+v", page.Parent)
	}
	page, _ = tree.List(ctx, c, "", Order{By: "size", Desc: true}, 100, "")
	if got := rowNames(page.Rows); got != "alpha|Zeta|b.txt|C.txt|a.txt" {
		t.Fatalf("by size descending: %s", got)
	}

	// Pages of two, and the provider changes in between: the pages stay those of the
	// listing that was started.
	page, err = tree.List(ctx, c, "", Order{By: "name"}, 2, "")
	if err != nil || page.Next == "" {
		t.Fatalf("first page: %+v, %v", page, err)
	}
	all := []string{rowNames(page.Rows)}
	p.file("root", "0-new.txt", 1)
	for page.Next != "" {
		if page, err = tree.List(ctx, c, "", Order{By: "name"}, 2, page.Next); err != nil {
			t.Fatal(err)
		}
		all = append(all, rowNames(page.Rows))
	}
	if got := strings.Join(all, "|"); got != "alpha|Zeta|a.txt|b.txt|C.txt" {
		t.Fatalf("paged: %s", got)
	}
}

func TestListTokens(t *testing.T) {
	p := newMem(false)
	for i := 0; i < 5; i++ {
		p.file("root", fmt.Sprintf("f%d", i), 1)
	}
	sub := p.dir("root", "sub")
	for i := 0; i < 5; i++ {
		p.file(sub, fmt.Sprintf("s%d", i), 1)
	}
	tree, c, ctx := NewTree(), memConn(p), context.Background()
	now := time.Unix(1800000000, 0)
	tree.now = func() time.Time { return now }

	first, err := tree.List(ctx, c, "", Order{}, 2, "")
	if err != nil || first.Next == "" {
		t.Fatalf("first page: %+v, %v", first, err)
	}
	id := strings.Split(first.Next, ".")[0]

	gone := func(what string, c *Conn, path string, o Order, token string) {
		t.Helper()
		if _, err := tree.List(ctx, c, path, o, 2, token); !errors.Is(err, ErrListingGone) {
			t.Errorf("%s: got %v, want ErrListingGone", what, err)
		}
	}
	gone("made-up token", c, "", Order{}, "zzz")
	gone("unknown listing", c, "", Order{}, strings.Repeat("0", 24)+".2")
	gone("another folder", c, "sub", Order{}, first.Next)
	gone("another order", c, "", Order{By: "size"}, first.Next)
	gone("another connection", &Conn{ID: "other", UserID: 1, Generation: 1, P: p}, "", Order{}, first.Next)
	gone("another account", &Conn{ID: "abc", UserID: 2, Generation: 1, P: p}, "", Order{}, first.Next)
	gone("after a reconnect", &Conn{ID: "abc", UserID: 1, Generation: 2, P: p}, "", Order{}, first.Next)
	gone("negative offset", c, "", Order{}, id+".-1")
	gone("offset past the end", c, "", Order{}, id+".99")

	if _, err := tree.List(ctx, c, "", Order{}, 2, first.Next); err != nil {
		t.Fatalf("own token: %v", err)
	}

	// A write in the folder ends its paged listings.
	tree.Changed(c, "root")
	gone("after a write", c, "", Order{}, first.Next)

	// Unused listings are dropped after a while.
	first, _ = tree.List(ctx, c, "", Order{}, 2, "")
	now = now.Add(snapshotIdle + time.Second)
	gone("after the idle time", c, "", Order{}, first.Next)

	// Everything of a connection goes when it ends.
	first, _ = tree.List(ctx, c, "", Order{}, 2, "")
	tree.Forget(c.ID)
	gone("after the connection ended", c, "", Order{}, first.Next)
	if len(tree.listings) != 0 || len(tree.snapshots) != 0 {
		t.Fatal("something is still kept for the connection")
	}
}

func TestBounds(t *testing.T) {
	p := newMem(false)
	ids := make([]string, 4)
	for d := range ids {
		ids[d] = p.dir("root", fmt.Sprintf("d%d", d))
		items := make([]provider.Item, 0, 20000)
		for i := 0; i < 20000; i++ {
			items = append(items, provider.Item{ID: fmt.Sprintf("%d-%d", d, i), Name: fmt.Sprintf("f%05d", i), Size: 1})
		}
		p.children[ids[d]] = items
	}
	huge := p.dir("root", "huge")
	p.pageSize = 10000
	items := make([]provider.Item, 0, maxRows+1)
	for i := 0; i <= maxRows; i++ {
		items = append(items, provider.Item{ID: fmt.Sprintf("h%d", i), Name: fmt.Sprintf("h%06d", i)})
	}
	p.children[huge] = items

	tree, c, ctx := NewTree(), memConn(p), context.Background()
	now := time.Unix(1800000000, 0)
	tree.now = func() time.Time { return now }

	// A folder above the limit is refused, and reading it stops early.
	if _, err := tree.List(ctx, c, "huge", Order{}, 100, ""); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("huge folder: %v", err)
	}
	if p.lists[huge] > maxRows/p.pageSize+1 {
		t.Fatalf("the huge folder was read to the end: %d pages", p.lists[huge])
	}

	// Four folders of 20,000 entries, listed within a second: what is kept of them
	// never goes over the bound, fresh listings and paged ones counted together.
	kept := func() int {
		total := 0
		for _, l := range tree.listings {
			total += len(l.entries)
		}
		for _, s := range tree.snapshots {
			total += len(s.rows)
		}
		return total
	}
	var tokens []string
	for d := range ids {
		page, err := tree.List(ctx, c, fmt.Sprintf("d%d", d), Order{}, 100, "")
		if err != nil || page.Next == "" {
			t.Fatalf("listing %d: %v", d, err)
		}
		tokens = append(tokens, page.Next)
		if total := kept(); total > maxRows {
			t.Fatalf("after listing %d: %d rows are kept, the bound is %d", d, total, maxRows)
		}
		now = now.Add(time.Millisecond)
	}
	if _, err := tree.List(ctx, c, "d0", Order{}, 100, tokens[0]); !errors.Is(err, ErrListingGone) {
		t.Fatalf("the oldest listing is still there: %v", err)
	}
	if _, err := tree.List(ctx, c, "d3", Order{}, 100, tokens[3]); err != nil {
		t.Fatalf("the newest listing is gone: %v", err)
	}
}

// What a path stands for must not depend on whether its folder happens to be in memory.
func resolveBothWays(t *testing.T, p *memProvider, path string) (warm, cold *Row, warmErr, coldErr error) {
	t.Helper()
	ctx := context.Background()
	coldTree := NewTree()
	cold, coldErr = coldTree.Resolve(ctx, memConn(p), path)

	warmTree, c := NewTree(), memConn(p)
	// List every folder on the way, so the walk finds them in memory.
	dir := ""
	for _, name := range strings.Split(path, "/") {
		if _, err := warmTree.List(ctx, c, dir, Order{}, 100, ""); err != nil {
			break
		}
		dir = joinPath(dir, name)
	}
	before := p.named
	warm, warmErr = warmTree.Resolve(ctx, c, path)
	if warmErr == nil && p.named != before {
		t.Errorf("%q: the provider was asked although the folders were listed", path)
	}
	return
}

func TestDuplicatesAndOddNames(t *testing.T) {
	p := newMem(false)
	p.file("root", "Report.docx", 5)
	p.file("root", "Report.docx", 7)
	twin := p.dir("root", "Twin")
	p.dir("root", "Twin")
	p.file("root", "ok.txt", 1)
	plain := p.dir("root", "Plain")
	inside := p.file(plain, "inside.txt", 1)
	p.file(twin, "hidden.txt", 1)
	p.file("root", "Foo", 1)
	p.file("root", "foo", 2)
	p.file("root", "a/b.txt", 1)
	p.file("root", "back\\slash", 1)
	p.file("root", "..", 1)
	// The same item reported twice, as can happen while a folder changes during paging.
	again := p.file("root", "once.txt", 1)
	p.children["root"] = append(p.children["root"], again)

	tree, c, ctx := NewTree(), memConn(p), context.Background()
	page, err := tree.List(ctx, c, "", Order{By: "name"}, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := rowNames(page.Rows); got != "Plain|Twin|Foo|foo|ok.txt|once.txt|Report.docx" {
		t.Fatalf("rows: %s", got)
	}
	if page.Omitted != 3 {
		t.Fatalf("omitted: %d", page.Omitted)
	}
	for _, r := range page.Rows {
		dup := r.Name == "Report.docx" || r.Name == "Twin"
		if dup != (r.Unsupported == UnsupportedDuplicate) {
			t.Errorf("%s: unsupported = %q", r.Name, r.Unsupported)
		}
		if dup && (r.ID != "" || r.Size != 0 || r.Version != "" || r.Enterable() || !r.ReadOnly()) {
			t.Errorf("%s: a duplicate row carries data or rights of one of the entries: %+v", r.Name, r)
		}
	}

	for _, path := range []string{"Report.docx", "Twin", "Twin/hidden.txt"} {
		_, _, warmErr, coldErr := resolveBothWays(t, p, path)
		if !errors.Is(warmErr, ErrAmbiguous) || !errors.Is(coldErr, ErrAmbiguous) {
			t.Errorf("Resolve(%q): %v / %v, want ErrAmbiguous both ways", path, warmErr, coldErr)
		}
	}
	for _, path := range []string{"nope", "ok.txt/below", "Plain/nope", "a/b.txt", "b.txt", "FOO"} {
		_, _, warmErr, coldErr := resolveBothWays(t, p, path)
		if !errors.Is(warmErr, ErrNotFound) || !errors.Is(coldErr, ErrNotFound) {
			t.Errorf("Resolve(%q): %v / %v, want ErrNotFound both ways", path, warmErr, coldErr)
		}
	}
	for path, wantSize := range map[string]int64{"Foo": 1, "foo": 2, "Plain/inside.txt": 1, "once.txt": 1} {
		warm, cold, warmErr, coldErr := resolveBothWays(t, p, path)
		if warmErr != nil || coldErr != nil || warm.ID != cold.ID || warm.Size != wantSize || warm.Path != path || cold.Path != path {
			t.Errorf("Resolve(%q): %+v (%v) / %+v (%v)", path, warm, warmErr, cold, coldErr)
		}
	}
	row, _ := tree.Resolve(ctx, c, "Plain/inside.txt")
	if row.ID != inside.ID || row.ReadOnly() {
		t.Fatalf("plain file: %+v", row)
	}
	root, err := tree.Resolve(ctx, c, "")
	if err != nil || !root.Enterable() || root.ID != "root" {
		t.Fatalf("root: %+v, %v", root, err)
	}
}

func TestCaseInsensitiveProvider(t *testing.T) {
	p := newMem(true)
	docs := p.dir("root", "Docs")
	file := p.file(docs, "Report.TXT", 3)

	for _, path := range []string{"Docs/Report.TXT", "docs/report.txt", "DOCS/REPORT.txt"} {
		warm, cold, warmErr, coldErr := resolveBothWays(t, p, path)
		if warmErr != nil || coldErr != nil || warm.ID != file.ID || cold.ID != file.ID {
			t.Fatalf("Resolve(%q): %+v (%v) / %+v (%v)", path, warm, warmErr, cold, coldErr)
		}
		// The path of the row is spelled as the provider spells it.
		if warm.Path != "Docs/Report.TXT" || cold.Path != "Docs/Report.TXT" {
			t.Fatalf("Resolve(%q): paths %q / %q", path, warm.Path, cold.Path)
		}
	}
}

// A document that exists only at the provider shows with the extension of its kind and
// never takes the place of a real file, whichever way the path is resolved.
func TestNativeDocuments(t *testing.T) {
	p := newMem(false)
	doc := p.add("root", provider.Item{Name: "Budget", Native: "gsheet"})
	real := p.file("root", "Budget", 9)
	xlsx := p.file("root", "Budget.xlsx", 9)
	clash := p.add("root", provider.Item{Name: "Plan", Native: "gdoc"})
	p.file("root", "Plan.gdoc", 4)
	p.add("root", provider.Item{Name: "Notes", Native: "gdoc"})
	p.add("root", provider.Item{Name: "Notes.gdoc", Native: "gdoc"})
	_ = clash

	tree, c, ctx := NewTree(), memConn(p), context.Background()
	page, err := tree.List(ctx, c, "", Order{By: "name"}, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := rowNames(page.Rows); got != "Budget|Budget.gsheet|Budget.xlsx|Notes.gdoc|Notes.gdoc.gdoc|Plan.gdoc" {
		t.Fatalf("rows: %s", got)
	}

	want := map[string]string{"Budget.gsheet": doc.ID, "Budget": real.ID, "Budget.xlsx": xlsx.ID}
	for path, id := range want {
		warm, cold, warmErr, coldErr := resolveBothWays(t, p, path)
		if warmErr != nil || coldErr != nil || warm.ID != id || cold.ID != id {
			t.Errorf("Resolve(%q): %+v (%v) / %+v (%v), want %s", path, warm, warmErr, cold, coldErr, id)
		}
	}
	row, _ := tree.Resolve(ctx, c, "Budget.gsheet")
	if row.Native != "gsheet" || !row.ReadOnly() || row.Name != "Budget.gsheet" {
		t.Fatalf("native row: %+v", row)
	}

	// A native "Plan" and a real "Plan.gdoc" show under one name: neither is usable.
	_, _, warmErr, coldErr := resolveBothWays(t, p, "Plan.gdoc")
	if !errors.Is(warmErr, ErrAmbiguous) || !errors.Is(coldErr, ErrAmbiguous) {
		t.Fatalf("clash: %v / %v", warmErr, coldErr)
	}
	// "Notes.gdoc" is the native "Notes"; the native "Notes.gdoc" shows one extension further.
	for path := range map[string]bool{"Notes.gdoc": true, "Notes.gdoc.gdoc": true} {
		warm, cold, warmErr, coldErr := resolveBothWays(t, p, path)
		if warmErr != nil || coldErr != nil || warm.ID != cold.ID {
			t.Errorf("Resolve(%q): %v / %v", path, warmErr, coldErr)
		}
	}
}

func TestLinksAreReadOnlyAllTheWayDown(t *testing.T) {
	p := newMem(false)
	shared := p.dir("root", "Target")
	deep := p.dir(shared, "Deep")
	leaf := p.file(deep, "leaf.txt", 1)
	p.add("root", provider.Item{Name: "Shortcut", IsDir: true, Link: &provider.Link{TargetID: shared}})
	p.add("root", provider.Item{Name: "Elsewhere", IsDir: true, Link: &provider.Link{TargetID: "x", TargetDrive: "other"}})
	p.add("root", provider.Item{Name: "file-link", Link: &provider.Link{TargetID: leaf.ID}})

	tree, c, ctx := NewTree(), memConn(p), context.Background()

	direct, err := tree.Resolve(ctx, c, "Target/Deep/leaf.txt")
	if err != nil || direct.ReadOnly() {
		t.Fatalf("direct: %+v, %v", direct, err)
	}
	for _, path := range []string{"Shortcut", "Shortcut/Deep", "Shortcut/Deep/leaf.txt", "file-link"} {
		warm, cold, warmErr, coldErr := resolveBothWays(t, p, path)
		if warmErr != nil || coldErr != nil || !warm.ReadOnly() || !cold.ReadOnly() {
			t.Errorf("Resolve(%q): read-only %v / %v (%v, %v)", path, warm, cold, warmErr, coldErr)
		}
	}
	via, _ := tree.Resolve(ctx, c, "Shortcut/Deep/leaf.txt")
	if via.ID != leaf.ID || via.Path != "Shortcut/Deep/leaf.txt" {
		t.Fatalf("through the link: %+v", via)
	}
	link, _ := tree.Resolve(ctx, c, "file-link")
	if link.ContentID() != leaf.ID || link.ID == leaf.ID {
		t.Fatalf("a link keeps its own id and names its target: %+v", link)
	}

	// A link into another drive is shown but not entered.
	other, err := tree.Resolve(ctx, c, "Elsewhere")
	if err != nil || other.Enterable() {
		t.Fatalf("link into another drive: %+v, %v", other, err)
	}
	if _, err := tree.Resolve(ctx, c, "Elsewhere/x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("below a link into another drive: %v", err)
	}
	if _, err := tree.List(ctx, c, "Elsewhere", Order{}, 10, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("listing a link into another drive: %v", err)
	}

	page, err := tree.List(ctx, c, "Shortcut", Order{}, 10, "")
	if err != nil || len(page.Rows) != 1 || !page.Rows[0].ViaLink || !page.Parent.ViaLink {
		t.Fatalf("listing through a link: %+v, %v", page, err)
	}
}

func TestFreshListingIsReused(t *testing.T) {
	p := newMem(false)
	docs := p.dir("root", "Docs")
	p.file(docs, "a.txt", 3)
	tree, c, ctx := NewTree(), memConn(p), context.Background()
	now := time.Unix(1800000000, 0)
	tree.now = func() time.Time { return now }

	for i := 0; i < 3; i++ {
		if _, err := tree.List(ctx, c, "Docs", Order{}, 10, ""); err != nil {
			t.Fatal(err)
		}
	}
	if p.lists[docs] != 1 {
		t.Fatalf("the folder was read %d times within seconds", p.lists[docs])
	}

	// After a write there, and after a while, the provider is asked again.
	p.file(docs, "new.txt", 1)
	tree.Changed(c, docs)
	page, _ := tree.List(ctx, c, "Docs", Order{By: "name"}, 10, "")
	if rowNames(page.Rows) != "a.txt|new.txt" {
		t.Fatalf("after a write: %s", rowNames(page.Rows))
	}
	now = now.Add(listingFresh + time.Second)
	if _, err := tree.List(ctx, c, "Docs", Order{}, 10, ""); err != nil || p.lists[docs] != 3 {
		t.Fatalf("after a while: %v, read %d times", err, p.lists[docs])
	}

	if _, err := tree.List(ctx, c, "Docs/a.txt", Order{}, 10, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("listing a file: %v", err)
	}
	if _, err := tree.List(ctx, c, "Gone", Order{}, 10, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("listing a missing folder: %v", err)
	}
}

// cyclingProvider answers a folder with pages that lead to each other for ever.
type cyclingProvider struct {
	*memProvider
	pages int
}

func (p *cyclingProvider) Children(ctx context.Context, folderID, page string) (*provider.Page, error) {
	p.pages++
	if page == "a" {
		return &provider.Page{Next: "b"}, nil
	}
	return &provider.Page{Next: "a"}, nil
}

func TestListingOfABrokenProviderEnds(t *testing.T) {
	p := &cyclingProvider{memProvider: newMem(false)}
	tree, ctx := NewTree(), context.Background()
	_, err := tree.List(ctx, memConn(p), "", Order{}, 100, "")
	if provider.KindOf(err) != provider.KindIncomplete {
		t.Fatalf("pages in a circle: %v", err)
	}
	if p.pages > maxPages+2 {
		t.Fatalf("%d pages were read", p.pages)
	}
}

// An entry that was renamed while the provider sent its pages shows up twice. It is one row.
func TestRenamedWhilePagingCountsOnce(t *testing.T) {
	p := newMem(false)
	p.pageSize = 2
	p.add("root", provider.Item{ID: "x", Name: "before.txt"})
	p.file("root", "other.txt", 1)
	p.add("root", provider.Item{ID: "x", Name: "after.txt"})
	tree, ctx := NewTree(), context.Background()
	page, err := tree.List(ctx, memConn(p), "", Order{}, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := rowNames(page.Rows); got != "before.txt|other.txt" {
		t.Fatalf("rows %q", got)
	}
}

// Every page of a listing names the folder that is listed, with its id.
func TestEveryPageNamesTheFolder(t *testing.T) {
	p := newMem(false)
	docs := p.dir("root", "Docs")
	for i := 0; i < 5; i++ {
		p.file(docs, fmt.Sprintf("f%d", i), 1)
	}
	tree, c, ctx := NewTree(), memConn(p), context.Background()
	first, err := tree.List(ctx, c, "Docs", Order{}, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := tree.List(ctx, c, "Docs", Order{}, 2, first.Next)
	if err != nil {
		t.Fatal(err)
	}
	if second.Parent == nil || second.Parent.ID != docs || second.Parent.Name != "Docs" {
		t.Fatalf("parent of the second page: %+v", second.Parent)
	}
}

// When all places for reading folders stay taken, a further listing gives up as busy.
func TestListingWaitsOnlySoLong(t *testing.T) {
	wait := listingWait
	listingWait = 20 * time.Millisecond
	defer func() { listingWait = wait }()

	tree := NewTree()
	for i := 0; i < parallelListings; i++ {
		tree.sem <- struct{}{}
	}
	_, err := tree.List(context.Background(), memConn(newMem(false)), "", Order{}, 100, "")
	if provider.KindOf(err) != provider.KindBusy {
		t.Fatalf("got %v", err)
	}
}

// The limit is on entries, not on how often the provider names them.
func TestLimitCountsEntriesOnce(t *testing.T) {
	p := newMem(false)
	p.pageSize = 10000
	items := make([]provider.Item, 0, maxRows+1)
	for i := 0; i < maxRows; i++ {
		items = append(items, provider.Item{ID: fmt.Sprintf("i%d", i), Name: fmt.Sprintf("f%06d", i)})
	}
	items = append(items, provider.Item{ID: "i0", Name: "renamed"})
	p.children["root"] = items

	page, err := NewTree().List(context.Background(), memConn(p), "", Order{}, 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != maxRows {
		t.Fatalf("%d rows", page.Total)
	}
}

// Rows of a page carry their paths; the rows kept for the listing are not touched.
func TestPagesBuildTheirPaths(t *testing.T) {
	p := newMem(false)
	docs := p.dir("root", "Docs")
	sub := p.dir(docs, "Sub")
	for i := 0; i < 3; i++ {
		p.file(sub, fmt.Sprintf("f%d", i), 1)
	}
	tree, c, ctx := NewTree(), memConn(p), context.Background()
	first, err := tree.List(ctx, c, "Docs/Sub", Order{}, 2, "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := tree.List(ctx, c, "Docs/Sub", Order{}, 2, first.Next)
	if err != nil {
		t.Fatal(err)
	}
	if first.Rows[0].Path != "Docs/Sub/f0" || second.Rows[0].Path != "Docs/Sub/f2" {
		t.Fatalf("paths %q %q", first.Rows[0].Path, second.Rows[0].Path)
	}
	for _, l := range tree.listings {
		for _, e := range l.entries {
			if e.Path != "" {
				t.Fatalf("a kept row was given the path %q", e.Path)
			}
		}
	}
}
