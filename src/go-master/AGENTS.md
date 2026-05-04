# AGENTS.md - PipelineGen System Documentation

## Overview
PipelineGen is a Go-based backend service that manages media processing pipelines for YouTube clips and Artlist assets. It runs as a systemd service on port 8080.

## Instructions

- **Non cambiare driver SQLite** (rimanere su `mattn/go-sqlite3`)
- **Non lavorare su FTS5** (il supporto dipende dal driver compilato, usare fallback LIKE)
- **Concentrarsi solo su schema boundaries, diagnostics e test**
- **Ogni database deve avere solo le tabelle necessarie** al servizio che lo usa
- **Non applicare migration generiche a più database se creano tabelle non usate da quel database.**
- Schema desiderato:
  - `velox.db.sqlite`: scripts, monitored_sources, harvester_jobs, media_items, media_files, media_tags, video_metadata, script_stock_matches, video_stats_history, artlist_runs
  - `stock.db.sqlite`: clips (stock), clip_folders (stock)
  - `clips.db.sqlite`: clips (YouTube), clip_folders, segment_embeddings
  - `artlist.db.sqlite`: clips (Artlist), clip_folders, artlist_runs
  - `images.db.sqlite`: (vuoto o image tables)
  - `voiceover.db.sqlite`: (vuoto o voiceover tables)
  - `jobs.db.sqlite`: jobs, job_events

## Architecture

### Core Components
- **Server**: Go binary (`pipelinegen`) using Gin web framework
- **Database**: SQLite with WAL mode
  - `velox.db.sqlite` - Main database (clips, channels, monitored sources)
  - `jobs.db.sqlite` - Job queue database
  - `artlist.db.sqlite` - Artlist scraper database (optional)
- **Storage**: Google Drive integration for clip uploads
- **Workers**: 2 background job workers for async processing

### Key Services
- **Artlist Service**: Search, download, and upload Artlist assets
- **YouTube Clip Service**: Extract and process YouTube clips
- **Job Service**: Queue and process background jobs
- **Drive Destination Service**: Manage Google Drive folders and uploads

## Configuration

### Systemd Service
- **Service name**: `pipelinegen`
- **Service file**: `/etc/systemd/system/pipelinegen.service`
- **Binary path**: `/home/pierone/Pyt/VeloxEditing/refactored/src/go-master/pipelinegen`
- **Run mode**: `--mode all` (starts HTTP server + workers)
- **Port**: `127.0.0.1:8080`

### Database Settings
All SQLite connections should use:
- **Journal mode**: WAL (Write-Ahead Logging)
- **Busy timeout**: 5000ms
- **Connection pool**: Max 5-10 open connections, 2-5 idle
- **Pragmas**: `journal_mode=WAL`, `busy_timeout=5000`, `synchronous=NORMAL`, `cache_size=-2000`

## API Endpoints

### Artlist
- `POST /api/artlist/run` - Start Artlist pipeline for a term
- `GET /api/artlist/runs/:run_id` - Get run status
- `GET /api/artlist/diagnostics` - Check system diagnostics
- `POST /api/artlist/search/live` - Perform live Artlist search

### YouTube Clips
- `POST /api/youtube-clips/extract` - Extract YouTube clips

### Diagnostics
Check system health:
```bash
curl http://127.0.0.1:8080/api/artlist/diagnostics | jq
```

## Common Operations

### Build and Restart Server
```bash
cd /home/pierone/Pyt/VeloxEditing/refactored/src/go-master
go build -o pipelinegen ./cmd/server/
echo "ciao" | sudo -S systemctl restart pipelinegen
```

### Check Service Status
```bash
systemctl status pipelinegen --no-pager -l
```

### View Live Logs
```bash
journalctl -u pipelinegen -f
```

### Check Database Tables
```bash
sqlite3 data/velox.db.sqlite ".tables"
sqlite3 data/jobs.db.sqlite ".schema jobs"
```

## Known Issues & Fixes

### Fixed Issues
1. **Artlist job status endpoint returning "sql: no rows in result set"**
   - Fixed: Corrected column names in `job_adapter.go` (`payload_json`, `result_json`)
   - Fixed: Added `getIntFromResult()` helper to handle JSON float64 integers

2. **SQLite "database is locked" errors**
   - Fixed: Added WAL mode and busy_timeout to `artlistDB` and `jobsDB` connections
   - Fixed: Set proper connection pool limits

3. **Missing `monitored_sources` table**
   - Fixed: Created table with proper schema in `velox.db.sqlite`

### Recurring Issues
1. **Artlist search is slow** (30-50 seconds per search via node-scraper)
2. **Inconsistent SQLite config** - 12+ locations still open DB without WAL/busy_timeout
3. **Binary and scripts in source dir** - Need proper `.gitignore`

## Development Notes

### Adding New Database Tables
When adding new tables to `velox.db.sqlite`, ensure:
- Use proper SQLite types (TEXT, INTEGER, etc.)
- Add indexes for frequently queried columns
- Update the schema documentation here

### Job System
- Jobs are stored in `jobs.db.sqlite`
- Job types: `media.artlist`, `media.youtube_clip`, etc.
- Workers poll for queued jobs every few seconds
- Jobs have max 3 retries by default

### Drive Integration
- Root folder ID: `1OAAf5dawAppdopsgCq1yHFGPUXCI9Vbk`
- Subfolders are created per term/tag
- Dry-run mode simulates uploads without actual Drive operations

## File Structure
```
src/go-master/
├── cmd/server/main.go          # Main entry point
├── internal/
│   ├── api/handlers/          # HTTP handlers
│   ├── service/               # Business logic
│   │   ├── artlist/          # Artlist pipeline
│   │   ├── jobs/             # Job queue system
│   │   └── youtubeclip/      # YouTube processing
│   └── storage/              # Database connections
├── data/                      # SQLite databases (gitignored)
├── pipelinegen                # Compiled binary (gitignored)
└── AGENTS.md                  # This file
```
