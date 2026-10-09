package quota

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"ant/server/internal/providers/aliyun"
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
	inner, err := aliyun.ParseCLIReply(body)
	if errors.Is(err, catalog.ErrSignedOut) {
		return errBailianSignedOut
	}
	if err != nil {
		return err
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

// readFreeQuotaList is the grants with room left, as windows, and how many
// more are used up or expired (the module's listing and parsing).
func readFreeQuotaList(ctx context.Context, g bailianAPI, now time.Time) ([]Window, int, string, error) {
	grants, gone, err := aliyun.FreeQuota(ctx, g.gateway, now)
	if err != nil {
		return nil, 0, "", err
	}
	ws := make([]Window, len(grants))
	for i, gr := range grants {
		left, limit := float64(gr.Left)/1e6, float64(gr.Limit)/1e6
		ws[i] = Window{Name: gr.Model, Model: gr.Model, Media: gr.Media, Left: &left, Limit: &limit, Unit: gr.Unit, Expires: true}
		if gr.Ends > 0 {
			ws[i].ResetsAt = ptr(time.UnixMilli(gr.Ends).UTC())
		}
	}
	return ws, gone, aliyun.GrantsGroup(grants), nil
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
	ws, gone, group, err := readFreeQuotaList(ctx, newBailianCLI(l, c), l.Now())
	if err != nil {
		bailianFailed(&a, err)
		return a
	}
	// one card per account, recognised by its grants (Bailian reports no account)
	a.Status, a.Identity, a.Windows = StatusOK, group, ws
	for _, w := range ws { // each grant is one model's, callable with the account's keys
		a.Models = append(a.Models, Model{ID: w.Model, Media: w.Media})
	}
	a.Message = aliyun.FreeQuotaNote(len(ws), gone)
	return a
}
