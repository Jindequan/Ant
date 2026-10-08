# T1 技术包接受记录

2026-10-03。本文索引已接受的 T1.1–T1.3 技术包与其边界。T0、IN-05/T1.4 和 G1 的真实输入/准入仍未满足；状态以 [STATUS](../../plans/STATUS.md) 为准。

## 接受范围

- CFG-01–07、D1–D4 技术要求已接受；细节见各 [CFG 证据](../WORK/CFG-01/README.md) 至 [CFG-06](../WORK/CFG-06/README.md)，独立接受见 [CFG-07](../WORK/CFG-07/REVIEW.md)。
- 最终源码、测试、工具和消费输入见 `final-source-manifest.json`（184 项，SHA-256 `72bf5aea2953fb51eb10c8709f9b31a087c20bca99cdfecf80eb7471d6abefe1`）。
- 标准检查、Go race、Web 用例、typecheck、manifest 漂移探针及 server 健康/关闭冒烟结果见同目录的 `gate-results.json`、日志和 `server-smoke-results.json`。

## 技术内容

- D1：窗口身份按类型、有效相位及时区别名规范化；rolling 自反，保留毫秒/周/月语义。
- D2：资源注册提供可信 `resourceKind` / `commercialRole`；资源束分别覆盖 metric 与资源/商业角色视图。
- D3：`bound`、`hard_stop`、`initial_state` 等证据字段完整且与固定核验快照一致。
- D4：目录加载继承父 scope/provider，拒绝父子矛盾并保留账户与共享别名的真实作用域。
- 清单消费覆盖既有语料及新增输入；测试和 manifest 保留变更前后的核验记录。

## 未决边界

接受的是工程、协议、纯规则及参考 Store 原语，不代表持久配置激活、完整 Ledger 业务事务、真实供应商外呼、UI 旅程、性能或上线通过。T0/IN-01–04、IN-05/T1.4 的真实场景、供应商授权与额度、部署边界、负载及运行参数仍须满足；后续实现必须遵守原有阶段门。
