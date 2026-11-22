package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cloudreve/Cloudreve/v4/pkg/request"
)

const (
	// maxWait is the longest a call waits in total when the provider asks to slow down.
	maxWait = 30 * time.Second
	// maxRedirects is how often a content download may be sent on to another address.
	maxRedirects = 3
	// maxErrorBody is how much of an error answer is read to find the provider's code.
	maxErrorBody = 64 * 1024
	// maxJSONBody bounds every JSON answer. A page of a listing is far below it.
	maxJSONBody = 32 * 1024 * 1024
)

// NewHTTPClient returns the HTTP client for talking to providers: no redirect is
// followed by itself, and connections to private, loopback and link-local addresses
// are refused at the moment of dialing, whatever name led there.
func NewHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	// Behind a proxy the server only ever dials the proxy, which does the rest.
	if os.Getenv("HTTPS_PROXY") == "" && os.Getenv("https_proxy") == "" {
		dialer.Control = func(network, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return request.ErrUnsafeURL
			}
			return request.CheckDialIP(net.ParseIP(host))
		}
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           dialer.DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   15 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Classifier turns an error answer of a provider into an Error. body is the start of
// the answer.
type Classifier func(status int, header http.Header, body []byte) *Error

// Caller sends the calls of one connection to its provider.
type Caller struct {
	// HTTP must not follow redirects by itself (see NewHTTPClient).
	HTTP *http.Client
	Auth Auth
	// Classify maps an error answer; nil maps by status code alone.
	Classify Classifier
	// AllowHTTP lets addresses without TLS through. For tests against a local server only.
	AllowHTTP bool
}

// ByStatus is the Classifier for answers that carry no usable error body.
func ByStatus(status int, _ http.Header, _ []byte) *Error {
	e := &Error{Status: status}
	switch {
	case status == http.StatusNotFound || status == http.StatusGone:
		e.Kind = KindNotFound
	case status == http.StatusConflict:
		e.Kind = KindExists
	case status == http.StatusPreconditionFailed || status == http.StatusRequestedRangeNotSatisfiable:
		e.Kind = KindStale
	case status == http.StatusForbidden:
		e.Kind = KindForbidden
	case status == http.StatusInsufficientStorage:
		e.Kind = KindFull
	case status == http.StatusTooManyRequests || status >= 500:
		e.Kind = KindBusy
	}
	return e
}

func (c *Caller) classify(res *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(res.Body, maxErrorBody))
	classify := c.Classify
	if classify == nil {
		classify = ByStatus
	}
	e := classify(res.StatusCode, res.Header, body)
	if e == nil {
		e = ByStatus(res.StatusCode, res.Header, nil)
	}
	e.Status = res.StatusCode
	return e
}

func (c *Caller) checkAddress(address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(c.AllowHTTP && u.Scheme == "http")) {
		return &Error{Kind: KindUnsafe}
	}
	return nil
}

// retryAfter returns how long the provider asks to wait, 0 when it does not say.
func retryAfter(h http.Header) time.Duration {
	if s, err := strconv.Atoi(strings.TrimSpace(h.Get("Retry-After"))); err == nil && s >= 0 {
		return time.Duration(s) * time.Second
	}
	return 0
}

// Do sends a call with the access token of the connection and returns the answer when
// its status is below 400. The body must be closed by the caller. A 401 is answered by
// one new token and one more try; "slow down" answers are obeyed within maxWait. body
// may be nil; it is sent again on a retry, so it is passed as bytes.
//
// Do is for calls that can be repeated. Ranges of an upload have their own rules.
func (c *Caller) Do(ctx context.Context, method, address string, header http.Header, body []byte) (*http.Response, error) {
	if err := c.checkAddress(address); err != nil {
		return nil, err
	}

	var (
		rejected string
		waited   time.Duration
		renewed  bool
	)
	for attempt := 0; ; attempt++ {
		token, err := c.Auth.Token(ctx, rejected)
		if err != nil {
			return nil, err
		}

		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, address, reader)
		if err != nil {
			return nil, &Error{Kind: KindOther}
		}
		for k, v := range header {
			req.Header[k] = v
		}
		req.Header.Set("Authorization", "Bearer "+token)

		res, err := c.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(err, request.ErrUnsafeURL) {
				return nil, &Error{Kind: KindUnsafe}
			}
			return nil, &Error{Kind: KindBusy}
		}
		if res.StatusCode < 400 {
			return res, nil
		}

		switch {
		case res.StatusCode == http.StatusUnauthorized && !renewed:
			res.Body.Close()
			renewed, rejected = true, token
			continue
		case res.StatusCode == http.StatusTooManyRequests || res.StatusCode == http.StatusServiceUnavailable:
			wait := retryAfter(res.Header)
			if wait == 0 {
				wait = time.Duration(1<<min(attempt, 4)) * time.Second
			}
			if waited+wait <= maxWait {
				res.Body.Close()
				waited += wait
				select {
				case <-time.After(wait):
					continue
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
		}

		err = c.classify(res)
		res.Body.Close()
		return nil, err
	}
}

// JSON sends a call and reads the JSON answer into out. in, when not nil, is sent as JSON.
func (c *Caller) JSON(ctx context.Context, method, address string, in, out any) error {
	var (
		body   []byte
		header = http.Header{"Accept": {"application/json"}}
	)
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return &Error{Kind: KindOther}
		}
		header.Set("Content-Type", "application/json")
	}

	res, err := c.Do(ctx, method, address, header, body)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode >= 300 {
		// A redirect where an answer was expected.
		return &Error{Kind: KindOther, Status: res.StatusCode}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, maxJSONBody))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, maxJSONBody)).Decode(out); err != nil {
		return &Error{Kind: KindBusy, Status: res.StatusCode}
	}
	return nil
}

// Download sends a call for content and follows the provider's redirects to its
// content hosts itself: only to https, without the access token, at most maxRedirects
// times, and with the Range passed on. An answer with the whole content where a range
// was asked is reported as such, never as a partial one.
func (c *Caller) Download(ctx context.Context, address, rangeHeader string) (*Content, error) {
	header := http.Header{}
	if rangeHeader != "" {
		header.Set("Range", rangeHeader)
	}

	res, err := c.Do(ctx, http.MethodGet, address, header, nil)
	if err != nil {
		return nil, err
	}

	for hop := 0; res.StatusCode >= 300; hop++ {
		location := res.Header.Get("Location")
		res.Body.Close()
		if hop >= maxRedirects || location == "" {
			return nil, &Error{Kind: KindUnsafe, Status: res.StatusCode}
		}
		next, err := res.Request.URL.Parse(location)
		if err != nil {
			return nil, &Error{Kind: KindUnsafe}
		}
		if err := c.checkAddress(next.String()); err != nil {
			return nil, err
		}

		// A plain request: the address itself is the permission, the token stays here.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next.String(), nil)
		if err != nil {
			return nil, &Error{Kind: KindUnsafe}
		}
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		res, err = c.HTTP.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(err, request.ErrUnsafeURL) {
				return nil, &Error{Kind: KindUnsafe}
			}
			return nil, &Error{Kind: KindBusy}
		}
		if res.StatusCode >= 400 {
			err := c.classify(res)
			res.Body.Close()
			return nil, err
		}
	}

	content := &Content{Body: res.Body, Length: res.ContentLength}
	if res.StatusCode == http.StatusPartialContent {
		content.Partial = true
		content.ContentRange = res.Header.Get("Content-Range")
	}
	return content, nil
}
