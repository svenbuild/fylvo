package dbfs

import (
	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/activity"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
)

// activityEntry fills the common fields of an activity log entry about target (Fylvo). The
// actor is the current user; a guest of a share link is recorded with the share, taken from
// via, the URI the request came in through. Callers record it after their commit, see
// activity.Record.
func (f *DBFS) activityEntry(typ activity.Type, target *File, via *fs.URI) activity.Entry {
	e := activity.Entry{
		Type:     typ,
		FileID:   target.ID(),
		OwnerID:  target.Model.OwnerID,
		ParentID: target.Model.FileChildren,
		Content: types.ActivityContent{
			Name:     target.DisplayName(),
			IsFolder: target.Type() == types.FileTypeFolder,
		},
	}

	if !inventory.IsAnonymousUser(f.user) {
		e.UserID = f.user.ID
	} else if via != nil && via.FileSystem() == constants.FileSystemShare {
		if id, err := f.hasher.Decode(via.ID(""), hashid.ShareID); err == nil {
			e.ShareID = id
		}
	}

	return e
}

// folderName is the name of a folder for the log; the root folder of a user has none.
func folderName(folder *File) string {
	if folder == nil || folder.IsRootFolder() {
		return ""
	}
	return folder.DisplayName()
}

// movedFrom is where a file was before MoveOrCopy moved it.
type movedFrom struct {
	parent   int
	folder   string
	restored bool
	entry    activity.Entry
}

// moved returns the entry for a file moved to destination: a restore from the trash or a move.
// A move into the folder the file is in already is none (WebDAV renames that way).
func (m movedFrom) moved(destination *File) (activity.Entry, bool) {
	e := m.entry
	e.ParentID = destination.ID()
	e.Content.ToFolder = folderName(destination)
	if m.restored {
		e.Type = activity.FileRestore
		return e, true
	}

	if m.parent == destination.ID() {
		return e, false
	}

	e.FromParentID = m.parent
	e.Content.FromFolder = m.folder
	return e, true
}
