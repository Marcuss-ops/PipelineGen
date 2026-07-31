#!/usr/bin/env bash
# scripts/systemd/sudoers/install_operator_access_test.sh
# Isolated sudoers contract tests. They never invoke sudo or write /etc.

set -Eeuo pipefail

ROOT=$(cd "$(dirname "$0")" && pwd)
INSTALLER="$ROOT/install_operator_access.sh"
POLICY="$ROOT/pipelinegen-operator"
WORK_DIR=$(mktemp -d)
trap 'rm -rf -- "$WORK_DIR"' EXIT

assert_contains() {
    local text="$1" expected="$2"
    [[ "$text" == *"$expected"* ]] || {
        printf 'FAIL: expected %q in output:\n%s\n' "$expected" "$text" >&2
        exit 1
    }
}

assert_fails() {
    if "$@" >"$WORK_DIR/command.out" 2>&1; then
        printf 'FAIL: command unexpectedly succeeded: %q\n' "$*" >&2
        cat "$WORK_DIR/command.out" >&2
        exit 1
    fi
}

bash -n "$INSTALLER"
visudo -cf "$POLICY" >/dev/null

valid_output=$(PIPELINEGEN_SUDOERS_POLICY="$POLICY" "$INSTALLER" --check)
assert_contains "$valid_output" 'policy valid'

if PIPELINEGEN_OPERATOR=other-user PIPELINEGEN_SUDOERS_POLICY="$POLICY" "$INSTALLER" --check >"$WORK_DIR/operator.out" 2>&1; then
    printf 'FAIL: policy unexpectedly accepted a different operator\n' >&2
    exit 1
fi
assert_contains "$(cat "$WORK_DIR/operator.out")" 'exact pipelinegen restart/start/stop grant'

bad_policy="$WORK_DIR/wildcard"
sed 's#stop pipelinegen.service#stop *.service#' "$POLICY" > "$bad_policy"
assert_fails env PIPELINEGEN_SUDOERS_POLICY="$bad_policy" "$INSTALLER" --check
assert_contains "$(cat "$WORK_DIR/command.out")" 'exact pipelinegen restart/start/stop grant'

bad_policy="$WORK_DIR/extra"
cat "$POLICY" > "$bad_policy"
printf '%s\n' '/usr/bin/systemctl daemon-reload' >> "$bad_policy"
assert_fails env PIPELINEGEN_SUDOERS_POLICY="$bad_policy" "$INSTALLER" --check
assert_contains "$(cat "$WORK_DIR/command.out")" 'exact pipelinegen restart/start/stop grant'

bad_policy="$WORK_DIR/include"
cat > "$bad_policy" <<'EOF'
#include /etc/sudoers.d/other
Cmnd_Alias PIPELINEGEN_SERVICE_CONTROL = \
    /usr/bin/systemctl restart pipelinegen.service, \
    /usr/bin/systemctl start pipelinegen.service, \
    /usr/bin/systemctl stop pipelinegen.service

pierone ALL=(root) NOPASSWD: PIPELINEGEN_SERVICE_CONTROL
EOF
assert_fails env PIPELINEGEN_SUDOERS_POLICY="$bad_policy" "$INSTALLER" --check
assert_contains "$(cat "$WORK_DIR/command.out")" 'includes external sudoers content'

bad_policy="$WORK_DIR/other-service"
sed 's#pipelinegen.service#artlist-scraper.service#g' "$POLICY" > "$bad_policy"
assert_fails env PIPELINEGEN_SUDOERS_POLICY="$bad_policy" "$INSTALLER" --check
assert_contains "$(cat "$WORK_DIR/command.out")" 'exact pipelinegen restart/start/stop grant'

if [[ "$(id -u)" -ne 0 ]]; then
    assert_fails env PIPELINEGEN_SUDOERS_TARGET="$WORK_DIR/should-not-be-used" "$INSTALLER" --install
    assert_contains "$(cat "$WORK_DIR/command.out")" 'must be run as root'
fi

printf 'sudoers installer tests: PASS\n'
