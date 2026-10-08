#!/usr/bin/env python3
"""Dependency boundary gate (R14). Parses the ACTUAL import graph via
`go list -json ./...`; a package-listing failure is a hard error, never a
silent skip. Checked edges mirror 01-ARCHITECTURE §2 and the rework freeze.
Run from anywhere: the script locates the server module itself.
"""
import json
import os
import subprocess
import sys

SERVER = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "server")

# package -> allowed internal package prefixes ("" entries are stdlib-only)
ALLOWED_INTERNAL = {
    "ant/server/internal/domain": ["ant/server/internal/domain", "ant/server/internal/jsonx"],
    "ant/server/internal/jsonx": [],
    # config compiles the embedded root JSON Schema (B2) — santhosh-tekuri is
    # a pure validation library, no IO/network. Q1: config consumes the pure
    # window rule (domain/resources) for executable-equal identity matching.
    "ant/server/internal/config": ["ant/server/internal/config", "ant/server/internal/domain", "ant/server/internal/domain/resources", "ant/server/internal/jsonx", "github.com/santhosh-tekuri/jsonschema"],
    # AUTH-01 introduces the HTTP middleware's dependency on the trusted
    # security principal; API handlers remain the only HTTP package.
    "ant/server/internal/api": ["ant/server/internal/api", "ant/server/internal/application", "ant/server/internal/domain", "ant/server/internal/jsonx", "ant/server/internal/security", "ant/server/internal/providers/localtest"],
    "ant/server/internal/adapters": ["ant/server/internal/adapters", "ant/server/internal/domain"],
    "ant/server/internal/planning": ["ant/server/internal/planning", "ant/server/internal/config", "ant/server/internal/domain"],
    "ant/server/internal/security/sqlitetoken": ["ant/server/internal/security/sqlitetoken", "ant/server/internal/security", "ant/server/internal/store", "ant/server/internal/domain"],
    "ant/server/internal/security": ["ant/server/internal/security", "ant/server/internal/domain"],
    # Hook contracts are pure, in-process policy and observer interfaces.
    "ant/server/internal/hooks": [],
    "ant/server/internal/runtime": ["ant/server/internal/runtime", "ant/server/internal/domain"],
    "ant/server/internal/store": ["ant/server/internal/store", "ant/server/internal/domain"],
    "ant/server/internal/store/sqlite": ["ant/server/internal/store", "ant/server/internal/store/sqlite", "ant/server/internal/domain"],
    # LED-01 claim commands use the Store transaction port; keep this edge
    # scoped to commands rather than allowing the entire ledger package.
    "ant/server/internal/ledger/commands": ["ant/server/internal/ledger/commands", "ant/server/internal/ledger", "ant/server/internal/domain", "ant/server/internal/store"],
    "ant/server/internal/ledger": ["ant/server/internal/ledger", "ant/server/internal/domain"],
    "ant/server/internal/transport": [
        "ant/server/internal/transport", "ant/server/internal/adapters",
        "ant/server/internal/domain", "ant/server/internal/ledger",
    ],
    "ant/server/internal/providers/localtest": ["ant/server/internal/providers/localtest", "ant/server/internal/domain", "ant/server/internal/providers/chatcompletions"],
    "ant/server/internal/providers/catalog": ["ant/server/internal/providers/catalog", "ant/server/internal/config", "ant/server/internal/domain"],
    # test-only contract package may import anything internal
    "ant/server/internal/contracttest": None,  # None = unrestricted
}

# ENG-01 ruling (2026-10-08, STATUS.md): two narrow, written exceptions for
# the quota observation subsystem. Neither is execution egress, and neither
# may be widened without a new ruling.
#
# - quota/cookiestore opens browsers' own cookie databases read-only and
#   immutable (never Ant's database); it is the only SQL use on the quota
#   side, so quota itself stays SQL-free.
# - quota keeps net/http TEMPORARILY for: read-only provider usage/balance
#   reads (origin-pinned client: no cross-host or HTTPS->HTTP redirects,
#   HTTPS only off loopback), the one-off 127.0.0.1 console-login callback,
#   and its loopback-only page API. Sunset: design 11 phase 4 removes
#   internal/quota; the supply engine's collectors receive an injected
#   fetch port implemented in the controlled transport, with no exemption.
QUOTA_OBSERVATION = "ant/server/internal/quota"
QUOTA_COOKIE_STORE = "ant/server/internal/quota/cookiestore"

FORBIDDEN_ANYWHERE = {
    "modernc.org": {"ant/server/internal/store/sqlite", QUOTA_COOKIE_STORE},
    "database/sql": {"ant/server/internal/store", "ant/server/internal/store/sqlite", QUOTA_COOKIE_STORE},
    # HTTP requests are issued only at the API server boundary and in the
    # controlled transport; providers, domain, application and storage remain
    # free of direct network clients. The page itself is served by api.
    "net/http": {"ant/server/internal/api", "ant/server/internal/transport", QUOTA_OBSERVATION},
}


def main() -> int:
    try:
        out = subprocess.run(
            ["go", "list", "-json", "./..."],
            cwd=SERVER, capture_output=True, text=True, check=True,
        )
    except subprocess.CalledProcessError as e:
        print("check-deps: go list failed (hard error):", e.stderr, file=sys.stderr)
        return 1

    decoder = json.JSONDecoder()
    text = out.stdout.strip()
    violations = []
    idx = 0
    packages = []
    while idx < len(text):
        obj, next_idx = decoder.raw_decode(text, idx)
        packages.append(obj)
        idx = next_idx
        while idx < len(text) and text[idx] in " \n":
            idx += 1

    for pkg in packages:
        import_path = pkg.get("ImportPath", "")
        if import_path.endswith(".test"):
            continue  # generated test mains may re-import anything
        imports = pkg.get("Imports", [])
        allowed = None
        for prefix, rule in ALLOWED_INTERNAL.items():
            if import_path == prefix or import_path.startswith(prefix + "/"):
                allowed = rule
                break
        if import_path.startswith("ant/server/cmd/"):
            allowed = None  # composition root: unrestricted
        for imp in imports:
            if allowed is not None and imp.startswith("ant/server/internal"):
                if not any(imp == a or imp.startswith(a + "/") for a in allowed):
                    violations.append(f"{import_path} -> {imp} (not in allowed graph)")
            if import_path.startswith("ant/server/cmd/"):
                continue  # composition root may hold the HTTP server (T1) and handlers (T2)
            for pattern, exempt in FORBIDDEN_ANYWHERE.items():
                if imp.startswith(pattern) and import_path not in exempt:
                    violations.append(f"{import_path} -> {imp} (forbidden outside {sorted(exempt)})")

    if violations:
        print("check-deps: DEPENDENCY VIOLATIONS:")
        for v in sorted(set(violations)):
            print("  ->", v)
        return 1
    print(f"check-deps: dependency boundaries OK ({len(packages)} packages)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
