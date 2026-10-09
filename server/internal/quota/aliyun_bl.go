package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The Bailian CLI's sign-in (bl login): ~/.bailian/config.json, or
// $BAILIAN_CONFIG_DIR. Its top level is the default profile; active_config
// names another profile kept as a nested object. A profile holds the
// console access token (access_token, console_site, console_region) and,
// when given, an AccessKey (access_key_id, access_key_secret).

func blConfigPath(l Local) string {
	if d, _ := l.env("BAILIAN_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "config.json")
	}
	return l.path(".bailian", "config.json")
}

// blProfile is the active profile's values as a connection's, or nil.
func blProfile(path string) map[string]string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var all map[string]json.RawMessage
	if json.Unmarshal(b, &all) != nil {
		return nil
	}
	type profile struct {
		Token  string `json:"access_token"`
		Site   string `json:"console_site"`
		Region string `json:"console_region"`
		AKID   string `json:"access_key_id"`
		AKSec  string `json:"access_key_secret"`
		Key    string `json:"api_key"`
		Base   string `json:"base_url"`
	}
	var p profile
	json.Unmarshal(b, &p)
	var active string
	json.Unmarshal(all["active_config"], &active)
	if raw, ok := all[active]; ok && active != "" {
		var n profile
		if json.Unmarshal(raw, &n) == nil {
			p = n
		}
	}
	if p.Token == "" && p.AKID == "" {
		return nil
	}
	v := map[string]string{"token": p.Token, "region": p.Region, "accessKeyId": p.AKID, "accessKeySecret": p.AKSec, "apiKey": p.Key, "baseURL": p.Base}
	if p.Site == "international" || p.Site == "intl" {
		v["site"] = "international"
	}
	return v
}

// discoverBLConfig finds bl's sign-in for a mode; what it needs (a token,
// or for pay-as-you-go an AccessKey) is read again at each read.
func discoverBLConfig(mode string) func(Local) []Connection {
	return func(l Local) []Connection {
		p := blConfigPath(l)
		v := blProfile(p)
		if v == nil || (mode == "payg" && v["accessKeyId"] == "") || (mode != "payg" && v["token"] == "") {
			return nil
		}
		return []Connection{{ID: "aliyun-bl-" + mode, Provider: "aliyun", Mode: mode, Origin: OriginFound, Source: "Bailian CLI",
			Values: map[string]string{"blConfig": p}}}
	}
}

// resolveBL is what a bl connection reads with now: bl's active profile.
func resolveBL(_ Local, c Connection) (map[string]string, error) {
	p := c.Values["blConfig"]
	if p == "" {
		return c.Values, nil
	}
	v := blProfile(p)
	if v == nil || (v["token"] == "" && v["accessKeyId"] == "") {
		return nil, fmt.Errorf("%w: The Bailian CLI is signed out. Run bl login.", ErrSignedOut)
	}
	v["via"] = "bl" // a refused token is renewed with bl login
	return v, nil
}

// withBLConfig wraps a reader so a bl connection reads with what bl holds now.
func withBLConfig(read func(context.Context, Local, Connection) Account) func(context.Context, Local, Connection) Account {
	return func(ctx context.Context, l Local, c Connection) Account {
		v, err := resolveBL(l, c)
		if err != nil {
			prov := aliyunProvider()
			m, _ := prov.mode(c.Mode)
			a := base(prov, m, c, l.Now())
			a.Status, a.Message = StatusSignedOut, strings.TrimPrefix(err.Error(), ErrSignedOut.Error()+": ")
			return a
		}
		c.Values = v
		a := read(ctx, l, c)
		// the profile's model key (the Token Plan's, when bl was set up with
		// one) lists what it can call
		if c.Mode == "token-plan" && v["apiKey"] != "" && v["baseURL"] != "" {
			keyModels(ctx, &a, l, v["apiKey"], strings.TrimRight(v["baseURL"], "/")+"/models")
		}
		return a
	}
}
