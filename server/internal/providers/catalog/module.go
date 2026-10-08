package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"ant/server/internal/domain/observed"
)

// Module is one provider: everything Ant knows about it, owned in one
// package internal/providers/<id> (design 12 §2, ruling R1). This slice
// covers the supply side; the execution side keeps its existing
// registration until modules carry it too.
type Module struct {
	ID, Name string
	Supply   *Supply // nil when Ant can't read what an account holds
}

// Supply is how Ant reads what an account holds.
type Supply struct {
	Origins   []string // every https origin the collector may contact
	Endpoints []Endpoint
	Collector Collector
}

// Endpoint is where an account's models are called, and the meters a call
// there draws on (resource template refs of the inventory).
type Endpoint struct {
	Base     string
	Protocol string // openai-chat, anthropic-messages, …
	Draws    []string
}

// Session is the credential a collector reads with: its values (an API key,
// a token), resolved by the engine. Collectors keep nothing.
type Session struct {
	Values map[string]string
}

// Collector reads one account. Implementations parse only: the network is
// fetch, nothing is stored, and the clock is the caller's.
type Collector interface {
	Inventory(ctx context.Context, s Session, fetch observed.Fetch) (observed.Inventory, error)
	Models(ctx context.Context, s Session, fetch observed.Fetch) ([]observed.UpstreamModel, error)
}

// ErrSignedOut: the provider refused the credential. Wrap it with what the
// user should do.
var ErrSignedOut = errors.New("signed out")

// ParseOpenAIModels reads an OpenAI-style model list,
// {"data":[{"id":"…"},…]}, sorted by id.
func ParseOpenAIModels(b []byte) ([]observed.UpstreamModel, error) {
	var r struct {
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
			// some lists say what a model is for
			Type string `json:"type"`
			Arch *struct {
				Output []string `json:"output_modalities"`
			} `json:"architecture"`
		} `json:"data"`
	}
	if json.Unmarshal(b, &r) != nil {
		return nil, errors.New("not a model list")
	}
	var out []observed.UpstreamModel
	for _, m := range r.Data {
		if m.ID == "" {
			continue
		}
		media := ModelMedia(m.ID, m.Type)
		if m.Arch != nil && len(m.Arch.Output) > 0 && !contains(m.Arch.Output, "text") {
			media = m.Arch.Output[0]
		}
		out = append(out, observed.UpstreamModel{ID: m.ID, Name: m.Name, Media: media})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ModelMedia guesses what a model makes from its id and the list's type
// field, for lists that don't say.
func ModelMedia(id, typ string) string {
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
