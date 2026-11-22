package connected

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeProvider is an OAuth provider with a token endpoint and an identity endpoint.
type fakeProvider struct {
	*httptest.Server
	mu sync.Mutex
	// codes maps a handed-out code to the challenge it was issued for.
	codes       map[string]string
	exchanges   int
	identities  int
	tokenAnswer func(w http.ResponseWriter) bool
	account     string
	bearerSeen  []string
}

func newFakeProvider(t *testing.T) *fakeProvider {
	p := &fakeProvider{codes: map[string]string{}, account: "account-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.exchanges++
		_ = r.ParseForm()
		if p.tokenAnswer != nil && p.tokenAnswer(w) {
			return
		}
		challenge, ok := p.codes[r.PostForm.Get("code")]
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge ||
			r.PostForm.Get("client_id") != "client-id" || r.PostForm.Get("client_secret") != "client-secret" ||
			r.PostForm.Get("redirect_uri") != "https://cloud.example.com/api/v4/fylvo/connected/callback" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "secret detail"})
			return
		}
		delete(p.codes, r.PostForm.Get("code"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "access-1", "refresh_token": "refresh-1", "token_type": "Bearer", "expires_in": 3600, "scope": "drive",
		})
	})
	mux.HandleFunc("/drive/v3/about", func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.identities++
		p.bearerSeen = append(p.bearerSeen, r.Header.Get("Authorization"))
		_ = json.NewEncoder(w).Encode(map[string]any{"user": map[string]string{
			"displayName": "Alex", "emailAddress": "user@example.com", "permissionId": p.account,
		}})
	})
	p.Server = httptest.NewServer(mux)
	t.Cleanup(p.Close)
	return p
}

// consent plays the user agreeing at the provider: it returns the code the
// provider would send back for the consent address.
func (p *fakeProvider) consent(t *testing.T, address string) (state, code string) {
	t.Helper()
	u, err := url.Parse(address)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("response_type") != "code" || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" ||
		q.Get("access_type") != "offline" || q.Get("prompt") != "consent" || q.Get("client_id") != "client-id" ||
		q.Get("scope") != "https://www.googleapis.com/auth/drive" {
		t.Fatalf("consent address is incomplete: %s", address)
	}
	code = randomToken(8)
	p.mu.Lock()
	p.codes[code] = q.Get("code_challenge")
	p.mu.Unlock()
	return q.Get("state"), code
}

func testFlow(t *testing.T) (*Flow, *fakeProvider, Client) {
	p := newFakeProvider(t)
	spec := Specs()[ProviderDrive]
	spec.AuthURL = p.URL + "/auth"
	spec.TokenURL = p.URL + "/token"
	spec.Identify = func(ctx context.Context, call APICall) (*Identity, error) {
		return identifyDrive(ctx, call, p.URL)
	}
	flow := NewFlow(map[string]*Spec{ProviderDrive: spec}, p.Client())
	client := Client{ID: "client-id", Secret: "client-secret", Redirect: "https://cloud.example.com/api/v4/fylvo/connected/callback"}
	return flow, p, client
}

func TestFlowHappyPath(t *testing.T) {
	flow, p, client := testFlow(t)
	ctx := context.Background()

	if flow.Busy(ProviderDrive) {
		t.Fatal("under way before anything started")
	}
	address, cookie, err := flow.Begin(7, ProviderDrive, client, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !flow.Busy(ProviderDrive) || flow.Busy(ProviderOneDrive) {
		t.Fatal("a started sign-in is under way for its provider only")
	}
	if strings.Contains(address, "client-secret") {
		t.Fatal("the consent address carries the client secret")
	}
	state, code := p.consent(t, address)

	finish, err := flow.Callback(ctx, state, cookie, code, "")
	if err != nil {
		t.Fatal(err)
	}
	if !flow.Busy(ProviderDrive) {
		t.Fatal("the completed sign-in is not reported as under way")
	}

	// Another account cannot take the result, and does not use it up.
	if _, err := flow.Finish(8, finish); !errors.Is(err, ErrFlowAccount) {
		t.Fatalf("finish by another account: %v", err)
	}
	g, err := flow.Finish(7, finish)
	if err != nil {
		t.Fatal(err)
	}
	if g.UserID != 7 || g.Identity.Key != "account-1" || g.Identity.Label != "user@example.com" {
		t.Fatalf("granted: %+v %+v", g, g.Identity)
	}
	var token map[string]string
	if json.Unmarshal([]byte(g.Token), &token) != nil || token["refresh_token"] != "refresh-1" ||
		token["access_token"] != "access-1" || token["expiry"] == "" {
		t.Fatalf("token: %s", g.Token)
	}
	if len(p.bearerSeen) != 1 || p.bearerSeen[0] != "Bearer access-1" {
		t.Fatalf("identity call: %v", p.bearerSeen)
	}

	// Everything works once.
	if _, err := flow.Finish(7, finish); !errors.Is(err, ErrFlowExpired) {
		t.Fatalf("second finish: %v", err)
	}
	if _, err := flow.Callback(ctx, state, cookie, code, ""); !errors.Is(err, ErrFlowExpired) {
		t.Fatalf("second callback: %v", err)
	}
	if flow.Busy(ProviderDrive) {
		t.Fatal("still under way after finish")
	}
}

func TestFlowRefusals(t *testing.T) {
	ctx := context.Background()

	t.Run("another browser", func(t *testing.T) {
		flow, p, client := testFlow(t)
		address, _, _ := flow.Begin(7, ProviderDrive, client, nil)
		state, code := p.consent(t, address)
		// Somebody else was sent the consent address and returns without the cookie.
		if _, err := flow.Callback(ctx, state, "", code, ""); !errors.Is(err, ErrFlowBrowser) {
			t.Fatalf("no cookie: %v", err)
		}
		if p.exchanges != 0 {
			t.Fatal("the code was exchanged for a foreign browser")
		}
		// The state is used up by the attempt.
		address, cookie, _ := flow.Begin(7, ProviderDrive, client, nil)
		state, code = p.consent(t, address)
		if _, err := flow.Callback(ctx, state, cookie+"x", code, ""); !errors.Is(err, ErrFlowBrowser) {
			t.Fatalf("wrong cookie: %v", err)
		}
		if _, err := flow.Callback(ctx, state, cookie, code, ""); !errors.Is(err, ErrFlowExpired) {
			t.Fatalf("state after a failed attempt: %v", err)
		}
	})

	t.Run("cookie of another sign-in", func(t *testing.T) {
		flow, p, client := testFlow(t)
		addressA, _, _ := flow.Begin(7, ProviderDrive, client, nil)
		_, cookieB, _ := flow.Begin(8, ProviderDrive, client, nil)
		state, code := p.consent(t, addressA)
		if _, err := flow.Callback(ctx, state, cookieB, code, ""); !errors.Is(err, ErrFlowBrowser) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("unknown state", func(t *testing.T) {
		flow, _, _ := testFlow(t)
		if _, err := flow.Callback(ctx, "made-up", "cookie", "code", ""); !errors.Is(err, ErrFlowExpired) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("too late", func(t *testing.T) {
		flow, p, client := testFlow(t)
		now := time.Unix(1800000000, 0)
		flow.now = func() time.Time { return now }
		address, cookie, _ := flow.Begin(7, ProviderDrive, client, nil)
		state, code := p.consent(t, address)
		now = now.Add(stateLife + time.Second)
		if _, err := flow.Callback(ctx, state, cookie, code, ""); !errors.Is(err, ErrFlowExpired) {
			t.Fatalf("late callback: %v", err)
		}

		address, cookie, _ = flow.Begin(7, ProviderDrive, client, nil)
		state, code = p.consent(t, address)
		finish, err := flow.Callback(ctx, state, cookie, code, "")
		if err != nil {
			t.Fatal(err)
		}
		now = now.Add(finishLife + time.Second)
		if _, err := flow.Finish(7, finish); !errors.Is(err, ErrFlowExpired) {
			t.Fatalf("late finish: %v", err)
		}
	})

	t.Run("user said no", func(t *testing.T) {
		flow, p, client := testFlow(t)
		address, cookie, _ := flow.Begin(7, ProviderDrive, client, nil)
		state, _ := p.consent(t, address)
		if _, err := flow.Callback(ctx, state, cookie, "", "access_denied"); !errors.Is(err, ErrFlowDenied) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("wrong code", func(t *testing.T) {
		flow, p, client := testFlow(t)
		address, cookie, _ := flow.Begin(7, ProviderDrive, client, nil)
		state, _ := p.consent(t, address)
		_, err := flow.Callback(ctx, state, cookie, "not-the-code", "")
		if !errors.Is(err, ErrFlowProvider) {
			t.Fatalf("got %v", err)
		}
		if strings.Contains(err.Error(), "secret detail") {
			t.Fatal("the provider's text is passed on")
		}
	})

	t.Run("client refused", func(t *testing.T) {
		flow, p, client := testFlow(t)
		p.tokenAnswer = func(w http.ResponseWriter) bool {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
			return true
		}
		address, cookie, _ := flow.Begin(7, ProviderDrive, client, nil)
		state, code := p.consent(t, address)
		if _, err := flow.Callback(ctx, state, cookie, code, ""); !errors.Is(err, ErrFlowSetup) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("no refresh token", func(t *testing.T) {
		flow, p, client := testFlow(t)
		p.tokenAnswer = func(w http.ResponseWriter) bool {
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "a", "expires_in": 3600})
			return true
		}
		address, cookie, _ := flow.Begin(7, ProviderDrive, client, nil)
		state, code := p.consent(t, address)
		if _, err := flow.Callback(ctx, state, cookie, code, ""); !errors.Is(err, ErrFlowProvider) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		flow, _, client := testFlow(t)
		if _, _, err := flow.Begin(7, "dropbox", client, nil); err == nil {
			t.Fatal("an unknown provider was accepted")
		}
	})
}

// The access token is sent to the address asked for and not to where it redirects.
func TestAPICallDoesNotFollowRedirects(t *testing.T) {
	var elsewhere []string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		elsewhere = append(elsewhere, r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{}`))
	}))
	defer other.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/x", http.StatusFound)
	}))
	defer api.Close()

	flow := NewFlow(Specs(), api.Client())
	var out map[string]any
	if err := flow.apiCall("token-1")(context.Background(), api.URL+"/me", &out); err == nil {
		t.Fatal("a redirect was followed")
	}
	if len(elsewhere) != 0 {
		t.Fatalf("the token reached another host: %v", elsewhere)
	}
}

func TestSpecsAreFixed(t *testing.T) {
	for kind, s := range Specs() {
		for _, address := range []string{s.AuthURL, s.TokenURL} {
			u, err := url.Parse(address)
			if err != nil || u.Scheme != "https" {
				t.Errorf("%s: %q is not an https address", kind, address)
			}
		}
		if len(s.Scopes) == 0 || s.Identify == nil {
			t.Errorf("%s: incomplete", kind)
		}
	}
}
