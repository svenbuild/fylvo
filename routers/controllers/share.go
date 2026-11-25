package controllers

import (
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/service/share"
	"github.com/gin-gonic/gin"
	"net/http"
)

// CreateShare creates a share
func CreateShare(c *gin.Context) {
	service := ParametersFromContext[*share.ShareCreateService](c, share.ShareCreateParamCtx{})
	uri, err := service.Upsert(c, 0)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}

	c.JSON(200, serializer.Response{Data: uri})
}

// EditShare edits a share
func EditShare(c *gin.Context) {
	service := ParametersFromContext[*share.ShareCreateService](c, share.ShareCreateParamCtx{})
	uri, err := service.Upsert(c, hashid.FromContext(c))
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}

	c.JSON(200, serializer.Response{Data: uri})
}

// GetShare views a share
func GetShare(c *gin.Context) {
	service := ParametersFromContext[*share.ShareInfoService](c, share.ShareInfoParamCtx{})
	info, err := service.Get(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}

	c.JSON(200, serializer.Response{Data: info})
}

// ListShare lists shares
func ListShare(c *gin.Context) {
	service := ParametersFromContext[*share.ListShareService](c, share.ListShareParamCtx{})
	resp, err := service.List(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	if resp != nil {
		c.JSON(200, serializer.Response{
			Data: resp,
		})
	}
}

// DeleteShare deletes a share
func DeleteShare(c *gin.Context) {
	err := share.DeleteShare(c, hashid.FromContext(c))
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}

	c.JSON(200, serializer.Response{})
}

func BatchDeleteShare(c *gin.Context) {
	service := ParametersFromContext[*share.BatchDeleteShareService](c, share.BatchDeleteParamCtx{})
	err := service.Delete(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
	}

	c.JSON(200, serializer.Response{})
}

func ShareRedirect(c *gin.Context) {
	service := ParametersFromContext[*share.ShortLinkRedirectService](c, share.ShortLinkRedirectParamCtx{})
	c.Redirect(http.StatusFound, service.RedirectTo(c))
}
