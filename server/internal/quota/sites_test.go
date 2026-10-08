package quota

import (
	"context"
	"encoding/binary"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// pasted is a connection read with a pasted Cookie header.
func pasted(provider, mode, cookie string) Connection {
	return Connection{ID: provider + "-x", Provider: provider, Mode: mode, Way: "cookie", Origin: OriginAdded, Values: map[string]string{"cookie": cookie}}
}

func TestKlingCreditLots(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/account/pointAndTicket" || r.URL.Query().Get("scope") != "PERSONAL" {
			w.WriteHeader(404)
			return
		}
		if !strings.Contains(r.Header.Get("Cookie"), "kuaishou.ai.portal_st=ok") {
			w.Write([]byte(`{"status":500,"result":1,"data":null,"error":{"type":"MID.S4B"}}`))
			return
		}
		w.Write([]byte(`{"status":200,"result":1,"data":{"total":16600,"points":[{"balance":10000,"endTime":1793000000000},{"balance":6600,"endTime":1791504000000},{"balance":0,"endTime":1}]}}`))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["kling"] = srv.URL
	a := readKling(context.Background(), l, pasted("kling", "credits", "kuaishou.ai.portal_st=ok; userId=1"))
	if a.Status != StatusOK || a.Balances[0].Amount != "166" || len(a.Windows) != 2 || *a.Windows[0].Left != 66 || !a.Windows[0].Expires {
		t.Fatalf("%+v", a)
	}
	if a.Media[0] != "video" || a.WayName != "Paste the cookie" {
		t.Fatalf("%v %q", a.Media, a.WayName)
	}
	if a := readKling(context.Background(), l, pasted("kling", "credits", "x=1")); a.Status != StatusSignedOut {
		t.Fatalf("%+v", a)
	}
}

func TestLeonardoWebAndAPI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/auth/get-session":
			if !strings.Contains(r.Header.Get("Cookie"), "better-auth.session_token=s") {
				w.Write([]byte(`null`))
				return
			}
			w.Write([]byte(`{"session":{"accessToken":"at"},"user":{"email":"me@example.com"}}`))
		case "/graphql":
			b, _ := io.ReadAll(r.Body)
			if r.Header.Get("Authorization") != "Bearer at" || !strings.Contains(string(b), "user_details") {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"data":{"users":[{"username":"me","user_details":[{"plan":"FREE","subscriptionTokens":120,"paidTokens":0,"rolloverTokens":0,"tokenRenewalDate":"2026-10-08T00:00:00.000Z"}]}]}}`))
		case "/me":
			if r.Header.Get("Authorization") != "Bearer key" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"user_details":[{"user":{"id":"u","username":"me"},"plan":"APPRENTICE","subscriptionTokens":8000,"paidTokens":500,"tokenRenewalDate":"2026-11-01T00:00:00","apiSubscriptionTokens":3500,"apiPaidTokens":0}]}`))
		}
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["leonardo.app"], l.URLs["leonardo.graphql"], l.URLs["leonardo.api"] = srv.URL, srv.URL+"/graphql", srv.URL
	a := readLeonardoWeb(context.Background(), l, pasted("leonardo", "tokens", "__Secure-better-auth.session_token=s"))
	if a.Status != StatusOK || a.User != "me@example.com" || a.Windows[0].Name != "Today" || *a.Windows[0].Left != 120 || *a.Windows[0].Limit != 150 || a.Windows[0].ResetsAt == nil || a.Kind != KindFree {
		t.Fatalf("%+v", a)
	}
	if a := readLeonardoWeb(context.Background(), l, pasted("leonardo", "tokens", "other=1")); a.Status != StatusSignedOut {
		t.Fatalf("%+v", a)
	}
	a = readLeonardoAPI(context.Background(), l, Connection{Way: "api-key", Values: map[string]string{"apiKey": "key"}})
	if a.Status != StatusOK || a.Kind != KindSubscription || *a.Windows[0].Left != 8000 || len(a.Groups) != 2 || a.User != "me" {
		t.Fatalf("%+v", a)
	}
}

func TestSeaArtStamina(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-Project-Id") != "seaart" {
			w.WriteHeader(400)
			return
		}
		if !strings.Contains(r.Header.Get("Cookie"), "T=tok") {
			w.Write([]byte(`{"data":null,"status":{"code":20000,"msg":"account not logged in"}}`))
			return
		}
		w.Write([]byte(`{"status":{"code":10000},"data":{"account_id":"a1","temp_coins":130,"free_coins":20,"paid_coins":5,"is_active":false}}`))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["seaart"] = srv.URL
	a := readSeaArt(context.Background(), l, pasted("seaart", "stamina", "T=tok"))
	if a.Status != StatusOK || *a.Windows[0].Left != 130 || a.Windows[0].ResetsAt.Hour() != 0 || a.Balances[0].Amount != "25" {
		t.Fatalf("%+v", a)
	}
	if a := readSeaArt(context.Background(), l, pasted("seaart", "stamina", "x=1")); a.Status != StatusSignedOut {
		t.Fatalf("%+v", a)
	}
}

func TestSDWebCredits(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth.user" {
			w.WriteHeader(404)
			return
		}
		if !strings.Contains(r.Header.Get("Cookie"), "session=s") {
			w.Write([]byte(`[{"result":{"data":{"json":null}}}]`))
			return
		}
		w.Write([]byte(`[{"result":{"data":{"json":{"email":"me@example.com","plan":"FREE","credits":40}}}}]`))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["sdweb"] = srv.URL
	a := readSDWeb(context.Background(), l, pasted("sdweb", "credits", "session=s"))
	if a.Status != StatusOK || *a.Windows[0].Left != 40 || a.Plan != "Free" {
		t.Fatalf("%+v", a)
	}
	// a browser that only visited: quiet, not a card
	c := Connection{ID: "sdweb-browser-credits", Provider: "sdweb", Mode: "credits", Way: "browser", Origin: OriginFound, Values: map[string]string{"cookie": "_ga=1"}}
	if a := readSDWeb(context.Background(), l, c); a.Status != StatusSignedOut || !a.Quiet || len(hideFailedWays([]Account{a})) != 0 {
		t.Fatalf("%+v", a)
	}
}

func TestManusAPIAndSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/usage.availableCredits":
			if r.Header.Get("x-manus-api-key") != "key" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"ok":true,"data":{"total_credits":1800,"free_credits":1000,"periodic_credits":0,"addon_credits":500,"pro_monthly_credits":0,"refresh_credits":300,"max_refresh_credits":300,"next_refresh_time":1791504000,"refresh_interval":"daily"}}`))
		case "/user.v1.UserService/GetAvailableCredits":
			if r.Header.Get("Authorization") != "Bearer sid" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"totalCredits":4300,"periodicCredits":3700,"proMonthlyCredits":4000,"refreshCredits":300,"maxRefreshCredits":300,"nextRefreshTime":1791504000,"refreshInterval":"daily","currentPeriodEnd":1793000000}`))
		}
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["manus.api"], l.URLs["manus.web"] = srv.URL, srv.URL
	a := readManus(context.Background(), l, Connection{Way: "api-key", Values: map[string]string{"apiKey": "key"}})
	if a.Status != StatusOK || a.Balances[0].Amount != "1800" || a.Windows[0].Name != "Refresh · day" || *a.Windows[0].Left != 300 || len(a.Groups[0].Balances) != 2 {
		t.Fatalf("%+v", a)
	}
	a = readManus(context.Background(), l, pasted("manus", "credits", "sid")) // a bare session_id
	if a.Status != StatusOK || a.Kind != KindSubscription || len(a.Windows) != 2 || *a.Windows[1].Left != 3700 || a.RenewsAt == nil {
		t.Fatalf("%+v", a)
	}
}

func TestPerplexityCreditsOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Cookie"), "__Secure-next-auth.session-token=t") {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`{"balance_cents":0,"renewal_date_ts":1793000000,"current_period_purchased_cents":0,"total_usage_cents":120000,
			"credit_grants":[{"type":"recurring","amount_cents":100000},{"type":"purchased","amount_cents":50000},{"type":"promotional","amount_cents":10000,"expires_at_ts":1791600000}]}`))
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["perplexity"] = srv.URL
	a := readPerplexity(context.Background(), l, pasted("perplexity", "credits", "t"))
	if a.Status != StatusOK || len(a.Windows) != 3 || *a.Windows[0].Left != 0 || *a.Windows[1].Left != 300 || *a.Windows[2].Left != 100 || !a.Windows[2].Expires {
		t.Fatalf("%+v", a.Windows)
	}
}

func TestPoeAndElevenLabs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/usage/current_balance":
			w.Write([]byte(`{"current_point_balance":2500}`))
		case "/usage/points_history":
			w.Write([]byte(`{"data":[{"bot_name":"GPT-5","cost_points":300,"creation_time":1791400000000000},{"bot_name":"Claude","cost_points":100,"creation_time":1791400000000}]}`))
		case "/v1/user/subscription":
			if r.Header.Get("xi-api-key") != "xi" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`{"tier":"free","character_count":2500,"character_limit":10000,"next_character_count_reset_unix":1793000000,"voice_slots_used":1,"voice_limit":3}`))
		}
	}))
	defer srv.Close()
	l := testLocal(t, nil)
	l.URLs["poe"], l.URLs["elevenlabs"] = srv.URL, srv.URL
	a := readPoe(context.Background(), l, Connection{Values: map[string]string{"apiKey": "k"}})
	if a.Status != StatusOK || a.Balances[0].Amount != "2500" || len(a.Groups) != 1 || a.Groups[0].Balances[0].Label != "GPT-5" {
		t.Fatalf("%+v", a)
	}
	a = readElevenLabs(context.Background(), l, Connection{Values: map[string]string{"apiKey": "xi"}})
	if a.Status != StatusOK || *a.Windows[0].Left != 7500 || a.RenewsAt == nil || !a.Windows[1].Aside {
		t.Fatalf("%+v", a)
	}
}

func TestFigmaAndStitchFollowTheRule(t *testing.T) {
	l := testLocal(t, nil)
	a := readFigma(context.Background(), l, Connection{Values: map[string]string{"plan": "starter"}})
	if a.Status != StatusOK || a.Live || len(a.Windows) != 2 || *a.Windows[0].Limit != 500 || a.Windows[0].Left != nil {
		t.Fatalf("%+v", a)
	}
	a = readFigma(context.Background(), l, Connection{Values: map[string]string{"plan": "professional", "credits": "3000", "since": "2026-01-31"}})
	if a.Kind != KindSubscription || len(a.Windows) != 1 || a.Windows[0].ResetsAt.Day() != 31 || a.RenewsAt == nil {
		t.Fatalf("%+v", a) // testNow is Oct 7: renews Oct 31
	}
	a = readStitch(context.Background(), l, Connection{Values: map[string]string{}})
	if *a.Windows[0].Limit != 400 || !a.Windows[0].ResetsAt.Equal(time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("%+v", a)
	}
}

// binaryCookies writes Safari's format for the given cookies.
func binaryCookies(cs []safariCookie) []byte {
	var page []byte
	page = binary.BigEndian.AppendUint32(page, 0x00000100)
	page = binary.LittleEndian.AppendUint32(page, uint32(len(cs)))
	header := 8 + 4*len(cs) + 4
	var recs [][]byte
	for _, c := range cs {
		strs := []string{c.host, c.name, "/", c.value}
		rec := make([]byte, 56)
		off := 56
		var tail []byte
		at := []int{16, 20, 24, 28}
		for i, s := range strs {
			binary.LittleEndian.PutUint32(rec[at[i]:], uint32(off+len(tail)))
			tail = append(tail, append([]byte(s), 0)...)
		}
		created := float64(c.access)/1e6 - 11644473600 - 978307200
		binary.LittleEndian.PutUint64(rec[48:], math.Float64bits(created))
		rec = append(rec, tail...)
		binary.LittleEndian.PutUint32(rec[0:], uint32(len(rec)))
		recs = append(recs, rec)
	}
	pos := header
	for _, r := range recs {
		page = binary.LittleEndian.AppendUint32(page, uint32(pos))
		pos += len(r)
	}
	page = append(page, 0, 0, 0, 0)
	for _, r := range recs {
		page = append(page, r...)
	}
	out := []byte("cook")
	out = binary.BigEndian.AppendUint32(out, 1)
	out = binary.BigEndian.AppendUint32(out, uint32(len(page)))
	return append(out, page...)
}

func TestSafariSessions(t *testing.T) {
	when := int64((float64(testNow.Unix()) + 11644473600) * 1e6)
	l := testLocal(t, nil)
	writeFile(t, l.safariStores()[0], string(binaryCookies([]safariCookie{
		{siteCookie{host: ".manus.im", name: "session_id", value: "sid"}, when},
		{siteCookie{host: "api.manus.im", name: "other", value: "x"}, when},
		{siteCookie{host: ".example.com", name: "session_id", value: "nope"}, when},
	})))
	s, err := l.findBrowserSession(context.Background(), "manus.im", "session_id")
	if err != nil || s.source != "Safari" || cookieValue(s.cookies, "session_id") != "sid" || len(s.cookies) != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	if c, err := l.findBrowserCookie(context.Background(), "manus.im", "session_id"); err != nil || c.value != "sid" {
		t.Fatalf("%+v %v", c, err)
	}
	if cs := manusSite.discover("credits")(l); len(cs) != 1 || cs[0].Source != "Safari" {
		t.Fatalf("%+v", cs)
	}
	if _, err := parseBinaryCookies([]byte("nope")); err == nil {
		t.Fatal("accepted a non-cookie file")
	}
}
