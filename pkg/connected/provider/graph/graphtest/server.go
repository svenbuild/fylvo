// Package graphtest serves an in-memory OneDrive with metadata, downloads,
// recycle-bin deletes and resumable upload sessions for tests.
package graphtest

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/graph"
)

const (
	driveID       = "drive!own"
	remoteDriveID = "drive!remote"
	rootID        = "item!root"
	quotaTotal    = 1 << 30
)

type itemKind int

const (
	folder itemKind = iota
	file
	remote
	notebook
	vault
)

type item struct {
	id, drive, parent, name string
	kind                    itemKind
	content                 []byte
	hash, cTag, eTag        string
	fileTime, itemTime      time.Time
	target                  *item
	deleted                 bool
	// noPreview: a picture OneDrive has made no preview of (yet).
	noPreview bool
}

func (i *item) isFolder() bool { return i.kind == folder || i.kind == vault }

// preview is the picture OneDrive shows for the item, nil when it has none. Like
// OneDrive the simulator has one of every picture: the picture itself.
func (i *item) preview() []byte {
	if i.kind == file && !i.noPreview {
		if kind := http.DetectContentType(i.content); kind == "image/png" || kind == "image/jpeg" {
			return i.content
		}
	}
	return nil
}

// Server holds the test drive and the separate host for downloads and uploads.
type Server struct {
	api, download *httptest.Server

	mu       sync.Mutex
	items    map[string]*item
	sequence int
	version  int
	now      time.Time

	pageSize      int
	throttled     int
	unauthorized  bool
	requireBearer bool
	tokens        []string
	calls         map[string]int
	downloadAuth  bool

	thumbnailSizes []string

	uploads       map[string]*uploadSession
	uploadNumber  int
	uploadAuth    bool
	breakAfter    int64
	loseUploadEnd bool
	uploadFull    bool
}

// New starts both hosts and closes them when the test ends.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{
		items:   map[string]*item{},
		calls:   map[string]int{},
		uploads: map[string]*uploadSession{},
		now:     time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	s.items[rootID] = &item{id: rootID, drive: driveID, name: "root", kind: folder, itemTime: s.now}
	s.download = httptest.NewServer(http.HandlerFunc(s.serveDownload))
	t.Cleanup(s.download.Close)
	s.api = httptest.NewServer(http.HandlerFunc(s.serveAPI))
	t.Cleanup(s.api.Close)
	return s
}

// URL is the origin to pass to graph.Options.BaseURL.
func (s *Server) URL() string { return s.api.URL }

// DriveID is the id of the connection's own drive.
func (s *Server) DriveID() string { return driveID }

// RootID is the id of the connection's root folder.
func (s *Server) RootID() string { return rootID }

// SetPageSize caps the requested page size. Zero uses the requested size.
func (s *Server) SetPageSize(size int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pageSize = size
}

// ThrottleNext makes the next n API requests answer 429 with Retry-After set to 0.
func (s *Server) ThrottleNext(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.throttled = n
}

// RejectNextToken makes the next API request answer 401.
func (s *Server) RejectNextToken() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unauthorized = true
}

// RequireBearerToken makes the API reject requests without a bearer token.
func (s *Server) RequireBearerToken() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requireBearer = true
}

// Tokens returns the bearer token from each API request, empty when absent.
func (s *Server) Tokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.tokens)
}

// Calls counts attempts by route, including "createUploadSession" and "upload".
// Name and id lookups both count as "item". Retries count as separate attempts.
func (s *Server) Calls(kind string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[kind]
}

// NoPreview takes the preview of a picture away, as while OneDrive has not made it yet.
func (s *Server) NoPreview(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items[id].noPreview = true
}

// ThumbnailSizes returns the sizes ("c400x400") the previews were asked in.
func (s *Server) ThumbnailSizes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.thumbnailSizes...)
}

// DownloadSawToken tells whether any request to the download host had Authorization.
func (s *Server) DownloadSawToken() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.downloadAuth
}

// AddFolder adds a folder to parentID. An empty parentID means the root.
func (s *Server) AddFolder(parentID, name string) string {
	return s.add(parentID, name, folder, nil, nil)
}

// AddFile adds a file to parentID. An empty parentID means the root.
func (s *Server) AddFile(parentID, name string, content []byte) string {
	return s.add(parentID, name, file, content, nil)
}

// AddRemote adds a link in parentID to a file or folder on another drive.
func (s *Server) AddRemote(parentID, name string, isDir bool, content []byte) string {
	target := &item{drive: remoteDriveID, name: name, kind: file}
	if isDir {
		target.kind = folder
	}
	return s.add(parentID, name, remote, content, target)
}

// AddNotebook adds a OneNote package to parentID.
func (s *Server) AddNotebook(parentID, name string) string {
	return s.add(parentID, name, notebook, nil, nil)
}

// AddVault adds the Personal Vault to the root.
func (s *Server) AddVault() string {
	return s.add("", "Personal Vault", vault, nil, nil)
}

// Fixture mutations panic for invalid ids or conflicting names.
func (s *Server) add(parentID, name string, kind itemKind, content []byte, target *item) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	parent := s.parent(parentID)
	s.checkName(parent.id, name, "")
	return s.addItem(parent.id, name, kind, content, target).id
}

func (s *Server) addItem(parentID, name string, kind itemKind, content []byte, target *item) *item {
	s.sequence++
	id := fmt.Sprintf("item!%08d", s.sequence)
	entry := &item{id: id, drive: driveID, parent: parentID, name: name, kind: kind, target: target}
	s.touch(entry)
	s.items[id] = entry
	contentItem := entry
	if target != nil {
		target.id = fmt.Sprintf("target!%08d", s.sequence)
		s.touch(target)
		s.items[target.id] = target
		contentItem = target
	}
	if contentItem.kind == file {
		s.setContent(contentItem, content)
	}
	return entry
}

func (s *Server) liveItem(id string) *item {
	entry := s.items[id]
	if entry == nil || entry.deleted {
		panic(fmt.Sprintf("no live item with id %q", id))
	}
	return entry
}

func (s *Server) parent(id string) *item {
	if id == "" {
		id = rootID
	}
	entry := s.liveItem(id)
	if !entry.isFolder() || entry.drive != driveID {
		panic(fmt.Sprintf("item %q is not a folder on the drive", id))
	}
	return entry
}

func (s *Server) checkName(parentID, name, exceptID string) {
	if entry := s.findChild(parentID, name, exceptID); entry != nil {
		panic(fmt.Sprintf("the name %q is already in use in %q", name, parentID))
	}
}

func (s *Server) touch(entry *item) {
	s.version++
	s.now = s.now.Add(time.Second)
	entry.itemTime = s.now
	entry.eTag = fmt.Sprintf("etag-%d", s.version)
}

func (s *Server) setContent(entry *item, content []byte) {
	s.touch(entry)
	entry.content = bytes.Clone(content)
	entry.hash = graph.QuickXorHash(entry.content)
	entry.fileTime = entry.itemTime
	entry.cTag = fmt.Sprintf("ctag-%d", s.version)
}

// SetContent replaces a file's bytes, tags, hash and modification time.
func (s *Server) SetContent(id string, content []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.liveItem(id)
	if entry.target != nil {
		entry = entry.target
	}
	if entry.kind != file || entry.deleted {
		panic(fmt.Sprintf("item %q has no file content", id))
	}
	s.setContent(entry, content)
}

// Rename changes the entry's name in its current folder.
func (s *Server) Rename(id, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.liveItem(id)
	s.checkName(entry.parent, name, id)
	entry.name = name
	s.touch(entry)
}

// Move puts an entry in another folder. An empty newParentID means the root.
func (s *Server) Move(id, newParentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.liveItem(id)
	parent := s.parent(newParentID)
	if s.within(parent, entry) {
		panic(fmt.Sprintf("cannot move item %q into itself", id))
	}
	s.checkName(parent.id, entry.name, id)
	entry.parent = parent.id
	s.touch(entry)
}

func (s *Server) within(parent, entry *item) bool {
	for ancestor := parent; ancestor != nil; ancestor = s.items[ancestor.parent] {
		if ancestor == entry {
			return true
		}
	}
	return false
}

// Delete marks the entry deleted so tests can exercise the deleted facet.
func (s *Server) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id == rootID {
		panic("cannot delete the root")
	}
	entry := s.liveItem(id)
	entry.deleted = true
	s.touch(entry)
}
