package controllers

import (
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/cloudreve/Cloudreve/v4/service/user"
	"github.com/gin-gonic/gin"
)

// UserFinishPasswordChange Fylvo: stores the new password of a held sign-in, then UserIssueToken
// signs the person in.
func UserFinishPasswordChange(c *gin.Context) {
	service := ParametersFromContext[*user.PasswordChangeService](c, user.PasswordChangeParameterCtx{})
	u, err := service.Finish(c)
	if err != nil {
		c.JSON(200, serializer.Err(c, err))
		c.Abort()
		return
	}

	util.WithValue(c, inventory.UserCtx{}, u)
}
