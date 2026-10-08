# W3 · 探针适配说明（原版逐字复跑的 3 个残余失败）

原版探针逐字复跑结果（`original-probes-post-rework.log`）：Web 17/17 全过；Go 28/31 过，3 个失败全部属于 本轮 W3 预告的"接口变更后探针需适配"情形。原始业务输入与业务期望全部保留，转换如下（永久化回归：`server/tests/integration/codex_regression_test.go`，33 子测试全过，-race）。

| 原探针用例 | 原始业务输入/期望 | 未适配失败原因 | 适配转换（期望不变） |
|---|---|---|---|
| TestCodexAuditCommandBudget | 5ms 预算、80ms 非协作回调；期望写者占用有界（字面 ≤40ms） | 非协作回调（不看 ctx 的 sleep）物理上不可被抢占；writer 在 Run 返回后的第一个 checkpoint 收回控制——字面 40ms 上界对这类回调不可达。这不是缺陷残留：预算契约（R09）明确"必须协作响应 ctx"，driver SQL 阶段由 ctx 取消 | 1) 协作回调用例：5ms 预算内退出（实测 5.8ms），writer 立即可用；2) 非协作回调：断言返回 ErrCommandBudget＋零业务残留＋后续控制命令可提交；3) 真实 SQLite 写锁 25ms 预算实测 24.7ms 返回、锁释放后控制命令 590µs 提交。能/不能承诺的边界写进 writer.go 文档与 budget_test.go |
| TestCodexAuditPermitAssembly | Prepare 后与许可"配对成功"；空期望不得免检 | 原探针用字面量 `deadbeefdigest` 发行许可、却用真实 prepared digest 验证并期待成功——这正是 R07 指出的缺陷形态，修复后的正确行为是拒绝错配 | 发行改用 prepared.RequestDigest()/CredentialRevision()；保留期望：真实配对 Validate 成功、空绑定期望失败、复用/撤销/跨 runtime/错配拒绝、并发恰一胜 |
| TestCodexAuditCapabilityFingerprint | 同域同池同 payload、不同 capabilityProfile ⇒ 不同摘要 | R10 冻结给 FingerprintInput 增加了 CapabilityProfile 必填投影字段；原探针未传该字段（其公开输入投影无法表达的正是被审缺口） | 构造 FingerprintInput 时显式传入两个 profile；期望不变：不同 profile 不同摘要（v2 前缀） |

其余 28 个 Go 探针用例（Arithmetic 3、ConfigCases 15、WireQuantity 2、Window 2、QueueConfig、RetryBudget、FutureSchema、OpenAPIUnion、PermitAssembly 的 empty-validation-context 子例、StaleObservationInput）逐字通过，未做任何适配。

注：run-probes.py 报告的 "Implementation files changed" 列表即本轮返工的实际改动集（集成者共享文件＋四名成员的独占文件），已由 manifest 记录。
