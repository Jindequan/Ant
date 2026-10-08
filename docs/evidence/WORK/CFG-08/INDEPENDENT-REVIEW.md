# CFG-08 独立终验

**结论：PASS（G1-DEV 本地 SQLite/localtest 服务叶子）**。复核者 `独立复核者` 未参与实现，审查只读、未读取真实凭证、未访问供应商。日期：2026-10-03。

## 复核结果

- 草稿保存支持持久化、版本 CAS 与可修复错误草稿；凭证材料字段（包括嵌套 `credentials.value`、`auth.token` 与 `credential_value`）在写入前拒绝。
- 活跃配置是全局单例；创建、更新、校验和激活均验证调用者 namespace 与现有 active revision 的授权边界，跨 namespace 读写拒绝。
- 校验基于持久化的精确草稿；激活事务内再次校验证据有效期、池/授权 namespace、凭证 ref/revision 存在性，原子写入不可变 revision、active pointer、control version、草稿状态和幂等 receipt。
- writer 回调返回值通过互斥结果槽同步；`SubmitWrite` 报错时服务返回零值，避免取消调用者与仍运行事务之间的竞态或暴露未提交结果。
- 失败激活保留现有配置；同命令重放返回持久化结果。

## 独立验证

独立复核者运行以下禁外网 race 测试，exit 0，无 race 报告：

```sh
GOPROXY=off GOSUMDB=off go test -race -count=1 -v ./internal/application ./internal/store/sqlite ./internal/security
```

复核前后相同的 SHA-256：

| 文件 | SHA-256 |
|---|---|
| `server/internal/application/config_service.go` | `ad1c049cf85eef5c6144b98a67991af7520fcecf2a9b5b3cb90a9fc0bf9d30cc` |
| `server/internal/application/config_service_test.go` | `9802736ddd89bc27326191515519550b372ed23fe4e71e1714cd95fcb0f67fa5` |
| `server/internal/store/store.go` | `74d73072aa51bfccc2761079b4ceb71de7f1f1492ea71b70f6ba564569e8999f` |
| `server/internal/store/sqlite/statements.go` | `c72165bf2eb421ba680c03226357b743b1890e0a7fc4c41f6fc35187e6a6f2fd` |

集成者另运行完整 `internal/...` 禁网 race 测试与根目录 `make check`，均通过。

## 边界

本 PASS 接受 CFG-08 的本地 SQLite/localtest 服务叶子；不代表完整 API/UI A26、完整 prepared-permit 竞争 A16、真实账户、真实供应商行为或 G1/G2 出站已验收。当前 active 配置按单机全局单例实现；多 namespace 管理策略仍需后续产品/权限模型支持。
