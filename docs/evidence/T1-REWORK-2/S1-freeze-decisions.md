# S1 · 准入契约 v2 冻结决定（第二轮返工）

冻结人：集成者。取代第一轮 W1-freeze-decisions.md 中被 REVIEW-T1-REWORK-CODEX 推翻的 D-R03 及窗口精度/折叠缺口；旧文件保留为历史。业务不变量优先于旧探针 JSON 形状；正反例均跑完整组合入口（`config.Validate(data, catalog)`，Web 同源）。

## D-R03-v2 · 物理身份与消费顺序彻底分离

- **身份来源**：权益桶物理身份 = 已核验事实 `(ownerKind, ownerID, providerFactId)`；事实元数据（metricId/unit）由目录提供并必须与资源一致。事实缺失/伪造的配置可保存草稿，不得成为可执行配置。
- **默认槽**：允许至多一个**无 factId** 的权益资源占每个分类槽 `(ownerKind, ownerID, metricId, unit, 规范化窗口身份)`——服务端派生的默认桶（S1：物理 ID 可由服务端派生）。同分类第二个无 factId 资源＝克隆，`duplicateCommercialFact` 拒绝（与 Codex 复现用例一致：改 ID+加 order 2 仍拒绝）。
- **混合禁止**：同分类内同时出现无 factId 与有 factId 资源 → 拒绝（默认槽与显式事实可能同桶，归属不明）。
- **组与顺序不参与身份**：providerOrder 只为已证实独立的资源排序；移动/复制资源到任何组、任何顺序、任何 ID 都不能产生第二份余额。组内成员仍须互异（同资源不重复出现）且同 factId 全局唯一。
- **限额（limit）**：分类身份不变，但窗口身份**规范化**（见 D-B5-v2 身份函数），锚点任意拼写不产生新限额。
- 正例：赠额+充值两显式事实共存（valid-catalog-two-facts）；单桶无 factId 默认槽（full-boundary-valid）。负例：同桶克隆六变体、factId 元数据错配、同组 quantumExp 不兼容。

## D-B2-v2 · 根 schema 在唯一后端边界执行

`config.Validate` 流水线固定为：`jsonx.DecodeStrict`（重复键/未知字段/尾随/规范数量）→ **根 JSON Schema 校验**（santhosh-tekuri 编译，嵌入副本与 contracts/schema 规范文件字节一致有测试）→ 有类型解码 → `ValidateStructural` 引用图 → 目录/上下文校验。schema 失败产出带路径的 FieldIssue。手写 required 判断不作为同源依据。

## D-B3-v2 · 证据适用性（存在+kind 正确 ≠ 适用）

- `initial_state` 证据 E 适用于资源 R ⟺ `E.subjectRef == R.resourceId` 且 `E.payload["metricId"]`（如存在）== `R.metricId`。
- `bound/hard_stop` 容量证据 E 适用于 R ⟺ `E.subjectRef == R.ownerId`（ownerKind=account 时严格；ownerKind=binding 时允许等于所属账户，即账户证据覆盖其绑定）且 `E.payload["metricId"] == R.metricId` 且 `E.payload["unit"]`（如存在）== `R.unit`。
- `entitlement_selector` 证据 E 适用于组 G ⟺ `E.subjectRef == G.groupId` 且 `E.payload["metricId"]`（如存在）== `G.metricId`。
- 组成员必须在同一数量空间：metricId、unit、**quantumExp**、currency 全等；不同精度/币种禁止隐式混用。
- grant 按 scopeKind 解析目标：pool → 存在且 `pool.namespace == grant.namespace`；namespace → `scopeId == grant.namespace`；offer/binding/account → 存在。悬空或 namespace 错配拒绝。

## D-B4-v2 · 目录完整交付与上下文支持判断

- `Descriptor()` 必须返回**全部四类 schema**（config/resource/capability/extension）＋事实清单；交付内容有测试断言（非空、可编译）。
- 资源→账户解析按 ownerKind：binding 资源经其 binding 解析到账户；解析失败形成 issue，绝不静默跳过扩展校验。
- 上下文支持：binding.domain 与 offer.domain 都必须 ∈ `SupportedDomains`，否则目录 issue（不可执行）。
- 目录接口：`KnownFact` 升级为 `FactMetadata(typeID, factID) (EntitlementFact, bool)`；校验使用元数据（metricId/unit 必须与资源一致），字符串存在性不构成核验。

## D-B5-v2 · 窗口语义（自然日历 + 显式折叠策略 + 精度）

- fixed 全部 period 用**当地自然日历**边界：minute/hour/day/week/month 都不使用历史纪元偏移网格。hour 窗 = 当地时钟 [H:00:00.000, 下一小时同相位)；anchor 的子相位（分/秒/毫秒）在每周期内生效。
- 锚点精度：支持到毫秒；亚毫秒精度在配置校验与 WindowAt **显式拒绝**（不静默截断）。
- **折叠/缺口显式策略**：歧义墙钟时刻（秋回）取**最早实例**（第一次重复，如柏林 2026-10-25 02:00 +02:00）；不存在的墙钟时刻（春跳）取**跳变后第一个有效时刻**。实现用候选枚举（±48h 扫描全部匹配实例后按策略选择），不依赖 time.Date 的偶然折叠选择。
- 窗口 end = 下一周期起点（同一策略解析）→ 秋回日日窗 25h、春跳日 23h 自然成立。
- **规范化窗口身份**（供 D-R03-v2 使用）：fixed = `kind|period|tz|当日墙钟 HH:MM:SS.mmm|（week）锚点星期|（month）锚点日`，从锚点时刻在规则时区的本地墙钟提取；不同拼写同一语义 → 同身份。
- 性质测试保持：start≤now<end、无缝不重叠、确定性；新增独立重置相位断言（加德满都自然小时、柏林第一次 02:00、历史偏移变更、半/四分之一小时时区）。

## D-B6-v2 · manifest 覆盖按消费集合定义

snapshot.py 改为**包含全部**被构建/检查/测试消费的源码、脚本、类型生成物与测试输入（server/、web/ 源与测试、contracts/、scripts/ 全部文件、web/scripts/、根文件，以及被永久测试读取的 `docs/evidence/*/config-cases.json`）；仅排除依赖缓存与构建产物目录。不以后缀白名单代替范围定义。验证覆盖集合本身：无害改动必须报漂移。

## D-B7-v2 · 预算测试确定性

以"同一绝对 deadline"的直接证据替代墙钟阈值：回调内读取 `CommandContext()` 的 deadline，跨重试断言绝对 deadline 单调不减（从不重置）；协作超时路径断言 ErrCommandBudget、运行次数有界、零残留、写者可继续服务。墙钟值仅记录不判分。首次失败日志与定向复跑日志都保留为历史。

---

## rev2 · S4 独立审查后的修订（2026-10-02，审查驱动）

S4 审查（先自提反例）发现 P1-1 与四处缺口；以下修订已实现并有正反例：

1. **D-B3-v2 rev2a（P1-1）**：selector 证据必须 `subjectRef == groupId` 且 payload metricId（如存在）匹配组——原实现漏了主体等值检查，A 组的权威证据可为 B 组背书。双侧已修+固定反例。
2. **D-B3-v2 rev2b（P2-2）**：bound/hard_stop 容量证据的 payload **metricId 必填**（原文"如存在"是冻结缺口——owner 持多 metric，无维度证据无法支持任何具体资源）；unit 如存在必须匹配。initial_state 豁免保留（主体=资源自身已锚定）。现有全部正例 payload 均含 metricId，收紧不破坏正例。
3. **D-R03-v2 rev2a（P2-1）**：事实是 provider 级——唯一性键改为 `(providerType, providerFactId)`（原 `(owner, factId)` 留下跨账户共享同一物理桶两本账的通道）。
4. **D-R03-v2 rev2b（P2-3）**：目录已注册与资源 (metricId, unit) 匹配的事实 when 资源无 factId → `providerFactRequired`（默认槽影子账本不得与已核验桶并存；"未核验配置不能成为可执行配置"）。**正例适配**：full-boundary-valid（Go/Web 测试）注入 alpha-usd-grant-promo，业务断言不变；Codex 探针同例按 S3 授权适配（逐项说明见 probe-adaptations）。合法负例：未适配原件现为带修复原因的草稿。
5. **D-B5-v2 rev2（P2-4）**：锚点精度准入与 WindowAt 完全一致——亚毫秒在 config 校验显式拒绝（anchorPrecisionUnsupported），不静默截断。

登记（不修改行为）：
- 窗口身份属于物理身份的必要维度（日上限≠终身上限是两个桶）；跨窗口分类的默认槽共存是表达力边界，激活时的证据核验（T2）兜底。
- metricId 大小写变体可产生两个分类——T2 事实元数据校验须覆盖 metric 词汇表（S4 P3-3）。
- grant 的 offer/binding/account 目标存在性即配置层全部可查边界（实体无 namespace 字段）；T2 授权检查须把 offer→binding→account 解析到 namespace 复核（S4 P3-4）。
- document.go schema 失败后的注释已改为如实描述（继续累积 issue，均判非法；S4 P3-1）。
- 合作路径预算测试的 60ms 墙钟为低风险自限断言，保留（S4 P3-2）。
