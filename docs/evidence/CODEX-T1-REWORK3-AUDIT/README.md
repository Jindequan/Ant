# T1 第三轮返工独立复核证据

日期：2026-10-02。结论见 [复核报告](../../REVIEW-T1-REWORK3-CODEX.md)。

- reviewed-source-sha256.json：审查前 168 个实现/消费输入摘要。
- baseline-results.json 及 baseline-*.log：check、Go race fresh、Web、build、manifest 独立复跑全部成功。
- config-cases.json：1 个合法控制配置及 6 个单项变更反例。
- audit_test.go.txt / audit.test.ts.txt：审查探针原件。Go 10 个叶子用例 1 通过、9 失败；Web 7 个用例 1 通过、6 失败。失败数不是独立缺陷数。
- go-probes.log / web-probes.log：原始输出；包括窗口误判见证和完整编译结果。
- probe-results.json：执行退出码、实现摘要未变、临时测试已清除。

复现：在仓库根目录执行 `python3 docs/evidence/CODEX-T1-REWORK3-AUDIT/run-probes.py`。脚本会在 Go/Web 测试目录临时放入探针，分别运行并在 finally 删除；结果写入本目录。需要保留本次证据时先复制输出日志。脚本退出不代表测试通过，应读取 probe-results.json 中各测试命令的 exitCode。

没有真实供应商外呼，没有实现文件改动。本次没有验收 UI、持久激活事务、Ledger 运行行为或性能。
