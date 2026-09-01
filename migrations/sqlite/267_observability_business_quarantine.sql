-- database: observability
-- Observability must not be a second business registry. Preserve the
-- accidental historical business schema under explicit recovery names while
-- retaining only telemetry/audit tables in the runtime observability plane.
CREATE TABLE asset_links_v267 (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id TEXT NOT NULL,
    link_type TEXT NOT NULL,
    url TEXT NOT NULL,
    label TEXT,
    FOREIGN KEY (asset_id) REFERENCES asset_index(asset_id) ON DELETE CASCADE
);
INSERT INTO asset_links_v267 (id, asset_id, link_type, url, label)
SELECT id, asset_id, link_type, url, label FROM asset_links;
DROP TABLE asset_links;
ALTER TABLE asset_links_v267 RENAME TO asset_links;
ALTER TABLE artifact_sources RENAME TO legacy_observability_artifact_sources;
ALTER TABLE artifact_stages RENAME TO legacy_observability_artifact_stages;
ALTER TABLE artifacts RENAME TO legacy_observability_artifacts;
ALTER TABLE artlist_download_audit RENAME TO legacy_observability_artlist_download_audit;
ALTER TABLE artlist_runs RENAME TO legacy_observability_artlist_runs;
ALTER TABLE artlist_search_cache RENAME TO legacy_observability_artlist_search_cache;
ALTER TABLE asset_artifacts RENAME TO legacy_observability_asset_artifacts;
ALTER TABLE asset_index RENAME TO legacy_observability_asset_index;
ALTER TABLE asset_licenses RENAME TO legacy_observability_asset_licenses;
ALTER TABLE asset_links RENAME TO legacy_observability_asset_links;
ALTER TABLE asset_locations RENAME TO legacy_observability_asset_locations;
ALTER TABLE asset_processing RENAME TO legacy_observability_asset_processing;
ALTER TABLE asset_releases RENAME TO legacy_observability_asset_releases;
ALTER TABLE asset_render_variants RENAME TO legacy_observability_asset_render_variants;
ALTER TABLE asset_renditions RENAME TO legacy_observability_asset_renditions;
ALTER TABLE asset_sources RENAME TO legacy_observability_asset_sources;
ALTER TABLE asset_subtitle_artifacts RENAME TO legacy_observability_asset_subtitle_artifacts;
ALTER TABLE asset_text_track_segments RENAME TO legacy_observability_asset_text_track_segments;
ALTER TABLE asset_text_tracks RENAME TO legacy_observability_asset_text_tracks;
ALTER TABLE asset_tree_nodes RENAME TO legacy_observability_asset_tree_nodes;
ALTER TABLE asset_versions RENAME TO legacy_observability_asset_versions;
ALTER TABLE asset_visual_summaries RENAME TO legacy_observability_asset_visual_summaries;
ALTER TABLE assets RENAME TO legacy_observability_assets;
ALTER TABLE category_channels RENAME TO legacy_observability_category_channels;
ALTER TABLE characters RENAME TO legacy_observability_characters;
ALTER TABLE clip_folders RENAME TO legacy_observability_clip_folders;
ALTER TABLE clip_search_terms RENAME TO legacy_observability_clip_search_terms;
ALTER TABLE clip_storage_index RENAME TO legacy_observability_clip_storage_index;
ALTER TABLE dead_letter_jobs RENAME TO legacy_observability_dead_letter_jobs;
ALTER TABLE deliveries RENAME TO legacy_observability_deliveries;
ALTER TABLE delivery_log RENAME TO legacy_observability_delivery_log;
ALTER TABLE drive_folder_catalog RENAME TO legacy_observability_drive_folder_catalog;
ALTER TABLE execution_steps RENAME TO legacy_observability_execution_steps;
ALTER TABLE gemma_memory_entries RENAME TO legacy_observability_gemma_memory_entries;
ALTER TABLE gemma_script_chunks RENAME TO legacy_observability_gemma_script_chunks;
ALTER TABLE gemma_script_outputs RENAME TO legacy_observability_gemma_script_outputs;
ALTER TABLE generated_image_details RENAME TO legacy_observability_generated_image_details;
ALTER TABLE idempotency_keys RENAME TO legacy_observability_idempotency_keys;
ALTER TABLE job_artifacts RENAME TO legacy_observability_job_artifacts;
ALTER TABLE job_assets RENAME TO legacy_observability_job_assets;
ALTER TABLE job_events RENAME TO legacy_observability_job_events;
ALTER TABLE job_results RENAME TO legacy_observability_job_results;
ALTER TABLE jobs RENAME TO legacy_observability_jobs;
ALTER TABLE media_assets RENAME TO legacy_observability_media_assets;
ALTER TABLE media_assets_pipeline_events RENAME TO legacy_observability_media_assets_pipeline_events;
ALTER TABLE media_bindings RENAME TO legacy_observability_media_bindings;
ALTER TABLE media_candidates RENAME TO legacy_observability_media_candidates;
ALTER TABLE media_concepts RENAME TO legacy_observability_media_concepts;
ALTER TABLE media_query_cache RENAME TO legacy_observability_media_query_cache;
ALTER TABLE media_usage_events RENAME TO legacy_observability_media_usage_events;
ALTER TABLE monitor_enqueue_outbox RENAME TO legacy_observability_monitor_enqueue_outbox;
ALTER TABLE monitored_sources RENAME TO legacy_observability_monitored_sources;
ALTER TABLE operations RENAME TO legacy_observability_operations;
ALTER TABLE outbox_events RENAME TO legacy_observability_outbox_events;
ALTER TABLE preparation_attempts RENAME TO legacy_observability_preparation_attempts;
ALTER TABLE preparation_claim_snapshots RENAME TO legacy_observability_preparation_claim_snapshots;
ALTER TABLE publication_intents RENAME TO legacy_observability_publication_intents;
ALTER TABLE qdrant_cleanup_audit RENAME TO legacy_observability_qdrant_cleanup_audit;
ALTER TABLE qdrant_collections RENAME TO legacy_observability_qdrant_collections;
ALTER TABLE qdrantprojection_checkpoints RENAME TO legacy_observability_qdrantprojection_checkpoints;
ALTER TABLE qdrantprojection_dlq RENAME TO legacy_observability_qdrantprojection_dlq;
ALTER TABLE research_cache RENAME TO legacy_observability_research_cache;
ALTER TABLE retrieved_image_details RENAME TO legacy_observability_retrieved_image_details;
ALTER TABLE script_generation_logs RENAME TO legacy_observability_script_generation_logs;
ALTER TABLE script_localizations RENAME TO legacy_observability_script_localizations;
ALTER TABLE script_outline_sections RENAME TO legacy_observability_script_outline_sections;
ALTER TABLE script_research_sources RENAME TO legacy_observability_script_research_sources;
ALTER TABLE script_sections RENAME TO legacy_observability_script_sections;
ALTER TABLE script_stock_matches RENAME TO legacy_observability_script_stock_matches;
ALTER TABLE script_versions RENAME TO legacy_observability_script_versions;
ALTER TABLE scripts RENAME TO legacy_observability_scripts;
ALTER TABLE search_queries RENAME TO legacy_observability_search_queries;
ALTER TABLE search_query_results RENAME TO legacy_observability_search_query_results;
ALTER TABLE source_identity_registry RENAME TO legacy_observability_source_identity_registry;
ALTER TABLE stock_artifacts RENAME TO legacy_observability_stock_artifacts;
ALTER TABLE stock_batch_groups RENAME TO legacy_observability_stock_batch_groups;
ALTER TABLE stock_batches RENAME TO legacy_observability_stock_batches;
ALTER TABLE stock_source_cache RENAME TO legacy_observability_stock_source_cache;
ALTER TABLE subjects RENAME TO legacy_observability_subjects;
ALTER TABLE transcript_cache RENAME TO legacy_observability_transcript_cache;
ALTER TABLE translation_cache RENAME TO legacy_observability_translation_cache;
ALTER TABLE upload_intents RENAME TO legacy_observability_upload_intents;
ALTER TABLE video_stats_history RENAME TO legacy_observability_video_stats_history;
ALTER TABLE voiceovers RENAME TO legacy_observability_voiceovers;
ALTER TABLE worker_nodes RENAME TO legacy_observability_worker_nodes;
ALTER TABLE workflow_step_dependencies RENAME TO legacy_observability_workflow_step_dependencies;
ALTER TABLE workflow_steps RENAME TO legacy_observability_workflow_steps;
ALTER TABLE workflows RENAME TO legacy_observability_workflows;
ALTER TABLE youtube_discoveries RENAME TO legacy_observability_youtube_discoveries;

