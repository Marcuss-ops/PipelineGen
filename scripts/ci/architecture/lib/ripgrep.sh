#!/usr/bin/env bash
# lib/ripgrep.sh — ripgrep wrappers for the ci/architecture suite.
#
# Centralises the awk pre-pass that all marker-using checks need:
#   - full-line comment stripping
#   - 25-line ARCH-ALLOWLIST marker-window tolerance
# The legacy scripts/ci-architectural-checks.sh hardcoded each marker's
# regexp into Check 5 / 8 / 10b / 33 — this helper takes the marker
# token at runtime so the four checks share the same logic without
# duplicating the awk boilerplate.
#
# Sourced by checks/*.sh — does NOT execute `set -e`. Each helper
# ALWAYS returns exit code 0 so callers with `set -e` are not tripped
# by rg's "no matches" exit 1.

# rg_n <pattern> [rg_args...]
# Plain rg -n wrapper. Output goes to stdout; helper exits 0 always.
rg_n() {
    { rg -n "$@" 2>/dev/null; } || true
}

# rg_strip_full_line_comments <pattern> [rg_args...]
# rg plus awk that drops any hit whose content line starts with "//"
# (a Go comment). Used by Checks 0, 1, 2, 3, 10, 11, etc. that don't
# need marker-window tolerance.
rg_strip_full_line_comments() {
    {
        rg -n "$@" 2>/dev/null \
            | awk -F: '
                {
                    rest = ""
                    for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
                    if (rest ~ /^[[:space:]]*\/\//) next
                    print
                }'
    } || true
}

# rg_window_allowlist <pattern> <marker> [rg_args...]
# rg plus awk pre-pass that:
#   (a) collects marker lines ("ARCH-ALLOWLIST: <marker>") with their
#       file + line number;
#   (b) strips the marker lines themselves from the output;
#   (c) strips full-line comment lines;
#   (d) drops any hit whose line number is within `marker_line+1..
#       marker_line+25` of any marker in the SAME file (per-grep line
#       grouping), tolerating the canonical `marker\n\n<call site>`
#       pattern with a 25-line scroll-window.
#
# The marker's <marker> arg is the EXACT token to match (e.g. admin-only
# for Check 5, factory-only for Check 8 factory-only form, clips-ssot-only
# for Check 10b, retention-created-at-mutable for Check 33).
#
# Notes for callers:
#   - The pattern positional arg is the LAST argument rg sees (rg -n
#     "$@" pattern passes paths/globs THEN the pattern).
#   - rg's exit 1 on "no matches" is harmless: the inner { ... } || true
#     wrapper guarantees the function returns 0 always.
rg_window_allowlist() {
    local pattern="${1}"
    local marker="${2}"
    shift 2
    {
        rg -n "$@" "${pattern}" 2>/dev/null \
            | awk -F: -v marker="${marker}" '
                {
                    rest = ""
                    for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
                    if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*[A-Za-z0-9_-]+/) {
                        after_marker = rest
                        sub(/^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*/, "", after_marker)
                        sub(/[[:space:]].*$/, "", after_marker)
                        if (after_marker == marker) {
                            markers[$1] = (markers[$1] == "" ? $2 : markers[$1] "," $2)
                            next
                        }
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
                }'
    } || true
}
