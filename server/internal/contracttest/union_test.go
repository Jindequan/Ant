package contracttest

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
)

// R05 probe regression: the OpenAPI union itself (not only the Go guard)
// must reject illegal kind/state combinations.
func TestOpenAPIUnionRejectsAcceptedWithTerminal(t *testing.T) {
	loader := openapi3.NewLoader()
	spec, err := loader.LoadFromFile("../../../contracts/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(fixtures + "/rejection.accepted-with-terminal.json")
	if err != nil {
		t.Fatal(err)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatal(err)
	}
	if err := spec.Components.Schemas["SubmitReply"].Value.VisitJSON(v); err == nil {
		t.Fatal("OpenAPI union must reject accepted+succeeded")
	}
	// happy paths still validate through the union
	for _, name := range []string{"operation.submit.accepted.response.json", "operation.replay.response.json"} {
		blob, err := os.ReadFile(fixtures + "/" + name)
		if err != nil {
			t.Fatal(err)
		}
		var hv any
		if err := json.Unmarshal(blob, &hv); err != nil {
			t.Fatal(err)
		}
		if err := spec.Components.Schemas["SubmitReply"].Value.VisitJSON(hv); err != nil {
			t.Fatalf("%s must validate: %v", name, err)
		}
	}
}
