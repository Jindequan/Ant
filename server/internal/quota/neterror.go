package quota

import (
	"net/url"
	"regexp"
	"strings"
)

// Network failures say what happened in words, not Go's error text.

var netFailures = []struct{ marker, says string }{ // first match wins: the proxy before what it caused
	{"proxyconnect", "the proxy didn't answer"},
	{"TLS handshake timeout", "the connection timed out"},
	{"i/o timeout", "the connection timed out"},
	{"context deadline exceeded", "it took too long to answer"},
	{"Client.Timeout exceeded", "it took too long to answer"},
	{"no such host", "its address couldn't be found"},
	{"connection refused", "the connection was refused"},
	{"connection reset by peer", "the connection was cut"},
	{"network is unreachable", "there's no network"},
}

var urlInText = regexp.MustCompile(`https?://[^\s"']+`)

// FriendlyNetError rewrites a message carrying a network failure as
// "Couldn't reach api.github.com: the connection timed out. Check your
// network or proxy."; any other message is returned as it is.
func FriendlyNetError(msg string) string {
	for _, f := range netFailures {
		if !strings.Contains(msg, f.marker) {
			continue
		}
		where := "the provider"
		if u, err := url.Parse(urlInText.FindString(msg)); err == nil && u.Host != "" {
			where = u.Host
		}
		hint := " Check your network or proxy."
		if f.marker == "proxyconnect" {
			hint = " Check the proxy (HTTPS_PROXY)."
		}
		return "Couldn't reach " + where + ": " + f.says + "." + hint
	}
	return msg
}
