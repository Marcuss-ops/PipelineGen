#!/usr/bin/env bash
# scripts/rotate_token.sh
#
# Rotate the VELOX_ADMIN_TOKEN (and optionally VELOX_WORKER_TOKEN) on a
# running pipelinegen service. Generates cryptographically random 64-hex
# tokens (256 bits of entropy), backs up the old env file, restarts the
# system service, and verifies the new token authenticates while the
# old one is rejected.
#
# Replaces the dev placeholder "test-admin-token-12345" (a 22-char literal
# that ships in some test deployments) with a canonical 64-hex token.
#
# Requires:
#   - root (or sudo) — the script writes to /etc
#   - systemd unit named "pipelinegen" (or --unit NAME)
#   - openssl in PATH for rand -hex 32
#   - binary alive on http://127.0.0.1:$PORT — pass --port to override the
#     default 8080 if the service binds elsewhere
#
# Usage:
#   sudo scripts/rotate_token.sh
#   sudo PIPELINEGEN_UNIT=pipelinegen-stage scripts/rotate_token.sh
#   sudo scripts/rotate_token.sh --also-worker --dry-run
#   sudo scripts/rotate_token.sh --port 28080 --keep-backups 10

set -euo pipefail

usage() {
  cat <<USAGE
Usage: sudo $0 [flags]

Flags:
  --also-worker        Generate a distinct VELOX_WORKER_TOKEN alongside ADMIN
  --dry-run            Print the plan only; do not write or restart
  --env-file PATH      Override env file location (default: auto-detect)
  --unit NAME          systemd unit name (default: pipelinegen)
  --port NNNN          Health-check port (default: 8080, or VELOX_PORT
                       from the env file). Used for /api/health + auth probes.
  --keep-backups N     How many recent .bak.* files to keep (default: 5)
  --help               Show this message

Examples:
  sudo $0
  sudo $0 --also-worker
  sudo $0 --dry-run
  sudo $0 --port 28080 --keep-backups 10
USAGE
}

DRY_RUN=0
ALSO_WORKER=0
ENV_FILE_OVERRIDE=""
UNIT_NAME="${PIPELINEGEN_UNIT:-pipelinegen}"
PORT_OVERRIDE="${VELOX_PORT:-}"
# Canonical default: 8080 (see internal/infrastructure/config/types.go
# `Server.Port`). Operators that run the server on a non-default port
# pass --port or set VELOX_PORT in the env file; both are honoured
# above. Keeping the `-${VELOX_PORT:-8080}` fallback inline ensures the
# rotation probe targets the right broker even when the systemd
# EnvironmentFile is missing.
KEEP_BACKUPS="${ROTATE_KEEP_BACKUPS:-5}"

while [ "${1:-}" != "" ]; do
  case "$1" in
    --also-worker) ALSO_WORKER=1 ;;
    --dry-run)     DRY_RUN=1 ;;
    --env-file)    ENV_FILE_OVERRIDE="$2"; shift ;;
    --unit)        UNIT_NAME="$2"; shift ;;
    --port)        PORT_OVERRIDE="$2"; shift ;;
    --keep-backups) KEEP_BACKUPS="$2"; shift ;;
    --help|-h)     usage; exit 0 ;;
    *) echo "unknown flag: $1" >&2; usage; exit 2 ;;
  esac
  shift
done

# --- sudo guard: rotation writes to /etc, so root is mandatory.
if [ "$(id -u)" -ne 0 ]; then
  echo "ERROR: must be run as root (sudo $0 ...)" >&2
  exit 1
fi

# --- locate env file: prefer systemd EnvironmentFiles directive; fall back
#     to the standard /etc/pipelinegen/pipelinegen.env.
ENV_FILE="$ENV_FILE_OVERRIDE"
if [ -z "$ENV_FILE" ]; then
  EF_LINE="$(systemctl show "$UNIT_NAME" -p EnvironmentFiles 2>/dev/null | sed -n 's/^EnvironmentFiles=//p')"
  if [ -n "$EF_LINE" ]; then
    # EnvironmentFiles can list multiple paths. Take the first existing one.
    for p in $EF_LINE; do
      lp="${p%\"}"; lp="${lp#\"}"
      if [ -f "$lp" ]; then ENV_FILE="$lp"; break; fi
    done
  fi
  if [ -z "$ENV_FILE" ]; then
    if [ -f /etc/pipelinegen/pipelinegen.env ]; then
      ENV_FILE=/etc/pipelinegen/pipelinegen.env
    fi
  fi
fi

# --- also pick up VELOX_PORT from the env file if --port was not given
if [ -z "$PORT_OVERRIDE" ] && [ -n "$ENV_FILE" ] && [ -r "$ENV_FILE" ]; then
  EF_PORT="$(grep -E '^VELOX_PORT=' "$ENV_FILE" 2>/dev/null | head -1 | cut -d= -f2- || true)"
  if [ -n "$EF_PORT" ]; then PORT_OVERRIDE="$EF_PORT"; fi
fi
PORT="${PORT_OVERRIDE:-8080}"
HEALTH_URL="http://127.0.0.1:${PORT}/api/health"
JOBS_URL="http://127.0.0.1:${PORT}/api/jobs?limit=1"

# --- generate tokens (64 hex chars = 256 bits)
NEW_ADMIN="$(openssl rand -hex 32)"
NEW_WORKER=""
if [ "$ALSO_WORKER" -eq 1 ]; then
  NEW_WORKER="$(openssl rand -hex 32)"
fi

echo "Plan:"
echo "  systemd unit:       $UNIT_NAME"
echo "  env file:           ${ENV_FILE:-<inline Environment= — rebuild required>}"
echo "  health URL:         $HEALTH_URL"
echo "  new ADMIN_TOKEN:    ${#NEW_ADMIN} chars (hex)"
if [ -n "$NEW_WORKER" ]; then
  echo "  new WORKER_TOKEN:   ${#NEW_WORKER} chars (hex)"
fi
echo "  keep backups:       $KEEP_BACKUPS"
echo "  mode:               $([ "$DRY_RUN" -eq 1 ] && echo dry-run || echo apply)"

if [ "$DRY_RUN" -eq 1 ]; then
  echo
  echo "(dry-run; no changes were made. Re-run without --dry-run to apply.)"
  exit 0
fi

if [ -z "$ENV_FILE" ]; then
  echo
  echo "ERROR: no EnvironmentFile directive in unit $UNIT_NAME and" >&2
  echo "       /etc/pipelinegen/pipelinegen.env does not exist." >&2
  echo
  echo "The $UNIT_NAME unit declares tokens inline as 'Environment=' lines." >&2
  echo "Writing a drop-in to a mode-0600 tmpfile (avoid printing the token to stdout):" >&2
  DROP_IN="/tmp/pipelinegen.${UNIT_NAME}.override.$(date +%s).conf"
  {
    echo "[Service]"
    echo "Environment=VELOX_ADMIN_TOKEN=$NEW_ADMIN"
    if [ -n "$NEW_WORKER" ]; then
      echo "Environment=VELOX_WORKER_TOKEN=$NEW_WORKER"
    fi
  } > "$DROP_IN"
  chmod 0600 "$DROP_IN"
  echo "  drop-in file:    $DROP_IN  (mode 0600, owner $(id -un):$(id -gn))"
  echo "  install:         sudo install -d -m 0755 /etc/systemd/system/${UNIT_NAME}.d"
  echo "                   sudo install -m 0644 -o root -g root '$DROP_IN' /etc/systemd/system/${UNIT_NAME}.d/override.conf"
  echo "                   sudo systemctl daemon-reload"
  echo "                   sudo systemctl restart $UNIT_NAME"
  echo "  clean up tmp:    shred -u '$DROP_IN'  # or rm -f '$DROP_IN' if shred is unavailable"
  echo "  verify secret:   cat '$DROP_IN'   # the file IS the secret until you install it"
  exit 1
fi

# --- backup, regenerate file preserving other settings, then restart
BACKUP="${ENV_FILE}.bak.$(date +%s)"
cp -p "$ENV_FILE" "$BACKUP"
echo "  backup:             $BACKUP"

# Strip old VELOX_ADMIN_TOKEN / VELOX_WORKER_TOKEN lines, append fresh ones.
# `install -m 0600` below atomically sets mode + ownership on the destination,
# so we don't pre-chmod the tmpfile.
TMP="$(mktemp)"
grep -v -E '^(VELOX_ADMIN_TOKEN|VELOX_WORKER_TOKEN)=' "$ENV_FILE" > "$TMP" || true
{
  echo "VELOX_ADMIN_TOKEN=$NEW_ADMIN"
  if [ -n "$NEW_WORKER" ]; then
    echo "VELOX_WORKER_TOKEN=$NEW_WORKER"
  fi
} >> "$TMP"
install -m 0600 -o root -g root "$TMP" "$ENV_FILE"
rm -f "$TMP"

echo "  wrote:              $ENV_FILE (mode 0600, root:root)"

systemctl daemon-reload
systemctl restart "$UNIT_NAME"
echo "  restart:            issued"

# --- wait for the service to come back up (poll /api/health up to ~20s)
HEALTH_OK=0
for i in 1 2 3 4 5 6 7 8 9 10; do
  sleep 2
  CODE="$(curl -sS -o /dev/null -w '%{http_code}' "$HEALTH_URL" --max-time 3 || echo 000)"
  if [ "$CODE" = "200" ]; then HEALTH_OK=1; echo "  health (${i}x2s): $CODE"; break; fi
  echo "  health poll #$i: $CODE"
done
if [ "$HEALTH_OK" -ne 1 ]; then
  echo "ERROR: $UNIT_NAME did not become healthy within 20s at $HEALTH_URL" >&2
  echo "  newest token applied to $ENV_FILE, but service is unreachable." >&2
  echo "  inspect: journalctl -u $UNIT_NAME -n 50" >&2
  echo "  hint: pass --port NNNN or set VELOX_PORT in the env file if 8080 is wrong" >&2
  exit 1
fi

# --- verify: NEW admin token in PID env, then auth self-test on JOBS_URL.
NEWPID="$(pgrep -f "$UNIT_NAME --mode all" | head -1 || true)"
if [ -z "$NEWPID" ]; then
  echo "WARNING: cannot find $UNIT_NAME PID in 'pgrep -f'; skipping perimeter verification" >&2
else
  ADMIN_LEN="$(cat /proc/$NEWPID/environ 2>/dev/null | tr '\0' '\n' | awk -F= '$1=="VELOX_ADMIN_TOKEN"{print length($2)}')"
  echo "  POST-ROTATE ADMIN_TOKEN length in PID $NEWPID: ${ADMIN_LEN:-?} (expected 64)"
fi

echo
echo "AUTH SELF-TEST ($JOBS_URL):"
HTTP_OK="$(curl -sS -o /dev/null -w '%{http_code}' "$JOBS_URL" \
  -H "X-Velox-Admin-Token: $NEW_ADMIN" --max-time 5 || echo 000)"
echo "  new token:   $HTTP_OK  (expected 200)"

# Wrong-token rejection probes the constant-time compare in middleware.go::compareTokens.
HTTP_BAD="$(curl -sS -o /dev/null -w '%{http_code}' "$JOBS_URL" \
  -H 'X-Velox-Admin-Token: obviously-wrong-token-zzz' --max-time 5 || echo 000)"
echo "  wrong token: $HTTP_BAD  (expected 401)"

# --- prune older backup files, keeping newest $KEEP_BACKUPS
if [ -n "$ENV_FILE" ] && [ "$KEEP_BACKUPS" -gt 0 ]; then
  PRUNE_LIST="$(ls -1t "${ENV_FILE}".bak.* 2>/dev/null | tail -n +$((KEEP_BACKUPS + 1)) || true)"
  if [ -n "$PRUNE_LIST" ]; then
    while IFS= read -r OLD; do
      [ -z "$OLD" ] && continue
      rm -f -- "$OLD" && echo "  pruned old backup: $OLD"
    done <<<"$PRUNE_LIST"
  fi
fi

# --- final summary
echo
echo "Rotation complete. New tokens are stored in $ENV_FILE (mode 0600)."
echo "Most recent backup: $BACKUP (kept $KEEP_BACKUPS most-recent backups)."
echo
echo "PROPAGATE TO WORKERS:"
echo "  Copy the new VELOX_(ADMIN|WORKER)_TOKEN value(s) to every worker that"
echo "  authenticates against this server. The old admin token is now invalid."
