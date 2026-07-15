# ── Check N: unresolved architecture-symbol references (action P0-5 slice 4/4) ─────
# The architecture/ docs reference internal/, pkg/, cmd/ paths and Go packages.
# When a ref'd file or package disappears (rename, deletion, move), the docs
# become silent lies. This check validates each leaf-string-scalar token matching
# the canonical Go-path prefixes (internal/, pkg/, cmd/) or the .go suffix.
# One Finding is emitted per missing reference; the trailing summary line
# reports the count. Exit 1 if any finding exists (fail-closed, AGENTS.md §8
# ARCHITECTURE-CI-GATES). The function lives at
# scripts/archcheck/symbol_refs.go and is invoked via
# `go run ./scripts/archcheck --symbol-refs`.
echo "=== Check N: symbol_refs unresolved references (action P0-5 slice 4/4) ==="
sr_out=$(go run ./scripts/archcheck --symbol-refs 2>&1 1>/dev/null) || sr_out=""
if [ -n "$sr_out" ]; then
    printf '%s\n' "$sr_out" | sed 's/^/  /'
    exit 1
fi
echo "Check N: 0 unresolved architecture-symbol references"

