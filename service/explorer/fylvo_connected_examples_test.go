package explorer

import (
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/connectedtest"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/stretchr/testify/require"
)

// Fylvo: the answers of the file routes for a connected storage, one of each kind a
// client has to handle. See connectedtest.Examples for how to get them as files.
func TestConnectedExamples(t *testing.T) {
	e := newConnectedEnv(t)
	x := connectedtest.NewExamples(t)

	docs := e.sim.AddFolder("", "Docs")
	e.sim.AddFolder("", "Empty")
	notes := e.sim.AddFile("", "notes.txt", []byte("hello"))
	e.sim.AddNative("", "Budget", "gsheet")
	e.sim.AddFile("", "twice.txt", []byte("a"))
	e.sim.AddFile("", "twice.txt", []byte("b"))
	e.sim.AddFile("", "with/slash", []byte("x"))
	e.sim.AddShortcut("", "Link to Docs", docs)
	e.sim.AddShortcut("", "Link to notes.txt", notes)
	for _, name := range []string{"a.pdf", "b.pdf", "c.pdf", "d.pdf", "e.pdf"} {
		e.sim.AddFile(docs, name, []byte(name))
	}
	e.sim.SetQuota(250, 1000)

	// The provider asks to slow down for longer than the server waits.
	base := provider.RetryBase
	provider.RetryBase = time.Millisecond
	t.Cleanup(func() { provider.RetryBase = base })
	e.sim.Throttle(provider.MaxTries + 1)
	_, err := e.list(e.alex, e.root, 0, "")
	require.Equal(t, serializer.CodeConnectedBusy, connectedCode(err))
	x.Add("error-busy", nil, err)

	// The root: a folder, a file, a document that exists only at the provider, a name
	// that is there twice, two links, and one name that cannot be shown.
	root, err := e.list(e.alex, e.root, 0, "")
	require.NoError(t, err)
	require.Equal(t, "Docs|Empty|Link to Docs|Budget.gsheet|Link to notes.txt|notes.txt|twice.txt", fileNames(root.Files))
	require.Equal(t, 1, root.Connected.Omitted)
	x.Add("list-root", root, nil)

	empty, err := e.list(e.alex, e.root+"/Empty", 0, "")
	require.NoError(t, err)
	require.Empty(t, empty.Files)
	x.Add("list-empty", empty, nil)

	first, err := e.list(e.alex, e.root+"/Docs", 2, "")
	require.NoError(t, err)
	require.NotEmpty(t, first.Pagination.NextPageToken)
	x.Add("list-page-1", first, nil)
	second, err := e.list(e.alex, e.root+"/Docs", 2, first.Pagination.NextPageToken)
	require.NoError(t, err)
	require.Equal(t, "c.pdf|d.pdf", fileNames(second.Files))
	x.Add("list-page-2", second, nil)

	for name, path := range map[string]string{
		"info-file":            "/notes.txt",
		"info-folder":          "/Docs",
		"info-native-document": "/Budget.gsheet",
		"info-link":            "/Link to notes.txt",
		"info-root":            "",
	} {
		info, err := e.info(e.alex, e.root+path)
		require.NoError(t, err, name)
		x.Add(name, info, nil)
	}

	urls, err := (&FileURLService{Uris: []string{e.root + "/notes.txt"}, Download: true}).Get(e.as(e.alex))
	require.NoError(t, err)
	x.Add("file-url", urls, nil)

	// Previews: a picture has one (its row carries no "thumb:disabled"), a text has none.
	e.sim.AddFile("", "photo.png", testPNG(1))
	e.m.Tree().Forget(e.id)
	photo, err := e.info(e.alex, e.root+"/photo.png")
	require.NoError(t, err)
	x.Add("info-file-with-preview", photo, nil)
	preview, err := e.thumb(e.alex, e.root+"/photo.png")
	require.NoError(t, err)
	x.Add("thumb", preview, nil)
	_, err = e.thumb(e.alex, e.root+"/notes.txt")
	require.Equal(t, serializer.CodeEntityNotExist, connectedCode(err))
	x.Add("error-thumb-none", nil, err)

	usage, err := ConnectedCapacity(e.as(e.alex), e.id)
	require.NoError(t, err)
	x.Add("capacity", usage, nil)

	// What a client can run into.
	_, err = e.info(e.alex, e.root+"/nope.txt")
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
	x.Add("error-not-found", nil, err)

	_, err = e.info(e.alex, e.root+"/twice.txt")
	require.Equal(t, connected.ReasonAmbiguous, connected.ReasonOf(err))
	x.Add("error-item-ambiguous", nil, err)

	_, err = e.list(e.alex, e.root+"/Docs", 2, "made-up")
	require.Equal(t, serializer.CodeConnectedListingGone, connectedCode(err))
	x.Add("error-listing-gone", nil, err)

	_, err = e.list(e.alex, e.root+"?name=notes", 0, "")
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err))
	x.Add("error-unsupported", nil, err)

	anonymous, err := e.dep.UserClient().AnonymousUser(e.ctx)
	require.NoError(t, err)
	_, err = e.list(anonymous, e.root, 0, "")
	require.Equal(t, connected.ReasonNotAllowed, connected.ReasonOf(err))
	x.Add("error-not-allowed", nil, err)

	// The provider no longer accepts the stored access: every route says so until the
	// account has connected the storage again.
	storages := inventory.NewConnectedStorageClient(e.client)
	row, err := storages.Get(e.ctx, e.alex.ID, e.id)
	require.NoError(t, err)
	_, err = storages.UpdateStatus(e.ctx, row.ID, row.Generation, inventory.ConnectedStatusReconnect, "refused")
	require.NoError(t, err)
	_, err = e.list(e.alex, e.root, 0, "")
	require.Equal(t, serializer.CodeConnectedReconnect, connectedCode(err))
	x.Add("error-reconnect", nil, err)
}
