# ENG-08 — hooks wiring stated honestly (2026-10-08)

[design/10-HOOKS.md §2.1](../../../design/10-HOOKS.md) now lists points defined vs invoked in
production, re-verified by grep: 5 invoked (4 Guard points + `candidates.ready`) in
`application/operation_executor.go`; `request.validated`, Transformer and the six Observer points
have no production call site; `ObserverDispatcher` is constructed only in tests; the composition
root seals an empty registry. No framework code changed.
