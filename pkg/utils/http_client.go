package utils

import (
	"net"
	"net/http"
	"net/url"
	"time"
)

// defaults. can be moved to configs later
const (
	// HTTP client timeouts
	defaultClientTimeout         = 2 * time.Second // absolute deadline for the whole request
	defaultResponseHeaderTimeout = 1 * time.Second // time to first byte of headers
	defaultIdleConnTimeout       = 90 * time.Second
	defaultTLSHandshakeTimeout   = 5 * time.Second
	defaultExpectContinueTimeout = 1 * time.Second

	defaultMaxConnsPerHost     = 128
	defaultMaxIdleConns        = 256
	defaultMaxIdleConnsPerHost = 128

	defaultDialerTimeout   = 500 * time.Millisecond
	defaultDialerKeepAlive = 30 * time.Second
)

// ClientConfig captures tunable for the HTTP client/transport.
// All fields are optional. zero-values will be replaced by defaults.
type ClientConfig struct {
	// Client-level deadline (caps total request time).
	ClientTimeout time.Duration

	// Transport timeouts.
	ResponseHeaderTimeout time.Duration
	IdleConnTimeout       time.Duration
	TLSHandshakeTimeout   time.Duration
	ExpectContinueTimeout time.Duration

	// Transport pool sizing.
	MaxConnsPerHost     int
	MaxIdleConns        int
	MaxIdleConnsPerHost int

	// Dialer options.
	DialerTimeout   time.Duration
	DialerKeepAlive time.Duration

	// Optional overrides.
	ForceAttemptHTTP2 bool                                  // default true
	Proxy             func(*http.Request) (*url.URL, error) // default http.ProxyFromEnvironment
}

// ClientOption ----- Functional options pattern -----
type ClientOption func(*ClientConfig)

func WithClientTimeout(d time.Duration) ClientOption {
	return func(c *ClientConfig) { c.ClientTimeout = d }
}
func WithResponseHeaderTimeout(d time.Duration) ClientOption {
	return func(c *ClientConfig) { c.ResponseHeaderTimeout = d }
}
func WithIdleConnTimeout(d time.Duration) ClientOption {
	return func(c *ClientConfig) { c.IdleConnTimeout = d }
}
func WithTLSHandshakeTimeout(d time.Duration) ClientOption {
	return func(c *ClientConfig) { c.TLSHandshakeTimeout = d }
}
func WithExpectContinueTimeout(d time.Duration) ClientOption {
	return func(c *ClientConfig) { c.ExpectContinueTimeout = d }
}
func WithMaxConnsPerHost(n int) ClientOption { return func(c *ClientConfig) { c.MaxConnsPerHost = n } }
func WithMaxIdleConns(n int) ClientOption    { return func(c *ClientConfig) { c.MaxIdleConns = n } }
func WithMaxIdleConnsPerHost(n int) ClientOption {
	return func(c *ClientConfig) { c.MaxIdleConnsPerHost = n }
}
func WithDialerTimeout(d time.Duration) ClientOption {
	return func(c *ClientConfig) { c.DialerTimeout = d }
}
func WithDialerKeepAlive(d time.Duration) ClientOption {
	return func(c *ClientConfig) { c.DialerKeepAlive = d }
}
func WithProxy(p func(*http.Request) (*url.URL, error)) ClientOption {
	return func(c *ClientConfig) { c.Proxy = p }
}
func WithForceAttemptHTTP2(b bool) ClientOption {
	return func(c *ClientConfig) { c.ForceAttemptHTTP2 = b }
}

// DefaultClientConfig returns a copy of the library defaults.
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		ClientTimeout:         defaultClientTimeout,
		ResponseHeaderTimeout: defaultResponseHeaderTimeout,
		IdleConnTimeout:       defaultIdleConnTimeout,
		TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
		ExpectContinueTimeout: defaultExpectContinueTimeout,
		MaxConnsPerHost:       defaultMaxConnsPerHost,
		MaxIdleConns:          defaultMaxIdleConns,
		MaxIdleConnsPerHost:   defaultMaxIdleConnsPerHost,
		DialerTimeout:         defaultDialerTimeout,
		DialerKeepAlive:       defaultDialerKeepAlive,
		ForceAttemptHTTP2:     true,
		Proxy:                 http.ProxyFromEnvironment,
	}
}

// NewHTTPClient builds an *http.Client with safe defaults overridden by opts.
// All zero/empty values are filled with defaults to avoid accidental infinite hangs.
func NewHTTPClient(opts ...ClientOption) *http.Client {
	cfg := DefaultClientConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	sanitizeClientConfig(&cfg)

	tr := &http.Transport{
		Proxy: cfg.Proxy,
		DialContext: (&net.Dialer{
			Timeout:   cfg.DialerTimeout,
			KeepAlive: cfg.DialerKeepAlive,
		}).DialContext,
		MaxConnsPerHost:       cfg.MaxConnsPerHost,
		MaxIdleConns:          cfg.MaxIdleConns,
		MaxIdleConnsPerHost:   cfg.MaxIdleConnsPerHost,
		IdleConnTimeout:       cfg.IdleConnTimeout,
		TLSHandshakeTimeout:   cfg.TLSHandshakeTimeout,
		ExpectContinueTimeout: cfg.ExpectContinueTimeout,

		ResponseHeaderTimeout: cfg.ResponseHeaderTimeout,
		ForceAttemptHTTP2:     cfg.ForceAttemptHTTP2,
	}

	return &http.Client{
		Transport: tr,
		Timeout:   cfg.ClientTimeout,
	}
}

// sanitizeClientConfig sanitizes the provided ClientConfig.
// Defensive bounds to keep the client healthy if callers pass odd values.
func sanitizeClientConfig(c *ClientConfig) {
	// Timeouts
	if c.ClientTimeout <= 0 {
		c.ClientTimeout = defaultClientTimeout
	}
	if c.ResponseHeaderTimeout <= 0 {
		c.ResponseHeaderTimeout = defaultResponseHeaderTimeout
	}
	if c.IdleConnTimeout <= 0 {
		c.IdleConnTimeout = defaultIdleConnTimeout
	}
	if c.TLSHandshakeTimeout <= 0 {
		c.TLSHandshakeTimeout = defaultTLSHandshakeTimeout
	}
	if c.ExpectContinueTimeout <= 0 {
		c.ExpectContinueTimeout = defaultExpectContinueTimeout
	}
	if c.DialerTimeout <= 0 {
		c.DialerTimeout = defaultDialerTimeout
	}
	if c.DialerKeepAlive <= 0 {
		c.DialerKeepAlive = defaultDialerKeepAlive
	}

	// Pool sizes
	if c.MaxConnsPerHost <= 0 {
		c.MaxConnsPerHost = defaultMaxConnsPerHost
	}
	if c.MaxIdleConns <= 0 {
		c.MaxIdleConns = defaultMaxIdleConns
	}
	if c.MaxIdleConnsPerHost <= 0 {
		c.MaxIdleConnsPerHost = defaultMaxIdleConnsPerHost
	}

	if c.Proxy == nil {
		c.Proxy = http.ProxyFromEnvironment
	}
}
