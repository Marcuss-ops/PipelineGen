#!/usr/bin/env bash
# check_05_mutation_primitives.sh — forbid mutation primitives in
# production callers (Wave 22 PR-4, June 2026).
#
# The three primitive methods UpsertClip / Restore / HardDelete are
# dispatcher-only / admin-only entry points to media_assets. The
# canonical narrow interface is mutations.AssetMutationPrimitives
# (consumed by outbox.Dispatcher) and admin.InternalAdminPurge
# (consumed by cmd/admin tooling). Production code paths in
# internal/application/** and internal/api/** MUST NOT call these
# methods directly.
#
# ARCH-ALLOWLIST opt-in: a transitional backfill or production test
# fixture that legitimately needs the literal at a non-production
# scope MUST prepend the magic marker `// ARCH-ALLOWLIST: admin-only`
# on the line preceding the call. lib/ripgrep.sh::rg_window_allowlist
# strips such hits from the failing-set via a 25-line scroll-window
# tolerance.
#
# Mirrors scripts/ci-architectural-checks.sh::Check 5 verbatim. The
# awk pre-pass for marker-window tolerance is centralised in
# lib/ripgrep.sh so other marker-using checks (Check 8 factory-only,
# Check 10b clips-ssot-only, Check 33 retention-created-at-mutable)
# reuse the same logic without duplicating the awk boilerplate.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "${SCRIPT_DIR}/../lib/allowlist.sh"
. "${SCRIPT_DIR}/../lib/ripgrep.sh"
. "${SCRIPT_DIR}/../lib/report.sh"

REPO_ROOT="${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)}"

report_check_header "Check 5: forbid mutation primitives in production callers (Wave 22 PR-4)"

# rg_window_allowlist with marker=admin-only. Six patterns cover the
# three primitive methods and their dotted-form variants; rg's -e
# flag allows multiple patterns in a single invocation. The helper
# applies the marker-window + full-line comment stripping pre-pass.
literal_calls=$(rg_window_allowlist admin-only \
    -e '\bUpsertClip\(' \
    -e '(^|[\s.(])r\.Restore\(' \
    -e '(^|[\s.(])r\.HardDelete\(' \
    -e '\.repo\.UpsertClip\(' \
    -e '\.clips\.UpsertClip\(' \
    -e '\.inner\.UpsertClip\(' \
    --glob '!**/*_test.go' \
    --glob '!**/mutations/primitives.go' \
    --glob '!**/admin/purge*.go' \
    --glob '!**/infrastructure/database/sqlite/**' \
    -t go \
    internal/application internal/api 2>/dev/null) || true

if [ -n "${literal_calls}" ]; then
    echo "FAIL: forbidden mutation primitive call in production caller:"
    echo "${literal_calls}"
    echo ""
    echo "Fix: route writes through outbox.Dispatcher.EnqueueAndIndex"
    echo "(production) or admin.InternalAdminPurge (offline tooling)."
    echo "The narrowed surface is mutations.AssetMutationPrimitives; the"
    echo "underlying methods on *assets.ClipsRepository are dispatcher-only."
    echo ""
    echo "If the call is genuinely admin migration / backfill, prepend the"
    echo "comment marker on the line preceding the call:"
    echo "    // ARCH-ALLOWLIST: admin-only"
    echo "    clipsRepo.UpsertClip(ctx, &asset.Asset{ID: \"__backfill__\"})"
    echo "The marker is stripped from the failing-set automatically."
    exit 1
fi
report_ok "no forbidden mutation primitive calls in production callers"
