package auth

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
)

var (
	ErrAuthFailed        = serializer.NewError(serializer.CodeInvalidSign, "invalid sign", nil)
	ErrAuthHeaderMissing = serializer.NewError(serializer.CodeNoPermissionErr, "authorization header is missing", nil)
	ErrExpiresMissing    = serializer.NewError(serializer.CodeNoPermissionErr, "expire timestamp is missing", nil)
	ErrExpired           = serializer.NewError(serializer.CodeSignExpired, "signature expired", nil)
)

const (
	TokenHeaderPrefixCr = "Bearer Cr "
)

// General generic authentication interface
// Deprecated
var General Auth

type (
	// Auth authentication
	Auth interface {
		// signs the given body, expires 0 means it never expires
		Sign(body string, expires int64) string
		// checks the given body and signature
		Check(body string, sign string) error
	}
)

// SignRequest signs complex HTTP requests such as PUT\POST, only the URI part,
// the request body and headers starting with `X-Cr-` are signed
func SignRequest(ctx context.Context, instance Auth, r *http.Request, expires *time.Time) *http.Request {
	// handle expiry
	expireTime := int64(0)
	if expires != nil {
		expireTime = expires.Unix()
	}

	// generate signature
	sign := instance.Sign(getSignContent(ctx, r), expireTime)

	// add the signature to the request header
	r.Header["Authorization"] = []string{TokenHeaderPrefixCr + sign}
	return r
}

// SignRequestDeprecated signs complex HTTP requests such as PUT\POST, only the URI part,
// the request body and headers starting with `X-Cr-` are signed
func SignRequestDeprecated(instance Auth, r *http.Request, expires int64) *http.Request {
	// handle expiry
	if expires > 0 {
		expires += time.Now().Unix()
	}

	// generate signature
	sign := instance.Sign(getSignContent(context.Background(), r), expires)

	// add the signature to the request header
	r.Header["Authorization"] = []string{TokenHeaderPrefixCr + sign}
	return r
}

// CheckRequest verifies the signature of complex requests
func CheckRequest(ctx context.Context, instance Auth, r *http.Request) error {
	var (
		sign []string
		ok   bool
	)
	if sign, ok = r.Header["Authorization"]; !ok || len(sign) == 0 {
		return ErrAuthHeaderMissing
	}
	sign[0] = strings.TrimPrefix(sign[0], TokenHeaderPrefixCr)

	return instance.Check(getSignContent(ctx, r), sign[0])
}

func isUploadDataRequest(r *http.Request) bool {
	return strings.Contains(r.URL.Path, constants.APIPrefix+"/slave/upload/") && r.Method != http.MethodPut

}

// getSignContent signs the request path, body and headers starting with `X-`. If the request path is the slave upload API,
// the body is not signed. Returns the string to sign/verify
func getSignContent(ctx context.Context, r *http.Request) (rawSignString string) {
	// read the whole body
	var body = []byte{}
	if !isUploadDataRequest(r) {
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
	}

	// decide which headers to sign
	var signedHeader []string
	for k, _ := range r.Header {
		if strings.HasPrefix(k, constants.CrHeaderPrefix) && k != constants.CrHeaderPrefix+"Filename" {
			signedHeader = append(signedHeader, fmt.Sprintf("%s=%s", k, r.Header.Get(k)))
		}
	}
	sort.Strings(signedHeader)

	// read all headers to sign
	rawSignString = serializer.NewRequestSignString(getUrlSignContent(ctx, r.URL), strings.Join(signedHeader, "&"), string(body))

	return rawSignString
}

// SignURI signs the URI
func SignURI(ctx context.Context, instance Auth, uri string, expires *time.Time) (*url.URL, error) {
	// handle expiry
	expireTime := int64(0)
	if expires != nil {
		expireTime = expires.Unix()
	}

	base, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}

	// generate signature
	sign := instance.Sign(getUrlSignContent(ctx, base), expireTime)

	// add the signature to the URI
	queries := base.Query()
	queries.Set("sign", sign)
	base.RawQuery = queries.Encode()

	return base, nil
}

// SignURIDeprecated signs the URI, only the path is signed, the query is not verified
// Deprecated
func SignURIDeprecated(instance Auth, uri string, expires int64) (*url.URL, error) {
	// handle expiry
	if expires != 0 {
		expires += time.Now().Unix()
	}

	base, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}

	// generate signature
	sign := instance.Sign(base.Path, expires)

	// add the signature to the URI
	queries := base.Query()
	queries.Set("sign", sign)
	base.RawQuery = queries.Encode()

	return base, nil
}

// CheckURI authenticates the URI
func CheckURI(ctx context.Context, instance Auth, url *url.URL) error {
	//get the signed content to verify
	queries := url.Query()
	sign := queries.Get("sign")
	queries.Del("sign")
	url.RawQuery = queries.Encode()

	return instance.Check(getUrlSignContent(ctx, url), sign)
}

// RedactSensitiveValues replaces secrets in the addresses in an error message.
func RedactSensitiveValues(errorMessage string) string {
	return logging.RedactText(errorMessage)
}

func getUrlSignContent(ctx context.Context, url *url.URL) string {
	// host := url.Host
	// if host == "" {
	// 	reqInfo := requestinfo.RequestInfoFromContext(ctx)
	// 	if reqInfo != nil {
	// 		host = reqInfo.Host
	// 	}
	// }
	// host = strings.TrimSuffix(host, "/")
	// // remove port if it exists
	// host = strings.Split(host, ":")[0]
	if url.Path == "" {
		return "/"
	}
	return url.Path
}
