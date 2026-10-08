# Codex · T1 第二轮返工 / 第三次复核证据

日期：2026-10-02。审查见 [REVIEW-T1-REWORK2-CODEX](../../../REVIEW-T1-REWORK2-CODEX.md)，对应历史返工证据见 `docs/evidence/T1-REWORK-3/`。。合成 local/test 数据，无真实业务副作用。

| 文件 | 内容/观察 |
|---|---|
| reviewed-source-sha256.json | 实施方最新覆盖集合162文件的审查基线摘要 |
| final-integrity.json | 162文件无最终字节变化、无新增/删除源码，临时测试已清除 |
| baseline-results.json、baseline-check/go/web/build/manifest.log | 五类标准入口独立通过；Web125，Go race fresh；清单162 |
| manifest-self-check.log | 清理临时测试后运行，六项漂移均被检测，162文件恢复一致 |
| config-cases.json | 最终17个完整组合入口用例：合法控制1个，15个准入反例，Go字段路径用例1个；每例有basis |
| rework3_audit_test.go.txt | 上述17例＋4个窗口修复控制＋3个执行等价控制；Go24叶子，16失败 |
| rework3-audit.test.ts.txt | Web完整schema/图/目录入口17例，15失败 |
| go-probes.log、web-probes.log、probe-results.json | 最终实际输出、命令、退出码、临时文件清理和实现摘要一致检查 |
| run-probes.py | 临时复制、执行、清除探针；保存结果，不修改最终实现 |
| identity-scope-observations.json | 未纳入最终门禁的探索材料，不证明双账户误拒 |
| preliminary/ | 第一轮探索日志和撤回说明；其计数不作为最终结论；临时测试存在时的漂移自检也只作探索记录 |

最终15个双侧非法接受归并：C1身份/规范化6例，C2数值/证据5例，C3目录/完整路径4例。Go额外1个字段定位失败为C4；7个窗口控制均通过。断言数不等于独立缺陷数。

## 复现

从项目根运行：

```sh
python3 docs/evidence/CODEX-T1-REWORK2-AUDIT/run-probes.py
```

runner 自身完成/退出不表示测试通过；读取 probe-results.json 的两条子命令退出码。当前两组测试返回1，临时 Go/Web 文件会在 finally 删除。原始源码最终应与 reviewed-source-sha256.json 一致。

独立基线命令：项目根 `make check`、`make test-web`、`make build`、`make verify-manifest`；server 目录 `go test -race -count=1 ./internal/... ./tests/integration/...`。漂移自检在没有临时测试时运行 `python3 scripts/snapshot.py --self-check`。

## 探索裁决与接口适配

双账户探索的合并初稿还有重复grant，且同factId是否实例共享缺少可信核验上下文；撤回通过断言。promo收费探索缺可靠计费元数据，撤回“必为付费”的断言。上述结果保留，不进入最终缺陷计数。事实作用域与收费性质需要在正式契约和受控核验材料中定义，而非审查者猜测。

允许补充可信事实上下文或调用新的编译入口，但必须保留业务断言：无法核验不产生可用容量；同物理容量不能复制；证据之外不能增额；错误数量空间不默默转换；缺必需限制不产生可执行offer。若完整编译将用户声明钳为合法保守容量，需要断言实际产物而非只看ok。

窗口等价控制使用同一ruleRevision确认 key/start/end 全等，分别覆盖自然/显式午夜、hour锚点小时无效、UTC别名。业务安全反例需要拒绝或规范化合并为单账，不能因为更改了分类字段就再获容量。
