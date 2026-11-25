package request

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cloudreve/Cloudreve/v4/application/constants"
	"github.com/cloudreve/Cloudreve/v4/pkg/auth"
	"github.com/cloudreve/Cloudreve/v4/pkg/conf"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"github.com/cloudreve/Cloudreve/v4/pkg/serializer"
	"github.com/samber/lo"
)

// GeneralClient general HTTP client
var GeneralClient Client = NewClientDeprecated()

const (
	CorrelationHeader = constants.CrHeaderPrefix + "Correlation-Id"
	SiteURLHeader     = constants.CrHeaderPrefix + "Site-Url"
	SiteVersionHeader = constants.CrHeaderPrefix + "Version"
	SiteIDHeader      = constants.CrHeaderPrefix + "Site-Id"
	SlaveNodeIDHeader = constants.CrHeaderPrefix + "Node-Id"
	ClientIDHeader    = constants.CrHeaderPrefix + "Client-Id"
	LocalIP           = "localhost"
)

// Response response or error info of a request
type Response struct {
	Err      error
	Response *http.Response
	l        logging.Logger
}

// Client request client
type Client interface {
	// Apply applies the given options to this client.
	Apply(opts ...Option)
	// Request send a HTTP request
	Request(method, target string, body io.Reader, opts ...Option) *Response
}

// HTTPClient implements the Client interface
type HTTPClient struct {
	mu         sync.Mutex
	options    *options
	tpsLimiter TPSLimiter
	l          logging.Logger
	config     conf.ConfigProvider
}

func NewClient(config conf.ConfigProvider, opts ...Option) Client {
	client := &HTTPClient{
		options:    newDefaultOption(),
		tpsLimiter: globalTPSLimiter,
		config:     config,
	}

	for _, o := range opts {
		o.apply(client.options)
	}

	return client
}

// Deprecated
func NewClientDeprecated(opts ...Option) Client {
	client := &HTTPClient{
		options:    newDefaultOption(),
		tpsLimiter: globalTPSLimiter,
	}

	for _, o := range opts {
		o.apply(client.options)
	}

	return client
}

func (c *HTTPClient) Apply(opts ...Option) {
	for _, o := range opts {
		o.apply(c.options)
	}
}

// Request sends an HTTP request
func (c *HTTPClient) Request(method, target string, body io.Reader, opts ...Option) *Response {
	// apply extra options
	c.mu.Lock()
	options := c.options.clone()
	c.mu.Unlock()
	for _, o := range opts {
		o.apply(&options)
	}

	// create the request client
	client := &http.Client{
		Timeout: options.timeout,
		Jar:     options.cookieJar,
	}

	if options.transport != nil {
		client.Transport = options.transport
	}

	// set body to nil when size is 0
	if options.contentLength == 0 {
		body = nil
	}

	// determine the request URL
	if options.endpoint != nil {
		targetPath, err := url.Parse(target)
		if err != nil {
			return &Response{Err: err}
		}

		targetURL := *options.endpoint
		target = targetURL.ResolveReference(targetPath).String()
	}

	// create the request
	var (
		req *http.Request
		err error
	)
	start := time.Now()
	if options.ctx != nil {
		req, err = http.NewRequestWithContext(options.ctx, method, target, body)
	} else {
		req, err = http.NewRequest(method, target, body)
	}
	if err != nil {
		return &Response{Err: err}
	}

	// add request related settings
	if options.header != nil {
		for k, v := range options.header {
			req.Header.Add(k, strings.Join(v, " "))
		}
	}

	req.Header.Set("User-Agent", "Fylvo/"+constants.BackendVersion)

	if options.ctx != nil && options.withCorrelationID {
		req.Header.Add(CorrelationHeader, logging.CorrelationID(options.ctx).String())
	}

	mode := c.config.System().Mode
	if options.masterMeta && mode == conf.MasterMode {
		req.Header.Add(SiteURLHeader, options.siteURL)
		req.Header.Add(SiteIDHeader, options.siteID)
		req.Header.Add(SiteVersionHeader, constants.BackendVersion)
	}

	if options.slaveNodeID > 0 {
		req.Header.Add(SlaveNodeIDHeader, strconv.Itoa(options.slaveNodeID))
	}

	if options.contentLength != -1 {
		req.ContentLength = options.contentLength
	}

	// sign the request
	if options.sign != nil {
		ctx := options.ctx
		if options.ctx == nil {
			ctx = context.Background()
		}
		expire := time.Now().Add(time.Second * time.Duration(options.signTTL))
		switch method {
		case "PUT", "POST", "PATCH":
			auth.SignRequest(ctx, options.sign, req, &expire)
		default:
			if resURL, err := auth.SignURI(ctx, options.sign, req.URL.String(), &expire); err == nil {
				req.URL = resURL
			}
		}
	}

	if options.tps > 0 {
		c.tpsLimiter.Limit(options.ctx, options.tpsLimiterToken, options.tps, options.tpsBurst)
	}

	// send the request
	resp, err := client.Do(req)

	// Logging request
	if options.logger != nil {
		statusCode := 0
		errStr := ""
		if resp != nil {
			statusCode = resp.StatusCode
		}

		if err != nil {
			errStr = err.Error()
		}

		logging.Request(options.logger, false, statusCode, req.Method, LocalIP, req.URL.String(), errStr, start)
	}

	// Apply cookies
	if resp != nil && resp.Cookies() != nil && options.cookieJar != nil {
		options.cookieJar.SetCookies(req.URL, resp.Cookies())
	}

	if err != nil {
		return &Response{Err: err}
	}

	return &Response{Err: nil, Response: resp, l: options.logger}
}

// GetResponse checks the response and gets the response body
func (resp *Response) GetResponse() (string, error) {
	if resp.Err != nil {
		return "", resp.Err
	}
	respBody, err := io.ReadAll(resp.Response.Body)
	_ = resp.Response.Body.Close()

	return string(respBody), err
}

// GetResponseIgnoreErr gets the response body
func (resp *Response) GetResponseIgnoreErr() (string, error) {
	if resp.Response == nil {
		return "", resp.Err
	}

	respBody, _ := io.ReadAll(resp.Response.Body)
	_ = resp.Response.Body.Close()

	return string(respBody), resp.Err
}

// CheckHTTPResponse checks the HTTP status code of the response
func (resp *Response) CheckHTTPResponse(status ...int) *Response {
	if resp.Err != nil {
		return resp
	}

	// check the HTTP status code
	if !lo.Contains(status, resp.Response.StatusCode) {
		resp.Err = fmt.Errorf("Remote returns unexpected status code: %d", resp.Response.StatusCode)
	}
	return resp
}

// DecodeResponse tries to parse as serializer.Response and check the status code
func (resp *Response) DecodeResponse() (*serializer.Response, error) {
	if resp.Err != nil {
		return nil, resp.Err
	}

	respString, err := resp.GetResponse()
	if err != nil {
		return nil, err
	}

	var res serializer.Response
	err = json.Unmarshal([]byte(respString), &res)
	if err != nil {
		if resp.l != nil {
			resp.l.Debug("Failed to parse response: %s", respString)
		}

		return nil, err
	}
	return &res, nil
}

// NopRSCloser incomplete seeker implementation
type NopRSCloser struct {
	body   io.ReadCloser
	status *rscStatus
}

type rscStatus struct {
	// http.ServeContent reads a small chunk to detect the content type,
	// but the response body cannot seek, so when this is true the first read returns fake data
	IgnoreFirst bool

	Size int64
}

// GetRSCloser returns an RSCloser with an empty seeker, for use by http.ServeContent
func (resp *Response) GetRSCloser() (*NopRSCloser, error) {
	if resp.Err != nil {
		return nil, resp.Err
	}

	return &NopRSCloser{
		body: resp.Response.Body,
		status: &rscStatus{
			Size: resp.Response.ContentLength,
		},
	}, resp.Err
}

// SetFirstFakeChunk makes the first read return empty data
// TODO test
func (instance NopRSCloser) SetFirstFakeChunk() {
	instance.status.IgnoreFirst = true
}

// SetContentLength sets the stream size
func (instance NopRSCloser) SetContentLength(size int64) {
	instance.status.Size = size
}

// Read implements the NopRSCloser reader
func (instance NopRSCloser) Read(p []byte) (n int, err error) {
	if instance.status.IgnoreFirst && len(p) == 512 {
		return 0, io.EOF
	}
	return instance.body.Read(p)
}

// Close implements the NopRSCloser closer
func (instance NopRSCloser) Close() error {
	return instance.body.Close()
}

// Seek implements the NopRSCloser seeker, only seeking to start/end so http.ServeContent can determine the body size
func (instance NopRSCloser) Seek(offset int64, whence int) (int64, error) {
	// after the first Seek, clear the ignore option
	if instance.status.IgnoreFirst {
		instance.status.IgnoreFirst = false
	}
	if offset == 0 {
		switch whence {
		case io.SeekStart:
			return 0, nil
		case io.SeekEnd:
			return instance.status.Size, nil
		}
	}
	return 0, errors.New("not implemented")

}
