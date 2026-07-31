#!/usr/bin/env bash
# scripts/systemd/sudoers/install_operator_access.sh
#
# Validate the versioned PipelineGen sudoers policy or install a rendered copy
# without invoking sudo and without reading any credential or token.
#
# Usage:
#   scripts/systemd/sudoers/install_operator_access.sh --check
#   sudo scripts/systemd/sudoers/install_operator_access.sh --install
#
# --install intentionally requires the caller to already be root. This keeps
# password prompts out of automation and prevents this helper from becoming a
# second, less-auditable privilege escalation path.

set -Eeuo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly POLICY_FILE_DEFAULT="$SCRIPT_DIR/pipelinegen-operator"
readonly TEMPLATE_FILE="$SCRIPT_DIR/pipelinegen-operator.template"
readonly DEFAULT_TARGET="/etc/sudoers.d/pipelinegen-operator"
readonly DEFAULT_OPERATOR="pierone"
readonly DEFAULT_SYSTEMCTL="/usr/bin/systemctl"

POLICY_FILE="${PIPELINEGEN_SUDOERS_POLICY:-$POLICY_FILE_DEFAULT}"
TARGET_FILE="${PIPELINEGEN_SUDOERS_TARGET:-$DEFAULT_TARGET}"
OPERATOR="${PIPELINEGEN_OPERATOR:-$DEFAULT_OPERATOR}"
SYSTEMCTL_PATH="${PIPELINEGEN_SYSTEMCTL_PATH:-$DEFAULT_SYSTEMCTL}"

fail() {
    printf '[pipelinegen-sudoers] ERROR: %s\n' "$*" >&2
    exit 1
}

usage() {
    cat >&2 <<USAGE
Usage: $0 {--check|--install}

  --check    Validate the versioned policy with visudo; never writes files.
  --install  Render, validate, and install the policy; caller must be root.

Environment overrides (for controlled deployments/tests):
  PIPELINEGEN_SUDOERS_POLICY   Policy to validate (check mode)
  PIPELINEGEN_SUDOERS_TARGET   Installation target (default: $DEFAULT_TARGET)
  PIPELINEGEN_OPERATOR          Unix operator account (default: $DEFAULT_OPERATOR)
  PIPELINEGEN_SYSTEMCTL_PATH    Absolute systemctl path (default: $DEFAULT_SYSTEMCTL)
USAGE
}

require_command() {
    command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

validate_operator() {
    [[ "$OPERATOR" =~ ^[a-z_][a-z0-9_-]*\$?$ ]] ||
        fail 'PIPELINEGEN_OPERATOR must be a valid Unix account name'
}

validate_systemctl_path() {
    [[ "$SYSTEMCTL_PATH" =~ ^/[A-Za-z0-9._/-]+$ ]] ||
        fail 'PIPELINEGEN_SYSTEMCTL_PATH must be an absolute path without unsafe characters'
    [[ "$(basename "$SYSTEMCTL_PATH")" == systemctl ]] ||
        fail 'PIPELINEGEN_SYSTEMCTL_PATH must name systemctl'
    [[ -x "$SYSTEMCTL_PATH" ]] ||
        fail "systemctl executable not found: $SYSTEMCTL_PATH"
}

validate_target_path() {
    [[ "$TARGET_FILE" == /etc/sudoers.d/* ]] ||
        fail 'PIPELINEGEN_SUDOERS_TARGET must remain under /etc/sudoers.d'
    [[ "$(dirname -- "$TARGET_FILE")" == /etc/sudoers.d ]] ||
        fail 'PIPELINEGEN_SUDOERS_TARGET must be directly inside /etc/sudoers.d'
    [[ "$(basename -- "$TARGET_FILE")" =~ ^[A-Za-z0-9_.-]+$ ]] ||
        fail 'PIPELINEGEN_SUDOERS_TARGET has an unsafe filename'
    [[ ! -L "$TARGET_FILE" ]] ||
        fail 'PIPELINEGEN_SUDOERS_TARGET must not be a symlink'
}

render_policy() {
    [[ -f "$TEMPLATE_FILE" ]] || fail "policy template not found: $TEMPLATE_FILE"
    sed \
        -e "s#__SYSTEMCTL__#$SYSTEMCTL_PATH#g" \
        -e "s#__OPERATOR__#$OPERATOR#g" \
        "$TEMPLATE_FILE"
}

policy_body() {
    sed -E '/^[[:space:]]*#/d; /^[[:space:]]*$/d' "$1"
}

validate_policy_contract() {
    local policy="$1"
    local actual expected

    [[ -f "$policy" ]] || fail "sudoers policy not found: $policy"
    [[ -r "$policy" ]] || fail "sudoers policy is not readable: $policy"

    # Includes would make a local review incomplete and could import broader
    # privileges, so reject them before comparing the complete policy body.
    if grep -Eq '^[[:space:]]*(#(include|includedir)|@include)' "$policy"; then
        fail 'policy includes external sudoers content'
    fi

    # Compare every non-comment, non-blank line to the exact rendered
    # three-command policy. This rejects wildcard commands, extra aliases,
    # extra services, and broad grants without a fragile deny-list.
    actual="$(policy_body "$policy")"
    expected="$(policy_body <(render_policy))"
    [[ "$actual" == "$expected" ]] ||
        fail 'policy must contain only the exact pipelinegen restart/start/stop grant'
}

validate_with_visudo() {
    local policy="$1"
    require_command visudo
    visudo -cf "$policy" >/dev/null 2>&1 ||
        fail "visudo rejected the policy: $policy"
}

check_policy() {
    validate_operator
    validate_systemctl_path
    validate_policy_contract "$POLICY_FILE"
    validate_with_visudo "$POLICY_FILE"
    printf '[pipelinegen-sudoers] policy valid: %s\n' "$POLICY_FILE"
}

install_policy() {
    [[ "$(id -u)" -eq 0 ]] ||
        fail '--install must be run as root; this helper never invokes sudo'
    validate_operator
    validate_systemctl_path
    validate_target_path
    require_command install
    require_command mktemp

    local temp
    temp="$(mktemp "${TARGET_FILE}.tmp.XXXXXX")"
    trap 'rm -f -- "$temp"' RETURN

    render_policy > "$temp"
    chmod 0440 "$temp"
    validate_policy_contract "$temp"
    validate_with_visudo "$temp"
    install -o root -g root -m 0440 "$temp" "$TARGET_FILE"
    validate_with_visudo "$TARGET_FILE"
    trap - RETURN
    rm -f -- "$temp"
    printf '[pipelinegen-sudoers] installed and validated: %s\n' "$TARGET_FILE"
}

main() {
    [[ $# -eq 1 ]] || { usage; exit 2; }
    case "$1" in
        --check) check_policy ;;
        --install) install_policy ;;
        *) usage; exit 2 ;;
    esac
}

main "$@"
