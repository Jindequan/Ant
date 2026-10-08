# T1 当前源码定向复核

日期：2026-10-03。范围：核对 P1.3 安排与最近变动后的 T1 配置编译入口；没有修改业务实现，也未派发新的编码 agent。

**结论：已有有限修复，D1–D4 尚未全部关闭，T1.1–T1.3 继续 verifying。** 不重建工程，不扩大架构设计，不进入 T2。本次为定向检查，未进行新的全量工程验收。

## 当前证据

- `reviewed-source-sha256.json`：本次检查开始时的 168 个既有实现/消费输入摘要；相对上次独立复核有 11 个文件变化。
- `go-probes.log`：原始 Go 探针，以 `go test -race -count=1 -run TestCodexFourth -v ./internal/config` 复跑；10 个叶子中 5 通过、5 失败，exit 1。
- `web-probes.log`：原始 Web 探针，以 `npm test -- --run tests/codex-fourth-audit.test.ts` 复跑；7 例中 3 通过、4 失败，exit 1。
- `probe-results.json`：命令退出码、原始探针的临时文件清除和实现摘要保持不变记录。历史审查日志未覆盖。
- `role-cases.json` / `role-audit_test.go.txt` / `role-probes.log`：新增 Go 独立反例。无 offer/pool 的合法资源配置先通过；仅变更并发资源 kind 或权益资源 commercialRole 后仍零 issue 并生成矛盾 Compiled。3 例中 1 通过、2 失败，exit 1；临时测试已删除。
- `python3 docs/plans/check_tree.py`：exit 0；55 叶子、7 阶段门，依赖无环，可读任务卡与 STATUS 行匹配。
- `make verify-manifest`：exit 0；当前签署清单 168 文件一致。此清单与旧审查快照不同；清单一致不证明变更的业务正确性。

## 剩余问题与已接受的局部进展

| 项目 | 当前观察 | 收敛要求 |
|---|---|---|
| D1 / P1 窗口身份 | rolling 与自身判不等；Casablanca/Lagos 仍被两个样本误判为等价 | CFG-01 补齐规范身份算法与用例，CFG-05 接线；保留 WindowAt |
| D2 / P1 可信类型 | 原两个角色反例现已被 Go/Web 的束覆盖检查拒绝；新 Go 反例证明未被 offer 引用的资源仍能携带矛盾类型进入 Compiled | 接受束覆盖进展；CFG-01/04 仍需从可信语义约束资源 kind/role，并定位资源字段，不依赖某一 profile 恰好拦截 |
| D3 / P1 证据 | Go 已拒绝缺 bound/remaining；Go/Web 仍接受负 remaining 和与快照冲突的 remaining；Web 还接受缺 bound/remaining | CFG-02 完成规范非负数量与固定快照一致性；CFG-06 同步。核验值为零的合法例与保守配置另验 |
| D4 / P2 作用域 | Go 合法控制例三个 CompiledResource.ScopeID 都为空；Web 日志也输出空 scopeId | CFG-03 补父 scope/provider 元数据继承与冲突拒绝；后续镜像检查真实输出值 |

新增 D2 反例只复跑 Go；未声称同一新增反例已在 Web 复现。窗口性质原探针只在 Go 断言。本次未证明实际账本超额、账户合并或真实供应商影响。

## 执行安排与反思

P1.3 的任务树、依赖、文件所有权与每包接受方式已建立，结构检查通过；实际新批次未派发，指定模型的实际运行记录仍待核实。CFG-01 的有限技术设计仍需由 Codex 补齐，不能将修复方向当作已冻结规格交给执行者猜测。

执行者应逐条把契约转成输入、issue 与 Compiled 输出断言；修复底层不变量，不能仅靠特定 profile 拦住原反例；Go 规则接受后及时同步 Web；报告局部接受与未决，不能用测试总数或清单通过代替领域正确性。前期 Codex 的接缝设计不足、批次过大与验证过晚也需承担并修正，当前问题不归因于新增用户需求。

现在可以继续有限设计、证据/目录修复、独立反例与 IN 资料收集。当前技术包通过 CFG-07 后，仍需 G0 与 IN-05，G1/T1 出站才成立；随后按计划开启 T2 最薄真实切片。真实场景、部署边界、两供应商核验材料与运行参数尚未齐全，不能给出有依据的 T2 日历开工日期。

对应宪法 §1.3/§1.4：以上结论绑定真实日志与源码摘要；§4.6：实现未改，剩余问题与下一步如上。未验证 UI 旅程、持久激活、Ledger 运行、真实外呼、性能或上线。
