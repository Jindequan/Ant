"""Validate the plan DAG and render its human-readable leaf cards; no app changes.
Usage: python3 docs/plans/check_tree.py [--render]
TASK-INDEX.json owns task metadata; STATUS.md owns actual state.
"""
import json
import sys
from pathlib import Path

base = Path(__file__).resolve().parent
index = json.loads((base / 'TASK-INDEX.json').read_text())
tasks = index['tasks']
gates = index['gates']
ids = [t['id'] for t in tasks]
assert len(ids) == len(set(ids)), 'duplicate task ID'
required = ('title', 'group', 'phase', 'ownerRole', 'writeScope', 'deliverable', 'acceptance', 'verification', 'workMode')
for task in tasks:
    for field in required:
        assert task.get(field), (task['id'], field)
edges = {t['id']: t['dependsOn'] + ([t['entryGate']] if t['entryGate'] else []) for t in tasks}
assert not set(edges).intersection(gates)
edges.update({k: v['dependsOn'] for k, v in gates.items()})
seen, active = set(), set()
def visit(key):
    assert key in edges, ('missing dependency', key)
    assert key not in active, ('dependency cycle', key)
    if key in seen:
        return
    active.add(key)
    for dep in edges[key]:
        visit(dep)
    active.remove(key)
    seen.add(key)
for key in edges:
    visit(key)
mode_names = {'new': '新实现', 'reuse': '复用既有实物，按证据核对', 'repair': '当前缺陷修复', 'design': '有限设计与例子', 'verify': '独立验证', 'optional': '按需扩展'}
for group in sorted({t['group'] for t in tasks}):
    path = base / 'workstreams' / (group + '.md')
    content = path.read_text()
    # Preserve curated module notes above the first leaf, render the leaf metadata.
    header = content.split('\n## ', 1)[0]
    lines = [header.rstrip(), '']
    for t in [t for t in tasks if t['group'] == group]:
        deps = ', '.join(t['dependsOn']) or '无叶子前置'
        gate = t['entryGate'] or '不要求阶段出站；需其叶子输入'
        lines += [f"## {t['id']} · {t['title']}\n",
                  f"- **责任/范围：**{t['ownerRole']}；{t['phase']}；{mode_names[t['workMode']]}。输入＝前置任务产物＋原阶段卡/技术设计；前置 `{deps}`；阶段门 `{gate}`。",
                  '- **写入：**' + '、'.join('`' + p + '`' for p in t['writeScope']) + '。',
                  f"- **交付：**{t['deliverable']}。",
                  f"- **接受标准：**{t['acceptance']}。",
                  f"- **验证：**{t['verification']}。\n"]
    expected = '\n'.join(lines)
    if '--render' in sys.argv:
        path.write_text(expected)
    else:
        assert content == expected, ('leaf card drift; run --render', str(path))
# Every leaf must appear once in the actual state register.
status = (base / 'STATUS.md').read_text()
for task_id in ids:
    assert status.count('| ' + task_id + ' |') == 1, ('state row missing or duplicated', task_id)
print(f'OK: {len(tasks)} tasks, {len(gates)} gates; dependencies resolve and form a DAG; cards and state IDs match.')
