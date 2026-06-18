#!/usr/bin/env bash
# scripts/ast-legacy-finder.sh
#
# Wrapper that runs the Go AST-based legacy finder and outputs
# one file path per line. Falls back to `rg` if Go is unavailable.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

INCLUDE_TESTS="${1:-}"

if command -v go &>/dev/null; then
    cd "$PROJECT_DIR"
    args=(scripts/ast_legacy_finder.go internal)
    if [[ "$INCLUDE_TESTS" == "--include-tests" ]]; then
        args+=(--include-tests)
    fi
    go run "${args[@]}" 2>/dev/null
elif command -v rg &>/dev/null; then
    # Fallback: rg (less precise, matches comments)
    rg -l 'models\.MediaAsset' internal --glob '*.go' 2>/dev/null | sort
else
    echo "ERROR: neither go nor rg found" >&2
    exit 1
fi
