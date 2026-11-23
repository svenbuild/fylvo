//go:build fylvo_dev

package connected

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/cloudreve/Cloudreve/v4/application/dependency"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive"
	"github.com/cloudreve/Cloudreve/v4/pkg/connected/provider/drive/drivetest"
)

// A storage to develop against, without an account at any provider.
//
// This file is part of the server only when it is built with the tag fylvo_dev, and it
// does something only when the server is started with FYLVO_DEV_STORAGE=1. Then "Google
// Drive" is a simulated provider inside the server: set up, with sample data, and with a
// page of the server in place of Google's sign-in. The data lives in memory and is the
// same after every start. Never for a server that holds real data: the page lets anyone
// who can reach it pick the account that signs in.

// DevEnv is the environment variable that turns the simulated provider on.
const DevEnv = "FYLVO_DEV_STORAGE"

// devPath is where the pages of the simulated provider are served by the server.
const devPath = "/api/v4/fylvo/dev/"

// devAccounts are the accounts one can sign in as. The sign-in page hands out the name
// as the code of the sign-in.
var devAccounts = []string{"alex", "sam"}

// devLabel is what an account shows as, "" for a name that is none.
func devLabel(account string) string {
	if !slices.Contains(devAccounts, account) {
		return ""
	}
	return account + "@example.com"
}

type devProvider struct {
	sim    *drivetest.Server
	tokens *httptest.Server

	mu sync.Mutex
	// epoch rises when all access is ended: tokens of an earlier epoch are refused.
	epoch int
	n     int
}

var dev = sync.OnceValue(func() *devProvider {
	if os.Getenv(DevEnv) != "1" {
		return nil
	}
	d := &devProvider{sim: drivetest.Start()}
	d.tokens = httptest.NewServer(http.HandlerFunc(d.serveTokens))
	d.sim.RejectTokens(func(token string) bool { return !d.valid(token, "dev") })
	devSampleData(d.sim)
	return d
})

// token makes a token of the given kind ("dev" for access, "devr" for refresh).
func (d *devProvider) token(kind, account string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.n++
	return fmt.Sprintf("%s.%s.%d.%d", kind, account, d.epoch, d.n)
}

// account returns the account a token of the given kind is for, "" for a token that is
// none of this provider or was ended.
func (d *devProvider) account(token, kind string) string {
	parts := strings.Split(token, ".")
	if len(parts) != 4 || parts[0] != kind || devLabel(parts[1]) == "" {
		return ""
	}
	epoch, err := strconv.Atoi(parts[2])
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil || epoch < d.epoch {
		return ""
	}
	return parts[1]
}

func (d *devProvider) valid(token, kind string) bool { return d.account(token, kind) != "" }

// serveTokens is the token endpoint and the "who am I" call of the simulated provider.
// Only the server itself talks to it.
func (d *devProvider) serveTokens(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.URL.Path == "/whoami" {
		token, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		account := d.account(token, "dev")
		if account == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"key": "dev-" + account, "label": devLabel(account)})
		return
	}

	_ = r.ParseForm()
	account := ""
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		if code := r.PostForm.Get("code"); devLabel(code) != "" {
			account = code
		}
	case "refresh_token":
		account = d.account(r.PostForm.Get("refresh_token"), "devr")
	}
	if account == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token":  d.token("dev", account),
		"refresh_token": d.token("devr", account),
		"token_type":    "Bearer",
		"expires_in":    3600,
	})
}

// ServeHTTP serves the pages a browser sees: the sign-in page that stands in for the
// provider's, and the switch that ends all access.
func (d *devProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	switch strings.TrimPrefix(r.URL.Path, devPath) {
	case "authorize":
		q := r.URL.Query()
		back, err := url.Parse(q.Get("redirect_uri"))
		if err != nil || back.Host != r.Host {
			http.Error(w, "The sign-in was not started by this server.", http.StatusBadRequest)
			return
		}
		link := func(key, value, text string) string {
			return fmt.Sprintf(`<p><a href="%s">%s</a></p>`, html.EscapeString(back.String()+"?"+url.Values{
				"state": {q.Get("state")}, key: {value},
			}.Encode()), html.EscapeString(text))
		}
		page := "<h1>Simulated provider</h1><p>This page stands in for the sign-in page of Google. Pick what happens:</p>"
		for _, account := range devAccounts {
			page += link("code", account, "Allow as "+devLabel(account))
		}
		page += link("error", "access_denied", "Deny")
		devPage(w, page)
	case "expire":
		if r.Method == http.MethodPost {
			d.mu.Lock()
			d.epoch++
			d.mu.Unlock()
			devPage(w, "<h1>Simulated provider</h1><p>All access was ended. Every connected sample storage now has to be connected again.</p>")
			return
		}
		devPage(w, `<h1>Simulated provider</h1><p>End the access of every connected sample storage, as if the account had taken it back at the provider.</p><form method="post"><button>End all access</button></form>`)
	default:
		http.NotFound(w, r)
	}
}

func devPage(w http.ResponseWriter, body string) {
	_, _ = fmt.Fprintf(w, `<!doctype html><meta name="viewport" content="width=device-width, initial-scale=1"><title>Simulated provider</title><body style="font-family: sans-serif; margin: 2em">%s</body>`, body)
}

// DevPath is where the server serves the pages of the simulated provider.
const DevPath = devPath

// DevHandler returns what serves the pages of the simulated provider below DevPath, or
// nil when it is off.
func DevHandler() http.Handler {
	if d := dev(); d != nil {
		return d
	}
	return nil
}

// devOptions makes "Google Drive" the simulated provider.
func devOptions(o *ManagerOptions) {
	d := dev()
	if d == nil {
		return
	}
	o.Specs = Specs()
	spec := o.Specs[ProviderDrive]
	// Served by this server, under the address its browser side has.
	spec.AuthURL = devPath + "authorize"
	spec.TokenURL = d.tokens.URL + "/token"
	spec.Revoke = nil
	spec.Identify = func(ctx context.Context, call APICall) (*Identity, error) {
		var who struct{ Key, Label string }
		if err := call(ctx, d.tokens.URL+"/whoami", &who); err != nil {
			return nil, err
		}
		return &Identity{Key: who.Key, Label: who.Label}, nil
	}
	spec.NewProvider = func(_ *http.Client, auth provider.Auth, _ map[string]string) provider.Provider {
		return drive.New(drive.Options{HTTP: d.sim.Client(), Auth: auth, BaseURL: d.sim.URL()})
	}
	// The simulated provider listens on this machine, which the client for real
	// providers refuses to talk to. Only requests to it get past that.
	hosts := map[string]bool{}
	for _, address := range []string{d.tokens.URL, d.sim.URL()} {
		if u, err := url.Parse(address); err == nil {
			hosts[u.Host] = true
		}
	}
	checked := provider.NewHTTPClient()
	o.HTTP = &http.Client{
		Transport:     devTransport{hosts: hosts, local: http.DefaultTransport, other: checked.Transport},
		CheckRedirect: checked.CheckRedirect,
	}
}

// devTransport sends requests to the simulated provider directly and all others the
// way an ordinary build sends them.
type devTransport struct {
	hosts        map[string]bool
	local, other http.RoundTripper
}

func (t devTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.hosts[req.URL.Host] {
		return t.local.RoundTrip(req)
	}
	return t.other.RoundTrip(req)
}

// devReady enters the client of the simulated provider, so it shows as set up.
func devReady(dep dependency.Dep, m *Manager) {
	if dev() == nil {
		return
	}
	dep.Logger().Warning("Connected storages: %s=1, \"Google Drive\" is a simulated provider with sample data. "+
		"Not for a server with real data.", DevEnv)
	ctx := context.Background()
	if setup, err := m.Setup(ctx); err == nil && setup[ProviderDrive].ClientID == "" {
		secret := "simulated"
		_ = m.SetProvider(ctx, ProviderDrive, "simulated", &secret)
	}
}

// devSampleData fills the simulated storage with one of everything a client has to handle.
func devSampleData(s *drivetest.Server) {
	picture := func(c color.Color) []byte {
		img := image.NewRGBA(image.Rect(0, 0, 320, 200))
		draw.Draw(img, img.Bounds(), image.NewUniform(c), image.Point{}, draw.Src)
		var buf bytes.Buffer
		_ = png.Encode(&buf, img)
		return buf.Bytes()
	}

	docs := s.AddFolder("", "Documents")
	s.AddFile(docs, "Notes.txt", []byte("Notes of Alex.\n"))
	s.AddFile(docs, "Shopping list.md", []byte("# Shopping list\n\n- Bread\n- Apples\n"))
	s.AddNative(docs, "Budget", "gsheet")
	s.AddNative(docs, "Letter", "gdoc")
	s.AddNative(docs, "Talk", "gslides")
	deep := s.AddFolder(s.AddFolder(s.AddFolder(docs, "Projects"), "2026"), "Garden")
	s.AddFile(deep, "Plan.txt", []byte("Three beds, one path.\n"))

	pictures := s.AddFolder("", "Pictures")
	s.AddFile(pictures, "Beach.png", picture(color.RGBA{R: 230, G: 200, B: 140, A: 255}))
	s.AddFile(pictures, "Mountain.png", picture(color.RGBA{R: 90, G: 120, B: 150, A: 255}))
	s.AddFile(pictures, "Zürich im Winter.png", picture(color.RGBA{R: 235, G: 240, B: 245, A: 255}))

	many := s.AddFolder("", "Many files")
	for i := 1; i <= 250; i++ {
		s.AddFile(many, fmt.Sprintf("File %03d.txt", i), []byte(fmt.Sprintf("File number %d\n", i)))
	}

	s.AddFolder("", "Empty folder")
	readme := s.AddFile("", "Read me.txt", []byte("Sample data of the simulated provider.\n"))
	s.AddFile("", "Large file.bin", make([]byte, 2*1024*1024))
	// Two entries with one name, as Google Drive allows: shown as one row without actions.
	s.AddFile("", "Twice.txt", []byte("one"))
	s.AddFile("", "Twice.txt", []byte("two"))
	// A name that cannot be part of a path: left out and counted.
	s.AddFile("", "Either/or.txt", []byte("x"))
	s.AddShortcut("", "Shortcut to Documents", docs)
	s.AddShortcut("", "Shortcut to Read me", readme)
	s.SetQuota(6*1024*1024*1024, 15*1024*1024*1024)
}
