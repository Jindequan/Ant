# CFG-02 — D3 typed evidence claims

2026-10-03. Scoped backend correction to `compileConstraintErrors` and its new `checkEvidenceQuantity` helper in `server/internal/config/compile.go`; durable complete-entry tests in `compiled_evidence_test.go`. No window execution, shared type, Web, historical audit artifact or global status edits.

The fixed trusted snapshot constrains evidence: bound/hard_stop require canonical nonnegative `bound` equal to `CapacityBound`; initial_state requires canonical nonnegative `remaining` equal to `InitialRemaining`. Invalid quantities produce `evidenceNotApplicable`; canonical conflicting claims produce `evidenceConflictsVerifiedFact`, at the resource evidence reference field. Compile produces no executable output on issues. Conservative configured capacity remains independent from the exact evidence snapshot.

§4.8 quick gate: no page contracts changed; affected §1.3/§1.4 evidence honesty and §3.8 input contracts. §4.1 checks cover malformed/negative/overflow inputs, conflicting snapshots and legitimate zero controls. §4.7 evidence is the actual logs below.

From `/Users/devin/code/Ant/server`:

- `go test -race -count=1 -run 'TestCompiledEvidenceClaims|TestRework3CapacityConstraints' -v ./internal/config` — exit 0; `go-evidence-race.log`. Successful controls precede the bound, hard_stop and initial_state mutation matrices. Cases cover required fields, -1, -0, 01, empty/malformed/overflow, zero conflicts, positive conflicts, matching zero snapshots and conservative capacity. Compiled output asserts capacity, trusted initial remaining and evidence references.
- `go test -race -count=1 -v ./internal/config` — exit 1; `go-config-race.log`. Only failed leaf: `TestRework2FullBoundaryConfigCases/valid-catalog-two-facts`. Historical prepaid input claims bound/remaining=500 against its trusted 1000 snapshot; the new rejection correctly locates `/resources/3/{capacityEvidenceRef,initialStateEvidenceRef}`. Root owns the sanctioned test-local completion; the original audit corpus remains unchanged.

§4.6 limits: CFG-02 targeted behavior is verified; full config acceptance waits for that historical fixture completion and merged tree validation. UI, activation transactions, real supplier requests, deployment and T2 were not tested. Runtime model identity was not exposed by tool results.
