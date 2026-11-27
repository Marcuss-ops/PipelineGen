-- database: cache
-- foreign_keys: off
-- DB-03 transition, step 2 of 2: the cache plane owns rebuildable caches only.
--
-- Same history as 307: 148 unscoped migrations replicated the business schema
-- into the cache database, the live 2026-11-27 cleanup dropped it out of band,
-- and this migration makes the drop reproducible so a fresh cache database
-- never carries it either.
--
-- verifyDeclaredTables compatibility relies on the same rule as 307: the corpus
-- scan is not gated by the ledger, and a DROP numbered above every in-scope
-- declaration marks the table expected-absent (its indexes are then skipped).
-- The newest business declaration here is 302 (asset_subtitle_artifacts).
--
-- The keep set mirrors cmd/admin/internal/database/plane_allowlist.go:
-- artifact_cache_entries/metrics, media_query_cache, render_attempt_analytics,
-- research_cache (299), semantic_plan_cache (299), stock_source_cache,
-- transcript_cache, translation_cache, vidrush_provider_cache (299),
-- admin_mutation_audit and schema_migrations. Everything else — including the
-- observability telemetry tables and job_attempts, which unscoped migrations had
-- copied here too — is not cache state.
--
-- DROP TABLE IF EXISTS keeps it idempotent on an already-cleaned database.
--
-- Backup gating this migration: data/cache/cache.db.sqlite.bak-20261127-db03

DROP TABLE IF EXISTS api_requests;
DROP TABLE IF EXISTS artifact_sources;
DROP TABLE IF EXISTS artifact_stages;
DROP TABLE IF EXISTS artifacts;
DROP TABLE IF EXISTS assembly_sessions;
DROP TABLE IF EXISTS asset_artifacts;
DROP TABLE IF EXISTS asset_index;
DROP TABLE IF EXISTS asset_licenses;
DROP TABLE IF EXISTS asset_links;
DROP TABLE IF EXISTS asset_locations;
DROP TABLE IF EXISTS asset_processing;
DROP TABLE IF EXISTS asset_provider_metadata;
DROP TABLE IF EXISTS asset_releases;
DROP TABLE IF EXISTS asset_render_variants;
DROP TABLE IF EXISTS asset_renditions;
DROP TABLE IF EXISTS asset_subtitle_artifacts;
DROP TABLE IF EXISTS asset_tags;
DROP TABLE IF EXISTS asset_text_track_segments;
DROP TABLE IF EXISTS asset_text_tracks;
DROP TABLE IF EXISTS asset_tree_nodes;
DROP TABLE IF EXISTS asset_versions;
DROP TABLE IF EXISTS asset_visual_summaries;
DROP TABLE IF EXISTS backup_registry;
DROP TABLE IF EXISTS benchmark_batch_jobs;
DROP TABLE IF EXISTS benchmark_batches;
DROP TABLE IF EXISTS benchmark_workloads;
DROP TABLE IF EXISTS canonical_mutations;
DROP TABLE IF EXISTS category_channels;
DROP TABLE IF EXISTS characters;
DROP TABLE IF EXISTS clip_folders;
DROP TABLE IF EXISTS clip_search_terms;
DROP TABLE IF EXISTS clip_storage_index;
DROP TABLE IF EXISTS content_objects;
DROP TABLE IF EXISTS control_plane_meta;
DROP TABLE IF EXISTS dead_letter_jobs;
DROP TABLE IF EXISTS deliveries;
DROP TABLE IF EXISTS delivery_log;
DROP TABLE IF EXISTS drive_folder_catalog;
DROP TABLE IF EXISTS entity_image_catalog_candidates;
DROP TABLE IF EXISTS entity_image_catalog_entities;
DROP TABLE IF EXISTS entity_image_catalog_materialization_attempts;
DROP TABLE IF EXISTS entity_image_catalog_materializations;
DROP TABLE IF EXISTS execution_steps;
DROP TABLE IF EXISTS gemma_memory_entries;
DROP TABLE IF EXISTS gemma_script_chunks;
DROP TABLE IF EXISTS gemma_script_outputs;
DROP TABLE IF EXISTS generated_image_details;
DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS job_artifacts;
DROP TABLE IF EXISTS job_asset_relations;
DROP TABLE IF EXISTS job_attempts;
DROP TABLE IF EXISTS job_checkpoints;
DROP TABLE IF EXISTS job_events;
DROP TABLE IF EXISTS job_payloads;
DROP TABLE IF EXISTS job_registry_events;
DROP TABLE IF EXISTS job_registry_metrics;
DROP TABLE IF EXISTS job_results;
DROP TABLE IF EXISTS job_steps;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS m2m_clients;
DROP TABLE IF EXISTS media_asset_sources;
DROP TABLE IF EXISTS media_assets;
DROP TABLE IF EXISTS media_assets_pipeline_events;
DROP TABLE IF EXISTS media_bindings;
DROP TABLE IF EXISTS media_candidates;
DROP TABLE IF EXISTS media_concepts;
DROP TABLE IF EXISTS media_usage_events;
DROP TABLE IF EXISTS monitor_enqueue_outbox;
DROP TABLE IF EXISTS monitored_sources;
DROP TABLE IF EXISTS operations;
DROP TABLE IF EXISTS outbox_events;
DROP TABLE IF EXISTS performance_artifacts;
DROP TABLE IF EXISTS performance_operations;
DROP TABLE IF EXISTS performance_runs;
DROP TABLE IF EXISTS performance_steps;
DROP TABLE IF EXISTS pipeline_runs;
DROP TABLE IF EXISTS preparation_attempts;
DROP TABLE IF EXISTS preparation_claim_snapshots;
DROP TABLE IF EXISTS preparation_dependencies;
DROP TABLE IF EXISTS preparation_job_units;
DROP TABLE IF EXISTS preparation_units;
DROP TABLE IF EXISTS projection_registry;
DROP TABLE IF EXISTS publication_intents;
DROP TABLE IF EXISTS registry_events;
DROP TABLE IF EXISTS registry_runs;
DROP TABLE IF EXISTS replay_bundles;
DROP TABLE IF EXISTS resource_observations;
DROP TABLE IF EXISTS retrieved_image_details;
DROP TABLE IF EXISTS run_artifact_checkpoints;
DROP TABLE IF EXISTS run_artifact_observations;
DROP TABLE IF EXISTS run_child_observations;
DROP TABLE IF EXISTS run_observability;
DROP TABLE IF EXISTS run_operation_observations;
DROP TABLE IF EXISTS run_report_snapshots;
DROP TABLE IF EXISTS run_resource_aggregates;
DROP TABLE IF EXISTS run_resource_reports;
DROP TABLE IF EXISTS run_resource_samples;
DROP TABLE IF EXISTS run_stage_observations;
DROP TABLE IF EXISTS script_localizations;
DROP TABLE IF EXISTS script_outline_sections;
DROP TABLE IF EXISTS script_research_sources;
DROP TABLE IF EXISTS script_sections;
DROP TABLE IF EXISTS script_semantic_items;
DROP TABLE IF EXISTS script_stock_matches;
DROP TABLE IF EXISTS script_versions;
DROP TABLE IF EXISTS script_visual_bindings;
DROP TABLE IF EXISTS scripts;
DROP TABLE IF EXISTS search_queries;
DROP TABLE IF EXISTS search_query_results;
DROP TABLE IF EXISTS source_identity_registry;
DROP TABLE IF EXISTS stock_artifacts;
DROP TABLE IF EXISTS stock_batch_groups;
DROP TABLE IF EXISTS stock_batches;
DROP TABLE IF EXISTS subjects;
DROP TABLE IF EXISTS upload_intents;
DROP TABLE IF EXISTS video_stats_history;
DROP TABLE IF EXISTS voiceovers;
DROP TABLE IF EXISTS worker_nodes;
DROP TABLE IF EXISTS workflow_step_dependencies;
DROP TABLE IF EXISTS workflow_steps;
DROP TABLE IF EXISTS workflows;
DROP TABLE IF EXISTS youtube_discoveries;
