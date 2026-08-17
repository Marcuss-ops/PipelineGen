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
export VELOX_LEXICON_ROOT
set +a
exec "$DIR/bin/pipelinegen" --mode all
