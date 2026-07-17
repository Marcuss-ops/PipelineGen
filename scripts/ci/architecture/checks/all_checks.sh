#!/usr/bin/env bash
# scripts/ci/architecture/checks/all_checks.sh — thin SSOT dispatcher
# (godlike/06 one canonical owner per fact).
#
# Sourced accumulator for every check_<id>_<subject>.sh in numerical order.
# godlike/07 NO-FAKE-AVAILABILITY: empty-glob safe (compgen guard absorbs
# missing-pattern no-match under set -e; || true on the read loop
# neutralizes the read-EOF sentinel).
# godlike/06 ordering invariant: numerical-natural sort -t_ -k2,2n
# (POSIX sort; NOT GNU -V — macOS BSD sort lacks -V).
# godlike/07 portable: POSIX sort + read + process substitution.
set -euo pipefail
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
if compgen -G "${SCRIPT_DIR}/all_checks/check_*.sh" > /dev/null; then
    while IFS= read -r extracted; do
        [ -e "${extracted}" ] || continue
        # shellcheck source=/dev/null
        source "${extracted}"
    done < <(LC_ALL=C ls -1 "${SCRIPT_DIR}"/all_checks/check_*.sh 2>/dev/null | LC_ALL=C sort -t'_' -k2,2n -k2,2) || true
fi
