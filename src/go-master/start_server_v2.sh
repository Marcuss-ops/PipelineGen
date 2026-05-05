#!/bin/bash
pkill -f "pipelinegen --mode all" 2>/dev/null
sleep 2
cd /home/pierone/Pyt/VeloxEditing/refactored/src/go-master
export VELOX_FEATURE_YOUTUBE_ENABLED=true
export VELOX_ENABLE_AUTH=false
nohup ./pipelinegen --mode all > /tmp/pipelinegen.log 2>&1 &
echo "Server starting with YouTube enabled and auth disabled"
sleep 3
echo "Testing endpoints..."
curl -s http://127.0.0.1:8080/api/health | jq .
curl -s http://127.0.0.1:8080/api/youtube-clips/folders | jq '.folders | length'
