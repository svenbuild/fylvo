package controllers

import (
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/service/setting"
	"github.com/gin-gonic/gin"
)

// ListDavAccounts lists all WebDAV accounts.
func ListDavAccounts(c *gin.Context) {
	service := ParametersFromContext[*setting.ListDavAccountsService](c, setting.ListDavAccountParamCtx{})
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

// CreateDAVAccounts creates a WebDAV account
func CreateDAVAccounts(c *gin.Context) {
	service := ParametersFromContext[*setting.CreateDavAccountService](c, setting.CreateDavAccountParamCtx{})
	resp, err := service.Create(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{
		Data: resp,
	})
}

// UpdateDAVAccounts updates WebDAV accounts.
func UpdateDAVAccounts(c *gin.Context) {
	service := ParametersFromContext[*setting.CreateDavAccountService](c, setting.CreateDavAccountParamCtx{})
	resp, err := service.Update(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{
		Data: resp,
	})
}

// DeleteDAVAccounts deletes WebDAV accounts.
func DeleteDAVAccounts(c *gin.Context) {
	err := setting.DeleteDavAccount(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	c.JSON(200, serializer.Response{})
}

//
//// DeleteWebDAVAccounts deletes a WebDAV account
//func DeleteWebDAVAccounts(c *gin.Context) {
//	var service setting.WebDAVAccountService
//	if err := c.ShouldBindUri(&service); err == nil {
//		res := service.Delete(c, CurrentUser(c))
//		c.JSON(200, res)
//	} else {
//		c.JSON(200, ErrorResponse(err))
//	}
//}
//
//// DeleteWebDAVMounts deletes a WebDAV mount
//func DeleteWebDAVMounts(c *gin.Context) {
//	var service setting.WebDAVListService
//	if err := c.ShouldBindUri(&service); err == nil {
//		res := service.Unmount(c, CurrentUser(c))
//		c.JSON(200, res)
//	} else {
//		c.JSON(200, ErrorResponse(err))
//	}
//}
//
//
//// CreateWebDAVMounts creates a WebDAV folder mount
//func CreateWebDAVMounts(c *gin.Context) {
//	var service setting.WebDAVMountCreateService
//	if err := c.ShouldBindJSON(&service); err == nil {
//		res := service.Create(c, CurrentUser(c))
//		c.JSON(200, res)
//	} else {
//		c.JSON(200, ErrorResponse(err))
//	}
//}
