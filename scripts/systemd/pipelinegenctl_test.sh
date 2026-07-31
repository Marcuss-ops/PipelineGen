#!/usr/bin/env bash
# scripts/systemd/pipelinegenctl_test.sh
# Isolated tests for pipelinegenctl. They use fake commands and never contact
# systemd, sudo, a live HTTP server, or Drive.

set -Eeuo pipefail

ROOT=$(cd "$(dirname "$0")" && pwd)
CLI="$ROOT/pipelinegenctl"
WORK_DIR=$(mktemp -d)
trap 'rm -rf "$WORK_DIR"' EXIT

FAKE_BIN="$WORK_DIR/bin"
mkdir -p "$FAKE_BIN"
CALLS="$WORK_DIR/calls.log"
: > "$CALLS"

cat > "$FAKE_BIN/systemctl" <<'FAKE_SYSTEMCTL'
#!/usr/bin/env bash
set -u
printf 'systemctl %s\n' "$*" >> "${FAKE_CALLS:?}"
case "${1:-}" in
    is-active)
        if [[ "${FAKE_SYSTEMCTL_ACTIVE:-1}" == "1" ]]; then
            [[ "${2:-}" == "--quiet" ]] || printf 'active\n'
            exit 0
        fi
        [[ "${2:-}" == "--quiet" ]] || printf 'inactive\n'
        exit 3
        ;;
    restart)
        exit "${FAKE_SYSTEMCTL_RESTART_RC:-0}"
        ;;
    *)
        exit 0
        ;;
esac
FAKE_SYSTEMCTL

cat > "$FAKE_BIN/sudo" <<'FAKE_SUDO'
#!/usr/bin/env bash
set -u
printf 'sudo %s\n' "$*" >> "${FAKE_CALLS:?}"
[[ "${1:-}" == "-n" ]] || exit 91
shift
exec "$@"
FAKE_SUDO

cat > "$FAKE_BIN/timeout" <<'FAKE_TIMEOUT'
#!/usr/bin/env bash
set -u
printf 'timeout %s\n' "$*" >> "${FAKE_CALLS:?}"
[[ "${1:-}" == "--signal=TERM" ]] || exit 92
[[ "${2:-}" == "--kill-after=5s" ]] || exit 93
shift 3
exec "$@"
FAKE_TIMEOUT

cat > "$FAKE_BIN/curl" <<'FAKE_CURL'
#!/usr/bin/env bash
set -u
printf 'curl %s\n' "$*" >> "${FAKE_CALLS:?}"
printf '%s' "${FAKE_CURL_CODE:-200}"
FAKE_CURL

cat > "$FAKE_BIN/journalctl" <<'FAKE_JOURNALCTL'
#!/usr/bin/env bash
set -u
printf 'journalctl %s\n' "$*" >> "${FAKE_CALLS:?}"
printf '%s\n' 'ok: normal log'
printf '%s\n' 'Authorization: Bearer 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'
printf '%s\n' 'VELOX_ADMIN_TOKEN=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'
printf '%s\n' 'folder_id: drive-folder-secret'
printf '%s\n' 'file_id="drive-file-secret"'
printf '%s\n' 'drive_link: https://drive.google.com/file/d/drive-file-secret/view'
printf '%s\n' '{"drive_link":"https://drive.google.com/file/d/drive-file-secret/view","webViewLink":"https://drive.google.com/file/d/drive-file-secret/view","fileId":"camel-file-secret","folderId":"camel-folder-secret","password":"secret-value","token":"token-value"}'
printf '%s\n' 'stderr-token: TOKEN=token-stderr-secret' >&2
exit "${FAKE_JOURNALCTL_RC:-0}"
FAKE_JOURNALCTL

chmod +x "$FAKE_BIN"/* "$CLI"

run_cli() {
    SYSTEMCTL_BIN="$FAKE_BIN/systemctl" \
    SUDO_BIN="$FAKE_BIN/sudo" \
    CURL_BIN="$FAKE_BIN/curl" \
    JOURNALCTL_BIN="$FAKE_BIN/journalctl" \
    TIMEOUT_BIN="$FAKE_BIN/timeout" \
    FAKE_CALLS="$CALLS" \
    PIPELINEGEN_READY_TIMEOUT=1 \
    PIPELINEGEN_RESTART_TIMEOUT=30 \
    PIPELINEGEN_LOG_LINES=7 \
    PIPELINEGEN_BASE_URL="${PIPELINEGEN_BASE_URL:-http://127.0.0.1:8000}" \
    "$CLI" "$@"
}

assert_contains() {
    local text="$1" expected="$2"
    [[ "$text" == *"$expected"* ]] || {
        printf 'FAIL: expected output to contain %q\n' "$expected" >&2
        printf '%s\n' "$text" >&2
        exit 1
    }
}

assert_not_contains() {
    local text="$1" forbidden="$2"
    [[ "$text" != *"$forbidden"* ]] || {
        printf 'FAIL: output contains forbidden value %q\n' "$forbidden" >&2
        printf '%s\n' "$text" >&2
        exit 1
    }
}

: > "$CALLS"
FAKE_SYSTEMCTL_ACTIVE=1 output=$(run_cli status)
assert_contains "$output" 'pipelinegen.service is active'
! grep -q '^sudo ' "$CALLS"

: > "$CALLS"
if FAKE_SYSTEMCTL_ACTIVE=0 run_cli status >"$WORK_DIR/status.out" 2>&1; then
    printf 'FAIL: inactive status unexpectedly succeeded\n' >&2
    exit 1
fi
assert_contains "$(cat "$WORK_DIR/status.out")" 'pipelinegen.service is inactive'
! grep -q '^sudo ' "$CALLS"

: > "$CALLS"
FAKE_SYSTEMCTL_ACTIVE=1 run_cli restart >/dev/null
calls=$(cat "$CALLS")
assert_contains "$calls" "timeout --signal=TERM --kill-after=5s 30s $FAKE_BIN/sudo -n $FAKE_BIN/systemctl restart pipelinegen.service"
assert_contains "$calls" "sudo -n $FAKE_BIN/systemctl restart pipelinegen.service"
if grep -Eq '(^| )((start|stop)|daemon-reload|artlist)' <<<"$calls"; then
    printf 'FAIL: restart used a forbidden systemd operation or service\n' >&2
    printf '%s\n' "$calls" >&2
    exit 1
fi

: > "$CALLS"
FAKE_SYSTEMCTL_ACTIVE=1 run_cli verify >/dev/null
calls=$(cat "$CALLS")
! grep -q '^sudo ' <<<"$calls"
assert_contains "$calls" 'curl '

: > "$CALLS"
if FAKE_SYSTEMCTL_ACTIVE=0 run_cli verify >"$WORK_DIR/inactive.out" 2>&1; then
    printf 'FAIL: inactive service unexpectedly verified\n' >&2
    exit 1
fi
assert_contains "$(cat "$WORK_DIR/inactive.out")" 'did not become active within 1s'

: > "$CALLS"
if FAKE_SYSTEMCTL_ACTIVE=1 FAKE_CURL_CODE=503 run_cli verify >"$WORK_DIR/not-ready.out" 2>&1; then
    printf 'FAIL: non-200 readiness unexpectedly verified\n' >&2
    exit 1
fi
assert_contains "$(cat "$WORK_DIR/not-ready.out")" 'did not return HTTP 200 within 1s'

: > "$CALLS"
if FAKE_SYSTEMCTL_ACTIVE=1 FAKE_SYSTEMCTL_RESTART_RC=1 run_cli restart >"$WORK_DIR/restart-failed.out" 2>&1; then
    printf 'FAIL: failed restart unexpectedly succeeded\n' >&2
    exit 1
fi
assert_contains "$(cat "$WORK_DIR/restart-failed.out")" 'restart denied, failed, or timed out'

: > "$CALLS"
FAKE_SYSTEMCTL_ACTIVE=1 run_cli restart-verify >/dev/null
calls=$(cat "$CALLS")
assert_contains "$calls" "sudo -n $FAKE_BIN/systemctl restart pipelinegen.service"
assert_contains "$calls" 'curl '

: > "$CALLS"
FAKE_SYSTEMCTL_ACTIVE=1 logs=$(run_cli logs)
assert_contains "$logs" '<SENSITIVE_LOG_REDACTED>'
assert_not_contains "$logs" '0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef'
assert_not_contains "$logs" 'drive-folder-secret'
assert_not_contains "$logs" 'drive-file-secret'
assert_not_contains "$logs" 'camel-file-secret'
assert_not_contains "$logs" 'camel-folder-secret'
assert_not_contains "$logs" 'secret-value'
assert_not_contains "$logs" 'token-value'
assert_not_contains "$logs" 'token-stderr-secret'
assert_contains "$(cat "$CALLS")" '--unit pipelinegen.service --lines 7 --no-pager --output cat'

: > "$CALLS"
if FAKE_JOURNALCTL_RC=1 run_cli logs >"$WORK_DIR/logs-failed.out" 2>&1; then
    printf 'FAIL: failed journal read unexpectedly succeeded\n' >&2
    exit 1
fi
assert_contains "$(cat "$WORK_DIR/logs-failed.out")" 'unable to read PipelineGen logs'

: > "$CALLS"
if SYSTEMCTL_BIN="$FAKE_BIN/systemctl" \
    SUDO_BIN="$FAKE_BIN/sudo" \
    CURL_BIN="$FAKE_BIN/curl" \
    JOURNALCTL_BIN="$FAKE_BIN/journalctl" \
    TIMEOUT_BIN="$FAKE_BIN/timeout" \
    FAKE_CALLS="$CALLS" \
    PIPELINEGEN_READY_TIMEOUT=1 \
    PIPELINEGEN_RESTART_TIMEOUT=30 \
    PIPELINEGEN_LOG_LINES=7 \
    PIPELINEGEN_BASE_URL='http://127.0.0.1:8000/unsafe' \
    "$CLI" verify >"$WORK_DIR/config.out" 2>&1; then
    printf 'FAIL: unsafe base URL unexpectedly accepted\n' >&2
    exit 1
fi
assert_contains "$(cat "$WORK_DIR/config.out")" 'PIPELINEGEN_BASE_URL must be an http(s) URL without a path'

if "$CLI" unknown >"$WORK_DIR/usage.out" 2>&1; then
    printf 'FAIL: unknown command unexpectedly succeeded\n' >&2
    exit 1
fi
assert_contains "$(cat "$WORK_DIR/usage.out")" 'Usage:'

printf 'pipelinegenctl tests: PASS\n'
