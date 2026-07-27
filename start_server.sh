#!/bin/bash
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
# systemd supplies the canonical auth/port values from its EnvironmentFile.
# Preserve them while loading non-secret local defaults from .env; otherwise
# a repository-local .env would silently override the authentication SSOT.
CANONICAL_ADMIN_TOKEN="${VELOX_ADMIN_TOKEN:-}"
CANONICAL_WORKER_TOKEN="${VELOX_WORKER_TOKEN:-}"
CANONICAL_METRICS_TOKEN="${METRICS_AUTH_TOKEN:-}"
CANONICAL_PORT="${VELOX_PORT:-}"
set -a
source "$DIR/.env"
if [[ -n "$CANONICAL_ADMIN_TOKEN" ]]; then
    VELOX_ADMIN_TOKEN="$CANONICAL_ADMIN_TOKEN"
else
    unset VELOX_ADMIN_TOKEN
fi
if [[ -n "$CANONICAL_WORKER_TOKEN" ]]; then
    VELOX_WORKER_TOKEN="$CANONICAL_WORKER_TOKEN"
else
    unset VELOX_WORKER_TOKEN
fi
if [[ -n "$CANONICAL_METRICS_TOKEN" ]]; then
    METRICS_AUTH_TOKEN="$CANONICAL_METRICS_TOKEN"
else
    unset METRICS_AUTH_TOKEN
fi
if [[ -n "$CANONICAL_PORT" ]]; then
    VELOX_PORT="$CANONICAL_PORT"
fi
export VELOX_FEATURE_IMAGES_ENABLED=true
set +a
exec "$DIR/bin/pipelinegen" --mode all
