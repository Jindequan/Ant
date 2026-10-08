# Codex · T1 返工批次独立复核证据

日期：2026-10-02。结论及归因见 [第二次审查](../../REVIEW-T1-REWORK-CODEX.md)，下一动作见后续返工记录。数据均为 local/test 合成配置，无供应商业务副作用。

## 文件与观察

| 文件 | 用途/结果 |
|---|---|
| reviewed-source-sha256.json | 本轮审查的实现源码逐文件 SHA-256，包括旧清单漏掉的 Python 和 `.d.mts` |
| final-integrity.json | 152 个实现文件最终摘要均一致；临时测试不存在 |
| baseline-results.json、baseline-*.log | 原有 check/Go/Web/manifest 入口独立复跑；Go 因 51.967584ms 超过 50ms 的预算断言失败，其余通过；baseline-build.log 记录构建成功 |
| retry-budget-repeat.log | 同一预算用例 `-race -count=10` 通过；不替代首次全量结果 |
| config-cases.json | 16 个完整配置反例/正例，共享业务 expected；Go 2 通过/14 失败，Web 7 通过/9 失败 |
| rework_audit_test.go.txt | 16 配置例＋Descriptor 交付＋3 窗口例的临时 Go 探针源；20 叶子断言，18 失败 |
| rework-audit.test.ts.txt | 根 schema＋引用图＋注册目录组合入口的 Web 探针源 |
| run-probes.py | 自动临时放置上述测试、执行、清除并检查实现摘要 |
| go-probes.log、web-probes.log、probe-results.json | 新探针最终原始输出、命令、退出码；临时文件已移除，实现最终无变化 |
| manifest-coverage.json | 实施方 148 文件范围与本轮 152 文件实现范围比较，列出漏项 |
| manifest-drift-probe.json/log | check-deps.py 追加无害注释，verify-manifest 错误地返回 0；已恢复原始字节 |

计数是断言数量，不是独立问题数量。配置 cases 的 expected 来自根 schema 或商业/证据/数量不变量，不以现有两侧同判作为正确性依据。

## 从项目根复现

```sh
python3 docs/evidence/CODEX-T1-REWORK-AUDIT/run-probes.py
make check
make test-contract
make test-integration
make test-web
make build
make verify-manifest
```

当前 runner 在未修复实现上预期两组测试命令返回 1；runner 会保存结果并完成清理，本身完成退出不代表测试通过，必须读取 probe-results.json。依赖已经安装，不需要真实账户或密钥。

原始 Go 预算全量命令：在 server 目录执行 `go test -race -count=1 ./internal/... ./tests/integration/...`。定向复跑：`go test -race -count=10 -run '^TestCodexRegressionRetryBudget$' -v ./tests/integration`。墙钟波动只能支持测试可靠性判断，不能单凭 51.97ms 判定当前绝对 deadline 实现错误。

窗口锚点小数例若最终契约明确仅支持整秒，可在完整配置入口证明该精度被拒绝，并相应适配低层测试；禁止继续默默截断。其他反例不能改成接受以掩盖问题。

漂移探针只追加注释并在 finally 恢复；最终摘要检查用来确认实现无字节变化。历史证据是审查输入，不应被后续实现静默覆盖；将适配后的永久业务回归放入受清单覆盖的测试/fixture 目录。
