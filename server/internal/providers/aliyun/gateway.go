// Package aliyun is the Alibaba Cloud Bailian provider module (design 13
// phase C): the Token Plan subscription and the new-user free quota, read
// through Bailian's CLI gateway with a console token (from `bl login` or
// Ant's own console sign-in), and pay-as-you-go, read through the official
// billing API with an AccessKey. Browser sign-ins stay with quota's readers.
package aliyun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"ant/server/internal/domain/observed"
	"ant/server/internal/providers/catalog"
)

// ErrChanged: a reply Ant doesn't understand. Shown as it is, never as
// wrong numbers.
var ErrChanged = errors.New("Bailian's console changed; Ant needs an update to read it")

// signedOut is what the user should do when the console token is refused.
const signedOut = "Your Alibaba Cloud sign-in has ended. Sign in again."

// gateway is Bailian's CLI gateway: /cli/api.json on the console gateway
// host, a bearer console token, the console's own calls and replies.
type gateway struct {
	host, token, region string
	fetch               observed.Fetch
}

func (g gateway) call(ctx context.Context, api string, data map[string]any, dst any) error {
	if data == nil {
		data = map[string]any{}
	}
	data["cornerstoneParam"] = map[string]any{
		"protocol": "V2", "console": "ONE_CONSOLE", "productCode": "p_efm", "switchUserType": 3, "consoleSite": "BAILIAN_ALIYUN",
	}
	params, _ := json.Marshal(map[string]any{"Api": api, "V": "1.0", "Data": data})
	form := url.Values{"params": {string(params)}, "region": {g.region}}
	res, err := g.fetch(ctx, observed.Request{Method: "POST",
		URL:    g.host + "/cli/api.json?action=BroadScopeAspnGateway&product=sfm_bailian&api=" + url.QueryEscape(api),
		Header: map[string]string{"Authorization": "Bearer " + g.token, "Content-Type": "application/x-www-form-urlencoded", "Accept": "*/*"},
		Body:   []byte(form.Encode())})
	if err != nil {
		return fmt.Errorf("Couldn't reach Bailian: %v", err)
	}
	if res.Status == 401 || res.Status == 403 {
		return fmt.Errorf("%w: %s", catalog.ErrSignedOut, signedOut)
	}
	if res.Status < 200 || res.Status > 299 {
		return fmt.Errorf("Bailian answered %d", res.Status)
	}
	inner, err := ParseCLIReply(res.Body)
	if err != nil || dst == nil || len(inner) == 0 || string(inner) == "null" {
		return err
	}
	return decode(inner, dst)
}

// ParseCLIReply is the call's own data in a CLI gateway reply:
// data.DataV2.data.data, else data.DataV2.data, else data.data. A refused
// token is catalog.ErrSignedOut; a reply that isn't the gateway's is
// ErrChanged.
func ParseCLIReply(b []byte) (json.RawMessage, error) {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(b, &env) != nil {
		return nil, ErrChanged
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
		return nil, ErrChanged
	}
	if gw.Success != nil && !*gw.Success {
		code := fmt.Sprint(gw.ErrorCode)
		if strings.Contains(code, "NotLogin") {
			return nil, fmt.Errorf("%w: %s", catalog.ErrSignedOut, signedOut)
		}
		return nil, fmt.Errorf("Bailian: %s", strings.TrimSpace(code+" "+gw.ErrorMsg))
	}
	inner := gw.Data
	if gw.DataV2 != nil {
		var d struct {
			Success *bool           `json:"success"`
			ErrorMs string          `json:"errorMsg"`
			Data    json.RawMessage `json:"data"`
		}
		if json.Unmarshal(gw.DataV2.Data, &d) == nil && d.Data != nil {
			if d.Success != nil && !*d.Success {
				return nil, fmt.Errorf("Bailian: %s", d.ErrorMs)
			}
			inner = d.Data
		} else {
			inner = gw.DataV2.Data
		}
	}
	return inner, nil
}

// decode reads a reply keeping numbers as their text (json.Number), so
// amounts are converted exactly.
func decode(b []byte, dst any) error {
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	if d.Decode(dst) != nil {
		return ErrChanged
	}
	return nil
}
