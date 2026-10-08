package resources

import (
	"fmt"
	"time"

	"ant/server/internal/domain"
)

// WindowRuleIdentity is a comparable rule identity, independent of time,
// resource ID and revision. Only fields relevant to Kind/Period are populated.
// It deliberately keeps distinct IANA names except the explicit UTC aliases.
type WindowRuleIdentity struct {
	Kind           string
	Period         string
	TimeZone       string
	PhaseMillis    int64
	CalendarDay    int // Sunday=0..Saturday=6 for week; 1..31 for month
	DurationMillis int64
}

// NormalizeWindowRuleIdentity implements 04-RESOURCE-ALGORITHMS §1.1.
// Fixed phases reuse the executor's anchor interpretation. Invalid rules
// return an error; callers must never compare two failed normalizations.
func NormalizeWindowRuleIdentity(rule domain.WindowRule) (WindowRuleIdentity, error) {
	identity := WindowRuleIdentity{Kind: rule.Kind}
	switch rule.Kind {
	case "lifetime":
		return identity, nil
	case "rolling":
		if rule.DurationMillis <= 0 {
			return WindowRuleIdentity{}, fmt.Errorf("window identity: rolling duration must be positive")
		}
		identity.DurationMillis = rule.DurationMillis
		return identity, nil
	case "fixed":
	default:
		return WindowRuleIdentity{}, fmt.Errorf("window identity: unknown kind %q", rule.Kind)
	}
	switch rule.Period {
	case "minute", "hour", "day", "week", "month":
	default:
		return WindowRuleIdentity{}, fmt.Errorf("window identity: unknown period %q", rule.Period)
	}
	zone := rule.TimeZone
	switch zone {
	case "", "UTC", "Etc/UTC", "Etc/GMT", "GMT":
		zone = "UTC"
	case "Local":
		return WindowRuleIdentity{}, fmt.Errorf("window identity: host-local time zone unsupported")
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return WindowRuleIdentity{}, fmt.Errorf("window identity: invalid time zone %q: %w", zone, err)
	}
	anchor, err := parseAnchor(rule.AnchorISO, loc)
	if err != nil {
		return WindowRuleIdentity{}, err
	}
	identity.Period, identity.TimeZone = rule.Period, zone
	switch rule.Period {
	case "minute":
		identity.PhaseMillis = int64(anchor.clock.second*1000 + anchor.clock.ms)
	case "hour":
		identity.PhaseMillis = int64((anchor.clock.minute*60+anchor.clock.second)*1000 + anchor.clock.ms)
	default:
		identity.PhaseMillis = int64(anchor.clockMillisOfDay())
	}
	switch rule.Period {
	case "week":
		identity.CalendarDay = anchor.weekday
	case "month":
		identity.CalendarDay = anchor.dayOfMonth
	}
	return identity, nil
}
