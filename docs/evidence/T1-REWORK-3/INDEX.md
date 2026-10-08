# T1 第三轮返工证据索引（T1-REWORK-3）

返工依据：[REVIEW-T1-REWORK2-CODEX](../../REVIEW-T1-REWORK2-CODEX.md)（3 项 P1 根因 C1–C3＋1 项 P2 C4）；本目录保留当轮修复和验收证据。
状态：T1.1–T1.3 维持 **verifying**；未进入 T2；未改变阶段前置。

## 文件

| 文件 | 内容 |
|---|---|
| [Q1-freeze-decisions.md](Q1-freeze-decisions.md) | 可信配置编译链冻结：解析式物理身份（VerifiedScope/VerifiedResourceFact/physicalKey）、类型化证据约束、目录上下文 schema+完整 bundle、CompiledConfig 端口 |
| [verify-full.log](verify-full.log) | Q3 全量验收 + Q4 修复后终验 |
| [probes-post-fixes.log](probes-post-fixes.log) / [probes-post-q4-fixes.log](probes-post-q4-fixes.log) | 探针逐字复跑原始输出（最终：Go 27/27、Web 17/17） |
| [rework3-source-manifest.json](rework3-source-manifest.json) | 终版逐文件 SHA256（168 文件；`make verify-manifest` 门禁基线） |
| [REVIEW.md](REVIEW.md) | Q4 对最终树的独立审查结论与闭合记录 |
| codex-audit-original/ | 本轮 Codex 审查原始证据副本 |

## C1–C4 闭合摘要（Q4 对最终树独立验收）

| 项 | 结论 | 关键证据 |
|---|---|---|
| C1 物理身份 | 关闭 | 身份=解析到服务端核验实例（physicalKey）；独立 scope 同名 fact 合法共存（不同 key）、shared scope 别名跨账户=同账克隆、三种窗口等价拼写合并、metric 大小写=草稿；执行等价用 WindowAt 双参照时刻与执行同源 |
| C2 证据约束 | 关闭 | configuredCapacity ≤ 核验 bound（999999 合法编译、1000001 拒绝且 Compiled=nil）；bound 畸形拒绝；currency/quantum/window/revision 显式错配拒绝；initial remaining ≤ bound |
| C3 上下文目录+完整束 | 关闭 | 四类 context schema 实际编译（resource 上 planName 拒绝）；semantics/profile 注册表（未注册拒绝）；profile requiredDimensions 束覆盖（删计费/并发拒绝）；Q4 补 profile 域匹配 |
| C4 字段定位 | 关闭 | required 定位 /resources/0/metered；键 a/b、x~y → a~1b、x~0y 正确转义，双侧一致 |
| Q4 新增 | 已修 | P1 空 capabilityProfiles panic（改为遍历声明+回归）；P2-1 profile 域匹配强制+死代码移除；P2-2 门禁宣称措辞更正；P3-1/P3-3 死代码与过时注释 |

## 验证性质标注

- 纯规则/契约/编译链：make check + test-contract（-race -count=1）+ test-web（156）。
- 真库：make test-integration（modernc，-race）。
- 受控运行：server 冒烟（本地 SQLite，无外呼）。
- 真实供应商：无（未获资料与测试范围）。UI 旅程/性能：未验（T2/T5/T6）。
