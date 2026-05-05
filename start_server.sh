#!/bin/bash
pkill -f "pipelinegen" 2>/dev/null
sleep 1
cd /home/pierone/Pyt/VeloxEditing/refactored/src/go-master
export VELOX_FEATURE_YOUTUBE_ENABLED=true
nohup ./pipelinegen --mode all > /tmp/pipelinegen.log 2>&1 &
echo "Server starting, check /tmp/pipelinegen.log for status"
