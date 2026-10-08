package deepseek

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"

	"ant/server/internal/domain"
	"ant/server/internal/jsonx"
)

const (
	BalanceEndpoint = "https://api.deepseek.com/user/balance"
	maxBalanceBytes = 1 << 20
)

var ErrInvalidBalanceResponse = errors.New("deepseek balance: invalid response")

// AccountBalance is one provider-reported currency bucket. Values use integer
// cents and retain the provider's distinction between total, granted, and
// topped-up balance.
type AccountBalance struct {
	Currency      string       `json:"currency"`
	TotalCents    domain.Units `json:"totalCents"`
	GrantedCents  domain.Units `json:"grantedCents"`
	ToppedUpCents domain.Units `json:"toppedUpCents"`
}

// BalanceObservation records the caller's local observation time because the
// provider response does not include a balance watermark or timestamp. It is
// an observation, not a reservation or proof against out-of-band spending.
type BalanceObservation struct {
	IsAvailable bool             `json:"isAvailable"`
	ObservedAt  domain.Millis    `json:"observedAt"`
	Balances    []AccountBalance `json:"balances"`
}

type balanceResponse struct {
	IsAvailable *bool         `json:"is_available"`
	BalanceInfo []balanceInfo `json:"balance_infos"`
}

type balanceInfo struct {
	Currency        string `json:"currency"`
	TotalBalance    string `json:"total_balance"`
	GrantedBalance  string `json:"granted_balance"`
	ToppedUpBalance string `json:"topped_up_balance"`
}

// ParseBalanceObservation validates the official GET /user/balance response
// without performing network I/O. Currency amounts are exact decimal strings;
// no float conversion or FX conversion is used.
func ParseBalanceObservation(data []byte, observedAt domain.Millis) (BalanceObservation, error) {
	if len(data) == 0 || len(data) > maxBalanceBytes || observedAt < 0 {
		return BalanceObservation{}, ErrInvalidBalanceResponse
	}
	var response balanceResponse
	if err := jsonx.DecodeStrict(data, &response); err != nil || response.IsAvailable == nil || len(response.BalanceInfo) == 0 || len(response.BalanceInfo) > 2 {
		return BalanceObservation{}, ErrInvalidBalanceResponse
	}
	balances := make([]AccountBalance, 0, len(response.BalanceInfo))
	seen := map[string]bool{}
	for _, info := range response.BalanceInfo {
		if (info.Currency != "CNY" && info.Currency != "USD") || seen[info.Currency] {
			return BalanceObservation{}, ErrInvalidBalanceResponse
		}
		seen[info.Currency] = true
		total, errTotal := parseCurrencyCents(info.TotalBalance)
		granted, errGranted := parseCurrencyCents(info.GrantedBalance)
		toppedUp, errToppedUp := parseCurrencyCents(info.ToppedUpBalance)
		parts, errParts := domain.AddUnits(granted, toppedUp)
		if errTotal != nil || errGranted != nil || errToppedUp != nil || errParts != nil || parts != total {
			return BalanceObservation{}, ErrInvalidBalanceResponse
		}
		balances = append(balances, AccountBalance{
			Currency: info.Currency, TotalCents: total, GrantedCents: granted, ToppedUpCents: toppedUp,
		})
	}
	sort.Slice(balances, func(i, j int) bool { return balances[i].Currency < balances[j].Currency })
	return BalanceObservation{IsAvailable: *response.IsAvailable, ObservedAt: observedAt, Balances: balances}, nil
}

func parseCurrencyCents(raw string) (domain.Units, error) {
	if raw == "" || strings.HasPrefix(raw, "-") || strings.HasPrefix(raw, "+") {
		return 0, ErrInvalidBalanceResponse
	}
	whole, fractional, hasDecimal := strings.Cut(raw, ".")
	if whole == "" || (len(whole) > 1 && whole[0] == '0') || (hasDecimal && (fractional == "" || len(fractional) > 2)) {
		return 0, ErrInvalidBalanceResponse
	}
	for _, digit := range whole {
		if digit < '0' || digit > '9' {
			return 0, ErrInvalidBalanceResponse
		}
	}
	if hasDecimal {
		for _, digit := range fractional {
			if digit < '0' || digit > '9' {
				return 0, ErrInvalidBalanceResponse
			}
		}
	}
	wholeValue, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || wholeValue > math.MaxInt64/100 {
		return 0, ErrInvalidBalanceResponse
	}
	minor := int64(0)
	if hasDecimal {
		minor, _ = strconv.ParseInt(fractional, 10, 64)
		if len(fractional) == 1 {
			minor *= 10
		}
	}
	base := wholeValue * 100
	if minor > math.MaxInt64-base {
		return 0, ErrInvalidBalanceResponse
	}
	return domain.Units(base + minor), nil
}
