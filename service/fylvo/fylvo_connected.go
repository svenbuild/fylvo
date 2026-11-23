package fylvo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/filemanager/fs"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/gin-gonic/gin"
)

// Connected storages: connecting, listing and disconnecting them (see pkg/connected).

const (
	connectedCallbackPath = constants.APIPrefix + "/fylvo/connected/callback"
	// connectedCookie ties a sign-in at a provider to the browser that started it.
	connectedCookie = "fylvo_connect"
	// connectedCookieAge matches the time a started sign-in stays valid.
	connectedCookieAge = 10 * 60
	// connectedSettingsPage is where the web app shows the connected storages.
	connectedSettingsPage = "/settings"
)

type (
	// ConnectedListResponse is what an account sees of connected storages.
	ConnectedListResponse struct {
		// Available is false when connected storages cannot work on this server.
		Available bool `json:"available"`
		// Allowed tells whether the group of the account may connect storages.
		Allowed bool `json:"allowed"`
		// Max is the most storages one account can connect.
		Max       int                      `json:"max"`
		Providers []connected.ProviderInfo `json:"providers"`
		Storages  []*connected.Storage     `json:"storages"`
		// AppSignin tells whether an app can connect storages through the address this
		// request came under (see fylvo_connected_app.go).
		AppSignin bool `json:"app_signin"`
	}

	ConnectedAuthorizeParamCtx struct{}
	ConnectedAuthorizeService  struct {
		Provider string `json:"provider" binding:"required"`
		// Reconnect is the id of a connection whose token is to be renewed.
		Reconnect string `json:"reconnect"`
		// Client is "app" when an app asks, which opens the system browser for the
		// sign-in. Challenge is then the hash of the secret the app keeps.
		Client    string `json:"client"`
		Challenge string `json:"challenge"`
	}
	ConnectedAuthorizeResponse struct {
		// URL is the consent page of the provider, to be opened in this browser. For an
		// app: the start page of this installation, to be opened in the system browser.
		URL string `json:"url"`
		// Attempt is the id an app knows its sign-in by.
		Attempt string `json:"attempt,omitempty"`
	}

	ConnectedFinishParamCtx struct{}
	ConnectedFinishService  struct {
		Code string `json:"code" binding:"required"`
		// Verifier is the secret of the app that started the sign-in; the web app has none.
		Verifier string `json:"verifier"`
		// Preview asks who signed in at the provider and stores nothing. Cancel drops
		// the result. Both are for sign-ins from apps.
		Preview bool `json:"preview"`
		Cancel  bool `json:"cancel"`
	}

	ConnectedRenameParamCtx struct{}
	ConnectedRenameService  struct {
		Name string `json:"name" binding:"required"`
	}

	ConnectedAdminParamCtx struct{}
	ConnectedAdminService  struct {
		Provider string `json:"provider" binding:"required"`
		ClientID string `json:"client_id"`
		// ClientSecret is written only when given; it is never returned.
		ClientSecret *string `json:"client_secret"`
	}
	// ConnectedAdminResponse is what the operator sees: where the providers have to
	// redirect to, and the OAuth clients without their secrets.
	ConnectedAdminResponse struct {
		CallbackURL string                             `json:"callback_url"`
		Providers   map[string]connected.ProviderSetup `json:"providers"`
	}
)

// The sidebar of an account knows connected storages through these two hooks of the
// preferences: which ones there are, and whether a link leads into one of them.
func init() {
	ConnectedStorages = func(ctx context.Context, dep dependency.Dep, u *ent.User) []string {
		storages, err := connected.For(dep).List(ctx, u.ID)
		if err != nil {
			return nil
		}
		ids := make([]string, 0, len(storages))
		for _, s := range storages {
			ids = append(ids, s.ID)
		}
		return ids
	}

	CheckConnectedLink = func(ctx context.Context, dep dependency.Dep, u *ent.User, uri *fs.URI) error {
		if _, err := connected.CheckPath(uri.U.Path); err != nil || len(uri.U.Query()) > 0 {
			return paramError("invalid link into a connected storage")
		}
		id := ""
		if uri.U.User != nil {
			id = uri.U.User.Username()
		}
		// Only that the storage is one of the account. Whether the folder still exists
		// shows when the link is opened, as for any other link.
		if !connected.For(dep).Has(ctx, u.ID, id) {
			return paramError("the link leads into a storage that is not connected")
		}
		return nil
	}
}

func mayConnectStorages(u *ent.User) bool {
	return u != nil && u.Edges.Group != nil && u.Edges.Group.Permissions != nil &&
		u.Edges.Group.Permissions.Enabled(int(types.GroupPermissionConnectedStorage))
}

// primarySiteURL is the address of the installation the operator registered at the
// providers: the first site URL, whatever host this request came in on.
func primarySiteURL(c *gin.Context, dep dependency.Dep) *url.URL {
	return dep.SettingProvider().SiteURL(setting.UseFirstSiteUrl(c))
}

func connectedCallbackURL(c *gin.Context, dep dependency.Dep) string {
	return primarySiteURL(c, dep).ResolveReference(&url.URL{Path: connectedCallbackPath}).String()
}

// ConnectedList returns the connected storages of the current account.
func ConnectedList(c *gin.Context) (*ConnectedListResponse, error) {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := connected.For(dep)

	providers, err := m.Providers(c)
	if err != nil {
		return nil, err
	}
	storages, err := m.List(c, user.ID)
	if err != nil {
		return nil, err
	}

	return &ConnectedListResponse{
		Available: true,
		Allowed:   mayConnectStorages(user),
		Max:       connected.MaxPerUser,
		Providers: providers,
		Storages:  storages,
		AppSignin: appSigninHere(c, dep),
	}, nil
}

// Authorize starts a sign-in at a provider and sets the cookie that ties it to this browser.
func (s *ConnectedAuthorizeService) Authorize(c *gin.Context) (*ConnectedAuthorizeResponse, error) {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	switch {
	case s.Client != "" && s.Client != connectedClientApp:
		return nil, paramError("unknown client")
	case s.Client == "" && s.Challenge != "":
		return nil, paramError("a challenge comes only from an app")
	case s.Client != "" && !connected.ValidChallenge(s.Challenge):
		return nil, paramError("invalid challenge")
	}
	if !mayConnectStorages(user) {
		return nil, connected.ErrNotAllowed
	}
	if s.Client == connectedClientApp {
		return s.authorizeApp(c, dep, user)
	}
	// The provider sends the browser back to the first site address, and the cookie
	// of the sign-in is for one host. Started under another address of the
	// installation, the sign-in could only fail after the consent.
	if dep.SettingProvider().SiteURL(c).Host != primarySiteURL(c, dep).Host {
		return nil, connected.ErrOtherAddress
	}

	address, cookie, err := connected.For(dep).Authorize(c, user.ID, s.Provider, s.Reconnect, connectedCallbackURL(c, dep))
	if err != nil {
		return nil, err
	}

	setConnectedCookie(c, dep, connectedCookieName(address), cookie, connectedCookieAge)
	return &ConnectedAuthorizeResponse{URL: address}, nil
}

// connectedCookieName is the name of the cookie of one sign-in, from an address that
// carries its state: the consent address, or the callback as the provider calls it. Each
// sign-in has its own, so two that are started side by side do not take each other's.
func connectedCookieName(address string) string {
	state := ""
	if u, err := url.Parse(address); err == nil {
		state = u.Query().Get("state")
	}
	sum := sha256.Sum256([]byte(state))
	return connectedCookie + "_" + hex.EncodeToString(sum[:8])
}

// setConnectedCookie sets or, with a negative age, removes the cookie of a sign-in. It is
// sent only to the callback, never readable by scripts, and only over https when the
// installation runs on https (TLS usually ends at a proxy, so the request does not tell).
func setConnectedCookie(c *gin.Context, dep dependency.Dep, name, value string, age int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     connectedCallbackPath,
		MaxAge:   age,
		HttpOnly: true,
		Secure:   primarySiteURL(c, dep).Scheme == "https",
		SameSite: http.SameSiteLaxMode,
	})
}

// ConnectedCallback receives the browser coming back from a provider. It answers with a
// redirect to the web app, which completes the sign-in as the signed-in account.
func ConnectedCallback(c *gin.Context) {
	dep := dependency.FromContext(c)
	if connected.IsAppState(c.Query("state")) {
		// A sign-in an app started ends on a page with the link back to the app.
		connectedAppCallback(c, dep)
		return
	}
	name := connectedCookieName(c.Request.URL.String())
	cookie, _ := c.Cookie(name)
	setConnectedCookie(c, dep, name, "", -1)

	target := primarySiteURL(c, dep).ResolveReference(&url.URL{Path: connectedSettingsPage})
	q := url.Values{"tab": {"connected"}}
	out, err := connected.For(dep).Callback(c, c.Query("state"), cookie, c.Query("code"), c.Query("error"))
	if err != nil {
		q.Set("error", connected.FlowErrorCode(err))
	} else {
		q.Set("finish", out.Finish)
	}
	target.RawQuery = q.Encode()

	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Redirect(http.StatusFound, target.String())
}

// Finish stores the connection for the account that started the sign-in. For a sign-in
// from an app it can also only show who signed in at the provider (a *connected.SignedIn),
// or drop the result.
func (s *ConnectedFinishService) Finish(c *gin.Context) (any, error) {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	m := connected.For(dep)
	if s.Preview && s.Cancel {
		return nil, paramError("preview and cancel exclude each other")
	}
	if (s.Verifier != "" || s.Preview || s.Cancel) && !appSigninHere(c, dep) {
		return nil, connected.ErrAppSigninOff
	}
	if s.Cancel {
		// Dropping a result needs the account and the verifier, not the right to connect.
		return nil, m.Cancel(user.ID, s.Code, s.Verifier)
	}
	if !mayConnectStorages(user) {
		return nil, connected.ErrNotAllowed
	}
	if s.Preview {
		signedIn, err := m.Preview(user.ID, s.Code, s.Verifier)
		if err != nil {
			return nil, err
		}
		return signedIn, nil
	}
	storage, err := m.Finish(c, user.ID, s.Code, s.Verifier)
	if err != nil {
		return nil, err
	}
	return storage, nil
}

// Rename changes the name of the connection with the given id.
func (s *ConnectedRenameService) Rename(c *gin.Context, id string) (*connected.Storage, error) {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	if !mayConnectStorages(user) {
		return nil, connected.ErrNotAllowed
	}
	return connected.For(dep).Rename(c, user.ID, id, s.Name)
}

// ConnectedDisconnect ends the connection with the given id. It needs no permission of
// the group: who may no longer connect storages can still remove the ones there are.
func ConnectedDisconnect(c *gin.Context, id string) error {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	if err := connected.For(dep).Disconnect(c, user.ID, id); err != nil {
		return err
	}
	// The storage is gone. Sidebar links into it are tidied up as well; should that
	// fail, they are left out when the sidebar is read or saved the next time.
	_ = DropConnectedLinks(c, dep, user, id)
	return nil
}

// ConnectedAdmin returns the setup of the providers for the operator.
func ConnectedAdmin(c *gin.Context) (*ConnectedAdminResponse, error) {
	dep := dependency.FromContext(c)
	providers, err := connected.For(dep).Setup(c)
	if err != nil {
		return nil, err
	}
	return &ConnectedAdminResponse{CallbackURL: connectedCallbackURL(c, dep), Providers: providers}, nil
}

// Set stores the OAuth client of a provider and returns the new setup.
func (s *ConnectedAdminService) Set(c *gin.Context) (*ConnectedAdminResponse, error) {
	dep := dependency.FromContext(c)
	if err := connected.For(dep).SetProvider(c, s.Provider, s.ClientID, s.ClientSecret); err != nil {
		return nil, err
	}
	return ConnectedAdmin(c)
}
