#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CHECKS_ENTRYPOINT="${SCRIPT_DIR}/ci/architecture/checks/all_checks.sh"

if [ ! -f "${CHECKS_ENTRYPOINT}" ]; then
  echo "CI: architectural checks entrypoint missing: ${CHECKS_ENTRYPOINT}" >&2
  exit 1
fi

exec bash "${CHECKS_ENTRYPOINT}" "$@"
