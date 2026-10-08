package supply

import "ant/server/internal/domain"

// Inventory is what one read says the account holds (design 11 §3.2).
type Inventory struct {
	// Identity is the provider-reported account identity. Two credentials
	// with the same Identity are one account. Empty when the provider
	// reports none: the credential is then its own account, never merged
	// by inference (§2.6).
	Identity    string
	DisplayName string
	Packages    []PackageReading
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
	Resources   []ResourceReading
}

// ResourceReading is one meter and, for an observed package, its value now.
type ResourceReading struct {
	TemplateRef       string
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
