from __future__ import annotations

import hashlib
import re
import shutil
import subprocess
import unicodedata
from pathlib import Path

source = Path("scripts/ci-architectural-checks.sh")
architecture_dir = Path("scripts/ci/architecture")
checks_dir = architecture_dir / "checks"
bootstrap_file = architecture_dir / "bootstrap.sh"
manifest_file = architecture_dir / "checks.manifest"
workflow_file = Path(".github/workflows/one-shot-split-ci-architectural-checks.yml")
migration_script = Path("scripts/ci/architecture/split_monolith.py")
legacy_copy = Path("scripts/.ci-architectural-checks.legacy.sh")

original = source.read_text(encoding="utf-8")
lines = original.splitlines(keepends=True)
if len(lines) < 1000:
    raise SystemExit(
        f"refusing to split {source}: expected the monolith, found only {len(lines)} lines"
    )

subprocess.run(["bash", "-n", str(source)], check=True)

first_check = next(
    (
        index
        for index, line in enumerate(lines)
        if line.startswith("# ── Check 0:")
    ),
    None,
)
if first_check is None:
    raise SystemExit("cannot locate the first architectural check marker")

preamble = list(lines[:first_check])
check_body = list(lines[first_check:])
if any("BASH_SOURCE" in line for line in check_body):
    raise SystemExit(
        "BASH_SOURCE occurs inside the check body; sourced modules would change its meaning"
    )

if not preamble or not preamble[0].startswith("#!"):
    raise SystemExit("expected a shebang at the start of the monolith")
preamble.pop(0)

try:
    strict_mode_index = preamble.index("set -euo pipefail\n")
except ValueError as exc:
    raise SystemExit("cannot locate the monolith strict-mode declaration") from exc
preamble.pop(strict_mode_index)

root_block_start = next(
    (
        index
        for index, line in enumerate(preamble)
        if line.startswith("# Resolve REPO_ROOT once")
    ),
    None,
)
if root_block_start is None:
    raise SystemExit("cannot locate the legacy REPO_ROOT resolution block")
root_block_end = next(
    (
        index
        for index in range(root_block_start, len(preamble))
        if preamble[index].startswith("REPO_ROOT=")
    ),
    None,
)
if root_block_end is None:
    raise SystemExit("cannot locate the end of the legacy REPO_ROOT resolution block")

preamble[root_block_start : root_block_end + 1] = [
    "# The canonical entrypoint resolves these paths, but assigns them here so\n",
    "# the wave-tracker lookup above keeps its original pre-REPO_ROOT behavior.\n",
    'SCRIPT_DIR="${ARCH_CI_ENTRYPOINT_DIR}"\n',
    'REPO_ROOT="${ARCH_CI_REPO_ROOT}"\n',
]
bootstrap = "".join(preamble)


def is_section_marker(line: str) -> bool:
    if line.startswith("# ──"):
        return True
    return re.match(r"^# Check (?:[0-9]+|[A-Z])\\b", line) is not None


candidate_boundaries = [
    index
    for index, line in enumerate(check_body[1:], start=1)
    if is_section_marker(line)
]


def shell_parses(fragment: str) -> bool:
    return subprocess.run(
        ["bash", "-n", "-c", fragment],
        stdout=subprocess.DEVNULL,
        stderr=subprocess.DEVNULL,
        check=False,
    ).returncode == 0


# Accept only marker boundaries that close a syntactically complete
# shell fragment. Marker-like text inside heredocs or open constructs
# is automatically folded into the surrounding module.
boundaries = [0]
start = 0
for boundary in [*candidate_boundaries, len(check_body)]:
    fragment = "".join(check_body[start:boundary])
    if fragment and shell_parses(fragment):
        boundaries.append(boundary)
        start = boundary
if boundaries[-1] != len(check_body):
    raise SystemExit("unable to find syntax-safe semantic boundaries for the check body")

sections: list[list[str]] = []
for start, end in zip(boundaries, boundaries[1:]):
    section = check_body[start:end]
    if section:
        sections.append(section)
if "".join("".join(section) for section in sections) != "".join(check_body):
    raise SystemExit("section split does not reconstruct the original check body")


def slugify(marker: str) -> str:
    marker = marker.lstrip("# ").strip()
    marker = unicodedata.normalize("NFKD", marker).encode("ascii", "ignore").decode("ascii")
    marker = re.sub(r"[^a-zA-Z0-9]+", "-", marker).strip("-").lower()
    return (marker or "section")[:72].rstrip("-")


generated: list[tuple[str, str]] = []
for position, section in enumerate(sections, start=1):
    marker = next((line for line in section if line.startswith("#")), f"section-{position}")
    filename = f"{position:03d}_{slugify(marker)}.sh"
    generated.append((filename, "".join(section)))

if len({name for name, _ in generated}) != len(generated):
    raise SystemExit("generated duplicate module filenames")
oversized = [
    (name, content.count("\n") + (0 if content.endswith("\n") else 1))
    for name, content in generated
    if content.count("\n") + (0 if content.endswith("\n") else 1) > 700
]
if oversized:
    details = ", ".join(f"{name}={count}" for name, count in oversized)
    raise SystemExit(f"semantic section still exceeds 700 lines: {details}")

# Remove the abandoned partial split so there is one canonical registry.
for stale_dir in [architecture_dir / "parts", architecture_dir / "lib", checks_dir]:
    if stale_dir.exists():
        shutil.rmtree(stale_dir)
for stale_file in [architecture_dir / "checks.sh", architecture_dir / "selfcheck.sh"]:
    stale_file.unlink(missing_ok=True)

checks_dir.mkdir(parents=True, exist_ok=True)
bootstrap_file.write_text(bootstrap, encoding="utf-8")
for filename, content in generated:
    (checks_dir / filename).write_text(content, encoding="utf-8")
manifest_file.write_text(
    "# Ordered SSOT registry for scripts/ci-architectural-checks.sh\n"
    + "".join(f"checks/{filename}\n" for filename, _ in generated),
    encoding="utf-8",
)

orchestrator = '''#!/usr/bin/env bash
# scripts/ci-architectural-checks.sh — architectural checks orchestrator.
#
# Check implementations live under scripts/ci/architecture/checks/ and
# execute in the ordered SSOT registry scripts/ci/architecture/checks.manifest.
# Modules are sourced deliberately: variables, shell options and exit behavior
# remain identical to the historical single-process monolith.
set -euo pipefail

if [ -n "${BASH_SOURCE[0]:-}" ]; then
  ARCH_CI_ENTRYPOINT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
  echo "CI: cannot resolve script directory from BASH_SOURCE[0]=" >&2
  echo "    (process substitution / bash -c \"source ...\" invocation)." >&2
  echo "    Run the script as: bash scripts/ci-architectural-checks.sh" >&2
  echo "    or set MIGRATIONS_ROOT=/abs/path/to/migrations/sqlite explicitly." >&2
  exit 1
fi

ARCH_CI_REPO_ROOT="$(cd "${ARCH_CI_ENTRYPOINT_DIR}/.." && pwd)"
ARCH_CI_MODULE_ROOT="${ARCH_CI_ENTRYPOINT_DIR}/ci/architecture"
ARCH_CI_BOOTSTRAP="${ARCH_CI_MODULE_ROOT}/bootstrap.sh"
ARCH_CI_MANIFEST="${ARCH_CI_MODULE_ROOT}/checks.manifest"

if [ ! -f "${ARCH_CI_BOOTSTRAP}" ]; then
  echo "CI: architectural bootstrap is missing: ${ARCH_CI_BOOTSTRAP}" >&2
  exit 1
fi
if [ ! -f "${ARCH_CI_MANIFEST}" ]; then
  echo "CI: architectural checks manifest is missing: ${ARCH_CI_MANIFEST}" >&2
  exit 1
fi

# shellcheck source=/dev/null
. "${ARCH_CI_BOOTSTRAP}"

arch_ci_seen="|"
arch_ci_count=0
while IFS= read -r arch_ci_relative || [ -n "${arch_ci_relative}" ]; do
  case "${arch_ci_relative}" in
    ""|\#*) continue ;;
    checks/*.sh) ;;
    *)
      echo "CI: invalid architectural check manifest entry: ${arch_ci_relative}" >&2
      exit 1
      ;;
  esac
  case "${arch_ci_seen}" in
    *"|${arch_ci_relative}|"*)
      echo "CI: duplicate architectural check manifest entry: ${arch_ci_relative}" >&2
      exit 1
      ;;
  esac
  arch_ci_seen="${arch_ci_seen}${arch_ci_relative}|"
  arch_ci_file="${ARCH_CI_MODULE_ROOT}/${arch_ci_relative}"
  if [ ! -f "${arch_ci_file}" ]; then
    echo "CI: architectural check module is missing: ${arch_ci_file}" >&2
    exit 1
  fi
  arch_ci_count=$((arch_ci_count + 1))
  # shellcheck source=/dev/null
  . "${arch_ci_file}"
done < "${ARCH_CI_MANIFEST}"

if [ "${arch_ci_count}" -eq 0 ]; then
  echo "CI: architectural checks manifest contains no modules" >&2
  exit 1
fi
'''
source.write_text(orchestrator, encoding="utf-8")

# Structural validation: the check body is byte-for-byte identical and
# every generated shell unit parses independently.
reconstructed = "".join(
    (architecture_dir / relative).read_text(encoding="utf-8")
    for relative in [f"checks/{filename}" for filename, _ in generated]
)
if reconstructed != "".join(check_body):
    raise SystemExit("generated modules differ from the original check body")
for shell_file in [source, bootstrap_file, *(checks_dir / name for name, _ in generated)]:
    subprocess.run(["bash", "-n", str(shell_file)], check=True)

# Behavioral validation: self-check output and status must remain exact.
legacy_copy.write_text(original, encoding="utf-8")
try:
    legacy = subprocess.run(
        ["bash", str(legacy_copy), "--self-check"],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        timeout=300,
        check=False,
    )
    modular = subprocess.run(
        ["bash", str(source), "--self-check"],
        stdout=subprocess.PIPE,
        stderr=subprocess.STDOUT,
        timeout=300,
        check=False,
    )
    if (legacy.returncode, legacy.stdout) != (modular.returncode, modular.stdout):
        raise SystemExit(
            "modular self-check behavior differs from the monolith\n"
            f"legacy_rc={legacy.returncode} modular_rc={modular.returncode}"
        )
    if modular.returncode != 0:
        raise SystemExit(modular.stdout.decode("utf-8", errors="replace"))
finally:
    legacy_copy.unlink(missing_ok=True)

digest = hashlib.sha256(original.encode("utf-8")).hexdigest()
print(f"split {len(lines)} lines into {len(generated)} semantic modules")
print(f"largest_module_lines={max(content.count(chr(10)) for _, content in generated)}")
print(f"original_sha256={digest}")

# Remove this one-shot workflow and migration helper in the generated commit.
workflow_file.unlink()
migration_script.unlink()
