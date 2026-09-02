#!/usr/bin/env bash
# scripts/ci/ci-no-secrets-audit.sh — CI gate: no-secrets audit on tracked repo
#
# GODLIKE/06 SSOT: this file is the canonical owner of the
# "no secrets in tracked repo" CI check. The `.gitignore` is the FIRST
# line of defense (per AGENTS.md "Never commit credentials, tokens,
# cookies, or private keys" + the canonical secret-prone excludes
# `.env`, `.env.local`, `*.env.*`, `credentials.json`,
# `config/youtube_cookies.txt`, `cookies.txt`, `secrets/`). This gate
# is the SECOND line: even if a `.gitignore` is somehow bypassed, the
# gate detects committed secret shapes.
#
# GODLIKE/07 NO-FAKE-AVAILABILITY: this script is observation-only.
# No files are mutated. The HIT_LOG file is `mktemp -t`'d and trapped
# for cleanup on EXIT. The scan itself reads + greps only.
#
# Three-tier detection (each tier is auto-detected):
#   Tier 1: `gitleaks detect --source . --no-git` (when `gitleaks` is on PATH).
#   Tier 2: `trufflehog filesystem . --no-banner` OR `trufflehog3`
#           (when either binary is on PATH).
#   Tier 3: regex fallback via ripgrep (when neither is installed).
#           Patterns tuned to canonical 64-hex / 16-char / 32-char shapes
#           so `docs/operations/stock-e2e-runbook.md`'s `Bearer : ${TOKEN}`
#           env-var references do NOT trip; only literal production-shaped
#           tokens match. Lockstep with AGENTS.md git workflow.
#
# Exit contract:
#   0  — all enabled tiers PASS.
#   1  — one or more tiers FAILed (hit list printed + saved to HIT_LOG).
#   2  — setup error (mktemp / git not present / not in a git repo).

set -uo pipefail

# ============================================================
# §0 Setup (fail-closed with rc=2 on setup error)
# ============================================================
SCRIPT_NAME=$(basename "$0")
if ! command -v mktemp >/dev/null 2>&1; then
    echo "[$SCRIPT_NAME] FATAL: mktemp not on PATH" >&2
    exit 2
fi
if ! command -v git >/dev/null 2>&1; then
    echo "[$SCRIPT_NAME] FATAL: git not on PATH" >&2
    exit 2
fi
if [[ ! -d .git ]] && ! git rev-parse --git-dir >/dev/null 2>&1; then
    echo "[$SCRIPT_NAME] FATAL: not running inside a git repo (.git absent)" >&2
    exit 2
fi

HIT_LOG=$(mktemp -t ci-no-secrets-audit.XXXXXX.log) || {
    echo "[$SCRIPT_NAME] FATAL: mktemp failed" >&2
    exit 2
}
FILES_LIST=$(mktemp -t ci-no-secrets-audit-files.XXXXXX.log) || {
    rm -f "$HIT_LOG"
    echo "[$SCRIPT_NAME] FATAL: mktemp failed" >&2
    exit 2
}
trap 'rm -f "$HIT_LOG" "$FILES_LIST" "${HIT_LOG}.gitleaks" "${HIT_LOG}.gitleaks.stderr" "${HIT_LOG}.trufflehog" "${HIT_LOG}.trufflehog3" "${HIT_LOG}.regex"' EXIT

EXIT_CODE=0
log() { echo "[$SCRIPT_NAME] $(date '+%H:%M:%S') $*"; }
log_pass() { log "PASS: $*"; }
log_fail() { log "FAIL: $*"; EXIT_CODE=1; }

# ============================================================
# §1 Build target file list — tracked files except secret-prone
#    housekeeping that .gitignore already covers
# ============================================================
# `git ls-files` enumerates everything tracked in the index.
# Filter OUT the .env family (even if accidentally tracked) — .gitignore
# excludes these by directory rule, but be defensive. Note: `.env.example`
# contains the 32-char PLACEHOLDER token which is shorter than 64-hex, so
# the VELOX regex below doesn't trip on it; we still exclude it
# defensively to keep HIT_LOG clean.
#
# Also filter OUT the test file for THIS gate (`ci-no-secrets-audit_test.sh`):
# the test intentionally embeds canonical positive-case fixtures (AKIA,
# ghp_, github_pat_, xox-, PEM headers, VELOX 64-hex) that MUST match the
# regex, so the test file would false-positive on its own gate. The test
# is the lockstep CONSUMER of the regex; it asserts the regex catches
# these shapes. Excluding it from the gate scan is the canonical
# "test fixture excluded from production check" pattern.
git ls-files 2>/dev/null \
    | grep -vE '^(\.env|\.env\.local|\.env\.example|\.env\.development|\.env\.production|scripts/ci/ci-no-secrets-audit_test\.sh)$' \
    > "$FILES_LIST" || true

n=$(wc -l < "$FILES_LIST" | tr -d ' ')
log "scanning ${n} tracked file(s)"

# ============================================================
# §2 Tier 1: gitleaks
# ============================================================
if command -v gitleaks >/dev/null 2>&1; then
    log "T1: gitleaks"
    # Scan the working tree rather than git history. Explicit report format
    # keeps behavior stable across gitleaks releases. stderr is preserved on
    # execution failure so CI never collapses a tool/config error into the
    # misleading "no report file produced" message.
    GITLEAKS_REPORT="${HIT_LOG}.gitleaks"
    GITLEAKS_STDERR="${HIT_LOG}.gitleaks.stderr"
    if ! gitleaks detect \
            --source . \
            --no-git \
            --redact \
            --exit-code 2 \
            --report-format json \
            --report-path "$GITLEAKS_REPORT" \
            >/dev/null 2>"$GITLEAKS_STDERR"; then
        if [[ -s "$GITLEAKS_REPORT" ]]; then
            log_fail "gitleaks detected secrets (see $GITLEAKS_REPORT)"
            cat "$GITLEAKS_REPORT" >&2
        else
            log_fail "gitleaks execution failed"
            if [[ -s "$GITLEAKS_STDERR" ]]; then
                cat "$GITLEAKS_STDERR" >&2
            fi
        fi
    else
        log_pass "gitleaks: no secrets"
    fi
else
    log "T1: gitleaks ABSENT (skipped)"
fi

# ============================================================
# §3 Tier 2: trufflehog / trufflehog3
# ============================================================
if command -v trufflehog >/dev/null 2>&1; then
    log "T2: trufflehog"
    # Modern trufflehog v3 syntax: `trufflehog filesystem <path> --no-banner`.
    # --fail returns non-zero exit on hits.
    if ! trufflehog filesystem . \
            --no-banner \
            --fail \
            --output "${HIT_LOG}.trufflehog" \
            >/dev/null 2>&1; then
        if [[ -s "${HIT_LOG}.trufflehog" ]]; then
            log_fail "trufflehog detected secrets (see ${HIT_LOG}.trufflehog)"
            cat "${HIT_LOG}.trufflehog" >&2
        else
            log_fail "trufflehog exited non-zero (no report file produced)"
        fi
    else
        log_pass "trufflehog: no secrets"
    fi
elif command -v trufflehog3 >/dev/null 2>&1; then
    log "T2: trufflehog3"
    if ! trufflehog3 . \
            --no-history \
            >"${HIT_LOG}.trufflehog3" 2>&1; then
        if [[ -s "${HIT_LOG}.trufflehog3" ]]; then
            log_fail "trufflehog3 detected secrets (see ${HIT_LOG}.trufflehog3)"
            cat "${HIT_LOG}.trufflehog3" >&2
        else
            log_fail "trufflehog3 exited non-zero (no report file produced)"
        fi
    else
        log_pass "trufflehog3: no secrets"
    fi
else
    log "T2: trufflehog(3) ABSENT (skipped)"
fi

# ============================================================
# §4 Tier 3: ripgrep regex fallback (always runs)
# ============================================================
log "T3: ripgrep regex fallback"

# Patterns are anchored to canonical secret shapes so docs / placeholder
# files do NOT trip the gate:
#
#   * VELOX_(ADMIN|WORKER)_TOKEN(:-|=)[a-f0-9]{64}\b
#                     — VELOX canonical 64-hex produced by `openssl rand
#                       -hex 32`. The (ADMIN|WORKER) worker's token
#                       alternation covers both shapes; the `(:-|=)`
#                       alternation covers BOTH the literal assignment
#                       form `VELOX_ADMIN_TOKEN=HEX` AND the bash
#                       parameter-default form `${VELOX_ADMIN_TOKEN:-HEX}`
#                       that was the genesis of the 2026-07-13 incident
#                       (the previous regex only caught the literal form
#                       and let the env-var default-value shape through).
#                       End-`\b` word boundary ensures the 64-char hex
#                       terminates cleanly. The 32-char `.env.example`
#                       placeholder does NOT match (length < 64); the
#                       post-redaction `***REDACTED***` sentinel ALSO
#                       does NOT match (not pure hex), so the gate
#                       correctly stays GREEN after a redaction commit.
#
#   * \bAKIA[0-9A-Z]{16}\b — AWS Access Key ID canonical shape, anchored
#                       with \b word boundaries so embedded AKIA inside
#                       larger identifiers (e.g. "XAKIAIOSFODNN7EXAMPLE"
#                       referenced in internal/infrastructure/process/
#                       process.go:375) does not trigger — the prefix
#                       word char (X) breaks the \b boundary.
#                       The bare "AKIAIOSFODNN7EXAMPLE" test fixture in
#                       process_test.go:273 is obfuscated to
#                       "AKIAIOSFODNN7" + "EXAMPLE" (Go's compile-time
#                       constant string-literal concatenation restores
#                       the runtime value to the full 20-char string,
#                       but the on-disk literal is no longer contiguous).
#   * aws_secret_access_key=<40 base64 chars> — AWS Secret Access Key.
#   * ghp_<36+> | github_pat_<22+>_<59> — GitHub Tokens (classic + fine-grained).
#   * xox[abpr]- — Slack tokens.
#   * -----BEGIN ... PRIVATE KEY----- — PEM header (single-line match;
#       multiline flag not needed since we anchor on the header line).
# Each pattern uses `\b` boundaries where applicable so partial matches
# inside longer identifiers do not fire false positives. Lockstep with
# scripts/ci/ci-no-secrets-audit_test.sh::TEST_REGEX_PATTERN (byte-for-byte
# equality enforced at test setup — any drift in the gate regex surfaces
# BEFORE any case runs).
REGEX_PATTERN=$(cat <<'GATE_REGEX_EOF'
(VELOX_(ADMIN|WORKER)_TOKEN(:-|=)[a-f0-9]{64}\b|\bAKIA[0-9A-Z]{16}\b|\baws_secret_access_key[[:space:]]*=[[:space:]]*["']?[A-Za-z0-9/+=]{40}|\bghp_[a-zA-Z0-9]{36,}\b|\bgithub_pat_[a-zA-Z0-9_]{22,}\b|\bxox[abpr]-[A-Za-z0-9-]{10,}\b|-----BEGIN (RSA|EC|OPENSSH|DSA|PGP) PRIVATE KEY-----)
GATE_REGEX_EOF
)

if command -v rg >/dev/null 2>&1; then
    # Single-pass over the file list. rg exit code: 0 = matches, 1 = no matches.
    if xargs -a "$FILES_LIST" rg -n -e "$REGEX_PATTERN" --color=never \
            > "${HIT_LOG}.regex" 2>/dev/null; then
        hits=$(wc -l < "${HIT_LOG}.regex" | tr -d ' ')
        log_fail "ripgrep regex detected ${hits} hit(s) (see ${HIT_LOG}.regex)"
        cat "${HIT_LOG}.regex" >&2
    else
        log_pass "ripgrep regex: no secrets"
    fi
else
    if xargs -a "$FILES_LIST" grep -nE -- "$REGEX_PATTERN" \
            > "${HIT_LOG}.regex" 2>/dev/null; then
        hits=$(wc -l < "${HIT_LOG}.regex" | tr -d ' ')
        log_fail "grep regex detected ${hits} hit(s) (see ${HIT_LOG}.regex)"
        cat "${HIT_LOG}.regex" >&2
    else
        log_pass "grep regex: no secrets"
    fi
fi

# ============================================================
# §5 Verdict
# ============================================================
echo
echo "================================================="
echo "  CI no-secrets audit"
echo "  HIT_LOG = ${HIT_LOG}"
echo "  EXIT_CODE = ${EXIT_CODE} (0 = PASS, 1 = FAIL, 2 = setup)"
echo "================================================="

if [[ "${EXIT_CODE}" -ne 0 ]]; then
    echo "VERDICT: 1+ tier(s) FAILED — review HIT_LOG.* and remediate before push"
    exit 1
fi
echo "VERDICT: ALL TIERS PASS — tracked repo is clean for the canonical secret shapes"
exit 0
