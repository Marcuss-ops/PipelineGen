-- Canonical asset identity contraction.
--
-- The old generic registry is no longer a runtime write/read surface:
-- content_objects owns byte identity, media_assets owns logical media
-- identity, media_asset_sources owns provenance, and asset_locations owns
-- physical/remote locations. Preserve historical rows for audit/recovery,
-- but remove the ambiguous table names from the live contract.
--
-- SQLite updates foreign-key targets when a table is renamed, so the legacy
-- job_assets relation remains recoverable without introducing a cross-plane
-- FK into jobs.db.sqlite.
ALTER TABLE assets RENAME TO legacy_assets;
ALTER TABLE asset_sources RENAME TO legacy_asset_sources;
ALTER TABLE job_assets RENAME TO legacy_job_assets;
