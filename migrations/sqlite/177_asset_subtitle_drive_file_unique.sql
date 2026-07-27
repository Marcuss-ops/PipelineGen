-- A Drive subtitle file is owned by exactly one clip artifact.
-- Duplicate non-empty IDs would mean two clips share one ASS file.
CREATE UNIQUE INDEX IF NOT EXISTS idx_subtitle_artifacts_drive_file_unique
ON asset_subtitle_artifacts(drive_file_id)
WHERE drive_file_id <> '';
