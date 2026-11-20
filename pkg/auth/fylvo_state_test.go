package auth

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newStateTestAuth(t *testing.T) (TokenAuth, inventory.UserClient, *ent.User) {
	ctx := context.Background()
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	g, err := client.Group.Create().SetName("User").SetPermissions(&boolset.BooleanSet{}).Save(ctx)
	require.NoError(t, err)
	users := inventory.NewUserClient(client)
	u, err := users.Upsert(ctx, &ent.User{Email: "neu@fylvo.local", Status: user.StatusActive, GroupUsers: g.ID},
		"erstes-passwort", "", &types.UserSetting{})
	require.NoError(t, err)

	encoder, err := hashid.New("test")
	require.NoError(t, err)
	l := logging.NewConsoleLogger(logging.LevelError)
	tokenAuth := NewTokenAuth(encoder, setting.NewProvider(setting.NewDbDefaultStore(nil)), []byte("secret"), users,
		l, cache.NewMemoStore("", l), inventory.NewOAuthClientClient(client, conf.SQLiteDB))
	return tokenAuth, users, u
}

func requestWithToken(t *testing.T, tokenAuth TokenAuth, accessToken string) *gin.Context {
	c, engine := gin.CreateTestContext(httptest.NewRecorder())
	engine.ContextWithFallback = true
	c.Request = httptest.NewRequest("GET", "/api/v4/user/me", nil)
	c.Request.Header.Set(AuthorizationHeader, TokenHeaderPrefix+accessToken)
	_, err := tokenAuth.VerifyAndRetrieveUser(c)
	require.NoError(t, err)
	return c
}

func TestAccessTokenEndsWithANewPassword(t *testing.T) {
	tokenAuth, users, u := newStateTestAuth(t)
	token, err := tokenAuth.Issue(context.Background(), &IssueTokenArgs{User: u})
	require.NoError(t, err)

	c := requestWithToken(t, tokenAuth, token.AccessToken)
	require.True(t, tokenAuth.UserStateValid(c, u))

	changed, err := users.UpdatePassword(context.Background(), u, "neues-passwort")
	require.NoError(t, err)
	require.False(t, tokenAuth.UserStateValid(c, changed))
}

func TestNoTokensForAHeldAccount(t *testing.T) {
	tokenAuth, users, u := newStateTestAuth(t)
	token, err := tokenAuth.Issue(context.Background(), &IssueTokenArgs{User: u})
	require.NoError(t, err)

	// The admin switches the hold on without a new password: Issue and Refresh both refuse.
	held, err := users.Upsert(context.Background(), u, "", "", &types.UserSetting{FylvoMustChangePassword: true})
	require.NoError(t, err)
	_, err = tokenAuth.Issue(context.Background(), &IssueTokenArgs{User: held})
	require.ErrorIs(t, err, ErrPasswordChangeRequired)
	_, err = tokenAuth.Refresh(context.Background(), token.RefreshToken)
	require.ErrorIs(t, err, ErrPasswordChangeRequired)
}
