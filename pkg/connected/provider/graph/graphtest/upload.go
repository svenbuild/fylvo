package graphtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
)

const uploadKey = "key=sample%2Bvalue"

type uploadSession struct {
	parent, name string
	itemID, etag string
	size         int64
	content      bytes.Buffer
}

// BreakNextFragment accepts the first n bytes from the next fragment and drops the connection.
func (s *Server) BreakNextFragment(n int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.breakAfter = n
}

// LoseNextUploadReceipt commits the next completed upload and drops its response.
func (s *Server) LoseNextUploadReceipt() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loseUploadEnd = true
}

// ForgetUploads forgets all open sessions.
func (s *Server) ForgetUploads() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.uploads)
}

// FillStorage makes the next valid upload fragment fail because the storage is full.
func (s *Server) FillStorage() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uploadFull = true
}

// UploadSawToken tells whether an upload request included Authorization.
func (s *Server) UploadSawToken() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.uploadAuth
}

func (s *Server) startUpload(w http.ResponseWriter, r *http.Request, route apiRoute, entry *item) {
	var in struct {
		Item struct {
			Behavior string `json:"@microsoft.graph.conflictBehavior"`
		} `json:"item"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		badWrite(w)
		return
	}
	session := &uploadSession{}
	if route.named {
		if in.Item.Behavior != "fail" || !entry.isFolder() {
			badWrite(w)
			return
		}
		if s.named(entry, route.name) != nil {
			nameTaken(w)
			return
		}
		session.parent, session.name = entry.id, route.name
	} else {
		if in.Item.Behavior != "replace" || entry.kind != file {
			badWrite(w)
			return
		}
		session.itemID, session.etag = entry.id, r.Header.Get("If-Match")
		if session.etag != "" && session.etag != entry.eTag {
			staleUpload(w)
			return
		}
	}
	s.uploadNumber++
	id := strconv.Itoa(s.uploadNumber)
	s.uploads[id] = session
	writeJSON(w, http.StatusOK, map[string]string{"uploadUrl": s.download.URL + "/upload/" + id + "?" + uploadKey})
}

func (s *Server) serveUpload(w http.ResponseWriter, r *http.Request, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls["upload"]++
	if r.Header.Get("Authorization") != "" {
		s.uploadAuth = true
		writeError(w, http.StatusUnauthorized, "invalidAuthenticationToken", "The upload URL is already authenticated.")
		return
	}
	session := s.uploads[id]
	if session == nil || r.URL.RawQuery != uploadKey {
		notFound(w)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeUploadState(w, http.StatusOK, session)
	case http.MethodDelete:
		delete(s.uploads, id)
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPut:
		s.receiveFragment(w, r, id, session)
	default:
		writeError(w, http.StatusMethodNotAllowed, "notSupported", "This method is unavailable.")
	}
}

func (s *Server) receiveFragment(w http.ResponseWriter, r *http.Request, id string, session *uploadSession) {
	var first, last, size int64
	contentRange := r.Header.Get("Content-Range")
	n, err := fmt.Sscanf(contentRange, "bytes %d-%d/%d", &first, &last, &size)
	length := r.ContentLength
	if n != 3 || err != nil || size <= 0 || first < 0 || last < first || last >= size ||
		last-first+1 != length || (session.size != 0 && session.size != size) {
		badWrite(w)
		return
	}
	if first != int64(session.content.Len()) {
		writeError(w, http.StatusRequestedRangeNotSatisfiable, "invalidRange", "The range was not expected.")
		return
	}
	final := last == size-1
	if !final && length%(320*1024) != 0 {
		badWrite(w)
		return
	}
	if s.uploadFull {
		s.uploadFull = false
		writeError(w, http.StatusInsufficientStorage, "quotaLimitReached", "The drive is full.")
		return
	}
	session.size = size
	if s.breakAfter > 0 && s.breakAfter < length {
		part := s.breakAfter
		s.breakAfter = 0
		_, _ = io.CopyN(&session.content, r.Body, part)
		panic(http.ErrAbortHandler)
	}
	if _, err := io.CopyN(&session.content, r.Body, length); err != nil {
		panic(http.ErrAbortHandler)
	}
	if !final {
		writeUploadState(w, http.StatusAccepted, session)
		return
	}

	delete(s.uploads, id)
	entry, status := s.finishUpload(w, session)
	if entry == nil {
		return
	}
	if s.loseUploadEnd {
		s.loseUploadEnd = false
		panic(http.ErrAbortHandler)
	}
	writeJSON(w, status, itemJSON(entry))
}

func (s *Server) finishUpload(w http.ResponseWriter, session *uploadSession) (*item, int) {
	if session.itemID != "" {
		entry := s.items[session.itemID]
		if entry == nil || entry.deleted {
			notFound(w)
			return nil, 0
		}
		if session.etag != "" && entry.eTag != session.etag {
			staleUpload(w)
			return nil, 0
		}
		s.setContent(entry, session.content.Bytes())
		return entry, http.StatusOK
	}
	parent := s.items[session.parent]
	if parent == nil || parent.deleted {
		notFound(w)
		return nil, 0
	}
	if s.named(parent, session.name) != nil {
		nameTaken(w)
		return nil, 0
	}
	return s.addItem(parent.id, session.name, file, session.content.Bytes(), nil), http.StatusCreated
}

func writeUploadState(w http.ResponseWriter, status int, session *uploadSession) {
	writeJSON(w, status, map[string][]string{
		"nextExpectedRanges": {strconv.Itoa(session.content.Len()) + "-"},
	})
}

func staleUpload(w http.ResponseWriter) {
	writeError(w, http.StatusPreconditionFailed, "preconditionFailed", "The item has changed.")
}
