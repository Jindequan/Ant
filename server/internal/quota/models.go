package quota

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// What each account can call, as its provider lists it with the account's
// own sign-in or key: the models a router may send that account's requests
// to. Lists change rarely, so each is kept for modelTTL; a read that fails
// keeps the last list.

// Model is one model an account can use.
type Model struct {
	ID    string `json:"id"`              // what requests name
	Name  string `json:"name,omitempty"`  // as the provider shows it
	Media string `json:"media,omitempty"` // text, image, video, speech, embedding…
}

const modelTTL = 6 * time.Hour

var modelCache = struct {
	sync.Mutex
	m map[string]modelEntry
}{m: map[string]modelEntry{}}

type modelEntry struct {
	models []Model
	at     time.Time
}

// withModels sets a's models from fetch, at most once per modelTTL per key
// (the provider and the credential's account). Failures keep the last list.
func withModels(a *Account, l Local, key string, fetch func() ([]Model, error)) {
	if a.Status != StatusOK || key == "" {
		return
	}
	now := l.Now()
	modelCache.Lock()
	e, ok := modelCache.m[key]
	modelCache.Unlock()
	if !ok || now.Sub(e.at) > modelTTL {
		if ms, err := fetch(); err == nil && len(ms) > 0 {
			e = modelEntry{models: ms, at: now}
			modelCache.Lock()
			modelCache.m[key] = e
			modelCache.Unlock()
		}
	}
	a.Models = e.models
}

// fixedModels is a plan's published model list, when its provider has no
// listing for it.
func fixedModels(ids ...string) []Model {
	out := make([]Model, len(ids))
	for i, id := range ids {
		out[i] = Model{ID: id, Media: "text"}
	}
	return out
}

// openAIModels reads an OpenAI-style list: {"data":[{"id":"…"},…]}.
func openAIModels(ctx context.Context, l Local, url string, headers map[string]string) ([]Model, error) {
	b, err := l.fetch(ctx, http.MethodGet, url, headers, nil)
	if err != nil {
		return nil, err
	}
	var r struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			// some lists say what a model is for
			Type         string   `json:"type"`
			Capabilities []string `json:"capabilities"`
			Arch         *struct {
				Output []string `json:"output_modalities"`
			} `json:"architecture"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &r) != nil {
		return nil, errors.New("not a model list")
	}
	var out []Model
	for _, m := range r.Data {
		if m.ID == "" {
			continue
		}
		media := modelMedia(m.ID, m.Type)
		if m.Arch != nil && len(m.Arch.Output) > 0 && !contains(m.Arch.Output, "text") {
			media = m.Arch.Output[0]
		}
		out = append(out, Model{ID: m.ID, Name: m.Name, Media: media})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// modelMedia guesses what a model makes from its id and the list's type
// field, for lists that don't say.
func modelMedia(id, typ string) string {
	s := strings.ToLower(id + " " + typ)
	switch {
	case strings.Contains(s, "embed"):
		return "embedding"
	case strings.Contains(s, "rerank"):
		return "rerank"
	case strings.Contains(s, "tts") || strings.Contains(s, "speech") || strings.Contains(s, "cosyvoice") || strings.Contains(s, "voice"):
		return "speech"
	case strings.Contains(s, "whisper") || strings.Contains(s, "asr") || strings.Contains(s, "-audio") || strings.Contains(s, "omni-realtime") || strings.Contains(s, "transcri") || strings.Contains(s, "paraformer"):
		return "audio"
	case strings.Contains(s, "video") || strings.Contains(s, "wan2") || strings.Contains(s, "wan3") || strings.Contains(s, "seedance") || strings.Contains(s, "kling"):
		return "video"
	case strings.Contains(s, "image") || strings.Contains(s, "dall-e") || strings.Contains(s, "flux") || strings.Contains(s, "seedream") ||
		strings.Contains(s, "stable-diffusion") || strings.Contains(s, "sdxl") || strings.Contains(s, "kolors"):
		return "image"
	}
	return "text"
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// keyModels lists models at url with the account's key as a bearer token.
func keyModels(ctx context.Context, a *Account, l Local, key, url string) {
	withModels(a, l, fingerprint(a.Provider, "models:"+url, key), func() ([]Model, error) {
		return openAIModels(ctx, l, url, bearer(key))
	})
}

// bearer is the usual key header.
func bearer(key string) map[string]string { return map[string]string{"Authorization": "Bearer " + key} }
