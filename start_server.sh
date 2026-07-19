#!/bin/bash
DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"
set -a
source "$DIR/.env"
export VELOX_FEATURE_IMAGES_ENABLED=true
set +a
exec "$DIR/bin/pipelinegen" --mode all
