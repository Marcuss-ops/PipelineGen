#!/bin/bash
set -e
cd /home/pierone/Pyt/VeloxEditing/refactored/src/go-master
echo "Building..."
go build -o main ./cmd/server
echo "Build completed at $(date)"
echo "Stopping old server..."
pkill -9 -f "./main" 2>/dev/null || true
sleep 1
echo "Starting new server..."
nohup ./main > server.log 2>&1 &
sleep 2
if lsof -i :8080 | grep -q LISTEN; then
    echo "Server is running"
else
    echo "Server failed to start"
    tail -20 server.log
fi
