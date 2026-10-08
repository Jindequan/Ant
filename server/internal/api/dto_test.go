package api

import (
	"strings"
	"testing"

	"ant/server/internal/domain"
)

func mustDecode(t *testing.T, data string, v any) {
	t.Helper()
	if err := DecodeStrict([]byte(data), v); err != nil {
		t.Fatalf("DecodeStrict unexpected error: %v", err)
	}
}

func mustFailDecode(t *testing.T, data string, v any) {
	t.Helper()
	if err := DecodeStrict([]byte(data), v); err == nil {
		t.Fatalf("DecodeStrict expected error for %s", data)
	}
}

func TestDecodeStrictAcceptsCleanInput(t *testing.T) {
	var in domain.ExecuteInput
	mustDecode(t, `{"operationId":"op-1","poolId":"p","request":{"domain":"ai/text","capabilityProfile":"c","payload":{"b":1,"a":2}},"deadlineAt":5,"maxAttempts":2,"allowMetered":false}`, &in)
	if in.OperationID != "op-1" || in.MaxAttempts != 2 {
		t.Fatalf("decode mismatch: %+v", in)
	}
}

func TestDecodeStrictRejectsUnknownFields(t *testing.T) {
	var in domain.ExecuteInput
	mustFailDecode(t, `{"operationId":"op-1","poolId":"p","request":{"domain":"d","capabilityProfile":"c","payload":{}},"deadlineAt":5,"maxAttempts":1,"allowMetered":false,"trace":"x"}`, &in)
}

func TestDecodeStrictRejectsDuplicateKeys(t *testing.T) {
	var in domain.ExecuteInput
	mustFailDecode(t, `{"operationId":"op-1","operationId":"op-2","poolId":"p","request":{"domain":"d","capabilityProfile":"c","payload":{}},"deadlineAt":5,"maxAttempts":1,"allowMetered":false}`, &in)
	// nested duplicate key too
	mustFailDecode(t, `{"operationId":"op-1","poolId":"p","request":{"domain":"d","domain":"x","capabilityProfile":"c","payload":{}},"deadlineAt":5,"maxAttempts":1,"allowMetered":false}`, &in)
}

func TestDecodeStrictRejectsTrailingData(t *testing.T) {
	var in domain.ExecuteInput
	mustFailDecode(t, `{"operationId":"op-1","poolId":"p","request":{"domain":"d","capabilityProfile":"c","payload":{}},"deadlineAt":5,"maxAttempts":1,"allowMetered":false} {}`, &in)
}

func TestQuantityStringCodec(t *testing.T) {
	type payload struct {
		N domain.Units `json:"n,string"`
	}
	var p payload
	mustDecode(t, `{"n":"42"}`, &p)
	if p.N != 42 {
		t.Fatalf("want 42 got %d", p.N)
	}
	// bare JSON number must be rejected
	mustFailDecode(t, `{"n":42}`, &p)
	// float form rejected
	mustFailDecode(t, `{"n":"4.2"}`, &p)
	// overflow rejected
	mustFailDecode(t, `{"n":"99999999999999999999"}`, &p)
	// negative fine
	mustDecode(t, `{"n":"-5"}`, &p)
	if p.N != -5 {
		t.Fatalf("want -5 got %d", p.N)
	}
	// R04: canonical spelling enforced at the wire boundary itself —
	// "-0"/"010" never reach a rule as silently rewritten values.
	mustFailDecode(t, `{"n":"-0"}`, &p)
	mustFailDecode(t, `{"n":"010"}`, &p)
	mustFailDecode(t, `{"n":null}`, &p)
}

func TestEncodeCanonicalStable(t *testing.T) {
	a, err := EncodeCanonical(map[string]string{"b": "2", "a": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(a); got != `{"a":"1","b":"2"}` {
		t.Fatalf("canonical mismatch: %s", got)
	}
}

func TestValidateSubmitReply(t *testing.T) {
	acc := &domain.SubmitReply{Kind: "accepted", State: domain.Running, OperationID: "op-1", OperationURL: "/v1/operations/op-1", EventsURL: "/v1/operations/op-1/events"}
	if err := ValidateSubmitReply(acc); err != nil {
		t.Fatalf("accepted should validate: %v", err)
	}
	bad := *acc
	bad.State = domain.Succeeded // accepted-with-terminal must fail (K6)
	if err := ValidateSubmitReply(&bad); err == nil {
		t.Fatal("accepted with terminal state must fail")
	}
	rep := &domain.SubmitReply{Kind: "replay", OperationID: "op-1", Result: &domain.OperationResult{State: domain.Succeeded}}
	if err := ValidateSubmitReply(rep); err != nil {
		t.Fatalf("replay should validate: %v", err)
	}
	repBad := &domain.SubmitReply{Kind: "replay", OperationID: "op-1", Result: &domain.OperationResult{State: domain.Running}}
	if err := ValidateSubmitReply(repBad); err == nil {
		t.Fatal("replay with running result must fail")
	}
	// cross-layer alignment cases (review P2-2): rules must match web validate.ts
	repNoTop := &domain.SubmitReply{Kind: "replay", OperationID: "op-1", Result: &domain.OperationResult{State: domain.Cancelled}}
	if err := ValidateSubmitReply(repNoTop); err != nil {
		t.Fatalf("replay without top-level state must pass: %v", err)
	}
	repMismatch := &domain.SubmitReply{Kind: "replay", OperationID: "op-1", State: domain.Succeeded, Result: &domain.OperationResult{State: domain.Cancelled}}
	if err := ValidateSubmitReply(repMismatch); err == nil {
		t.Fatal("replay top state must match result state")
	}
	inpBad := &domain.SubmitReply{Kind: "inProgress", OperationID: "op-1", Operation: &domain.OperationView{State: domain.Succeeded}}
	if err := ValidateSubmitReply(inpBad); err == nil {
		t.Fatal("inProgress with non-running operation must fail")
	}
	inp := &domain.SubmitReply{Kind: "inProgress", OperationID: "op-1", Operation: &domain.OperationView{State: domain.Running}}
	if err := ValidateSubmitReply(inp); err != nil {
		t.Fatalf("inProgress running should validate: %v", err)
	}
	rej := &domain.SubmitReply{Kind: "rejected", OperationID: "op-1", Error: &domain.ErrorDTO{Code: "OperationIdConflict"}}
	if err := ValidateSubmitReply(rej); err != nil {
		t.Fatalf("rejected should validate: %v", err)
	}
	if err := ValidateSubmitReply(&domain.SubmitReply{Kind: "wat"}); err == nil {
		t.Fatal("unknown kind must fail")
	}
}

func TestValidateOperationResultEnum(t *testing.T) {
	if err := ValidateOperationResult(&domain.OperationResult{State: domain.Unknown}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateOperationResult(&domain.OperationResult{State: "partially_succeeded"}); err == nil {
		t.Fatal("unknown enum must fail")
	}
	if err := ValidateOperationResult(&domain.OperationResult{State: domain.Running}); err == nil {
		t.Fatal("running result must fail")
	}
}

func TestValidateIfMatch(t *testing.T) {
	if err := ValidateIfMatch(`"3"`, 3); err != nil {
		t.Fatalf("quoted header should match: %v", err)
	}
	err := ValidateIfMatch("4", 3)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("mismatch must fail, got %v", err)
	}
	if err := ValidateIfMatch("", 3); err == nil {
		t.Fatal("missing If-Match must fail")
	}
	for _, header := range []string{`W/"3"`, `W/3`, `"3`, `3"`, `03`, `+3`, `3, "4"`, `*`} {
		t.Run(header, func(t *testing.T) {
			if err := ValidateIfMatch(header, 3); err == nil {
				t.Fatalf("invalid or weak If-Match %q must fail", header)
			}
		})
	}
}
