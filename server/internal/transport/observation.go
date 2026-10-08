package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ant/server/internal/domain/observed"
)

// ErrOriginNotAllowed: a collector asked for an origin its module doesn't
// declare.
var ErrOriginNotAllowed = errors.New("transport: origin not declared by the provider module")

const (
	observationTimeout  = 20 * time.Second
	observationMaxBytes = 1 << 20
)

// NewObservationFetch is the network a provider module's collector reads
// through (design 12 §2): HTTPS only, only the module's declared origins
// ("https://host" or "https://host:port"), no redirect to another origin or
// from HTTPS to HTTP (so a key in a custom header can't leave its origin),
// replies bounded in size and time. base is nil in production; tests pass a
// transport that trusts their server.
func NewObservationFetch(origins []string, base http.RoundTripper) (observed.Fetch, error) {
	if len(origins) == 0 {
		return nil, ErrInvalidRequest
	}
	allowed := make(map[string]bool, len(origins))
	for _, o := range origins {
		u, err := url.Parse(o)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
			return nil, ErrInvalidEndpoint
		}
		allowed[originOf(u)] = true
	}
	if base == nil {
		base = http.DefaultTransport.(*http.Transport).Clone()
	}
	client := &http.Client{
		Timeout:   observationTimeout,
		Transport: base,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 || originOf(req.URL) != originOf(via[0].URL) {
				return http.ErrUseLastResponse // answered as the redirect itself
			}
			return nil
		},
	}
	return func(ctx context.Context, r observed.Request) (observed.Response, error) {
		u, err := url.Parse(r.URL)
		if err != nil || u.Scheme != "https" || u.User != nil {
			return observed.Response{}, ErrInvalidEndpoint
		}
		if !allowed[originOf(u)] {
			return observed.Response{}, ErrOriginNotAllowed
		}
		method := r.Method
		if method == "" {
			method = http.MethodGet
		}
		req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(r.Body))
		if err != nil {
			return observed.Response{}, ErrInvalidRequest
		}
		for k, v := range r.Header {
			req.Header.Set(k, v)
		}
		res, err := client.Do(req)
		if err != nil {
			return observed.Response{}, err
		}
		defer res.Body.Close()
		body, err := io.ReadAll(io.LimitReader(res.Body, observationMaxBytes+1))
		if err != nil {
			return observed.Response{}, err
		}
		if len(body) > observationMaxBytes {
			return observed.Response{}, ErrResponseTooLarge
		}
		return observed.Response{Status: res.StatusCode, Header: res.Header, Body: body}, nil
	}, nil
}

func originOf(u *url.URL) string { return strings.ToLower(u.Scheme + "://" + u.Host) }
