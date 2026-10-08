# 实时资源池 Web 接入

日期：2026-10-05

## 实现

- Web 池页读取 `GET /v1/pools` 的 SQLite 实时投影，不再从配置文档拼装假实时状态。
- 展示成员的可用/受阻/停用状态、逐资源可用量和观测刷新提示；`available` 缺失时显示“未知”，不会换成 0。
- 整组排序调用 `PUT /v1/pools/{poolId}/order`，发送完整成员集合、显式 priority、当前 pool version、active config revision、强 `If-Match` 和 CSRF token。
- 成功后使用 API 返回的新投影刷新页面。版本冲突会重读池及配置修订；本地排序保留仍然存在的成员，并补入并发新增的成员。
- 中英文文案同步；覆盖加载、登录、权限、请求失败、空池和排序交互。

## 验证

命令：`make check test-contract test-integration test-web`

结果：通过。`go vet`、Go build、依赖边界检查、Web TypeScript 类型检查、生成类型漂移检查、Go internal race、SQLite integration race 和 Web 244/244 测试均通过。

另运行 `cd web && npm run build`，Vite 生产构建通过。

Web 回归验证了实时可用量展示、完整排序请求字段、版本头、CSRF、写入响应刷新和当前排序顺序。

SQLite/HTTP 集成用例 `TestPoolHTTPActivationProjectionAndFullOrderRoundTrip` 另外从真实配置草稿开始，验证激活投影、鉴权后的 `GET /v1/pools`、完整 priority 写入、排序响应、详情回读及活动配置 revision 中保存的值一致。用例将本地 fixture 时钟固定在证据有效时点，避免随当前日期漂移。

## 范围

这完成资源池读取与排序的 Web/API 连接。供应商和资源页仍以已激活配置为数据源；操作提交路由、生产供应商 adapter 和真实账户执行仍未完成，不能据此宣称 T2 或项目完成。
