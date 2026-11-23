package drive

import "net/http"

// resourceKeyContext marks a call that needs resource keys, as "<id>/<key>", several
// with commas between them.
type resourceKeyContext struct{}

// keyTransport adds the resource key of a call to the requests that go to the Drive
// API. provider.Caller.Download takes no extra headers, and the key must not travel
// on to a content host a redirect leads to, so it is added here, by host.
type keyTransport struct {
	base http.RoundTripper
	host string
}

func (t *keyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if key, _ := req.Context().Value(resourceKeyContext{}).(string); key != "" && req.URL.Host == t.host {
		req = req.Clone(req.Context())
		req.Header.Set(resourceKeyHeader, key)
	}
	return t.base.RoundTrip(req)
}

// withResourceKeys returns a copy of c that sends resource keys to the API at host.
func withResourceKeys(c *http.Client, host string) *http.Client {
	if c == nil {
		return nil
	}
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	copied := *c
	copied.Transport = &keyTransport{base: base, host: host}
	return &copied
}
