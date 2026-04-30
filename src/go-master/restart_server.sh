#!/bin/bash
pkill -9 -f "./main" 2>/dev/null
sleep 1
cd /home/pierone/Pyt/VeloxEditing/refactored/src/go-master
go build -o main ./cmd/server && echo "Build done"
nohup ./main > server.log 2>&1 &
sleep 2
lsof -i :8080 | grep LISTEN && echo "Server running" || echo "Server not running"
