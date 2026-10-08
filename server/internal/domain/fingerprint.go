package domain

// Semantic fingerprint (docs/design/03-EXECUTION.md §2): the dedup key of an
// operation. Same business semantics must always produce the same
// fingerprint; any change to business semantics must change it. Execution
// policy is deliberately excluded — deadline, maxAttempts, preferred
// affinity, allowMetered and trace are first-operation strategy fixed on
// first accept; re-submitting the same ID with different policy values can
// never become a different operation.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
)

// fingerprintSeed prefixes every fingerprint hash input so fingerprints from
// different hash generations can never collide (versioned domain separator).
// v2 (03-EXECUTION §2 freeze, T1 rework 2026-10-02): the document explicitly
// carries "capabilityProfile". v1 was never published with any persisted
// operation, so v1→v2 carries no replay-compatibility burden; from here on,
// any change to the hash input must bump the version and register the mapping
// in 03-EXECUTION §2.
const fingerprintSeed = "ant:semantic:v2\n"

// AffinityModeRequired marks an affinity as a hard placement constraint that
// participates in the fingerprint and in pool eligibility. "preferred" and
// any other mode are diagnostic only: they never enter the fingerprint and
// never filter candidates (07-POOLS-AND-CONFIG §3).
const AffinityModeRequired = "required"

var (
	ErrInvalidJSON     = errors.New("fingerprint: input is not valid JSON")
	ErrDuplicateKey    = errors.New("fingerprint: duplicate object key")
	ErrNonFiniteNumber = errors.New("fingerprint: non-finite number")
	// ErrNegativeZero: the canonical zero is "0"; "-0" and its relatives are
	// rejected the same way quantity parsing rejects them (quantity.go).
	ErrNegativeZero = errors.New("fingerprint: negative zero is not canonical, use 0")
	ErrTrailingData = errors.New("fingerprint: trailing data after JSON value")
)

// FingerprintInput is the complete fingerprint material. It intentionally has
// no Deadline, MaxAttempts, AllowMetered or trace field. (R10 freeze: the
// capability profile is part of the semantic material — a Router must pass
// DomainRequest.CapabilityProfile here; sameness is decided by this
// projection, not by callers smuggling the profile into the payload.)
type FingerprintInput struct {
	Domain string
	PoolID ID
	// CapabilityProfile selects the model/capability semantics that change
	// business output. Two requests differing only here MUST produce
	// different fingerprints; treating two profiles as semantically
	// equivalent is a DomainPolicy decision that must go through an explicit
	// normalization rule, never a missing field.
	CapabilityProfile ID
	// RequiredAffinity enters the fingerprint only when its Mode is
	// AffinityModeRequired (with accountId/bindingId as set). Nil or
	// preferred/other modes are excluded entirely.
	RequiredAffinity *Affinity
	// Payload is the normalized business content as raw JSON. Duplicate keys,
	// non-finite numbers and negative zero inside it are rejected — they are
	// canonicalization failures, not silent rewrites.
	Payload json.RawMessage
}

// SemanticFingerprint returns the hex sha256 of
// "ant:semantic:v2\n" + canonicalJSON over the fixed document shape
//
//	{"affinity":{...},"capabilityProfile":"...","domain":"...","payload":<canonical payload>,"poolId":"..."}
//
// with keys in sorted order (affinity < capabilityProfile < domain < payload
// < poolId). The affinity object is present only when RequiredAffinity has
// Mode == AffinityModeRequired and carries accountId / bindingId when
// non-empty plus the constant mode; preferred/nil affinity yields no affinity
// key at all. capabilityProfile is always present (possibly as the empty
// string) — two requests differing only in CapabilityProfile MUST produce
// different digests; declaring two profiles semantically equivalent is a
// DomainPolicy normalization decision, never a missing field. The payload is
// canonicalized independently, so duplicate keys, non-finite numbers and
// negative zero inside it are rejected with the CanonicalJSON sentinel
// errors.
func SemanticFingerprint(in FingerprintInput) (string, error) {
	payload, err := CanonicalJSON(in.Payload)
	if err != nil {
		return "", err
	}
	buf := make([]byte, 0, len(payload)+128)
	buf = append(buf, '{')
	if in.RequiredAffinity != nil && in.RequiredAffinity.Mode == AffinityModeRequired {
		aff := *in.RequiredAffinity // copy: never alias caller state
		// Keys below are fixed ASCII literals in sorted order
		// (accountId < bindingId < mode); empty ids are omitted.
		buf = append(buf, `"affinity":{`...)
		first := true
		comma := func() {
			if !first {
				buf = append(buf, ',')
			}
			first = false
		}
		if aff.AccountID != "" {
			comma()
			buf = append(buf, `"accountId":`...)
			buf = appendJSONString(buf, string(aff.AccountID))
		}
		if aff.BindingID != "" {
			comma()
			buf = append(buf, `"bindingId":`...)
			buf = appendJSONString(buf, string(aff.BindingID))
		}
		comma()
		buf = append(buf, `"mode":`...)
		buf = appendJSONString(buf, aff.Mode)
		buf = append(buf, '}', ',')
	}
	// Sorted key order: "capabilityProfile" sorts after "affinity" (or first,
	// when the affinity object is absent) and before "domain".
	buf = append(buf, `"capabilityProfile":`...)
	buf = appendJSONString(buf, string(in.CapabilityProfile))
	buf = append(buf, `"domain":`...)
	buf = appendJSONString(buf, in.Domain)
	buf = append(buf, `,"payload":`...)
	buf = append(buf, payload...)
	buf = append(buf, `,"poolId":`...)
	buf = appendJSONString(buf, string(in.PoolID))
	buf = append(buf, '}')
	sum := sha256.Sum256(append([]byte(fingerprintSeed), buf...))
	return hex.EncodeToString(sum[:]), nil
}

// CanonicalJSON renders v as canonical JSON bytes: object keys sorted
// bytewise (deterministic UTF-8 codepoint order), array order preserved, no
// whitespace, and Unicode kept verbatim — HTML characters and U+2028/2029 are
// NOT escaped; only quotes, backslashes and control characters are.
// Duplicate object keys, non-finite numbers (NaN/Inf literals and literals
// that overflow float64) and negatively signed zero are rejected with errors.
//
// json.RawMessage inputs are treated as pre-encoded JSON and re-validated
// token by token; any other value is first marshalled with encoding/json
// (which itself rejects NaN/Inf floats and unsupported types) and then
// canonicalized, so duplicates inside inlined RawMessage fields are still
// caught. Number literals are preserved verbatim after validation ("1.50"
// stays "1.50", distinct from "1.5") — textual number forms are part of the
// canonical form; only key order and whitespace are normalized.
func CanonicalJSON(v any) ([]byte, error) {
	var raw []byte
	switch t := v.(type) {
	case json.RawMessage:
		raw = t
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	out, err := appendCanonicalValue(nil, dec)
	if err != nil {
		return nil, err
	}
	// Exactly one value must remain: only whitespace may follow it.
	switch _, err := dec.Token(); {
	case err == io.EOF:
		// complete
	case err != nil:
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	default:
		return nil, ErrTrailingData
	}
	return out, nil
}

func appendCanonicalValue(dst []byte, dec *json.Decoder) ([]byte, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	return appendCanonicalToken(dst, dec, tok)
}

func appendCanonicalToken(dst []byte, dec *json.Decoder, tok json.Token) ([]byte, error) {
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			return appendCanonicalObject(dst, dec)
		case '[':
			return appendCanonicalArray(dst, dec)
		}
		return nil, fmt.Errorf("%w: unexpected delimiter %v", ErrInvalidJSON, t)
	case json.Number:
		if err := validateJSONNumber(t); err != nil {
			return nil, err
		}
		return append(dst, string(t)...), nil
	case string:
		return appendJSONString(dst, t), nil
	case bool:
		if t {
			return append(dst, "true"...), nil
		}
		return append(dst, "false"...), nil
	case nil:
		return append(dst, "null"...), nil
	default:
		return nil, fmt.Errorf("%w: unsupported token %T", ErrInvalidJSON, tok)
	}
}

func appendCanonicalObject(dst []byte, dec *json.Decoder) ([]byte, error) {
	var keys []string
	values := make(map[string][]byte)
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
		}
		key, ok := kt.(string)
		if !ok {
			return nil, fmt.Errorf("%w: object key is not a string", ErrInvalidJSON)
		}
		val, err := appendCanonicalValue(nil, dec)
		if err != nil {
			return nil, err
		}
		if _, dup := values[key]; dup {
			return nil, fmt.Errorf("%w: %q", ErrDuplicateKey, key)
		}
		keys = append(keys, key)
		values[key] = val
	}
	if _, err := dec.Token(); err != nil { // consume closing '}'
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	sort.Strings(keys)
	dst = append(dst, '{')
	for i, k := range keys {
		if i > 0 {
			dst = append(dst, ',')
		}
		dst = appendJSONString(dst, k)
		dst = append(dst, ':')
		dst = append(dst, values[k]...)
	}
	return append(dst, '}'), nil
}

func appendCanonicalArray(dst []byte, dec *json.Decoder) ([]byte, error) {
	dst = append(dst, '[')
	first := true
	for dec.More() {
		if !first {
			dst = append(dst, ',')
		}
		first = false
		var err error
		if dst, err = appendCanonicalValue(dst, dec); err != nil {
			return nil, err
		}
	}
	if _, err := dec.Token(); err != nil { // consume closing ']'
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	return append(dst, ']'), nil
}

// appendJSONString encodes s with HTML escaping disabled so Unicode content
// survives verbatim; Encode on a plain string cannot fail.
func appendJSONString(dst []byte, s string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	b := buf.Bytes()
	return append(dst, b[:len(b)-1]...) // trim the '\n' Encoder appends
}

// validateJSONNumber enforces the fingerprint number contract: finite
// (float64 range), not NaN/Inf, and never a negatively signed zero literal.
func validateJSONNumber(n json.Number) error {
	s := string(n)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		// ParseFloat ErrRange means the literal exceeds float64 range (±Inf).
		return fmt.Errorf("%w: %s", ErrNonFiniteNumber, s)
	}
	if f == 0 && isNegativeZeroLiteral(s) {
		return fmt.Errorf("%w: %s", ErrNegativeZero, s)
	}
	return nil
}

// isNegativeZeroLiteral reports whether s is a minus-signed literal whose
// mantissa digits are all zero ("-0", "-0.0", "-0e3"). Non-zero mantissae
// with tiny exponents ("-1e-400") are ordinary values, not negative zero.
func isNegativeZeroLiteral(s string) bool {
	if !strings.HasPrefix(s, "-") {
		return false
	}
	mant := s[1:]
	if i := strings.IndexAny(mant, "eE"); i >= 0 {
		mant = mant[:i]
	}
	for i := 0; i < len(mant); i++ {
		c := mant[i]
		if c == '.' {
			continue
		}
		if c != '0' {
			return false
		}
	}
	return true
}
