package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"ant/server/internal/providers/aliyun"
)

// --- the balance through the official API, with an AccessKey ---

// discoverAliyunAccessKey reads the standard Alibaba Cloud credentials: the
// ALIBABA_CLOUD_ACCESS_KEY_ID/SECRET environment, then the aliyun CLI's
// current AccessKey profile in ~/.aliyun/config.json.
func discoverAliyunAccessKey(l Local) []Connection {
	conn := func(id, secret, source string) []Connection {
		return []Connection{{ID: fingerprint("aliyun", "payg", id), Provider: "aliyun", Mode: "payg", Origin: OriginFound, Source: source,
			Values: map[string]string{"accessKeyId": id, "accessKeySecret": secret, "site": "china"}}}
	}
	id, n := l.env("ALIBABA_CLOUD_ACCESS_KEY_ID")
	if secret, _ := l.env("ALIBABA_CLOUD_ACCESS_KEY_SECRET"); id != "" && secret != "" {
		return conn(id, secret, "$"+n)
	}
	var cfg struct {
		Current  string `json:"current"`
		Profiles []struct {
			Name   string `json:"name"`
			Mode   string `json:"mode"`
			ID     string `json:"access_key_id"`
			Secret string `json:"access_key_secret"`
		} `json:"profiles"`
	}
	p := l.path(".aliyun", "config.json")
	if !readJSON(p, &cfg) {
		return nil
	}
	for _, pr := range cfg.Profiles {
		if pr.Name == cfg.Current && strings.EqualFold(pr.Mode, "AK") && pr.ID != "" && pr.Secret != "" {
			return conn(pr.ID, pr.Secret, l.display(p))
		}
	}
	return nil
}

// officialBilling calls Alibaba Cloud's BssOpenApi with the connection's
// AccessKey (signature v1), as the billing API documents.
func officialBilling(l Local, c Connection, now time.Time) billing {
	endpoint := "https://business.aliyuncs.com/"
	if c.Values["site"] == "intl" {
		endpoint = "https://business.ap-southeast-1.aliyuncs.com/"
	}
	endpoint = l.url("aliyun.business", endpoint)
	return func(ctx context.Context, action string, params map[string]any, dst any) error {
		p := map[string]string{"Action": action, "Version": "2017-12-14"}
		for k, v := range params {
			p[k] = fmt.Sprint(v)
		}
		q, err := aliyun.SignRPC(c.Values["accessKeyId"], c.Values["accessKeySecret"], p, now)
		if err != nil {
			return err
		}
		b, err := l.fetch(ctx, http.MethodGet, endpoint+"?"+q, nil, nil)
		if err != nil {
			if isAuthError(err) {
				return fmt.Errorf("Alibaba Cloud refused the AccessKey: %s", aliyunError(err))
			}
			return errors.New(aliyunError(err))
		}
		return aliyun.ParseBSSReply(b, dst)
	}
}

// aliyunError turns Alibaba Cloud's {"Code","Message"} error body into one line.
func aliyunError(err error) string {
	if se, ok := err.(*statusError); ok {
		var e struct{ Code, Message string }
		if json.Unmarshal([]byte(se.Body), &e) == nil && e.Code != "" {
			return short(e.Code + ": " + e.Message)
		}
	}
	return short(err.Error())
}
