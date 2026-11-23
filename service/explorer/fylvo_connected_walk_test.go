package explorer

import (
	"net/http"
	"strings"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/stretchr/testify/require"
)

// Fylvo: everything below a folder of a connected storage, the search built on it, and
// the formats a document of the provider can be read in.

func (e *connectedEnv) walk(u *ent.User, uri string, size int, token string) (*ListResponse, error) {
	return (&ListFileService{Uri: uri, PageSize: size, OrderBy: "name", NextPageToken: token, Recursive: true}).List(e.as(u))
}

// filePaths are the addresses of rows, without the part that names the storage.
func (e *connectedEnv) filePaths(files []FileResponse) string {
	paths := make([]string, 0, len(files))
	for _, f := range files {
		paths = append(paths, strings.TrimPrefix(f.Path, e.root))
	}
	return strings.Join(paths, "|")
}

func (e *connectedEnv) walkSample() {
	docs := e.sim.AddFolder("", "Docs")
	work := e.sim.AddFolder(docs, "Work")
	e.sim.AddFolder("", "Empty")
	e.sim.AddFile("", "readme.txt", []byte("r"))
	e.sim.AddFile(docs, "Report.pdf", []byte("pdf"))
	e.sim.AddFile(work, "report-draft.txt", []byte("draft"))
	e.sim.AddNative(work, "Budget", "gsheet")
	e.sim.AddShortcut("", "to docs", docs)
}

func TestConnectedRecursiveListing(t *testing.T) {
	e := newConnectedEnv(t)
	e.walkSample()

	// A plain listing says that everything below can be listed and searched.
	plain, err := e.list(e.alex, e.root, 100, "")
	require.NoError(t, err)
	require.True(t, plain.Connected.Search)
	require.False(t, plain.Connected.Incomplete)
	require.False(t, plain.RecursionLimitReached)
	require.Equal(t, "/Docs|/Empty|/to%20docs|/readme.txt", e.filePaths(plain.Files))

	all, err := e.walk(e.alex, e.root, 100, "")
	require.NoError(t, err)
	require.Equal(t, "/Docs|/Empty|/to%20docs|/Docs/Work|/Docs/Work/Budget.gsheet|/readme.txt|/Docs/Work/report-draft.txt|/Docs/Report.pdf", e.filePaths(all.Files))
	require.Equal(t, 8, all.Pagination.TotalItems)
	require.Empty(t, all.Pagination.NextPageToken)
	require.False(t, all.Connected.Incomplete)
	require.False(t, all.RecursionLimitReached)
	require.Equal(t, 1, all.Connected.Links, "the shortcut is listed and not entered")
	require.Equal(t, e.root, all.Parent.Path)
	// A row of the walk is the row of its own folder's listing: same id, same version.
	inFolder, err := e.list(e.alex, e.root+"/Docs/Work", 100, "")
	require.NoError(t, err)
	byPath := map[string]FileResponse{}
	for _, f := range all.Files {
		byPath[f.Path] = f
	}
	require.Len(t, inFolder.Files, 2)
	for _, f := range inFolder.Files {
		require.Equal(t, f.ID, byPath[f.Path].ID, f.Path)
		require.Equal(t, f.PrimaryEntity, byPath[f.Path].PrimaryEntity, f.Path)
		require.Equal(t, f.Metadata, byPath[f.Path].Metadata, f.Path)
	}

	// Below a folder, and in pages: the pages are those of the walk that was started.
	first, err := e.walk(e.alex, e.root+"/Docs", 2, "")
	require.NoError(t, err)
	require.Equal(t, "/Docs/Work|/Docs/Work/Budget.gsheet", e.filePaths(first.Files))
	require.NotEmpty(t, first.Pagination.NextPageToken)
	require.Equal(t, 4, first.Pagination.TotalItems)
	e.sim.AddFile("", "new.txt", []byte("n"))
	second, err := e.walk(e.alex, e.root+"/Docs", 2, first.Pagination.NextPageToken)
	require.NoError(t, err)
	require.Equal(t, "/Docs/Work/report-draft.txt|/Docs/Report.pdf", e.filePaths(second.Files))
	require.Empty(t, second.Pagination.NextPageToken)
	// Its token is no token of a plain listing.
	_, err = e.list(e.alex, e.root+"/Docs", 2, first.Pagination.NextPageToken)
	require.Error(t, err)
	gone := connectedCode(err)
	_, err = e.walk(e.alex, e.root+"/Docs", 2, "made-up.2")
	require.Equal(t, gone, connectedCode(err), "the same answer as for a listing that is no more")

	// Somebody else's storage, a file, a folder that is not there.
	_, err = e.walk(e.sam, e.root, 100, "")
	require.Equal(t, serializer.CodeParentNotExist, connectedCode(err))
	for _, path := range []string{"/readme.txt", "/Missing"} {
		_, err = e.walk(e.alex, e.root+path, 100, "")
		require.Equal(t, serializer.CodeParentNotExist, connectedCode(err), path)
	}
}

func TestConnectedSearch(t *testing.T) {
	e := newConnectedEnv(t)
	e.walkSample()
	found := func(query string) string {
		t.Helper()
		res, err := e.walk(e.alex, e.root+"?"+query, 100, "")
		require.NoError(t, err, query)
		return e.filePaths(res.Files)
	}

	require.Equal(t, "/Docs/Work/report-draft.txt", found("name=report"))
	require.Equal(t, "/Docs/Work/report-draft.txt|/Docs/Report.pdf", found("name=report&case_folding"))
	require.Equal(t, "/Docs/Work/report-draft.txt", found("name=report&name=draft"))
	require.Equal(t, "/Docs/Work/Budget.gsheet|/Docs/Work/report-draft.txt", found("name=draft&name=Budget&name_op_or"))
	require.Equal(t, "/Docs|/Empty|/to%20docs|/Docs/Work", found("type=folder"))
	require.Equal(t, "/Docs|/to%20docs", found("type=folder&name=docs&case_folding"))
	require.Equal(t, "/Docs/Work/Budget.gsheet|/readme.txt|/Docs/Work/report-draft.txt|/Docs/Report.pdf", found("type=file"))
	require.Empty(t, found("name=nothing-like-it"))
	// Below a folder only.
	res, err := e.walk(e.alex, e.root+"/Docs/Work?name=report", 100, "")
	require.NoError(t, err)
	require.Equal(t, "/Docs/Work/report-draft.txt", e.filePaths(res.Files))

	// A search is a look at everything below, and is asked for as one: without
	// "recursive" the parameters are refused, so a plain listing is never mistaken for
	// it. What a connected storage cannot be searched by is refused either way.
	_, err = e.list(e.alex, e.root+"?name=report", 100, "")
	require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err))
	for _, query := range []string{"size_gte=10", "category=image", "meta_tag=x", "type=image", "type=file&type=folder", "name=a&updated_gte=1", "use_or"} {
		_, err = e.walk(e.alex, e.root+"?"+query, 100, "")
		require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err), query)
	}
	// A parameter that cannot be read is refused, not left out: neither a search nor a
	// plain listing answers as if it had not been there.
	for _, query := range []string{"size_gte=%ZZ", "name=report&size_gte=%ZZ", "name=%ZZ", "name=a;size_gte=1"} {
		_, err = e.walk(e.alex, e.root+"?"+query, 100, "")
		require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err), query)
		_, err = e.list(e.alex, e.root+"?"+query, 100, "")
		require.Equal(t, serializer.CodeConnectedUnsupported, connectedCode(err), query)
	}
}

// What the walk could not see it says, in the words clients know.
func TestConnectedRecursiveListingIncomplete(t *testing.T) {
	e := newConnectedEnv(t)
	docs := e.sim.AddFolder("", "Docs")
	e.sim.AddFile(docs, "Either/or.txt", []byte("x"))
	e.sim.AddFile(docs, "twice.txt", []byte("one"))
	e.sim.AddFile(docs, "twice.txt", []byte("two"))
	e.sim.AddFile(docs, "fine.txt", []byte("f"))

	res, err := e.walk(e.alex, e.root, 100, "")
	require.NoError(t, err)
	require.Equal(t, "/Docs|/Docs/fine.txt|/Docs/twice.txt", e.filePaths(res.Files))
	require.True(t, res.Connected.Incomplete)
	require.True(t, res.RecursionLimitReached)
	require.Equal(t, 1, res.Connected.Omitted)
}

// A document that exists only at the provider is read in the format asked for, if that
// kind of document has it.
func TestConnectedExportFormats(t *testing.T) {
	e := newConnectedEnv(t)
	e.sim.AddNative("", "Budget", "gsheet")
	e.sim.AddNative("", "Letter", "gdoc")
	e.sim.AddOther("", "Feedback", "application/vnd.google-apps.form")
	e.sim.AddFile("", "notes.txt", []byte("notes"))

	// The row tells the formats, the usual one first.
	info, err := e.info(e.alex, e.root+"/Budget.gsheet")
	require.NoError(t, err)
	require.Equal(t, "xlsx", info.Metadata[connectedMetaExport])
	require.Equal(t, "xlsx,pdf", info.Metadata[connectedMetaExports])
	form, err := e.info(e.alex, e.root+"/Feedback.gform")
	require.NoError(t, err)
	require.NotContains(t, form.Metadata, connectedMetaExports)

	export := func(path, format string) (string, error) {
		res, err := (&FileURLService{Uris: []string{e.root + path}, Download: true, Export: format}).Get(e.as(e.alex))
		if err != nil {
			return "", err
		}
		return res.Urls[0].Url, nil
	}
	for format, mimeType := range map[string]string{
		"":     "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		"pdf":  "application/pdf",
	} {
		address, err := export("/Budget.gsheet", format)
		require.NoError(t, err, format)
		name := "Budget." + format
		if format == "" {
			name = "Budget.xlsx"
		}
		require.True(t, strings.HasSuffix(address, "/"+name), address)
		rec := e.fetch(address, "")
		require.Equal(t, http.StatusOK, rec.Code, format)
		require.Contains(t, rec.Body.String(), "as "+mimeType, format)
		require.Contains(t, rec.Header().Get("Content-Disposition"), name)
	}
	address, err := export("/Letter.gdoc", "pdf")
	require.NoError(t, err)
	require.True(t, strings.HasSuffix(address, "/Letter.pdf"))

	// A format that kind of document does not have, a document without any, and a file
	// with bytes of its own, which has no formats to choose from.
	refused := func(path, format string) int {
		t.Helper()
		_, err := export(path, format)
		require.Error(t, err, path)
		// The call answers for each address by itself.
		res := serializer.Err(e.as(e.alex), err)
		entry, ok := res.AggregatedError.(map[string]serializer.Response)[e.root+path]
		require.True(t, ok, "%+v", res.AggregatedError)
		return entry.Code
	}
	for path, format := range map[string]string{"/Budget.gsheet": "docx", "/Letter.gdoc": "xlsx", "/Feedback.gform": "pdf", "/notes.txt": "pdf"} {
		require.Equal(t, serializer.CodeConnectedUnsupported, refused(path, format), path)
	}
	require.Equal(t, serializer.CodeConnectedUnsupported, refused("/Budget.gsheet", "../etc"))
}
