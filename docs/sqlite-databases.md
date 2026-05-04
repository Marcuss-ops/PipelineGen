# SQLite Databases Architecture

## Overview

The system uses multiple SQLite databases to separate concerns and improve maintainability.

## Database Inventory

### velox.db.sqlite (Main)
**Purpose**: Core application data, scripts, monitoring, media items.

**Current Tables**:
- `scripts` - Script definitions
- `monitored_sources` - Sources to monitor
- `harvester_jobs` - Harvester job tracking
- `media_items` - Media items catalog
- `media_files` - Media files
- `media_tags` - Media tags
- `clip_folders` - Clip folders (legacy?)
- `clip_tags` - Clip tags (legacy?)
- `clips` - Clips (legacy?)
- `artlist_runs` - Artlist pipeline runs
- `segment_embeddings` - Timeline cache (should be evaluated)
- `video_metadata` - Video metadata
- `script_stock_matches` - Script-stock matches
- `video_stats_history` - Video stats history

**Target Tables** (desired state):
- `scripts`
- `monitored_sources`
- `harvester_jobs`
- `media_items`
- `media_files`
- `media_tags`
- `video_metadata`
- `script_stock_matches`
- `video_stats_history`
- `artlist_runs` (if Artlist-specific)

---

### stock.db.sqlite (Stock Media)
**Purpose**: Stock footage and clips from stock providers.

**Current Tables**:
- `clips` - Stock clips
- `clip_folders` - Stock folders
- `media_files` - Media files (from main migrations)
- `media_items` - Media items (from main migrations)
- `media_tags` - Media tags (from main migrations)
- `segment_embeddings` - Timeline cache (likely unnecessary)

**Target Tables** (desired state):
- `clips` (stock-specific)
- `clip_folders` (stock-specific)
- `stock_folders` (if exists)
- `stock_clips` (if exists)

---

### clips.db.sqlite (YouTube Clips)
**Purpose**: YouTube clips extracted via youtubeclip service.

**Current Tables**:
- `clips` - YouTube clips
- `clip_folders` - YouTube clip folders
- `segment_embeddings` - Timeline cache (appropriate here)

**Target Tables** (desired state):
- `clips` (YouTube-specific)
- `clip_folders` (YouTube-specific)
- `segment_embeddings` (for timeline cache)

---

### artlist.db.sqlite (Artlist Media)
**Purpose**: Artlist assets downloaded via artlist service.

**Current Tables**:
- `clips` - Artlist clips
- `clip_folders` - Artlist folders
- `segment_embeddings` - Timeline cache (likely unnecessary)

**Target Tables** (desired state):
- `clips` (Artlist-specific)
- `clip_folders` (Artlist-specific)
- `artlist_runs` - Pipeline run tracking

---

### images.db.sqlite (Images)
**Purpose**: Image assets.

**Current Tables**:
- Only `schema_migrations`

**Target Tables** (desired state):
- Image-specific tables (TBD - feature incomplete or legacy)

---

### voiceover.db.sqlite (Voiceovers)
**Purpose**: Voiceover audio files.

**Current Tables**:
- Only `schema_migrations`

**Target Tables** (desired state):
- Voiceover-specific tables (TBD - feature incomplete or legacy)

---

### jobs.db.sqlite (Job Queue)
**Purpose**: Background job processing.

**Current Tables**:
- `jobs` - Job definitions
- `job_events` - Job event log

**Target Tables** (desired state):
- `jobs`
- `job_events`

---

## Migration Strategy

### Current Issue
The `clips/` migrations are applied to multiple databases:
- `main` (velox.db.sqlite) - unnecessary tables
- `stock` (stock.db.sqlite) - OK (has clips)
- `clips` (clips.db.sqlite) - OK (primary)
- `artlist` (artlist.db.sqlite) - OK (has clips)

### Recommendations

1. **Stop applying `clips/migrations` to `main`** unless tables like `segment_embeddings` are needed there.

2. **Remove `segment_embeddings` from `stock` and `artlist`** if only used by timeline service (which uses `clips.db.sqlite`).

3. **Clarify `images.db` and `voiceover.db`** - either add proper migrations or document as placeholder.

4. **Add DB isolation tests** to verify each database has only expected tables.

---

## FTS5 Support

FTS5 is **not currently available** in the `mattn/go-sqlite3` driver build.

- Clips search falls back to `LIKE` queries
- Full-Text Search can be enabled by:
  - Building custom `mattn/go-sqlite3` with FTS5 enabled
  - Migrating to `modernc.org/sqlite` (pure Go, FTS5 support)
- **Not a priority now** - focus on schema boundaries first

---

## Schema Boundaries Summary

| Database | Should Have | Should NOT Have |
|-----------|-------------|------------------|
| velox.db | scripts, monitored_sources, harvester, media_items, etc. | clips, clip_folders (legacy) |
| stock.db | clips (stock), clip_folders (stock) | segment_embeddings (if unused) |
| clips.db | clips (YouTube), clip_folders, segment_embeddings | (clean) |
| artlist.db | clips (Artlist), clip_folders, artlist_runs | segment_embeddings (if unused) |
| images.db | (empty or image tables) | clips, segment_embeddings |
| voiceover.db | (empty or voiceover tables) | clips, segment_embeddings |
| jobs.db | jobs, job_events | everything else |
