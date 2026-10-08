# T1 第二轮返工证据索引（T1-REWORK-2）

返工依据：[REVIEW-T1-REWORK-CODEX](../../REVIEW-T1-REWORK-CODEX.md)（5 项 P1 根因 B1–B5＋2 项 P2 验证问题 B6/B7）；本目录保留当轮修复和验收证据。
状态：T1.1–T1.3 维持 **verifying**；未进入 T2；未改变阶段前置。

## 文件

| 文件 | 内容 |
|---|---|
| [S1-freeze-decisions.md](S1-freeze-decisions.md) | 准入契约 v2 冻结（取代第一轮被推翻的 D-R03；业务不变量优先，不以"探针缺字段"推导身份规则） |
| [verify-full.log](verify-full.log) | S3 全量验收：make check、Go -race -count=1 全绿、web 120、build、第二轮探针 Go 20/20 + Web 16/16、第一轮适配回归、server 冒烟 |
| [probes-post-rework2.log](probes-post-rework2.log) | 第二轮探针复跑原始输出 |
| [probe-consumed-inputs.txt](probe-consumed-inputs.txt) | 永久测试消费的 43 个仓库文件路径及消费者定位（43/43 被 manifest 覆盖） |
| [rework2-source-manifest.json](rework2-source-manifest.json) | 终版逐文件 SHA256（162 文件，含门禁脚本/生成物/测试输入；`make verify-manifest` 门禁基线） |
| [REVIEW.md](REVIEW.md) | S4 独立审查结论、P1-1/P2-1..4 闭合记录、rev2 冻结修订 |
| [probe-adaptations-rev2.md](probe-adaptations-rev2.md) | S4 修复后的探针适配说明（full-boundary-valid 1 例双侧，业务期望不变） |
| codex-audit-original/ | 本轮 Codex 审查原始证据副本（探针复跑前保存，含首次预算失败与十次复跑日志） |
| ../T1-REWORK/、../T1-REWORK-codex-audit-original/、../CODEX-T1-REWORK-AUDIT/ | 历史轮次证据，保留不覆盖 |

## B1–B7 闭合摘要（逐项证据见 REVIEW.md 与 verify-full.log）

| 项 | 根因→修复 | 反例结果 |
|---|---|---|
| B1 消费顺序冒充身份 | **S4 发现 P2-1（factId 唯一性按 owner 留跨账户克隆通道）+P2-3（已核验事实旁的默认槽影子账本）→ 已修复（rev2a/rev2b）**：身份=已核验事实 (providerType,factId) 全局唯一；目录存在同维度事实时必须显式声明（默认槽影子账本拒绝）；组/order/ID 完全退出身份；limit 窗口身份规范化 | 同桶改 ID+加 order 2 拒绝；同 factId 换组/换 ID/跨账户拒绝；等价锚点拼写拒绝；已注册事实旁的无 factId 资源→providerFactRequired；两显式事实共存与显式事实重组正例通过 |
| B2 根 schema 未执行 | config.Validate 流水线嵌入根 schema（santhosh-tekuri 编译，嵌入副本与 contracts 字节一致测试） | 缺 enabled/semanticsRef/quantumExp/metered、enabled=null 全部在完整入口拒绝（带路径 FieldIssue）；合法 false/0 不误拒 |
| B3 证据存在≠适用 | 适用性规则：capacity 主体=owner（binding 可用账户证据）+payload metric/unit；initial_state 主体=资源自身+metric；selector 主体=组；组数量空间（quantumExp/currency）全等；grant 目标解析+namespace 边界 | token 引 USD initial、账户 token 引 binding 并发 bound、跨 quantum 组员、悬空 grant——四例全拒；合法作用域通过 |
| B4 目录未完整生效 | Descriptor 交付全部四类 schema（构造期非空校验）；资源按 ownerKind 解析到账户（失败=issue 不跳过）；SupportsDomain 上下文判断；KnownFact→FactMetadata 元数据核验 | Descriptor 三类 schema 交付探针通过；binding-owned 未知扩展拒绝；mail/send 域拒绝；已知 factId 错 metric/unit 拒绝 |
| B5 窗口重置时刻 | **S4 发现 P2-4（亚毫秒锚点准入接受/执行拒绝的分裂）→ 已修复（准入显式拒绝）**；自然日历边界（弃 1970 网格）；折叠/缺口候选枚举（最早实例/跳变后首个）；锚点毫秒精度 | 加德满都自然小时；柏林第一次 02:00；.500 保留；.123456 双侧拒绝；重置相位独立断言（7 时区×全 period，S4 另验 Chatham/Lord Howe/Santiago/NY） |
| B6 manifest 漏项 | snapshot.py 按消费集合全量覆盖（含门禁脚本与测试输入）；漂移自证三态 | 追加无害注释必报漂移；还原后干净；159→162 文件基线 |
| B7 预算阈值不稳 | 墙钟阈值改确定性断言：同一绝对 deadline 直接证据（跨重试从不重置）、协作超时、零残留、写者可继续 | 首次 51.97ms 失败日志保留为历史；新断言 -count=10 稳定 |

## 验证性质标注

- 纯规则/契约：make check + test-contract（-race -count=1）+ test-web（120）。
- 真库：make test-integration（modernc，-race）。
- 受控运行：server 冒烟（本地 SQLite，无外呼）。
- 真实供应商：无（与历轮相同，未获资料与测试范围）。
- UI 旅程/性能：未验（T2/T5/T6）。
