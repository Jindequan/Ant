package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"ant/server/internal/providers/catalog"
)

// Bailian's CLI gateway: the endpoint the official CLI reads with a console
// access token — /cli/api.json on the console gateway host, a bearer
// token, no browser cookies. Calls and replies are the console's own.

// bailianAPI is a way of calling Bailian's console APIs: the CLI gateway
// with a token, or the browser console with a signed-in session.
type bailianAPI interface {
	gateway(ctx context.Context, api string, data map[string]any, dst any) error
}

type bailianCLI struct {
	l      Local
	token  string
	region string
	host   string
}

func newBailianCLI(l Local, c Connection) *bailianCLI {
	region := c.Values["region"]
	if region == "" {
		region = "cn-beijing"
	}
	host := "https://bailian-cs.console.aliyun.com"
	if c.Values["site"] == "international" {
		host = "https://bailian-cs.console.alibabacloud.com"
	}
	return &bailianCLI{l: l, token: c.Values["token"], region: region, host: l.url("aliyun.cli", host)}
}

func (b *bailianCLI) gateway(ctx context.Context, api string, data map[string]any, dst any) error {
	if data == nil {
		data = map[string]any{}
	}
	data["cornerstoneParam"] = map[string]any{
		"protocol": "V2", "console": "ONE_CONSOLE", "productCode": "p_efm", "switchUserType": 3, "consoleSite": "BAILIAN_ALIYUN",
	}
	params, _ := json.Marshal(map[string]any{"Api": api, "V": "1.0", "Data": data})
	form := url.Values{"params": {string(params)}, "region": {b.region}}
	endpoint := b.host + "/cli/api.json?action=BroadScopeAspnGateway&product=sfm_bailian&api=" + url.QueryEscape(api)
	body, err := b.l.fetch(ctx, http.MethodPost, endpoint, map[string]string{
		"Authorization": "Bearer " + b.token, "Content-Type": "application/x-www-form-urlencoded", "Accept": "*/*",
	}, strings.NewReader(form.Encode()))
	if err != nil {
		if isAuthError(err) {
			return errBailianSignedOut
		}
		return err
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(body, &env) != nil {
		return errBailianChanged
	}
	var gw struct {
		Success   *bool  `json:"success"`
		ErrorCode any    `json:"errorCode"`
		ErrorMsg  string `json:"errorMsg"`
		DataV2    *struct {
			Data json.RawMessage `json:"data"`
		} `json:"DataV2"`
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(env.Data, &gw) != nil {
		return errBailianChanged
	}
	if gw.Success != nil && !*gw.Success {
		code := fmt.Sprint(gw.ErrorCode)
		if strings.Contains(code, "NotLogin") {
			return errBailianSignedOut
		}
		return fmt.Errorf("Bailian: %s", strings.TrimSpace(code+" "+gw.ErrorMsg))
	}
	// the call's own data: DataV2.data.data, else DataV2.data, else data
	inner := gw.Data
	if gw.DataV2 != nil {
		var d struct {
			Success *bool           `json:"success"`
			ErrorMs string          `json:"errorMsg"`
			Data    json.RawMessage `json:"data"`
		}
		if json.Unmarshal(gw.DataV2.Data, &d) == nil && d.Data != nil {
			if d.Success != nil && !*d.Success {
				return fmt.Errorf("Bailian: %s", d.ErrorMs)
			}
			inner = d.Data
		} else {
			inner = gw.DataV2.Data
		}
	}
	if dst == nil || len(inner) == 0 || string(inner) == "null" {
		return nil
	}
	if json.Unmarshal(inner, dst) != nil {
		return errBailianChanged
	}
	return nil
}

// bailianClient is the way connection c reads the console: its CLI token,
// else a browser session. It fills a when there is none.
func bailianClient(ctx context.Context, l Local, c Connection, a *Account) bailianAPI {
	if c.Values["token"] != "" {
		return newBailianCLI(l, c)
	}
	if s := openFor(ctx, l, c, a); s != nil {
		return s
	}
	return nil
}

// --- the new-user free quota ---

// bailianModel is one model of the model list, as far as free quota needs it.
type bailianModel struct {
	Model        string         `json:"model"`
	Capabilities []string       `json:"capabilities"`
	Items        []bailianModel `json:"items"`
}

// listBailianModels is every model name, with its capabilities, page by page
// as the CLI lists them.
func listBailianModels(ctx context.Context, g bailianAPI) ([]bailianModel, error) {
	var out []bailianModel
	seen := map[string]bool{}
	for page, pages := 1, 1; page <= pages && page <= 40; page++ {
		var r struct {
			Total int            `json:"total"`
			List  []bailianModel `json:"list"`
		}
		err := g.gateway(ctx, "zeldaHttp.dashscopeModel./zelda/api/v1/modelCenter/listFoundationModels", map[string]any{"input": map[string]any{
			"pageNo": page, "pageSize": 50, "name": "", "providers": []string{}, "inferenceProviders": []string{},
			"features": []string{}, "group": true, "capabilities": []string{}, "contextWindows": []string{},
		}}, &r)
		if err != nil {
			return nil, err
		}
		pages = (r.Total + 49) / 50
		for _, m := range r.List {
			for _, it := range append([]bailianModel{m}, m.Items...) {
				if it.Model != "" && !seen[it.Model] {
					seen[it.Model] = true
					out = append(out, it)
				}
			}
		}
	}
	return out, nil
}

// freeTierQuota is one row of queryFreeTierQuota: quotaTotal is what is
// left, quotaInitTotal what was granted, quotaValidityPeriod the due time.
type freeTierQuota struct {
	Model               string     `json:"model"`
	QuotaTotal          flexNumber `json:"quotaTotal"`
	QuotaInitTotal      flexNumber `json:"quotaInitTotal"`
	QuotaValidityPeriod flexNumber `json:"quotaValidityPeriod"`
	QuotaStatus         string     `json:"quotaStatus"`
}

// freeQuotaUnit is the unit a model's grant is counted in, by what the
// model does: text in tokens; the rest as Bailian counts them.
func freeQuotaUnit(caps []string) string {
	for _, c := range caps {
		switch c {
		case "TG", "VU", "Reasoning", "ME", "TR", "Multimodal-Omni", "Realtime-Omni":
			return "tokens"
		case "IG":
			return "images"
		}
	}
	return ""
}

// capsMedia is what a Bailian model makes, from its capabilities: IG
// image, VG video, TTS speech, ASR audio, else text.
func capsMedia(caps []string) string {
	for _, c := range caps {
		switch c {
		case "IG", "IE":
			return "image"
		case "VG", "VE":
			return "video"
		case "TTS", "SS":
			return "speech"
		case "ASR", "SR":
			return "audio"
		}
	}
	return "text"
}

func readFreeQuotaList(ctx context.Context, g bailianAPI, now time.Time) ([]Window, int, error) {
	models, err := listBailianModels(ctx, g)
	if err != nil {
		return nil, 0, err
	}
	names := make([]string, len(models))
	caps := map[string][]string{}
	for i, m := range models {
		names[i], caps[m.Model] = m.Model, m.Capabilities
	}
	var r struct {
		Quotas []freeTierQuota `json:"freeTierQuotas"`
	}
	if err := g.gateway(ctx, "zeldaEasy.bailian-commerce.freeTrial.queryFreeTierQuota",
		map[string]any{"queryFreeTierQuotaRequest": map[string]any{"models": names}}, &r); err != nil {
		return nil, 0, err
	}
	var ws []Window
	gone := 0
	for _, q := range r.Quotas {
		if q.Model == "" || q.QuotaInitTotal <= 0 {
			continue
		}
		ends := time.UnixMilli(int64(q.QuotaValidityPeriod)).UTC()
		if q.QuotaStatus != "VALID" || q.QuotaTotal <= 0 || (q.QuotaValidityPeriod > 0 && !ends.After(now)) {
			gone++
			continue
		}
		media := capsMedia(caps[q.Model])
		if media == "text" { // capabilities that don't say: the name may
			media = catalog.ModelMedia(q.Model, "")
		}
		w := Window{Name: q.Model, Model: q.Model, Media: media, Left: ptr(float64(q.QuotaTotal)), Limit: ptr(float64(q.QuotaInitTotal)), Unit: freeQuotaUnit(caps[q.Model]), Expires: true}
		if q.QuotaValidityPeriod > 0 {
			w.ResetsAt = &ends
		}
		ws = append(ws, w)
	}
	sort.SliceStable(ws, func(i, j int) bool {
		a, b := ws[i].ResetsAt, ws[j].ResetsAt
		if a == nil || b == nil {
			return a != nil
		}
		if !a.Equal(*b) {
			return a.Before(*b)
		}
		return ws[i].Name < ws[j].Name
	})
	return ws, gone, nil
}

func readBailianFreeQuota(ctx context.Context, l Local, c Connection) Account {
	p := aliyunProvider()
	a := base(p, p.Modes[2], c, l.Now())
	a.Link = bailianFreeQuota
	if c.Values["token"] == "" {
		// the browser console serves this list only behind its anti-bot
		// check; the CLI sign-in reads it the way Bailian's CLI does
		a.Status, a.Message = StatusSignedOut, "Sign in with Alibaba Cloud to list each model's free quota."
		return a
	}
	ws, gone, err := readFreeQuotaList(ctx, newBailianCLI(l, c), l.Now())
	if err != nil {
		bailianFailed(&a, err)
		return a
	}
	a.Status, a.Identity, a.Windows = StatusOK, "free-quota", ws
	for _, w := range ws { // each grant is one model's, callable with the account's keys
		a.Models = append(a.Models, Model{ID: w.Model, Media: w.Media})
	}
	switch {
	case len(ws) == 0:
		a.Message = "No model has free quota left."
	case gone > 0:
		a.Message = fmt.Sprintf("%d models with free quota · %d more used up or expired.", len(ws), gone)
	default:
		a.Message = fmt.Sprintf("%d models with free quota.", len(ws))
	}
	return a
}
