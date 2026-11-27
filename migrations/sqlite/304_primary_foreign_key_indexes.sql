-- database: primary
-- DB-07 (2026-11-27): every SQLite foreign-key child column without a
-- covering index forces a full scan on parent DELETE/UPDATE (and on
-- ON DELETE CASCADE). These are the business-plane FK columns that had no
-- index whose first column was the FK column. The jobs and observability
-- planes have their own migrations (sqlite_jobs/010, 305).

CREATE INDEX IF NOT EXISTS idx_fk_video_stats_history_video_id ON video_stats_history(video_id);
CREATE INDEX IF NOT EXISTS idx_fk_script_sections_script_id ON script_sections(script_id);
CREATE INDEX IF NOT EXISTS idx_fk_script_stock_matches_script_id ON script_stock_matches(script_id);
CREATE INDEX IF NOT EXISTS idx_fk_gemma_script_chunks_generation_id ON gemma_script_chunks(generation_id);
CREATE INDEX IF NOT EXISTS idx_fk_workflow_steps_job_id ON workflow_steps(job_id);
CREATE INDEX IF NOT EXISTS idx_fk_workflow_steps_workflow_id ON workflow_steps(workflow_id);
CREATE INDEX IF NOT EXISTS idx_fk_workflow_step_dependencies_depends_on_step_id ON workflow_step_dependencies(depends_on_step_id);
CREATE INDEX IF NOT EXISTS idx_fk_workflow_step_dependencies_step_id ON workflow_step_dependencies(step_id);
CREATE INDEX IF NOT EXISTS idx_fk_workflow_step_dependencies_workflow_id ON workflow_step_dependencies(workflow_id);
CREATE INDEX IF NOT EXISTS idx_fk_retrieved_image_details_asset_id ON retrieved_image_details(asset_id);
CREATE INDEX IF NOT EXISTS idx_fk_generated_image_details_asset_id ON generated_image_details(asset_id);
CREATE INDEX IF NOT EXISTS idx_fk_asset_visual_summaries_asset_id ON asset_visual_summaries(asset_id);
CREATE INDEX IF NOT EXISTS idx_fk_asset_text_tracks_source_track_id ON asset_text_tracks(source_track_id);
CREATE INDEX IF NOT EXISTS idx_fk_asset_provider_metadata_asset_id ON asset_provider_metadata(asset_id);
CREATE INDEX IF NOT EXISTS idx_fk_asset_subtitle_artifacts_text_track_id ON asset_subtitle_artifacts(text_track_id);
CREATE INDEX IF NOT EXISTS idx_fk_performance_artifacts_run_id ON performance_artifacts(run_id);
CREATE INDEX IF NOT EXISTS idx_fk_entity_image_catalog_materializations_candidate_id ON entity_image_catalog_materializations(candidate_id);
CREATE INDEX IF NOT EXISTS idx_fk_asset_links_asset_id ON asset_links(asset_id);
