# CFG-05：D1 规则身份接线

日期：2026-10-03。CFG-01 的原设计和 31 小例经集成者接受后实施。新增 `server/internal/domain/resources/window_identity.go` 的可比较 `WindowRuleIdentity` 和 `NormalizeWindowRuleIdentity`；fixed 相位直接调用既有 parseAnchor。`server/internal/config/compile.go` 仅替换 windowsExecutableEqual 的说明／函数体，比较成功规范化的身份。WindowAt/window.go/window_fold.go 未改；不再用日期抽样判等。

## 证据

命令（server 工作目录）：

```sh
go test -race -count=1 -run 'TestWindowRuleIdentity|TestCompiledWindowIdentity|TestRework3WindowIdentityVariants' -v ./internal/domain/resources ./internal/config
```

exit 0；原始输出见 `targeted.log`。生产 helper 的 31 例覆盖 accepted 设计规则；新增 Compile 30 子例分别走显式 factId 与隐式维度解析，覆盖 rolling 自反／duration 差异、Casablanca/Lagos、hour 无关日期／毫秒差异、五 UTC 别名和五周期自然相位。反例断言 Compiled=nil、issue 存在且定位 `/resources/0/window` 或 `/resources/0/providerFactId`；正例断言产物 Window 为可信快照规则、PhysicalKey 不变、Capacity/InitialRemaining 均为 1000000。原有 5 窗口变体保持通过。

首轮新增 month 维度控制使用 SGT，触发现有 shared monthly fact 的真实维度歧义；其余 29 新子例通过。修正控制快照为 UTC 月规则以避免这份真实共享事实碰撞后，30 子例全通过。未修改独立原始 corpus 或放宽歧义规则。

## 边界与下一步

§1.1：设计接受后实现；§1.3/§1.4：以上声明绑定实际 targeted.log；§4.8：无页面契约/IA 变化，影响既有身份校验，按合法／非法输入与可信产物实测。§4.6：D1 helper 和完整入口接线已验证，待 CFG-07 合流及独立原审查探针；本包未验证 UI、持久激活、Ledger、真实外呼或上线。

实际运行模型 ID 未在工具/环境暴露，故不推断。
