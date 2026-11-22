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
	// Identify reads who signed in and which drive is theirs.
	Identify func(ctx context.Context, call APICall) (*Identity, error)
	// Revoke takes the access back at the provider, for providers that can. It ends the
	// access of every token the account granted to this installation.
	Revoke func(ctx context.Context, client *http.Client, refreshToken string) error
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
	// Props go into the setup of the remote (drive id and type for OneDrive).
	Props map[string]string
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
		},
		ProviderOneDrive: {
			Type:     ProviderOneDrive,
			Name:     "OneDrive",
			AuthURL:  "https://login.microsoftonline.com/common/oauth2/v2.0/authorize",
			TokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token",
			Scopes:   []string{"Files.ReadWrite", "offline_access", "User.Read"},
			Identify: func(ctx context.Context, call APICall) (*Identity, error) {
				return identifyOneDrive(ctx, call, "https://graph.microsoft.com")
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
	return &Identity{Key: about.User.PermissionID, Label: label}, nil
}

func revokeGoogle(ctx context.Context, client *http.Client, address, refreshToken string) error {
	form := url.Values{"token": {refreshToken}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := client.Do(req)
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
	return &Identity{
		Key:   me.ID + "|" + drive.ID,
		Label: label,
		Props: map[string]string{"drive_id": drive.ID, "drive_type": drive.DriveType},
	}, nil
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
)

const (
	stateLife  = 10 * time.Minute
	finishLife = 5 * time.Minute
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
}

// Granted is the outcome of a completed sign-in, waiting to be stored.
type Granted struct {
	UserID    int
	Provider  string
	Identity  *Identity
	Reconnect *Reconnect
	// Token is the token as rclone keeps it (JSON).
	Token   string
	Scopes  string
	expires time.Time
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
	// exchanging counts the callbacks per provider that are talking to the provider.
	exchanging map[string]int
}

func NewFlow(specs map[string]*Spec, client *http.Client) *Flow {
	return &Flow{
		specs: specs, http: client, now: time.Now,
		states: map[string]*flowState{}, granted: map[string]*Granted{}, exchanging: map[string]int{},
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
}

// Begin starts a sign-in for a user. It returns the consent address of the
// provider and the value of the cookie that ties the sign-in to this browser.
func (f *Flow) Begin(userID int, provider string, client Client, reconnect *Reconnect) (address, cookie string, err error) {
	spec := f.specs[provider]
	if spec == nil {
		return "", "", fmt.Errorf("unknown provider %q", provider)
	}

	state, verifier, cookie := randomToken(24), randomToken(32), randomToken(32)
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

	f.mu.Lock()
	f.sweep()
	f.states[state] = &flowState{
		userID: userID, provider: provider, verifier: verifier, browser: sha256.Sum256([]byte(cookie)),
		redirect: client.Redirect, reconnect: reconnect, expires: f.now().Add(stateLife),
		clientID: client.ID, clientAuth: client.Secret,
	}
	f.mu.Unlock()

	return spec.AuthURL + "?" + q.Encode(), cookie, nil
}

// Callback completes the provider's side of a sign-in: it takes the state out,
// checks the browser, exchanges the code and asks who signed in. The result waits
// under the returned code for Finish. providerError is the "error" parameter of
// the callback, if any.
func (f *Flow) Callback(ctx context.Context, state, cookie, code, providerError string) (finish string, err error) {
	f.mu.Lock()
	f.sweep()
	s := f.states[state]
	delete(f.states, state)
	if s != nil {
		f.exchanging[s.provider]++
	}
	f.mu.Unlock()

	if s == nil {
		return "", ErrFlowExpired
	}
	// Counted until the result is stored below, so Busy never has a gap.
	defer func() {
		f.mu.Lock()
		f.exchanging[s.provider]--
		f.mu.Unlock()
	}()
	sum := sha256.Sum256([]byte(cookie))
	if cookie == "" || subtle.ConstantTimeCompare(sum[:], s.browser[:]) != 1 {
		return "", ErrFlowBrowser
	}
	if providerError != "" || code == "" {
		return "", ErrFlowDenied
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
		return "", ErrFlowProvider
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := f.http.Do(req)
	if err != nil {
		return "", ErrFlowProvider
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
		return "", ErrFlowProvider
	}
	switch {
	case tok.Error == "invalid_client" || tok.Error == "unauthorized_client":
		return "", ErrFlowSetup
	case res.StatusCode != http.StatusOK || tok.Error != "":
		return "", ErrFlowProvider
	case tok.AccessToken == "" || tok.RefreshToken == "":
		// Without a refresh token the connection would end within the hour.
		return "", ErrFlowProvider
	}

	identity, err := spec.Identify(ctx, f.apiCall(tok.AccessToken))
	if err != nil {
		return "", ErrFlowProvider
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

	finish = randomToken(24)
	f.mu.Lock()
	f.granted[finish] = &Granted{
		UserID: s.userID, Provider: s.provider, Identity: identity, Reconnect: s.reconnect,
		Token: string(token), Scopes: tok.Scope, expires: f.now().Add(finishLife),
	}
	f.mu.Unlock()
	return finish, nil
}

// apiCall returns a function that reads JSON from the provider with the token.
// The token goes only to the address asked for; redirects are not followed.
func (f *Flow) apiCall(accessToken string) APICall {
	client := *f.http
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
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

// Finish hands the result of a sign-in to the user who started it, once.
func (f *Flow) Finish(userID int, finish string) (*Granted, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweep()
	g := f.granted[finish]
	if g == nil {
		return nil, ErrFlowExpired
	}
	if g.UserID != userID {
		// Left in place: the user who started the sign-in can still finish it.
		return nil, ErrFlowAccount
	}
	delete(f.granted, finish)
	return g, nil
}

// Busy tells whether a sign-in at this provider is under way: started, talking to the
// provider, or waiting to be stored. As long as one is, no token of that provider may be
// revoked, because it is not yet known which provider account the sign-in is for.
func (f *Flow) Busy(provider string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sweep()
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
