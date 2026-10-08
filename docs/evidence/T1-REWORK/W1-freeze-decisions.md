# W1 · 共享契约冻结决定（返工批次）

冻结人：集成者（主会话）。以下决定已落入可编译/可校验实物与正反样例，各成员以同一版本开发。修改这些共享文件的需求一律回集成者，禁止并行改。

## D-R03 · 商业事实/桶身份

- 物理商业事实＝供应商侧一本账。**limit** 类事实身份 = `(ownerKind, ownerID, metricID, windowIdentity)` 分类；同分类第二份即克隆，`duplicateCommercialFact` 拒绝。
- **entitlement 桶**允许多桶共存（赠额+充值）。桶独立性由 **同一 entitlement group 内不同 providerOrder 的成员**声明（组的权威性来自 `entitlement_selector` 证据）。同分类出现在不同组、孤桶+组、多个孤桶＝同一桶伪装，拒绝。
- 可选 `providerFactId`（ResourceDef 新字段）来自注册目录 `ProviderDescriptor.EntitlementFacts`；`(owner, providerFactId)` 全局唯一；目录核对在边界入口 `config.Validate` 执行（`ValidateStructural` 保持无目录可测）。
- 权益组每 `(scopeKind, scopeId, metricId, unit)` 唯一（一个 waterfall 一份证据）。
- 实物：`internal/config/validate_graph.go`（checkCommercialIdentity）、`contracts/fixtures/api/manifest.json` configFixtures、探针 `valid-gift-and-prepaid-buckets` 为合法正例。
- 同桶跨绑定/池＝引用同一 resourceId，天然共享账本，无复制。

## D-R02/R12 · 配置分层与目录

- 分层：原始形状（`jsonx` 严格解码：未知字段/重复键/尾随数据/规范数量）→ 有类型引用图（`ValidateStructural`，无目录可测）→ 目录/schema（`Validate(data, ProviderCatalog)`：注册类型、受限 extensionSchema、KnownFact）。错误草稿可保存；validated/activated 要求零 issue（T2.1 ConfigService 执行，本批交付边界入口）。
- 引用图边（每边有负例）：binding.accountId、binding.resourceRefs、offer.bindingId、offer.resourceRefs(⊇binding)、offer.domain/capabilityProfile==binding、offer.requiredEvidenceRefs(kind=bound)、resource.ownerId(按 ownerKind)、resource 容量证据(kind=bound|hard_stop)、初始状态证据(kind=initial_state)、resource→group 双向、group.scopeId(按 kind)、组成员 metric/unit==组、组成员 owner==scope、组唯一性、selectorEvidence(kind=entitlement_selector)、evidence.subjectRef(存在+按 kind 分型)、pool.offerId、grant 形状、根数组非 nil（缺失≠空）。
- local/test 目录：`contracts/providers/provider-alpha.descriptor.json`（受限 extensionSchema 封闭 allow-list + 两个 entitlementFacts），Go `internal/providers/localtest` 编译 schema 消费；嵌入式副本与规范文件字节一致有测试。

## D-R04/R05 · wire 权威

- `Units` 自定义 Marshal/UnmarshalJSON 取代 `,string` tag（marshaler+tag 双重引号；内建 string 解码丢失原拼写）。wire 一律规范十进制串；裸数字/null/浮点/指数/前导零/"-0"/溢出在 UnmarshalJSON 拒绝。算术按上下界 checked（见 D-R01）。
- SubmitReply 在 OpenAPI 3.0 用 **oneOf 四变体**（每 kind 必需/禁止字段 + state const）表达，description 不再承载可执行约束；顶层 state 与 result.state 一致性属 guard 规则，两侧守卫按同一 fixture 验证。
- spec 版本 `1.1.0-t1-rework`；四个列表投影标 `x-ant-pending: T2.1`；admin 写路由（adjustments/reconciliations/clear/observations/rotate/cancel）补请求体与响应 schema。
- fixture manifest（`contracts/fixtures/api/manifest.json`）是分层预期的唯一权威：schema/codec/guard/web 每层 accept/reject + issueKey；Go contracttest 与 Web contract.test.ts 逐 fixture 按清单断言。重复 JSON 键只在原始文本层可查（codec 层），schema 层不可见——清单已注明。

## D-R10 · 摘要

- `FingerprintInput` 增加 `CapabilityProfile`；哈希文档含 `capabilityProfile`，前缀 `ant:semantic:v2`。v1 从未持久化，无重放兼容负担（03-EXECUTION §2 已登记映射与升版规则）。profile 语义等价只能由 DomainPolicy 显式归一化决定。

## D-R11 · 规则事实输入

- `ResourceContext.AdjustmentVersion`＝账本权威当前版本；观察比较对象是它，不是观察自带版本。
- `SettlementProjection{Actual, ChargedAt, End EndProofKind, EvidenceRefs, Attribution WindowAttribution}`：结束证明分 none/reliable_unsent/external_ended；证据 ID 列表只记录不证明。归属分 attributed/unattributed。
- `ResourceChangePlan.IsolationRequests []AttributionIsolation`：未归属结算必须随计划请求隔离（Ledger 同事务附 encumbrance/hold，T2.2 执行），禁止只发 reason 后释放容量。

## D-R07 · 句柄

- 组装顺序：Prepare → 以真实 digest/credentialRevision 发行 permit → Validate 全绑定事实 → 受控消费。`Validate` 空绑定期望＝失败（不再免检）。
- `MarkUsed` 返回防御性拷贝（字节所有权：校验后其他模块无法改动待发内容）；一次性标记互斥有并发测试。
- 持久 CAS 与真实单次发送验收归 T2.2/T2.3，本地测试不冒充。

## D-R09 · 预算

- 写命令预算＝**一个绝对 deadline**：出队时计算一次，首次尝试、busy 等待、全部重试共享；重试前查剩余。回调契约：禁网络/消费者等待/无界 sleep，必须协作响应 ctx（注释与测试共同约束）。不用后台遗留事务实现超时。
