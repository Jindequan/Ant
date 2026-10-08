# Staged Execution and Acceptance Checklist

Each stage's main execution steps and business exits are in the `../00-07` stage cards; leaf task dependencies, gates, write sets, deliverables, acceptance criteria, and verification methods are auto-projected from `TASK-INDEX.json` into this directory. After changing the index, run `python3 scripts/render_stage_runbooks.py`; to verify, use `python3 scripts/render_stage_runbooks.py --check`.

| Order | Execution/Acceptance Checklist | Scope |
|---|---|---|
| 0 | [G1-DEV](G1-DEV.md) | Only the designated local network-disabled leaves; not equivalent to the real G1 |
| 1 | [T0](T0.md) | IN-01–04 and G0 |
| 2 | [T1](T1.md) | Existing accepted T1 items are not redone; after G0, execute IN-05/G1 |
| 3 | [T2](T2.md) | After G1, single-binding real closed loop |
| 4 | [T3](T3.md) | Correctness, concurrency, and recovery |
| 5 | [T4](T4.md) | Second account, manual ordering, and safe reallocation |
| 6 | [T5](T5.md) | Seven page types, first use, and operational disposition |
| 7 | [T6](T6.md) | Frozen load, performance, installation, and delivery |
| 8 | [T7](T7.md) | Optional extension, started only when there are real new requirements |

The executor implements and self-checks leaf by leaf within the same batch, leaving tasks at `verifying`; the phase gate still blocks unauthorized starts. In the end, Codex performs a unified independent review of the final source code and evidence and decides `done`. Task status is updated only in `../STATUS.md`.
