package api

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
)

var localtestSequence atomic.Uint64

type localtestRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	MaxTokens           *int64 `json:"max_tokens"`
	MaxCompletionTokens *int64 `json:"max_completion_tokens"`
	Stream              bool   `json:"stream"`
}

// LocalTestChatCompletionsHandler is a loopback-only synthetic endpoint. It
// exists solely to exercise the configured localtest execution pipeline.
func LocalTestChatCompletionsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/localtest/chat/completions" {
			http.NotFound(w, r)
			return
		}
		remoteHost := r.RemoteAddr
		if host, _, err := net.SplitHostPort(remoteHost); err == nil {
			remoteHost = host
		}
		ip := net.ParseIP(strings.Trim(remoteHost, "[]"))
		if ip == nil || !ip.IsLoopback() {
			http.Error(w, "localtest endpoint accepts loopback traffic only", http.StatusForbidden)
			return
		}
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		var body localtestRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&body); err != nil || ensureLocalTestRequestEnd(decoder) != nil || body.Model == "" || len(body.Messages) == 0 || (body.MaxTokens == nil) == (body.MaxCompletionTokens == nil) || body.Stream {
			http.Error(w, "invalid localtest request", http.StatusBadRequest)
			return
		}
		limit := body.MaxTokens
		if limit == nil {
			limit = body.MaxCompletionTokens
		}
		if *limit < 1 || *limit > 8192 {
			http.Error(w, "invalid localtest request", http.StatusBadRequest)
			return
		}
		var promptTokens int64
		for _, message := range body.Messages {
			promptTokens += int64(len([]rune(message.Content)))
		}
		completionTokens := int64(1)
		result := map[string]any{
			"id": "localtest-response-" + strconv.FormatUint(localtestSequence.Add(1), 10), "object": "chat.completion", "created": int64(1_800_000_000), "model": body.Model,
			"choices": []any{map[string]any{"index": 0, "message": map[string]string{"role": "assistant", "content": "Local test reply"}, "finish_reason": "stop"}},
			"usage":   map[string]int64{"prompt_tokens": promptTokens, "completion_tokens": completionTokens, "total_tokens": promptTokens + completionTokens},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(result)
	})
}

func ensureLocalTestRequestEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return io.ErrUnexpectedEOF
	}
	return nil
}
