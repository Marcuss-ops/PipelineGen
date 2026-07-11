-- 140_artlist_download_audit_license_release.sql
--
-- Extend the existing artlist_download_audit table so each download can be
-- linked to the license and release records that governed it, plus the
-- project and the user/principal that performed the download.
ALTER TABLE artlist_download_audit ADD COLUMN downloaded_at TEXT;
ALTER TABLE artlist_download_audit ADD COLUMN license_id TEXT
    REFERENCES asset_licenses(id) ON DELETE SET NULL;
ALTER TABLE artlist_download_audit ADD COLUMN release_id TEXT
    REFERENCES asset_releases(id) ON DELETE SET NULL;
ALTER TABLE artlist_download_audit ADD COLUMN project_id TEXT;
ALTER TABLE artlist_download_audit ADD COLUMN downloaded_by TEXT;

CREATE INDEX IF NOT EXISTS idx_artlist_download_audit_license
    ON artlist_download_audit (license_id);

CREATE INDEX IF NOT EXISTS idx_artlist_download_audit_release
    ON artlist_download_audit (release_id);

CREATE INDEX IF NOT EXISTS idx_artlist_download_audit_project
    ON artlist_download_audit (project_id);

CREATE INDEX IF NOT EXISTS idx_artlist_download_audit_downloaded_by
    ON artlist_download_audit (downloaded_by);
