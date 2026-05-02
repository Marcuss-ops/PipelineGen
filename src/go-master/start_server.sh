#!/bin/bash
cd /home/pierone/Pyt/VeloxEditing/refactored/src/go-master
nohup ./server_bin > server.log 2>&1 < /dev/null &
echo "Server started with PID: $!"
