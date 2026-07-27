DROP INDEX IF EXISTS idx_subtitle_artifacts_drive_file_unique;

CREATE UNIQUE INDEX IF NOT EXISTS idx_subtitle_artifacts_drive_file_unique
ON asset_subtitle_artifacts(drive_file_id)
WHERE is_current = 1 AND drive_file_id <> '';
