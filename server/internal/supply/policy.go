package supply

import (
	"time"

	"ant/server/internal/domain"
)

// Policy is when the engine reads a credential again (design 11 §3.4).
type Policy struct {
	Default     time.Duration // after a good read
	NearReset   time.Duration // when a window resets within NearWindow
	NearWindow  time.Duration
	Balance     time.Duration // accounts that hold only balances move slowly
	MaxBackoff  time.Duration // errors back off exponentially up to this; signed-out waits this long
	MaxInFlight int           // reads running at once, across credentials
	ReadTimeout time.Duration // one collection
	Rediscover  time.Duration // how often discovery looks for new or vanished sign-ins
}

func DefaultPolicy() Policy {
	return Policy{
		Default:     5 * time.Minute,
		NearReset:   time.Minute,
		NearWindow:  15 * time.Minute,
		Balance:     30 * time.Minute,
		MaxBackoff:  time.Hour,
		MaxInFlight: 8,
		ReadTimeout: 20 * time.Second,
		Rediscover:  5 * time.Minute,
	}
}

func ms(d time.Duration) domain.Millis { return domain.Millis(d.Milliseconds()) }

// NextDue is when a credential is read next, given the result of the read
// that just finished at now and how many reads in a row have failed
// (including this one).
func (p Policy) NextDue(now domain.Millis, r Result, failures int) domain.Millis {
	switch r.Outcome {
	case OutcomeOK:
		if r.NextReset != nil && *r.NextReset > now && *r.NextReset-now <= ms(p.NearWindow) {
			return now + ms(p.NearReset)
		}
		if r.BalanceOnly {
			return now + ms(p.Balance)
		}
		return now + ms(p.Default)
	case OutcomeSignedOut:
		// The user has to act; reading again soon only repeats the refusal.
		return now + ms(p.MaxBackoff)
	default:
		wait := p.Default
		for i := 1; i < failures && wait < p.MaxBackoff; i++ {
			wait *= 2
		}
		return now + ms(min(wait, p.MaxBackoff))
	}
}
