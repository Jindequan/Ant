# AUTH-01 实施记录（已接受：G1-DEV 本地叶子）

2026-10-03。门禁 G1-DEV（本地禁网实现授权，见 TASK-TREE §5）。实现与独立终验记录见 [独立终验](INDEPENDENT-REVIEW.md)。本叶子仅接受 G1-DEV 本地禁网实现，不构成 G1/G2 或真实供应商行为验收；全局 manifest 待当前 T2-DEV 文件组复核后重签。

## 交付（写集内，5 个新文件，零改动已签署 184 文件）

- `server/internal/security/auth.go`：TokenStore port＋内存实现（只存 SHA-256 hash）、BootstrapAdmin（一次性 CLI 引导）、AuthProvider.Authenticate（服务端 grants 推导身份，统一 ErrUnauthenticated 不区分失败形态）、Can（namespace 严格相等＋白名单权限；跨 namespace 全拒）、ConstantTimeEqual。
- `server/internal/security/secretservice.go`：SecretService 实现 T1 冻结 SecretResolver 接缝（secrets.go 未动字节）；AES-256-GCM、每记录新随机 nonce、ref+revision 作 AAD（换 revision/换主密钥解密失败）；SecretView（ref/revision/mask）是写路径唯一外部可见形态；SecretCleartext String/GoString/LogValue 恒为 [REDACTED]；未提供可整体导出明文的 Map 方法。
- `server/internal/api/middleware.go`：AuthMiddleware（Bearer→Principal，401 统一）、RequirePermission（403）、AssertNamespace/GuardHandler（body 声明 namespace≠服务端 namespace → 403 namespace_mismatch，body 不能自授权）、LogRequest（只记 principalId/namespace，不记 Authorization）。
- 测试：`server/internal/security/security_test.go`、`server/internal/api/middleware_test.go`；覆盖 AUTH-01 的拒绝、并发与脱敏反例。

## 验收覆盖（对应任务卡三条验收）

1. body 不能自授权：`TestPrincipalDerivesFromServerGrantsNotRequestBody`（body 声明 ns-beta 仍得 ns-alpha）、`TestBodySelfAuthorizationIsRejectedWith403`。
2. 跨 namespace 读写/取消拒绝：`TestCanDeniesCrossNamespaceEveryPermission`（read/cancel/manage 全测）、`TestCrossNamespaceAccessDeniedForReadCancelManage`。
3. 日志/GET/前端不泄密：`TestSecretViewMaskNeverCarriesCleartext`、`TestSecretCleartextRedactedInStringAndLogs`（slog 字段与格式化双路径）、`TestLogsAndResponsesNeverCarriesCredentials`、`TestLogOfBadRequestsCarriesNoAuthorizationHeader`。
   另有失败闭合：`TestAuthenticateFailsClosedUniformly`、`TestSecretBoundToRefAndRevision`（未知 ref/revision 同一错误；跨 revision AAD 拒绝；错主密钥拒绝）、`TestRevokedTokenFailsAtTheBoundary`、`TestBootstrapTokenStoredAsHashOnly`。

## 测试命令与结果（Codex 独立重跑）

在 `server/` 下执行；macOS Seatbelt 明确拒绝 outbound networking，Go 依赖下载关闭：

```sh
sandbox-exec -p '(version 1) (allow default) (deny network-outbound)' env GOPROXY=off GOFLAGS=-mod=mod go test -race -count=1 ./internal/security/... ./internal/api/...
# ok ant/server/internal/security 2.013s
# ok ant/server/internal/api      2.165s
sandbox-exec -p '(version 1) (allow default) (deny network-outbound)' env GOPROXY=off GOFLAGS=-mod=mod go vet ./internal/security/... ./internal/api/...
# exit 0
sandbox-exec -p '(version 1) (allow default) (deny network-outbound)' env GOPROXY=off GOFLAGS=-mod=mod go build ./...
# exit 0
sandbox-exec -p '(version 1) (allow default) (deny network-outbound)' env GOPROXY=off GOFLAGS=-mod=mod go test -race -count=1 ./internal/...
# all internal packages pass; exit 0
```

HTTP 测试全部经 `httptest.NewRequest`＋`ServeHTTP` 直接驱动 recorder，无监听 socket；源码未新增任何 HTTP/client/dial/listener 外呼。无真实凭证（测试仅用 `sk-fake-*`/假 token），无供应商请求。`GOPROXY=off` 排除 Go 模块下载。


## Codex 复核及修订

Codex 复核原实现发现并修正：内存测试存储的并发访问保护缺失；bootstrap“一次性”约束未实际执行（现在通过原子 `PutBootstrapIfAbsent`，撤销后仍不允许再引导）；`%#v` 可绕过 `String`/`LogValue` 的脱敏。为使未来存储实现能统一处理读取故障，`TokenStore.Lookup` 现有 error 返回并在 AuthProvider 侧将故障统一折叠为 ErrUnauthenticated。加入并发访问、并发一次性引导、撤销后不可重引导及 %#v 泄漏回归。具体最终树仍需独立终验。

2026-10-03 进一步只读审查发现一个版本完整性缺陷：`SecretRecordStore.PutSealed` 对相同 `(ref, revision)` 使用覆盖语义，允许已引用 revision 后续替换成不同凭证。现改为存储端原子 insert-only，重复 revision 返回 `ErrSecretRevisionExists`，rotation 必须分配新 revision；新增重复写与并发创建竞态测试。定向 security/API race 测试、全量 `./internal/...` race、vet 与 `go build ./...` 均在出站禁网沙箱通过。此为 Codex 自审修订，不作为独立审查证据，AUTH-01 继续 verifying。

同轮自审还覆盖了 `TokenStore` port 的实现边界：认证出口此前直接返回 store 提供的 permission slice；若未来实现返回别名切片，调用者可通过修改 Principal 反向改写授权存储。现由 `Authenticate` 复制 grants，并加入刻意返回 backing slice 的 store 回归测试。最终写集上的 targeted security/API race、全量 `./internal/...` race、`go vet` 与 `go build ./...` 均在出站禁网沙箱通过。独立终验当时仍未完成。

2026-10-03 Codex 扩展复核发现 fail-closed 边界：`AuthProvider.Authenticate` 此前信任 TokenStore 返回的任何存在记录，即使 principal/namespace 为空、permissions 为空或含未知值、token kind 非法。内存 store 写入也允许其中一部分形态。现增加共享 `validTokenRecord`，在内存 store 写入及认证出口都拒绝非法身份/grants；新增非法记录反例测试。扩展复核后的 `go test -race -count=1 ./internal/security/... ./internal/api/...`、出站禁网的全量 `go test -race -count=1 ./internal/... ./tests/integration/...`、`go vet ./...` 与 `go build ./...` 全通过。该修订仍是 Codex 复核，不替代最终审查。

后续根目录 `make check` 首次运行发现 T2 已引入的依赖边尚未登记：API middleware→security、ledger/commands→store，以及 API 作为唯一新增 `net/http` 边界。集成者在 `scripts/check-deps.py` 仅登记这三条窄边，并以反向探针确认 `ledger` 根包仍不能导入 store；完整禁网 `make check`（gofmt、vet、build、check-deps、Web typecheck、generated guard）通过。该共享门禁扩展待随最终源码树一并复核/签署。

## 边界与未决

- 本批未实现：session/CSRF 细节、SQLite token/grant/secret_records 表绑定（归 CFG-08/API-01 接线，port 已留）、mask 展示规则若 CFG-08 需调整不得扩权。
- 该历史快照当时报告 11 个新增实现文件，且已签署的 184 文件没有修改/删除。此后 T2-DEV 文件组继续扩展；当前全局 manifest 状态以 STATUS 和后续合并复核为准，本记录不自行重签。
- AUTH-01 仍为 verifying：原实现完成后，Codex复核并补入并发锁、一次性bootstrap原子端口、 %#v 脱敏、secret revision不可覆盖、grants切片隔离和非法TokenRecord fail-closed。扩展测试通过，但这不能替代外部最终复核。状态未标 done。
