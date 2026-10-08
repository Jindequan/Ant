// Package observed holds what a provider read observes and the network seam
// it reads through (design 12 §2). It is a leaf: the supply engine, provider
// modules and the transport all use it, and it uses only domain.
package observed

import (
	"context"

	"ant/server/internal/domain"
)

// Inventory is what one read says the account holds (design 11 §3.2).
type Inventory struct {
	// Identity is the provider-reported account identity. Two credentials
	// with the same Identity are one account. Empty when the provider
	// reports none: the credential is then its own account, never merged
	// by inference (§2.6).
	Identity    string
	DisplayName string
	Packages    []PackageReading
	// DisplayGroup puts readings of one provider account on one card when
	// the provider reports no identity but the readings show it (Z.ai keys
	// of one plan read the same windows). Display only: the store never
	// merges accounts on it (design 11 §2.6).
	DisplayGroup string
	// Note is what the user should know about this read, in a sentence.
	Note string
}

type PackageReading struct {
	TemplateRef string
	Kind        string // subscription | prepaid | payg | free_grant | credit_pack
	Basis       string // observed | declared (§3.6: declared packages get no readings)
	Plan        string
	StartsAt    *domain.Millis
	RenewsAt    *domain.Millis
	EndsAt      *domain.Millis
	AutoRenew   *bool
	// Usable: whether the provider accepts calls on this package now (a
	// balance too low to call is false); nil when it doesn't say.
	Usable    *bool
	Resources []ResourceReading
}

// ResourceReading is one meter and, for an observed package, its value now.
type ResourceReading struct {
	TemplateRef       string
	Label             string // as the user sees it: "5 hours", "Week", "Balance"
	Aside             bool   // used up, it doesn't stop the models (Z.ai's MCP calls)
	MetricID          domain.ID
	Unit              string
	QuantumExp        int
	Currency          string
	SemanticsRef      domain.ID
	SemanticsRevision domain.ID
	WindowJSON        []byte
	ScopeJSON         []byte

	ResetBasis  string // reported | computed | none
	WindowStart *domain.Millis
	WindowEnd   *domain.Millis
	Limit       *domain.Units
	Used        *domain.Units
	Remaining   *domain.Units
	Evidence    []byte // JSON, ≤ evidenceLimit
}

// UpstreamModel is a model an account lists, as the provider names it.
type UpstreamModel struct {
	ID    string // what requests name
	Name  string // as the provider shows it
	Media string // text, image, video, speech, embedding…
}

// Request is one HTTP request a collector makes.
type Request struct {
	Method string
	URL    string
	Header map[string]string
	Body   []byte
}

// Response is the reply; Body is bounded by the Fetch implementation.
type Response struct {
	Status int
	Header map[string][]string
	Body   []byte
}

// Fetch is the only network access a collector has. The implementation
// (internal/transport) enforces the module's origins, HTTPS, redirects,
// size and time.
type Fetch func(ctx context.Context, r Request) (Response, error)
