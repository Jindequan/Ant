package deepseek

import (
	"errors"
	"strings"
	"testing"

	"ant/server/internal/domain"
)

func TestParseBalanceObservationPreservesExactCurrenciesAndTime(t *testing.T) {
	body := []byte(`{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"},{"currency":"CNY","total_balance":"3.5","granted_balance":"0.00","topped_up_balance":"3.50"}]}`)
	observation, err := ParseBalanceObservation(body, 1_800_000_000_123)
	if err != nil {
		t.Fatal(err)
	}
	if !observation.IsAvailable || observation.ObservedAt != 1_800_000_000_123 || len(observation.Balances) != 2 {
		t.Fatalf("observation metadata = %+v", observation)
	}
	if observation.Balances[0].Currency != "CNY" || observation.Balances[0].TotalCents != 350 || observation.Balances[0].GrantedCents != 0 || observation.Balances[0].ToppedUpCents != 350 {
		t.Fatalf("CNY balance = %+v", observation.Balances[0])
	}
	if observation.Balances[1].Currency != "USD" || observation.Balances[1].TotalCents != 11_000 || observation.Balances[1].GrantedCents != 1_000 || observation.Balances[1].ToppedUpCents != 10_000 {
		t.Fatalf("USD balance = %+v", observation.Balances[1])
	}
}

func TestParseBalanceObservationPreservesUnavailableState(t *testing.T) {
	observation, err := ParseBalanceObservation([]byte(`{"is_available":false,"balance_infos":[{"currency":"USD","total_balance":"0.00","granted_balance":"0","topped_up_balance":"0.00"}]}`), 99)
	if err != nil || observation.IsAvailable || observation.Balances[0].TotalCents != 0 {
		t.Fatalf("unavailable state not preserved: %+v err=%v", observation, err)
	}
}

func TestParseBalanceObservationRejectsUntrustedOrInconsistentData(t *testing.T) {
	valid := `{"is_available":true,"balance_infos":[{"currency":"USD","total_balance":"1.00","granted_balance":"0.25","topped_up_balance":"0.75"}]}`
	tests := map[string]string{
		"missing availability": `{"balance_infos":[{"currency":"USD","total_balance":"1.00","granted_balance":"0.25","topped_up_balance":"0.75"}]}`,
		"unknown field":        strings.Replace(valid, `"is_available":true`, `"is_available":true,"account_id":"secret"`, 1),
		"duplicate key":        strings.Replace(valid, `"is_available":true`, `"is_available":true,"is_available":true`, 1),
		"trailing value":       valid + ` {}`,
		"unsupported currency": strings.Replace(valid, `"USD"`, `"EUR"`, 1),
		"duplicate currency":   strings.Replace(valid, `}]}`, `},{"currency":"USD","total_balance":"1.00","granted_balance":"0.25","topped_up_balance":"0.75"}]}`, 1),
		"parts do not sum":     strings.Replace(valid, `"topped_up_balance":"0.75"`, `"topped_up_balance":"0.74"`, 1),
		"negative":             strings.Replace(valid, `"granted_balance":"0.25"`, `"granted_balance":"-0.25"`, 1),
		"more than cents":      strings.Replace(valid, `"total_balance":"1.00"`, `"total_balance":"1.001"`, 1),
		"exponent":             strings.Replace(valid, `"total_balance":"1.00"`, `"total_balance":"1e2"`, 1),
		"overflow":             strings.Replace(valid, `"total_balance":"1.00"`, `"total_balance":"92233720368547758.08"`, 1),
	}
	for name, body := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseBalanceObservation([]byte(body), 100); !errors.Is(err, ErrInvalidBalanceResponse) {
				t.Fatalf("ParseBalanceObservation error=%v", err)
			}
		})
	}
	if _, err := ParseBalanceObservation([]byte(valid), -1); !errors.Is(err, ErrInvalidBalanceResponse) {
		t.Fatalf("negative observation time error=%v", err)
	}
	if _, err := ParseBalanceObservation(make([]byte, maxBalanceBytes+1), 100); !errors.Is(err, ErrInvalidBalanceResponse) {
		t.Fatalf("oversized response error=%v", err)
	}
}

func TestParseCurrencyCentsCanonicalValues(t *testing.T) {
	for raw, want := range map[string]domain.Units{"0": 0, "0.0": 0, "0.01": 1, "7.5": 750, "10.00": 1_000} {
		got, err := parseCurrencyCents(raw)
		if err != nil || got != want {
			t.Errorf("parseCurrencyCents(%q) = %d, %v; want %d", raw, got, err, want)
		}
	}
}
