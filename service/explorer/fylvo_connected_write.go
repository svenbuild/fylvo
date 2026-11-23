package explorer

import (
	"strings"

	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gin-gonic/gin"
)

// Fylvo: the routes that change something in a connected storage (see pkg/connected).
//
// Rename, move and delete need "ids": for each address the id of the row as the client
// last listed it. The server compares it with what the address leads to now and changes
// nothing when they differ.

// connectedIDs are the ids a client sends with the addresses of a change, by address.
type connectedIDs map[string]string

// addressKey names an address by what it leads to, so "Trial x" and "Trial%20x" are one.
func addressKey(uri *fs.URI, fold bool) (string, bool) {
	path, err := connected.CheckPath(uri.U.Path)
	if err != nil || len(uri.U.Query()) > 0 {
		return "", false
	}
	if fold {
		// Where the provider takes "Docs" and "docs" for one name, they are one address.
		path = strings.ToLower(path)
	}
	return connectionOf(uri) + "\x00" + path, true
}

// idsByAddress are the ids of a request by the address their keys name. unclear are the
// addresses that got two different ids.
type idsByAddress struct {
	ids     map[string]string
	unclear map[string]bool
	// fold: the provider of the connection does not tell names apart by case.
	fold bool
}

// byAddress sorts the ids by the address their keys name. A client may spell the key
// of an address differently from the address it sends (escaped or not). Two keys for
// one address with different ids contradict each other: a change that names this
// address is refused, also where an id would have been optional.
func (ids connectedIDs) byAddress(fold bool) idsByAddress {
	sorted, unclear := make(map[string]string, len(ids)), map[string]bool{}
	for raw, id := range ids {
		uri, ok := connectedURI(raw)
		if !ok {
			continue
		}
		key, ok := addressKey(uri, fold)
		if !ok {
			continue
		}
		if before, twice := sorted[key]; twice && before != id {
			unclear[key] = true
		}
		sorted[key] = id
	}
	return idsByAddress{ids: sorted, unclear: unclear, fold: fold}
}

// of returns the id sent for an address, "" when there is none, and an error when the
// request names two.
func (s idsByAddress) of(uri *fs.URI) (string, error) {
	key, ok := addressKey(uri, s.fold)
	if !ok {
		return "", nil
	}
	if s.unclear[key] {
		return "", connected.ItemError(connected.ReasonChanged)
	}
	return s.ids[key], nil
}

// errConnectedBatch is the answer to a request that names more entries than one request
// moves or deletes.
var errConnectedBatch = serializer.NewError(serializer.CodeParamErr, "Too many entries in one request", nil)

// connectionOf returns the id of the connection an address names.
func connectionOf(uri *fs.URI) string {
	if uri.U.User == nil {
		return ""
	}
	return uri.U.User.Username()
}

// connectedSources turns the addresses of a change into sources. All of them have to be
// in one connection, which the first address opens.
func connectedSources(c *gin.Context, uris []*fs.URI, ids connectedIDs) (*connectedRequest, []connected.Source, error) {
	req, err := openConnected(c, uris[0])
	if err != nil {
		return nil, nil, err
	}
	sources := make([]connected.Source, 0, len(uris))
	seen, sorted := map[string]bool{}, ids.byAddress(req.conn.P.CaseInsensitive())
	for _, uri := range uris {
		path, err := connected.CheckPath(uri.U.Path)
		key := path
		if req.conn.P.CaseInsensitive() {
			key = strings.ToLower(path)
		}
		if err == nil && seen[key] {
			// One entry named twice: which of the two results would be whose?
			err = connected.ErrBadPath
		}
		seen[key] = true
		if err == nil && connectionOf(uri) != req.conn.ID {
			// Nothing works across two storages.
			err = connected.ErrUnsupported
		}
		if err == nil && len(uri.U.Query()) > 0 {
			err = connected.ErrBadPath
		}
		id, idErr := sorted.of(uri)
		if err == nil {
			err = idErr
		}
		if err != nil {
			req.done()
			return nil, nil, connected.Error(c, err)
		}
		sources = append(sources, connected.Source{Path: path, ID: id})
	}
	return req, sources, nil
}

// connectedFailures turns the entries a change could not be made for into the answer
// for a batch: one error per address.
func connectedFailures(c *gin.Context, raws []string, results []error) error {
	ae := serializer.NewAggregateError()
	for i, err := range results {
		if err != nil {
			ae.Add(raws[i], connected.Public(c, err))
		}
	}
	return ae.Aggregate()
}

// connectedCreate answers POST /file/create: a new folder or an empty file.
func connectedCreate(c *gin.Context, uri *fs.URI, s *CreateFileService) (*FileResponse, error) {
	req, err := openConnected(c, uri)
	if err != nil {
		return nil, err
	}
	defer req.done()

	dir := types.FileTypeFromString(s.Type) == types.FileTypeFolder
	row, err := req.m.CreateEntry(req.ctx, req.conn, req.path, dir, s.ErrOnConflict)
	if err != nil {
		return nil, connected.Error(c, err)
	}
	return connectedFile(req, row, false), nil
}

// connectedRename answers POST /file/rename.
func connectedRename(c *gin.Context, uri *fs.URI, s *RenameFileService) (*FileResponse, error) {
	req, err := openConnected(c, uri)
	if err != nil {
		return nil, err
	}
	defer req.done()

	id, err := s.Ids.byAddress(req.conn.P.CaseInsensitive()).of(uri)
	if err != nil {
		return nil, err
	}
	row, err := req.m.RenameEntry(req.ctx, req.conn, connected.Source{Path: req.path, ID: id}, s.NewName)
	if err != nil {
		return nil, connected.Error(c, err)
	}
	return connectedFile(req, row, false), nil
}

// connectedMove answers POST /file/move for entries of one connected storage that go to
// another folder of it. Copies, and anything between a connected storage and another
// place, are not offered.
func connectedMove(c *gin.Context, uris []*fs.URI, dst *fs.URI, s *MoveFileService) error {
	if s.Copy || dst == nil || len(uris) == 0 {
		return connected.ErrUnsupported
	}
	if len(uris) > connected.MaxBatch {
		return errConnectedBatch
	}
	req, sources, err := connectedSources(c, append(append([]*fs.URI{}, uris...), dst), s.Ids)
	if err != nil {
		return err
	}
	defer req.done()

	target := sources[len(sources)-1]
	sources = sources[:len(sources)-1]
	failed, err := req.m.MoveEntries(req.ctx, req.conn, sources, target)
	if err != nil {
		return connected.Error(c, err)
	}
	return connectedFailures(c, s.Uris, failed)
}

// connectedDelete answers DELETE /file: the entries go into the trash of the provider.
func connectedDelete(c *gin.Context, uris []*fs.URI, s *DeleteFileService) error {
	if len(uris) > connected.MaxBatch {
		return errConnectedBatch
	}
	req, sources, err := connectedSources(c, uris, s.Ids)
	if err != nil {
		return err
	}
	defer req.done()

	failed, err := req.m.TrashEntries(req.ctx, req.conn, sources)
	if err != nil {
		return connected.Error(c, err)
	}
	return connectedFailures(c, s.Uris, failed)
}
