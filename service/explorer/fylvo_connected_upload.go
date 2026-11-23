package explorer

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
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
	uri, ok := connectedURI(connected.RootURI(conn))
	if !ok {
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
	if err != nil || c.Request.ContentLength != length {
		// The length of the body has to be known, and be the one that is announced: a
		// body without one would be taken for an empty chunk.
		return nil, connected.ErrChunkLength
	}
	row, err := req.m.UploadChunk(req.ctx, req.conn, service.ID, service.Index, movingBody(c, body), length)
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

// meantForConnected tells whether an address names the connected storages, also when it
// is no address one of them has (connectedURI refuses it).
func meantForConnected(raw string) bool {
	return strings.Contains(raw, "@"+string(constants.FileSystemConnected))
}

// connectedPutContent answers PUT /file/content: one request that runs a whole upload
// session, from the start to the commit. The body is passed on chunk by chunk as it
// arrives.
func connectedPutContent(c *gin.Context, uri *fs.URI, s *FileUpdateService) (*FileResponse, error) {
	// Whoever throws away what is left of the body after an error does not wait for a
	// client that stands still. Set before anything can go wrong, the length included.
	defer func() { _ = http.NewResponseController(c.Writer).SetReadDeadline(time.Now().Add(connectedIdle)) }()
	body, size, err := request.SniffContentLength(c.Request)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeParamErr, "invalid content length", err)
	}
	if size > dependency.FromContext(c).SettingProvider().MaxOnlineEditSize(c) {
		return nil, fs.ErrFileSizeTooBig
	}
	if len(uri.U.Query()) > 0 {
		return nil, connected.Error(c, connected.ErrBadPath)
	}
	// The length has to be known before anything is opened: a body of unknown length
	// would be taken for an empty file.
	if size < 0 || c.Request.ContentLength != size {
		return nil, connected.ErrChunkLength
	}
	moving := movingBody(c, body)
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
		row, err := req.m.UploadChunk(req.ctx, req.conn, info.ID, index, io.LimitReader(moving, length), length)
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

// movingBody is the body of a request with a limit on how long the client may stand
// still in the middle of it: a client that stops sending must not hold one of the few
// places for uploads.
func movingBody(c *gin.Context, body io.Reader) io.Reader {
	return &stallingReader{r: body, control: http.NewResponseController(c.Writer), end: time.Now().Add(connectedBodyTime)}
}

// connectedBodyTime is how long the body of one request may take in all, however it moves.
const connectedBodyTime = 30 * time.Minute

type stallingReader struct {
	r       io.Reader
	control *http.ResponseController
	// end is when the body has to be through: a client that sends a byte now and then
	// stands still as far as everybody else is concerned.
	end time.Time
}

func (s *stallingReader) Read(p []byte) (int, error) {
	// Not every connection can take a deadline (a recorder in a test cannot). It is
	// for this read only: what the request does after its body must not run into it.
	deadline := time.Now().Add(connectedIdle)
	if s.end.Before(deadline) {
		deadline = s.end
	}
	_ = s.control.SetReadDeadline(deadline)
	defer func() { _ = s.control.SetReadDeadline(time.Time{}) }()
	return s.r.Read(p)
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
	status, err := connected.For(dependency.FromContext(c)).UploadStatus(c, user.ID, id)
	if err != nil {
		return nil, err
	}
	res := &ConnectedUploadStateResponse{State: status.State, Received: status.Received}
	if status.Row != nil {
		req := &connectedRequest{conn: &connected.Conn{ID: status.Conn, Provider: status.Provider}}
		res.File = connectedFile(req, status.Row, false)
	}
	var app serializer.AppError
	if errors.As(status.Err, &app) {
		res.Code = app.Code
	}
	return res, nil
}
