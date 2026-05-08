#!/bin/bash
# Generate thumbnails for all media items across all databases
# Usage: bash generate_thumbnails_v2.sh

set -e

DB_DIR="data"
THUMBS_DIR="$DB_DIR/assets/thumbnails"
LOG_FILE="$DB_DIR/thumbnail_generation_v2.log"

echo "=== Thumbnail Generation Script v2 ===" | tee -a "$LOG_FILE"
echo "Started at: $(date)" | tee -a "$LOG_FILE"

# Create thumbnails directory
mkdir -p "$THUMBS_DIR"

# Function to extract file ID from various Drive URL formats
extract_file_id() {
    local url="$1"
    # Try to extract from /file/d/ID/view format
    local file_id=$(echo "$url" | grep -oP '(?<=/file/d/)[^/]+' 2>/dev/null)
    if [ -n "$file_id" ]; then
        echo "$file_id"
        return 0
    fi
    # Try to extract from ?id=ID format
    file_id=$(echo "$url" | grep -oP '(?<=?id=)[^&]+' 2>/dev/null)
    if [ -n "$file_id" ]; then
        echo "$file_id"
        return 0
    fi
    # Try to extract from /d/ID/ format
    file_id=$(echo "$url" | grep -oP '(?<=/d/)[^/]+' 2>/dev/null)
    if [ -n "$file_id" ]; then
        echo "$file_id"
        return 0
    fi
    echo ""
}

# Function to generate thumbnail for a video file
generate_video_thumbnail() {
    local file_path="$1"
    local output_path="$2"
    
    if [ ! -f "$file_path" ]; then
        echo "  [SKIP] File not found: $file_path" | tee -a "$LOG_FILE"
        return 1
    fi
    
    if ! command -v ffmpeg &> /dev/null; then
        echo "  [ERROR] ffmpeg not found" | tee -a "$LOG_FILE"
        return 1
    fi
    
    ffmpeg -i "$file_path" -ss 00:00:01 -vframes 1 -q:v 2 "$output_path" -y 2>>"$LOG_FILE"
    
    if [ -f "$output_path" ]; then
        echo "  [OK] Generated: $output_path" | tee -a "$LOG_FILE"
        return 0
    else
        echo "  [ERROR] Failed to generate: $output_path" | tee -a "$LOG_FILE"
        return 1
    fi
}

# Process artlist.db
echo -e "\n--- Processing artlist.db ---" | tee -a "$LOG_FILE"
sqlite3 "$DB_DIR/artlist.db.sqlite" "SELECT id, drive_file_id, download_link, drive_link, local_path FROM clips;" 2>/dev/null | while IFS='|' read -r id drive_file_id download_link drive_link local_path; do
    echo "Processing clip: $id" | tee -a "$LOG_FILE"
    
    thumb_url=""
    file_id=""
    
    # Try drive_file_id first
    if [ -n "$drive_file_id" ]; then
        file_id="$drive_file_id"
    # Try to extract from download_link
    elif [ -n "$download_link" ]; then
        file_id=$(extract_file_id "$download_link")
    # Try to extract from drive_link (if it's a file link, not folder)
    elif [ -n "$drive_link" ]; then
        file_id=$(extract_file_id "$drive_link")
    fi
    
    if [ -n "$file_id" ]; then
        thumb_url="https://drive.google.com/thumbnail?id=${file_id}&sz=w800-h600"
        sqlite3 "$DB_DIR/artlist.db.sqlite" "UPDATE clips SET thumb_url = '$thumb_url' WHERE id = '$id';"
        echo "  [DB] Updated thumb_url for $id: $thumb_url" | tee -a "$LOG_FILE"
    # Try to generate from local file
    elif [ -n "$local_path" ] && [ -f "$local_path" ]; then
        thumb_path="$THUMBS_DIR/artlist_${id}.jpg"
        if generate_video_thumbnail "$local_path" "$thumb_path"; then
            thumb_url="/assets/thumbnails/artlist_${id}.jpg"
            sqlite3 "$DB_DIR/artlist.db.sqlite" "UPDATE clips SET thumb_url = '$thumb_url' WHERE id = '$id';"
            echo "  [DB] Updated thumb_url (local) for $id" | tee -a "$LOG_FILE"
        fi
    else
        echo "  [SKIP] No Drive file ID or local path for $id" | tee -a "$LOG_FILE"
    fi
done

# Process clips.db (YouTube clips)
echo -e "\n--- Processing clips.db ---" | tee -a "$LOG_FILE"
sqlite3 "$DB_DIR/clips.db.sqlite" "SELECT id, drive_file_id, download_link, drive_link, local_path FROM clips;" 2>/dev/null | while IFS='|' read -r id drive_file_id download_link drive_link local_path; do
    echo "Processing clip: $id" | tee -a "$LOG_FILE"
    
    thumb_url=""
    file_id=""
    
    # Try drive_file_id first
    if [ -n "$drive_file_id" ]; then
        file_id="$drive_file_id"
    # Try to extract from download_link
    elif [ -n "$download_link" ]; then
        file_id=$(extract_file_id "$download_link")
    # Try to extract from drive_link
    elif [ -n "$drive_link" ]; then
        file_id=$(extract_file_id "$drive_link")
    fi
    
    if [ -n "$file_id" ]; then
        thumb_url="https://drive.google.com/thumbnail?id=${file_id}&sz=w800-h600"
        sqlite3 "$DB_DIR/clips.db.sqlite" "UPDATE clips SET thumb_url = '$thumb_url' WHERE id = '$id';"
        echo "  [DB] Updated thumb_url for $id: $thumb_url" | tee -a "$LOG_FILE"
    # Try to generate from local file
    elif [ -n "$local_path" ] && [ -f "$local_path" ]; then
        thumb_path="$THUMBS_DIR/youtube_${id}.jpg"
        if generate_video_thumbnail "$local_path" "$thumb_path"; then
            thumb_url="/assets/thumbnails/youtube_${id}.jpg"
            sqlite3 "$DB_DIR/clips.db.sqlite" "UPDATE clips SET thumb_url = '$thumb_url' WHERE id = '$id';"
            echo "  [DB] Updated thumb_url (local) for $id" | tee -a "$LOG_FILE"
        fi
    else
        echo "  [SKIP] No Drive file ID or local path for $id" | tee -a "$LOG_FILE"
    fi
done

# Process stock.db
echo -e "\n--- Processing stock.db ---" | tee -a "$LOG_FILE"
sqlite3 "$DB_DIR/stock.db.sqlite" "SELECT id, drive_file_id, download_link, drive_link, local_path FROM clips;" 2>/dev/null | while IFS='|' read -r id drive_file_id download_link drive_link local_path; do
    echo "Processing clip: $id" | tee -a "$LOG_FILE"
    
    thumb_url=""
    file_id=""
    
    # Try drive_file_id first
    if [ -n "$drive_file_id" ]; then
        file_id="$drive_file_id"
    # Try to extract from download_link
    elif [ -n "$download_link" ]; then
        file_id=$(extract_file_id "$download_link")
    # Try to extract from drive_link
    elif [ -n "$drive_link" ]; then
        file_id=$(extract_file_id "$drive_link")
    fi
    
    if [ -n "$file_id" ]; then
        thumb_url="https://drive.google.com/thumbnail?id=${file_id}&sz=w800-h600"
        sqlite3 "$DB_DIR/stock.db.sqlite" "UPDATE clips SET thumb_url = '$thumb_url' WHERE id = '$id';"
        echo "  [DB] Updated thumb_url for $id: $thumb_url" | tee -a "$LOG_FILE"
    # Try to generate from local file
    elif [ -n "$local_path" ] && [ -f "$local_path" ]; then
        thumb_path="$THUMBS_DIR/stock_${id}.jpg"
        if generate_video_thumbnail "$local_path" "$thumb_path"; then
            thumb_url="/assets/thumbnails/stock_${id}.jpg"
            sqlite3 "$DB_DIR/stock.db.sqlite" "UPDATE clips SET thumb_url = '$thumb_url' WHERE id = '$id';"
            echo "  [DB] Updated thumb_url (local) for $id" | tee -a "$LOG_FILE"
        fi
    else
        echo "  [SKIP] No Drive file ID or local path for $id" | tee -a "$LOG_FILE"
    fi
done

# Process images.db
echo -e "\n--- Processing images.db ---" | tee -a "$LOG_FILE"
sqlite3 "$DB_DIR/images.db.sqlite" "SELECT id, drive_file_id, source_url, local_path FROM images;" 2>/dev/null | while IFS='|' read -r id drive_file_id source_url local_path; do
    echo "Processing image: $id" | tee -a "$LOG_FILE"
    
    thumb_url=""
    file_id=""
    
    # Try drive_file_id first
    if [ -n "$drive_file_id" ]; then
        file_id="$drive_file_id"
    # Try to extract from source_url
    elif [ -n "$source_url" ]; then
        file_id=$(extract_file_id "$source_url")
    fi
    
    if [ -n "$file_id" ]; then
        thumb_url="https://drive.google.com/thumbnail?id=${file_id}&sz=w800-h600"
        sqlite3 "$DB_DIR/images.db.sqlite" "UPDATE images SET thumb_url = '$thumb_url' WHERE id = '$id';"
        echo "  [DB] Updated thumb_url for $id: $thumb_url" | tee -a "$LOG_FILE"
    # Try to use local file
    elif [ -n "$local_path" ] && [ -f "$local_path" ]; then
        thumb_path="$THUMBS_DIR/images_${id}.jpg"
        cp "$local_path" "$thumb_path" 2>/dev/null && echo "  [OK] Copied image: $thumb_path" | tee -a "$LOG_FILE"
        if [ -f "$thumb_path" ]; then
            thumb_url="/assets/thumbnails/images_${id}.jpg"
            sqlite3 "$DB_DIR/images.db.sqlite" "UPDATE images SET thumb_url = '$thumb_url' WHERE id = '$id';"
            echo "  [DB] Updated thumb_url (local) for $id" | tee -a "$LOG_FILE"
        fi
    else
        echo "  [SKIP] No Drive file ID or local path for $id" | tee -a "$LOG_FILE"
    fi
done

echo -e "\n=== Thumbnail Generation Complete ===" | tee -a "$LOG_FILE"
echo "Completed at: $(date)" | tee -a "$LOG_FILE"
echo "Log file: $LOG_FILE"

# Summary
echo -e "\n--- Summary ---"
echo "artlist.db clips with thumb_url: $(sqlite3 $DB_DIR/artlist.db.sqlite "SELECT COUNT(*) FROM clips WHERE thumb_url != '';")"
echo "clips.db clips with thumb_url: $(sqlite3 $DB_DIR/clips.db.sqlite "SELECT COUNT(*) FROM clips WHERE thumb_url != '';")"
echo "stock.db clips with thumb_url: $(sqlite3 $DB_DIR/stock.db.sqlite "SELECT COUNT(*) FROM clips WHERE thumb_url != '';")"
echo "images.db images with thumb_url: $(sqlite3 $DB_DIR/images.db.sqlite "SELECT COUNT(*) FROM images WHERE thumb_url != '';")"
