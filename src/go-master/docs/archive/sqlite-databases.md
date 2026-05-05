# SQLite Databases Architecture

**Status:** ACTIVE - Complete database schema reference

## Overview

The system uses multiple SQLite databases to separate concerns and improve maintainability. Each database has a specific purpose and should only contain tables relevant to that purpose.

## Critical Rules for Future Agents

1. **Never change SQLite driver** - Stay with `mattn/go-sqlite3`
2. **Never implement FTS5** - Driver doesn't support it; use `LIKE` fallback queries
3. **Focus on schema boundaries, diagnostics, and tests**
4. **Each database must have only the tables necessary for its purpose**
5. **Never apply generic migrations to multiple databases if they create tables not used by that database**

## Database Inventory (Current Clean State)

### velox.db.sqlite (Main)
**Purpose**: Core application data, scripts, monitoring, media items.

**Tables**:
- `scripts` - Script definitions
- `monitored_sources` - Sources to monitor
- `harvester_jobs` - Harvester job tracking
- `media_items` - Media items catalog
- `media_files` - Media files
- `media_tags` - Media tags
- `video_metadata` - Video metadata
- `script_stock_matches` - Script-stock matches
- `video_stats_history` - Video stats history
- `artlist_runs` - Artlist pipeline runs
- `schema_migrations` - Migration tracking

**Must NOT contain**: `clips`, `clip_folders`, `clip_tags`, `segment_embeddings`, `indexing_checkpoints`, `script_sections`

---

### stock.db.sqlite (Stock Media)
**Purpose**: Stock footage and clips from stock providers.

**Tables**:
- `clips` - Stock clips (stock-specific schema)
- `clip_folders` - Stock folders
- `schema_migrations` - Migration tracking

**Must NOT contain**: `media_files`, `media_items`, `media_tags`, `segment_embeddings`

---

### clips.db.sqlite (YouTube Clips)
**Purpose**: YouTube clips extracted via youtubeclip service.

**Tables**:
- `clips` - YouTube clips (YouTube-specific schema)
- `clip_folders` - YouTube clip folders
- `segment_embeddings` - Timeline cache (appropriate here)
- `schema_migrations` - Migration tracking

**Migrations location**: `internal/repository/clips/migrations/`

---

### artlist.db.sqlite (Artlist Media)
**Purpose**: Artlist assets downloaded via artlist service.

**Tables**:
- `clips` - Artlist clips (Artlist-specific schema)
- `clip_folders` - Artlist folders
- `artlist_runs` - Pipeline run tracking
- `schema_migrations` - Migration tracking

**Must NOT contain**: `segment_embeddings`

---

### images.db.sqlite (Images)
**Purpose**: Image assets (placeholder for future use).

**Tables**:
- `schema_migrations` - Migration tracking

**Target**: Add image-specific tables when feature is implemented.

---

### voiceover.db.sqlite (Voiceovers)
**Purpose**: Voiceover audio files (placeholder for future use).

**Tables**:
- `schema_migrations` - Migration tracking

**Target**: Add voiceover-specific tables when feature is implemented.

---

### jobs.db.sqlite (Job Queue)
**Purpose**: Background job processing.

**Tables**:
- `jobs` - Job definitions
- `job_events` - Job event log
- `schema_migrations` - Migration tracking

---

## Migration Architecture

### Migration Directory Structure
```
internal/repository/
├── clips/migrations/          # For clips.db.sqlite
│   ├── clips_001_create_core_tables.sql
│   ├── clips_002_create_fts.sql.disabled  # FTS5 disabled (driver lacks support)
│   └── clips_003_create_segment_embeddings.sql
├── catalog/                   # No migrations (uses other repos)
└── ...

migrations/                    # For velox.db.sqlite and jobs.db.sqlite
├── sqlite/
│   ├── 001_create_scripts.sql
│   ├── 002_create_monitored_sources.sql
│   └── ...
└── jobs/
    ├── 001_create_jobs.sql
    └── ...
```

### Migration Runner Behavior
- Uses **relative paths** as version identifiers (e.g., `clips/clips_001_create_core_tables`)
- Prevents collisions between migrations in different directories
- FTS5 migrations are disabled (`.sql.disabled` extension) when driver doesn't support FTS5
- Check FTS5 support with `internal/storage/sqlite_fts5.go:HasFTS5()`

### Adding New Migrations
1. Create SQL file in appropriate directory
2. For `clips.db.sqlite`: `internal/repository/clips/migrations/`
3. For `velox.db.sqlite`: `migrations/sqlite/`
4. For `jobs.db.sqlite`: `migrations/jobs/`
5. Never apply migrations from one domain to another database

---

## FTS5 Support

FTS5 is **NOT available** in the current `mattn/go-sqlite3` driver build.

**Current behavior**:
- Clips search falls back to `LIKE` queries
- FTS5 migration files are renamed to `.sql.disabled`

**Future options** (not recommended now):
- Build custom `mattn/go-sqlite3` with FTS5 enabled
- Migrate to `modernc.org/sqlite` (pure Go, has FTS5 support)

**Priority**: Focus on schema boundaries first, FTS5 is not a priority.

---

## Repository Injection Pattern

The `catalog.Repository` uses dependency injection for database access:

```go
type Repository struct {
    stockRepo   *stock.Repository    // → stock.db.sqlite
    clipsRepo   *clips.Repository    // → clips.db.sqlite
    artlistRepo *clips.Repository    // → artlist.db.sqlite (named artlistRepo for clarity)
}
```

**Key points**:
- No direct `sql.Open()` calls in catalog repository
- Each service gets its own repository instance
- Clear separation of database access

---

## Diagnostics and Testing

### DB Isolation Test
Run `internal/storage/sqlite_isolation_test.go:TestDBIsolation` to verify each database has only expected tables.

```bash
go test ./internal/storage/... -v -run TestDBIsolation
```

### FTS5 Status
Check FTS5 support:
```go
if storage.HasFTS5(db) {
    // Use FTS5 queries
} else {
    // Use LIKE fallback
}
```

### Schema Verification
To check tables in each database:
```bash
sqlite3 data/velox.db.sqlite ".tables"
sqlite3 data/stock.db.sqlite ".tables"
sqlite3 data/clips.db.sqlite ".tables"
sqlite3 data/artlist.db.sqlite ".tables"
```

---

## Schema Boundaries Summary

| Database | Contains | Must NOT Contain |
|----------|----------|------------------|
| velox.db | scripts, monitored_sources, harvester_jobs, media_items, media_files, media_tags, video_metadata, script_stock_matches, video_stats_history, artlist_runs | clips, clip_folders, segment_embeddings |
| stock.db | clips (stock), clip_folders (stock) | media_files, media_items, media_tags, segment_embeddings |
| clips.db | clips (YouTube), clip_folders, segment_embeddings | (clean) |
| artlist.db | clips (Artlist), clip_folders, artlist_runs | segment_embeddings |
| images.db | (empty or image tables) | clips, segment_embeddings |
| voiceover.db | (empty or voiceover tables) | clips, segment_embeddings |
| jobs.db | jobs, job_events | everything else |

---

## Cleanup History

**2026-05-04**: Removed legacy tables from databases:
- `velox.db.sqlite`: Dropped `clips`, `clip_folders`, `clip_tags`, `segment_embeddings`, `indexing_checkpoints`, `script_sections`
- `stock.db.sqlite`: Dropped `media_files`, `media_items`, `media_tags`, `segment_embeddings`
- `artlist.db.sqlite`: Dropped `segment_embeddings`

All databases now conform to their intended schema boundaries.
