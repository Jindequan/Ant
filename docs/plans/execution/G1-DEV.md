# G1-DEV 本地禁网切片 · 执行与验收清单

> 自动生成：节点字段以 [TASK-INDEX.json](../TASK-INDEX.json) 为唯一来源。编辑索引后运行 `python3 scripts/render_stage_runbooks.py`。本文件给执行者逐项勾验；实际状态仍只记在 [STATUS.md](../STATUS.md)。

仅包括 TASK-TREE §5 明确开放的 AUTH-01、CFG-08、LED-01–04。全部使用 localtest/假身份/假凭证与本地 SQLite，并对测试进程禁用外网。它不满足真实 G1，也不授权供应商调用、G2 出站或生产动作。

## 叶子执行与验收

按依赖顺序执行。每一行的写集、交付物、验收标准和验证方法均来自任务索引；对应阶段卡的步骤与业务语义继续有效。

| ID / 阶段 | 任务 | 前置 | 门禁 | 写入范围 | 交付物 | 验收标准 | 验证方法 |
|---|---|---|---|---|---|---|---|
| AUTH-01 / T2.1 | 身份、namespace与秘密边界 | BASE-02 | G1-DEV | `server/internal/security/`<br>`server/internal/api/middleware.go` | 可信身份/权限上下文、凭证只写mask/ref/revision和秘密解析 | body不能自授权；跨namespace读写/取消拒绝；日志/GET/前端不泄密 | 本地假身份/假凭证源/API授权反例＋禁外网运行；不得加载真实凭证或发外呼 |
| CFG-08 / T2.1 | 草稿验证与配置激活服务 | AUTH-01, CFG-07, BASE-05 | G1-DEV | `server/internal/application/config_service.go`<br>`server/internal/application/config_service_test.go`<br>`server/internal/store/store.go`<br>`server/internal/store/sqlite/statements.go` | SaveDraft/Validate/Activate、GET读模型、原子版本与不可变快照 | 错误草稿能保存；激活失败保留旧版；事务内复核证据/revision；409无局部保存 | 本地SQLite集成＋localtest事实/假凭证源；覆盖A26/A16；禁外网；不能据此接受真实供应商激活 |
| LED-01 / T2.2 | 操作身份和执行权事务 | BASE-04, BASE-05 | G1-DEV | `server/internal/store/claim.go`<br>`server/internal/store/sqlite/claim_statements.go`<br>`server/internal/ledger/commands/claim.go`<br>`server/tests/integration/claim_test.go`<br>`docs/evidence/WORK/LED-01/` | claim/receipt/同异摘要/owner/单活跃attempt；仅新增兼容的typed Store claim扩展，所有SQL仍限于store/sqlite | 同ID同语义复用；异语义冲突；多进程只授一个执行权；无半提交 | 本地SQLite真库并发；A01/A02 |
| LED-02 / T2.2 | 全资源准备与发送授权 | LED-01, CFG-07 | G1-DEV | `server/internal/ledger/commands/prepare.go`<br>`server/internal/ledger/commands/authorize.go`<br>`server/internal/ledger/validate_change.go`<br>`server/internal/store/prepare.go`<br>`server/internal/store/sqlite/prepare_statements.go`<br>`server/tests/integration/prepare_test.go`<br>`docs/evidence/WORK/LED-02/` | quote/tryPrepare/authorize/consume持久CAS与I01–I10校验 | 任一资源/版本/策略失败全回滚；许可与预算一致；网络不入事务 | 本地SQLite真库容量/版本/许可竞争＋禁外网运行；A03/A16/A20 |
| LED-03 / T2.2 | 结算与结果事件持久化 | LED-02 | G1-DEV | `server/internal/ledger/commands/settle.go`<br>`server/internal/ledger/commands/settle_test.go`<br>`server/internal/domain/planning.go`<br>`server/internal/domain/planning_report_test.go`<br>`server/internal/domain/quantity.go`<br>`server/internal/domain/quantity_test.go`<br>`server/internal/domain/types.go`<br>`server/internal/domain/result_resolver.go`<br>`server/internal/domain/result_resolver_test.go`<br>`server/internal/ledger/commands/abort.go`<br>`server/internal/ledger/commands/sweep.go`<br>`server/internal/store/settle.go`<br>`server/internal/store/sqlite/settle_statements.go`<br>`server/internal/contracttest/contract_test.go`<br>`contracts/fixtures/api/manifest.json`<br>`contracts/openapi.yaml`<br>`web/src/api/schema.d.ts`<br>`web/src/api/types.ts`<br>`web/tests/contract.test.ts`<br>`docs/design/contracts.go`<br>`docs/design/02-STORAGE.md`<br>`docs/design/03-EXECUTION.md`<br>`docs/plans/03-CORRECTNESS.md`<br>`docs/plans/execution/T2.md`<br>`docs/plans/STATUS.md`<br>`docs/plans/workstreams/03-execution.md`<br>`docs/evidence/WORK/LED-03/` | 一次结算、真实超额、消费/占用分离、结果和事件同提交 | 重复报告不重复扣减；冲突不覆盖；unknown保留槽；超int64精确保留、释放预留、资源隔离、结果明示且无伪账；同事务原子提交 | 本地SQLite真库报告/资源/事件/hold/幂等核对；全量race与契约校验；A06/A08/A22/QA1-OVERAGE |
| LED-04 / T2.2 | 取消、续租、清扫与未发送收敛 | LED-03 | G1-DEV | `server/internal/ledger/commands/cancel.go`<br>`server/internal/ledger/commands/renew.go`<br>`server/internal/ledger/commands/sweep.go`<br>`server/internal/ledger/commands/abort.go`<br>`server/internal/ledger/commands/recovery_test.go`<br>`server/internal/ledger/commands/settle.go`<br>`server/internal/ledger/commands/settle_test.go`<br>`server/internal/ledger/commands/prepare.go`<br>`server/internal/ledger/permit.go`<br>`server/internal/ledger/permit_test.go`<br>`server/internal/store/recovery.go`<br>`server/internal/store/sqlite/recovery_statements.go`<br>`docs/evidence/WORK/LED-04/` | AbortBeforeDispatch事务、有限恢复、旧owner拒绝 | 六类竞争有稳定结果；lease不等于外部结束；可靠未发送才释放；许可注册表有生命周期 | 本地SQLite真库故障/竞争＋禁外网运行；A04/A05/A15/A20 |

## 本阶段完成条件

- 本阶段所有必需叶子均有实现或真实输入产物，逐项自检通过，证据保存到各叶子的 `docs/evidence/WORK/<ID>/` 或阶段证据目录。
- 当前阶段卡列出的阶段出口、决定项和适用的 ACCEPTANCE-MATRIX 行全部有证据；失败、未执行、不适用须分别标注并给出原因。
- 执行者将任务状态留在 `verifying`，并记录自检 PASS/FAIL、最终文件清单与摘要、命令和退出码；不得把自检写成独立验收。
- 只有本阶段出口的全部前置均已正式满足，才能越过阶段门。缺失的真实输入或授权必须停在门前，登记准确缺口；允许的独立准备工作可继续。

## 最终验收

独立验收者对最终源码树与原始证据复核，重跑适用命令，抽查正向路径和关键反例，并检查写集/契约/阶段门。发现问题时退回对应叶子修复；通过后才把叶子从 `verifying` 改为 `done` 并关闭阶段出口。
