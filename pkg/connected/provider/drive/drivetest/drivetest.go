// Package drivetest is an in-memory Google Drive behind an httptest server, for the
// tests of every package that talks to Drive.
//
// It answers the part of the Drive API v3 the drive package uses, the way Drive does:
// listings by a query of the form "'<id>' in parents and trashed = false" (with "and
// name = '...'", "and mimeType = '...'" added), paging, metadata, downloads with ranges,
// downloads of revisions, exports, the storage quota and errors in Drive's JSON shape.
// The fields parameter is ignored: every answer carries all fields the drive package
// asks for.
//
// Like Drive it allows several entries with one name in a folder. Unlike Drive, which
// may or may not, its name filter ignores case, so that callers have to check names
// themselves.
package drivetest

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// MIME types of Drive.
const (
	FolderType   = "application/vnd.google-apps.folder"
	ShortcutType = "application/vnd.google-apps.shortcut"
)

// nativeTypes are the MIME types of native documents by their kind in Item.Native.
var nativeTypes = map[string]string{
	"gdoc":    "application/vnd.google-apps.document",
	"gsheet":  "application/vnd.google-apps.spreadsheet",
	"gslides": "application/vnd.google-apps.presentation",
	"gdraw":   "application/vnd.google-apps.drawing",
}

// exportTypes are the formats Drive offers per native MIME type.
var exportTypes = map[string][]string{
	nativeTypes["gdoc"]:    {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "application/pdf"},
	nativeTypes["gsheet"]:  {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "application/pdf"},
	nativeTypes["gslides"]: {"application/vnd.openxmlformats-officedocument.presentationml.presentation", "application/pdf"},
	nativeTypes["gdraw"]:   {"image/svg+xml", "application/pdf"},
}

// Call is a kind of call, for Calls.
type Call string

const (
	CallAbout Call = "about"
	CallList  Call = "list"
	// CallGet is a read of an entry's metadata.
	CallGet Call = "get"
	// CallMedia is a download of content, of the current one or of a revision.
	CallMedia  Call = "media"
	CallExport Call = "export"
)

// entry is one item in the store.
type entry struct {
	id, name, mimeType, parent string
	modTime                    time.Time
	version                    int64
	trashed                    bool
	resourceKey                string

	// Binary files only: the current revision and all stored ones.
	revision  string
	revisions map[string][]byte

	// Shortcuts only: Drive keeps the target's type from when the shortcut was made.
	target, targetType string
}

func (e *entry) content() []byte { return e.revisions[e.revision] }

func (e *entry) binary() bool { return !strings.HasPrefix(e.mimeType, "application/vnd.google-apps.") }

// failure is an answer queued for a coming call.
type failure struct {
	status int
	reason string
}

// Server is the simulated Drive. All methods may be called while it serves.
type Server struct {
	api, content *httptest.Server

	mu    sync.Mutex
	items map[string]*entry
	// order is the order of creation, which listings follow.
	order []string
	root  string
	n     int
	clock time.Time

	pageSize       int
	failures       []failure
	incompleteNext bool
	emptyNext      bool
	token          string
	tokens         []string
	contentTokens  []string
	failRevisions  bool
	redirect       bool
	calls          map[Call]int
	usage, limit   int64
}

// New starts a simulated Drive with an empty root folder and closes it at the end of t.
func New(t testing.TB) *Server {
	s := &Server{
		items:    map[string]*entry{},
		root:     "root0",
		clock:    time.Date(2026, 1, 1, 9, 0, 0, 0, time.UTC),
		pageSize: 1000,
		calls:    map[Call]int{},
	}
	s.items[s.root] = &entry{id: s.root, name: "My Drive", mimeType: FolderType, modTime: s.clock}
	s.api = httptest.NewServer(http.HandlerFunc(s.serveAPI))
	s.content = httptest.NewServer(http.HandlerFunc(s.serveContent))
	t.Cleanup(s.api.Close)
	t.Cleanup(s.content.Close)
	return s
}

// URL is the base to pass as drive.Options.BaseURL.
func (s *Server) URL() string { return s.api.URL }

// Client returns an HTTP client for the simulator: like provider.NewHTTPClient it
// follows no redirect, but it may reach the loopback address the servers listen on.
func (s *Server) Client() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// RootID returns the id of the root folder.
func (s *Server) RootID() string { return s.root }

// add stores a new entry. The caller holds s.mu.
func (s *Server) add(parentID, name, mimeType string) *entry {
	if parentID == "" {
		parentID = s.root
	}
	s.n++
	s.clock = s.clock.Add(time.Second)
	e := &entry{id: fmt.Sprintf("id%03d", s.n), name: name, mimeType: mimeType, parent: parentID, modTime: s.clock, version: 1}
	s.items[e.id] = e
	s.order = append(s.order, e.id)
	return e
}

// AddFolder adds a folder. parentID "" means the root.
func (s *Server) AddFolder(parentID, name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.add(parentID, name, FolderType).id
}

// AddFile adds a binary file with its first revision.
func (s *Server) AddFile(parentID, name string, content []byte) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.add(parentID, name, "application/octet-stream")
	e.revisions = map[string][]byte{}
	s.setContent(e, content)
	return e.id
}

// AddNative adds a native document of a kind as in Item.Native ("gdoc", ...).
func (s *Server) AddNative(parentID, name, kind string) string {
	mimeType, ok := nativeTypes[kind]
	if !ok {
		panic("drivetest: unknown native kind " + kind)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.add(parentID, name, mimeType).id
}

// AddShortcut adds a shortcut to targetID, which must exist.
func (s *Server) AddShortcut(parentID, name, targetID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	target := s.must(targetID)
	e := s.add(parentID, name, ShortcutType)
	e.target, e.targetType = target.id, target.mimeType
	return e.id
}

// AddOther adds an entry of any other type, such as a form or a site.
func (s *Server) AddOther(parentID, name, mimeType string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.add(parentID, name, mimeType).id
}

// SetContent stores new content as a new revision, with a new checksum and time. The
// earlier revisions stay downloadable.
func (s *Server) SetContent(id string, content []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.must(id)
	s.clock = s.clock.Add(time.Second)
	e.modTime = s.clock
	e.version++
	s.setContent(e, content)
}

func (s *Server) setContent(e *entry, content []byte) {
	s.n++
	e.revision = fmt.Sprintf("rev%03d", s.n)
	e.revisions[e.revision] = bytes.Clone(content)
}

// SetResourceKey gives an item a resource key. Its content can then be downloaded only
// with the key in the X-Goog-Drive-Resource-Keys header, and shortcuts to it tell it.
func (s *Server) SetResourceKey(id, key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.must(id).resourceKey = key
}

// Rename gives an entry a new name.
func (s *Server) Rename(id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.must(id)
	e.name = name
	e.version++
}

// Move puts an entry into another folder. newParentID "" means the root.
func (s *Server) Move(id, newParentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if newParentID == "" {
		newParentID = s.root
	}
	e := s.must(id)
	e.parent = s.must(newParentID).id
	e.version++
}

// Trash moves an entry to the trash: listings leave it out, a read shows it as trashed.
func (s *Server) Trash(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e := s.must(id)
	e.trashed = true
	e.version++
}

func (s *Server) must(id string) *entry {
	e, ok := s.items[id]
	if !ok {
		panic("drivetest: no item " + id)
	}
	return e
}

// Knobs for tests.

// SetPageSize sets the largest page of a listing. Drive may hand out smaller pages than
// asked for, and so does the simulator.
func (s *Server) SetPageSize(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pageSize = max(n, 1)
}

// Fail answers the next call with an error of Drive's shape. Calls queue up: each one
// answers one call, in order.
func (s *Server) Fail(status int, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failures = append(s.failures, failure{status, reason})
}

// Throttle answers the next n calls with 429 and Retry-After: 0.
func (s *Server) Throttle(n int) {
	for range n {
		s.Fail(http.StatusTooManyRequests, "rateLimitExceeded")
	}
}

// Reject answers the next call with 401, as for an expired token.
func (s *Server) Reject() { s.Fail(http.StatusUnauthorized, "authError") }

// IncompleteNext marks the next listing page as incompleteSearch.
func (s *Server) IncompleteNext() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.incompleteNext = true
}

// EmptyNext answers the next listing call with an empty page that still has a
// nextPageToken, as Drive sometimes does.
func (s *Server) EmptyNext() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.emptyNext = true
}

// RequireToken accepts only this bearer token from now on and answers 401 to every
// other one. Without it any non-empty token is accepted.
func (s *Server) RequireToken(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.token = token
}

// Tokens returns the bearer tokens of all API calls so far, in order.
func (s *Server) Tokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.tokens...)
}

// FailRevisions makes downloads of a revision answer 404, as for a revision Drive no
// longer serves.
func (s *Server) FailRevisions(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failRevisions = fail
}

// Redirect makes downloads answer with a redirect to a second server that serves the
// content, as Drive does at times.
func (s *Server) Redirect(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.redirect = on
}

// ContentTokens returns the Authorization headers that reached the content server,
// one per request ("" when there was none).
func (s *Server) ContentTokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.contentTokens...)
}

// Calls returns how often a kind of call reached the server, failed ones included.
func (s *Server) Calls(kind Call) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[kind]
}

// SetQuota sets what About answers. limit 0 means a storage without a limit.
func (s *Server) SetQuota(usage, limit int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.usage, s.limit = usage, limit
}

// Serving.

// route tells which call a request is, and the ids in its path.
func route(r *http.Request) (kind Call, id, rev string, ok bool) {
	path, found := strings.CutPrefix(r.URL.Path, "/drive/v3/")
	if !found || r.Method != http.MethodGet {
		return "", "", "", false
	}
	media := r.URL.Query().Get("alt") == "media"
	parts := strings.Split(path, "/")
	switch {
	case path == "about":
		return CallAbout, "", "", true
	case path == "files":
		return CallList, "", "", true
	case len(parts) == 2 && parts[0] == "files" && media:
		return CallMedia, parts[1], "", true
	case len(parts) == 2 && parts[0] == "files":
		return CallGet, parts[1], "", true
	case len(parts) == 3 && parts[0] == "files" && parts[2] == "export":
		return CallExport, parts[1], "", true
	case len(parts) == 4 && parts[0] == "files" && parts[2] == "revisions" && media:
		return CallMedia, parts[1], parts[3], true
	}
	return "", "", "", false
}

func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	kind, id, rev, ok := route(r)
	if !ok {
		writeError(w, http.StatusNotFound, "notFound", "Not Found")
		return
	}
	s.calls[kind]++

	token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.tokens = append(s.tokens, token)
	if token == "" || (s.token != "" && token != s.token) {
		writeError(w, http.StatusUnauthorized, "authError", "Invalid Credentials")
		return
	}
	if len(s.failures) > 0 {
		f := s.failures[0]
		s.failures = s.failures[1:]
		if f.status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "0")
		}
		writeError(w, f.status, f.reason, "The simulated Drive refused this call for Alex ("+f.reason+").")
		return
	}

	switch kind {
	case CallAbout:
		quota := map[string]string{"usage": strconv.FormatInt(s.usage, 10)}
		if s.limit > 0 {
			quota["limit"] = strconv.FormatInt(s.limit, 10)
		}
		writeJSON(w, map[string]any{"storageQuota": quota})
	case CallList:
		s.list(w, r)
	default:
		if id == "root" {
			id = s.root
		}
		e, found := s.items[id]
		if !found {
			writeError(w, http.StatusNotFound, "notFound", "File not found: "+id+".")
			return
		}
		switch kind {
		case CallGet:
			writeJSON(w, s.file(e))
		case CallMedia:
			s.media(w, r, e, rev)
		case CallExport:
			s.export(w, r, e)
		}
	}
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	match, err := parseQuery(query.Get("q"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid", "Invalid Value")
		return
	}
	size := s.pageSize
	if n, err := strconv.Atoi(query.Get("pageSize")); err == nil && n > 0 {
		size = min(size, n)
	}
	offset := 0
	if token := query.Get("pageToken"); token != "" {
		n, err := strconv.Atoi(strings.TrimPrefix(token, "page-"))
		if err != nil || n < 0 || !strings.HasPrefix(token, "page-") {
			writeError(w, http.StatusBadRequest, "invalid", "Invalid Value")
			return
		}
		offset = n
	}

	var found []*entry
	for _, id := range s.order {
		if e := s.items[id]; match(e) {
			found = append(found, e)
		}
	}

	answer := map[string]any{"files": []any{}}
	if s.emptyNext {
		s.emptyNext = false
		answer["nextPageToken"] = fmt.Sprintf("page-%d", offset)
		writeJSON(w, answer)
		return
	}
	files := []any{}
	for i := offset; i < len(found) && i < offset+size; i++ {
		files = append(files, s.file(found[i]))
	}
	answer["files"] = files
	if offset+size < len(found) {
		answer["nextPageToken"] = fmt.Sprintf("page-%d", offset+size)
	}
	if s.incompleteNext {
		s.incompleteNext = false
		answer["incompleteSearch"] = true
	}
	writeJSON(w, answer)
}

// file is an entry the way Drive shows it.
func (s *Server) file(e *entry) map[string]any {
	f := map[string]any{
		"kind":         "drive#file",
		"id":           e.id,
		"name":         e.name,
		"mimeType":     e.mimeType,
		"modifiedTime": e.modTime.Format("2006-01-02T15:04:05.000Z"),
		"version":      strconv.FormatInt(e.version, 10),
		"trashed":      e.trashed,
		"webViewLink":  "https://drive.example.com/file/d/" + e.id + "/view",
	}
	if e.parent != "" {
		f["parents"] = []string{e.parent}
	}
	if e.resourceKey != "" {
		f["resourceKey"] = e.resourceKey
	}
	if e.binary() {
		content := e.content()
		sum := md5.Sum(content)
		f["size"] = strconv.Itoa(len(content))
		f["md5Checksum"] = hex.EncodeToString(sum[:])
		f["headRevisionId"] = e.revision
	}
	if e.mimeType == ShortcutType {
		details := map[string]string{"targetId": e.target, "targetMimeType": e.targetType}
		if t, ok := s.items[e.target]; ok && t.resourceKey != "" {
			details["targetResourceKey"] = t.resourceKey
		}
		f["shortcutDetails"] = details
	}
	return f
}

func (s *Server) media(w http.ResponseWriter, r *http.Request, e *entry, rev string) {
	if !e.binary() {
		writeError(w, http.StatusForbidden, "fileNotDownloadable", "Only files with binary content can be downloaded.")
		return
	}
	if e.resourceKey != "" && !strings.Contains(r.Header.Get("X-Goog-Drive-Resource-Keys"), e.id+"/"+e.resourceKey) {
		writeError(w, http.StatusNotFound, "notFound", "File not found: "+e.id+".")
		return
	}
	if rev == "" {
		rev = e.revision
	} else if _, ok := e.revisions[rev]; !ok || s.failRevisions {
		writeError(w, http.StatusNotFound, "notFound", "Revision not found: "+rev+".")
		return
	}
	if s.redirect {
		http.Redirect(w, r, s.content.URL+"/content/"+e.id+"/"+rev, http.StatusFound)
		return
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(e.revisions[rev]))
}

// serveContent is the second host that redirected downloads lead to. The address alone
// is the permission, as at Drive's content hosts.
func (s *Server) serveContent(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.contentTokens = append(s.contentTokens, r.Header.Get("Authorization"))
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/content/"), "/")
	if len(parts) == 2 {
		if e, ok := s.items[parts[0]]; ok {
			if content, ok := e.revisions[parts[1]]; ok {
				http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(content))
				return
			}
		}
	}
	http.NotFound(w, r)
}

func (s *Server) export(w http.ResponseWriter, r *http.Request, e *entry) {
	formats, ok := exportTypes[e.mimeType]
	if !ok {
		writeError(w, http.StatusForbidden, "fileNotExportable", "Export only supports Docs Editors files.")
		return
	}
	mimeType := r.URL.Query().Get("mimeType")
	for _, f := range formats {
		if f == mimeType {
			w.Header().Set("Content-Type", mimeType)
			_, _ = fmt.Fprintf(w, "export of %s as %s", e.id, mimeType)
			return
		}
	}
	writeError(w, http.StatusBadRequest, "badRequest", "The requested conversion is not supported.")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	_ = json.NewEncoder(w).Encode(v)
}

// statusNames are the status words Drive puts next to the code.
var statusNames = map[int]string{
	http.StatusBadRequest:      "INVALID_ARGUMENT",
	http.StatusUnauthorized:    "UNAUTHENTICATED",
	http.StatusForbidden:       "PERMISSION_DENIED",
	http.StatusNotFound:        "NOT_FOUND",
	http.StatusTooManyRequests: "RESOURCE_EXHAUSTED",
}

// writeError answers with an error in Drive's shape.
func writeError(w http.ResponseWriter, status int, reason, message string) {
	name, ok := statusNames[status]
	if !ok {
		name = "INTERNAL"
	}
	w.Header().Set("Content-Type", "application/json; charset=UTF-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"code":    status,
		"message": message,
		"errors":  []any{map[string]string{"domain": "global", "reason": reason, "message": message}},
		"status":  name,
	}})
}

// Auth hands out the tokens "token-1", "token-2", ...: a new one each time the current
// one is reported as rejected. It implements provider.Auth.
type Auth struct {
	mu       sync.Mutex
	n        int
	renewals int
}

func (a *Auth) Token(_ context.Context, rejected string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.n = max(a.n, 1)
	if rejected == fmt.Sprintf("token-%d", a.n) {
		a.n++
		a.renewals++
	}
	return fmt.Sprintf("token-%d", a.n), nil
}

// Renewals returns how often a rejected token was replaced.
func (a *Auth) Renewals() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.renewals
}
