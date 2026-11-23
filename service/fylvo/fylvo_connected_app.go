package fylvo

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/ent"
	"github.com/cloudreve/Cloudreve/v4/inventory"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/gin-gonic/gin"
	"golang.org/x/text/language"
)

// Connecting a storage from an app (see pkg/connected/oauth_app.go).
//
// The app is signed in, the system browser it opens is not. These are the pages of the
// server in between: the start page, which names the account before the provider is
// involved, and the page the provider's answer ends on, with the link back to the app.

const (
	// connectedClientApp is the value of "client" in authorize that asks for this flow.
	connectedClientApp = "app"
	// connectedStartPath is where the start page of a ticket is.
	connectedStartPath = constants.APIPrefix + "/fylvo/connected/start"
	// connectedConfirmCookie ties a ticket to the browser that opened it first. It is
	// sent only to the page of that ticket, and only by that page itself.
	connectedConfirmCookie = "fylvo_confirm"
	// connectedConfirmAge is as long as a ticket and the sign-in made from it can live.
	connectedConfirmAge = 13 * 60
	// connectedAppCookie is connectedCookie for a sign-in an app started.
	connectedAppCookie = "fylvo_connect_app"
	// connectedConfirmField is the field of the form that carries the value derived from
	// the confirmation cookie.
	connectedConfirmField = "confirm"
	// connectedFormSize is the most a POST of the start page is read of: its form is one
	// short field.
	connectedFormSize = 4096
)

// appSigninHere tells whether apps can connect storages through the address this request
// came under. A request under a host that is none of the installation's addresses counts
// as its first address, as everywhere in the server.
func appSigninHere(c *gin.Context, dep dependency.Dep) bool {
	return connected.AppSignin(primarySiteURL(c, dep), dep.SettingProvider().SiteURL(c))
}

// underFirstAddress tells whether this request came under the first site address, the
// one the cookies of the flow are for and the provider sends the browser back to.
func underFirstAddress(c *gin.Context, dep dependency.Dep) bool {
	return dep.SettingProvider().SiteURL(c).Host == primarySiteURL(c, dep).Host
}

// authorizeApp keeps the sign-in an app asks for and answers the address of its start
// page. Nothing is at the provider yet and no cookie is set: the app is not the browser.
func (s *ConnectedAuthorizeService) authorizeApp(c *gin.Context, dep dependency.Dep, user *ent.User) (*ConnectedAuthorizeResponse, error) {
	if !appSigninHere(c, dep) {
		return nil, connected.ErrAppSigninOff
	}
	ticket, attempt, err := connected.For(dep).AuthorizeApp(c, user.ID, s.Provider, s.Reconnect, s.Challenge)
	if err != nil {
		return nil, err
	}
	start := primarySiteURL(c, dep).ResolveReference(&url.URL{Path: connectedStartPath + "/" + ticket})
	return &ConnectedAuthorizeResponse{URL: start.String(), Attempt: attempt}, nil
}

func hashedName(prefix, value string) string {
	sum := sha256.Sum256([]byte(value))
	return prefix + "_" + hex.EncodeToString(sum[:8])
}

// connectedAppCookieName is connectedCookieName for a sign-in an app started.
func connectedAppCookieName(address string) string {
	return connectedAppCookie + strings.TrimPrefix(connectedCookieName(address), connectedCookie)
}

// setConfirmCookie sets or, with a negative age, removes the confirmation cookie of a
// ticket. Strict: no other site can make the browser send it, not even with a link.
func setConfirmCookie(c *gin.Context, dep dependency.Dep, ticket, value string, age int) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     hashedName(connectedConfirmCookie, ticket),
		Value:    value,
		Path:     connectedStartPath + "/" + ticket,
		MaxAge:   age,
		HttpOnly: true,
		Secure:   primarySiteURL(c, dep).Scheme == "https",
		SameSite: http.SameSiteStrictMode,
	})
}

// confirmValue is what the form of the start page carries for a confirmation cookie. The
// same browser gets the same working form on every load, and a request that knows the
// address alone has neither.
func confirmValue(cookie string) string {
	sum := sha256.Sum256([]byte("fylvo connected start form\x00" + cookie))
	return hex.EncodeToString(sum[:])
}

// ownOrigin tells whether a POST says it comes from a page of this installation's first
// address. A request that says nothing about where it comes from is refused.
func ownOrigin(c *gin.Context, primary *url.URL) bool {
	if site := c.GetHeader("Sec-Fetch-Site"); site != "" {
		return site == "same-origin"
	}
	origin, err := url.Parse(c.GetHeader("Origin"))
	return err == nil && origin.Host != "" && origin.Scheme == primary.Scheme &&
		strings.EqualFold(origin.Hostname(), primary.Hostname()) && portOf(origin) == portOf(primary)
}

// portOf is the port of an address, the one of its scheme when it names none: a browser
// leaves that one out of an origin, a site address may spell it out.
func portOf(u *url.URL) string {
	if port := u.Port(); port != "" {
		return port
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// isPrefetch tells whether a browser loads the address ahead of the user going there.
func isPrefetch(c *gin.Context) bool {
	for _, name := range []string{"Sec-Purpose", "Purpose", "X-Purpose", "X-Moz"} {
		value := strings.ToLower(c.GetHeader(name))
		if strings.Contains(value, "prefetch") || strings.Contains(value, "prerender") || strings.Contains(value, "preview") {
			return true
		}
	}
	return false
}

// ConnectedStart serves the start page of a ticket (GET) and takes its button (POST).
func ConnectedStart(c *gin.Context) {
	dep := dependency.FromContext(c)
	connectedPageHeaders(c)
	if isPrefetch(c) {
		// Nothing is bound and nothing shown: the browser asks again when the user
		// really goes there.
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	page := newConnectedPage(c, dep)
	if !appSigninHere(c, dep) || !underFirstAddress(c, dep) {
		page.unavailable()
		return
	}

	m := connected.For(dep)
	ticket := c.Param("ticket")
	cookie, _ := c.Cookie(hashedName(connectedConfirmCookie, ticket))

	if c.Request.Method != http.MethodPost {
		view, confirm, err := m.OpenTicket(ticket, cookie)
		if err != nil {
			page.invalid()
			return
		}
		if confirm != "" {
			// The first browser that asks: the ticket is its own from now on.
			setConfirmCookie(c, dep, ticket, confirm, connectedConfirmAge)
			cookie = confirm
		}
		page.confirm(view, cookie, false)
		return
	}

	// The button. It counts only from the browser that owns the ticket, from the form
	// of the page above, and from that page: no other site can send it for the user.
	// Nothing of the body is read before the cookie and the origin are there, and then
	// only as much as the form of the page can be.
	if cookie == "" || !ownOrigin(c, primarySiteURL(c, dep)) ||
		!strings.HasPrefix(c.ContentType(), "application/x-www-form-urlencoded") {
		page.invalid()
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, connectedFormSize)
	hidden := c.PostForm(connectedConfirmField)
	if hidden == "" || subtle.ConstantTimeCompare([]byte(hidden), []byte(confirmValue(cookie))) != 1 {
		page.invalid()
		return
	}

	address, signIn, view, err := m.StartTicket(c, ticket, cookie, connectedCallbackURL(c, dep),
		func(ctx context.Context, userID int) error {
			// As for a request of the signed-in account: it is there, active, and not
			// held until it has set a new password.
			u, err := dep.UserClient().GetLoginUserByID(ctx, userID)
			switch {
			case ent.IsNotFound(err):
				return connected.ErrFlowExpired
			case err != nil:
				return serializer.NewError(serializer.CodeDBError, "Failed to load the account", err)
			case inventory.MustChangePassword(u):
				return connected.ErrFlowExpired
			case !mayConnectStorages(u):
				return connected.ErrNotAllowed
			}
			return nil
		})
	var refused *connected.TicketRefused
	switch {
	case err == nil:
		// The cookie of the sign-in, as the web flow sets it with its answer to authorize.
		setConnectedCookie(c, dep, connectedAppCookieName(address), signIn, connectedCookieAge)
		c.Redirect(http.StatusSeeOther, address)
	case errors.As(err, &refused):
		setConfirmCookie(c, dep, ticket, "", -1)
		page.ended(http.StatusForbidden, view.Attempt, refused.Reason)
	case errors.Is(err, connected.ErrFlowWait) && view != nil:
		page.confirm(view, cookie, true)
	default:
		page.invalid()
	}
}

// connectedAppCallback answers the browser that comes back from a provider for a sign-in
// an app started: a page with the link back to the app. What the link carries is of use
// only to the app that started, signed in as the account that started.
func connectedAppCallback(c *gin.Context, dep dependency.Dep) {
	connectedPageHeaders(c)
	page := newConnectedPage(c, dep)
	if !underFirstAddress(c, dep) {
		// The cookie of the sign-in is not here. The state is left alone.
		page.unavailable()
		return
	}
	name := connectedAppCookieName(c.Request.URL.String())
	cookie, _ := c.Cookie(name)
	setConnectedCookie(c, dep, name, "", -1)

	out, err := connected.For(dep).Callback(c, c.Query("state"), cookie, c.Query("code"), c.Query("error"))
	switch {
	case err == nil:
		page.signedIn(&out)
	case out.Attempt != "":
		// This browser is the one that confirmed, and the sign-in ended.
		page.ended(http.StatusOK, out.Attempt, connected.FlowErrorCode(err))
	default:
		// Nothing about the attempt is proven: the link back names none.
		page.unproven(connected.FlowErrorCode(err))
	}
}

// connectedPageHeaders are the headers of every page of this flow: never kept, never
// framed, nothing loaded, and no address of such a page in a Referer.
func connectedPageHeaders(c *gin.Context) {
	h := c.Writer.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "strict-origin")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	// No form-action: browsers apply it to the redirect that follows the form, which
	// goes to the provider.
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; frame-ancestors 'none'")
}

// connectedTexts are the texts of the pages in one language.
type connectedTexts struct {
	Title         string
	Server        string
	Connect       string // provider
	Reconnect     string // storage, provider
	ReconnectBare string // provider
	Warn          string
	Next          string // provider
	Button        string // provider
	Again         string
	Later         string
	Back          string
	Invalid       string
	Unavailable   string
	SignedInHead  string
	SignedIn      map[string]string // by kind of account name; provider
	ToAccount     string
	Private       string
	EndedHead     string
	Reasons       map[string]string
}

var connectedLanguages = language.NewMatcher([]language.Tag{language.English, language.German})

var connectedPageTexts = []*connectedTexts{
	{
		Title:         "Connect a storage",
		Server:        "Server",
		Connect:       "The Fylvo app asks to connect %s to this Fylvo account:",
		Reconnect:     "The Fylvo app asks to connect the storage \"%s\" (%s) again for this Fylvo account:",
		ReconnectBare: "The Fylvo app asks to connect a storage at %s again for this Fylvo account:",
		Warn:          "Confirm only if you started this yourself in the Fylvo app just now.",
		Next:          "Next you sign in at %s.",
		Button:        "Continue to %s",
		Again:         "That did not work right now. Please press the button again in a moment.",
		Later:         "That did not work right now. Please load this page again in a moment.",
		Back:          "Back to Fylvo",
		Invalid:       "This link is no longer valid. Go back to the app and start again.",
		Unavailable:   "Connecting a storage from the app is not available here.",
		SignedInHead:  "Signed in",
		SignedIn: map[string]string{
			connected.AccountAddress:    "You signed in at %s as:",
			connected.AccountSignInName: "You signed in at %s with the sign-in name:",
			connected.AccountName:       "You signed in at %s. It told only this name, which anybody can choose:",
			connected.AccountNone:       "You signed in at %s. It did not name the account.",
		},
		ToAccount: "Back in the app you can connect it to this Fylvo account:",
		Private:   "Do not pass this page or its link on. Fylvo never asks you to send it to anybody.",
		EndedHead: "Not connected",
		Reasons: map[string]string{
			"denied":      "Access was not granted at the provider.",
			"provider":    "The provider did not complete the sign-in. Try again later.",
			"setup":       "This server is not set up for this provider right now.",
			"expired":     "The sign-in is no longer valid. Go back to the app and start again.",
			"not_allowed": "This account may not connect storages.",
			"too_many":    "This account has as many connected storages as it can have.",
			"browser":     "This sign-in was not started in this browser. Go back to the app and start again.",
		},
	},
	{
		Title:         "Speicher verbinden",
		Server:        "Server",
		Connect:       "Die Fylvo-App möchte %s mit diesem Fylvo-Konto verbinden:",
		Reconnect:     "Die Fylvo-App möchte den Speicher «%s» (%s) für dieses Fylvo-Konto neu verbinden:",
		ReconnectBare: "Die Fylvo-App möchte einen Speicher bei %s für dieses Fylvo-Konto neu verbinden:",
		Warn:          "Bestätige nur, wenn du das eben selbst in der Fylvo-App gestartet hast.",
		Next:          "Danach meldest du dich bei %s an.",
		Button:        "Weiter zu %s",
		Again:         "Das hat gerade nicht geklappt. Bitte drück den Knopf gleich noch einmal.",
		Later:         "Das hat gerade nicht geklappt. Bitte lade diese Seite gleich noch einmal.",
		Back:          "Zurück zu Fylvo",
		Invalid:       "Dieser Link ist nicht mehr gültig. Geh zurück zur App und starte neu.",
		Unavailable:   "Das Verbinden eines Speichers aus der App ist hier nicht verfügbar.",
		SignedInHead:  "Angemeldet",
		SignedIn: map[string]string{
			connected.AccountAddress:    "Du hast dich bei %s angemeldet als:",
			connected.AccountSignInName: "Du hast dich bei %s angemeldet mit dem Anmeldenamen:",
			connected.AccountName:       "Du hast dich bei %s angemeldet. Der Anbieter nennt nur diesen Namen, den jeder frei wählen kann:",
			connected.AccountNone:       "Du hast dich bei %s angemeldet. Der Anbieter nennt das Konto nicht.",
		},
		ToAccount: "Zurück in der App kannst du es mit diesem Fylvo-Konto verbinden:",
		Private:   "Gib diese Seite und ihren Link nicht weiter. Fylvo bittet dich nie, sie jemandem zu schicken.",
		EndedHead: "Nicht verbunden",
		Reasons: map[string]string{
			"denied":      "Der Zugriff wurde beim Anbieter nicht erlaubt.",
			"provider":    "Der Anbieter hat die Anmeldung nicht abgeschlossen. Versuch es später noch einmal.",
			"setup":       "Dieser Server ist für diesen Anbieter gerade nicht eingerichtet.",
			"expired":     "Die Anmeldung ist nicht mehr gültig. Geh zurück zur App und starte neu.",
			"not_allowed": "Dieses Konto darf keine Speicher verbinden.",
			"too_many":    "Dieses Konto hat bereits so viele verbundene Speicher wie möglich.",
			"browser":     "Diese Anmeldung wurde nicht in diesem Browser gestartet. Geh zurück zur App und starte neu.",
		},
	},
}

// connectedPageData is what one page shows. Every value is escaped by the template. The
// link back to the app is written in the template with its scheme as fixed text; only
// the values of its query come from here.
type connectedPageData struct {
	Lang   string
	T      *connectedTexts
	Server string
	Host   string
	// Kind picks the page: confirm, signedin, ended, later or plain.
	Kind string
	// confirm
	Lead, Next, Button string
	Nick, Email        string
	Confirm            string
	Again              bool
	// signedin
	Account string
	Finish  string
	// ended and plain
	Message string
	Attempt string
	Reason  string
}

var connectedPageTemplate = template.Must(template.New("connected").Parse(`<!DOCTYPE html>
<html lang="{{.Lang}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex">
<title>{{.T.Title}}</title>
<style>
body { margin: 0; font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; background: #f4f5f7; color: #1c1d21; line-height: 1.45; }
main { max-width: 30em; margin: 0 auto; padding: 2em 1.25em 3em; }
h1 { font-size: 1.35em; margin: 0 0 1em; }
p { margin: 0 0 1em; }
.server { font-size: 0.9em; color: #5d6068; margin-bottom: 1.5em; }
.account { margin: 0 0 1.25em; padding: 0.9em 1em; background: #fff; border: 1px solid #d5d7dc; border-radius: 0.6em; overflow-wrap: anywhere; }
.account strong { display: block; font-size: 1.45em; line-height: 1.25; }
.account span { display: block; font-size: 1.2em; }
.warn { font-weight: 600; }
.note { color: #a4370f; font-weight: 600; }
button, .button { display: block; box-sizing: border-box; width: 100%; margin-top: 1.5em; padding: 0.85em 1em; border: 0; border-radius: 0.6em; background: #2456c8; color: #fff; font: inherit; font-weight: 600; text-align: center; text-decoration: none; cursor: pointer; }
@media (prefers-color-scheme: dark) {
body { background: #17181b; color: #ecedf0; }
.server { color: #a3a6ae; }
.account { background: #222428; border-color: #3a3d44; }
.note { color: #ff9d73; }
button, .button { background: #4b7bec; }
}
</style>
</head>
<body>
<main>
<p class="server">{{.T.Server}}: {{if .Server}}{{.Server}} ({{.Host}}){{else}}{{.Host}}{{end}}</p>
{{if eq .Kind "confirm"}}<h1>{{.T.Title}}</h1>
<p>{{.Lead}}</p>
<p class="account"><strong>{{.Nick}}</strong><span>{{.Email}}</span></p>
<p class="warn">{{.T.Warn}}</p>
<p>{{.Next}}</p>
{{if .Again}}<p class="note">{{.T.Again}}</p>
{{end}}<form method="post">
<input type="hidden" name="confirm" value="{{.Confirm}}">
<button type="submit">{{.Button}}</button>
</form>
{{else if eq .Kind "signedin"}}<h1>{{.T.SignedInHead}}</h1>
<p>{{.Lead}}</p>
{{if .Account}}<p class="account"><strong>{{.Account}}</strong></p>
{{end}}{{if .Email}}<p>{{.T.ToAccount}}</p>
<p class="account"><strong>{{.Nick}}</strong><span>{{.Email}}</span></p>
{{end}}<p class="warn">{{.T.Private}}</p>
<a class="button" href="fylvo://connected?attempt={{.Attempt}}&amp;finish={{.Finish}}">{{.T.Back}}</a>
{{else if eq .Kind "ended"}}<h1>{{.T.EndedHead}}</h1>
<p>{{.Message}}</p>
<a class="button" href="fylvo://connected?attempt={{.Attempt}}&amp;error={{.Reason}}">{{.T.Back}}</a>
{{else if eq .Kind "later"}}<h1>{{.T.Title}}</h1>
<p class="note">{{.Message}}</p>
{{else}}<h1>{{.T.Title}}</h1>
<p>{{.Message}}</p>
<a class="button" href="fylvo://connected">{{.T.Back}}</a>
{{end}}</main>
</body>
</html>
`))

// connectedPage writes the pages of the flow for one request.
type connectedPage struct {
	c    *gin.Context
	dep  dependency.Dep
	data connectedPageData
}

func newConnectedPage(c *gin.Context, dep dependency.Dep) *connectedPage {
	lang, texts := "en", connectedPageTexts[0]
	if tags, _, err := language.ParseAcceptLanguage(c.GetHeader("Accept-Language")); err == nil && len(tags) > 0 {
		if _, i, _ := connectedLanguages.Match(tags...); i == 1 {
			lang, texts = "de", connectedPageTexts[1]
		}
	}
	return &connectedPage{c: c, dep: dep, data: connectedPageData{
		Lang: lang, T: texts,
		Server: dep.SettingProvider().SiteBasic(c).Name,
		Host:   primarySiteURL(c, dep).Host,
	}}
}

func (p *connectedPage) write(status int) {
	p.c.Status(status)
	p.c.Header("Content-Type", "text/html; charset=utf-8")
	_ = connectedPageTemplate.Execute(p.c.Writer, &p.data)
}

func (p *connectedPage) plain(status int, message string) {
	p.data.Kind, p.data.Message = "plain", message
	p.write(status)
}

// invalid: the ticket is not one this browser can use (any more).
func (p *connectedPage) invalid() { p.plain(http.StatusGone, p.data.T.Invalid) }

// unavailable: the flow is off, or this is not the address it runs under.
func (p *connectedPage) unavailable() { p.plain(http.StatusNotFound, p.data.T.Unavailable) }

// unproven: the sign-in ended and nothing ties this browser to an attempt.
func (p *connectedPage) unproven(reason string) {
	p.plain(http.StatusOK, p.reason(reason))
}

func (p *connectedPage) reason(reason string) string {
	if text := p.data.T.Reasons[reason]; text != "" {
		return text
	}
	return p.data.T.Reasons["provider"]
}

// ended: the sign-in of this attempt ended without a result, and the app is told why.
func (p *connectedPage) ended(status int, attempt, reason string) {
	p.data.Kind, p.data.Attempt, p.data.Reason, p.data.Message = "ended", attempt, reason, p.reason(reason)
	p.write(status)
}

// later: the server could not read what the page needs right now. Nothing was used up;
// loading the page again shows it.
func (p *connectedPage) later() {
	p.data.Kind, p.data.Message = "later", p.data.T.Later
	p.write(http.StatusOK)
}

// account loads the Fylvo account a page names. found is false for one that is gone or
// no longer active; an error is trouble of the database, which says nothing about it.
func (p *connectedPage) account(userID int) (found bool, err error) {
	u, err := p.dep.UserClient().GetLoginUserByID(p.c, userID)
	switch {
	case ent.IsNotFound(err):
		return false, nil
	case err != nil:
		return false, err
	}
	p.data.Nick, p.data.Email = u.Nick, u.Email
	return true, nil
}

// confirm: the page that names the account and asks before the provider is involved.
func (p *connectedPage) confirm(view *connected.TicketView, cookie string, again bool) {
	switch found, err := p.account(view.UserID); {
	case err != nil:
		p.later()
		return
	case !found:
		p.invalid()
		return
	}
	m := connected.For(p.dep)
	provider := m.ProviderName(view.Provider)
	t := p.data.T
	p.data.Lead = fmt.Sprintf(t.Connect, provider)
	if view.Reconnect != nil {
		// The name of the storage, not the account at the provider: that stays between
		// the app and its user.
		p.data.Lead = fmt.Sprintf(t.ReconnectBare, provider)
		if storage, err := m.Get(p.c, view.UserID, view.Reconnect.ID); err == nil {
			p.data.Lead = fmt.Sprintf(t.Reconnect, storage.Name, provider)
		}
	}
	p.data.Kind = "confirm"
	p.data.Next, p.data.Button = fmt.Sprintf(t.Next, provider), fmt.Sprintf(t.Button, provider)
	p.data.Confirm, p.data.Again = confirmValue(cookie), again
	p.write(http.StatusOK)
}

// signedIn: the provider answered. Both accounts are named once more, and the link
// carries the code the app completes with.
func (p *connectedPage) signedIn(out *connected.Outcome) {
	// The state is used up: this page is the only way the code gets to the app. If the
	// account cannot be read right now the page goes without its name; the app names
	// it once more before anything is stored.
	if found, err := p.account(out.UserID); err == nil && !found {
		p.ended(http.StatusOK, out.Attempt, connected.ReasonExpired)
		return
	}
	kind, account := out.AccountKind, out.Account
	if account == "" || p.data.T.SignedIn[kind] == "" {
		kind, account = connected.AccountNone, ""
	}
	p.data.Kind = "signedin"
	p.data.Lead = fmt.Sprintf(p.data.T.SignedIn[kind], connected.For(p.dep).ProviderName(out.Provider))
	p.data.Account, p.data.Attempt, p.data.Finish = account, out.Attempt, out.Finish
	p.write(http.StatusOK)
}
