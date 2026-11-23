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
	"github.com/cloudreve/Cloudreve/v4/pkg/auth/requestinfo"
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

	cookie := cookieNamed(rec, connectedCookieName(res.URL))
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
	cleared := cookieNamed(rec, connectedCookieName(res.URL))
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
	require.False(t, cookieNamed(rec, connectedCookieName(res.URL)).Secure)
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
	require.Empty(t, rec.Result().Cookies())

	c, _ = e.as(alex, "POST", "/api/v4/fylvo/connected/finish")
	_, err = (&ConnectedFinishService{Code: "anything"}).Finish(c)
	require.Equal(t, serializer.CodeConnectedUnavailable, appCode(err))
	require.Equal(t, connected.ReasonNotAllowed, connected.ReasonOf(err))
	c, _ = e.as(alex, "PATCH", "/api/v4/fylvo/connected/aaaaaaaaaaaaaaaa")
	_, err = (&ConnectedRenameService{Name: "Photos"}).Rename(c, "aaaaaaaaaaaaaaaa")
	require.Equal(t, connected.ReasonNotAllowed, connected.ReasonOf(err))
}

// Every request gets a Dep of its own. All of them lead to the one Manager of the
// server: a sign-in is started by one request and completed by others.
func TestConnectedManagerIsOnePerServer(t *testing.T) {
	e := newConnectedEnv(t, "https://cloud.example.com")
	fork := func() dependency.Dep {
		return dependency.FromContext(e.dep.ForkWithLogger(e.ctx, logging.NewConsoleLogger(logging.LevelError)))
	}
	first, second := fork(), fork()
	require.NotSame(t, first, second)
	require.Same(t, connected.For(first), connected.For(second))
	require.Same(t, connected.For(e.dep), connected.For(first))

	// Another server has another one.
	other := newConnectedEnv(t, "https://cloud.example.com")
	require.NotSame(t, connected.For(e.dep), connected.For(other.dep))
}

// The provider sends the browser back to the first site address, so a sign-in is
// started only there.
func TestConnectedAuthorizeOnlyUnderTheFirstAddress(t *testing.T) {
	e := newConnectedEnv(t, "https://cloud.example.com,https://files.example.com")
	alex := e.newUser("alex", 2)
	secret := "the-client-secret"
	c, _ := e.as(alex, "PUT", "/api/v4/admin/fylvo/connected")
	_, err := (&ConnectedAdminService{Provider: "drive", ClientID: "client-id.example", ClientSecret: &secret}).Set(c)
	require.NoError(t, err)

	on := func(host string) *gin.Context {
		c, _ := e.as(alex, "POST", "/api/v4/fylvo/connected/authorize")
		util.WithValue(c, requestinfo.RequestInfoCtx{}, &requestinfo.RequestInfo{Host: host})
		return c
	}
	_, err = (&ConnectedAuthorizeService{Provider: "drive"}).Authorize(on("files.example.com"))
	require.Equal(t, connected.ReasonOtherAddress, connected.ReasonOf(err))
	_, err = (&ConnectedAuthorizeService{Provider: "drive"}).Authorize(on("cloud.example.com"))
	require.NoError(t, err)
}

// Two sign-ins started side by side, as in two tabs, each have their own cookie: the
// return of one does not end the other.
func TestConnectedSignInsSideBySide(t *testing.T) {
	e := newConnectedEnv(t, "https://cloud.example.com")
	alex := e.newUser("alex", 2)
	secret := "the-client-secret"
	c, _ := e.as(alex, "PUT", "/api/v4/admin/fylvo/connected")
	_, err := (&ConnectedAdminService{Provider: "drive", ClientID: "client-id.example", ClientSecret: &secret}).Set(c)
	require.NoError(t, err)

	start := func() (*url.URL, *http.Cookie) {
		c, rec := e.as(alex, "POST", "/api/v4/fylvo/connected/authorize")
		res, err := (&ConnectedAuthorizeService{Provider: "drive"}).Authorize(c)
		require.NoError(t, err)
		consent, err := url.Parse(res.URL)
		require.NoError(t, err)
		cookies := rec.Result().Cookies()
		require.Len(t, cookies, 1)
		return consent, cookies[0]
	}
	firstConsent, first := start()
	_, second := start()
	require.NotEqual(t, first.Name, second.Name)

	// The first one comes back, denied at the provider. Only its own cookie is taken away.
	c, rec := e.as(nil, "GET", "/api/v4/fylvo/connected/callback?state="+firstConsent.Query().Get("state")+"&error=access_denied")
	c.Request.AddCookie(first)
	c.Request.AddCookie(second)
	ConnectedCallback(c)
	back, err := url.Parse(rec.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "denied", back.Query().Get("error"))
	cleared := rec.Result().Cookies()
	require.Len(t, cleared, 1)
	require.Equal(t, first.Name, cleared[0].Name)
	require.Negative(t, cleared[0].MaxAge)
}

// A sidebar link into a connected storage goes away with the storage: when it is
// disconnected, when the sidebar is read, and when a client saves a list that still has it.
func TestConnectedSidebarLinksEndWithTheStorage(t *testing.T) {
	e := newActivityEnv(t)
	m := connected.For(e.dep)
	token := `{"access_token":"access-1","token_type":"Bearer","refresh_token":"refresh-1","expiry":"2099-01-01T00:00:00Z"}`
	storage, err := m.Create(e.ctx, e.owner.ID, connected.ProviderOneDrive,
		&connected.Identity{Key: "account-1", Label: "user@example.com"}, token)
	require.NoError(t, err)

	link := storage.URI + "/Documents"
	body := `{"sidebar":{"items":[{"type":"my_files"},{"type":"link","uri":"` + link + `","name":"Documents"},{"type":"trash"}]}}`
	res, err := e.patch(e.owner, body)
	require.NoError(t, err)
	require.Contains(t, sidebarTypes(res.Sidebar.Items), "link:"+link)

	// A link with a path that is none, into a storage the account has, is still refused.
	_, err = e.patch(e.owner, `{"sidebar":{"items":[{"type":"link","uri":"`+storage.URI+`/a/../b","name":"x"}]}}`)
	requireParamError(t, err)

	require.NoError(t, ConnectedDisconnect(e.as(e.reload(e.owner)), storage.ID))
	stored := e.reload(e.owner).Settings
	require.Empty(t, stored.Pined, "the link was removed with the storage")
	res, err = GetPrefs(e.as(e.reload(e.owner)))
	require.NoError(t, err)
	require.NotContains(t, sidebarTypes(res.Sidebar.Items), "link:"+link)

	// A client that still has the old list saves it again: the dead link falls away,
	// the rest is saved.
	res, err = e.patch(e.owner, body)
	require.NoError(t, err)
	shown := sidebarTypes(res.Sidebar.Items)
	require.NotContains(t, shown, "link:"+link)
	require.Equal(t, "my_files", shown[0])
	require.Empty(t, e.reload(e.owner).Settings.Pined)

	// A link into a storage the account never had is not stored either, and no error:
	// it cannot be told from one that was disconnected on another device.
	res, err = e.patch(e.owner, `{"sidebar":{"items":[{"type":"my_files"},{"type":"link","uri":"cloudreve://aaaaaaaaaaaaaaaa@connected/Docs","name":"Docs"}]}}`)
	require.NoError(t, err)
	require.NotContains(t, sidebarTypes(res.Sidebar.Items), "link:cloudreve://aaaaaaaaaaaaaaaa@connected/Docs")
	require.Empty(t, e.reload(e.owner).Settings.Pined)

	// Should the tidying at disconnect ever fail, a link that is left over is not shown
	// and does not keep the sidebar from being saved.
	_, err = e.dep.UserClient().UpdateSettings(e.ctx, e.owner.ID, func(s *types.UserSetting) error {
		s.Pined = append(s.Pined, types.PinedFile{Uri: link, Name: "Documents"})
		return nil
	})
	require.NoError(t, err)
	res, err = GetPrefs(e.as(e.reload(e.owner)))
	require.NoError(t, err)
	require.NotContains(t, sidebarTypes(res.Sidebar.Items), "link:"+link)
	_, err = e.patch(e.owner, `{"sidebar":{"items":[{"type":"trash"},{"type":"my_files"}]}}`)
	require.NoError(t, err)
	require.Empty(t, e.reload(e.owner).Settings.Pined)
}
