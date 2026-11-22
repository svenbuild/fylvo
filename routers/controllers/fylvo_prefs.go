package controllers

import (
	"encoding/json"

	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/service/fylvo"
	"github.com/gin-gonic/gin"
)

func fylvoRespond(c *gin.Context, data any, err error) {
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		return
	}
	c.JSON(200, serializer.Response{Data: data})
}

// FylvoGetPrefs returns the effective preferences of the current account.
func FylvoGetPrefs(c *gin.Context) {
	res, err := fylvo.GetPrefs(c)
	fylvoRespond(c, res, err)
}

// FylvoPatchPrefs saves some parts of the preferences.
func FylvoPatchPrefs(c *gin.Context) {
	var body map[string]json.RawMessage
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(200, ErrorResponse(err))
		return
	}
	res, err := fylvo.PatchPrefs(c, body)
	fylvoRespond(c, res, err)
}

// FylvoResetPrefs sets the preferences back to the defaults.
func FylvoResetPrefs(c *gin.Context) {
	res, err := fylvo.ResetPrefs(c)
	fylvoRespond(c, res, err)
}

// FylvoListFavorites returns the starred files the user can open.
func FylvoListFavorites(c *gin.Context) {
	res, err := fylvo.ListFavorites(c)
	fylvoRespond(c, res, err)
}

// FylvoAddFavorites stars files.
func FylvoAddFavorites(c *gin.Context) {
	service := ParametersFromContext[*fylvo.FavoritesAddService](c, fylvo.FavoritesAddParamCtx{})
	res, err := service.Add(c)
	fylvoRespond(c, res, err)
}

// FylvoRemoveFavorites unstars files.
func FylvoRemoveFavorites(c *gin.Context) {
	service := ParametersFromContext[*fylvo.FavoritesRemoveService](c, fylvo.FavoritesRemoveParamCtx{})
	res, err := service.Remove(c)
	fylvoRespond(c, res, err)
}

// FylvoListTags returns the tags of the user's own files.
func FylvoListTags(c *gin.Context) {
	res, err := fylvo.ListTags(c)
	fylvoRespond(c, res, err)
}
