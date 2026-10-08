# LED-01 独立终验

**结论：PASS（G1-DEV 本地叶子接受）**。复核者 `独立复核者` 未参与 LED-01 实现；审查只读且未访问供应商。日期：2026-10-03。

## 复核结果与验证

复核覆盖同语义重放、异语义冲突、过期／新策略校验顺序、receipt 故障回滚、跨独立进程恰一 owner 及终态重放。SQLite 的 `BEGIN IMMEDIATE` 保护整条命令；仅在 `SubmitWrite` 成功后返回 owner；receipt 不持久化 owner。相关禁网 race 测试 exit 0，包含 5 个实质测试及 helper：

```sh
sandbox-exec -p '(version 1) (allow default) (deny network-outbound)' \
  env GOPROXY=off GOSUMDB=off \
  go test -race -count=1 -v ./tests/integration/ -run '^TestClaim'
```

复核前后相同的 SHA-256：

| 文件 | SHA-256 |
|---|---|
| `server/internal/store/claim.go` | `2233f4e1bad54f5abeb1e88ef8d1e2fd3740890c78c4e82ed36e2f58524aabc3` |
| `server/internal/store/sqlite/claim_statements.go` | `0d0bed7400152a04743824e43ee385a6ef6cd962c8b265d638ef53af65be2aa5` |
| `server/internal/ledger/commands/claim.go` | `8a6b67142001d73709100cef791609c59eb70d4f46efd1e1ecb87788e21ccfa0` |
| `server/tests/integration/claim_test.go` | `484e840b78a3d99b8f0fb563c5a94227511142ce74837516a001f0ee2c3b2a79` |

过期 running 操作当前保持 inProgress 且不产生新 owner；恢复与清扫属于 LED-04，不宣称本叶子已实现恢复。此 PASS 仅接受 LED-01 本地事务叶子，不构成 G1/G2 或供应商出站验收。
