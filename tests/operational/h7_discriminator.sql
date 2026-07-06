-- H7 (silent-no-op cascade) vs H5/H6 (orphan worker / resume-skip) discriminator
-- Per godlike/07 typed-error contract: 1-shot read-only diagnosis.

-- ─── STEP 0: TABLE SCHEMA ASSURANCE ────────────────────────────────
.print === STEP 1: SCHEMA SNAPSHOT ===
SELECT 'asset_versions columns' AS check_name, group_concat(name, ',') AS cols FROM pragma_table_info('asset_versions');
SELECT 'media_assets columns' AS check_name, group_concat(name, ',') AS cols FROM pragma_table_info('media_assets');

-- ─── STEP 2: ROW COUNT BASELINE ────────────────────────────────────
.print
.print === STEP 2: ROW COUNT BASELINE ===
SELECT 'media_assets total' AS metric, COUNT(*) AS value FROM media_assets
UNION ALL SELECT 'asset_versions total', COUNT(*) FROM asset_versions
UNION ALL SELECT 'jobs total', COUNT(*) FROM jobs
UNION ALL SELECT 'jobs media.stock', COUNT(*) FROM jobs WHERE type='media.stock'
UNION ALL SELECT 'outbox_events total', COUNT(*) FROM outbox_events
UNION ALL SELECT 'execution_steps stock.*', COUNT(*) FROM execution_steps WHERE step_key LIKE 'stock.%';

-- ─── STEP 3: CANONICAL DISCRIMINANT (user spec) ────────────────────
.print
.print === STEP 3A: Q1 — asset_versions grouped by asset_id, HAVING v_count > 1 ===
SELECT asset_id, COUNT(version_number) AS v_count FROM asset_versions GROUP BY asset_id HAVING v_count > 1;

.print Q1 verdict: 0 rows = H7 FALSE; >=1 row = H7 TRUE.

.print
.print === STEP 3B: Q2 — media_assets WHERE filename LIKE 'chunk_%' ===
SELECT COUNT(*) AS chunk_count FROM media_assets WHERE filename LIKE 'chunk_%';

.print Q2 verdict: 1 per distinct fp = H7 TRUE; 0 = H5/H6 TRUE.

-- ─── STEP 4: TRIANGULATION ─────────────────────────────────────────
.print
.print === STEP 4A: media_assets by source (which capabilities wrote) ===
SELECT source, COUNT(*) AS n FROM media_assets GROUP BY source ORDER BY n DESC LIMIT 20;

.print
.print === STEP 4B: asset_versions top-20 most-versioned asset_ids ===
SELECT asset_id, COUNT(*) AS v_count, MIN(created_at) AS first_at, MAX(created_at) AS last_at FROM asset_versions GROUP BY asset_id ORDER BY v_count DESC LIMIT 20;

.print
.print === STEP 4C: media_assets rows that share file_hash (silent no-op signature) ===
SELECT file_hash, COUNT(*) AS dup_count, MIN(created_at) AS first, MAX(updated_at) AS last FROM media_assets WHERE file_hash != '' GROUP BY file_hash HAVING dup_count > 1 ORDER BY dup_count DESC LIMIT 10;

.print
.print === STEP 4D: jobs media.stock recent (terminal state) ===
SELECT id, status, attempt, lease_id, completed_at, updated_at FROM jobs WHERE type='media.stock' ORDER BY updated_at DESC LIMIT 10;

.print
.print === STEP 4E: execution_steps for stock.finalize recent (resume-skip evidence) ===
SELECT job_id, step_key, status, started_at, completed_at FROM execution_steps WHERE step_key='stock.finalize' ORDER BY completed_at DESC LIMIT 10;

.print
.print === STEP 4F: outbox_events asset.index.* (post-publish emit evidence) ===
SELECT event_type, status, COUNT(*) AS n FROM outbox_events WHERE event_type LIKE 'asset.index.%' GROUP BY event_type, status ORDER BY n DESC LIMIT 10;

.print
.print === STEP 4G: INSERT-collision signature (H7 fingerprint: version_count > 1 + same filename + same file_hash) ===
SELECT ma.id, ma.filename, ma.source, ma.file_hash, ma.created_at, ma.updated_at, COUNT(av.version_number) AS v_count
FROM media_assets ma JOIN asset_versions av ON av.asset_id = ma.id
WHERE ma.filename LIKE 'chunk_%' OR ma.filename = 'metadata.json'
GROUP BY ma.id ORDER BY v_count DESC LIMIT 20;

.print
.print === STEP 4H: media_assets recent 20 (operator-visible post-publish state) ===
SELECT id, source, filename, file_hash, drive_file_id, created_at, updated_at, lifecycle_state FROM media_assets ORDER BY updated_at DESC LIMIT 20;

.print
.print === STEP 5: HARD VERDICT COMPUTATION ===
SELECT
  (SELECT COUNT(*) FROM media_assets WHERE filename LIKE 'chunk_%') AS H7_chunk_count,
  (SELECT IFNULL(SUM(c), 0) FROM (SELECT COUNT(*) AS c FROM asset_versions GROUP BY asset_id HAVING c > 1)) AS H7_asset_versions_dupes,
  (SELECT COUNT(*) FROM media_assets) AS total_media_assets,
  (SELECT COUNT(*) FROM asset_versions) AS total_asset_versions,
  (SELECT COUNT(*) FROM jobs WHERE type='media.stock') AS total_stock_jobs;

.print H7 TRUE  iff H7_chunk_count=1 AND H7_asset_versions_dupes>=1.
.print H5/H6 TRUE iff total_media_assets=0 AND total_asset_versions=0 (and stock jobs exist).

.print
.print === STEP 6: SCHEMA-STATE FRESHNESS ===
SELECT 'media_assets.latest_update' AS metric, IFNULL(MAX(updated_at), 'EMPTY') AS value FROM media_assets
UNION ALL SELECT 'jobs.latest_update', IFNULL(MAX(updated_at), 'EMPTY') FROM jobs
UNION ALL SELECT 'asset_versions.latest_create', IFNULL(MAX(created_at), 'EMPTY') FROM asset_versions;

.print
.print === END ===
