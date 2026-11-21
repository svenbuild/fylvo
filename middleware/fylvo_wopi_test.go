package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/manager"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/cloudreve/Cloudreve/v4/pkg/wopi"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Fylvo: an Office session ends like a sign-in when the password changes or has to change.
func TestOfficeSessionEndsWithNewPassword(t *testing.T) {
	open := func(e *holdEnv, fingerprint string) *gin.Context {
		kv := cache.NewMemoStore("", logging.NewConsoleLogger(logging.LevelError))
		token := "session.secret"
		require.NoError(t, kv.Set(manager.ViewerSessionCachePrefix+"session", manager.ViewerSessionCache{
			ID: "session", UserID: e.user.ID, Token: token, PasswordFingerprint: fingerprint,
		}, 0))
		c, _ := e.context(stateTokenAuth{valid: true})
		c.Request = httptest.NewRequest(http.MethodGet, "/file/wopi/x?"+wopi.AccessTokenQuery+"="+token, nil)
		util.WithValue(c, dependency.DepCtx{}, dependency.NewDependency(
			dependency.WithUserClient(e.users),
			dependency.WithDbClient(e.client),
			dependency.WithKV(kv),
			dependency.WithSettingProvider(setting.NewProvider(setting.NewKvSettingStore(kv,
				setting.NewDbSettingStore(inventory.NewSettingClient(e.client, kv), nil)))),
			dependency.WithLogger(logging.NewConsoleLogger(logging.LevelError)),
		))
		util.WithValue(c, hashid.ObjectIDCtx{}, 99) // another file: a valid session stops there
		ViewerSessionValidation()(c)
		return c
	}
	ended := func(c *gin.Context) bool {
		return c.IsAborted() && c.Writer.Status() == http.StatusUnauthorized &&
			c.Writer.Header().Get(wopi.ServerErrorHeader) == "session ended"
	}

	e := newHoldEnv(t, false)
	current := inventory.PasswordFingerprint(e.user)
	// The session itself is fine; the request then stops at the file.
	valid := open(e, current)
	require.False(t, ended(valid))
	require.Equal(t, "invalid file", valid.Writer.Header().Get(wopi.ServerErrorHeader))
	require.True(t, ended(open(e, "")), "a session without fingerprint is not valid")

	// A new password ends the session.
	require.NoError(t, e.users.SetOwnPassword(context.Background(), e.user, "neues-passwort"))
	require.True(t, ended(open(e, current)))

	// An account that has to set a new password has no session either.
	held := newHoldEnv(t, true)
	require.True(t, ended(open(held, inventory.PasswordFingerprint(held.user))))
}

func TestSessionStillValid(t *testing.T) {
	u := &ent.User{Password: "hash"}
	require.True(t, inventory.SessionStillValid(u, inventory.PasswordFingerprint(u)))
	require.False(t, inventory.SessionStillValid(u, ""))
	require.False(t, inventory.SessionStillValid(nil, inventory.PasswordFingerprint(u)))
	held := &ent.User{Password: "hash", Settings: &types.UserSetting{FylvoMustChangePassword: true}}
	require.False(t, inventory.SessionStillValid(held, inventory.PasswordFingerprint(held)))
	require.False(t, inventory.SessionStillValid(&ent.User{Password: "other"}, inventory.PasswordFingerprint(u)))
}
