# T1 返工批次 · W4 独立对抗审查结论与闭合记录

审查者：独立审查独立审查者（未参与返工实现）。方法按本轮独立审查约定：**先依据 R01–R17 文字自提反例探针，再对照修复实现运行**；临时探针运行后删除（审计痕迹经确认）。全文见审查者报告（会话存档）；本文件记录逐项裁定与闭合。

## 一、逐项裁定（审查者自提反例 + 实测/读码）

| 项 | 裁定 | 审查者自提验证（摘要） |
|---|---|---|
| R01 算术 | **关闭** | 31 组手工边界（Min/Max/±1/0 全组合、和恰为 0、SubUnits(Min,-1)、SubUnits(0,Min)、SubUnits(Min,Max) 等）全过；oracle 覆盖 25 边界对×2 op＋500 随机＋200 边界偏置（约 1450 断言），big.Int 精确二值断言 |
| R02 引用图 | **关闭（按 W1 冻结范围）** | 7 条新边负例（offer.domain≠binding、bundle⊄binding、组内私带 providerOrder、缺失组、capacity 证据 kind 错、bound 证据 subject 指资源、selector 证据 kind 错）全部以预期 messageKey 拒绝；Web 同变体同判 |
| R03 桶身份 | **关闭（按 W1 冻结）** | 合法：双 providerOrder 桶、同 owner 异 metric 双孤桶；克隆 6 变体（三孤桶、组+孤桶、同 factId 跨两组、组内重 order、成员 owner≠scope、同 factId 双孤桶）Go/Web 同键拒绝 |
| R04 数量 | **关闭** | "010"/"-0"/裸数字/null/浮点/指数/20 位溢出/空串全层拒绝；±int64 边界精确接受；marshal 往返规范；`,string` 已从生产类型清除（grep 证实） |
| R05 wire union | **关闭**（附 P2-1，已修） | kin VisitJSON：4 变体正例过；5 个跨变体污染全拒；guard 专属规则 Go/Web 同拒；manifest 分层两侧消费；pending 端点如实标注 |
| R06 窗口 | **关闭** | 6 个未见组合：Chatham(+12:45)、UTC 闰日 2028、NYC 春跳锚点（23.5h 日窗）、NYC 秋回 25h、Kolkata 周锚点——start<=now<end＋确定性全过；配置↔实现支持集闭合 |
| R07 句柄 | **关闭** | digest 差一字符拒；跨 runtime/错凭证/空期望各归其错；Prepare 后改调用方字节不影响 digest 与存储字节（探针）；MarkUsed 无共享 slice 逃逸（读码） |
| R08 参数校验 | **关闭** | 10 组非法配置全部带字段名 error 无 panic；校验先于 sql.Open（store.go:166）；失败路径关池 |
| R09 预算 | **关闭（边界诚实）** | 读码证实单一绝对 deadline（writer.go:207）共享重试与 busy（min 封顶）、逾期提交被回滚；协作/非协作/真锁三路径实测与文档一致 |
| R10 摘要 | **关闭** | 异 profile 异摘要、同输入确定、数字拼写敏感、required 入/preferred 出 |
| R11 规则事实 | **关闭** | 版本 2+旧观察 1→获准；版本 1→观察封顶（双向探针）；未归属强制 IsolationRequests 且过 ValidateChangePlan；零值 End/Attribution 报错 |
| R12 目录 | **关闭（local/test 范围）** | descriptor 驱动双侧；nil catalog fail-closed；嵌套/大小写秘密双层拒；嵌入副本字节一致 |
| R13 迁移 | **关闭** | 版本 2 库拒绝且不写数据（表数/版本行/业务数据原样）；PRAGMA defer 恢复 |
| R14 证据/门禁 | **关闭**（P2-1 修复后） | verify-manifest 注入实测非零；check-deps 注入两类坏边实测非零、go list 失败硬错；Makefile 强制 -race -count=1；guard:gen 已接入 make check（P2-1 修复） |
| R15 SSE | **关闭（本批范围）** | URL/abort/close/游标/malformed 全部锁进永久 sse.test.ts |
| R16 UI | **关闭（本批范围）** | 用户语言、可见禁用原因+aria；报告措辞=壳与骨架已验证、业务可用性未验收 |
| R17 前置 | **关闭** | STATUS/任务卡/交接一致；未发现前置改写；T2 未进入 |

## 二、新发现问题与闭合

| 级别 | 问题 | 处置 |
|---|---|---|
| P2-1 | 返工中 openapi Quantity description 修改后未重跑 generate:types → schema.d.ts 漂移一行注释；guard:gen 对最终树失败且不在标准入口 | **已修复并复审**：重新生成 schema.d.ts；make check 增加 guard:gen；manifest 重签（148 文件零漂移）；终验 make check=0、Go 172/0、web 98 |
| P3-1 | 两个 quantity 拒绝 fixture 无 Go 按名消费者；W0 表"逐 fixture typed decode"对 happy-path 表述略夸大 | **已修**：contracttest 补双层按名消费；表述以本 INDEX 与 verify-full.log 实测为准 |
| P3-2 | INDEX 161 vs log 172 计数口径未注明 | **已修**：INDEX 注明 172=顶层测试数（含 11 个回归测试），33 为其子测试数 |
| P3-3 | metric→unit 1:1 关系仅由商业分类间接表达 | **登记 T4.1**（STATUS 原有登记项体系）：第二供应商引入时在 provider schema 明示 metric/unit 关系 |

审查者结论原文（要点）："13 项 P1 全部实证关闭……未发现'以旧 API 编译失败冒充修复'或'借新字段缺失掩盖引用边'的情形……完成 P2-1 修复并重签 manifest 后，本批可交 Codex 复核。"

## 三、层级结论（可验/未验）

| 层 | 可验到 | 未验（归属） |
|---|---|---|
| 工程 | make check=0（含依赖图/生成漂移/manifest 三道门禁，注入实测非零） | CI 集成（无 CI 配置） |
| 纯规则 | 算术 oracle、窗口日历/DST/性质、counter/settle、摘要投影——反例级 | Ledger 侧 I01–I10 事务复验（T2.2） |
| wire | oneOf union 三层、严格数量三层、manifest 分层双侧同源 | receipt 冲突事务/HTTP 行为（T2.2） |
| 真库 | 迁移矩阵（含未来版本拒且不写）、预算/deadline/回滚/零残留/控制推进（modernc -race） | 持久 permit CAS（T2.2）；真实锁竞争覆盖有限（受控回调为主） |
| 受控网络 | 句柄配对/一次标记/防御性拷贝（本地级） | 真实 HTTP 发送、真实 SSE 服务端（T2.3/T2.4） |
| 供应商 | local/test descriptor 双侧驱动 | 真实供应商/凭证/外呼（T0/T1.4） |
| UI | 壳+空态+可见禁用原因（vitest/jsdom 永久断言） | 浏览器实机截图、业务旅程（T2/T5） |
| 性能 | 无测量、无声明 | T6 |

## 四、总体结论

**R01–R17 全部关闭（P2-1 为审查新增、已修复复审）；无未闭合的 P1/P2。修复后终验：make check=0（三道门禁）、Go 172/0（-race -count=1）、web 98、manifest 148 文件零漂移。本批可交 Codex 复核。** T1.1–T1.3 维持 verifying；未进入 T2；未改变阶段前置。
