package transport

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"ant/server/internal/adapters"
)

var (
	ErrInvalidEndpoint  = errors.New("transport: endpoint must be an absolute HTTPS URL")
	ErrInvalidRequest   = errors.New("transport: invalid one-shot request")
	ErrResponseTooLarge = errors.New("transport: response exceeds configured limit")
)

// HTTPClient is the runtime's bounded HTTP/1.1 sender. It intentionally does
// not reuse connections: a failed reused connection can make net/http replay a
// request even when application code called Do only once.
type HTTPClient struct {
	client        *http.Client
	allowHTTP     bool
	localTestPort string
	providerURLs  map[string]struct{}
}

func NewHTTPClient() *HTTPClient {
	return newHTTPClient(false)
}

// NewHTTPClientWithAllowedEndpoints permits HTTPS requests only to the exact
// URLs supplied by the composition root. It is for explicit provider reads
// that do not use the operation dispatch transport.
func NewHTTPClientWithAllowedEndpoints(endpoints ...string) (*HTTPClient, error) {
	if len(endpoints) == 0 {
		return nil, ErrInvalidRequest
	}
	allowed := make(map[string]struct{}, len(endpoints))
	for _, endpoint := range endpoints {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path == "" {
			return nil, ErrInvalidEndpoint
		}
		if _, duplicate := allowed[endpoint]; duplicate {
			return nil, ErrInvalidRequest
		}
		allowed[endpoint] = struct{}{}
	}
	client := newHTTPClient(false)
	client.providerURLs = allowed
	return client, nil
}

// NewLocalTestHTTPClient permits only the exact loopback listener endpoint
// used by the in-process simulated provider. Production composition must use
// NewHTTPClient or a trusted CA.
func NewLocalTestHTTPClient(port string) (*HTTPClient, error) {
	return newLocalTestHTTPClient(port, nil)
}

// NewLocalTestHTTPClientWithAllowedEndpoints combines the exact loopback
// fixture target with a finite set of exact HTTPS provider endpoints. It is
// used by mixed local/test composition roots so fixture traffic cannot use
// public networking and provider traffic cannot target arbitrary hosts.
func NewLocalTestHTTPClientWithAllowedEndpoints(port string, endpoints ...string) (*HTTPClient, error) {
	if len(endpoints) == 0 {
		return nil, ErrInvalidRequest
	}
	allowed := make(map[string]struct{}, len(endpoints))
	for _, endpoint := range endpoints {
		u, err := url.Parse(endpoint)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path == "" {
			return nil, ErrInvalidEndpoint
		}
		if _, duplicate := allowed[endpoint]; duplicate {
			return nil, ErrInvalidRequest
		}
		allowed[endpoint] = struct{}{}
	}
	return newLocalTestHTTPClient(port, allowed)
}

func newLocalTestHTTPClient(port string, providerURLs map[string]struct{}) (*HTTPClient, error) {
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return nil, ErrInvalidRequest
	}
	client := newHTTPClient(true)
	client.localTestPort = strconv.Itoa(n)
	client.providerURLs = providerURLs
	return client, nil
}

// NewHTTPClientWithRootCAs is the secure custom-CA variant for private
// provider endpoints and local TLS integration tests. It keeps HTTPS
// verification enabled and copies the caller's pool.
func NewHTTPClientWithRootCAs(roots *x509.CertPool) (*HTTPClient, error) {
	if roots == nil {
		return nil, ErrInvalidRequest
	}
	return newHTTPClientWithRoots(roots.Clone()), nil
}

// newHTTPClient permits plain HTTP only for loopback fixture tests.
func newHTTPClient(allowHTTP bool) *HTTPClient {
	return buildHTTPClient(allowHTTP, nil)
}

func newHTTPClientWithRoots(roots *x509.CertPool) *HTTPClient {
	return buildHTTPClient(false, roots)
}

func buildHTTPClient(allowHTTP bool, roots *x509.CertPool) *HTTPClient {
	tr := &http.Transport{
		Proxy:                 nil,
		ForceAttemptHTTP2:     false,
		DisableKeepAlives:     true,
		MaxConnsPerHost:       1,
		MaxIdleConns:          0,
		MaxIdleConnsPerHost:   0,
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		ResponseHeaderTimeout: 30 * time.Second,
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
	}
	return &HTTPClient{
		allowHTTP: allowHTTP,
		client: &http.Client{
			Transport: tr,
			Timeout:   2 * time.Minute,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// DoOnce sends an adapter-encoded request once and returns a body that cannot
// yield more than MaxResponseBytes. The adapter owns response decoding; this
// layer owns protocol, redirect, replay, and size limits.
func (c *HTTPClient) DoOnce(ctx context.Context, encoded adapters.EncodedRequest) (*http.Response, error) {
	u, err := c.validate(encoded)
	if err != nil {
		return nil, err
	}
	var body io.Reader
	if encoded.Body != nil {
		body = bytes.NewReader(encoded.Body)
	}
	req, err := http.NewRequestWithContext(ctx, encoded.Method, u.String(), body)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	// Do not expose a replayable body, including for empty requests.
	req.GetBody = nil
	for key, value := range encoded.Headers {
		req.Header.Set(key, value)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	resp.Body = &boundedResponseBody{body: resp.Body, limit: encoded.MaxResponseBytes}
	return resp, nil
}

// ValidateRequest checks every local condition that could reject a request
// before the durable permit is consumed. Dispatch calls it again as defense in
// depth, but Prepare must run it before the attempt enters execution.
func (c *HTTPClient) ValidateRequest(encoded adapters.EncodedRequest) error {
	_, err := c.validate(encoded)
	return err
}

func (c *HTTPClient) validate(encoded adapters.EncodedRequest) (*url.URL, error) {
	if c == nil || c.client == nil || encoded.MaxResponseBytes <= 0 || encoded.Method == "" {
		return nil, ErrInvalidRequest
	}
	u, err := url.Parse(encoded.Endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, ErrInvalidEndpoint
	}
	if u.Scheme != "https" && !(c.allowHTTP && u.Scheme == "http" && isLoopbackHost(u.Hostname())) {
		return nil, ErrInvalidEndpoint
	}
	if c.localTestPort != "" {
		if u.Scheme == "http" {
			if u.Port() != c.localTestPort || u.Path != "/v1/localtest/chat/completions" || u.RawQuery != "" || !isLoopbackHost(u.Hostname()) {
				return nil, ErrInvalidEndpoint
			}
		} else if _, allowed := c.providerURLs[u.String()]; !allowed {
			return nil, ErrInvalidEndpoint
		}
	} else if c.providerURLs != nil {
		if u.Scheme != "https" {
			return nil, ErrInvalidEndpoint
		}
		if _, allowed := c.providerURLs[u.String()]; !allowed {
			return nil, ErrInvalidEndpoint
		}
	}
	if strings.ContainsAny(encoded.Method, "\r\n") {
		return nil, ErrInvalidRequest
	}
	if _, err := http.NewRequest(encoded.Method, u.String(), nil); err != nil {
		return nil, ErrInvalidRequest
	}
	seenHeaders := make(map[string]struct{}, len(encoded.Headers))
	reserved := map[string]bool{"Host": true, "Connection": true, "Proxy-Authorization": true, "Cookie": true, "Content-Length": true, "Transfer-Encoding": true}
	for key, value := range encoded.Headers {
		canonical := http.CanonicalHeaderKey(key)
		if !validHeaderName(key) || canonical == "" || strings.ContainsAny(value, "\r\n") || reserved[canonical] {
			return nil, ErrInvalidRequest
		}
		if _, duplicate := seenHeaders[canonical]; duplicate {
			return nil, ErrInvalidRequest
		}
		seenHeaders[canonical] = struct{}{}
		if strings.EqualFold(canonical, "Authorization") && !strings.EqualFold(canonical, encoded.CredentialHeader) {
			return nil, ErrInvalidRequest
		}
	}
	return u, nil
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
			return false
		}
	}
	return true
}

type boundedResponseBody struct {
	body  io.ReadCloser
	limit int64
	read  int64
	over  bool
}

func (b *boundedResponseBody) Read(p []byte) (int, error) {
	if b.over {
		return 0, ErrResponseTooLarge
	}
	remaining := b.limit - b.read
	if remaining < 0 {
		b.over = true
		return 0, ErrResponseTooLarge
	}
	if int64(len(p)) > remaining+1 {
		p = p[:remaining+1]
	}
	n, err := b.body.Read(p)
	b.read += int64(n)
	if b.read > b.limit {
		b.over = true
		return 0, ErrResponseTooLarge
	}
	return n, err
}

func (b *boundedResponseBody) Close() error { return b.body.Close() }

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
