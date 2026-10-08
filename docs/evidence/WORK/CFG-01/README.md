# CFG-01：D1/D2 有限设计

日期：2026-10-03。设计权威在 `docs/design/04-RESOURCE-ALGORITHMS.md` §1.1 与 `docs/design/07-POOLS-AND-CONFIG.md` §1.1；本目录提供可运行设计示例和实际输出，不是生产实现或阶段验收。

## 证据

执行 `cd server && go run ../docs/evidence/WORK/CFG-01/rules.go`，exit 0；原始输出 `rules.log`，31 个规则例通过。示例只使用标准库和嵌入 tzdata，未调用供应商或 WindowAt 日期采样。

覆盖 rolling 自反／duration 差异、lifetime、五个 UTC 允许别名、五周期自然相位与显式相位、minute/hour/day 无关日期成分、毫秒差异、转换时区跨日后 Monday、毫秒尾随零、周／月相关日期差异、month 30/31 不合并、Casablanca/Lagos 分开，以及未知种类／周期／时区、宿主 Local、畸形／sub-ms 锚点、零／负 rolling duration 拒绝。

算法依据是现有 `window.go` parseAnchor、slotWindow、civilWindow、monthWindow 与 `window_fold.go` 的冻结执行政策。示例中小范围复制锚点提取以先验证设计；依赖实现必须复用 parseAnchor，不更改已接受的 WindowAt。它不证明完整 Compile 已接入；CFG-04/05/07 仍需真实入口 issue 与 Compiled 断言。

D2 决定：服务端 RegisteredSemantics 提供 ResourceKind/CommercialRole；三个精确映射 fixed-window-counter=counter/limit，entitlement-pool=counter/entitlement，occupancy-slot=occupancy/limit。所有配置资源先核对，即使未被 offer 引用；Compiled 从可信注册输出。profile occupancy 查 Kind，limit/entitlement 查 CommercialRole，均带 metricId；无效注册约束不可执行。

## 范围与交接

§1.1：先补有限设计，再开放生产依赖。§1.3/§1.4：声明绑定本目录真实命令输出；没有声称编译入口、UI、Ledger、真实外呼或上线已验证。§4.8：本包只明确既有资源身份和可信类型，未动页面契约／IA／生产数据；受影响为 §1.1/§1.3/§1.5，规则例是本设计门禁。§4.6：已改两个原设计文件和本目录；待集成者接受并实现、独立完整入口复核。

实际运行模型标识：工具／运行环境未暴露可核验的具体模型 ID，故不推断型号。
