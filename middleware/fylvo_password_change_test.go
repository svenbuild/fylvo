package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/auth"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// stateTokenAuth is a TokenAuth whose access token matches the password or not.
type stateTokenAuth struct {
	auth.TokenAuth
	valid bool
}

func (s stateTokenAuth) UserStateValid(*gin.Context, *ent.User) bool { return s.valid }

type holdEnv struct {
	client *ent.Client
	users  inventory.UserClient
	user   *ent.User
}

func newHoldEnv(t *testing.T, held bool) *holdEnv {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	webdav := &boolset.BooleanSet{}
	boolset.Set(types.GroupPermissionWebDAV, true, webdav)
	for _, name := range []string{"Admin", "User", "Anonymous"} {
		_, err := client.Group.Create().SetName(name).SetPermissions(webdav).Save(ctx)
		require.NoError(t, err)
	}

	users := inventory.NewUserClient(client)
	u, err := users.Upsert(ctx, &ent.User{Email: "neu@fylvo.local", Status: user.StatusActive, GroupUsers: 2},
		"erstes-passwort", "", &types.UserSetting{FylvoMustChangePassword: held})
	require.NoError(t, err)
	_, err = client.DavAccount.Create().SetOwnerID(u.ID).SetName("Handy").SetURI("cloudreve://my").
		SetPassword("app-passwort").SetOptions(&boolset.BooleanSet{}).Save(ctx)
	require.NoError(t, err)
	return &holdEnv{client: client, users: users, user: u}
}

func (e *holdEnv) context(tokenAuth auth.TokenAuth) (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, engine := gin.CreateTestContext(w)
	engine.ContextWithFallback = true
	c.Request = httptest.NewRequest("PROPFIND", "/dav/", nil)
	util.WithValue(c, dependency.DepCtx{}, dependency.NewDependency(
		dependency.WithUserClient(e.users),
		dependency.WithDbClient(e.client),
		dependency.WithTokenAuth(tokenAuth),
		dependency.WithLogger(logging.NewConsoleLogger(logging.LevelError)),
	))
	return c, w
}

func (e *holdEnv) signedIn(t *testing.T, tokenAuth auth.TokenAuth) *ent.User {
	c, _ := e.context(tokenAuth)
	util.WithValue(c, inventory.UserIDCtx{}, e.user.ID)
	require.NoError(t, SetUserCtx(c, e.user.ID))
	require.NoError(t, dropHeldOrStaleUser(c))
	require.Equal(t, inventory.UserFromContext(c).ID, inventory.UserIDFromContext(c))
	return inventory.UserFromContext(c)
}

func TestHeldAccountCountsAsSignedOut(t *testing.T) {
	require.True(t, inventory.IsAnonymousUser(newHoldEnv(t, true).signedIn(t, stateTokenAuth{valid: true})))
}

func TestTokenForAnOlderPasswordCountsAsSignedOut(t *testing.T) {
	e := newHoldEnv(t, false)
	require.True(t, inventory.IsAnonymousUser(e.signedIn(t, stateTokenAuth{valid: false})))
	require.Equal(t, e.user.ID, e.signedIn(t, stateTokenAuth{valid: true}).ID)
}

func TestWebDAVRefusesHeldAccount(t *testing.T) {
	for _, held := range []bool{true, false} {
		e := newHoldEnv(t, held)
		c, _ := e.context(stateTokenAuth{valid: true})
		c.Request.SetBasicAuth("neu@fylvo.local", "app-passwort")
		WebDAVAuth()(c)
		if held {
			require.True(t, c.IsAborted())
			require.Equal(t, http.StatusUnauthorized, c.Writer.Status())
		} else {
			require.False(t, c.IsAborted())
			require.Equal(t, e.user.ID, inventory.UserFromContext(c).ID)
		}
	}
}
