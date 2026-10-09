package quota

import "testing"

func TestNetworkFailuresSayWhatHappened(t *testing.T) {
	for in, want := range map[string]string{
		`Couldn't read Copilot: Get "https://api.github.com/copilot_internal/user": net/http: TLS handshake timeout`: "Couldn't reach api.github.com: the connection timed out. Check your network or proxy.",
		`Get "https://api.z.ai/api/x": dial tcp: lookup api.z.ai: no such host`:                                      "Couldn't reach api.z.ai: its address couldn't be found. Check your network or proxy.",
		`proxyconnect tcp: dial tcp 127.0.0.1:7890: connect: connection refused`:                                     "Couldn't reach the provider: the proxy didn't answer. Check the proxy (HTTPS_PROXY).",
		"Bailian answered 500": "Bailian answered 500",
	} {
		if got := FriendlyNetError(in); got != want {
			t.Errorf("%q\n got %q\nwant %q", in, got, want)
		}
	}
}
