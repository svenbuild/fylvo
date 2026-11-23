package graphtest

import (
	"encoding/json"
	"net/http"
)

const conflictBehavior = "@microsoft.graph.conflictBehavior"

func (s *Server) serveWrite(w http.ResponseWriter, r *http.Request, route apiRoute) {
	entry := s.items[route.id]
	if route.drive != driveID || entry == nil || entry.drive != driveID || entry.deleted {
		notFound(w)
		return
	}
	switch {
	case r.Method == http.MethodPost && route.kind == "children":
		var in struct {
			Name     string    `json:"name"`
			Folder   *struct{} `json:"folder"`
			Behavior string    `json:"@microsoft.graph.conflictBehavior"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil || in.Name == "" || in.Folder == nil || in.Behavior != "fail" {
			badWrite(w)
			return
		}
		s.create(w, entry, in.Name, folder)
	case r.Method == http.MethodPut && route.kind == "content" && route.named:
		if r.ContentLength != 0 || r.URL.Query().Get(conflictBehavior) != "fail" {
			badWrite(w)
			return
		}
		s.create(w, entry, route.name, file)
	case r.Method == http.MethodPatch && route.kind == "item" && !route.named:
		s.update(w, r, entry)
	case r.Method == http.MethodDelete && route.kind == "item" && !route.named:
		if entry.id == rootID {
			badWrite(w)
			return
		}
		entry.deleted = true
		s.touch(entry)
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPost && route.kind == "createUploadSession":
		s.startUpload(w, r, route, entry)
	default:
		writeError(w, http.StatusMethodNotAllowed, "notSupported", "This method is unavailable.")
	}
}

func (s *Server) create(w http.ResponseWriter, parent *item, name string, kind itemKind) {
	if !parent.isFolder() {
		badWrite(w)
		return
	}
	if s.named(parent, name) != nil {
		nameTaken(w)
		return
	}
	entry := s.addItem(parent.id, name, kind, nil, nil)
	writeJSON(w, http.StatusCreated, itemJSON(entry))
}

func (s *Server) update(w http.ResponseWriter, r *http.Request, entry *item) {
	var in struct {
		Name            string `json:"name"`
		ParentReference struct {
			ID string `json:"id"`
		} `json:"parentReference"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&in) != nil || entry.id == rootID {
		badWrite(w)
		return
	}
	name, parent := entry.name, s.items[entry.parent]
	if in.Name != "" {
		name = in.Name
	}
	if in.ParentReference.ID != "" {
		parent = s.items[in.ParentReference.ID]
	}
	if parent == nil || parent.deleted || parent.drive != driveID {
		notFound(w)
		return
	}
	if !parent.isFolder() || s.within(parent, entry) {
		badWrite(w)
		return
	}
	if s.findChild(parent.id, name, entry.id) != nil {
		nameTaken(w)
		return
	}
	entry.name, entry.parent = name, parent.id
	s.touch(entry)
	writeJSON(w, http.StatusOK, itemJSON(entry))
}

func badWrite(w http.ResponseWriter) {
	writeError(w, http.StatusBadRequest, "invalidRequest", "The write request is invalid.")
}

func nameTaken(w http.ResponseWriter) {
	writeError(w, http.StatusConflict, "nameAlreadyExists", "That name is already in use.")
}
