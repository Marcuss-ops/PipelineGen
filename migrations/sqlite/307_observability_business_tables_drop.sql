-- database: observability
-- foreign_keys: off
-- DB-03 transition, step 1 of 2: observability is a telemetry/audit plane, not a
-- second business registry.
--
-- 267 quarantined the business schema it found, but the 148 unscoped migrations
-- kept recreating it on every subsequent boot, and 302 (scoped
-- primary,observability,cache) recreated asset_subtitle_artifacts here as well.
-- The live 2026-11-27 cleanup dropped the leaked copy out of band; this
-- migration makes that drop reproducible.
--
-- Why it does not break verifyDeclaredTables: migrations_verify.go scans the
-- corpus WITHOUT the ledger gate and treats a table whose newest statement is a
-- DROP (higher version than every in-scope declaration) as expected-absent, and
-- skips the indexes of an absent table. The declarations it must outrank are
-- the baseline (v0), 218 (replay_bundles — KEPT, see below) and 302
-- (asset_subtitle_artifacts, the newest business declaration here).
--
-- The keep set is authoritative and mirrors
-- cmd/admin/internal/database/plane_allowlist.go: api_requests, the run_* and
-- resource_observations telemetry, job_attempts (FK parent of run_observability,
-- written by the run recorder), replay_bundles (DB-04 retention plan entry),
-- outbox_events/monitor_enqueue_outbox and admin_mutation_audit.
--
-- foreign_keys: off is required because several of these tables are FK parents
-- of one another and of kept tables; a plain DROP under enforcement fires the
-- implicit DELETE's FK actions and aborts the migration.
--
-- DROP TABLE IF EXISTS keeps it idempotent: on a database already cleaned out of
-- band (the 2026-11-27 live state) every statement is a no-op.
--
-- Backup gating this migration: data/observability/api_requests.db.sqlite.bak-20261127-db03

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
DROP TABLE IF EXISTS monitored_sources;
DROP TABLE IF EXISTS operations;
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
DROP TABLE IF EXISTS render_attempt_analytics;
DROP TABLE IF EXISTS retrieved_image_details;
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
