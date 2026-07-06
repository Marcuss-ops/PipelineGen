-- H7 close-the-loop: filename convention + jobs schema + cross-reference 13 dup asset_versions
.print === STEP 1: ACTUAL FILENAME CONVENTION IN media_assets ===
SELECT filename, COUNT(*) AS n FROM media_assets GROUP BY filename ORDER BY n DESC LIMIT 30;

.print
.print === STEP 2: filename LIKE patterns (testing multiple wildcard fits) ===
SELECT 'LIKE chunk_%' AS pattern, COUNT(*) AS n FROM media_assets WHERE filename LIKE 'chunk_%'
UNION ALL SELECT 'LIKE %chunk%', COUNT(*) FROM media_assets WHERE filename LIKE '%chunk%'
UNION ALL SELECT 'LIKE %mp4', COUNT(*) FROM media_assets WHERE filename LIKE '%mp4'
UNION ALL SELECT 'LIKE %_0.mp4', COUNT(*) FROM media_assets WHERE filename LIKE '%_0.mp4'
UNION ALL SELECT 'LIKE stock_%', COUNT(*) FROM media_assets WHERE filename LIKE 'stock_%'
UNION ALL SELECT 'LIKE %metadata%', COUNT(*) FROM media_assets WHERE filename LIKE '%metadata%'
UNION ALL SELECT 'source=stock total', COUNT(*) FROM media_assets WHERE source='stock';

.print
.print === STEP 3: jobs TABLE SCHEMA ===
SELECT name FROM pragma_table_info('jobs') ORDER BY cid;

.print
.print === STEP 4: RECENT STOCK JOBS DETAILED ===
SELECT id, status, lease_id, completed_at, updated_at FROM jobs WHERE type='media.stock' ORDER BY updated_at DESC LIMIT 5;

.print
.print === STEP 5: CROSS-REF — 13 asset_versions dupes vs their media_assets row ===
SELECT
  av.asset_id,
  COUNT(av.version_number) AS versions,
  ma.filename,
  ma.file_hash,
  ma.drive_file_id,
  ma.created_at,
  ma.updated_at,
  ma.lifecycle_state
FROM asset_versions av
LEFT JOIN media_assets ma ON ma.id = av.asset_id
GROUP BY av.asset_id
HAVING versions > 1
ORDER BY versions DESC
LIMIT 20;

.print
.print === STEP 6: dup-hash rows in media_assets (signature of byte-identical UPSERTs) ===
SELECT file_hash, COUNT(*) AS n, MIN(created_at) AS first_written, MAX(updated_at) AS last_updated
FROM media_assets WHERE file_hash != '' GROUP BY file_hash HAVING n > 1 ORDER BY n DESC LIMIT 10;

.print
.print === STEP 7: outbox_events for stock finalizer emit (post-publish evidence) ===
SELECT event_type, status, COUNT(*) AS n FROM outbox_events GROUP BY event_type, status ORDER BY n DESC LIMIT 20;

.print
.print === STEP 8: execution_steps — stock.finalize recency ===
SELECT job_id, step_key, status, started_at, completed_at FROM execution_steps WHERE step_key LIKE 'stock.%' ORDER BY completed_at DESC LIMIT 10;

.print === END ===
