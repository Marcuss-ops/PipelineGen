#!/usr/bin/env bash
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
