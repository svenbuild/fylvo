package graphtest

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

func (s *Server) serveAPI(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	token, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !bearer {
		token = ""
	}
	s.tokens = append(s.tokens, token)

	route := parseRoute(r.URL)
	if route.kind != "" {
		s.calls[route.kind]++
	}
	if s.unauthorized || (s.requireBearer && token == "") {
		s.unauthorized = false
		writeError(w, http.StatusUnauthorized, "invalidAuthenticationToken", "A bearer token is required.")
		return
	}
	if s.throttled > 0 {
		s.throttled--
		w.Header().Set("Retry-After", "0")
		writeError(w, http.StatusTooManyRequests, "activityLimitReached", "Try again later.")
		return
	}
	if r.Method != http.MethodGet {
		s.serveWrite(w, r, route)
		return
	}

	if route.drive == driveID {
		switch route.kind {
		case "root":
			writeJSON(w, http.StatusOK, itemJSON(s.items[rootID]))
			return
		case "quota":
			s.serveQuota(w)
			return
		}
	}
	entry := s.items[route.id]
	if entry != nil && entry.drive == route.drive {
		switch route.kind {
		case "item":
			if route.named {
				entry = s.named(entry, route.name)
			}
			if entry != nil {
				writeJSON(w, http.StatusOK, itemJSON(entry))
				return
			}
		case "children":
			if entry.isFolder() && !entry.deleted {
				s.serveChildren(w, r, entry)
				return
			}
		case "content":
			if entry.kind == file && !entry.deleted {
				http.Redirect(w, r, s.download.URL+"/"+url.PathEscape(entry.id), http.StatusFound)
				return
			}
		}
	}
	notFound(w)
}

type apiRoute struct {
	kind, drive, id, name string
	named                 bool
}

func parseRoute(u *url.URL) apiRoute {
	// Split before unescaping so an escaped separator stays inside its segment.
	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	if len(parts) < 3 || parts[0] != "v1.0" || parts[1] != "drives" {
		return apiRoute{}
	}
	drive, _ := url.PathUnescape(parts[2])
	switch {
	case len(parts) == 3:
		return apiRoute{kind: "quota", drive: drive}
	case len(parts) == 4 && parts[3] == "root":
		return apiRoute{kind: "root", drive: drive}
	case len(parts) < 5 || parts[3] != "items":
		return apiRoute{}
	}
	id, isNamed := strings.CutSuffix(parts[4], ":")
	id, _ = url.PathUnescape(id)
	switch {
	case len(parts) == 5 && !isNamed:
		return apiRoute{kind: "item", drive: drive, id: id}
	case len(parts) == 6 && isNamed && parts[5] != "":
		name, _ := url.PathUnescape(parts[5])
		return apiRoute{kind: "item", drive: drive, id: id, name: name, named: true}
	case len(parts) == 6 && !isNamed && (parts[5] == "children" || parts[5] == "content" || parts[5] == "createUploadSession"):
		return apiRoute{kind: parts[5], drive: drive, id: id}
	case len(parts) == 7 && isNamed && (parts[6] == "content" || parts[6] == "createUploadSession"):
		name, named := strings.CutSuffix(parts[5], ":")
		if named && name != "" {
			name, _ = url.PathUnescape(name)
			return apiRoute{kind: parts[6], drive: drive, id: id, name: name, named: true}
		}
	}
	return apiRoute{}
}

func (s *Server) named(parent *item, name string) *item {
	if parent == nil || !parent.isFolder() || parent.deleted {
		return nil
	}
	return s.findChild(parent.id, name, "")
}

func (s *Server) findChild(parentID, name, exceptID string) *item {
	for _, entry := range s.items {
		if entry.parent == parentID && entry.id != exceptID && !entry.deleted && strings.EqualFold(entry.name, name) {
			return entry
		}
	}
	return nil
}

func (s *Server) serveChildren(w http.ResponseWriter, r *http.Request, parent *item) {
	var ids []string
	for id, entry := range s.items {
		if entry.parent == parent.id {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)

	query := r.URL.Query()
	size, _ := strconv.Atoi(query.Get("$top"))
	if size <= 0 {
		size = 200
	}
	if s.pageSize > 0 {
		size = min(size, s.pageSize)
	}
	start := 0
	if token := query.Get("$skiptoken"); token != "" {
		var err error
		start, err = strconv.Atoi(token)
		if err != nil || start < 0 || start > len(ids) {
			writeError(w, http.StatusBadRequest, "invalidRequest", "The page token is invalid.")
			return
		}
	}
	end := start + min(size, len(ids)-start)
	items := make([]map[string]any, 0, end-start)
	for _, id := range ids[start:end] {
		items = append(items, itemJSON(s.items[id]))
	}
	answer := map[string]any{"value": items}
	if end < len(ids) {
		query.Set("$skiptoken", strconv.Itoa(end))
		answer["@odata.nextLink"] = s.URL() + r.URL.EscapedPath() + "?" + query.Encode()
	}
	writeJSON(w, http.StatusOK, answer)
}

func (s *Server) serveQuota(w http.ResponseWriter) {
	var used int64
	for _, entry := range s.items {
		if entry.drive == driveID && entry.kind == file && !entry.deleted {
			used += int64(len(entry.content))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"quota": map[string]any{"used": used, "total": quotaTotal}})
}

func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request) {
	if id, ok := strings.CutPrefix(r.URL.Path, "/upload/"); ok {
		s.serveUpload(w, r, id)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/")
	s.mu.Lock()
	s.downloadAuth = s.downloadAuth || r.Header.Get("Authorization") != ""
	var content []byte
	entry := s.items[id]
	found := entry != nil && entry.kind == file && !entry.deleted
	if found {
		content = entry.content
	}
	s.mu.Unlock()

	if !found {
		notFound(w)
		return
	}
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(content))
}

func itemJSON(entry *item) map[string]any {
	result := map[string]any{
		"id": entry.id, "name": entry.name, "size": len(entry.content),
		"lastModifiedDateTime": entry.itemTime,
		"eTag":                 entry.eTag,
		"parentReference":      map[string]string{"driveId": entry.drive, "id": entry.parent},
		"webUrl":               "https://example.com/items/" + url.PathEscape(entry.id),
	}
	switch {
	case entry.isFolder():
		result["folder"] = struct{}{}
	case entry.kind == file:
		result["file"] = map[string]any{"hashes": map[string]string{"quickXorHash": entry.hash}}
		result["cTag"] = entry.cTag
		result["fileSystemInfo"] = map[string]any{"lastModifiedDateTime": entry.fileTime}
	case entry.kind == remote:
		result["remoteItem"] = itemJSON(entry.target)
	case entry.kind == notebook:
		result["package"] = map[string]string{"type": "oneNote"}
	}
	if entry.kind == vault {
		result["specialFolder"] = map[string]string{"name": "vault"}
	}
	if entry.deleted {
		result["deleted"] = map[string]string{"state": "deleted"}
	}
	return result
}

func notFound(w http.ResponseWriter) {
	writeError(w, http.StatusNotFound, "itemNotFound", "The requested item is missing.")
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
