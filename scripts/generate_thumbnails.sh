#!/bin/bash
# Generate thumbnails for all media items across all databases
# Usage: bash generate_thumbnails.sh

set -e

DB_DIR="data"
THUMBS_DIR="$DB_DIR/assets/thumbnails"
LOG_FILE="$DB_DIR/thumbnail_generation.log"

echo "=== Thumbnail Generation Script ===" | tee -a "$LOG_FILE"
echo "Started at: $(date)" | tee -a "$LOG_FILE"

# Create thumbnails directory
mkdir -p "$THUMBS_DIR"

# Function to generate thumbnail for a video file
generate_video_thumbnail() {
    local file_path="$1"
    local output_path="$2"
    
    if [ ! -f "$file_path" ]; then
        echo "  [SKIP] File not found: $file_path" | tee -a "$LOG_FILE"
        return 1
    fi
    
    # Check if ffmpeg is available
    if ! command -v ffmpeg &> /dev/null; then
        echo "  [ERROR] ffmpeg not found" | tee -a "$LOG_FILE"
        return 1
    fi
    
    # Extract frame at 1 second
    ffmpeg -i "$file_path" -ss 00:00:01 -vframes 1 -q:v 2 "$output_path" -y 2>>"$LOG_FILE"
    
    if [ -f "$output_path" ]; then
        echo "  [OK] Generated: $output_path" | tee -a "$LOG_FILE"
        return 0
    else
        echo "  [ERROR] Failed to generate: $output_path" | tee -a "$LOG_FILE"
        return 1
    fi
}

# Function to get Drive thumbnail URL
get_drive_thumbnail() {
    local drive_link="$1"
    local file_id=$(echo "$drive_link" | grep -oP '(?<=/d/)[^/]+')
    if [ -n "$file_id" ]; then
        echo "https://drive.google.com/thumbnail?id=${file_id}&sz=w800-h600"
    fi
}

# Process artlist.db
echo -e "\n--- Processing artlist.db ---" | tee -a "$LOG_FILE"
sqlite3 "$DB_DIR/artlist.db.sqlite" "SELECT id, drive_file_id, local_path FROM clips;" 2>/dev/null | while IFS='|' read -r id drive_file_id local_path; do
    echo "Processing clip: $id" | tee -a "$LOG_FILE"
    
    thumb_url=""
    
    # If drive_file_id is available, use Drive thumbnail
    if [ -n "$drive_file_id" ]; then
        thumb_url="https://drive.google.com/thumbnail?id=${drive_file_id}&sz=w800-h600"
        sqlite3 "$DB_DIR/artlist.db.sqlite" "UPDATE clips SET thumb_url = '$thumb_url' WHERE id = '$id';"
        echo "  [DB] Updated thumb_url for $id: $thumb_url" | tee -a "$LOG_FILE"
    # Try to generate from local file
    elif [ -n "$local_path" ] && [ -f "$local_path" ]; then
        thumb_path="$THUMBS_DIR/artlist_${id}.jpg"
        generate_video_thumbnail "$local_path" "$thumb_path"
        if [ -f "$thumb_path" ]; then
            thumb_url="/assets/thumbnails/artlist_${id}.jpg"
            sqlite3 "$DB_DIR/artlist.db.sqlite" "UPDATE clips SET thumb_url = '$thumb_url' WHERE id = '$id';"
            echo "  [DB] Updated thumb_url for $id" | tee -a "$LOG_FILE"
        fi
    fi
done

# Process clips.db (YouTube clips)
echo -e "\n--- Processing clips.db ---" | tee -a "$LOG_FILE"
sqlite3 "$DB_DIR/clips.db.sqlite" "SELECT id, drive_file_id, local_path FROM clips;" 2>/dev/null | while IFS='|' read -r id drive_file_id local_path; do
    echo "Processing clip: $id" | tee -a "$LOG_FILE"
    
    thumb_url=""
    
    # If drive_file_id is available, use Drive thumbnail
    if [ -n "$drive_file_id" ]; then
        thumb_url="https://drive.google.com/thumbnail?id=${drive_file_id}&sz=w800-h600"
        sqlite3 "$DB_DIR/clips.db.sqlite" "UPDATE clips SET thumb_url = '$thumb_url' WHERE id = '$id';"
        echo "  [DB] Updated thumb_url for $id: $thumb_url" | tee -a "$LOG_FILE"
    # Try to generate from local file
    elif [ -n "$local_path" ] && [ -f "$local_path" ]; then
        thumb_path="$THUMBS_DIR/youtube_${id}.jpg"
        generate_video_thumbnail "$local_path" "$thumb_path"
        if [ -f "$thumb_path" ]; then
            thumb_url="/assets/thumbnails/youtube_${id}.jpg"
            sqlite3 "$DB_DIR/clips.db.sqlite" "UPDATE clips SET thumb_url = '$thumb_url' WHERE id = '$id';"
            echo "  [DB] Updated thumb_url for $id" | tee -a "$LOG_FILE"
        fi
    fi
done

# Process stock.db
echo -e "\n--- Processing stock.db ---" | tee -a "$LOG_FILE"
sqlite3 "$DB_DIR/stock.db.sqlite" "SELECT id, drive_file_id, local_path FROM clips;" 2>/dev/null | while IFS='|' read -r id drive_file_id local_path; do
    echo "Processing clip: $id" | tee -a "$LOG_FILE"
    
    thumb_url=""
    
    # If drive_file_id is available, use Drive thumbnail
    if [ -n "$drive_file_id" ]; then
        thumb_url="https://drive.google.com/thumbnail?id=${drive_file_id}&sz=w800-h600"
        sqlite3 "$DB_DIR/stock.db.sqlite" "UPDATE clips SET thumb_url = '$thumb_url' WHERE id = '$id';"
        echo "  [DB] Updated thumb_url for $id: $thumb_url" | tee -a "$LOG_FILE"
    # Try to generate from local file
    elif [ -n "$local_path" ] && [ -f "$local_path" ]; then
        thumb_path="$THUMBS_DIR/stock_${id}.jpg"
        generate_video_thumbnail "$local_path" "$thumb_path"
        if [ -f "$thumb_path" ]; then
            thumb_url="/assets/thumbnails/stock_${id}.jpg"
            sqlite3 "$DB_DIR/stock.db.sqlite" "UPDATE clips SET thumb_url = '$thumb_url' WHERE id = '$id';"
            echo "  [DB] Updated thumb_url for $id" | tee -a "$LOG_FILE"
        fi
    fi
done

# Process images.db
echo -e "\n--- Processing images.db ---" | tee -a "$LOG_FILE"
sqlite3 "$DB_DIR/images.db.sqlite" "SELECT id, drive_file_id, local_path, source_url FROM images;" 2>/dev/null | while IFS='|' read -r id drive_file_id local_path source_url; do
    echo "Processing image: $id" | tee -a "$LOG_FILE"
    
    thumb_url=""
    
    # If drive_file_id is available, use Drive thumbnail
    if [ -n "$drive_file_id" ]; then
        thumb_url="https://drive.google.com/thumbnail?id=${drive_file_id}&sz=w800-h600"
        sqlite3 "$DB_DIR/images.db.sqlite" "UPDATE images SET thumb_url = '$thumb_url' WHERE id = '$id';"
        echo "  [DB] Updated thumb_url for $id: $thumb_url" | tee -a "$LOG_FILE"
    # Try to generate from local file
    elif [ -n "$local_path" ] && [ -f "$local_path" ]; then
        thumb_path="$THUMBS_DIR/images_${id}.jpg"
        cp "$local_path" "$thumb_path" 2>/dev/null && echo "  [OK] Copied image: $thumb_path" | tee -a "$LOG_FILE"
        if [ -f "$thumb_path" ]; then
            thumb_url="/assets/thumbnails/images_${id}.jpg"
            sqlite3 "$DB_DIR/images.db.sqlite" "UPDATE images SET thumb_url = '$thumb_url' WHERE id = '$id';"
            echo "  [DB] Updated thumb_url for $id" | tee -a "$LOG_FILE"
        fi
    # Try source_url for Drive links
    elif [ -n "$source_url" ] && [[ "$source_url" == *"drive.google.com"* ]]; then
        drive_thumb=$(get_drive_thumbnail "$source_url")
        if [ -n "$drive_thumb" ]; then
            sqlite3 "$DB_DIR/images.db.sqlite" "UPDATE images SET thumb_url = '$drive_thumb' WHERE id = '$id';"
            echo "  [DB] Updated thumb_url (Drive) for $id: $drive_thumb" | tee -a "$LOG_FILE"
        fi
    fi
done

echo -e "\n=== Thumbnail Generation Complete ===" | tee -a "$LOG_FILE"
echo "Completed at: $(date)" | tee -a "$LOG_FILE"
echo "Log file: $LOG_FILE"
