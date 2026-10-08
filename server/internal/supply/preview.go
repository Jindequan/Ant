package supply

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

// Route preview (design 12 §5): for one model, which accounts could serve
// it, in the order Ant would use them, and which it wouldn't, each with one
// sentence of why. Read-only: nothing is sent to any provider.

// Candidate classes, best first.
const (
	ClassUseBeforeLost = "use_before_lost"
	ClassPlan          = "plan"
	ClassPrepaid       = "prepaid"
	ClassPayg          = "payg"
)

var classRank = map[string]int{ClassUseBeforeLost: 0, ClassPlan: 1, ClassPrepaid: 2, ClassPayg: 3}

const (
	staleAfter     = 30 * time.Minute
	loseWithin     = 24 * time.Hour
	loseAtLeastPct = 30.0
)

// Route is one way to the model: an account's credential at an endpoint.
type Route struct {
	CredentialID  domain.ID
	ProviderID    domain.ID
	Endpoint      string
	UpstreamModel string
}

type Candidate struct {
	Route
	Class  string
	Reason string
	Stale  string // "reading is 2h old", or ""
	score  float64
	fresh  domain.Millis
}

type Excluded struct {
	Route
	Reason string
}

type Preview struct {
	Model    string
	Eligible []Candidate // best first
	Excluded []Excluded
}

// meter is one drawn resource with what's known about it now.
type meter struct {
	res     store.SupplyResourceRecord
	pkg     store.SupplyPackageRecord
	latest  *store.SupplyReadingRecord
	outlook Outlook
}

// Preview answers for a canonical model id.
func (e *Engine) Preview(ctx context.Context, model string) (Preview, error) {
	now := e.clock.Now()
	out := Preview{Model: model}
	err := e.st.Read(ctx, func(rx store.ReaderTx) error {
		sx, ok := rx.(store.SupplyReaderTx)
		if !ok {
			return errNoSupplyStore
		}
		offers, err := sx.ListSupplyOffers(e.ns, model)
		if err != nil || len(offers) == 0 {
			return err
		}
		creds, err := sx.ListSupplyCredentials(e.ns)
		if err != nil {
			return err
		}
		pkgs, err := sx.ListSupplyPackages(e.ns)
		if err != nil {
			return err
		}
		resources, err := sx.ListSupplyResources(e.ns)
		if err != nil {
			return err
		}
		credOf := map[domain.ID]store.SupplyCredentialRecord{}
		for _, c := range creds {
			credOf[c.CredentialID] = c
		}
		pkgOf := map[domain.ID]store.SupplyPackageRecord{}
		pkgsOfAccount := map[domain.ID][]store.SupplyPackageRecord{}
		for _, p := range pkgs {
			pkgOf[p.PackageID] = p
			pkgsOfAccount[p.AccountID] = append(pkgsOfAccount[p.AccountID], p)
		}
		resOf := map[domain.ID]map[string]store.SupplyResourceRecord{} // account → template ref
		for _, r := range resources {
			a := pkgOf[r.PackageID].AccountID
			if resOf[a] == nil {
				resOf[a] = map[string]store.SupplyResourceRecord{}
			}
			resOf[a][r.TemplateRef] = r
		}
		for _, o := range offers {
			c, ok := credOf[o.CredentialID]
			if !ok {
				continue
			}
			route := Route{CredentialID: c.CredentialID, ProviderID: c.ProviderID, Endpoint: o.Endpoint, UpstreamModel: o.UpstreamModel}
			var draws []string
			_ = json.Unmarshal(o.DrawsJSON, &draws)
			var meters []meter
			for _, ref := range draws {
				r, found := resOf[c.AccountID][ref]
				if !found {
					continue
				}
				m := meter{res: r, pkg: pkgOf[r.PackageID]}
				readings, err := sx.ListSupplyReadings(e.ns, r.ResourceID, now-ms(balanceLookback))
				if err != nil {
					return err
				}
				if len(readings) == 0 {
					latest, found, err := sx.LatestSupplyReading(e.ns, r.ResourceID)
					if err != nil {
						return err
					}
					if found {
						readings = append(readings, latest)
					}
				}
				if len(readings) > 0 {
					m.latest = &readings[len(readings)-1]
					m.outlook = Forecast(readings, verifiedAt(c))
				}
				meters = append(meters, m)
			}
			cand, why := judge(c, meters, pkgsOfAccount[c.AccountID], now)
			if why != "" {
				out.Excluded = append(out.Excluded, Excluded{Route: route, Reason: why})
				continue
			}
			cand.Route = route
			out.Eligible = append(out.Eligible, cand)
		}
		return nil
	})
	sort.SliceStable(out.Eligible, func(i, j int) bool {
		a, b := out.Eligible[i], out.Eligible[j]
		if classRank[a.Class] != classRank[b.Class] {
			return classRank[a.Class] < classRank[b.Class]
		}
		if a.score != b.score {
			return a.score > b.score
		}
		return a.fresh > b.fresh
	})
	return out, err
}

func verifiedAt(c store.SupplyCredentialRecord) domain.Millis {
	if c.VerifiedAt == nil {
		return 0
	}
	return *c.VerifiedAt
}

// judge decides one route: a candidate, or why not (design 12 §5, in order).
func judge(c store.SupplyCredentialRecord, meters []meter, accountPkgs []store.SupplyPackageRecord, now domain.Millis) (Candidate, string) {
	switch {
	case c.Paused:
		return Candidate{}, "Paused by you"
	case c.Status == "signed_out":
		return Candidate{}, joinReason("Signed out", c.StatusDetail)
	case c.Status == "error":
		return Candidate{}, joinReason("Couldn't read it", c.StatusDetail)
	case c.Status != "ok" || c.AccountID == "":
		return Candidate{}, "Not read yet"
	}
	for _, m := range meters {
		if m.pkg.Basis == "declared" {
			return Candidate{}, "Only declared, never read"
		}
		if left, ok := remaining(m); ok && left <= 0 {
			why := name(m) + " used up"
			if m.latest.WindowEnd != nil && *m.latest.WindowEnd > now {
				why += " · resets in " + span(time.Duration(*m.latest.WindowEnd-now)*time.Millisecond)
			}
			return Candidate{}, why
		}
		if usable, said := packageUsable(m.pkg); said && !usable {
			return Candidate{}, "Balance too low to call"
		}
	}
	cand := Candidate{fresh: verifiedAt(c)}
	if age := now - verifiedAt(c); age > ms(staleAfter) {
		cand.Stale = "reading is " + span(time.Duration(age)*time.Millisecond) + " old"
	}
	var windows, balances []meter
	for _, m := range meters {
		switch {
		case m.latest == nil:
		case m.res.Currency != "":
			balances = append(balances, m)
		default:
			windows = append(windows, m)
		}
	}
	switch {
	case len(windows) > 0:
		return planCandidate(cand, windows, now), ""
	case len(balances) > 0:
		return prepaidCandidate(cand, balances, now), ""
	}
	for _, p := range accountPkgs {
		if p.Kind == "payg" {
			cand.Class, cand.Reason = ClassPayg, "Pay-as-you-go: no plan on this key, billed per call"
			return cand, ""
		}
	}
	return Candidate{}, "Nothing it draws on was read"
}

func planCandidate(cand Candidate, windows []meter, now domain.Millis) Candidate {
	tightest := math.Inf(1)
	var parts []string
	var lose *meter
	for i, m := range windows {
		p, ok := percentLeft(m)
		if !ok {
			continue
		}
		tightest = math.Min(tightest, p)
		parts = append(parts, name(m)+" "+pct(p)+" left")
		end := m.latest.WindowEnd
		if lose == nil && end != nil && *end > now && *end-now <= ms(loseWithin) && p >= loseAtLeastPct && m.outlook.Kind != OutlookRunsOut {
			lose = &windows[i]
		}
	}
	if math.IsInf(tightest, 1) {
		tightest = 0
	}
	cand.score = tightest
	if lose != nil {
		p, _ := percentLeft(*lose)
		cand.Class = ClassUseBeforeLost
		cand.Reason = "Use before it's lost: " + name(*lose) + " " + pct(p) + " left, resets in " +
			span(time.Duration(*lose.latest.WindowEnd-now)*time.Millisecond)
		return cand
	}
	cand.Class, cand.Reason = ClassPlan, "Plan: "+strings.Join(parts, ", ")
	return cand
}

func prepaidCandidate(cand Candidate, balances []meter, now domain.Millis) Candidate {
	cand.Class, cand.score = ClassPrepaid, math.MaxFloat64
	var parts []string
	for _, m := range balances {
		left, _ := remaining(m)
		parts = append(parts, Money(left, m.res.QuantumExp)+" "+m.res.Currency)
		if m.outlook.Kind == OutlookRunsOut && m.outlook.At != nil {
			lasts := float64(*m.outlook.At - now)
			cand.score = math.Min(cand.score, lasts)
		}
	}
	cand.Reason = "Prepaid: " + strings.Join(parts, " + ") + " left"
	if cand.score != math.MaxFloat64 {
		cand.Reason += ", lasts ~" + span(time.Duration(cand.score)*time.Millisecond) + " at this pace"
	}
	return cand
}

func remaining(m meter) (domain.Units, bool) {
	if m.latest == nil {
		return 0, false
	}
	return remainingOf(*m.latest)
}

func percentLeft(m meter) (float64, bool) {
	left, ok := remaining(m)
	if !ok || m.latest.Limit == nil || *m.latest.Limit <= 0 {
		return 0, false
	}
	return float64(left) / float64(*m.latest.Limit) * 100, true
}

func pct(p float64) string { return Decimal(domain.Units(math.Round(p)), 0) + "%" }

// name is how a meter is called: its window's name, or "Balance".
func name(m meter) string {
	var w struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(m.res.WindowJSON, &w) == nil && w.Name != "" {
		return w.Name
	}
	if m.res.Currency != "" {
		return m.res.Currency + " balance"
	}
	return m.res.TemplateRef
}

func packageUsable(p store.SupplyPackageRecord) (usable, said bool) {
	var d struct {
		Usable *bool `json:"usable"`
	}
	if json.Unmarshal(p.DefinitionJSON, &d) != nil || d.Usable == nil {
		return false, false
	}
	return *d.Usable, true
}

func joinReason(what, detail string) string {
	if detail = strings.TrimSpace(detail); detail == "" {
		return what
	}
	return what + ": " + detail
}
