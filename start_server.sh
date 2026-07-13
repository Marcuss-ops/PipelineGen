#!/bin/bash
set -a
source .env
export VELOX_FEATURE_IMAGES_ENABLED=false
set +a
exec ./bin/pipelinegen --mode all
