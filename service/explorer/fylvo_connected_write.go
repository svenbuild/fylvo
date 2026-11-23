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

// connectedIDs are the ids a client sends with the addresses of a change: the keys are
// the exact strings it sent as addresses.
type connectedIDs map[string]string

// connectionOf returns the id of the connection an address names.
func connectionOf(uri *fs.URI) string {
	if uri.U.User == nil {
		return ""
	}
	return uri.U.User.Username()
}

// connectedSources turns the addresses of a change into sources. All of them have to be
// in one connection, which the first address opens.
func connectedSources(c *gin.Context, raws []string, uris []*fs.URI, ids connectedIDs) (*connectedRequest, []connected.Source, error) {
	if len(uris) > connected.MaxBatch+1 {
		return nil, nil, serializer.NewError(serializer.CodeParamErr, "Too many entries in one request", nil)
	}
	req, err := openConnected(c, uris[0])
	if err != nil {
		return nil, nil, err
	}
	sources := make([]connected.Source, 0, len(uris))
	seen := map[string]bool{}
	for i, uri := range uris {
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
		if err != nil {
			req.done()
			return nil, nil, connected.Error(c, err)
		}
		sources = append(sources, connected.Source{Path: path, ID: ids[raws[i]]})
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

	row, err := req.m.RenameEntry(req.ctx, req.conn, connected.Source{Path: req.path, ID: s.Ids[s.Uri]}, s.NewName)
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
	req, sources, err := connectedSources(c, append(append([]string{}, s.Uris...), s.Dst), append(append([]*fs.URI{}, uris...), dst), s.Ids)
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
	req, sources, err := connectedSources(c, s.Uris, uris, s.Ids)
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
