# LED-03 current settlement source review

Date: 2026-10-04. Reviewer: Codex, after implementation and before status promotion. This is a current-tree review; it preserves historical accept hashes below and does not claim to recover the missing old source copies or independently re-sign the earlier review.

## Reviewed source hashes

| File | Historical accepted SHA-256 | Current SHA-256 | Current lines |
|---|---|---|---:|
| `server/internal/ledger/commands/abort.go` | `614680cd0082b227d25386ceed11cfd2b9f4e6abd1742bdd659a71f7771a235e` | `0d15aeed518527f8eb6c1a0329a192949d258627f755dff8cd657fbf6ad5b50c` | 428 |
| `server/internal/ledger/commands/settle.go` | `033fa3a5dd506ac31f0bb55eee9b10aebf11019c7f2ac7244e14da818485df1f` | `fe51bc848472081d02f82e2019bd72bc18683d28d5404ab18aba2769436bedd1` | 460 |
| `server/internal/ledger/commands/sweep.go` | `c04d8fb2ff3ce7000cb57341995260121bfee8d2f9a9040a567abdf7c653f90c` | `f3da023f7df0b0ed81644bda07d03829015333be0bac6e94a94f714d2961a837` | 149 |

The current `settle_test.go` and SQLite statement hashes are recorded in the [shared LED-04 current-tree review](../../LED-04/CURRENT-TREE-REVIEW-2026-10-04.md). The test is now 870 lines and the statement file remains 216 lines. Historical-source mismatch is distinct from current-tree review and remains visible in source reconciliation.

## Review findings

- Report validation checks canonical unsigned decimal syntax, strict `> MaxInt64`, mutual exclusion with `Actual`, metric/unit membership in the stored plan, and evidence presence before persistence.
- The exact string is included in `commandHash` and `ReportJSON`; the optional JSON field omits itself for ordinary reports, preserving their prior serialized shape. Legacy report JSON without the field still decodes.
- For every matching counter allocation, settlement applies only `reserved -= bound` and `committed += 0`, sets the resource basis to `unrepresentable` with `consumed=null`, and inserts a scoped hold in the same writer transaction. It does not use the conservative-bound branch or create numeric encumbrance from an unknowable/int64-inexpressible amount.
- `blocked` disables continuation. The single result resolver returns `unknown/UnknownOutcome` before confirmed success or cancellation, with an accounting reason, exact metric details, `retrySafety=undetermined`, and `suggestedAction=review_usage`.
- A repeated report returns the persisted receipt; a changed report hash goes only to reconciliation evidence. The store persists attempt/report, resource events, reservation phases, holds, operation result, and ordered events under one transaction; injected hold failure verifies rollback.
- `consumed` is represented as a pointer so normal outcomes remain decimal quantities while an unrepresentable outcome serializes as JSON null. All existing settlement/abort paths initialize the field, and OpenAPI plus generated Web types allow null.
- `abort.go` and `sweep.go` map empty/stored output projections to the OpenAPI `none`/`available` vocabulary. The existing SQLite statement layer was inspected: owner/definition/reservation identity checks precede projection, CAS updates and all settlement writes use the same `writerTx` transaction, conflict reports append evidence, and report/result reads remain JSON-backed without a schema-column migration.

No remaining issue was found in this narrow implementation review. RUN-01 must still prove adapter normalization from provider response text before any int64/float conversion; this local G1-DEV slice does not accept real provider behavior or close G1.
