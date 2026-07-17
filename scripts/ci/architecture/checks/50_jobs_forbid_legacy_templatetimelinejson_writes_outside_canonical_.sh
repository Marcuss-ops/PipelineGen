#!/usr/bin/env bash
# 50_jobs sub-check (verbatim-extracted section of the original monolithic
# scripts/ci/architecture/checks/50_jobs.sh — see
# scripts/ci/architecture/checks/lib/50_jobs_section_map.json for the
# byte-precise line range, and the lib/50_jobs_profile.sh for the
# analysis that produced this split). Do NOT hand-edit body to fix
# checks; edit the original 50_jobs.sh and re-run the splitter (or
# move body content out-of-line manually here with a corresponding
# orchestrator update).

if [ -n "${BASH_SOURCE[0]:-}" ]; then
    SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
else
    echo "CI: cannot resolve sub-check directory from BASH_SOURCE[0]=" >&2
    exit 1
fi
# shellcheck source=/dev/null
source "${SCRIPT_DIR}/lib/50_jobs_lib.sh"

# ── Verbatim section body extracted from the original monolithic ────────
# ── Check 58: forbid legacy Template/TimelineJSON writes outside canonical allowlist ──
# godlike/06 SSOT one-canonical-owner-per-fact: PersistenceProcessor is the
# SOLE WRITER of Template + TimelineJSON on the scripts table (both set to
# empty "" under PR 6 — the dedicated idempotency_key + specscene columns are
# the canonical storage). The translators in repository.go
# (toSQLiteScriptRecord / fromSQLiteScriptRecord) are the canonical READ-path
# owners that translate between SQLite side and ports side. Every other
# production-code struct literal in internal/application/scripts/ that
# assigns Template: or TimelineJSON: outside those two canonical files is
# a SSOT regression — the fields are legacy columns intentionally left empty
# for newly-inserted rows per the PR 6 migration strategy.
#
# Pattern anchors (ripgrep regex, root-anchored substring):
#   Template:       — struct-literal field assignment (any value)
#   TimelineJSON:   — struct-literal field assignment (any value)
#
# Allowlist (the ONLY legitimate Template:/TimelineJSON: sites):
#   - internal/application/scripts/adapters/repository.go
#     (toSQLiteScriptRecord + fromSQLiteScriptRecord — canonical translators)
#   - internal/application/scripts/adapters/processor_persistence.go
#     (PersistenceProcessor — SOLE canonical writer, sets both to "")
#
# Tests (*_test.go) are excluded so test fixtures may freely construct
# ScriptRecord literals.
#
# ARCH-ALLOWLIST opt-in (mirrors Check 5/8/9/11/50): a transitional
# backfill that legitimately needs to write Template: or TimelineJSON:
# MUST prepend the magic marker
# `// ARCH-ALLOWLIST: template-timeline-legacy` on the line preceding
# the field assignment. The awk pre-pass strips such hits from the
# failing-set via the 25-line scroll-window tolerated by Check 5/8/9.
# Per AGENTS.md §8 zero-baseline rule, new allowlist entries require
# explicit owner + deadline.
echo "=== Check 58: forbid legacy Template/TimelineJSON writes outside canonical allowlist ==="
all_hits=$(rg -n --type go \
    -e 'Template:\s' \
    -e 'TimelineJSON:\s' \
    --glob '!**/application/scripts/adapters/repository.go' \
    --glob '!**/application/scripts/adapters/processor_persistence.go' \
    --glob '!**/*_test.go' \
    internal/application/scripts/ 2>/dev/null \
    || true)
# Drop full-line comments AND lines preceded by the ARCH-ALLOWLIST marker
# (25-line scroll-window, mirrors Check 5/8/9 pattern).
literal_calls=$(printf '%s\n' "$all_hits" \
    | awk -F: '
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*template-timeline-legacy/) {
                markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2)
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
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
if [ -n "$literal_calls" ]; then
    echo "FAIL: legacy Template:/TimelineJSON: field write outside canonical allowlist:"
    echo "$literal_calls"
    echo ""
    echo "Fix: Template and TimelineJSON are legacy columns intentionally left"
    echo "      empty for newly-inserted rows under PR 6. The dedicated"
    echo "      idempotency_key + specscene columns are the canonical storage."
    echo "      PersistenceProcessor is the SOLE canonical writer; repository.go"
    echo "      translators are the canonical READ-path owners. Any new"
    echo "      Template:/TimelineJSON: assignment outside those two files is a"
    echo "      godlike/06 one-owner-per-fact regression."
    echo ""
    echo "If the write is genuinely a transitional backfill / migration,"
    echo "      prepend the magic marker on the line preceding the assignment:"
    echo "    // ARCH-ALLOWLIST: template-timeline-legacy"
    echo "    Template: \"backfill_value\","
    exit 1
fi
echo "OK: no legacy Template:/TimelineJSON: writes outside canonical allowlist (godlike/06 SSOT)"


# === Check 59: Azione 13 VLM direct-caller ban (forward-prevention godlike/07) ===
# Bypass callers that hit /vlm/<verb> without going through the canonical
# *vlm.Client proxy are godlike/06 SSOT regressions. Canonical call surface
#   (SSOT): internal/infrastructure/ai/vlm/ (4 methods: AutoTagImage,
#   ValidateScript, DedupCheck, AutoTagLocal).
# Production callers MUST consume *vlm.Client via composition root.
# Permitted exceptions carry // ARCH-ALLOWLIST: vlm-direct-caller on the
# line preceding the call site (mirrors Check 54 + 58 posture).
vlm_bypass_hits=$(rg -n --hidden '\bhttp(|Get|Post|NewRequest|NewRequestWithContext)\(.*"/vlm/' internal/application internal/api 2>/dev/null || true)
filtered_hits=""
if [ -n "$vlm_bypass_hits" ]; then
  filtered_hits=$(echo "$vlm_bypass_hits" | while IFS= read -r hit; do
    [ -z "$hit" ] && continue
    f=$(echo "$hit" | cut -d: -f1)
    l=$(echo "$hit" | cut -d: -f2)
    prev=$((l - 1))
    allow=$(sed -n "${prev}p" "$f" 2>/dev/null | grep -c "ARCH-ALLOWLIST: vlm-direct-caller" || true)
    if [ "$allow" = "0" ]; then
      echo "$hit"
    fi
  done)
fi
fail_count=0
if [ -n "$filtered_hits" ]; then
  echo "Check 59 (VLM direct-caller ban): FAIL" >&2
  echo "  Direct http.*"/vlm/" callers in application/api without ARCH-ALLOWLIST:" >&2
  echo "$filtered_hits" | sed 's/^/    /' >&2
  echo "  Apply // ARCH-ALLOWLIST: vlm-direct-caller on the line preceding the call." >&2
  fail_count=$((fail_count + 1))
fi
if [ "$fail_count" -gt 0 ]; then
  exit 1
fi
echo "Check 59 (VLM direct-caller ban): OK (0 http.*"/vlm/" hits)"
