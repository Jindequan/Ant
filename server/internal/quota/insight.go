package quota

import (
	"context"
	"time"
)

// Insight is how one allowance or balance of an account is going, from its
// recorded history: whether it runs out at the recent pace, and its recent
// values. The page shows it on the matching row of the card.
type Insight struct {
	// Which row: a window by name (and model), or a balance by currency
	// (and label).
	Window   string `json:"window,omitempty"`
	Model    string `json:"model,omitempty"`
	Currency string `json:"currency,omitempty"`
	Label    string `json:"label,omitempty"`

	Outlook string     `json:"outlook"`          // runs_out | lasts | idle | "" (not enough history yet)
	At      *time.Time `json:"at,omitempty"`     // runs_out: when, at this pace
	PerDay  *float64   `json:"perDay,omitempty"` // spent per day, in the row's unit
	Unit    string     `json:"unit,omitempty"`
	// Points are recent [unix ms, remaining] pairs, oldest first.
	Points [][2]float64 `json:"points,omitempty"`
}

// InsightSource gives each connection's insights, by connection id.
type InsightSource interface {
	Insights(ctx context.Context) (map[string][]Insight, error)
}
