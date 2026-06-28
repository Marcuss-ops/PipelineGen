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

Graduation is split-phase per the Wave CONFORMANCE-001 / id-24 follow-up
ticket ladder (`architecture/current.yaml#id-24 follow_up_tickets`):

- **A1.3** — category_channels backfill CLI ships day-1 (BACKFILL phase).
- **A2.1** — seed-channels entrypoint removal at CONTRACT phase (synchronous-
  with-backfill; GC'd via cherry-pick 1826d344 forwarding 0907ec8a).
- **Migration 110** — atomic close: git rm of the 2 ARCH-ALLOWLIST files +
  media_assets backfill CLI + adapter ACL removal on the YouTube-side.

**Count vs condition mapping** for the future verified_zero flip:
the `current_count: 2` transitional baseline in `architecture/current.yaml#id-24`
counts the 2 source files carrying the `// ARCH-ALLOWLIST:
monitored-sources-readonly` marker; the 4 unchecked AC items below are
QUALITATIVE close conditions (port rename, A2.1 media_assets backfill CLI,
migration 110 atomic close, CI GREEN). Both the count and the 4 conditions
must clear for migration 110 to flip `verified_zero: true` on the wave entry.

The items below cover the migration_109 → migration_110 graduation:

- [x] Migration 109 file applies cleanly (idempotent via IF NOT EXISTS).
- [x] Ledger row inserted in `schema_migrations` with correct SHA256.
- [x] All 4 columns + 3 indexes verified present.
- [x] `MediaAssetDiscoveryRepository` written with QDRANT-002 bypass rationale.
- [x] `IncrementProcessed` contract documented (filters on `id = ?` PK + `discovered_via != ''` scope).
- [x] **A1.3 — category_channels backfill CLI shipped day-1** as part of W24
      cherry-pick 1826d344 (cmd/admin/backfill_monitored_sources_to_category_
      channels.go with cross-run zero-drift assertion; channels.Service.
      UpsertBulk writes to category_channels; ledger row count drift
      between Run-1 and Run-2 confirmed = 0).
- [x] **A2.1 — seed-channels entrypoint removal at CONTRACT phase** (cmd/
      admin/seed_channels.go + config/channel_monitor_config.json:
      REMOVED by the same cherry-pick; `rg 'runSeedChannels|SeedChannels'
      --glob '!*_test.go' .` returns ZERO hits; migration_phase=CONTRACT;
      replacement=channels.Service via canonical POST /channels/
      bulk-upsert; synchronous-with-backfill, no deprecation window).
- [ ] Port rename + adapter + ServiceDeps + composition cleanup (open work — see followups).
- [ ] **A2.1 — Backfill CLI runs zero-drift on a DB snapshot** (the
      media_assets backfill CLI per
      cmd/admin/backfill_monitored_sources_to_media_assets.go —
      future ship contract scoped in migration 110).
- [ ] Migration 110 lands atomically with `git rm` + allowlist deletion
      (`docs/migrations/monitored-sources-allowlist.txt` removed; the
      2 transitional ARCH-ALLOWLIST sites un-marked).
- [ ] `bash scripts/ci-architectural-checks.sh` returns GREEN except for the
      documented pre-existing Check 1 failure (`qdrant.NewIndexWriter` direct
      ctor in `internal/app/composition.go:323`).
