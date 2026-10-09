package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Gemini CLI's Google sign-in: its Code Assist quota per model, read the
// way Gemini CLI reads it (as yetone/magpie does). The sign-in is
// ~/.gemini/oauth_creds.json; an expired access token is renewed in memory
// with Gemini CLI's own OAuth client (an installed app's, shipped in the
// CLI), never written back. loadCodeAssist names the account's project and
// tier; retrieveUserQuota its buckets.

const (
	geminiClientID     = ""
	geminiClientSecret = ""
	codeAssistBase     = "https://cloudcode-pa.googleapis.com"
)

func geminiProvider() Provider {
	return Provider{ID: "gemini", Name: "Gemini CLI", Link: "https://aistudio.google.com/usage", Modes: []Mode{{
		ID: "code-assist", Name: "Code Assist", Kind: KindFree, Live: true,
		Rule: "A daily allowance per model on Google's Code Assist (free tier, or Standard / Enterprise), renewing each day.",
		Ways: []Way{
			{ID: "cli", Name: "Gemini CLI", Hint: "Ant reads Gemini CLI's Google sign-in (~/.gemini). Run gemini and sign in with Google.",
				Fields: []Field{}, Discover: discoverGemini, Read: readGemini},
			{ID: "folder", Name: "Another Google account", Key: "folder", Read: readGemini,
				Hint:   "Keep each Google account's Gemini CLI sign-in in its own folder: sign in with that account, copy ~/.gemini/oauth_creds.json into a folder such as ~/.gemini-work, and give that folder here.",
				Fields: []Field{{ID: "folder", Label: "Folder with oauth_creds.json", Type: "text", Placeholder: "~/.gemini-work"}}},
		},
	}}}
}

type geminiCreds struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	Expiry       int64  `json:"expiry_date"` // ms
	IDToken      string `json:"id_token"`
}

func discoverGemini(l Local) []Connection {
	p := l.path(".gemini", "oauth_creds.json")
	var c geminiCreds
	if !readJSON(p, &c) || c.RefreshToken == "" {
		return nil
	}
	return []Connection{{ID: "gemini-cli", Provider: "gemini", Mode: "code-assist", Origin: OriginFound, Source: l.display(p), Values: map[string]string{"creds": p}}}
}

func geminiToken(ctx context.Context, l Local, c geminiCreds) (string, error) {
	if c.AccessToken != "" && time.UnixMilli(c.Expiry).After(l.Now().Add(2*time.Minute)) {
		return c.AccessToken, nil
	}
	form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.RefreshToken}, "client_id": {geminiClientID}, "client_secret": {geminiClientSecret}}
	b, err := l.fetch(ctx, http.MethodPost, l.url("google.token", "https://oauth2.googleapis.com/token"),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, strings.NewReader(form.Encode()))
	if err != nil {
		return "", errGeminiSignedOut
	}
	var t struct {
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(b, &t) != nil || t.AccessToken == "" {
		return "", errGeminiSignedOut
	}
	return t.AccessToken, nil
}

func codeAssist(ctx context.Context, l Local, token, method string, body, dst any) error {
	b, _ := json.Marshal(body)
	out, err := l.fetch(ctx, http.MethodPost, l.url("google.codeassist", codeAssistBase)+"/v1internal:"+method, map[string]string{
		"Authorization": "Bearer " + token, "Content-Type": "application/json", "User-Agent": "GeminiCLI",
	}, bytes.NewReader(b))
	if err != nil {
		return err
	}
	return json.Unmarshal(out, dst)
}

func readGemini(ctx context.Context, l Local, c Connection) Account {
	p := geminiProvider()
	a := base(p, p.Modes[0], c, l.Now())
	var creds geminiCreds
	path := c.Values["creds"]
	if f := strings.TrimSpace(c.Values["folder"]); f != "" {
		path = filepath.Join(l.expand(f), "oauth_creds.json")
	}
	if !readJSON(path, &creds) || creds.RefreshToken == "" {
		a.Status, a.Message = StatusSignedOut, "Gemini CLI is signed out. Run gemini and sign in with Google."
		if c.Values["folder"] != "" {
			a.Message = "No Gemini CLI sign-in (oauth_creds.json) in " + c.Values["folder"] + "."
		}
		return a
	}
	a.User = claimString(jwtClaims(creds.IDToken), "email")
	token, err := geminiToken(ctx, l, creds)
	if err != nil {
		a.Status, a.Message = StatusSignedOut, "Gemini CLI's Google sign-in has ended. Run gemini and sign in again."
		return a
	}
	meta := map[string]any{"ideType": "IDE_UNSPECIFIED", "platform": "PLATFORM_UNSPECIFIED", "pluginType": "GEMINI"}
	var load struct {
		Project     json.RawMessage `json:"cloudaicompanionProject"`
		CurrentTier *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"currentTier"`
	}
	if err := codeAssist(ctx, l, token, "loadCodeAssist", map[string]any{"metadata": meta}, &load); err != nil {
		a.Status, a.Message = StatusError, "Couldn't reach Code Assist: "+short(err.Error())
		return a
	}
	project := strings.Trim(string(load.Project), `"`)
	var obj struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(load.Project, &obj) == nil && obj.ID != "" {
		project = obj.ID
	}
	if load.CurrentTier == nil || project == "" || project == "null" {
		a.Status, a.Message = StatusError, "Google gives Gemini CLI's sign-in only to Code Assist accounts with a Google Cloud project; run gemini once to set one up."
		return a
	}
	a.Plan = load.CurrentTier.Name
	if load.CurrentTier.ID != "free-tier" {
		a.Kind = KindSubscription
	}
	var q struct {
		Buckets []struct {
			ModelID           string   `json:"modelId"`
			RemainingFraction *float64 `json:"remainingFraction"`
			ResetTime         string   `json:"resetTime"`
		} `json:"buckets"`
	}
	if err := codeAssist(ctx, l, token, "retrieveUserQuota", map[string]any{"project": project}, &q); err != nil {
		a.Status, a.Message = StatusError, "Couldn't read the quota: "+short(err.Error())
		return a
	}
	seen := map[string]bool{}
	for _, bk := range q.Buckets {
		if bk.ModelID == "" || seen[bk.ModelID] || strings.HasSuffix(bk.ModelID, "_vertex") || bk.RemainingFraction == nil {
			continue
		}
		seen[bk.ModelID] = true
		w := Window{Name: bk.ModelID, Model: bk.ModelID, UsedPercent: ptr(max(0, min(100, (1-*bk.RemainingFraction)*100)))}
		if t, err := time.Parse(time.RFC3339, bk.ResetTime); err == nil {
			w.ResetsAt = ptr(t.UTC())
		}
		a.Windows = append(a.Windows, w)
	}
	sort.SliceStable(a.Windows, func(i, j int) bool { return a.Windows[i].Name < a.Windows[j].Name })
	a.Status = StatusOK
	for _, w := range a.Windows {
		a.Models = append(a.Models, Model{ID: w.Model, Media: "text"})
	}
	if len(a.Windows) == 0 {
		a.Message = fmt.Sprintf("%s reported no per-model quota.", a.Plan)
	}
	return a
}

var errGeminiSignedOut = errors.New("gemini signed out")
