#!/usr/bin/env bash
# scripts/systemd/migrate_to_systemd.sh
#
# Idempotent migration script: stop the unstable nohup/tmux-managed manual
# processes and delegate to systemd-managed services. The systemd unit files
# at /etc/systemd/system/{pipelinegen,artlist-scraper}.service ALREADY have
# Restart=always (pipelinegen) / Restart=on-failure (artlist-scraper) so the
# post-migration state has auto-restart on crash.
#
# Operator action required: `sudo systemctl enable --now ...` for each service.
# The current host does NOT have NOPASSWD for `sudo systemctl` (see
# architecture/current.yaml#PR-SYSTEMD-RESTART-SUDO-NOPASSDEP), so this script
# prints the exact commands and lets the operator paste them with password.
#
# This script is SAFE to re-run (idempotent). Each step prints a clear marker
# so the operator can verify what happened.
#
# Usage:
#   bash scripts/systemd/migrate_to_systemd.sh              # interactive
#   AUTO_YES=1 bash scripts/systemd/migrate_to_systemd.sh   # non-interactive
#
# Environment variables:
#   AUTO_YES=1            Skip the "proceed?" confirmation
#   SKIP_SUDO=1           Print sudo commands but do not run them (default for
#                         this host; flip to 0 when NOPASSWD is configured)
#   SERVICES="pipelinegen artlist-scraper"  Override the service list
#
# Exit codes:
#   0 = success (services active and healthy)
#   1 = operator declined / pre-condition failed
#   2 = systemctl command failed (sudo needed or service missing)
#   3 = post-condition failed (services not active after enable)
#   4 = manual action required (Step 4 with SKIP_SUDO=1 — operator must
#       run the printed sudo commands and re-run the script)

set -euo pipefail

# --- Configuration (override via env) ---------------------------------------
SERVICES="${SERVICES:-pipelinegen.service artlist-scraper.service}"
AUTO_YES="${AUTO_YES:-0}"
SKIP_SUDO="${SKIP_SUDO:-1}"   # default to print-only (host has no NOPASSWD)
PROJECT_DIR="${PROJECT_DIR:-/home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored}"
EXPECTED_BIN="${EXPECTED_BIN:-${PROJECT_DIR}/pipelinegen}"
EXPECTED_PORT_PG="${EXPECTED_PORT_PG:-8000}"
EXPECTED_PORT_AS="${EXPECTED_PORT_AS:-9123}"

# --- Output helpers --------------------------------------------------------
log()    { printf '\033[1;36m[migrate]\033[0m %s\n' "$*"; }
ok()     { printf '\033[1;32m[OK]\033[0m %s\n' "$*"; }
warn()   { printf '\033[1;33m[WARN]\033[0m %s\n' "$*"; }
err()    { printf '\033[1;31m[ERR]\033[0m %s\n' "$*" >&2; }
hr()     { printf '%s\n' "------------------------------------------------------------"; }

# --- Step 0: preconditions --------------------------------------------------
hr
log "Pre-flight: project root + binary + tooling"
log "PROJECT_DIR=${PROJECT_DIR}"
log "EXPECTED_BIN=${EXPECTED_BIN}"

cd "${PROJECT_DIR}" || { err "cannot cd to ${PROJECT_DIR}"; exit 1; }
[ -f "${EXPECTED_BIN}" ] || { err "binary not found at ${EXPECTED_BIN}"; exit 1; }
[ -f .env ] || { err ".env not found at ${PROJECT_DIR}/.env"; exit 1; }
command -v systemctl >/dev/null 2>&1 || { err "systemctl not found"; exit 1; }
command -v curl >/dev/null 2>&1 || { err "curl not found (required for Step 5 /ready + /health probes)"; exit 1; }
# Note: pgrep + pkill are in /usr/bin/procps on most distros and are
# part of the base system; no separate pre-flight check needed.
ok "preconditions OK"

# --- Step 1: detect manual processes ---------------------------------------
hr
log "Step 1: detect manual (nohup/tmux) processes"

MANUAL_PIDS=()
TMUX_SESSIONS=()

# pipelinegen: typically launched via `nohup ... &` or inside a tmux session.
# The `[/]` in the pgrep pattern is a regex trick to avoid the pgrep command
# line itself matching the pattern (otherwise the shell that runs this
# script — whose argv may contain the path `scripts/systemd/...` and the
# string 'pipelinegen' via the comments above — would be flagged).
while read -r pid; do
    [ -z "${pid}" ] && continue
    # defensive: re-check the cmdline is still our target before adding to the
    # kill list (PID reuse safety, see Code Reviewer #7).
    cmdline=$(ps -o args= -p "${pid}" 2>/dev/null)
    if ! echo "${cmdline}" | grep -qE 'pipelinegen --mode all'; then
        continue
    fi
    MANUAL_PIDS+=("${pid}")
    ppid=$(ps -o ppid= -p "${pid}" 2>/dev/null | tr -d ' ')
    sid=$(ps -o sid= -p "${pid}" 2>/dev/null | tr -d ' ')
    log "  manual pipelinegen PID=${pid} ppid=${ppid} sid=${sid}"
    log "    cmdline: $(echo "${cmdline}" | head -c 200)"
done < <(pgrep -f '[/]pipelinegen --mode all' 2>/dev/null || true)

# artlist-scraper: typically launched via `nohup node artlist_server.js &`.
# Same `[/]` regex trick for self-match avoidance.
while read -r pid; do
    [ -z "${pid}" ] && continue
    cmdline=$(ps -o args= -p "${pid}" 2>/dev/null)
    if ! echo "${cmdline}" | grep -qE 'artlist_server\.js'; then
        continue
    fi
    MANUAL_PIDS+=("${pid}")
    ppid=$(ps -o ppid= -p "${pid}" 2>/dev/null | tr -d ' ')
    log "  manual artlist-scraper PID=${pid} ppid=${ppid}"
    log "    cmdline: $(echo "${cmdline}" | head -c 200)"
done < <(pgrep -f '[/]artlist_server\.js' 2>/dev/null || true)

# detect tmux sessions wrapping the manual processes
if command -v tmux >/dev/null 2>&1; then
    while read -r sess; do
        [ -z "${sess}" ] && continue
        # only flag sessions whose first window runs the binary
        first_cmd=$(tmux list-windows -t "${sess}" -F '#{window_id}:#{pane_current_command}' 2>/dev/null | head -1)
        if echo "${first_cmd}" | grep -qE 'pipelinegen|artlist_server' ; then
            TMUX_SESSIONS+=("${sess}")
            log "  tmux session wrapping a service: ${sess} (${first_cmd})"
        fi
    done < <(tmux list-sessions -F '#{session_name}' 2>/dev/null || true)
fi

if [ ${#MANUAL_PIDS[@]} -eq 0 ] && [ ${#TMUX_SESSIONS[@]} -eq 0 ]; then
    ok "no manual processes detected — services may already be systemd-managed"
fi

# --- Step 2: operator confirmation -----------------------------------------
hr
if [ "${AUTO_YES}" != "1" ]; then
    log "About to: (a) kill ${#MANUAL_PIDS[@]} manual process(es), (b) kill ${#TMUX_SESSIONS[@]} tmux session(s),"
    log "            (c) ask you to run 'sudo systemctl enable --now' (see Step 4 below)."
    # Detect non-interactive shell: if stdin is not a TTY, skip the prompt
    # (otherwise the script would hang indefinitely waiting for input that
    # will never arrive — Code Reviewer #3).
    if ! [ -t 0 ]; then
        warn "stdin is not a TTY — use AUTO_YES=1 to skip the prompt"
        warn "aborting to avoid blocking on a non-interactive shell"
        exit 1
    fi
    printf '\nProceed? [y/N] '
    if ! read -t 30 -r reply; then
        warn "no reply within 30s — aborting"
        exit 1
    fi
    case "${reply}" in
        y|Y|yes|YES) log "proceeding..." ;;
        *) warn "aborted by user"; exit 1 ;;
    esac
fi

# --- Step 3: kill manual processes -----------------------------------------
hr
log "Step 3: stop manual processes"

if [ ${#TMUX_SESSIONS[@]} -gt 0 ]; then
    for sess in "${TMUX_SESSIONS[@]}"; do
        log "  killing tmux session: ${sess}"
        tmux kill-session -t "${sess}" 2>&1 || warn "  tmux kill-session failed for ${sess}"
    done
fi

if [ ${#MANUAL_PIDS[@]} -gt 0 ]; then
    for pid in "${MANUAL_PIDS[@]}"; do
        log "  TERM -> PID ${pid}"
        kill -TERM "${pid}" 2>/dev/null || warn "  kill -TERM ${pid} failed (already gone?)"
    done
    sleep 2
    for pid in "${MANUAL_PIDS[@]}"; do
        if kill -0 "${pid}" 2>/dev/null; then
            warn "  PID ${pid} still alive after TERM, sending KILL"
            kill -KILL "${pid}" 2>/dev/null || true
        fi
    done
    sleep 1
fi

# Re-check: any manual processes still alive?
remaining=$(pgrep -f 'pipelinegen --mode all' 2>/dev/null; pgrep -f 'artlist_server.js' 2>/dev/null) || true
if [ -n "${remaining}" ]; then
    err "manual processes still alive after kill:"
    echo "${remaining}" | sed 's/^/  /'
    err "aborting — fix the stuck process manually and re-run"
    exit 1
fi
ok "all manual processes stopped"

# --- Step 4: ask operator to run sudo commands -----------------------------
hr
log "Step 4: enable + start systemd services (operator action)"

cat <<'BANNER'

The systemd unit files at /etc/systemd/system/{pipelinegen,artlist-scraper}.service
ALREADY have the right Restart= directives:
  pipelinegen.service:    Restart=always   RestartSec=3
  artlist-scraper.service: Restart=on-failure RestartSec=10

The 5 drop-in files in /etc/systemd/system/pipelinegen.service.d/ are preserved.

Now run these commands (you will be prompted for your sudo password):

BANNER

SUDO_CMDS=()
SUDO_CMDS+=("sudo systemctl daemon-reload")
for svc in ${SERVICES}; do
    SUDO_CMDS+=("sudo systemctl enable --now ${svc}")
    SUDO_CMDS+=("sudo systemctl status ${svc} --no-pager")
done
SUDO_CMDS+=("systemctl is-active ${SERVICES}")

if [ "${SKIP_SUDO}" = "1" ] || ! command -v sudo >/dev/null 2>&1; then
    log "SKIP_SUDO=${SKIP_SUDO} — printing commands for manual execution:"
    for cmd in "${SUDO_CMDS[@]}"; do
        printf '  %s\n' "${cmd}"
    done
    # LOUD MANUAL ACTION banner — Code Reviewer #2: exit 0 is misleading
    # when the migration is incomplete. Use exit 4 to signal "pending
    # operator action" so callers (CI, monitoring) can detect this state.
    printf '\n'
    err "=========================================================================="
    err "MANUAL ACTION REQUIRED — migration is NOT complete"
    err "Run the 4 commands printed above, then re-run this script to verify."
    err "=========================================================================="
    exit 4
fi

# --- Optional: actually run the sudo commands (requires NOPASSWD) ----------
log "running sudo commands (will prompt for password unless NOPASSWD)..."
for cmd in "${SUDO_CMDS[@]}"; do
    log "  ${cmd}"
    if ! ${cmd}; then
        err "command failed: ${cmd}"
        warn "if password was the issue, run the commands manually (printed above)"
        exit 2
    fi
done
ok "sudo commands completed"

# --- Step 5: post-condition verification -----------------------------------
hr
log "Step 5: verify post-condition"

for svc in ${SERVICES}; do
    if systemctl is-active --quiet "${svc}"; then
        ok "${svc} is active"
    else
        err "${svc} is NOT active"
        systemctl status "${svc}" --no-pager 2>&1 | head -10
        exit 3
    fi
done

# Port binding check.
# Use `grep -E ":PORT(\s|$)"` instead of `grep -q ":PORT "` so the check
# is robust to different `ss` output formats — `ss -tlnp` can render the
# address column with either trailing whitespace OR a newline (e.g. for
# the last row), and IPv6-bound sockets are printed as `[::]:PORT` without
# a trailing space. The `\s|$` pattern matches either a whitespace
# boundary OR end-of-line (Code Reviewer #4 — round 1).
if ss -tlnp 2>/dev/null | grep -E -q ":${EXPECTED_PORT_PG}(\s|$)"; then
    ok "port ${EXPECTED_PORT_PG} (pipelinegen) is bound"
else
    warn "port ${EXPECTED_PORT_PG} (pipelinegen) is NOT bound yet (may need a few seconds)"
fi
if ss -tlnp 2>/dev/null | grep -E -q ":${EXPECTED_PORT_AS}(\s|$)"; then
    ok "port ${EXPECTED_PORT_AS} (artlist-scraper) is bound"
else
    warn "port ${EXPECTED_PORT_AS} (artlist-scraper) is NOT bound yet (may need a few seconds)"
fi

# /ready + /health checks (after a short settle)
sleep 2
# HTTP status code check.
# Use `grep -qE '^200$'` instead of `grep -q 200` so the check is robust
# against false-positive matches on any non-`200` 3+ digit string (e.g.
# corrupted curl output like `2000` or `200\nFoo` if a proxy appends
# extra chars; HTTP `000` from curl connection failure; etc.).
# The curl `-w '%{http_code}'` output is just a 3-digit code followed by
# newline, so `^200$` (anchor to start AND end of line) is the correct
# match — not just `^200` (which would match `200` followed by anything)
# nor just `200` (substring match, fragile per round 1).
# Code Reviewer #4 — round 2.
if curl -sS -o /dev/null -w '%{http_code}' --max-time 3 "http://127.0.0.1:${EXPECTED_PORT_PG}/ready" 2>/dev/null | grep -qE '^200$'; then
    ok "pipelinegen /ready = 200"
else
    warn "pipelinegen /ready did not return 200 yet"
fi
if curl -sS -o /dev/null -w '%{http_code}' --max-time 3 "http://127.0.0.1:${EXPECTED_PORT_AS}/health" 2>/dev/null | grep -qE '^200$'; then
    ok "artlist-scraper /health = 200"
else
    warn "artlist-scraper /health did not return 200 yet"
fi

# --- Done -------------------------------------------------------------------
hr
ok "migration complete"
log "Next steps:"
log "  1. update the forward-pointer architecture/current.yaml#PR-SYSTEMD-RESTART-SUDO-NOPASSDEP"
log "     to reflect that the operator migration script is available"
log "  2. (optional) install the restricted daily operator policy:"
log "     sudo scripts/systemd/sudoers/install_operator_access.sh --install"
log "  3. (optional) configure log rotation: see scripts/systemd/README.md"

exit 0
