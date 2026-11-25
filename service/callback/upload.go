package callback

import (
	"fmt"
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/driver/onedrive"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager"
	"github.com/gin-gonic/gin"
)

// RemoteUploadCallbackService service for remote storage upload callbacks
type RemoteUploadCallbackService struct {
}

// UploadCallbackService service for OSS/Qiniu storage upload callbacks
type UploadCallbackService struct {
	Name       string `json:"name"`
	SourceName string `json:"source_name"`
	PicInfo    string `json:"pic_info"`
	Size       int64  `json:"size"`
}

// UpyunCallbackService service for Upyun upload callbacks
type UpyunCallbackService struct {
	Code       int    `form:"code" binding:"required"`
	Message    string `form:"message" binding:"required"`
	SourceName string `form:"url" binding:"required"`
	Width      string `form:"image-width"`
	Height     string `form:"image-height"`
	Size       uint64 `form:"file_size"`
}

// OneDriveCallback OneDrive client callback body
type OneDriveCallback struct {
	Meta *onedrive.FileInfo
}

// S3Callback S3 client callback body
type S3Callback struct {
}

// ProcessCallback handles the upload result callback
func ProcessCallback(c *gin.Context) error {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := manager.NewFileManager(dep, user)
	defer m.Recycle()

	// get the upload session
	uploadSession := c.MustGet(manager.UploadSessionCtx).(*fs.UploadSession)

	_, err := m.CompleteUpload(c, uploadSession)
	if err != nil {
		return fmt.Errorf("failed to complete upload: %w", err)
	}

	return nil
}

//// PreProcess pre-validates the OneDrive client callback
//func (service *OneDriveCallback) PreProcess(c *gin.Context) serializer.Response {
//	// create the file system
//	fs, err := filesystem.NewFileSystemFromCallback(c)
//	if err != nil {
//		return serializer.ErrDeprecated(serializer.CodeCreateFSError, "", err)
//	}
//	defer fs.Recycle()
//
//	// get the callback session
//	uploadSession := c.MustGet(filesystem.UploadSessionCtx).(*serializer.UploadSession)
//
//	// get file info
//	info, err := fs.Handler.(onedrive.Driver).Client.Meta(context.Background(), "", uploadSession.SavePath)
//	if err != nil {
//		return serializer.ErrDeprecated(serializer.CodeQueryMetaFailed, "", err)
//	}
//
//	// verify it matches the callback session
//	actualPath := strings.TrimPrefix(uploadSession.SavePath, "/")
//	isSizeCheckFailed := uploadSession.Size != info.Size
//
//	// SharePoint adds meta data to Office documents, which changes the file size, so allow 1 MB of tolerance here
//	// See: https://github.com/OneDrive/onedrive-api-docs/issues/935
//	if (strings.Contains(fs.Policy.OptionsSerialized.OdDriver, "sharepoint.com") || strings.Contains(fs.Policy.OptionsSerialized.OdDriver, "sharepoint.cn")) && isSizeCheckFailed && (info.Size > uploadSession.Size) && (info.Size-uploadSession.Size <= 1048576) {
//		isSizeCheckFailed = false
//	}
//
//	if isSizeCheckFailed || !strings.EqualFold(info.GetSourcePath(), actualPath) {
//		fs.Handler.(onedrive.Driver).Client.Delete(context.Background(), []string{info.GetSourcePath()})
//		return serializer.ErrDeprecated(serializer.CodeMetaMismatch, "", err)
//	}
//	service.Meta = info
//	return ProcessCallback(c)
//}
//

//
//// PreProcess pre-processes the S3 client callback
//func (service *S3Callback) PreProcess(c *gin.Context) serializer.Response {
//	// create the file system
//	fs, err := filesystem.NewFileSystemFromCallback(c)
//	if err != nil {
//		return serializer.ErrDeprecated(serializer.CodeCreateFSError, "", err)
//	}
//	defer fs.Recycle()
//
//	// get the callback session
//	uploadSession := c.MustGet(filesystem.UploadSessionCtx).(*serializer.UploadSession)
//
//	// get file info
//	info, err := fs.Handler.(*s3.Driver).Meta(context.Background(), uploadSession.SavePath)
//	if err != nil {
//		return serializer.ErrDeprecated(serializer.CodeMetaMismatch, "", err)
//	}
//
//	// verify the actual file info matches the callback session
//	if uploadSession.Size != info.Size {
//		return serializer.ErrDeprecated(serializer.CodeMetaMismatch, "", err)
//	}
//
//	return ProcessCallback(service, c)
//}
//
//// PreProcess pre-validates the OneDrive client callback
//func (service *UploadCallbackService) PreProcess(c *gin.Context) serializer.Response {
//	// create the file system
//	fs, err := filesystem.NewFileSystemFromCallback(c)
//	if err != nil {
//		return serializer.ErrDeprecated(serializer.CodeCreateFSError, "", err)
//	}
//	defer fs.Recycle()
//
//	// get the callback session
//	uploadSession := c.MustGet(filesystem.UploadSessionCtx).(*serializer.UploadSession)
//
//	// verify the file size
//	if uploadSession.Size != service.Size {
//		fs.Handler.Delete(context.Background(), []string{uploadSession.SavePath})
//		return serializer.ErrDeprecated(serializer.CodeMetaMismatch, "", err)
//	}
//
//	return ProcessCallback(service, c)
//}
