package graph_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/graph"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/graph/graphtest"
)

type auth struct {
	renewals int
}

func (a *auth) Token(_ context.Context, rejected string) (string, error) {
	current := "test-token-" + strconv.Itoa(a.renewals+1)
	if rejected == current {
		a.renewals++
		current = "test-token-" + strconv.Itoa(a.renewals+1)
	}
	return current, nil
}

func newClient(s *graphtest.Server, a *auth) provider.Provider {
	return graph.New(graph.Options{Auth: a, DriveID: s.DriveID(), BaseURL: s.URL()})
}

func TestRootAndMultiPageListing(t *testing.T) {
	s := graphtest.New(t)
	s.SetPageSize(2)
	file := s.AddFile("", "Alex.txt", []byte("sample"))
	s.Delete(s.AddFile("", "Deleted 1.txt", []byte("deleted")))
	s.Delete(s.AddFile("", "Deleted 2.txt", []byte("deleted")))
	s.Delete(s.AddFile("", "Deleted.txt", []byte("deleted")))
	folder := s.AddFolder("", "Folder")
	remoteFile := s.AddRemote("", "Remote file", false, []byte("target"))
	remoteFolder := s.AddRemote("", "Remote folder", true, nil)
	c := newClient(s, &auth{})
	ctx := t.Context()

	for range 2 {
		root, err := c.Root(ctx)
		require.NoError(t, err)
		require.Equal(t, s.RootID(), root)
	}
	require.Equal(t, 1, s.Calls("root"))
	require.True(t, c.CaseInsensitive())

	page, err := c.Children(ctx, s.RootID(), "")
	require.NoError(t, err)
	first := page.Next
	require.NotEmpty(t, first)
	require.Len(t, page.Items, 1)
	gotFile := page.Items[0]
	require.Equal(t, file, gotFile.ID)
	require.Equal(t, "Alex.txt", gotFile.Name)
	require.False(t, gotFile.IsDir)
	require.EqualValues(t, 6, gotFile.Size)
	require.Equal(t, graph.QuickXorHash([]byte("sample")), gotFile.Hash)
	require.True(t, strings.HasPrefix(gotFile.Version, "c:ctag-"))
	require.NotEmpty(t, gotFile.ETag)
	require.Equal(t, "https://example.com/items/"+url.PathEscape(file), gotFile.WebURL)
	require.False(t, gotFile.ModTime.IsZero())
	require.Empty(t, gotFile.Revision)
	require.Empty(t, gotFile.Native)
	require.Nil(t, gotFile.Link)

	// A whole page can be filtered out while more entries remain.
	page, err = c.Children(ctx, s.RootID(), first)
	require.NoError(t, err)
	require.Empty(t, page.Items)
	require.NotEmpty(t, page.Next)

	page, err = c.Children(ctx, s.RootID(), page.Next)
	require.NoError(t, err)
	require.Len(t, page.Items, 2)
	gotFolder, gotRemoteFile := page.Items[0], page.Items[1]
	require.Equal(t, folder, gotFolder.ID)
	require.True(t, gotFolder.IsDir)
	require.False(t, gotFolder.ModTime.IsZero())
	require.Empty(t, gotFolder.Hash)
	require.Empty(t, gotFolder.Version)

	require.Equal(t, remoteFile, gotRemoteFile.ID)
	require.False(t, gotRemoteFile.IsDir)
	require.EqualValues(t, 6, gotRemoteFile.Size)
	require.Equal(t, graph.QuickXorHash([]byte("target")), gotRemoteFile.Hash)
	require.True(t, gotRemoteFile.ModTime.After(gotFolder.ModTime))
	require.NotNil(t, gotRemoteFile.Link)
	require.NotEmpty(t, gotRemoteFile.Link.TargetID)
	require.NotEmpty(t, gotRemoteFile.Link.TargetDrive)
	require.NotEqual(t, remoteFile, gotRemoteFile.Link.TargetID)
	require.NotEqual(t, s.DriveID(), gotRemoteFile.Link.TargetDrive)
	require.Empty(t, gotRemoteFile.Version)
	require.Empty(t, gotRemoteFile.ETag)
	require.Equal(t, "https://example.com/items/"+url.PathEscape(remoteFile), gotRemoteFile.WebURL)

	page, err = c.Children(ctx, s.RootID(), page.Next)
	require.NoError(t, err)
	require.Empty(t, page.Next)
	require.Len(t, page.Items, 1)
	gotRemoteFolder := page.Items[0]
	require.Equal(t, remoteFolder, gotRemoteFolder.ID)
	require.True(t, gotRemoteFolder.IsDir)
	require.NotNil(t, gotRemoteFolder.Link)
	require.Equal(t, gotRemoteFile.Link.TargetDrive, gotRemoteFolder.Link.TargetDrive)
	require.Empty(t, gotRemoteFolder.Hash)
	require.Empty(t, gotRemoteFolder.Version)
	require.Empty(t, gotRemoteFolder.ETag)
	require.True(t, gotRemoteFolder.ModTime.After(gotRemoteFile.ModTime))
	require.Equal(t, 4, s.Calls("children"))

	s.Rename(remoteFile, "Renamed link")
	renamed, err := c.Get(ctx, remoteFile)
	require.NoError(t, err)
	require.Equal(t, "Renamed link", renamed.Name)
	require.Equal(t, gotRemoteFile.ModTime, renamed.ModTime)
	require.Equal(t, gotRemoteFile.Link, renamed.Link)
}

func TestNamedAndGetAfterChanges(t *testing.T) {
	s := graphtest.New(t)
	folder := s.AddFolder("", "Folder")
	const name = "Alex ä #%' Ö.txt"
	id := s.AddFile(folder, name, []byte("original"))
	require.Panics(t, func() { s.AddFile(folder, "ALEX Ä #%' ö.TXT", nil) })
	s.AddNotebook(folder, "Notebook")
	s.AddVault()
	c := newClient(s, &auth{})
	ctx := t.Context()

	matches, err := c.Named(ctx, folder, "ALEX Ä #%' ö.TXT")
	require.NoError(t, err)
	require.Len(t, matches, 1)
	require.Equal(t, name, matches[0].Name)
	require.Equal(t, id, matches[0].ID)
	original, err := c.Get(ctx, id)
	require.NoError(t, err)
	require.Equal(t, matches[0], *original)
	matches, err = c.Named(ctx, folder, "Missing")
	require.NoError(t, err)
	require.Empty(t, matches)
	// A notebook and the vault have nothing to read and nothing to list. They are
	// entries all the same, each of its kind, and open at the provider.
	matches, err = c.Named(ctx, folder, "Notebook")
	require.NoError(t, err)
	require.Len(t, matches, 1)
	require.Equal(t, "onenote", matches[0].Native)
	require.False(t, matches[0].IsDir)
	require.NotEmpty(t, matches[0].WebURL)
	matches, err = c.Named(ctx, s.RootID(), "personal vault")
	require.NoError(t, err)
	require.Len(t, matches, 1)
	require.Equal(t, "vault", matches[0].Native)
	require.False(t, matches[0].IsDir, "the vault stays locked: it is not a folder to walk into")
	require.Empty(t, matches[0].Version)

	s.Rename(id, "Renamed.txt")
	s.Move(id, "")
	matches, err = c.Named(ctx, folder, name)
	require.NoError(t, err)
	require.Empty(t, matches)
	matches, err = c.Named(ctx, s.RootID(), "RENAMED.TXT")
	require.NoError(t, err)
	require.Len(t, matches, 1)
	require.Equal(t, id, matches[0].ID)
	require.Equal(t, original.Version, matches[0].Version)
	require.Equal(t, original.ModTime, matches[0].ModTime)
	require.NotEqual(t, original.ETag, matches[0].ETag)

	s.SetContent(id, []byte("new"))
	updated, err := c.Get(ctx, id)
	require.NoError(t, err)
	require.EqualValues(t, 3, updated.Size)
	require.Equal(t, graph.QuickXorHash([]byte("new")), updated.Hash)
	require.NotEqual(t, original.Version, updated.Version)
	require.NotEqual(t, matches[0].ETag, updated.ETag)
	require.True(t, updated.ModTime.After(original.ModTime))

	s.Delete(id)
	_, err = c.Get(ctx, id)
	require.Equal(t, provider.KindNotFound, provider.KindOf(err))
	_, err = c.Get(ctx, "missing")
	var failure *provider.Error
	require.ErrorAs(t, err, &failure)
	require.Equal(t, provider.KindNotFound, failure.Kind)
	require.Equal(t, "itemNotFound", failure.Code)
	require.NotContains(t, err.Error(), "The requested item is missing.")
}

func TestChildrenRefusesUnsafePage(t *testing.T) {
	s := graphtest.New(t)
	c := newClient(s, &auth{})
	var sent atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		sent.Add(1)
	}))
	t.Cleanup(other.Close)

	for _, address := range []string{
		other.URL + "/v1.0/drives",
		s.URL() + "/v1.0-other/drives",
		s.URL() + "/v1.0/%2e%2e/drives",
		s.URL() + "/v1.0/%2f..%2fdrives",
		s.URL() + "/v1.0/%5c..%5cdrives",
		strings.Replace(s.URL(), "http:", "https:", 1) + "/v1.0/drives",
		strings.Replace(s.URL(), "://", "://Alex@", 1) + "/v1.0/drives",
		"/v1.0/drives",
	} {
		_, err := c.Children(t.Context(), s.RootID(), address)
		require.Equal(t, provider.KindUnsafe, provider.KindOf(err), address)
	}
	require.Zero(t, sent.Load())
	require.Empty(t, s.Tokens())
}

func TestRequestShapes(t *testing.T) {
	paths := make(chan string, 6)
	const next = "/v1.0/drives/drive%2Fpart/items/id%3A/children?$skiptoken=a%2Bb%252Fc"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.RequestURI
		_, _ = io.WriteString(w, `{"id":"id:","file":{},"value":[],"@odata.nextLink":"http://`+r.Host+next+`","quota":{}}`)
	}))
	t.Cleanup(s.Close)
	c := graph.New(graph.Options{Auth: &auth{}, DriveID: "drive/part", BaseURL: s.URL, HTTP: s.Client()})
	ctx := t.Context()
	const drivePath = "/v1.0/drives/drive%2Fpart"
	const fields = "id,name,size,folder,file,package,deleted,specialFolder,fileSystemInfo,lastModifiedDateTime,cTag,eTag,parentReference,remoteItem,webUrl"
	selectItems := "?$select=" + url.QueryEscape(fields)
	check := func(err error, path string) {
		t.Helper()
		require.NoError(t, err)
		require.Len(t, paths, 1)
		require.Equal(t, path, <-paths)
	}

	_, err := c.Root(ctx)
	check(err, drivePath+"/root?$select=id")
	_, err = c.Get(ctx, "id:")
	check(err, drivePath+"/items/id%3A"+selectItems)
	_, err = c.Named(ctx, "id:", "Alex #%' ä")
	check(err, drivePath+"/items/id%3A:/Alex%20%23%25%27%20%C3%A4"+selectItems)
	page, err := c.Children(ctx, "id:", "")
	check(err, drivePath+"/items/id%3A/children"+selectItems+"&$top=200")
	require.Equal(t, s.URL+next, page.Next)
	_, err = c.Children(ctx, "id:", page.Next)
	check(err, next)
	_, err = c.About(ctx)
	check(err, drivePath+"?$select=quota")
}

func TestOpenExportAndQuota(t *testing.T) {
	s := graphtest.New(t)
	s.RequireBearerToken()
	id := s.AddFile("", "File.txt", []byte("0123456789"))
	linkID := s.AddRemote("", "Shared.txt", false, []byte("shared"))
	folderID := s.AddFolder("", "Folder")
	s.Delete(s.AddFile("", "Deleted.txt", []byte("deleted")))
	c := newClient(s, &auth{})
	ctx := t.Context()

	item, err := c.Get(ctx, id)
	require.NoError(t, err)
	got, err := c.Open(ctx, item, "")
	require.NoError(t, err)
	require.Equal(t, "0123456789", readContent(t, got))
	require.False(t, got.Partial)
	require.EqualValues(t, 10, got.Length)

	got, err = c.Open(ctx, item, "bytes=2-4")
	require.NoError(t, err)
	require.Equal(t, "234", readContent(t, got))
	require.True(t, got.Partial)
	require.EqualValues(t, 3, got.Length)
	require.Equal(t, "bytes 2-4/10", got.ContentRange)

	link, err := c.Get(ctx, linkID)
	require.NoError(t, err)
	got, err = c.Open(ctx, link, "")
	require.NoError(t, err)
	require.Equal(t, "shared", readContent(t, got))
	require.False(t, s.DownloadSawToken())

	folder, err := c.Get(ctx, folderID)
	require.NoError(t, err)
	_, err = c.Open(ctx, folder, "")
	require.Equal(t, provider.KindForbidden, provider.KindOf(err))
	_, err = c.Export(ctx, item, "pdf")
	require.Equal(t, provider.KindForbidden, provider.KindOf(err))
	require.Equal(t, 3, s.Calls("content"))

	usage, err := c.About(ctx)
	require.NoError(t, err)
	require.Equal(t, &provider.Usage{Used: 10, Total: 1 << 30}, usage)
}

func readContent(t *testing.T, content *provider.Content) string {
	t.Helper()
	defer content.Body.Close()
	data, err := io.ReadAll(content.Body)
	require.NoError(t, err)
	return string(data)
}

func TestRetriesAndTokenRenewal(t *testing.T) {
	s := graphtest.New(t)
	s.RequireBearerToken()
	s.ThrottleNext(1)
	a := &auth{}
	c := newClient(s, a)

	_, err := c.Children(t.Context(), s.RootID(), "")
	require.NoError(t, err)
	require.Equal(t, 2, s.Calls("children"))
	require.Zero(t, a.renewals)

	s.RejectNextToken()
	_, err = c.Children(t.Context(), s.RootID(), "")
	require.NoError(t, err)
	require.Equal(t, 4, s.Calls("children"))
	require.Equal(t, 1, a.renewals)
	require.Equal(t, []string{"test-token-1", "test-token-1", "test-token-1", "test-token-2"}, s.Tokens())
}

// Target tells the state of the item a link points to, which lives in another drive.
func TestTarget(t *testing.T) {
	s := graphtest.New(t)
	c := newClient(s, &auth{})
	ctx := context.Background()
	remote := s.AddRemote("", "Remote file", false, []byte("target"))

	link, err := c.Get(ctx, remote)
	require.NoError(t, err)
	require.Empty(t, link.Version, "a listing does not tell the state of the target")
	fresh, err := c.Target(ctx, link)
	require.NoError(t, err)
	require.Equal(t, remote, fresh.ID)
	require.Equal(t, link.Link, fresh.Link)
	require.EqualValues(t, 6, fresh.Size)
	require.NotEmpty(t, fresh.Version)
	content, err := c.Open(ctx, fresh, "")
	require.NoError(t, err)
	require.Equal(t, "target", readContent(t, content))

	s.SetContent(remote, []byte("changed"))
	again, err := c.Target(ctx, link)
	require.NoError(t, err)
	require.NotEqual(t, fresh.Version, again.Version)

	_, err = c.Target(ctx, &provider.Item{ID: remote})
	require.Equal(t, provider.KindNotFound, provider.KindOf(err), "not a link")
	gone := *link
	gone.Link = &provider.Link{TargetID: "nothing", TargetDrive: link.Link.TargetDrive}
	_, err = c.Target(ctx, &gone)
	require.Equal(t, provider.KindNotFound, provider.KindOf(err))
}
