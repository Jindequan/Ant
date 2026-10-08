#!/usr/bin/env python3
"""Compare two manifest files produced by scripts/snapshot.py. Exits non-zero
listing added/removed/changed files — used by `make verify-manifest` so an
evidence manifest cannot silently drift from the source it claims to cover."""
import json
import sys


def load(path):
    with open(path) as f:
        return {e["path"]: e["sha256"] for e in json.load(f)["files"]}


def main() -> int:
    if len(sys.argv) != 3:
        print("usage: manifest-diff.py <baseline> <current>", file=sys.stderr)
        return 2
    base, cur = load(sys.argv[1]), load(sys.argv[2])
    added = sorted(set(cur) - set(base))
    removed = sorted(set(base) - set(cur))
    changed = sorted(p for p in set(base) & set(cur) if base[p] != cur[p])
    if added or removed or changed:
        print("manifest DRIFT detected (review and re-sign before evidence):")
        for p in added:
            print("  +", p)
        for p in removed:
            print("  -", p)
        for p in changed:
            print("  ~", p)
        return 1
    print(f"manifest clean ({len(cur)} files identical)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
