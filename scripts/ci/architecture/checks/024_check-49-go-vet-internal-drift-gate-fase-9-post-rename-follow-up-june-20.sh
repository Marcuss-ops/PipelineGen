# ── Check 49: go vet ./internal/... drift gate (FASE 9 post-rename follow-up, June 2026) ──
# Canonical fail-closed `go vet` pass (covering internal/ entirely).
# Catches the regression class where an upstream rename (e.g. FASE 9
# Step 6 gdrive.Service -> drive.Admin) updates a struct field but a
# consumer (production code, test fixture, or composition wiring) still
# references the OLD field/method name. rg-based content gates miss
# type-signature drift because they scan for patterns, not type
# conformance; `go vet --all` runs the canonical `composites` checker
# (Go 1.20+) which catches `unknown field X in struct literal of type Y`
# regressions like the one observed at
# `internal/app/voiceover_adapters_drive_test.go:53:30`. This gate
# fails BEFORE a force-with-lease push lands.
#
# Fail-closed per godlike-08 zero-baseline rule: any non-allowlisted
# vet warning exits 1 with the offender listed.
#
# ARCH-ALLOWLIST opt-in (mirrors Check 5 / 10b / 11 / 33): a
# transitional backfill or intentional deprecation call that
# legitimately surfaces a vet warning MUST prepend the magic marker
# `// ARCH-ALLOWLIST: vet-warn` on the line preceding the offending
# construct. Per godlike-08 zero-baseline rule, new allowlist
# sites require explicit owner + deadline.
echo "=== Check 49: go vet ./internal/... drift gate ==="
all_vet=$(go vet ./internal/... 2>&1) || vet_rc=$?
vet_rc=${vet_rc:-0}
# Strip ARCH-ALLOWLIST: vet-warn sites from the failing-set (25-line
# scroll-window of the magic marker - mirrors Check 5 semantics).
literal_vet=$(printf '%s\n' "$all_vet" \
    | awk -F: '
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*vet-warn/) {
                markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2)
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next
            n = (markers[$1] == "" ? 0 : split(markers[$1], mlist, ","))
            allowed = 0
            for (mi = 1; mi <= n; mi++) {
              m = mlist[mi] + 0
              if (m > 0 && $2 + 0 >= m + 1 && $2 + 0 <= m + 25) { allowed = 1; break }
            }
            if (allowed) next
            print
        }' \
    || true)
if [ "$vet_rc" -ne 0 ] && [ -n "$literal_vet" ]; then
    echo "FAIL: go vet drift detected (non-allowlisted warnings):"
    printf '%s\n' "$literal_vet" | sed 's/^/  /'
    echo ""
    echo "Fix: align struct literals and method signatures with the canonical"
    echo "      type after upstream renames. If a vet warning is intentional,"
    echo "      prepend the magic marker on the preceding line:"
    echo "    // ARCH-ALLOWLIST: vet-warn"
    exit 1
fi
echo "OK: go vet ./internal/... passes (0 non-allowlisted warnings)"

