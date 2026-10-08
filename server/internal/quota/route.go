package quota

import "context"

// RouteChoice is one account in a route preview: why it would be used (in
// order), or why not.
type RouteChoice struct {
	ID       string `json:"id"` // the connection: the page finds its card by it
	Provider string `json:"provider"`
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
	Model    string `json:"model"` // as the provider names it
	Class    string `json:"class,omitempty"`
	Reason   string `json:"reason"`
	Stale    string `json:"stale,omitempty"`
}

// RoutePreview is which accounts would serve a model, best first, and which
// wouldn't. Nothing is sent to a provider to make it.
type RoutePreview struct {
	Model   string        `json:"model"`
	Use     []RouteChoice `json:"use"`
	NotUsed []RouteChoice `json:"notUsed"`
}

// Router answers route previews and keeps the user's pauses.
type Router interface {
	Preview(ctx context.Context, model string) (RoutePreview, error)
	SetPaused(ctx context.Context, ids []string, paused bool) error
	Paused(ctx context.Context) (map[string]bool, error)
}

// pausedCards are the cards with a paused connection.
func (s *Service) pausedCards(accounts []Account) []string {
	out := []string{}
	if s.router == nil {
		return out
	}
	paused, err := s.router.Paused(context.Background())
	if err != nil {
		return out
	}
	for _, a := range accounts {
		for _, k := range append([]string{a.ID}, a.Keys...) {
			if paused[k] {
				out = append(out, a.ID)
				break
			}
		}
	}
	return out
}

// cardKeys are the connections shown on card id.
func (s *Service) cardKeys(id string) []string {
	for _, a := range s.Snapshot().Accounts {
		if a.ID == id {
			if len(a.Keys) > 0 {
				return a.Keys
			}
			return []string{a.ID}
		}
	}
	return nil
}
