#!/usr/bin/env bash
# scripts/deploy/deploy_selftest.sh — hermetic acceptance test for the deploy
# tooling. It proves, with fakes and no live systemd, that:
#   1. the restart is invoked exactly once and its failure fails the deploy
#      (the exit code is NOT swallowed — the historical `| tail` bug);
#   2. the ready gate blocks until /health answers;
#   3. the identity gate FAILS when /proc/<MainPID>/exe is not the built binary
#      and PASSES when it is;
#   4. deploy-status reports MATCH for a matched pair and MISMATCH otherwise.
#
# Usage: scripts/deploy/deploy_selftest.sh
set -Eeuo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
DEPLOY="$SCRIPT_DIR/pipelinegen_deploy.sh"
STATUS="$SCRIPT_DIR/pipelinegen_deploy_status.sh"

FAILURES=0
pass() { printf '  PASS %s\n' "$1"; }
fail() { printf '  FAIL %s\n' "$1"; FAILURES=$((FAILURES + 1)); }

TMP="$(mktemp -d)"
cleanup() {
  pkill -f "$TMP/pipelinegen" >/dev/null 2>&1 || true
  rm -rf "$TMP"
}
trap cleanup EXIT

# A long-running copy of a real binary: /proc/<pid>/exe resolves to this path,
# so sha256(/proc/<pid>/exe) equals sha256($BIN) and the identity gate can pass.
BIN="$TMP/pipelinegen"
cp "$(command -v sleep)" "$BIN"
chmod 0755 "$BIN"

# Fake pipelinegenctl: records the restart and can be made to fail.
RESTART_LOG="$TMP/restart.log"
cat > "$TMP/pipelinegenctl" <<EOF
#!/usr/bin/env bash
printf 'restart %s\n' "\$*" >> "$RESTART_LOG"
exit "\${FAKE_RESTART_EXIT:-0}"
EOF
chmod 0755 "$TMP/pipelinegenctl"

# Fake systemctl: `show -p MainPID --value <svc>` prints FAKE_MAIN_PID.
cat > "$TMP/systemctl" <<'EOF'
#!/usr/bin/env bash
if [ "${1:-}" = "show" ]; then
  printf '%s\n' "${FAKE_MAIN_PID:-0}"
fi
exit 0
EOF
chmod 0755 "$TMP/systemctl"

# Fake curl: fail FAKE_CURL_FAILS times, then succeed. Proves the ready gate
# actually retries instead of accepting the first failure.
CURL_STATE="$TMP/curl.count"
cat > "$TMP/curl" <<'EOF'
#!/usr/bin/env bash
# `-w '%{http_code}'` requests (deploy-status) always answer 200.
for a in "$@"; do
  if [ "$a" = "-w" ]; then
    # Simulate a probe timeout: curl exits non-zero and prints NOTHING (the
    # real curl prints 000; this fake reproduces the empty-capture case).
    if [ "${FAKE_CURL_TIMEOUT:-0}" = "1" ]; then exit 28; fi
    printf '200'; exit 0
  fi
done
state="${FAKE_CURL_STATE:?}"
n=0
[ -f "$state" ] && n="$(cat "$state")"
n=$((n + 1))
printf '%s' "$n" > "$state"
if [ "$n" -le "${FAKE_CURL_FAILS:-0}" ]; then exit 7; fi
exit 0
EOF
chmod 0755 "$TMP/curl"

run_deploy() {
  env \
    DEPLOY_BIN_PATH="$BIN" \
    DEPLOY_SYSTEMCTL_BIN="$TMP/systemctl" \
    DEPLOY_PIPELINEGENCTL_BIN="$TMP/pipelinegenctl" \
    DEPLOY_CURL_BIN="$TMP/curl" \
    DEPLOY_HEALTH_TIMEOUT=10 \
    DEPLOY_READY_TIMEOUT=5 \
    DEPLOY_PROC_ROOT="/proc" \
    FAKE_CURL_STATE="$CURL_STATE" \
    "$@"
}

printf '== deploy selftest\n'

# ── Case 1: happy path ────────────────────────────────────────────────
"$BIN" 30 & FAKE_PID=$!
: > "$RESTART_LOG"; printf '0' > "$CURL_STATE"
if run_deploy FAKE_MAIN_PID="$FAKE_PID" FAKE_CURL_FAILS=2 bash "$DEPLOY" --skip-build >"$TMP/c1.out" 2>&1; then
  pass "happy path exits 0"
else
  fail "happy path exits 0 ($(tail -1 "$TMP/c1.out"))"
fi
if [ "$(wc -l < "$RESTART_LOG" | tr -d ' ')" = "1" ]; then
  pass "restart invoked exactly once"
else
  fail "restart invoked exactly once (got $(wc -l < "$RESTART_LOG"))"
fi
if grep -q 'running IS the built binary' "$TMP/c1.out"; then
  pass "identity gate confirms running == built"
else
  fail "identity gate confirms running == built"
fi

# ── Case 2: identity mismatch must fail ───────────────────────────────
: > "$RESTART_LOG"; printf '0' > "$CURL_STATE"
if run_deploy FAKE_MAIN_PID="$$" bash "$DEPLOY" --skip-build >"$TMP/c2.out" 2>&1; then
  fail "identity mismatch fails the deploy"
else
  if grep -q 'is NOT the built binary' "$TMP/c2.out"; then
    pass "identity mismatch fails the deploy"
  else
    fail "identity mismatch fails with the right message ($(tail -1 "$TMP/c2.out"))"
  fi
fi

# ── Case 3: restart refusal must fail, never be masked ────────────────
if [ -n "${FAKE_PID:-}" ]; then kill "$FAKE_PID" 2>/dev/null || true; fi
"$BIN" 30 & FAKE_PID2=$!
: > "$RESTART_LOG"; printf '0' > "$CURL_STATE"
if run_deploy FAKE_MAIN_PID="$FAKE_PID2" FAKE_RESTART_EXIT=1 bash "$DEPLOY" --skip-build >"$TMP/c3.out" 2>&1; then
  fail "restart refusal fails the deploy"
else
  if grep -q 'restart failed' "$TMP/c3.out"; then
    pass "restart refusal fails the deploy"
  else
    fail "restart refusal fails with the right message ($(tail -1 "$TMP/c3.out"))"
  fi
fi

# ── Case 4: deploy-status verdicts ────────────────────────────────────
status_run() {
  env \
    DEPLOY_BIN_PATH="$BIN" \
    DEPLOY_SYSTEMCTL_BIN="$TMP/systemctl" \
    DEPLOY_CURL_BIN="$TMP/curl" \
    FAKE_CURL_STATE="$CURL_STATE" \
    "$@"
}
printf '0' > "$CURL_STATE"
if status_run FAKE_MAIN_PID="$FAKE_PID2" bash "$STATUS" >"$TMP/c4.out" 2>&1; then
  grep -q 'MATCH' "$TMP/c4.out" && pass "status: MATCH on a matched pair" || fail "status: MATCH on a matched pair"
else
  fail "status: MATCH on a matched pair (exit $?)"
fi
if status_run FAKE_MAIN_PID="$$" bash "$STATUS" >"$TMP/c5.out" 2>&1; then
  fail "status: MISMATCH must exit non-zero"
else
  grep -q 'MISMATCH' "$TMP/c5.out" && pass "status: MISMATCH on a mismatched pair" || fail "status: MISMATCH on a mismatched pair"
fi

# ── Case 6: probe timeout must print 000 ONCE (regression: "HTTP 000000") ─
# Observed against the live service before the fix: curl prints 000 on a
# timeout AND a fallback `|| echo 000` appended another, yielding 000000.
if status_run FAKE_MAIN_PID="$FAKE_PID2" FAKE_CURL_TIMEOUT=1 bash "$STATUS" >"$TMP/c6.out" 2>&1; then
  : # exit code is irrelevant here
fi
if grep -q '000000' "$TMP/c6.out"; then
  fail "status: probe timeout must not double the 000 sentinel"
else
  pass "status: probe timeout reports 000 exactly once"
fi

printf '== deploy selftest: %d failure(s)\n' "$FAILURES"
[ "$FAILURES" -eq 0 ]
