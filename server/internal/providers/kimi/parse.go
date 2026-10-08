// Package kimi is the Kimi provider module (design 13 phase A): Kimi Code,
// a subscription for coding tools whose windows are read from its usages
// endpoint, and the Moonshot (Kimi) open platform, a prepaid balance read
// from the official balance API in its China (CNY) or international (USD)
// region.
package kimi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"ant/server/internal/providers/catalog"
)

// Window is one Kimi Code allowance.
type Window struct {
	Name        string  // "5 hours", "Week"
	Short       bool    // the rolling window under a day (limits[]); false for the week (usage)
	UsedPercent float64 // 0–100
	ResetsAt    int64   // unix ms; 0 when not reported
	Limit, Used string  // as reported, for evidence
}

// ParseUsages reads Kimi Code's GET /coding/v1/usages (observed, as kimi-cli
// reads it; not in the official API reference):
//
//	{"usage":{"limit":"100","used":"12","resetTime":"…"},
//	 "limits":[{"window":{"duration":300,"timeUnit":"TIME_UNIT_MINUTE"},
//	            "detail":{"limit":"20","remaining":"18","resetTime":"…"}}]}
//
// usage is the week; each limit a shorter window. Numbers may be strings.
func ParseUsages(b []byte) ([]Window, error) {
	type row map[string]any
	var r struct {
		Usage  row `json:"usage"`
		Limits []struct {
			Window row `json:"window"`
			Detail row `json:"detail"`
		} `json:"limits"`
	}
	if json.Unmarshal(b, &r) != nil {
		return nil, errors.New("Kimi answered something Ant doesn't understand")
	}
	var out []Window
	for _, lim := range r.Limits {
		d := lim.Detail
		if d == nil {
			d = lim.Window
		}
		n, _ := number(lim.Window["duration"])
		unit, _ := lim.Window["timeUnit"].(string)
		var span time.Duration
		switch {
		case strings.Contains(unit, "MINUTE"):
			span = time.Duration(n) * time.Minute
		case strings.Contains(unit, "HOUR"):
			span = time.Duration(n) * time.Hour
		case strings.Contains(unit, "DAY"):
			span = time.Duration(n) * 24 * time.Hour
		}
		if w, ok := window(d, catalog.WindowName(span)); ok {
			w.Short = true
			out = append(out, w)
		}
	}
	if w, ok := window(r.Usage, "Week"); ok {
		out = append(out, w)
	}
	if len(out) == 0 {
		return nil, errors.New("Kimi reported no usage windows for this key")
	}
	return out, nil
}

func window(d map[string]any, name string) (Window, bool) {
	limit, ok := number(d["limit"])
	if !ok || limit <= 0 {
		return Window{}, false
	}
	used, ok := number(d["used"])
	if !ok {
		left, ok := number(d["remaining"])
		if !ok {
			return Window{}, false
		}
		used = limit - left
	}
	w := Window{Name: name, UsedPercent: max(0, min(100, used/limit*100)),
		Limit: strconv.FormatFloat(limit, 'f', -1, 64), Used: strconv.FormatFloat(used, 'f', -1, 64)}
	for _, k := range []string{"resetTime", "resetAt", "reset_at", "reset_time"} {
		if s, _ := d[k].(string); s != "" {
			if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
				w.ResetsAt = t.UnixMilli()
				break
			}
		}
	}
	return w, true
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
}

// Balance is the Moonshot platform balance, as exact decimal strings in the
// region's currency (CNY on platform.kimi.com, USD on platform.kimi.ai).
// Available is cash plus vouchers; cash below zero is arrears, and then
// available equals the voucher balance. Available ≤ 0 means calls fail.
type Balance struct {
	Available, Voucher, Cash string
}

// ParseBalance reads the official GET /v1/users/me/balance reply, keeping
// every digit (the platform reports five decimals): numbers are read as
// their JSON text, never as floating point.
func ParseBalance(b []byte) (Balance, error) {
	var r struct {
		Code   *int  `json:"code"`
		Status *bool `json:"status"`
		Data   *struct {
			Available json.Number `json:"available_balance"`
			Voucher   json.Number `json:"voucher_balance"`
			Cash      json.Number `json:"cash_balance"`
		} `json:"data"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if dec.Decode(&r) != nil {
		return Balance{}, errors.New("Moonshot answered something Ant doesn't understand")
	}
	if r.Error != nil && r.Error.Message != "" {
		return Balance{}, fmt.Errorf("Moonshot: %s", r.Error.Message)
	}
	if (r.Code != nil && *r.Code != 0) || (r.Status != nil && !*r.Status) || r.Data == nil {
		return Balance{}, errors.New("Moonshot didn't return a balance")
	}
	out := Balance{Available: r.Data.Available.String(), Voucher: r.Data.Voucher.String(), Cash: r.Data.Cash.String()}
	for _, v := range []string{out.Available, out.Voucher, out.Cash} {
		if _, ok := catalog.ParseDecimal(v); !ok {
			return Balance{}, errors.New("Moonshot answered an amount Ant can't read exactly")
		}
	}
	return out, nil
}

// Money writes an exact amount the way people write money: at least two
// decimals, every digit the platform gave kept ("3" is "3.00", "49.58894"
// stays).
func Money(s string) string {
	whole, frac, _ := strings.Cut(s, ".")
	for len(frac) < 2 {
		frac += "0"
	}
	return whole + "." + frac
}
