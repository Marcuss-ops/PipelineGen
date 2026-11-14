#!/usr/bin/env bash
# Render the tracked systemd templates (@VELOX_ROOT@ / @VELOX_HOME@ /
# @VELOX_USER@) and install them. The repository never stores a machine's
# absolute paths or its operator user name.
#
# Usage:
#   ./install.sh --dry-run          # show what would be installed
#   ./install.sh                    # install into /etc/systemd/system
#   SYSTEMD_DEST=/tmp/units ./install.sh
#   VELOX_SYSTEMD_NO_RELOAD=1 ...   # render/install without daemon-reload
#
# VELOX_ROOT defaults to this checkout; VELOX_HOME defaults to the target
# user's home; VELOX_USER defaults to the sudo caller (or the current user).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="${VELOX_ROOT:-$(cd "$HERE/../../.." && pwd)}"
if [[ -n "${VELOX_USER:-}" ]]; then
  USER_NAME="$VELOX_USER"
elif [[ -n "${SUDO_USER:-}" ]]; then
  USER_NAME="$SUDO_USER"
else
  USER_NAME="$(id -un)"
fi
if [[ -n "${VELOX_HOME:-}" ]]; then
  HOME_DIR="$VELOX_HOME"
else
  HOME_DIR="$(getent passwd "$USER_NAME" | cut -d: -f6)"
  HOME_DIR="${HOME_DIR:-$HOME}"
fi
DEST="${SYSTEMD_DEST:-/etc/systemd/system}"
DRY_RUN=0

if [[ "${1:-}" == "--dry-run" ]]; then
  DRY_RUN=1
elif [[ $# -gt 0 ]]; then
  echo "usage: $0 [--dry-run]" >&2
  exit 2
fi

render() {
  local src="$1" dst="$2" tmp
  tmp="$(mktemp)"
  sed -e "s|@VELOX_ROOT@|${ROOT}|g" \
      -e "s|@VELOX_HOME@|${HOME_DIR}|g" \
      -e "s|@VELOX_USER@|${USER_NAME}|g" "$src" >"$tmp"
  if [[ "$DRY_RUN" == 1 ]]; then
    echo "[dry-run] $src -> $dst"
    diff -u "$src" "$tmp" || true
  else
    install -D -m 0644 "$tmp" "$dst"
    echo "installed $dst"
  fi
  rm -f "$tmp"
}

for unit in \
  pipelinegen.service \
  pipelinegen-worker.service \
  chronon3d.service \
  chronon3d-b.service; do
  render "$HERE/$unit" "$DEST/$unit"
done

for dir in \
  pipelinegen.service.d \
  pipelinegen-worker.service.d \
  ollama.service.d \
  pipelinegen-embedding-server.service.d; do
  [[ -d "$HERE/$dir" ]] || continue
  for dropin in "$HERE/$dir"/*.conf; do
    [[ -e "$dropin" ]] || continue
    render "$dropin" "$DEST/$dir/$(basename "$dropin")"
  done
done

if [[ "$DRY_RUN" == 0 && "${VELOX_SYSTEMD_NO_RELOAD:-0}" != "1" ]]; then
  systemctl daemon-reload
fi
