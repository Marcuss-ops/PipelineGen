#!/usr/bin/env bash
# check_01_indexwriter.sh — forbid direct IndexWriter constructors
# outside the canonical composition root (QDRANT-002, Wave 14 §3).
#
# The canonical IndexWriter MUST live behind outbox.Dispatcher
# (production) or the admin reindex CLI (one-shot operator tool). Both
# sites are explicitly allowlisted:
#   - cmd/admin/reindex_qdrant.go            : operator-driven reindex
#   - internal/app/build_bundles_process.go  : SSOT composition root
# Every other Go file that constructs (or takes the address of) an
# IndexWriter is a QDRANT-002 regression.
#
# Mirrors scripts/ci-architectural-checks.sh::Check 1 verbatim. The
# canonical error signature is preserved bit-for-bit for any future
# scripts/ci-archcheck-e2e.sh switch (the e2e harness currently
# hard-greps the legacy; PR4 will switch the call site).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
. "${SCRIPT_DIR}/../lib/allowlist.sh"
. "${SCRIPT_DIR}/../lib/ripgrep.sh"
. "${SCRIPT_DIR}/../lib/report.sh"

REPO_ROOT="${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)}"

report_check_header "Check 1: forbid direct IndexWriter callers (QDRANT-002, Wave 14 §3)"

# Two patterns: qdrant.NewIndexWriter(...) (function call form) and
# (&qdrant.IndexWriter){...} / := qdrant.IndexWriter{...} (literal
# construction form). The allowlist mirrors the legacy.
literals=$(rg_strip_full_line_comments \
    -e 'qdrant\.NewIndexWriter\(' \
    -e '(&?qdrant\.IndexWriter)\{' \
    --glob '!**/cmd/admin/**' \
    --glob '!**/build_bundles_process.go' \
    --glob '!**/*_test.go' \
    -t go \
    . 2>/dev/null) || true

if [ -n "${literals}" ]; then
    echo "FAIL: direct IndexWriter constructor outside canonical composition root:"
    echo "${literals}"
    echo ""
    echo "Fix: route writes through outbox.Dispatcher (production) or the admin"
    echo "reindex CLI (operator tooling). The allowlist (cmd/admin/, internal/app/"
    echo "build_bundles_process.go) is the ONLY legitimate construction site."
    exit 1
fi
report_ok "no direct IndexWriter constructors outside the canonical allowlist"
