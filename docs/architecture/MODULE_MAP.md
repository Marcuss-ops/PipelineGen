# MODULE_MAP.md - PipelineGen Official Module Map

**Last updated**: 2026-05-25
**Purpose**: Official map of active, experimental and deprecated modules. Single source of truth for agents and developers.

---

## Status Legend

| Status | Meaning | Action |
|--------|---------|--------|
| **ACTIVE** | Stable module, in production | Maintain, test |
| **EXPERIMENTAL** | In development, feature flag OFF | Evaluate, complete or remove |
| **DEPRECATED** | To be deleted, do not extend | Migrate and remove |
| **DISABLED** | Feature flag OFF, in quarantine | Rewrite or delete |

---

## Official Module Map

### Core System

| Module | Status | Feature Flag | Description | Database | Module Path |
|--------|--------|--------------|-------------|----------|-------------|
| **System** | ACTIVE | always ON | Health, diagnostics, doctor endpoint | none | `internal/app/` (SystemWiring) |
| **Jobs** | ACTIVE | always ON | Job queue, workers, events | `velox.db.sqlite` (`jobs` table) | `internal/jobs/` + `internal/repository/jobs/` |
| **Registry** | ACTIVE | always ON | Module wiring, dependency injection | none | `internal/app/registry.go` |

### Media Processing

| Module | Status | Feature Flag | Description | Database | Module Path |
|--------|--------|--------------|-------------|----------|-------------|
| **Artlist** | ACTIVE | `ARTLIST_ENABLED` | Artlist pipeline (search, download, upload) | `media.db.sqlite` | `internal/sources/artlist/` |
| **YouTube Clips** | ACTIVE | `YOUTUBE_ENABLED` | YouTube clip extraction | `media.db.sqlite` | `internal/sources/youtube/` |
| **Media** | ACTIVE | always ON | Manifest export, asset management | `velox.db.sqlite` | `internal/module/media.go` |
| **MediaAsset Processor** | ACTIVE | always ON | Canonical media processor (shared) | varies | `internal/media/mediaasset/` |
| **AssetRegistry** | EXPERIMENTAL | n/a | Finalizer, registry pattern | varies | `internal/media/assetregistry/` |
| **Stock Pipeline** | ACTIVE | always ON | Stock footage pipeline | `media.db.sqlite` | `internal/module/stock.go` |

### Content Generation

| Module | Status | Feature Flag | Description | Database | Module Path |
|--------|--------|--------------|-------------|----------|-------------|
| **ScriptDocs** | ACTIVE | `SCRIPT_DOCS_ENABLED` | Script generation + multi-image scene pipeline | `velox.db.sqlite` | `internal/api/handlers/script/` |
| **Script History** | ACTIVE | `SCRIPT_CLIPS_ENABLED` | Generated script history | `velox.db.sqlite` | `internal/module/scripthistory.go` |
| **Voiceover** | EXPERIMENTAL | `VOICEOVER_ENABLED` | Voiceover generation, sync | `media.db.sqlite` | `internal/media/voiceover/` |
| **Images** | EXPERIMENTAL | `IMAGES_ENABLED` | Image generation (NVIDIA/Google), semantic enrichment | `media.db.sqlite` | `internal/media/images/` |
| **Semantic Tagger** | ACTIVE | always ON | LLM metadata enrichment at ingest (concept_tags, visual_objects, emotional_tone, search_text_expanded) | n/a (JSON in media_assets) | `internal/media/semantic/` + `scripts/semantic_tagger.py` |

### Asset Management

| Module | Status | Feature Flag | Description | Database | Module Path |
|--------|--------|--------------|-------------|----------|-------------|
| **Assets** | ACTIVE | always ON | Unified asset search | `velox.db.sqlite`, `media.db.sqlite` | `internal/module/assets.go` |
| **Asset Index** | ACTIVE | always ON | Asset index for fast search | `velox.db.sqlite` (`asset_index` table) | `internal/media/assetindex/` |
| **Asset Tree** | ACTIVE | always ON | Hierarchical asset tree | `velox.db.sqlite` (`asset_tree_nodes` table) | `internal/media/assettree/` |
| **Drive Destination** | ACTIVE | always ON | Drive upload, destination resolver | none (API) | `internal/upload/drive/` + `internal/storage/assetdestination/` |
| **Asset Destination** | ACTIVE | always ON | Unified destination resolver | none | `internal/storage/assetdestination/` |
| **Deletion Service** | ACTIVE | always ON | Multi-source asset deletion | various DBs | `internal/media/deletion.go` |

### Automation & Sync

| Module | Status | Feature Flag | Description | Database | Module Path |
|--------|--------|--------------|-------------|----------|-------------|
| **Catalog Sync** | ACTIVE | always ON | Catalog sync from Drive | `media.db.sqlite` | `internal/media/catalogsync/` |
| **Voiceover Sync** | ACTIVE | always ON | Voiceover sync from Drive | `media.db.sqlite` | `internal/media/voiceoversync/` |
| **Lifecycle Scheduler** | ACTIVE | always ON | Periodic maintenance scheduler | `velox.db.sqlite` | `internal/storage/scheduler/` |
| **Channel Monitor** | ACTIVE | `VELOX_ENABLE_CHANNEL_MONITOR` | YouTube channel monitoring | `media.db.sqlite` | `internal/media/monitor/` |

### Maintenance

| Module | Status | Feature Flag | Description | Database | Module Path |
|--------|--------|--------------|-------------|----------|-------------|
| **Maintenance** | ACTIVE | always ON | DB maintenance (VACUUM, cleanup) | `velox.db.sqlite` | `internal/core/maintenance/` |
| **Admin CLI** | ACTIVE | always ON | One-shot admin commands via `go run ./cmd/admin` | various DBs | `cmd/admin/` |

---

## Database Boundaries (Current)

| Database | Path | Tables | Used By |
|----------|------|--------|---------|
| `velox.db.sqlite` | `data/velox/velox.db.sqlite` | scripts, jobs, job_events, asset_index, asset_links, asset_tree_nodes, artlist_runs, pipeline_runs, pipeline_run_items, monitored_sources, harvester_jobs, media_items, media_files, media_tags, script_stock_matches, video_stats_history, script_sections, schema_migrations, api_requests | System, ScriptDocs, Artlist, Media, Assets, Jobs, AssetIndex, AssetTree |
| `media.db.sqlite` | `data/media/media.db.sqlite` | media_assets, clip_folders, segment_embeddings, voiceovers, subjects, sketchfab_models, schema_migrations | YouTube, Artlist, Stock, Voiceover, Images, CatalogSync, Sketchfab |

### Database Notes

- **Separate databases** for `artlist.db.sqlite`, `stock.db.sqlite`, `clips.db.sqlite`, `images.db.sqlite` **no longer exist**. All unified into `media.db.sqlite`.
- The old `data/artlist/artlist.db.sqlite` file is **legacy** and no longer opened by the active server.
- The `jobs` table lives in `velox.db.sqlite`, not in a separate `jobs.db.sqlite`.

---

## Architectural Contracts

### Single Media Processor
- **Canonical**: `mediaasset.MediaProcessor` in `internal/media/mediaasset/`
- **Input**: `AssetInput`
- **Output**: `AssetResult`
- **Used by**: Artlist, YouTube, Voiceover, Stock

### Single Job System
- **Canonical**: `internal/jobs/` + `internal/repository/jobs/`
- **Storage**: `velox.db.sqlite` (`jobs` table)
- **Worker**: Configurable runner, polls every 2s, lease TTL 5min
- **Retry**: Max 3 by default

### Single Module Registry
- **Registry**: `internal/app/registry.go`
- **Interface**: `module.Module`
- **Wiring**: `WireRegistry()` in `internal/app/`
- **Lifecycle**: `RegisterRoutes()`, cleanup on shutdown

---

## Survival Rules

### ACTIVE Modules
1. Must have tests
2. Must use job system for operations > 3 seconds
3. Cannot use `context.Background()` in handlers
4. Must propagate context correctly

### EXPERIMENTAL Modules
1. Feature flag OFF by default
2. Cannot be used by active modules without review
3. Required exit plan (completion or removal)

### DEPRECATED Modules
1. No new features
2. Gradually removed
3. Code deleted, not left to rot

---

## Notes for Agents

1. Before adding a new module, check if similar functionality already exists
2. Always use the `module.Module` interface for new modules
3. Register new modules in `internal/app/registry.go`
4. Update this map on every architectural change
5. Run `scripts/ci-architectural-checks.sh` to validate
