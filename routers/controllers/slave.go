package controllers

import (
	"errors"
	"fmt"

	"github.com/cloudreve/Cloudreve/v4/pkg/cluster"
	"github.com/cloudreve/Cloudreve/v4/pkg/downloader"
	"github.com/cloudreve/Cloudreve/v4/pkg/downloader/slave"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/request"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/service/admin"
	"github.com/cloudreve/Cloudreve/v4/service/explorer"
	"github.com/cloudreve/Cloudreve/v4/service/node"
	"github.com/gin-gonic/gin"
)

// SlaveUpload uploads a file on the slave node
func SlaveUpload(c *gin.Context) {
	service := ParametersFromContext[*explorer.UploadService](c, explorer.UploadParameterCtx{})
	err := service.SlaveUpload(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		request.BlackHole(c.Request.Body)
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{})
}

// SlaveGetUploadSession creates an upload session on the slave node
func SlaveGetUploadSession(c *gin.Context) {
	service := ParametersFromContext[*explorer.SlaveCreateUploadSessionService](c, explorer.SlaveCreateUploadSessionParamCtx{})
	if err := service.Create(c); err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{})
}

// SlaveDeleteUploadSession deletes an upload session on the slave node
func SlaveDeleteUploadSession(c *gin.Context) {
	service := ParametersFromContext[*explorer.SlaveDeleteUploadSessionService](c, explorer.SlaveDeleteUploadSessionParamCtx{})
	if err := service.Delete(c); err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{})
}

// SlaveServeEntity download entity content
func SlaveServeEntity(c *gin.Context) {
	service := ParametersFromContext[*explorer.EntityDownloadService](c, explorer.EntityDownloadParameterCtx{})
	err := service.SlaveServe(c)
	if err != nil {
		c.JSON(400, serializer.Err(c, err))
		c.Abort()
		return
	}
}

// SlaveMeta retrieve media metadata
func SlaveMeta(c *gin.Context) {
	service := ParametersFromContext[*explorer.SlaveMetaService](c, explorer.SlaveMetaParamCtx{})
	res, err := service.MediaMeta(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.NewResponseWithGobData(c, res))
}

// SlaveThumb gets a file thumbnail on the slave node
func SlaveThumb(c *gin.Context) {
	service := ParametersFromContext[*explorer.SlaveThumbService](c, explorer.SlaveThumbParamCtx{})
	err := service.Thumb(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}
}

// SlaveDelete deletes files on the slave node
func SlaveDelete(c *gin.Context) {
	service := ParametersFromContext[*explorer.SlaveDeleteFileService](c, explorer.SlaveDeleteFileParamCtx{})
	if failed, err := service.Delete(c); err != nil {
		c.JSON(200, serializer.NewResponseWithGobData(c, serializer.Response{
			Code:  serializer.CodeNotFullySuccess,
			Data:  failed,
			Msg:   fmt.Sprintf("Failed to delete %d files(s)", len(failed)),
			Error: err.Error(),
		}))
	} else {
		c.JSON(200, serializer.Response{Data: ""})
	}
}

// SlavePing tests the slave node
func SlavePing(c *gin.Context) {
	service := ParametersFromContext[*admin.SlavePingService](c, admin.SlavePingParameterCtx{})
	if err := service.Test(c); err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{})
}

// SlaveList lists files on the slave node
func SlaveList(c *gin.Context) {
	service := ParametersFromContext[*explorer.SlaveListService](c, explorer.SlaveListParamCtx{})
	objects, err := service.List(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.NewResponseWithGobData(c, objects))
}

// SlaveDownloadTaskCreate creates a download task on slave
func SlaveDownloadTaskCreate(c *gin.Context) {
	service := ParametersFromContext[*slave.CreateSlaveDownload](c, node.CreateSlaveDownloadTaskParamCtx{})
	d := c.MustGet(downloader.DownloaderCtxKey).(downloader.Downloader)
	handle, err := d.CreateTask(c, service.Url, service.Options)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.NewResponseWithGobData(c, handle))
}

// SlaveDownloadTaskStatus queries the Aria2 task status on the slave node
func SlaveDownloadTaskStatus(c *gin.Context) {
	service := ParametersFromContext[*slave.GetSlaveDownload](c, node.GetSlaveDownloadTaskParamCtx{})
	d := c.MustGet(downloader.DownloaderCtxKey).(downloader.Downloader)
	info, err := d.Info(c, service.Handle)
	if err != nil {
		if errors.Is(err, downloader.ErrTaskNotFount) {
			c.JSON(200, serializer.NewError(serializer.CodeNotFound, "task not found", err))
			c.Abort()
			return
		}

		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.NewResponseWithGobData(c, info))
}

// SlaveCancelDownloadTask cancels an offline download task on the slave node
func SlaveCancelDownloadTask(c *gin.Context) {
	service := ParametersFromContext[*slave.CancelSlaveDownload](c, node.CancelSlaveDownloadTaskParamCtx{})
	d := c.MustGet(downloader.DownloaderCtxKey).(downloader.Downloader)
	err := d.Cancel(c, service.Handle)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{})
}

// SlaveSelectFilesToDownload selects offline download files on the slave node
func SlaveSelectFilesToDownload(c *gin.Context) {
	service := ParametersFromContext[*slave.SetSlaveFilesToDownload](c, node.SelectSlaveDownloadFilesParamCtx{})
	d := c.MustGet(downloader.DownloaderCtxKey).(downloader.Downloader)
	err := d.SetFilesToDownload(c, service.Handle, service.Args...)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{})
}

// SlaveTestDownloader tests the downloader connection on the slave node
func SlaveTestDownloader(c *gin.Context) {
	d := c.MustGet(downloader.DownloaderCtxKey).(downloader.Downloader)
	res, err := d.Test(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{Data: res})
}

// SlaveGetOauthCredential gets the OneDrive storage policy credential from the master
func SlaveGetCredential(c *gin.Context) {
	service := ParametersFromContext[*node.OauthCredentialService](c, node.OauthCredentialParamCtx{})
	cred, err := service.Get(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.NewResponseWithGobData(c, cred))
}

// SlaveCreateTask creates tasks and register it in registry
func SlaveCreateTask(c *gin.Context) {
	service := ParametersFromContext[*cluster.CreateSlaveTask](c, node.CreateSlaveTaskParamCtx{})
	taskId, err := node.CreateTaskInSlave(service, c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.NewResponseWithGobData(c, taskId))
}

// SlaveCreateTask creates tasks and register it in registry
func SlaveGetTask(c *gin.Context) {
	service := ParametersFromContext[*node.GetSlaveTaskService](c, node.GetSlaveTaskParamCtx{})
	task, err := service.Get(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.NewResponseWithGobData(c, task))
}

func SlaveCleanupFolder(c *gin.Context) {
	service := ParametersFromContext[*cluster.FolderCleanup](c, node.FolderCleanupParamCtx{})
	if err := node.Cleanup(service, c); err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{})
}

func StatelessPrepareUpload(c *gin.Context) {
	service := ParametersFromContext[*fs.StatelessPrepareUploadService](c, node.StatelessPrepareUploadParamCtx{})
	uploadSession, err := node.StatelessPrepareUpload(service, c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.NewResponseWithGobData(c, uploadSession))
}

func StatelessCompleteUpload(c *gin.Context) {
	service := ParametersFromContext[*fs.StatelessCompleteUploadService](c, node.StatelessCompleteUploadParamCtx{})
	_, err := node.StatelessCompleteUpload(service, c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{})
}

func StatelessOnUploadFailed(c *gin.Context) {
	service := ParametersFromContext[*fs.StatelessOnUploadFailedService](c, node.StatelessOnUploadFailedParamCtx{})
	if err := node.StatelessOnUploadFailed(service, c); err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{})
}

func StatelessCreateFile(c *gin.Context) {
	service := ParametersFromContext[*fs.StatelessCreateFileService](c, node.StatelessCreateFileParamCtx{})
	if err := node.StatelessCreateFile(service, c); err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{})
}
