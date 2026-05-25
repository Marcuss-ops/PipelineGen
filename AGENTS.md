# AGENTS.md - PipelineGen System Documentation

## Overview
PipelineGen is a Go-based backend service that manages media processing pipelines for YouTube clips and Artlist assets. It runs as a systemd service on port 8080.

## Documentation Map

- **This file (AGENTS.md)**: Critical rules and instructions for all agents
- **docs/INTELLIGENCE_ROADMAP.md**: Roadmap for advanced AI features and Hybrid Search evolutions
- **docs/archive/sqlite-databases.md**: Complete database schema, boundaries, and migration strategy
- **README.md**: Project structure and architecture overview
- **PROJECT_GUIDE.md**: Italian language getting started guide

## Instructions

- **Non cambiare driver SQLite** (rimanere su `mattn/go-sqlite3`)
- **Non lavorare su FTS5** (il supporto dipende dal driver compilato, usare fallback LIKE)
- **Concentrarsi solo su schema boundaries, diagnostics e test**
- **Ogni database deve avere solo le tabelle necessarie** al servizio che lo usa
- **Non applicare migration generiche a più database se creano tabelle non usate da quel database.**
- Schema attuale (Unificato):
  - `data/velox/velox.db.sqlite`: Generic (scripts, jobs, asset_index, harvester, pipeline_runs)
  - `data/media/media.db.sqlite`: Unified Media (YouTube, Artlist, Stock, Images, Voiceovers)

## Architecture

### Core Components
- **Server**: Go binary (`pipelinegen`) using Gin web framework
- **Rendering Engine**: FFmpeg (via Go media pipeline)
- **Database**: SQLite with WAL mode
  - `data/velox/velox.db.sqlite` - Main database (Scripts, Jobs, Asset Index)
  - `data/media/media.db.sqlite` - Unified Media database (Artlist, YouTube, Stock, Images, Voiceovers)
- **Storage**: Google Drive integration for clip uploads
- **Workers**: 2 background job workers for async processing

### Key Services
- **Artlist Service**: Search, download, and upload Artlist assets
- **YouTube Clip Service**: Extract and process YouTube clips
- **Job Service**: Queue and process background jobs
- **Drive Destination Service**: Manage Google Drive folders and uploads
- **Clipindexer Service**: Generates `search_text` and `embedding_json` metadata for Artlist clips via `scripts/index_clips.py`. Integrates Go service with Python script, passes database path/clip details, handles `None` tags, and updates `media.db.sqlite`.

## Configuration

### Systemd Service
- **Service name**: `pipelinegen`
- **Service file**: `/etc/systemd/system/pipelinegen.service`
- **Binary path**: `/home/pierone/Pyt/Pipeline Gen/pipelinegen`
- **Run mode**: `--mode all` (starts HTTP server + workers)
- **Port**: `127.0.0.1:8080`

### Database Settings
All SQLite connections should use:
- **Journal mode**: WAL (Write-Ahead Logging)
- **Busy timeout**: 5000ms
- **Connection pool**: Max 5-10 open connections, 2-5 idle
- **Pragmas**: `journal_mode=WAL`, `busy_timeout=5000`, `synchronous=NORMAL`, `cache_size=-2000`
- **Mapping**: Le associazioni tra moduli e database sono centralizzate in `internal/storage/db_config.go`.

## API Endpoints

### Artlist
- `POST /api/artlist/run` - Start Artlist pipeline for a term
- `GET /api/artlist/runs/:run_id` - Get run status
- `GET /api/artlist/diagnostics` - Check system diagnostics
- `POST /api/artlist/search/live` - Perform live Artlist search

### YouTube Clips
- `POST /api/clips/process` - Download and process YouTube clips
- `GET /api/clips/info` - Fetch YouTube metadata

### Diagnostics
Check system health:
```bash
curl http://127.0.0.1:8080/api/artlist/diagnostics | jq
```

## Common Operations

### Build and Restart Server
```bash
cd /home/pierone/Pyt/Pipeline\ Gen
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

### NVIDIA AI Image Generation
Test different models:
- **Local NIM**: `python3 scripts/test_nvidia_images.py --model local-nim`
- **Flux 1 Dev**: `python3 scripts/test_nvidia_images.py --model flux-1-dev`
- **Flux 2 Klein**: `python3 scripts/test_nvidia_images.py --model flux-2-klein`

API Endpoint: `POST /api/images/generate/nvidia`
Payload: `{"prompt": "...", "model": "flux-1-dev", "width": 1280, "height": 720}` (options: `local-nim`, `flux-1-dev`, `flux-2-klein`)

Animate Endpoint: `POST /api/images/animate`
Payload: `{"image_hash": "...", "duration": 7}`
Generates a 1080p zoom-out video (MP4) from a stored image.

### Check Database Tables
```bash
sqlite3 data/velox/velox.db.sqlite ".tables"
sqlite3 data/velox/velox.db.sqlite ".schema jobs"
```

### Clipindexer Testing
Test the Python script manually:
```bash
python3 scripts/index_clips.py --db data/media/media.db.sqlite --clip-id <CLIP_ID>
```
Verify metadata updates:
```bash
sqlite3 data/media/media.db.sqlite "SELECT search_text, embedding_json FROM clips WHERE id = <CLIP_ID>;"
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

7. **Inconsistent SQLite configurations**
   - Fixed: Centralized all database access via `storage.OpenSQLiteDB` ensuring WAL mode and `busy_timeout` are applied system-wide.
   - Fixed: Migrated all initialization and schema management to `runAllMigrations` in bootstrap.

8. **Missing models and broken registry wiring**
   - Fixed: Restored `AssetNode` model and fixed type mismatches in API handlers.
   - Fixed: Corrected module registration loop in `registry.go` to handle multiple return values.

### Recurring Issues
1. **Artlist search is slow** (30-50 seconds per search via node-scraper)
2. **Binary and scripts in source dir** - Needs proper `.gitignore` rules
3. **Admin token must be set via `VELOX_ADMIN_TOKEN` env var** - The `config.yaml` in the repo must NOT contain the production token. The server reads from `VELOX_ADMIN_TOKEN` at runtime.
4. **Tests in `internal/media/voiceover/` had compilation errors** - Functions `sanitizeFilename` and `buildVoiceoverID` were `*Service` methods but tests called them as package-level functions. Fixed by removing the receiver. Also fixed `toSlug` trailing-dash bug and path traversal detection in `sanitizeFilename`.
5. **context.Background() in production code** - `cmd/admin/*`, `internal/api/handlers/sources/stock_handler.go`, `internal/repository/catalog/*`, `internal/media/images/service.go`, `internal/media/clipindexer/service.go` still use `context.Background()` instead of propagating request contexts. The stock_handler.go handler was fixed; the others need a larger refactor to add context parameters.
6. **Large files (God Objects)** - `internal/media/images/service.go` (1069 lines), `internal/repository/clips/repository.go` (1066 lines), `internal/media/stockpipeline/service.go` (968 lines) are too large and should be split into smaller modules.
7. **`data/` directory not gitignored per-database** - The whole `data/` dir is gitignored but individual DB backup files at root level (`data/*.bak`) can leak between ignores.
8. **Heavy AI-generated codebase** - ~80% of commits from AI agents. Code works but subtle bugs are harder to diagnose without human oversight. Keep test coverage high and document non-obvious architectural decisions.

### Drive Token Regeneration
If Google Drive authentication fails, regenerate the token:
```bash
python3 scripts/generate_drive_token.py
```
Follow the link, authorize, and paste the code back into the terminal.

## Development Notes

### Adding New Database Tables
When adding new tables to `velox.db.sqlite`, ensure:
- Use proper SQLite types (TEXT, INTEGER, etc.)
- Add indexes for frequently queried columns
- Update the schema documentation here

### Job System
- Jobs are stored in `velox.db.sqlite` (under `jobs` table)
- Job types: `media.artlist`, `media.youtube_clip`, etc.
- Workers poll for queued jobs every few seconds
- Jobs have max 3 retries by default

### Drive Integration
- Root folder ID: `1OAAf5dawAppdopsgCq1yHFGPUXCI9Vbk`
- Subfolders are created per term/tag
- Dry-run mode simulates uploads without actual Drive operations

## Migration Status (Brutal Care Plan)

### Completed
- ✅ Database Unification: All media sources migrated to `media.db.sqlite`
- ✅ Eliminated `internal/service/assetpipeline/` thin wrapper
- ✅ Migrated `workflowrunner.results` from in-memory maps to job system
- ✅ Migrated `assetdestination.Resolver` to `internal/core/destination.Resolver`
- ✅ Migrated `mediaasset.Processor` to `internal/core/processor.Processor`
- ✅ Consolidated `internal/core/media/` - unified models in `model.go`
- ✅ Fixed Go toolchain corruption (Go 1.25.9 installed)
- ✅ Removed deprecated `api-cron` mode from server
- ✅ Fixed import paths (`internal/pkg/` → `pkg/`)
- ✅ Centralized database migrations and connection pooling (WAL/busy_timeout)
- ✅ Migrated harvester/catalog sync/db backup to job system
- ✅ Integrated CI checks: `scripts/ci-architectural-checks.sh` is now executed in the GitHub Actions pipeline

### Pending
- Remove any remaining duplicates in legacy doc folders

## Core Contracts

All modules must use canonical contracts in `internal/core/`:
- `core/destination.Resolver` - adapter in `service/assetdestination/adapter.go`
- `core/processor.Processor` - adapter in `service/mediaasset/adapter.go`
- All long-running operations must use `internal/service/jobs/` system

## File Structure
```
.
├── cmd/server/main.go          # Main entry point
├── cmd/admin/main.go           # One-shot admin and maintenance commands
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
