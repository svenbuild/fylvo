package provider

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// testAuth hands out tokens "token-1", "token-2", ...: a new one each time the current
// one is reported as rejected.
type testAuth struct {
	mu       sync.Mutex
	n        int
	renewals int
	fail     error
	// refused are the tokens the provider refused right after handing them out.
	refused []string
}

func (a *testAuth) Refused(_ context.Context, token string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.refused = append(a.refused, token)
}

func (a *testAuth) Token(ctx context.Context, rejected string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.fail != nil {
		return "", a.fail
	}
	if a.n == 0 {
		a.n = 1
	}
	current := "token-" + string(rune('0'+a.n))
	if rejected == current {
		a.n++
		a.renewals++
		current = "token-" + string(rune('0'+a.n))
	}
	return current, nil
}

// plainClient talks to the local test servers: like NewHTTPClient it follows no redirect,
// but it may dial the loopback address.
func plainClient() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func TestDoRenewsTokenOnce(t *testing.T) {
	var seen []string
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Header.Get("Authorization"))
		if r.Header.Get("Authorization") == "Bearer token-1" || r.URL.Path == "/always" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer api.Close()

	auth := &testAuth{}
	c := &Caller{HTTP: plainClient(), Auth: auth, AllowHTTP: true}
	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.JSON(context.Background(), http.MethodGet, api.URL+"/x", nil, &out); err != nil || !out.OK {
		t.Fatalf("call: %v %+v", err, out)
	}
	if len(seen) != 2 || seen[1] != "Bearer token-2" || auth.renewals != 1 {
		t.Fatalf("seen %v renewals %d", seen, auth.renewals)
	}
	if len(auth.refused) != 0 {
		t.Fatalf("one refusal is an old token, not a refused connection: %v", auth.refused)
	}

	// A provider that keeps answering 401 is asked twice, not for ever.
	seen = nil
	err := c.JSON(context.Background(), http.MethodGet, api.URL+"/always", nil, nil)
	if KindOf(err) != KindReconnect || len(seen) != 2 {
		t.Fatalf("err %v, %d calls", err, len(seen))
	}
	// The connection is told that its brand-new token was refused, once.
	if len(auth.refused) != 1 || "Bearer "+auth.refused[0] != seen[1] {
		t.Fatalf("refused %v, sent %v", auth.refused, seen)
	}
}

func TestDoObeysRetryAfterAndClassifies(t *testing.T) {
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/slow":
			if calls == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			_, _ = w.Write([]byte(`{}`))
		case "/busy":
			// A provider that reports "slow down" with a 403 and says so only in its body.
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`busy`))
		case "/gone":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"itemNotFound","message":"secret detail"}}`))
		case "/full":
			w.WriteHeader(http.StatusInsufficientStorage)
		}
	}))
	defer api.Close()

	c := &Caller{HTTP: plainClient(), Auth: &testAuth{}, AllowHTTP: true}
	if err := c.JSON(context.Background(), http.MethodGet, api.URL+"/slow", nil, nil); err != nil || calls != 2 {
		t.Fatalf("slow: %v after %d calls", err, calls)
	}

	// A "slow down" that only the classifier recognises is tried again, a bounded number
	// of times, and then reported.
	base := RetryBase
	RetryBase = time.Millisecond
	defer func() { RetryBase = base }()
	busy := &Caller{HTTP: plainClient(), Auth: &testAuth{}, AllowHTTP: true,
		Classify: func(status int, _ http.Header, body []byte) *Error {
			if string(body) == "busy" {
				return &Error{Kind: KindBusy, Code: "rateLimitExceeded"}
			}
			return nil
		}}
	calls = 0
	if err := busy.JSON(context.Background(), http.MethodGet, api.URL+"/busy", nil, nil); KindOf(err) != KindBusy || calls != MaxTries+1 {
		t.Fatalf("busy: %v after %d calls", err, calls)
	}

	err := c.JSON(context.Background(), http.MethodGet, api.URL+"/gone", nil, nil)
	if KindOf(err) != KindNotFound {
		t.Fatalf("gone: %v", err)
	}
	if got := err.Error(); len(got) == 0 || containsAny(got, "secret detail") {
		t.Fatalf("the provider's text is in the error: %q", got)
	}
	if err := c.JSON(context.Background(), http.MethodGet, api.URL+"/full", nil, nil); KindOf(err) != KindFull {
		t.Fatalf("full: %v", err)
	}

	// The provider's own code can be picked up by a classifier.
	c.Classify = func(status int, _ http.Header, body []byte) *Error {
		return &Error{Kind: KindNotFound, Code: "itemNotFound"}
	}
	err = c.JSON(context.Background(), http.MethodGet, api.URL+"/gone", nil, nil)
	var pe *Error
	if !asError(err, &pe) || pe.Code != "itemNotFound" || pe.Status != http.StatusNotFound {
		t.Fatalf("classified: %+v", pe)
	}
}

func containsAny(s string, parts ...string) bool {
	for _, p := range parts {
		for i := 0; i+len(p) <= len(s); i++ {
			if s[i:i+len(p)] == p {
				return true
			}
		}
	}
	return false
}

func asError(err error, target **Error) bool {
	e, ok := err.(*Error)
	if ok {
		*target = e
	}
	return ok
}

func TestDownloadFollowsRedirectWithoutToken(t *testing.T) {
	var contentAuth, contentRange []string
	content := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		contentAuth = append(contentAuth, r.Header.Get("Authorization"))
		contentRange = append(contentRange, r.Header.Get("Range"))
		switch r.URL.Path {
		case "/whole":
			// Ignores the range, as some content hosts do.
			_, _ = w.Write([]byte("0123456789"))
		default:
			w.Header().Set("Content-Range", "bytes 2-4/10")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("234"))
		}
	}))
	defer content.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A redirect that leads back here arrives without the token, like any other hop.
		if r.Header.Get("Authorization") == "" && r.URL.Path != "/loop" {
			t.Errorf("the API was called without a token")
		}
		switch r.URL.Path {
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/insecure":
			http.Redirect(w, r, "ftp://example.com/file", http.StatusFound)
		case "/whole":
			http.Redirect(w, r, content.URL+"/whole", http.StatusFound)
		default:
			http.Redirect(w, r, content.URL+"/part?key=one-time", http.StatusFound)
		}
	}))
	defer api.Close()

	c := &Caller{HTTP: plainClient(), Auth: &testAuth{}, AllowHTTP: true}
	ctx := context.Background()

	got, err := c.Download(ctx, api.URL+"/file", "bytes=2-4")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(got.Body)
	got.Body.Close()
	if string(body) != "234" || !got.Partial || got.ContentRange != "bytes 2-4/10" {
		t.Fatalf("partial: %q %+v", body, got)
	}
	if len(contentAuth) != 1 || contentAuth[0] != "" {
		t.Fatalf("the token travelled to the content host: %v", contentAuth)
	}
	if contentRange[0] != "bytes=2-4" {
		t.Fatalf("the range was not passed on: %v", contentRange)
	}

	// The whole file where a range was asked is not presented as the range.
	got, err = c.Download(ctx, api.URL+"/whole", "bytes=2-4")
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(got.Body)
	got.Body.Close()
	if got.Partial || string(body) != "0123456789" {
		t.Fatalf("whole: %q %+v", body, got)
	}

	if _, err := c.Download(ctx, api.URL+"/loop", ""); KindOf(err) != KindUnsafe {
		t.Fatalf("endless redirects: %v", err)
	}
	if _, err := c.Download(ctx, api.URL+"/insecure", ""); KindOf(err) != KindUnsafe {
		t.Fatalf("redirect to another scheme: %v", err)
	}

	// Without the test switch only https is spoken at all.
	strict := &Caller{HTTP: plainClient(), Auth: &testAuth{}}
	if _, err := strict.Download(ctx, api.URL+"/file", ""); KindOf(err) != KindUnsafe {
		t.Fatalf("plain http: %v", err)
	}
}

// The client for real use refuses to connect to the machine itself and to private
// networks, also when a public-looking name or a redirect leads there.
func TestClientRefusesInternalAddresses(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("https_proxy", "")
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("an internal address was reached: %s", r.URL)
	}))
	defer internal.Close()

	c := &Caller{HTTP: NewHTTPClient(), Auth: &testAuth{}, AllowHTTP: true}
	for _, address := range []string{internal.URL + "/x", "http://localhost:1/x", "http://192.168.1.10/x", "http://169.254.169.254/latest", "http://[::1]:1/x"} {
		if _, err := c.Do(context.Background(), http.MethodGet, address, nil, nil); KindOf(err) != KindUnsafe {
			t.Errorf("%s: %v, want refused", address, err)
		}
	}
}

// With a proxy in use the check stays on for everything that is dialed directly, and a
// request that goes through the proxy is refused when its address names an internal one.
func TestClientBehindProxy(t *testing.T) {
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("an internal address was reached: %s", r.URL)
	}))
	defer internal.Close()
	var proxied []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxied = append(proxied, r.URL.String())
		_, _ = w.Write([]byte("through the proxy"))
	}))
	defer proxy.Close()
	proxyURL, _ := url.Parse(proxy.URL)

	// The proxy is for one name only, as with NO_PROXY for everything else.
	client := newHTTPClient(func(r *http.Request) (*url.URL, error) {
		if strings.HasSuffix(r.URL.Hostname(), ".example.com") || r.URL.Hostname() == "192.168.1.10" || r.URL.Hostname() == "localhost." {
			return proxyURL, nil
		}
		return nil, nil
	}, func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "files.example.com":
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.7")}}, nil
		case "inside.example.com":
			return []net.IPAddr{{IP: net.ParseIP("203.0.113.7")}, {IP: net.ParseIP("10.0.0.5")}}, nil
		}
		return nil, errors.New("no such host")
	})
	c := &Caller{HTTP: client, Auth: &testAuth{}, AllowHTTP: true}
	ctx := context.Background()

	res, err := c.Do(ctx, http.MethodGet, "http://files.example.com/a", nil, nil)
	if err != nil {
		t.Fatalf("through the proxy, which itself is on the loopback address: %v", err)
	}
	res.Body.Close()
	if len(proxied) != 1 || proxied[0] != "http://files.example.com/a" {
		t.Fatalf("proxied %v", proxied)
	}

	if _, err := c.Do(ctx, http.MethodGet, internal.URL+"/x", nil, nil); KindOf(err) != KindUnsafe {
		t.Errorf("dialed directly beside the proxy: %v, want refused", err)
	}
	if _, err := c.Do(ctx, http.MethodGet, "http://192.168.1.10/x", nil, nil); KindOf(err) != KindUnsafe {
		t.Errorf("an internal address through the proxy: %v, want refused", err)
	}
	// A name that leads inside is refused too, although the proxy would look it up.
	if _, err := c.Do(ctx, http.MethodGet, "http://inside.example.com/x", nil, nil); KindOf(err) != KindUnsafe {
		t.Errorf("a name that leads to an internal address: %v, want refused", err)
	}
	if len(proxied) != 1 {
		t.Errorf("proxied %v", proxied)
	}
	// The machine itself, spelled with the dot that ends a full name.
	if _, err := c.Do(ctx, http.MethodGet, "http://localhost.:1/x", nil, nil); KindOf(err) != KindUnsafe {
		t.Errorf("localhost with a trailing dot through the proxy: %v, want refused", err)
	}
	// A name only the proxy knows is the proxy's to judge.
	res, err = c.Do(ctx, http.MethodGet, "http://unknown.example.com/b", nil, nil)
	if err != nil {
		t.Fatalf("a name this machine cannot look up: %v", err)
	}
	res.Body.Close()
}

func TestAuthErrorIsPassedOn(t *testing.T) {
	c := &Caller{HTTP: plainClient(), Auth: &testAuth{fail: &Error{Kind: KindReconnect}}, AllowHTTP: true}
	if _, err := c.Do(context.Background(), http.MethodGet, "http://example.com/x", nil, nil); KindOf(err) != KindReconnect {
		t.Fatalf("got %v", err)
	}
}

// A call about an item in another drive that is refused twice says something about
// that item: the connection is not told, and the answer is not "sign in again".
func TestItemOnlyRefusal(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer api.Close()
	auth := &testAuth{}
	c := &Caller{HTTP: plainClient(), Auth: auth, AllowHTTP: true, ItemOnly: true}
	_, err := c.Do(context.Background(), http.MethodGet, api.URL+"/x", nil, nil)
	if KindOf(err) != KindForbidden || len(auth.refused) != 0 {
		t.Fatalf("err %v, refused %v", err, auth.refused)
	}
}

// A download is not held back to wait for the provider: what was checked before it
// would be old by then.
func TestDownloadDoesNotWait(t *testing.T) {
	calls := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer api.Close()
	c := &Caller{HTTP: plainClient(), Auth: &testAuth{}, AllowHTTP: true}
	if _, err := c.Download(context.Background(), api.URL+"/file", ""); KindOf(err) != KindBusy || calls != 1 {
		t.Fatalf("err %v after %d calls", err, calls)
	}
}
