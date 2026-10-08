#!/usr/bin/env python3
"""Reconcile G1-DEV leaf source hashes without changing historical evidence."""

from __future__ import annotations

import hashlib
import json
import re
import sys
from pathlib import Path, PurePosixPath


REPORTS = (
    "AUTH-01/INDEPENDENT-REVIEW.md",
    "CFG-08/INDEPENDENT-REVIEW.md",
    "LED-01/INDEPENDENT-REVIEW.md",
    "LED-02/INDEPENDENT-REVIEW.md",
    "LED-03/INDEPENDENT-REVIEW.md",
    "LED-04/INDEPENDENT-REVIEW.md",
)
HASH_ROW = re.compile(
    r"^\|\s*`(?P<path>[^`]+)`\s*\|\s*`?(?P<digest>[0-9a-f]{64})`?(?:\s*\|.*)?\s*$"
)
CURRENT_REVIEW_ROW = re.compile(
    r"^\|\s*`(?P<path>[^`]+)`\s*\|\s*`[0-9a-f]{64}`\s*\|\s*`(?P<digest>[0-9a-f]{64})`\s*\|\s*(?P<lines>[0-9]+)\s*\|\s*$"
)
MANIFEST_ROW = re.compile(r"^(?P<digest>[0-9a-f]{64})\s+(?P<path>\S+)\s*$")
CURRENT_TREE_REVIEWS = (
    "LED-03/ACTUAL-OUT-OF-RANGE/CURRENT-SOURCE-REVIEW.md",
    "LED-04/CURRENT-TREE-REVIEW-2026-10-04.md",
)


def repo_root() -> Path:
    for parent in Path(__file__).resolve().parents:
        if (parent / "docs/plans/TASK-INDEX.json").is_file():
            return parent
    raise RuntimeError("cannot locate repository root")


def parse_report(path: Path) -> list[dict[str, str]]:
    entries: list[dict[str, str]] = []
    for line in path.read_text(encoding="utf-8").splitlines():
        match = HASH_ROW.match(line)
        if not match:
            continue
        source = PurePosixPath(match.group("path"))
        if source.is_absolute() or ".." in source.parts:
            raise ValueError(f"unsafe source path in {path}: {source}")
        entries.append({"path": source.as_posix(), "sha256": match.group("digest")})
    if not entries:
        raise ValueError(f"no source hash table found in {path}")
    return entries


def parse_manifest(path: Path) -> dict[str, str]:
    entries: dict[str, str] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        match = MANIFEST_ROW.match(line)
        if match:
            source = match.group("path")
            if source in entries:
                raise ValueError(f"duplicate source path in aggregate manifest: {source}")
            entries[source] = match.group("digest")
    return entries


def parse_current_review(path: Path) -> dict[str, str]:
    entries: dict[str, str] = {}
    for line in path.read_text(encoding="utf-8").splitlines():
        match = CURRENT_REVIEW_ROW.match(line)
        if not match:
            continue
        source = PurePosixPath(match.group("path"))
        if source.is_absolute() or ".." in source.parts:
            raise ValueError(f"unsafe reviewed source path in {path}: {source}")
        entries[source.as_posix()] = match.group("digest")
    return entries


def main() -> None:
    root = repo_root()
    evidence = root / "docs/evidence/WORK"
    by_path: dict[str, list[dict[str, str]]] = {}
    report_entries: list[dict[str, str]] = []

    for report_name in REPORTS:
        report = evidence / report_name
        leaf = report_name.split("/", 1)[0]
        for item in parse_report(report):
            record = {"report": leaf, **item}
            report_entries.append(record)
            by_path.setdefault(item["path"], []).append(record)

    aggregate_path = evidence / "G1-DEV/source-manifest.sha256"
    aggregate = parse_manifest(aggregate_path)
    current_review: dict[str, str] = {}
    for review_name in CURRENT_TREE_REVIEWS:
        for source, digest in parse_current_review(evidence / review_name).items():
            previous = current_review.get(source)
            if previous is not None and previous != digest:
                raise ValueError(f"current reviews disagree for {source}: {previous} vs {digest}")
            current_review[source] = digest
    # Report order is chronological; a later leaf review may re-review and
    # re-hash a shared file after an approved write-scope extension.
    latest: dict[str, str] = {name: records[-1]["sha256"] for name, records in by_path.items()}

    comparisons: list[dict[str, object]] = []
    for source in sorted(latest):
        source_path = root / source
        accepted = latest[source]
        if not source_path.is_file():
            current = None
            state = "missing"
        else:
            current = hashlib.sha256(source_path.read_bytes()).hexdigest()
            state = "match" if current == accepted else "mismatch"
        reviewed = current_review.get(source)
        review_state = (
            "not-reviewed"
            if reviewed is None
            else "match"
            if current == reviewed
            else "mismatch"
        )
        history = by_path[source]
        comparisons.append(
            {
                "path": source,
                "acceptedSha256": accepted,
                "currentSha256": current,
                "state": state,
                "currentReviewSha256": reviewed,
                "currentReviewState": review_state,
                "reports": history,
            }
        )

    union_paths = set(latest)
    manifest_paths = set(aggregate)
    output = {
        "formatVersion": 1,
        "sourceReports": [f"docs/evidence/WORK/{name}" for name in REPORTS],
        "reportPrecedence": "listed order; later reports supersede earlier hashes for shared paths",
        "currentTreeReview": f"docs/evidence/WORK/LED-04/CURRENT-TREE-REVIEW-2026-10-04.md",
        "currentTreeReviews": [f"docs/evidence/WORK/{name}" for name in CURRENT_TREE_REVIEWS],
        "aggregateManifest": "docs/evidence/WORK/G1-DEV/source-manifest.sha256",
        "reportEntryCount": len(report_entries),
        "distinctLeafSourceCount": len(union_paths),
        "aggregateManifestPathCount": len(manifest_paths),
        "aggregateManifestOmissions": sorted(union_paths - manifest_paths),
        "aggregateManifestOnlyPaths": sorted(manifest_paths - union_paths),
        "aggregateManifestHashMismatches": {
            source: {"leafAcceptedSha256": latest[source], "manifestSha256": aggregate[source]}
            for source in sorted(union_paths & manifest_paths)
            if aggregate[source] != latest[source]
        },
        "leafReportHashConflicts": {
            source: records
            for source, records in sorted(by_path.items())
            if len({item["sha256"] for item in records}) > 1
        },
        "currentMatchCount": sum(item["state"] == "match" for item in comparisons),
        "currentMismatchCount": sum(item["state"] == "mismatch" for item in comparisons),
        "currentMissingCount": sum(item["state"] == "missing" for item in comparisons),
        "currentReviewedSourceCount": len(current_review),
        "currentReviewMatchCount": sum(item["currentReviewState"] == "match" for item in comparisons),
        "currentReviewMismatchCount": sum(item["currentReviewState"] == "mismatch" for item in comparisons),
        "sources": comparisons,
    }
    review_required = bool(
        output["currentMismatchCount"]
        or output["currentMissingCount"]
        or output["aggregateManifestOmissions"]
        or output["aggregateManifestOnlyPaths"]
        or output["aggregateManifestHashMismatches"]
        or output["currentReviewMismatchCount"]
    )
    output["status"] = "review-required" if review_required else "reconciled"
    destination = evidence / "G1-DEV/source-reconciliation.json"
    destination.write_text(json.dumps(output, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print(
        f"wrote {destination.relative_to(root)}: {output['distinctLeafSourceCount']} leaf sources, "
        f"{output['currentMatchCount']} current matches, {output['currentMismatchCount']} mismatches, "
        f"{output['currentMissingCount']} missing; "
        f"{len(output['aggregateManifestOmissions'])} aggregate omissions"
    )
    if review_required:
        print("status: REVIEW REQUIRED")
    print(
        f"current-tree review: {output['currentReviewMatchCount']}/"
        f"{output['currentReviewedSourceCount']} source digests match"
    )
    for item in comparisons:
        if item["state"] != "match":
            print(f"{item['state']}: {item['path']}")
    for source in output["aggregateManifestOmissions"]:
        print(f"aggregate omission: {source}")
    if review_required:
        sys.exit(1)


if __name__ == "__main__":
    main()
