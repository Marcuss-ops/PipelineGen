# AGENTS.md - PipelineGen System Documentation

## Overview
PipelineGen is a Go-based backend service that manages media processing pipelines for YouTube clips and Artlist assets. It runs as a systemd service on port 8080.

## Documentation Map

- **This file (AGENTS.md)**: Critical rules and instructions for all agents
- **docs/archive/sqlite-databases.md**: Complete database schema, boundaries, and migration strategy
- **README.md**: Project structure and architecture overview
- **PROJECT_GUIDE.md**: Italian language getting started guide

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
  - `artlist.db.sqlite`: clips (Artlist, with `search_text` TEXT, `embedding_json` TEXT), clip_folders, artlist_runs
  - `images.db.sqlite`: (vuoto o image tables)
  - `voiceover.db.sqlite`: (vuoto o voiceover tables)
  - `jobs.db.sqlite`: jobs, job_events

## Architecture

### Core Components
- **Server**: Go binary (`pipelinegen`) using Gin web framework
- **Database**: SQLite with WAL mode
  - `velox.db.sqlite` - Main database (clips, channels, monitored sources)
  - `jobs.db.sqlite` - Job queue database
  - `artlist.db.sqlite` - Artlist scraper database, stores clip metadata updated by clipindexer
- **Storage**: Google Drive integration for clip uploads
- **Workers**: 2 background job workers for async processing

### Key Services
- **Artlist Service**: Search, download, and upload Artlist assets
- **YouTube Clip Service**: Extract and process YouTube clips
- **Job Service**: Queue and process background jobs
- **Drive Destination Service**: Manage Google Drive folders and uploads
- **Clipindexer Service**: Generates `search_text` and `embedding_json` metadata for Artlist clips via `scripts/index_clips.py`. Integrates Go service with Python script, passes database path/clip details, handles `None` tags, and updates `artlist.db.sqlite`.

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

### Clipindexer Testing
Test the Python script manually:
```bash
python3 scripts/index_clips.py --db data/artlist.db.sqlite --clip-id <CLIP_ID>
```
Verify metadata updates:
```bash
sqlite3 data/artlist.db.sqlite "SELECT search_text, embedding_json FROM clips WHERE id = <CLIP_ID>;"
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

4. **Clipindexer not passing database path to Python script**
   - Fixed: Added `dbPath` field to clipindexer service, updated `IndexClip` to pass `--db` argument to `scripts/index_clips.py`
   - Fixed: Added `Path()` method to `SQLiteDB` to expose database file path

5. **Python script `index_clips.py` failing on `None` tags**
   - Fixed: Added try-except blocks to default to empty list for invalid/missing tags
   - Fixed: Updated script to accept `--clip-id`, `--clip-name`, `--clip-path` arguments from Go service

6. **Numpy compatibility conflicts from `tts` and `fish-speech` packages**
   - Fixed: Uninstalled `tts` (0.22.0) and `fish-speech` (0.1.0) Python packages, resolving version conflicts with `sentence-transformers` and `spacy`

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

## Migration Status (Brutal Care Plan)

### Completed
- ✅ Eliminated `internal/service/assetpipeline/` thin wrapper (Service struct removed, using Finalizer directly)
- ✅ Migrated `workflowrunner.results` from in-memory maps to job system
- ✅ Migrated `assetdestination.Resolver` to `internal/core/destination.Resolver`
- ✅ Migrated `mediaasset.Processor` to `internal/core/processor.Processor` (adapters created)
- ✅ Fixed Go toolchain corruption (Go 1.25.9 installed to `/usr/local/go`)
- ✅ Merged `internal/core/media/models.go` into `model.go`
- ✅ Removed deprecated `api-cron` mode from server
- ✅ Fixed import paths (`internal/pkg/` → `pkg/`)
- ✅ Fixed clipindexer service integration with `scripts/index_clips.py` (added `--db` argument passing, updated Python script to handle `None` tags and accept clip-specific arguments)
- ✅ Added `Path()` method to `SQLiteDB` struct to expose database file path for clipindexer
- ✅ Resolved numpy compatibility conflicts by uninstalling `tts` (Coqui TTS) and `fish-speech` Python packages
- ✅ Committed and pushed clipindexer fixes to `origin/main` (commit `88bcef3`)

### Pending
- Consolidate `internal/core/media/` - verify unified models work
- Migrate harvester/catalog sync/db backup from cron to job system (cron already removed, verify)
- Remove duplicates and adapt all modules to canonical contracts
- CI checks: `scripts/ci-architectural-checks.sh` must block violations

## Core Contracts

All modules must use canonical contracts in `internal/core/`:
- `core/destination.Resolver` - adapter in `service/assetdestination/adapter.go`
- `core/processor.Processor` - adapter in `service/mediaasset/adapter.go`
- All long-running operations must use `internal/service/jobs/` system

## File Structure
```
src/go-master/
├── cmd/server/main.go          # Main entry point
├── internal/
│   ├── core/                  # Canonical contracts (destination, processor, media, jobs)
│   ├── api/handlers/          # HTTP handlers
│   ├── service/               # Business logic
│   │   ├── artlist/          # Artlist pipeline
│   │   ├── clipindexer/      # Clip metadata indexing (integrates with Python script)
│   │   ├── jobs/             # Job queue system
│   │   ├── mediaasset/       # Media processing (adapter pattern)
│   │   ├── assetdestination/ # Destination resolver (adapter pattern)
│   │   └── youtubeclip/      # YouTube processing
│   └── storage/              # Database connections
├── data/                      # SQLite databases (gitignored)
├── pipelinegen                # Compiled binary (gitignored)
└── AGENTS.md                  # This file
```
