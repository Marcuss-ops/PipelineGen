#!/usr/bin/env bash
# scripts/ci/architecture/checks/43_db_chain_outside_infra.sh
# P1.6 (June 2026): forward-prevention gate — forbid .DB() chain method
# calls outside internal/infrastructure/.
#
# The canonical SQL surface is *sql.DB directly, owned by
# internal/platform/sqlite/** only. Any production code
# outside infrastructure that chains .DB().QueryContext( (or .DB().ExecContext,
# .DB().Query, .DB().Exec, .DB().QueryRow, .DB().QueryRowContext, .DB().Prepare,
# .DB().PrepareContext) is a layering leak — SQL access must flow through
# typed ports declared in the application layer (internal/application/*/ports/)
# and implemented by infrastructure adapters (internal/platform/sqlite/**).
#
# Allowlist:
#   - internal/infrastructure/** : canonical owner of SQL
#   - *_test.go                  : test fixtures
#   - tests/fixtures/zero_legacy/** : self-check fixtures
#
# Zero-baseline: as of P1.6 (June 2026), there are 0 production hits.
# Any new .DB() chain outside infrastructure fails this gate.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"

hits=$(rg -n --type go \
    -e '\.DB\(\)\.(QueryContext|ExecContext|Query|Exec|QueryRow|QueryRowContext|Prepare|PrepareContext)\(' \
    --glob '!**/internal/infrastructure/**' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/**' \
    "${REPO_ROOT}"/internal/ "${REPO_ROOT}"/cmd/ "${REPO_ROOT}"/pkg/ 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    || true)

if [ -n "$hits" ]; then
    echo "FAIL: .DB() chain method call outside internal/infrastructure:"
    echo "$hits"
    echo ""
    echo "Fix: SQL must flow through typed ports in"
    echo "      internal/application/<consumer>/ports/ with the adapter in"
    echo "      internal/platform/sqlite/<feature>/, wired at the"
    echo "      composition root (internal/app/)."
    exit 1
fi
echo "OK: no .DB() chain method calls outside internal/infrastructure/"
