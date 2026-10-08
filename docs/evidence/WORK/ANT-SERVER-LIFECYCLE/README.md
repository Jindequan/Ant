# ant-server 启停顺序

## 变更

- 执行 supervisor 在 HTTP listener 成功创建后才启动；监听地址不可用时不会让它与正在关闭的 store 并发运行。
- HTTP 服务异常退出时，在有限的 15 秒关闭上下文中依次停止 supervisor、等待执行主机、关闭 store。
- 收到进程关闭信号后的正常停机同样先结束 supervisor，再收尾执行主机，最后关闭 store。

这修正了组合根的资源生命周期次序；没有挂接测试领域策略或伪装生产供应商 adapter，实际操作提交路由仍待生产注册信息具备后接入。

## 验证

- `cd server && go test -race ./cmd/ant-server ./internal/runtime`：通过。
- `make check test-contract test-integration`：通过；包含 gofmt、vet、build、依赖边界、Web 类型／生成契约、全部 Go internal race 和 SQLite integration race。
