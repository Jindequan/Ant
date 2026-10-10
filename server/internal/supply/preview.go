package supply

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"slices"
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
	CredentialID    domain.ID
	ProviderID      domain.ID
	OfferID         domain.ID
	OfferVerifiedAt domain.Millis
	Endpoint        string
	UpstreamModel   string
}

// RouteEvidence pins a preview to the immutable observation rows it used.
// A blank ReadingID means the resource was known but had no observation.
type RouteEvidence struct {
	ResourceID domain.ID     `json:"resourceId"`
	ReadingID  domain.ID     `json:"readingId,omitempty"`
	ObservedAt domain.Millis `json:"observedAt,omitempty"`
}

type Candidate struct {
	Route
	Class    string
	Reason   string
	Stale    string // "reading is 2h old", or ""
	Evidence []RouteEvidence
	score    float64
	fresh    domain.Millis
}

type Excluded struct {
	Route
	Reason   string
	Evidence []RouteEvidence
}

type Preview struct {
	Model         string
	OrderVersion  int64
	ManualOrder   bool
	OrderOfferIDs []domain.ID // persisted preference, including currently dormant offers
	Eligible      []Candidate // eligible first, ordered by explicit preference or default ranking
	Excluded      []Excluded
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
	model = observed.ModelID(model)
	var out Preview
	err := e.st.Read(ctx, func(rx store.ReaderTx) error {
		sx, ok := rx.(store.SupplyRoutePreviewTx)
		if !ok {
			return errNoSupplyStore
		}
		var previewErr error
		out, previewErr = e.previewFrom(sx, model, e.clock.Now())
		return previewErr
	})
	return out, err
}

// previewFrom evaluates the route against the supplied snapshot. The same
// calculation runs on read snapshots for previews and inside the writer
// transaction before persisting a manual order.
func (e *Engine) previewFrom(sx store.SupplyRoutePreviewTx, model string, now domain.Millis) (Preview, error) {
	out := Preview{Model: model}
	order, _, err := sx.GetSupplyRouteOrder(e.ns, model)
	if err != nil {
		return out, err
	}
	out.OrderVersion = order.Revision
	out.ManualOrder = len(order.OfferIDs) > 0
	out.OrderOfferIDs = slices.Clone(order.OfferIDs)
	offers, err := sx.ListSupplyOffers(e.ns, model)
	if err != nil || len(offers) == 0 {
		return out, err
	}
	creds, err := sx.ListSupplyCredentials(e.ns)
	if err != nil {
		return out, err
	}
	pkgs, err := sx.ListSupplyPackages(e.ns)
	if err != nil {
		return out, err
	}
	resources, err := sx.ListSupplyResources(e.ns)
	if err != nil {
		return out, err
	}
	manualRank := make(map[domain.ID]int, len(order.OfferIDs))
	for i, id := range order.OfferIDs {
		if _, exists := manualRank[id]; !exists {
			manualRank[id] = i
		}
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
		route := Route{CredentialID: c.CredentialID, ProviderID: c.ProviderID, OfferID: o.OfferID, OfferVerifiedAt: o.VerifiedAt,
			Endpoint: o.Endpoint, UpstreamModel: o.UpstreamModel}
		var draws []string
		_ = json.Unmarshal(o.DrawsJSON, &draws)
		routeResources := drawn(resOf[c.AccountID], draws, o.ModelID)
		constraintResources, missingConstraints := routeConstraints(pkgsOfAccount[c.AccountID], resOf[c.AccountID], o.ModelID)
		resources := uniqueResources(append(slices.Clone(routeResources), constraintResources...))
		byResource := make(map[domain.ID]meter, len(resources))
		for _, r := range resources {
			m := meter{res: r, pkg: pkgOf[r.PackageID]}
			readings, err := sx.ListSupplyReadings(e.ns, r.ResourceID, now-ms(balanceLookback))
			if err != nil {
				return out, err
			}
			if len(readings) == 0 {
				latest, found, err := sx.LatestSupplyReading(e.ns, r.ResourceID)
				if err != nil {
					return out, err
				}
				if found {
					readings = append(readings, latest)
				}
			}
			if len(readings) > 0 {
				m.latest = &readings[len(readings)-1]
				m.outlook = Forecast(readings, verifiedAt(c))
			}
			byResource[r.ResourceID] = m
		}
		meters := make([]meter, 0, len(routeResources))
		for _, r := range routeResources {
			meters = append(meters, byResource[r.ResourceID])
		}
		constraints := make([]meter, 0, len(constraintResources))
		for _, r := range constraintResources {
			constraints = append(constraints, byResource[r.ResourceID])
		}
		evidence := routeEvidence(append(slices.Clone(routeResources), constraintResources...), byResource)
		cand, why := judge(c, meters, constraints, missingConstraints, pkgsOfAccount[c.AccountID], now)
		if why != "" {
			out.Excluded = append(out.Excluded, Excluded{Route: route, Reason: why, Evidence: evidence})
			continue
		}
		cand.Route = route
		cand.Evidence = evidence
		out.Eligible = append(out.Eligible, cand)
	}
	sort.SliceStable(out.Eligible, func(i, j int) bool {
		a, b := out.Eligible[i], out.Eligible[j]
		ai, aManual := manualRank[a.OfferID]
		bi, bManual := manualRank[b.OfferID]
		if aManual != bManual {
			return aManual
		}
		if aManual && ai != bi {
			return ai < bi
		}
		return defaultCandidateLess(a, b)
	})
	return out, nil
}

func defaultCandidateLess(a, b Candidate) bool {
	if classRank[a.Class] != classRank[b.Class] {
		return classRank[a.Class] < classRank[b.Class]
	}
	if a.score != b.score {
		return a.score > b.score
	}
	if a.fresh != b.fresh {
		return a.fresh > b.fresh
	}
	return a.OfferID < b.OfferID
}

// SetRouteOrder stores a complete order for the currently eligible offer set
// of one canonical model. An empty order clears the preference and restores
// Ant's default ranking. Non-empty orders revalidate eligibility inside the
// same write transaction that commits the preference.
func (e *Engine) SetRouteOrder(ctx context.Context, model string, offerIDs []domain.ID, expectedVersion int64) (int64, error) {
	model = observed.ModelID(model)
	if model == "" || expectedVersion < 0 {
		return 0, store.ErrSupplyRouteOrderInvalid
	}
	p, err := e.Preview(ctx, model)
	if err != nil {
		return 0, err
	}
	if p.OrderVersion != expectedVersion {
		return p.OrderVersion, store.ErrSupplyRouteOrderConflict
	}
	eligible := make(map[domain.ID]bool, len(p.Eligible))
	for _, c := range p.Eligible {
		eligible[c.OfferID] = true
	}
	reset := len(offerIDs) == 0
	if !reset {
		seen := make(map[domain.ID]bool, len(offerIDs))
		for _, id := range offerIDs {
			if id == "" || seen[id] {
				return p.OrderVersion, store.ErrSupplyRouteOrderInvalid
			}
			if !eligible[id] {
				return p.OrderVersion, store.ErrSupplyCandidatesChanged
			}
			seen[id] = true
		}
		if len(seen) != len(eligible) {
			return p.OrderVersion, store.ErrSupplyCandidatesChanged
		}
	}
	var next int64
	err = e.write(ctx, "route-order-"+string(newID("ord-")), func(tx store.SupplyWriterTx) error {
		prior, _, err := tx.GetSupplyRouteOrder(e.ns, model)
		if err != nil {
			return err
		}
		if prior.Revision != expectedVersion {
			return store.ErrSupplyRouteOrderConflict
		}
		merged := []domain.ID{}
		if !reset {
			// The transaction has the SQLite writer lock. Re-run qualification
			// against its own snapshot so pause, refresh, removal and a crossed
			// time boundary cannot slip between validation and commit.
			current, err := e.previewFrom(tx, model, e.clock.Now())
			if err != nil {
				return err
			}
			if current.OrderVersion != expectedVersion {
				return store.ErrSupplyRouteOrderConflict
			}
			if !sameCandidateSet(eligible, current.Eligible) {
				return store.ErrSupplyCandidatesChanged
			}
			merged = slices.Clone(offerIDs)
			for _, id := range prior.OfferIDs {
				if !eligible[id] {
					merged = append(merged, id)
				}
			}
		}
		next, err = tx.ReplaceSupplyRouteOrder(e.ns, model, merged, expectedVersion, e.clock.Now())
		return err
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return next, errors.Join(ErrWriteOutcomeUnknown, err)
		}
		return next, err
	}
	return next, nil
}

func sameCandidateSet(expected map[domain.ID]bool, current []Candidate) bool {
	if len(expected) != len(current) {
		return false
	}
	for _, c := range current {
		if !expected[c.OfferID] {
			return false
		}
	}
	return true
}

func routeEvidence(resources []store.SupplyResourceRecord, meters map[domain.ID]meter) []RouteEvidence {
	resources = uniqueResources(resources)
	out := make([]RouteEvidence, 0, len(resources))
	for _, r := range resources {
		fact := RouteEvidence{ResourceID: r.ResourceID}
		if m, ok := meters[r.ResourceID]; ok && m.latest != nil {
			fact.ReadingID, fact.ObservedAt = m.latest.ReadingID, m.latest.ObservedAt
		}
		out = append(out, fact)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ResourceID < out[j].ResourceID })
	return out
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

// routeConstraints are provider-declared hard guards, separate from the
// resources the offer may spend. A free grant can replace a payment source;
// it cannot override an account-level window/cap the provider says applies
// to every call.
func routeConstraints(packages []store.SupplyPackageRecord, resources []store.SupplyResourceRecord, model string) ([]store.SupplyResourceRecord, []string) {
	var found []store.SupplyResourceRecord
	var missing []string
	for _, p := range packages {
		var definition struct {
			RouteConstraints []string `json:"routeConstraints"`
		}
		if json.Unmarshal(p.DefinitionJSON, &definition) != nil {
			continue
		}
		packageResources := make([]store.SupplyResourceRecord, 0)
		for _, r := range resources {
			if r.PackageID == p.PackageID {
				packageResources = append(packageResources, r)
			}
		}
		for _, ref := range definition.RouteConstraints {
			matched := drawn(packageResources, []string{ref}, model)
			if len(matched) == 0 {
				missing = append(missing, ref)
				continue
			}
			found = append(found, matched...)
		}
	}
	return uniqueResources(found), missing
}

func uniqueResources(resources []store.SupplyResourceRecord) []store.SupplyResourceRecord {
	seen := make(map[domain.ID]bool, len(resources))
	out := make([]store.SupplyResourceRecord, 0, len(resources))
	for _, r := range resources {
		if !seen[r.ResourceID] {
			seen[r.ResourceID] = true
			out = append(out, r)
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

func inactivePackage(p store.SupplyPackageRecord, now domain.Millis) string {
	if p.Basis != "observed" {
		return "Only declared, never read"
	}
	if !p.Live {
		return "Package isn't active"
	}
	if p.StartsAt != nil && *p.StartsAt > now {
		return "Package starts in " + span(time.Duration(*p.StartsAt-now)*time.Millisecond)
	}
	if p.EndsAt != nil && *p.EndsAt <= now {
		return "Package has ended"
	}
	return ""
}

func inactiveReading(m meter, now domain.Millis) string {
	if m.latest == nil {
		return ""
	}
	if m.latest.ObservedAt > now {
		return name(m) + " has a future reading; refresh before routing"
	}
	if m.latest.WindowStart != nil && *m.latest.WindowStart > now {
		return name(m) + " window starts in " + span(time.Duration(*m.latest.WindowStart-now)*time.Millisecond) + "; refresh before routing"
	}
	if m.latest.WindowEnd != nil && *m.latest.WindowEnd <= now {
		return name(m) + " reading expired; refresh before routing"
	}
	return ""
}

// judge decides one route: a candidate, or why not (design 12 §5, in order).
func judge(c store.SupplyCredentialRecord, meters, constraints []meter, missingConstraints []string, accountPkgs []store.SupplyPackageRecord, now domain.Millis) (Candidate, string) {
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
	// A provider-marked hard constraint applies regardless of which package
	// pays for the call. Missing, inactive, expired, or used-up facts are a
	// closed gate. In particular, a free grant never bypasses this check.
	if len(missingConstraints) > 0 {
		return Candidate{}, "Required quota " + missingConstraints[0] + " wasn't observed"
	}
	for _, m := range constraints {
		if reason := inactivePackage(m.pkg, now); reason != "" {
			return Candidate{}, reason
		}
		if m.latest == nil {
			return Candidate{}, name(m) + " wasn't observed"
		}
		if reason := inactiveReading(m, now); reason != "" {
			return Candidate{}, reason
		}
		if left, ok := remaining(m); !ok {
			return Candidate{}, name(m) + " quota is unknown; refresh before routing"
		} else if left <= 0 {
			return Candidate{}, name(m) + " used up"
		}
		if usable, said := packageUsable(m.pkg); said && !usable {
			return Candidate{}, "Provider says this package can't serve calls now"
		}
	}
	// Free grants (design 13 §4.1) are used first while they have room, and
	// skipped once used up when the route has another payment path. They are
	// filtered by the grant's own dates and reported window end.
	var availableGrants, open, rest []meter
	var inactiveReason string
	for _, m := range meters {
		if m.pkg.Kind != "free_grant" {
			if reason := inactivePackage(m.pkg, now); reason != "" {
				if inactiveReason == "" {
					inactiveReason = reason
				}
				continue
			}
			if reason := inactiveReading(m, now); reason != "" {
				if inactiveReason == "" {
					inactiveReason = reason
				}
				continue
			}
			rest = append(rest, m)
			continue
		}
		if reason := inactivePackage(m.pkg, now); reason != "" {
			if inactiveReason == "" {
				inactiveReason = reason
			}
			continue
		}
		if m.latest == nil {
			if inactiveReason == "" {
				inactiveReason = name(m) + " quota wasn't observed"
			}
			continue
		}
		if reason := inactiveReading(m, now); reason != "" {
			if inactiveReason == "" {
				inactiveReason = reason
			}
			continue
		}
		availableGrants = append(availableGrants, m)
		if left, ok := remaining(m); ok && left > 0 && m.pkg.Basis == "observed" {
			open = append(open, m)
		}
	}
	if len(open) > 0 {
		return freeCandidate(cand, open, now), ""
	}
	if len(availableGrants) > 0 && len(rest) == 0 {
		meters = availableGrants
	} else {
		meters = rest
	}
	if len(meters) == 0 && inactiveReason != "" {
		return Candidate{}, inactiveReason
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
	var paygInactive string
	for _, p := range accountPkgs {
		if p.Kind == "payg" {
			if reason := inactivePackage(p, now); reason != "" {
				if paygInactive == "" {
					paygInactive = reason
				}
				continue
			}
			cand.Class, cand.Reason = ClassPayg, "Pay-as-you-go: no plan on this key, billed per call"
			return cand, ""
		}
	}
	if paygInactive != "" {
		return Candidate{}, paygInactive
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
