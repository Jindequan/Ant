# LED-03 接受后的 LED-04 生命周期窄扩展

日期：2026-10-03。LED-03 原独立验收保持其业务结算结论；此记录追踪 LED-04 为关闭 T2.2 PermitRegistry 生命周期缺口而对两个 LED-03 写集文件所作的有限变更。原 review 与本后续记录共同构成当前证据，不能再将原文件摘要视为最新 SHA。

## 文件变化与影响

- `server/internal/ledger/commands/settle.go`：仅在持久 settle 事务成功提交后，调用同一 `PreparationService` 清除已终态 attempt 的 runtime-local permit handle 和 PermitRegistry 两个索引。即使 settlement 允许 operation 继续，已 settle attempt 的 permit 也不再可用；下一 attempt 有独立身份。回滚/conflict 时不清理。
- `server/internal/ledger/commands/settle_test.go`：fixture 将同一 `PreparationService` 传给 `SettlementService`；新增断言：终态结算后旧 permit 在本地 registry 中以 unknown 拒绝。原账务、结果、事件和继续规则不变。

Codex 复核后的文件摘要：

| 文件 | SHA-256 | 行数 |
|---|---|---:|
| `server/internal/ledger/commands/settle.go` | `033fa3a5dd506ac31f0bb55eee9b10aebf11019c7f2ac7244e14da818485df1f` | 393 |
| `server/internal/ledger/commands/settle_test.go` | `455f8315286e39df45549de35853d08ac6e96ec53d85dc66f9c3b42760deacd5` | 691 |

## 回归验证

在最终合并树上、显式禁外网与只读 Go module 设置下，以下均通过：

- `make test-contract test-integration`：全部 Go internal race 与 SQLite integration race。
- LED-04 聚焦 abort/cancel/renew/sweep/recovery/terminal-permit 测试。
- `make check`：gofmt、vet、build、依赖边界、Web typecheck、生成物与执行清单检查。

原始日志链接见 [LED-04 Codex 独立复核](../LED-04/INDEPENDENT-REVIEW.md)。没有改变 Store 冻结接口、schema 或 LED-03 账务结果算法。
