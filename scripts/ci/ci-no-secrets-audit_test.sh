#!/usr/bin/env bash
# scripts/ci/ci-no-secrets-audit_test.sh — lockstep test for the §4
# REGEX_PATTERN in scripts/ci/ci-no-secrets-audit.sh.
#
# GODLIKE/06 SSOT: this test is a CONSUMER of the gate's regex. The
# gate is the canonical owner; the test verifies the regex catches
# canonical positive cases (must match) AND lets known false-positive
# shapes through (must NOT match).
#
# GODLIKE/07 NO-FAKE-AVAILABILITY: this test is observation-only. No
# files are mutated. The temp fixtures are `mktemp -t`'d and trapped
# for cleanup on EXIT. Fail-loud: any case failure exits 1; any setup
# error (gate file missing, regex extraction failed, lockstep drift)
# exits 2 — so a downstream operator can distinguish "test case is
# broken" from "regex drift in the gate".
#
# Lockstep check (simplified design): the regex is HARDCODED here
# (TEST_REGEX_PATTERN) instead of extracted at runtime. The lockstep
# check then verifies byte-for-byte equality with the gate's regex at
# SETUP time, so any drift in the gate's regex surfaces as exit 2
# BEFORE any case runs. This avoids the grep/sed extraction
# complexity in the hot path (no unquoted-expansion bugs, no
# shell-quoting hazards) while still catching drift. The hardcoded
# pattern is self-documenting (the test cases line up with the regex
# anchors) and the lockstep is a one-liner comparison.
#
# Exit contract:
#   0  — all 10 positives match, all 6 negatives don't match, both
#        code lockstep checks pass.
#   1  — at least one test case failed (printed with context).
#   2  — setup error: gate file not found, regex extraction failed,
#        or TEST_REGEX_PATTERN drift vs gate (lockstep violation).
#
# Usage:
#   bash scripts/ci/ci-no-secrets-audit_test.sh            # full output
#   bash scripts/ci/ci-no-secrets-audit_test.sh --quiet    # failures + verdict only
#   bash scripts/ci/ci-no-secrets-audit_test.sh --help     # usage

set -u

SCRIPT_NAME=$(basename "$0")
GATE_FILE="scripts/ci/ci-no-secrets-audit.sh"

# ── Args ────────────────────────────────────────────────────────────
QUIET=0
for arg in "$@"; do
    case "$arg" in
        --quiet|-q) QUIET=1 ;;
        --help|-h)
            cat <<USAGE
Usage: $SCRIPT_NAME [--quiet] [--help]

Lockstep test for scripts/ci/ci-no-secrets-audit.sh §4 REGEX_PATTERN.

  --quiet, -q   Only print failures + final verdict.
  --help,  -h   Show this help.

Exit codes:
  0  all checks pass
  1  at least one test case failed
  2  setup error (gate missing, regex drift)
USAGE
            exit 0
            ;;
        *)
            echo "[$SCRIPT_NAME] FATAL: unknown arg: $arg" >&2
            exit 2
            ;;
    esac
done

log()  { echo "[$SCRIPT_NAME] $*"; }
qlog() { [[ $QUIET -eq 0 ]] && log "$@"; return 0; }

# ── Setup: hardcoded TEST_REGEX_PATTERN + lockstep check vs gate ──
# Here-doc with single-quoted delimiter avoids bash interpretation
# entirely (no shell-quoting hazards with the nested ["'] class or
# the [[:space:]] POSIX classes). The hardcoded pattern MUST match
# the gate's REGEX_PATTERN byte-for-byte; the lockstep check below
# enforces that.
TEST_REGEX_PATTERN=$(cat <<'EOF'
(VELOX_(ADMIN|WORKER)_TOKEN(:-|=)[a-f0-9]{64}\b|\bAKIA[0-9A-Z]{16}\b|\baws_secret_access_key[[:space:]]*=[[:space:]]*["']?[A-Za-z0-9/+=]{40}|\bghp_[a-zA-Z0-9]{36,}\b|\bgithub_pat_[a-zA-Z0-9_]{22,}\b|\bxox[abpr]-[A-Za-z0-9-]{10,}\b|-----BEGIN (RSA|EC|OPENSSH|DSA|PGP) PRIVATE KEY-----)
EOF
)

# Locate the gate file. Fail-closed with rc=2 if it's not where we
# expect (suggests the test was moved without updating the gate
# pairing, or the gate was deleted).
if [[ ! -f "$GATE_FILE" ]]; then
    echo "[$SCRIPT_NAME] FATAL: gate file not found at $GATE_FILE" >&2
    exit 2
fi

# Extract the gate's REGEX_PATTERN. The gate defines it on a single
# line as REGEX_PATTERN='...' (single-quoted). Strip the assignment
# prefix and trailing single quote.
# Extract the gate's REGEX_PATTERN from the here-doc block. The gate
# defines it as:
#   REGEX_PATTERN=$(cat <<'GATE_REGEX_EOF'
#   <pattern>
#   GATE_REGEX_EOF
#   )
# We use awk to extract the content between the markers (the literal
# regex string, with no bash quoting applied). This avoids the
# `"'"'"'` quoting hazard and ensures byte-for-byte equality with the
# test's hardcoded TEST_REGEX_PATTERN.
GATE_REGEX_PATTERN="$(awk '/^REGEX_PATTERN=\$\(cat <<.GATE_REGEX_EOF.$/{flag=1; next} /^GATE_REGEX_EOF$/{flag=0; next} flag' "$GATE_FILE")"
if [[ -z "$GATE_REGEX_PATTERN" ]]; then
    echo "[$SCRIPT_NAME] FATAL: could not extract REGEX_PATTERN from $GATE_FILE" >&2
    exit 2
fi

# Sanity: both patterns must contain the canonical anchors (AKIA +
# VELOX_). Catches operator comment-out / accidental rewrite that
# would otherwise pass the byte-for-byte check.
# NOTE: the regex uses `VELOX_(ADMIN|WORKER)_TOKEN` (with parens around
# the alternation), so the literal substring `VELOX_ADMIN_TOKEN` does
# NOT appear verbatim — we anchor on `VELOX_` instead, which IS
# present as a literal substring in both `VELOX_(ADMIN|WORKER)_TOKEN`
# and `VELOX_ADMIN_TOKEN` (legacy regex shape).
if ! [[ "$GATE_REGEX_PATTERN" == *AKIA* ]] || ! [[ "$GATE_REGEX_PATTERN" == *VELOX_* ]]; then
    echo "[$SCRIPT_NAME] FATAL: gate REGEX_PATTERN is missing canonical anchors (AKIA / VELOX_)" >&2
    echo "  GATE: $GATE_REGEX_PATTERN" >&2
    exit 2
fi

# THE LOCKSTEP CHECK. Byte-for-byte equality. If the gate's regex
# drifts (someone edits the gate and forgets to update the test),
# the test fails loud BEFORE any case runs, with a clear diff.
if [[ "$TEST_REGEX_PATTERN" != "$GATE_REGEX_PATTERN" ]]; then
    echo "[$SCRIPT_NAME] FATAL: TEST_REGEX_PATTERN drift vs gate (lockstep violation)" >&2
    echo "  TEST (hardcoded): $TEST_REGEX_PATTERN" >&2
    echo "  GATE (extracted): $GATE_REGEX_PATTERN" >&2
    exit 2
fi

qlog "lockstep OK: TEST_REGEX_PATTERN == GATE_REGEX_PATTERN"
qlog "regex: $TEST_REGEX_PATTERN"
echo

# ── Test runner ─────────────────────────────────────────────────────
# `matches` returns 0 (true) if the regex matches the input,
# 1 (false) otherwise. Uses `rg -e` (NOT `rg -E` which is
# `--encoding` in modern ripgrep — a real gotcha that surfaced
# during the prior runtime-extraction design). Falls back to
# `grep -E` if rg is absent.
matches() {
    local input="$1"
    if command -v rg >/dev/null 2>&1; then
        echo "$input" | rg -e "$TEST_REGEX_PATTERN" --color=never >/dev/null 2>&1
    else
        echo "$input" | grep -E -- "$TEST_REGEX_PATTERN" >/dev/null 2>&1
    fi
}

# ── Positive cases (MUST match) ─────────────────────────────────────
# 10 canonical secret shapes. Each MUST trigger the gate's regex.
# Token fixtures use "TEST" prefixes where the regex allows so that
# GitHub push protection does not false-positive on the test file
# itself (defense-in-depth; the test file is also added to the gate's
# exclusion list, but the prefix is belt-and-suspenders).
# Format: "description|input"
POSITIVE_CASES=(
    'VELOX 64-hex admin token (literal assignment)|VELOX_ADMIN_TOKEN=abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789'
    'VELOX 64-hex admin token (bash env-var default-value form; the 2026-07-13 leak shape)|TOKEN="${VELOX_ADMIN_TOKEN:-abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789}"'
    'VELOX 64-hex worker token (literal assignment; canonical alternate to admin)|VELOX_WORKER_TOKEN=abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789'
    'VELOX 64-hex worker token (env-var default-value form)|SERVICE="${VELOX_WORKER_TOKEN:-abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789}"'
    'AWS access key ID (AKIA + 16 uppercase alnum; TEST-prefixed)|AKIATESTSTESTSTEST12'
    'GitHub classic PAT (ghp_ + 36+ alnum; TEST-prefixed)|ghp_TESTabcdefghijklmnopqrstuvwxyz0123456789'
    'GitHub fine-grained PAT (github_pat_ + 22+ alnum/_; TEST-prefixed)|github_pat_11TESTABCDEF0_abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZab'
    'Slack bot token (xoxb- + 10+ alnum/-; TEST-prefixed)|xoxb-TEST-1234567890-abcdefghijklmnopqrstuvwxyz0123456789ab'
    'Slack user token (xoxp- + 10+ alnum/-; TEST-prefixed)|xoxp-TEST-1234567890-abcdefghijklmnopqrstuvwxyz0123456789ab'
    'PEM RSA private key header|-----BEGIN RSA PRIVATE KEY-----'
    'PEM EC private key header|-----BEGIN EC PRIVATE KEY-----'
    'PEM OpenSSH private key header|-----BEGIN OPENSSH PRIVATE KEY-----'
    'PEM DSA private key header|-----BEGIN DSA PRIVATE KEY-----'
)

# ── Negative cases (MUST NOT match) ─────────────────────────────────
# 6 known false-positive shapes + near-misses. Each MUST NOT trigger.
# Format: "description|input"
NEGATIVE_CASES=(
    'X-prefixed AKIA (X breaks \b boundary; canonical FP in process.go:375)|XAKIAIOSFODNN7EXAMPLE'
    'Runtime-concat-split AKIA (Go compile-time constant fold; canonical FP in process_test.go:273-279)|awsKey := "AKIAIOSFODNN7" + "EXAMPLE"'
    'Short hex (40 chars, not 64 — VELOX regex requires {64})|VELOX_ADMIN_TOKEN=abcdef0123456789abcdef0123456789abcdef01'
    'PEM PUBLIC key header (PUBLIC, not PRIVATE)|-----BEGIN RSA PUBLIC KEY-----'
    'Bearer reference (no value after = or :- adjacent to hex)|Authorization: Bearer ${VELOX_ADMIN_TOKEN}'
    '***REDACTED*** sentinel via env-var default-value (post-redaction canonical sentinel; not hex-shaped)|${VELOX_ADMIN_TOKEN:-***REDACTED***}'
    '***REDACTED*** sentinel via literal assignment (post-redaction Watchlist — gate must stay GREEN)|VELOX_ADMIN_TOKEN=***REDACTED***'
)

PASS_COUNT=0
FAIL_COUNT=0
TOTAL=$((${#POSITIVE_CASES[@]} + ${#NEGATIVE_CASES[@]}))

run_case() {
    local idx="$1" desc="$2" input="$3" want_match="$4"  # want_match: "true" or "false"
    local got_match
    if matches "$input"; then
        got_match="true"
    else
        got_match="false"
    fi
    if [[ "$got_match" == "$want_match" ]]; then
        PASS_COUNT=$((PASS_COUNT + 1))
        qlog "  PASS [$idx] $desc"
        return 0
    fi
    FAIL_COUNT=$((FAIL_COUNT + 1))
    echo "  FAIL [$idx] $desc" >&2
    echo "    want match=$want_match, got match=$got_match" >&2
    echo "    input: $input" >&2
    return 1
}

qlog "── Positive cases (${#POSITIVE_CASES[@]} — MUST match) ──"
i=0
for entry in "${POSITIVE_CASES[@]}"; do
    i=$((i + 1))
    desc="${entry%%|*}"
    input="${entry#*|}"
    run_case "P$i" "$desc" "$input" "true" || true
done
echo

qlog "── Negative cases (${#NEGATIVE_CASES[@]} — MUST NOT match) ──"
i=0
for entry in "${NEGATIVE_CASES[@]}"; do
    i=$((i + 1))
    desc="${entry%%|*}"
    input="${entry#*|}"
    run_case "N$i" "$desc" "$input" "false" || true
done
echo

# ── Code lockstep checks ────────────────────────────────────────────
# Content-anchored (NOT line-anchored) verification: each obfuscation
# primitive MUST be present in the source code. If someone refactors
# out the X-prefix or the runtime-concat-split (re-introducing the
# contiguous AKIAIOSFODNN7EXAMPLE literal), the gate would re-trigger
# on process_test.go or the comment in process.go and the canonical
# `go test ./internal/infrastructure/process/...` would still pass —
# so the CI no-secrets-audit is the ONLY safety net. This test fails
# loud if either obfuscation primitive is removed.
#
# Content-anchored (NOT line-anchored): a prior version used
# `sed -n '<line>p'` to scope the check to a specific line range.
# That was fragile — refactors that add/remove lines above the
# obfuscation shift line numbers and silently check the wrong code
# (or the wrong number). Content-anchored via `grep -qF` is
# refactor-robust: the literal must appear SOMEWHERE in the file,
# but the specific line does not matter.
#
# The negative-case test fixtures (XAKIAIOSFODNN7EXAMPLE and
# awsKey := "AKIAIOSFODNN7" + "EXAMPLE") ALSO use these literals,
# so the gate's regex NEGATIVE case is implicitly coupled: if someone
# rewrites the negative-case fixture to remove the literal, this
# lockstep check catches it.
qlog "── Code lockstep (2 obfuscation primitives) ──"

# 3 parallel arrays — index `i` corresponds to one lockstep check.
# LOCKSTEP_LOCK_FILES[i]     is the file to scan.
# LOCKSTEP_LOCK_DESCS[i]     is the human-readable description.
# LOCKSTEP_LOCK_LITERALS[i]  is the EXACT text/code that proves the
#                             obfuscation primitive is in place
#                             (grep -F, no regex).
LOCKSTEP_LOCK_FILES=(
    'internal/infrastructure/process/process.go'
    'internal/infrastructure/process/process_test.go'
)
LOCKSTEP_LOCK_DESCS=(
    'XAKIA reference comment (X prefix breaks \b boundary)'
    'Runtime-concat-split awsKey (Go compile-time literal fold)'
)
LOCKSTEP_LOCK_LITERALS=(
    'XAKIAIOSFODNN7EXAMPLE'
    'awsKey := "AKIAIOSFODNN7" + "EXAMPLE"'
)

for i in "${!LOCKSTEP_LOCK_FILES[@]}"; do
    file="${LOCKSTEP_LOCK_FILES[$i]}"
    desc="${LOCKSTEP_LOCK_DESCS[$i]}"
    pattern="${LOCKSTEP_LOCK_LITERALS[$i]}"
    if [[ ! -f "$file" ]]; then
        FAIL_COUNT=$((FAIL_COUNT + 1))
        echo "  FAIL [code] $desc" >&2
        echo "    file not found: $file" >&2
        continue
    fi
    # grep -qF: fixed-string match (no regex, no word-boundary expansion).
    # Fails loud if the obfuscation primitive is missing from the file.
    if ! grep -qF -- "$pattern" "$file"; then
        FAIL_COUNT=$((FAIL_COUNT + 1))
        echo "  FAIL [code] $desc" >&2
        echo "    file $file is MISSING the obfuscation primitive" >&2
        echo "    expected literal: $pattern" >&2
        echo "    investigate: grep -F -- \$pattern $file" >&2
    else
        PASS_COUNT=$((PASS_COUNT + 1))
        qlog "  PASS [code] $desc"
    fi
done
echo


# ── Verdict ─────────────────────────────────────────────────────────
TOTAL=$((TOTAL + ${#LOCKSTEP_LOCK_FILES[@]}))
echo "================================================="
echo "  CI no-secrets-audit test"
echo "  PASS=$PASS_COUNT  FAIL=$FAIL_COUNT  TOTAL=$TOTAL"
echo "  lockstep: TEST_REGEX_PATTERN == GATE_REGEX_PATTERN"
echo "================================================="

if [[ $FAIL_COUNT -gt 0 ]]; then
    echo "VERDICT: FAILED — $FAIL_COUNT test case(s) regressed; remediate before push"
    exit 1
fi
echo "VERDICT: ALL CASES PASS — regex locks the 10 positives, lets the 6 negatives through, and the 2 known FP code locations are clean"
exit 0
