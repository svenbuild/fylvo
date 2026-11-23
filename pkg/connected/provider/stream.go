package provider

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/cloudreve/Cloudreve/v4/pkg/request"
)

// Stream sends one request whose body is read once, for a range of an upload. There
// is exactly one attempt: nothing is sent again, no redirect is followed. With
// withToken the access token of the connection is sent along; an address the provider
// handed out for an upload gets it only where the provider asks for it.
//
// The answer is returned whatever its status, and the caller closes its body. An error
// means no answer arrived, so it is open what the provider has; the error never names
// the address, which works like a key while it is valid.
func (c *Caller) Stream(ctx context.Context, method, address string, header http.Header, body io.Reader, length int64, withToken bool) (*http.Response, error) {
	if err := c.checkAddress(address); err != nil {
		return nil, err
	}
	if length == 0 {
		body = http.NoBody
	}
	req, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return nil, &Error{Kind: KindOther}
	}
	req.ContentLength = length
	for k, v := range header {
		req.Header[k] = v
	}
	token := ""
	if withToken {
		if token, err = c.Auth.Token(ctx, ""); err != nil {
			// No token, no request: the provider was not asked anything.
			return nil, notSent{err}
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, request.ErrUnsafeURL) {
			return nil, &Error{Kind: KindUnsafe}
		}
		return nil, &Error{Kind: KindOther}
	}
	if withToken && res.StatusCode == http.StatusUnauthorized {
		// This attempt is over. The next call gets a new token; when there is none to
		// get, that is the answer: the provider refused, and nothing was stored.
		res.Body.Close()
		if _, err := c.Auth.Token(ctx, token); err != nil {
			return nil, err
		}
		// A plain "no" for a token that ran out: nothing was taken, and there is a new one.
		return nil, &Error{Kind: KindRetry, Status: http.StatusUnauthorized}
	}
	return res, nil
}

// Failure turns an error answer (status 400 and above) into an Error, the way Do does.
// It reads the start of the body; the caller still closes it.
func (c *Caller) Failure(res *http.Response) error {
	return c.classify(res)
}
