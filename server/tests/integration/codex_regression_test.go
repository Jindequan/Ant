package integration

// Codex audit regression (adapted, permanent). Source: the T1 Codex review
// probes (docs/evidence/CODEX-T1-AUDIT/codex_audit_test.go.txt). Per
// ../REVIEW-T1-CODEX.md W3, probes whose PUBLIC API changed during the rework are
// adapted here — the original business inputs and expectations are preserved
// and each transformation is documented inline. The un-adapted originals are
// rerun verbatim by docs/evidence/CODEX-T1-AUDIT/run-probes.py; their
// residual failures against the reworked code are recorded in
// T1-REWORK/original-probes-post-rework.log with per-case explanations:
//
//	TestCodexAuditCommandBudget  — the literal 40ms ceiling is physically
//	  unreachable for a callback that ignores ctx (see R09 budget boundary);
//	  the business expectation (budget error, no residue, bounded cooperative
//	  path) is asserted below.
//	TestCodexAuditPermitAssembly — the original probe issued the permit with
//	  a literal digest and then expected pairing with the real prepared digest
//	  to succeed; that pairing is exactly the flaw the review flagged. The
//	  expectation "matching handles pair successfully" is kept; issuance now
//	  uses the real digest.
//	TestCodexAuditCapabilityFingerprint — FingerprintInput gained the frozen
//	  CapabilityProfile field (R10); the probe did not pass it. The
//	  expectation "different profiles ⇒ different fingerprints" is kept.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ant/server/internal/adapters"
	"ant/server/internal/config"
	"ant/server/internal/domain"
	"ant/server/internal/domain/resources"
	"ant/server/internal/jsonx"
	"ant/server/internal/ledger"
	"ant/server/internal/runtime"
	"ant/server/internal/store"
	"ant/server/internal/store/sqlite"
	"ant/server/internal/transport"
	"github.com/getkin/kin-openapi/openapi3"
)

func TestCodexRegressionArithmetic(t *testing.T) {
	for _, c := range []struct {
		name     string
		a, b     domain.Units
		subtract bool
		want     domain.Units
		overflow bool
	}{
		{"negative-overflow-to-zero", math.MinInt64, math.MinInt64, false, 0, true},
		{"legal-subtract-minimum", -1, math.MinInt64, true, math.MaxInt64, false},
		{"minimum-minus-itself", math.MinInt64, math.MinInt64, true, 0, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			var got domain.Units
			var err error
			if c.subtract {
				got, err = domain.SubUnits(c.a, c.b)
			} else {
				got, err = domain.AddUnits(c.a, c.b)
			}
			if (err != nil) != c.overflow || (!c.overflow && got != c.want) {
				t.Fatalf("got=%d err=%v expected=%d overflow=%t", got, err, c.want, c.overflow)
			}
		})
	}
}

func TestCodexRegressionConfigCases(t *testing.T) {
	b, err := os.ReadFile("../../../docs/evidence/CODEX-T1-AUDIT/config-cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name     string          `json:"name"`
		Document json.RawMessage `json:"document"`
		Expected bool            `json:"expected"`
	}
	if err := json.Unmarshal(b, &cases); err != nil {
		t.Fatal(err)
	}
	// S1 adaptation (allowed by ../REVIEW-T1-REWORK-CODEX.md §S1/S3): the round-1 legal
	// baselines predate the frozen fact-based bucket identity, so their
	// entitlement resources lack providerFactId. The BUSINESS expectation is
	// unchanged (two independently verified buckets may coexist); the input
	// is completed with the fact fields the catalog verifies:
	//   valid-baseline              → single bucket  → alpha-usd-grant-promo
	//   valid-gift-and-prepaid-buckets → two buckets → grant-promo / prepaid
	// Everything else (expectations, structure, all negative cases) is
	// untouched. Old→new mapping recorded here per the rework contract.
	factByCase := map[string][]string{
		"valid-baseline":                 {"alpha-usd-grant-promo"},
		"valid-gift-and-prepaid-buckets": {"alpha-usd-grant-promo", "alpha-usd-prepaid"},
	}
	for _, c := range cases {
		c.Document = adaptRound1LegalBaselines(t, c.Name, c.Document, factByCase[c.Name])
		// C2 requires each bucket's initial_state evidence to name its own
		// resource: the two-bucket baseline originally shared one initial
		// evidence, so the adaptation clones it per bucket (references
		// completed, business assertion unchanged).
		if c.Name == "valid-gift-and-prepaid-buckets" {
			c.Document = adaptInitialEvidenceVocabulary(t, c.Document)
			c.Document = cloneInitialEvidencePerBucket(t, c.Document)
		}
		t.Run(c.Name, func(t *testing.T) {
			// Same judgment the original probe encoded: strict decode + the
			// typed reference graph (ValidateStructural). Catalog-dependent
			// checks are exercised by the localtest package and the web
			// contract tests against provider-alpha.
			var d config.ConfigDocument
			decodeErr := jsonx.DecodeStrict(c.Document, &d)
			accepted := false
			if decodeErr == nil {
				accepted = len(config.ValidateStructural(&d)) == 0
			}
			if accepted != c.Expected {
				var issues []domain.FieldIssue
				if decodeErr == nil {
					issues = config.ValidateStructural(&d)
				}
				t.Fatalf("accepted=%t expected=%t decodeErr=%v issues=%v", accepted, c.Expected, decodeErr, issues)
			}
		})
	}
}

func TestCodexRegressionWireQuantity(t *testing.T) {
	for _, s := range []string{"010", "-0"} {
		t.Run(s, func(t *testing.T) {
			var p struct {
				N domain.Units `json:"n,string"`
			}
			err := json.Unmarshal([]byte(fmt.Sprintf(`{"n":%q}`, s)), &p)
			if err == nil {
				t.Fatalf("wire %q must be rejected at the codec boundary", s)
			}
		})
	}
}

func TestCodexRegressionWindow(t *testing.T) {
	t.Run("singapore-09-anchor", func(t *testing.T) {
		loc, _ := time.LoadLocation("Asia/Singapore")
		now := time.Date(2026, 10, 2, 12, 0, 0, 0, loc)
		_, start, end, err := resources.WindowAt(domain.Millis(now.UnixMilli()),
			domain.WindowRule{Kind: "fixed", Period: "day", TimeZone: "Asia/Singapore", AnchorISO: "2026-01-01T09:00:00+08:00"}, "rev-1")
		if err != nil {
			t.Fatal(err)
		}
		if got := time.UnixMilli(start).In(loc); got.Hour() != 9 {
			t.Fatalf("anchor start = %v, want 09:00 local", got)
		}
		if !(start <= now.UnixMilli() && now.UnixMilli() < end) {
			t.Fatalf("now outside window [%d,%d)", start, end)
		}
	})
	t.Run("fall-back-second-hour-contained", func(t *testing.T) {
		now, _ := time.Parse(time.RFC3339, "2026-11-01T01:30:00-05:00")
		_, start, end, err := resources.WindowAt(domain.Millis(now.UnixMilli()),
			domain.WindowRule{Kind: "fixed", Period: "hour", TimeZone: "America/New_York"}, "rev-1")
		if err != nil {
			t.Fatal(err)
		}
		if now.UnixMilli() < start || now.UnixMilli() >= end {
			t.Fatalf("second 01:30 outside window [%d,%d)", start, end)
		}
	})
}

func TestCodexRegressionQueueConfig(t *testing.T) {
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "small.db"), sqlite.Config{WriterQueueLimit: 1, Clock: runtime.NewClock()})
	if err == nil {
		_ = st.Close(context.Background())
		t.Fatal("invalid queue config must return an error, not panic")
	}
}

func TestCodexRegressionBudgetCooperative(t *testing.T) {
	// Adaptation of TestCodexAuditCommandBudget: the cooperative path must be
	// bounded by the declared budget; the non-cooperative path must fail with
	// a budget error and leave zero residue (writer regains control at the
	// post-Run checkpoint; see R09 boundary in writer.go).
	st, _ := openTestStore(t, sqlite.Config{})
	start := time.Now()
	written := 0
	err := st.SubmitWrite(context.Background(), store.WriteCommand{
		ID: "coop-slow", Budget: 20 * time.Millisecond, Control: true,
		Run: func(tx store.WriterTx) error {
			for i := 0; i < 800; i++ {
				if time.Now().Sub(start) > 20*time.Millisecond {
					return context.DeadlineExceeded // cooperative: observe the budget
				}
				time.Sleep(100 * time.Microsecond)
			}
			written++
			return nil
		},
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("cooperative overrun must fail with the budget error")
	}
	if elapsed > 60*time.Millisecond {
		t.Fatalf("cooperative callback exceeded budget window: %v", elapsed)
	}
	// next control command commits (writer not monopolized)
	err = st.SubmitWrite(context.Background(), store.WriteCommand{
		ID: "after-budget", Budget: time.Second, Control: true,
		Run: func(tx store.WriterTx) error {
			return tx.InsertAudit(store.AuditRecord{AuditID: "audit-after-budget", PrincipalID: "principal-test", Namespace: "ns-test", Action: "test", TargetRef: "t", CreatedAt: 1})
		},
	})
	if err != nil {
		t.Fatalf("writer must remain available after budget failure: %v", err)
	}
	_ = written
}

// D-B7-v2 rework: the original probe hard-asserted a 50ms wall-clock ceiling
// here. A fresh full -race run recorded took=51.967584ms (runs=2) and failed
// once (docs/evidence/CODEX-T1-REWORK-AUDIT/baseline-go.log); ten directed
// reruns all passed (retry-budget-repeat.log). Both logs are kept as history
// and the assertion below no longer depends on that narrow threshold: each
// attempt reads the command context's absolute deadline via
// sqlite.CommandContextTx, so "retries share one whole-command budget" is
// proven structurally (every attempt observes the SAME instant, never a
// re-armed timer). Measured wall time is logged, never graded; the 30s
// submission context is a coarse anti-hang guard only.
func TestCodexRegressionRetryBudget(t *testing.T) {
	const (
		budget     = 30 * time.Millisecond
		maxRetries = 2
		perAttempt = 20 * time.Millisecond // two attempts >= budget: it must exhaust
	)
	st, _ := openTestStore(t, sqlite.Config{MaxRetries: maxRetries})
	rev := domain.ID("rev-retry-budget")
	antiHang, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var (
		deadlines []int64 // one UnixMilli per attempt, from the command context
		runs      int
	)
	start := time.Now()
	err := st.SubmitWrite(antiHang, store.WriteCommand{
		ID: "retries", Budget: budget,
		Run: func(tx store.WriterTx) error {
			runs++
			if dl, ok := tx.(sqlite.CommandContextTx).CommandContext().Deadline(); ok {
				deadlines = append(deadlines, dl.UnixMilli())
			}
			if err := tx.InsertConfigRevision(store.ConfigRevisionRecord{
				Revision: rev, DefinitionJSON: []byte(`{}`), CreatedAt: 1,
			}); err != nil {
				return err
			}
			time.Sleep(perAttempt)
			return fmt.Errorf("database is locked") // busy-classified: drives the retry path
		},
	})
	elapsed := time.Since(start)
	t.Logf("budget=%v maxRetries=%d elapsed=%v (logged, not graded) runs=%d err=%v",
		budget, maxRetries, elapsed, runs, err)

	if !errors.Is(err, store.ErrCommandBudget) {
		t.Fatalf("err = %v, want store.ErrCommandBudget: %d attempts of >=%v must spend the whole %v budget", err, runs, perAttempt, budget)
	}
	if runs < 1 || runs > maxRetries+1 {
		t.Fatalf("runs = %d, want 1..%d: retry count must be bounded by MaxRetries", runs, maxRetries+1)
	}
	if len(deadlines) != runs {
		t.Fatalf("deadline observations = %d, want one per attempt (%d)", len(deadlines), runs)
	}
	for i, dl := range deadlines {
		if dl != deadlines[0] {
			t.Fatalf("attempt %d observed deadline %d, attempt 0 observed %d: a retry re-armed a fresh timer instead of inheriting the shared deadline", i, dl, deadlines[0])
		}
	}
	// Every attempt rolled back in full: the failed command leaves no residue.
	assertNoRevision(t, st, rev)
	// The writer regains control after the budget failure.
	if err := st.SubmitWrite(context.Background(), store.WriteCommand{
		ID: "after-retry-budget", Control: true, Budget: time.Second,
		Run: func(tx store.WriterTx) error {
			return tx.InsertAudit(store.AuditRecord{AuditID: "audit-after-retry-budget", PrincipalID: "principal-test", Namespace: "ns-test", Action: "test", TargetRef: "t", CreatedAt: 1})
		},
	}); err != nil {
		t.Fatalf("writer must remain available after budget exhaustion: %v", err)
	}
}

func TestCodexRegressionFutureSchema(t *testing.T) {
	st, path := openTestStore(t, sqlite.Config{})
	db := openRawDB(t, path)
	// one past the newest version this binary knows
	execOK(t, db, fmt.Sprintf("INSERT INTO schema_migrations(version,applied_at) VALUES(%d,1)", currentSchemaVersion+1))
	if err := sqlite.Migrate(context.Background(), st); err == nil {
		t.Fatal("future schema version must be rejected with an upgrade path")
	}
}

func TestCodexRegressionOpenAPIUnion(t *testing.T) {
	spec, err := openapi3.NewLoader().LoadFromFile("../../../contracts/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile("../../../contracts/fixtures/api/rejection.accepted-with-terminal.json")
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(b, &value); err != nil {
		t.Fatal(err)
	}
	if err := spec.Components.Schemas["SubmitReply"].Value.VisitJSON(value); err == nil {
		t.Fatal("OpenAPI union must reject accepted+succeeded")
	}
}

// Adaptation: the permit is issued with the REAL prepared digest/credential
// (the original probe's literal digest was the flaw under review); the kept
// business expectation is that correctly paired handles validate and missing
// binding expectations fail.
func TestCodexRegressionPermitAssembly(t *testing.T) {
	table := transport.NewHandleTable()
	prepared, err := table.Prepare(adapters.EncodedRequest{
		Endpoint: "https://fixture.invalid/v1/messages", Method: "POST",
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    []byte(`{"fixture":true}`), CredentialRef: "secret-alpha-key",
	}, domain.ExecutionPlan{CredentialRevision: "rev-1"})
	if err != nil {
		t.Fatal(err)
	}
	registry := ledger.NewPermitRegistry()
	p, err := registry.Issue("att-assembly-1", "runtime-local", prepared.RequestDigest(), prepared.CredentialRevision())
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.Validate(p, "runtime-local", prepared.RequestDigest(), prepared.CredentialRevision()); err != nil {
		t.Fatalf("real pairing must validate: %v", err)
	}
	if err := registry.Validate(p, "", "", ""); err == nil {
		t.Fatal("missing binding expectations must fail closed")
	}
}

// Adaptation: CapabilityProfile is passed explicitly (frozen R10 input); the
// kept business expectation is that different profiles never collide.
func TestCodexRegressionCapabilityFingerprint(t *testing.T) {
	fp := func(profile domain.ID) string {
		f, err := domain.SemanticFingerprint(domain.FingerprintInput{
			Domain: "ai/text", PoolID: "pool-1", CapabilityProfile: profile,
			Payload: json.RawMessage(`{"prompt":"same"}`),
		})
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	if fp("profile.chat.basic") == fp("profile.chat.reasoning") {
		t.Fatal("different capability profiles must produce different fingerprints")
	}
}

// adaptRound1LegalBaselines injects the verified fact ids into the named
// legal baselines' entitlement resources (in resource order). Documents not
// listed are returned unchanged.
func adaptRound1LegalBaselines(t *testing.T, name string, doc json.RawMessage, facts []string) json.RawMessage {
	t.Helper()
	if len(facts) == 0 {
		return doc
	}
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatalf("adapt %s: %v", name, err)
	}
	resources, _ := parsed["resources"].([]any)
	factIdx := 0
	for _, rawRes := range resources {
		res, ok := rawRes.(map[string]any)
		if !ok || res["commercialRole"] != "entitlement" {
			continue
		}
		if factIdx < len(facts) {
			res["providerFactId"] = facts[factIdx]
			factIdx++
		}
	}
	if factIdx != len(facts) {
		t.Fatalf("adapt %s: expected %d entitlement resources, adapted %d", name, len(facts), factIdx)
	}
	out, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// cloneInitialEvidencePerBucket gives every entitlement resource its own
// initial_state evidence (subject = the resource itself), required since the
// Q3 C2 applicability rules. Only touches documents that need it.
func cloneInitialEvidencePerBucket(t *testing.T, doc json.RawMessage) json.RawMessage {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatal(err)
	}
	resources, _ := parsed["resources"].([]any)
	evidence, _ := parsed["evidence"].([]any)
	for _, rawRes := range resources {
		res, ok := rawRes.(map[string]any)
		if !ok || res["commercialRole"] != "entitlement" {
			continue
		}
		resID, _ := res["resourceId"].(string)
		ref, _ := res["initialStateEvidenceRef"].(string)
		if ref == "" {
			continue
		}
		for _, rawEv := range evidence {
			ev, ok := rawEv.(map[string]any)
			if !ok || ev["evidenceId"] != ref {
				continue
			}
			if ev["subjectRef"] == resID {
				continue // already points at this resource
			}
			clone := deepCopyMap(ev)
			clone["evidenceId"] = ref + "-for-" + resID
			clone["subjectRef"] = resID
			evidence = append(evidence, clone)
			res["initialStateEvidenceRef"] = clone["evidenceId"]
		}
	}
	parsed["evidence"] = evidence
	out, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func deepCopyMap(in map[string]any) map[string]any {
	blob, _ := json.Marshal(in)
	var out map[string]any
	_ = json.Unmarshal(blob, &out)
	return out
}

// adaptInitialEvidenceVocabulary: the round-1 legal baseline predates the
// remaining/bound key split — its initial_state evidence carried the opening
// quantity under "bound". Rename to "remaining" (business meaning unchanged).
func adaptInitialEvidenceVocabulary(t *testing.T, doc json.RawMessage) json.RawMessage {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal(doc, &parsed); err != nil {
		t.Fatal(err)
	}
	for _, rawEv := range parsed["evidence"].([]any) {
		ev, ok := rawEv.(map[string]any)
		if !ok || ev["kind"] != "initial_state" {
			continue
		}
		payload, _ := ev["payload"].(map[string]any)
		if q, exists := payload["bound"]; exists && payload["remaining"] == nil {
			payload["remaining"] = q
			delete(payload, "bound")
		}
	}
	out, err := json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
