package config

import (
	"ant/server/internal/domain"
	"ant/server/internal/domain/resources"
	"ant/server/internal/providers/localtest"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestT1OriginalFourthConfig(t *testing.T) {
	b, e := os.ReadFile("../../../docs/evidence/CODEX-T1-REWORK3-AUDIT/config-cases.json")
	if e != nil {
		t.Fatal(e)
	}
	var cs []struct {
		Name     string
		Document json.RawMessage
		Expected bool
	}
	if e = json.Unmarshal(b, &cs); e != nil {
		t.Fatal(e)
	}
	cat, e := localtest.New()
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range cs {
		t.Run(c.Name, func(t *testing.T) {
			r := Compile(c.Document, cat)
			t.Logf("issues=%+v compiled=%+v", r.Issues, r.Compiled)
			if (len(r.Issues) == 0) != c.Expected {
				t.Fail()
			}
		})
	}
	t.Run("compiled-resource-has-resolved-scope", func(t *testing.T) {
		r := Compile(cs[0].Document, cat)
		if len(r.Issues) != 0 || r.Compiled == nil {
			t.Fatalf("invalid control: %+v", r.Issues)
		}
		for _, res := range r.Compiled.Resources {
			if res.ScopeID == "" {
				t.Errorf("%s physicalKey=%s has empty ScopeID", res.ResourceID, res.PhysicalKey)
			}
		}
	})
}
func TestT1OriginalFourthWindowEquivalence(t *testing.T) {
	t.Run("rolling-rule-reflexive", func(t *testing.T) {
		r := domain.WindowRule{Kind: "rolling", DurationMillis: 60000}
		if !windowsExecutableEqual(r, r) {
			t.Fatal("identical supported rolling rule rejected as unequal")
		}
	})
	t.Run("two-samples-do-not-prove-rule-equivalence", func(t *testing.T) {
		a := domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: "Africa/Casablanca"}
		b := domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: "Africa/Lagos"}
		now, _ := time.Parse(time.RFC3339, "2026-03-01T12:00:00Z")
		ka, _, _, ea := resources.WindowAt(domain.Millis(now.UnixMilli()), a, "same")
		kb, _, _, eb := resources.WindowAt(domain.Millis(now.UnixMilli()), b, "same")
		if ea != nil || eb != nil || ka == kb {
			t.Fatalf("bad witness a=%s b=%s errors=%v/%v", ka, kb, ea, eb)
		}
		t.Logf("March witness a=%s b=%s comparator=%v", ka, kb, windowsExecutableEqual(a, b))
		if windowsExecutableEqual(a, b) {
			t.Fatal("different March reset rules accepted as globally equal")
		}
	})
}
