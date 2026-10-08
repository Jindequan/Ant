# ANT 重新实现 · 完整需求书（交给 Claude）

整理日期：2026-10-07 · 来源：本仓库 `docs/PRD.md`（V0.12，需求唯一来源 R01–R25/N01–N07/A01–A30）、`docs/DESIGN.md` + `docs/design/01–10`（V0.9 技术设计基线，语义参考）、`docs/plans/STATUS.md`（进度与裁决唯一记录）、`docs/CORE.md`、`docs/implementation/*`、`docs/README.md`（裁决汇总）。

## 0. 这份文档怎么用

- 目的：让 Claude **重新设计并重新实现** ANT。本文档是自足的需求书：不依赖原仓库也能开工；若能访问原仓库（`/Users/devin/code/Ant`），其中的 `docs/` 是更细的权威参考，现有 Go/React 代码是**参考实现**（其中资源配置流程已被用户否决，见 §5）。
- 约束力分级：
  - **硬约束**：§5 用户裁决与红线、§7 功能需求、§8 计费规则、§9 非功能需求、§10 语义底线、§14 验收标准、§6 技术基线。重新设计可以改变模块划分与实现方式，但不得削弱这些语义。
  - **参考**：§11–§13 的对象模型/API/页面契约来自现有设计与实现，是"已验证可行的形状"，可以重新设计得更好，但不得丢掉其中的语义承诺。
- 防自欺条款（原项目既定纪律，继续有效）：静态评审通过 ≠ 运行时验收；测试通过 ≠ 产品验收；文档宣称 ≠ 实测证据；候选性能数字不是承诺。

---

## 1. 产品定位与目标

**ANT 是什么**：一个自 hosted 的 AI（及类似）资源池调度与记账网关。它把用户在多个 provider（如 DeepSeek 等标准 API）处的**有限配额资源**（token 包、余额、并发槽、请求速率、免费额度、订阅套餐）统一登记、组池、排序，为业务调用方做：准入判定 → 原子预留 → 单次受控出网 → 精确计量结算 → 可追溯查询与失败/未知处置。

一句话：**业务方提交一个逻辑操作，ANT 依据已验证的能力/配额/消费策略选一个可用账号通道执行，并在阻塞或结果不明时告诉调用方发生了什么、下一步怎么办**。

- 核心价值：接入简单、调用契约稳定、错误与结果清晰；跨账号派发可验证；严格策略下 ANT **绝不主动选择已知会产生费用的容量**（allowMetered=false）。
- 成功标准：第一个真实业务上，onboarding → 调用 → 记账 → 查询 → 失败处理形成完整闭环；两个账号间派发可验证。
- 首个业务域已由用户裁定为 **AI 调用**；部署形态为**单机**。

## 2. 用户与场景

| 用户 | 任务 | 要求的结果 |
|---|---|---|
| 应用开发者（集成方） | 接入多个服务账号，减少手工故障切换 | 接入简单、调用契约稳定、错误与结果清晰 |
| 宿主应用的业务调用方 | 发起 AI 请求（或事务性邮件） | 拿到可用结果；被阻塞时知道该等还是改天再来 |
| 开发/运维维护者 | 查配额、定位失败、核实"未知"结果 | 记录可追溯，有不破坏历史的处置入口 |

候选场景：桌面 AI 调用、SaaS 服务端事务性邮件。第一版先完成**单账号单域真实闭环**，再做**双独立账号派发**。

## 3. 业务对象（领域词汇表）

| 对象 | 含义 |
|---|---|
| Provider（供应商） | 外部服务身份（如 DeepSeek）；协议、目录、预设的归属 |
| Provider 账户（Account） | 真实配额与计费归属主体；**账户 ≠ 订阅**（见 §5 红线）；多个调用通道可共享一个账户 |
| 套餐/资源包（Plan/Package） | 账户名下真实的商业事实：订阅、购买的积分包、免费额度、按量计费；带价格、续期/到期、重置窗口 |
| 调用绑定（Binding） | 凭据 + 端点 + 适配器组成的调用路径，可引用共享或独享资源 |
| 配额限额（Quota limit） | 是否允许继续执行：周期用量、速率、并发等 |
| 可消耗权益（Entitlement） | 送的额度、会过期的额度、购买的容量；扣减顺序必须遵循 provider 已验证事实 |
| 消费策略（Spending policy） | 是否允许使用已知会产生增量的容量的开关（allowMetered） |
| 逻辑操作（Operation） | 一次业务动作，由稳定 ID 标识（如"某订单的邮件发送"）；同 ID 同内容幂等 |
| 尝试（Attempt） | 为该逻辑操作发出的一次 provider 请求 |
| 预留与结算（Reservation/Settlement） | 调用前原子预留容量；调用后按真实用量（或保守上限）确认 |
| 外部执行占用（Occupancy/Encumbrance） | 尚未证明结束的请求持有的并发资源，与操作最终状态分开管理 |
| 阻塞（Block/Hold） | 暂时或永久不可分配，带原因与解除条件 |
| 核验与调整（Reconciliation/Adjustment） | 对不确定结果的补充确认与账目更正；只追加证据，不改写历史 |

普通集成方不需要手工设计这些对象间的关系；受支持 provider 的预设应吸收复杂性。**纠正后的产品主流程必须是：注册 provider → 录入账户与其真实套餐/资源包 → 由包导出可读的命名资源与限额 → 组池并手动排序 → 之后才谈消费执行**（见 §5.2）。

## 4. 主闭环与逆向处理

| 阶段 | 输入 | 输出与下一步 | 职责 |
|---|---|---|---|
| Onboarding | 凭据引用、provider、套餐 | 已验证配置；提交最小请求 | 配置/适配器 |
| 提交 | 逻辑 ID、业务请求、deadline | 接受（202）/冲突/既有结果重放 | 路由 |
| 分配 | 可用账号与资源状态 | 选择理由、预留、出网意图 | 调度/账本 |
| 执行 | 已授权的 attempt | 输出、用量、执行报告 | 适配器 |
| 结算 | 报告或失联证据 | 操作结果、余额变动、查询记录 | 账本 |
| 处置与复用 | 错误、阻塞、未知 | 等待、取消、核验、修配置，或定义新动作 | 业务/运维 |

**逆向边界**：未开始的出网可取消并释放；出网之后取消不能撤回业务副作用、不能退款。已提交的邮件收不回，已发生的 AI 消费不能无依据删除。核验可以追加证据、更正错误记账，但**事实不能靠抹历史重置**。

## 5. 产品方向的关键裁决（必须遵守）

### 5.1 裁决时间线（含用户原话）

| 日期 | 裁决 |
|---|---|
| 2026-10-02 | 交付裁定（译自中文原话）：**后端用 Go**，用好其特性提升性能；**前端自行选择**，初版可以简陋，但**必须支持在 UI 里配置各类 provider 的各类资源、把不同类型资源组成池、池内按优先级消费、优先级可手动调整、资源配置在用户界面完成；切换调度在服务端**。旧 Node 后端与"无图形控制台"立场作废。 |
| 2026-10-03 | 首域=AI 调用、单机部署；三个 provider 候选：DeepSeek API / 阿里云 Token Plan / 火山方舟 Coding Plan。 |
| 2026-10-04 | ANT 内部 Hooks 机制要求支持灵活生命周期扩展（R25）；禁止动态加载代码；后续校准出 Guard/CandidateOrderer/Transformer/Observer 四类与封存清单/版本。 |
| 2026-10-07 | 用户裁定优先做核心而非前端："前端不是最重要……你必须构建 Ant 最伟大的核心！自由基础设施、强能力、可扩展 provider/资源/用量计算"（CORE-EXTENSIONS 增量授权：不加新基础设施、不加付费依赖、保持 Go + 本地 SQLite）。 |
| 2026-10-07 | "先配置资源，再谈消费"。随后用户**否决**了交付的资源配置编辑器，原话："**what a shit! do you know what should you build??**"——该实现把 provider 账户误当订阅、让用户通过内部记账字段拼配置。修正范围（账户→套餐/包→资源/限额→池→优先级）**在当时未实现**。 |
| 2026-10-07 | 对后续表单式配置的再次否决，原话："**ridiculous form and stupid config progressing**"——不要交出"荒谬表单 + 愚蠢配置流程"。 |

### 5.2 纠正后的业务模型（configure-first，必须作为产品设计主轴）

1. **Provider 身份**：注册供应商（目录、协议、能力档案），与服务端代码中的适配器/策略绑定。
2. **账户**：录入真实账户（名称、凭据引用、端点）；账户是配额与账单的归属主体。
3. **套餐/资源包**：在账户下登记真实的商业事实，类型区分：订阅（subscription）/ 购买积分（credit）/ 免费额度（free）/ 按量计费（payg）；含价格与币种、计费周期、续期日/到期日、时区、重置窗口（日历制或滚动制）、共享的账户级限额、归档/恢复。
4. **资源与限额**：由套餐/包**导出**命名资源声明（token、金额、并发槽、请求数……），带版本化续期/窗口规则与证据；声明数量 ≠ 可用余额 ≠ 已验证证据，三者必须可区分。
5. **池与优先级**：兼容的资源包组成池，池成员手动排序（小者优先）；同一底层资源被多个池引用仍共享真实容量。
6. **执行是下一阶段**：配置闭环先交付且可用，消费执行随后接上。

### 5.3 红线（重新设计时不可再犯）

- **账户不是订阅**：provider、账户、套餐/包、资源/限额、计量规则、池必须是**可区分的概念**，不得合并或让用户混填。
- **不得让普通用户通过内部记账字段构造配置**（计量精度指数、内部 metric ID 裸表单这类东西不能成为主流程）；预设应把套餐/包规则翻译成规范化的资源与计量定义。
- 被否决的旧编辑器（`/resources?view=legacy` 的 ResourceDraftPage：账户名即"订阅账户"、裸 ID/单位/精度字段）只能作历史记录，**不得复活为主流程**。
- 任何 UI 都不得把"未核验/未知"的余额显示为 0；草稿不得显示为已激活。
- 不虚构免费容量：声明一个 provider ≠ 拥有免费容量；协议兼容 ≠ 后端调用许可。

### 5.4 Provider 准入裁定（证据在 `docs/implementation/PROVIDER-EVIDENCE.md`）

- **火山方舟 Coding Plan：不可受理**——订阅条款（§2.8/2.9/2.11/3.1，浏览器渲染双人复核）限认可工具内终端用户交互使用，禁止后端/服务端程序化调用。
- **阿里云 Token Plan：不可受理**——官方 FAQ 明确禁止自动化脚本/后端调用；免费额度每模型"通常" 1M/90 天（北京区）、有激活延迟，不等于零成本硬停。标准实时按量 API 仍是候选。
- **DeepSeek 标准 API：候选且已部分落地**——现有参考实现冻结了模型 `deepseek-flash`、端点 `https://api.deepseek.com/chat/completions`、输出上限 393216 token，以及一个冻结费率快照（cache hit/miss/output 分项计费；费率是快照而非实时账单事实，上线前必须用真实账户核验）。公开余额接口 `GET /user/balance` 已实现为显式观察动作。
- 条件推荐组合：DeepSeek 标准 API + 阿里云标准实时 API。真实账户、套餐、余额、凭据引用、调用/费用授权仍需用户提供（见 §16）。

---

## 6. 技术基线（已冻结 D07 + 工程约束）

- **后端**：Go 常驻进程（参考实现用 Go 1.27、标准库 `net/http`、`database/sql`）。模块化单体：Router / Ledger / Transport / ConfigCompiler / Query-Admin 是职责边界，不是微服务拆分。
- **存储**：本地 SQLite（参考实现用纯 Go 的 modernc.org/sqlite，无 CGO；WAL、FULL 同步、foreign_keys；**单写连接** + 独立只读连接池）。生产如需多主机共享配额，须换共享事务型 Store（如 PostgreSQL 行锁），第一版按单机交付。
- **前端**：独立 React + TypeScript(strict) + Vite 应用；参考实现用 React 19 + React Router + TanStack Query，无 Redux/Zustand；构建产物可由 Go 同源托管；Node 只参与构建，不进后端运行时。包管理用 npm（本仓库没有 bun 脚本；质量门命令见 §17）。
- **通信**：HTTP 命令 + SSE 状态/输出流。不用 WebSocket（持续音视频双工才需要另行设计）。
- **依赖纪律**：第三方依赖刻意极少（参考实现直接依赖仅 3 个：kin-openapi、santhosh-tekuri/jsonschema、modernc.org/sqlite）；有 `check-deps.py` 之类的依赖边界门。**不引入新基础设施服务（Redis/消息队列/托管数据库），不加付费依赖。**
- **密钥**：秘密只存 AES-GCM 密文（每条随机 nonce，ref+revision 作 associated data）；master key 在库外（0600 文件/系统 key 服务，目录 0700）；API 从不回显秘密，只回 ref/revision/掩码。
- **认证**：首版无公开注册。初始管理员由一次性 bootstrap CLI 创建（打印一次性高熵 token）；Web 用服务端不透明会话 cookie（HttpOnly、SameSite=Strict、HTTPS 下 Secure）+ CSRF/Origin 校验；业务程序用高熵 API token（服务端只存 hash），绑定 namespace 与权限。
- **第三依赖准入**：不支持"受控 Transport"约束的库（如带自动重试的 SDK）不得引入。

## 7. 功能需求（R01–R25，全部为"待实现/待验证"口径）

| ID | 需求 | 业务价值与验收焦点 |
|---|---|---|
| R01 | 受支持的 provider 附带已验证预设、完整配置示例与前置条件 | 集成方不自己写记账规则；示例可验证 |
| R02 | 校验凭据引用、账户范围、套餐、计量、能力与配置关系 | 错误在调用前被发现；说明如何修复 |
| R03 | 逻辑 ID 绑定请求语义；同 ID 同内容对应恰好一次逻辑执行 | 并发只产生一个执行权；不同内容即冲突 |
| R04 | 按能力、指定账户/绑定、消费策略、配额与阻塞状态过滤与选择 | 保留当时的选择证据；required affinity 不得被静默绕过 |
| R05 | 原子预留所有适用资源；失败不留部分占用 | 并发不超过配置的准入容量 |
| R06 | 一次出网对应一条持久化 attempt；适配器不得隐藏重试或换账号 | 每个外部副作用可追溯、独立问责 |
| R07 | 成功、失败、部分消费、取消、未知都有明确结算 | 实际用量超预留也如实记录；重复结算不重复扣减 |
| R08 | 已知失败才按业务规则重分配；未知终止自动尝试 | 不确定送达的邮件不得自动重发；attempt 与 deadline 有上限 |
| R09 | 提交应答与业务结果分离；配额、配置变更、消费拒绝等原因可区分 | 默认快速失败不排队；终态由统一优先级裁决，不为 provider 增加状态 |
| R10 | 长任务续租、失联任务收敛、重复/迟到事件受控 | "已知未执行"才释放；未知消费保守，租约到期不等于占用已释放 |
| R11 | AI 支持流式输出与取消；断连、未消费输出、背压有收敛行为 | 部分输出可见后，不自动拼接另一 provider 的结果 |
| R12 | 查询操作、账号与绑定，解释历史候选、选择、尝试、结算与阻塞 | 成败都有历史证据；旧决策不按新配置重算 |
| R13 | 支持核验、追加调整、基于证据解除阻塞 | 历史不改写；调整不自动重发 |
| R14 | 隔离可信业务命名空间与操作权限，保护凭据与内容 | 查询/取消/调整不越权；日志无秘密 |
| R15 | 配置更新先验证、失败保留旧配置；更新不得把配额清零 | 固定计量计划与当前安全授权分离；旧计划不能绕过新阻塞 |
| R16 | 结果附带持久化、交付与重放能力的明确声明 | 服务器执行生命周期与持久查询分离；调用方中断后按原 ID 取回 |
| R17 | 真实存储不可用时不出网；备份恢复后先核验历史缺口 | 不因账本丢失重复业务动作 |
| R18 | provider、业务、资源、排序、部署经显式接口扩展 | 普通扩展不改去重、发送许可、结算；核心不变量不能被插件关闭 |
| R19 | 消费计数与执行占用分离；未知之后未证明释放的资源保留 | 后台仍在执行的槽不被重分配；不确定窗口归属按 scope 隔离 |
| R20 | 准备与发送许可检查当前准入、取消与业务守卫 | 停用、凭据吊销、消费收紧、邮件抑制不能拿旧快照绕过 |
| R21 | Web 配置 provider、凭据引用、账户/绑定、各类资源及其规则 | 草稿/验证/激活闭环；错误字段可修复、旧配置保留、秘密不回显 |
| R22 | 不同类型的 offer 组成逻辑资源池，不复制底层共享资源 | 按能力/计量单位分组；跨池共享余额与未知占用，不造混合假总额 |
| R23 | 用户手动调整池成员优先级，服务端按真实顺序安全故障切换 | 整组原子保存/版本冲突保留草稿；新请求按新序；执行中 attempt 不迁移、未知不重发 |
| R24 | Go 常驻后端 + 独立 Web，经授权的网络契约配置与执行 | HTTP 命令 + SSE 状态/输出；浏览器断连不终止服务端记账；持久结果可取回 |
| R25 | ANT 内部扩展可在核心显式开放的业务生命周期边界声明并注入类型化 Hooks | 组合根可为命名检查点配置带版本的 Guard/CandidateOrderer/Transformer/Observer；封存清单/修订（含 Hook API 版本与生效预算）可审计；变换只在显式授权阶段生效并由核心逐步验证；扩展不得获得或绕过授权、账本、单次发送、终态控制 |

### 7.1 无容量后的重试

- 一旦逻辑操作被接受创建，其终态结果稳定。无容量终态存续期间用同 ID 重提交，仍返回原结果；配额恢复后**不会**悄悄执行。
- 业务可显式创建新逻辑动作。`retrySafety=proved_safe/unsafe/undetermined` 统一三值；仅当上一动作被证明无可重复副作用才可 proved_safe；未知时不得据此自动换 ID。
- 基础校验前就被拒绝的首个请求**不创建**逻辑记录；此边界必须在 API 上可区分。
- 默认 Router 不等待容量、不在 Retry-After 上睡眠；deadline 是本次尝试的总时限，不是排队许可。临时限速也立即形成稳定失败，预估恢复时间仅供宿主调度后续业务动作。第一版没有隐式等待队列。

### 7.2 unknown 的用户行为

返回已知出网、保守消费、核验入口与重复风险。后续确认可追加核验结果、更正记账，但原 unknown 终态与证据保留。是否发起新动作由业务决定，账本调整不隐式触发。

### 7.3 邮件结果边界（若首域选邮件）

"provider 受理提交"与"最终送达"必须区分；第一版 Router 成功默认只代表受理。退信、投诉、抑制名单、最终送达由宿主邮件流程负责；适配器准入验证跨 provider 的抑制/发件人身份守卫。动态收件人抑制由宿主统一提供，切换 provider 不得绕过；所需状态拿不到时 ANT 阻塞。

### 7.4 取消请求与事实结果

取消成功应答只表示停止意图已记录、新尝试被阻止。业务已成功仍返回 succeeded；确认停止且业务未完成才可 cancelled；外部结果无法确认则 unknown。消费与未释放占用分开显示；取消不退款、不改历史。终态不被迟到取消改写，同 ID 重放稳定。取消与无容量/预算耗尽竞争时：确认未执行或已知结束 → cancelled 优先（竞争原因保留可查）；确认成功或外部结果未知 → 各自优先于取消；结果提交后到达的取消不改终态。全系统按 §10.5 的一套规则裁决。

### 7.5 "灵活性"的约束

加 provider、凭据、业务计量或排序必须有显式落点，不能靠在 Router 里加 provider 特例。扩展只提出能力/套餐/排序/资源建议；核心统一把守授权、原子性、去重与恢复。第一版不做动态插件市场；真正超出保障范围的新语义仍需显式设计评审。Hooks 机制（R25）取代旧的"暂不开排序注册表"限制，但约束更严：默认排序仍是纯函数；受信组合根可在启动时为**核心显式开放的命名边界**注册四类 Hook；不提供动态加载或绕过核心代码的任意插桩；Transformer 只在显式启用的点开放，核心验证初始值与每一步输出，失败拒绝整条链不返回部分结果；Observer 只在持久提交后收不可变事件，不改结果、不同步阻塞主路径；任何 Hook 不得获得秘密、Store、Ledger、Transport 权限。

### 7.6 长任务取回与观察刷新

服务端接管后返回 accepted + 查询/事件地址，不传进程内 Promise。浏览器断连服务端存活 → 保存与结算继续；服务端退出 → 按租约恢复，可能以 unknown 结束，不承诺自动二次出网。重连按原 ID 读持久化事件/结果，不重挂 provider 网络流。观察刷新由运维经 Query/Admin **按需**触发；第一版无隐式周期采集、无自动重试。有可信容量 API 时证据收集在事务外；没有 API 就导入有依据的材料；Ledger 只验证与保存。刷新失败展示下一步，不触发业务重发、不增加容量。

### 7.7 资源池与手动优先级

逻辑池可组织多种资源 offer 类型（AI token/金额、邮件封数、存储等）；按请求域与能力筛选兼容候选，各单元独立展示与记账。一次尝试选定**一个完整资源包**，不能用另一类型的单元凑数。多池引用同一资源身份时共享真实容量。手动优先级决定 offer 之间的顺序；provider 账户内部的权益扣减顺序必须遵循已验证事实，不可拖拽改变。优先级数字小者在前，offerId 破并列。缺能力、配额、证据、权限或消费许可的候选被跳过并说明。顺序变更整组原子保存，冲突保留草稿；执行中保持原路径，未发送的 permit 对照最新版本复查，未知不因重排而重发。**UI 是唯一主配置路径，服务端是准入/故障切换的裁决者。**音视频双工会话需另行设计，不得假装第一版文本 API 已支持。

## 8. 配额与计费硬规则

- 限额与权益必须匹配 provider 真实规则；同一商业事实不得被表示两次。
- 多 key 共享同一真实配额时必须共享配额身份，否则配置被显式拒绝；不得伪装成多个独立余额。加 key 不加容量。
- 单凭据认证失败默认不耗尽/禁用同账户其他合法通道；账户级计费问题不能靠换 key 绕过。阻塞粒度按信号能证明的范围：凭据级错误作用于 binding/credentialRef+revision；计费失败作用于账户；资源耗尽作用于具体资源；范围未知才保守扩大。
- `allowMetered=false` 拒绝已知需计费容量的候选。有依据的"零消费承诺"只有在：可靠计费上限 + 完整消费记录 + 可信初始余额 + provider 硬停全部成立时才可做。
- 上限必须有可执行的来源与逐计费维度的验证证据；未强制执行的请求参数或经验值不算可靠上限。
- 第一版直接对照已证明的上限预留，不隐式缩放；外部调用或未知计费项破坏保证时，严格模式下阻塞并说明。
- 超限结果要说明是哪条规则阻止、预计何时恢复或需要什么人工处理；解除阻塞不增加余额。
- 第一版不向最终用户卖配额，不做收费、退款、撤销 provider 账单；已发生的消费不因取消而删除。
- 租约与本地超时不是外部执行已结束的证据。unknown 之后并发占用或跨周期隔离仍可持有；展示原因与取证路径；不能靠跨月、重启或无依据清理回收容量。

## 9. 非功能需求（N01–N07）

| ID | 要求与验收方法 |
|---|---|
| N01 正确性 | 并发、乱序事件、重复、过期、崩溃下保持 R03–R10、R19/R20；记录迁移并解释外部无法栅栏的边界 |
| N02 接入 | 前置凭据与外部设置就绪时，未参与开发的开发者按指引 60 秒内完成最小动作，或看到真实可用性与下一步；记录实际耗时 |
| N03 可用性 | 错误说明发生了什么、如何处理、结果/消费是否保留；阻塞有解除方法 |
| N04 性能 | 冻结负载后实测 ANT 附加 p95/p99、吞吐、锁等待、拒绝率；与 provider 延迟分开 |
| N05 可观测 | 按 operationId/attemptId/accountId 可追溯决策与状态变更；敏感信息脱敏 |
| N06 持久与恢复 | 预留、意图、终态在真实存储下持久；失联与备份恢复有可验证处置 |
| N07 可维护 | provider 预设带验证时间、来源、适用套餐与维护责任；变更可追溯到需求与验收 |

候选性能目标（未确认、未实测，冻结前不得当作承诺，也不得事后下调掩盖失败）：10 账户、每账户至多 8 资源、100 万历史用量记录；20 ops/秒持续 30 分钟、50 并发长任务；ANT 附加时延（提交→发送许可）p95≤50ms、p99≤100ms；正常负载无未处理锁错误；2 小时代表性运行后无持续预留泄漏。

## 10. 语义底线（无论怎么重新设计都必须成立的性质）

以下 I01–I10 与机制语义来自现有设计（`docs/DESIGN.md` §2/§4/§5/§7、`docs/design/03/04/05/10`）。重新设计可以改变模块与实现，但下面的性质必须被达成并有测试证明。

### 10.1 核心不变量 I01–I10

- **I01 身份授权**：namespace 与 operation 身份正确；资源只能来自编译引用与当前授权；不得捏造新窗口/资源重铸配额。
- **I02 单位与数值**：单位、乘数、窗口身份与计划一致；预留非负有限；正负调整带显式语义与证据；不接受 NaN/Infinity。
- **I03 状态版本化**：更新符合 expectedState/owner/generation/资源版本；终态绝不复活；过期持有者拿不到新 permit。
- **I04 准入原子性**：prepare/permit 检查所有适用资源、取消、gates、预算与证据；任何失败不留部分变更或 permit。
- **I05 预留转换**：每份预留只能被消费、释放或转为未确认占用恰好一次；拆分持有不得凭空增减；真实超额记为 overage 事实而非截断。
- **I06 占用保守性**：active/unconfirmed/released 互斥；租约到期/重置不删除外部 unknown 占用；释放必须携带结束/归属证据。
- **I07 计量幂等**：事件、结算、调整、核验 ID 唯一；重放绝不重复扣减；冲突记录不覆盖既有事实；事件与汇总同事务提交。
- **I08 结果优先**：操作结果由统一裁决（§10.5）唯一决定；取消或准入拒绝不抹除消费/占用；迟到事实走核验。
- **I09 单次出网**：一个 attempt 最多持有一个有效发送 permit；预算按已授予 permit 保守计数；Transport 绝不复用 permit 或隐藏重试。
- **I10 忠实记录**：已发生消费即使超限也要结算并标 overrun；观察绝不制造 unknown 容量；账目更正需要授权、来源与审计。

### 10.2 操作身份与去重

- 归一化语义指纹：`fingerprint = SHA-256('ant:semantic:v2' + canonicalJSON)`；键排序、保数组序、拒绝非法 JSON/非有限数/重复键；显式纳入 capabilityProfile；不同能力档案必然不同指纹。指纹含 domain、poolId、完整归一化业务内容、required affinity、输出约束；**排除** deadline、maxAttempts、preferred、allowMetered、trace——这些是首操作策略，一经存储同 ID 重提交不可修改。
- 提交应答四态：`accepted(202)` / `inProgress` / `replay` / `rejected`（仅用于未建操作的校验失败或身份冲突）。`StoreUnavailable` 表示访问失败且操作可能已持久化，不得假设"数据库错误=未执行"。

### 10.3 原子预留与预留状态机

计数型：`prepare: reserved += upperBound` → 已知结算 `reserved -= upperBound; committed += actual` → 实际未知但窗口归属可信 → 追加 upperBound → 真实超额 → 追加全部 actual、标 overrun、加 hold。条件更新（版本化 CAS + 容量余量谓词），changes≠1 整体回滚。
占用型：`availableSlots = capacity - reserved - active - uncertain`；authorize 时 reserved→active；endProved（仅显式结束证据）释放；unknown 时 active→uncertain 并开 open encumbrance；后续结束证据恰好一次释放。cancel/abort/本地超时/租约过期**不**调用 endProved。
AND 约束：一次请求可同时受账户总额、key 速率、并发槽、免费权益约束——各查同一业务用量，是控制视图，不叠加成多次计费。权益瀑布（entitlement waterfall）只采用已验证的 provider 扣减顺序；metered 池有余额且 allowMetered=false → 拒绝；任一段不足整体拒绝回滚。

### 10.4 发送许可（Dispatch Permit）

每 attempt 至多一个 permit；含 attempt/generation、绑定/凭据 revision、计划与准入版本、requestDigest、有效期（deadline/lease/guard 最小值）、资源准备引用、runtimeId。permit 不可从 API 反序列化构造（服务端私有句柄表）。Transport 先进程内原子标记 used，再向账本请求持久 CAS（granted→consumed 恰好一次，重复调用必须拒绝）；consume 事务核查操作状态/owner/租约/取消/deadline、attempt executing+permit granted+同 runtime+凭据未吊销、池/配置版本仍允许+消费策略/holds、原分配仍合法。authorize 提交后不得插入长操作。**承认边界**：consume 成功后 socket 写前的停顿仍可能一次迟发——无 provider fencing 不能宣称绝对撤回或外部 exactly-once。

### 10.5 终态统一裁决（所有终态路径共用，在结果提交事务内核对）

1. 终态已存在 → 稳定 replay；迟到材料只能追加证据。
2. 持久化事实确认完整业务成功 → succeeded（取消意图保留为诊断）。
3. 任何已获发送 permit 的 action 结局不可确认且需终止 → unknown，不得被 cancelled/配额拒绝掩盖。
4. 取消意图已持久化且所有 action 确认未执行/已知结束且无成功 → cancelled；竞争原因（无容量/预算/deadline）留在 reasons。
5. 无取消意图且需失败 → required affinity 阻塞优先；其后 DeadlineExceeded、AttemptsExhausted、ExecutionInterrupted、AdmissionBlocked、领域失败分类。
6. 均不成立且活跃执行有有效归属 → 保持 running。

终态四值：`succeeded / failed / cancelled / unknown`。`ExecutionInterrupted/owner_lease_expired_before_dispatch` 仅用于持久记录证明从未授予发送 permit 的场景。已授权 permit 但缺"未发送"可靠证据不算"确认未执行"。

### 10.6 租约/心跳/清扫与崩溃收敛

参考参数（可重设计但语义要保持）：lease 30s、heartbeat 10s、sweep 5s、每批 sweep ≤100。operation 与活跃 attempt 共同维持租约；共享部署用数据库时间。崩溃收敛：claim 后无 attempt → ExecutionInterrupted/owner_lease_expired；prepared 未授权 → 释放；executing 出网前/中 → 无法证明未发则 unknown（计上限、必要占用/跨周期隔离）；已得结果未 settle → 账本重试不重发；settle 后返回前丢失 → 同 ID 重放已存结果；终态后迟到报告 → 追加证据走核验；存储不可用/旧备份恢复 → 停新出网、判定缺口、不默认"从未执行"。sweep 条件 UPDATE 竞争、递增 generation、吊销未消费 permit，旧 owner 失去写与发送资格；**租约过期不是外部结束证据**。

### 10.7 AbortBeforeDispatch 事务矩阵

读 (attempt.phase, permit.status, generation) 与 consume CAS/sweep 竞争同一状态守卫：
- prepared/未授权 → attempt 终态 not_executed、释放全部预留、按取消意图定 cancelled 或允许重规划；
- executing/granted → permit→revoked、attempt not_executed、释放预留（未消费则不可能已发）、permitsIssued 不回退；
- executing/consumed + 可靠未发证据（本进程句柄证明 socket 写未开始）→ 按 not_executed 结算，不轻信调用参数；
- executing/consumed + 无证据 → attempt 终态 unknown、计数按上限保守入账、占用 active→uncertain 开 encumbrance、跨窗口归属隔离、禁止自动切换/重发。

补充：prepared 取消也必须经此事务关闭 attempt；abort 各路径均不退还 permitsIssued；幂等键 `abort/<attemptId>`+输入 hash。

### 10.8 计量与计费

- 统一 int64 最小单位；中间计算用 math/big 或 checked arithmetic；SQLite 数值绝不溢出为 REAL。外部报告超 int64 时：把规范十进制原值存报告 JSON 的可选字段（`actualOutOfRangeDecimal`，严格大于 2^63-1），原子释放预留、写 `actual_out_of_range` hold、返回 unknown/UnknownOutcome、阻断后续自动继续——**不伪造 committed_units**。JSON 数量一律十进制字符串，严格编解码拒绝浮点/溢出，浏览器不转 Number。
- 计量计算器：代码注册的公式（不是用户输入表达式）；输出维度 = 固定整数 + Σ(输入维度 × 有理数比率)；每输出一次显式舍入（Exact/Ceiling）；单位与币种显式，无隐式换算；缺样本、重复样本、单位不兼容、负数量、不支持输出 → 失败无部分结果；零价格项也需要显式样本。计算器不可变、并发读安全、有确定性 SHA-256 Digest（覆盖身份/费率/单位/舍入/出处）。计量规则或费率变更必须版本化；旧估算与新余额绝不拼接，重编译/重预留或稳定返回 config_changed。
- 结算幂等键如 `settle/<attemptId>`+reportHash；实际用量标注 `provider_reported`，保守上限标注 `conservative_bound`，占用资源可标 `not_applicable`。

### 10.9 资源窗口与身份

- 单位只做显式精确转换；不同单位/货币绝不相加。固定窗口 `[startMs,endMs)`；`windowKey = ruleRevision:startMs:endMs`；月不当 30 天；DST 按该时区日历；规则修订变更需显式迁移，不靠换 resourceId 重置余额。
- 窗口规则身份规范化：lifetime / rolling(正时长) / fixed-minute/hour/day/week/month（时区+毫秒锚点；month 保留原 dayOfMonth、执行时月末 clamp；UTC 别名仅限空串/UTC/Etc/UTC/Etc/GMT/GMT；其余 IANA 名须可加载且精确匹配；禁止 Local）。
- 新窗口容量只能由编译验证的 reset/refill 规则创建；跨窗口归属未知 → 资源级隔离，reset/月初不自动清除。
- 观察公式：`available = max(0, min(capacity - committed - adjustment - reserved, observedRemaining - max(0, committedNow - committedAtObs) - reserved))`；观察只收紧；收集在事务外，摄入时核对窗口未切换/证据未过期/无不相容调整，期间新增消费按水位差保守扣减；失败可见、不重发业务、不增容量。

### 10.10 受控传输与凭据

- ProviderAdapter 只做 Plan→请求编码、响应→领域报告；无 http.Client/SQL/秘密解析；不自动重试。provider 业务需要多请求时拆为显式计量的多个 action，不伪装单次出网。
- Transport 独占出网：固定 HTTP/1.1、禁连接复用、禁重定向（3xx 按响应处理）、不配代理、每个 permit 恰好一次 Do 且在全新连接上；响应体有限额、header 等待与整体超时。requestDigest 覆盖 method/URL/非秘密 header/body 与凭据注入规则；秘密值不入 digest；只有 Transport 解析凭据并在发送前一刻注入。
- 端点是 allow-list 地址（管理员配置）；阻断内网/metadata/重定向 SSRF；DNS 解析与连接目标复验。
- 诊断收集也走核心发送入口，不自动豁免配额或认证阻塞。

### 10.11 存储与事务纪律

- 参考实现 36 张 STRICT 表（含 operations/attempts/reservations/dispatch_permits/command_receipts/resource_events/encumbrances/holds/observations/decision_records/output_chunks/operation_events/reconciliations/audit_log 等）；operation_events/resource_events 追加只写触发器；operations 终态不可变触发器。关键索引：单一活跃 attempt 的 partial unique index、池成员排序、pending 预留、open encumbrance/hold 等。
- 事务模板：写队列（control/settlement 与普通写两通道、有界、防饿死）→ BEGIN IMMEDIATE → 读运行时控制与激活版本 → 幂等回执（同 ID 同 hash 返回旧结果，异 hash 拒绝）→ 按稳定顺序加锁 → 校验状态/版本/租约/取消 → 纯规则计算变更计划并逐项验不变量 → 事件+条件更新+回执+审计同事务 COMMIT。全局统一锁序；有界重试只在数据库事务内，绝不在网络上重发。
- 命令幂等：claim=namespace/operationId+指纹；prepare=attemptId；authorize=每 attempt 一个 permit；consume-permit 不可重放；settle=attemptId+reportHash；appendOutput=attemptId/sequence+chunkHash；调整/核验/清 hold 用调用方稳定 ID。丢失 consume 回复不得重新消费再发。
- 迁移：每版本号单事务、失败不开新出网、绝不 drop 重建旧 schema。恢复：先置 restore_guard=1 封锁发送 → 核验缺口与未解除占用 → 审计移除后才允许新发送。

### 10.12 Hooks（R25 的落地契约）

- 定位：内部 Go 接口，组合根显式注册；**不动态加载未知代码**。注册后 Seal 冻结。
- 四类：Guard（只允许 allow 或带稳定分类的拒绝；失败/panic/超时/未知分类一律=拒绝，fail-closed）；CandidateOrderer（只可重排核心硬过滤后的候选，必须返回完全相同的 ID 集合各一次；错误/非法排列 → 该轮规划被拒，不静默回退默认排序；同输入同序可复现）；Transformer（只在显式启用的点；接收快照+数据副本返回下一版本；核心先验初始值再逐步验每个输出；失败/panic/超时/验证拒绝 → 整链拒绝无部分结果；默认不启用）；Observer（只在事务提交后收不可变事件；禁止修改结果或同步阻塞主路径；进程内固定 worker/有界队列、非阻塞提交、满则立即报错必须接告警——队列仅进程内存，可靠投递须由持久 outbox 驱动）。
- 检查点：pre-commit——`request.validated`、`candidates.ready`、`candidate.before_prepare`、`attempt.before_authorize`、`dispatch.before_send`、`report.before_settle`；post-commit——`operation.claimed`、`decision.committed`、`attempt.prepared.committed`、`attempt.settled.committed`、`operation.committed`、`config.committed`。禁止跨阶段混用（Observer 只能定义在 post-commit 点）。
- 封存与预算：注册键全局唯一；每个注册有稳定 Name+Version；同点按 priority 升序再按 name 字典序调用；Seal 产出确定性 manifest（Hook API 版本、生效预算、检查点、阶段、能力、注册名/版本/类型、优先级）+ SHA-256 Revision；事件携带 Registry 自动注入、不可伪造的 APIVersion 与 RegistryRevision；预算（自定义点数/每点 Hook 数/Transformer 与事件 payload 字节上限）计入 manifest/revision。超预算 fail-closed。启动时拒绝重复键/空名/缺版本/未声明点/未授权类型。Hook 不得在事务内、持锁时、秘密解析期间或发送入口内执行；不得获得 SQL/Store/Ledger/Transport/SecretResolver/可变领域对象引用。
- 决策记录：候选优先级/理由、池与配置版本、Hook Registry revision/manifest、排序前后候选与选中项，与 prepare 预留在同一 SQLite 事务持久化；查询按 namespace 隔离、读历史 trace 而非按当前配置重算。

## 11. 资源池与配置模型（参考形状）

- 对象模型：ProviderDescriptor（类型 ID/适配器 revision/支持域/配置与资源 schema/已验证权益桶）· AccountingAccount/ExecutionBinding · Resource（一个资源身份一本账）· ResourceOffer（一个可执行资源组合，引用调用须检查的全部约束与权益路径；漏一个必需约束即不合法）· ResourcePool（poolId/namespace/enabled/version）· PoolMember（offerId/enabled/priority/version）· ProviderEntitlementGroup（provider 实际扣减顺序，不被拖拽改写）。成员引用 offer 而非复制余额；同一 offer 可进多个池。
- **两种排序不得混淆**：用户派发顺序（池成员优先级）vs provider 权益扣减顺序。字段、接口、UI 控件分离。
- 商业事实身份（resolution-based）：物理商业事实是服务器验证实例（VerifiedScope + VerifiedResourceFact，physicalKey 服务端派生全局唯一）；配置中每个资源必须解析到恰好一个实例（0 匹配=未解析草稿问题；多匹配=歧义，必须显式声明）；重复 physicalKey=克隆，拒绝。证据是类型化声明（metricId/bound 必填且匹配实例；configuredCapacity ≤ 已验证上限；声明不得加大数额）。受信语义种类经注册表管理（fixed-window-counter / entitlement-pool / occupancy-slot ↔ counter-limit / counter-entitlement / occupancy-limit 精确映射），非配置可编辑、不从 profile 反推。
- 派发算法：claim（指纹含 poolId）→ 读授权池 → 过滤 enabled/域/能力档案/required affinity → 稳定排序 (priority ASC, offerId ASC) → 逐成员编译/查证据/quote → prepare 原子校验池版本/成员/绑定+全部资源 → 版本竞变回滚、有界重规划（上限如 planningMaxRounds=3）→ authorize→consume→一次发送→settle → 仅已知安全才尝试下一 offer → 全阻塞持久化稳定 AdmissionBlocked。cost 仅诊断；allowMetered 是硬 gate；preferred affinity 第一版只记偏好；maxAttempts 只数发送 permit；unknown/已见输出/取消/deadline 禁止"下一个资源"。
- 优先级修改：UI 上下移/拖拽/数字编辑；服务器一次收整组顺序（If-Match 池版本 + expectedConfigRevision + commandId + 完整成员列表，每个 offerId 恰好一次）；一次事务原样保存用户显式数字（**不做** 归一化），同事务更新池版本/配置快照/激活指针/审计。409 返回当前版本与原因，UI 保留本地草稿；同 commandId 同 body 返回原结果。执行中已消费 attempt 不迁移；新 permit 检查池版本不符不得出网。
- 配置闭环：草稿（SaveDraft）→ 验证（ValidateDraft：引用、能力、单位、共享身份、窗口、证据、计费、完整资源映射）→ 激活（ActivateDraft(expectedRevision)：事务内复核证据与当前 revision，全部成功才切换；无效新配置保留旧激活版本）。错误草稿可保存待修，但绝不成为可执行配置。网络校验在事务外。停用/移除成员影响新准入，不删除既有引用/事件/unknown 预留；用过的资源只能归档。回滚配置=以新激活版本通过当前校验；余额/审计不回滚。
- 空库首次使用 expectedConfigRevision 用空串且仅当前无配置时激活；已有激活版本必须给出精确 revision。

## 12. API 与事件契约（参考形状）

- 统一错误信封 `{error:{code,messageKey,params,fieldIssues,operationId?,retryable,action},requestId}`；400 字段错 / 401 会话失效 / 403 权限 / 404 不可见 / 409 身份或版本冲突 / 422 配置不可执行 / 429 Busy / 503 存储或主机不可用；业务 failed/unknown 是正常 Operation 投影，不包成 500。请求体限 1MiB；schema 解析拒绝未知业务字段与非法枚举。
- 数量一律十进制字符串；时间 epoch-ms；未知值省略或带显式 availability；数字 0 不得替代 unknown。
- 主要端点（/v1）：会话（POST /sessions、GET/DELETE /session）；目录（GET /catalog/providers）；配置（GET /config、GET /config/revisions、POST /config/drafts、GET/PATCH /config/drafts/{id}（If-Match）、POST …/validate、POST …/activate）；秘密（POST /secrets、POST /secrets/{id}/rotate、GET /secrets/{id} 只回 ref/revision/mask）；实体查询（GET /providers、/resources、/offers）；池（GET /pools、GET /pools/{id} 按能力/单位分组无混合总额、PUT /pools/{id}/order 整组原子）；执行（POST /pools/{id}/operations → 202 accepted/200 既有投影/409 冲突；GET /operations、/operations/{id}；POST /operations/{id}/cancel（200 只是停止意图 ACK）；GET /operations/{id}/events（SSE，afterSequence/Last-Event-ID）；GET /operations/{id}/output（分页、base64、只回已提交字节）；GET /operations/{id}/decisions）；管理（POST /resources/{id}/observations、POST /resources/{id}/adjustments、POST /attempts/{id}/reconciliations、POST /holds/{id}/clear）；provider 专属（如 DeepSeek 余额观察：POST /providers/deepseek/balance-observations + GET 按 commandId 回读，全局并发上限、同命令合并）；健康（GET /healthz、/readyz）。
- 事件协议：operation_events 按 namespace/operation/sequence 持久有序；信封 `{operationId,sequence,type,createdAt,payload}`；类型：accepted、attempt_prepared、dispatch_authorized、output_stored、cancel_requested、attempt_settled、operation_terminal、reconciled。状态写与事件写同事务；EventHub 只提供唤醒不保证投递——订阅者先查库、注册、再查库，周期补查兜底。SSE：id=sequence、15s 心跳、每订阅/每用户有界缓冲、慢消费者断开可按序追赶、终态事件后关流、前端按 sequence 去重。输出 body 先持久后可见：首 chunk 与 visible_output_at 同事务；每 operation 持久 body 上限（参考 16MiB）；chunk 从 1 递增、SHA-256、精确重放幂等、冲突与跳号拒绝；流关闭不等于业务结束；首段可见后禁止自动换 provider 拼输出。

## 13. Web 控制台（参考形状）

- 信息架构：仅四项导航——providers / resources / pools / operations。首用空态主 CTA "Add provider"。主路径：添加 provider → 录入账户与套餐/资源包 → 配置资源与限额 → 入池排序 → validate+activate → 池详情提交最小请求 → 操作详情。首批 7 类页面（Login、Provider 列表+配置、Resource 列表+配置、Pool 列表+创建、Pool 详情、Operation 列表、Operation 详情），每页一个主任务/主 CTA。
- 每页四态（loading/empty/error/success）+ 适用反向态（无权限、缺凭据、证据过期、无容量、未保存变更、版本冲突、unknown）。unknown 余额显示"待核验"绝不显示 0；草稿不显示为已激活。取消 ACK 显示"requested"而非"cancelled"。对账（reconciliation）表单仅在能无歧义映射到一个 unknown attempt 时出现；负调整必须绑定所选原始事件且不超可冲销余额；禁止一键重发 unknown。
- 前端边界：React Router + 单一查询层（TanStack Query）；表单按 catalog schema 生成受控控件，绝不执行任意 catalog JavaScript；api/client 统一 cookie/CSRF/错误/429 退避；业务提交不自动换 ID/自动重发；事件按 sequence 去重；token 响应丢失后重试**复用原 operationId 与请求体**。客户端校验仅辅助，服务器同一 schema 裁决；优先级不做乐观保存；成功显示前必须 GET 回读真实投影。
- 视觉方向（可沿用可重做，但信息架构与状态纪律不变）：AI 资源路由控制台——冷雾底色（#edf2f1）、深海军导航（#17383e）、teal 主操作（#08766d）、lime 路由节点（#c8e47b）、amber 待核验（#8a5a10）；Avenir Next + PingFang SC 回退；桌面 1280/1440 表格、390px 竖排信息行（字段名+值，无横向滚动）；主按钮 44px、颜色不是唯一状态信号、语义化 alert/dialog 与键盘焦点；zh/en 文案同步。
- **注意**：现存的"资源草稿编辑器"（/resources?view=legacy）与"高级 ConfigDocument 文档编辑器"是被否决方向的遗留，重设计时以 §5.2 的纠正模型（账户→套餐/包→资源→池）为主流程。

## 14. 验收标准（A01–A30，全部待验证）

| ID/需求 | Given | When | Then/Evidence |
|---|---|---|---|
| A01/R03 | 同 ID 同内容 | 并发提交 | 恰一个逻辑执行权；并发不重复已批准出网 |
| A02/R03,R16 | 不同内容或已终态同 ID | 重提交 | 冲突或稳定重放；无新出网 |
| A03/R05 | 容量 50、每次预留 1 且持有不放 | 100 并发请求 | 恰 50 个成功；失败无残留 |
| A04/R10 | claim 后无 attempt；或 prepared 后崩溃（有无取消意图两种） | 租约到期清扫与旧 owner 恢复 | 按统一优先级收敛；无取消且无更高优先级事实 → ExecutionInterrupted/owner_lease_expired_before_dispatch；可释放预留被释放；旧 owner 不能再启动 |
| A05/R08,R10 | executing 失联 | 清扫与迟到报告 | unknown 保守结算、无自动重发；证据可核 |
| A06/R07,R10 | 租约续期/清扫、重复/冲突报告 | 竞争更新 | 恰一个合法结果、不重复扣减 |
| A07/R05,R15 | 窗口边界或配置切换 | 准备后启动、跨边界完成 | 归属正确；配额不能靠切换清零 |
| A08/R07,R13 | 旧观察、负调整、实际用量超预留 | 重算/结算 | 不错误放松；超额如实记录 |
| A09/R04,R08,R09 | 两账号持有/亲和 | 执行或无容量 | 决策符合策略；已知失败与未知后续不同 |
| A10/R01,R02,N02 | 前置就绪、集成方未参与开发 | 按指引 onboarding | 无需设计记账模型；记录耗时与卡点 |
| A11/R11,R16 | 长流、断连、取消、无消费者（调用方中断或执行宿主退出） | 查原 ID 或恢复清扫 | 宿主存活结算继续；宿主退出按事实恢复；持久结果可取回；不重建 provider 网络流 |
| A12/R12,R13 | 认证/配额阻塞或 unknown | 修复/核验 | 处置有据、历史保留、后续查询准确 |
| A13/R14 | 未授权 namespace 或操作身份 | 查询/取消/调整 | 拒绝；审计记录；无秘密泄漏 |
| A14/R17 | 存储故障或旧备份恢复 | 新提交 | 出网被阻；恢复前核验缺口 |
| A15/R07,R09,R11 | 取消与成功/未知/无容量/预算耗尽竞争，取消在终态提交前或后 | 统一裁决与查询 | 按优先级排序；已提交终态不改；消费保留 |
| A16/R09,R15,R20 | 已 prepared，账号停用或消费收紧 | 取发送许可/重提交 | 未开始的出网被拒并保存具体原因；旧计划不能放松授权 |
| A17/R02,R05 | 两绑定共享总额、其一凭据失效 | 并发预留与认证失败 | 容量不加倍；认证只隔离对应绑定，账户计费冻结共享 |
| A18/R02,R07 | 上限证据缺维度/过期/参数未强制 | 严格候选准入 | 拒绝；有足够强制上限的来源通过，不限于特定参数名 |
| A19/R04,R12 | 选择成功后配置/余额变更 | 查询旧操作 | 返回当时的选择、版本与准备竞争；不按新配置重算 |
| A20/R06,R10,R20 | 最终发送前校验之前/之后跨租约暂停 | 恢复/清扫 | 前者零出网；后者若有迟发则保守记录、无自动重发，不宣称绝对栅栏 |
| A21/R02,R20 | 邮件目标被宿主抑制或发件人身份无效 | provider 切换/permit 前取证 | 所有不合规路径被阻；守卫 fail-closed；仅邮件域适用 |
| A22/R05,R19 | 并发上限 1，A 已 unknown 但 provider 仍在执行 | B 的请求与后续完成取证 | B 不复用槽；证据确认完成后恰好释放一次；窗口重置不清除 unknown 隔离 |
| A23/R18 | 内置/新资源计划故意违反 DESIGN I01–I10 适用项；或上报真实超预留 | 验证与提交 | 非法身份/单位/版本/预留/释放/许可/终态被拒；合法超额事实被记录并阻塞 |
| A24/R09 | 候选仅限速 2 秒、操作 deadline 60 秒 | 提交 | 稳定阻塞不等 2 秒，返回预估时间；同 ID 重提交仍重放且无出网 |
| A25/R12,R13 | 调整使观察失效、来源采集失败、采集期间窗口/调整版本变化 | 按需刷新/查询 | Ledger 不做网络调用；失败可见；过期证据被拒；不增容量、不重发；可信新观察正确入库 |
| A26/R21 | 真实类型描述、错误与正确资源草稿 | 从 Web 配置/验证/激活 | 正确配置 GET 回读一致且可执行；错误指出字段且保留旧激活版本；秘密不回显 |
| A27/R22 | 一池异构 offer；同一物理资源跨池 | AI 筛选/并发调用/unknown 后再调 | 只选兼容路径；按单位展示；共享引用不加配额、不清 unknown 占用 |
| A28/R23 | 两个可用 offer、两个同池版本标签页、有 prepared/executing attempt | 手动保存顺序/并发修改/新请求 | 原子顺序决定新选择；409 冲突保留草稿；prepared 复查、executing 不迁移、旧历史不重算 |
| A29/R24 | Go 常驻服务、Web 订阅、两种权限身份 | 断连/重连/取消/未授权调用 | 同 ID 读有序持久事件/结果且无重复出网；取消 ACK 不冒充结果；未授权被拒 |
| A30/R25 | 内置与自定义生命周期点、按阶段声明带版本的 Guard/CandidateOrderer/Transformer/Observer | 注册/封存/真实调用点与清单修订（含 Hook API 版本与生效预算）持久化；deadline、panic、验证器失败、Observer 故障注入 | 允许的 Hook 在真实调用点稳定顺序生效；决策记录含封存修订与阶段结果，可从 manifest 重建 Hook ID/版本；排序只返回原候选集；Transformer 只在显式开启点生效、初始值与每步输出经核心验证、失败无部分结果；超预算/未声明类型/deadline/panic 全部按契约 fail-closed；无 Hook 绕过身份、许可、账本、单次发送、终态；Observer 只见提交后脱敏快照、其失败不改已提交结果不阻塞主路径 |

证据必须包含构建与配置版本、部署形态、输入、期望与实际；模拟故障与真实 provider 记录分开标注。

## 15. 范围与非目标

第一版必须交付：Go 服务器 + 独立 Web；provider/资源配置；异构逻辑资源池；手动优先级；服务端故障切换派发；先完成单账号单域真实闭环，再双独立账号派发。幂等、预留、结算、恢复、授权查询、运维处置**同时**交付。首批 provider 真正需要的计量/限速/并发/周期/过期/超额/共享配额规则必须可表达；无法表达时该账号不得被准入。

暂不做：任意协议自动适配、跨币种换算、累计预算系统、AI 任意协议翻译、自适应排序、插件市场、公开多租户商业平台、第二域未验证真实出网。图形配置控制台属于第一版：前端可以朴素，但配置/结果闭环与错误恢复必须可用。新域执行适配在第一闭环后按真实需要启用；多资源类型的配置与池组织**不是**第二域扩展，第一版必须实现。未实现的能力必须明确标注。

## 16. 未决事项与需要用户提供的输入

以下Decision（D01–D07）中，D07 已冻结（技术栈），其余 open；G0（真实准入门）未退出，第一阻断门是 IN-01：

| 项 | 内容 | 需要用户提供的私有事实 |
|---|---|---|
| D01 | 首个真实出网切片的域、真实调用方、成功定义、两个 provider、测试范围 | 真实 AI 应用/调用方、脱敏真实请求样例、测试次数与成本授权（是否允许付费用量） |
| D02 | 单机/跨机、执行宿主存活方式、事务型 Store 选择 | 目标主机 OS/常驻服务/权限（单机已选定，验证待做） |
| D03 | 共享关系、逐维上限、初始状态、计费硬停、外部终止、观察来源 | 两个真实账户的套餐/ID/凭据引用/初始余额证据 |
| D04 | 真实负载与 p95/p99/耐久指标、流背压/缓冲预算 | 真实负载数据或确认采用候选目标 |
| D05 | 凭据/运行时权限、输出 body 保留/清理、去重与 unknown 证据保留策略 | 保留期限等运营决定 |
| D06 | 时钟、租约/续租/清扫、取消/guard 期限、unknown 占用与恢复、按需采集频率 | 确认候选参数或给出调整 |
| IN-01 | 首域=AI 已定；**缺：真实调用方、脱敏请求、测试授权/预算** | 以上三项 |

约束：私有事实不得编造；未获授权不得发真实出网调用；密钥只进获准的凭据来源，不入文档与代码。

## 17. 工程流程与验收纪律

- 质量门（参考实现现值，重实现应保持同级强度）：`make check`（runbook 渲染检查、gofmt、go vet、go build、依赖边界门 check-deps.py、web typecheck + 生成物 drift 门禁）；`make test-contract`（internal 全包 `-race -count=1`）；`make test-integration`（真实 SQLite 集成 `-race`）；`make test-fault`（故障注入：恢复守卫审计回滚、响应丢失幂等重放、配置安全 fail-closed 等）；`make test-web`（Vitest）；`make build`；`make verify-manifest`（源码清单签名 diff，防静默漂移）。参考实现规模：Go internal ~428 测试 + 集成 ~86 + 故障若干 + Web 254 项。
- 依赖边界由脚本强制（如 domain 不 import 数据库驱动/api/store/transport；hooks 零内部依赖；adapters 不碰 HTTP/SQL/秘密；Web 不引用 server 代码或秘密）。
- 文档纪律：需求约束设计、设计约束实现；冲突时记录触发条件并修订受影响条款；计划可细化但不得降低正确性/花费/权限/恢复的要求。进度与裁决只记录在单一 STATUS 文件；**每批开工必须重读 STATUS**。
- 证据纪律：逐项交付并保留证据；外部事实缺失时先完成不依赖它的工作，并显式登记缺失输入及影响；"无证据不得标完成"。
- 单文件不超过 500 行（接近上限优先重构模块边界）；不确定的抽象等到第二个真实用例出现再引入。

## 18. 参考实现地图（现有仓库，仅供参照）

- `server/cmd/ant-server/`：组合根（main.go 装配、providers.go 内置 provider 注册：localtest fixture + DeepSeek、双启动模式 `-enable-resource-config` / `-enable-localtest-config`、bootstrap CLI、备份/恢复 CLI、web 静态托管）。
- `server/internal/`：`domain`（类型/精确数量/指纹/排序/结果解析）、`domain/metering`（精确计算器）、`domain/pools`、`domain/resources`（窗口/计数器/占用/瀑布/变更校验）、`config`（ConfigDocument schema/编译/图校验）、`ledger`（17 个端口方法 + permit/abort）、`ledger/commands`（claim/prepare/authorize/renew/settle/abort/cancel/output/sweep/admin…）、`adapters`、`providers/chatcompletions`（OpenAI 兼容非流式共享编解码）、`providers/deepseek`、`providers/localtest`、`transport`（受控 HTTP）、`runtime`（ExecutionHost/Supervisor/Clock）、`hooks`（Registry + ObserverDispatcher）、`store/sqlite`（单写队列+读池、迁移、备份）、`application`（Router/Planner/Executor/ConfigService/…）、`api`（handler/DTO/SSE）、`security`（认证/AES-GCM）、`planning`（中立 ProviderPolicy 端口）、`jsonx`（严格 JSON）、`contracttest`。
- `contracts/`：openapi.yaml（v1.1.21，数量十进制字符串、未知字段拒绝、统一错误信封）、config-document.schema.json、provider 契约 JSON、正负 fixture。
- `web/`：React 19 + Vite；四导航页面 + 双模型遗留视图（inventory 供应清单视图是 2026-10-07 否决后的最新重做方向；legacy 草稿编辑器是被否决实现）。
- `docs/`：PRD（需求唯一来源）、DESIGN + design/01–10（设计）、plans/STATUS（进度与裁决唯一记录）、implementation/（BASELINE/CORE-EXTENSIONS/PROVIDER-EVIDENCE/RESOURCE-CONFIGURATION/RUNTIME-PROPOSAL/SUPPLY-INVENTORY/LOAD-ACCEPTANCE-PROPOSAL）、operations/RECOVERY（恢复手册）、LOCAL-DEVELOPMENT（本地启动：`make build` → bootstrap token → `-enable-localtest-config` 起服 → Web 配置 localtest 账户/池 → Operations 提交）。

---

**最后强调**：重新设计的自由度在架构与实现；R01–R25、§5 裁决与红线、§8 计费规则、§10 语义底线、A01–A30 是不可让渡的验收口径。配置体验必须按 §5.2 的"账户→套餐/包→资源→池→优先级"业务模型设计——这是前一轮实现被用户否决的直接原因，也是本次重实现的第一优先级。
