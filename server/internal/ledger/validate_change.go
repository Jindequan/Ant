package ledger

import (
	"ant/server/internal/domain"
	"ant/server/internal/domain/resources"
	"fmt"
)

// ValidatePreparationChange is the LED-02 mutation gate. Preparation cannot
// settle usage, adjust capacity, erase uncertainty or resurrect terminal work.
// I08/I10 settlement/reconciliation paths remain LED-03/04, not this command.
func ValidatePreparationChange(s domain.ResourceSnapshot, d domain.ResourceDelta, q domain.Units, stage string) error {
	if q < 0 || d.Committed != 0 || d.Adjustment != 0 || d.Uncertain != 0 {
		return fmt.Errorf("ledger: forbidden preparation accounting change")
	}
	switch stage {
	case "reserve":
		if d.Reserved != q || d.Active != 0 {
			return fmt.Errorf("ledger: invalid reservation")
		}
	case "activate":
		if s.Kind != "occupancy" || d.Reserved != -q || d.Active != q {
			return fmt.Errorf("ledger: invalid occupancy activation")
		}
	case "window_release":
		if s.Kind != "counter" || d.Reserved != -q || d.Active != 0 {
			return fmt.Errorf("ledger: invalid window migration")
		}
	default:
		return fmt.Errorf("ledger: unknown preparation transition")
	}
	return resources.ValidateChange(s, d)
}
