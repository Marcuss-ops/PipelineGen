package storage

// CanonicalMediaAssetsSchema is the single source of truth for the
// in-memory CREATE TABLE block required by assets.ClipsRepository.UpsertClipTx
// (37 INSERT columns) and scanMediaAsset (39-column SELECT projection).
//
// The 39 columns resolve in three layers, oldest at the top of the
// CREATE TABLE so the column-order history is preserved:
//
//  1. Original 22 columns from the pre-migration-059 media_assets
//     (id through tags_norm, embedding_json, duration_ms, url,
//     created_at, metadata_json, drive_folder_id, visual_embedding,
//     transcript_embedding, updated_at).
//
//  2. Eight legacy-promoted columns (drive_link, download_link,
//     drive_file_id, file_hash, local_path, media_type,
//     audio_embedding, language)
//     plus image dimensions (width, height) — already-existed columns
//     whose JSON mirror was deleted by migration 059's json_remove.
//
//     The `status` column is REMOVED by migration 101 (PR1 — Lifecycle
//     state SSOT, June 2026) because it co-existed with `lifecycle_state`
//     and writers used it for a parallel lowercase asset-status enum.
//     Pre-101 readers consulted COALESCE(lifecycle_state, status, …);
//     post-101 reads skip the column entirely. Including `status`
//     here would re-introduce the dual-source-of-truth drift the
//     migration was authored to eliminate.
//
//  3. Fifteen canonical columns added by migration 059
//     (lifecycle_state, deleted_at, folder_id, parent_folder_id,
//     folder_path, category, filename, error, thumb_url, phash,
//     search_text, scene_type, quality_score, reuse_count, last_used_at).
//
// Test fixtures that call drive.NewTestDBWithSchema MUST embed this
// constant so their schema stays in lockstep with the production
// schema. Fixtures that need an EXACT column-count OR a
// semantic-test-contract inline schema are exempt — the inline
// schema in such tests is the test's own pedagogical contract, not
// a violation of the SSOT. The canonical exempt-fixture list (July
// 2026, post-PR-CLIPINDEXER-FOLD-INVESTIGATE closure):
//
//   - clips_crud_test.go::newAlignTestDB
//     40-col MediaAssetColumns projection-alignment test; folding
//     would invalidate the contract assertion (the test pins the
//     40-column EXACT count).
//   - images_repository_test.go::fase4TestSchema
//     FASE 4 CUTOVER dual-write minimal schema; folding would
//     inflate the test surface beyond the FK-cascade contract.
//   - clip_atomic_writer_test.go::clipAtomicWriterSchema
//     5-step tx shape minimal schema; folding would obscure the
//     writer's narrow write-surface intent.
//
// Historical audits (NOT active exemptions — recorded so future agents
// can verify the exemption archaeology when re-reading the file):
//
//   - clipindexer/service_test.go::TestIndexingDoesNotSpawnPythonPerClip
//     Was exempted through CANONICAL-DRIFT-MIG094 closure pass
//     (July 2026) on the (incorrect at the time) hypothesis that
//     canonical's `embedding_json TEXT NOT NULL DEFAULT '[]'`
//     broke the indexer's CAS check treating NULL as `not yet
//     indexed`. PR-CLIPINDEXER-FOLD-INVESTIGATE reopened the
//     investigation and revealed the real cause was a setup-shape
//     mismatch: the inline 10-col schema lacked the search_text
//     column, so computeContentHash errored with `no such column`
//     and fell back to contentHash=""; the empty hash then matched
//     row.file_hash=” in the CAS fence and the test passed by
//     accident. The fold + new seedFileHash helper (above the
//     test) now exercise the production-shape CAS fence honestly.
//     Marked closed July 2026.
//
// See CANONICAL.md §1 for the SSOT contract; see each exempt test
// file's header comment for the test-specific exemption rationale.
//
// Migration 094 closure (CANONICAL-DRIFT-MIG094, July 2026): the
// canonical constant now embeds `index_state` + `index_state_updated_at`
// (the two columns PR-AUDIO-CHANNEL-EXTENSION setIndexedAt reads/writes
// from a first-class column rather than via json_extract). Pre-094
// fixtures that inlined index_state were violating the SSOT contract;
// the closure pass folds the residual 2 strictly-subset inline schemas
// (clipindexer/service_test.go + catalogsync/service_test.go) to embed
// the canonical constant per the rule below.
//
// When migration adds another canonical column it should:
//   - append a column definition here, AND
//   - append a matching mediaAssetColumns entry in
//     internal/infrastructure/database/sqlite/clips_repository.go
//     (the INSERT projection used by UpsertClipTx), AND
//   - append a matching scan target in the same file
//     (the SELECT projection used by reading rows).
//
// Keeping these three edits in lockstep prevents drift. Do NOT
// recreate the same column list inline in any other file.
//
// Pre-migration fixture schemas (e.g. the pre059Schema in
// internal/storage/migrations_test.go that exercises migration 059
// itself) intentionally do NOT embed this constant — they must mirror
// the schema as it existed BEFORE migration 059 ran.
const CanonicalMediaAssetsSchema = `
CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    tags TEXT NOT NULL DEFAULT '[]',
    tags_norm TEXT NOT NULL DEFAULT '',
    embedding_json TEXT NOT NULL DEFAULT '[]',
    duration_ms INTEGER NOT NULL DEFAULT 0,
    url TEXT NOT NULL DEFAULT '',
    created_at TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    drive_folder_id TEXT,
    visual_embedding TEXT,
    transcript_embedding TEXT,
    updated_at TEXT NOT NULL DEFAULT '',
    media_type TEXT NOT NULL DEFAULT '',
    status TEXT,
    local_path TEXT,
    relative_path TEXT,
    drive_file_id TEXT,
    drive_link TEXT,
    download_link TEXT,
    file_hash TEXT,
    audio_embedding TEXT NOT NULL DEFAULT '[]',
    language TEXT NOT NULL DEFAULT '',
    width INTEGER NOT NULL DEFAULT 0,
    height INTEGER NOT NULL DEFAULT 0,
    lifecycle_state TEXT NOT NULL DEFAULT 'ACTIVE',
    deleted_at TEXT NOT NULL DEFAULT '',
    folder_id TEXT NOT NULL DEFAULT '',
    parent_folder_id TEXT NOT NULL DEFAULT '',
    folder_path TEXT NOT NULL DEFAULT '',
    category TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    thumb_url TEXT NOT NULL DEFAULT '',
    phash TEXT NOT NULL DEFAULT '',
    search_text TEXT NOT NULL DEFAULT '',
    scene_type TEXT NOT NULL DEFAULT '',
    quality_score REAL NOT NULL DEFAULT 0.0,
    reuse_count INTEGER NOT NULL DEFAULT 0,
    last_used_at TEXT NOT NULL DEFAULT '',
    group_name TEXT NOT NULL DEFAULT '',
    search_terms TEXT NOT NULL DEFAULT '[]',
    clip_page_url TEXT NOT NULL DEFAULT '',
    thumbnail_url TEXT NOT NULL DEFAULT '',
    external_url TEXT NOT NULL DEFAULT '',
    usable_for TEXT NOT NULL DEFAULT '[]',
    avoid_for TEXT NOT NULL DEFAULT '[]',
    child_count INTEGER NOT NULL DEFAULT 0,
    is_folder INTEGER NOT NULL DEFAULT 0,
    depth INTEGER NOT NULL DEFAULT 0,
    visual_embedding_json TEXT NOT NULL DEFAULT '',
    mime_type TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    project TEXT NOT NULL DEFAULT '',
    -- Migration 099 (June 2026) — QDRANT asset column alignment.
    -- Types MUST match migrations/sqlite/099_qdrant_asset_columns.sql
    -- so the canonical CREATE TABLE reproduces what the migration chain
    -- produces on a fresh DB. start_time / end_time are stored as TEXT
    -- (not REAL) per the migration — that decision is the source of
    -- truth; this constant mirrors it.
    youtube_video_id TEXT NOT NULL DEFAULT '',
    youtube_url TEXT NOT NULL DEFAULT '',
    start_time TEXT NOT NULL DEFAULT '',
    end_time TEXT NOT NULL DEFAULT '',
    workspace_id TEXT NOT NULL DEFAULT '',
    channel_id TEXT NOT NULL DEFAULT '',
    license TEXT NOT NULL DEFAULT '',
    source_version TEXT NOT NULL DEFAULT '',
    style TEXT NOT NULL DEFAULT '',
    -- Migration 094 (June 2026) — QDRANT-002 PR6: promote index_state
    -- from a JSON-set value inside metadata_json ($.index_state) to a
    -- first-class media_assets.index_state column. index_state_updated_at
    -- is the sibling column — pairing them keeps a single source of
    -- truth for state-machine rotation (the worker writes both in one
    -- UPDATE). DEFAULTs match the production migration
    -- (migrations/sqlite/094_add_media_assets_index_state_column.sql)
    -- so the canonical CREATE TABLE reproduces what the migration
    -- chain produces on a fresh DB. Historical ordering note: 094
    -- landed BEFORE migration 099, but the schema groups them at the
    -- bottom for readability — see the layered-enumeration block in
    -- the package doc above for the detailed column history.
    index_state TEXT NOT NULL DEFAULT 'DISCOVERED',
    index_state_updated_at TEXT NOT NULL DEFAULT '',
    -- Migration 152 (July 2026, PR-CATALOG-MULTILINGUA step 1):
    -- 13 canonical metadata columns added by
    -- migrations/sqlite/152_add_canonical_metadata_columns.sql.
    -- Types and DEFAULTs match 152 byte-for-byte so this constant
    -- stays in lockstep with the canonical migration. The Italian
    -- plan's Step 1 groups identity, source, time, language,
    -- integrity, rights, and lifecycle on one row so the
    -- multilingual catalog has a single source of truth.
    -- See migration 152 header for the per-column rationale (e.g.
    -- binary_sha256 length-64 guard against MD5 pollution).
    source_provider   TEXT    NOT NULL DEFAULT '',
    source_video_id   TEXT    NOT NULL DEFAULT '',
    source_channel_id TEXT    NOT NULL DEFAULT '',
    source_url        TEXT    NOT NULL DEFAULT '',
    start_ms          INTEGER NOT NULL DEFAULT 0,
    end_ms            INTEGER NOT NULL DEFAULT 0,
    original_language TEXT    NOT NULL DEFAULT '',
    title             TEXT    NOT NULL DEFAULT '',
    binary_sha256     TEXT    NOT NULL DEFAULT '',
    semantic_hash     TEXT    NOT NULL DEFAULT '',
    rights_status     TEXT    NOT NULL DEFAULT 'review_required',
    policy_version    TEXT    NOT NULL DEFAULT 'v1',
    lifecycle_status  TEXT    NOT NULL DEFAULT 'ACTIVE',
    -- Migration 158 (July 2026, PR-CLIPINGEST-PIPELINE step 10) —
    -- 6 rights-extension columns. Types and DEFAULTs match
    -- migrations/sqlite/158_asset_rights_extension.sql byte-for-
    -- byte so this constant stays in lockstep with the canonical
    -- migration. The Italian plan's Step 10 groups licensing,
    -- ownership, channel scope, regional scope, expiry, and the
    -- active-review gate on one row so the Clip Pre-Planner has
    -- a single source of truth for the rights surface.
    --
    -- godlike/06 SSOT: the enum alphabets for license_basis
    -- (freeform string), review_status (closed 4), and
    -- allowed_channels/allowed_regions (JSON arrays) live in
    -- internal/kernel/asset/rights_state.go. The archcheck gates
    -- percheck_rights_status_canonical_6 +
    -- percheck_review_status_canonical_4 enforce the enum
    -- counts. expires_at carries an RFC3339-numeric string so
    -- string sort matches chronological order (deferred to a
    -- follow-up if operator workflow requires timezone-naive
    -- timestamps).
    --
    -- See migration 158 header for the per-column rationale
    -- (e.g. allowed_channels default '[]' so a NULL-vs-empty
    -- disambiguation is unnecessary at planner-side filter time).
    license_basis      TEXT    NOT NULL DEFAULT '',
    owner_channel_id   TEXT    NOT NULL DEFAULT '',
    allowed_channels   TEXT    NOT NULL DEFAULT '[]',
    allowed_regions    TEXT    NOT NULL DEFAULT '[]',
    expires_at         TEXT    NOT NULL DEFAULT '',
    review_status      TEXT    NOT NULL DEFAULT 'none',
    -- Migration 105 (July 2026) — asset_version is the canonical
    -- per-asset version string written by the clip atomic writer
    -- and artlist atomic writer. It is distinct from the
    -- asset_versions table (sequential version history) and is
    -- required by asset_committer UPSERT projections.
    asset_version      TEXT    NOT NULL DEFAULT '',
    -- asset_location and rendition are canonical per-asset
    -- metadata fields consumed by the asset committer and the
    -- artlist/clip atomic writers. They mirror the columns used
    -- in internal/infrastructure/database/sqlite/assets/*.go.
    asset_location     TEXT    NOT NULL DEFAULT '',
    rendition          TEXT    NOT NULL DEFAULT ''
);`

// CanonicalAssetArtifactsTable is the single source of truth for the
// in-memory CREATE TABLE block required by asset_artifacts readers
// that don't want to round-trip through RunMigrationsOnDB (tests,
// fixtures, dry-run admin tooling).
//
// Mirror of migrations/sqlite/153_create_asset_artifacts.sql
// (PR-CATALOG-MULTILINGUA step 1, July 2026). Types, CHECK constraints,
// and 3 supporting indexes MUST match the migration byte-for-byte
// so the canonical CREATE TABLE reproduces what the migration chain
// produces on a fresh DB.
//
// Why godlike/06 SSOT — kept together with the media_assets constant:
// asset_artifacts is the canonical file-registry surface for
// media_assets.id; tests that exercise the FK or the partial UNIQUE
// index need both tables in lockstep. Splitting into a separate
// Constants file would invite drift between the fixture and the
// production migration.
const CanonicalAssetArtifactsTable = `
CREATE TABLE IF NOT EXISTS asset_artifacts (
    id            TEXT PRIMARY KEY,
    asset_id      TEXT NOT NULL,
    role          TEXT NOT NULL
                  CHECK (role IN ('render_master','preview','thumbnail','waveform','source_archive')),
    mime_type     TEXT NOT NULL DEFAULT '',
    local_path    TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link    TEXT NOT NULL DEFAULT '',
    file_size     INTEGER NOT NULL DEFAULT 0,
    file_sha256   TEXT NOT NULL DEFAULT '',
    width         INTEGER NOT NULL DEFAULT 0,
    height        INTEGER NOT NULL DEFAULT 0,
    frame_rate    REAL NOT NULL DEFAULT 0.0,
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','uploaded','verified','deleted')),
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_asset_artifacts_asset_role
    ON asset_artifacts (asset_id, role);
CREATE UNIQUE INDEX IF NOT EXISTS idx_asset_artifacts_unique_singleton
    ON asset_artifacts (asset_id, role)
    WHERE role IN ('render_master', 'preview');
CREATE INDEX IF NOT EXISTS idx_asset_artifacts_status_updated
    ON asset_artifacts (status, updated_at DESC);
`

// CanonicalScriptLocalizationsTable is the single source of truth for
// the in-memory CREATE TABLE block required by script_localizations
// readers that don't want to round-trip through RunMigrationsOnDB
// (tests, fixtures, dry-run admin tooling).
//
// Mirror of migrations/sqlite/154_create_script_localizations.sql
// (PR-CATALOG-MULTILINGUA step 5, July 2026). Types, CHECK constraints,
// the UNIQUE(source/tuple) fingerprint, the FK CASCADE to scripts.id,
// and the 2 supporting indexes MUST match the migration byte-for-byte
// so the canonical CREATE TABLE reproduces what the migration chain
// produces on a fresh DB.
//
// Why godlike/06 SSOT — kept together with the media_assets + asset_artifacts
// constants: script_localizations is the localized-SpecSceneOutput
// projection surface for the same scripts.id that owns
// scripts.specscene (migration 100, the original-language source of
// truth). Tests that exercise the FK CASCADE, the UNIQUE constraint,
// or the per-status drain pattern need all three tables in lockstep.
// Splitting into a separate Constants file would invite drift
// between the fixture and the production migration.
// CanonicalAssetTextTracksTable is the single source of truth for the
// in-memory CREATE TABLE block required by asset_text_tracks readers
// that don't want to round-trip through RunMigrationsOnDB (tests,
// fixtures, dry-run admin tooling).
//
// Mirror of migrations/sqlite/155_asset_text_tracks_translation_fingerprint.sql
// (PR-CATALOG-MULTILINGUA step 4, July 2026). Types, CHECK constraints,
// the partial UNIQUE INDEX WHERE is_current=1 invariant, the FK CASCADE
// to media_assets(id), and the 4 supporting indexes MUST match the
// migration byte-for-byte so the canonical CREATE TABLE reproduces
// what the migration chain produces on a fresh DB.
//
// Why godlike/06 SSOT — kept together with the media_assets +
// asset_artifacts + script_localizations constants: asset_text_tracks
// is the localized-text persistence surface for media_assets.id and
// the structural backbone of the multilingual catalog. Tests that
// exercise the partial UNIQUE audit-trail invariant, the FK CASCADE
// or the search-text lookup need this constant in lockstep with
// media_assets (the FK target). Splitting into a separate file
// would invite drift between the fixture and the production
// migration.
//
// Forward-prevention note (godlike/06): future agents finding this
// constant MUST NOT add UNIQUE(asset_id, language_code, text_kind)
// back as an inline constraint — that would re-introduce the
// silent-overwrite regression that step 4 explicitly removes. The
// partial UNIQUE INDEX idx_asset_text_tracks_current is the SOLE
// "at most one current row per context" gate. archcheck
// (percheck_image_asset_invariants and its step-4 siblings) is the
// canonical detector for that misstep.
const CanonicalAssetTextTracksTable = `
CREATE TABLE IF NOT EXISTS asset_text_tracks (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,

    asset_id            TEXT NOT NULL,
    language_code       TEXT NOT NULL,
    text_kind           TEXT NOT NULL,

    text_content        TEXT NOT NULL DEFAULT '',

    source_type         TEXT NOT NULL DEFAULT 'provided',
    source_language_code TEXT NOT NULL DEFAULT '',
    is_original         INTEGER NOT NULL DEFAULT 0,

    provider            TEXT NOT NULL DEFAULT '',
    model_name          TEXT NOT NULL DEFAULT '',
    model_version       TEXT NOT NULL DEFAULT '',
    prompt_version      TEXT NOT NULL DEFAULT '',

    text_hash           TEXT NOT NULL DEFAULT '',
    source_version      TEXT NOT NULL DEFAULT '',
    translation_key     TEXT NOT NULL DEFAULT '',
    is_current          INTEGER NOT NULL DEFAULT 1,

    -- Migration 156 (July 2026) — PR-CATALOG-MULTILINGUA step 2:
    -- source_track_id is the FK back to this same table for
    -- audit-trail links from translations to their source-language
    -- track. source_text_hash is the persisted SHA-256 of the
    -- source text used for translation-key computation. Both
    -- match migrations/sqlite/156_text_track_spec_columns.sql.
    source_track_id     INTEGER
                        REFERENCES asset_text_tracks(id) ON DELETE SET NULL,
    source_text_hash  TEXT NOT NULL DEFAULT '',

    confidence          REAL,
    status              TEXT NOT NULL DEFAULT 'READY'
                        CHECK (status IN ('READY', 'PENDING', 'FAILED')),

    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at          TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_asset_text_tracks_asset
    ON asset_text_tracks (asset_id);
CREATE INDEX IF NOT EXISTS idx_asset_text_tracks_language
    ON asset_text_tracks (language_code, text_kind);
CREATE INDEX IF NOT EXISTS idx_asset_text_tracks_hash
    ON asset_text_tracks (text_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_asset_text_tracks_current
    ON asset_text_tracks (asset_id, language_code, text_kind)
    WHERE is_current = 1;
`

const CanonicalScriptLocalizationsTable = `
CREATE TABLE IF NOT EXISTS script_localizations (
    script_id          INTEGER NOT NULL,

    source_script_hash TEXT NOT NULL
                       CHECK (length(source_script_hash) > 0),

    language_code      TEXT NOT NULL
                       CHECK (length(language_code) >= 2),

    specscene_json     TEXT NOT NULL DEFAULT ''
                       CHECK (status != 'ready' OR length(specscene_json) > 0),

    translation_model  TEXT NOT NULL DEFAULT '',
    model_version      TEXT NOT NULL DEFAULT '',
    prompt_version     TEXT NOT NULL DEFAULT '',

    status             TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','running','ready','failed')),

    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE,

    UNIQUE(script_id, source_script_hash, language_code, model_version, prompt_version)
);
CREATE INDEX IF NOT EXISTS idx_script_localizations_script_id
    ON script_localizations (script_id);
CREATE INDEX IF NOT EXISTS idx_script_localizations_language_status
    ON script_localizations (language_code, status);
`
