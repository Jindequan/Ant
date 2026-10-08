package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// volcToken is the token endpoint's answer: access_token holds the STS
// credentials as a JSON string.
type volcToken struct {
	AccessToken  string `json:"access_token"`
	ExpiresIn    int    `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

type volcSTS struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token"`
}

// volcPost posts a form to the sign-in service; an OAuth error comes back
// in the reply's error field, whatever the status.
func volcPost(ctx context.Context, l Local, path string, form url.Values, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.url("volc.signin", volcSignin)+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := l.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return json.NewDecoder(res.Body).Decode(dst)
}

// volcLogin runs the device-code sign-in and returns the refresh token and
// first STS credentials.
func volcLogin(ctx context.Context, l Local) (map[string]string, error) {
	w, release, err := beginSignIn()
	if err != nil {
		return nil, err
	}
	defer release()
	var auth struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
		URI        string `json:"verification_uri"`
		URIFull    string `json:"verification_uri_complete"`
		ExpiresIn  int    `json:"expires_in"`
		Interval   int    `json:"interval"`
		Error      string `json:"error"`
	}
	if err := volcPost(ctx, l, "/authorize/oauth/device_authorization", url.Values{
		"client_id": {volcClientID}, "scope": {volcScope}, "device_info": {"Ant"},
	}, &auth); err != nil || auth.DeviceCode == "" {
		return nil, errors.New("Volcengine didn't start a sign-in. Try again in a moment.")
	}
	page := auth.URIFull
	if page == "" {
		page = auth.URI
	}
	// the user confirms this code on Volcengine's page: Ant shows it too
	w.show(auth.UserCode, page)
	if err := openURL(page); err != nil {
		return nil, errors.New("couldn't open your browser for the sign-in")
	}
	interval := time.Duration(max(auth.Interval, 5)) * time.Second
	ctx, stop := context.WithTimeout(ctx, time.Duration(max(auth.ExpiresIn, 60))*time.Second)
	defer stop()
	for {
		select {
		case <-w.cancel:
			return nil, errSignInCancelled
		case <-ctx.Done():
			return nil, errors.New("the Volcengine sign-in expired before it was approved")
		case <-time.After(interval):
		}
		var tok volcToken
		if err := volcPost(ctx, l, "/authorize/oauth/token", url.Values{
			"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "client_id": {volcClientID},
			"scope": {volcScope}, "device_code": {auth.DeviceCode},
		}, &tok); err != nil {
			continue // a network blip: poll again
		}
		switch tok.Error {
		case "":
			return volcValues(tok, l.Now())
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied":
			return nil, errors.New("the sign-in was declined in the browser")
		default:
			return nil, fmt.Errorf("Volcengine: %s", strings.TrimSpace(tok.Error+" "+tok.Description))
		}
	}
}

// volcValues keeps a token answer as a connection's values.
func volcValues(tok volcToken, now time.Time) (map[string]string, error) {
	var sts volcSTS
	if json.Unmarshal([]byte(tok.AccessToken), &sts) != nil || sts.AccessKeyID == "" {
		return nil, errors.New("Volcengine's sign-in answered without credentials")
	}
	v := map[string]string{"sts": tok.AccessToken,
		"stsExpires": strconv.FormatInt(now.Add(time.Duration(max(tok.ExpiresIn, 60))*time.Second).Unix(), 10)}
	if tok.RefreshToken != "" {
		v["refreshToken"] = tok.RefreshToken
	}
	return v, nil
}

// volcCredentials are the keys to sign with: a static AccessKey, or STS
// credentials renewed with the refresh token when they are about to expire.
func volcCredentials(ctx context.Context, l Local, c Connection, a *Account) (id, secret, session string, ok bool) {
	// another tool's sign-in: its current keys, read fresh each time. Its
	// refresh token stays its own: renewing it here could sign that tool out.
	if p, tool, run := c.Values["veConfig"], "The Volcengine CLI", "ve login"; p != "" || c.Values["arkEnv"] != "" {
		var v map[string]string
		var found bool
		if p != "" {
			v, found = volcCLIConfig(p)
		} else {
			tool, run = "arkcli", "arkcli login"
			v, found = arkCLIEnv(c.Values["arkEnv"])
		}
		exp, _ := strconv.ParseInt(v["stsExpires"], 10, 64)
		switch {
		case !found || v["accessKeyId"] == "":
			a.Status, a.Message = StatusSignedOut, tool+" is signed out. Run "+run+"."
			return "", "", "", false
		case v["sessionToken"] != "" && exp > 0 && time.Unix(exp, 0).Before(l.Now()):
			a.Status, a.Message = StatusSignedOut, tool+"'s short-lived keys have expired. Run any "+strings.Fields(run)[0]+" command to renew them, or "+run+"."
			return "", "", "", false
		}
		return v["accessKeyId"], v["secretAccessKey"], v["sessionToken"], true
	}
	if c.Values["accessKeyId"] != "" && c.Values["secretAccessKey"] != "" {
		return c.Values["accessKeyId"], c.Values["secretAccessKey"], c.Values["sessionToken"], true
	}
	stsJSON := c.Values["sts"]
	exp, _ := strconv.ParseInt(c.Values["stsExpires"], 10, 64)
	if stsJSON == "" || time.Unix(exp, 0).Before(l.Now().Add(time.Minute)) {
		if c.Values["refreshToken"] == "" {
			a.Status, a.Message = StatusSignedOut, "Sign in with Volcengine to read the Coding Plan."
			return "", "", "", false
		}
		var tok volcToken
		err := volcPost(ctx, l, "/authorize/oauth/token", url.Values{
			"grant_type": {"refresh_token"}, "client_id": {volcClientID}, "scope": {volcScope}, "refresh_token": {c.Values["refreshToken"]},
		}, &tok)
		if err != nil {
			a.Status, a.Message = StatusError, "Couldn't renew the Volcengine sign-in: "+short(err.Error())
			return "", "", "", false
		}
		if tok.Error != "" {
			a.Status, a.Message = StatusSignedOut, "Your Volcengine sign-in has ended. Sign in again."
			return "", "", "", false
		}
		v, err := volcValues(tok, l.Now())
		if err != nil {
			a.Status, a.Message = StatusError, short(err.Error())
			return "", "", "", false
		}
		a.Renewed = v
		stsJSON = v["sts"]
	}
	var sts volcSTS
	if json.Unmarshal([]byte(stsJSON), &sts) != nil {
		a.Status, a.Message = StatusSignedOut, "Sign in with Volcengine again."
		return "", "", "", false
	}
	return sts.AccessKeyID, sts.SecretAccessKey, sts.SessionToken, true
}
