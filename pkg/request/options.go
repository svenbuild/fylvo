package request

import (
	"context"
	"github.com/cloudreve/Cloudreve/v4/pkg/auth"
	"github.com/cloudreve/Cloudreve/v4/pkg/logging"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Option extra settings for sending requests
type Option interface {
	apply(*options)
}

type options struct {
	timeout           time.Duration
	header            http.Header
	sign              auth.Auth
	signTTL           int64
	ctx               context.Context
	contentLength     int64
	masterMeta        bool
	siteID            string
	siteURL           string
	endpoint          *url.URL
	slaveNodeID       int
	tpsLimiterToken   string
	tps               float64
	tpsBurst          int
	logger            logging.Logger
	withCorrelationID bool
	cookieJar         http.CookieJar
	transport         *http.Transport
}

type optionFunc func(*options)

func (f optionFunc) apply(o *options) {
	f(o)
}

func newDefaultOption() *options {
	return &options{
		header:        http.Header{},
		timeout:       0,
		contentLength: -1,
		ctx:           context.Background(),
	}
}

func (o *options) clone() options {
	newOptions := *o
	newOptions.header = o.header.Clone()
	return newOptions
}

// WithTransport sets the request Transport
func WithTransport(transport *http.Transport) Option {
	return optionFunc(func(o *options) {
		o.transport = transport
	})
}

// WithTimeout sets the request timeout
func WithTimeout(t time.Duration) Option {
	return optionFunc(func(o *options) {
		o.timeout = t
	})
}

// WithContext sets the request context
func WithContext(c context.Context) Option {
	return optionFunc(func(o *options) {
		o.ctx = c
	})
}

// WithCredential signs the request
func WithCredential(instance auth.Auth, ttl int64) Option {
	return optionFunc(func(o *options) {
		o.sign = instance
		if ttl > 0 {
			o.signTTL = ttl
		}
	})
}

// WithHeader sets the request header
func WithHeader(header http.Header) Option {
	return optionFunc(func(o *options) {
		for k, v := range header {
			o.header[k] = v
		}
	})
}

// WithoutHeader removes a request header
func WithoutHeader(header []string) Option {
	return optionFunc(func(o *options) {
		for _, v := range header {
			delete(o.header, v)
		}

	})
}

// WithContentLength sets the request size
func WithContentLength(s int64) Option {
	return optionFunc(func(o *options) {
		o.contentLength = s
	})
}

// WithMasterMeta sends master info with the request
func WithMasterMeta(siteID string, siteURL string) Option {
	return optionFunc(func(o *options) {
		o.masterMeta = true
		o.siteID = siteID
		o.siteURL = siteURL
	})
}

// WithSlaveMeta set slave node ID in master's request header
func WithSlaveMeta(s int) Option {
	return optionFunc(func(o *options) {
		o.slaveNodeID = s
	})
}

// Endpoint uses the same request endpoint
func WithEndpoint(endpoint string) Option {
	if !strings.HasSuffix(endpoint, "/") {
		endpoint += "/"
	}

	endpointURL, _ := url.Parse(endpoint)
	return optionFunc(func(o *options) {
		o.endpoint = endpointURL
	})
}

// WithTPSLimit uses the global traffic limit for requests
func WithTPSLimit(token string, tps float64, burst int) Option {
	return optionFunc(func(o *options) {
		o.tpsLimiterToken = token
		o.tps = tps
		if burst < 1 {
			burst = 1
		}
		o.tpsBurst = burst
	})
}

// WithLogger set logger for logging requests
func WithLogger(logger logging.Logger) Option {
	return optionFunc(func(o *options) {
		o.logger = logger
	})
}

// WithCorrelationID set correlation ID for logging requests
func WithCorrelationID() Option {
	return optionFunc(func(o *options) {
		o.withCorrelationID = true
	})
}

// WithCookieJar set cookie jar for request
func WithCookieJar(jar http.CookieJar) Option {
	return optionFunc(func(o *options) {
		o.cookieJar = jar
	})
}
