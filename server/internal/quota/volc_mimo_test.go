package quota

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// volcServer plays signin.volcengine.com's device flow and the Ark OpenAPI.
func volcServer(t *testing.T, approved *atomic.Bool) *httptest.Server {
	t.Helper()
	sts := func(id string) string {
		b, _ := json.Marshal(map[string]string{"access_key_id": id, "secret_access_key": "s", "session_token": "st-" + id})
		return string(b)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.URL.Path {
		case "/authorize/oauth/device_authorization":
			if r.PostForm.Get("client_id") != volcClientID || r.PostForm.Get("scope") != volcScope {
				t.Errorf("device authorization form = %v", r.PostForm)
			}
			_, _ = w.Write([]byte(`{"device_code":"dc","user_code":"ABCD","verification_uri":"https://signin.volcengine.com/device","verification_uri_complete":"https://signin.volcengine.com/device?code=ABCD","expires_in":600,"interval":1}`))
		case "/authorize/oauth/token":
			switch r.PostForm.Get("grant_type") {
			case "urn:ietf:params:oauth:grant-type:device_code":
				if !approved.Load() {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
					return
				}
				resp, _ := json.Marshal(map[string]any{"access_token": sts("AK1"), "expires_in": 900, "refresh_token": "rt1"})
				_, _ = w.Write(resp)
			case "refresh_token":
				if r.PostForm.Get("refresh_token") != "rt1" {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
					return
				}
				resp, _ := json.Marshal(map[string]any{"access_token": sts("AK2"), "expires_in": 900, "refresh_token": "rt2"})
				_, _ = w.Write(resp)
			}
		case "/":
			auth := r.Header.Get("Authorization")
			if r.URL.Query().Get("Action") != "GetCodingPlanUsage" || !strings.HasPrefix(auth, "HMAC-SHA256 Credential=AK") ||
				!strings.Contains(auth, "/cn-beijing/ark/request") || !strings.Contains(auth, "x-security-token") ||
				!strings.HasPrefix(r.Header.Get("X-Security-Token"), "st-AK") {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"ResponseMetadata":{"Error":{"Code":"InvalidAuthorization","Message":"bad signature"}}}`))
				return
			}
			_, _ = w.Write([]byte(`{"ResponseMetadata":{},"Result":{"QuotaUsage":[
				{"Level":"monthly","Percent":30,"ResetTimestamp":1793500000},
				{"Level":"session","Percent":80.5,"ResetTimestamp":1791400000},
				{"Level":"weekly","Percent":45,"ResetTimestamp":1791900000000}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestVolcengineSignsInWithTheDeviceFlowAndReadsTheCodingPlan(t *testing.T) {
	var approved atomic.Bool
	srv := volcServer(t, &approved)
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["volc.signin"], l.URLs["volc.api"], l.URLs["volc.ark"] = srv.URL, srv.URL, srv.URL
	old := openURL
	t.Cleanup(func() { openURL = old })
	openURL = func(page string) error {
		if !strings.Contains(page, "code=ABCD") {
			t.Errorf("opened %q", page)
		}
		if st := CurrentSignIn(); !st.Open || st.Code != "ABCD" || !strings.Contains(st.Page, "code=ABCD") {
			t.Errorf("status while waiting = %+v", st) // the page shows the code to confirm
		}
		go func() { time.Sleep(1500 * time.Millisecond); approved.Store(true) }() // the user approves
		return nil
	}
	v, err := volcLogin(context.Background(), l)
	if err != nil || v["refreshToken"] != "rt1" || !strings.Contains(v["sts"], "AK1") {
		t.Fatalf("login = %v, %v", v, err)
	}
	c := Connection{ID: "v", Provider: "volcengine", Mode: "coding-plan", Values: v}
	a := readVolcCodingPlan(context.Background(), l, c)
	if a.Status != StatusOK || len(a.Windows) != 3 || a.Windows[0].Name != "5 hours" || *a.Windows[0].UsedPercent != 80.5 ||
		a.Windows[1].Name != "Week" || a.Windows[1].ResetsAt == nil || a.Windows[2].Name != "Month" || a.Renewed != nil {
		t.Fatalf("plan = %+v", a)
	}
	// after the STS lapses, the refresh token renews it and the rotated token is handed back to keep
	c.Values["stsExpires"] = "1"
	a = readVolcCodingPlan(context.Background(), l, c)
	if a.Status != StatusOK || a.Renewed["refreshToken"] != "rt2" || !strings.Contains(a.Renewed["sts"], "AK2") {
		t.Fatalf("renewed = %+v", a)
	}
	c.Values["refreshToken"] = "gone"
	if a = readVolcCodingPlan(context.Background(), l, c); a.Status != StatusSignedOut {
		t.Fatalf("ended sign-in = %+v", a)
	}
}

func TestRenewedCredentialsAreKept(t *testing.T) {
	l := testLocal(t, nil)
	store := NewStore(l.AccountsFile())
	c := Connection{ID: "v", Provider: "volcengine", Mode: "coding-plan", Origin: OriginAdded, Values: map[string]string{"refreshToken": "rt1", "other": "x"}}
	a := Account{ID: "v", Renewed: map[string]string{"refreshToken": "rt2"}}
	if err := KeepRenewed(store, c, &a); err != nil {
		t.Fatal(err)
	}
	d, _ := store.Data()
	if len(d.Accounts) != 1 || d.Accounts[0].Values["refreshToken"] != "rt2" || d.Accounts[0].Values["other"] != "x" || a.Renewed != nil {
		t.Fatalf("stored = %+v", d)
	}
}

func TestMiMoPlanAndBalanceFromTheConsoleSignIn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := r.Header.Get("Cookie"); !strings.Contains(c, "serviceToken=tok") || !strings.Contains(c, "userId=42") {
			_, _ = w.Write([]byte(`<html>login</html>`))
			return
		}
		switch r.URL.Path {
		case "/api/v1/tokenPlan/usage":
			_, _ = w.Write([]byte(`{"code":0,"data":{"monthUsage":{"percent":0.25,"items":[
				{"name":"month_total_token","used":"250000","limit":"1000000","percent":0.25},
				{"name":"compensation_total_token","used":0,"limit":0,"percent":0}]}}}`))
		case "/api/v1/tokenPlan/detail":
			_, _ = w.Write([]byte(`{"code":0,"data":{"planCode":"standard","currentPeriodEnd":"2026-11-04 23:59:59","expired":false}}`))
		case "/api/v1/balance":
			_, _ = w.Write([]byte(`{"code":0,"data":{"balance":"25.51","currency":"USD","cashBalance":"20","giftBalance":"5.51"}}`))
		}
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["mimo"] = srv.URL
	session := encodeSession([]siteCookie{{"platform.xiaomimimo.com", "serviceToken", "tok"}, {".xiaomimimo.com", "userId", "42"}})
	c := Connection{ID: "m", Provider: "xiaomi", Mode: "token-plan", Values: map[string]string{"session": session}}
	a := readMiMoPlan(context.Background(), l, c)
	if a.Status != StatusOK || a.Plan != "Standard" || a.EndsAt == nil || a.EndsAt.Format(time.RFC3339) != "2026-11-04T15:59:59Z" ||
		len(a.Windows) != 2 || a.Windows[0].Name != "Plan credits" || *a.Windows[0].Left != 750000 || a.Windows[1].Name != "Compensation credits" {
		t.Fatalf("plan = %+v", a)
	}
	b := readMiMoBalance(context.Background(), l, Connection{ID: "m2", Provider: "xiaomi", Mode: "api", Values: map[string]string{"session": session}})
	if b.Status != StatusOK || b.Balances[0] != (Balance{Currency: "USD", Amount: "25.51", Granted: "5.51", ToppedUp: "20.00"}) {
		t.Fatalf("balance = %+v", b)
	}
	stale := encodeSession([]siteCookie{{"platform.xiaomimimo.com", "serviceToken", "old"}, {".xiaomimimo.com", "userId", "42"}})
	if a := readMiMoPlan(context.Background(), l, Connection{Values: map[string]string{"session": stale}}); a.Status != StatusSignedOut {
		t.Fatalf("stale = %+v", a)
	}
}

func TestAntsProfileKeepsSessionCookies(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "Default", "Preferences")
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	_ = os.WriteFile(p, []byte(`{"profile":{"name":"Ant"},"session":{"startup_urls":[]}}`), 0o600)
	keepSessionCookies(dir)
	var prefs map[string]any
	b, _ := os.ReadFile(p)
	_ = json.Unmarshal(b, &prefs)
	if prefs["session"].(map[string]any)["restore_on_startup"] != float64(1) || prefs["profile"].(map[string]any)["name"] != "Ant" {
		t.Fatalf("prefs = %s", b)
	}
}
