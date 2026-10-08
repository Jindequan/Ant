# ANT Hooks 生产接入阶段复核

日期：2026-10-04。复核范围：用户提出的 ANT 内部 Hooks 灵活性目标、R25/A30、RUN-04 写集和当前 `server/` 源码树。

## 结论

Hooks 注册基础层具备扩展性，但尚未接入 ANT 生产执行链。它当前不能在任意代码位置自动生效；只能在核心明确声明并调用的生命周期边界运行。要满足用户目标，RUN-04 必须把下列阶段边界实际接入，组合根可在这些受支持边界声明命名扩展点；所有决策仍由核心验证并保留账务、权限和单次发送不变量。

## 验收对照

| 要求 | 当前证据 | 判定 |
|---|---|---|
| Guard、候选排序、Transformer、Observer 类型化且受能力限制 | `server/internal/hooks/hooks.go` 与 `hooks_test.go`；基础层快照见同目录 `README.md` | 基础层已实现并验证 |
| 可声明具名扩展点、稳定顺序、预算、deadline、panic/错误隔离、manifest/revision | `hooks.go`、基础层 race 测试及 `README.md` | 基础层已实现并验证 |
| 在请求、候选、prepare、授权、发送前、报告结算等实际核心边界调用 Hooks | `rg -n 'internal/hooks' server --glob '*.go'` 只命中 `server/internal/hooks/`；`server/internal/application/router.go` 不存在 | 未实现；注册自定义点不会自动接线 |
| Hook registry revision 与阶段决定摘要持久化至 `DecisionRecord`，可恢复 Hook ID/版本 | 当前 `server/internal/` 没有 `DecisionRecord` 类型或生产 revision 消费者 | 未实现 |
| Observer 在提交和解锁后异步、有界运行；失败可观测/重试 | 新增 `ObserverDispatcher`，提供固定 worker/队列、快速满队列错误、deadline、关闭排空和失败统计；仍无生产调用点或持久 outbox | 调度基础已实现，生产接线/可靠重试未实现 |
| A30/RUN-04 故障与权限边界，和真实发送/账务闭环验收 | `docs/plans/ACCEPTANCE-MATRIX.md` A30、`docs/plans/workstreams/03-execution.md` RUN-04；两者依赖门未关闭 | 未验收 |

## 灵活性边界校准

“任意环节”适合解释为**任意已定义且由核心实际调用的生命周期边界**，而非通过字符串注册即可插入任意函数/事务/锁内代码。后者会破坏事务原子性、权限控制和单次发送边界。当前注册表允许组合根命名自定义点及扩展同阶段能力；生产调用链尚不存在，因此目前可扩展的是框架，不是实际运行链。

期望 RUN-04 的实际接线至少覆盖设计中的 `request.validated`、`candidates.ready`、`candidate.before_prepare`、`attempt.before_authorize`、`dispatch.before_send`、`report.before_settle`，并为 claim/decision/prepared/settled/operation/config 的 durable commit 事件提供 post-commit 通知。每个点应把最小脱敏快照传给 Hook，并在决定记录中保存该点决定及 sealed registry revision。Observer 需由有界 worker 或持久 outbox 驱动；不能从同步 `Notify` 直接阻塞请求路径。

## 本轮验证

- 基础层复核发现零 Transformer 字节预算仍可声明/扩展 Transformer 能力，形成“manifest 看似开放、注册或执行必失败”的无效启动状态。`DefinePoint` 与 `EnableCapabilities` 现都在配置阶段用 `ErrLimitExceeded` 拒绝，失败不改变 Registry；回归覆盖两条路径。修改于 `server/internal/hooks/hooks.go`、`hooks_test.go`。
- `cd server && go test -race -count=1 ./internal/hooks`：修复前快照 PASS，原始输出 `hooks-production-readiness-race.log` SHA-256：`032c4b606c7e143dbfe59cd6d6b5627f88795d9df9e44a4fc404e1ef2c93e968`；修复后新鲜 race PASS，原始输出 `hooks-zero-transform-budget-race.log` SHA-256：`2e7db7389419c17c0d794094332db461846d22ceb66a58c29cb93048e051ffde`。
- `make test-contract`：修复后全量 internal `-race -count=1` PASS；原始输出 `make-test-contract-zero-transform-budget.log` SHA-256：`40cb1f635094da94b5b8ef57dfc9239049b485821a0ba50971522ede06f336f9`。
- `make check`：本轮去除旧仓库脚本后 PASS，覆盖 runbook、gofmt、vet、build、依赖边界、Web typecheck 与生成文件一致性。
- `python3 docs/plans/check_tree.py`：PASS，55 个任务、8 个门禁。
- 生产接线搜索：无 `server/` 生产包导入 `internal/hooks`；应用 Router 尚不存在。

## 下一阶段

RUN-04 仍需等待 G1 及 LED-05、RUN-01、RUN-02、RUN-03。G1-INPUTS-CHECKLIST 的 IN-01 最小业务/授权事实和两家账户证据尚未提供。门禁打开前可继续独立准备，但不能把基础包测试、类型声明或自定义点注册冒充 Hooks 生产验收。

## 后续复核：进程内 Observer 调度器

2026-10-04 增加有界 `ObserverDispatcher`，解决“同步 Notify 只能由尚不存在的 worker 调用”的基础设施缺口。调度器接收已提交事件后非阻塞入固定队列；满队列明确返回错误，结果 handler 获得 Observer 失败并可接入运维策略。此实现的内存队列易失，且无生产事务调用方、持久 outbox 和 DecisionRecord revision/阶段摘要。因此矩阵中的生产 Hooks 接线、可靠投递及 A30/RUN-04 结论保持未完成。新鲜 package race、全量 `make test-contract`、`make check` 通过，细节见同目录 `README.md`。
