#!/usr/bin/env bash
# Focused tests for scripts/submit-and-poll.sh. No network, no real service:
# curl and df are fake binaries, and the auth wrapper is a pass-through.
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin" "$TMP/state"

cat > "$TMP/bin/curl" <<'EOF'
#!/usr/bin/env bash
set -u
url=""
for arg in "$@"; do
  case "$arg" in http://*|https://*) url="$arg" ;; esac
done
case "$url" in
  */health) exit 0 ;;
  */api/script/generate) printf '{"job_id":"job-test"}\n'; exit 0 ;;
  */api/jobs/job-test/full)
    count=$(cat "$TEST_STATE/polls" 2>/dev/null || echo 0)
    count=$((count + 1))
    printf '%s\n' "$count" > "$TEST_STATE/polls"
    limit=$(cat "$TEST_STATE/limit" 2>/dev/null || echo 999999)
    if (( count <= limit )); then exit 7; fi
    printf '{"status":"SUCCEEDED","current_stage":"done","result":{"result":{"render_metrics":{"expected":1,"successful":1,"failed":0},"localized_renders":[]}}}\n'
    exit 0
    ;;
  *) exit 0 ;;
esac
EOF

cat > "$TMP/bin/df" <<'EOF'
#!/usr/bin/env bash
printf 'Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/fake 1000000 0 900000 10%% /\n'
EOF

cat > "$TMP/auth" <<'EOF'
#!/usr/bin/env bash
# Mirror scripts/with-velox-auth: it exports the token before exec.
export VELOX_ADMIN_TOKEN="$(printf '%064d' 0)"
exec "$@"
EOF
chmod +x "$TMP/bin/curl" "$TMP/bin/df" "$TMP/auth"
printf '{}\n' > "$TMP/payload.json"

run_poll() {
  local key="$1" timeout="$2"
  TEST_STATE="$TMP/state" \
  VELOX_AUTH_BIN="$TMP/auth" \
  VELOX_CURL_BIN="$TMP/bin/curl" \
  VELOX_DF_BIN="$TMP/bin/df" \
  VELOX_MIN_FREE_BYTES=1 \
  VELOX_MIN_FREE_PERCENT=0 \
  VELOX_REQUIRED_STAGING_BYTES=0 \
  VELOX_WORKER_ROOT="$TMP" \
  VELOX_POLL_INTERVAL_SECONDS=0.01 \
  "$ROOT/scripts/submit-and-poll.sh" "$TMP/payload.json" test "$key" "$timeout"
}

fail() { echo "FAIL: $*" >&2; exit 1; }

# 1. Six consecutive transport misses must be reported as POLLING_UNAVAILABLE,
#    must not become JOB_FAILED, and must recover on the same job id.
printf '6\n' > "$TMP/state/limit"
rm -f "$TMP/state/polls"
set +e
out="$(run_poll key-recover 10 2>&1)"
code=$?
set -e
[[ $code -eq 0 ]] || fail "recovering poll exit=$code output=$out"
grep -q 'POLLING_UNAVAILABLE job_id=job-test' <<<"$out" || fail "missing POLLING_UNAVAILABLE: $out"
grep -q 'POLLING_RECOVERED job_id=job-test' <<<"$out" || fail "missing POLLING_RECOVERED: $out"
grep -q 'job_id.*SUCCEEDED\|status.*SUCCEEDED' <<<"$out" || fail "final success not reported: $out"

# 2. A polling outage that outlives the deadline exits with the distinct
#    POLLING_UNAVAILABLE code (4), never the JOB_FAILED code (1).
printf '999999\n' > "$TMP/state/limit"
rm -f "$TMP/state/polls"
set +e
out="$(run_poll key-outage 1 2>&1)"
code=$?
set -e
[[ $code -eq 4 ]] || fail "outage exit=$code, want 4; output=$out"
grep -q 'POLLING_UNAVAILABLE job_id=job-test reason=deadline' <<<"$out" || fail "missing deadline observation: $out"

# 3. The preflight requirement is the maximum of floor, percent and staging.
#    A staging estimate larger than free space must block before submit.
set +e
out="$(
  TEST_STATE="$TMP/state" \
  VELOX_AUTH_BIN="$TMP/auth" \
  VELOX_CURL_BIN="$TMP/bin/curl" \
  VELOX_DF_BIN="$TMP/bin/df" \
  VELOX_MIN_FREE_BYTES=1 \
  VELOX_MIN_FREE_PERCENT=0 \
  VELOX_REQUIRED_STAGING_BYTES=999999999999 \
  VELOX_WORKER_ROOT="$TMP" \
  "$ROOT/scripts/submit-and-poll.sh" "$TMP/payload.json" test key-disk 10 2>&1
)"
code=$?
set -e
[[ $code -eq 3 ]] || fail "disk preflight exit=$code, want 3; output=$out"
grep -q 'richiesti 999999999999' <<<"$out" || fail "staging estimate not enforced: $out"

echo "submit-and-poll tests: PASS"
