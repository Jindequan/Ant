package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"ant/server/internal/adapters"
)

var ErrBearerJSONGet = errors.New("transport: provider read unavailable")

// BearerJSONGET makes one bounded request to a fixed endpoint. The endpoint
// itself must also be pinned by the supplied HTTPClient's exact URL allowlist.
type BearerJSONGET struct {
	client *HTTPClient
	url    string
	limit  int64
}

func NewBearerJSONGET(client *HTTPClient, endpoint string, maxResponseBytes int64) (*BearerJSONGET, error) {
	if client == nil || endpoint == "" || maxResponseBytes <= 0 {
		return nil, ErrInvalidRequest
	}
	request := adapters.EncodedRequest{Endpoint: endpoint, Method: http.MethodGet, MaxResponseBytes: maxResponseBytes}
	if err := client.ValidateRequest(request); err != nil {
		return nil, err
	}
	return &BearerJSONGET{client: client, url: endpoint, limit: maxResponseBytes}, nil
}

func (c *BearerJSONGET) FetchBalance(ctx context.Context, key string) ([]byte, error) {
	if c == nil || ctx == nil || key == "" || len(key) > 8192 || strings.ContainsAny(key, "\r\n") {
		return nil, ErrBearerJSONGet
	}
	resp, err := c.client.DoOnce(ctx, adapters.EncodedRequest{
		Endpoint:         c.url,
		Method:           http.MethodGet,
		Headers:          map[string]string{"Accept": "application/json", "Authorization": "Bearer " + key},
		CredentialHeader: "Authorization",
		MaxResponseBytes: c.limit,
	})
	if err != nil || resp == nil || resp.Body == nil {
		return nil, ErrBearerJSONGet
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrBearerJSONGet
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil || len(data) == 0 || int64(len(data)) > c.limit {
		for i := range data {
			data[i] = 0
		}
		return nil, ErrBearerJSONGet
	}
	return data, nil
}
