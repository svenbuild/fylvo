package graph_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/graph"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/graph/graphtest"
)

const chunk = 320 * 1024

func TestCreateFolderAndEmptyFile(t *testing.T) {
	for _, dir := range []bool{true, false} {
		t.Run(fmt.Sprintf("folder=%t", dir), func(t *testing.T) {
			s := graphtest.New(t)
			c := newClient(s, &auth{})
			w := c.(provider.Writer)
			create := w.CreateEmpty
			if dir {
				create = w.CreateFolder
			}
			const name = "Alex ä #%.txt"
			parent := s.AddFolder("", "Parent")
			item, err := create(t.Context(), parent, name)
			require.NoError(t, err)
			require.NotEmpty(t, item.ID)
			require.Equal(t, name, item.Name)
			require.Equal(t, dir, item.IsDir)
			require.Zero(t, item.Size)

			_, err = create(t.Context(), parent, strings.ToUpper(name))
			require.Equal(t, provider.KindExists, provider.KindOf(err))
			page, err := c.Children(t.Context(), parent, "")
			require.NoError(t, err)
			require.Equal(t, []provider.Item{*item}, page.Items)
		})
	}
}

func TestUpdateAndTrash(t *testing.T) {
	for _, tc := range []struct {
		name, rename string
		move, clash  bool
	}{
		{name: "rename", rename: "New.txt"},
		{name: "move", move: true},
		{name: "rename and move", rename: "New.txt", move: true},
		{name: "taken name on rename", rename: "New.txt", clash: true},
		{name: "taken name on move", move: true, clash: true},
		{name: "taken name on rename and move", rename: "New.txt", move: true, clash: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := graphtest.New(t)
			c := newClient(s, &auth{})
			w := c.(provider.Writer)
			ctx := t.Context()
			from := s.AddFolder("", "Source")
			dest := s.AddFolder("", "Destination")
			id := s.AddFile(from, "Original.txt", []byte("sample"))
			change := provider.Change{Name: tc.rename, OldParent: from}
			expectedParent, expectedName := from, "Original.txt"
			if tc.move {
				change.Parent = dest
				expectedParent = dest
			}
			if tc.rename != "" {
				expectedName = tc.rename
			}
			if tc.clash {
				s.AddFile(expectedParent, strings.ToUpper(expectedName), nil)
			}
			item, err := w.Update(ctx, id, change)
			require.Equal(t, 1, s.Calls("item"))
			if tc.clash {
				require.Equal(t, provider.KindExists, provider.KindOf(err))
				expectedParent, expectedName = from, "Original.txt"
			} else {
				require.NoError(t, err)
				require.Equal(t, id, item.ID)
				require.Equal(t, expectedName, item.Name)
			}
			matches, err := c.Named(ctx, expectedParent, expectedName)
			require.NoError(t, err)
			require.Len(t, matches, 1)
			require.Equal(t, id, matches[0].ID)

			require.NoError(t, w.Trash(ctx, id))
			require.Equal(t, provider.KindNotFound, provider.KindOf(w.Trash(ctx, id)))
			_, err = c.Get(ctx, id)
			require.Equal(t, provider.KindNotFound, provider.KindOf(err))
		})
	}
}

func TestNewUploadReceiptAndState(t *testing.T) {
	s := graphtest.New(t)
	s.RequireBearerToken()
	a := &auth{}
	c := newClient(s, a)
	w := c.(provider.Writer)
	ctx := t.Context()
	data := append(bytes.Repeat([]byte("a"), chunk), bytes.Repeat([]byte("b"), chunk)...)
	data = append(data, []byte("tail")...)
	const name = "Alex #%.txt"
	u, err := w.NewUpload(ctx, s.RootID(), name, int64(len(data)))
	require.NoError(t, err)
	require.EqualValues(t, chunk, u.Granularity())
	assertUploadState(t, u, 0, http.StatusOK)

	// Renew through Graph before the first fragment. The upload host needs no token.
	s.RejectNextToken()
	_, err = c.About(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, a.renewals)
	tokens := s.Tokens()
	require.Equal(t, []string{"test-token-1", "test-token-1", "test-token-2"}, tokens)

	for _, start := range []int{0, chunk} {
		item, err := u.Send(ctx, int64(start), bytes.NewReader(data[start:start+chunk]), chunk)
		require.NoError(t, err)
		require.Nil(t, item)
		assertUploadState(t, u, int64(start+chunk), http.StatusOK)
	}
	require.Equal(t, tokens, s.Tokens())
	absent, err := c.Named(ctx, s.RootID(), name)
	require.NoError(t, err)
	require.Empty(t, absent)

	receipt, err := u.Send(ctx, 2*chunk, bytes.NewReader(data[2*chunk:]), int64(len(data)-2*chunk))
	require.NoError(t, err)
	require.NotEmpty(t, receipt.ID)
	require.Equal(t, name, receipt.Name)
	require.False(t, receipt.IsDir)
	require.EqualValues(t, len(data), receipt.Size)
	require.True(t, strings.HasPrefix(receipt.Version, "c:ctag-"))
	require.NotEmpty(t, receipt.ETag)
	require.Empty(t, receipt.Revision)
	require.Equal(t, graph.QuickXorHash(data), receipt.Hash)
	require.False(t, receipt.ModTime.IsZero())
	require.Equal(t, "https://example.com/items/"+url.PathEscape(receipt.ID), receipt.WebURL)
	require.Empty(t, receipt.Native)
	require.Nil(t, receipt.Link)
	stored, err := c.Get(ctx, receipt.ID)
	require.NoError(t, err)
	require.Equal(t, stored, receipt)
	assertUploadState(t, u, 0, http.StatusNotFound)
	require.NoError(t, u.Cancel(ctx))
	require.False(t, s.UploadSawToken())
}

func TestReplaceUpload(t *testing.T) {
	for _, tc := range []string{"replaced", "stale at start", "stale at commit", "no ETag"} {
		t.Run(tc, func(t *testing.T) {
			s := graphtest.New(t)
			c := newClient(s, &auth{})
			w := c.(provider.Writer)
			ctx := t.Context()
			id := s.AddFile("", "File.txt", []byte("before"))
			original, err := c.Get(ctx, id)
			require.NoError(t, err)
			requestItem := *original
			if tc == "stale at start" {
				s.SetContent(id, []byte("different"))
			}
			if tc == "no ETag" {
				requestItem.ETag = ""
			}
			const replacement = "replacement"
			u, err := w.ReplaceUpload(ctx, &requestItem, int64(len(replacement)))
			if tc == "stale at start" {
				require.Equal(t, provider.KindStale, provider.KindOf(err))
				require.Nil(t, u)
				return
			}
			require.NoError(t, err)
			if tc == "stale at commit" || tc == "no ETag" {
				s.SetContent(id, []byte("different"))
			}
			receipt, err := u.Send(ctx, 0, strings.NewReader(replacement), int64(len(replacement)))
			stored, getErr := c.Get(ctx, id)
			require.NoError(t, getErr)
			if tc == "stale at commit" {
				require.Equal(t, provider.KindStale, provider.KindOf(err))
				require.Equal(t, graph.QuickXorHash([]byte("different")), stored.Hash)
				return
			}
			require.NoError(t, err)
			require.Equal(t, id, receipt.ID)
			require.Equal(t, original.Name, receipt.Name)
			require.EqualValues(t, len(replacement), receipt.Size)
			require.NotEqual(t, original.ETag, receipt.ETag)
			require.NotEqual(t, original.Version, receipt.Version)
			require.Equal(t, graph.QuickXorHash([]byte(replacement)), receipt.Hash)
			require.True(t, receipt.ModTime.After(original.ModTime))
			require.Equal(t, stored, receipt)
		})
	}
}

func TestNewUploadNameConflict(t *testing.T) {
	for _, takenBeforeStart := range []bool{true, false} {
		t.Run(fmt.Sprintf("takenBeforeStart=%t", takenBeforeStart), func(t *testing.T) {
			s := graphtest.New(t)
			c := newClient(s, &auth{})
			w := c.(provider.Writer)
			ctx := t.Context()
			const name = "File.txt"
			var existing string
			if takenBeforeStart {
				existing = s.AddFile("", strings.ToUpper(name), []byte("original"))
			}
			u, err := w.NewUpload(ctx, s.RootID(), name, 1)
			if !takenBeforeStart {
				require.NoError(t, err)
				existing = s.AddFile("", strings.ToUpper(name), []byte("original"))
				_, err = u.Send(ctx, 0, strings.NewReader("x"), 1)
			}
			require.Equal(t, provider.KindExists, provider.KindOf(err))
			matches, err := c.Named(ctx, s.RootID(), name)
			require.NoError(t, err)
			require.Len(t, matches, 1)
			require.Equal(t, existing, matches[0].ID)
			require.Equal(t, graph.QuickXorHash([]byte("original")), matches[0].Hash)
		})
	}
}

func TestUploadInterruptedFragmentCanResume(t *testing.T) {
	s := graphtest.New(t)
	w := newClient(s, &auth{}).(provider.Writer)
	ctx := t.Context()
	data := append(bytes.Repeat([]byte("a"), chunk), bytes.Repeat([]byte("b"), chunk)...)
	data = append(data, 'c')
	u, err := w.NewUpload(ctx, s.RootID(), "File.txt", int64(len(data)))
	require.NoError(t, err)
	s.BreakNextFragment(chunk)
	item, err := u.Send(ctx, 0, bytes.NewReader(data[:2*chunk]), 2*chunk)
	require.Nil(t, item)
	require.Error(t, err)
	require.Equal(t, provider.KindOther, provider.KindOf(err))
	require.Equal(t, 1, s.Calls("upload"))
	assertUploadState(t, u, chunk, http.StatusOK)

	receipt, err := u.Send(ctx, chunk, bytes.NewReader(data[chunk:]), int64(len(data)-chunk))
	require.NoError(t, err)
	require.Equal(t, graph.QuickXorHash(data), receipt.Hash)
}

func TestUploadLostCompletionResponse(t *testing.T) {
	s := graphtest.New(t)
	c := newClient(s, &auth{})
	ctx := t.Context()
	w := c.(provider.Writer)
	u, err := w.NewUpload(ctx, s.RootID(), "File.txt", 1)
	require.NoError(t, err)
	s.LoseNextUploadReceipt()
	item, err := u.Send(ctx, 0, strings.NewReader("x"), 1)
	require.Nil(t, item)
	require.Error(t, err)
	require.Equal(t, provider.KindOther, provider.KindOf(err))
	require.Equal(t, 1, s.Calls("upload"))
	assertUploadState(t, u, 0, http.StatusNotFound)

	items, err := c.Named(ctx, s.RootID(), "File.txt")
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, graph.QuickXorHash([]byte("x")), items[0].Hash)
}

func TestUploadCancelAndRefusals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		offset int64
		kind   provider.Kind
		status int
	}{
		{"canceled", 0, provider.KindNotFound, http.StatusNotFound},
		{"forgotten", 0, provider.KindNotFound, http.StatusNotFound},
		{"full", 0, provider.KindFull, http.StatusInsufficientStorage},
		{"wrong offset", 1, provider.KindOther, http.StatusRequestedRangeNotSatisfiable},
		{"unaligned fragment", 0, provider.KindOther, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := graphtest.New(t)
			c := newClient(s, &auth{})
			ctx := t.Context()
			u, err := c.(provider.Writer).NewUpload(ctx, s.RootID(), "File.txt", 2)
			require.NoError(t, err)
			content := "x"
			switch tc.name {
			case "canceled":
				require.NoError(t, u.Cancel(ctx))
				require.NoError(t, u.Cancel(ctx))
			case "forgotten":
				s.ForgetUploads()
			case "full":
				s.FillStorage()
				content = "xx"
			}
			attempts := s.Calls("upload")
			receipt, err := u.Send(ctx, tc.offset, strings.NewReader(content), int64(len(content)))
			require.Nil(t, receipt)
			var failure *provider.Error
			require.ErrorAs(t, err, &failure)
			require.Equal(t, tc.kind, failure.Kind)
			require.Equal(t, tc.status, failure.Status)
			require.Equal(t, attempts+1, s.Calls("upload"))
			if tc.kind == provider.KindNotFound {
				assertUploadState(t, u, 0, http.StatusNotFound)
			} else {
				assertUploadState(t, u, 0, http.StatusOK)
			}
			matches, err := c.Named(ctx, s.RootID(), "File.txt")
			require.NoError(t, err)
			require.Empty(t, matches)
		})
	}
}

func assertUploadState(t *testing.T, u provider.Upload, offset int64, status int) {
	t.Helper()
	got, done, err := u.State(t.Context())
	if status == http.StatusOK {
		require.NoError(t, err)
	} else {
		var failure *provider.Error
		require.ErrorAs(t, err, &failure)
		require.Equal(t, status, failure.Status)
		require.Equal(t, provider.KindNotFound, failure.Kind)
	}
	require.Equal(t, offset, got)
	require.Nil(t, done)
}

func TestUploadRedirectsKeepAddressPrivate(t *testing.T) {
	var redirected atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirected.Add(1)
	}))
	t.Cleanup(other.Close)
	const secret = "private-upload-key"
	var fragmentCalls atomic.Int32
	session := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			fragmentCalls.Add(1)
		}
		http.Redirect(w, r, other.URL+"/?key="+secret, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(session.Close)
	address := session.URL + "/?key=" + secret
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"uploadUrl":%q}`, address)
	}))
	t.Cleanup(api.Close)
	w := graph.New(graph.Options{Auth: &auth{}, DriveID: "drive", BaseURL: api.URL}).(provider.Writer)
	ctx := t.Context()
	u, err := w.NewUpload(ctx, "parent", "File.txt", 1)
	require.NoError(t, err)
	require.NotContains(t, fmt.Sprintf("%v %+v %#v", u, u, u), secret)
	_, err = u.Send(ctx, 0, strings.NewReader("x"), 1)
	_, _, stateErr := u.State(ctx)
	for _, err := range []error{err, stateErr, u.Cancel(ctx)} {
		var failure *provider.Error
		require.ErrorAs(t, err, &failure)
		require.Equal(t, http.StatusTemporaryRedirect, failure.Status)
		require.NotContains(t, err.Error(), secret)
		require.NotContains(t, err.Error(), session.URL)
	}
	require.EqualValues(t, 1, fragmentCalls.Load())
	require.Zero(t, redirected.Load())
}

func TestMetadataWritesDoNotRetryAmbiguousFailures(t *testing.T) {
	for _, status := range []int{0, http.StatusServiceUnavailable} {
		t.Run(fmt.Sprintf("status=%d", status), func(t *testing.T) {
			var calls atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				_, _ = io.Copy(io.Discard, r.Body)
				if status == 0 {
					panic(http.ErrAbortHandler)
				}
				w.Header().Set("Retry-After", "0")
				http.Error(w, "private detail for Alex", status)
			}))
			t.Cleanup(api.Close)
			w := graph.New(graph.Options{Auth: &auth{}, DriveID: "drive", BaseURL: api.URL}).(provider.Writer)
			ctx := t.Context()
			_, folderErr := w.CreateFolder(ctx, "parent", "Folder")
			_, emptyErr := w.CreateEmpty(ctx, "parent", "File.txt")
			_, updateErr := w.Update(ctx, "item", provider.Change{Name: "New.txt"})
			for _, err := range []error{folderErr, emptyErr, updateErr, w.Trash(ctx, "item")} {
				require.Error(t, err)
				require.NotContains(t, err.Error(), "private detail")
			}
			require.EqualValues(t, 4, calls.Load())
		})
	}
}
