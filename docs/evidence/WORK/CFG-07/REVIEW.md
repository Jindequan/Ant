# CFG-07 独立终验 · D1–D4

2026-10-03；审查者 `独立复核者`（未实施生产改动；工具未公开实际运行模型 ID，不作推断）。

**接受当前 D1–D4 有限技术修复。** 该结论不构成 T1/G1 出站；T0、T1.4、IN-05 及原 G1 前置仍需满足，不进入 T2。未验真实供应商外呼、UI 旅程、持久激活、Ledger 运行、性能或上线。

## 不可变输入与实测证据

审查绑定 `docs/evidence/T1-CODEX/final-source-manifest.json`，SHA256 `72bf5aea2953fb51eb10c8709f9b31a087c20bca99cdfecf80eb7471d6abefe1`，184 个生产、测试、工具、消费输入。独立生成 `review-input-before.json`、`review-input-after.json`；三个清单的 files 数组逐项相同，manifest-diff 报 clean。设计 04 §1.1、07 §1.1、T1 卡、两份原审查说明、宪法加载器及全文和上述清单共 8 项权威输入另绑定 `contract-input-before.json`／`contract-input-after.json`，前后摘要一致。本报告不修改共享源清单。

独立实跑（全部 exit 0）：

- `go test -race -count=1 -v ./internal/config ./internal/domain/resources ./internal/providers/localtest` → `go-final-independent-race.log`。
- `npm test -- --run tests/t1-independent-review.test.ts tests/compiled-config-final.test.ts tests/codex-fourth-regression.test.ts` → `web-final-independent.log`，79/79。
- `npm run typecheck` → `web-final-independent-typecheck.log`。

另核对 root 的 `T1-CODEX/gate-results.json` 与对应日志：make check、test-contract、test-integration、test-web（235/235）、build 均 exit 0；不能把根日志核对写成审查者亲自重跑。最终清单一致性由本审查者独立复现。已有目录中无测试包的标记不代表空验收：配置、资源算法、目录、协议和真库集成均有实际运行用例；未把尚未实现的 fault/e2e/bench 声称通过。

## 为什么四项可以接受

- **D1**：规范身份按种类及相位比较，不再采样日期；rolling 正持续时长自反，Casablanca/Lagos 分开，有限 UTC 别名归一，周日／月日／毫秒相位保留，非法规则拒绝。独立完整入口另验证时区瞬时点转换、不同日期同 hour 相位、毫秒尾随零；只改 weekday、month day 或 sub-ms 的反例拒绝且定位 window。正例检查实际 Compiled 的可信原窗口、scope、capacity 与 remaining。原完整入口及窗口回归一起实跑。
- **D2**：注册语义提供执行 kind/commercialRole，所有资源都核验，包括无 offer 引用资源，矛盾不得生成 Compiled。profile 对 role 分别检查两个维度；独立正例把并发同 metric 的 limit 需求加入原 occupancy 需求，仍输出 occupancy/limit、真实账户 scope、physicalKey、容量 1／余额 1。只改新增角色为 entitlement 或 unknown，均拒绝且定位 `/offers/0/resourceRefs`。注册缺失、非法、重复与原类型反例一起实跑。
- **D3**：必填规范非负声明及固定快照相等成立。原缺值、负值、冲突、有效零快照、保守容量控制实跑；独立追加 whitespace、plus、exponent、decimal、number/null 声明，字符串格式错误定位证据引用，非字符串由 schema 拒绝。实际 Compiled 保留可信初始余额，不用保守 configuredCapacity 覆盖快照。
- **D4**：加载时继承真实父 scope/provider，显式匹配合法；矛盾父子 scope/provider 拒绝。账户、第二账户、共享直访及账户／binding alias 解析实跑；完整 Compile 的共享正例输出 billing-alpha-shared 及对应 physicalKey，账户输出 acct-demo-1。没有用访问 owner 替代共享 ScopeID。

## 审查中发现并关闭的边界

1. Go 原覆盖实现让 occupancy 覆盖时遮蔽 CommercialRole=limit，与 07 §1.1 相冲突。独立提出后 root 同步修正覆盖，Go/Web 正反入口通过；审查者未编辑生产文件。
2. Web 以 Number.isSafeInteger 限制 rolling，错误拒绝规范允许且 JS 精确可表示的正 int64 `9007199254740992`；Go 完整入口接受并精确保留。失败原日志保留 `web-independent-initial-int64.log`（22 pass／1 fail）。生产 owner 修正为 integer、正数及 int64 上界检查后，最终同一入口断言通过，未扩大 schema／历史观测范围。

## 签署与下一步

宪法 §1.3／§1.4：完成声明有真实日志与不可变输入，无伪造实际模型或业务验证；§3.8：Go/Web 完整配置编译契约的输入、issue 定位与实际产物均验证；§4.1：攻击视角拒绝可信类型伪装与无效证据，使用者视角保留零值、保守容量及合法归一，开发者视角验证相位边界、元数据冲突并运行 race；§4.6／§4.7：上述为本次有限修复、证据、未验范围及下一步。

签署：`独立复核者`，上述确切 184 输入树的 D1–D4 技术接受。下一步由 root 合流门禁与交接；T0／IN-05／G1 等既有前置独立完成后才可判阶段出站。
