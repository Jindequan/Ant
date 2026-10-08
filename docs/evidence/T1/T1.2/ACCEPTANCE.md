# T1.2 验收记录（模型、端口、网络契约与纯算法；K1/K2/K3/K4/K6）

状态：verifying（独立审查通过前不置 done；K3 事务实现与故障验证按裁决属 T2.2，本批只冻结协议与判定表）。

## 出口逐项（任务卡 T1.2 ＋ 各 K 项）

| 出口 | 结果 | 证据 |
|---|---|---|
| 从 contracts.go 拆分领域/API DTO/内部端口；permit 与 secret 不出现在 OpenAPI | 通过 | internal/domain（types/planning）、internal/api（dto/validate）、internal/ledger（port+permit+abort）、internal/transport、internal/adapters、internal/security；openapi.yaml 无 permit/handle/secret 明文（只有 SecretMetadata.mask）；依赖边界脚本通过 |
| quantity 十进制 string 严格 codec | 通过 | internal/domain/quantity.go（ParseUnits 拒绝浮点/指数/前导零/-0/溢出）+ internal/jsonx（未知字段/重复键/尾随数据拒绝）；dto_test.go |
| 摘要包含 poolId；Operation 策略首次固定 | 通过 | internal/domain/fingerprint.go（SemanticFingerprint 输入含 poolId；排除 deadline/maxAttempts/preferred/allowMetered/trace）12 测 |
| 六类结果竞争统一解析 | 通过 | internal/domain/result_resolver.go 按 DESIGN §7.1 严格六级；9 测（含取消×成功/unknown/无容量、终态重放、ExecutionInterrupted 限定未授许可） |
| counter/window/occupancy/waterfall 纯规则 | 通过 | internal/domain/resources/* 38 测（含 DST/月界/锚点、超额如实入账标 overrun、占用 unknown 不释放、waterfall 硬门与顺序不可变） |
| 池筛选 priority ASC/offerId ASC；费用/preferred 不覆盖人工顺序 | 通过 | internal/domain/pools/* 15 测（K4 同值 7/7 原样、offerId 破同值、required/metered 硬门、稳定排序） |
| K1：版本化 ConfigDocument＋create/update/get DTO＋无密钥 fixture＋同一 fixture 过 schema/codec/Web/保存回读 | 通过 | internal/config/document.go（结构+引用完整性+商业身份不复制+秘密禁入+未支持供应商不可执行）；contracts/schema/config-document.schema.json；contracts/fixtures/config-document-valid.json + 8 个非法 fixture；Go：internal/config/document_test.go（schema+结构+codec 三层）；Web：web/tests/contract.test.ts（ajv+类型+8 非法全拒）；真库保存回读：tests/integration/config_roundtrip_test.go |
| K2：句柄归属明确 server internal 包；受控接口；非零句柄/同 attempt/digest/伪造/跨 runtime/复用拒绝；无 unsafe/reflection | 通过 | internal/ledger/permit.go（PermitRegistry，I09 单许可）+ internal/transport/transport.go（HandleTable 一次性标记、DigestRequest 覆盖 method/endpoint/headers/body）；三包组装测试 internal/ledger/permit_test.go；grep 无 unsafe/reflection |
| K3：完整事务矩阵冻结＋可判定状态用例；实现与故障验证留 T2.2 | 通过（协议级） | docs/design/03-EXECUTION.md §9（权威矩阵）＋docs/design/contracts.go 附件同步＋internal/ledger/abort.go（DecideAbort 判定表）＋abort_test.go（矩阵每行+非法组合+幂等+unknown 不降级）；**未做**事务实现（T2.2），状态不标通过 |
| K4：保留用户明确数字 priority；整组写入；所有成员恰好一次；范围与乐观版本校验；providerOrder 不改写 | 通过 | ReorderPoolInput.Entries（offerId+priority）；pools/priority.go 校验恰好一次/范围/原样同值；integration reorder_test.go（整组原子+过期版本全回滚+并发恰一生效）；权益组 providerOrder 由 entitlementGroups 承载，reorder 只动 pool_members（K1 fixture+校验器） |
| K6：版本化 OpenAPI/wire DTO/请求响应 fixture；accepted.state、error/requestId、创建更新分页、空数组/缺失语义、非法联合状态、数量溢出、重复 path/body 身份、header/body 版本冲突明确拒绝 | 通过（列表端点响应 schema 留 T2.1，已登记） | contracts/openapi.yaml v1.0.0-frozen-t1（3.0.3，kin-openapi 校验通过）；contracts/fixtures/api/*（11 happy-path + 6 rejection）；internal/contracttest/contract_test.go：fixture↔组件 schema 值校验、ValidateSubmitReply 联合规则、ValidateIfMatch 冲突拒绝、溢出/重复键拒绝、同 commandId 异 body 哈希冲突；Web 侧同 fixture 类型+守卫校验 |
| 未知/缺字段和协议非法态均有真实单测 | 通过 | jsonx 重复键/未知字段/尾随数据；contracttest 6 个 rejection fixture 全部断言拒绝 |

## 验证命令（实际执行）

- `make test-contract`（= go test ./internal/...）→ exit 0；PASS 103、FAIL/SKIP 0（test-counts.log）
- `make test-web` → exit 0；2 文件 11 用例（verify-web.log）

## 未决

- 独立审查未完成前不置 done。
- 4 个列表端点（/v1/providers、/v1/resources、/v1/offers、/v1/operations 列表）响应投影 schema 未冻结——设计未定义其 DTO，留 T2.1 与 ConfigService 一起冻结（已登记 STATUS）。
- SubmitReply.rejected 必带 operationId 的语义（回显客户端提交值）已确认为冻结行为，备注于 K6。
