package supply

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
	"ant/server/internal/domain/usefirst"
	"ant/server/internal/store"
)

// Route preview (design 12 §5): for one model, which accounts could serve
// it, in the order Ant would use them, and which it wouldn't, each with one
// sentence of why. Read-only: nothing is sent to any provider.

// Candidate classes, best first (design 13 §4.1).
const (
	ClassUseBeforeLost = "use_before_lost"
	ClassFree          = "free" // a free grant with room: costs nothing and ends
	ClassPlan          = "plan"
	ClassPrepaid       = "prepaid"
	ClassPayg          = "payg"
)

var classRank = map[string]int{ClassUseBeforeLost: 0, ClassFree: 1, ClassPlan: 2, ClassPrepaid: 3, ClassPayg: 4}

const (
	staleAfter = 30 * time.Minute
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
		resOf := map[domain.ID][]store.SupplyResourceRecord{} // by account
		for _, r := range resources {
			a := pkgOf[r.PackageID].AccountID
			resOf[a] = append(resOf[a], r)
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
			for _, r := range drawn(resOf[c.AccountID], draws, o.ModelID) {
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

// drawn are the account's meters a call to model draws on (design 13
// §4.1), in the order of draws: each ref's meters, and for
// observed.DrawModelScope every meter scoped to the model. A meter scoped
// to another model is never drawn.
func drawn(resources []store.SupplyResourceRecord, draws []string, model string) []store.SupplyResourceRecord {
	var out []store.SupplyResourceRecord
	seen := map[domain.ID]bool{}
	for _, ref := range draws {
		for _, r := range resources {
			scoped := scopeModel(r)
			var ok bool
			if ref == observed.DrawModelScope {
				ok = scoped != "" && observed.ModelID(scoped) == model
			} else {
				ok = r.TemplateRef == ref && (scoped == "" || observed.ModelID(scoped) == model)
			}
			if ok && !seen[r.ResourceID] {
				seen[r.ResourceID] = true
				out = append(out, r)
			}
		}
	}
	return out
}

// scopeModel is the model a meter is scoped to, as the provider names it;
// "" for a meter of the whole account.
func scopeModel(r store.SupplyResourceRecord) string {
	var sc struct {
		Kind  string `json:"kind"`
		Model string `json:"model"`
	}
	if json.Unmarshal(r.ScopeJSON, &sc) != nil || sc.Kind != "model" {
		return ""
	}
	return sc.Model
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
	cand := Candidate{fresh: verifiedAt(c)}
	// Freshness is when Ant last read the account successfully: readings are
	// stored only when a value changes, so a reading's own time says when it
	// last changed, not how current it is (design 12 §5).
	if age := now - verifiedAt(c); age > ms(staleAfter) {
		cand.Stale = "last checked " + span(time.Duration(age)*time.Millisecond) + " ago"
	}
	// Free grants (design 13 §4.1) are used first while they have room, and
	// skipped once used up when the route draws on anything else.
	var grants, open, rest []meter
	for _, m := range meters {
		if m.pkg.Kind != "free_grant" {
			rest = append(rest, m)
			continue
		}
		grants = append(grants, m)
		if left, ok := remaining(m); ok && left > 0 && m.pkg.Basis != "declared" {
			open = append(open, m)
		}
	}
	if len(open) > 0 {
		return freeCandidate(cand, open, now), ""
	}
	if len(grants) > 0 && len(rest) > 0 {
		meters = rest
	}
	// design 12 §5's order holds across all the route's meters: used up,
	// then balance too low to call, then declared only
	for _, m := range meters {
		if left, ok := remaining(m); ok && left <= 0 {
			why := name(m) + " used up"
			if m.pkg.Kind != "free_grant" && m.latest.WindowEnd != nil && *m.latest.WindowEnd > now { // a grant ends, it doesn't reset
				why += " · resets in " + span(time.Duration(*m.latest.WindowEnd-now)*time.Millisecond)
			}
			return Candidate{}, why
		}
	}
	for _, m := range meters {
		if usable, said := packageUsable(m.pkg); said && !usable {
			return Candidate{}, "Balance too low to call"
		}
	}
	for _, m := range meters {
		if m.pkg.Basis == "declared" {
			return Candidate{}, "Only declared, never read"
		}
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
		if lose == nil && usefirst.Today(allowance(m, p), time.UnixMilli(int64(now))) {
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

// freeCandidate describes the grant that ends soonest; among free
// candidates, the one ending soonest comes first. A grant ending within a
// day with at least 30% left is "use before it's lost", as plan windows are.
func freeCandidate(cand Candidate, grants []meter, now domain.Millis) Candidate {
	g := grants[0]
	for _, m := range grants[1:] {
		if ends(m) < ends(g) {
			g = m
		}
	}
	left, _ := remaining(g)
	amount := grouped(count(left, g.res.QuantumExp)) + " " + g.res.Unit
	if p, ok := percentLeft(g); ok && g.res.Unit == "percent" {
		amount = pct(p)
	}
	cand.Class, cand.score = ClassFree, -float64(ends(g))
	cand.Reason = "Free: " + name(g) + " " + amount + " left"
	if end := ends(g); end != math.MaxInt64 {
		cand.Reason += ", ends in " + span(time.Duration(end-now)*time.Millisecond)
		if p, ok := percentLeft(g); ok && usefirst.Today(allowance(g, p), time.UnixMilli(int64(now))) {
			cand.Class = ClassUseBeforeLost
			cand.Reason = "Use before it's lost: " + cand.Reason[len("Free: "):]
		}
	}
	return cand
}

// allowance is what the use-first rule needs to know about a meter.
func allowance(m meter, pctLeft float64) usefirst.Allowance {
	a := usefirst.Allowance{PercentLeft: &pctLeft, RunsOut: m.outlook.Kind == OutlookRunsOut}
	if m.latest != nil && m.latest.WindowEnd != nil {
		a.At = time.UnixMilli(int64(*m.latest.WindowEnd))
	}
	return a
}

// ends is when a meter's allowance ends, or never.
func ends(m meter) domain.Millis {
	if m.latest == nil || m.latest.WindowEnd == nil {
		return math.MaxInt64
	}
	return *m.latest.WindowEnd
}

// count is an amount of something other than money: exact, no trailing
// zeros ("820000.000000" is "820000", "2.50" is "2.5").
func count(u domain.Units, exp int) string {
	s := Decimal(u, exp)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// grouped writes a decimal's whole part in thousands: "820000" is
// "820,000".
func grouped(s string) string {
	whole, frac, hasFrac := strings.Cut(s, ".")
	neg := strings.HasPrefix(whole, "-")
	whole = strings.TrimPrefix(whole, "-")
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	if neg {
		whole = "-" + whole
	}
	if hasFrac {
		return whole + "." + frac
	}
	return whole
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

// name is how a meter is called: its window's name, its currency's
// balance, or the model it's for.
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
	if model := scopeModel(m.res); model != "" {
		return model
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
