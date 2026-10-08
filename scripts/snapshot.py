#!/usr/bin/env python3
"""Per-file source manifest for the ANT workspace (R14; coverage per D-B6-v2).

The covered set is defined by CONSUMPTION, not by a file-extension whitelist
(the old suffix filter silently dropped scripts/check-deps.py,
scripts/manifest-diff.py, scripts/snapshot.py and
web/scripts/contract-tools.d.mts — the very files that decide dependency
checking, snapshot generation and manifest comparison; see B6 in
docs/REVIEW-T1-REWORK-CODEX.md). Coverage now means:

  - every file under server/, web/, contracts/ and scripts/ (sources, tests,
    configs, generated types, lock files, scripts — everything), plus the
    root build files;
  - the config-case inputs read by the permanent regression tests
    (server/tests and web/tests read docs/evidence/*/config-cases.json).

Excluded are ONLY dependency caches and build outputs (node_modules, dist,
bin, __pycache__, .git). Files are hashed as raw bytes, so binary inputs are
covered exactly like text.

Drift protocol (`make verify-manifest`): snapshot the tree, diff against the
signed baseline with scripts/manifest-diff.py; a harmful OR harmless change
to any gated file must surface before evidence is re-signed.

The coverage set itself is verifiable:

    python3 scripts/snapshot.py --self-check

mutates each gatekeeping file (the three gate scripts, the web test-side
type surface, both regression config-case inputs) with a harmless marker,
asserts the change is reported as drift, restores the original bytes and
asserts a clean restore. Exits non-zero if any mutation would go unreported.

Run from the repository root:

    python3 scripts/snapshot.py <output.json>
"""
import hashlib
import json
import os
import sys

INCLUDE_DIRS = ["server", "web", "contracts", "scripts"]
# Root build/config files. go.mod/go.sum live under server/ and
# package.json/package-lock.json under web/: covered by directory inclusion,
# not listed here.
ROOT_FILES = ["Makefile", ".tool-versions", ".gitignore"]
# Test inputs consumed by the permanent regression tests (outside the
# gated directories): D-B6-v2 binds them to the reviewed snapshot.
CONSUMED_INPUTS = [
    "docs/evidence/CODEX-T1-REWORK-AUDIT/config-cases.json",
    "docs/evidence/CODEX-T1-AUDIT/config-cases.json",
    "docs/evidence/CODEX-T1-REWORK2-AUDIT/config-cases.json",
    "docs/evidence/CODEX-T1-REWORK3-AUDIT/config-cases.json",
    "docs/evidence/CODEX-T1-CURRENT-2026-10-03/role-cases.json",
    "docs/evidence/WORK/CFG-01/rules.go",
]
EXCLUDE_PARTS = {"node_modules", "dist", "bin", "__pycache__", ".git"}
# Files mutated (and restored) by --self-check: (path, comment marker); a
# None marker appends bare whitespace for JSON inputs. These are the files
# whose omission B6 flagged, plus the regression test inputs.
SELF_CHECK_TARGETS = [
    ("scripts/check-deps.py", "#"),
    ("scripts/manifest-diff.py", "#"),
    ("scripts/snapshot.py", "#"),
    ("web/scripts/contract-tools.d.mts", "//"),
    ("docs/evidence/CODEX-T1-REWORK-AUDIT/config-cases.json", None),
    ("docs/evidence/CODEX-T1-AUDIT/config-cases.json", None),
]


def excluded(rel_path: str) -> bool:
    return any(part in EXCLUDE_PARTS for part in rel_path.split(os.sep))


def iter_covered_files():
    for root_dir in INCLUDE_DIRS:
        for dirpath, dirnames, filenames in os.walk(root_dir):
            # Prune excluded subtrees entirely (never descend into caches).
            dirnames[:] = [d for d in dirnames if d not in EXCLUDE_PARTS]
            for fn in filenames:
                full = os.path.join(dirpath, fn)
                if not excluded(full):
                    yield full
    for name in ROOT_FILES + CONSUMED_INPUTS:
        if os.path.isfile(name):
            yield name


def scan() -> list:
    entries = []
    for full in iter_covered_files():
        with open(full, "rb") as f:
            data = f.read()  # raw bytes: binary-safe by construction
        entries.append({
            "path": full.replace(os.sep, "/"),
            "sha256": hashlib.sha256(data).hexdigest(),
            "bytes": len(data),
        })
    entries.sort(key=lambda e: e["path"])
    return entries


def sha_by_path(entries: list) -> dict:
    return {e["path"]: e["sha256"] for e in entries}


def diff_hashes(base: dict, cur: dict) -> tuple:
    added = sorted(set(cur) - set(base))
    removed = sorted(set(base) - set(cur))
    changed = sorted(p for p in set(base) & set(cur) if base[p] != cur[p])
    return added, removed, changed


def self_check() -> int:
    base = sha_by_path(scan())
    print(f"self-check: baseline covers {len(base)} files")
    failures = []
    for target, marker in SELF_CHECK_TARGETS:
        if not os.path.isfile(target):
            failures.append(target)
            print(f"  MISSED: {target} (file missing from the covered tree)")
            continue
        with open(target, "rb") as f:
            original = f.read()
        try:
            if marker is None:  # JSON input: harmless trailing whitespace
                probe = original + b"\n"
            else:
                probe = original + b"\n" + marker.encode() + b" manifest self-check probe (harmless)\n"
            with open(target, "wb") as f:
                f.write(probe)
            added, removed, changed = diff_hashes(base, sha_by_path(scan()))
            if changed == [target] and not added and not removed:
                print(f"  DRIFT reported: ~ {target}")
            else:
                failures.append(target)
                print(f"  MISSED: {target} (added={added} removed={removed} changed={changed})")
        finally:
            with open(target, "wb") as f:
                f.write(original)
    if sha_by_path(scan()) != base:
        failures.append("<restore>")
        print("  MISSED: the tree did not restore to baseline bytes")
    else:
        print(f"  restored clean ({len(base)} files identical)")
    if failures:
        print(f"self-check FAILED: {failures}", file=sys.stderr)
        return 1
    print("self-check PASSED: every gatekeeping file change is reported as drift")
    return 0


def write_manifest(out_path: str) -> int:
    entries = scan()
    manifest = {
        "generatedAt": os.environ.get("ANT_SNAPSHOT_TIME", ""),
        "generator": "scripts/snapshot.py",
        "algorithm": "sha256",
        "files": entries,
    }
    with open(out_path, "w") as f:
        json.dump(manifest, f, ensure_ascii=False, indent=1)
        f.write("\n")
    print(f"{len(entries)} files -> {out_path}")
    return 0


def main() -> int:
    if len(sys.argv) == 2 and sys.argv[1] == "--self-check":
        return self_check()
    if len(sys.argv) != 2:
        print("usage: snapshot.py <output.json> | snapshot.py --self-check", file=sys.stderr)
        return 2
    return write_manifest(sys.argv[1])


if __name__ == "__main__":
    sys.exit(main())
