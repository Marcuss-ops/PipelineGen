# Architecture Status

This document tracks the status of packages in the PipelineGen codebase.

## Package Status Legend

- **ACTIVE** - Actively used, maintained, source of truth
- **LEGACY** - Still used but deprecated, migration planned
- **EXPERIMENTAL** - New code, not yet fully integrated
- **DEPRECATED** - No longer used, to be removed

---

## Core Packages

| Package | Status | Notes |
|---------|--------|-------|
| `internal/service/jobs` | ACTIVE | Main job system (uses `jobs` table) |
| `internal/repository/jobs` | ACTIVE | Job repository (uses `jobs` table) |
| `pkg/models` | ACTIVE | Core data models |
| `internal/core/media` | ACTIVE | Media abstraction layer |

## Adapters

| Package | Status | Notes |
|---------|--------|-------|
| `internal/adapters/artlist` | EXPERIMENTAL | Interface defined, not yet implemented |
| `internal/adapters/drive` | EXPERIMENTAL | Interface defined, not yet implemented |
| `internal/adapters/ffmpeg` | EXPERIMENTAL | Interface defined, not yet implemented |
| `internal/adapters/ytdlp` | EXPERIMENTAL | Interface defined, not yet implemented |
| `internal/adapters/ollama` | EXPERIMENTAL | Interface defined, not yet implemented |

## Media Packages

| Package | Status | Notes |
|---------|--------|-------|
| `pkg/media/ffmpeg` | ACTIVE | Used by mediaasset.Processor, mediapipeline |
| `pkg/media/audio` | ACTIVE | Used by voiceover service |
| `pkg/media/downloader` | ACTIVE | Used by mediaasset.Processor, mediapipeline, artlist |

## Legacy / Deprecated

| Package | Status | Notes |
|---------|--------|-------|
| `internal/jobs` | REMOVED | Was using `jobs_new` table, unused |
| `internal/service/artlist/run_record.go` | DEPRECATED | Legacy `artlist_runs` table, read-only |
| `artlist_runs` table | DEPRECATED | Replaced by `jobs` table |

## Drive Query Building

| Package | Status | Notes |
|---------|--------|-------|
| `pkg/drive` | ACTIVE | Shared Drive API query helpers (BuildQuery, BuildNameQuery) |

---

**Last Updated:** 2026-05-03
**Next Steps:**
1. Migrate from `pkg/media/*` to `internal/adapters/*` (or vice versa) - choose ONE
2. Complete migration to `jobs` table (remove `artlist_runs` references)
3. Implement `internal/adapters/*` interfaces or remove them
