# 持久化操作事件的 HTTP/SSE 恢复验证

日期：2026-10-05

## 验证内容

`TestOperationEventsSSEReadsCommittedSQLiteRowsAndResumesCursor` 使用真实 SQLite 文件和 HTTP socket：

1. 建立一个运行中操作并先写入已提交的 `attempt_prepared` 事件。
2. 通过鉴权后的 SSE HTTP 请求读取首个事件。
3. 在连接保持期间向 SQLite 提交 `operation_terminal` 事件，确认 SSE 轮询只从持久事件表发现并发送该记录。
4. 以 `afterSequence=1` 重连，确认只返回序号 2 的终态事件，没有重复序号 1。

使用真实 socket 是必要的：事件 writer 设置逐条写入 deadline，`httptest.ResponseRecorder` 不实现该能力，不能代表网络 handler。

## 验证结果

`make check test-contract test-integration test-web` 通过，包括 Go internal race、全部 SQLite integration race、Web 244/244、类型检查、生成类型检查、Go vet/build 和依赖边界检查。

## 范围

这证明已提交事件的 HTTP/SSE 读取与序号重连恢复。它不证明主组合根已挂载操作提交／执行器，也不代替真实供应商调用验证。
