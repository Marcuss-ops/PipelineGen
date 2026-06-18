-- 063_drop_orphan_ledger_entry.sql
--
-- PR12a.2 cleanup: drops any `schema_migrations` ledger row whose filename
-- references the now-deleted `062_backfill_asset_locations.sql`.
--
-- ─────────────────────────────────────────────────────────────────────────
-- Background (PR12a)
-- ─────────────────────────────────────────────────────────────────────────
-- PR12a deleted `migrations/sqlite/062_backfill_asset_locations.sql` because
-- the canonical `062_asset_locations_backfill.sql` (kept in the tree) is a
-- strict superset: it adds the same 11 stable columns to `media_assets`,
-- backfills `asset_locations`, and promotes `metadata_json.$.*` into the new
-- columns. The smaller 062 conflicted with the bigger one on first-apply
-- anyway (both target `asset_locations` + `media_assets.{columns}`).
--
-- ─────────────────────────────────────────────────────────────────────────
-- Why this ledger-cleanup migration is needed
-- ─────────────────────────────────────────────────────────────────────────
-- Any developer who previously ran `migrations/sqlite/062_backfill_asset_locations.sql`
-- against their local SQLite DB has a row in `schema_migrations` with
-- `version = 62` and `filename = '062_backfill_asset_locations.sql'`. After
-- PR12a ships, that file no longer exists on disk. The Go migration runner
-- (`internal/storage/migrations.go::migrateAll`) would, on next `migrate up`,
-- compare:
--
--   ledger row:  version=62, filename='062_backfill_asset_locations.sql',
--                checksum=<sha256 of deleted file>
--   on-disk file: version=62, filename='062_asset_locations_backfill.sql',
--                 checksum=<sha256 of canonical>
--
-- The versions match but the filenames+checksums differ → runner aborts with:
--   "storage: migration 062 checksum mismatch — applied=... current=...
--    Migrations must never be modified after being applied"
--
-- That error blocks every subsequent `migrate up` for that developer's DB
-- until the orphan ledger row is manually removed. This migration removes
-- it cleanly.
--
-- ─────────────────────────────────────────────────────────────────────────
-- Safety / idempotency
-- ─────────────────────────────────────────────────────────────────────────
-- * Fresh databases (no row matching the deleted filename): DELETE affects
--   zero rows and is a no-op. Safe.
-- * Local dev boxes that applied the smaller 062: row is removed; runner
--   re-applies the canonical `062_asset_locations_backfill.sql` on next
--   `migrate up`. The schema effect from both files is equivalent (the
--   bigger one is a strict superset, so re-running is safe).
-- * Production / staging databases that ran the canonical 062 (most common
--   path after PR12a ships): no row matches, no-op. The canonical ledger
--   row (filename='062_asset_locations_backfill.sql') is preserved
--   untouched.
--
-- The `schema_migrations` table itself is created earlier by the runner
-- (`internal/storage/migrations.go::migrateAll::"Ensure ledger table
-- exists"`), so this DELETE always finds the table present when 063 runs.
--
-- ─────────────────────────────────────────────────────────────────────────
-- Post-condition audit
-- ─────────────────────────────────────────────────────────────────────────
-- The trailing SELECT confirms:
--   * If a dev applied the smaller 062: only the canonical 062 row remains.
--   * If a dev applied the canonical 062: only the canonical 062 row remains
--     (no row matched the DELETE).
--   * Fresh DB: zero rows (no 062 has been applied yet).

DELETE FROM schema_migrations
WHERE filename = '062_backfill_asset_locations.sql';

-- Post-condition audit: show the surviving 062 row(s), if any.
-- Operators grep for `062_asset_locations_backfill.sql` (canonical)
-- to confirm cleanup landed without erasing the canonical ledger entry.
SELECT version, filename, applied_at
FROM schema_migrations
WHERE filename IN (
    '062_asset_locations_backfill.sql',
    '062_backfill_asset_locations.sql'
)
ORDER BY version;
