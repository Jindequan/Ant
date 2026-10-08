# T1 返工批次证据索引（T1-REWORK）

返工依据：[REVIEW-T1-CODEX](../../REVIEW-T1-CODEX.md)（复核不通过：13 P1 / 4 P2）；本目录保留当轮修复和验收证据。
状态：T1.1–T1.3 维持 **verifying**；本批未进入 T2，未改变任何阶段前置。

## 文件

| 文件 | 内容 |
|---|---|
| [W0-closure-table.md](W0-closure-table.md) | R01–R17 触发/根因/修改位置/断言/阶段 闭合表；复现记录（Go 24/Web 12 失败，实现文件零改动） |
| [W1-freeze-decisions.md](W1-freeze-decisions.md) | 共享契约冻结决定（商业桶身份、配置分层+目录、数量 codec、OpenAPI oneOf、摘要 v2、规则事实输入、句柄配对、整命令预算） |
| [W3-probe-adaptations.md](W3-probe-adaptations.md) | 原版探针逐字复跑的 3 个接口适配情形逐条说明（业务期望不变） |
| [verify-full.log](verify-full.log) | W3 全量验收：make check、Go -race -count=1 全绿（172 顶层测试 PASS / 0 FAIL；含 11 个 TestCodexRegression*，其 33 个子测试为适配回归）、web 98、build、原版探针、适配回归 |
| [original-probes-post-rework.log](original-probes-post-rework.log) | 原版探针复跑原始输出 |
| [rework-source-manifest.json](rework-source-manifest.json) / [rework-source-manifest-final.json](rework-source-manifest-final.json) | 返工开始 / 终版逐文件 SHA256（148 文件，scripts/snapshot.py 可重建；`make verify-manifest` 门禁） |
| [REVIEW.md](REVIEW.md) | W4 独立对抗审查结论与 P2-1 闭合记录 |
| codex-audit-original/（见 docs/evidence/T1-REWORK-codex-audit-original/） | Codex 原始审查证据副本（探针运行前保存） |

## 每项闭合摘要（详见闭合表与各成员报告）

| 项 | 状态 | 关键实物 |
|---|---|---|
| R01 算术 | 关闭 | quantity.go 上下界 checked 运算＋quantity_oracle_test.go（big.Int oracle，Min/Max/−1/0/1 全组合+700 组生成输入）；3 反例通过 |
| R02 引用图 | 关闭 | validate_graph.go 全量有类型引用图＋Validate 边界入口＋15 共享 config-cases Go/Web 双侧按预期；12+1 非法 fixture 每边负例 |
| R03 桶身份 | 关闭 | 权益组 providerOrder 声明桶独立性＋(owner,providerFactId) 唯一＋多孤桶/跨组拒绝（两桶正例通过；同桶伪装负例拒绝） |
| R04 数量 | 关闭 | Units 自定义 JSON（规范串）＋openapi/config schema 规范 pattern＋Web BigInt；010/-0/null/裸数字/浮点/溢出全层拒绝 |
| R05 wire | 关闭 | OpenAPI oneOf 可执行 union（VisitJSON 实测拒绝 accepted+succeeded）＋manifest 分层预期＋生成类型实际消费＋guard:gen 漂移门禁（实测非零） |
| R06 窗口 | 关闭 | window.go 日历化＋锚点/时区/DST/闰日性质测试（start<=now<end、无缝不重叠）；探针两例通过；配置支持集与实现一致 |
| R07 句柄 | 关闭 | 真实摘要发行→配对→受控消费；空期望失败；防御性拷贝；并发恰一胜（-race） |
| R08 参数校验 | 关闭 | 先默认后校验、校验先于打开、14 组非法组合带字段错误、失败路径资源回收 |
| R09 预算 | 关闭 | 单一绝对 deadline 共享全部重试；协作回调预算内退出；真实锁竞争耗尽预算后控制命令可推进；零残留 |
| R10 摘要 | 关闭 | FingerprintInput.CapabilityProfile＋v2 前缀；不同 profile 不同摘要 |
| R11 规则事实 | 关闭 | ctx.AdjustmentVersion 权威版本；End/Attribution 结构化证明；未归属必带 IsolationRequests |
| R12 目录 | 关闭 | local/test ProviderDescriptor（受限 extensionSchema+entitlementFacts）驱动 Go/Web；嵌套/大小写秘密双层一致拒绝 |
| R13 迁移 | 关闭 | max>1 拒绝（upgrade required）；busy_timeout defer 恢复（含失败路径） |
| R14 证据/门禁 | 关闭 | snapshot.py 逐文件 manifest＋manifest-diff 门禁＋check-deps.py 强制图检查（列包失败非零）＋Makefile 强制 -race/-count=1 |
| R15 SSE | 关闭（本批范围） | URL 解析、已 abort 不建连、close 移除监听＋12 用例；恢复游标参数化；真存储/重连归 T2.4 |
| R16 UI | 关闭（本批范围） | 用户语言文案、可见禁用原因（aria）；结论改为"壳与空态骨架已验证，业务可用性未验收" |
| R17 前置 | 关闭 | STATUS/任务卡/交接一致：不进入 T2、不缩水 T0/T1.4 前置、D 项不自动关闭 |

## W4 审查后追加修复（2026-10-02）

- P2-1（审查发现）：返工中 openapi.yaml Quantity description 修改后未重跑 generate:types，导致 schema.d.ts 漂移一行注释且 guard:gen 未接入标准入口。修复：重新生成 schema.d.ts、`make check` 增加 `npm run guard:gen`、manifest 重签。已实测 guard:gen 对注入漂移非零、对最终树为零。
- P3-1：两个 quantity 拒绝 fixture 补 Go 双层（codec+schema）按名消费者（contracttest）。
- P3-2：本文件计数口径已注明（172=顶层测试数；适配回归 33 为其子测试数）。
- P3-3（登记 T4.1）：metric→unit 1:1 关系目前仅由商业分类间接表达；第二供应商引入时在 provider schema 中明示。
- 修复后终验：make check=0（含 guard:gen）、Go 172/0（-race -count=1）、web 98、manifest 148 文件零漂移。

## 验证性质标注

- 纯规则/契约：`make test-contract`（-race -count=1）＋ `make test-web`。
- 真库：`make test-integration`（modernc 文件库，-race）。
- 受控运行：server 冒烟（本地 SQLite，无外呼）。
- 真实供应商：无（与首批相同，未获资料与测试范围）。
- UI 旅程/性能：未验（T2/T5/T6）。
