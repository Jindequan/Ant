# Q1 · 可信配置编译链冻结（第三轮返工）

冻结人：集成者。取代前两轮的身份/证据/目录决定中被第三次复核推翻的部分（默认槽、分类字符串身份、自证证据）；旧文件保留为历史。核心模型：**可信事实 → 配置约束 → 完整资源组合 → 可消费执行输入**。

## D-C1-v3 · 物理身份 = 服务端核验实例的解析结果

- 服务端（local/test 为受控快照；生产为核验端口）持有 **VerifiedScope**（账务范围：account 作用域或带别名列表的 shared 作用域）及其 **VerifiedResourceFact** 实例：`physicalKey`（服务端派生、全局唯一）、factId、metricId/unit/quantumExp/currency、metered（核验收费性质）、semanticsRef/revision、**规范窗口**、capacityBound、initialRemaining。
- 配置中每个资源必须**解析**到恰好一个已核验实例：
  - 有 factId：`FactResolver.Resolve(owner 的 scope, factId)`；解析后资源的 metric/unit/quantumExp/currency/metered/semantics 必须与实例一致（不一致=不可执行 issue）。
  - 无 factId（limit 常见）：按维度解析——(metricId, unit, 规范化窗口) 在该 scope 的实例中**恰好一个**匹配 → 解析成功；0 个=`factUnresolved`（草稿）；>1 个=`ambiguousFact`（必须显式声明）。
  - **默认槽撤销**：解析不到就不可执行，不再有"目录没列这个维度就自动获准"的匿名账本。
- 克隆判定：两个资源解析到同一 `physicalKey` → `duplicateCommercialFact`。改变 ID/组/顺序/窗口拼写（WindowAt 等价）不改变解析目标→仍是同一账；改 unit/quantum/window 为未核验值→解析失败→草稿。三类前轮绕过全部闭合。
- **规范窗口身份与执行同源**：解析匹配与身份使用 `WindowAt` 在固定参照时刻（跨 DST 两点）的 key 相等来判定"同一窗口规则"，不复制第二套解释；自然/显式午夜、hour 锚点仅取分秒相位、UTC/Etc 别名经执行等价自动合并。
- 必须支持：两个独立 billing scope 各有同类赠额（不同 physicalKey，合法共存）；多 account/binding 别名共享一个 scope（同 physicalKey，一账）；同物理容量跨 pool/offer 引用一账。

## D-C2-v3 · 类型化证据约束（存在≠适用≠一致≠足够）

- bound/hard_stop 证据为 TypedEvidenceClaim：必填 `metricId`、`bound`（规范十进制，精确解析，畸形拒绝）；可选 `unit`/`quantumExp`/`currency`/`windowRuleRevision`——**出现即必须与资源及其解析实例一致**（显式不兼容精度/窗口/版本拒绝）。initial_state 必填 `remaining`（规范解析）。
- 解析实例的 `capacityBound` 是容量上限：`configuredCapacity > capacityBound` → `capacityExceedsVerifiedBound`（证据之外不得增额；保守更低值合法）。证据 `bound` 与实例 `capacityBound` 不一致 → `evidenceConflictsVerifiedFact`（用户声明不能自证）。
- 实例的 metered/currency/quantum/semantics 约束资源同名字段；付费/免费来自核验实例，不由 UI bool 决定。

## D-C3-v3 · 目录承担上下文编译与完整 bundle

- ProviderDescriptor += `semantics` 注册表（semanticsRef+accepted revisions）与 `profiles`（profileId、domain、requiredDimensions: [{metricId, role}]）。
- **上下文 schema 映射并实际编译**：account 扩展→configSchema；resource 扩展→resourceSchema；binding/offer 扩展→extensionSchema。通用白名单不再是跨对象通行证（resource 上的 planName 被.resourceSchema 拒绝）。
- **注册解析**：resource.semanticsRef/revision ∈ 注册表；binding/offer 的 capabilityProfile ∈ 注册 profiles 且域匹配。
- **完整 bundle**：offer 必须覆盖其 profile 的全部 requiredDimensions（经资源解析到的事实 metricId+role），删除计费/并发限制的 offer 不可执行；合法可选限制可额外引用。UI 不能增删真实必需限制。
- 状态区分：`Validate`/`Compile` 返回 `DocumentValidation{Doc, Issues, Compiled *CompiledConfig}`——零 issue 且 `Compiled != nil` 才是"可编译为执行输入"；有 issue=可保存草稿+修复原因；激活事务仍属 T2.1。CompiledResource/CompiledOffer 携带解析后的物理键、数量空间、保守容量与完整组合。

## D-C4-v3 · 根 schema 错误必须定位字段

schemaErrorPath 遍历 jsonschema 校验错误的 causes 链，输出正确转义（`/`→`~1`、`~`→`~0`）的 JSON Pointer；required 类定位缺失字段。Web 侧已可定位，保持同源语料。

## 语料适配预登记

第三轮语料的 control 合法例按本模型**预计零适配**（无 factId 的 limit 走维度解析；entitlement 已带 factId；bound 证据已含 metricId+bound）。若实现中发现个别键缺失，按授权补充核验上下文并逐项记录。
