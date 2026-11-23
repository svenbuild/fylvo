package fylvo

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/ent/group"
	"github.com/cloudreve/Cloudreve/v4/ent/user"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/inventory/types"
	"github.com/cloudreve/Cloudreve/v4/pkg/auth/requestinfo"
	"github.com/cloudreve/Cloudreve/v4/pkg/boolset"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/connectedtest"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/cloudreve/Cloudreve/v4/pkg/setting"
	"github.com/cloudreve/Cloudreve/v4/pkg/util"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Fylvo: connecting a storage from an app, through the pages of the server.

const (
	// The test value of PKCE (RFC 7636, appendix B).
	appVerifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	appChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"

	startPath    = "/api/v4/fylvo/connected/start/"
	callbackPath = "/api/v4/fylvo/connected/callback"
)

// appEnv is a server with a provider that signs everybody in as identity.
type appEnv struct {
	*connectedEnv
	alex     *ent.User
	identity *connected.Identity
}

func newAppEnv(t *testing.T, siteURL string) *appEnv {
	e := &appEnv{connectedEnv: newConnectedEnv(t, siteURL)}
	e.identity = &connected.Identity{Key: "account-1", Label: "anna@example.com", Shown: "anna@example.com", Kind: connected.AccountAddress}
	require.NoError(t, e.dep.SettingClient().Set(e.ctx, map[string]string{"siteName": "Example Cloud"}))
	require.NoError(t, e.dep.KV().Delete(setting.KvSettingPrefix, "siteName"))

	tokens := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-1", "refresh_token": "refresh-1", "token_type": "Bearer", "expires_in": 3600, "scope": "drive",
		})
	}))
	t.Cleanup(tokens.Close)
	spec := connected.Specs()[connected.ProviderDrive]
	spec.AuthURL = "https://provider.example/auth"
	spec.TokenURL = tokens.URL
	spec.Identify = func(context.Context, connected.APICall) (*connected.Identity, error) { return e.identity, nil }
	spec.Revoke = nil
	connected.Install(e.dep, connected.NewManager(connected.ManagerOptions{
		Storages: inventory.NewConnectedStorageClient(e.client),
		Settings: connected.DepSettings(e.dep),
		Sealer:   connected.DepSealer(e.dep),
		Specs:    map[string]*connected.Spec{connected.ProviderDrive: spec},
		HTTP:     tokens.Client(),
		UserExists: func(ctx context.Context, userID int) bool {
			u, err := e.dep.UserClient().GetByID(ctx, userID)
			return err == nil && u != nil
		},
	}))

	e.alex = e.newUser("alex", 2)
	secret := "the-client-secret"
	c, _ := e.as(e.alex, "PUT", "/api/v4/admin/fylvo/connected")
	_, err := (&ConnectedAdminService{Provider: "drive", ClientID: "client-id.example", ClientSecret: &secret}).Set(c)
	require.NoError(t, err)
	return e
}

// authorize plays the app asking for a sign-in. It returns the ticket and the attempt id.
func (e *appEnv) authorize(u *ent.User, reconnect string) (ticket, attempt string) {
	e.t.Helper()
	c, rec := e.as(u, "POST", "/api/v4/fylvo/connected/authorize")
	res, err := (&ConnectedAuthorizeService{Provider: "drive", Reconnect: reconnect, Client: "app", Challenge: appChallenge}).Authorize(c)
	require.NoError(e.t, err)
	// Nothing for a browser comes with the answer to the app.
	require.Empty(e.t, rec.Result().Cookies())
	require.Regexp(e.t, `^https://cloud\.example\.com/api/v4/fylvo/connected/start/[A-Za-z0-9_-]{43}$`, res.URL)
	require.Regexp(e.t, `^[A-Za-z0-9_-]{16}$`, res.Attempt)
	return strings.TrimPrefix(res.URL, "https://cloud.example.com"+startPath), res.Attempt
}

// browser is a browser as far as these pages care: it keeps cookies and sends each one
// only to the path it is for.
type browser struct {
	e       *appEnv
	cookies map[string]*http.Cookie
	// host is the address of the installation the browser uses, "" for its first one.
	host string
}

func (e *appEnv) browser() *browser { return &browser{e: e, cookies: map[string]*http.Cookie{}} }

func (b *browser) request(method, target string, form url.Values, headers map[string]string) *httptest.ResponseRecorder {
	b.e.t.Helper()
	rec := httptest.NewRecorder()
	c, engine := gin.CreateTestContext(rec)
	engine.ContextWithFallback = true
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
		c.Request = httptest.NewRequest(method, target, body)
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		c.Request = httptest.NewRequest(method, target, nil)
	}
	for name, value := range headers {
		c.Request.Header.Set(name, value)
	}
	for _, cookie := range b.cookies {
		if strings.HasPrefix(c.Request.URL.Path, cookie.Path) {
			c.Request.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
		}
	}
	util.WithValue(c, dependency.DepCtx{}, b.e.dep)
	if b.host != "" {
		util.WithValue(c, requestinfo.RequestInfoCtx{}, &requestinfo.RequestInfo{Host: b.host})
	}
	if strings.HasPrefix(c.Request.URL.Path, startPath) {
		c.Params = gin.Params{{Key: "ticket", Value: strings.TrimPrefix(c.Request.URL.Path, startPath)}}
		ConnectedStart(c)
	} else {
		ConnectedCallback(c)
	}
	// As the server does after every handler: an answer without a body is sent too.
	c.Writer.WriteHeaderNow()
	for _, cookie := range rec.Result().Cookies() {
		if cookie.MaxAge < 0 {
			delete(b.cookies, cookie.Name)
		} else {
			b.cookies[cookie.Name] = cookie
		}
	}
	return rec
}

func (b *browser) get(target string) *httptest.ResponseRecorder {
	return b.request("GET", target, nil, nil)
}

var (
	hiddenValue = regexp.MustCompile(`<input type="hidden" name="confirm" value="([0-9a-f]{64})">`)
	backLink    = regexp.MustCompile(`<a class="button" href="([^"]*)">`)
)

// press sends the form of the start page as the browser itself would.
func (b *browser) press(ticket, page string) *httptest.ResponseRecorder {
	b.e.t.Helper()
	found := hiddenValue.FindStringSubmatch(page)
	require.NotNil(b.e.t, found, "the page has no form")
	return b.request("POST", startPath+ticket, url.Values{"confirm": {found[1]}}, map[string]string{"Sec-Fetch-Site": "same-origin"})
}

// link returns the link back to the app a page carries, as a browser reads it.
func link(t *testing.T, page string) string {
	t.Helper()
	found := backLink.FindStringSubmatch(page)
	require.NotNil(t, found, "the page has no link back")
	return html.UnescapeString(found[1])
}

func checkPageHeaders(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	h := rec.Header()
	require.Equal(t, "no-store", h.Get("Cache-Control"))
	require.Equal(t, "strict-origin", h.Get("Referrer-Policy"))
	require.Equal(t, "DENY", h.Get("X-Frame-Options"))
	require.Equal(t, "nosniff", h.Get("X-Content-Type-Options"))
	csp := h.Get("Content-Security-Policy")
	require.Contains(t, csp, "default-src 'none'")
	require.Contains(t, csp, "frame-ancestors 'none'")
	require.NotContains(t, csp, "form-action", "browsers apply it to the redirect to the provider")
	if body := rec.Body.String(); body != "" {
		require.Contains(t, h.Get("Content-Type"), "text/html")
		require.NotContains(t, body, "<script")
		require.NotContains(t, body, "http://")
		require.NotContains(t, strings.ReplaceAll(body, "https://", ""), "//cdn")
	}
}

// signIn plays the browser from the start page to the page the provider's answer ends
// on, and returns that page.
func (b *browser) signIn(ticket string) string {
	b.e.t.Helper()
	page := b.get(startPath + ticket)
	require.Equal(b.e.t, http.StatusOK, page.Code)
	redirect := b.press(ticket, page.Body.String())
	require.Equal(b.e.t, http.StatusSeeOther, redirect.Code)
	consent, err := url.Parse(redirect.Header().Get("Location"))
	require.NoError(b.e.t, err)
	back := b.get(callbackPath + "?state=" + url.QueryEscape(consent.Query().Get("state")) + "&code=granted")
	require.Equal(b.e.t, http.StatusOK, back.Code)
	return back.Body.String()
}

func (e *appEnv) finish(u *ent.User, s ConnectedFinishService) (any, error) {
	c, _ := e.as(u, "POST", "/api/v4/fylvo/connected/finish")
	return s.Finish(c)
}

func TestAppConnectFromStartToStorage(t *testing.T) {
	e := newAppEnv(t, "https://cloud.example.com")
	c, _ := e.as(e.alex, "GET", "/api/v4/fylvo/connected")
	list, err := ConnectedList(c)
	require.NoError(t, err)
	require.True(t, list.AppSignin)

	ticket, attempt := e.authorize(e.alex, "")
	b := e.browser()

	// The page names the account and the server before the provider is involved.
	page := b.get(startPath + ticket)
	require.Equal(t, http.StatusOK, page.Code)
	checkPageHeaders(t, page)
	body := page.Body.String()
	for _, text := range []string{
		"<strong>alex</strong>", "<span>alex@example.com</span>", "Example Cloud (cloud.example.com)", "Google Drive",
		"Confirm only if you started this yourself in the Fylvo app just now.", `<html lang="en">`,
	} {
		require.Contains(t, body, text)
	}
	require.NotContains(t, body, ticket)
	require.NotContains(t, body, attempt)
	require.NotContains(t, body, "fylvo://")

	// The cookie that makes the ticket this browser's: for this page only, and never
	// sent because another site says so.
	require.Len(t, b.cookies, 1)
	var confirm *http.Cookie
	for _, cookie := range b.cookies {
		confirm = cookie
	}
	require.Regexp(t, `^fylvo_confirm_[0-9a-f]{16}$`, confirm.Name)
	require.Equal(t, startPath+ticket, confirm.Path)
	require.Equal(t, http.SameSiteStrictMode, confirm.SameSite)
	require.True(t, confirm.HttpOnly)
	require.True(t, confirm.Secure)
	require.Empty(t, confirm.Domain)
	require.Equal(t, 13*60, confirm.MaxAge)
	require.NotContains(t, body, confirm.Value)

	// The same browser gets the same working form again, and no new cookie.
	again := b.get(startPath + ticket)
	require.Equal(t, http.StatusOK, again.Code)
	require.Empty(t, again.Result().Cookies())
	require.Equal(t, hiddenValue.FindStringSubmatch(body), hiddenValue.FindStringSubmatch(again.Body.String()))

	// The button: on to the provider, with the cookie of the sign-in for the callback.
	redirect := b.press(ticket, body)
	require.Equal(t, http.StatusSeeOther, redirect.Code)
	checkPageHeaders(t, redirect)
	consent, err := url.Parse(redirect.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "provider.example", consent.Host)
	require.Equal(t, "https://cloud.example.com"+callbackPath, consent.Query().Get("redirect_uri"))
	state := consent.Query().Get("state")
	require.True(t, connected.IsAppState(state))
	set := redirect.Result().Cookies()
	require.Len(t, set, 1)
	signIn := set[0]
	require.Regexp(t, `^fylvo_connect_app_[0-9a-f]{16}$`, signIn.Name)
	require.Equal(t, callbackPath, signIn.Path)
	require.Equal(t, http.SameSiteLaxMode, signIn.SameSite)
	require.True(t, signIn.HttpOnly)
	require.True(t, signIn.Secure)
	require.Empty(t, signIn.Domain)
	require.NotEqual(t, confirm.Value, signIn.Value)

	// A double tap, or back and the button again: the same again, nothing new.
	twice := b.press(ticket, body)
	require.Equal(t, http.StatusSeeOther, twice.Code)
	require.Equal(t, redirect.Header().Get("Location"), twice.Header().Get("Location"))
	require.Equal(t, signIn.Value, twice.Result().Cookies()[0].Value)
	// Going back shows the page again.
	require.Equal(t, http.StatusOK, b.get(startPath+ticket).Code)

	// The provider sends the browser back: a page of the server with the link to the app.
	back := b.get(callbackPath + "?state=" + url.QueryEscape(state) + "&code=granted")
	require.Equal(t, http.StatusOK, back.Code)
	checkPageHeaders(t, back)
	body = back.Body.String()
	for _, text := range []string{
		"<strong>anna@example.com</strong>", "You signed in at Google Drive as:",
		"<strong>alex</strong>", "<span>alex@example.com</span>", "Example Cloud (cloud.example.com)",
		"Do not pass this page or its link on. Fylvo never asks you to send it to anybody.",
	} {
		require.Contains(t, body, text)
	}
	require.Nil(t, b.cookies[signIn.Name], "the cookie of the sign-in is taken away")
	target, err := url.Parse(link(t, body))
	require.NoError(t, err)
	require.Equal(t, "fylvo", target.Scheme)
	require.Equal(t, "connected", target.Host)
	require.Equal(t, url.Values{"attempt": {attempt}, "finish": {target.Query().Get("finish")}}, target.Query())
	code := target.Query().Get("finish")
	require.Regexp(t, `^[A-Za-z0-9_-]{32}$`, code)

	// The ticket is over, also for its own browser.
	require.Equal(t, http.StatusGone, b.get(startPath+ticket).Code)

	// The app looks, asks its user, and stores.
	seen, err := e.finish(e.alex, ConnectedFinishService{Code: code, Verifier: appVerifier, Preview: true})
	require.NoError(t, err)
	require.Equal(t, &connected.SignedIn{Provider: "drive", Account: "anna@example.com", AccountKind: connected.AccountAddress}, seen)
	stored, err := e.finish(e.alex, ConnectedFinishService{Code: code, Verifier: appVerifier})
	require.NoError(t, err)
	storage := stored.(*connected.Storage)
	require.Equal(t, "anna@example.com", storage.Account)
	// Its answer was lost: the same again.
	stored, err = e.finish(e.alex, ConnectedFinishService{Code: code, Verifier: appVerifier})
	require.NoError(t, err)
	require.Equal(t, storage.ID, stored.(*connected.Storage).ID)

	// Connecting again names the storage, not the account at the provider.
	c, _ = e.as(e.alex, "PATCH", "/api/v4/fylvo/connected/"+storage.ID)
	_, err = (&ConnectedRenameService{Name: `Photos <b>&"`}).Rename(c, storage.ID)
	require.NoError(t, err)
	ticket, attempt = e.authorize(e.alex, storage.ID)
	b = e.browser()
	page = b.get(startPath + ticket)
	body = page.Body.String()
	require.Contains(t, body, `again for this Fylvo account`)
	require.Contains(t, body, `Photos &lt;b&gt;&amp;&#34;`)
	require.NotContains(t, body, "<b>")
	require.NotContains(t, body, "anna@example.com")
	target, err = url.Parse(link(t, b.signIn(ticket)))
	require.NoError(t, err)
	require.Equal(t, attempt, target.Query().Get("attempt"))
	seen, err = e.finish(e.alex, ConnectedFinishService{Code: target.Query().Get("finish"), Verifier: appVerifier, Preview: true})
	require.NoError(t, err)
	require.Equal(t, storage.ID, seen.(*connected.SignedIn).Reconnect)
	stored, err = e.finish(e.alex, ConnectedFinishService{Code: target.Query().Get("finish"), Verifier: appVerifier})
	require.NoError(t, err)
	require.Equal(t, storage.ID, stored.(*connected.Storage).ID)
}

// Only the button of the page counts, pressed in the browser that owns the ticket. What
// is refused leaves the ticket to that browser.
func TestAppStartTakesOnlyItsOwnButton(t *testing.T) {
	e := newAppEnv(t, "https://cloud.example.com")
	ticket, _ := e.authorize(e.alex, "")
	b := e.browser()
	page := b.get(startPath + ticket).Body.String()
	hidden := hiddenValue.FindStringSubmatch(page)[1]
	sameOrigin := map[string]string{"Sec-Fetch-Site": "same-origin"}

	refused := func(name string, by *browser, form url.Values, headers map[string]string) {
		t.Helper()
		rec := by.request("POST", startPath+ticket, form, headers)
		require.Equal(t, http.StatusGone, rec.Code, name)
		checkPageHeaders(t, rec)
		require.Empty(t, rec.Header().Get("Location"), name)
		require.Empty(t, rec.Result().Cookies(), name)
		require.Contains(t, rec.Body.String(), "This link is no longer valid.", name)
		require.Equal(t, "fylvo://connected", link(t, rec.Body.String()), name)
	}

	// Another browser: no cookie, or one it made up with the value that would fit it.
	stranger := e.browser()
	refused("no cookie", stranger, url.Values{"confirm": {hidden}}, sameOrigin)
	stranger.cookies["x"] = &http.Cookie{Name: hashedName(connectedConfirmCookie, ticket), Value: "made-up", Path: startPath}
	refused("a cookie of its own", stranger, url.Values{"confirm": {confirmValue("made-up")}}, sameOrigin)
	refused("a cookie of its own and the real value", stranger, url.Values{"confirm": {hidden}}, sameOrigin)

	// The right browser, but not the form of the page.
	refused("no value", b, url.Values{}, sameOrigin)
	refused("empty value", b, url.Values{"confirm": {""}}, sameOrigin)
	refused("wrong value", b, url.Values{"confirm": {strings.Repeat("0", 64)}}, sameOrigin)
	// The right browser and the right form, sent from somewhere else.
	for _, site := range []string{"cross-site", "same-site", "none"} {
		refused(site, b, url.Values{"confirm": {hidden}}, map[string]string{"Sec-Fetch-Site": site})
		refused(site+" with our origin", b, url.Values{"confirm": {hidden}},
			map[string]string{"Sec-Fetch-Site": site, "Origin": "https://cloud.example.com"})
	}
	refused("foreign origin", b, url.Values{"confirm": {hidden}}, map[string]string{"Origin": "https://evil.example"})
	refused("our host on http", b, url.Values{"confirm": {hidden}}, map[string]string{"Origin": "http://cloud.example.com"})
	refused("origin null", b, url.Values{"confirm": {hidden}}, map[string]string{"Origin": "null"})
	refused("says nothing about where it comes from", b, url.Values{"confirm": {hidden}}, nil)

	// The form of the page is one short field of an ordinary form. Anything else is not
	// read: no upload, no long body.
	upload := func(contentType, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c, engine := gin.CreateTestContext(rec)
		engine.ContextWithFallback = true
		c.Request = httptest.NewRequest("POST", startPath+ticket, strings.NewReader(body))
		c.Request.Header.Set("Content-Type", contentType)
		c.Request.Header.Set("Sec-Fetch-Site", "same-origin")
		for _, cookie := range b.cookies {
			c.Request.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
		}
		util.WithValue(c, dependency.DepCtx{}, e.dep)
		c.Params = gin.Params{{Key: "ticket", Value: ticket}}
		ConnectedStart(c)
		c.Writer.WriteHeaderNow()
		return rec
	}
	multipart := "--x\r\nContent-Disposition: form-data; name=\"confirm\"\r\n\r\n" + hidden + "\r\n--x--\r\n"
	require.Equal(t, http.StatusGone, upload("multipart/form-data; boundary=x", multipart).Code)
	require.Equal(t, http.StatusGone, upload("text/plain", "confirm="+hidden).Code)
	require.Equal(t, http.StatusGone, upload("application/x-www-form-urlencoded", "pad="+strings.Repeat("a", 8192)+"&confirm="+hidden).Code)
	require.Equal(t, http.StatusSeeOther, upload("application/x-www-form-urlencoded; charset=UTF-8", "confirm="+hidden).Code)
	// A request without the cookie or from elsewhere is answered without a look at its body.
	for _, site := range []string{"same-origin", "cross-site"} {
		unread := httptest.NewRecorder()
		c, engine := gin.CreateTestContext(unread)
		engine.ContextWithFallback = true
		c.Request = httptest.NewRequest("POST", startPath+ticket, readFails{t})
		c.Request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		c.Request.Header.Set("Sec-Fetch-Site", site)
		if site == "cross-site" {
			for _, cookie := range b.cookies {
				c.Request.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
			}
		}
		util.WithValue(c, dependency.DepCtx{}, e.dep)
		c.Params = gin.Params{{Key: "ticket", Value: ticket}}
		ConnectedStart(c)
		require.Equal(t, http.StatusGone, unread.Code)
	}

	// Nothing of that used the ticket up or made anything new at the provider.
	rec := b.request("POST", startPath+ticket, url.Values{"confirm": {hidden}}, map[string]string{"Origin": "https://cloud.example.com"})
	require.Equal(t, http.StatusSeeOther, rec.Code, "a browser without Sec-Fetch-Site tells its origin")
	require.Equal(t, http.StatusSeeOther, b.press(ticket, page).Code)
}

// readFails is a body nobody may read.
type readFails struct{ t *testing.T }

func (r readFails) Read([]byte) (int, error) {
	r.t.Error("the body of a request that is not the button was read")
	return 0, io.EOF
}

// A browser that does not send Sec-Fetch-Site tells its origin, without the port its
// scheme implies, however the site address spells it.
func TestAppStartOriginWithDefaultPort(t *testing.T) {
	for site, origins := range map[string]map[string]bool{
		"https://cloud.example.com:443": {"https://cloud.example.com": true, "https://cloud.example.com:443": true, "https://CLOUD.example.com": true,
			"https://cloud.example.com:8443": false, "http://cloud.example.com": false, "https://cloud.example.com.evil.example": false},
		"https://cloud.example.com:8443": {"https://cloud.example.com:8443": true, "https://cloud.example.com": false},
		"https://cloud.example.com":      {"https://cloud.example.com:443": true, "cloud.example.com": false, "": false, "null": false, "https://": false},
	} {
		e := newAppEnv(t, site)
		for origin, ok := range origins {
			c, rec := e.as(e.alex, "POST", "/api/v4/fylvo/connected/authorize")
			res, err := (&ConnectedAuthorizeService{Provider: "drive", Client: "app", Challenge: appChallenge}).Authorize(c)
			require.NoError(t, err)
			require.Empty(t, rec.Result().Cookies())
			path := strings.TrimPrefix(res.URL, site)
			b := e.browser()
			hidden := hiddenValue.FindStringSubmatch(b.get(path).Body.String())[1]
			got := b.request("POST", path, url.Values{"confirm": {hidden}}, map[string]string{"Origin": origin})
			want := http.StatusGone
			if ok {
				want = http.StatusSeeOther
			}
			require.Equal(t, want, got.Code, site+" from "+origin)
		}
	}
}

// The ticket belongs to the first browser that really goes there.
func TestAppStartFirstBrowser(t *testing.T) {
	e := newAppEnv(t, "https://cloud.example.com")
	ticket, _ := e.authorize(e.alex, "")

	// Loading ahead binds nothing and shows nothing.
	b := e.browser()
	for _, headers := range []map[string]string{
		{"Sec-Purpose": "prefetch"}, {"Sec-Purpose": "prefetch;prerender"}, {"Purpose": "prefetch"}, {"X-Moz": "prefetch"}, {"X-Purpose": "preview"},
	} {
		rec := b.request("GET", startPath+ticket, nil, headers)
		require.Equal(t, http.StatusBadRequest, rec.Code)
		require.Empty(t, rec.Body.String())
		require.Empty(t, rec.Result().Cookies())
		require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	}
	// The real request of the same browser gets the page.
	page := b.get(startPath + ticket)
	require.Equal(t, http.StatusOK, page.Code)
	require.Len(t, b.cookies, 1)

	// A second browser, however it learned the address, gets nothing.
	other := e.browser()
	rec := other.get(startPath + ticket)
	require.Equal(t, http.StatusGone, rec.Code)
	checkPageHeaders(t, rec)
	require.Empty(t, rec.Result().Cookies())
	require.NotContains(t, rec.Body.String(), "alex")
	require.Equal(t, "fylvo://connected", link(t, rec.Body.String()))
	// Not after the first one pressed either.
	require.Equal(t, http.StatusSeeOther, b.press(ticket, page.Body.String()).Code)
	require.Equal(t, http.StatusGone, other.get(startPath+ticket).Code)

	// A ticket nobody has is the same page.
	rec = other.get(startPath + strings.Repeat("A", 43))
	require.Equal(t, http.StatusGone, rec.Code)
	require.Empty(t, rec.Result().Cookies())
}

// The provider's address in another browser: that one has no cookie of the sign-in. It is
// told nothing about the attempt, and the state is used up.
func TestAppCallbackPages(t *testing.T) {
	e := newAppEnv(t, "https://cloud.example.com")
	start := func() (b *browser, attempt, state string) {
		ticket, attempt := e.authorize(e.alex, "")
		b = e.browser()
		redirect := b.press(ticket, b.get(startPath+ticket).Body.String())
		consent, err := url.Parse(redirect.Header().Get("Location"))
		require.NoError(t, err)
		return b, attempt, consent.Query().Get("state")
	}

	b, attempt, state := start()
	rec := e.browser().get(callbackPath + "?state=" + url.QueryEscape(state) + "&code=granted")
	require.Equal(t, http.StatusOK, rec.Code)
	checkPageHeaders(t, rec)
	require.Empty(t, rec.Header().Get("Location"))
	require.Contains(t, rec.Body.String(), "This sign-in was not started in this browser.")
	require.NotContains(t, rec.Body.String(), attempt)
	require.NotContains(t, rec.Body.String(), "alex")
	require.Equal(t, "fylvo://connected", link(t, rec.Body.String()))
	// The browser that confirmed comes too late: the state is gone. It is still a page
	// for the app and no redirect into the web app.
	rec = b.get(callbackPath + "?state=" + url.QueryEscape(state) + "&code=granted")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Header().Get("Location"))
	require.Contains(t, rec.Body.String(), "The sign-in is no longer valid.")
	require.Equal(t, "fylvo://connected", link(t, rec.Body.String()))
	c, _ := e.as(e.alex, "GET", "/api/v4/fylvo/connected")
	list, err := ConnectedList(c)
	require.NoError(t, err)
	require.Empty(t, list.Storages)

	// Denied at the provider, in the browser that confirmed: the app is told.
	b, attempt, state = start()
	rec = b.get(callbackPath + "?state=" + url.QueryEscape(state) + "&error=access_denied")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "Access was not granted at the provider.")
	require.Equal(t, "fylvo://connected?attempt="+attempt+"&error=denied", link(t, rec.Body.String()))

	// The account is blocked while the user is at the provider: nothing to go back for.
	b, attempt, state = start()
	require.NoError(t, e.client.User.UpdateOneID(e.alex.ID).SetStatus(user.StatusManualBanned).Exec(e.ctx))
	rec = b.get(callbackPath + "?state=" + url.QueryEscape(state) + "&code=granted")
	require.Equal(t, "fylvo://connected?attempt="+attempt+"&error=expired", link(t, rec.Body.String()))
	require.NotContains(t, rec.Body.String(), "finish=")
	require.NoError(t, e.client.User.UpdateOneID(e.alex.ID).SetStatus(user.StatusActive).Exec(e.ctx))

	// A sign-in of the web app still ends in the web app.
	c, webRec := e.as(e.alex, "POST", "/api/v4/fylvo/connected/authorize")
	res, err := (&ConnectedAuthorizeService{Provider: "drive"}).Authorize(c)
	require.NoError(t, err)
	consent, err := url.Parse(res.URL)
	require.NoError(t, err)
	require.False(t, connected.IsAppState(consent.Query().Get("state")))
	web := e.browser()
	for _, cookie := range webRec.Result().Cookies() {
		web.cookies[cookie.Name] = cookie
	}
	rec = web.get(callbackPath + "?state=" + consent.Query().Get("state") + "&code=granted")
	require.Equal(t, http.StatusFound, rec.Code)
	target, err := url.Parse(rec.Header().Get("Location"))
	require.NoError(t, err)
	require.Equal(t, "/settings", target.Path)
	require.NotEmpty(t, target.Query().Get("finish"))
	// Its result is not for a verifier, and completes as ever.
	_, err = e.finish(e.alex, ConnectedFinishService{Code: target.Query().Get("finish"), Verifier: appVerifier})
	require.Equal(t, "verifier", connected.ReasonOf(err))
	_, err = e.finish(e.alex, ConnectedFinishService{Code: target.Query().Get("finish"), Preview: true})
	require.Equal(t, "verifier", connected.ReasonOf(err))
	_, err = e.finish(e.alex, ConnectedFinishService{Code: target.Query().Get("finish")})
	require.NoError(t, err)
	_, err = e.finish(e.alex, ConnectedFinishService{Code: target.Query().Get("finish")})
	require.Equal(t, connected.ReasonExpired, connected.ReasonOf(err))
}

func (e *appEnv) allowGroup(allowed bool) {
	e.t.Helper()
	g, err := e.client.Group.Query().Where(group.ID(2)).First(e.ctx)
	require.NoError(e.t, err)
	permissions := make(boolset.BooleanSet, len(*g.Permissions))
	copy(permissions, *g.Permissions)
	boolset.Set(types.GroupPermissionConnectedStorage, allowed, &permissions)
	require.NoError(e.t, e.client.Group.Update().Where(group.ID(2)).SetPermissions(&permissions).Exec(e.ctx))
}

// What was true when the app asked is checked again when the button is pressed.
func TestAppStartChecksTheAccountAgain(t *testing.T) {
	e := newAppEnv(t, "https://cloud.example.com")

	// The group loses the right.
	ticket, attempt := e.authorize(e.alex, "")
	b := e.browser()
	page := b.get(startPath + ticket).Body.String()
	e.allowGroup(false)
	rec := b.press(ticket, page)
	require.Equal(t, http.StatusForbidden, rec.Code)
	checkPageHeaders(t, rec)
	require.Empty(t, rec.Header().Get("Location"))
	require.Contains(t, rec.Body.String(), "This account may not connect storages.")
	require.Equal(t, "fylvo://connected?attempt="+attempt+"&error=not_allowed", link(t, rec.Body.String()))
	require.Empty(t, b.cookies, "the confirmation cookie is taken away")
	// The ticket is used up.
	e.allowGroup(true)
	require.Equal(t, http.StatusGone, b.get(startPath+ticket).Code)

	// The account is blocked: the page cannot name it any more, and the button ends the attempt.
	ticket, attempt = e.authorize(e.alex, "")
	b = e.browser()
	page = b.get(startPath + ticket).Body.String()
	require.NoError(t, e.client.User.UpdateOneID(e.alex.ID).SetStatus(user.StatusManualBanned).Exec(e.ctx))
	require.Equal(t, http.StatusGone, b.get(startPath+ticket).Code)
	rec = b.press(ticket, page)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "fylvo://connected?attempt="+attempt+"&error=expired", link(t, rec.Body.String()))
	require.NoError(t, e.client.User.UpdateOneID(e.alex.ID).SetStatus(user.StatusActive).Exec(e.ctx))

	// The account has to set a new password first: until then nothing of it starts, as
	// with its other requests.
	ticket, attempt = e.authorize(e.alex, "")
	b = e.browser()
	page = b.get(startPath + ticket).Body.String()
	require.NoError(t, e.client.User.UpdateOneID(e.alex.ID).SetSettings(&types.UserSetting{FylvoMustChangePassword: true}).Exec(e.ctx))
	rec = b.press(ticket, page)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "fylvo://connected?attempt="+attempt+"&error=expired", link(t, rec.Body.String()))
	require.NoError(t, e.client.User.UpdateOneID(e.alex.ID).SetSettings(&types.UserSetting{}).Exec(e.ctx))

	// The operator takes the provider out.
	ticket, attempt = e.authorize(e.alex, "")
	b = e.browser()
	page = b.get(startPath + ticket).Body.String()
	c, _ := e.as(e.alex, "PUT", "/api/v4/admin/fylvo/connected")
	_, err := (&ConnectedAdminService{Provider: "drive"}).Set(c)
	require.NoError(t, err)
	rec = b.press(ticket, page)
	require.Equal(t, http.StatusForbidden, rec.Code)
	require.Equal(t, "fylvo://connected?attempt="+attempt+"&error=setup", link(t, rec.Body.String()))
}

// The pages are the server's own: names are escaped, and a browser that prefers German
// gets German.
func TestAppPagesEscapeAndSpeakGerman(t *testing.T) {
	e := newAppEnv(t, "https://cloud.example.com")
	require.NoError(t, e.dep.SettingClient().Set(e.ctx, map[string]string{"siteName": `Cloud <i>"x"</i>`}))
	require.NoError(t, e.dep.KV().Delete(setting.KvSettingPrefix, "siteName"))
	sly, err := e.client.User.Create().SetEmail("sly@example.com").SetNick(`</strong><script>alert(1)</script>`).
		SetStatus(user.StatusActive).SetGroupID(2).SetSettings(&types.UserSetting{}).Save(e.ctx)
	require.NoError(t, err)
	sly, err = e.dep.UserClient().GetByID(context.WithValue(e.ctx, inventory.LoadUserGroup{}, true), sly.ID)
	require.NoError(t, err)
	e.identity = &connected.Identity{Key: "account-2", Shown: `<img src=x onerror=alert(1)>`, Kind: connected.AccountName}

	ticket, _ := e.authorize(sly, "")
	b := e.browser()
	german := map[string]string{"Accept-Language": "de-CH,de;q=0.9,en;q=0.8"}
	page := b.request("GET", startPath+ticket, nil, german)
	body := page.Body.String()
	checkPageHeaders(t, page)
	require.Contains(t, body, `<html lang="de">`)
	require.Contains(t, body, "Bestätige nur, wenn du das eben selbst in der Fylvo-App gestartet hast.")
	require.Contains(t, body, "&lt;/strong&gt;&lt;script&gt;alert(1)&lt;/script&gt;")
	require.Contains(t, body, "Cloud &lt;i&gt;&#34;x&#34;&lt;/i&gt;")
	require.NotContains(t, body, "<i>")

	redirect := b.press(ticket, body)
	consent, err := url.Parse(redirect.Header().Get("Location"))
	require.NoError(t, err)
	back := b.request("GET", callbackPath+"?state="+url.QueryEscape(consent.Query().Get("state"))+"&code=granted", nil, german)
	body = back.Body.String()
	checkPageHeaders(t, back)
	require.Contains(t, body, "Der Anbieter nennt nur diesen Namen, den jeder frei wählen kann:")
	require.Contains(t, body, "&lt;img src=x onerror=alert(1)&gt;")
	require.NotContains(t, body, "<img")
	require.Contains(t, body, "Gib diese Seite und ihren Link nicht weiter.")
	require.True(t, strings.HasPrefix(link(t, body), "fylvo://connected?attempt="))

	// English is what everybody else gets.
	for _, accept := range []string{"", "fr-FR,fr;q=0.9", "en-GB,de;q=0.5", "*"} {
		ticket, _ := e.authorize(sly, "")
		page := e.browser().request("GET", startPath+ticket, nil, map[string]string{"Accept-Language": accept})
		require.Contains(t, page.Body.String(), `<html lang="en">`, accept)
	}

	// A provider that names nobody.
	e.identity = &connected.Identity{Key: "account-3"}
	ticket, _ = e.authorize(sly, "")
	body = e.browser().signIn(ticket)
	require.Contains(t, body, "It did not name the account.")
}

// Where apps cannot connect storages: switched off by the operator, no https, or another
// address of the installation than its first.
func TestAppSigninUnavailable(t *testing.T) {
	off := func(t *testing.T, e *appEnv) {
		t.Helper()
		c, _ := e.as(e.alex, "GET", "/api/v4/fylvo/connected")
		list, err := ConnectedList(c)
		require.NoError(t, err)
		require.False(t, list.AppSignin)
		require.True(t, list.Allowed)

		c, rec := e.as(e.alex, "POST", "/api/v4/fylvo/connected/authorize")
		_, err = (&ConnectedAuthorizeService{Provider: "drive", Client: "app", Challenge: appChallenge}).Authorize(c)
		require.Equal(t, serializer.CodeConnectedUnavailable, appCode(err))
		require.Equal(t, connected.ReasonAppSigninOff, connected.ReasonOf(err))
		require.Empty(t, rec.Result().Cookies())
		for _, s := range []ConnectedFinishService{
			{Code: "x", Verifier: appVerifier}, {Code: "x", Verifier: appVerifier, Preview: true},
			{Code: "x", Verifier: appVerifier, Cancel: true}, {Code: "x", Preview: true}, {Code: "x", Cancel: true},
		} {
			_, err = e.finish(e.alex, s)
			require.Equal(t, connected.ReasonAppSigninOff, connected.ReasonOf(err))
		}

		for _, method := range []string{"GET", "POST"} {
			rec := e.browser().request(method, startPath+strings.Repeat("A", 43), url.Values{"confirm": {"x"}}, map[string]string{"Sec-Fetch-Site": "same-origin"})
			require.Equal(t, http.StatusNotFound, rec.Code)
			checkPageHeaders(t, rec)
			require.Empty(t, rec.Result().Cookies())
			require.Contains(t, rec.Body.String(), "Connecting a storage from the app is not available here.")
		}

		// The web flow is not touched by any of this.
		c, _ = e.as(e.alex, "POST", "/api/v4/fylvo/connected/authorize")
		_, err = (&ConnectedAuthorizeService{Provider: "drive"}).Authorize(c)
		require.NoError(t, err)
		_, err = e.finish(e.alex, ConnectedFinishService{Code: "made-up"})
		require.Equal(t, connected.ReasonExpired, connected.ReasonOf(err))
	}

	t.Run("switched off", func(t *testing.T) {
		t.Setenv(connected.EnvAppSignin, "0")
		off(t, newAppEnv(t, "https://cloud.example.com"))
	})
	t.Run("no https", func(t *testing.T) {
		off(t, newAppEnv(t, "http://cloud.example.com"))
	})
	t.Run("another value of the switch leaves it on", func(t *testing.T) {
		t.Setenv(connected.EnvAppSignin, "1")
		e := newAppEnv(t, "https://cloud.example.com")
		e.authorize(e.alex, "")
	})

	t.Run("another address of the installation", func(t *testing.T) {
		e := newAppEnv(t, "https://cloud.example.com,https://files.example.com,http://192.168.1.10:5212")
		at := func(host string, u *ent.User, method, target string) *gin.Context {
			c, _ := e.as(u, method, target)
			util.WithValue(c, requestinfo.RequestInfoCtx{}, &requestinfo.RequestInfo{Host: host})
			return c
		}
		// The app may talk to another https address of the installation: its browser
		// is sent to the first one.
		list, err := ConnectedList(at("files.example.com", e.alex, "GET", "/api/v4/fylvo/connected"))
		require.NoError(t, err)
		require.True(t, list.AppSignin)
		res, err := (&ConnectedAuthorizeService{Provider: "drive", Client: "app", Challenge: appChallenge}).
			Authorize(at("files.example.com", e.alex, "POST", "/api/v4/fylvo/connected/authorize"))
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(res.URL, "https://cloud.example.com"+startPath))
		ticket := strings.TrimPrefix(res.URL, "https://cloud.example.com"+startPath)

		// Not over an http address of it.
		list, err = ConnectedList(at("192.168.1.10:5212", e.alex, "GET", "/api/v4/fylvo/connected"))
		require.NoError(t, err)
		require.False(t, list.AppSignin)
		_, err = (&ConnectedAuthorizeService{Provider: "drive", Client: "app", Challenge: appChallenge}).
			Authorize(at("192.168.1.10:5212", e.alex, "POST", "/api/v4/fylvo/connected/authorize"))
		require.Equal(t, connected.ReasonAppSigninOff, connected.ReasonOf(err))
		_, err = (&ConnectedFinishService{Code: "x", Verifier: appVerifier}).Finish(at("192.168.1.10:5212", e.alex, "POST", "/api/v4/fylvo/connected/finish"))
		require.Equal(t, connected.ReasonAppSigninOff, connected.ReasonOf(err))

		// The pages exist under the first address only. Elsewhere they touch nothing:
		// the ticket is still free for the browser that comes to the right place.
		elsewhere := e.browser()
		elsewhere.host = "files.example.com"
		rec := elsewhere.get(startPath + ticket)
		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Empty(t, rec.Result().Cookies())
		b := e.browser()
		b.host = "cloud.example.com"
		page := b.get(startPath + ticket)
		require.Equal(t, http.StatusOK, page.Code)
		redirect := b.press(ticket, page.Body.String())
		require.Equal(t, http.StatusSeeOther, redirect.Code)
		consent, err := url.Parse(redirect.Header().Get("Location"))
		require.NoError(t, err)
		back := callbackPath + "?state=" + url.QueryEscape(consent.Query().Get("state")) + "&code=granted"
		// The callback under another address leaves the state alone.
		for name, cookie := range b.cookies {
			elsewhere.cookies[name] = cookie
		}
		rec = elsewhere.get(back)
		require.Equal(t, http.StatusNotFound, rec.Code)
		require.Empty(t, rec.Result().Cookies())
		rec = b.get(back)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, link(t, rec.Body.String()), "&finish=")
	})
}

// The fields of the two calls of the app.
func TestAppAuthorizeAndFinishRules(t *testing.T) {
	e := newAppEnv(t, "https://cloud.example.com")
	authorize := func(s ConnectedAuthorizeService) error {
		c, _ := e.as(e.alex, "POST", "/api/v4/fylvo/connected/authorize")
		s.Provider = "drive"
		_, err := s.Authorize(c)
		return err
	}
	for name, s := range map[string]ConnectedAuthorizeService{
		"a challenge without a client": {Challenge: appChallenge},
		"an unknown client":            {Client: "desktop", Challenge: appChallenge},
		"no challenge":                 {Client: "app"},
		"a challenge of another form":  {Client: "app", Challenge: appVerifier[:42]},
		"the padded form":              {Client: "app", Challenge: appChallenge + "="},
	} {
		err := authorize(s)
		require.Equal(t, serializer.CodeParamErr, appCode(err), name)
		require.Empty(t, connected.ReasonOf(err), name)
	}

	ticket, _ := e.authorize(e.alex, "")
	target, err := url.Parse(link(t, e.browser().signIn(ticket)))
	require.NoError(t, err)
	code := target.Query().Get("finish")
	bob := e.newUser("bob", 2)

	// Preview and cancel exclude each other; nothing happens to the result.
	_, err = e.finish(e.alex, ConnectedFinishService{Code: code, Verifier: appVerifier, Preview: true, Cancel: true})
	require.Equal(t, serializer.CodeParamErr, appCode(err))
	require.Empty(t, connected.ReasonOf(err))
	// Another account, no verifier, a wrong one: refused, and the result stays.
	for _, s := range []ConnectedFinishService{{Code: code, Verifier: appVerifier}, {Code: code, Verifier: appVerifier, Preview: true}, {Code: code, Verifier: appVerifier, Cancel: true}} {
		_, err = e.finish(bob, s)
		require.Equal(t, "account", connected.ReasonOf(err))
		s.Verifier = appChallenge
		_, err = e.finish(e.alex, s)
		require.Equal(t, "verifier", connected.ReasonOf(err))
	}
	_, err = e.finish(e.alex, ConnectedFinishService{Code: code})
	require.Equal(t, "verifier", connected.ReasonOf(err))

	// Without the right to connect: no look and nothing stored, the result stays.
	e.allowGroup(false)
	alex, err := e.dep.UserClient().GetByID(context.WithValue(e.ctx, inventory.LoadUserGroup{}, true), e.alex.ID)
	require.NoError(t, err)
	for _, s := range []ConnectedFinishService{{Code: code, Verifier: appVerifier}, {Code: code, Verifier: appVerifier, Preview: true}} {
		_, err = e.finish(alex, s)
		require.Equal(t, connected.ReasonNotAllowed, connected.ReasonOf(err))
	}
	// Dropping it needs only the account and the verifier.
	res, err := e.finish(alex, ConnectedFinishService{Code: code, Verifier: appVerifier, Cancel: true})
	require.NoError(t, err)
	require.Nil(t, res)
	e.allowGroup(true)
	_, err = e.finish(e.alex, ConnectedFinishService{Code: code, Verifier: appVerifier})
	require.Equal(t, connected.ReasonExpired, connected.ReasonOf(err))
	// A cancel for a code that is gone says so, which the app takes as done.
	_, err = e.finish(e.alex, ConnectedFinishService{Code: code, Verifier: appVerifier, Cancel: true})
	require.Equal(t, connected.ReasonExpired, connected.ReasonOf(err))
	c, _ := e.as(e.alex, "GET", "/api/v4/fylvo/connected")
	list, err := ConnectedList(c)
	require.NoError(t, err)
	require.Empty(t, list.Storages)
}

// The answers an app has to handle when it connects a storage. See connectedtest.Examples
// for how to get them as files.
func TestConnectedAppExamples(t *testing.T) {
	e := newAppEnv(t, "https://cloud.example.com")
	x := connectedtest.NewExamples(t)

	c, _ := e.as(e.alex, "GET", "/api/v4/fylvo/connected")
	list, err := ConnectedList(c)
	require.NoError(t, err)
	x.Add("app-storages", list, nil)

	c, _ = e.as(e.alex, "POST", "/api/v4/fylvo/connected/authorize")
	started, err := (&ConnectedAuthorizeService{Provider: "drive", Client: "app", Challenge: appChallenge}).Authorize(c)
	require.NoError(t, err)
	x.Add("app-authorize", started, nil)
	ticket := strings.TrimPrefix(started.URL, "https://cloud.example.com"+startPath)
	target, err := url.Parse(link(t, e.browser().signIn(ticket)))
	require.NoError(t, err)
	code := target.Query().Get("finish")

	_, err = e.finish(e.alex, ConnectedFinishService{Code: code})
	x.Add("app-error-verifier", nil, err)
	_, err = e.finish(e.newUser("sam", 2), ConnectedFinishService{Code: code, Verifier: appVerifier})
	x.Add("app-error-account", nil, err)
	seen, err := e.finish(e.alex, ConnectedFinishService{Code: code, Verifier: appVerifier, Preview: true})
	require.NoError(t, err)
	x.Add("app-finish-preview", seen, nil)
	stored, err := e.finish(e.alex, ConnectedFinishService{Code: code, Verifier: appVerifier})
	require.NoError(t, err)
	x.Add("app-finish", stored, nil)

	// A second sign-in with the same account at the provider, and one the user says no to.
	ticket, _ = e.authorize(e.alex, "")
	target, err = url.Parse(link(t, e.browser().signIn(ticket)))
	require.NoError(t, err)
	_, err = e.finish(e.alex, ConnectedFinishService{Code: target.Query().Get("finish"), Verifier: appVerifier})
	require.Equal(t, connected.ReasonExists, connected.ReasonOf(err))
	x.Add("app-error-exists", nil, err)
	ticket, _ = e.authorize(e.alex, "")
	target, err = url.Parse(link(t, e.browser().signIn(ticket)))
	require.NoError(t, err)
	res, err := e.finish(e.alex, ConnectedFinishService{Code: target.Query().Get("finish"), Verifier: appVerifier, Cancel: true})
	require.NoError(t, err)
	x.Add("app-finish-cancel", res, nil)
	_, err = e.finish(e.alex, ConnectedFinishService{Code: target.Query().Get("finish"), Verifier: appVerifier})
	x.Add("app-error-expired", nil, err)

	t.Setenv(connected.EnvAppSignin, "0")
	c, _ = e.as(e.alex, "POST", "/api/v4/fylvo/connected/authorize")
	_, err = (&ConnectedAuthorizeService{Provider: "drive", Client: "app", Challenge: appChallenge}).Authorize(c)
	require.Equal(t, connected.ReasonAppSigninOff, connected.ReasonOf(err))
	x.Add("app-error-signin-off", nil, err)
}

// Trouble of the database says nothing about the account: the ticket stays, and a code
// the provider's answer already produced still gets to the app.
func TestAppPagesWhenTheDatabaseFails(t *testing.T) {
	e := newAppEnv(t, "https://cloud.example.com")
	// One sign-in is at the provider, another one has its page open.
	atProvider, attempt := e.authorize(e.alex, "")
	first := e.browser()
	redirect := first.press(atProvider, first.get(startPath+atProvider).Body.String())
	consent, err := url.Parse(redirect.Header().Get("Location"))
	require.NoError(t, err)
	waiting, _ := e.authorize(e.alex, "")
	second := e.browser()
	form := second.get(startPath + waiting).Body.String()

	require.NoError(t, e.client.Close())

	// The page cannot name the account right now. It says so and uses nothing up.
	rec := second.get(startPath + waiting)
	require.Equal(t, http.StatusOK, rec.Code)
	checkPageHeaders(t, rec)
	require.Contains(t, rec.Body.String(), "Please load this page again in a moment.")
	require.NotContains(t, rec.Body.String(), "fylvo://")
	// The same for the button.
	rec = second.press(waiting, form)
	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Header().Get("Location"))
	require.Contains(t, rec.Body.String(), "Please load this page again in a moment.")
	require.Len(t, second.cookies, 1, "the confirmation cookie stays")

	// The provider answered for the first one. Its state is used up now, so the page
	// carries the code, without the name of the account it cannot read.
	rec = first.get(callbackPath + "?state=" + url.QueryEscape(consent.Query().Get("state")) + "&code=granted")
	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "<strong>anna@example.com</strong>")
	require.NotContains(t, body, "alex")
	target, err := url.Parse(link(t, body))
	require.NoError(t, err)
	require.Equal(t, attempt, target.Query().Get("attempt"))
	require.Regexp(t, `^[A-Za-z0-9_-]{32}$`, target.Query().Get("finish"))
}
