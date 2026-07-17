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
# ── Check 54: FASE 3.7 Commit 3 — gate banning infra imports in monitor/ ──
# FASE 3.7 closed the pre-existing infra-import leak in
# internal/application/assets/monitor/ via two consecutive adapter-pattern
# commits (1b for the discoveries+downloader surfaces; 2 for the metrics
# surface). The post-Cleanup state is canonical: monitor/ holds 4 Pattern-0
# ports + NopMetricsRecorder zero-value default; infra access is wired
# exclusively through the composition-root adapter in
# internal/app/lifecycle.go.
#
# Gate clause (godlike/08 zero-baseline rule):
#   monitor/ must NEVER import internal/infrastructure/... in production
#   code. All infra access flows through monitor.{MonitorDownloaderPort,
#   YoutubeDiscoveriesPort, MetricsRecorder, ...} ports + composition-root
#   adapters. The hatchable surface is the
#   `// ARCH-ALLOWLIST: monitor-infra-import` marker (mirrors Check 5/9/11
#   etiquette; per owner + deadline per AGENTS.md §7).
#
# Scope: strictly internal/application/assets/monitor/ ONLY. Widening this
# gate to internal/application/** would over-block legitimate cross-layer
# composition wiring (every other application-layer package legitimately
# consumes infra types via its own composition-root adapter). Mirrors the
# user-spec scope: "questo package strettamente (NON allargare)".
#
# Behaviour (per user spec):
#   - Hard-fail: production import of internal/infrastructure/... not
#     preceded by the ARCH-ALLOWLIST marker in the same file's 25-line
#     scroll window. Exit 1.
#   - Warn (no-fail): comment-only references (descriptive prose) +
#     ARCH-ALLOWLIST marker sites (log + count, do not accumulate to the
#     failing-set; godlike/07 no-fake-availability guarantees the marker
#     sites are observable in CI output every run so future audit-pin
#     regressions surface immediately).
#
# Pattern anchor: any literal occurrence of
# `github.com/Marcuss-ops/PipelineGen/internal/infrastructure` inside the
# monitor/ package (rg output), interpreted as either an import statement,
# a comment reference, or an ARCH-ALLOWLIST marker file.
#
# _test.go INCLUSION RATIONALE (godlike/06 SSOT): unlike Check 0/1/3/5/8/9/
# 11/23 which exclude *_test.go, Check 54 does NOT. Reason: the test layer
# in monitor/ asserts the canonical Pattern-0 surface via compile-time
# `var _ monitor.Port = (*Adapter)(nil)` pins. The Adapter concrete lives
# in infra, so the test file MUST import the infra side to satisfy the
# pin — excluding tests would hide the very class of drift (drift in the
# test-side structural-identity guard) that the gate exists to catch.
# Per godlike/07 zero-baseline rule: the canonical surface for the test
# file to bind is the composition-root adapter (lifecycle.go's adapter),
# not the raw infra package; a legitimate `var _ ...  = (*Adapter)(nil)`
# pin satisfies the canonical SSOT without bypassing the gate.
#
# Marker placement (canonical Go syntax, two acceptable patterns):
#   (a) PREFERRED: marker immediately above the `import (` line:
#         N:   // ARCH-ALLOWLIST: monitor-infra-import
#         N+1: import (
#         N+2:     "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/foo"
#       rg matches line N+2; the awk allows when offending_line == marker+2.
#   (b) ACCEPTABLE: marker immediately above the `import "..."` line
#       (no `import (` block; single-line import):
#         N:   // ARCH-ALLOWLIST: monitor-infra-import
#         N+1: _ "github.com/Marcuss-ops/PipelineGen/internal/infrastructure/foo"
#       rg matches line N+1; the awk allows when offending_line == marker+1.
#   The two patterns are intentionally supported (off-by-one BS-ratchet
#   avoidance per godlike/07); the canonical godlike/06 surface is (a).
echo "=== Check 54: FASE 3.7 Commit 3 — gate banning infra imports in monitor/ ==="
# Two rg calls merged with sort -u: the marker line (// ARCH-ALLOWLIST:...)
# is NOT an infra-path match, so the original single-rg implementation
# never registered the marker and the marker+1/marker+2 logic was dead
# code. The second rg ensures marker lines flow into all_hits so the awk
# can register them, enabling both canonical Go import patterns
# (single-line `import "path"` with marker on previous line, and
# multi-line `import ( / "path"` with marker on or above the `import (`
# line). sort -u handles the same-line case (marker + import on one line).
infra_hits=$(rg -n --type go \
    'github\.com/Marcuss-ops/PipelineGen/internal/infrastructure' \
    internal/application/assets/monitor/ 2>/dev/null \
    || true)
marker_hits=$(rg -n --type go \
    'ARCH-ALLOWLIST:[[:space:]]*monitor-infra-import' \
    internal/application/assets/monitor/ 2>/dev/null \
    || true)
all_hits=$(printf '%s\n%s\n' "$infra_hits" "$marker_hits" | grep -v '^$' | sort -u)
# Stage 1: drop full-line comments + ARCH-ALLOWLIST marker lines + lines
# whose marker site (in the SAME file) is on marker+1 OR marker+2 lines
# upstream of the offending import statement (covers the canonical
# `marker / import ( / "path"` pattern AND the single-line import pattern
# per the canonical Go syntax contract documented above).
literal_calls=$(printf '%s\n' "$all_hits" \
    | awk -F: '
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            # ARCH-ALLOWLIST: monitor-infra-import marker recognised on
            # the candidate line itself. The window is FIXED at zero
            # scroll tolerance (the import statement has a deterministic
            # parser position relative to the marker). Two acceptable
            # offsets are supported: marker+1 (single-line import pattern)
            # and marker+2 (canonical multi-line `import ( / "path"`
            # block pattern). See the bash comment block above for the
            # rationale.
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*monitor-infra-import/) {
                markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2)
                next
            }
            if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
            # Allow if the offending line is marker+1 OR marker+2 lines
            # downstream of a marker site in the SAME file (covers both
            # canonical Go import syntax patterns).
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
# Stage 2: audit-pin residue accounting (godlike/07 honest-limitation
# disclosure). Comment-only hits + ARCH-ALLOWLIST marker hits get logged
# as WARN so future drift is visible in CI output (the canonical
# no-fake-availability auditability requirement). They do NOT contribute
# to the hard-fail set.
comment_count=0
allowlist_count=0
if [ -n "$all_hits" ]; then
    comment_count=$(printf '%s\n' "$all_hits" | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:/) next   # exclude marker lines
        if (rest ~ /^[[:space:]]*\/\//) print
    }' | wc -l | awk '{print $1+0}')
    allowlist_count=$(printf '%s\n' "$all_hits" | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*monitor-infra-import/) print
    }' | wc -l | awk '{print $1+0}')
fi
# Stage 3: hard-fail on production imports. Comment-only matches + ARCH-
# ALLOWLIST sites are warning-only per user spec.
if [ -n "$literal_calls" ]; then
    echo "FAIL: forbidden internal/infrastructure/ import in internal/application/assets/monitor/ (FASE 3.7 Commit 3):"
    echo "$literal_calls"
    echo ""
    echo "Fix: route any infra access through the composition-root adapter in"
    echo "      internal/app/lifecycle.go. The canonical Pattern 0 surface is:"
    echo "      import ( // ARCH-ALLOWLIST: monitor-infra-import)"
    echo "        \"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/...\""
    echo "And the adapter (struct wrap / function-port ctor) lives on the"
    echo "infra side; the monitor-side port (MonitorDownloaderPort /"
    echo "YoutubeDiscoveriesPort / MetricsRecorder / ...) consumes only domain"
    echo "types. Any direct import is a FASE 3.7 commitment regression."
    echo ""
    echo "If the import is genuinely transitional (rare; documented per-file"
    echo "      in the commit body), prepend the magic marker on the line preceding"
    echo "      the import (the import block's opening paren):"
    echo "    // ARCH-ALLOWLIST: monitor-infra-import"
    echo "    import ("
    echo "      \"github.com/Marcuss-ops/PipelineGen/internal/infrastructure/...\""
    echo "    )"
    echo "The marker is stripped from the failing-set automatically; per AGENTS.md"
    echo "§7 every marker entry requires explicit owner + deadline."
    exit 1
fi
if [ "$comment_count" -gt 0 ]; then
    echo "WARN (${comment_count} hits): comment-only internal/infrastructure/ references in monitor/"
    echo "      (descriptive prose; non-fatal per godlike/07 no-fake-availability; counts visible per CI run)"
fi
if [ "$allowlist_count" -gt 0 ]; then
    echo "WARN (${allowlist_count} hits): ARCH-ALLOWLIST: monitor-infra-import sites in monitor/"
    echo "      (each entry requires explicit owner + deadline per AGENTS.md §7; verify currency at promote-to-zero pass)"
fi
echo "OK: 0 hard-fail internal/infrastructure/ imports in monitor/ (FASE 3.7 Commit 3 invariants upheld)"
