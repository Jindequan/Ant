package config

// Regression for Q4 review findings: P1 (empty capabilityProfiles panic) and
// P2-1 (profile domain matching).

import (
	"encoding/json"
	"testing"

	"ant/server/internal/providers/localtest"
)

func TestQ4EmptyCapabilityProfilesNoPanic(t *testing.T) {
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	loadCorpus(t, "control-completed-verified-fact", &doc)
	bindings := doc["bindings"].([]any)
	bindings[0].(map[string]any)["capabilityProfiles"] = []any{}
	blob, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	res := Validate(blob, cat)
	if len(res.Issues) == 0 {
		t.Fatal("empty capabilityProfiles must carry issues (schema/graph), never silently pass")
	}
	// no panic reaching this point is the P1 assertion
}

func TestQ4ProfileDomainMismatchRejected(t *testing.T) {
	cat, err := localtest.New()
	if err != nil {
		t.Fatal(err)
	}
	// the descriptor has one profile (profile.chat.basic, domain ai/text);
	// craft a binding+offer pair on a DIFFERENT domain referencing it
	var doc map[string]any
	loadCorpus(t, "control-completed-verified-fact", &doc)
	bindings := doc["bindings"].([]any)
	bindings[0].(map[string]any)["capabilityProfiles"] = []any{"profile.chat.basic"}
	// add a second binding on an unsupported domain referencing the profile
	bindings = append(bindings, map[string]any{
		"bindingId": "bind-alpha-other", "accountId": "acct-demo-1",
		"domain": "mail/send", "endpoint": "https://fixture.invalid/v2",
		"credentialRef": "secret-alpha-key", "credentialRevision": "rev-1",
		"enabled": true, "capabilityProfiles": []any{"profile.chat.basic"},
	})
	doc["bindings"] = bindings
	blob, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	res := Validate(blob, cat)
	if !hasIssue(res.Issues, "ant.config.unsupportedDomain") {
		t.Fatalf("unsupported domain must be flagged: %+v", res.Issues)
	}
	// offer on the wrong domain with the registered profile
	offers := doc["offers"].([]any)
	offers[0].(map[string]any)["domain"] = "mail/send"
	blob, _ = json.Marshal(doc)
	res = Validate(blob, cat)
	if !hasIssue(res.Issues, "ant.config.profileDomainMismatch") {
		t.Fatalf("profile/domain mismatch must be rejected: %+v", res.Issues)
	}
}
