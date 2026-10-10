package quota

import (
	"context"
	"errors"
)

var (
	ErrRouteOrderConflict     = errors.New("quota: route order version conflict")
	ErrRouteCandidatesChanged = errors.New("quota: eligible routes changed")
	ErrRouteOrderInvalid      = errors.New("quota: invalid route order")
)

// RouteChoice is one account in a route preview: why it would be used (in
// order), or why not.
type RouteChoice struct {
	ID              string          `json:"id"` // the connection: the page finds its card by it
	Provider        string          `json:"provider"`
	Name            string          `json:"name"`
	Endpoint        string          `json:"endpoint"`
	Model           string          `json:"model"` // as the provider names it
	Class           string          `json:"class,omitempty"`
	Reason          string          `json:"reason"`
	Stale           string          `json:"stale,omitempty"`
	OfferID         string          `json:"offerId,omitempty"`
	OfferVerifiedAt int64           `json:"offerVerifiedAt,omitempty"`
	Evidence        []RouteEvidence `json:"evidence,omitempty"`
}

// RouteEvidence identifies the stored reading versions used for this answer.
type RouteEvidence struct {
	ResourceID string `json:"resourceId"`
	ReadingID  string `json:"readingId,omitempty"`
	ObservedAt int64  `json:"observedAt,omitempty"`
}

// RoutePreview is which accounts would serve a model, best first, and which
// wouldn't. Nothing is sent to a provider to make it.
type RoutePreview struct {
	Model         string        `json:"model"`
	OrderVersion  int64         `json:"orderVersion"`
	ManualOrder   bool          `json:"manualOrder"`
	SavedOfferIDs []string      `json:"savedOfferIds"`
	Use           []RouteChoice `json:"use"`
	NotUsed       []RouteChoice `json:"notUsed"`
}

// RouteOrderResult reports the durable outcome separately from refreshing the
// preview. Preview may be nil after a committed write if the follow-up read
// failed; clients can then perform a read-only refresh.
type RouteOrderResult struct {
	Outcome      string        `json:"outcome"` // committed | unknown
	Model        string        `json:"model"`
	OrderVersion int64         `json:"orderVersion"`
	ManualOrder  bool          `json:"manualOrder"`
	Preview      *RoutePreview `json:"preview,omitempty"`
	Message      string        `json:"message,omitempty"`
}

// Router answers route previews and keeps the user's pauses.
type Router interface {
	Preview(ctx context.Context, model string) (RoutePreview, error)
	SetPaused(ctx context.Context, ids []string, paused bool) error
	Paused(ctx context.Context) (map[string]bool, error)
}

// RouteOrderer is separate from Router so older preview-only adapters remain
// read-only until they implement the supply-store order write path.
type RouteOrderer interface {
	Reorder(ctx context.Context, model string, offerIDs []string, expectedVersion int64) (RouteOrderResult, error)
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
