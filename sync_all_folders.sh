#!/bin/bash

# Bulk sync all Google Drive folders to the database

FOLDERS=(
  "1NUWT1bont3RvIYHLaLdFJs9fTammbX4e"
  "1TbuSwERv1h7MPWGdNH7vTGHE19AGk9Dq"
  "1GFDgpznZIJozvZg7ZoIH6CJHlL2CZy8f"
  "1E0oJDJf1MZkNiORX3Yb_9v2eM5QfHh1A"
  "12ncviAMoZCl1qW2ZIay5BI0N-5R7aROD"
  "16vLXqbrYYM0iyBxdFtb7WX6Watyd5kF2"
  "1tzfqvPsk3pu1EeDKvsXPBEvh_Yqi1AIQ"
  "19GpEkU7W-lNudqqIPirSs4zs8qic8aSi"
  "1_rph9DatS4DpqhRwY1xPT1uqPAl6AMwb"
)

cd /home/pierone/Pyt/VeloxEditing/refactored/src/go-master

# Set credentials path
export VELOX_CREDENTIALS_FILE="/home/pierone/Pyt/VeloxEditing/refactored/config/credentials.json"
export VELOX_TOKEN_FILE="/home/pierone/Pyt/VeloxEditing/refactored/token.json"

for folder_id in "${FOLDERS[@]}"; do
  echo "Syncing folder: $folder_id"
  go run cmd/sync_drive/main.go "$folder_id" "stock_drive"
  echo "---"
done

echo "All folders synced!"
