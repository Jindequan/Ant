package supply

import (
	"context"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/store"
)

// Outlook says how a meter is going at the pace of its recent readings
// (design 11 phase 6: burn rate and "lasts until").
type Outlook struct {
	Kind   string         // runs_out | lasts | idle | "" (not enough history yet)
	At     *domain.Millis // runs_out: when it runs out at this pace
	PerDay *domain.Units  // spent per day, in the meter's units
}

const (
	OutlookRunsOut = "runs_out" // empty before it resets (windows) or at At (balances)
	OutlookLasts   = "lasts"    // a window that lasts until it resets
	OutlookIdle    = "idle"     // nothing spent in the period looked at
)

const (
	windowMinSpan   = 15 * time.Minute // shorter than this says nothing about a pace
	balanceMinSpan  = time.Hour
	balanceLookback = 7 * 24 * time.Hour
	maxPoints       = 48
)

// Forecast reads one meter's readings, oldest first, whose value Ant last
// confirmed at asOf. Readings are appended only on change, so the value is
// taken as unchanged from the last reading until asOf. A windowed meter is
// paced within its current window; a balance over the readings given
// (the last week), counting only decreases, so top-ups are not spending.
func Forecast(readings []store.SupplyReadingRecord, asOf domain.Millis) Outlook {
	if len(readings) == 0 {
		return Outlook{}
	}
	latest := readings[len(readings)-1]
	remaining, ok := remainingOf(latest)
	if !ok || remaining <= 0 {
		return Outlook{} // unknown, or already used up: the card says so
	}
	asOf = max(asOf, latest.ObservedAt)
	period, minSpan := readings, balanceMinSpan
	if latest.WindowEnd != nil {
		minSpan = windowMinSpan
		start := len(readings) - 1
		for start > 0 && sameEnd(readings[start-1].WindowEnd, latest.WindowEnd) {
			start--
		}
		period = readings[start:]
	}
	span := asOf - period[0].ObservedAt
	if span < ms(minSpan) {
		return Outlook{}
	}
	var spent domain.Units
	for i := 1; i < len(period); i++ {
		before, ok1 := remainingOf(period[i-1])
		after, ok2 := remainingOf(period[i])
		if ok1 && ok2 && after < before {
			spent += before - after
		}
	}
	if spent == 0 {
		return Outlook{Kind: OutlookIdle}
	}
	perMs := float64(spent) / float64(span)
	perDay := domain.Units(perMs * float64(24*time.Hour/time.Millisecond))
	out := asOf + domain.Millis(float64(remaining)/perMs)
	if latest.WindowEnd != nil && out >= *latest.WindowEnd {
		return Outlook{Kind: OutlookLasts, PerDay: &perDay}
	}
	return Outlook{Kind: OutlookRunsOut, At: &out, PerDay: &perDay}
}

func remainingOf(r store.SupplyReadingRecord) (domain.Units, bool) {
	switch {
	case r.Remaining != nil:
		return *r.Remaining, true
	case r.Limit != nil && r.Used != nil:
		return *r.Limit - *r.Used, true
	}
	return 0, false
}

func sameEnd(a, b *domain.Millis) bool { return a != nil && b != nil && *a == *b }

// MeterOutlook is one meter with its latest reading, recent values and
// outlook.
type MeterOutlook struct {
	Resource store.SupplyResourceRecord
	Latest   store.SupplyReadingRecord
	Points   []store.SupplyReadingRecord // at most maxPoints, oldest first, ending with Latest
	Outlook  Outlook
}

// Outlooks is every recorded credential's meters and their outlook, by
// credential id: credentials of one account share its meters.
func (e *Engine) Outlooks(ctx context.Context) (map[domain.ID][]MeterOutlook, error) {
	now := e.clock.Now()
	out := map[domain.ID][]MeterOutlook{}
	err := e.st.Read(ctx, func(rx store.ReaderTx) error {
		sx, ok := rx.(store.SupplyReaderTx)
		if !ok {
			return errNoSupplyStore
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
		accountOf := map[domain.ID]domain.ID{}
		for _, p := range pkgs {
			accountOf[p.PackageID] = p.AccountID
		}
		confirmed := map[domain.ID]domain.Millis{} // per account: the last good read of any of its credentials
		for _, c := range creds {
			if c.AccountID != "" && c.VerifiedAt != nil && *c.VerifiedAt > confirmed[c.AccountID] {
				confirmed[c.AccountID] = *c.VerifiedAt
			}
		}
		byAccount := map[domain.ID][]MeterOutlook{}
		for _, r := range resources {
			account := accountOf[r.PackageID]
			readings, err := sx.ListSupplyReadings(e.ns, r.ResourceID, now-ms(balanceLookback))
			if err != nil {
				return err
			}
			if len(readings) == 0 {
				latest, found, err := sx.LatestSupplyReading(e.ns, r.ResourceID)
				if err != nil {
					return err
				}
				if !found {
					continue // declared, or never read
				}
				readings = append(readings, latest)
			}
			asOf := confirmed[account]
			byAccount[account] = append(byAccount[account], MeterOutlook{Resource: r, Latest: readings[len(readings)-1],
				Points: thin(readings), Outlook: Forecast(readings, asOf)})
		}
		for _, c := range creds {
			if m := byAccount[c.AccountID]; c.AccountID != "" && len(m) > 0 {
				out[c.CredentialID] = m
			}
		}
		return nil
	})
	return out, err
}

// thin keeps at most maxPoints readings, evenly, always the first and last.
func thin(rs []store.SupplyReadingRecord) []store.SupplyReadingRecord {
	if len(rs) <= maxPoints {
		return rs
	}
	out := make([]store.SupplyReadingRecord, 0, maxPoints)
	for i := range maxPoints {
		out = append(out, rs[i*(len(rs)-1)/(maxPoints-1)])
	}
	return out
}
