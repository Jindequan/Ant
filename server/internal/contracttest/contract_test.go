// Package contracttest validates the versioned wire contract (K6): the
// OpenAPI spec must parse, and every API fixture must validate against its
// component schema. Rejection fixtures prove the codec/rules refuse illegal
// wire shapes. The same fixture files are consumed by the Web test suite.
package contracttest

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"ant/server/internal/api"
	"ant/server/internal/application"
	"ant/server/internal/domain"
	"ant/server/internal/jsonx"
)

const fixtures = "../../../contracts/fixtures/api"

func TestFixtureManifestVersionMatchesOpenAPI(t *testing.T) {
	spec := loadSpec(t)
	data, err := os.ReadFile(fixtures + "/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if spec.Info.Version != manifest.Version {
		t.Fatalf("OpenAPI version %q != fixture manifest version %q", spec.Info.Version, manifest.Version)
	}
}

func loadSpec(t *testing.T) *openapi3.T {
	t.Helper()
	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromFile("../../../contracts/openapi.yaml")
	if err != nil {
		t.Fatalf("load openapi.yaml: %v", err)
	}
	if err := spec.Validate(loader.Context); err != nil {
		t.Fatalf("openapi.yaml invalid: %v", err)
	}
	return spec
}

func readJSON(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(fixtures + "/" + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return v
}

func mustValidateSchema(t *testing.T, spec *openapi3.T, schemaName string, value any, fixture string) {
	t.Helper()
	schemaRef := spec.Components.Schemas[schemaName]
	if schemaRef == nil {
		t.Fatalf("component schema %s missing", schemaName)
	}
	schema := schemaRef.Value
	if err := schema.VisitJSON(value); err != nil {
		t.Errorf("%s does not match %s: %v", fixture, schemaName, err)
	}
}

func TestHappyPathFixturesMatchOpenAPI(t *testing.T) {
	spec := loadSpec(t)

	cases := []struct {
		fixture string
		schema  string
	}{
		{"operation.submit.request.json", "ExecuteInput"},
		{"operation.submit.accepted.response.json", "SubmitReply"},
		{"operation.replay.response.json", "SubmitReply"},
		{"operation.conflict.response.json", "ErrorEnvelope"},
		{"error.envelope.json", "ErrorEnvelope"},
		{"pool.order.request.json", "ReorderPoolInput"},
		{"pool.order.response.json", "PoolView"},
		{"pool.order.conflict.response.json", "ErrorEnvelope"},
		{"operation.cancel.ack.response.json", "CancellationAck"},
		{"operation.view.json", "OperationView"},
		{"operation.output.page.json", "OutputPage"},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			mustValidateSchema(t, spec, tc.schema, readJSON(t, tc.fixture), tc.fixture)
		})
	}

	// events are an array of OperationEvent
	data, err := os.ReadFile(fixtures + "/operation.events.json")
	if err != nil {
		t.Fatal(err)
	}
	var arr []map[string]any
	if err := json.Unmarshal(data, &arr); err != nil {
		t.Fatal(err)
	}
	if len(arr) == 0 {
		t.Fatal("events fixture must be non-empty")
	}
	for _, ev := range arr {
		mustValidateSchema(t, spec, "OperationEvent", ev, "operation.events.json")
	}
}

func TestDeepSeekBalanceWireShapesMatchOpenAPI(t *testing.T) {
	spec := loadSpec(t)
	request := api.DeepSeekBalanceObserveInput{CommandID: "balance-command-1", CredentialRef: "credential-1", CredentialRevision: "credential-revision-1"}
	requestJSON, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	mustValidateSchema(t, spec, "DeepSeekBalanceObserveInput", readJSONBytes(t, requestJSON), "DeepSeekBalanceObserveInput")
	var decoded api.DeepSeekBalanceObserveInput
	if err := jsonx.DecodeStrict(requestJSON, &decoded); err != nil || decoded != request {
		t.Fatalf("strict request decode = %#v, %v", decoded, err)
	}
	if err := jsonx.DecodeStrict(append(requestJSON[:len(requestJSON)-1], []byte(`,"apiKey":"must-not-accept"}`)...), &decoded); err == nil {
		t.Fatal("balance request must reject cleartext provider credentials")
	}
	response := application.DeepSeekBalanceObservation{IsAvailable: true, ObservedAt: 1_800_000_000_000, Balances: []application.DeepSeekAccountBalance{{Currency: "USD", TotalCents: 1250, GrantedCents: 250, ToppedUpCents: 1000}}}
	responseJSON, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	mustValidateSchema(t, spec, "DeepSeekBalanceObservation", readJSONBytes(t, responseJSON), "DeepSeekBalanceObservation")
	if spec.Paths.Find("/v1/providers/deepseek/balance-observations") == nil || spec.Paths.Find("/v1/providers/deepseek/balance-observations/{commandId}") == nil {
		t.Fatal("balance observe and receipt read paths must both be described")
	}
}

func readJSONBytes(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestConsumptionBasisDomainValuesMatchOpenAPI(t *testing.T) {
	spec := loadSpec(t)
	resourceOutcome := spec.Components.Schemas["ResourceOutcome"]
	if resourceOutcome == nil || resourceOutcome.Value == nil {
		t.Fatal("ResourceOutcome schema is missing")
	}
	field := resourceOutcome.Value.Properties["consumptionBasis"]
	if field == nil || field.Value == nil {
		t.Fatal("ResourceOutcome.consumptionBasis schema is missing")
	}

	want := make([]string, 0)
	for _, basis := range domain.ConsumptionBasisValues() {
		want = append(want, string(basis))
		outcome := map[string]any{
			"resourceId":       "resource",
			"windowKey":        "window",
			"metricId":         "metric",
			"unit":             "unit",
			"consumed":         "0",
			"consumptionBasis": string(basis),
			"occupancy":        "released",
			"evidenceRefs":     []any{},
		}
		if err := resourceOutcome.Value.VisitJSON(outcome); err != nil {
			t.Errorf("domain consumption basis %q does not satisfy ResourceOutcome: %v", basis, err)
		}
	}

	got := make([]string, 0, len(field.Value.Enum))
	for _, raw := range field.Value.Enum {
		value, ok := raw.(string)
		if !ok {
			t.Fatalf("consumptionBasis enum includes non-string value %T", raw)
		}
		got = append(got, value)
	}
	sort.Strings(want)
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("consumptionBasis OpenAPI enum = %v, domain values = %v", got, want)
	}
}

func TestUnrepresentableMetricAndNullConsumptionMatchOpenAPI(t *testing.T) {
	spec := loadSpec(t)
	result := spec.Components.Schemas["OperationResult"]
	if result == nil || result.Value == nil {
		t.Fatal("OperationResult schema is missing")
	}
	metric := map[string]any{
		"metricId": "tokens", "unit": "token",
		"actualOutOfRangeDecimal": "9223372036854775808",
		"evidenceRefs":            []any{"provider-report"},
	}
	if err := result.Value.VisitJSON(map[string]any{
		"operationId": "op", "state": "unknown", "code": "UnknownOutcome",
		"reasons":     []any{map[string]any{"group": "accounting", "code": "actual_out_of_range"}},
		"retrySafety": "undetermined", "attempts": []any{"attempt"},
		"resources": []any{map[string]any{
			"resourceId": "counter", "windowKey": "lifetime", "metricId": "tokens", "unit": "token",
			"consumed": nil, "consumptionBasis": "unrepresentable", "occupancy": "released", "evidenceRefs": []any{"provider-report"},
		}},
		"unrepresentableMetrics": []any{metric}, "decisionRefs": []any{},
		"outputAvailability": "none", "suggestedAction": "review_usage",
	}); err != nil {
		t.Fatalf("unrepresentable accounting result does not satisfy OpenAPI: %v", err)
	}
}

// K6 union discipline: the accepted fixture must satisfy ValidateSubmitReply;
// replay carries a terminal result.
func TestSubmitReplyUnionRules(t *testing.T) {
	acc := readJSON(t, "operation.submit.accepted.response.json")
	if acc["state"] != string(domain.Running) {
		t.Fatalf("accepted fixture must carry state=running, got %v", acc["state"])
	}
	rep := readJSON(t, "operation.replay.response.json")
	if rep["kind"] != "replay" {
		t.Fatalf("replay fixture kind mismatch: %v", rep["kind"])
	}
	// K6: the accepted-with-terminal rejection must fail union validation
	bad := readJSON(t, "rejection.accepted-with-terminal.json")
	var sr domain.SubmitReply
	blob, _ := json.Marshal(bad)
	if err := json.Unmarshal(blob, &sr); err != nil {
		t.Fatal(err)
	}
	if err := api.ValidateSubmitReply(&sr); err == nil {
		t.Fatal("accepted with terminal state must be rejected")
	}
}

func TestRejectionFixtures(t *testing.T) {
	spec := loadSpec(t)

	t.Run("quantity-overflow", func(t *testing.T) {
		data, err := os.ReadFile(fixtures + "/rejection.quantity-overflow.json")
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Delta domain.Units `json:"delta,string"`
		}
		if err := jsonx.DecodeStrict(data, &wire); err == nil {
			t.Fatal("int64 overflow quantity must fail decoding")
		}
		// schema layer must reject it too (Quantity pattern bounds 19 digits)
		spec := loadSpec(t)
		mustFailSchema(t, spec, "AdjustmentInput", readJSON(t, "rejection.quantity-overflow.json"))
	})

	t.Run("quantity-leading-zero", func(t *testing.T) {
		// P3-1: the fixture file now has a named Go consumer on both layers.
		data, err := os.ReadFile(fixtures + "/rejection.quantity-leading-zero.json")
		if err != nil {
			t.Fatal(err)
		}
		var wire struct {
			Delta domain.Units `json:"delta,string"`
		}
		if err := jsonx.DecodeStrict(data, &wire); err == nil {
			t.Fatal("leading-zero quantity must fail decoding")
		}
		spec := loadSpec(t)
		mustFailSchema(t, spec, "AdjustmentInput", readJSON(t, "rejection.quantity-leading-zero.json"))
	})

	t.Run("unknown-state-enum", func(t *testing.T) {
		bad := readJSON(t, "rejection.unknown-state.json")
		blob, _ := json.Marshal(bad)
		var res domain.OperationResult
		if err := json.Unmarshal(blob, &res); err != nil {
			t.Fatal(err)
		}
		if err := api.ValidateOperationResult(&res); err == nil {
			t.Fatal("illegal state enum must be rejected")
		}
		// and the schema itself must reject it as well
		mustFailSchema(t, spec, "OperationResult", bad)
	})

	t.Run("command-id-body-conflict", func(t *testing.T) {
		data, err := os.ReadFile(fixtures + "/rejection.command-id-conflict.json")
		if err != nil {
			t.Fatal(err)
		}
		var two []domain.ReorderPoolInput
		if err := json.Unmarshal(data, &two); err != nil {
			t.Fatal(err)
		}
		if len(two) != 2 || two[0].CommandID != two[1].CommandID {
			t.Fatalf("fixture must be two entries sharing one commandId")
		}
		h1 := mustHash(t, two[0])
		h2 := mustHash(t, two[1])
		if h1 == h2 {
			t.Fatal("same commandId with different bodies must produce different hashes (receipt conflict)")
		}
	})

	t.Run("ifmatch-body-mismatch", func(t *testing.T) {
		var in domain.ReorderPoolInput
		data, err := os.ReadFile(fixtures + "/rejection.ifmatch-mismatch.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &in); err != nil {
			t.Fatal(err)
		}
		// header claims 4, body says 3 → explicit rejection, never override
		if err := api.ValidateIfMatch("4", in.ExpectedPoolVersion); err == nil {
			t.Fatal("If-Match/body version conflict must be rejected")
		}
		if err := api.ValidateIfMatch("3", in.ExpectedPoolVersion); err != nil {
			t.Fatalf("matching header should pass: %v", err)
		}
	})

	t.Run("duplicate-json-key", func(t *testing.T) {
		data, err := os.ReadFile(fixtures + "/rejection.duplicate-key.json.txt")
		if err != nil {
			t.Fatal(err)
		}
		var v map[string]any
		if err := jsonx.DecodeStrict(data, &v); err == nil {
			t.Fatal("duplicate keys must fail strict decoding")
		}
	})
}

func mustFailSchema(t *testing.T, spec *openapi3.T, schemaName string, value any) {
	t.Helper()
	schema := spec.Components.Schemas[schemaName].Value
	if err := schema.VisitJSON(value); err == nil {
		t.Errorf("schema %s should reject the value", schemaName)
	}
}

func mustHash(t *testing.T, in domain.ReorderPoolInput) string {
	t.Helper()
	b, err := jsonx.EncodeCanonical(in)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
