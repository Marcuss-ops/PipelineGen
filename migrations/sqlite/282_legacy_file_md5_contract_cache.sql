-- database: cache
-- 282_legacy_file_md5_contract_cache.sql
--
-- Cache-plane half of 281_legacy_file_md5_contract.sql. The cache-plane
-- `stock_source_cache` acquired the renamed legacy bucket in 269 (it lives
-- in the cache database, so 281's primary scope cannot reach it).
--
-- Same demolition rule as 281: a non-SHA digest is a legacy record, not a
-- content address. The values are cleared, the column keeps them under the
-- honest name `content_sha256`.
--
-- IDEMPOTENCY: NOT idempotent (RENAME COLUMN has no IF EXISTS).

UPDATE stock_source_cache SET legacy_file_md5 = ''
 WHERE legacy_file_md5 != ''
   AND NOT (length(legacy_file_md5) = 64 AND legacy_file_md5 NOT GLOB '*[^0-9A-Fa-f]*');

ALTER TABLE stock_source_cache RENAME COLUMN legacy_file_md5 TO content_sha256;
