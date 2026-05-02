#!/bin/bash
export VELOX_ARTLIST_ROOT_FOLDER="1OAAf5dawAppdopsgCq1yHFGPUXCI9Vbk"
cd /home/pierone/Pyt/VeloxEditing/refactored/src/go-master
nohup ./server_bin > server.log 2>&1 &
echo "Server started with PID: $!"
disown
