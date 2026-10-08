# T1.1 验收记录（Go/Web 工程、依赖边界、生命周期骨架）

状态：verifying（独立审查通过前不置 done）。

## 出口逐项

| 出口（任务卡 T1.1） | 结果 | 证据 |
|---|---|---|
| server/go.mod／go.sum、cmd/ant-server、internal 分层（Go 1.27 系列） | 通过 | server/go.mod（go 1.27，modernc.org/sqlite v1.60.1 等）；`.tool-versions`=golang 1.27.1（asdf 实际安装，非声明）；verify-check.log |
| 独立 web（React/TS strict/Vite/npm 锁） | 通过 | web/package.json+package-lock.json；tsconfig strict 全开；npm run typecheck=0 |
| 不建 Node 业务后端 | 通过 | 仓库无 Node 后端；web 仅为前端工程 |
| 组合根／端口与依赖检查 | 通过 | cmd/ant-server/main.go；scripts/check-deps.sh（domain 纯净/adapters 无网络/store 独占 SQL 等七条边），dependency boundaries OK |
| Makefile check/build/test-contract/test-integration/test-fault/test-web/test-e2e/bench | 通过 | 根 Makefile；check/test-contract/test-integration/test-web/build 全部 exit=0；test-fault/test-e2e/bench 无内容时非零退出（verify-smoke.log），未以空测试充数 |
| 最小 server 启动／ready／关闭骨架 | 通过（真实运行） | verify-smoke.log：真实迁移后 /healthz {"status":"ok"}、/readyz、SIGTERM 优雅关闭、同库重启幂等、缺 -db 退出 1 |
| Web 空态路由骨架，不填假数据 | 通过 | web/src/features/*（诚实空态文案）；web/tests/app.test.tsx 断言无 console.error |
| 启动不开未经核验外呼 | 通过 | main.go 仅 healthz/readyz；无 ProviderAdapter 注册、无外呼路径 |
| 无不受控 goroutine | 通过（静态+测试级） | server 仅 writer goroutine（有界队列、Close 排空）；go test -race 通过；goroutine 泄漏的专项证明属 T2.3/T6（runtime host 未实现，本批无其他常驻 goroutine） |
| D07 环境兼容证据 | 部分完成（属 T0.2 关闭） | Go 1.27.1（asdf 安装日志）+ modernc 真库集成通过，为 T0.2 提供输入；宿主部署定案仍开放 |

## 验证命令（实际执行，完整输出见同目录 verify-*.log）

- `make check` → exit 0（gofmt/vet/build/依赖边界/web typecheck）
- `make build` → exit 0（bin/ant-server + web/dist）
- 冒烟：`./bin/ant-server -addr 127.0.0.1:18099 -db /tmp/ant-t11-smoke.db` + curl + SIGTERM（见 verify-smoke.log）
- `make test-fault` / `make test-e2e` / `make bench` → exit 1（按设计：T3/T5/T6 范围，无测试即失败）

## 未决

- 独立审查（批次门槛）未完成前不置 done。
- D07 宿主环境核验（T0.2）开放。
