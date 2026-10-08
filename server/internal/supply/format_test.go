package supply

import (
	"testing"
	"time"

	"ant/server/internal/domain"
)

func TestDecimalIsExact(t *testing.T) {
	for in, want := range map[[2]int64]string{{2340, -2}: "23.40", {5, -2}: "0.05", {-1500, -2}: "-15.00", {7, 0}: "7", {3, 2}: "300", {0, -2}: "0.00"} {
		if got := Decimal(domain.Units(in[0]), int(in[1])); got != want {
			t.Errorf("Decimal(%d, %d) = %s, want %s", in[0], in[1], got, want)
		}
	}
}

func TestSpan(t *testing.T) {
	for d, want := range map[time.Duration]string{20 * time.Second: "under a minute", 45 * time.Minute: "45m", 3*time.Hour + 20*time.Minute: "3h 20m", 5 * time.Hour: "5h", 30 * time.Hour: "1 day", 80 * time.Hour: "3 days"} {
		if got := span(d); got != want {
			t.Errorf("span(%v) = %q, want %q", d, got, want)
		}
	}
}
