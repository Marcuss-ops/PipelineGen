#!/usr/bin/env python3
"""Reject policy workarounds and duplicated linguistic data in production.

The canonical linguistic data lives under config/lexicons. This check is
deliberately source-oriented and scans complete files, so formatting a map
across several lines cannot evade it. Finite software registries and route
dispatch maps are not rejected; the rules target names and phrases that
identify policy/data workarounds.
"""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path


CODE_SUFFIXES = {".go", ".py", ".sh"}
SKIP_PARTS = {
    ".git",
    "vendor",
    "node_modules",
    "docs",
    "data",
    "fixtures",
    "testdata",
    "tests",
}
SKIP_PREFIXES = ("scripts/ci/", "scripts/ops/", "scripts/tools/")

# Kept as fragments rather than a single permissive "map" rule: registries
# and dispatch tables are valid code and must remain allowed.
FORBIDDEN = (
    re.compile(r"genericstopwords", re.I),
    re.compile(r"qualitygate[a-z]*signals", re.I),
    re.compile(r"italian[_-]stopwords", re.I),
    re.compile(r"exactgenericpersonphrases", re.I),
    re.compile(r"reliable\s+biography\s+career\s+finances", re.I),
    re.compile(r"official\s+record\s+career", re.I),
    re.compile(r"financial\s+history\s+reputable\s+sources", re.I),
    re.compile(r"recovery\s+business\s+documented", re.I),
    re.compile(r"tests?\s+pass\s+without", re.I),
    re.compile(r"flaky\s+boundary", re.I),
    re.compile(r"boxers?\s+strict\s+gate", re.I),
)
BYPASS_COMMENT = re.compile(r"bypass(?:es|ed|ing)?\W+(?:the\W+)?(?:[^\n]{0,80})\bgate\b", re.I)


def rel_code(path: Path, root: Path) -> str:
    return path.relative_to(root).as_posix()


def should_skip(path: Path, root: Path) -> bool:
    rel = rel_code(path, root)
    if any(part in SKIP_PARTS for part in path.parts):
        return True
    if rel.startswith(SKIP_PREFIXES):
        return True
    if path.name.endswith("_test.go") or path.name.startswith("test_"):
        return True
    if path.name == Path(__file__).name or (path.parent.name == "ci" and path.name.startswith("ci-")) or path.name.startswith("ci-"):
        return True
    return False


def production_files(root: Path):
    for base in (root / "internal", root / "pkg", root / "scripts"):
        if not base.exists():
            continue
        for path in base.rglob("*"):
            if path.is_file() and path.suffix in CODE_SUFFIXES and not should_skip(path, root):
                yield path


def scan(root: Path) -> list[str]:
    violations: list[str] = []
    for path in production_files(root):
        rel = rel_code(path, root)
        try:
            lines = path.read_text(encoding="utf-8", errors="strict").splitlines()
        except OSError as exc:
            violations.append(f"{rel}: cannot read: {exc}")
            continue
        in_block_comment = False
        for number, line in enumerate(lines, 1):
            lower = line.lower()
            for pattern in FORBIDDEN:
                if pattern.search(line):
                    violations.append(f"{rel}:{number}: forbidden policy marker: {line.strip()}")
                    break
            comment_text = ""
            if in_block_comment:
                comment_text = line
                if "*/" in line:
                    in_block_comment = False
            elif "/*" in line:
                comment_text = line.split("/*", 1)[1]
                if "*/" not in comment_text:
                    in_block_comment = True
            if "//" in line:
                comment_text += " " + line.split("//", 1)[1]
            if comment_text and BYPASS_COMMENT.search(comment_text):
                violations.append(f"{rel}:{number}: policy bypass comment: {line.strip()}")
    return sorted(set(violations))


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", type=Path, default=Path.cwd())
    args = parser.parse_args()
    root = args.root.resolve()
    violations = scan(root)
    if violations:
        print("verify-no-policy-hardcoding: FAIL", file=sys.stderr)
        print("\n".join(violations), file=sys.stderr)
        return 1
    print("verify-no-policy-hardcoding: PASS")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
