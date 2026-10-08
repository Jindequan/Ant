# QA-03 fault verification progress

The dedicated fault suite now injects two storage failures against a real
SQLite file:

- An audit trigger aborts a restore-guard mutation after its state update and
  receipt insertion have begun. The test checks that the transaction leaves
  the guard, control version, receipt, and audit unchanged, then retries the
  same command successfully.
- A Store wrapper drops the acknowledgement after SQLite commits. Retrying
  the identical command returns the persisted result and leaves exactly one
  state change, receipt, and audit row.
- Read-only credentials are denied at the ledger command boundary for manual
  observation, adjustment, and hold clearing before storage is accessed.
- An older verified backup is restored over a newer live database. The test
  proves the snapshot omissions are real, the displaced database is preserved,
  a system audit/receipt is present, `restore_guard=1` is stored before
  publication, and a valid new claim is rejected with `ErrClaimNotReady`.
- DeepSeek encoding rejects a prepared plan whose report-mapping revision or
  bound mapping revision differs from the adapter's current revision. The
  executor encodes before creating the transport handle or authorizing a
  dispatch permit.

Run the focused suite with `make test-fault`. It includes the fault package,
selected integration cases for pool reorder and authorization rollback,
ledger cases for observation watermarks/adjustment/mapping integrity, and the
DeepSeek stale-mapping check. These are local synthetic records; they do not
contact a provider or use a real account.

This is focused progress, not completion of QA-03. Existing preparation and
transport suites separately cover revoked/changed preparation facts, pool
reordering, stale observations, mapping-integrity mismatch at settlement,
restore-guard closure, and dispatch recovery. Mapping revisions are currently
code-pinned constants rather than a separately deployable catalog; the suite
verifies an old plan is rejected by the current adapter, while an actual
multi-binary upgrade transition remains outside the local simulator's scope.
QA-03's broader operational evidence list still needs review before closure.
