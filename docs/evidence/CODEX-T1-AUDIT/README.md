# Codex · T1 实施独立反例证据

日期：2026-10-02。审查 [REVIEW-T1-CODEX](../../REVIEW-T1-CODEX.md)，整改。本目录记录实际复核，不是应用实现。

## 基线

`baseline-results.json` 记录四组命令、实际退出码、耗时与日志。现有 check、全 Go race fresh run、Web测试、构建全部通过。`baseline-1.log` 至 `baseline-4.log` 是对应输出。

## 反例

- `config-cases.json`：15个共同输入与业务预期，含合法基线、悬挂引用、单位错配、数量、扩展及合法两权益桶。预期描述验证结果，不禁止错误草稿保存。
- `codex_audit_test.go.txt`：Go探针，临时放入server/tests/integration运行；31个叶子用例，24个断言失败。
- `codex-audit.test.ts.txt`：Web探针，临时放入web/tests运行；17个用例，12个断言失败。SSE采用EventSource替身，不是真实网络证据。
- `go-probes.log`、`web-probes.log`：最后一次实际复核输出。非零是这些反例发现缺陷的预期，不是编译/环境失败。
- `probe-results.json`：实际命令/退出码、原实现文件变化为空、临时用例已删除。

Go回调预算用例是受控逻辑，不声称实证真实SQLite锁等待；profile摘要用例说明现有公开投影无法直接携带该字段，不声称已实现Router发生误重放。窗口日志可能按宿主本地时区打印time，但比较的是绝对Unix毫秒。

复现（会覆盖本目录探针日志，整改时应先复制原证据到新目录）：

```sh
python3 docs/evidence/CODEX-T1-AUDIT/run-probes.py
```

脚本执行前检查同名临时文件不存在，运行后finally清除，核对 `reviewed-source-sha256.json` 中原实现文件。测试源以 `.txt` 留存，未留在正常test目录里。

## 快照

`reviewed-source-sha256.json` 是本轮逐文件SHA256，包含运行源码、依赖锁、contracts/fixtures及根构建脚本，排除node_modules/dist/bin；用于识别本轮审查对象。它不涵盖文档修改，也不是永久证明代码正确。

`snapshot-check.json` 对原T1证据的239条逐文件hash核对：8条不匹配，4个当前关键文件未列。原快照末尾追加stdin聚合hash但未给出输入构造方法，因此只据可复现逐文件记录作此判断，不推断审查者实际阅读时间。
