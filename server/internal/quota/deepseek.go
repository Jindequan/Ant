package quota

import (
	"context"
	"fmt"
	"net/http"

	"ant/server/internal/domain"
	"ant/server/internal/providers/deepseek"
)

// DeepSeek is pay-as-you-go: a prepaid balance, read from the official
// GET /user/balance with the API key.

func deepSeekProvider() Provider {
	return Provider{ID: "deepseek", Name: "DeepSeek", Link: "https://platform.deepseek.com/usage", Modes: []Mode{{
		ID: "api", Name: "API balance", Kind: KindAPI, Live: true, Key: "apiKey",
		Rule: "Prepaid balance, spent per token. Granted balance is used before topped-up balance. It doesn't expire or reset.",
		Fields: []Field{
			{ID: "apiKey", Label: "API key", Type: "secret", Placeholder: "sk-…", Help: "From platform.deepseek.com › API keys."},
		},
		Discover: discoverDeepSeek,
		Read:     readDeepSeek,
	}}}
}

func deepSeekConnection(key, source string) Connection {
	return Connection{ID: fingerprint("deepseek", "api", key), Provider: "deepseek", Mode: "api",
		Origin: OriginFound, Source: source, Values: map[string]string{"apiKey": key}}
}

func discoverDeepSeek(l Local) []Connection {
	var out []Connection
	for _, k := range l.zcodeKeys() {
		if hostOf(k.BaseURL) == "api.deepseek.com" {
			out = append(out, deepSeekConnection(k.Key, "ZCode"))
		}
	}
	if v, n := l.env("DEEPSEEK_API_KEY"); v != "" {
		out = append(out, deepSeekConnection(v, "$"+n))
	}
	return out
}

func readDeepSeek(ctx context.Context, l Local, c Connection) Account {
	p := deepSeekProvider()
	now := l.Now()
	a := base(p, p.Modes[0], c, now)
	b, err := l.fetch(ctx, http.MethodGet, l.url("deepseek.balance", deepseek.BalanceEndpoint), map[string]string{"Authorization": "Bearer " + c.Values["apiKey"]}, nil)
	if err != nil {
		a.Status, a.Message = StatusError, "Couldn't read the balance: "+short(err.Error())
		if isAuthError(err) {
			a.Status, a.Message = StatusSignedOut, "DeepSeek refused the key. Check it in your API keys."
		}
		return a
	}
	obs, err := deepseek.ParseBalanceObservation(b, domain.Millis(now.UnixMilli()))
	if err != nil {
		a.Status, a.Message = StatusError, "DeepSeek answered something Ant doesn't understand."
		return a
	}
	a.Status = StatusOK
	for _, bal := range obs.Balances {
		a.Balances = append(a.Balances, Balance{
			Currency: bal.Currency, Amount: cents(bal.TotalCents),
			Granted: cents(bal.GrantedCents), ToppedUp: cents(bal.ToppedUpCents),
		})
	}
	if !obs.IsAvailable {
		a.Message = "Balance is too low for API calls. Top up to keep using it."
	}
	keyModels(ctx, &a, l, c.Values["apiKey"], l.url("deepseek.models", "https://api.deepseek.com/models"))
	return a
}

func cents(u domain.Units) string { return fmt.Sprintf("%d.%02d", u/100, u%100) }
