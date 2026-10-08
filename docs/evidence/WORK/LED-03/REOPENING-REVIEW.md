# LED-03 重新审查：超 int64 实际用量无法进入结算契约

日期：2026-10-04。范围：G1-DEV 本地合成账本；未读取真实凭证、未联网、未发供应商请求。此审查重新打开 LED-03 的接受结论，不代表实现或重新验收已完成。

## 发现

权威设计明确要求：超过 SQLite/int64 可表示范围的供应商用量必须把原始十进制事实写入 `attempts.report_json`，并封闭相关资源，不能截断或丢弃事实（`docs/design/02-STORAGE.md` §2；`docs/design/03-EXECUTION.md` §5）。现有数据链无法做到：

1. `domain.MetricReport.Actual` 的唯一数值字段是 `*Units`，而 `Units` 是 int64；严格 `ParseUnits` 对溢出只返回 `ErrQuantityRange`。`RawDigest` 只是一段摘要，无法还原数字。
2. `SettlementService.Settle` 对已构造的 `ExecutionReport` 计算 `commandHash` 并把该类型 JSON 作为 `ReportJSON` 持久化；它没有超范围十进制字段或替代的原始计量证据入口。
3. `CounterSettle` 接收 `*Units`；超范围数值不能进入已接受的实际量结算分支。用 nil 代替会走“用量未知、按 bound 保守结算”，但不能保存已知的超范围实际量，且没有 `actual_out_of_range` 资源 hold。
4. Adapter 的 `Decode` 收到原始响应体，但返回的 `ExecutionReport` 不含可持久化的该项精确十进制值。因此在 Adapter 解码与 Ledger 之间没有保留该事实的通道。

具体位置：`server/internal/domain/planning.go` `MetricReport`/`ExecutionReport`；`server/internal/ledger/commands/settle.go` `commandHash`、报告校验和结算映射；`server/internal/domain/resources/counter.go` `CounterSettle`。现有 LED-03 接受证据确认的是可表示范围内超额、unknown 与重复结算，不覆盖超 int64 输入。

## 闭环契约与技术裁决

- 保持 `Actual *Units` 表示正常可计量值；为计量报告增加可选字段 `ActualOutOfRangeDecimal string`，仅承载“已按计划单位归一化、非负且大于 `math.MaxInt64`”的精确十进制整数。一个报告只能设置 `Actual` 或该字段之一；非空值必须为规范十进制、无符号、无前导零，且经溢出分类确认为不可表示。Adapter 与 Ledger 都校验格式及 metric/unit/计划匹配。字段只保存该计量数值，不保存原始响应正文、凭证或提示词。
- 该字段随完整报告进入 `commandHash` 与 `attempts.report_json`，保证幂等比较和后续核查可取回原值；旧 JSON 缺少新字段时仍解析为零值，不需改 SQL 列或迁移。
- 超范围值不可伪装成 int64 结算额。结算原子释放该 reservation、保留精确原值、写入 `actual_out_of_range` 资源 hold 和稳定结果原因；不增加虚假的 `committed_units` 数字，不允许自动继续选路。结果为 `unknown` / `UnknownOutcome`，带 `accounting/actual_out_of_range` 原因、每个受影响 resource 的 `consumptionBasis=unrepresentable` 与 `consumed=null`，通过 `unrepresentableMetrics` 精确保留 metric/unit/原值/证据引用，`retrySafety=undetermined`，并给出 `suggestedAction=review_usage`；不得把未计入账本的 0 显示为实际零。其它可独立计量的资源和 attempt/report 仍在同一事务中结算。
- 若上述事实在当前代码结构中不能作为同事务、无假账的结算表达，则返回经持久化的核查/隔离结果；绝不能因 overflow 回滚并遗失已发生的供应商消费事实。
- 回归测试分别覆盖 int64 内超额、超 int64 正值、负值／非规范字符串、重复报告，以及资源/报告/hold 原子提交和后续准备被阻断。

建议影响范围扩大 LED-03 写集：增加 `server/internal/domain/planning.go`、`server/internal/domain/resources/counter.go` 及其测试，并同步 `docs/design/contracts.go`、`docs/design/02-STORAGE.md` 与 `docs/design/03-EXECUTION.md`。这是 `attempts.report_json` 的向后兼容可选字段，不需 SQL 列迁移；旧报告缺字段仍可读。但它改变了持久 JSON 报告 schema，且 operation result 不能把未表示的用量当成零，因此需同时冻结 query/result 的明确语义；裁决前不更改该 schema 或代码。

## 验收影响

QA1-OVERAGE 的“超 int64 原值保留并隔离”是既有设计要求，当前实现不满足，不能当成未来测试已通过。LED-03 重新审查只覆盖此缺口；G1-DEV 禁网真库测试完成且独立复核通过前，LED-03 保持未接受。RUN-01 还需证明 Adapter 能把供应商用量精确归一化为此格式；真实供应商边界另按 G1 处理。

## 2026-10-04 技术裁决（按项目负责人授权执行）

在项目负责人授予 Codex 全权负责项目实施、验证与验收后，Codex 采用上述最小、向后兼容闭环：旧 JSON 缺少该可选字段继续解析；不改 SQLite 列；OpenAPI 版本升级并同步生成类型；扩展 LED-03 的 typed 实现与测试写集。此变更使既有存储设计和 QA1-OVERAGE 可执行，不改变“不截断事实、不伪造用量、隔离资源”的业务规则。实现及本地 SQLite 真库 race 验收完成前，LED-03 仍为 `verifying`，不恢复旧接受声明。
