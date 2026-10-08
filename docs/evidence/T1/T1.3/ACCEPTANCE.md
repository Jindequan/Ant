# T1.3 验收记录（34 表迁移、真库 Store、有界写通道、锁序原语）

状态：verifying（独立审查通过前不置 done）。

## 出口逐项（任务卡 T1.3）

| 出口 | 结果 | 证据 |
|---|---|---|
| 按 34 表 SQL／事务设计落实实际 Store 迁移 | 通过 | server/migrations/001_initial.sql（34 表、STRICT、CHECK、索引、终态不可变/append-only 触发器）；internal/store/sqlite/migrate.go（go:embed、单事务、并发安全、嵌入副本与规范文件字节一致由测试强制） |
| 单机 modernc database/sql，一写连接／有界 writer、只读连接池 | 通过 | store.go（DSN: WAL/FULL/foreign_keys/_txlock=immediate/busy_timeout；写池 SetMaxOpenConns(1)；读池 4 连接 query_only）；writer.go（control/normal 双有界队列 128+384、每轮≤8 control 必配 1 normal） |
| prepared statements／StoreTx 接口 | 通过 | statements.go 全部 WriterTx/ReaderTx 类型化 SQL；接口冻结于 internal/store/store.go（实现未改签名） |
| 所有 Ledger 命令按 operation→pool/member→binding/account→resourceId/window 排序读／锁 | 部分（T1 范围） | 本批实现的是 store 原语与 K4 组合事务（GetPool→CAS members→config revision→active→controlVersion→audit→receipt 单命令）；完整命令锁序在 T2.2 按同一 StoreTx 模式实现（接口注释已固定该要求） |
| 配置草稿／激活／整组优先级版本与审计／receipt 同步提交 | 通过 | integration/reorder_test.go：单命令内 6 处变更一致；过期版本→applied=false→全回滚（逐表读回证明无部分写入）；并发两命令恰好一生效（-race，-count=3 无 flake） |
| operation_events 与业务状态同步 | 部分 | 表/触发器/不可变已验证（triggers_test.go）；业务事件写入命令属 T2.2 |
| 验证空库迁移、重复启动、池 namespace/member 外键、单活跃 attempt、permit、事件 immutable、局部失败全回滚 | 通过 | migrate_test（空库 34 表/幂等/并发/外键/STRICT）、triggers_test（终态不可变+事件 append-only）、attempts_test（单活跃部分索引+permit CAS granted→consumed 仅一次）、writer_queue_test（队列满 ErrQueueFull、ctx 取消后命令仍提交、budget 超时回滚）、config_roundtrip_test（K1 fixture 字节级回读） |
| 参考Python验证不能替代Go实际驱动集成 | 通过 | 全部测试经 modernc.org/sqlite v1.60.1 真驱动、文件库（t.TempDir）、-race |
| 队列超载、context 取消／结果丢失查 receipt | 通过 | writer_queue_test.go：容量断言、取消后提交证明、budget 回滚 |

## 验证命令（实际执行，输出见 verify-integration.log）

- `cd server && asdf exec go test -race -count=1 -timeout 300s ./tests/integration/...` → ok，exit 0；PASS 22、FAIL/SKIP 0
- B 报告附加：`-count=3 -race` 复跑 3 次无 flake
- server 冒烟中"同库重启幂等"即迁移重复启动验证（verify-smoke.log 属 T1.1 目录）

## 语义裁量记录（实现者 B，集成者确认接受）

1. go.mod 在并行 tidy 中丢失 modernc 条目，B 以 GOPROXY=off 从本地缓存外科恢复（v1.60.1 精确钉版）——唯一越界改动，集成者已复核 go.mod。
2. go:embed 不能引用父目录且拒绝 symlink：规范 SQL 在 server/migrations/，嵌入副本在同包目录，测试强制字节一致。
3. JSON 列以 CAST(? AS TEXT) 保住 Go []byte 模型（STRICT 表拒 BLOB）。
4. 命令预算 ctx 派生自 Background（非调用方 ctx），超时映射 ErrCommandBudget；仅 busy/locked 做全事务回滚重试 ≤2。
5. 迁移窗口 busy_timeout 临时放宽到 5s，提交后恢复。

## 未决

- 独立审查未完成前不置 done。
- 完整 Ledger 命令锁序与资源条件更新（T2.2）。
