package quota

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io/fs"
	"math"
	"os"
	"strings"
)

// Safari keeps its cookies in Cookies.binarycookies inside its sandbox
// container, which macOS opens only to apps with Full Disk Access. Ant
// reads it when it may; otherwise Safari is skipped and the page says how
// to allow it (System Settings › Privacy & Security › Full Disk Access,
// for the app that runs Ant).

// errSafariLocked: the cookie file is there but macOS didn't open it.
var errSafariLocked = errors.New("Safari's sign-ins need Full Disk Access for the app that runs Ant (System Settings › Privacy & Security › Full Disk Access)")

// safariStores are where Safari keeps cookies, newest macOS first.
func (l Local) safariStores() []string {
	return []string{
		l.path("Library", "Containers", "com.apple.Safari", "Data", "Library", "Cookies", "Cookies.binarycookies"),
		l.path("Library", "Cookies", "Cookies.binarycookies"),
	}
}

// safariCookie is one Safari cookie; access is its creation time on
// Chromium's clock (µs since 1601), to compare with other browsers.
type safariCookie struct {
	siteCookie
	access int64
}

// readSafari reads every cookie Safari has, or errSafariLocked.
func (l Local) readSafari() ([]safariCookie, error) {
	locked := false
	for _, p := range l.safariStores() {
		b, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrPermission) {
			locked = true
			continue
		}
		if err != nil {
			continue
		}
		return parseBinaryCookies(b)
	}
	if locked {
		return nil, errSafariLocked
	}
	return nil, errNoCookie
}

// parseBinaryCookies reads the binarycookies format: "cook", a big-endian
// page count and page sizes, then pages of little-endian cookie records
// whose strings are at offsets from the record's start.
func parseBinaryCookies(b []byte) ([]safariCookie, error) {
	bad := errors.New("Safari's cookie file has a format Ant doesn't know")
	if len(b) < 8 || string(b[:4]) != "cook" {
		return nil, bad
	}
	n := int(binary.BigEndian.Uint32(b[4:8]))
	if 8+4*n > len(b) {
		return nil, bad
	}
	sizes := make([]int, n)
	for i := range sizes {
		sizes[i] = int(binary.BigEndian.Uint32(b[8+4*i:]))
	}
	off := 8 + 4*n
	var out []safariCookie
	for _, size := range sizes {
		if off+size > len(b) || size < 8 {
			return out, nil
		}
		page := b[off : off+size]
		off += size
		count := int(binary.LittleEndian.Uint32(page[4:8]))
		for i := 0; i < count && 8+4*i+4 <= len(page); i++ {
			start := int(binary.LittleEndian.Uint32(page[8+4*i:]))
			if start+56 > len(page) {
				continue
			}
			rec := page[start:]
			recSize := int(binary.LittleEndian.Uint32(rec[0:4]))
			if recSize < 56 || recSize > len(rec) {
				continue
			}
			rec = rec[:recSize]
			str := func(at int) string {
				o := int(binary.LittleEndian.Uint32(rec[at:]))
				if o <= 0 || o >= len(rec) {
					return ""
				}
				s := rec[o:]
				if j := bytes.IndexByte(s, 0); j >= 0 {
					s = s[:j]
				}
				return string(s)
			}
			created := math.Float64frombits(binary.LittleEndian.Uint64(rec[48:56])) // seconds since 2001
			out = append(out, safariCookie{
				siteCookie: siteCookie{host: str(16), name: str(20), value: str(28)},
				access:     int64((created + 978307200 + 11644473600) * 1e6),
			})
		}
	}
	return out, nil
}

// safariFor is Safari's cookies for domain, and the newest use of marker
// among them ("" for any cookie).
func safariFor(all []safariCookie, domain, marker string) ([]siteCookie, int64, bool) {
	d := strings.TrimPrefix(domain, ".")
	var out []siteCookie
	var best int64
	found := false
	for _, c := range all {
		h := strings.TrimPrefix(c.host, ".")
		if h != d && !strings.HasSuffix(h, "."+d) {
			continue
		}
		out = append(out, c.siteCookie)
		if (marker == "" || c.name == marker) && c.value != "" {
			found = true
			best = max(best, c.access)
		}
	}
	return out, best, found
}
