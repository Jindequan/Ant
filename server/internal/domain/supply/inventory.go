// Package supply describes configured resource supply independently of the
// executable ledger. A declaration is never a verified or available balance.
package supply

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
)

const SchemaVersion = 1

var ErrInvalid = errors.New("supply: invalid inventory")
var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var decimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,37})(\.[0-9]{1,18})?$`)
var currency = regexp.MustCompile(`^[A-Z]{3}$`)

// ExactDecimal validates and canonicalizes human quantities without float64.
func ValidID(id string) bool { return identifier.MatchString(id) }

func ExactDecimal(value string) (string, error) {
	if !decimal.MatchString(value) {
		return "", ErrInvalid
	}
	if strings.Contains(value, ".") {
		value = strings.TrimRight(strings.TrimRight(value, "0"), ".")
	}
	return value, nil
}

type Document struct {
	SchemaVersion int        `json:"schemaVersion"`
	Accounts      []Account  `json:"accounts"`
	Packages      []Package  `json:"packages"`
	Resources     []Resource `json:"resources"`
	Pools         []Pool     `json:"pools"`
}
type Account struct {
	ID           string `json:"id"`
	ProviderID   string `json:"providerId"`
	ProviderName string `json:"providerName"`
	Name         string `json:"name"`
}
type Price struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
	Cadence  string `json:"cadence"`         // once|week|month|year|usage
	Basis    string `json:"basis,omitempty"` // user-described price basis; not an executable calculator
}
type Package struct {
	ID           string   `json:"id"`
	AccountID    string   `json:"accountId"`
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`       // subscription|credit|free|payg
	Capability   string   `json:"capability"` // declared capability; never adapter admission
	Status       string   `json:"status"`     // configured|archived
	TimeZone     string   `json:"timeZone"`
	StartsOn     string   `json:"startsOn,omitempty"`
	RenewsOn     string   `json:"renewsOn,omitempty"`
	ExpiresOn    string   `json:"expiresOn,omitempty"`
	Price        *Price   `json:"price,omitempty"`
	ResourceRefs []string `json:"resourceRefs"`
}
type Reset struct {
	Kind     string `json:"kind"` // none|calendar|rolling
	Period   string `json:"period,omitempty"`
	TimeZone string `json:"timeZone,omitempty"`
	AnchorOn string `json:"anchorOn,omitempty"`
	Seconds  int64  `json:"seconds,omitempty"`
}
type Resource struct {
	Retired   bool   `json:"retired,omitempty"`
	ID        string `json:"id"`
	AccountID string `json:"accountId"`
	PackageID string `json:"packageId,omitempty"` // absent for account-wide shared constraints
	Name      string `json:"name"`
	Metric    string `json:"metric"`
	Unit      string `json:"unit"`
	Quantity  string `json:"quantity"`
	Role      string `json:"role"` // allowance|spending_limit|rate|concurrency
	Reset     Reset  `json:"reset"`
}
type Pool struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Capability  string   `json:"capability"`
	PackageRefs []string `json:"packageRefs"` // stable IDs, order is priority
}

func Empty() Document {
	return Document{SchemaVersion: SchemaVersion, Accounts: []Account{}, Packages: []Package{}, Resources: []Resource{}, Pools: []Pool{}}
}
func label(s string) bool {
	return strings.TrimSpace(s) == s && len(s) > 0 && len(s) <= 200 && !strings.ContainsAny(s, "\x00\r\n")
}
func date(s string) bool {
	if s == "" {
		return true
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}
func zone(s string) bool { _, err := time.LoadLocation(s); return s != "" && err == nil }
func unique(values []string) bool {
	seen := map[string]bool{}
	for _, v := range values {
		if seen[v] {
			return false
		}
		seen[v] = true
	}
	return true
}

// Normalize validates graph references, scoped sharing and commercial/window
// rules. Unlike executable config validation, missing credentials are not an
// error: this document is deliberately a non-executable supply inventory.
func (d *Document) Normalize() error {
	if d == nil || d.SchemaVersion != SchemaVersion || d.Accounts == nil || d.Packages == nil || d.Resources == nil || d.Pools == nil || len(d.Accounts) > 256 || len(d.Packages) > 512 || len(d.Resources) > 2048 || len(d.Pools) > 256 {
		return ErrInvalid
	}
	ids := map[string]bool{}
	takeID := func(id string) bool {
		if !identifier.MatchString(id) || ids[id] {
			return false
		}
		ids[id] = true
		return true
	}
	accounts := map[string]Account{}
	for _, a := range d.Accounts {
		if !takeID(a.ID) || !identifier.MatchString(a.ProviderID) || !label(a.ProviderName) || !label(a.Name) {
			return ErrInvalid
		}
		accounts[a.ID] = a
	}
	packages := map[string]Package{}
	for i := range d.Packages {
		p := &d.Packages[i]
		if !takeID(p.ID) || accounts[p.AccountID].ID == "" || !label(p.Name) || !identifier.MatchString(p.Capability) || !slices.Contains([]string{"subscription", "credit", "free", "payg"}, p.Kind) || !slices.Contains([]string{"configured", "archived"}, p.Status) || !zone(p.TimeZone) || !date(p.StartsOn) || !date(p.RenewsOn) || !date(p.ExpiresOn) || (p.StartsOn != "" && p.ExpiresOn != "" && p.ExpiresOn < p.StartsOn) || (p.RenewsOn != "" && p.Kind != "subscription") || len(p.ResourceRefs) == 0 || len(p.ResourceRefs) > 64 || !unique(p.ResourceRefs) {
			return ErrInvalid
		}
		if p.Price != nil {
			v, err := ExactDecimal(p.Price.Amount)
			if err != nil || !currency.MatchString(p.Price.Currency) || !slices.Contains([]string{"once", "week", "month", "year", "usage"}, p.Price.Cadence) || len(p.Price.Basis) > 200 {
				return ErrInvalid
			}
			if p.Kind == "payg" && p.Price.Cadence != "usage" {
				return ErrInvalid
			}
			if p.Kind == "subscription" && !slices.Contains([]string{"week", "month", "year"}, p.Price.Cadence) {
				return ErrInvalid
			}
			p.Price.Amount = v
		}
		packages[p.ID] = *p
	}
	resources := map[string]Resource{}
	for i := range d.Resources {
		r := &d.Resources[i]
		value, err := ExactDecimal(r.Quantity)
		if !takeID(r.ID) || accounts[r.AccountID].ID == "" || !label(r.Name) || !identifier.MatchString(r.Metric) || !label(r.Unit) || len(r.Unit) > 32 || err != nil || value == "0" || !slices.Contains([]string{"allowance", "spending_limit", "rate", "concurrency"}, r.Role) {
			return ErrInvalid
		}
		if r.PackageID != "" && (packages[r.PackageID].ID == "" || packages[r.PackageID].AccountID != r.AccountID) {
			return ErrInvalid
		}
		if r.Role == "concurrency" && (r.Metric != "concurrency" || r.Unit != "slots" || strings.Contains(value, ".") || r.Reset.Kind != "none") {
			return ErrInvalid
		}
		if r.Role == "rate" && r.Reset.Kind == "none" {
			return ErrInvalid
		}
		if r.Role == "spending_limit" && (r.Metric != "money" || !currency.MatchString(r.Unit)) {
			return ErrInvalid
		}
		switch r.Metric {
		case "tokens":
			if r.Unit != "tokens" {
				return ErrInvalid
			}
		case "requests":
			if r.Unit != "requests" {
				return ErrInvalid
			}
		case "money":
			if !currency.MatchString(r.Unit) {
				return ErrInvalid
			}
		case "storage":
			if !slices.Contains([]string{"bytes", "GB"}, r.Unit) {
				return ErrInvalid
			}
		case "compute":
			if !slices.Contains([]string{"seconds", "hours"}, r.Unit) {
				return ErrInvalid
			}
		case "concurrency":
			if r.Role != "concurrency" {
				return ErrInvalid
			}
		default:
			if !strings.HasPrefix(r.Metric, "custom.") {
				return ErrInvalid
			}
		}
		switch r.Reset.Kind {
		case "none":
			if r.Reset.Period != "" || r.Reset.TimeZone != "" || r.Reset.AnchorOn != "" || r.Reset.Seconds != 0 {
				return ErrInvalid
			}
		case "calendar":
			if !slices.Contains([]string{"minute", "hour", "day", "week", "month", "year"}, r.Reset.Period) || !zone(r.Reset.TimeZone) || !date(r.Reset.AnchorOn) || r.Reset.Seconds != 0 {
				return ErrInvalid
			}
		case "rolling":
			if r.Reset.Seconds < 1 || r.Reset.Seconds > 31_536_000 || r.Reset.Period != "" || r.Reset.TimeZone != "" || r.Reset.AnchorOn != "" {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
		r.Quantity = value
		resources[r.ID] = *r
	}
	for _, p := range d.Packages {
		hasBudget := false
		for _, id := range p.ResourceRefs {
			r := resources[id]
			if r.ID == "" || r.Retired || r.AccountID != p.AccountID || (r.PackageID != "" && r.PackageID != p.ID) {
				return ErrInvalid
			}
			if r.Role == "spending_limit" {
				hasBudget = true
			}
		}
		if p.Kind == "payg" && !hasBudget {
			return ErrInvalid
		}
	}
	for _, r := range d.Resources {
		if r.PackageID != "" && !r.Retired && !slices.Contains(packages[r.PackageID].ResourceRefs, r.ID) {
			return ErrInvalid
		}
	}
	// Account-wide constraints apply to every package on that account. They
	// are referenced, never copied, and cannot be opted out of by a package.
	for _, r := range d.Resources {
		if r.PackageID == "" && !r.Retired {
			for _, p := range d.Packages {
				if p.AccountID == r.AccountID && !slices.Contains(p.ResourceRefs, r.ID) {
					return ErrInvalid
				}
			}
		}
	}
	for _, p := range d.Pools {
		if !takeID(p.ID) || !label(p.Name) || !identifier.MatchString(p.Capability) || p.PackageRefs == nil || len(p.PackageRefs) > 512 || !unique(p.PackageRefs) {
			return ErrInvalid
		}
		for _, id := range p.PackageRefs {
			pack := packages[id]
			if pack.ID == "" || pack.Capability != p.Capability || pack.Status != "configured" {
				return ErrInvalid
			}
		}
	}
	return nil
}

// CompatibleUpdate preserves durable identity. Existing supplies are archived,
// not deleted. Editable declaration values do not mutate executable ledgers.
func CompatibleUpdate(old, next Document) bool {
	accounts := map[string]Account{}
	for _, a := range next.Accounts {
		accounts[a.ID] = a
	}
	for _, a := range old.Accounts {
		n := accounts[a.ID]
		if n.ID == "" || n.ProviderID != a.ProviderID {
			return false
		}
	}
	packages := map[string]Package{}
	for _, p := range next.Packages {
		packages[p.ID] = p
	}
	for _, p := range old.Packages {
		n := packages[p.ID]
		if n.ID == "" || n.AccountID != p.AccountID {
			return false
		}
	}
	resources := map[string]Resource{}
	for _, r := range next.Resources {
		resources[r.ID] = r
	}
	for _, r := range old.Resources {
		n := resources[r.ID]
		if n.ID == "" || n.AccountID != r.AccountID || n.PackageID != r.PackageID || n.Metric != r.Metric || n.Unit != r.Unit {
			return false
		}
	}
	// Removed declarations retain their identity as retired records. Restoring
	// a declaration cannot change its owner, metric or unit.
	return true
}
