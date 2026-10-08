# T1 第一批证据索引（批次：2026-10-02）

批次范围：首批——T1.1、T1.2、T1.3 中不依赖真实供应商资料的工作＋K1–K6 对应落实。K3 本批冻结完整事务协议，实际事务与故障验证在 T2.2，未标通过。

## 文件

| 文件 | 内容 |
|---|---|
| [source-snapshot.txt](source-snapshot.txt) | 源码快照 SHA256（无 Git，按执行标准记录） |
| [test-counts.log](test-counts.log) | 全量测试计数（Go 内部 103 / 集成 22 / Web 11；0 FAIL 0 SKIP） |
| [T1.1/ACCEPTANCE.md](T1.1/ACCEPTANCE.md) ＋ [T1.1/verify-check.log](T1.1/verify-check.log) ＋ [T1.1/verify-smoke.log](T1.1/verify-smoke.log) | 工程、依赖边界、Makefile、组合根、真实启动/关闭冒烟、按设计失败的空目标 |
| [T1.2/ACCEPTANCE.md](T1.2/ACCEPTANCE.md) ＋ [T1.2/verify-web.log](T1.2/verify-web.log) | 契约/纯规则/K1/K2/K3/K4/K6 落实与验收 |
| [T1.3/ACCEPTANCE.md](T1.3/ACCEPTANCE.md) ＋ [T1.3/verify-integration.log](T1.3/verify-integration.log) | 34 表迁移、真库 Store、writer 队列、K4 事务、K1 回读 |
| [REVIEW.md](REVIEW.md) | 独立审查结论与问题闭合（审查完成后填写） |

## 验证性质标注

- 纯规则／契约：`make test-contract`（Go 单测，103 用例）＋ `make test-web`（vitest，11 用例）。
- 真库：`make test-integration`（modernc.org/sqlite v1.60.1 文件库，-race，22 用例）。
- 受控运行：server 二进制冒烟（本地 SQLite，无任何外呼）。
- 真实供应商：无——本批无任何真实付费请求或邮件副作用（未获资料与测试范围，按约束禁止）。

## 门槛对照

1. 前置确认：STATUS 前置与本批出口在开工时核对；范围＝T1.1–T1.3 不依赖真实输入部分。
2. 实现：Go server／独立 Web 分层成立；组合根唯一；依赖边界有脚本门禁。
3. 验证：全部为真实命令真实输出；空目标按设计非零；race 已跑（集成）；Go 并发新增路径中 writer 在 -race 下覆盖。
4. 独立审查：见 REVIEW.md（审查完成后补）。
5. 证据：本目录。
6. 交接：T1.1–T1.3 状态 verifying；done 待独立审查＋出口实证通过。

## 已知未决（不阻塞本批出口）

- K3 事务实现＋故障验证 → T2.2（裁决如此，不得提前标通过）。
- 4 个列表端点响应投影 schema → T2.1 冻结。
- D01–D06 真实输入 → STATUS 阻塞记录。
- D07 宿主环境核验关闭 → T0.2（本批已提供 Go 1.27.1＋modernc 兼容证据）。
