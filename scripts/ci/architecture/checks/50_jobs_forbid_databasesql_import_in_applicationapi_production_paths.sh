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
# ── Check 42: forbid `database/sql` import in application/api production paths (P1-8, Wave 19) ──
# AGENTS.md Pattern 0 mandates that `internal/platform/sqlite/**`
# owns SQL; `internal/application/**` and `internal/api/**` consume SQL
# ONLY through typed ports declared in the consumer's `ports.go`.
# Direct `database/sql` import in production app/api code is a layering
# leak — the canonical placement is the typed-port adapter, not the
# consumer's import block. The one legitimate exception is the
# typed-port signature itself (e.g., `*sql.Tx` as a typed-port parameter
# in `internal/application/voiceover/ports.go::TxOutboxEnqueuer`); it
# stays in the allowlist with `never-canonical` deadline so the
# tx-outbox bridge shape survives the ratchet.
#
# Allowlist: `docs/migrations/app-sql-imports-allowlist.txt` lists
# one `<file_path>` per line for the P1-8 (Wave 19) grandfathered
# baseline. Per AGENTS.md §8 ARCHITECTURE-CI-GATES zero-baseline rule,
# every entry MUST carry an inline comment with owner + deadline.
# The inline deadline preamble is stripped here to compare against
# `rg` hits; the comment line stays attached to the entry so the
# zero-baseline rationale is auditable from the file.
#
# Pattern anchor: `^\s*"database/sql"\s*$` — matches the single-line
# Go import of `"database/sql"` exactly. Aliased imports are
# intentionally out of scope; introducing aliases is itself a layering
# indicator that code review should surface, not a CI fast-pass.
#
# Tests are excluded via `--glob '!**/*_test.go'` per the convention
# used by every other architectural check; test fixtures may freely
# construct `sql.Open` for `internal/infrastructure/health/...` smoke tests.
#
# Symmetric compare mirrors Check 19's two-way gate:
#   * violations: production files importing `"database/sql"` NOT in the
#     allowlist → FAIL the gate (regression detected).
#   * stale:     allowlist entries whose file no longer carries the
#     import → FAIL the gate (zombie-prevention — a dead row would
#     silently mask a future regression). Per AGENTS.md 1-PR rule the
#     removal ships in the same PR as the migration that drops the import.
echo "=== Check 42: forbid 'database/sql' import in app/api production paths (P1-8, Wave 19) ==="
allowlist_file="docs/migrations/app-sql-imports-allowlist.txt"
if [ ! -f "${REPO_ROOT}/${allowlist_file}" ]; then
    echo "FAIL: ${allowlist_file} missing — cannot run P1-8 gate"
    echo "      (the gate cannot grandfather without an allowlist file)"
    exit 1
fi

# Collect every production non-test .go file that imports `"database/sql"`
# exactly (the canonical Go import line shape).
actual=$(rg -l --type go \
    -e '^\s*"database/sql"\s*$' \
    --glob '!**/*_test.go' \
    internal/application internal/api 2>/dev/null | sort || true)

# Build sorted allowlist: strip full-line comments + blank lines +
# the trailing inline `# rationale + owner + deadline` part of each
# entry, keeping only the first whitespace-delimited token (= the
# file path).
allowed=$(grep -vE '^[[:space:]]*(#|$)' "${REPO_ROOT}/${allowlist_file}" 2>/dev/null \
          | awk -F'#' '{print $1}' \
          | awk '{print $1}' \
          | grep -v '^$' \
          | sort || true)

# Symmetric Check 42: fail on production hits NOT in allowlist AND on
# stale allowlist entries (mirrors Check 19's two-way gate).
violations=$(comm -13 <(printf '%s\n' "$allowed" | grep .) \
                   <(printf '%s\n' "$actual" | grep .) 2>/dev/null || true)
stale=$(comm -23 <(printf '%s\n' "$allowed" | grep .) \
               <(printf '%s\n' "$actual" | grep .) 2>/dev/null || true)

if [ -n "$violations" ]; then
    echo "FAIL: forbidden 'database/sql' import in production app/api layers (P1-8):"
    echo "$violations"
    echo ""
    echo "Fix: route SQL through a typed port in"
    echo "      internal/application/<consumer>/ports.go with the adapter"
    echo "      in internal/platform/sqlite/<feature>/, wired at"
    echo "      the composition root (internal/app/<feature>_adapters.go)."
    echo ""
    echo "If the import is grandfathered under the Wave 19 P1-8 transitional"
    echo "      baseline, add the file path to ${allowlist_file} with explicit"
    echo "      owner + deadline per AGENTS.md §8 zero-baseline rule."
    exit 1
fi
if [ -n "$stale" ]; then
    echo "FAIL: stale allowlist entry (file no longer imports 'database/sql'):"
    echo "$stale"
    echo ""
    echo "Fix: remove the stale path from ${allowlist_file} IN THE SAME PR"
    echo "      as the migration that drops the import (AGENTS.md 1-PR rule)."
    echo "      Leaving a dead allowlist entry masks future regressions."
    exit 1
fi
actual_count=$(printf '%s\n' "$actual" | grep -c . || true)
allowed_count=$(printf '%s\n' "$allowed" | grep -c . || true)
echo "OK: P1-8 'database/sql' baseline symmetric clean (${actual_count} actual = ${allowed_count} allowlisted; 0 pending migrations)"
# ── Main gate ──────────────────────────────────────────────────────────
# Run the focused+ratchet archcheck; PR-A's `--future-ratchet` keeps the
# 5 Phase 0 rules in grace-cycle regression-detection mode.
go run ./scripts/archcheck --ratchet --future-ratchet

# PR-I (June 2026): promote cmd/archcheck --strict as a blocking CI gate.
# Reads architecture/policy.yaml; --strict turns warn → exit-1 on any
# violation per cmd/archcheck/main.go:204-205. Ratchets #id-20-21:
# duplicate-types-allowlist (Check 5) + max_files_per_package=40
# (pack-size cap). Transitional baseline:
# docs/migrations/archcheck-strict-baseline.json holds any open
# exceptions; fail-closed semantics deadlined entries become hard
# fail (verdict: PR-I implementation in_progress per ADR-0002 §D5).
go run ./cmd/archcheck --strict
# HC-1 (June 2026) deletes the pre-HC-1 package-level `var jobTimeoutRegistry`
# global in internal/application/jobs/worker.go + the `SetJobTimeout` and
# `jobTimeout(` helper callers. Per-job-type timeouts are now keyed through
# `*jobs.Registry.Compose()[j.Type]` (or the typed `JobTimeout()` method)
# via the Worker.WithRegistry(reg) builder attached at composition time.
#
# Pattern anchors (re-introduction patterns we forbid):
#   var jobTimeoutRegistry[[:space:]]*=
#       — package-level map re-emergence with a MapType-typed name
#       (catches `var jobTimeoutRegistry`, `var ( ... jobTimeoutRegistry ...)`).
#   SetJobTimeout\(
#       — exported helper to mutate the map (the pre-HC-1 surface);
#       only worker.go::SetJobTimeout defined this; the alias was removed.
#   ^func jobTimeout\(  (top-level package function)
#   {{:blank:}}jobTimeout\(  (in-function call to package helper)
#       — the lowercase helper that read from the global; renamed to
#       Worker.jobTimeoutFor(t) post-HC-1.
#
# Scope: internal/ + cmd/ (composition root + production callers).
# The canonical site is internal/application/jobs/registry.go (owns the
# TimeoutMap + TimeoutResolver surface); it does NOT contain the
# forbidden patterns. *Registry.Compose() / JobTimeout() are the
# AND ONLY the supported lookup paths.
#
# Negative examples (the patterns being checked for, when invoked
# legitimately as inline fixtures/tests) live in tests/fixtures/zero_legacy/
# — excluded below to mirror Check 36 / Check 39 gating convention.
echo "=== Check 40: HC-1 anti-reintro gate (var jobTimeoutRegistry re-emergence) ==="
hc1_hits=$(rg -n --type go \
    -e 'var[[:space:]]+jobTimeoutRegistry[[:space:]]*=' \
    -e 'SetJobTimeout\(' \
    -e '^func[[:space:]]+jobTimeout\(' \
    -e '\bjobTimeout\(' \
    --glob '!**/*_test.go' \
    --glob '!tests/fixtures/zero_legacy/*' \
    internal cmd 2>/dev/null \
    | awk -F: '{
        rest = ""
        for (i = 3; i <= NF; i++) rest = rest (i > 3 ? ":" : "") $i
        if (rest ~ /^[[:space:]]*\/\//) next   # drop full-line comments
        print
    }' \
    || true)
if [ -n "$hc1_hits" ]; then
    echo "FAIL: HC-1 re-introduction detected (jobTimeoutRegistry global / SetJobTimeout / jobTimeout helper):"
    printf '%s\n' "$hc1_hits" | sed 's/^/  /'
    echo ""
    echo "Fix: per-job-type timeouts MUST be keyed through *jobs.Registry via"
    echo "      Worker.WithRegistry(reg) at composition time. The HC-1 surface:"
    echo "    - registry.Compose()  → TimeoutMap (type-keyed snapshot)"
    echo "    - registry.JobTimeout(t) → typed single-shot lookup (the canonical"
    echo "                              TimeoutResolver method)"
    echo "    - worker.WithRegistry(reg)  → builder attached at composition time"
    echo "      (also snapshots reg.Compose() so runJob's lookup is branch-free)."
    echo ""
    echo "There is NO legitimate use of `var jobTimeoutRegistry ... = ...`, no"
    echo "`SetJobTimeout(t, d)` mutation hook, and no top-level `jobTimeout(t)`"
    echo "helper. Adding any of these requires a godlike/07 EXPAND/BACKFILL/"
    echo "CUTOVER/CONTRACT migration sequence (architecture/deprecations.yaml)"
    echo "and a tracking entry in architecture/current.yaml#HC-1 sub_tasks."
    exit 1
fi
echo "Check 40: 0 HC-1 re-introduction patterns (var jobTimeoutRegistry \/ SetJobTimeout \/ jobTimeout)"

# File and package size are enforced by the canonical cmd/archcheck ratchet.
# Check 43: forbid .DB() chain outside infrastructure (P1.6, June 2026)
bash "${SCRIPT_DIR}/43_db_chain_outside_infra.sh" || { echo "Check 43 (DB chain) failed"; exit 1; }

# Check 45: forbid inline bare map[string]*ClipsRepository{...} literals (Wave 23, action P1-3)
# Companion to Check 8 (S3e) which bans the fully-qualified
# `"map[string]*assets.ClipsRepository{"` shape. Check 45 catches the
# BARE / unqualified variant `"map[string]*ClipsRepository{"` -- a
# likely regression shape if a future contributor imports the canonical
# type without a package alias (or introduces a new unqualified alias).
# Canonical-allowed sites (composition root + canonical registry +
# tests + zero_legacy fixtures) are excluded via rg --glob inside the
# check script.
