package supply

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"ant/server/internal/domain"
)

// Decimal writes units at their quantum exponent exactly: 2340 at −2 is
// "23.40", never through floating point.
func Decimal(u domain.Units, exp int) string {
	neg := u < 0
	if neg {
		u = -u
	}
	s := strconv.FormatInt(int64(u), 10)
	if exp >= 0 {
		s += strings.Repeat("0", exp)
	} else {
		if len(s) <= -exp {
			s = strings.Repeat("0", -exp-len(s)+1) + s
		}
		s = s[:len(s)+exp] + "." + s[len(s)+exp:]
	}
	if neg {
		s = "-" + s
	}
	return s
}

// span is a duration as a person says it: "45m", "3h 20m", "2 days".
func span(d time.Duration) string {
	m := int(d.Round(time.Minute) / time.Minute)
	switch {
	case m < 1:
		return "under a minute"
	case m < 60:
		return fmt.Sprintf("%dm", m)
	case m < 24*60:
		if m%60 == 0 {
			return fmt.Sprintf("%dh", m/60)
		}
		return fmt.Sprintf("%dh %dm", m/60, m%60)
	case m < 48*60:
		return "1 day"
	}
	return fmt.Sprintf("%d days", m/(24*60))
}

// Money is an amount as people write it: exact, without zeros past the
// cents ("23.40", "0.0012", "7.00").
func Money(u domain.Units, exp int) string {
	s := Decimal(u, exp)
	if exp >= -2 {
		if exp == 0 {
			return s + ".00"
		}
		if exp == -1 {
			return s + "0"
		}
		return s
	}
	whole, frac, _ := strings.Cut(s, ".")
	frac = strings.TrimRight(frac, "0")
	for len(frac) < 2 {
		frac += "0"
	}
	return whole + "." + frac
}
