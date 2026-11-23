package explorer

import (
	"strings"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs/dbfs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/workflows"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gin-gonic/gin"
)

// Fylvo: "Copy to" between Fylvo and connected storages runs as a task (see
// workflows/fylvo_connected_copy.go). Here the addresses are checked before it is made.

type (
	ConnectedCopyService struct {
		Src []string `json:"src" binding:"required,min=1,max=1000"`
		Dst string   `json:"dst" binding:"required"`
	}
	ConnectedCopyParamCtx struct{}
)

// copyEnd is one end of a copy: a folder or file of Fylvo or of a connected storage.
type copyEnd struct {
	connection string // "" for Fylvo
	path       string // for a connected storage, below its root
	uri        *fs.URI
}

// contains tells whether b lies at or below a, on the same storage.
func (a copyEnd) contains(b copyEnd, userID string) bool {
	if a.connection != b.connection {
		return false
	}
	if a.connection != "" {
		return a.path == "" || b.path == a.path || strings.HasPrefix(b.path, a.path+"/")
	}
	return b.uri.EqualOrIsDescendantOf(a.uri, userID)
}

// CreateConnectedCopy answers POST /workflow/copy.
func (s *ConnectedCopyService) CreateConnectedCopy(c *gin.Context) (*TaskResponse, error) {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	hasher := dep.HashIDEncoder()
	if !mayUseConnected(user) {
		return nil, connected.ErrNotAllowed
	}
	fm := manager.NewFileManager(dep, user)
	defer fm.Recycle()
	uid := ""
	if user != nil {
		uid = hashid.EncodeUserID(hasher, user.ID)
	}

	// The target: a folder that can take new entries.
	dst, err := s.checkEnd(c, fm, s.Dst, true)
	if err != nil {
		return nil, err
	}
	involvesConnected := dst.connection != ""
	for _, raw := range s.Src {
		src, err := s.checkEnd(c, fm, raw, false)
		if err != nil {
			return nil, err
		}
		if dst.connection == "" && src.connection == "" {
			// Within Fylvo the ordinary copy does it, also beside sources of a storage.
			return nil, serializer.NewError(serializer.CodeParamErr, "Files of Fylvo cannot be copied into Fylvo here", nil)
		}
		involvesConnected = involvesConnected || src.connection != ""
		if src.contains(dst, uid) {
			// Into itself, or below itself.
			return nil, serializer.NewError(serializer.CodeParamErr, "A folder cannot be copied into itself", nil)
		}
	}
	if !involvesConnected {
		// Within Fylvo the ordinary copy does it.
		return nil, serializer.NewError(serializer.CodeParamErr, "Nothing of a connected storage is involved", nil)
	}

	t, err := workflows.NewConnectedCopyTask(c, s.Src, s.Dst)
	if err != nil {
		return nil, serializer.NewError(serializer.CodeCreateTaskError, "Failed to create task", err)
	}
	if err := dep.IoIntenseQueue(c).QueueTask(c, t); err != nil {
		return nil, serializer.NewError(serializer.CodeCreateTaskError, "Failed to queue task", err)
	}
	return BuildTaskResponse(t, nil, hasher), nil
}

// checkEnd checks one address of a copy: that it is there and that the account may read
// it (a source) or put something into it (the target).
func (s *ConnectedCopyService) checkEnd(c *gin.Context, fm manager.FileManager, raw string, target bool) (copyEnd, error) {
	if uri, ok := connectedURI(raw); ok {
		if len(uri.U.Query()) > 0 {
			return copyEnd{}, connected.ErrUnsupported
		}
		req, err := openConnected(c, uri)
		if err != nil {
			return copyEnd{}, err
		}
		defer req.done()
		row, err := req.m.Tree().ResolveNow(req.ctx, req.conn, req.path)
		if err != nil {
			return copyEnd{}, connected.Error(c, err)
		}
		if row.Unsupported != "" {
			return copyEnd{}, connected.ErrUnsupported
		}
		if target {
			// Into a folder of the storage itself, while changes are allowed.
			if !connected.Writes() || !row.Enterable() || row.ViaLink || row.Link != nil {
				return copyEnd{}, connected.ErrUnsupported
			}
		} else if req.path == "" || (row.ViaLink && row.Link == nil) {
			// Not the whole storage, and nothing through a shortcut. A shortcut itself
			// may be chosen: the copy reports it as not followed.
			return copyEnd{}, connected.ErrUnsupported
		}
		// Compared as the provider compares names.
		path := req.path
		if req.conn.P.CaseInsensitive() {
			path = connected.Fold(path)
		}
		return copyEnd{connection: req.conn.ID, path: path, uri: uri}, nil
	}

	uri, err := fs.NewUriFromString(raw)
	if err != nil {
		return copyEnd{}, serializer.NewError(serializer.CodeParamErr, "Invalid address", err)
	}
	fsType := uri.FileSystem()
	if fsType != constants.FileSystemMy && (target || fsType != constants.FileSystemShare && fsType != constants.FileSystemSharedWithMe) {
		// Into the account's own files only (they count against its room); out of them
		// and of what is shared with it.
		return copyEnd{}, serializer.NewError(serializer.CodeParamErr, "Invalid address", nil)
	}
	if uri.U.RawQuery != "" || !target && len(uri.Elements()) == 0 {
		// Not a whole file system.
		return copyEnd{}, serializer.NewError(serializer.CodeParamErr, "Invalid address", nil)
	}
	capability := dbfs.NavigatorCapabilityDownloadFile
	if target {
		capability = dbfs.NavigatorCapabilityUploadFile
	}
	f, err := fm.Get(c, uri, dbfs.WithRequiredCapabilities(capability))
	if err != nil {
		return copyEnd{}, err
	}
	if target && f.Type() != types.FileTypeFolder {
		return copyEnd{}, fs.ErrPathNotExist
	}
	return copyEnd{uri: f.Uri(false)}, nil
}

// CancelConnectedCopy answers DELETE /workflow/copy/:id: the copy stops; what is copied
// stays.
func CancelConnectedCopy(c *gin.Context, taskID int) error {
	dep := dependency.FromContext(c)
	u := inventory.UserFromContext(c)
	t, found := dep.TaskRegistry().Get(taskID)
	if !found || t.Owner() == nil || t.Owner().ID != u.ID {
		return serializer.NewError(serializer.CodeNotFound, "Task not found", nil)
	}
	copyTask, ok := t.(*workflows.ConnectedCopyTask)
	if !ok {
		return serializer.NewError(serializer.CodeNotFound, "Task not found", nil)
	}
	copyTask.Halt()
	return nil
}
