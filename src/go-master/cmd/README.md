# CLI Utilities (`cmd/`)

This directory contains various standalone tools and workers that support the main Velox server.

## Core Services
- **`server/`**: The main Go Master API server.
- **`harvester/`**: (Referenced in GEMINI.md) Service for IO-bound content harvesting.
- **`downloader/`**: (Referenced in GEMINI.md) Service for parallel video fetching.

## Ingestion & Sync Tools
- **`sync_drive/`**: Syncs Google Drive folders into the SQLite database. Supports recursive traversal and stores metadata for video clips.
- **`artlist_import/`**: Specialized importer that reads from the Node Scraper's SQLite database (`artlist_videos.db`) and upserts clips into the main Velox database.
- **`sync_drive_content/`**: Enhanced sync tool for Drive content.
- **`sync_images/`**: Specialized tool for syncing image assets into the `images.db.sqlite` database.

## Migration & Data Management
- **`migrate_json_to_sql/`**: A critical utility for transitioning from the legacy JSON-based storage (`clip_index.json`, `artlist_stock_index.json`) to the new SQLite-based repository.
- **`indexer/`**: Re-indexes assets and updates tags or metadata.

## Test & Debugging
- **`test_dbs/`**: Utility to verify database connectivity and integrity.
- **`test_images/`**: Verification tool for image processing and storage.
- **`test_lebron/`**: Specialized test script (likely for a specific project/video type).

## Usage Note
Most of these tools expect the `VELOX_CONFIG` or standard configuration paths to be set. Always check the specific `main.go` for required flags or environment variables.

Example running a migration:
```bash
go run cmd/migrate_json_to_sql/main.go -json ./data/clip_index.json -db-dir ./data
```
