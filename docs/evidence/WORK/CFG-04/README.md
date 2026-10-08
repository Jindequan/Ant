# CFG-04 可信资源类型与完整束

2026-10-03。由root集成，输入为已接受CFG-01/02/03及既有契约。

共享RegisteredSemantics新增resourceKind/commercialRole，两份canonical/embedded descriptor同步三条明确注册。每个资源按核验fact的semantics/ref与revision查唯一合法注册，核对配置声明；Compiled类型来自该注册而非用户。缺类型、非法枚举、歧义注册拒绝；事实provider必须与访问账户provider相符。所有资源都验证，不依赖offer恰好检查该维度。

RequiredDimension.Role按两个维度覆盖：limit/entitlement来自商业角色，occupancy来自kind；同metric的occupancy/limit能同时满足这两种需求，不能满足entitlement或未知role。

§1.3/§4.1/§4.8证据：go-types-scope-original-probes.log，Go race fresh exit0，涵盖新可信类型/双角色/共享ScopeID产物、原D1–D4探针及未引用资源角色探针。合法控制先通过，字段冲突精确定位且Compiled=nil；共享别名输出真实billing-alpha-shared。窗口执行未改，不声明UI、激活事务、真实外呼或阶段出站。

历史valid-catalog-two-facts正例在测试内补全预付桶自己的bound/initial=1000及独立证据引用，保留configuredCapacity=500和两个独立物理事实、原expected=true及原语料字节；实际Compiled断言Capacity500/InitialRemaining1000并检查不合并物理键。此适配纠正旧材料与固定快照的矛盾，不放宽D3规则。

待CFG-07最终树独立接受，root自己的定向检查不是独立终验。
