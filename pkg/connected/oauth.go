package connected

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/graph"
)

// Spec describes how a provider signs accounts in. The addresses are constants of
// the provider, never taken from a request.
type Spec struct {
	Type     string
	Name     string
	AuthURL  string
	TokenURL string
	Scopes   []string
	// AuthParams are added to the consent address.
	AuthParams map[string]string
	// AppAuthParams are added on top for a sign-in that an app started (see oauth_app.go).
	AppAuthParams map[string]string
	// Identify reads who signed in and which drive is theirs.
	Identify func(ctx context.Context, call APICall) (*Identity, error)
	// Revoke takes the access back at the provider, for providers that can. It ends the
	// access of every token the account granted to this installation.
	Revoke func(ctx context.Context, client *http.Client, refreshToken string) error
	// NewProvider returns the client that reads and writes files for one connection.
	// props are the stored properties of the connection (drive_id, drive_type).
	NewProvider func(client *http.Client, auth provider.Auth, props map[string]string) provider.Provider
}

// Token is the OAuth token of a connection, in the JSON form it is sealed in.
type Token struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
}

// ParseToken reads a stored token.
func ParseToken(raw string) (*Token, error) {
	var t Token
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		return nil, errors.New("stored token is not readable")
	}
	return &t, nil
}

func (t *Token) String() string {
	raw, _ := json.Marshal(t)
	return string(raw)
}

// APICall fetches a JSON document from the provider with the fresh access token.
type APICall func(ctx context.Context, address string, out any) error

// Identity is the provider account behind a connection.
type Identity struct {
	// Key never changes for the account and its drive. Reconnecting must lead to the same key.
	Key string
	// Label is shown as "connected as".
	Label string
	// Shown and Kind are for the look an app takes before it stores a new connection:
	// the name of the account that says most about whose it is, and what kind of name
	// that is (see the Account* kinds). A name anybody can set is never called an address.
	Shown string
	Kind  string
	// Props go into the setup of the remote (drive id and type for OneDrive).
	Props map[string]string
}

// The kinds of name a provider tells for an account.
const (
	// AccountAddress: the address the provider itself knows the account by (Google).
	AccountAddress = "address"
	// AccountSignInName: the name the account signs in with (Microsoft). It looks like an
	// address and proves no mailbox.
	AccountSignInName = "sign_in_name"
	// AccountName: only a display name, which the owner of the account chooses.
	AccountName = "name"
	// AccountNone: the provider told no name at all.
	AccountNone = "none"
)

// shown picks what an app shows of an account: the first name that is there.
func shown(names ...[2]string) (name, kind string) {
	for _, n := range names {
		if n[0] != "" {
			return n[0], n[1]
		}
	}
	return "", AccountNone
}

// Specs returns the providers of this version.
func Specs() map[string]*Spec {
	return map[string]*Spec{
		ProviderDrive: {
			Type:     ProviderDrive,
			Name:     "Google Drive",
			AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL: "https://oauth2.googleapis.com/token",
			Scopes:   []string{"https://www.googleapis.com/auth/drive"},
			// Without these Google hands out no refresh token on a second consent.
			AuthParams: map[string]string{"access_type": "offline", "prompt": "consent"},
			Identify: func(ctx context.Context, call APICall) (*Identity, error) {
				return identifyDrive(ctx, call, "https://www.googleapis.com")
			},
			Revoke: func(ctx context.Context, client *http.Client, refreshToken string) error {
				return revokeGoogle(ctx, client, "https://oauth2.googleapis.com/revoke", refreshToken)
			},
			NewProvider: func(client *http.Client, auth provider.Auth, _ map[string]string) provider.Provider {
				return drive.New(drive.Options{HTTP: client, Auth: auth})
			},
		},
		ProviderOneDrive: {
			Type:     ProviderOneDrive,
			Name:     "OneDrive",
			AuthURL:  "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
			TokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token",
			Scopes:   []string{"Files.ReadWrite", "offline_access", "User.Read"},
			// Microsoft passes a signed-in user through without a screen when it can. A
			// sign-in from an app asks for the account chooser, so there always is one.
			AppAuthParams: map[string]string{"prompt": "select_account"},
			Identify: func(ctx context.Context, call APICall) (*Identity, error) {
				return identifyOneDrive(ctx, call, "https://graph.microsoft.com")
			},
			NewProvider: func(client *http.Client, auth provider.Auth, props map[string]string) provider.Provider {
				return graph.New(graph.Options{HTTP: client, Auth: auth, DriveID: props["drive_id"]})
			},
		},
	}
}

func identifyDrive(ctx context.Context, call APICall, base string) (*Identity, error) {
	var about struct {
		User struct {
			DisplayName  string `json:"displayName"`
			EmailAddress string `json:"emailAddress"`
			PermissionID string `json:"permissionId"`
		} `json:"user"`
	}
	address := base + "/drive/v3/about?fields=" + url.QueryEscape("user(displayName,emailAddress,permissionId)")
	if err := call(ctx, address, &about); err != nil {
		return nil, err
	}
	if about.User.PermissionID == "" {
		return nil, errors.New("the provider did not say who signed in")
	}
	label := about.User.EmailAddress
	if label == "" {
		label = about.User.DisplayName
	}
	identity := &Identity{Key: about.User.PermissionID, Label: label}
	identity.Shown, identity.Kind = shown(
		[2]string{about.User.EmailAddress, AccountAddress}, [2]string{about.User.DisplayName, AccountName})
	return identity, nil
}

func revokeGoogle(ctx context.Context, client *http.Client, address, refreshToken string) error {
	form := url.Values{"token": {refreshToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	fixed := *client
	fixed.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := fixed.Do(req)
	if err != nil {
		return errors.New("the provider did not answer")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("the provider answered %d", res.StatusCode)
	}
	return nil
}

func identifyOneDrive(ctx context.Context, call APICall, base string) (*Identity, error) {
	var me struct {
		ID                string `json:"id"`
		DisplayName       string `json:"displayName"`
		Mail              string `json:"mail"`
		UserPrincipalName string `json:"userPrincipalName"`
	}
	if err := call(ctx, base+"/v1.0/me?$select=id,displayName,mail,userPrincipalName", &me); err != nil {
		return nil, err
	}
	var drive struct {
		ID        string `json:"id"`
		DriveType string `json:"driveType"`
	}
	if err := call(ctx, base+"/v1.0/me/drive?$select=id,driveType", &drive); err != nil {
		return nil, err
	}
	if me.ID == "" || drive.ID == "" {
		return nil, errors.New("the provider did not say who signed in")
	}
	label := me.Mail
	if label == "" {
		label = me.UserPrincipalName
	}
	if label == "" {
		label = me.DisplayName
	}
	identity := &Identity{
		Key:   me.ID + "|" + drive.ID,
		Label: label,
		Props: map[string]string{"drive_id": drive.ID, "drive_type": drive.DriveType},
	}
	// Not "mail": the administrator of any organisation at Microsoft can set it to an
	// address of somebody else.
	identity.Shown, identity.Kind = shown(
		[2]string{me.UserPrincipalName, AccountSignInName}, [2]string{me.DisplayName, AccountName})
	return identity, nil
}

// Errors of the sign-in flow. They are fixed texts: what a provider answers is
// never passed on.
var (
	ErrFlowExpired  = errors.New("the sign-in was not completed in time or was already used")
	ErrFlowBrowser  = errors.New("the sign-in was not started in this browser")
	ErrFlowDenied   = errors.New("access was not granted at the provider")
	ErrFlowProvider = errors.New("the provider did not complete the sign-in")
	ErrFlowSetup    = errors.New("the provider refused the client of this installation")
	ErrFlowAccount  = errors.New("the sign-in belongs to another account")
	// ErrFlowWait: access is being taken back at this provider right now. A sign-in
	// that started now could be for the same account and lose its new token to that.
	ErrFlowWait = errors.New("the provider is busy with another step, try again in a moment")
)

const (
	stateLife  = 10 * time.Minute
	finishLife = 5 * time.Minute
	// callbackTime bounds the calls to the provider that complete a sign-in.
	callbackTime = time.Minute
	// maxStates is how many sign-ins one account can have under way. A further one
	// takes the place of the oldest.
	maxStates = 5
)

// Client is the OAuth client of the installation for one provider.
type Client struct {
	ID       string
	Secret   string
	Redirect string
}

// Reconnect names the connection whose token a flow renews.
type Reconnect struct {
	ID         string
	Generation int
}

type flowState struct {
	userID     int
	provider   string
	verifier   string
	browser    [32]byte
	redirect   string
	reconnect  *Reconnect
	expires    time.Time
	clientID   string
	clientAuth string
	// made is when the sign-in was asked for: the oldest gives way to a new one.
	made time.Time
	// app marks a sign-in an app started, challenge and attempt are those of its ticket.
	app       bool
	challenge string
	attempt   string
}

// Granted is the outcome of a completed sign-in, waiting to be stored.
type Granted struct {
	UserID    int
	Provider  string
	Identity  *Identity
	Reconnect *Reconnect
	// Token is the token in the JSON form it is stored in.
	Token   string
	Scopes  string
	expires time.Time
	made    time.Time
	// App marks the result of a sign-in an app started: it is handed out only with the
	// verifier whose challenge this is. Attempt is the id the app knows it by.
	App       bool
	Challenge string
	Attempt   string
}

// Outcome is what a callback tells about its sign-in besides its error.
type Outcome struct {
	// Finish is the code the result waits under.
	Finish string
	// App: the sign-in was started by an app. Known also when the state is gone.
	App bool
	// Attempt is the id of the sign-in of an app, set once the browser that came back
	// proved to be the one that confirmed. UserID and Provider are set with it.
	Attempt  string
	UserID   int
	Provider string
	// Account and AccountKind name the account that signed in at the provider, when the
	// sign-in worked (see Identity.Shown).
	Account     string
	AccountKind string
}

// Flow runs sign-ins at providers. States and results live in memory only and
// are taken out when used, so each one works exactly once.
type Flow struct {
	specs map[string]*Spec
	http  *http.Client
	now   func() time.Time

	mu      sync.Mutex
	states  map[string]*flowState
	granted map[string]*Granted
	// tickets and receipts belong to sign-ins from apps (see oauth_app.go).
	tickets  map[string]*flowTicket
	receipts map[string]*receipt
	// exchanging counts the callbacks per provider that are talking to the provider.
	exchanging map[string]int
	// revoking counts, per provider, the tokens that are being taken back right now.
	revoking map[string]int
	// talking counts, per account, the callbacks that are talking to a provider, and
	// dropped marks the accounts whose callbacks must not store what they get.
	talking map[int]int
	dropped map[int]bool
}

func NewFlow(specs map[string]*Spec, client *http.Client) *Flow {
	// Calls to a provider's token endpoint and API go to the address they name and
	// nowhere else: no redirect is followed, so no code, secret or token travels on.
	fixed := *client
	fixed.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Flow{
		specs: specs, http: &fixed, now: time.Now,
		states: map[string]*flowState{}, granted: map[string]*Granted{}, exchanging: map[string]int{},
		tickets: map[string]*flowTicket{}, receipts: map[string]*receipt{},
		revoking: map[string]int{}, talking: map[int]int{}, dropped: map[int]bool{},
	}
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// sweep drops what ran out. The caller holds f.mu.
func (f *Flow) sweep() {
	now := f.now()
	for k, s := range f.states {
		if now.After(s.expires) {
			delete(f.states, k)
		}
	}
	for k, g := range f.granted {
		if now.After(g.expires) {
			delete(f.granted, k)
		}
	}
	for k, t := range f.tickets {
		// One that was not used runs out by itself; one that was used lives as long
		// as the state it became.
		if (t.state == "" && now.After(t.made.Add(ticketLife))) || (t.state != "" && f.states[t.state] == nil) {
			delete(f.tickets, k)
		}
	}
	for k, r := range f.receipts {
		if now.After(r.expires) {
			delete(f.receipts, k)
		}
	}
}

// place makes room for one more sign-in of an account. Everything it has under way
// counts: tickets that were not used, sign-ins that wait for the provider, ones that
// talk to it, and ones that wait to be stored. A further one takes the place of the one
// asked for longest ago among tickets and waiting sign-ins, else of the oldest result;
// when all of them talk to a provider right now it has to wait. The caller holds f.mu.
func (f *Flow) place(userID int) error {
	var (
		oldest        func()
		oldestMade    time.Time
		oldestGranted string
	)
	older := func(made time.Time, drop func()) {
		if oldest == nil || made.Before(oldestMade) {
			oldest, oldestMade = drop, made
		}
	}
	open := f.talking[userID]
	for k, s := range f.states {
		if s.userID == userID {
			open++
			older(s.made, func() { delete(f.states, k) })
		}
	}
	for k, t := range f.tickets {
		if t.userID == userID && t.state == "" {
			open++
			older(t.made, func() { delete(f.tickets, k) })
		}
	}
	for k, g := range f.granted {
		if g.UserID != userID {
			continue
		}
		open++
		if oldestGranted == "" || g.made.Before(f.granted[oldestGranted].made) {
			oldestGranted = k
		}
	}
	switch {
	case open < maxStates:
	case oldest != nil:
		oldest()
	case oldestGranted != "":
		delete(f.granted, oldestGranted)
	default:
		return ErrFlowWait
	}
	return nil
}

// consent is the address of the provider a sign-in starts at.
func (f *Flow) consent(spec *Spec, client Client, state, verifier string, app bool) string {
	challenge := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {client.ID},
		"redirect_uri":          {client.Redirect},
		"scope":                 {strings.Join(spec.Scopes, " ")},
		"state":                 {state},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	for k, v := range spec.AuthParams {
		q.Set(k, v)
	}
	if app {
		for k, v := range spec.AppAuthParams {
			q.Set(k, v)
		}
	}
	address := spec.AuthURL
	if strings.HasPrefix(address, "/") {
		// A sign-in page this installation serves itself (development builds only): it
		// is at the address the browser reaches the installation by.
		if back, err := url.Parse(client.Redirect); err == nil {
			address = back.Scheme + "://" + back.Host + address
		}
	}
	return address + "?" + q.Encode()
}

// Begin starts a sign-in for a user. It returns the consent address of the
// provider and the value of the cookie that ties the sign-in to this browser.
func (f *Flow) Begin(userID int, provider string, client Client, reconnect *Reconnect) (address, cookie string, err error) {
	spec := f.specs[provider]
	if spec == nil {
		return "", "", fmt.Errorf("unknown provider %q", provider)
	}

	state, verifier, cookie := randomToken(24), randomToken(32), randomToken(32)

	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweep()
	if f.revoking[provider] > 0 {
		return "", "", ErrFlowWait
	}
	if err := f.place(userID); err != nil {
		return "", "", err
	}
	f.states[state] = &flowState{
		userID: userID, provider: provider, verifier: verifier, browser: sha256.Sum256([]byte(cookie)),
		redirect: client.Redirect, reconnect: reconnect, expires: f.now().Add(stateLife),
		clientID: client.ID, clientAuth: client.Secret, made: f.now(),
	}
	return f.consent(spec, client, state, verifier, false), cookie, nil
}

// Callback completes the provider's side of a sign-in: it takes the state out,
// checks the browser, exchanges the code and asks who signed in. The result waits
// under the returned code for Finish. providerError is the "error" parameter of
// the callback, if any.
func (f *Flow) Callback(ctx context.Context, state, cookie, code, providerError string) (out Outcome, err error) {
	f.mu.Lock()
	f.sweep()
	s := f.states[state]
	delete(f.states, state)
	if s != nil {
		f.exchanging[s.provider]++
		f.talking[s.userID]++
	}
	f.mu.Unlock()

	if s == nil {
		// The kind of the sign-in still shows in the state value.
		return Outcome{App: IsAppState(state)}, ErrFlowExpired
	}
	out.App = s.app
	// A provider that stalls must not keep the sign-in under way for good.
	ctx, cancel := context.WithTimeout(ctx, callbackTime)
	defer cancel()
	// Counted until the result is stored below, so Busy never has a gap. done gives the
	// count back; the last step below does it together with keeping the result.
	counted := true
	done := func() {
		if !counted {
			return
		}
		counted = false
		f.exchanging[s.provider]--
		if f.talking[s.userID]--; f.talking[s.userID] <= 0 {
			delete(f.talking, s.userID)
			delete(f.dropped, s.userID)
		}
	}
	defer func() {
		f.mu.Lock()
		done()
		f.mu.Unlock()
	}()
	sum := sha256.Sum256([]byte(cookie))
	if cookie == "" || subtle.ConstantTimeCompare(sum[:], s.browser[:]) != 1 {
		return out, ErrFlowBrowser
	}
	// From here on the browser is the one that started (or, for an app, confirmed).
	out.Attempt, out.UserID, out.Provider = s.attempt, s.userID, s.provider
	if providerError != "" || code == "" {
		return out, ErrFlowDenied
	}

	spec := f.specs[s.provider]
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {s.redirect},
		"client_id":     {s.clientID},
		"client_secret": {s.clientAuth},
		"code_verifier": {s.verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, spec.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return out, ErrFlowProvider
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := f.http.Do(req)
	if err != nil {
		return out, ErrFlowProvider
	}
	defer res.Body.Close()

	var tok struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
		Error        string `json:"error"`
	}
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if json.Unmarshal(raw, &tok) != nil {
		return out, ErrFlowProvider
	}
	switch {
	case tok.Error == "invalid_client" || tok.Error == "unauthorized_client":
		return out, ErrFlowSetup
	case res.StatusCode != http.StatusOK || tok.Error != "":
		return out, ErrFlowProvider
	case tok.AccessToken == "" || tok.RefreshToken == "":
		// Without a refresh token the connection would end within the hour.
		return out, ErrFlowProvider
	}

	identity, err := spec.Identify(ctx, f.apiCall(tok.AccessToken))
	if err != nil {
		return out, ErrFlowProvider
	}

	tokenType := tok.TokenType
	if tokenType == "" {
		tokenType = "Bearer"
	}
	token, _ := json.Marshal(map[string]any{
		"access_token":  tok.AccessToken,
		"token_type":    tokenType,
		"refresh_token": tok.RefreshToken,
		"expiry":        f.now().Add(time.Duration(tok.ExpiresIn) * time.Second).UTC().Format(time.RFC3339),
	})

	out.Finish = randomToken(24)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dropped[s.userID] {
		// The account was removed while the provider answered: its token is let go.
		out.Finish = ""
		return out, ErrFlowExpired
	}
	// The result takes the place of its callback in one step, so the sign-in is never
	// counted twice.
	f.granted[out.Finish] = &Granted{
		UserID: s.userID, Provider: s.provider, Identity: identity, Reconnect: s.reconnect,
		Token: string(token), Scopes: tok.Scope, expires: f.now().Add(finishLife), made: s.made,
		App: s.app, Challenge: s.challenge, Attempt: s.attempt,
	}
	done()
	out.Account, out.AccountKind = identity.Shown, identity.Kind
	return out, nil
}

// apiCall returns a function that reads JSON from the provider with the token.
// The token goes only to the address asked for; redirects are not followed.
func (f *Flow) apiCall(accessToken string) APICall {
	client := f.http
	return func(ctx context.Context, address string, out any) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("Accept", "application/json")
		res, err := client.Do(req)
		if err != nil {
			return errors.New("the provider did not answer")
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("the provider answered %d", res.StatusCode)
		}
		return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(out)
	}
}

// Finish hands the result of a sign-in to the user who started it, once. A result of a
// sign-in from an app needs the verifier of that app (see oauth_app.go).
func (f *Flow) Finish(userID int, finish, verifier string) (*Granted, error) {
	return f.take(userID, finish, verifier, true)
}

// Revoking asks whether a token of this provider may be taken back now. It may not while
// a sign-in at the provider is under way (see busy). When it may, no sign-in at the
// provider starts until done is called.
func (f *Flow) Revoking(provider string) (done func(), ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweep()
	if f.busy(provider) {
		return nil, false
	}
	f.revoking[provider]++
	var once sync.Once
	return func() {
		once.Do(func() {
			f.mu.Lock()
			f.revoking[provider]--
			f.mu.Unlock()
		})
	}, true
}

// Forget drops the sign-ins of an account that are under way or wait to be stored.
func (f *Flow) Forget(userID int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for k, s := range f.states {
		if s.userID == userID {
			delete(f.states, k)
		}
	}
	for k, g := range f.granted {
		if g.UserID == userID {
			delete(f.granted, k)
		}
	}
	for k, t := range f.tickets {
		if t.userID == userID {
			delete(f.tickets, k)
		}
	}
	for k, r := range f.receipts {
		if r.userID == userID {
			delete(f.receipts, k)
		}
	}
	if f.talking[userID] > 0 {
		f.dropped[userID] = true
	}
}

// Busy tells whether a sign-in at this provider is under way (see busy).
func (f *Flow) Busy(provider string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweep()
	return f.busy(provider)
}

// busy tells whether a sign-in at this provider is under way: started, talking to the
// provider, or waiting to be stored. As long as one is, no token of that provider may be
// revoked, because it is not yet known which provider account the sign-in is for. The
// caller holds f.mu.
func (f *Flow) busy(provider string) bool {
	if f.exchanging[provider] > 0 {
		return true
	}
	for _, s := range f.states {
		if s.provider == provider {
			return true
		}
	}
	for _, g := range f.granted {
		if g.Provider == provider {
			return true
		}
	}
	return false
}
