#!/bin/bash
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
# systemd supplies the canonical auth/port values from its EnvironmentFile.
# Preserve them while loading non-secret local defaults from .env; otherwise
# a repository-local .env would silently override the authentication SSOT.
CANONICAL_ADMIN_TOKEN="${VELOX_ADMIN_TOKEN:-}"
CANONICAL_WORKER_TOKEN="${VELOX_WORKER_TOKEN:-}"
CANONICAL_METRICS_TOKEN="${METRICS_AUTH_TOKEN:-}"
CANONICAL_PORT="${VELOX_PORT:-}"
CANONICAL_PATH="${PATH:-}"
CANONICAL_LD_LIBRARY_PATH="${LD_LIBRARY_PATH:-}"
CANONICAL_WHISPER_DEVICE="${VELOX_WHISPER_DEVICE:-}"
CANONICAL_WHISPER_MODEL="${VELOX_WHISPER_MODEL:-}"
CANONICAL_WHISPER_CUDA_LIB_DIR="${VELOX_WHISPER_CUDA_LIB_DIR:-}"
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
if [[ -n "$CANONICAL_PATH" ]]; then
    PATH="$CANONICAL_PATH"
fi
if [[ -n "$CANONICAL_LD_LIBRARY_PATH" ]]; then
    LD_LIBRARY_PATH="$CANONICAL_LD_LIBRARY_PATH"
fi
if [[ -n "$CANONICAL_WHISPER_DEVICE" ]]; then
    VELOX_WHISPER_DEVICE="$CANONICAL_WHISPER_DEVICE"
fi
if [[ -n "$CANONICAL_WHISPER_MODEL" ]]; then
    VELOX_WHISPER_MODEL="$CANONICAL_WHISPER_MODEL"
fi
if [[ -n "$CANONICAL_WHISPER_CUDA_LIB_DIR" ]]; then
    VELOX_WHISPER_CUDA_LIB_DIR="$CANONICAL_WHISPER_CUDA_LIB_DIR"
fi
# Keep the repository lexicon as the operational default when an inherited
# environment exports an empty override. An empty env value must not erase
# the config.yaml path during a systemd restart.
if [[ -z "${VELOX_LEXICON_ROOT:-}" ]]; then
    VELOX_LEXICON_ROOT="$DIR/config/lexicons"
fi
export VELOX_FEATURE_IMAGES_ENABLED=true
export VELOX_LEXICON_ROOT
set +a
exec "$DIR/bin/pipelinegen" --mode all
