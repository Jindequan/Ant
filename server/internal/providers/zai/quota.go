// Package zai is the Z.ai provider module (design 12): the GLM Coding
// Plan's windows read through the plan's own quota endpoint with its API
// key, and the models its coding endpoint serves.
package zai

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrKeyRefused: the endpoint refused the key. Z.ai says so in the body of
// an HTTP 200 — {"code":401,"msg":"token expired or incorrect","success":false}
// (observed 2026-10-08) — not with an HTTP status.
var ErrKeyRefused = errors.New("key refused")

// Limit is one allowance of a coding plan as the quota endpoint reports it.
type Limit struct {
	Kind        string   // hours | week | mcp | other
	Hours       int      // for hours: the window's length (5)
	UsedPercent *float64 // nil when not reported
	NextReset   int64    // unix ms; 0 when not reported
}

// Quota is a coding plan's level and allowances; no limits means the key
// has no plan.
type Quota struct {
	Level  string // "Pro", "Lite", … (capitalized)
	Limits []Limit
}

// ParseQuota reads
//
//	{"success":true,"data":{"level":"pro","limits":[
//	  {"type":"TOKENS_LIMIT","unit":3,"number":5,"percentage":12,"nextResetTime":1758800000000},
//	  {"type":"TOKENS_LIMIT","unit":6,"number":1,"percentage":40,"nextResetTime":…},
//	  {"type":"TIME_LIMIT","unit":5,"number":1,"percentage":3,"nextResetTime":…}]}}
//
// unit 3 is hours, unit 6 the week; TIME_LIMIT is the month's MCP tool
// calls, which don't stop the models (shape as read by yetone/magpie).
// BigModel (open.bigmodel.cn) answers in the same shape.
func ParseQuota(b []byte) (Quota, error) {
	var r struct {
		Code    int    `json:"code"`
		Success *bool  `json:"success"`
		Msg     string `json:"msg"`
		Data    *struct {
			Level  string `json:"level"`
			Limits []struct {
				Type          string   `json:"type"`
				Unit          int      `json:"unit"`
				Number        int      `json:"number"`
				Percentage    *float64 `json:"percentage"`
				NextResetTime int64    `json:"nextResetTime"`
			} `json:"limits"`
		} `json:"data"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return Quota{}, errors.New("Z.ai answered something Ant doesn't understand")
	}
	if r.Code == 401 || r.Code == 403 {
		return Quota{}, ErrKeyRefused
	}
	if (r.Success != nil && !*r.Success) || r.Data == nil {
		if r.Msg != "" {
			return Quota{}, fmt.Errorf("Z.ai: %s", r.Msg)
		}
		return Quota{}, errors.New("Z.ai returned no plan")
	}
	q := Quota{Level: capitalize(r.Data.Level), Limits: []Limit{}}
	for _, lim := range r.Data.Limits {
		l := Limit{UsedPercent: lim.Percentage, NextReset: max(lim.NextResetTime, 0)}
		switch {
		case strings.EqualFold(lim.Type, "TIME_LIMIT"):
			l.Kind = "mcp"
		case lim.Unit == 3:
			l.Kind, l.Hours = "hours", lim.Number
			if l.Hours <= 0 {
				l.Hours = 5
			}
		case lim.Unit == 6:
			l.Kind = "week"
		default:
			l.Kind = "other"
		}
		q.Limits = append(q.Limits, l)
	}
	return q, nil
}

// Name is how a limit is shown.
func (l Limit) Name() string {
	switch l.Kind {
	case "mcp":
		return "MCP tools · month"
	case "hours":
		return fmt.Sprintf("%d hours", l.Hours)
	case "week":
		return "Week"
	}
	return "Allowance"
}

func capitalize(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
