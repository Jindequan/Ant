package quota

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
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
		q, err := signRPC(c.Values["accessKeyId"], c.Values["accessKeySecret"], p, now)
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
		var ok struct {
			Success bool   `json:"Success"`
			Message string `json:"Message"`
		}
		if json.Unmarshal(b, &ok) != nil || !ok.Success {
			return fmt.Errorf("Alibaba Cloud answered: %s", short(ok.Message))
		}
		return json.Unmarshal(b, dst)
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

// signRPC signs an Alibaba Cloud RPC request (signature version 1.0,
// HMAC-SHA1) and returns its query string.
func signRPC(id, secret string, params map[string]string, now time.Time) (string, error) {
	if id == "" || secret == "" {
		return "", fmt.Errorf("set the AccessKey ID and secret")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	all := map[string]string{
		"Format": "JSON", "AccessKeyId": id, "SignatureMethod": "HMAC-SHA1", "SignatureVersion": "1.0",
		"SignatureNonce": hex.EncodeToString(nonce), "Timestamp": now.UTC().Format("2006-01-02T15:04:05Z"),
	}
	for k, v := range params {
		all[k] = v
	}
	return rpcQuery(secret, all), nil
}

// rpcQuery is the canonical query of params followed by its signature.
func rpcQuery(secret string, all map[string]string) string {
	keys := make([]string, 0, len(all))
	for k := range all {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pairs := make([]string, len(keys))
	for i, k := range keys {
		pairs[i] = rpcEscape(k) + "=" + rpcEscape(all[k])
	}
	canonical := strings.Join(pairs, "&")
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	mac.Write([]byte("GET&%2F&" + rpcEscape(canonical)))
	return canonical + "&Signature=" + rpcEscape(base64.StdEncoding.EncodeToString(mac.Sum(nil)))
}

// rpcEscape is RFC 3986 percent-encoding, as Alibaba Cloud signs it.
func rpcEscape(s string) string {
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")
	e = strings.ReplaceAll(e, "*", "%2A")
	return strings.ReplaceAll(e, "%7E", "~")
}
