-- database: primary,cache,observability
-- DEMOL-13 step A: drop the quarantine and retired-provider tables.
--
-- Every table below is certified by ops/tools/db_census.py as having ZERO
-- runtime references (only the audit reachability map and gen_baseline still
-- spell some of the names) and ZERO rows on the 2026-11-26 live census
-- (docs/evidence/demol13-db-census-2026-11-26.json). They are the archives
-- migrations 263-268 created for operator recovery, plus the Artlist-era
-- tables of a retired provider and the media-authority retirement marker.
--
-- The backup that gates this migration is
-- data/backup/demol13-2026-11-26/ (SQLite .backup + sha256 manifest).
-- DROP TABLE IF EXISTS keeps the migration idempotent and safe on a fresh
-- database, where the historical tables were never created. Order matters:
-- children (artlist_query_clips, artlist_download_audit) go before the parents
-- so no intermediate schema leaves a foreign key pointing at a dropped table.

DROP TABLE IF EXISTS artlist_query_clips;
DROP TABLE IF EXISTS artlist_download_audit;
DROP TABLE IF EXISTS artlist_search_cache;
DROP TABLE IF EXISTS artlist_clips;
DROP TABLE IF EXISTS artlist_queries;
DROP TABLE IF EXISTS artlist_runs;
DROP TABLE IF EXISTS legacy_observability_artifact_sources;
DROP TABLE IF EXISTS legacy_observability_artifact_stages;
DROP TABLE IF EXISTS legacy_observability_artifacts;
DROP TABLE IF EXISTS legacy_observability_artlist_download_audit;
DROP TABLE IF EXISTS legacy_observability_artlist_runs;
DROP TABLE IF EXISTS legacy_observability_artlist_search_cache;
DROP TABLE IF EXISTS legacy_observability_asset_artifacts;
DROP TABLE IF EXISTS legacy_observability_asset_index;
DROP TABLE IF EXISTS legacy_observability_asset_licenses;
DROP TABLE IF EXISTS legacy_observability_asset_links;
DROP TABLE IF EXISTS legacy_observability_asset_locations;
DROP TABLE IF EXISTS legacy_observability_asset_processing;
DROP TABLE IF EXISTS legacy_observability_asset_releases;
DROP TABLE IF EXISTS legacy_observability_asset_render_variants;
DROP TABLE IF EXISTS legacy_observability_asset_renditions;
DROP TABLE IF EXISTS legacy_observability_asset_sources;
DROP TABLE IF EXISTS legacy_observability_asset_subtitle_artifacts;
DROP TABLE IF EXISTS legacy_observability_asset_text_track_segments;
DROP TABLE IF EXISTS legacy_observability_asset_text_tracks;
DROP TABLE IF EXISTS legacy_observability_asset_tree_nodes;
DROP TABLE IF EXISTS legacy_observability_asset_versions;
DROP TABLE IF EXISTS legacy_observability_asset_visual_summaries;
DROP TABLE IF EXISTS legacy_observability_assets;
DROP TABLE IF EXISTS legacy_observability_category_channels;
DROP TABLE IF EXISTS legacy_observability_characters;
DROP TABLE IF EXISTS legacy_observability_clip_folders;
DROP TABLE IF EXISTS legacy_observability_clip_search_terms;
DROP TABLE IF EXISTS legacy_observability_clip_storage_index;
DROP TABLE IF EXISTS legacy_observability_dead_letter_jobs;
DROP TABLE IF EXISTS legacy_observability_deliveries;
DROP TABLE IF EXISTS legacy_observability_delivery_log;
DROP TABLE IF EXISTS legacy_observability_drive_folder_catalog;
DROP TABLE IF EXISTS legacy_observability_execution_steps;
DROP TABLE IF EXISTS legacy_observability_gemma_memory_entries;
DROP TABLE IF EXISTS legacy_observability_gemma_script_chunks;
DROP TABLE IF EXISTS legacy_observability_gemma_script_outputs;
DROP TABLE IF EXISTS legacy_observability_generated_image_details;
DROP TABLE IF EXISTS legacy_observability_idempotency_keys;
DROP TABLE IF EXISTS legacy_observability_job_artifacts;
DROP TABLE IF EXISTS legacy_observability_job_assets;
DROP TABLE IF EXISTS legacy_observability_job_events;
DROP TABLE IF EXISTS legacy_observability_job_results;
DROP TABLE IF EXISTS legacy_observability_jobs;
DROP TABLE IF EXISTS legacy_observability_media_assets;
DROP TABLE IF EXISTS legacy_observability_media_assets_pipeline_events;
DROP TABLE IF EXISTS legacy_observability_media_bindings;
DROP TABLE IF EXISTS legacy_observability_media_candidates;
DROP TABLE IF EXISTS legacy_observability_media_concepts;
DROP TABLE IF EXISTS legacy_observability_media_query_cache;
DROP TABLE IF EXISTS legacy_observability_media_usage_events;
DROP TABLE IF EXISTS legacy_observability_monitor_enqueue_outbox;
DROP TABLE IF EXISTS legacy_observability_monitored_sources;
DROP TABLE IF EXISTS legacy_observability_operations;
DROP TABLE IF EXISTS legacy_observability_outbox_events;
DROP TABLE IF EXISTS legacy_observability_preparation_attempts;
DROP TABLE IF EXISTS legacy_observability_preparation_claim_snapshots;
DROP TABLE IF EXISTS legacy_observability_publication_intents;
DROP TABLE IF EXISTS legacy_observability_research_cache;
DROP TABLE IF EXISTS legacy_observability_retrieved_image_details;
DROP TABLE IF EXISTS legacy_observability_script_generation_logs;
DROP TABLE IF EXISTS legacy_observability_script_localizations;
DROP TABLE IF EXISTS legacy_observability_script_outline_sections;
DROP TABLE IF EXISTS legacy_observability_script_research_sources;
DROP TABLE IF EXISTS legacy_observability_script_sections;
DROP TABLE IF EXISTS legacy_observability_script_stock_matches;
DROP TABLE IF EXISTS legacy_observability_script_versions;
DROP TABLE IF EXISTS legacy_observability_scripts;
DROP TABLE IF EXISTS legacy_observability_search_queries;
DROP TABLE IF EXISTS legacy_observability_search_query_results;
DROP TABLE IF EXISTS legacy_observability_source_identity_registry;
DROP TABLE IF EXISTS legacy_observability_stock_artifacts;
DROP TABLE IF EXISTS legacy_observability_stock_batch_groups;
DROP TABLE IF EXISTS legacy_observability_stock_batches;
DROP TABLE IF EXISTS legacy_observability_stock_source_cache;
DROP TABLE IF EXISTS legacy_observability_subjects;
DROP TABLE IF EXISTS legacy_observability_transcript_cache;
DROP TABLE IF EXISTS legacy_observability_translation_cache;
DROP TABLE IF EXISTS legacy_observability_upload_intents;
DROP TABLE IF EXISTS legacy_observability_video_stats_history;
DROP TABLE IF EXISTS legacy_observability_voiceovers;
DROP TABLE IF EXISTS legacy_observability_worker_nodes;
DROP TABLE IF EXISTS legacy_observability_workflow_step_dependencies;
DROP TABLE IF EXISTS legacy_observability_workflow_steps;
DROP TABLE IF EXISTS legacy_observability_workflows;
DROP TABLE IF EXISTS legacy_observability_youtube_discoveries;
DROP TABLE IF EXISTS legacy_cache_artifact_entries;
DROP TABLE IF EXISTS legacy_cache_artifact_metrics;
DROP TABLE IF EXISTS legacy_cache_artlist_search;
DROP TABLE IF EXISTS legacy_cache_media_query;
DROP TABLE IF EXISTS legacy_cache_research;
DROP TABLE IF EXISTS legacy_cache_stock_source;
DROP TABLE IF EXISTS legacy_cache_transcript;
DROP TABLE IF EXISTS legacy_cache_translation;
DROP TABLE IF EXISTS legacy_cache_vidrush_provider;
DROP TABLE IF EXISTS legacy_asset_sources;
DROP TABLE IF EXISTS legacy_job_assets;
DROP TABLE IF EXISTS _media_authority_retirement;
DROP TABLE IF EXISTS legacy_assets;
