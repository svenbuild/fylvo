package connected

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCopyName(t *testing.T) {
	for name, want := range map[string]string{
		"letter.docx":      "letter.docx",
		"a/b.txt":          "a_b.txt",
		`what?.txt`:        "what_.txt",
		" spaced .txt":     "spaced.txt",
		"..":               "_",
		"":                 "_",
		"trailing.":        "trailing",
		"with\ttab.txt":    "with_tab.txt",
		"Zürich im Winter": "Zürich im Winter",
	} {
		require.Equal(t, want, CopyName(name), name)
	}
	require.Equal(t, "letter (2).docx", NumberedName("letter.docx", 2))
	require.Equal(t, "letter.docx", NumberedName("letter.docx", 1))
	require.Equal(t, "Docs (3)", NumberedName("Docs", 3))
	require.Equal(t, ".env (2)", NumberedName(".env", 2))
}

// A file is read for copying in one version, and its last bytes are handed out only when
// all of them were that version.
func TestOpenCopy(t *testing.T) {
	forBoth(t, func(t *testing.T, e *uploadEnv) {
		data := payload(3*testChunk + 17)
		id := e.sim.AddFile("", "big.bin", data)
		row, err := e.f.Tree().ResolveNow(e.ctx, e.conn, "big.bin")
		require.NoError(t, err)

		r, err := e.f.OpenCopy(e.ctx, e.conn, row)
		require.NoError(t, err)
		require.Equal(t, int64(len(data)), r.Size())
		got, err := io.ReadAll(r)
		require.NoError(t, err)
		require.NoError(t, r.Close())
		require.Equal(t, data, got)

		// The file changes after the provider handed out the old bytes: the end of them
		// is not handed out.
		redirected := false
		e.http.hook = func(req *http.Request) (*http.Response, error) {
			if !isContent(req) && !redirected {
				return nil, nil
			}
			res, err := e.http.base.RoundTrip(req)
			if err != nil || res.StatusCode != http.StatusOK {
				redirected = err == nil && res.StatusCode >= 300 && res.StatusCode < 400
				return res, err
			}
			redirected = false
			defer res.Body.Close()
			old, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			e.sim.SetContent(id, []byte("changed in that moment"))
			res.Body = io.NopCloser(bytes.NewReader(old))
			return res, nil
		}
		r, err = e.f.OpenCopy(e.ctx, e.conn, row)
		require.NoError(t, err)
		got, err = io.ReadAll(r)
		require.ErrorIs(t, err, ErrCopyChanged)
		require.Less(t, len(got), len(data))
		require.NoError(t, r.Close())
		e.http.hook = nil

		// A provider that ends early.
		e.http.hook = func(req *http.Request) (*http.Response, error) {
			if !isContent(req) && !redirected {
				return nil, nil
			}
			res, err := e.http.base.RoundTrip(req)
			if err != nil || res.StatusCode != http.StatusOK {
				redirected = err == nil && res.StatusCode >= 300 && res.StatusCode < 400
				return res, err
			}
			redirected = false
			defer res.Body.Close()
			all, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			res.Body = io.NopCloser(bytes.NewReader(all[:len(all)/2]))
			return res, nil
		}
		row, err = e.f.Tree().ResolveNow(e.ctx, e.conn, "big.bin")
		require.NoError(t, err)
		r, err = e.f.OpenCopy(e.ctx, e.conn, row)
		require.NoError(t, err)
		_, err = io.ReadAll(r)
		require.ErrorIs(t, err, ErrCopyChanged)
		require.NoError(t, r.Close())
		e.http.hook = nil

		// Folders, links and documents of the provider are not opened this way.
		folder := e.sim.AddFolder("", "Docs")
		_ = folder
		dir, err := e.f.Tree().ResolveNow(e.ctx, e.conn, "Docs")
		require.NoError(t, err)
		_, err = e.f.OpenCopy(e.ctx, e.conn, dir)
		require.ErrorIs(t, err, ErrUnsupported)
	})
}

// A document of the provider is exported, and the export checked against its version.
func TestOpenExport(t *testing.T) {
	e := newUploadEnv(t, ProviderDrive)
	e.d.AddNative("", "Budget", "gsheet")
	row, err := e.f.Tree().ResolveNow(e.ctx, e.conn, "Budget.gsheet")
	require.NoError(t, err)
	body, err := e.f.OpenExport(e.ctx, e.conn, row, "xlsx")
	require.NoError(t, err)
	got, err := io.ReadAll(body)
	require.NoError(t, err)
	require.NoError(t, body.Close())
	require.NotEmpty(t, got)

	// The document changes while it is exported: the end of the export says so.
	id := row.ID
	e.http.hook = func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Path, "/export") {
			return nil, nil
		}
		res, err := e.http.base.RoundTrip(req)
		if err == nil {
			// Gone in the moment it was exported: the export is no file to keep.
			e.d.Trash(id)
		}
		return res, err
	}
	body, err = e.f.OpenExport(e.ctx, e.conn, row, "xlsx")
	require.NoError(t, err)
	_, err = io.ReadAll(body)
	require.Error(t, err)
	require.NoError(t, body.Close())
	e.http.hook = nil
	e.d.AddNative("", "Budget", "gsheet")
	row, err = e.f.Tree().ResolveNow(e.ctx, e.conn, "Budget.gsheet")
	require.NoError(t, err)

	_, err = e.f.OpenExport(e.ctx, e.conn, row, "docx")
	require.ErrorIs(t, err, ErrUnsupported)
	plain := e.d.AddFile("", "notes.txt", []byte("notes"))
	_ = plain
	file, err := e.f.Tree().ResolveNow(e.ctx, e.conn, "notes.txt")
	require.NoError(t, err)
	_, err = e.f.OpenExport(e.ctx, e.conn, file, "pdf")
	require.ErrorIs(t, err, ErrUnsupported)
}
