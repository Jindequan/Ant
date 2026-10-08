# 整组排序与执行预留并发验证

日期：2026-10-05

## 验证内容

- `TestPreparationRejectsAuthorizationAfterPoolOrderChanges` 先对真实 SQLite 预留资源，再执行完整池排序事务（CAS、成员替换、活动配置 revision、control version、receipt、audit）。随后授权必须以 stale 失败，不写 dispatch permit；prepared attempt 安全关闭并释放预留后，按新池版本和优先级成功重新准备。
- `TestControlledTransportDoesNotSendWhenDurablePermitBecomesStale` 在授权和构造受控请求后执行同一完整排序事务。后续 Dispatch 被拒绝，TLS fixture 收到的请求数为零。
- `TestPreparedCancellationPreventsAuthorizationAndWireSend` 在真实受控请求准备完成后记录取消。授权失败、dispatch permit 为零、预留释放，TLS fixture 收到的请求数为零。
- `TestControlledTransportDoesNotSendWhenConsumeCommitReplyIsLost` 在 SQLite 成功提交 consume 后丢弃确认回复。Dispatch 不发请求；随后创建全新的 runtime/permit registry 并执行 lease sweep，持久操作进入 unknown，计数用量和不确定并发占用保留。

这验证排序事务与准备／授权／发出的先后关系、恢复时的资源处理和重新准备能力，不代表真实供应商协议或计费语义。

## 验证结果

Focused race 用例通过。`make check test-contract test-integration` 通过，包括 Go vet/build、依赖边界、Web 类型与生成契约检查、Go internal race 和完整 SQLite integration race。
