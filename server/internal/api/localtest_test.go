package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalTestChatCompletionsIsLoopbackOnlyAndDeterministic(t *testing.T) {
	handler := LocalTestChatCompletionsHandler()
	request := func(remote, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/v1/localtest/chat/completions", strings.NewReader(body))
		r.RemoteAddr = remote
		r.Header.Set("Authorization", "Bearer test-only")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	body := `{"model":"fixture","messages":[{"role":"user","content":"你好"}],"max_tokens":1,"stream":false}`
	if denied := request("198.51.100.2:3000", body); denied.Code != http.StatusForbidden {
		t.Fatalf("external caller status=%d", denied.Code)
	}
	accepted := request("127.0.0.1:3000", body)
	if accepted.Code != http.StatusOK {
		t.Fatalf("loopback request status=%d body=%s", accepted.Code, accepted.Body.String())
	}
	var response struct {
		Model string `json:"model"`
		Usage struct {
			Prompt int64 `json:"prompt_tokens"`
			Total  int64 `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(accepted.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Model != "fixture" || response.Usage.Prompt != 2 || response.Usage.Total != 3 {
		t.Fatalf("unexpected response: %+v", response)
	}
	if streaming := request("127.0.0.1:3000", `{"model":"fixture","messages":[{"role":"user","content":"你好"}],"max_tokens":1,"stream":true}`); streaming.Code != http.StatusBadRequest {
		t.Fatalf("streaming request status=%d", streaming.Code)
	}
	if malformed := request("127.0.0.1:3000", body+` {}`); malformed.Code != http.StatusBadRequest {
		t.Fatalf("trailing JSON status=%d", malformed.Code)
	}
}
