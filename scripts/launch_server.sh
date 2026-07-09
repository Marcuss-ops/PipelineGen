#!/usr/bin/env bash
# Launch PipelineGen server fully detached from terminal
set -euo pipefail

cd /home/pierone/src/go-master/projects/Pyt/VeloxEditing/refactored

# Kill any existing server
pkill -9 -f './pipelinegen' 2>/dev/null || true
sleep 1

# Ensure port is free
fuser -k 8000/tcp 2>/dev/null || true
sleep 1

# Launch fully detached via setsid
setsid ./pipelinegen --mode all </dev/null >/tmp/pipelinegen.log 2>&1 &
PID=$!
echo "$PID" > /tmp/pipelinegen.pid
echo "Launched PID=$PID"

# Wait for startup (max 15s)
for i in $(seq 1 15); do
  sleep 1
  HTTP=$(curl -s -o /dev/null -w '%{http_code}' --max-time 2 http://127.0.0.1:8000/health 2>/dev/null || echo "000")
  if [[ "$HTTP" == "200" ]]; then
    echo "Server healthy after ${i}s (HTTP $HTTP)"
    exit 0
  fi
done

echo "WARN: server not healthy after 15s"
tail -5 /tmp/pipelinegen.log
exit 1
