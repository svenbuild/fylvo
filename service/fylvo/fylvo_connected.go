package fylvo

import (
	"net/http"
	"net/url"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
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
		Allowed   bool                     `json:"allowed"`
		Providers []connected.ProviderInfo `json:"providers"`
		Storages  []*connected.Storage     `json:"storages"`
	}

	ConnectedAuthorizeParamCtx struct{}
	ConnectedAuthorizeService  struct {
		Provider string `json:"provider" binding:"required"`
		// Reconnect is the id of a connection whose token is to be renewed.
		Reconnect string `json:"reconnect"`
	}
	ConnectedAuthorizeResponse struct {
		// URL is the consent page of the provider, to be opened in this browser.
		URL string `json:"url"`
	}

	ConnectedFinishParamCtx struct{}
	ConnectedFinishService  struct {
		Code string `json:"code" binding:"required"`
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
		Providers: providers,
		Storages:  storages,
	}, nil
}

// Authorize starts a sign-in at a provider and sets the cookie that ties it to this browser.
func (s *ConnectedAuthorizeService) Authorize(c *gin.Context) (*ConnectedAuthorizeResponse, error) {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	if !mayConnectStorages(user) {
		return nil, connected.ErrUnavailable
	}

	address, cookie, err := connected.For(dep).Authorize(c, user.ID, s.Provider, s.Reconnect, connectedCallbackURL(c, dep))
	if err != nil {
		return nil, err
	}

	setConnectedCookie(c, dep, cookie, connectedCookieAge)
	return &ConnectedAuthorizeResponse{URL: address}, nil
}

// setConnectedCookie sets or, with a negative age, removes the cookie of a sign-in. It is
// sent only to the callback, never readable by scripts, and only over https when the
// installation runs on https (TLS usually ends at a proxy, so the request does not tell).
func setConnectedCookie(c *gin.Context, dep dependency.Dep, value string, age int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     connectedCookie,
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
	cookie, _ := c.Cookie(connectedCookie)
	setConnectedCookie(c, dep, "", -1)

	target := primarySiteURL(c, dep).ResolveReference(&url.URL{Path: connectedSettingsPage})
	q := url.Values{"tab": {"connected"}}
	finish, err := connected.For(dep).Callback(c, c.Query("state"), cookie, c.Query("code"), c.Query("error"))
	if err != nil {
		q.Set("error", connected.FlowErrorCode(err))
	} else {
		q.Set("finish", finish)
	}
	target.RawQuery = q.Encode()

	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Redirect(http.StatusFound, target.String())
}

// Finish stores the connection for the account that started the sign-in.
func (s *ConnectedFinishService) Finish(c *gin.Context) (*connected.Storage, error) {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	if !mayConnectStorages(user) {
		return nil, connected.ErrUnavailable
	}
	return connected.For(dep).Finish(c, user.ID, s.Code)
}

// Rename changes the name of the connection with the given id.
func (s *ConnectedRenameService) Rename(c *gin.Context, id string) (*connected.Storage, error) {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	return connected.For(dep).Rename(c, user.ID, id, s.Name)
}

// ConnectedDisconnect ends the connection with the given id. It needs no permission of
// the group: who may no longer connect storages can still remove the ones there are.
func ConnectedDisconnect(c *gin.Context, id string) error {
	dep := dependency.FromContext(c)
	user := inventory.UserFromContext(c)
	return connected.For(dep).Disconnect(c, user.ID, id)
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
