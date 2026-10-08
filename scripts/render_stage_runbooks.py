#!/usr/bin/env python3
"""Render per-stage execution/acceptance tables from TASK-INDEX.json."""

from __future__ import annotations

import argparse
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
INDEX = ROOT / "docs/plans/TASK-INDEX.json"
OUT = ROOT / "docs/plans/execution"


def esc(value: object) -> str:
    return str(value or "—").replace("|", "\\|").replace("\n", " ")


def render_group(tasks: list[dict], title: str, intro: str) -> str:
    rows = [
        f"# {title} · 执行与验收清单",
        "",
        "> 自动生成：节点字段以 [TASK-INDEX.json](../TASK-INDEX.json) 为唯一来源。编辑索引后运行 `python3 scripts/render_stage_runbooks.py`。本文件给执行者逐项勾验；实际状态仍只记在 [STATUS.md](../STATUS.md)。",
        "",
        intro,
        "",
        "## 叶子执行与验收",
        "",
        "按依赖顺序执行。每一行的写集、交付物、验收标准和验证方法均来自任务索引；对应阶段卡的步骤与业务语义继续有效。",
        "",
        "| ID / 阶段 | 任务 | 前置 | 门禁 | 写入范围 | 交付物 | 验收标准 | 验证方法 |",
        "|---|---|---|---|---|---|---|---|",
    ]
    for task in tasks:
        deps = ", ".join(task.get("dependsOn", [])) or "—"
        scope = "<br>".join(f"`{esc(x)}`" for x in task.get("writeScope", [])) or "—"
        rows.append(
            "| " + " | ".join([
                esc(task["id"] + " / " + task.get("phase", "")),
                esc(task.get("title")),
                esc(deps),
                esc(task.get("entryGate") or "无"),
                scope,
                esc(task.get("deliverable")),
                esc(task.get("acceptance")),
                esc(task.get("verification")),
            ]) + " |"
        )
    rows += [
        "",
        "## 本阶段完成条件",
        "",
        "- 本阶段所有必需叶子均有实现或真实输入产物，逐项自检通过，证据保存到各叶子的 `docs/evidence/WORK/<ID>/` 或阶段证据目录。",
        "- 当前阶段卡列出的阶段出口、决定项和适用的 ACCEPTANCE-MATRIX 行全部有证据；失败、未执行、不适用须分别标注并给出原因。",
        "- 执行者将任务状态留在 `verifying`，并记录自检 PASS/FAIL、最终文件清单与摘要、命令和退出码；不得把自检写成独立验收。",
        "- 只有本阶段出口的全部前置均已正式满足，才能越过阶段门。缺失的真实输入或授权必须停在门前，登记准确缺口；允许的独立准备工作可继续。",
        "",
        "## 最终验收",
        "",
        "独立验收者对最终源码树与原始证据复核，重跑适用命令，抽查正向路径和关键反例，并检查写集/契约/阶段门。发现问题时退回对应叶子修复；通过后才把叶子从 `verifying` 改为 `done` 并关闭阶段出口。",
        "",
    ]
    return "\n".join(rows)


def outputs() -> dict[Path, str]:
    data = json.loads(INDEX.read_text())
    tasks = data["tasks"]
    result = {}
    for stage in range(8):
        stage_id = f"T{stage}"
        grouped = sorted(
            [t for t in tasks if t.get("phase", "").split(".")[0] == stage_id],
            key=lambda t: (t.get("phase", ""), t["id"]),
        )
        result[OUT / f"{stage_id}.md"] = render_group(
            grouped,
            stage_id,
            f"对应阶段执行步骤：[{stage_id} 阶段卡](../{stage:02d}-{'SCOPE-AND-ADMISSION' if stage == 0 else 'CONTRACTS' if stage == 1 else 'VERTICAL-SLICE' if stage == 2 else 'CORRECTNESS' if stage == 3 else 'MULTI-ACCOUNT' if stage == 4 else 'OPERATIONS-AND-ONBOARDING' if stage == 5 else 'PERFORMANCE-AND-DELIVERY' if stage == 6 else 'EXTENSION'}.md)。阶段卡为业务步骤权威来源；本清单补足索引中每个叶子的精确验收、写集和验证入口。"
        )
    dev = sorted([t for t in tasks if t.get("entryGate") == "G1-DEV"], key=lambda t: t["id"])
    result[OUT / "G1-DEV.md"] = render_group(
        dev,
        "G1-DEV 本地禁网切片",
        "仅包括 TASK-TREE §5 明确开放的 AUTH-01、CFG-08、LED-01–04。全部使用 localtest/假身份/假凭证与本地 SQLite，并对测试进程禁用外网。它不满足真实 G1，也不授权供应商调用、G2 出站或生产动作。",
    )
    return result


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true", help="fail if generated files are stale")
    args = parser.parse_args()
    rendered = outputs()
    stale = []
    for path, content in rendered.items():
        if args.check:
            if not path.exists() or path.read_text() != content:
                stale.append(str(path.relative_to(ROOT)))
        else:
            path.parent.mkdir(parents=True, exist_ok=True)
            path.write_text(content)
    if stale:
        print("stale stage runbooks: " + ", ".join(stale))
        print("run: python3 scripts/render_stage_runbooks.py")
        return 1
    if args.check:
        print(f"stage runbooks current ({len(rendered)} files)")
    else:
        print(f"rendered {len(rendered)} stage runbooks")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
