package aliyun

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"ant/server/internal/domain"
	"ant/server/internal/domain/observed"
	"ant/server/internal/providers/catalog"
)

// The new-user free quota: each model has its own grant (tokens for text
// models; images, seconds or calls for others), valid 90 days from
// activation, read the way Bailian's CLI reads it: the model list, then
// queryFreeTierQuota for those models. The browser console guards this
// list behind its anti-bot check, so only a console token reads it.

// Call is one console call: the module's CLI gateway, or quota's.
type Call func(ctx context.Context, api string, data map[string]any, dst any) error

const (
	APIListModels = "zeldaHttp.dashscopeModel./zelda/api/v1/modelCenter/listFoundationModels"
	APIFreeQuota  = "zeldaEasy.bailian-commerce.freeTrial.queryFreeTierQuota"
)

// Model is one model of the model list, as far as free quota needs it.
type Model struct {
	Model        string   `json:"model"`
	Capabilities []string `json:"capabilities"`
	Items        []Model  `json:"items"`
}

// ListModels is every model name with its capabilities, page by page as
// the CLI lists them (at most 40 pages of 50).
func ListModels(ctx context.Context, call Call) ([]Model, error) {
	var out []Model
	seen := map[string]bool{}
	for page, pages := 1, 1; page <= pages && page <= 40; page++ {
		var r struct {
			Total Amount  `json:"total"`
			List  []Model `json:"list"`
		}
		err := call(ctx, APIListModels, map[string]any{"input": map[string]any{
			"pageNo": page, "pageSize": 50, "name": "", "providers": []string{}, "inferenceProviders": []string{},
			"features": []string{}, "group": true, "capabilities": []string{}, "contextWindows": []string{},
		}}, &r)
		if err != nil {
			return nil, err
		}
		total, ok := r.Total.Int()
		if !ok {
			return nil, ErrChanged
		}
		pages = int((total + 49) / 50)
		for _, m := range r.List {
			for _, it := range append([]Model{m}, m.Items...) {
				if it.Model != "" && !seen[it.Model] {
					seen[it.Model] = true
					out = append(out, it)
				}
			}
		}
	}
	return out, nil
}

// quotaRow is one row of queryFreeTierQuota: quotaTotal is what is left,
// quotaInitTotal what was granted, quotaValidityPeriod when it ends (ms).
type quotaRow struct {
	Model               string `json:"model"`
	QuotaTotal          Amount `json:"quotaTotal"`
	QuotaInitTotal      Amount `json:"quotaInitTotal"`
	QuotaValidityPeriod Amount `json:"quotaValidityPeriod"`
	QuotaStatus         string `json:"quotaStatus"`
}

// Grant is one model's free quota with room left.
type Grant struct {
	Model       string
	Media, Unit string
	Left, Limit domain.Units // 10^-6
	Ends        int64        // unix ms; 0 when not reported
}

// FreeQuota lists the grants with room left, soonest-ending first, and how
// many more are used up or expired.
func FreeQuota(ctx context.Context, call Call, now time.Time) ([]Grant, int, error) {
	models, err := ListModels(ctx, call)
	if err != nil {
		return nil, 0, err
	}
	names := make([]string, len(models))
	caps := map[string][]string{}
	for i, m := range models {
		names[i], caps[m.Model] = m.Model, m.Capabilities
	}
	var r struct {
		Quotas []quotaRow `json:"freeTierQuotas"`
	}
	if err := call(ctx, APIFreeQuota, map[string]any{"queryFreeTierQuotaRequest": map[string]any{"models": names}}, &r); err != nil {
		return nil, 0, err
	}
	var out []Grant
	gone := 0
	for _, q := range r.Quotas {
		left, ok1 := q.QuotaTotal.Units()
		limit, ok2 := q.QuotaInitTotal.Units()
		endMs, ok3 := q.QuotaValidityPeriod.Int()
		if !ok1 || !ok2 || !ok3 {
			return nil, 0, ErrChanged
		}
		if q.Model == "" || limit <= 0 {
			continue
		}
		if q.QuotaStatus != "VALID" || left <= 0 || (endMs > 0 && endMs <= now.UnixMilli()) {
			gone++
			continue
		}
		media := capsMedia(caps[q.Model])
		if media == "text" { // capabilities that don't say: the name may
			media = catalog.ModelMedia(q.Model, "")
		}
		out = append(out, Grant{Model: q.Model, Media: media, Unit: freeQuotaUnit(caps[q.Model]), Left: left, Limit: limit, Ends: endMs})
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Ends, out[j].Ends
		if (a == 0) != (b == 0) {
			return b == 0
		}
		if a != b {
			return a < b
		}
		return out[i].Model < out[j].Model
	})
	return out, gone, nil
}

// freeQuotaUnit is what a model's grant is counted in, by what the model
// does: text in tokens; the rest as Bailian counts them.
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

// capsMedia is what a Bailian model makes, from its capabilities.
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

// FreeQuotaNote says how many models have quota, and how many more don't.
func FreeQuotaNote(live, gone int) string {
	models := fmt.Sprintf("%d models", live)
	if live == 1 {
		models = "1 model"
	}
	switch {
	case live == 0:
		return "No model has free quota left."
	case gone > 0:
		return fmt.Sprintf("%s with free quota · %d more used up or expired.", models, gone)
	}
	return models + " with free quota."
}

// freeQuota is the grants as one free_grant package: a meter per model,
// scoped to that model, ending when the grant does.
func freeQuota(ctx context.Context, g gateway, now time.Time) (observed.Inventory, []Grant, error) {
	grants, gone, err := FreeQuota(ctx, g.call, now)
	if err != nil {
		return observed.Inventory{}, nil, err
	}
	p := observed.PackageReading{TemplateRef: "aliyun/free-quota", Kind: "free_grant", Basis: "observed", Plan: "New-user free quota"}
	for _, gr := range grants {
		left, limit := gr.Left, gr.Limit
		r := observed.ResourceReading{TemplateRef: "grant/" + gr.Model, Label: gr.Model, MetricID: domain.ID("free." + gr.Model), Unit: firstOf(gr.Unit, "units"),
			QuantumExp: -6, SemanticsRef: "aliyun.free-grant", SemanticsRevision: "v1",
			WindowJSON: []byte(`{"kind":"lifetime"}`), ScopeJSON: mustJSON(map[string]string{"kind": "model", "model": gr.Model}),
			ResetBasis: "none", Limit: &limit, Remaining: &left, Evidence: mustJSON(map[string]string{"media": gr.Media})}
		if gr.Ends > 0 {
			end := domain.Millis(gr.Ends)
			r.WindowEnd, r.ResetBasis = &end, "reported"
		}
		p.Resources = append(p.Resources, r)
	}
	// every way into one Alibaba Cloud account reads the same grants, ending
	// to the millisecond: one card (display only; another account's grants
	// end at other times, so it keeps its own)
	inv := observed.Inventory{Packages: []observed.PackageReading{p}, DisplayGroup: GrantsGroup(grants), Note: FreeQuotaNote(len(grants), gone)}
	return inv, grants, nil
}

// GrantsGroup recognises an account by its grants: each model and when its
// grant ends. "" when there are none to go by.
func GrantsGroup(grants []Grant) string {
	if len(grants) == 0 {
		return ""
	}
	parts := make([]string, len(grants))
	for i, g := range grants {
		parts[i] = g.Model + "@" + strconv.FormatInt(g.Ends, 10)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, ",")))
	return "aliyun:free-quota:" + hex.EncodeToString(sum[:8])
}

// freeQuotaModels are the models the grants are for.
func freeQuotaModels(grants []Grant) []observed.UpstreamModel {
	out := make([]observed.UpstreamModel, len(grants))
	for i, g := range grants {
		out[i] = observed.UpstreamModel{ID: g.Model, Media: g.Media}
	}
	return out
}
