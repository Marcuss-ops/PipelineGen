#!/usr/bin/env bash
# scripts/launch_server_test.sh — focused tests for the auth/runtime
# precedence contract: explicit environment always wins over .env, .env only
# fills missing variables, and the boot log never prints the token.
#
# These tests exercise scripts/lib/dotenv.sh (the single resolver used by
# scripts/launch_server.sh, scripts/start.sh, and scripts/start_server_bg.sh)
# without launching a real server.

set -Eeuo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK_DIR=$(mktemp -d "${TMPDIR:-/tmp}/launch-server-test.XXXXXX")
trap 'rm -rf "$WORK_DIR"' EXIT

# shellcheck source=scripts/lib/dotenv.sh
source "$ROOT/scripts/lib/dotenv.sh"

file_token=$(printf 'a%.0s' {1..64})
env_token=$(printf 'b%.0s' {1..64})
printf 'VELOX_ADMIN_TOKEN=%s\n' "$file_token" > "$WORK_DIR/.env"

# 1. Explicit environment wins: .env must not override a caller-provided value.
observed=$(VELOX_ADMIN_TOKEN="$env_token" bash -c '
    source "$0" "$1" || exit 1
    load_dotenv_missing "$1"
    printf %s "$VELOX_ADMIN_TOKEN"
' "$ROOT/scripts/lib/dotenv.sh" "$WORK_DIR/.env")
if [[ "$observed" != "$env_token" ]]; then
    echo "FAIL: .env overrode an explicit VELOX_ADMIN_TOKEN" >&2
    exit 1
fi

# 2. .env is used only when the variable is missing from the environment.
observed=$(env -u VELOX_ADMIN_TOKEN bash -c '
    source "$0" "$1" || exit 1
    load_dotenv_missing "$1"
    printf %s "$VELOX_ADMIN_TOKEN"
' "$ROOT/scripts/lib/dotenv.sh" "$WORK_DIR/.env")
if [[ "$observed" != "$file_token" ]]; then
    echo "FAIL: .env did not fill a missing VELOX_ADMIN_TOKEN" >&2
    exit 1
fi

# 3. An empty (but set) variable is also filled by .env — empty is treated as
#    missing so a blank .env entry never wins over a real caller value.
observed=$(VELOX_ADMIN_TOKEN="" bash -c '
    source "$0" "$1" || exit 1
    load_dotenv_missing "$1"
    printf %s "$VELOX_ADMIN_TOKEN"
' "$ROOT/scripts/lib/dotenv.sh" "$WORK_DIR/.env")
if [[ "$observed" != "$file_token" ]]; then
    echo "FAIL: empty VELOX_ADMIN_TOKEN was not filled from .env" >&2
    exit 1
fi

# 4. A file with surrounding quotes still loads (value unwrapped once).
printf 'VELOX_WORKER_TOKEN="%s"\n' "$file_token" > "$WORK_DIR/quoted.env"
observed=$(env -u VELOX_WORKER_TOKEN bash -c '
    source "$0" "$1" || exit 1
    load_dotenv_missing "$1"
    printf %s "$VELOX_WORKER_TOKEN"
' "$ROOT/scripts/lib/dotenv.sh" "$WORK_DIR/quoted.env")
if [[ "$observed" != "$file_token" ]]; then
    echo "FAIL: quoted .env value was not unwrapped" >&2
    exit 1
fi

# 5. The boot diagnostics never print the token: launch_server.sh reports
#    provenance and presence only, and contains no printf/echo/cat of the
#    token value itself.
if grep -nE '(printf|echo|cat)[^|]*\$\{?VELOX_ADMIN_TOKEN' "$ROOT/scripts/launch_server.sh" >&2; then
    echo "FAIL: launch_server.sh prints VELOX_ADMIN_TOKEN" >&2
    exit 1
fi
if ! grep -q "auth_source=" "$ROOT/scripts/launch_server.sh"; then
    echo "FAIL: launch_server.sh does not log auth_source" >&2
    exit 1
fi
if ! grep -q "token_present=" "$ROOT/scripts/launch_server.sh"; then
    echo "FAIL: launch_server.sh does not log token_present" >&2
    exit 1
fi

# 6. Every production boot script must route .env through load_dotenv_missing
#    (env wins). The `set -a; source .env` override pattern is banned in
#    scripts/operate_script_generate.sh — it must use the canonical helper.
if ! grep -q 'load_dotenv_missing .env' "$ROOT/scripts/operate_script_generate.sh"; then
    echo "FAIL: operate_script_generate.sh does not use load_dotenv_missing" >&2
    exit 1
fi
if grep -nE '^[[:space:]]*set -a' "$ROOT/scripts/operate_script_generate.sh" >&2; then
    echo "FAIL: operate_script_generate.sh still uses the set -a override pattern" >&2
    exit 1
fi

echo "launch_server auth/runtime precedence: PASS"
