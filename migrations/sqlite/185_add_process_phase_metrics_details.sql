-- database: primary
-- 185_add_process_phase_metrics_details.sql
-- Extensible numeric details for process-specific metrics such as
-- videos_found, download_bytes, output_duration_seconds and
-- segments_completed while the common columns remain stable.
ALTER TABLE process_phase_metrics
    ADD COLUMN details_json TEXT NOT NULL DEFAULT '{}';
