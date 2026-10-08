package quota

import (
	"context"
	"net/http"
	"sync"
	"time"

	"ant/server/internal/providers/catalog"
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

// openAIModels reads an OpenAI-style list with the shared parser.
func openAIModels(ctx context.Context, l Local, url string, headers map[string]string) ([]Model, error) {
	b, err := l.fetch(ctx, http.MethodGet, url, headers, nil)
	if err != nil {
		return nil, err
	}
	list, err := catalog.ParseOpenAIModels(b)
	if err != nil {
		return nil, err
	}
	out := make([]Model, len(list))
	for i, m := range list {
		out[i] = Model{ID: m.ID, Name: m.Name, Media: m.Media}
	}
	return out, nil
}

// keyModels lists models at url with the account's key as a bearer token.
func keyModels(ctx context.Context, a *Account, l Local, key, url string) {
	withModels(a, l, fingerprint(a.Provider, "models:"+url, key), func() ([]Model, error) {
		return openAIModels(ctx, l, url, bearer(key))
	})
}

// bearer is the usual key header.
func bearer(key string) map[string]string { return map[string]string{"Authorization": "Bearer " + key} }
