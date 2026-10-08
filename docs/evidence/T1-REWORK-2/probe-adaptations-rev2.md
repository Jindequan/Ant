# 第二轮 S4 修复后的探针适配说明（逐项）

S4 独立审查修复（P1-1/P2-1/P2-2/P2-3/P2-4）后，逐字复跑第二轮探针（`probes-post-s4-fixes.log`）：
Go 19/20、Web 15/16 通过。唯一残余失败双侧同为 `full-boundary-valid`，属 本轮 S3 明确授权的适配情形。

| 用例 | 原始业务输入/期望 | 未适配失败原因 | 适配转换（业务期望不变） |
|---|---|---|---|
| full-boundary-valid（Go+Web 各 1 例） | 完整合法配置→接受 | S4 P2-3 修订：目录已注册与资源 (metric,unit) 匹配的事实时，无 factId 的权益资源是未核验影子账本，不得可执行（`providerFactRequired`）。原正例恰好是无 factId 的单桶——正是本轮要关闭的"消费顺序/默认槽冒充身份"通道 | 永久测试注入已核验事实 `alpha-usd-grant-promo`（Go rework2_regression_test.go / Web contract.test.ts withFacts），业务断言"完整合法配置被接受"保留；未适配原件另测断言现为带 `providerFactRequired` 的草稿 |

其余 15 个 Go 配置例（含 14 个拒绝例的 messageKey 路径）、Descriptor 交付、3 个窗口例、Web 15 例全部逐字通过，无任何适配。

永久回归中的适配位置：
- server/internal/config/rework2_regression_test.go（injectFactIds + default-slot-with-registered-facts-rejected + S4 五个新负例）
- web/tests/contract.test.ts（withFacts + 6 个 S4 断言 + limit-anchor 变体适配）
