package webdav

import (
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
)

// refuseCaseTwin refuses a COPY or MOVE that the file system would refuse for a name that is
// another spelling of a name in the destination folder (Fylvo: names that differ only in case,
// inventory/fylvo_case_names.go). The file system refuses it too, but only after the
// destination was deleted. performCopyMove first brings the source in under its own name and
// renames it after, so both names are checked. Not counted: the destination that is deleted
// first, and for a MOVE in its own folder the source itself (a rename of only its case).
func refuseCaseTwin(c *gin.Context, fm manager.FileManager, srcUri, dstUri, dstFolderUri *fs.URI,
	isCopy, dstExists bool, uid string) error {
	folder, err := fm.Get(c, dstFolderUri)
	if err != nil {
		return nil // the move or copy itself answers that the folder is missing
	}

	sameFolder := srcUri.DirUri().IsSame(dstFolderUri, uid)
	names := []string{dstUri.Name()}
	if !sameFolder && srcUri.Name() != dstUri.Name() {
		names = append(names, srcUri.Name())
	}

	files := dependency.FromContext(c).FileClient()
	for _, name := range names {
		twins, err := files.CaseTwins(c, folder.ID(), name)
		if err != nil {
			return err
		}
		twins = lo.Filter(twins, func(twin string, _ int) bool {
			return !(dstExists && twin == dstUri.Name()) && !(!isCopy && sameFolder && twin == srcUri.Name())
		})
		if len(twins) > 0 {
			return inventory.ErrNameCaseTaken
		}
	}
	return nil
}
