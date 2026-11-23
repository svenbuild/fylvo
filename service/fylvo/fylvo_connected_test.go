package fylvo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/enttest"
	"github.com/cloudreve/Cloudreve/v4/ent/group"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/cache"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/hashid"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Fylvo: the routes of connected storages, without a provider.

type connectedEnv struct {
	t      *testing.T
	ctx    context.Context
	dep    dependency.Dep
	client *ent.Client
}

func newConnectedEnv(t *testing.T, siteURL string) *connectedEnv {
	logger := logging.NewConsoleLogger(logging.LevelError)
	kv := cache.NewMemoStore("", logger)
	client := enttest.Open(t, "sqlite3", filepath.Join(t.TempDir(), "fylvo.db"))
	t.Cleanup(func() { _ = client.Close() })
	client, err := inventory.InitializeDBClient(logger, client, kv, "4.19.1")
	require.NoError(t, err)
	hasher, err := hashid.New("fylvo-test")
	require.NoError(t, err)

	dep := dependency.NewDependency(
		dependency.WithLogger(logger),
		dependency.WithConfigPath(filepath.Join(t.TempDir(), "conf.ini")),
		dependency.WithDbClient(client),
		dependency.WithKV(kv),
		dependency.WithHashIDEncoder(hasher),
	)
	ctx := context.WithValue(context.Background(), dependency.DepCtx{}, dep)
	ctx = context.WithValue(ctx, logging.LoggerCtx{}, logger)

	require.NoError(t, dep.SettingClient().Set(ctx, map[string]string{"siteURL": siteURL}))
	require.NoError(t, kv.Delete(setting.KvSettingPrefix, "siteURL"))
	return &connectedEnv{t: t, ctx: ctx, dep: dep, client: client}
}

func (e *connectedEnv) newUser(name string, groupID int) *ent.User {
	created, err := e.client.User.Create().SetEmail(name + "@example.com").SetNick(name).
		SetStatus(user.StatusActive).SetGroupID(groupID).SetSettings(&types.UserSetting{}).Save(e.ctx)
	require.NoError(e.t, err)
	loaded, err := e.dep.UserClient().GetByID(context.WithValue(e.ctx, inventory.LoadUserGroup{}, true), created.ID)
	require.NoError(e.t, err)
	return loaded
}

// as returns a request of u and the recorder of its answer.
func (e *connectedEnv) as(u *ent.User, method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, engine := gin.CreateTestContext(rec)
	engine.ContextWithFallback = true
	c.Request = httptest.NewRequest(method, target, nil)
	util.WithValue(c, dependency.DepCtx{}, e.dep)
	if u != nil {
		util.WithValue(c, inventory.UserCtx{}, u)
	}
	return c, rec
}

func appCode(err error) int {
	var app serializer.AppError
	if errors.As(err, &app) {
		return app.Code
	}
	return -1
}

func cookieNamed(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestConnectedSetupAndAuthorize(t *testing.T) {
	e := newConnectedEnv(t, "https://cloud.example.com")
	alex := e.newUser("alex", 2)

	// Nothing is set up: the account sees the providers, none usable.
	c, _ := e.as(alex, "GET", "/api/v4/fylvo/connected")
	list, err := ConnectedList(c)
	require.NoError(t, err)
	require.True(t, list.Allowed)
	require.Equal(t, connected.MaxPerUser, list.Max)
	require.Empty(t, list.Storages)
	require.Len(t, list.Providers, 2)
	for _, p := range list.Providers {
		require.False(t, p.Configured)
	}
	c, _ = e.as(alex, "POST", "/api/v4/fylvo/connected/authorize")
	_, err = (&ConnectedAuthorizeService{Provider: "drive"}).Authorize(c)
	require.Equal(t, serializer.CodeConnectedUnavailable, appCode(err))

	// The operator enters the client. The answer names the callback address and never the secret.
	secret := "the-client-secret"
	c, _ = e.as(alex, "PUT", "/api/v4/admin/fylvo/connected")
	admin, err := (&ConnectedAdminService{Provider: "drive", ClientID: "client-id.example", ClientSecret: &secret}).Set(c)
	require.NoError(t, err)
	require.Equal(t, "https://cloud.example.com/api/v4/fylvo/connected/callback", admin.CallbackURL)
	require.Equal(t, connected.ProviderSetup{ClientID: "client-id.example", SecretSet: true}, admin.Providers["drive"])
	shown, _ := json.Marshal(admin)
	require.NotContains(t, string(shown), secret)

	// In the database the secret is sealed.
	stored, err := e.dep.SettingClient().Get(e.ctx, inventory.ConnectedClientSecretSetting("drive"))
	require.NoError(t, err)
	require.NotEmpty(t, stored)
	require.NotContains(t, stored, secret)

	// Now the account can start: the consent address goes to the provider with the
	// registered callback, and the cookie is bound to the callback of this installation.
	c, rec := e.as(alex, "POST", "/api/v4/fylvo/connected/authorize")
	res, err := (&ConnectedAuthorizeService{Provider: "drive"}).Authorize(c)
	require.NoError(t, err)
	consent, err := url.Parse(res.URL)
	require.NoError(t, err)
	require.Equal(t, "accounts.google.com", consent.Host)
	require.Equal(t, "https://cloud.example.com/api/v4/fylvo/connected/callback", consent.Query().Get("redirect_uri"))
	require.Equal(t, "client-id.example", consent.Query().Get("client_id"))
	require.NotContains(t, res.URL, secret)

	cookie := cookieNamed(rec, connectedCookie)
	require.NotNil(t, cookie)
	require.True(t, cookie.HttpOnly)
	require.True(t, cookie.Secure, "the installation runs on https")
	require.Equal(t, http.SameSiteLaxMode, cookie.SameSite)
	require.Equal(t, "/api/v4/fylvo/connected/callback", cookie.Path)
	require.Empty(t, cookie.Domain, "only for this host")
	require.NotEmpty(t, cookie.Value)
	require.NotEqual(t, consent.Query().Get("state"), cookie.Value)

	// The provider sends the browser back without the cookie (another browser): refused,
	// and the web app is told why, nothing more.
	c, rec = e.as(nil, "GET", "/api/v4/fylvo/connected/callback?state="+consent.Query().Get("state")+"&code=abc")
	ConnectedCallback(c)
	require.Equal(t, http.StatusFound, rec.Code)
	back, err := url.Parse(rec.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "cloud.example.com", back.Host)
	require.Equal(t, "/settings", back.Path)
	require.Equal(t, "connected", back.Query().Get("tab"))
	require.Equal(t, "browser", back.Query().Get("error"))
	require.Empty(t, back.Query().Get("finish"))
	require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	cleared := cookieNamed(rec, connectedCookie)
	require.NotNil(t, cleared)
	require.Negative(t, cleared.MaxAge)

	// A made-up finish code stores nothing.
	c, _ = e.as(alex, "POST", "/api/v4/fylvo/connected/finish")
	_, err = (&ConnectedFinishService{Code: "made-up"}).Finish(c)
	require.Error(t, err)
	require.Equal(t, connected.ReasonExpired, connected.ReasonOf(err))
	c, _ = e.as(alex, "GET", "/api/v4/fylvo/connected")
	list, err = ConnectedList(c)
	require.NoError(t, err)
	require.Empty(t, list.Storages)
	require.True(t, list.Providers[0].Configured)
}

// On plain http (a test server on localhost) the cookie cannot be marked secure.
func TestConnectedCookieOnHTTP(t *testing.T) {
	e := newConnectedEnv(t, "http://localhost:5270")
	alex := e.newUser("alex", 2)
	secret := "the-client-secret"
	c, _ := e.as(alex, "PUT", "/api/v4/admin/fylvo/connected")
	admin, err := (&ConnectedAdminService{Provider: "onedrive", ClientID: "client-id", ClientSecret: &secret}).Set(c)
	require.NoError(t, err)
	require.Equal(t, "http://localhost:5270/api/v4/fylvo/connected/callback", admin.CallbackURL)

	c, rec := e.as(alex, "POST", "/api/v4/fylvo/connected/authorize")
	res, err := (&ConnectedAuthorizeService{Provider: "onedrive"}).Authorize(c)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(res.URL, "https://login.microsoftonline.com/common/oauth2/v2.0/authorize?"))
	require.False(t, cookieNamed(rec, connectedCookie).Secure)
}

// A group without the permission can neither start nor complete connecting a storage.
func TestConnectedNeedsGroupPermission(t *testing.T) {
	e := newConnectedEnv(t, "https://cloud.example.com")
	g, err := e.client.Group.Query().Where(group.ID(2)).First(e.ctx)
	require.NoError(t, err)
	permissions := make(boolset.BooleanSet, len(*g.Permissions))
	copy(permissions, *g.Permissions)
	boolset.Set(types.GroupPermissionConnectedStorage, false, &permissions)
	require.NoError(t, e.client.Group.Update().Where(group.ID(2)).SetPermissions(&permissions).Exec(e.ctx))
	alex := e.newUser("alex", 2)

	secret := "the-client-secret"
	c, _ := e.as(alex, "PUT", "/api/v4/admin/fylvo/connected")
	_, err = (&ConnectedAdminService{Provider: "drive", ClientID: "client-id", ClientSecret: &secret}).Set(c)
	require.NoError(t, err)

	c, _ = e.as(alex, "GET", "/api/v4/fylvo/connected")
	list, err := ConnectedList(c)
	require.NoError(t, err)
	require.False(t, list.Allowed)

	c, rec := e.as(alex, "POST", "/api/v4/fylvo/connected/authorize")
	_, err = (&ConnectedAuthorizeService{Provider: "drive"}).Authorize(c)
	require.Equal(t, serializer.CodeConnectedUnavailable, appCode(err))
	require.Nil(t, cookieNamed(rec, connectedCookie))

	c, _ = e.as(alex, "POST", "/api/v4/fylvo/connected/finish")
	_, err = (&ConnectedFinishService{Code: "anything"}).Finish(c)
	require.Equal(t, serializer.CodeConnectedUnavailable, appCode(err))
	require.Equal(t, connected.ReasonNotAllowed, connected.ReasonOf(err))
}
