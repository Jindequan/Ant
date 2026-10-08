# T1 第一批独立审查报告与闭合记录

审查者：独立审查独立审查者（未参与本批实现；全程只读，审查中创建的两个临时复现测试已删除并核实无残留）。两轮：首轮全面审查（2026-10-02）、修复后复核（2026-10-02）。集成者未参与审查判断，仅执行修复与重跑验证。

## 一、首轮审查实证抽查（审查者本人重跑）

| 声明 | 复核结果 |
|---|---|
| make check exit 0 | 通过 |
| Go 内部 103 PASS / 0 FAIL / 0 SKIP | 实测一致（127 RUN 含 24 子测试） |
| 集成 22 PASS（-race） | 实测一致，无 race 告警 |
| Web 11 PASS | 实测一致 |
| test-fault/test-e2e/bench 非零退出 | 实测一致（无空测试充数） |
| 迁移 SQL 规范文件与嵌入副本 | 字节一致；与设计 SQL 差异仅为文档化适配 |
| 源码快照 SHA256 抽查 | 一致 |
| 无 unsafe/reflect、无 TODO/FIXME、日志无敏感值 | 证实 |
| main.go 无业务外呼、healthz/readyz 不泄露配置 | 证实 |
| **K3 红线（协议冻结≠运行验证）** | **未违反**：全部材料标 verifying，实现与故障验证如实留 T2.2 |
| 测试断言质量抽查（result_resolver/reorder/receipt/writer_queue） | 货真价实，非空测试 |

## 二、问题清单与闭合状态

### P1（必须修，已闭合）

| ID | 问题 | 位置 | 修复 | 复核 |
|---|---|---|---|---|
| P1-1 | transport.HandleTable 无并发保护，-race 实证 DATA RACE；违反 03-EXECUTION §4"以 mutex/atomic 先标记已使用" | internal/transport/transport.go | 加 sync.Mutex（Prepare/Discard/MarkUsed 全临界区，MarkUsed 检查+翻转同段）；新增 transport_concurrency_test.go（8×50 并发 + 16 路同句柄恰一胜） | **关闭**（复核者重跑 -race 零告警） |

### P2（涉及出口，已全部闭合）

| ID | 问题 | 修复 | 复核 |
|---|---|---|---|
| P2-1 | domain.SubUnits 对 b==MinInt64 静默绕回（实证 SubUnits(0,Min) 无错返回），违反 checked-arithmetic 保证/I02 | quantity.go 显式 ErrQuantityRange + quantity_test.go 边界用例 | 关闭 |
| P2-2 | Go 与 Web 的 SubmitReply 守卫规则不同源（replay/inProgress/rejected 各有分歧） | 以 openapi description 为唯一规则源重写两侧：Go validate.go 补 inProgress/replay 检查；Web 改以 result.state 为准并补禁携带项；各加对齐用例 | 关闭（残余不对称降级 N-1） |
| P2-3 | Web 商业事实去重键缺窗口身份→对"日限+月限"合法配置误拒 | validate.ts 键加入 window 身份（与 Go windowIdentity 语义一致） | 关闭 |
| P2-4 | 服务端引用完整性缺口（binding.resourceRefs 不查存在性、entitlementGroupId 无反查） | document.go 二遍校验 + 反查；新增 3 个非法 fixture（dangling-binding-ref / unknown-entitlement-group / secret-in-evidence） | 关闭 |
| P2-5 | Evidence.Payload 可走私秘密键（schema 与 Go 校验均放行） | Go checkEvidence 对 payload 键应用 secretKeyNames；Web 侧同规则同键集 | 关闭 |
| P2-6 | 07-POOLS §4"server 生成 priority=10/20/30"与 K4 冻结（原样保存）矛盾 | §4 改为"明确数字 priority 恰好一次、原样保存、不归一化"；grep 确认无残留 | 关闭 |
| P2-7 | go.mod 将直接依赖 modernc.org/sqlite 误标 // indirect | go mod tidy（tidy -diff 为空） | 关闭 |

### P3（登记后续，不阻塞本批出口）

| ID | 内容 | 归属 |
|---|---|---|
| P3-1 | PermitRegistry byID/byAttempt 无生命周期清理（T1 无调用方） | T2.2（STATUS 已登记） |
| P3-2 | jsonx 对 `,string` 数量字段接受 "-0"/"010"；规则边界必须显式 ParseUnits | T2.2（已登记） |
| P3-3 | DecideAbort 的 CancelRequested 未被判定表消费（取消语义在结果解析器） | T2.2（已登记） |
| P3-4 | DigestRequest 覆盖全部 headers（含潜在秘密 header）；需在 T2.3 保证秘密不进 EncodedRequest.Headers | T2.3（已登记） |
| P3-5 | check-deps.sh 未覆盖 config 的 modernc 禁令及 security/runtime/jsonx 包 | 随时（低风险） |
| P3-6 | web ajv/ajv-formats 应为 dependencies（被应用代码 import） | T2.1 前（已登记） |
| P3-7 | PoolView 等直接内嵌带 JSON tag 的 domain 值类型作 wire 输出——设计附件本身如此定义，备案说明 | 备案 |
| P3-8 | Clock 回拨保护留 T2.3；writer budget 错误归类可能掩盖同刻业务错误（诊断性失真） | 备案/T2.3 |
| N-1（复核新发现） | SubmitReply 守卫三处轻微不对称，方向均为 Web 更宽松（无误拒风险）：inProgress/replay 携带 result 或 error 时 Go 拒 Web 不查；rejected 的 operationId 非空仅 Web 强制 | T2.1 接线 API client 前镜像（登记 STATUS） |
| N-2（复核新发现） | binding.resourceRefs 非法 ID 会被两遍各报一条 issue（行为正确，仅诊断冗余） | 备案 |

## 三、七方面结论（首轮，复核后维持）

1. 需求：通过（范围守住了 T1.1–T1.3 不依赖真实输入的部分；K3 未提前标运行通过）。
2. 契约：修复后有保留问题全部闭合（P2-2..5）。
3. 状态/事务：通过（DecideAbort 与 §9 矩阵逐行一致；writer 并发/K4 原子性/迁移并发正确且有真断言）。
4. 权限：通过（P2-5 闭合后秘密防护完整；本批无 API handler，DomainPolicy 校验留 T2 属如实登记）。
5. 失败/恢复：通过（队列/预算/回滚/迁移语义正确；P3-1 登记生命周期）。
6. UI 闭环：通过（空态诚实、四态齐备、契约类型与 fixture 一致）。
7. 依赖：通过（go.mod 最小且锁定；边界门禁有效）。

## 四、总体结论

**复核确认：8 项修复全部关闭；无修复引入的新 P1/P2；受影响验证全部由审查者本人重跑通过（make check=0、internal -race 108/0、integration -race 22、web 11、web build=0、tidy clean）。本批范围内 P0/P1/P2 全部闭合，P3/N 项已登记归属。从独立审查角度，本批可交 Codex 核查。**

批次状态：T1.1/T1.2/T1.3 维持 **verifying**——按门槛 6，"done"还需 Codex 核查反馈后按阶段出口整体判定；本批出口的独立审查门槛已满足。
