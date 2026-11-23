package controllers

import (
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/service/explorer"
	"github.com/cloudreve/Cloudreve/v4/service/fylvo"
	"github.com/gin-gonic/gin"
)

// Fylvo connected storages.

func fylvoAnswer(c *gin.Context, data any, err error) {
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}
	c.JSON(200, serializer.Response{Data: data})
}

// FylvoConnectedList returns the connected storages of the current account.
func FylvoConnectedList(c *gin.Context) {
	res, err := fylvo.ConnectedList(c)
	fylvoAnswer(c, res, err)
}

// FylvoConnectedAuthorize starts connecting a storage.
func FylvoConnectedAuthorize(c *gin.Context) {
	service := ParametersFromContext[*fylvo.ConnectedAuthorizeService](c, fylvo.ConnectedAuthorizeParamCtx{})
	res, err := service.Authorize(c)
	fylvoAnswer(c, res, err)
}

// FylvoConnectedCallback receives the browser coming back from the provider.
func FylvoConnectedCallback(c *gin.Context) {
	fylvo.ConnectedCallback(c)
}

// FylvoConnectedFinish completes connecting a storage.
func FylvoConnectedFinish(c *gin.Context) {
	service := ParametersFromContext[*fylvo.ConnectedFinishService](c, fylvo.ConnectedFinishParamCtx{})
	res, err := service.Finish(c)
	fylvoAnswer(c, res, err)
}

// FylvoConnectedRename renames a connected storage.
func FylvoConnectedRename(c *gin.Context) {
	service := ParametersFromContext[*fylvo.ConnectedRenameService](c, fylvo.ConnectedRenameParamCtx{})
	res, err := service.Rename(c, c.Param("id"))
	fylvoAnswer(c, res, err)
}

// FylvoConnectedDisconnect disconnects a storage.
func FylvoConnectedDisconnect(c *gin.Context) {
	fylvoAnswer(c, nil, fylvo.ConnectedDisconnect(c, c.Param("id")))
}

// FylvoConnectedCapacity returns the fill level of a connected storage.
func FylvoConnectedCapacity(c *gin.Context) {
	res, err := explorer.ConnectedCapacity(c, c.Param("id"))
	fylvoAnswer(c, res, err)
}

// FylvoConnectedUploadChunk takes a chunk of an upload into a connected storage. The
// chunk that completes the upload is answered with the file.
func FylvoConnectedUploadChunk(c *gin.Context, service *explorer.UploadService) {
	res, err := service.ConnectedUploadChunk(c)
	if err != nil {
		// What is left of the body is the server's to throw away, with its own limits.
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}
	if res == nil {
		c.JSON(200, serializer.Response{})
		return
	}
	c.JSON(200, serializer.Response{Data: res})
}

// FylvoConnectedUploadState tells where an upload into a connected storage stands.
func FylvoConnectedUploadState(c *gin.Context) {
	res, err := explorer.ConnectedUploadState(c, c.Param("session"))
	fylvoAnswer(c, res, err)
}

// FylvoConnectedContent serves the content of a file in a connected storage.
func FylvoConnectedContent(c *gin.Context) {
	explorer.ServeConnectedContent(c)
}

// The calls of the Office server for a file in a connected storage.
func FylvoConnectedOfficeInfo(c *gin.Context)  { explorer.ConnectedOfficeInfo(c) }
func FylvoConnectedOfficeGet(c *gin.Context)   { explorer.ConnectedOfficeGet(c) }
func FylvoConnectedOfficePut(c *gin.Context)   { explorer.ConnectedOfficePut(c) }
func FylvoConnectedOfficeOther(c *gin.Context) { explorer.ConnectedOfficeOther(c) }

// FylvoConnectedAdmin returns the setup of the providers.
func FylvoConnectedAdmin(c *gin.Context) {
	res, err := fylvo.ConnectedAdmin(c)
	fylvoAnswer(c, res, err)
}

// FylvoConnectedAdminSet stores the OAuth client of a provider.
func FylvoConnectedAdminSet(c *gin.Context) {
	service := ParametersFromContext[*fylvo.ConnectedAdminService](c, fylvo.ConnectedAdminParamCtx{})
	res, err := service.Set(c)
	fylvoAnswer(c, res, err)
}
