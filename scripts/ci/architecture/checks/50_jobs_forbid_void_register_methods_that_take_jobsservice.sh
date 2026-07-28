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
# ── Check 50: forbid void Register* methods that take jobs.Service (P1 #1, July 2026) ──
# Audit P1 #1 closed the silent-success class on every JobHandler.Register*
# style method: nil-typed-dispatcher + duplicate-bind failures now surface as
# typed errors (wrapped appjobs.ErrMissingDeps via %w) and the composition
# root fails-closed on non-nil return. This CI gate is the forward-prevention
# rule that locks the typed-error contract at compile time so a future
# contributor cannot reintroduce a `void` Register* method that would
# silently drop the bind failure (the pre-P1 #1 audit-closed failure class).
#
# Pattern anchor (ripgrep multi-line via -U flag):
#   `func (\w+ \*?\w+) Register\([^)]*jobs\.?Service[^)]*\)\s*\{`
# i.e. closing paren of the arg list is followed ONLY by whitespace + `{`.
# If the return type `error` is between `)` and `{` (e.g.
# `) error {`), the regex `\)\s*\{` does NOT match because the literal
# `error` text breaks the `\s*\{` binding.
#
# Scope:
#   - All `func (h *X) Register(... *jobs.Service ...)` methods in
#     internal/application/** and internal/infrastructure/**/*. The match
#     is permissive (catches `jobs.Service`, `appjobs.Service`,
#     `jobtools.Service`, and the canonical alias `*jobs.Service`).
#   - Tests (`*_test.go`) excluded so test fixtures may freely construct
#     mocks with void signatures.
#
# Allowlist (production sites that CAN keep their existing shape):
#   - internal/api/assets/clips/handler.go::(*Handler).RegisterJobHandlers()
#     — takes NO jobs.Service argument (the receiver reads h.jobsSvc);
#     P1 #1 contract is "Register method that takes jobsSvc must return
#     error" — this method's signature doesn't match the pattern so the
#     regex skips it cleanly. (See clips.ports.go::HTTPHandlerPort for the
#     canonical interface declaration with `error` return — that's the
#     allowlisted surface.)
#
# ARCH-ALLOWLIST opt-in (mirrors Check 5 / 10b / 11): a transitional
# backfill that legitimately needs a void Register* signature (e.g. a
# one-shot operator CLI) MUST prepend the magic marker
# `// ARCH-ALLOWLIST: register-void-allowed` on the line preceding the
# function definition. The awk pre-pass strips such hits from the
# failing-set via the same 25-line window tolerated by Check 5 / 10b.
# Per AGENTS.md §8 zero-baseline rule, new allowlist entries require
# explicit owner + deadline; the marker is the call-site equivalent
# of an allowlist row.
#
# Per godlike/08 ARCHITECTURE-CI-GATES zero-baseline rule: any new
# failure on this gate is a guaranteed binding contract regression;
# the production handlers refactored in commit `refactor(jobs): make
# JobHandler.Register fail-fast (audit P1 #1)` already saturate the
# surface (10 handlers, all returning `error`).
echo "=== Check 50: forbid void Register* methods that take jobs.Service (P1 #1, July 2026) ==="
# P1 #1 fixup: the previous version used `[ \t]*\{` between `)` and `{`
# which only matches horizontal whitespace, allowing a multi-line signature
# like `func (h *X) Register(\n svc *jobs.Service,\n) { ... }` to slip
# through as not-a-void-trigger. The tightened pattern uses `\s*` which
# ripgrep's default regex semantics DO treat as multi-line whitespace.
# Single-line signatures still match (`\s` includes space + tab + newline).
#
# Pattern anchor: `func (h *X) Register(svc *jobs.Service) {` — the closing
# paren of the arg list is followed ONLY by whitespace + `{` (NO `error`
# type token between `)` and `{`). A typed-error return like
# `func (h *X) Register(svc *jobs.Service) error {` does NOT match because
# the literal `error` text breaks the `\s*\{` binding.
all_void_registers=$(rg -nU --type go \
    -e 'func\s+(\(\w+\s+\*?\w+\)\s+)?[A-Z][A-Za-z0-9_]*[Rr]egister\([^)]*\bjobs\.?Service[^)]*\)\s*\{' \
    --glob '!**/*_test.go' \
    internal/application internal/infrastructure 2>/dev/null \
    || true)
# Drop lines preceded by the ARCH-ALLOWLIST marker (25-line window).
literal_void_registers=$(printf '%s\n' "$all_void_registers" \
    | awk -F: '
        BEGIN { prev_marker = 0 }
        {
            rest = ""
            for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
            if (rest ~ /^[[:space:]]*\/\/.*ARCH-ALLOWLIST:[[:space:]]*register-void-allowed/) {
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
if [ -n "$literal_void_registers" ]; then
    echo "FAIL: void Register* signature detected that takes a jobs.Service arg:"
    echo "$literal_void_registers"
    echo ""
    echo "Fix: change the Register* method signature to return error and wrap"
    echo "      the nil-dispatcher + duplicate-bind cases with"
    echo "      fmt.Errorf(\"<handler>.Register: <diagnostic>: %w\", ErrMissingDeps)"
    echo "      so the composition root fails-closed on non-nil return."
    echo "      The ErrMissingDeps sentinel lives in"
    echo "      internal/application/jobs/errors.go and is the typed-error"
    echo "      contract that tests assert via errors.Is(err, appjobs.ErrMissingDeps)."
    echo ""
    echo "If the void shape is genuinely transitional (rare; e.g. a one-shot"
    echo "      operator CLI), prepend the magic marker on the line preceding"
    echo "      the function definition:"
    echo "    // ARCH-ALLOWLIST: register-void-allowed"
    echo "    func (h *X) Register(svc *jobs.Service) { ... }"
    exit 1
fi
echo "OK: every Register* method that takes jobs.Service returns error (P1 #1 contract)"
