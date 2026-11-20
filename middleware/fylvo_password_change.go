package middleware

import (
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-gonic/gin"
)

// dropHeldOrStaleUser replaces the signed-in user with the anonymous one when the account has
// to set a new password, or when the access token was issued for an older password. Routes
// that need a signed-in user then answer "login required".
func dropHeldOrStaleUser(c *gin.Context) error {
	u := inventory.UserFromContext(c)
	if u == nil || inventory.IsAnonymousUser(u) {
		return nil
	}
	if !inventory.MustChangePassword(u) && dependency.FromContext(c).TokenAuth().UserStateValid(c, u) {
		return nil
	}

	util.WithValue(c, inventory.UserIDCtx{}, 0)
	return SetUserCtx(c, 0)
}
