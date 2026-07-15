# ── Check 9: forbid nil-dispatcher silent fallback (return nil) (TODO 16, Wave 19) ────
# The canonical write path for indexed mutations is outbox.Dispatcher. Any code
# path that silently no-ops when the dispatcher is nil (`if dispatcher == nil {
# return nil }`) risks silently dropping writes. Hard-error patterns (return
# fmt.Errorf, return err) are intentionally NOT caught by this check — those
# correctly fail-fast and the existing artlist/search_core.go is a canonical
# example of the fail-fast pattern.
#
# Allowlist:
#   - internal/app/**                : composition root (Build*Bundle constructors).
#   - internal/infrastructure/database/sqlite/outbox/** : canonical dispatcher impl.
#   - *_test.go                      : test fixtures may stub nil dispatcher.
#   - cmd/admin/**                   : one-shot operator tooling.
#   - tests/fixtures/zero_legacy/**  : self-check fixtures.
echo "=== Check 9: forbid nil-dispatcher silent fallback (return nil) (TODO 16) ==="
nilDispatcher=$(rg -nU --type go \
    -e 'dispatcher\s*==\s*nil\s*\{[^}]*return\s+nil\b' \
    -e 'dispatcher\s*==\s*nil\s*\{?\s*\n\s*return(\s+nil\b|\s*$)' \
    --glob '!**/internal/app/**' \
    --glob '!**/internal/infrastructure/database/sqlite/outbox/**' \
    --glob '!**/*_test.go' \
    --glob '!**/cmd/admin/**' \
    --glob '!tests/fixtures/zero_legacy/**' \
    . 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next
        print
    }' \
    || true)
if [ -n "$nilDispatcher" ]; then
    echo "FAIL: nil-dispatcher silent fallback (return nil) outside composition/test/allowlist:"
    echo "$nilDispatcher"
    echo ""
    echo "Fix: handlers MUST fail-fast when the dispatcher is nil rather than"
    echo "silently returning nil. The canonical pattern is:"
    echo "  if d.dispatcher == nil { return fmt.Errorf(\"dispatcher is nil — invariant broken\") }"
    echo "instead of:"
    echo "  if d.dispatcher == nil { return nil }  // silently drops writes"
    exit 1
fi
echo "OK: no nil-dispatcher silent fallback patterns outside composition/test/allowlist"

