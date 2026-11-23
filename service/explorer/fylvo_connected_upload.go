package explorer

import (
	"context"
	"errors"
	"io"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/request"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gin-gonic/gin"
)

// Fylvo: uploads into a connected storage (see pkg/connected/upload.go). The chunks a
// client sends go straight on to the provider; nothing is stored here.

// connectedPolicy is the storage policy a connected storage announces: chunks are sent
// to this server, one after the other, and there is no limit of its own on the size.
func connectedPolicy(req *connectedRequest) *StoragePolicy {
	return &StoragePolicy{ID: "connected", Name: req.conn.Name, Type: types.PolicyTypeLocal, Relay: true}
}

// connectedUploadRequest admits a request to the connection an upload goes into.
func connectedUploadRequest(c context.Context, id string) (*connectedRequest, error) {
	user := inventory.UserFromContext(c)
	if !mayUseConnected(user) {
		return nil, connected.ErrNotAllowed
	}
	conn, err := connected.For(dependency.FromContext(c)).UploadConnection(user.ID, id)
	if err != nil {
		return nil, err
	}
	uri, err := fs.NewAnyUriFromString(connected.RootURI(conn))
	if err != nil {
		return nil, connected.ErrNoStorage
	}
	return openConnected(c, uri)
}

// connectedCreateUpload answers PUT /file/upload.
func connectedCreateUpload(c context.Context, uri *fs.URI, s *CreateUploadSessionService) (*UploadSessionResponse, error) {
	if s.EntityType == "live_photo" {
		return nil, connected.ErrUnsupported
	}
	if len(uri.U.Query()) > 0 {
		return nil, connected.Error(c, connected.ErrBadPath)
	}
	req, err := openConnected(c, uri)
	if err != nil {
		return nil, err
	}
	defer req.done()

	info, err := req.m.StartUpload(req.ctx, req.conn, connected.UploadRequest{
		Path:             req.path,
		Size:             s.Size,
		Replace:          s.EntityType == "version",
		Previous:         s.Previous,
		PreviousRequired: s.PreviousRequired,
	})
	if err != nil {
		return nil, connected.Error(c, err)
	}
	return &UploadSessionResponse{
		SessionID:     info.ID,
		ChunkSize:     info.ChunkSize,
		Expires:       info.Expires.Unix(),
		Uri:           connectedRowURI(req.conn.ID, req.path),
		StoragePolicy: connectedPolicy(req),
	}, nil
}

// ConnectedUploadChunk answers POST /file/upload/:sessionId/:index for an upload into a
// connected storage. The chunk that completes the upload is answered with the file.
func (service *UploadService) ConnectedUploadChunk(c *gin.Context) (*FileResponse, error) {
	req, err := connectedUploadRequest(c, service.ID)
	if err != nil {
		return nil, err
	}
	defer req.done()

	body, length, err := request.SniffContentLength(c.Request)
	if err != nil {
		return nil, connected.ErrChunkLength
	}
	row, err := req.m.UploadChunk(req.ctx, req.conn, service.ID, service.Index, body, length)
	if err != nil || row == nil {
		return nil, connected.Public(c, err)
	}
	return connectedFile(req, row, false), nil
}

// connectedCancelUpload answers DELETE /file/upload.
func connectedCancelUpload(c *gin.Context, uri *fs.URI, s *DeleteUploadSessionService) error {
	if uri == nil || !connected.IsUploadID(s.ID) {
		return connected.ErrNoStorage
	}
	req, err := openConnected(c, uri)
	if err != nil {
		return err
	}
	defer req.done()
	return connected.Public(c, req.m.CancelUpload(req.ctx, req.conn, s.ID))
}

// connectedPutContent answers PUT /file/content: one request that runs a whole upload
// session, from the start to the commit. The body is passed on chunk by chunk as it
// arrives.
func connectedPutContent(c *gin.Context, uri *fs.URI, s *FileUpdateService, body io.Reader, size int64) (*FileResponse, error) {
	if len(uri.U.Query()) > 0 {
		return nil, connected.Error(c, connected.ErrBadPath)
	}
	req, err := openConnected(c, uri)
	if err != nil {
		return nil, err
	}
	defer req.done()

	info, err := req.m.StartUpload(req.ctx, req.conn, connected.UploadRequest{
		Path:             req.path,
		Size:             size,
		Replace:          true,
		Previous:         s.Previous,
		PreviousRequired: s.PreviousRequired,
	})
	if err != nil {
		return nil, connected.Error(c, err)
	}
	for index := 0; ; index++ {
		length := min(info.ChunkSize, size-int64(index)*info.ChunkSize)
		row, err := req.m.UploadChunk(req.ctx, req.conn, info.ID, index, io.LimitReader(body, length), length)
		if err != nil {
			// The body cannot be read a second time. Unless the outcome is open, the
			// upload ends here, and nothing was stored.
			if !errors.Is(err, connected.ErrOutcomeUnknown) {
				_ = req.m.CancelUpload(req.ctx, req.conn, info.ID)
			}
			return nil, connected.Public(c, err)
		}
		if row != nil {
			return connectedFile(req, row, false), nil
		}
	}
}

// ConnectedUploadStateResponse tells where an upload into a connected storage stands.
type ConnectedUploadStateResponse struct {
	State    string `json:"state"`
	Received int64  `json:"received"`
	// File is the stored file, once the upload is done.
	File *FileResponse `json:"file,omitempty"`
	// Code is the error code of a failed upload, or of one whose outcome is unknown.
	Code int `json:"code,omitempty"`
}

// ConnectedUploadState answers GET /fylvo/connected/upload/:session.
func ConnectedUploadState(c *gin.Context, id string) (*ConnectedUploadStateResponse, error) {
	user := inventory.UserFromContext(c)
	if !mayUseConnected(user) {
		return nil, connected.ErrNotAllowed
	}
	status, err := connected.For(dependency.FromContext(c)).UploadStatus(user.ID, id)
	if err != nil {
		return nil, err
	}
	res := &ConnectedUploadStateResponse{State: status.State, Received: status.Received}
	if status.Row != nil {
		req := &connectedRequest{conn: &connected.Conn{ID: status.Conn}}
		res.File = connectedFile(req, status.Row, false)
	}
	var app serializer.AppError
	if errors.As(status.Err, &app) {
		res.Code = app.Code
	}
	return res, nil
}
