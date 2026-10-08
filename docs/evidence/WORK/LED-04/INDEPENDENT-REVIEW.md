# LED-04 Codex 独立最终验收

日期：2026-10-03。范围：对该批次最终源码、任务标准、真库测试和原始日志作独立复核；含按 LED-04 `scopeNote` 登记的 PermitRegistry 生命周期窄扩展。结论：**PASS（仅 G1-DEV 本地合成切片）**。不构成真实 G1、T2/G2、真实供应商或生产验收。

## 结论与修复

初始实现正确覆盖 AbortBeforeDispatch、取消、续租与清扫的主要事务路径，但原版 `PermitRegistry.byID/byAttempt` 记录仍会随终态尝试累积。按要求没有静默越权写文件；Codex 将这一项精确登记到 LED-04 任务写集，增加 `permit.go`/`settle.go` 与针对性测试后补齐：

- 持久事务成功提交后，abort、prepared cancel、sweep、settle 都清理 runtime-local `handles`、registry `byID` 与 `byAttempt`。数据库 `dispatch_permits` 行不删除，仍是审计与恢复事实源；旧句柄在清理后以 unknown 拒绝。
- Settle 继续时也会清除刚结束的 attempt；新 attempt 拥有独立许可身份。结算回滚、取消事务失败时不提前清理。
- 额外反例复核发现续租 `now + leaseMillis` 可发生 int64 溢出；现先比较剩余 deadline 再相加，并有最大 duration 回归测试。
- 取消事件不再拼接用户 ID 字符串构造 JSON；使用标准 JSON encoder，带引号与反斜线的 commandID 回归通过。

上述变更已写入 `TASK-INDEX.json` 的 LED-04 写集/范围说明，并重生成 workstream 与阶段执行清单。未改冻结 `WriterTx`/`ReaderTx`/`Store` 签名、SQL schema 或开放通用 SQL。LED-03 结算业务结果算法未改；settle.go 的终态内存清理后置钩子及 fixture 变化见 [LED-03 后续影响记录](../LED-03/POST-ACCEPTANCE-LED04.md)。

## 验收标准

| 标准 | 独立复核证据 | 结论 |
|---|---|---|
| AbortBeforeDispatch 按冻结 DecideAbort 表原子收敛、幂等 receipt、CAS 失败回滚 | `abort.go`；`TestAbortClosesPreparedAttempt`、`TestAbortRevokesGrantedPermit`、`TestAbortAfterConsumeWithoutProofConserves`、`TestAbortReliableUnsentProof`、`TestRecoveryFailsClosedOnDrift` | PASS |
| 六类竞争与取消优先级稳定；prepared 关闭，executing 只记录意图，成功报告优先于取消 | `cancel.go`、`recovery_test.go`；focused race 日志 | PASS |
| lease 不等于外部结束；granted 未消费可撤销；consumed 未证实未发送则保守 unknown | `sweep.go`、`abort.go`；prepared/granted/consumed sweep 测试 | PASS |
| 可靠未发送证据要 handle 与持久 permit 同源；缺 handle、granted、跨 attempt 等反例 fail closed | `TestAbortReliableUnsentProof`，审查 `applyRecoveryAbort` identity checks | PASS |
| PermitRegistry 有终态生命周期：索引/缓存清理，过期句柄拒绝；正常结算、abort/cancel/sweep 都在 durable commit 后清理 | `permit.go`、`prepare.go`、`cancel.go`、`sweep.go`、`settle.go`；`TestPermitRegistryReleaseTerminalCompactsBothIndexes`、`TestReleaseTerminalAttemptDropsHandleAndRegistryRecord`，settle 与 revoke 集成断言 | PASS |
| 续租期限 capped at deadline 且不溢出 | `renew.go`；`TestRenewLeaseCapsBeforeAdditionOverflow` | PASS |
| JSON 事件保持有效并完整保留任意 commandID | `cancel.go`；`TestCancelEventEscapesCommandID` | PASS |
| A04/A05/A15/A20 适用证据：旧 owner 失格、崩溃/迟到报告、结果事实次序、授权未发送后零发送 | `recovery_test.go` 中 sweep/cancel/consume 与 resolver 测试 | PASS（本地合成真库范围） |

## 最终源码摘要

下列 SHA-256 由 Codex 对本次复核后的工作树直接计算。范围内 13 个实现/测试文件（包含持有 runtime handle 的 `prepare.go`）：

| 文件 | SHA-256 | 行数 |
|---|---|---:|
| `server/internal/ledger/commands/abort.go` | `614680cd0082b227d25386ceed11cfd2b9f4e6abd1742bdd659a71f7771a235e` | 426 |
| `server/internal/ledger/commands/cancel.go` | `37e30d678d5144a59c5e6111544385fa4118f267b64eafddc04f1aac689f1b2e` | 133 |
| `server/internal/ledger/commands/renew.go` | `20c818936447649e33fe45701575de6d915f4250d991a59adb38bda4c6a76a96` | 77 |
| `server/internal/ledger/commands/sweep.go` | `c04d8fb2ff3ce7000cb57341995260121bfee8d2f9a9040a567abdf7c653f90c` | 149 |
| `server/internal/ledger/commands/recovery_test.go` | `7acef7a68a69bb94dc17a5ef7e3a250653cc503f4003c90986e7178cdacdad5e` | 502 |
| `server/internal/ledger/commands/settle.go` | `033fa3a5dd506ac31f0bb55eee9b10aebf11019c7f2ac7244e14da818485df1f` | 393 |
| `server/internal/ledger/commands/settle_test.go` | `455f8315286e39df45549de35853d08ac6e96ec53d85dc66f9c3b42760deacd5` | 691 |
| `server/internal/ledger/commands/permit_lifecycle_test.go` | `64d855667b5c5a6d75c31ff87716c535731d38e55df3e65d0aa3da1f1d114e75` | 27 |
| `server/internal/ledger/commands/prepare.go` | `8420474b036f247ed3e2ac524f22b6d24c7ed102b7bafb9123a0030bb47b14b5` | 509 |
| `server/internal/ledger/permit.go` | `40e6f978993ca5d5d8517b343fd07f9966f51b1b7265879e42a7586733d70f1f` | 191 |
| `server/internal/ledger/permit_test.go` | `e19395ad085fb26c955716e51ccadf1924ddf7b6da7d79dac284f29981f5d124` | 246 |
| `server/internal/store/recovery.go` | `bedcc23435b751b7bcc99fbb9924fffc8875897e2db7a2b1ad4dc772ecbd41a7` | 67 |
| `server/internal/store/sqlite/recovery_statements.go` | `cbfe26179a4ef910cc0ef45ccc7871ab21c2e4486588817d12ab892f2c1f7159` | 178 |

## 复跑

所有命令以 `sandbox-exec` 明确 `deny network-outbound`、`GOPROXY=off`、`GOSUMDB=off`、`GOFLAGS=-mod=readonly` 执行；使用合成 local SQLite，未读取密钥或调用供应商。

| 命令 | 结果 | 原始输出 |
|---|---|---|
| `go test -race -count=1 -v -run 'TestAbort\|TestCancel\|TestRenew\|TestSweep\|TestRecovery\|TestReleaseTerminal' ./internal/ledger/... ./internal/ledger/commands/...` | PASS；新增取消转义、续租溢出、registry compaction 测试均通过 | [codex-focused-race.log](codex-focused-race.log) |
| `make test-contract test-integration` | PASS；全部 Go internal race 与 integration race | [codex-full-race.log](codex-full-race.log) |
| `make check` | PASS；gofmt、vet、build、依赖边界、Web typecheck、生成物、runbook 一致性 | [codex-make-check.log](codex-make-check.log) |
| `python3 docs/plans/check_tree.py` | PASS；55 tasks、8 gates、DAG 有效 | 本次复核工具输出 |

全局 `verify-manifest` 未在此签名：T2-DEV 仍有多个任务新增/修改的实现文件，按项目既有计划等合并文件组审查后统一复核。此项不阻止本叶子功能验收，但不能宣称当前树与 T1 的 184 文件 manifest 一致。

## 边界与未决

- 仅接受 LED-04 G1-DEV 本地合成切片。真实 Transport 的 socket 写入前证明、真实账户、真实调用、A26/A16 集成、跨进程故障注入与 T2/G2 仍未验收。
- IN-03 火山官方条款的可渲染读取已完成，Codex独立用官方网页复核 §2.8/§2.9/§2.11/§3.1；证据与限缩后的措辞在 `docs/implementation/PROVIDER-EVIDENCE.md`。这只解除“条款不可渲染”一项，不关闭 IN-03/D03，因为账户、套餐、席位、授权等事实仍未取得。
- G0/G1 未出站；真实供应商调用和生产发布均未授权。
