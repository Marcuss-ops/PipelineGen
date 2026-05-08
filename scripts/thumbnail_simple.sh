#!/bin/bash
# Simple script to generate thumbnails for clips with local files

DB_DIR="data"
THUMBS_DIR="$DB_DIR/assets/thumbnails"
LOG_FILE="$DB_DIR/thumbnail_simple.log"

echo "=== Simple Thumbnail Generation ===" | tee "$LOG_FILE"
echo "Started at: $(date)" | tee -a "$LOG_FILE"

mkdir -p "$THUMBS_DIR"

# Function to generate thumbnail
generate_thumb() {
    local id="$1"
    local local_path="$2"
    local output_path="$THUMBS_DIR/artlist_${id}.jpg"
    
    if [ ! -f "$local_path" ]; then
        echo "  [SKIP] File not found: $local_path" | tee -a "$LOG_FILE"
        return 1
    fi
    
    ffmpeg -i "$local_path" -ss 00:00:01 -vframes 1 -q:v 2 "$output_path" -y 2>>"$LOG_FILE"
    
    if [ -f "$output_path" ]; then
        echo "  [OK] Generated: $output_path" | tee -a "$LOG_FILE"
        sqlite3 "$DB_DIR/artlist.db.sqlite" "UPDATE clips SET thumb_url = '/assets/thumbnails/artlist_${id}.jpg' WHERE id = '$id';"
        echo "  [DB] Updated thumb_url for $id" | tee -a "$LOG_FILE"
        return 0
    else
        echo "  [ERROR] Failed: $output_path" | tee -a "$LOG_FILE"
        return 1
    fi
}

# Process artlist.db clips with local_path
echo -e "\n--- Processing artlist.db (local files) ---" | tee -a "$LOG_FILE"
sqlite3 "$DB_DIR/artlist.db.sqlite" "SELECT id, local_path FROM clips WHERE thumb_url = '' AND local_path != '';" 2>/dev/null | while IFS='|' read -r id local_path; do
    echo "Processing clip: $id" | tee -a "$LOG_FILE"
    generate_thumb "$id" "$local_path"
done

# Process clips.db (YouTube) clips with local_path
echo -e "\n--- Processing clips.db (local files) ---" | tee -a "$LOG_FILE"
sqlite3 "$DB_DIR/clips.db.sqlite" "SELECT id, local_path FROM clips WHERE thumb_url = '' AND local_path != '';" 2>/dev/null | while IFS='|' read -r id local_path; do
    echo "Processing clip: $id" | tee -a "$LOG_FILE"
    generate_thumb "$id" "$local_path"
done

echo -e "\n=== Simple Thumbnail Generation Complete ===" | tee -a "$LOG_FILE"
echo "Completed at: $(date)" | tee -a "$LOG_FILE"

# Summary
echo -e "\n--- Summary ---" | tee -a "$LOG_FILE"
echo "artlist.db clips with thumb_url: $(sqlite3 $DB_DIR/artlist.db.sqlite "SELECT COUNT(*) FROM clips WHERE thumb_url != '';")" | tee -a "$LOG_FILE"
echo "clips.db clips with thumb_url: $(sqlite3 $DB_DIR/clips.db.sqlite "SELECT COUNT(*) FROM clips WHERE thumb_url != '';")" | tee -a "$LOG_FILE"
