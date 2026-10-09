package aliyun

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// The Token Plan replies, as the console's calls answer them (2026-10-07,
// docs/research/ALIYUN-BAILIAN.md). Numbers are kept as their JSON text.

// Subscription is /tokenplan/personal/api/v2/subscription.
type Subscription struct {
	InstanceCode  string      `json:"instanceCode"`
	SpecCode      string      `json:"specCode"`
	StartTime     json.Number `json:"startTime"`
	EndTime       json.Number `json:"endTime"`
	AutoRenewFlag *bool       `json:"autoRenewFlag"`
	Status        string      `json:"status"`
}

// Addons is /tokenplan/personal/api/v2/addon/summary.
type Addons struct {
	RemainingCredits json.Number `json:"remainingCredits"`
	TotalCredits     json.Number `json:"totalCredits"`
	ActiveCount      json.Number `json:"activeCount"`
}

// Equity is one tool benefit of queryTokenPlanEquityInfo.
type Equity struct {
	EquityType      string      `json:"equityType"`
	AvailableQuota  json.Number `json:"availableQuota"`
	TotalQuota      json.Number `json:"totalQuota"`
	InstanceEndTime json.Number `json:"instanceEndTime"`
}

// Equities is the queryTokenPlanEquityInfo reply.
type Equities struct {
	Infos []Equity `json:"tokenPlanEquityInfos"`
}

// The console calls, by name.
const (
	APISubscription = "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/subscription"
	APIUsage        = "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage"
	APIAddons       = "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/addon/summary"
	APIEquity       = "zeldaEasy.bailian-commerce.tokenPlan.queryTokenPlanEquityInfo"
)

// ToolsNote is what the Token Plan's tool benefits come with.
const ToolsNote = "Other AgentStudio tools are 12% off while the plan is active."

var tools = map[string]struct{ name, unit string }{
	"tokenplan_harnesstool_image_generation": {"Image generation MCP", "images"},
	"tokenplan_mcp_codeinterpreter":          {"Code interpreter MCP", "calls"},
	"tokenplan_harnesstool_search_pro":       {"Web search pro MCP", "calls"},
	"tokenplan_harnesstool_vertical_search":  {"Vertical search MCP", "calls"},
	"tokenplan_harnesstool_webfetch":         {"Web page parsing MCP", "calls"},
	"tokenplan_harnesstool_tts":              {"Speech synthesis MCP", "characters"},
	"tokenplan_harnesstool_asr":              {"Speech recognition MCP", "seconds"},
}

// Tool names a tool benefit and its unit as the console labels them; an
// unknown one by its type.
func Tool(equityType string) (name, unit string) {
	if t, ok := tools[equityType]; ok {
		return t.name, t.unit
	}
	return strings.ReplaceAll(strings.TrimPrefix(strings.TrimPrefix(equityType, "tokenplan_"), "harnesstool_"), "_", " "), ""
}

// UsageWindow is one window of the usage reply.
type UsageWindow struct {
	N        int
	Unit     string // Hour, Day, Week, Month
	Name     string // "5 hours", "Week", "Month"
	Used     string // the fraction used, as reported ("0.25"; 1.0 = all of it)
	ResetsAt int64  // unix ms; 0 when not reported
}

// Ref is the window's meter: "window/5hour", "window/1month".
func (w UsageWindow) Ref() string { return "window/" + strconv.Itoa(w.N) + strings.ToLower(w.Unit) }

var usageWindowRE = regexp.MustCompile(`^per(\d+)(Hour|Day|Week|Month)Percentage$`)

// ParseUsage reads {"per1MonthPercentage":0.25,"per1MonthResetTime":…,…}:
// every per<N><Unit> window, shortest first.
func ParseUsage(raw map[string]json.RawMessage) []UsageWindow {
	order := map[string]int{"Hour": 0, "Day": 1, "Week": 2, "Month": 3}
	var ws []UsageWindow
	for k, v := range raw {
		m := usageWindowRE.FindStringSubmatch(k)
		if m == nil {
			continue
		}
		var f json.Number
		if decode(v, &f) != nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		w := UsageWindow{N: n, Unit: m[2], Name: WindowName(n, m[2]), Used: f.String()}
		if r, ok := raw["per"+m[1]+m[2]+"ResetTime"]; ok {
			var ms int64
			if json.Unmarshal(r, &ms) == nil && ms > 0 {
				w.ResetsAt = ms
			}
		}
		ws = append(ws, w)
	}
	sort.Slice(ws, func(i, j int) bool {
		if ws[i].Unit != ws[j].Unit {
			return order[ws[i].Unit] < order[ws[j].Unit]
		}
		return ws[i].N < ws[j].N
	})
	return ws
}

// WindowName is "Month" for one month, "5 hours" for five.
func WindowName(n int, unit string) string {
	if n == 1 {
		return unit
	}
	return fmt.Sprintf("%d %ss", n, strings.ToLower(unit))
}
