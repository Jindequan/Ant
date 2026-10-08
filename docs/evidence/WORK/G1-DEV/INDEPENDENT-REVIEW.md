# G1-DEV 本地批次汇总验收

日期：2026-10-03。结论：**六个授权叶子全部独立接受，G1-DEV 本地实施批次完成**。G1-DEV 是 G1 前的禁网实现授权，不是 T 阶段出站门；本结论不关闭 G1、G2、T2 集成或真实供应商准入。

## 叶子验收矩阵

| 叶子 | 结论 | 独立验收证据 | 范围限制 |
|---|---|---|---|
| AUTH-01 | PASS | [独立终验](../AUTH-01/INDEPENDENT-REVIEW.md) | 本地身份/权限/秘密服务；未接真实路由或凭证 |
| CFG-08 | PASS | [独立终验](../CFG-08/INDEPENDENT-REVIEW.md) | 本地 SQLite/localtest；API/UI 完整接线和 A26/A16 集成未验 |
| LED-01 | PASS | [独立终验](../LED-01/INDEPENDENT-REVIEW.md) | 本地 SQLite claim 事务 |
| LED-02 | PASS | [独立终验](../LED-02/INDEPENDENT-REVIEW.md) | 本地合成准备与授权；窗口 revision 仍是 synthetic convention |
| LED-03 | PASS | [独立终验](../LED-03/INDEPENDENT-REVIEW.md)＋[LED-04 后续影响](../LED-03/POST-ACCEPTANCE-LED04.md) | 本地合成结算；后续 permit 终态清理纳入 LED-04 |
| LED-04 | PASS | [Codex 最终验收](../LED-04/INDEPENDENT-REVIEW.md) | 恢复/取消/续租/清扫及 permit 生命周期；本地合成真库 |

## 汇总复核与新鲜度

- 汇总核对六份独立验收记录中的 **31 个唯一实现/测试文件**，生成 [最终源码摘要](source-manifest.sha256)。摘要是 G1-DEV 已接受切片的清单，不替代项目全局 manifest。
- 原 LED-02 报告里的 `server/internal/ledger/commands/prepare.go` 摘要已不匹配当前文件。Codex 重读当前文件并复核 handle 所有权与生命周期边界，将该文件登记到 LED-04 生命周期扩展，更新 LED-04 最终报告；当前摘要写入本汇总 manifest。其余已列文件与当前摘要匹配。
- 最终树禁网执行 `make test-contract test-integration`：Go internal race 与 SQLite integration race 均通过；`make check` 通过；`python3 docs/plans/check_tree.py` 与 `python3 scripts/render_stage_runbooks.py --check` 通过。
- `G1-DEV.md` 已依据更新后的任务索引重生成；每个叶子的源哈希、关键反例与原始命令证据仍保留在各叶子报告中。

## 出站边界

本批只证明六个叶子在 localtest、假身份/假凭证和本地 SQLite 环境下通过验收。真实应用与请求授权、目标主机事实、供应商账户/套餐/席位/额度、IN-05 真实 bundle、API/UI/Transport/Adapter 接线以及 G1/G2 条件仍按 STATUS 和 TASK-INDEX 保持未满足；没有读取真实密钥、访问供应商或产生费用。因此只关闭 G1-DEV 允许的本地实现批次，不关闭 G1、G2 或 T2 集成出口。
