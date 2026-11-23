package drivetest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// The calls that change the store. All of them run with s.mu held.

// folder returns the folder a new entry goes into: the first of parents, the root
// without any. ok is false when it is not a folder the request may reach.
func (s *Server) folder(r *http.Request, parents []string) (e *entry, ok bool) {
	id := s.root
	if len(parents) > 0 {
		id = parents[0]
	}
	e, found := s.items[id]
	return e, found && e.mimeType == FolderType && s.keyGiven(r, e)
}

func writeFull(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "storageQuotaExceeded", "The storage quota of Alex has been exceeded.")
}

// create makes a folder, or with any other type an entry without content.
func (s *Server) create(w http.ResponseWriter, r *http.Request) {
	var meta struct {
		Name     string   `json:"name"`
		MimeType string   `json:"mimeType"`
		Parents  []string `json:"parents"`
	}
	if err := json.NewDecoder(r.Body).Decode(&meta); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "parseError", "Parse Error")
		return
	}
	parent, ok := s.folder(r, meta.Parents)
	if !ok {
		writeError(w, http.StatusNotFound, "notFound", "File not found: "+strings.Join(meta.Parents, ",")+".")
		return
	}
	if s.full {
		writeFull(w)
		return
	}
	if meta.Name == "" {
		meta.Name = "Untitled"
	}
	if meta.MimeType == "" {
		meta.MimeType = "application/octet-stream"
	}
	e := s.add(parent.id, meta.Name, meta.MimeType)
	if e.binary() {
		e.revisions = map[string][]byte{}
		s.setContent(e, nil)
	}
	writeJSON(w, s.file(e))
}

// update renames, moves and trashes an entry. Like Drive it takes a move only as the
// exchange of the one parent for another.
func (s *Server) update(w http.ResponseWriter, r *http.Request, e *entry) {
	var meta struct {
		Name    *string `json:"name"`
		Trashed *bool   `json:"trashed"`
	}
	if err := json.NewDecoder(r.Body).Decode(&meta); err != nil && err != io.EOF {
		writeError(w, http.StatusBadRequest, "parseError", "Parse Error")
		return
	}
	add, remove := r.URL.Query().Get("addParents"), r.URL.Query().Get("removeParents")
	if add != "" {
		if _, ok := s.folder(r, []string{add}); !ok {
			writeError(w, http.StatusNotFound, "notFound", "File not found: "+add+".")
			return
		}
	}
	if (add != "") != (remove != "") || (remove != "" && remove != e.parent) {
		writeError(w, http.StatusForbidden, "cannotAddParent", "Increasing the number of parents is not allowed.")
		return
	}
	if meta.Name != nil {
		e.name = *meta.Name
	}
	if meta.Trashed != nil {
		e.trashed = *meta.Trashed
	}
	if add != "" {
		e.parent = add
	}
	e.version++
	writeJSON(w, s.file(e))
}

// start opens an upload session: for a new file with its metadata in the body, or for
// new content of the file with the given id.
func (s *Server) start(w http.ResponseWriter, r *http.Request, id string) {
	size, err := strconv.ParseInt(r.Header.Get("X-Upload-Content-Length"), 10, 64)
	if err != nil || size < 0 {
		writeError(w, http.StatusBadRequest, "badRequest", "Bad Request")
		return
	}
	u := &session{size: size, target: id}
	if id != "" {
		e, found := s.items[id]
		if !found || !e.binary() || !s.keyGiven(r, e) {
			writeError(w, http.StatusNotFound, "notFound", "File not found: "+id+".")
			return
		}
	} else {
		var meta struct {
			Name    string   `json:"name"`
			Parents []string `json:"parents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&meta); err != nil && err != io.EOF {
			writeError(w, http.StatusBadRequest, "parseError", "Parse Error")
			return
		}
		parent, ok := s.folder(r, meta.Parents)
		if !ok {
			writeError(w, http.StatusNotFound, "notFound", "File not found: "+strings.Join(meta.Parents, ",")+".")
			return
		}
		u.parent, u.name = parent.id, meta.Name
		if u.name == "" {
			u.name = "Untitled"
		}
	}
	if s.full {
		writeFull(w)
		return
	}
	s.n++
	u.id = fmt.Sprintf("session%03d", s.n)
	s.sessions[u.id] = u
	host := s.api.URL
	if s.elsewhere {
		host = s.content.URL
	}
	path := "/upload/drive/v3/files"
	if id != "" {
		path += "/" + id
	}
	w.Header().Set("Location", host+path+"?uploadType=resumable&upload_id="+u.id)
	w.WriteHeader(http.StatusOK)
}

// put takes a range of an upload, or with status only tells where the upload stands.
func (s *Server) put(w http.ResponseWriter, r *http.Request, id string, status bool) {
	u, found := s.sessions[id]
	if !found {
		writeError(w, http.StatusNotFound, "notFound", "Upload session not found.")
		return
	}
	if u.done != "" || status {
		s.progress(w, u)
		return
	}

	var first, last, total int64
	n, err := fmt.Sscanf(r.Header.Get("Content-Range"), "bytes %d-%d/%d", &first, &last, &total)
	length := last - first + 1
	if err != nil || n != 3 || total != u.size || first != int64(len(u.data)) || last < first || last >= total ||
		r.ContentLength != length {
		writeError(w, http.StatusBadRequest, "badContentRange", "Invalid Content-Range.")
		return
	}
	if s.checkRanges && last+1 < total && length%Granularity != 0 {
		writeError(w, http.StatusBadRequest, "badRequest", "The chunk size must be a multiple of 256 KiB.")
		return
	}
	if s.full {
		writeFull(w)
		return
	}
	if _, found := s.items[u.target]; u.target != "" && !found {
		delete(s.sessions, id)
		writeError(w, http.StatusNotFound, "notFound", "File not found: "+u.target+".")
		return
	}

	keep, broken := length, s.breakNext
	if broken {
		s.breakNext = false
		keep = min(s.breakKeep, length)
	}
	body := make([]byte, keep)
	got, err := io.ReadFull(r.Body, body)
	u.data = append(u.data, body[:got]...)
	if err != nil {
		// The connection broke on the side of the client: what arrived stays.
		return
	}
	if broken {
		panic(http.ErrAbortHandler)
	}
	if int64(len(u.data)) == u.size {
		s.complete(u)
	}
	if s.loseNext {
		s.loseNext = false
		panic(http.ErrAbortHandler)
	}
	s.progress(w, u)
}

// complete stores the file of an upload whose bytes have all arrived.
func (s *Server) complete(u *session) {
	if u.target != "" {
		e := s.items[u.target]
		s.clock = s.clock.Add(time.Second)
		e.modTime = s.clock
		e.version++
		s.setContent(e, u.data)
		u.done = e.id
		return
	}
	e := s.add(u.parent, u.name, "application/octet-stream")
	e.revisions = map[string][]byte{}
	s.setContent(e, u.data)
	u.done = e.id
}

// progress answers with where an upload stands: 308 with the bytes that are there in
// Range, or the stored file once it is complete.
func (s *Server) progress(w http.ResponseWriter, u *session) {
	if u.done != "" {
		if e, found := s.items[u.done]; found {
			writeJSON(w, s.file(e))
			return
		}
		writeError(w, http.StatusNotFound, "notFound", "File not found: "+u.done+".")
		return
	}
	if len(u.data) > 0 {
		w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", len(u.data)-1))
	}
	w.WriteHeader(http.StatusPermanentRedirect)
}
