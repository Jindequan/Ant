# T1 第二轮返工 · S4 独立审查结论与闭合记录

审查者：独立审查独立审查者（未参与本轮实现）。方法按 S4：先读业务不变量与两轮审查史，独立构造反例（"正确类型但错误对象""同语义不同拼写""正常返回对象丢字段"），经 Go/Web **完整组合入口**运行；并倒查冻结决定是否满足原不变量。全文见审查者报告（会话存档）。

## 一、B1–B7 裁定（S4 审查时点）

B1/B2/B4/B6/B7 关闭（各有独立反例验证）；B5 实现侧关闭（7 个新窗口组合+分区性质独立验证：Chatham/Lord Howe 30 分钟 DST/Santiago 午夜折叠/NY 折叠 25 小时窗/Berlin 春跳秋回/月末钳制）；**B3 未关闭**——P1-1。另发现 P2-1..P2-4 与 P3-1..P3-4。

## 二、审查发现与闭合

| 级别 | 发现 | 闭合 |
|---|---|---|
| **P1-1** | selector 证据缺 `subjectRef == groupId` 适用性检查（双侧同缺，违反 D-B3-v2 冻结文本）：A 组的权威选择证据可为 B 组背书 | **已修复**：Go checkEntitlementGroups/checkEvidence 与 Web config-graph 三处加主体等值检查；新增固定反例（selector 指向另一组双侧拒绝，evidenceNotApplicable） |
| P2-1 | factId 唯一性按 (owner, factId)——跨账户共享同一 provider 事实产生两本账 | **已修复（D-R03-v2 rev2a）**：唯一性键改 (providerType, factId)；反例：第二账户引用同 factId 拒绝 |
| P2-2 | bound/hard_stop 容量证据 payload metricId "如存在才检查"——无维度证据可为任意资源背书（冻结文本自身缺口） | **已修复（D-B3-v2 rev2b）**：capacity 证据 metricId 必填；反例：删 payload.metricId → evidenceNotApplicable；全部正例不破坏 |
| P2-3 | 目录已有同维度已核验事实时，无 factId 资源仍可执行（影子账本） | **已修复（D-R03-v2 rev2b）**：`providerFactRequired`；正例适配（注入已核验 factId，业务断言不变，有先例且逐项记录）；未适配原件现为带修复原因的草稿（另测断言） |
| P2-4 | 亚毫秒锚点 config 准入接受而 WindowAt 拒绝（准入/执行分裂） | **已修复（D-B5-v2 rev2）**：准入显式拒绝（anchorPrecisionUnsupported）；反例：.123456 双侧拒绝、.500 保留 |
| P3-1 | schema 失败后注释与行为不一致（继续累积 issue，无正确性影响） | 已修正注释 |
| P3-2 | 合作路径预算测试留 60ms 自限墙钟（3 倍余量，低风险） | 保留，登记 |
| P3-3 | metricId 大小写变体可产生两个分类（无 metric 词汇表） | 登记 T2 义务 |
| P3-4 | grant 的 offer/binding/account 授权链 namespace 复核须在 T2 落实 | 登记 T2 义务 |

冻结倒查四问结论（审查者）：① 窗口维度属于物理身份的必要维度，跨窗口默认槽共存为表达力边界（登记）；② 默认槽影子账本违反 B1——按建议修订为 P2-3 并实现；③ "如存在"是冻结缺口——按建议收紧为必填；④ grant 三类目标存在性即可查边界（实体无 namespace 字段）——可接受，T2 复核义务登记。

双侧同源对拍：审查者 13 个变体 Go/Web 判定 13/13 一致（含全部问题通道——三个错误通道为双侧共同缺口，修复双侧同步）。临时探针删除并确认；manifest 162 文件零漂移。

## 三、P1-1/P2 修复后终验（集成者执行，日志见 verify-full.log）

- make check=0（依赖图/生成漂移/manifest 三门禁）
- Go `-race -count=1`：183 PASS / 0 FAIL / 0 SKIP（含 S4 五个新反例与适配后的语料回归）
- Web：125 passed；build=0
- 第二轮探针逐字复跑：Go 19/20、Web 15/16——唯一残余 `full-boundary-valid` 为 S3 授权的适配例（P2-3 语义修正使未适配原件正确变为草稿；业务断言"完整合法配置被接受"由注入已核验事实保留），逐项说明见 probe-adaptations-rev2.md
- 第一轮适配回归全过；manifest 重签零漂移

## 四、总体结论

**B1–B7 全部闭合（B3 的 P1-1 与四项 P2 经 S4 审查发现后修复，均有双侧反例与永久回归）；冻结决定经审查倒查并修订（rev2），修订由独立审查驱动而非实现者自证。本批可交 Codex 第三次复核。** T1.1–T1.3 维持 verifying；未进入 T2；未改变阶段前置。

层级结论：工程（三门禁+清单零漂移）✅；纯规则（算术/窗口/准入图，反例级）✅；wire（union/数量 codec）✅（本批无新增变更）；真库（modernc 集成/预算确定性/迁移）✅；受控网络（句柄本地级）✅；真供应商/UI 旅程/性能 未验（T0/T1.4/T2/T5/T6）。
