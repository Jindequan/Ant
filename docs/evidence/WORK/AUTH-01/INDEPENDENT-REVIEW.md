# AUTH-01 独立终验

**结论：PASS（G1-DEV 本地叶子接受）**。复核者 `独立复核者` 未参与 AUTH-01 实现；审查为只读，未读取真实凭证、访问供应商或修改文件。日期：2026-10-03。

## 复核结果

- 服务端 TokenStore 是身份与 grants 的唯一来源；body 无法自授权，跨 namespace 请求拒绝。
- 空身份、空 namespace、空 grants、未知权限或 token kind、存储读取失败均 fail-closed。
- 撤销后认证失败；并发 bootstrap 恰好一个成功，撤销后不能重新引导。
- Secret revision 是原子 insert-only；AES-GCM AAD 绑定 ref/revision，换 revision 或主密钥无法解密。
- SecretView 只暴露 ref/revision/mask；格式化与 slog 路径均脱敏；Authorization 不进入请求日志。测试覆盖模拟 handler，未宣称真实路由已接通。

## 独立验证

复核者在禁外网沙箱运行以下命令，35 项测试通过，exit 0，无 race 报告：

```sh
sandbox-exec -p '(version 1) (allow default) (deny network-outbound)' \
  env GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly \
  go test -race -count=1 -v ./internal/security/... ./internal/api/...
```

审查前后相同的 SHA-256：

| 文件 | SHA-256 |
|---|---|
| `server/internal/security/auth.go` | `f834493156e829e006cc2dfd87b790f6264df7d9c9ba6d1bd58c1bea3dbe06ba` |
| `server/internal/security/secretservice.go` | `fb015fb75033123276d3323a5590331b980a3507659525d33c9734ee87005cbd` |
| `server/internal/security/security_test.go` | `3eff5830ac08caa38ddbd94a13afe12acca1cc4c12e4181bcea36596221d5992` |
| `server/internal/api/middleware.go` | `3aeed068a7381f68a58ead3fe4d0e384cc3a1e540da1e1213554365b27ae4169` |
| `server/internal/api/middleware_test.go` | `8b4cf5f86dd30cd9e5fc7df6a57e87481b3db71dac7ea3a4eac0b76fdfdeef74` |

## 接线义务与边界

后续持久存储须保留 bootstrap 原子一次性和 revision 不可覆盖；真实 API 路由须逐端点执行授权并投影 SecretView；session/CSRF、主密钥权限及 Transport 明文使用边界仍需后续验收。全局源码 manifest 待当前 T2-DEV 文件组都复核后重签。

本 PASS 仅接受 AUTH-01 在 G1-DEV 下的本地实现，不代表 G1/G2 出站或任何真实供应商行为。
