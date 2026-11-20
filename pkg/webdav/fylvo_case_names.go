package webdav

import (
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager"
	"github.com/gin-gonic/gin"
)

// refuseCaseTwin refuses a COPY or MOVE whose destination name is another spelling of a name
// in the destination folder (Fylvo: names that differ only in case, inventory/fylvo_case_names.go).
// The file system refuses it as well, but only after the destination was deleted and, for a
// move under a new name, after the file was moved. A rename of only the case of the source in
// its own folder stays allowed.
func refuseCaseTwin(c *gin.Context, fm manager.FileManager, srcUri, dstUri, dstFolderUri *fs.URI, uid string) error {
	folder, err := fm.Get(c, dstFolderUri)
	if err != nil {
		return nil // the move or copy itself answers that the folder is missing
	}

	twin, err := dependency.FromContext(c).FileClient().CaseTwin(c, folder.ID(), dstUri.Name())
	if err != nil || twin == "" {
		return err
	}
	if twin == srcUri.Name() && srcUri.DirUri().IsSame(dstFolderUri, uid) {
		return nil
	}
	return inventory.ErrNameCaseTaken
}
