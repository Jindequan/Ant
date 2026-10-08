package quota

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha1"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"ant/server/internal/quota/cookiestore"
)

// A website with no app keeps its sign-in only in the browser. Ant reads
// that cookie from the browser's own cookie store on this machine, as the
// user's browser would send it: Chromium browsers encrypt it with a key in
// the macOS Keychain (one "Always Allow" the first time), Firefox doesn't.
// Safari's store needs Full Disk Access and isn't read.

type chromium struct{ name, dir, keychain string }

var chromiumBrowsers = []chromium{
	{"Chrome", "Google/Chrome", "Chrome Safe Storage"},
	{"Arc", "Arc/User Data", "Arc Safe Storage"},
	{"Brave", "BraveSoftware/Brave-Browser", "Brave Safe Storage"},
	{"Edge", "Microsoft Edge", "Microsoft Edge Safe Storage"},
	{"Vivaldi", "Vivaldi", "Vivaldi Safe Storage"},
	{"Chromium", "Chromium", "Chromium Safe Storage"},
}

// browserCookie is one cookie found in a browser profile.
type browserCookie struct {
	value  string
	source string // "Chrome · Default"
	access int64  // last used, µs since 1601, to prefer the profile in use
}

var errNoCookie = cookiestore.ErrNoCookie

// keychainPassword reads a Chromium browser's cookie key from the macOS
// Keychain. Tests replace it.
var keychainPassword = func(service string) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("reading browser cookies needs macOS")
	}
	out, err := exec.Command("security", "find-generic-password", "-w", "-s", service).Output()
	if err != nil {
		return "", errors.New("the Keychain didn't allow reading " + service)
	}
	return strings.TrimSpace(string(out)), nil
}

var (
	keyMu   sync.Mutex
	keyMemo = map[string][]byte{} // per Keychain item, for this process's life
)

func chromiumKey(service string) ([]byte, error) {
	keyMu.Lock()
	defer keyMu.Unlock()
	if k, ok := keyMemo[service]; ok {
		return k, nil
	}
	pw, err := keychainPassword(service)
	if err != nil {
		return nil, err
	}
	k, err := pbkdf2.Key(sha1.New, pw, []byte("saltysalt"), 1003, 16)
	if err != nil {
		return nil, err
	}
	keyMemo[service] = k
	return k, nil
}

// findBrowserCookie looks in every browser profile for the cookie name on
// a host ending in domain and returns the most recently used one.
func (l Local) findBrowserCookie(ctx context.Context, domain, name string) (browserCookie, error) {
	var found []browserCookie
	var lastErr error
	support := l.path("Library", "Application Support")
	for _, b := range chromiumBrowsers {
		root := filepath.Join(support, b.dir)
		var files []string
		for _, pattern := range []string{"*/Cookies", "*/Network/Cookies"} {
			m, _ := filepath.Glob(filepath.Join(root, pattern))
			files = append(files, m...)
		}
		for _, f := range files {
			c, err := readChromiumCookie(ctx, f, b.keychain, domain, name)
			if err != nil {
				if !errors.Is(err, errNoCookie) {
					lastErr = err
				}
				continue
			}
			c.source = b.name + " · " + profileName(root, f)
			found = append(found, c)
		}
	}
	ff, _ := filepath.Glob(filepath.Join(support, "Firefox", "Profiles", "*", "cookies.sqlite"))
	for _, f := range ff {
		if c, err := readFirefoxCookie(ctx, f, domain, name); err == nil {
			c.source = "Firefox · " + filepath.Base(filepath.Dir(f))
			found = append(found, c)
		}
	}
	if all, err := l.readSafari(); err == nil {
		for _, c := range all {
			h := strings.TrimPrefix(c.host, ".")
			d := strings.TrimPrefix(domain, ".")
			if c.name == name && c.value != "" && (h == d || strings.HasSuffix(h, "."+d)) {
				found = append(found, browserCookie{value: c.value, source: "Safari", access: c.access})
			}
		}
	}
	if len(found) == 0 {
		if lastErr != nil {
			return browserCookie{}, lastErr
		}
		return browserCookie{}, errNoCookie
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].access > found[j].access })
	return found[0], nil
}

// siteCookie is one cookie of a signed-in site.
type siteCookie struct {
	host, name, value string
}

// browserSession is every cookie a browser profile holds for a site, from
// the profile where the user signed in most recently.
type browserSession struct {
	cookies []siteCookie
	source  string
}

// header is the Cookie header the browser would send to host.
func (s browserSession) header(host string) string {
	var parts []string
	for _, c := range s.cookies {
		h := strings.TrimPrefix(c.host, ".")
		if host == h || strings.HasSuffix(host, "."+h) {
			parts = append(parts, c.name+"="+c.value)
		}
	}
	return strings.Join(parts, "; ")
}

// findBrowserSession finds the profile whose marker cookie on domain ("" for
// any cookie there) was used last, and reads all of that profile's cookies for domain. Sites sign
// in with several cookies, some HttpOnly; the marker only says a sign-in
// is there.
func (l Local) findBrowserSession(ctx context.Context, domain, marker string) (browserSession, error) {
	type profile struct {
		file, keychain, source string
		firefox                bool
		access                 int64
	}
	var best *profile
	var lastErr error
	support := l.path("Library", "Application Support")
	for _, b := range chromiumBrowsers {
		root := filepath.Join(support, b.dir)
		var files []string
		for _, pattern := range []string{"*/Cookies", "*/Network/Cookies"} {
			m, _ := filepath.Glob(filepath.Join(root, pattern))
			files = append(files, m...)
		}
		for _, f := range files {
			c, err := readChromiumCookie(ctx, f, b.keychain, domain, marker)
			if err != nil {
				if !errors.Is(err, errNoCookie) {
					lastErr = err
				}
				continue
			}
			if best == nil || c.access > best.access {
				best = &profile{file: f, keychain: b.keychain, source: b.name + " · " + profileName(root, f), access: c.access}
			}
		}
	}
	ff, _ := filepath.Glob(filepath.Join(support, "Firefox", "Profiles", "*", "cookies.sqlite"))
	for _, f := range ff {
		if c, err := readFirefoxCookie(ctx, f, domain, marker); err == nil && (best == nil || c.access > best.access) {
			best = &profile{file: f, source: "Firefox · " + filepath.Base(filepath.Dir(f)), firefox: true, access: c.access}
		}
	}
	if all, err := l.readSafari(); err == nil {
		if cookies, access, ok := safariFor(all, domain, marker); ok && (best == nil || access > best.access) {
			return browserSession{cookies: cookies, source: "Safari"}, nil
		}
	}
	if best == nil {
		if lastErr != nil {
			return browserSession{}, lastErr
		}
		return browserSession{}, errNoCookie
	}
	var cookies []siteCookie
	var err error
	if best.firefox {
		cookies, err = readFirefoxSite(ctx, best.file, domain)
	} else {
		keychain := best.keychain
		cookies, err = readSiteCookies(ctx, best.file, domain, func() ([]byte, error) { return chromiumKey(keychain) })
	}
	if err != nil {
		return browserSession{}, err
	}
	return browserSession{cookies: cookies, source: best.source}, nil
}

// readSiteCookies reads a Chromium cookie store's cookies for domain,
// decrypting them with the key key gives.
func readSiteCookies(ctx context.Context, file, domain string, key func() ([]byte, error)) ([]siteCookie, error) {
	rows, version, err := cookiestore.ChromiumSite(ctx, file, domain)
	if err != nil {
		return nil, err
	}
	var out []siteCookie
	for _, r := range rows {
		c := siteCookie{host: r.Host, name: r.Name, value: r.Value}
		if c.value == "" && len(r.Encrypted) > 0 {
			k, err := key()
			if err != nil {
				return nil, err
			}
			if c.value, err = decryptChromium(r.Encrypted, k, version); err != nil {
				continue // one unreadable cookie doesn't spoil the session
			}
		}
		out = append(out, c)
	}
	return out, nil
}

func readFirefoxSite(ctx context.Context, file, domain string) ([]siteCookie, error) {
	rows, err := cookiestore.FirefoxSite(ctx, file, domain)
	if err != nil {
		return nil, err
	}
	out := make([]siteCookie, 0, len(rows))
	for _, r := range rows {
		out = append(out, siteCookie{host: r.Host, name: r.Name, value: r.Value})
	}
	return out, nil
}

func profileName(root, file string) string {
	rel, err := filepath.Rel(root, file)
	if err != nil {
		return "profile"
	}
	return strings.Split(rel, string(filepath.Separator))[0]
}

// readChromiumCookie returns the cookie's value and when it was last used,
// in µs since 1601 like every browserCookie.access.
func readChromiumCookie(ctx context.Context, file, keychain, domain, name string) (browserCookie, error) {
	r, version, err := cookiestore.ChromiumLatest(ctx, file, domain, name)
	if err != nil {
		return browserCookie{}, err
	}
	value := r.Value
	if value == "" && len(r.Encrypted) > 0 {
		key, err := chromiumKey(keychain)
		if err != nil {
			return browserCookie{}, err
		}
		if value, err = decryptChromium(r.Encrypted, key, version); err != nil {
			return browserCookie{}, err
		}
	}
	if value == "" {
		return browserCookie{}, errNoCookie
	}
	return browserCookie{value: value, access: r.Access}, nil
}

// decryptChromium undoes macOS Chromium's "v10" cookie encryption:
// AES-128-CBC, IV of 16 spaces, PKCS#7 padding. From cookie database
// version 24 the plaintext starts with the SHA-256 of the cookie's host.
func decryptChromium(enc, key []byte, version int) (string, error) {
	if !bytes.HasPrefix(enc, []byte("v10")) {
		return "", errors.New("browser cookie uses an encryption Ant doesn't read")
	}
	enc = enc[3:]
	if len(enc) == 0 || len(enc)%aes.BlockSize != 0 {
		return "", errors.New("browser cookie is damaged")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	out := make([]byte, len(enc))
	cipher.NewCBCDecrypter(block, bytes.Repeat([]byte{' '}, aes.BlockSize)).CryptBlocks(out, enc)
	pad := int(out[len(out)-1])
	if pad == 0 || pad > aes.BlockSize || pad > len(out) {
		return "", errors.New("browser cookie couldn't be decrypted")
	}
	out = out[:len(out)-pad]
	if version >= 24 {
		if len(out) < 32 {
			return "", errors.New("browser cookie couldn't be decrypted")
		}
		out = out[32:]
	}
	return string(out), nil
}

func readFirefoxCookie(ctx context.Context, file, domain, name string) (browserCookie, error) {
	r, err := cookiestore.FirefoxLatest(ctx, file, domain, name)
	if err != nil {
		return browserCookie{}, err
	}
	return browserCookie{value: r.Value, access: r.Access}, nil
}

// parseCookieHeader reads a pasted Cookie header ("a=1; b=2") as cookies of
// domain.
func parseCookieHeader(h, domain string) []siteCookie {
	var out []siteCookie
	for _, part := range strings.Split(strings.TrimPrefix(strings.TrimSpace(h), "Cookie:"), ";") {
		if k, v, ok := strings.Cut(strings.TrimSpace(part), "="); ok && k != "" {
			out = append(out, siteCookie{host: domain, name: k, value: v})
		}
	}
	return out
}
