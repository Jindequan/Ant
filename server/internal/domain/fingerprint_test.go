package domain

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"testing"
)

func mustCanonical(t *testing.T, v any) string {
	t.Helper()
	b, err := CanonicalJSON(v)
	if err != nil {
		t.Fatalf("CanonicalJSON(%v): unexpected error: %v", v, err)
	}
	return string(b)
}

func TestCanonicalJSONSortsKeysKeepsArrayOrder(t *testing.T) {
	got := mustCanonical(t, json.RawMessage(`{ "b" : 1 , "a" : [2, 1] }`))
	if got != `{"a":[2,1],"b":1}` {
		t.Fatalf("canonical form mismatch: got %s", got)
	}
	// Array order is semantic and must not be sorted.
	got = mustCanonical(t, json.RawMessage(`[3,1,2]`))
	if got != `[3,1,2]` {
		t.Fatalf("array order not preserved: got %s", got)
	}
}

func TestCanonicalJSONPreservesUnicodeAndHTML(t *testing.T) {
	got := mustCanonical(t, json.RawMessage(`{"名":"值<>&"}`))
	if got != `{"名":"值<>&"}` {
		t.Fatalf("unicode/HTML not preserved verbatim: got %s", got)
	}
	// Escaped and literal forms of the same string are the same semantics.
	a := mustCanonical(t, json.RawMessage(`{"s":"é"}`))
	b := mustCanonical(t, json.RawMessage(`{"s":"\u00e9"}`))
	if a != b {
		t.Fatalf("escaped vs literal unicode differ: %s vs %s", a, b)
	}
}

func TestCanonicalJSONKeyOrderAndWhitespaceIrrelevant(t *testing.T) {
	a := mustCanonical(t, json.RawMessage(`{"x":{"q":1,"p":2},"y":[{"z":3}]}`))
	b := mustCanonical(t, json.RawMessage("{\n  \"y\" : [ {\"z\":3} ],\n  \"x\" : {\"p\":2, \"q\":1}\n}"))
	if a != b {
		t.Fatalf("same semantics produced different canonical forms:\n%s\n%s", a, b)
	}
}

func TestCanonicalJSONRejectsDuplicateKeys(t *testing.T) {
	if _, err := CanonicalJSON(json.RawMessage(`{"a":1,"a":2}`)); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("want ErrDuplicateKey, got %v", err)
	}
	// Duplicates nested inside marshalled values are still caught.
	if _, err := CanonicalJSON(map[string]any{"p": json.RawMessage(`{"a":1,"a":2}`)}); !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("nested duplicate key not caught, got %v", err)
	}
}

func TestCanonicalJSONRejectsNonFiniteAndNegativeZero(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want error
	}{
		{"NaN literal", `{"x":NaN}`, ErrInvalidJSON},
		{"Infinity literal", `{"x":Infinity}`, ErrInvalidJSON},
		{"float64 overflow", `{"x":1e400}`, ErrNonFiniteNumber},
		{"negative overflow", `{"x":-1e400}`, ErrNonFiniteNumber},
		{"negative zero", `{"x":-0}`, ErrNegativeZero},
		{"negative zero exponent", `{"x":-0.0e10}`, ErrNegativeZero},
	}
	for _, tc := range cases {
		if _, err := CanonicalJSON(json.RawMessage(tc.in)); !errors.Is(err, tc.want) {
			t.Fatalf("%s: want %v, got %v", tc.name, tc.want, err)
		}
	}
	// Go float NaN/Inf values are rejected at the marshal stage.
	if _, err := CanonicalJSON(map[string]any{"x": math.NaN()}); err == nil {
		t.Fatalf("NaN float input must fail")
	}
	if _, err := CanonicalJSON(map[string]any{"x": math.Inf(1)}); err == nil {
		t.Fatalf("Inf float input must fail")
	}
}

func TestCanonicalJSONScalarsAndTrailing(t *testing.T) {
	if got := mustCanonical(t, nil); got != "null" {
		t.Fatalf("nil must canonicalize to null, got %s", got)
	}
	if got := mustCanonical(t, json.RawMessage(`true`)); got != "true" {
		t.Fatalf("top-level scalar unsupported: got %s", got)
	}
	if _, err := CanonicalJSON(json.RawMessage(`{"a":1} {"b":2}`)); !errors.Is(err, ErrTrailingData) {
		t.Fatalf("want ErrTrailingData, got %v", err)
	}
	if _, err := CanonicalJSON(json.RawMessage(``)); !errors.Is(err, ErrInvalidJSON) {
		t.Fatalf("empty input must be invalid JSON, got %v", err)
	}
}

func fingerprintFor(t *testing.T, in FingerprintInput) string {
	t.Helper()
	fp, err := SemanticFingerprint(in)
	if err != nil {
		t.Fatalf("SemanticFingerprint: unexpected error: %v", err)
	}
	return fp
}

func TestSemanticFingerprintSameSemanticsSameFingerprint(t *testing.T) {
	base := FingerprintInput{
		Domain:  "ai/text",
		PoolID:  "pool-1",
		Payload: json.RawMessage(`{"model":"glm-6","messages":[{"role":"user","content":"hi"}],"temperature":0.7}`),
	}
	reordered := FingerprintInput{
		Domain:  "ai/text",
		PoolID:  "pool-1",
		Payload: json.RawMessage(`{"temperature":0.7 , "messages":[ { "content":"hi" , "role":"user" } ], "model":"glm-6"}`),
	}
	if fingerprintFor(t, base) != fingerprintFor(t, reordered) {
		t.Fatal("same semantics with different key order/whitespace must share a fingerprint")
	}
}

func TestSemanticFingerprintDifferentSemanticsDifferentFingerprint(t *testing.T) {
	base := FingerprintInput{
		Domain:  "ai/text",
		PoolID:  "pool-1",
		Payload: json.RawMessage(`{"a":1}`),
	}
	type mutate func(*FingerprintInput)
	mutations := map[string]mutate{
		"payload field": func(f *FingerprintInput) { f.Payload = json.RawMessage(`{"a":2}`) },
		"poolId":        func(f *FingerprintInput) { f.PoolID = "pool-2" },
		"domain":        func(f *FingerprintInput) { f.Domain = "email/send" },
		"required account": func(f *FingerprintInput) {
			f.RequiredAffinity = &Affinity{Mode: AffinityModeRequired, AccountID: "acc-1"}
		},
	}
	baseFP := fingerprintFor(t, base)
	for name, m := range mutations {
		alt := base
		m(&alt)
		if fingerprintFor(t, alt) == baseFP {
			t.Fatalf("mutation %q must change the fingerprint", name)
		}
	}
	// Required affinity binding change also changes it.
	withB1 := base
	withB1.RequiredAffinity = &Affinity{Mode: AffinityModeRequired, BindingID: "b1"}
	withB2 := withB1
	withB2.RequiredAffinity = &Affinity{Mode: AffinityModeRequired, BindingID: "b2"}
	if fingerprintFor(t, withB1) == fingerprintFor(t, withB2) {
		t.Fatal("different required bindings must produce different fingerprints")
	}
}

// R10: the capability profile is part of the semantic material. Two requests
// differing only in CapabilityProfile must produce different digests — the
// profile is an explicit input field, not something callers smuggle into the
// payload. Same input (including profile) must stay deterministic, and the
// digest must be stable across payload key order.
func TestSemanticFingerprintCapabilityProfileParticipates(t *testing.T) {
	base := FingerprintInput{
		Domain:            "ai/text",
		CapabilityProfile: "profile.chat.basic",
		Payload:           json.RawMessage(`{"model":"glm-6","prompt":"same"}`),
	}
	baseFP := fingerprintFor(t, base)
	if fingerprintFor(t, base) != baseFP {
		t.Fatal("same input (including profile) must be deterministic")
	}

	// The exact probe scenario from the Codex audit: identical domain, pool
	// and payload, different capabilityProfile.
	other := base
	other.CapabilityProfile = "profile.chat.reasoning"
	if fingerprintFor(t, other) == baseFP {
		t.Fatal("different capabilityProfile must produce a different fingerprint")
	}

	// Empty vs set profile is a semantic difference too.
	empty := base
	empty.CapabilityProfile = ""
	if fingerprintFor(t, empty) == baseFP {
		t.Fatal("empty vs set capabilityProfile must produce different fingerprints")
	}

	// Profile participation is not masked by payload key order: reordered
	// payloads with the same profile still collide, reordered payloads with
	// different profiles still differ.
	reordered := FingerprintInput{
		Domain:            "ai/text",
		CapabilityProfile: "profile.chat.basic",
		Payload:           json.RawMessage(`{"prompt":"same" , "model":"glm-6"}`),
	}
	if fingerprintFor(t, reordered) != baseFP {
		t.Fatal("payload key order must not change the fingerprint")
	}
	otherReordered := reordered
	otherReordered.CapabilityProfile = "profile.chat.reasoning"
	if fingerprintFor(t, otherReordered) == fingerprintFor(t, reordered) {
		t.Fatal("profile difference must survive payload key reordering")
	}
}

func TestSemanticFingerprintPolicyFieldsExcluded(t *testing.T) {
	// FingerprintInput has no policy fields by construction; assert the
	// affinity-mode matrix instead: none→preferred unchanged, preferred→required changed.
	base := FingerprintInput{Domain: "d", PoolID: "p", Payload: json.RawMessage(`{"a":1}`)}
	noneFP := fingerprintFor(t, base)

	preferred := base
	preferred.RequiredAffinity = &Affinity{Mode: "preferred", AccountID: "acc-1"}
	preferredFP := fingerprintFor(t, preferred)
	if preferredFP != noneFP {
		t.Fatal("preferred affinity must not enter the fingerprint (none→preferred unchanged)")
	}

	required := base
	required.RequiredAffinity = &Affinity{Mode: AffinityModeRequired, AccountID: "acc-1"}
	if fingerprintFor(t, required) == preferredFP {
		t.Fatal("preferred→required must change the fingerprint")
	}
	if fingerprintFor(t, required) == noneFP {
		t.Fatal("required affinity must enter the fingerprint")
	}
}

func TestSemanticFingerprintPayloadValidationErrors(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    error
	}{
		{"duplicate key", `{"a":1,"a":2}`, ErrDuplicateKey},
		{"negative zero", `{"a":-0}`, ErrNegativeZero},
		{"NaN", `{"a":NaN}`, ErrInvalidJSON},
		{"Inf overflow", `{"a":1e999}`, ErrNonFiniteNumber},
	}
	for _, tc := range cases {
		in := FingerprintInput{Domain: "d", PoolID: "p", Payload: json.RawMessage(tc.payload)}
		if _, err := SemanticFingerprint(in); !errors.Is(err, tc.want) {
			t.Fatalf("%s: want %v, got %v", tc.name, tc.want, err)
		}
	}
}

func TestSemanticFingerprintFormat(t *testing.T) {
	fp := fingerprintFor(t, FingerprintInput{Domain: "d", PoolID: "p", Payload: json.RawMessage(`{}`)})
	if len(fp) != 64 {
		t.Fatalf("fingerprint must be 64-char sha256 hex, got %q (len %d)", fp, len(fp))
	}
	for _, c := range fp {
		if !bytes.ContainsRune([]byte("0123456789abcdef"), c) {
			t.Fatalf("non-hex character %q in %q", c, fp)
		}
	}
}

func TestSemanticFingerprintDoesNotAliasAffinity(t *testing.T) {
	aff := &Affinity{Mode: AffinityModeRequired, BindingID: "b1"}
	in := FingerprintInput{Domain: "d", PoolID: "p", RequiredAffinity: aff, Payload: json.RawMessage(`{}`)}
	fp1 := fingerprintFor(t, in)
	aff.BindingID = "b2" // mutate after hashing
	if fingerprintFor(t, in) == fp1 {
		t.Fatal("input affinity mutation after first call must be visible: input was aliased")
	}
	if !reflect.DeepEqual(Affinity{Mode: AffinityModeRequired, BindingID: "b2"}, *aff) {
		t.Fatal("sanity: affinity mutation lost")
	}
}
