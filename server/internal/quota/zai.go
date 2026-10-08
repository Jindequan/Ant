package quota

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"ant/server/internal/providers/zai"
)

// Z.ai's GLM Coding Plan — the plan ZCode runs on — is read through the
// plan's own quota endpoint with the plan's API key. A key without a plan
// is pay-as-you-go and has no windows to read.

var zaiQuotaURL = map[string]string{
	"global": "https://api.z.ai/api/monitor/usage/quota/limit",
	"china":  "https://open.bigmodel.cn/api/monitor/usage/quota/limit",
}

// the coding plan's own OpenAI-style endpoint lists the models it serves
var zaiModelsURL = map[string]string{
	"global": "https://api.z.ai/api/coding/paas/v4/models",
	"china":  "https://open.bigmodel.cn/api/coding/paas/v4/models",
}

func zaiRegionOf(host string) string {
	switch host {
	case "api.z.ai":
		return "global"
	case "open.bigmodel.cn":
		return "china"
	}
	return ""
}

// Z.ai (global) and BigModel 智谱 (China) are two providers with the same
// kind of plan, each its own site and account.
func zaiProvider() Provider      { return glmProvider("zai", "Z.ai", "global") }
func bigModelProvider() Provider { return glmProvider("bigmodel", "BigModel 智谱", "china") }

func glmProvider(id, name, region string) Provider {
	link := "https://z.ai/manage-apikey/subscription"
	host := "api.z.ai"
	env := []string{"ZAI_API_KEY", "Z_AI_API_KEY"}
	opencode := []string{"zai-coding-plan", "zai"}
	if region == "china" {
		link, host = "https://open.bigmodel.cn/usercenter/glm-coding/usage", "open.bigmodel.cn"
		env, opencode = []string{"ZHIPU_API_KEY", "BIGMODEL_API_KEY"}, []string{"zhipuai-coding-plan", "zhipuai"}
	}
	return Provider{ID: id, Name: name, Link: link, Modes: []Mode{{
		ID: "coding-plan", Name: "GLM Coding Plan", Kind: KindSubscription, Live: true, Key: "apiKey",
		Rule:   "A 5-hour window and a weekly window, each renewing on its own clock. MCP tool calls are counted per month and don't stop the models.",
		Fields: keyField("id.secret", "The key your coding plan is used with, from "+name+" › API keys."),
		Discover: func(l Local) []Connection {
			return keyConnections(l, id, "coding-plan", keyHint{Hosts: []string{host}, OpenCode: opencode, Env: env},
				func(foundKey) map[string]string { return map[string]string{"region": region} })
		},
		Read: readZAI,
	}}}
}

func readZAI(ctx context.Context, l Local, c Connection) Account {
	p := zaiProvider()
	if c.Provider == "bigmodel" {
		p = bigModelProvider()
	}
	region := c.Values["region"]
	if region == "" {
		region = "global"
		if c.Provider == "bigmodel" {
			region = "china"
		}
	}
	a := base(p, p.Modes[0], c, l.Now())
	url := l.url("zai."+region, zaiQuotaURL[region])
	if url == "" {
		a.Status, a.Message = StatusError, "Choose the key's region."
		return a
	}
	// The plan's endpoint takes the bare key as the Authorization.
	b, err := l.fetch(ctx, http.MethodGet, url, map[string]string{"Authorization": c.Values["apiKey"]}, nil)
	if err != nil {
		a.Status, a.Message = StatusError, "Couldn't read the plan: "+short(err.Error())
		if isAuthError(err) {
			a.Status, a.Message = StatusSignedOut, "The key was refused. Check it in your Z.ai API keys."
		}
		return a
	}
	plan, windows, err := parseZAIQuota(b)
	if errors.Is(err, zai.ErrKeyRefused) {
		a.Status, a.Message = StatusSignedOut, "The key was refused. Check it in your Z.ai API keys."
		return a
	}
	if err != nil {
		a.Status, a.Message = StatusError, short(err.Error())
		return a
	}
	a.Status, a.Plan, a.Windows = StatusOK, plan, windows
	// a plan belongs to the Z.ai account, not the key: every key of one
	// account reads the same windows, reset to the millisecond
	var id []string
	for _, w := range windows {
		if w.ResetsAt != nil {
			id = append(id, strconv.FormatInt(w.ResetsAt.UnixMilli(), 10))
		}
	}
	if len(id) > 0 {
		a.Identity = region + ":" + plan + ":" + strings.Join(id, ",")
	}
	if len(windows) == 0 {
		a.Kind, a.ModeName = KindAPI, "API key"
		a.Message = "This key has no coding plan; it's billed pay-as-you-go."
	}
	keyModels(ctx, &a, l, c.Values["apiKey"], l.url("zai.models."+region, zaiModelsURL[region]))
	return a
}

// parseZAIQuota reads a plan with the Z.ai module's parser (BigModel answers
// in the same shape) as the page's windows.
func parseZAIQuota(b []byte) (string, []Window, error) {
	q, err := zai.ParseQuota(b)
	if err != nil {
		return "", nil, err
	}
	out := []Window{}
	for _, l := range q.Limits {
		w := Window{Name: l.Name(), UsedPercent: l.UsedPercent, Aside: l.Kind == "mcp"}
		if l.NextReset > 0 {
			w.ResetsAt = ptr(time.UnixMilli(l.NextReset).UTC())
		}
		out = append(out, w)
	}
	return q.Level, out, nil
}
