# scripts/ci/architecture/checks/all_checks/check_56_fase_2_1_pr_voice_freeze.sh
#
# Layer-split (July 2026): extracted from monolithic
# scripts/ci/architecture/checks/all_checks/check_60_governance.sh
# (857 LOC) into 12 sourceable rule files.
#
# Rule 56: FASE 2.1 PR-VOICE-FREEZE — gate banning new imports in
#                          legacy script handlers
#                          (internal/api/script/handler_legacy_*.go)
#
# Sourced by scripts/ci/architecture/checks/all_checks.sh
# (numerical-natural sort -t_ -k2,2n; macOS/BSD-portable per
# godlike/07 minimum-blast-radius).
#
# Per godlike/06 SSOT: FREEZE file set IS the canonical surface
# for legacy design-time concerns.
#
# Per godlike/07 NO-FAKE-AVAILABILITY: rule fails closed (exit 1)
# on any literal "github.com/... import line outside the 25-line
# ARCH-ALLOWLIST scroll-window.

# ── Check 56: FASE 2.1 PR-VOICE-FREEZE — gate banning new imports in legacy script handlers ──
# FASE 2.1 (July 2026) freezes the legacy script-generation adapter
# surface (internal/api/script/handler_legacy_*.go) for retirement on
# 2026-12-31. The FREEZE pattern is the canonical deadline-driven
# retirement per godlike/07 minimum-blast-radius: counters
# (legacy_clip_generation_total + legacy_generate_with_images_total)
# keep observability alive until rate(...[7d]) == 0.
echo "=== Check 56: FASE 2.1 PR-VOICE-FREEZE — gate banning new imports in legacy script handlers ==="
all_hits=$(rg -n --type go \
    -e '^[[:space:]]*"github\.com/' \
    --glob '!*_test.go' \
    internal/api/script/handler_legacy_*.go 2>/dev/null \
    || true)
literal_calls=$(printf '%s\n' "$all_hits" \
    | awk -F: '
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*legacy-script-freeze/) {
                markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2)
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next
            n = (markers[$1] == "" ? 0 : split(markers[$1], mlist, ","))
            allowed = 0
            for (mi = 1; mi <= n; mi++) {
              m = mlist[mi] + 0
              if (m > 0 && ($2 + 0 == m + 1 || $2 + 0 == m + 2)) { allowed = 1; break }
            }
            if (allowed) next
            print
        }' \
    || true)
if [ -n "$literal_calls" ]; then
    echo "FAIL: forbidden new external/internal import in internal/api/script/handler_legacy_*.go (FASE 2.1 PR-VOICE-FREEZE):"
    echo "$literal_calls"
    exit 1
fi
echo "OK: FASE 2.1 PR-VOICE-FREEZE respected — no new imports in legacy script handlers"
