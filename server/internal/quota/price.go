package quota

import (
	"maps"
	"regexp"
	"strconv"
	"strings"
)

// Price is what the user pays for an account: an amount per month or year,
// as they typed it. Providers rarely report prices, so the user says them.
type Price struct {
	Amount   string `json:"amount"`   // exact decimal, e.g. "20" or "19.99"
	Currency string `json:"currency"` // ISO code, e.g. USD, CNY
	Period   string `json:"period"`   // month | year
}

var (
	amountRE   = regexp.MustCompile(`^\d{1,7}(\.\d{1,2})?$`)
	currencyRE = regexp.MustCompile(`^[A-Z]{3}$`)
)

// normalPrice checks and tidies a typed price.
func normalPrice(p Price) (Price, error) {
	p.Amount = strings.TrimSpace(p.Amount)
	p.Currency = strings.ToUpper(strings.TrimSpace(p.Currency))
	if !amountRE.MatchString(p.Amount) {
		return Price{}, inputError("the price must be a number like 20 or 19.99")
	}
	if f, _ := strconv.ParseFloat(p.Amount, 64); f <= 0 {
		return Price{}, inputError("the price must be more than zero")
	}
	if !currencyRE.MatchString(p.Currency) {
		return Price{}, inputError("choose a currency")
	}
	if p.Period != "month" && p.Period != "year" {
		return Price{}, inputError("choose per month or per year")
	}
	return p, nil
}

// SetPrice records what the user pays for card id; nil forgets it.
func (s *Store) SetPrice(id string, p *Price) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load()
	if err != nil {
		return err
	}
	if p == nil {
		delete(d.Prices, id)
	} else {
		if d.Prices == nil {
			d.Prices = map[string]Price{}
		}
		d.Prices[id] = *p
	}
	return s.save(d)
}

// Prices are every recorded price, by card id.
func (s *Store) Prices() map[string]Price {
	d, err := s.Data()
	if err != nil || d.Prices == nil {
		return map[string]Price{}
	}
	return maps.Clone(d.Prices)
}
