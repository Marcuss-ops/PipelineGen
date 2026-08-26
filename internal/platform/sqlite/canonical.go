package sqlite

// PR-MIGRATIONS-SSOT (August 2026): the CREATE TABLE constants that
// previously lived in this file (CanonicalMediaAssetsSchema,
// CanonicalAssetLocationsTable, CanonicalAssetArtifactsTable,
// CanonicalAssetTextTracksTable, CanonicalScriptLocalizationsTable) are
// DELETED. The sole physical schema authority for every table is
// migrations/sqlite/.
//
// Tests must use NewMigratedTestDB(t) which applies the full migration
// chain, or NewMigratedTestDBWithExtra(t, extra) for test-specific
// additions on top.

// ── Asset state column matrix (godlike/06 SSOT, August 2026) ─────────────────
//
// media_assets has exactly THREE state-bearing columns. No shadows, no
// COALESCE fallbacks, no dual-source-of-truth.
//
//   COLUMN            OWNER                    MEANING
//   ───────────────── ──────────────────────── ───────────────────────────────
//   lifecycle_state   MediaCommitter           availability / deletion status.
//                      (write)                  Values: ACTIVE, DELETED,
//                      MediaCommitter           DELETE_REQUESTED, STAGING,
//                      AssetMutationDispatcher  ARCHIVED. This is the SOLE
//                      (restore/delete)         operational source of truth
//                                               for asset availability.
//
//   index_state       outbox index handler     projection / search status.
//                      (write)                  Values: DISCOVERED, INDEXING,
//                      AssetMutationDispatcher  INDEXED, INDEX_FAILED,
//                      (restore)                DELETE_PENDING, DELETED.
//                                               Tracks whether Qdrant has a
//                                               current embedding for this asset.
//
//   asset_state       migration 189 trigger    DERIVED compatibility column.
//                      (read-only)              Computed from lifecycle_state +
//                                               index_state. Application code
//                                               MUST NEVER write this column
//                                               directly (guard trigger fails
//                                               closed). Read-only convenience
//                                               for dashboards/operators.
//
// Removed columns (do NOT re-add):
//   - status             → removed by migration 101 (duplicated lifecycle_state)
//   - lifecycle_status   → removed by migration 230 (shadow of lifecycle_state)
