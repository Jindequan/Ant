# Chat Completions 适配器基础

日期：2026-10-05

## 实现范围

`server/internal/providers/chatcompletions` 提供不做网络访问的 provider adapter，必须由受控 HTTP transport 发出请求。它接受严格的文本消息字段、强制单个非流式 completion，并验证请求目标／adapter revision／credential ref 与执行计划相符。构造 adapter 必须注入计划上界校验、用量投影和业务完成分类三个纯策略；缺任一策略就不能启动。响应必须包含一个完整回答及一致的 prompt/completion/total token 用量，并提供可选的 cache/reasoning breakdown；仅输出回答正文，报告带 provider request ID、结束证据和时钟标记的投影 usage evidence。

适配器没有内置 token 上界、价格或“HTTP成功即业务成功”的推测。金额及所有计划维度必须由具体供应商与领域 projector 完整映射；工具调用、多选、流式与多模态请求在 claim 前被拒绝。请求显式支持互斥的 `max_tokens` 与 `max_completion_tokens`，以及标准 reasoning_effort 取值；具体字段含义、推理输出与供应商文档声明的上限偏差必须由 provider/domain PlanValidator 处理。解析保留通用 `prompt_tokens_details.cached_tokens`、`prompt_cache_hit_tokens`、`prompt_cache_miss_tokens` 与 `reasoning_tokens`；命中/未命中字段若出现必须成对提供、相加等于 prompt 总量，并与 cached_tokens 一致。这样既支持只给通用 breakdown 的兼容 API，也能精确保留 DeepSeek 的计费拆分。对于已知的 `insufficient_system_resource` / `aborted` 终止状态，分类器可在 usage 完整时记录失败或 unknown 并保留计量；未知 wire 状态仍拒绝。当前测试 projector 仅是本地 fixture，不能注册为生产策略。

协议形状参考[DeepSeek Chat Completions API](https://api-docs.deepseek.com/api/create-chat-completion/)和[阿里云百炼 OpenAI 兼容 Chat API](https://help.aliyun.com/en/model-studio/qwen-api-via-openai-chat-completions)。这不是已核验的生产供应商注册，也没有调用真实账户。

## 验证

适配器 race 用例覆盖严格输入、目标与凭证绑定、stream=false、fixture 上界策略、未映射维度 fail-closed、usage 总量一致性、DeepSeek cache hit/miss 精确保留及相互矛盾 breakdown 拒绝、截断输出分类及只持久化回答正文。Router 要求组合根提供纯请求校验器；`TestRouterValidatesDomainRequestBeforeClaim` 验证不支持的请求在 claim 前拒绝且不留 operation；`TestHTTPSubmitRunsSQLiteLedgerAndControlledTLSEndToEnd` 使用同一校验器和本地 fixture 策略，穿过 HTTP submit、SQLite claim/prepare/permit/consume、受控 TLS 请求、usage 结算与输出持久化。

协议依据：[DeepSeek Chat Completions API](https://api-docs.deepseek.com/api/create-chat-completion/) 说明 prompt 总量等于 cache hit 与 miss 之和，并提供两项字段；价格按 cache hit、miss、output 分开。[DeepSeek Models & Pricing](https://api-docs.deepseek.com/quick_start/pricing/) 当前价格还区分峰/谷时段。因此在计费投影接入前保留原始整数拆分，不由通用 adapter 估价。

最终验证：新增 output-bound 字段和 provider interruption 回归后，`go test -race ./internal/providers/chatcompletions`、`make check test-contract test-integration` 与 `make test-web` 均通过。全量后端结果包括 gofmt、vet/build、依赖边界、Web 类型与生成契约、全部 Go internal race 和 SQLite integration race；Web 为 244/244。
