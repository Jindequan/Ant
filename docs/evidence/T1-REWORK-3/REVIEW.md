# T1 第三轮返工 · Q4 对最终树的独立验收结论与闭合记录

审查者：独立审查独立审查者（未参与本轮实现）。按 Q4 要求对**最终修复后的树**验收：签名清单核对（167→168 文件两次零漂移）、五类标准入口独立重跑、自提反例 21 个新变体经 Go/Web 完整入口运行、冻结倒查。全文见审查者报告（会话存档）。

## 一、C1–C4 对最终树的裁定

| 项 | 裁定 | 独立验证摘要 |
|---|---|---|
| C1 物理身份 | **关闭** | 同 factId 跨独立 scope 合法（Compiled physicalKey 不同）；shared scope 别名两账户同维度=duplicateCommercialFact；锚点换日期/Z 拼写/显式毫秒三种等价组合均同一账；metric 大小写=factUnresolved 草稿；physicalKey 服务端派生，窗口等价与执行同源 |
| C2 证据约束 | **关闭** | bound=1000000：999999 合法且断言 Compiled 容量==999999；1000001 拒绝且 Compiled==nil；initial remaining 超 bound 拒绝；currency→EUR、quantumExp→18、window、revision 各自拒绝（键各归其位） |
| C3 上下文目录+完整束 | **有保留关闭 → 修复后关闭** | 合法完整束+可选资源=合法；删 token 维度=incompleteRequiredBundle；未注册 profile/semantics 拒绝；resource 扩展 account 字段拒绝。保留点 P2-1（profile 域匹配未强制+死代码）→ 已修复 |
| C4 字段定位 | **关闭** | required → /resources/0/metered 精确定位；a/b、x~y → a~1b、x~0y 转义正确，双侧一致 |

两处误导性回归确认已真修：clone 负例引用补齐、只断 identity 键；reorganization 正例真实改名并重写全部引用后断言零 issue。

## 二、Q4 新发现与闭合

| 级别 | 发现 | 闭合 |
|---|---|---|
| **P1** | `document.go` binding 校验先索引 `CapabilityProfiles[0]` 后查长度：空/缺失声明触发 panic（实测复现）——打破"有 issue=可保存草稿"承诺 | **已修复**：改为遍历声明、长度检查先于索引，永不 panic；回归 TestQ4EmptyCapabilityProfilesNoPanic |
| P2-1 | profile 只查存在性不校验域（违反 D-C3-v3"域匹配"）；profileRegistered 死代码 | **已修复**：binding/offer 双侧 profileDomainMismatch 强制；死代码移除；回归 TestQ4ProfileDomainMismatchRejected |
| P2-2 | verify-full.log 宣称 make check 含 manifest 门禁（不实，verify-manifest 为独立 target） | **已更正**措辞；两 target 均实测=0 |
| P3-1 | 死代码 extCtx | 已移除 |
| P3-3 | document.go 注释仍教旧 (owner, factId) 身份模型 | 已更新为 Q1 解析式表述 |
| P3-2/P3-4..P3-10 | 60ms 自限阈值保留；TestRework3ScopeIdentity 注释夸大（由审查者探针补验共享别名路径）；walkSecretKeys 无指针转义；畸形容量双 issue 键名；schema 短路差异；INDEX/STATUS 未登记本轮（本轮补齐）等 | 已登记或随本轮交付补齐（INDEX/STATUS/闭合表/适配说明） |

冻结倒查：默认槽撤销彻底（源码无默认槽路径）；identity 不依赖用户可变字符串（physicalKey 服务端派生）；Q1 冻结与原需求无实质矛盾（残留措辞偏差已随 P3 修正）。

## 三、修复后终验（集成者执行，追加于 verify-full.log）

- make check=0（依赖图+生成漂移两门禁）；make verify-manifest=0（168 文件零漂移）
- Go `-race -count=1`：192 PASS / 0 FAIL / 0 SKIP（含 Q4 两个新回归）
- Web：156 passed；探针 Go 27/27、Web 17/17（逐字复跑，无适配残余）
- make build=0

## 四、总体结论

**C1–C4 全部关闭（含 Q4 审查发现并修复的 P1/P2）；对最终树的独立验收证据链完整（清单零漂移、双侧同源、Compiled 内容断言）。本批可交 Codex 复核。** T1.1–T1.3 维持 verifying；未进入 T2；未改变阶段前置。

层级：工程/门禁/清单=通过；纯规则（编译链 Go+Web）=C1–C4 关闭；wire=沿用既往通过；真库集成=通过；受控网络/真供应商=未验（T0/T1.4）；UI 旅程/性能=未验（T2/T5/T6）。
