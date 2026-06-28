# Migration 109 — Rationale & Operational Notes

> **Sibling file** to `migrations/sqlite/109_add_media_assets_discovery_columns.sql`.
> The migration file itself is condensed to a 4-line identifier header so that
> `sqlite3 ... < file.sql` re-apply is unambiguous and matches the canonical
> pattern set by migration 108. This document carries the FULL rationale +
> operational notes that previously lived in the migration file's godoc.

## Wave tracking

- **Wave**: CONFORMANCE-001
- **ID**: id-24
- **Date**: June 28, 2026
- **Owner**: PipelineGen Agent (conformance consolidation)
- **Deadline**: 2026-08-31 (Wave CONFORMANCE-001 close)

## What this migration does

Adds 4 columns to `media_assets` so per-video discovery state (previously stored
in `monitored_sources`) has a defined destination in the canonical assets table:

| Column               | Type | Default | Purpose                                                |
|----------------------|------|---------|--------------------------------------------------------|
| `external_id`        | TEXT | `''`    | Source's stable identifier (YouTube video ID, etc.)    |
| `discovered_via`     | TEXT | `''`    | Provenance marker (`monitor:monitored:<id>` for now)   |
| `discovered_at`      | TEXT | `''`    | RFC3339 timestamp of the discovery event               |
| `monitored_source_id`| TEXT | `''`    | Opaque link to the legacy `monitored_sources.id` row   |

These columns are NOT NULL DEFAULT `''` so the ADD COLUMN sweep over existing
`media_assets` rows is safe. **Migrations naming + ledger conventions** (see
`AGENTS.md` §Instructions) apply — every row remains owned by `media_assets`.

## Index strategy: partial unique on `(external_id, discovered_via)`

```sql
CREATE UNIQUE INDEX IF NOT EXISTS idx_media_assets_ext_discovered
    ON media_assets(external_id, discovered_via)
    WHERE external_id != '' AND discovered_via != '';
```

The **partial WHERE** is required because every legacy `media_assets` row
receives `external_id = ''` and `discovered_via = ''` during the ADD COLUMN
sweep. A full unique index would immediately crash on apply. The partial index
ignores legacy rows and constrains only discovery rows.

Two auxiliary partial secondary indexes (also non-empty scoped) support the
incremental-projection paths used by `MonitoredSourceStatus` and the
`backfill-monitored-sources-to-media-assets` CLI:

```sql
CREATE INDEX IF NOT EXISTS idx_media_assets_discovered_via ON media_assets(discovered_via) WHERE discovered_via != '';
CREATE INDEX IF NOT EXISTS idx_media_assets_monitored_source_id ON media_assets(monitored_source_id) WHERE monitored_source_id != '';
```

## QDRANT-002: discovery rows INTENTIONALLY bypass the outbox

`internal/infrastructure/database/sqlite/assets/media_assets_discovery_repository.go`
writes directly to `media_assets` without going through `outbox.Dispatcher`.
This is the documented exception to the QDRANT-002 invariant (see
`architecture/godlike/06_DATA_AND_CONFIG_OWNERSHIP.md`).

**Rationale**: discovery rows are STUBS — they carry `external_id + URL + minimal
metadata` but they DO NOT carry video files, transcripts, or embeddings. The
Qdrant projection is therefore logically N/A for these stubs. The canonical
outbox pathway (`outbox.Dispatcher → media_assets UPSERT → index_state transition`)
takes over once a `youtube.clip.extract` job hydrates the discovery stub with
real embeddings + transcripts.

**Documented marker**: the godoc on the discovery repository methods starts with
`// QDRANT-002: THIS METHOD BYPASSES THE OUTBOX` so a future reader grepping for
direct `media_assets` writers sees the rationale instantly.

## Backfill CLI

The companion CLI
`cmd/admin/backfill_monitored_sources_to_media_assets.go`
mirrors the A1.3 `category_channels` CLI but writes to `media_assets` (using the
partial unique index as the natural key). The CLI runs ONCE on a DB snapshot
and asserts zero-drift end-to-end.

## ROLLBACK

```sql
ALTER TABLE media_assets DROP COLUMN external_id;
ALTER TABLE media_assets DROP COLUMN discovered_via;
ALTER TABLE media_assets DROP COLUMN discovered_at;
ALTER TABLE media_assets DROP COLUMN monitored_source_id;
DROP INDEX IF EXISTS idx_media_assets_ext_discovered;
DROP INDEX IF EXISTS idx_media_assets_discovered_via;
DROP INDEX IF EXISTS idx_media_assets_monitored_source_id;
DELETE FROM schema_migrations WHERE version = 109;
```

## POLICY REFERENCES

- **AGENTS.md §Instructions** — single-table-per-capability ownership;
  `media_assets` remains the owner of the per-asset discovery state.
- **AGENTS.md §QDRANT Entity Associations** — Qdrant projection is async;
  discovery stubs are explicitly exempt (see QDRANT-002 section above).
- **AGENTS.md Pattern 0 (Port abstraction layer, PR1.7)** — the
  `youtubeports.MediaAssetDiscoveryPort` typed port pattern follows the same
  port + adapter + compile-time assertion shape as the existing YouTube
  extractor ports.
- **AGENTS.md §Git-Lesson-2 (direct-to-main workflow)** — this migration
  lands on `main` directly via `git push origin main` after Wave CONFORMANCE-001
  closure (2026-08-31).
- **architecture/current.yaml** — id-24 follow_up_tickets (Wave CONFORMANCE-001).
- **architecture/policy.yaml** — Wave CONFORMANCE-001 → id-24 closure sequence.

## ACCEPTANCE CRITERIA (for graduation to CONTRACT 110)

- [x] Migration 109 file applies cleanly (idempotent via IF NOT EXISTS).
- [x] Ledger row inserted in `schema_migrations` with correct SHA256.
- [x] All 4 columns + 3 indexes verified present.
- [x] `MediaAssetDiscoveryRepository` written with QDRANT-002 bypass rationale.
- [x] `IncrementProcessed` contract documented (filters on `id = ?` PK + `discovered_via != ''` scope).
- [ ] Port rename + adapter + ServiceDeps + composition cleanup (open work — see followups).
- [ ] Backfill CLI runs zero-drift on a DB snapshot.
- [ ] Migration 110 lands atomically with `git rm` + allowlist deletion.
- [ ] `bash scripts/ci-architectural-checks.sh` returns GREEN except for the
      documented pre-existing Check 1 failure (`qdrant.NewIndexWriter` direct
      ctor in `internal/app/composition.go:323`).
