#!/bin/bash
set -u
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

# Explicit environment wins. The dotenv loader only fills missing development
# defaults and therefore cannot overwrite the systemd EnvironmentFile or a
# caller-provided VELOX_ADMIN_TOKEN.
# shellcheck source=scripts/lib/dotenv.sh
source "$DIR/scripts/lib/dotenv.sh"
load_dotenv_missing "$DIR/.env"
# Keep the repository lexicon as the operational default when an inherited
# environment exports an empty override. An empty env value must not erase
# the config.yaml path during a systemd restart.
if [[ -z "${VELOX_LEXICON_ROOT:-}" ]]; then
    VELOX_LEXICON_ROOT="$DIR/config/lexicons"
fi
export VELOX_FEATURE_IMAGES_ENABLED=true
# Bounded GPU render concurrency. Three persistent Rust runners overlap
# independent NVDEC/NVENC jobs without allowing an unbounded FFmpeg burst;
# override this in the environment for a measured 1..4 worker canary.
if [[ -z "${VELOX_MEDIA_EXECUTION_SLOTS:-}" ]]; then
    VELOX_MEDIA_EXECUTION_SLOTS=3
fi
export VELOX_LEXICON_ROOT
export VELOX_MEDIA_EXECUTION_SLOTS
set +a
exec "$DIR/bin/pipelinegen" --mode "${VELOX_RUN_MODE:-all}"
