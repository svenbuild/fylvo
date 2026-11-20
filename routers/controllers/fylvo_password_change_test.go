package controllers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/cloudreve/Cloudreve/v4/service/user"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// All sign-in routes (password, 2FA, passkey, password change) end in UserIssueToken. For an
// account that has to set a new password, it answers with a ticket and no tokens.
func TestIssueTokenHoldsAccountThatMustChangePassword(t *testing.T) {
	w := httptest.NewRecorder()
	c, engine := gin.CreateTestContext(w)
	engine.ContextWithFallback = true
	c.Request = httptest.NewRequest("POST", "/api/v4/session/token", nil)
	kv := cache.NewMemoStore("", logging.NewConsoleLogger(logging.LevelError))
	util.WithValue(c, dependency.DepCtx{}, dependency.NewDependency(dependency.WithKV(kv)))
	util.WithValue(c, inventory.UserCtx{}, &ent.User{ID: 7, Password: "hash",
		Settings: &types.UserSetting{FylvoMustChangePassword: true}})

	UserIssueToken(c)

	var res serializer.Response
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Equal(t, serializer.CodePasswordChangeRequired, res.Code)
	require.Equal(t, user.PasswordChangeRequiredMsg, res.Msg)
	ticket, ok := res.Data.(string)
	require.True(t, ok)
	_, stored := kv.Get("fylvo_pw_change_" + ticket)
	require.True(t, stored)
	require.NotContains(t, w.Body.String(), "access_token")
}
