package controllers

import (
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/service/fylvo"
	"github.com/gin-gonic/gin"
)

// FylvoFileActivity returns the history of a file or folder (Fylvo activity log).
func FylvoFileActivity(c *gin.Context) {
	service := ParametersFromContext[*fylvo.FileActivityService](c, fylvo.FileActivityParamCtx{})
	res, err := service.List(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}
	c.JSON(200, serializer.Response{Data: res})
}

// FylvoOwnActivity returns the recent sign-ins of the current account.
func FylvoOwnActivity(c *gin.Context) {
	service := ParametersFromContext[*fylvo.OwnActivityService](c, fylvo.OwnActivityParamCtx{})
	res, err := service.List(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}
	c.JSON(200, serializer.Response{Data: res})
}

// FylvoAdminActivity returns all activity log entries, for the admin.
func FylvoAdminActivity(c *gin.Context) {
	service := ParametersFromContext[*fylvo.AdminActivityService](c, fylvo.AdminActivityParamCtx{})
	res, err := service.List(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}
	c.JSON(200, serializer.Response{Data: res})
}
