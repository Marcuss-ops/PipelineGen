# MODULE_OWNERSHIP.md - Active Systems Map

This document defines ownership and status for every module in the system.
It helps agents understand what is active, what is experimental, and what must be removed.

## General Rule

- **One system per function** - If duplicates exist, one must die
- **Experimental = feature flag OFF + review required**
- **Old systems must be removed**, not left to rot

---

## Current Systems Map

| Area              | Current Module                      | Path                                    | Status            |
| ----------------- | ----------------------------------- | --------------------------------------- | ----------------- |
| **Jobs**          | Job queue + worker                   | `internal/jobs/` + `internal/repository/jobs/` | **ACTIVE**        |
| **Artlist**       | Artlist pipeline                     | `internal/sources/artlist/`             | **ACTIVE**        |
| **YouTube Clips** | YouTube clip extraction              | `internal/sources/youtube/`             | **ACTIVE**        |
| **Stock Pipeline**| Stock footage pipeline               | `internal/module/stock.go`              | **ACTIVE**        |
| **MediaAsset Processor** | Shared media processor        | `internal/media/mediaasset/`            | **ACTIVE**        |
| **Asset Registry**| Finalizer / registry pattern         | `internal/media/assetregistry/`         | **EXPERIMENTAL**  |
| **Asset Index**   | Asset index for fast search          | `internal/media/assetindex/`            | **ACTIVE**        |
| **Asset Tree**    | Hierarchical asset tree              | `internal/media/assettree/`             | **ACTIVE**        |
| **Drive Destination** | Drive upload and resolver        | `internal/upload/drive/` + `internal/storage/assetdestination/` | **ACTIVE** |
| **Voiceover**     | Voiceover generation                 | `internal/media/voiceover/`             | **EXPERIMENTAL**  |
| **Images**        | Image search, sync and generation    | `internal/media/images/`                | **EXPERIMENTAL**  |
| **Script**        | Script generation via Ollama         | `internal/api/handlers/script/`         | **ACTIVE**        |
| **System**        | Health, diagnostics, doctor endpoint | `internal/app/` (SystemWiring)          | **ACTIVE**        |
| **Catalog Sync**  | Catalog sync from Drive              | `internal/media/catalogsync/`           | **ACTIVE**        |
| **Deletion**      | Multi-source asset deletion          | `internal/media/deletion.go`            | **ACTIVE**        |
| **Maintenance**   | DB maintenance (VACUUM, cleanup)     | `internal/core/maintenance/`            | **ACTIVE**        |

---

## Removed / To Be Removed

| Old System                          | Reason                         | Status         |
| ----------------------------------- | ------------------------------ | -------------- |
| `internal/cron/*`                  | Legacy job system              | ✅ REMOVED     |
| `internal/repository/harvester/`   | Used legacy cron               | ✅ REMOVED     |
| `internal/service/workflowrunner/` | Dangerous (context.Background)  | ✅ REMOVED     |
| `internal/service/assetpipeline/`  | Useless thin wrapper           | ✅ REMOVED     |
| `pkg/idutil`, `pkg/jsonutil`       | Useless wrappers               | ✅ REMOVED     |
| `internal/service/*` (old paths)   | Migrated to `internal/sources/` or `internal/media/` | ✅ REMOVED |

---

## Architectural Contracts

- **Media Processor**: `internal/media/mediaasset/` (interface `core/processor.Processor`)
- **Destination Resolver**: `internal/storage/assetdestination/` (interface `core/destination.Resolver`)
- **Job System**: `internal/jobs/` + `internal/repository/jobs/` (storage in `velox.db.sqlite`)
- **Module Registry**: `internal/app/registry.go` (interface `module.Module`)
