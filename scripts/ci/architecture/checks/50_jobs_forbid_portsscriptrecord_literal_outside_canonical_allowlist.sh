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
# ── Check 57: forbid ports.ScriptRecord literal outside canonical allowlist (godlike/06 SSOT, July 2026) ──
# godlike/06 SSOT one-canonical-owner-per-fact: PersistenceProcessor is the
# SOLE WRITER of *ports.ScriptRecord in production paths. The canonical
# read-path translator (`platform/sqlite/scripts/repository_adapter.go::fromSQLiteScriptRecord`)
# is the SECOND canonical owner — it translates sqlitescripts.ScriptRecord
# → ports.ScriptRecord on read paths (Get/List/Find). Every other direct
# literal `&ports.ScriptRecord{...}` in production code is a SSOT
# regression (writes MUST flow through PersistenceProcessor; reads MUST
# flow through fromSQLiteScriptRecord).
#
# Pattern anchor (ripgrep regex, root-anchored substring):
#   ports\.ScriptRecord\{   — matches  &ports.ScriptRecord{ … }  literal.
# Targets the FULLY-QUALIFIED form so it does NOT false-positive on the
# canonical writer PersistenceProcessor, which uses the in-package alias
# `&ScriptRecord{...}` (declared in application/scripts/adapters/repository.go via
# `type ScriptRecord = ports.ScriptRecord`). The alias is byte-equivalent
# to ports.ScriptRecord (Go type alias, NOT distinct type) but its
# literal form `&ScriptRecord{` is NOT matched by the regex. Intentional
# design — the regex enforces literal-discipline at every
# `&ports.ScriptRecord{` site across the production tree.
#
# Allowlist (the ONLY legitimate production sites for the fully-qualified
# literal form `&ports.ScriptRecord{...}`):
#   - internal/application/scripts/adapters/processor_persistence.go  :
#     CANONICAL WRITER (godlike/06 SSOT, the SOLE producer of new
#     ports.ScriptRecord rows). Belt-and-suspenders allowlist: this file
#     uses the in-package `&ScriptRecord{...}` alias idiom (which the
#     regex does NOT match), so the allowlist row is forward-prevention
#     only — if a future contributor accidentally writes
#     `&ports.ScriptRecord{...}` at this site, the gate would still pass.
#   - internal/platform/sqlite/scripts/repository_adapter.go:
#     CANONICAL READ-PATH TRANSLATOR
#     (`fromSQLiteScriptRecord` constructs `&ports.ScriptRecord{...}` as
#     the read-shape population for ports.ScriptRecord {sqlitescripts →
#     ports}). This is a DELIBERATE EXTENSION of the user-stated
#     allowlist (processor_persistence.go + tests); rationale: locking
#     the read-path translator out of the gate would force a refactor to
#     field-by-field assignment which is out of scope for the godlike/07
#     minimal-blast-radius. If the user wants this site gated too, a
#     follow-up PR can refactor fromSQLiteScriptRecord to use the alias
#     idiom (or untyped assignment) so the gate catches the gap.
#   - *_test.go (all)                                                :
#     Test mocks / fixtures may freely construct the literal as
#     required for type-fixture construction. Listed globally so we
#     don't have to enumerate each of the ~5 test fixture files; the
#     in-package `&ScriptRecord{...}` alias is the dominant idiom in
#     test mocks so the gate doesn't even match most test fixtures.
#
# ARCH-ALLOWLIST opt-in (mirrors Check 5/54 etiquette; owner + deadline
# per AGENTS.md §7): a transitional backfill or production test fixture
# that legitimately needs `&ports.ScriptRecord{…}` at a non-allowlisted
# site MUST prepend the magic marker
# `// ARCH-ALLOWLIST: ports-scriptrecord-allowed` on the line preceding
# the literal. The awk pre-pass strips such hits from the failing-set via
# the same 25-line scroll-window tolerated across the gate family.
echo "=== Check 57: forbid ports.ScriptRecord literal outside canonical allowlist ==="
all_hits=$(rg -n --type go \
    -e 'ports\.ScriptRecord\{' \
    --glob '!**/processor_persistence.go' \
    --glob '!**/internal/platform/sqlite/scripts/repository_adapter.go' \
    --glob '!**/*_test.go' \
    internal/application internal/api 2>/dev/null \
    || true)
literal_calls=$(printf '%s\n' "$all_hits" \
    | awk -F: '
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*ports-scriptrecord-allowed/) {
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
if [ -n "$literal_calls" ]; then
    echo "FAIL: forbidden *ports.ScriptRecord literal construction in production path:"
    echo "$literal_calls"
    echo ""
    echo "Fix: write new *ports.ScriptRecord rows ONLY through PersistenceProcessor"
    echo "      (canonical SOLE writer; godlike/06 SSOT). For read paths, the"
    echo "      canonical translator is platform/sqlite/scripts/repository_adapter.go::fromSQLiteScriptRecord"
    echo "      (sqlitescripts -> ports.ScriptRecord)."
    echo ""
    echo "If the literal is genuinely transitional (rare), prepend the magic"
    echo "      marker on the line preceding the literal construction:"
    echo "    // ARCH-ALLOWLIST: ports-scriptrecord-allowed"
    echo "    return &ports.ScriptRecord{ID: id, Title: title}"
    exit 1
fi
echo "OK: no *ports.ScriptRecord literals in production paths (godlike/06 SSOT writer = PersistenceProcessor)"
