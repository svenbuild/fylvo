package drive

import (
	"context"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive/drivetest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setup(t *testing.T) (*drivetest.Server, provider.Provider, *drivetest.Auth) {
	s := drivetest.New(t)
	auth := &drivetest.Auth{}
	return s, New(Options{HTTP: s.Client(), Auth: auth, BaseURL: s.URL()}), auth
}

func names(items []provider.Item) []string {
	res := make([]string, len(items))
	for i, item := range items {
		res[i] = item.Name
	}
	return res
}

func read(t *testing.T, c *provider.Content) string {
	t.Helper()
	defer c.Body.Close()
	b, err := io.ReadAll(c.Body)
	require.NoError(t, err)
	return string(b)
}

func TestRootIsCached(t *testing.T) {
	s, d, _ := setup(t)
	ctx := context.Background()
	for range 2 {
		root, err := d.Root(ctx)
		require.NoError(t, err)
		assert.Equal(t, s.RootID(), root)
	}
	assert.Equal(t, 1, s.Calls(drivetest.CallGet))
	assert.False(t, d.CaseInsensitive())
}

func TestChildrenPages(t *testing.T) {
	s, d, _ := setup(t)
	ctx := context.Background()
	folder := s.AddFolder("", "Photos")
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		s.AddFile(folder, name, []byte(name))
	}
	s.SetPageSize(2)

	var got []string
	page, err := d.Children(ctx, folder, "")
	require.NoError(t, err)
	got = append(got, names(page.Items)...)

	// Drive may answer with an empty page that still leads on.
	s.EmptyNext()
	page, err = d.Children(ctx, folder, page.Next)
	require.NoError(t, err)
	assert.Empty(t, page.Items)
	require.NotEmpty(t, page.Next)

	for page.Next != "" {
		page, err = d.Children(ctx, folder, page.Next)
		require.NoError(t, err)
		got = append(got, names(page.Items)...)
	}
	assert.Equal(t, []string{"a", "b", "c", "d", "e"}, got)
	assert.Equal(t, 4, s.Calls(drivetest.CallList))
}

func TestChildrenLeavesOutTrashedAndUnsupported(t *testing.T) {
	s, d, _ := setup(t)
	gone := s.AddFile("", "gone.txt", []byte("x"))
	s.Trash(gone)
	s.AddOther("", "Survey", "application/vnd.google-apps.form")
	s.AddOther("", "Homepage", "application/vnd.google-apps.site")
	doc := s.AddNative("", "Notes", "gdoc")
	s.AddShortcut("", "Notes link", doc)
	s.AddFile("", "same", []byte("one"))
	s.AddFile("", "same", []byte("two"))

	page, err := d.Children(context.Background(), s.RootID(), "")
	require.NoError(t, err)
	assert.Equal(t, []string{"Notes", "same", "same"}, names(page.Items))
	assert.NotEqual(t, page.Items[1].ID, page.Items[2].ID)
	assert.Empty(t, page.Next)
}

func TestMapping(t *testing.T) {
	s, d, _ := setup(t)
	ctx := context.Background()
	folder := s.AddFolder("", "Work")
	fileID := s.AddFile(folder, "report.pdf", []byte("0123456789"))
	sheet := s.AddNative("", "Budget", "gsheet")
	toFile := s.AddShortcut("", "report link", fileID)
	toFolder := s.AddShortcut("", "Work link", folder)

	f, err := d.Get(ctx, fileID)
	require.NoError(t, err)
	assert.Equal(t, "report.pdf", f.Name)
	assert.False(t, f.IsDir)
	assert.Equal(t, int64(10), f.Size)
	assert.Equal(t, "781e5e245d69b566979b86e28d23f2c7", f.Hash)
	assert.NotEmpty(t, f.Revision)
	assert.Equal(t, "r:"+f.Revision, f.Version)
	assert.False(t, f.ModTime.IsZero())
	assert.Contains(t, f.WebURL, fileID)
	assert.Nil(t, f.Link)

	dir, err := d.Get(ctx, folder)
	require.NoError(t, err)
	assert.True(t, dir.IsDir)
	assert.Empty(t, dir.Version)

	doc, err := d.Get(ctx, sheet)
	require.NoError(t, err)
	assert.Equal(t, "gsheet", doc.Native)
	assert.Zero(t, doc.Size)
	assert.Equal(t, "t:"+doc.ModTime.Format("2006-01-02T15:04:05.000Z"), doc.Version)

	s.SetResourceKey(fileID, "key-1")
	link, err := d.Get(ctx, toFile)
	require.NoError(t, err)
	assert.Equal(t, &provider.Link{TargetID: fileID, ResourceKey: "key-1"}, link.Link)
	assert.False(t, link.IsDir)
	assert.Zero(t, link.Size)
	assert.Empty(t, link.Version)
	assert.Equal(t, fileID, link.ContentID())

	link, err = d.Get(ctx, toFolder)
	require.NoError(t, err)
	assert.True(t, link.IsDir)
	assert.Equal(t, folder, link.Link.TargetID)

	// A new revision changes the version.
	s.SetContent(fileID, []byte("new"))
	again, err := d.Get(ctx, fileID)
	require.NoError(t, err)
	assert.NotEqual(t, f.Version, again.Version)
	assert.Equal(t, int64(3), again.Size)
}

func TestVersionWithoutRevision(t *testing.T) {
	withSum := file{ID: "x", MimeType: "text/plain", Size: 3, MD5Checksum: "abc", ModifiedTime: "2026-01-01T00:00:00.000Z"}
	item, ok := withSum.item()
	require.True(t, ok)
	assert.Equal(t, "h:abc:3", item.Version)

	bare := file{ID: "x", MimeType: "text/plain", ModifiedTime: "2026-01-01T00:00:00.000Z"}
	item, ok = bare.item()
	require.True(t, ok)
	assert.Equal(t, "t:2026-01-01T00:00:00.000Z", item.Version)
}

func TestGetTrashedAndUnsupported(t *testing.T) {
	s, d, _ := setup(t)
	ctx := context.Background()
	id := s.AddFile("", "old.txt", []byte("x"))
	s.Trash(id)
	_, err := d.Get(ctx, id)
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))

	form := s.AddOther("", "Survey", "application/vnd.google-apps.form")
	_, err = d.Get(ctx, form)
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))

	_, err = d.Get(ctx, "missing")
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))
}

func TestNamed(t *testing.T) {
	s, d, _ := setup(t)
	ctx := context.Background()
	folder := s.AddFolder("", "it's")
	tricky := []string{`a/b`, `Alex's file`, `back\slash`, `both\'`, `and name = 'x'`}
	for _, name := range tricky {
		s.AddFile(folder, name, []byte(name))
	}
	s.AddFile(folder, "Foo", []byte("upper"))
	s.AddFile(folder, "foo", []byte("lower"))
	s.AddFile(folder, "foo", []byte("lower again"))
	s.AddFile(folder, "x", nil)

	for _, name := range tricky {
		got, err := d.Named(ctx, folder, name)
		require.NoError(t, err, name)
		assert.Equal(t, []string{name}, names(got), name)
	}

	got, err := d.Named(ctx, folder, "foo")
	require.NoError(t, err)
	assert.Equal(t, []string{"foo", "foo"}, names(got))
	got, err = d.Named(ctx, folder, "Foo")
	require.NoError(t, err)
	assert.Equal(t, []string{"Foo"}, names(got))
	got, err = d.Named(ctx, folder, "none")
	require.NoError(t, err)
	assert.Empty(t, got)

	// Named follows the pages to the end.
	s.SetPageSize(1)
	got, err = d.Named(ctx, folder, "foo")
	require.NoError(t, err)
	assert.Equal(t, []string{"foo", "foo"}, names(got))

	// But not without end.
	many := s.AddFolder("", "many")
	for range 12 {
		s.AddFile(many, "DUP", nil)
	}
	_, err = d.Named(ctx, many, "dup")
	assert.Equal(t, provider.KindIncomplete, provider.KindOf(err))
}

func TestIncompleteSearch(t *testing.T) {
	s, d, _ := setup(t)
	s.AddFile("", "a", nil)
	s.IncompleteNext()
	_, err := d.Children(context.Background(), s.RootID(), "")
	assert.Equal(t, provider.KindIncomplete, provider.KindOf(err))

	s.IncompleteNext()
	_, err = d.Named(context.Background(), s.RootID(), "a")
	assert.Equal(t, provider.KindIncomplete, provider.KindOf(err))
}

func TestOpen(t *testing.T) {
	s, d, _ := setup(t)
	ctx := context.Background()
	id := s.AddFile("", "digits", []byte("0123456789"))
	item, err := d.Get(ctx, id)
	require.NoError(t, err)

	c, err := d.Open(ctx, item, "")
	require.NoError(t, err)
	assert.False(t, c.Partial)
	assert.Equal(t, "0123456789", read(t, c))

	c, err = d.Open(ctx, item, "bytes=2-4")
	require.NoError(t, err)
	assert.True(t, c.Partial)
	assert.Equal(t, "bytes 2-4/10", c.ContentRange)
	assert.Equal(t, "234", read(t, c))

	// The revision that was listed is read, not the newer content.
	s.SetContent(id, []byte("changed"))
	c, err = d.Open(ctx, item, "")
	require.NoError(t, err)
	assert.Equal(t, "0123456789", read(t, c))

	// Without a revision the current content is read.
	noRevision := *item
	noRevision.Revision = ""
	c, err = d.Open(ctx, &noRevision, "")
	require.NoError(t, err)
	assert.Equal(t, "changed", read(t, c))

	s.FailRevisions(true)
	_, err = d.Open(ctx, item, "")
	assert.Equal(t, provider.KindStale, provider.KindOf(err))

	// A rate limit on a revision stays a rate limit, also after the tries it gets.
	base := provider.RetryBase
	provider.RetryBase = time.Millisecond
	defer func() { provider.RetryBase = base }()
	for range provider.MaxTries + 1 {
		s.Fail(http.StatusForbidden, "userRateLimitExceeded")
	}
	_, err = d.Open(ctx, item, "")
	assert.Equal(t, provider.KindBusy, provider.KindOf(err))

	// An empty file is read whole, Drive refuses ranges on it.
	empty, err := d.Get(ctx, s.AddFile("", "empty", nil))
	require.NoError(t, err)
	s.FailRevisions(false)
	c, err = d.Open(ctx, empty, "bytes=0-9")
	require.NoError(t, err)
	assert.False(t, c.Partial)
	assert.Equal(t, "", read(t, c))

	doc, err := d.Get(ctx, s.AddNative("", "Notes", "gdoc"))
	require.NoError(t, err)
	_, err = d.Open(ctx, doc, "")
	assert.Equal(t, provider.KindForbidden, provider.KindOf(err))
}

func TestOpenThroughRedirect(t *testing.T) {
	s, d, _ := setup(t)
	ctx := context.Background()
	item, err := d.Get(ctx, s.AddFile("", "digits", []byte("0123456789")))
	require.NoError(t, err)
	s.Redirect(true)

	c, err := d.Open(ctx, item, "bytes=5-")
	require.NoError(t, err)
	assert.True(t, c.Partial)
	assert.Equal(t, "56789", read(t, c))
	assert.Equal(t, []string{""}, s.ContentTokens())
}

func TestOpenLinkSendsResourceKey(t *testing.T) {
	s, d, _ := setup(t)
	ctx := context.Background()
	target := s.AddFile("", "shared.bin", []byte("shared"))
	s.SetResourceKey(target, "key-1")
	link, err := d.Get(ctx, s.AddShortcut("", "shared link", target))
	require.NoError(t, err)

	c, err := d.Open(ctx, link, "")
	require.NoError(t, err)
	assert.Equal(t, "shared", read(t, c))

	// Without the key the simulator, like Drive, does not find the file. A client that
	// never saw the shortcut does not know the key.
	noKey := *link
	noKey.Link = &provider.Link{TargetID: target}
	other := New(Options{HTTP: s.Client(), Auth: &drivetest.Auth{}, BaseURL: s.URL()})
	_, err = other.Open(ctx, &noKey, "")
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))

	// Through a redirect the key is needed only for the first call.
	s.Redirect(true)
	c, err = d.Open(ctx, link, "")
	require.NoError(t, err)
	assert.Equal(t, "shared", read(t, c))
}

func TestExport(t *testing.T) {
	s, d, _ := setup(t)
	ctx := context.Background()
	cases := []struct{ kind, format, mime string }{
		{"gdoc", "docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document"},
		{"gdoc", "pdf", "application/pdf"},
		{"gsheet", "xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"},
		{"gslides", "pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation"},
		{"gdraw", "svg", "image/svg+xml"},
	}
	for _, tc := range cases {
		doc, err := d.Get(ctx, s.AddNative("", tc.kind, tc.kind))
		require.NoError(t, err)
		c, err := d.Export(ctx, doc, tc.format)
		require.NoError(t, err, tc.kind)
		assert.Equal(t, "export of "+doc.ID+" as "+tc.mime, read(t, c))
	}

	doc, err := d.Get(ctx, s.AddNative("", "Notes", "gdoc"))
	require.NoError(t, err)
	_, err = d.Export(ctx, doc, "xlsx")
	assert.Equal(t, provider.KindForbidden, provider.KindOf(err))

	binary, err := d.Get(ctx, s.AddFile("", "a.bin", []byte("x")))
	require.NoError(t, err)
	_, err = d.Export(ctx, binary, "pdf")
	assert.Equal(t, provider.KindForbidden, provider.KindOf(err))

	s.Fail(http.StatusForbidden, "exportSizeLimitExceeded")
	_, err = d.Export(ctx, doc, "pdf")
	assert.Equal(t, provider.KindExportTooLarge, provider.KindOf(err))
	assert.Equal(t, 5+1, s.Calls(drivetest.CallExport))
}

func TestAbout(t *testing.T) {
	s, d, _ := setup(t)
	ctx := context.Background()
	s.SetQuota(1234, 15000000000)
	u, err := d.About(ctx)
	require.NoError(t, err)
	assert.Equal(t, provider.Usage{Used: 1234, Total: 15000000000}, *u)

	s.SetQuota(99, 0)
	u, err = d.About(ctx)
	require.NoError(t, err)
	assert.Equal(t, provider.Usage{Used: 99, Total: 0}, *u)
}

func TestErrorReasons(t *testing.T) {
	s, d, _ := setup(t)
	ctx := context.Background()
	cases := []struct {
		status int
		reason string
		kind   provider.Kind
	}{
		{404, "notFound", provider.KindNotFound},
		{403, "rateLimitExceeded", provider.KindBusy},
		{403, "userRateLimitExceeded", provider.KindBusy},
		{500, "backendError", provider.KindBusy},
		{403, "dailyLimitExceeded", provider.KindDailyLimit},
		{403, "downloadQuotaExceeded", provider.KindDailyLimit},
		{403, "sharingRateLimitExceeded", provider.KindDailyLimit},
		{403, "storageQuotaExceeded", provider.KindFull},
		{403, "cannotDownloadAbusiveFile", provider.KindFlagged},
		{403, "exportSizeLimitExceeded", provider.KindExportTooLarge},
		{403, "insufficientFilePermissions", provider.KindForbidden},
		{403, "appNotAuthorizedToFile", provider.KindForbidden},
		{403, "forbidden", provider.KindForbidden},
		{403, "domainPolicy", provider.KindForbidden},
		// Unknown reasons fall back to the status.
		{404, "somethingNew", provider.KindNotFound},
		{400, "invalid", provider.KindOther},
	}
	// "Slow down" answers are tried again a few times before they are reported.
	base := provider.RetryBase
	provider.RetryBase = time.Millisecond
	defer func() { provider.RetryBase = base }()
	for _, tc := range cases {
		s.Fail(tc.status, tc.reason)
		if tc.kind == provider.KindBusy {
			for range provider.MaxTries {
				s.Fail(tc.status, tc.reason)
			}
		}
		_, err := d.Get(ctx, s.RootID())
		var e *provider.Error
		require.ErrorAs(t, err, &e, tc.reason)
		assert.Equal(t, tc.kind, e.Kind, tc.reason)
		assert.Equal(t, tc.reason, e.Code, tc.reason)
		assert.Equal(t, tc.status, e.Status, tc.reason)
		assert.NotContains(t, err.Error(), "simulated", tc.reason)
		assert.NotContains(t, err.Error(), "Alex", tc.reason)
	}

	// A reason that is not a plain word is not kept.
	e := classify(403, nil, []byte(`{"error":{"errors":[{"reason":"see the message of Alex"}]}}`))
	assert.Equal(t, provider.KindForbidden, e.Kind)
	assert.Empty(t, e.Code)
	e = classify(503, nil, []byte(`not json`))
	assert.Equal(t, provider.KindBusy, e.Kind)
}

func TestRetriesAndRenewal(t *testing.T) {
	s, d, auth := setup(t)
	ctx := context.Background()

	s.Throttle(1)
	_, err := d.Get(ctx, s.RootID())
	require.NoError(t, err)
	assert.Equal(t, 2, s.Calls(drivetest.CallGet))

	s.Reject()
	_, err = d.About(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, auth.Renewals())

	// Only one renewal per call: a token refused twice is an error.
	s.RequireToken("token-9")
	_, err = d.About(ctx)
	require.Error(t, err)
	assert.Equal(t, 2, auth.Renewals())

	assert.Equal(t, []string{"token-1", "token-1", "token-1", "token-2", "token-2", "token-3"}, s.Tokens())
}

// A folder behind a shortcut can need a resource key for every call about it. The client
// keeps the key from the shortcut and sends it for the folder's id.
func TestFolderBehindShortcutWithResourceKey(t *testing.T) {
	s, d, _ := setup(t)
	ctx := context.Background()
	shared := s.AddFolder("", "Shared")
	s.AddFile(shared, "inside.txt", []byte("x"))
	s.SetResourceKey(shared, "key-2")
	s.AddShortcut("", "Shared link", shared)

	// Not known yet: like Drive, the simulator shows nothing of the folder.
	page, err := d.Children(ctx, shared, "")
	require.NoError(t, err)
	assert.Empty(t, page.Items)

	// Walking there passes the shortcut, by a listing or by a lookup of its name.
	links, err := d.Named(ctx, s.RootID(), "Shared link")
	require.NoError(t, err)
	require.Len(t, links, 1)
	page, err = d.Children(ctx, links[0].ContentID(), "")
	require.NoError(t, err)
	assert.Equal(t, []string{"inside.txt"}, names(page.Items))
	found, err := d.Named(ctx, links[0].ContentID(), "inside.txt")
	require.NoError(t, err)
	assert.Len(t, found, 1)
}

// Target tells the state of what a shortcut points to, which a listing does not.
func TestTarget(t *testing.T) {
	s, d, _ := setup(t)
	ctx := context.Background()
	target := s.AddFile("", "shared.bin", []byte("0123456789"))
	s.SetResourceKey(target, "key-1")
	linkID := s.AddShortcut("", "shared link", target)

	// A client that starts with nothing but the shortcut, as after a restart.
	link := &provider.Item{ID: linkID, Name: "shared link", Link: &provider.Link{TargetID: target, ResourceKey: "key-1"}}
	fresh, err := d.Target(ctx, link)
	require.NoError(t, err)
	assert.Equal(t, linkID, fresh.ID)
	assert.Equal(t, link.Link, fresh.Link)
	assert.Equal(t, int64(10), fresh.Size)
	assert.NotEmpty(t, fresh.Revision)
	assert.Equal(t, "r:"+fresh.Revision, fresh.Version)

	// What Target returned reads exactly that content, also after the target changed.
	s.SetContent(target, []byte("changed"))
	c, err := d.Open(ctx, fresh, "bytes=2-4")
	require.NoError(t, err)
	assert.Equal(t, "234", read(t, c))
	again, err := d.Target(ctx, link)
	require.NoError(t, err)
	assert.NotEqual(t, fresh.Version, again.Version)

	s.Trash(target)
	_, err = d.Target(ctx, link)
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err))
	_, err = d.Target(ctx, &provider.Item{ID: target})
	assert.Equal(t, provider.KindNotFound, provider.KindOf(err), "not a link")
}
