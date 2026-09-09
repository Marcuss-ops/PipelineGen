-- 000_baseline_267.sql — consolidated SQLite baseline (post-267)
-- database: all
--
-- Generated from a clean DB bootstrapped via RunMigrationsOnDB
-- primary + observability (BASELINE_PLAN.md §1–3). This file is
-- idempotent (IF NOT EXISTS / IF NOT EXISTS) and replaces the
-- incremental museum 001..267 for fresh installs. Old DBs that
-- already carry 1..267 ledger rows skip this file (see
-- migrations_discovery.go::isHistoricalWindowCovered).
--
-- DO NOT EDIT MANUALLY — regenerate via `go run ./cmd/gen_baseline`
-- and verify with `go test ./internal/platform/sqlite -run TestMigrations_Smoke_Baseline`.

CREATE TABLE IF NOT EXISTS monitored_sources (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    kind TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    metadata_json TEXT,
    last_harvester_run TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS video_stats_history (
    video_id TEXT NOT NULL,
    timestamp TEXT NOT NULL DEFAULT (datetime('now')),
    view_count INTEGER,
    like_count INTEGER,
    comment_count INTEGER,
    PRIMARY KEY (video_id, timestamp),
    FOREIGN KEY (video_id) REFERENCES video_metadata(video_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS artlist_runs (
    id TEXT PRIMARY KEY,
    term TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    root_folder_id TEXT,
    tag_folder_id TEXT,
    requested_count INTEGER DEFAULT 0,
    found_count INTEGER DEFAULT 0,
    processed_count INTEGER DEFAULT 0,
    skipped_count INTEGER DEFAULT 0,
    failed_count INTEGER DEFAULT 0,
    error_message TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS scripts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    topic TEXT NOT NULL DEFAULT '',
    duration INTEGER NOT NULL DEFAULT 0,
    language TEXT NOT NULL DEFAULT 'en',
    template TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT '',
    narrative_text TEXT,
    timeline_json TEXT,
    entities_json TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    full_document TEXT,
    model_used TEXT NOT NULL DEFAULT '',
    ollama_base_url TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    parent_script_id INTEGER,
    is_deleted INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
, title TEXT NOT NULL DEFAULT '', tone TEXT NOT NULL DEFAULT '', target_words INTEGER NOT NULL DEFAULT 0, final_word_count INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'completed', idempotency_key TEXT NOT NULL DEFAULT '', specscene TEXT NOT NULL DEFAULT '', manifest_v2 TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS script_sections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    section_type TEXT NOT NULL DEFAULT '',
    section_title TEXT NOT NULL DEFAULT '',
    content TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0, word_count INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'completed', voiceover_link TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS script_stock_matches (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    segment_index INTEGER NOT NULL DEFAULT 0,
    stock_path TEXT NOT NULL DEFAULT '',
    stock_source TEXT NOT NULL DEFAULT '',
    score REAL NOT NULL DEFAULT 0,
    matched_terms TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS asset_tree_nodes (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    parent_id TEXT NOT NULL DEFAULT '',
    root_id TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    depth INTEGER NOT NULL DEFAULT 0,
    is_folder INTEGER NOT NULL DEFAULT 0,
    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS job_events (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    type TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    data_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS asset_index (
    asset_id TEXT PRIMARY KEY,
    asset_type TEXT NOT NULL,
    source TEXT NOT NULL,
    source_id TEXT NOT NULL,
    operation_key TEXT,
    group_name TEXT,
    subfolder TEXT,
    local_path TEXT,
    drive_link TEXT,
    download_link TEXT,
    file_hash TEXT,
    content_hash TEXT,
    status TEXT NOT NULL DEFAULT 'ready',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
, legacy_file_md5 TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_job_events_job ON job_events(job_id);
CREATE INDEX IF NOT EXISTS idx_asset_index_hash ON asset_index(content_hash);
CREATE INDEX IF NOT EXISTS idx_asset_index_source ON asset_index(source);
CREATE INDEX IF NOT EXISTS idx_asset_index_status ON asset_index(status);
CREATE INDEX IF NOT EXISTS idx_scripts_topic ON scripts(topic);
CREATE INDEX IF NOT EXISTS idx_asset_tree_path ON asset_tree_nodes(path);
CREATE INDEX IF NOT EXISTS idx_asset_tree_parent ON asset_tree_nodes(parent_id);
CREATE TABLE IF NOT EXISTS api_requests (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts DATETIME DEFAULT CURRENT_TIMESTAMP,
  request_id TEXT,
  method TEXT NOT NULL,
  path TEXT NOT NULL,
  status INTEGER,
  duration_ms REAL,
  client_ip TEXT,
  user_id TEXT,
  bytes_in INTEGER,
  bytes_out INTEGER,
  user_agent TEXT,
  error TEXT
);
CREATE INDEX IF NOT EXISTS idx_api_requests_ts ON api_requests(ts);
CREATE INDEX IF NOT EXISTS idx_api_requests_path_status ON api_requests(path, status);
CREATE INDEX IF NOT EXISTS idx_api_requests_user ON api_requests(user_id, ts);
CREATE INDEX IF NOT EXISTS idx_api_requests_request_id ON api_requests(request_id);
CREATE TABLE IF NOT EXISTS characters (
    id TEXT PRIMARY KEY,          
    name TEXT NOT NULL,           
    image_drive_id TEXT,          
    image_drive_link TEXT,        
    voice_id TEXT,                
    metadata_json TEXT DEFAULT '{}',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_characters_name ON characters(name);
CREATE TABLE IF NOT EXISTS "legacy_cache_artlist_search" (
    term TEXT PRIMARY KEY,
    clips_json TEXT NOT NULL,
    cached_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS gemma_script_outputs (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL DEFAULT 'default',
    mode TEXT NOT NULL DEFAULT 'generate',
    language TEXT DEFAULT 'en',
    title TEXT,
    prompt TEXT NOT NULL,
    normalized_input TEXT NOT NULL,
    input_hash TEXT NOT NULL,
    output_text TEXT,
    output_json TEXT,
    model TEXT,
    job_id TEXT,
    word_count INTEGER DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(channel_id, mode, input_hash)
);
CREATE INDEX IF NOT EXISTS idx_gemma_outputs_hash ON gemma_script_outputs(channel_id, mode, input_hash);
CREATE INDEX IF NOT EXISTS idx_gemma_outputs_channel ON gemma_script_outputs(channel_id, mode);
CREATE TABLE IF NOT EXISTS gemma_memory_entries (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL DEFAULT 'default',
    memory_type TEXT NOT NULL,
    topic_key TEXT,
    title TEXT,
    summary TEXT NOT NULL,
    content_text TEXT,
    content_json TEXT,
    source_generation_id TEXT,
    source_job_id TEXT,
    usefulness_score REAL DEFAULT 1.0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
, last_used_at TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_gemma_memory_channel ON gemma_memory_entries(channel_id, memory_type);
CREATE INDEX IF NOT EXISTS idx_gemma_memory_topic ON gemma_memory_entries(channel_id, topic_key);
CREATE INDEX IF NOT EXISTS idx_gemma_memory_type ON gemma_memory_entries(memory_type);
CREATE TABLE IF NOT EXISTS gemma_script_chunks (
    id TEXT PRIMARY KEY,
    generation_id TEXT NOT NULL,
    channel_id TEXT NOT NULL DEFAULT 'default',
    chunk_index INTEGER NOT NULL,
    chunk_type TEXT DEFAULT 'paragraph',
    topic_key TEXT,
    title TEXT,
    text TEXT NOT NULL,
    search_text TEXT NOT NULL,
    embedding_json TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY(generation_id) REFERENCES gemma_script_outputs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_gemma_chunks_channel ON gemma_script_chunks(channel_id, topic_key);
CREATE INDEX IF NOT EXISTS idx_gemma_chunks_search ON gemma_script_chunks(channel_id, search_text);
CREATE TABLE IF NOT EXISTS "legacy_cache_research" (
    key TEXT PRIMARY KEY,
    topic TEXT NOT NULL,
    language TEXT NOT NULL,
    max_steps INTEGER NOT NULL,
    source_text TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_used TEXT NOT NULL DEFAULT (datetime('now'))
, concept_id TEXT, topic_fingerprint TEXT, source_fingerprint TEXT, resolver_version TEXT, research_version TEXT, hit_count INTEGER NOT NULL DEFAULT 0, expires_at DATETIME, updated_at TEXT NOT NULL DEFAULT (datetime('now')), source_text_hash TEXT NOT NULL DEFAULT '', research_report_json TEXT NOT NULL DEFAULT '', sources_count INTEGER NOT NULL DEFAULT 0, claims_verified INTEGER NOT NULL DEFAULT 0, claims_rejected INTEGER NOT NULL DEFAULT 0, search_query_count INTEGER NOT NULL DEFAULT 0, pages_fetched INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS idx_research_cache_topic ON "legacy_cache_research"(topic);
CREATE INDEX IF NOT EXISTS idx_research_cache_last_used ON "legacy_cache_research"(last_used);
CREATE TABLE IF NOT EXISTS category_channels (
    id TEXT PRIMARY KEY,
    category TEXT NOT NULL,           
    channel_url TEXT NOT NULL,         
    channel_name TEXT NOT NULL DEFAULT '',
    keywords TEXT NOT NULL DEFAULT '[]',  
    min_views INTEGER NOT NULL DEFAULT 0,
    max_clip_duration INTEGER NOT NULL DEFAULT 60,
    drive_folder_id TEXT NOT NULL DEFAULT '',  

    
    semantic_keywords TEXT NOT NULL DEFAULT '[]',  
    min_semantic_score INTEGER NOT NULL DEFAULT 60,  
    playlist_end INTEGER NOT NULL DEFAULT -1,  

    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
, check_interval TEXT NOT NULL DEFAULT '24h', max_videos_per_run INTEGER NOT NULL DEFAULT 0, priority INTEGER NOT NULL DEFAULT 2, lookback_days INTEGER NOT NULL DEFAULT 0, max_segments INTEGER NOT NULL DEFAULT 0, segment_prompt TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, next_check_at TEXT, last_checked_at TEXT, consecutive_failures INTEGER NOT NULL DEFAULT 0, last_error TEXT, last_success_at TEXT, lease_owner TEXT, lease_until TEXT, monitor_source_kind TEXT NOT NULL DEFAULT 'youtube', monitor_handler_pin TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_category_channels_category ON category_channels(category);
CREATE INDEX IF NOT EXISTS idx_category_channels_url ON category_channels(channel_url);
CREATE TABLE IF NOT EXISTS "legacy_cache_transcript" (
    video_id TEXT PRIMARY KEY,           
    transcript_text TEXT NOT NULL,        
    language TEXT NOT NULL DEFAULT 'en',  
    cached_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_transcript_cache_cached_at ON "legacy_cache_transcript"(cached_at);
CREATE TABLE IF NOT EXISTS search_queries (
    id TEXT PRIMARY KEY,
    query TEXT NOT NULL,                     
    category TEXT NOT NULL,                  
    drive_folder_id TEXT DEFAULT '',         
    min_score INTEGER DEFAULT 60,            
    max_results INTEGER DEFAULT 5,           
    check_interval TEXT DEFAULT '7d',        
    last_run_at TEXT,                        
    last_video_published_at TEXT,            
    is_active INTEGER DEFAULT 1,             
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS search_query_results (
    query_id TEXT NOT NULL,
    video_id TEXT NOT NULL,                  
    video_title TEXT NOT NULL DEFAULT '',
    channel_name TEXT DEFAULT '',
    published_at TEXT,                       
    processed_at TEXT DEFAULT (datetime('now')),
    score INTEGER DEFAULT 0,                 
    PRIMARY KEY (query_id, video_id)
);
CREATE INDEX IF NOT EXISTS idx_search_queries_active ON search_queries(is_active);
CREATE INDEX IF NOT EXISTS idx_search_queries_interval ON search_queries(check_interval);
CREATE INDEX IF NOT EXISTS idx_search_query_results_video ON search_query_results(video_id);
CREATE INDEX IF NOT EXISTS idx_gemma_memory_last_used ON gemma_memory_entries(last_used_at);
CREATE TABLE IF NOT EXISTS dead_letter_jobs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    job_type TEXT NOT NULL,
    correlation_id TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL,
    payload_json TEXT,
    retry_count INTEGER NOT NULL DEFAULT 0,
    failed_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_dlq_job_id ON dead_letter_jobs(job_id);
CREATE INDEX IF NOT EXISTS idx_dlq_correlation ON dead_letter_jobs(correlation_id);
CREATE INDEX IF NOT EXISTS idx_dlq_failed_at ON dead_letter_jobs(failed_at);
CREATE TABLE IF NOT EXISTS "legacy_cache_translation" (
    cache_key TEXT PRIMARY KEY,
    source_text_hash TEXT NOT NULL,
    target_language TEXT NOT NULL,
    translated_text TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_used TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_translation_cache_source_hash ON "legacy_cache_translation"(source_text_hash);
CREATE INDEX IF NOT EXISTS idx_translation_cache_last_used ON "legacy_cache_translation"(last_used);
CREATE INDEX IF NOT EXISTS idx_translation_cache_lang ON "legacy_cache_translation"(target_language);
CREATE TABLE IF NOT EXISTS script_research_sources (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    query TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    snippet TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT 'web',
    used_in_sections TEXT NOT NULL DEFAULT '[]',
    relevance_score REAL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_research_sources_script ON script_research_sources(script_id);
CREATE TABLE IF NOT EXISTS script_outline_sections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    section_index INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    purpose TEXT NOT NULL DEFAULT '',
    target_words INTEGER NOT NULL DEFAULT 0,
    key_points_json TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL DEFAULT (datetime('now')), emotional_role TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_outline_sections_script ON script_outline_sections(script_id);
CREATE INDEX IF NOT EXISTS idx_outline_sections_index ON script_outline_sections(script_id, section_index);
CREATE TABLE IF NOT EXISTS script_versions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    final_text TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_script_versions_script ON script_versions(script_id);
CREATE INDEX IF NOT EXISTS idx_script_versions_lookup ON script_versions(script_id, version);
CREATE INDEX IF NOT EXISTS idx_scripts_status ON scripts(status);
CREATE INDEX IF NOT EXISTS idx_scripts_tone ON scripts(tone);
CREATE INDEX IF NOT EXISTS idx_script_sections_status ON script_sections(status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_script_research_unique
    ON script_research_sources(script_id, url, query)
    WHERE url != '' AND url IS NOT NULL;
CREATE TABLE IF NOT EXISTS media_assets (
    id TEXT PRIMARY KEY,
    source TEXT,
    name TEXT,
    tags TEXT,
    tags_norm TEXT,
    duration_ms INTEGER,
    url TEXT,
    media_type TEXT,
    local_path TEXT,
    relative_path TEXT,
    drive_file_id TEXT,
    drive_folder_id TEXT,
    drive_link TEXT,
    download_link TEXT,
    file_hash TEXT,
    embedding_json TEXT,
    metadata_json TEXT,
    visual_embedding TEXT,
    transcript_embedding TEXT,
    created_at TEXT,
    updated_at TEXT
, lifecycle_state TEXT NOT NULL DEFAULT 'ready', deleted_at      TEXT NOT NULL DEFAULT '', folder_id       TEXT NOT NULL DEFAULT '', parent_folder_id TEXT NOT NULL DEFAULT '', folder_path     TEXT NOT NULL DEFAULT '', category        TEXT NOT NULL DEFAULT '', filename        TEXT NOT NULL DEFAULT '', error           TEXT NOT NULL DEFAULT '', thumb_url       TEXT NOT NULL DEFAULT '', phash           TEXT NOT NULL DEFAULT '', search_text     TEXT NOT NULL DEFAULT '', scene_type      TEXT NOT NULL DEFAULT '', quality_score   REAL NOT NULL DEFAULT 0.0, reuse_count     INTEGER NOT NULL DEFAULT 0, last_used_at    TEXT NOT NULL DEFAULT '', thumbnail_url   TEXT    NOT NULL DEFAULT '', clip_page_url   TEXT    NOT NULL DEFAULT '', width INTEGER NOT NULL DEFAULT 0, height INTEGER NOT NULL DEFAULT 0, group_name TEXT NOT NULL DEFAULT '', search_terms TEXT NOT NULL DEFAULT '', index_state TEXT NOT NULL DEFAULT 'DISCOVERED', index_state_updated_at TEXT NOT NULL DEFAULT '', collection_version TEXT NOT NULL DEFAULT '', audio_embedding TEXT NOT NULL DEFAULT '[]', language TEXT NOT NULL DEFAULT '', youtube_video_id TEXT NOT NULL DEFAULT '', youtube_url TEXT NOT NULL DEFAULT '', start_time TEXT NOT NULL DEFAULT '', end_time TEXT NOT NULL DEFAULT '', workspace_id TEXT NOT NULL DEFAULT '', channel_id TEXT NOT NULL DEFAULT '', license TEXT NOT NULL DEFAULT '', source_version TEXT NOT NULL DEFAULT '', style TEXT NOT NULL DEFAULT '', external_id         TEXT NOT NULL DEFAULT '', discovered_via      TEXT NOT NULL DEFAULT '', discovered_at       TEXT NOT NULL DEFAULT '', monitored_source_id TEXT NOT NULL DEFAULT '', origin TEXT NOT NULL DEFAULT 'retrieved', provider TEXT NOT NULL DEFAULT '', enrich_state TEXT NOT NULL DEFAULT 'PENDING', enrich_state_updated_at TEXT NOT NULL DEFAULT '', source_provider   TEXT    NOT NULL DEFAULT '', source_video_id   TEXT    NOT NULL DEFAULT '', source_channel_id TEXT    NOT NULL DEFAULT '', source_url        TEXT    NOT NULL DEFAULT '', start_ms          INTEGER NOT NULL DEFAULT 0, end_ms            INTEGER NOT NULL DEFAULT 0, original_language TEXT    NOT NULL DEFAULT '', title             TEXT    NOT NULL DEFAULT '', binary_sha256     TEXT    NOT NULL DEFAULT '', semantic_hash     TEXT    NOT NULL DEFAULT '', rights_status     TEXT    NOT NULL DEFAULT 'review_required', policy_version    TEXT    NOT NULL DEFAULT 'v1', asset_state TEXT NOT NULL DEFAULT 'DISCOVERED', license_basis TEXT NOT NULL DEFAULT '', owner_channel_id TEXT NOT NULL DEFAULT '', allowed_channels TEXT NOT NULL DEFAULT '[]', allowed_regions TEXT NOT NULL DEFAULT '[]', expires_at TEXT NOT NULL DEFAULT '', review_status TEXT NOT NULL DEFAULT 'none', asset_version  TEXT NOT NULL DEFAULT '', asset_location TEXT NOT NULL DEFAULT '', rendition      TEXT NOT NULL DEFAULT '', admin_version INTEGER NOT NULL DEFAULT 0, namespace TEXT NOT NULL DEFAULT '', asset_kind TEXT NOT NULL DEFAULT '', source_type TEXT NOT NULL DEFAULT '', semantic_role TEXT NOT NULL DEFAULT '', content_sha256 TEXT NOT NULL DEFAULT '', legacy_file_md5 TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_media_assets_youtube_video_id
  ON media_assets(json_extract(COALESCE(metadata_json, '{}'), '$.youtube_video_id'))
  WHERE json_extract(COALESCE(metadata_json, '{}'), '$.youtube_video_id') IS NOT NULL
    AND json_extract(COALESCE(metadata_json, '{}'), '$.youtube_video_id') != '';
CREATE TABLE IF NOT EXISTS artifact_sources (
    source_id         TEXT PRIMARY KEY,
    artifact_id       TEXT NOT NULL,
    source_type       TEXT NOT NULL DEFAULT '',
    source_reference  TEXT NOT NULL DEFAULT '',
    source_account_id TEXT NOT NULL DEFAULT '',
    imported_at       TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_artifact_sources_artifact ON artifact_sources(artifact_id);
CREATE TABLE IF NOT EXISTS job_artifacts (
    job_id      TEXT NOT NULL,
    artifact_id TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT '',
    ordinal     INTEGER NOT NULL DEFAULT 0,
    required    INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (job_id, artifact_id)
);
CREATE INDEX IF NOT EXISTS idx_job_artifacts_job ON job_artifacts(job_id);
CREATE TABLE IF NOT EXISTS artifacts (
    id              TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL DEFAULT 'unknown',
    status          TEXT NOT NULL DEFAULT 'STAGING'
        CHECK (status IN ('STAGING','VERIFYING','READY','FAILED','QUARANTINED','DELETED')),
    storage_backend TEXT NOT NULL DEFAULT 'local',
    storage_key     TEXT NOT NULL DEFAULT '',
    sha256          TEXT NOT NULL DEFAULT '',
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    mime_type       TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    verified_at     TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_artifacts_sha256 ON artifacts(sha256) WHERE sha256 != '';
CREATE INDEX IF NOT EXISTS idx_artifacts_job ON artifacts(job_id);
CREATE INDEX IF NOT EXISTS idx_artifacts_status ON artifacts(status);
CREATE TABLE IF NOT EXISTS deliveries (
    id               TEXT PRIMARY KEY,
    artifact_id      TEXT NOT NULL,
    target_id        TEXT NOT NULL DEFAULT '',
    provider         TEXT NOT NULL DEFAULT 'drive',
    status           TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING','LEASED','RUNNING','RETRY_WAIT','SUCCEEDED','FAILED','BLOCKED_AUTH','CANCELLED')),
    attempt_count    INTEGER NOT NULL DEFAULT 0,
    max_attempts     INTEGER NOT NULL DEFAULT 3,
    next_attempt_at  TEXT,
    lease_id         TEXT NOT NULL DEFAULT '',
    lease_expires_at TEXT,
    remote_id        TEXT NOT NULL DEFAULT '',
    remote_url       TEXT NOT NULL DEFAULT '',
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at     TEXT
);
CREATE INDEX IF NOT EXISTS idx_deliveries_status ON deliveries(status, next_attempt_at);
CREATE INDEX IF NOT EXISTS idx_deliveries_artifact ON deliveries(artifact_id);
CREATE TABLE IF NOT EXISTS "legacy_assets" (
    asset_id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('voiceover','scene_image','stock_clip','music','font','subtitle','thumbnail')),
    status TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING','READY','FAILED','DELETED')),

    sha256 TEXT NOT NULL UNIQUE,
    storage_backend TEXT NOT NULL DEFAULT 'local',
    storage_key TEXT NOT NULL UNIQUE,

    mime_type TEXT,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER,
    width INTEGER,
    height INTEGER,

    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    verified_at TEXT,
    last_accessed_at TEXT,
    deleted_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_assets_sha256 ON "legacy_assets"(sha256);
CREATE INDEX IF NOT EXISTS idx_assets_kind ON "legacy_assets"(kind, status);
CREATE INDEX IF NOT EXISTS idx_assets_storage ON "legacy_assets"(storage_backend, storage_key);
CREATE TABLE IF NOT EXISTS "legacy_asset_sources" (
    source_id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL,
    source_type TEXT NOT NULL,
    source_reference TEXT NOT NULL,
    source_account_id TEXT,
    imported_at TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY(asset_id) REFERENCES "legacy_assets"(asset_id)
);
CREATE INDEX IF NOT EXISTS idx_asset_sources_asset ON "legacy_asset_sources"(asset_id);
CREATE TABLE IF NOT EXISTS asset_locations (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id        TEXT NOT NULL
                    REFERENCES media_assets(id)
                    ON DELETE CASCADE,
    location_kind   TEXT NOT NULL        
                    CHECK (location_kind IN ('local', 'drive', 'object_storage')),
    uri             TEXT NOT NULL,       
    mime_type       TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    file_hash       TEXT NOT NULL DEFAULT '',
    is_primary      INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT '', external_id  TEXT NOT NULL DEFAULT '', access_url   TEXT NOT NULL DEFAULT '', download_url TEXT NOT NULL DEFAULT '', web_view_link TEXT NOT NULL DEFAULT '', legacy_file_md5 TEXT NOT NULL DEFAULT '',
    UNIQUE (asset_id, location_kind)
);
CREATE INDEX IF NOT EXISTS idx_asset_locations_asset
    ON asset_locations (asset_id);
CREATE INDEX IF NOT EXISTS idx_asset_locations_primary
    ON asset_locations (asset_id)
    WHERE is_primary = 1;
CREATE TABLE IF NOT EXISTS worker_nodes (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    status              TEXT NOT NULL,
    session_id          TEXT NOT NULL,
    session_expires_at  TEXT NOT NULL,
    capabilities_json   TEXT NOT NULL,
    version             TEXT NOT NULL,
    hostname            TEXT NOT NULL,
    last_seen_at        TEXT NOT NULL,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_worker_nodes_status ON worker_nodes(status);
CREATE INDEX IF NOT EXISTS idx_worker_nodes_session ON worker_nodes(session_id);
CREATE TABLE IF NOT EXISTS workflows (
    id              TEXT PRIMARY KEY,
    type            TEXT NOT NULL,
    version         INTEGER NOT NULL,
    status          TEXT NOT NULL,
    correlation_id  TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL DEFAULT '',
    input_json      TEXT NOT NULL,
    output_json     TEXT NOT NULL DEFAULT '{}',
    error_code      TEXT NOT NULL DEFAULT '',
    error_message   TEXT NOT NULL DEFAULT '',
    revision        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    started_at      TEXT,
    completed_at    TEXT,
    cancelled_at    TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS workflows_idempotency_idx
    ON workflows(type, idempotency_key)
    WHERE idempotency_key != '';
CREATE TABLE IF NOT EXISTS workflow_steps (
    id              TEXT PRIMARY KEY,
    workflow_id     TEXT NOT NULL,
    step_key        TEXT NOT NULL,
    step_type       TEXT NOT NULL,
    status          TEXT NOT NULL,
    position        INTEGER NOT NULL,
    job_id          TEXT,
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 3,
    input_json      TEXT NOT NULL DEFAULT '{}',
    output_json     TEXT NOT NULL DEFAULT '{}',
    error_code      TEXT NOT NULL DEFAULT '',
    error_message   TEXT NOT NULL DEFAULT '',
    available_at    TEXT,
    started_at      TEXT,
    completed_at    TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,

    FOREIGN KEY(workflow_id) REFERENCES workflows(id),
    FOREIGN KEY(job_id) REFERENCES jobs(id),
    UNIQUE(workflow_id, step_key)
);
CREATE TABLE IF NOT EXISTS workflow_step_dependencies (
    workflow_id        TEXT NOT NULL,
    step_id            TEXT NOT NULL,
    depends_on_step_id TEXT NOT NULL,

    PRIMARY KEY(step_id, depends_on_step_id),
    FOREIGN KEY(workflow_id) REFERENCES workflows(id),
    FOREIGN KEY(step_id) REFERENCES workflow_steps(id),
    FOREIGN KEY(depends_on_step_id) REFERENCES workflow_steps(id)
);
CREATE TABLE IF NOT EXISTS asset_processing (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id        TEXT NOT NULL,
    step            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    started_at      TEXT,
    completed_at    TEXT,
    error_message   TEXT NOT NULL DEFAULT '',
    attempt_count   INTEGER NOT NULL DEFAULT 1,
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT '',
    UNIQUE (asset_id, step)
);
CREATE INDEX IF NOT EXISTS idx_asset_processing_asset
    ON asset_processing(asset_id);
CREATE INDEX IF NOT EXISTS idx_asset_processing_status
    ON asset_processing(status);
CREATE INDEX IF NOT EXISTS idx_media_assets_lifecycle ON media_assets(lifecycle_state);
CREATE INDEX IF NOT EXISTS idx_media_assets_category  ON media_assets(category);
CREATE INDEX IF NOT EXISTS idx_media_assets_folder_id ON media_assets(folder_id);
CREATE INDEX IF NOT EXISTS idx_asset_locations_external_id
    ON asset_locations(external_id)
    WHERE external_id != '';
CREATE TABLE IF NOT EXISTS delivery_log (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  asset_id      TEXT NOT NULL,
  endpoint_url  TEXT NOT NULL,
  delivery_id   TEXT NOT NULL UNIQUE,
  status_code   INTEGER,
  response_hash TEXT,
  delivered_at  TEXT,
  created_at    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_delivery_log_asset_id
  ON delivery_log (asset_id);
CREATE INDEX IF NOT EXISTS idx_delivery_log_delivered_at
  ON delivery_log (delivered_at);
CREATE TABLE IF NOT EXISTS "jobs" (
    id              TEXT    NOT NULL PRIMARY KEY,
    type            TEXT    NOT NULL,
    status          TEXT    NOT NULL CHECK(status IN (
                            'QUEUED',
                            'LEASED',
                            'RUNNING',
                            'SUCCEEDED',
                            'RETRY_WAIT',
                            'FAILED',
                            'CANCELLED'
                        )),
    priority        INTEGER NOT NULL DEFAULT 0,
    project         TEXT    NOT NULL DEFAULT '',
    video_name      TEXT    NOT NULL DEFAULT '',
    active_key      TEXT    NOT NULL DEFAULT '',
    correlation_id  TEXT    NOT NULL DEFAULT '',
    payload_json    TEXT    NOT NULL DEFAULT '{}',
    result_json     TEXT    NOT NULL DEFAULT '{}',
    progress        INTEGER NOT NULL DEFAULT 0,
    error           TEXT    NOT NULL DEFAULT '',
    retry_count     INTEGER NOT NULL DEFAULT 0,
    max_retries     INTEGER NOT NULL DEFAULT 3,
    worker_id       TEXT    NOT NULL DEFAULT '',
    lease_id        TEXT    NOT NULL DEFAULT '',
    lease_expiry    TEXT,
    created_at      TEXT    NOT NULL,
    updated_at      TEXT    NOT NULL,
    started_at      TEXT,
    completed_at    TEXT,
    cancelled_at    TEXT,
    revision        INTEGER NOT NULL DEFAULT 1
, parent_state_typed TEXT NOT NULL DEFAULT '', git_sha TEXT NOT NULL DEFAULT '', app_version TEXT NOT NULL DEFAULT '', parent_job_id TEXT NOT NULL DEFAULT '', root_job_id TEXT NOT NULL DEFAULT '', project_id TEXT NOT NULL DEFAULT '', video_id TEXT NOT NULL DEFAULT '', payload_hash TEXT NOT NULL DEFAULT '', host TEXT NOT NULL DEFAULT '', duration_ms INTEGER NOT NULL DEFAULT 0, client_id TEXT NOT NULL DEFAULT '', idempotency_key TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_jobs_claim
    ON jobs(status, priority DESC, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_jobs_expired_leases
    ON jobs(lease_expiry)
    WHERE status IN ('LEASED', 'RUNNING');
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_active_lease
    ON jobs(lease_id)
    WHERE lease_id IS NOT NULL AND lease_id <> '';
CREATE TABLE IF NOT EXISTS "legacy_job_assets" (
    job_id TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('voiceover','scene_image','stock_clip','music','font','subtitle','thumbnail')),
    ordinal INTEGER NOT NULL DEFAULT 0,
    required INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),

    PRIMARY KEY(job_id, role, ordinal),
    FOREIGN KEY(job_id) REFERENCES jobs(id),
    FOREIGN KEY(asset_id) REFERENCES "legacy_assets"(asset_id)
);
CREATE TABLE IF NOT EXISTS outbox_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL DEFAULT '',
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '',
    event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    last_error TEXT NOT NULL DEFAULT '',
    next_attempt_at TEXT,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_id TEXT NOT NULL DEFAULT '',
    lease_expiry TEXT,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT ''
, priority INTEGER NOT NULL DEFAULT 5);
CREATE UNIQUE INDEX IF NOT EXISTS ux_outbox_events_event_key
    ON outbox_events(event_key);
CREATE INDEX IF NOT EXISTS idx_outbox_events_status_next_attempt
    ON outbox_events(status, next_attempt_at, id);
CREATE TABLE IF NOT EXISTS clip_folders (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    video_id TEXT NOT NULL DEFAULT '',
    folder_id TEXT NOT NULL DEFAULT '',
    folder_path TEXT NOT NULL DEFAULT '',
    local_folder_path TEXT NOT NULL DEFAULT '',
    group_name TEXT NOT NULL DEFAULT '',
    manifest_txt_path TEXT NOT NULL DEFAULT '',
    manifest_json_path TEXT NOT NULL DEFAULT '',
    clip_count INTEGER NOT NULL DEFAULT 0,
    processed_count INTEGER NOT NULL DEFAULT 0,
    failed_count INTEGER NOT NULL DEFAULT 0,
    skipped_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    search_key TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_clip_folders_search_key ON clip_folders(search_key);
CREATE INDEX IF NOT EXISTS idx_media_assets_index_state
    ON media_assets(index_state);
CREATE TABLE IF NOT EXISTS idempotency_keys (
    key TEXT PRIMARY KEY,                       
    body_hash TEXT NOT NULL DEFAULT '',         
    status TEXT NOT NULL DEFAULT 'in_flight',   
    response_status INTEGER NOT NULL DEFAULT 0, 
    response_body TEXT NOT NULL DEFAULT '',     
    response_content_type TEXT NOT NULL DEFAULT '', 
    created_at TEXT NOT NULL,                   
    expires_at TEXT NOT NULL,                   
    last_replayed_at TEXT NOT NULL DEFAULT ''   
);
CREATE INDEX IF NOT EXISTS idx_idempotency_keys_expires_at
    ON idempotency_keys(expires_at);
CREATE INDEX IF NOT EXISTS idx_media_assets_collection_version
    ON media_assets(collection_version);
CREATE TABLE IF NOT EXISTS qdrant_cleanup_audit (
    run_id           TEXT PRIMARY KEY,
    collection       TEXT NOT NULL,
    started_at       TEXT NOT NULL,
    completed_at     TEXT NOT NULL,
    status           TEXT NOT NULL,
    points_scanned   INTEGER NOT NULL DEFAULT 0,
    points_affected  INTEGER NOT NULL DEFAULT 0,
    errors_json      TEXT NOT NULL DEFAULT '[]',
    dry_run          INTEGER NOT NULL DEFAULT 0,
    keys_redacted_json TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX IF NOT EXISTS idx_qdrant_cleanup_audit_collection_completed
    ON qdrant_cleanup_audit(collection, completed_at);
CREATE INDEX IF NOT EXISTS idx_scripts_idempotency_key
    ON scripts(idempotency_key, language);
CREATE INDEX IF NOT EXISTS idx_media_assets_lifecycle_state
  ON media_assets(lifecycle_state);
CREATE TABLE IF NOT EXISTS subjects (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
, slug            TEXT    NOT NULL DEFAULT '', uuid            TEXT    NOT NULL DEFAULT '', display_name    TEXT    NOT NULL DEFAULT '', display_name_norm TEXT NOT NULL DEFAULT '', aliases         TEXT    NOT NULL DEFAULT '[]', kind            TEXT    NOT NULL DEFAULT 'person', origin          TEXT    NOT NULL DEFAULT 'image', category        TEXT    NOT NULL DEFAULT '', wikidata_id     TEXT    NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS asset_versions (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id        TEXT NOT NULL
                    REFERENCES media_assets(id)
                    ON DELETE CASCADE,
    version_number  INTEGER NOT NULL,
    source_uri      TEXT NOT NULL DEFAULT '',
    file_hash       TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    mime_type       TEXT NOT NULL DEFAULT '',
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT '', legacy_file_md5 TEXT NOT NULL DEFAULT '',

    
    
    
    
    
    UNIQUE (asset_id, version_number)
);
CREATE INDEX IF NOT EXISTS idx_asset_versions_asset
    ON asset_versions (asset_id);
CREATE INDEX IF NOT EXISTS idx_category_channels_monitor_due
    ON category_channels(enabled, next_check_at)
    WHERE enabled = 1;
CREATE INDEX IF NOT EXISTS idx_category_channels_lease_reclaim
    ON category_channels(lease_until)
    WHERE lease_until IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_category_channels_consecutive_failures
    ON category_channels(consecutive_failures)
    WHERE consecutive_failures > 0;
CREATE INDEX IF NOT EXISTS idx_category_channels_last_success_at
    ON category_channels(last_success_at);
CREATE UNIQUE INDEX IF NOT EXISTS idx_media_assets_ext_discovered
    ON media_assets(external_id, discovered_via)
    WHERE external_id != '' AND discovered_via != '';
CREATE INDEX IF NOT EXISTS idx_media_assets_discovered_via
    ON media_assets(discovered_via)
    WHERE discovered_via != '';
CREATE INDEX IF NOT EXISTS idx_media_assets_monitored_source_id
    ON media_assets(monitored_source_id)
    WHERE monitored_source_id != '';
CREATE TABLE IF NOT EXISTS qdrantprojection_checkpoints (
    
    
    
    job_id              TEXT PRIMARY KEY,

    
    
    
    target_collection   TEXT NOT NULL,

    
    
    
    
    last_indexed_id     TEXT NOT NULL DEFAULT '',

    
    
    indexed_count       INTEGER NOT NULL DEFAULT 0,

    
    
    
    
    error_count         INTEGER NOT NULL DEFAULT 0,

    
    
    skipped_count       INTEGER NOT NULL DEFAULT 0,

    
    
    
    
    started_at          TEXT NOT NULL,

    
    
    
    
    
    finished_at         TEXT,

    
    
    
    last_batch_at       TEXT,

    
    
    
    
    
    
    
    
    
    
    status              TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('running','succeeded','failed','abandoned')),

    
    
    
    
    
    updated_at          TEXT NOT NULL,

    
    
    
    last_error          TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_qdrantprojection_checkpoints_status_lastbatch
    ON qdrantprojection_checkpoints (status, last_batch_at);
CREATE TABLE IF NOT EXISTS qdrantprojection_dlq (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,

    
    
    job_id              TEXT NOT NULL,

    
    
    
    asset_id            TEXT NOT NULL DEFAULT '',

    
    
    
    
    
    
    
    reason_category     TEXT NOT NULL DEFAULT 'other'
        CHECK (reason_category IN ('embedding_obsolete','content_hash_missing','dimension_mismatch','payload_invalid','other')),

    
    
    last_error          TEXT NOT NULL DEFAULT '',

    
    observed_at         TEXT NOT NULL,

    
    
    resolved_at         TEXT,

    
    
    
    resolved_by         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_qdrantprojection_dlq_job_observed
    ON qdrantprojection_dlq (job_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_qdrantprojection_dlq_resolved_observed
    ON qdrantprojection_dlq (resolved_at, observed_at);
CREATE INDEX IF NOT EXISTS idx_qdrantprojection_dlq_category
    ON qdrantprojection_dlq (reason_category);
CREATE TABLE IF NOT EXISTS qdrant_collections (
    
    
    
    
    collection_name        TEXT PRIMARY KEY,

    
    
    
    
    schema_version         TEXT NOT NULL DEFAULT 'v3',

    
    
    created_at             TEXT NOT NULL,

    
    
    
    
    indexed_at             TEXT,

    
    
    verified_at            TEXT,

    
    
    
    
    
    
    promoted_at            TEXT,

    
    retired_at             TEXT,

    
    
    
    
    point_count            INTEGER NOT NULL DEFAULT 0,

    
    
    
    
    
    verification_hash      TEXT,

    
    
    
    
    
    
    
    
    
    
    status                 TEXT NOT NULL DEFAULT 'created'
        CHECK (status IN ('created','indexing','verified','active','reindexing','in_use','retired')),

    
    
    updated_at             TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_qdrant_collections_status_promoted
    ON qdrant_collections (status, promoted_at);
CREATE INDEX IF NOT EXISTS idx_qdrant_collections_schema_status
    ON qdrant_collections (schema_version, status);
CREATE TABLE IF NOT EXISTS "youtube_discoveries" (
    id                TEXT PRIMARY KEY,
    channel_id        TEXT NOT NULL,
    video_id          TEXT NOT NULL,
    policy_version    TEXT NOT NULL DEFAULT 'v1',
    state             TEXT NOT NULL DEFAULT 'pending',
    attempt_count     INTEGER NOT NULL DEFAULT 0,
    discovered_at     TEXT NOT NULL DEFAULT (datetime('now')),
    enqueued_at       TEXT,
    next_retry_at     TEXT,
    lease_owner       TEXT,
    lease_until       TEXT,
    job_id            TEXT,
    last_error        TEXT,
    source_url        TEXT,
    title             TEXT,
    
    
    
    outcome           TEXT NOT NULL DEFAULT 'pending',
    rejection_reason  TEXT,
    updated_at        TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(channel_id, video_id, policy_version)
);
CREATE INDEX IF NOT EXISTS idx_youtube_discoveries_watermark
    ON youtube_discoveries(channel_id, discovered_at DESC);
CREATE INDEX IF NOT EXISTS idx_youtube_discoveries_retry
    ON youtube_discoveries(next_retry_at)
    WHERE state = 'rejected_retryable';
CREATE INDEX IF NOT EXISTS idx_youtube_discoveries_lease
    ON youtube_discoveries(lease_until)
    WHERE state IN ('pending', 'analyzing');
CREATE TABLE IF NOT EXISTS upload_intents (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    voiceover_id    TEXT    NOT NULL,
    drive_file_id   TEXT    NOT NULL DEFAULT '',
    status          TEXT    NOT NULL
        CHECK (status IN ('pending', 'uploaded', 'finalized', 'completed', 'failed')),
    reason          TEXT    NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,

    
    
    
    
    UNIQUE(voiceover_id)
);
CREATE INDEX IF NOT EXISTS idx_upload_intents_status_updated_at
    ON upload_intents (status, updated_at);
CREATE INDEX IF NOT EXISTS idx_upload_intents_voiceover_id
    ON upload_intents (voiceover_id);
CREATE TABLE IF NOT EXISTS retrieved_image_details (
    asset_id         TEXT PRIMARY KEY,
    source_image_url TEXT NOT NULL DEFAULT '',
    source_page_url  TEXT NOT NULL DEFAULT '',
    license          TEXT NOT NULL DEFAULT '',
    author           TEXT NOT NULL DEFAULT '',
    search_query     TEXT NOT NULL DEFAULT '',
    retrieved_at     TEXT NOT NULL DEFAULT '',
    provider         TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS publication_intents (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id           TEXT    NOT NULL DEFAULT '',
    attempt          INTEGER NOT NULL DEFAULT 0,
    artifact_id      TEXT    NOT NULL DEFAULT '',
    idempotency_key  TEXT    NOT NULL DEFAULT '',
    provider         TEXT    NOT NULL DEFAULT 'drive',
    state            TEXT    NOT NULL DEFAULT 'PREPARED'
        CHECK (state IN (
            'PREPARED', 'UPLOADING', 'PUBLISHED', 'COMMITTED',
            'ORPHANED', 'CLEANUP_PENDING', 'CLEANED', 'FAILED'
        )),
    remote_file_id   TEXT    NOT NULL DEFAULT '',
    last_error       TEXT    NOT NULL DEFAULT '',
    created_at       TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT    NOT NULL DEFAULT (datetime('now')),

    UNIQUE(idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_publication_intents_state_updated
    ON publication_intents (state, updated_at);
CREATE INDEX IF NOT EXISTS idx_publication_intents_job_id
    ON publication_intents (job_id);
CREATE INDEX IF NOT EXISTS idx_publication_intents_idempotency_key
    ON publication_intents (idempotency_key);
CREATE TABLE IF NOT EXISTS job_results (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0,
    result_hash TEXT NOT NULL DEFAULT '',
    codec_id TEXT NOT NULL DEFAULT '',
    result_payload TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS uniq_job_results_dedup
    ON job_results (job_id, attempt, result_hash);
CREATE INDEX IF NOT EXISTS ix_job_results_job_id
    ON job_results (job_id, attempt DESC);
CREATE TABLE IF NOT EXISTS monitor_enqueue_outbox (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    discovery_id      TEXT NOT NULL,
    idempotency_key   TEXT NOT NULL UNIQUE,
    payload_json      TEXT NOT NULL,
    state             TEXT NOT NULL DEFAULT 'pending',
    created_at        TEXT NOT NULL DEFAULT (datetime('now')),
    dispatched_at     TEXT,
    job_id            TEXT,
    error             TEXT
, retry_count INTEGER NOT NULL DEFAULT 0, next_retry_at TEXT, lease_id TEXT NOT NULL DEFAULT '', lease_until TEXT);
CREATE INDEX IF NOT EXISTS idx_monitor_outbox_drain
    ON monitor_enqueue_outbox(state, next_retry_at, created_at)
    WHERE state IN ('pending', 'dispatching');
CREATE TABLE IF NOT EXISTS execution_steps (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    step_key TEXT NOT NULL,
    input_fingerprint TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    attempt INTEGER NOT NULL DEFAULT 0,
    result_json TEXT NOT NULL DEFAULT '{}',
    artifact_refs_json TEXT NOT NULL DEFAULT '[]',
    started_at TEXT NOT NULL DEFAULT '',
    completed_at TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT ''
, lease_until TEXT NOT NULL DEFAULT '');
CREATE UNIQUE INDEX IF NOT EXISTS uniq_execution_steps_dedup
    ON execution_steps (job_id, step_key, input_fingerprint);
CREATE INDEX IF NOT EXISTS ix_execution_steps_resume
    ON execution_steps (job_id, status, step_key);
CREATE INDEX IF NOT EXISTS ix_execution_steps_audit
    ON execution_steps (job_id, step_key);
CREATE INDEX IF NOT EXISTS ix_execution_steps_leased_stale
    ON execution_steps (lease_until)
    WHERE lease_until != '';
CREATE INDEX IF NOT EXISTS idx_media_assets_enrich_state
    ON media_assets(enrich_state);
CREATE INDEX IF NOT EXISTS idx_media_assets_enrich_state_scrape
    ON media_assets(enrich_state, enrich_state_updated_at)
    WHERE enrich_state IN ('PENDING','FAILED');
CREATE TABLE IF NOT EXISTS generated_image_details (
    asset_id          TEXT PRIMARY KEY,
    prompt_original   TEXT NOT NULL DEFAULT '',
    prompt_resolved   TEXT NOT NULL DEFAULT '',
    style_id          TEXT NOT NULL DEFAULT '',
    style_version     TEXT NOT NULL DEFAULT '',
    model             TEXT NOT NULL DEFAULT '',
    seed              INTEGER NOT NULL DEFAULT 0,
    generation_job_id TEXT NOT NULL DEFAULT '',
    source_hash       TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_jobs_type_status
    ON jobs(type, status);
CREATE INDEX IF NOT EXISTS idx_media_assets_origin
    ON media_assets(origin)
    WHERE origin != '';
CREATE TABLE IF NOT EXISTS artlist_download_audit (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL DEFAULT 'artlist',
    account_id TEXT NOT NULL DEFAULT 'default',
    asset_id TEXT NOT NULL,
    external_url TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT DEFAULT (datetime('now'))
, downloaded_at TEXT, license_id TEXT
    REFERENCES asset_licenses(id) ON DELETE SET NULL, release_id TEXT
    REFERENCES asset_releases(id) ON DELETE SET NULL, project_id TEXT, downloaded_by TEXT);
CREATE INDEX IF NOT EXISTS idx_artlist_download_audit_provider_account_day
ON artlist_download_audit (provider, account_id, date(created_at));
CREATE INDEX IF NOT EXISTS idx_artlist_download_audit_status
ON artlist_download_audit (status);
CREATE TABLE IF NOT EXISTS asset_licenses (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    account_id TEXT NOT NULL DEFAULT 'default',
    project_id TEXT,
    asset_id TEXT NOT NULL,
    license_type TEXT NOT NULL DEFAULT 'standard',
    license_name TEXT,
    license_url TEXT,
    license_terms TEXT,
    receipt_url TEXT,
    receipt_path TEXT,
    certificate_url TEXT,
    certificate_path TEXT,
    valid_from TEXT,
    valid_until TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_asset_licenses_asset
    ON asset_licenses (asset_id);
CREATE INDEX IF NOT EXISTS idx_asset_licenses_provider_account
    ON asset_licenses (provider, account_id);
CREATE INDEX IF NOT EXISTS idx_asset_licenses_project
    ON asset_licenses (project_id);
CREATE TABLE IF NOT EXISTS asset_releases (
    id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL,
    release_type TEXT NOT NULL CHECK (release_type IN ('model', 'property', 'both')),
    model_release_url TEXT,
    model_release_path TEXT,
    property_release_url TEXT,
    property_release_path TEXT,
    certificate_url TEXT,
    certificate_path TEXT,
    receipt_url TEXT,
    receipt_path TEXT,
    status TEXT DEFAULT 'pending',
    verified_at TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_asset_releases_asset
    ON asset_releases (asset_id);
CREATE INDEX IF NOT EXISTS idx_asset_releases_status
    ON asset_releases (status);
CREATE INDEX IF NOT EXISTS idx_artlist_download_audit_license
    ON artlist_download_audit (license_id);
CREATE INDEX IF NOT EXISTS idx_artlist_download_audit_release
    ON artlist_download_audit (release_id);
CREATE INDEX IF NOT EXISTS idx_artlist_download_audit_project
    ON artlist_download_audit (project_id);
CREATE INDEX IF NOT EXISTS idx_artlist_download_audit_downloaded_by
    ON artlist_download_audit (downloaded_by);
CREATE TABLE IF NOT EXISTS asset_renditions (
    id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL,
    location_id INTEGER,
    kind TEXT NOT NULL DEFAULT 'master',
    container TEXT,
    codec TEXT,
    width INTEGER,
    height INTEGER,
    fps REAL,
    bitrate INTEGER,
    color_space TEXT,
    sha256 TEXT,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,
    FOREIGN KEY (location_id) REFERENCES asset_locations(id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_asset_renditions_asset
    ON asset_renditions (asset_id);
CREATE INDEX IF NOT EXISTS idx_asset_renditions_location
    ON asset_renditions (location_id);
CREATE INDEX IF NOT EXISTS idx_asset_renditions_kind
    ON asset_renditions (kind);
CREATE UNIQUE INDEX IF NOT EXISTS ux_asset_renditions_asset_kind
    ON asset_renditions (asset_id, kind);
CREATE TABLE IF NOT EXISTS asset_text_track_segments (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    track_id    INTEGER NOT NULL,
    sequence_no INTEGER NOT NULL,
    start_ms    INTEGER NOT NULL,
    end_ms      INTEGER NOT NULL,
    text        TEXT NOT NULL, text_hash TEXT NOT NULL DEFAULT '',

    FOREIGN KEY (track_id)
        REFERENCES asset_text_tracks(id)
        ON DELETE CASCADE,
    UNIQUE(track_id, sequence_no)
);
CREATE INDEX IF NOT EXISTS idx_asset_text_track_segments_track
    ON asset_text_track_segments(track_id, sequence_no);
CREATE TABLE IF NOT EXISTS operations (
    operation_id            TEXT PRIMARY KEY,
    scope                   TEXT NOT NULL,
    idempotency_key         TEXT NOT NULL,
    request_hash            TEXT NOT NULL,
    job_id                  TEXT NOT NULL,
    state                   TEXT NOT NULL,
    created_at              TEXT NOT NULL,
    updated_at              TEXT NOT NULL,
    supersedes_operation_id TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_operations_idem_lookup
    ON operations(scope, idempotency_key, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_operations_state_created
    ON operations(state, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS ux_operations_active_scope_key
    ON operations(scope, idempotency_key)
    WHERE state != 'SUPERSEDED';
CREATE TABLE IF NOT EXISTS artifact_stages (
    id                 TEXT PRIMARY KEY,
    job_id             TEXT NOT NULL DEFAULT '',
    local_path         TEXT NOT NULL DEFAULT '',
    hash               TEXT NOT NULL DEFAULT '',
    size               INTEGER NOT NULL DEFAULT 0,
    mime               TEXT NOT NULL DEFAULT '',
    requirement        TEXT NOT NULL DEFAULT 'optional'
        CHECK (requirement IN ('required','optional')),
    destination        TEXT NOT NULL DEFAULT '',
    state              TEXT NOT NULL DEFAULT 'STAGED'
        CHECK (state IN ('STAGED','PUBLISHED','SUCCEEDED','FAILED_PERMANENT')),
    attempt_count      INTEGER NOT NULL DEFAULT 0,
    last_error         TEXT NOT NULL DEFAULT '',
    published_location TEXT NOT NULL DEFAULT '',
    published_at       TEXT,
    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_artifact_stages_job_state
    ON artifact_stages(job_id, state);
CREATE INDEX IF NOT EXISTS idx_artifact_stages_state_created
    ON artifact_stages(state, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_artifact_stages_dest
    ON artifact_stages(destination);
CREATE TABLE IF NOT EXISTS drive_folder_catalog (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    destination      TEXT NOT NULL,
    namespace        TEXT NOT NULL DEFAULT '',
    path             TEXT NOT NULL,
    folder_id        TEXT NOT NULL DEFAULT '',
    parent_folder_id TEXT NOT NULL DEFAULT '',
    source           TEXT NOT NULL DEFAULT 'created',
    status           TEXT NOT NULL DEFAULT 'active',
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),

    UNIQUE(destination, path)
);
CREATE INDEX IF NOT EXISTS idx_drive_folder_catalog_dest
    ON drive_folder_catalog(destination);
CREATE INDEX IF NOT EXISTS idx_drive_folder_catalog_status
    ON drive_folder_catalog(status);
CREATE TABLE IF NOT EXISTS media_assets_pipeline_events (
    id           TEXT PRIMARY KEY,
    clip_id      TEXT NOT NULL,
    run_id       TEXT NOT NULL DEFAULT '',
    fase         TEXT NOT NULL,
    attempt      INTEGER NOT NULL DEFAULT 1,
    error_code   TEXT NOT NULL DEFAULT '',
    safe_message TEXT NOT NULL DEFAULT '',
    retryable    INTEGER NOT NULL DEFAULT 0,
    source_url   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_media_assets_pipeline_events_clip_id_created_at
    ON media_assets_pipeline_events(clip_id, created_at);
CREATE INDEX IF NOT EXISTS idx_media_assets_pipeline_events_run_id_created_at
    ON media_assets_pipeline_events(run_id, created_at);
CREATE INDEX IF NOT EXISTS idx_media_assets_pipeline_events_fase
    ON media_assets_pipeline_events(fase);
CREATE TABLE IF NOT EXISTS asset_visual_summaries (
    asset_id              TEXT PRIMARY KEY NOT NULL,

    visual_summary_text   TEXT NOT NULL DEFAULT '',
    visible_actions_json  TEXT NOT NULL DEFAULT '[]',  
    visible_entities_json TEXT NOT NULL DEFAULT '[]',  

    frame_count           INTEGER NOT NULL DEFAULT 0
                          CHECK (frame_count >= 0),
    interval_seconds      REAL NOT NULL DEFAULT 0.0
                          CHECK (interval_seconds >= 0.0),

    preprocessing_version TEXT NOT NULL DEFAULT '',  
    model_name            TEXT NOT NULL DEFAULT '',  
    model_version         TEXT NOT NULL DEFAULT '',  

    source_hash           TEXT NOT NULL DEFAULT '',
    sampled_at            TEXT NOT NULL DEFAULT '',  
    sampled_at_unix       INTEGER NOT NULL DEFAULT 0,

    created_at            TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at            TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_asset_visual_summaries_model
    ON asset_visual_summaries (model_name, model_version);
CREATE INDEX IF NOT EXISTS idx_asset_visual_summaries_source_hash
    ON asset_visual_summaries (source_hash);
CREATE INDEX IF NOT EXISTS idx_asset_visual_summaries_sampled_at
    ON asset_visual_summaries (sampled_at_unix DESC);
CREATE INDEX IF NOT EXISTS idx_media_assets_canonical_source
    ON media_assets (source_provider, source_video_id, source_channel_id)
    WHERE source_provider != '';
CREATE TABLE IF NOT EXISTS asset_artifacts (
    id            TEXT PRIMARY KEY,
    asset_id      TEXT NOT NULL,

    role          TEXT NOT NULL
                  CHECK (role IN ('render_master','preview','thumbnail','waveform','source_archive')),
    mime_type     TEXT NOT NULL DEFAULT '',

    local_path    TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link    TEXT NOT NULL DEFAULT '',

    file_size     INTEGER NOT NULL DEFAULT 0,
    file_sha256   TEXT NOT NULL DEFAULT '',

    width         INTEGER NOT NULL DEFAULT 0,
    height        INTEGER NOT NULL DEFAULT 0,
    frame_rate    REAL NOT NULL DEFAULT 0.0,
    duration_ms   INTEGER NOT NULL DEFAULT 0,

    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','uploaded','verified','deleted')),

    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_asset_artifacts_asset_role
    ON asset_artifacts (asset_id, role);
CREATE UNIQUE INDEX IF NOT EXISTS idx_asset_artifacts_unique_singleton
    ON asset_artifacts (asset_id, role)
    WHERE role IN ('render_master', 'preview');
CREATE INDEX IF NOT EXISTS idx_asset_artifacts_status_updated
    ON asset_artifacts (status, updated_at DESC);
CREATE TABLE IF NOT EXISTS script_localizations (
    script_id          INTEGER NOT NULL,

    source_script_hash TEXT NOT NULL
                       CHECK (length(source_script_hash) > 0),

    language_code      TEXT NOT NULL
                       CHECK (length(language_code) >= 2),

    specscene_json     TEXT NOT NULL DEFAULT ''
                       CHECK (status != 'ready' OR length(specscene_json) > 0),

    translation_model  TEXT NOT NULL DEFAULT '',
    model_version      TEXT NOT NULL DEFAULT '',
    prompt_version     TEXT NOT NULL DEFAULT '',

    status             TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','running','ready','failed')),

    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE,

    UNIQUE(script_id, source_script_hash, language_code, model_version, prompt_version)
);
CREATE INDEX IF NOT EXISTS idx_script_localizations_script_id
    ON script_localizations (script_id);
CREATE INDEX IF NOT EXISTS idx_script_localizations_language_status
    ON script_localizations (language_code, status);
CREATE TABLE IF NOT EXISTS "asset_text_tracks" (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,

    asset_id            TEXT NOT NULL,
    language_code       TEXT NOT NULL,
    text_kind           TEXT NOT NULL,

    text_content        TEXT NOT NULL DEFAULT '',

    source_type         TEXT NOT NULL DEFAULT 'provided',
    source_language_code TEXT NOT NULL DEFAULT '',
    is_original         INTEGER NOT NULL DEFAULT 0,

    provider            TEXT NOT NULL DEFAULT '',
    model_name          TEXT NOT NULL DEFAULT '',
    model_version       TEXT NOT NULL DEFAULT '',
    prompt_version      TEXT NOT NULL DEFAULT '',

    text_hash           TEXT NOT NULL DEFAULT '',
    source_version      TEXT NOT NULL DEFAULT '',
    translation_key     TEXT NOT NULL DEFAULT '',
    is_current          INTEGER NOT NULL DEFAULT 1,

    confidence          REAL,  
    status              TEXT NOT NULL DEFAULT 'READY'
                        CHECK (status IN ('READY', 'PENDING', 'FAILED')),

    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at          TEXT NOT NULL DEFAULT (datetime('now')), source_track_id INTEGER
    REFERENCES asset_text_tracks(id) ON DELETE SET NULL, source_text_hash TEXT NOT NULL DEFAULT '',

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_asset_text_tracks_asset
    ON asset_text_tracks (asset_id);
CREATE INDEX IF NOT EXISTS idx_asset_text_tracks_language
    ON asset_text_tracks (language_code, text_kind);
CREATE INDEX IF NOT EXISTS idx_asset_text_tracks_hash
    ON asset_text_tracks (text_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_asset_text_tracks_current
    ON asset_text_tracks (asset_id, language_code, text_kind)
    WHERE is_current = 1;
CREATE INDEX IF NOT EXISTS idx_asset_text_track_segments_hash
    ON asset_text_track_segments (text_hash);
CREATE INDEX IF NOT EXISTS idx_media_assets_asset_state ON media_assets(asset_state);
CREATE TABLE IF NOT EXISTS "legacy_cache_stock_source" (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    cache_key       TEXT    NOT NULL,
    provider        TEXT    NOT NULL DEFAULT '',
    external_id     TEXT    NOT NULL DEFAULT '',
    source_url      TEXT    NOT NULL,
    local_path      TEXT    NOT NULL,
    file_size       INTEGER NOT NULL DEFAULT 0,
    file_hash       TEXT    NOT NULL DEFAULT '',
    download_section TEXT   NOT NULL DEFAULT '',
    merge_format    TEXT    NOT NULL DEFAULT '',
    force_keyframes INTEGER NOT NULL DEFAULT 0,
    state           TEXT    NOT NULL DEFAULT 'active'
                            CHECK (state IN ('active', 'invalidated', 'expired')),
    last_verified_at TEXT,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT    NOT NULL DEFAULT (datetime('now'))
, legacy_file_md5 TEXT NOT NULL DEFAULT '');
CREATE UNIQUE INDEX IF NOT EXISTS ux_stock_source_cache_key
    ON "legacy_cache_stock_source"(cache_key);
CREATE INDEX IF NOT EXISTS idx_stock_source_cache_state
    ON "legacy_cache_stock_source"(state);
CREATE INDEX IF NOT EXISTS idx_stock_source_cache_provider_external
    ON "legacy_cache_stock_source"(provider, external_id);
CREATE TABLE IF NOT EXISTS stock_batches (
    id                TEXT PRIMARY KEY,
    fingerprint       TEXT NOT NULL DEFAULT '',
    source_url        TEXT NOT NULL DEFAULT '',
    source_cache_key  TEXT NOT NULL DEFAULT '',
    root_folder_id    TEXT NOT NULL DEFAULT '',
    root_folder_name  TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'PLANNED'
                      CHECK (status IN ('PLANNED','RUNNING','SUCCEEDED','FAILED','RETRY_WAIT')),
    expected_groups   INTEGER NOT NULL DEFAULT 0,
    expected_clips    INTEGER NOT NULL DEFAULT 0,
    verified_clips    INTEGER NOT NULL DEFAULT 0,
    policy_version    TEXT NOT NULL DEFAULT '',
    last_error        TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at        TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_stock_batches_status ON stock_batches(status);
CREATE INDEX IF NOT EXISTS idx_stock_batches_source_cache_key ON stock_batches(source_cache_key);
CREATE INDEX IF NOT EXISTS idx_stock_batches_fingerprint ON stock_batches(fingerprint);
CREATE TABLE IF NOT EXISTS stock_batch_groups (
    id               TEXT PRIMARY KEY,
    batch_id         TEXT NOT NULL REFERENCES stock_batches(id) ON DELETE CASCADE,
    group_key        TEXT NOT NULL DEFAULT '',
    title            TEXT NOT NULL DEFAULT '',
    folder_name      TEXT NOT NULL DEFAULT '',
    drive_folder_id  TEXT NOT NULL DEFAULT '',
    start_sec        REAL NOT NULL DEFAULT 0,
    end_sec          REAL NOT NULL DEFAULT 0,
    expected_clips   INTEGER NOT NULL DEFAULT 0,
    verified_clips   INTEGER NOT NULL DEFAULT 0,
    status           TEXT NOT NULL DEFAULT 'PLANNED'
                     CHECK (status IN ('PLANNED','RUNNING','SUCCEEDED','FAILED','RETRY_WAIT')),
    child_job_id     TEXT NOT NULL DEFAULT '',
    attempts         INTEGER NOT NULL DEFAULT 0,
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_stock_batch_groups_batch_id ON stock_batch_groups(batch_id);
CREATE INDEX IF NOT EXISTS idx_stock_batch_groups_status ON stock_batch_groups(status);
CREATE INDEX IF NOT EXISTS idx_stock_batch_groups_child_job_id ON stock_batch_groups(child_job_id);
CREATE TABLE IF NOT EXISTS stock_artifacts (
    id                   TEXT PRIMARY KEY,
    batch_id             TEXT NOT NULL REFERENCES stock_batches(id) ON DELETE CASCADE,
    group_id             TEXT NOT NULL REFERENCES stock_batch_groups(id) ON DELETE CASCADE,
    ordinal              INTEGER NOT NULL DEFAULT 0,
    artifact_key         TEXT NOT NULL DEFAULT '',
    source_url           TEXT NOT NULL DEFAULT '',
    start_sec            REAL NOT NULL DEFAULT 0,
    end_sec              REAL NOT NULL DEFAULT 0,
    expected_duration_ms INTEGER NOT NULL DEFAULT 0,
    actual_duration_ms   INTEGER NOT NULL DEFAULT 0,
    local_path           TEXT NOT NULL DEFAULT '',
    sha256               TEXT NOT NULL DEFAULT '',
    status               TEXT NOT NULL DEFAULT 'PLANNED'
                         CHECK (status IN ('PLANNED','EXTRACTING','EXTRACTED','COMPOSING','COMPOSED','PUBLISHING','PUBLISHED','VERIFIED','RETRY_WAIT','FAILED_PERMANENT','QUARANTINED')),
    drive_file_id        TEXT NOT NULL DEFAULT '',
    drive_folder_id      TEXT NOT NULL DEFAULT '',
    drive_link           TEXT NOT NULL DEFAULT '',
    attempts             INTEGER NOT NULL DEFAULT 0,
    last_error           TEXT NOT NULL DEFAULT '',
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_stock_artifacts_batch_id ON stock_artifacts(batch_id);
CREATE INDEX IF NOT EXISTS idx_stock_artifacts_group_id ON stock_artifacts(group_id);
CREATE INDEX IF NOT EXISTS idx_stock_artifacts_status ON stock_artifacts(status);
CREATE INDEX IF NOT EXISTS idx_stock_artifacts_ordinal ON stock_artifacts(group_id, ordinal);
CREATE TABLE IF NOT EXISTS media_concepts (
    id                  TEXT     PRIMARY KEY,
    canonical_text      TEXT     NOT NULL,
    language            TEXT     NOT NULL,
    normalized_text     TEXT     NOT NULL,
    phrase_fingerprint  TEXT     NOT NULL,
    concept_type        TEXT     NOT NULL,
    embedding_version   TEXT,
    created_at          DATETIME NOT NULL,
    updated_at          DATETIME NOT NULL,
    UNIQUE(language, phrase_fingerprint)
);
CREATE INDEX IF NOT EXISTS idx_media_concepts_language
    ON media_concepts(language);
CREATE INDEX IF NOT EXISTS idx_media_concepts_type
    ON media_concepts(concept_type);
CREATE INDEX IF NOT EXISTS idx_media_concepts_embedding_version
    ON media_concepts(embedding_version);
CREATE TABLE IF NOT EXISTS media_bindings (
    id                TEXT     PRIMARY KEY,
    concept_id        TEXT     NOT NULL,
    asset_id          TEXT     NOT NULL,
    start_ms          INTEGER,
    end_ms            INTEGER,
    slot_kind         TEXT     NOT NULL,
    origin            TEXT     NOT NULL,
    approval_status   TEXT     NOT NULL,
    manual_score      REAL     NOT NULL DEFAULT 0,
    semantic_score    REAL     NOT NULL DEFAULT 0,
    quality_score     REAL     NOT NULL DEFAULT 0,
    success_score     REAL     NOT NULL DEFAULT 0,
    usage_count       INTEGER  NOT NULL DEFAULT 0,
    last_used_at      DATETIME,
    created_at        DATETIME NOT NULL,
    updated_at        DATETIME NOT NULL, provider TEXT NOT NULL DEFAULT 'local',
    UNIQUE(concept_id, asset_id, slot_kind),
    FOREIGN KEY(concept_id) REFERENCES media_concepts(id)
);
CREATE INDEX IF NOT EXISTS idx_media_bindings_concept_id
    ON media_bindings(concept_id);
CREATE INDEX IF NOT EXISTS idx_media_bindings_asset_id
    ON media_bindings(asset_id);
CREATE INDEX IF NOT EXISTS idx_media_bindings_approved_slot
    ON media_bindings(approval_status, concept_id, slot_kind);
CREATE INDEX IF NOT EXISTS idx_media_bindings_success
    ON media_bindings(success_score DESC);
CREATE TABLE IF NOT EXISTS "legacy_cache_media_query" (
    id                  TEXT     PRIMARY KEY,
    phrase_fingerprint  TEXT     NOT NULL,
    language            TEXT     NOT NULL,
    request_json        TEXT     NOT NULL,
    result_json         TEXT     NOT NULL,
    provider_state_json TEXT,
    hit_count           INTEGER  NOT NULL DEFAULT 0,
    expires_at          DATETIME,
    created_at          DATETIME NOT NULL,
    updated_at          DATETIME NOT NULL,
    UNIQUE(phrase_fingerprint)
);
CREATE INDEX IF NOT EXISTS idx_media_query_cache_fingerprint
    ON "legacy_cache_media_query"(phrase_fingerprint);
CREATE INDEX IF NOT EXISTS idx_media_query_cache_expiration
    ON "legacy_cache_media_query"(expires_at);
CREATE TABLE IF NOT EXISTS media_candidates (
    id                     TEXT     PRIMARY KEY,
    provider               TEXT     NOT NULL,
    provider_asset_id      TEXT     NOT NULL,
    source_url             TEXT     NOT NULL,
    thumbnail_url          TEXT,
    title                  TEXT,
    description            TEXT,
    duration_ms            INTEGER,
    candidate_score        REAL     NOT NULL DEFAULT 0,
    rights_status          TEXT     NOT NULL,
    license_basis          TEXT,
    allowed_channels       TEXT,
    allowed_regions        TEXT,
    owner                  TEXT,
    expiration             DATETIME,
    discovery_status       TEXT     NOT NULL,
    materialization_status TEXT     NOT NULL,
    asset_id               TEXT,
    created_at             DATETIME NOT NULL,
    updated_at             DATETIME NOT NULL,
    UNIQUE(provider, provider_asset_id)
);
CREATE INDEX IF NOT EXISTS idx_media_candidates_provider
    ON media_candidates(provider);
CREATE INDEX IF NOT EXISTS idx_media_candidates_mater
    ON media_candidates(materialization_status);
CREATE INDEX IF NOT EXISTS idx_media_candidates_rights
    ON media_candidates(rights_status);
CREATE INDEX IF NOT EXISTS idx_media_candidates_score
    ON media_candidates(candidate_score DESC);
CREATE TABLE IF NOT EXISTS media_usage_events (
    id                TEXT     PRIMARY KEY,
    project_id        TEXT     NOT NULL,
    scene_id          TEXT     NOT NULL,
    concept_id        TEXT     NOT NULL,
    asset_id          TEXT     NOT NULL,
    binding_id        TEXT     NOT NULL,
    slot_kind         TEXT     NOT NULL,
    selected          INTEGER  NOT NULL DEFAULT 0,
    manually_selected INTEGER  NOT NULL DEFAULT 0,
    rejected          INTEGER  NOT NULL DEFAULT 0,
    render_completed  INTEGER  NOT NULL DEFAULT 0,
    created_at        DATETIME NOT NULL
, channel_id TEXT NOT NULL DEFAULT '', video_id TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_media_usage_events_concept
    ON media_usage_events(concept_id);
CREATE INDEX IF NOT EXISTS idx_media_usage_events_asset
    ON media_usage_events(asset_id);
CREATE INDEX IF NOT EXISTS idx_media_usage_events_project_scene
    ON media_usage_events(project_id, scene_id);
CREATE TABLE IF NOT EXISTS admin_mutation_audit (
    id TEXT PRIMARY KEY,
    entity_type TEXT NOT NULL,
    entity_id TEXT NOT NULL,
    action TEXT NOT NULL,
    previous_json TEXT,
    next_json TEXT,
    changed_fields_json TEXT,
    actor TEXT NOT NULL,
    request_id TEXT,
    idempotency_key TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    success INTEGER NOT NULL DEFAULT 1,
    error_message TEXT
);
CREATE INDEX IF NOT EXISTS idx_admin_mutation_audit_entity
    ON admin_mutation_audit(entity_type, entity_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_mutation_audit_actor
    ON admin_mutation_audit(actor, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_admin_mutation_audit_created_at
    ON admin_mutation_audit(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_media_usage_events_project_channel
    ON media_usage_events(project_id, channel_id);
CREATE INDEX IF NOT EXISTS idx_media_usage_events_project_video
    ON media_usage_events(project_id, video_id);
CREATE INDEX IF NOT EXISTS idx_media_bindings_provider
    ON media_bindings(provider);
CREATE TABLE IF NOT EXISTS asset_provider_metadata (
    asset_id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    external_id TEXT NOT NULL,
    creator TEXT,
    country TEXT,
    location TEXT,
    collection_id TEXT,
    collection_title TEXT,
    page_url TEXT,
    thumbnail_url TEXT,
    preview_url TEXT,
    license_class TEXT,
    provider_metadata_hash TEXT,
    raw_metadata_json TEXT,
    fetched_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),
    FOREIGN KEY(asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,
    UNIQUE(provider, external_id)
);
CREATE INDEX IF NOT EXISTS idx_asset_provider_metadata_provider_external
    ON asset_provider_metadata(provider, external_id);
CREATE TABLE IF NOT EXISTS asset_tags (
    asset_id TEXT NOT NULL,
    tag TEXT NOT NULL,
    normalized_tag TEXT NOT NULL,
    source TEXT NOT NULL,
    confidence REAL,
    language TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    PRIMARY KEY(asset_id, normalized_tag, source),
    FOREIGN KEY(asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,
    CHECK (source IN ('provider', 'semantic', 'manual', 'transcript', 'visual', 'import'))
);
CREATE INDEX IF NOT EXISTS idx_asset_tags_asset_id
    ON asset_tags(asset_id);
CREATE INDEX IF NOT EXISTS idx_asset_tags_normalized_tag
    ON asset_tags(normalized_tag);
CREATE INDEX IF NOT EXISTS idx_research_cache_topic_fingerprint
    ON "legacy_cache_research"(topic_fingerprint);
CREATE INDEX IF NOT EXISTS idx_research_cache_source_fingerprint
    ON "legacy_cache_research"(source_fingerprint);
CREATE INDEX IF NOT EXISTS idx_research_cache_expires_at
    ON "legacy_cache_research"(expires_at);
CREATE TABLE IF NOT EXISTS asset_subtitle_artifacts (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id TEXT NOT NULL,
    text_track_id INTEGER NOT NULL,
    language_code TEXT NOT NULL,
    format TEXT NOT NULL CHECK (format IN ('ass', 'srt', 'vtt')),

    local_path TEXT NOT NULL,
    drive_file_id TEXT NOT NULL DEFAULT '',

    file_hash TEXT NOT NULL,
    text_hash TEXT NOT NULL,
    cues_hash TEXT NOT NULL,
    clip_content_hash TEXT NOT NULL,

    cue_count INTEGER NOT NULL,
    clip_duration_ms INTEGER NOT NULL,
    last_cue_end_ms INTEGER NOT NULL,

    style_version TEXT NOT NULL,
    generator_version TEXT NOT NULL,

    status TEXT NOT NULL
        CHECK (status IN ('PENDING', 'READY', 'FAILED', 'STALE')),

    is_current INTEGER NOT NULL DEFAULT 1,
    validation_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, drive_url TEXT NOT NULL DEFAULT '', legacy_file_md5 TEXT NOT NULL DEFAULT '',

    FOREIGN KEY (asset_id) REFERENCES media_assets(id) ON DELETE CASCADE,
    FOREIGN KEY (text_track_id) REFERENCES asset_text_tracks(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_current_clip_ass
ON asset_subtitle_artifacts(asset_id, language_code, format)
WHERE is_current = 1;
CREATE UNIQUE INDEX IF NOT EXISTS idx_subtitle_artifacts_drive_file_unique
ON asset_subtitle_artifacts(drive_file_id)
WHERE is_current = 1 AND drive_file_id <> '';
CREATE TABLE IF NOT EXISTS clip_search_terms (
    clip_id TEXT NOT NULL,
    term    TEXT NOT NULL,
    source  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (clip_id, term)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_subjects_slug
    ON subjects (slug);
CREATE UNIQUE INDEX IF NOT EXISTS idx_subjects_uuid
    ON subjects (uuid);
CREATE INDEX IF NOT EXISTS idx_subjects_display_name_norm
    ON subjects (display_name_norm);
CREATE TABLE IF NOT EXISTS clip_storage_index (
    clip_key        TEXT PRIMARY KEY,        
    asset_id        TEXT,                    
    has_db          INTEGER NOT NULL,        
    has_drive       INTEGER NOT NULL,        
    has_qdrant      INTEGER NOT NULL,        
    drive_file_id   TEXT,                    
    drive_link      TEXT,                    
    qdrant_point_id TEXT,                    
    persisted_at    TEXT,                    
    uploaded_at     TEXT,                    
    indexed_at      TEXT,                    
    created_at      TEXT NOT NULL,           
    updated_at      TEXT NOT NULL            
);
CREATE INDEX IF NOT EXISTS ix_clip_storage_index_drive_missing
    ON clip_storage_index(has_drive, has_db)
    WHERE has_drive = 0 AND has_db = 1;
CREATE INDEX IF NOT EXISTS ix_clip_storage_index_qdrant_missing
    ON clip_storage_index(has_qdrant, has_db)
    WHERE has_qdrant = 0 AND has_db = 1;
CREATE INDEX IF NOT EXISTS ix_clip_storage_index_asset_id
    ON clip_storage_index(asset_id)
    WHERE asset_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_outbox_events_status_priority_claim
    ON outbox_events(status, priority, next_attempt_at, id);
CREATE TABLE IF NOT EXISTS "legacy_cache_vidrush_provider" (
    namespace   TEXT NOT NULL,
    cache_key   TEXT NOT NULL,
    payload_json TEXT NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (namespace, cache_key)
);
CREATE INDEX IF NOT EXISTS idx_vidrush_provider_cache_updated_at
    ON "legacy_cache_vidrush_provider"(updated_at);
CREATE TRIGGER trg_media_assets_state_valid_insert
BEFORE INSERT ON media_assets
WHEN NEW.lifecycle_state NOT IN (
    'PREPARING','PUBLISHED','STAGING','PROCESSING','ACTIVE',
    'DELETE_PENDING','DELETE_REQUESTED','DRIVE_DELETE_PENDING',
    'DRIVE_DELETED','INDEX_DELETE_PENDING','INDEX_DELETED','DELETED','ERROR'
)
OR NEW.index_state NOT IN (
    'NOT_INDEXABLE','DISCOVERED','EMBEDDING','EMBEDDED','INDEXING','INDEXED',
    'EMBEDDING_FAILED','INDEXING_FAILED','INDEXING_SKIPPED_NO_INDEXER',
    'DELETE_PENDING','DELETED'
)
BEGIN
    SELECT RAISE(ABORT, 'media_assets: invalid lifecycle_state or index_state');
END;
CREATE TRIGGER trg_media_assets_state_valid_update
BEFORE UPDATE OF lifecycle_state, index_state ON media_assets
WHEN NEW.lifecycle_state NOT IN (
    'PREPARING','PUBLISHED','STAGING','PROCESSING','ACTIVE',
    'DELETE_PENDING','DELETE_REQUESTED','DRIVE_DELETE_PENDING',
    'DRIVE_DELETED','INDEX_DELETE_PENDING','INDEX_DELETED','DELETED','ERROR'
)
OR NEW.index_state NOT IN (
    'NOT_INDEXABLE','DISCOVERED','EMBEDDING','EMBEDDED','INDEXING','INDEXED',
    'EMBEDDING_FAILED','INDEXING_FAILED','INDEXING_SKIPPED_NO_INDEXER',
    'DELETE_PENDING','DELETED'
)
BEGIN
    SELECT RAISE(ABORT, 'media_assets: invalid lifecycle_state or index_state');
END;
CREATE TRIGGER trg_media_assets_asset_state_projection_insert
AFTER INSERT ON media_assets
BEGIN
    UPDATE media_assets
    SET asset_state = CASE
        WHEN NEW.lifecycle_state IN ('DELETED', 'INDEX_DELETED') OR NEW.index_state = 'DELETED' THEN 'FAILED_PERMANENT'
        WHEN NEW.index_state IN ('EMBEDDING_FAILED', 'INDEXING_FAILED') THEN 'FAILED_RETRYABLE'
        WHEN NEW.lifecycle_state IN ('STAGING', 'PREPARING', 'PROCESSING') THEN 'DISCOVERED'
        WHEN NEW.index_state = 'NOT_INDEXABLE' THEN 'UPLOADED'
        WHEN NEW.index_state = 'DISCOVERED' THEN 'DISCOVERED'
        WHEN NEW.index_state = 'EMBEDDING' THEN 'TRANSLATED'
        WHEN NEW.index_state IN ('EMBEDDED', 'INDEXING') THEN 'INDEX_PENDING'
        WHEN NEW.index_state = 'INDEXED' AND NEW.lifecycle_state = 'ACTIVE' THEN 'READY'
        WHEN NEW.index_state = 'INDEXED' THEN 'INDEXED'
        ELSE 'DISCOVERED'
    END
    WHERE id = NEW.id;
END;
CREATE TRIGGER trg_media_assets_asset_state_projection_update
AFTER UPDATE OF lifecycle_state, index_state ON media_assets
BEGIN
    UPDATE media_assets
    SET asset_state = CASE
        WHEN NEW.lifecycle_state IN ('DELETED', 'INDEX_DELETED') OR NEW.index_state = 'DELETED' THEN 'FAILED_PERMANENT'
        WHEN NEW.index_state IN ('EMBEDDING_FAILED', 'INDEXING_FAILED') THEN 'FAILED_RETRYABLE'
        WHEN NEW.lifecycle_state IN ('STAGING', 'PREPARING', 'PROCESSING') THEN 'DISCOVERED'
        WHEN NEW.index_state = 'NOT_INDEXABLE' THEN 'UPLOADED'
        WHEN NEW.index_state = 'DISCOVERED' THEN 'DISCOVERED'
        WHEN NEW.index_state = 'EMBEDDING' THEN 'TRANSLATED'
        WHEN NEW.index_state IN ('EMBEDDED', 'INDEXING') THEN 'INDEX_PENDING'
        WHEN NEW.index_state = 'INDEXED' AND NEW.lifecycle_state = 'ACTIVE' THEN 'READY'
        WHEN NEW.index_state = 'INDEXED' THEN 'INDEXED'
        ELSE 'DISCOVERED'
    END
    WHERE id = NEW.id;
END;
CREATE TRIGGER trg_media_assets_asset_state_projection_guard
BEFORE UPDATE OF asset_state ON media_assets
WHEN NEW.asset_state != CASE
    WHEN NEW.lifecycle_state IN ('DELETED', 'INDEX_DELETED') OR NEW.index_state = 'DELETED' THEN 'FAILED_PERMANENT'
    WHEN NEW.index_state IN ('EMBEDDING_FAILED', 'INDEXING_FAILED') THEN 'FAILED_RETRYABLE'
    WHEN NEW.lifecycle_state IN ('STAGING', 'PREPARING', 'PROCESSING') THEN 'DISCOVERED'
    WHEN NEW.index_state = 'NOT_INDEXABLE' THEN 'UPLOADED'
    WHEN NEW.index_state = 'DISCOVERED' THEN 'DISCOVERED'
    WHEN NEW.index_state = 'EMBEDDING' THEN 'TRANSLATED'
    WHEN NEW.index_state IN ('EMBEDDED', 'INDEXING') THEN 'INDEX_PENDING'
    WHEN NEW.index_state = 'INDEXED' AND NEW.lifecycle_state = 'ACTIVE' THEN 'READY'
    WHEN NEW.index_state = 'INDEXED' THEN 'INDEXED'
    ELSE 'DISCOVERED'
END
BEGIN
    SELECT RAISE(ABORT, 'media_assets: asset_state is a derived projection');
END;
CREATE TRIGGER trg_pipeline_events_fase_valid_insert
BEFORE INSERT ON media_assets_pipeline_events
WHEN NEW.fase NOT IN (
    'DISCOVERED','DOWNLOAD_PENDING','DOWNLOADING','DOWNLOADED',
    'PROCESSING','PROCESSED','PUBLISHING','PUBLISHED','INDEX_PENDING',
    'INDEXED','FAILED','SKIPPED'
)
BEGIN
    SELECT RAISE(ABORT, 'media_assets_pipeline_events: invalid fase');
END;
CREATE TABLE IF NOT EXISTS content_objects (
    sha256           TEXT PRIMARY KEY,
    size_bytes       INTEGER NOT NULL,
    mime_type        TEXT,
    storage_uri      TEXT NOT NULL,
    created_at       TEXT NOT NULL,
    verified_at      TEXT,
    integrity_status TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_content_objects_integrity_status
    ON content_objects(integrity_status)
    WHERE integrity_status != 'VERIFIED';
CREATE INDEX IF NOT EXISTS idx_media_assets_namespace_kind
    ON media_assets(namespace, asset_kind, source_type);
CREATE INDEX IF NOT EXISTS idx_media_assets_content_sha256
    ON media_assets(content_sha256)
    WHERE content_sha256 != '';
CREATE TABLE IF NOT EXISTS media_asset_sources (
    source_id      TEXT PRIMARY KEY,
    asset_id       TEXT NOT NULL,
    content_sha256 TEXT NOT NULL DEFAULT '',
    source_type    TEXT NOT NULL,
    source_uri     TEXT NOT NULL,
    source_version TEXT NOT NULL DEFAULT '',
    discovered_at  TEXT NOT NULL,
    is_primary     INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_media_asset_sources_asset_id
    ON media_asset_sources(asset_id);
CREATE INDEX IF NOT EXISTS idx_media_asset_sources_content_sha256
    ON media_asset_sources(content_sha256)
    WHERE content_sha256 != '';
CREATE TABLE IF NOT EXISTS control_plane_meta (
    singleton_id     INTEGER PRIMARY KEY CHECK (singleton_id = 1),
    database_id      TEXT NOT NULL UNIQUE,
    schema_family    TEXT NOT NULL,
    instance_role    TEXT NOT NULL CHECK (instance_role IN ('CANONICAL','READ_ONLY','MIGRATION_SOURCE','ARCHIVE')),
    canonical_version INTEGER NOT NULL,
    created_at       TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_control_plane_meta_singleton
    ON control_plane_meta(singleton_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_control_plane_meta_schema_family
    ON control_plane_meta(schema_family);
CREATE TABLE IF NOT EXISTS "legacy_cache_artifact_entries" (
    cache_key          TEXT PRIMARY KEY,
    source_sha256      TEXT NOT NULL,
    operation          TEXT NOT NULL,
    parameters_json    TEXT NOT NULL,
    processor_version  TEXT NOT NULL,
    artifact_sha256    TEXT NOT NULL,
    size_bytes         INTEGER NOT NULL DEFAULT 0,
    mime_type          TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'READY'
                       CHECK (status IN ('BUILDING','READY','FAILED','INVALID')),
    lease_id           TEXT NOT NULL DEFAULT '',
    lease_until        TEXT,
    created_at         TEXT NOT NULL,
    last_accessed_at   TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    error_message      TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_artifact_cache_source_operation
    ON "legacy_cache_artifact_entries"(source_sha256, operation, processor_version);
CREATE INDEX IF NOT EXISTS idx_artifact_cache_status_lease
    ON "legacy_cache_artifact_entries"(status, lease_until);
CREATE TABLE IF NOT EXISTS "legacy_cache_artifact_metrics" (
    operation             TEXT PRIMARY KEY,
    hit_count             INTEGER NOT NULL DEFAULT 0,
    miss_count            INTEGER NOT NULL DEFAULT 0,
    invalidation_count    INTEGER NOT NULL DEFAULT 0,
    avoided_bytes         INTEGER NOT NULL DEFAULT 0,
    avoided_work_ms       INTEGER NOT NULL DEFAULT 0,
    created_at            TEXT NOT NULL,
    updated_at            TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS source_identity_registry (
    source_type         TEXT NOT NULL,
    source_key          TEXT NOT NULL,
    content_sha256      TEXT NOT NULL,
    source_version      TEXT NOT NULL DEFAULT '',
    discovered_at       TEXT NOT NULL,
    last_seen_at        TEXT NOT NULL,
    verification_status TEXT NOT NULL DEFAULT 'UNVERIFIED',
    PRIMARY KEY (source_type, source_key)
);
CREATE INDEX IF NOT EXISTS idx_source_identity_content
    ON source_identity_registry(content_sha256);
CREATE TABLE IF NOT EXISTS canonical_mutations (
    command_id       TEXT PRIMARY KEY,
    idempotency_key  TEXT NOT NULL UNIQUE,
    request_hash     TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL CHECK (status IN ('IN_PROGRESS', 'COMPLETED')),
    result_json      TEXT NOT NULL DEFAULT '{}',
    created_at       TEXT NOT NULL,
    completed_at     TEXT,
    error_message    TEXT NOT NULL DEFAULT ''
, registry_seq INTEGER NOT NULL DEFAULT 0, outbox_event_id INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS idx_canonical_mutations_status_created
    ON canonical_mutations(status, created_at);
CREATE INDEX IF NOT EXISTS idx_canonical_mutations_request_hash
    ON canonical_mutations(request_hash);
CREATE TABLE IF NOT EXISTS registry_events (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    event_id TEXT NOT NULL UNIQUE,
    asset_id TEXT,
    event_type TEXT NOT NULL,
    run_id TEXT,
    actor TEXT NOT NULL DEFAULT '',
    before_hash TEXT NOT NULL DEFAULT '',
    after_hash TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '{}',
    git_sha TEXT NOT NULL DEFAULT '',
    app_version TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_registry_events_asset_created ON registry_events(asset_id, created_at);
CREATE INDEX IF NOT EXISTS idx_registry_events_run_created ON registry_events(run_id, created_at);
CREATE TABLE IF NOT EXISTS registry_runs (
    run_id TEXT PRIMARY KEY,
    run_type TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    git_sha TEXT NOT NULL DEFAULT '',
    parameters_json TEXT NOT NULL DEFAULT '{}',
    assets_seen INTEGER NOT NULL DEFAULT 0,
    assets_created INTEGER NOT NULL DEFAULT 0,
    assets_updated INTEGER NOT NULL DEFAULT 0,
    transcripts_before INTEGER NOT NULL DEFAULT 0,
    transcripts_after INTEGER NOT NULL DEFAULT 0,
    descriptions_before INTEGER NOT NULL DEFAULT 0,
    descriptions_after INTEGER NOT NULL DEFAULT 0,
    qdrant_points_before INTEGER NOT NULL DEFAULT 0,
    qdrant_points_after INTEGER NOT NULL DEFAULT 0,
    error TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS backup_registry (
    backup_id TEXT PRIMARY KEY,
    backup_type TEXT NOT NULL,
    source_revision INTEGER NOT NULL DEFAULT 0,
    path TEXT NOT NULL DEFAULT '',
    remote_uri TEXT NOT NULL DEFAULT '',
    sha256 TEXT NOT NULL DEFAULT '',
    size_bytes INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL,
    app_git_sha TEXT NOT NULL DEFAULT '',
    qdrant_version TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    verified_at TEXT,
    restored_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_backup_registry_status_created ON backup_registry(status, created_at);
CREATE TABLE IF NOT EXISTS job_steps (
    step_id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    step_name TEXT NOT NULL,
    step_type TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    started_at TEXT,
    completed_at TEXT,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    input_count INTEGER NOT NULL DEFAULT 0,
    output_count INTEGER NOT NULL DEFAULT 0,
    input_bytes INTEGER NOT NULL DEFAULT 0,
    output_bytes INTEGER NOT NULL DEFAULT 0,
    metrics_json TEXT NOT NULL DEFAULT '{}',
    error_code TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_job_steps_job_created ON job_steps(job_id, created_at);
CREATE INDEX IF NOT EXISTS idx_job_steps_name_status ON job_steps(step_name, status, started_at);
CREATE TABLE IF NOT EXISTS job_registry_metrics (
    metric_id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    step_id TEXT,
    metric_name TEXT NOT NULL,
    metric_value REAL NOT NULL,
    unit TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE,
    FOREIGN KEY (step_id) REFERENCES job_steps(step_id) ON DELETE SET NULL
);
CREATE INDEX IF NOT EXISTS idx_job_registry_metrics_job ON job_registry_metrics(job_id, created_at);
CREATE INDEX IF NOT EXISTS idx_job_registry_metrics_name ON job_registry_metrics(metric_name, created_at);
CREATE TABLE IF NOT EXISTS job_registry_events (
    event_id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    payload_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_job_registry_events_job_created ON job_registry_events(job_id, created_at);
CREATE INDEX IF NOT EXISTS idx_job_registry_events_type_created ON job_registry_events(event_type, created_at);
CREATE TABLE IF NOT EXISTS "job_asset_relations" (
    job_id TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    relation TEXT NOT NULL,
    step_id TEXT NOT NULL DEFAULT '',
    ordinal INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    PRIMARY KEY (job_id, asset_id, relation, step_id),
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_job_asset_relations_asset
    ON job_asset_relations(asset_id, relation, created_at);
CREATE INDEX IF NOT EXISTS idx_job_asset_relations_job_relation
    ON job_asset_relations(job_id, relation, ordinal);
CREATE INDEX IF NOT EXISTS idx_job_asset_relations_step
    ON job_asset_relations(step_id, created_at);
CREATE TABLE IF NOT EXISTS performance_runs (
    run_id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL DEFAULT '',
    root_job_id TEXT NOT NULL DEFAULT '',
    video_id TEXT NOT NULL DEFAULT '',
    workload_id TEXT NOT NULL DEFAULT '',
    workload_version TEXT NOT NULL DEFAULT '',
    git_sha TEXT NOT NULL DEFAULT '',
    worker_id TEXT NOT NULL DEFAULT '',
    host_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('RUNNING','SUCCEEDED','FAILED')),
    wall_ms INTEGER NOT NULL DEFAULT 0,
    cpu_user_ms INTEGER NOT NULL DEFAULT 0,
    cpu_system_ms INTEGER NOT NULL DEFAULT 0,
    peak_rss_bytes INTEGER NOT NULL DEFAULT 0,
    disk_read_bytes INTEGER NOT NULL DEFAULT 0,
    disk_write_bytes INTEGER NOT NULL DEFAULT 0,
    network_rx_bytes INTEGER NOT NULL DEFAULT 0,
    network_tx_bytes INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    started_at TEXT NOT NULL,
    completed_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_performance_runs_job ON performance_runs(job_id, started_at);
CREATE INDEX IF NOT EXISTS idx_performance_runs_workload ON performance_runs(workload_id, workload_version, started_at);
CREATE TABLE IF NOT EXISTS performance_steps (
    step_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    job_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('RUNNING','SUCCEEDED','FAILED')),
    duration_ms INTEGER NOT NULL DEFAULT 0,
    input_count INTEGER NOT NULL DEFAULT 0,
    output_count INTEGER NOT NULL DEFAULT 0,
    input_bytes INTEGER NOT NULL DEFAULT 0,
    output_bytes INTEGER NOT NULL DEFAULT 0,
    cache_hits INTEGER NOT NULL DEFAULT 0,
    cache_misses INTEGER NOT NULL DEFAULT 0,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    started_at TEXT NOT NULL,
    completed_at TEXT,
    FOREIGN KEY(run_id) REFERENCES performance_runs(run_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_performance_steps_run ON performance_steps(run_id, started_at);
CREATE TABLE IF NOT EXISTS performance_artifacts (
    artifact_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    uri TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    FOREIGN KEY(run_id) REFERENCES performance_runs(run_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS benchmark_workloads (
    workload_id TEXT NOT NULL,
    version TEXT NOT NULL,
    input_manifest_sha256 TEXT NOT NULL,
    parameters_json TEXT NOT NULL DEFAULT '{}',
    expected_output_sha256 TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY(workload_id, version)
);
CREATE TRIGGER trg_job_registry_status_changed
AFTER UPDATE OF status, started_at, completed_at, error ON jobs
WHEN OLD.status IS NOT NEW.status OR OLD.started_at IS NOT NEW.started_at
  OR OLD.completed_at IS NOT NEW.completed_at OR OLD.error IS NOT NEW.error
BEGIN
    INSERT OR IGNORE INTO job_registry_events
        (event_id, job_id, event_type, payload_json, created_at)
    VALUES
        ('job-status-' || NEW.id || '-' || NEW.revision || '-' || NEW.status,
         NEW.id,
         'JOB_STATUS_CHANGED',
         json_object('from', OLD.status, 'to', NEW.status, 'error', NEW.error),
         COALESCE(NEW.updated_at, datetime('now')));
END;
CREATE TABLE IF NOT EXISTS "projection_registry" (
    projection_id TEXT PRIMARY KEY,
    projection_type TEXT NOT NULL,
    collection_name TEXT NOT NULL,
    alias_name TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL CHECK (status IN ('BUILDING','VALIDATED','ACTIVE','RETIRED','FAILED','FAILED_CLEANED')),
    source_registry_seq INTEGER NOT NULL DEFAULT 0,
    embedding_model TEXT NOT NULL DEFAULT '',
    embedding_dimensions INTEGER NOT NULL DEFAULT 0,
    asset_count INTEGER NOT NULL DEFAULT 0,
    transcript_count INTEGER NOT NULL DEFAULT 0,
    collection_hash TEXT NOT NULL DEFAULT '',
    qdrant_version TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    activated_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_projection_registry_type_status
    ON projection_registry(projection_type, status);
CREATE INDEX IF NOT EXISTS idx_projection_registry_status
    ON projection_registry(status, source_registry_seq);
CREATE TABLE IF NOT EXISTS render_attempt_analytics (
    attempt_id      TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL DEFAULT '',
    phrase_count    INTEGER NOT NULL DEFAULT 0,
    word_count      INTEGER NOT NULL DEFAULT 0,
    image_count     INTEGER NOT NULL DEFAULT 0,
    leak_count      INTEGER NOT NULL DEFAULT 0,
    render_ms       INTEGER NOT NULL DEFAULT 0,
    encode_ms       INTEGER NOT NULL DEFAULT 0,
    width           INTEGER NOT NULL DEFAULT 0,
    height          INTEGER NOT NULL DEFAULT 0,
    fps_num         INTEGER NOT NULL DEFAULT 0,
    fps_den         INTEGER NOT NULL DEFAULT 0,
    frame_count     INTEGER NOT NULL DEFAULT 0,
    duration_us     INTEGER NOT NULL DEFAULT 0,
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    sha256          TEXT NOT NULL DEFAULT '',
    drive_file_id   TEXT NOT NULL DEFAULT '',
    drive_link      TEXT NOT NULL DEFAULT '',
    recorded_at     TEXT NOT NULL
, completion_wait_ms INTEGER NOT NULL DEFAULT 0, polling_sleep_ms INTEGER NOT NULL DEFAULT 0, polling_interval_ms INTEGER NOT NULL DEFAULT 0, poll_count INTEGER NOT NULL DEFAULT 0);
CREATE INDEX IF NOT EXISTS idx_render_attempt_analytics_job ON render_attempt_analytics(job_id, recorded_at);
CREATE TABLE IF NOT EXISTS job_checkpoints (
    job_id TEXT NOT NULL,
    stage TEXT NOT NULL,
    unit_id TEXT NOT NULL,

    input_fingerprint TEXT NOT NULL,

    status TEXT NOT NULL,

    artifact_sha256 TEXT NOT NULL DEFAULT '',
    artifact_uri TEXT NOT NULL DEFAULT '',

    processor_version TEXT NOT NULL,

    completed_at TEXT NOT NULL,

    PRIMARY KEY(job_id, stage, unit_id)
);
CREATE INDEX IF NOT EXISTS idx_job_checkpoints_job ON job_checkpoints(job_id, completed_at);
CREATE TABLE IF NOT EXISTS performance_operations (
    operation_id TEXT PRIMARY KEY,

    run_id TEXT NOT NULL,
    job_id TEXT NOT NULL,
    step_id TEXT NOT NULL,

    operation TEXT NOT NULL,

    source_sha256 TEXT NOT NULL DEFAULT '',
    source_duration_ms INTEGER NOT NULL DEFAULT 0,
    source_size_bytes INTEGER NOT NULL DEFAULT 0,

    width INTEGER NOT NULL DEFAULT 0,
    height INTEGER NOT NULL DEFAULT 0,
    fps REAL NOT NULL DEFAULT 0,

    input_codec TEXT NOT NULL DEFAULT '',
    output_codec TEXT NOT NULL DEFAULT '',

    elapsed_ms INTEGER NOT NULL DEFAULT 0,

    cpu_user_ms INTEGER NOT NULL DEFAULT 0,
    cpu_system_ms INTEGER NOT NULL DEFAULT 0,

    output_size_bytes INTEGER NOT NULL DEFAULT 0,

    cache_hit INTEGER NOT NULL DEFAULT 0,

    strategy TEXT NOT NULL DEFAULT '',

    metadata_json TEXT NOT NULL DEFAULT '{}',

    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_performance_operations_operation ON performance_operations(operation, created_at);
CREATE INDEX IF NOT EXISTS idx_performance_operations_run ON performance_operations(run_id, created_at);
CREATE TABLE IF NOT EXISTS replay_bundles (
    original_job_id TEXT PRIMARY KEY,

    version TEXT NOT NULL,
    plan_sha256 TEXT NOT NULL,

    renderer_version TEXT NOT NULL,
    rust_protocol_version TEXT NOT NULL,
    ffmpeg_version TEXT NOT NULL,
    encoder_policy_hash TEXT NOT NULL DEFAULT '',

    render_plan_json TEXT NOT NULL,
    assets_json TEXT NOT NULL,

    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_replay_bundles_plan ON replay_bundles(plan_sha256);
CREATE TABLE IF NOT EXISTS asset_render_variants (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_clip_id TEXT NOT NULL,
    language_code TEXT NOT NULL,

    fingerprint TEXT NOT NULL,
    source_clip_sha256 TEXT NOT NULL,
    transcript_sha256 TEXT NOT NULL,
    translation_version TEXT NOT NULL DEFAULT '',
    subtitle_style_version TEXT NOT NULL DEFAULT '',
    render_profile_version TEXT NOT NULL DEFAULT '',

    subtitle_hash TEXT NOT NULL DEFAULT '',
    output_hash TEXT NOT NULL DEFAULT '',

    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link TEXT NOT NULL DEFAULT '',

    duration_ms INTEGER NOT NULL DEFAULT 0,
    size_bytes INTEGER NOT NULL DEFAULT 0,

    status TEXT NOT NULL
        CHECK (status IN ('PENDING', 'READY', 'FAILED')),
    validation_error TEXT NOT NULL DEFAULT '',

    is_current INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (source_clip_id) REFERENCES media_assets(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_render_variants_source_lang
ON asset_render_variants(source_clip_id, language_code);
CREATE UNIQUE INDEX IF NOT EXISTS idx_render_variants_current
ON asset_render_variants(source_clip_id, language_code)
WHERE is_current = 1;
CREATE INDEX IF NOT EXISTS idx_render_variants_fingerprint
ON asset_render_variants(source_clip_id, language_code, fingerprint);
CREATE TABLE IF NOT EXISTS script_semantic_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    script_id INTEGER NOT NULL,
    semantic_id TEXT NOT NULL,
    scene_id TEXT NOT NULL,

    type TEXT NOT NULL,
    subtype TEXT NOT NULL DEFAULT '',

    text TEXT NOT NULL,
    normalized_text TEXT NOT NULL,
    canonical_entity_id TEXT NOT NULL DEFAULT '',

    start_char INTEGER NOT NULL,
    end_char INTEGER NOT NULL,
    start_us INTEGER NOT NULL,
    end_us INTEGER NOT NULL,

    confidence REAL NOT NULL DEFAULT 1.0
        CHECK (confidence >= 0 AND confidence <= 1),

    metadata_json TEXT NOT NULL DEFAULT '{}',

    created_at TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE,
    UNIQUE(script_id, semantic_id)
);
CREATE INDEX IF NOT EXISTS idx_script_semantic_items_script_scene
    ON script_semantic_items (script_id, scene_id);
CREATE INDEX IF NOT EXISTS idx_script_semantic_items_entity
    ON script_semantic_items (canonical_entity_id)
    WHERE canonical_entity_id != '';
CREATE TABLE IF NOT EXISTS script_visual_bindings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,

    script_id INTEGER NOT NULL,
    semantic_id TEXT NOT NULL,
    visual_event_id TEXT NOT NULL,

    preset_family TEXT NOT NULL,
    preset_id TEXT NOT NULL DEFAULT '',

    asset_id TEXT NOT NULL DEFAULT '',

    animation_in TEXT NOT NULL DEFAULT '',
    animation_idle TEXT NOT NULL DEFAULT '',
    animation_out TEXT NOT NULL DEFAULT '',

    start_us INTEGER NOT NULL,
    duration_us INTEGER NOT NULL,

    resolver_version TEXT NOT NULL DEFAULT '',
    sampler_version TEXT NOT NULL DEFAULT '',

    created_at TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (script_id) REFERENCES scripts(id) ON DELETE CASCADE,
    UNIQUE(script_id, visual_event_id)
);
CREATE INDEX IF NOT EXISTS idx_script_visual_bindings_script_scene
    ON script_visual_bindings (script_id, start_us);
CREATE INDEX IF NOT EXISTS idx_script_visual_bindings_semantic
    ON script_visual_bindings (script_id, semantic_id);
CREATE TABLE IF NOT EXISTS artlist_clips (
    clip_id             TEXT PRIMARY KEY,
    title               TEXT NOT NULL DEFAULT '',
    author              TEXT NOT NULL DEFAULT '',
    duration_ms         INTEGER NOT NULL DEFAULT 0 CHECK (duration_ms >= 0),
    canonical_clip_url  TEXT NOT NULL DEFAULT '',
    thumbnail_url       TEXT NOT NULL DEFAULT '',
    tags_json           TEXT NOT NULL DEFAULT '[]',
    categories_json     TEXT NOT NULL DEFAULT '[]',
    description         TEXT NOT NULL DEFAULT '',
    metadata_json       TEXT NOT NULL DEFAULT '{}',
    first_seen_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen_at        TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at          TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    active              INTEGER NOT NULL DEFAULT 1 CHECK (active IN (0, 1)),
    downloaded          INTEGER NOT NULL DEFAULT 0 CHECK (downloaded IN (0, 1)),
    drive_file_id       TEXT NOT NULL DEFAULT '',
    drive_link          TEXT NOT NULL DEFAULT '',
    local_path          TEXT NOT NULL DEFAULT '',
    file_hash           TEXT NOT NULL DEFAULT ''
, legacy_file_md5 TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_artlist_clips_active_seen
    ON artlist_clips(active, last_seen_at DESC);
CREATE INDEX IF NOT EXISTS idx_artlist_clips_downloaded
    ON artlist_clips(downloaded, active);
CREATE INDEX IF NOT EXISTS idx_artlist_clips_drive_file
    ON artlist_clips(drive_file_id)
    WHERE drive_file_id != '';
CREATE TABLE IF NOT EXISTS artlist_queries (
    query_id                    INTEGER PRIMARY KEY AUTOINCREMENT,
    query                       TEXT NOT NULL DEFAULT '',
    normalized_query            TEXT NOT NULL DEFAULT '',
    query_key                   TEXT NOT NULL UNIQUE,
    filters_json                TEXT NOT NULL DEFAULT '{}',
    provider_sort_type          INTEGER NOT NULL DEFAULT 1,
    provider_total              INTEGER NOT NULL DEFAULT 0 CHECK (provider_total >= 0),
    provider_total_authoritative INTEGER NOT NULL DEFAULT 1 CHECK (provider_total_authoritative IN (0, 1)),
    result_count                INTEGER NOT NULL DEFAULT 0 CHECK (result_count >= 0),
    first_synced_at             TEXT,
    last_synced_at              TEXT,
    expires_at                  TEXT,
    sync_status                 TEXT NOT NULL DEFAULT 'never'
                                CHECK (sync_status IN ('never', 'running', 'succeeded', 'failed')),
    last_error                  TEXT NOT NULL DEFAULT '',
    created_at                  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at                  TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
);
CREATE INDEX IF NOT EXISTS idx_artlist_queries_normalized
    ON artlist_queries(normalized_query);
CREATE INDEX IF NOT EXISTS idx_artlist_queries_sync_due
    ON artlist_queries(expires_at, sync_status);
CREATE TABLE IF NOT EXISTS artlist_query_clips (
    query_id        INTEGER NOT NULL,
    clip_id         TEXT NOT NULL,
    provider_rank   INTEGER NOT NULL DEFAULT 0 CHECK (provider_rank >= 0),
    provider_page   INTEGER NOT NULL DEFAULT 1 CHECK (provider_page >= 1),
    first_seen_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    last_seen_at    TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    PRIMARY KEY (query_id, clip_id),
    FOREIGN KEY (query_id) REFERENCES artlist_queries(query_id) ON DELETE CASCADE,
    FOREIGN KEY (clip_id) REFERENCES artlist_clips(clip_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_artlist_query_clips_rank
    ON artlist_query_clips(query_id, provider_page, provider_rank, clip_id);
CREATE INDEX IF NOT EXISTS idx_artlist_query_clips_clip
    ON artlist_query_clips(clip_id, query_id);
CREATE TABLE IF NOT EXISTS entity_image_catalog_entities (
    canonical_entity_id TEXT PRIMARY KEY
        CHECK (canonical_entity_id LIKE 'person:%'),
    entity_type         TEXT NOT NULL DEFAULT 'PERSON'
        CHECK (entity_type = 'PERSON'),
    canonical_name      TEXT NOT NULL,
    first_seen_at       TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen_at        TEXT NOT NULL DEFAULT (datetime('now')),
    last_refresh_at     TEXT NOT NULL DEFAULT '',
    refresh_status      TEXT NOT NULL DEFAULT 'never'
        CHECK (refresh_status IN ('never', 'running', 'succeeded', 'failed')),
    last_error          TEXT NOT NULL DEFAULT '',
    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at          TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_entities_refresh
    ON entity_image_catalog_entities(refresh_status, last_refresh_at);
CREATE TABLE IF NOT EXISTS entity_image_catalog_candidates (
    candidate_id        INTEGER PRIMARY KEY AUTOINCREMENT,
    canonical_entity_id TEXT NOT NULL,
    provider            TEXT NOT NULL,
    rank                INTEGER NOT NULL CHECK (rank >= 1),
    source_url          TEXT NOT NULL,
    thumbnail_url       TEXT NOT NULL DEFAULT '',
    width               INTEGER NOT NULL DEFAULT 0 CHECK (width >= 0),
    height              INTEGER NOT NULL DEFAULT 0 CHECK (height >= 0),
    status              TEXT NOT NULL DEFAULT 'fresh'
        CHECK (status IN ('fresh', 'active', 'stale', 'broken', 'retired')),
    first_seen_at       TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen_at        TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at          TEXT NOT NULL DEFAULT (datetime('now')), semantic_status TEXT NOT NULL DEFAULT 'unknown'
        CHECK (semantic_status IN ('unknown', 'accepted', 'rejected')), semantic_score REAL NOT NULL DEFAULT 0
        CHECK (semantic_score >= 0 AND semantic_score <= 1), technical_score REAL NOT NULL DEFAULT 0
        CHECK (technical_score >= 0 AND technical_score <= 1), quality_reason TEXT NOT NULL DEFAULT '', validation_attempts INTEGER NOT NULL DEFAULT 0
        CHECK (validation_attempts >= 0), last_validation_at TEXT NOT NULL DEFAULT '', next_retry_at TEXT NOT NULL DEFAULT '', last_validation_error TEXT NOT NULL DEFAULT '', legacy_file_md5 TEXT NOT NULL DEFAULT '',
    UNIQUE (canonical_entity_id, provider, source_url),
    FOREIGN KEY (canonical_entity_id)
        REFERENCES entity_image_catalog_entities(canonical_entity_id)
        ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS entity_image_catalog_materializations (
    candidate_id     INTEGER PRIMARY KEY,
    asset_id         TEXT NOT NULL DEFAULT '',
    file_hash        TEXT NOT NULL DEFAULT '',
    drive_file_id    TEXT NOT NULL DEFAULT '',
    drive_link       TEXT NOT NULL DEFAULT '',
    local_path       TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'materialized', 'failed')),
    materialized_at  TEXT NOT NULL DEFAULT '',
    last_verified_at TEXT NOT NULL DEFAULT '',
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT NOT NULL DEFAULT (datetime('now')), legacy_file_md5 TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (candidate_id)
        REFERENCES entity_image_catalog_candidates(candidate_id)
        ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_candidates_lookup
    ON entity_image_catalog_candidates(canonical_entity_id, status, rank, candidate_id);
CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_candidates_provider
    ON entity_image_catalog_candidates(provider, source_url);
CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_materializations_asset
    ON entity_image_catalog_materializations(asset_id)
    WHERE asset_id != '';
CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_materializations_drive
    ON entity_image_catalog_materializations(drive_file_id)
    WHERE drive_file_id != '';
CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_candidates_quality
    ON entity_image_catalog_candidates(canonical_entity_id, semantic_status, technical_score, rank);
CREATE INDEX IF NOT EXISTS idx_entity_image_catalog_candidates_recertification
    ON entity_image_catalog_candidates(status, next_retry_at, last_seen_at, validation_attempts);
CREATE TABLE IF NOT EXISTS "voiceovers" (
    id TEXT PRIMARY KEY,
    request_id TEXT NOT NULL DEFAULT '',
    text_hash TEXT NOT NULL DEFAULT ''
        CHECK (length(text_hash) = 0 OR length(text_hash) = 64),
    text_preview TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT 'it',
    voice TEXT NOT NULL DEFAULT '',
    file_hash TEXT NOT NULL DEFAULT '',
    fingerprint TEXT NOT NULL DEFAULT '',
    duration_seconds REAL NOT NULL DEFAULT 0.0,
    status TEXT NOT NULL DEFAULT 'pending',
    error TEXT NOT NULL DEFAULT '',
    strategy TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    job_id TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_voiceovers_request_id ON voiceovers(request_id);
CREATE INDEX IF NOT EXISTS idx_voiceovers_text_hash ON voiceovers(text_hash);
CREATE INDEX IF NOT EXISTS idx_voiceovers_fingerprint ON voiceovers(fingerprint);
CREATE TABLE IF NOT EXISTS assembly_sessions (
    assembly_id TEXT PRIMARY KEY,
    parent_job_id TEXT NOT NULL,
    preparation_job_id TEXT NOT NULL DEFAULT '',
    preparation_id TEXT NOT NULL DEFAULT '',
    preparation_hash TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1,
    runtime_assets_json TEXT NOT NULL DEFAULT '[]',
    finalize_plan_json TEXT NOT NULL DEFAULT '',
    project TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_assembly_sessions_parent_job ON assembly_sessions(parent_job_id);
CREATE TABLE IF NOT EXISTS resource_observations (
    observation_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL,
    job_id TEXT NOT NULL,
    worker_id TEXT NOT NULL DEFAULT '',
    host TEXT NOT NULL DEFAULT '',
    observed_at TEXT NOT NULL,

    cpu_avg_pct REAL,
    cpu_peak_pct REAL,
    rss_avg_bytes INTEGER,
    rss_peak_bytes INTEGER,
    gpu_avg_pct REAL,
    gpu_peak_pct REAL,
    vram_peak_bytes INTEGER,
    encoder_avg_pct REAL,
    temperature_peak_c REAL,
    disk_read_bytes INTEGER,
    disk_write_bytes INTEGER,
    network_rx_bytes INTEGER,
    network_tx_bytes INTEGER,

    metadata_json TEXT NOT NULL DEFAULT '{}'
, attempt_id TEXT NOT NULL DEFAULT '', swap_in_bytes INTEGER, swap_out_bytes INTEGER, disk_util_pct REAL, io_wait_pct REAL, disk_queue_depth REAL, decoder_avg_pct REAL, cpu_temp_peak_c REAL, gpu_temp_peak_c REAL, throttled INTEGER);
CREATE INDEX IF NOT EXISTS idx_resource_observations_run
    ON resource_observations(run_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_resource_observations_job
    ON resource_observations(job_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_resource_observations_host
    ON resource_observations(host, observed_at);
CREATE TABLE IF NOT EXISTS benchmark_batches (
    batch_id TEXT PRIMARY KEY,
    run_id TEXT NOT NULL DEFAULT '',
    worker_slot_count INTEGER NOT NULL CHECK (worker_slot_count > 0),
    started_at TEXT NOT NULL,
    completed_at TEXT,
    created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS benchmark_batch_jobs (
    batch_id TEXT NOT NULL,
    job_id TEXT NOT NULL,
    run_id TEXT NOT NULL DEFAULT '',
    worker_slot INTEGER,
    queued_at TEXT,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    status TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (batch_id, job_id),
    FOREIGN KEY (batch_id) REFERENCES benchmark_batches(batch_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_benchmark_batches_started
    ON benchmark_batches(started_at);
CREATE INDEX IF NOT EXISTS idx_benchmark_batch_jobs_batch_started
    ON benchmark_batch_jobs(batch_id, started_at);
CREATE INDEX IF NOT EXISTS idx_benchmark_batch_jobs_job
    ON benchmark_batch_jobs(job_id, started_at);
CREATE INDEX IF NOT EXISTS idx_resource_observations_attempt
    ON resource_observations(attempt_id, observed_at);
CREATE TABLE IF NOT EXISTS preparation_units (
    unit_fingerprint TEXT PRIMARY KEY,
    fingerprint TEXT NOT NULL DEFAULT '',
    unit_id TEXT NOT NULL DEFAULT '',
    job_type TEXT NOT NULL DEFAULT '',

    unit_kind TEXT NOT NULL,
    fingerprint_version TEXT NOT NULL,
    processor_version TEXT NOT NULL,

    input_manifest_json TEXT NOT NULL DEFAULT '{}',

    state TEXT NOT NULL
        CHECK (state IN ('PLANNED', 'RUNNING', 'READY', 'FAILED', 'STALE')),

    resource_class TEXT NOT NULL
        CHECK (resource_class IN (
            'NETWORK',
            'DISK',
            'CPU_LIGHT',
            'CPU_HEAVY',
            'LLM',
            'TTS',
            'GPU',
            'DRIVE'
        )),

    speculation_level INTEGER NOT NULL DEFAULT 0
        CHECK (speculation_level BETWEEN 0 AND 5),

    cost_class TEXT NOT NULL DEFAULT 'MEDIUM'
        CHECK (cost_class IN ('CHEAP', 'MEDIUM', 'EXPENSIVE')),

    reusable INTEGER NOT NULL DEFAULT 1,
    preemptible INTEGER NOT NULL DEFAULT 1,

    expected_work_ms INTEGER NOT NULL DEFAULT 0,
    actual_work_ms INTEGER NOT NULL DEFAULT 0,

    result_kind TEXT NOT NULL DEFAULT 'NONE'
        CHECK (result_kind IN (
            'NONE',
            'ARTIFACT_CACHE',
            'CONTENT_OBJECT',
            'DOMAIN_CACHE',
            'INLINE_JSON'
        )),

    result_ref TEXT NOT NULL DEFAULT '',
    result_metadata_json TEXT NOT NULL DEFAULT '{}',
    artifact_id TEXT NOT NULL DEFAULT '',
    cache_key TEXT NOT NULL DEFAULT '',
    result_json TEXT NOT NULL DEFAULT '{}',

    scheduler_owner TEXT NOT NULL DEFAULT '',
    lease_owner TEXT NOT NULL DEFAULT '',
    lease_until TEXT,
    lease_expires_at TEXT,

    attempt_count INTEGER NOT NULL DEFAULT 0,

    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,

    started_at TEXT,
    ready_at TEXT,
    last_accessed_at TEXT,
    expires_at TEXT,

    last_error_code TEXT NOT NULL DEFAULT '',
    last_error_message TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_preparation_units_state
    ON preparation_units(state, resource_class);
CREATE INDEX IF NOT EXISTS idx_preparation_units_lease
    ON preparation_units(state, lease_until);
CREATE INDEX IF NOT EXISTS idx_preparation_units_kind
    ON preparation_units(unit_kind, processor_version);
CREATE INDEX IF NOT EXISTS idx_preparation_units_expiry
    ON preparation_units(expires_at)
    WHERE expires_at IS NOT NULL;
CREATE TABLE IF NOT EXISTS preparation_job_units (
    job_id       TEXT NOT NULL,
    unit_id      TEXT NOT NULL,
    fingerprint  TEXT NOT NULL,

    required     INTEGER NOT NULL DEFAULT 1,
    adopted      INTEGER NOT NULL DEFAULT 0,

    queue_rank   INTEGER,

    planned_at   TEXT NOT NULL,
    adopted_at   TEXT, phase     TEXT NOT NULL DEFAULT '', scene_id  TEXT NOT NULL DEFAULT '', language  TEXT NOT NULL DEFAULT '', queue_distance       INTEGER NOT NULL DEFAULT 0, speculation_ceiling   INTEGER NOT NULL DEFAULT 0, priority_score        REAL    NOT NULL DEFAULT 0, critical_path_ms      INTEGER NOT NULL DEFAULT 0, adoption_state    TEXT NOT NULL DEFAULT 'PENDING', promoted_at       TEXT, invalidated_at    TEXT, checkpoint_stage   TEXT NOT NULL DEFAULT '', checkpoint_unit_id TEXT NOT NULL DEFAULT '',

    PRIMARY KEY (job_id, unit_id)
);
CREATE INDEX IF NOT EXISTS idx_preparation_job_units_fingerprint
    ON preparation_job_units(fingerprint);
CREATE INDEX IF NOT EXISTS idx_preparation_job_units_job_adopted
    ON preparation_job_units(job_id, adopted);
CREATE TABLE IF NOT EXISTS preparation_dependencies (
    job_id             TEXT NOT NULL,
    unit_id            TEXT NOT NULL,
    depends_on_unit_id TEXT NOT NULL,

    dependency_kind TEXT NOT NULL DEFAULT 'HARD'
        CHECK (dependency_kind IN ('HARD', 'SOFT')),

    created_at TEXT NOT NULL,

    PRIMARY KEY (job_id, unit_id, depends_on_unit_id)
);
CREATE INDEX IF NOT EXISTS idx_preparation_dependencies_downstream
    ON preparation_dependencies(job_id, depends_on_unit_id);
CREATE INDEX IF NOT EXISTS idx_preparation_dependencies_upstream
    ON preparation_dependencies(job_id, unit_id);
CREATE INDEX IF NOT EXISTS idx_preparation_job_units_job_state
    ON preparation_job_units(job_id, adoption_state);
CREATE INDEX IF NOT EXISTS idx_preparation_job_units_scene
    ON preparation_job_units(job_id, scene_id);
CREATE INDEX IF NOT EXISTS idx_preparation_job_units_priority
    ON preparation_job_units(job_id, priority_score DESC);
CREATE TABLE IF NOT EXISTS preparation_claim_snapshots (
    job_id                 TEXT NOT NULL,
    attempt_id             TEXT NOT NULL,

    job_revision           INTEGER NOT NULL DEFAULT 0,
    claimed_at             TEXT NOT NULL,

    total_units            INTEGER NOT NULL DEFAULT 0,
    required_units         INTEGER NOT NULL DEFAULT 0,
    ready_units            INTEGER NOT NULL DEFAULT 0,
    running_units          INTEGER NOT NULL DEFAULT 0,
    missing_units          INTEGER NOT NULL DEFAULT 0,

    prepared_ratio         REAL NOT NULL DEFAULT 0,
    estimated_saved_ms     INTEGER NOT NULL DEFAULT 0,
    speculative_work_ms    INTEGER NOT NULL DEFAULT 0,
    queue_wait_ms          INTEGER NOT NULL DEFAULT 0,
    queue_position_at_plan INTEGER NOT NULL DEFAULT 0,

    metadata_json          TEXT NOT NULL DEFAULT '{}',

    PRIMARY KEY (job_id, attempt_id)
);
CREATE INDEX IF NOT EXISTS idx_preparation_claim_snapshots_claimed
    ON preparation_claim_snapshots(claimed_at);
CREATE TABLE IF NOT EXISTS preparation_attempts (
    attempt_id          TEXT PRIMARY KEY,

    unit_fingerprint    TEXT NOT NULL,

    trigger_job_id      TEXT NOT NULL DEFAULT '',

    worker_id           TEXT NOT NULL DEFAULT '',
    host                TEXT NOT NULL DEFAULT '',

    execution_mode      TEXT NOT NULL
                        CHECK (execution_mode IN ('SPECULATIVE', 'ACTIVE', 'ADOPTION_CHECK')),

    resource_class      TEXT NOT NULL,

    scheduler_priority  REAL NOT NULL DEFAULT 0,

    status              TEXT NOT NULL
                        CHECK (status IN ('RUNNING', 'READY', 'FAILED', 'CANCELLED', 'PREEMPTED', 'HIT')),

    expected_work_ms    INTEGER NOT NULL DEFAULT 0,

    workload_dimension  TEXT NOT NULL DEFAULT '',
    workload_amount     REAL NOT NULL DEFAULT 0,

    queued_at           TEXT,
    started_at          TEXT NOT NULL,
    finished_at         TEXT,

    queue_wait_ms       INTEGER NOT NULL DEFAULT 0,
    wall_ms             INTEGER NOT NULL DEFAULT 0,

    singleflight_wait_ms INTEGER NOT NULL DEFAULT 0,

    bytes_read          INTEGER NOT NULL DEFAULT 0,
    bytes_written       INTEGER NOT NULL DEFAULT 0,
    network_rx_bytes    INTEGER NOT NULL DEFAULT 0,
    network_tx_bytes    INTEGER NOT NULL DEFAULT 0,

    cache_hit           INTEGER NOT NULL DEFAULT 0,

    preempted_by_active INTEGER NOT NULL DEFAULT 0,

    estimated_saved_ms  INTEGER NOT NULL DEFAULT 0,

    error_code          TEXT NOT NULL DEFAULT '',
    error_message       TEXT NOT NULL DEFAULT '',

    created_at          TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_preparation_attempts_unit
    ON preparation_attempts(unit_fingerprint, started_at);
CREATE INDEX IF NOT EXISTS idx_preparation_attempts_job
    ON preparation_attempts(trigger_job_id, started_at);
CREATE INDEX IF NOT EXISTS idx_preparation_attempts_mode
    ON preparation_attempts(execution_mode, status, started_at);
CREATE INDEX IF NOT EXISTS idx_preparation_attempts_workload
    ON preparation_attempts(workload_dimension, workload_amount)
    WHERE workload_dimension <> '' AND workload_amount > 0;
CREATE INDEX IF NOT EXISTS idx_preparation_units_canonical_fingerprint
    ON preparation_units(unit_fingerprint);
CREATE INDEX IF NOT EXISTS idx_preparation_attempts_status_finished
    ON preparation_attempts(status, finished_at)
    WHERE status IN ('READY', 'HIT');
CREATE UNIQUE INDEX IF NOT EXISTS idx_jobs_client_idempotency
    ON jobs(client_id, idempotency_key)
    WHERE client_id != '' AND idempotency_key != '';
CREATE TABLE IF NOT EXISTS m2m_clients (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    client_id TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    secret_hash TEXT NOT NULL UNIQUE,
    scopes_json TEXT NOT NULL DEFAULT '[]',
    enabled INTEGER NOT NULL DEFAULT 1,
    rate_limit_rps REAL NOT NULL DEFAULT 2,
    rate_limit_burst INTEGER NOT NULL DEFAULT 10,
    quota_max_scenes INTEGER NOT NULL DEFAULT 1000,
    quota_max_total_secs INTEGER NOT NULL DEFAULT 14400,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_used_at TEXT
);
CREATE INDEX IF NOT EXISTS idx_m2m_clients_secret_hash ON m2m_clients(secret_hash);
CREATE TABLE IF NOT EXISTS job_payloads (
    job_id TEXT PRIMARY KEY,
    codec_id TEXT NOT NULL DEFAULT 'json',
    payload TEXT NOT NULL DEFAULT '{}',
    payload_hash TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "asset_links" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id TEXT NOT NULL,
    link_type TEXT NOT NULL,
    url TEXT NOT NULL,
    label TEXT,
    FOREIGN KEY (asset_id) REFERENCES asset_index(asset_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_monitored_sources" (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    url TEXT NOT NULL,
    kind TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'active',
    metadata_json TEXT,
    last_harvester_run TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS "legacy_observability_video_stats_history" (
    video_id TEXT NOT NULL,
    timestamp TEXT NOT NULL DEFAULT (datetime('now')),
    view_count INTEGER,
    like_count INTEGER,
    comment_count INTEGER,
    PRIMARY KEY (video_id, timestamp),
    FOREIGN KEY (video_id) REFERENCES video_metadata(video_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_artlist_runs" (
    id TEXT PRIMARY KEY,
    term TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'queued',
    root_folder_id TEXT,
    tag_folder_id TEXT,
    requested_count INTEGER DEFAULT 0,
    found_count INTEGER DEFAULT 0,
    processed_count INTEGER DEFAULT 0,
    skipped_count INTEGER DEFAULT 0,
    failed_count INTEGER DEFAULT 0,
    error_message TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS "legacy_observability_scripts" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    topic TEXT NOT NULL DEFAULT '',
    duration INTEGER NOT NULL DEFAULT 0,
    language TEXT NOT NULL DEFAULT 'en',
    template TEXT NOT NULL DEFAULT '',
    mode TEXT NOT NULL DEFAULT '',
    narrative_text TEXT,
    timeline_json TEXT,
    entities_json TEXT,
    metadata_json TEXT NOT NULL DEFAULT '{}',
    full_document TEXT,
    model_used TEXT NOT NULL DEFAULT '',
    ollama_base_url TEXT NOT NULL DEFAULT '',
    version INTEGER NOT NULL DEFAULT 1,
    parent_script_id INTEGER,
    is_deleted INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
, title TEXT NOT NULL DEFAULT '', tone TEXT NOT NULL DEFAULT '', target_words INTEGER NOT NULL DEFAULT 0, final_word_count INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'completed', idempotency_key TEXT NOT NULL DEFAULT '', specscene TEXT NOT NULL DEFAULT '', manifest_v2 TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS "legacy_observability_script_sections" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    section_type TEXT NOT NULL DEFAULT '',
    section_title TEXT NOT NULL DEFAULT '',
    content TEXT,
    sort_order INTEGER NOT NULL DEFAULT 0, word_count INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'completed', voiceover_link TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (script_id) REFERENCES "legacy_observability_scripts"(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_script_stock_matches" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    segment_index INTEGER NOT NULL DEFAULT 0,
    stock_path TEXT NOT NULL DEFAULT '',
    stock_source TEXT NOT NULL DEFAULT '',
    score REAL NOT NULL DEFAULT 0,
    matched_terms TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (script_id) REFERENCES "legacy_observability_scripts"(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_asset_tree_nodes" (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    name TEXT NOT NULL,
    type TEXT NOT NULL,
    parent_id TEXT NOT NULL DEFAULT '',
    root_id TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    depth INTEGER NOT NULL DEFAULT 0,
    is_folder INTEGER NOT NULL DEFAULT 0,
    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS "legacy_observability_job_events" (
    id TEXT PRIMARY KEY,
    job_id TEXT NOT NULL,
    type TEXT NOT NULL,
    message TEXT NOT NULL DEFAULT '',
    data_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (job_id) REFERENCES "legacy_observability_jobs"(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_asset_index" (
    asset_id TEXT PRIMARY KEY,
    asset_type TEXT NOT NULL,
    source TEXT NOT NULL,
    source_id TEXT NOT NULL,
    operation_key TEXT,
    group_name TEXT,
    subfolder TEXT,
    local_path TEXT,
    drive_link TEXT,
    download_link TEXT,
    file_hash TEXT,
    content_hash TEXT,
    status TEXT NOT NULL DEFAULT 'ready',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
, legacy_file_md5 TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS "legacy_observability_characters" (
    id TEXT PRIMARY KEY,          
    name TEXT NOT NULL,           
    image_drive_id TEXT,          
    image_drive_link TEXT,        
    voice_id TEXT,                
    metadata_json TEXT DEFAULT '{}',
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS "legacy_observability_artlist_search_cache" (
    term TEXT PRIMARY KEY,
    clips_json TEXT NOT NULL,
    cached_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS "legacy_observability_gemma_script_outputs" (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL DEFAULT 'default',
    mode TEXT NOT NULL DEFAULT 'generate',
    language TEXT DEFAULT 'en',
    title TEXT,
    prompt TEXT NOT NULL,
    normalized_input TEXT NOT NULL,
    input_hash TEXT NOT NULL,
    output_text TEXT,
    output_json TEXT,
    model TEXT,
    job_id TEXT,
    word_count INTEGER DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(channel_id, mode, input_hash)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_gemma_memory_entries" (
    id TEXT PRIMARY KEY,
    channel_id TEXT NOT NULL DEFAULT 'default',
    memory_type TEXT NOT NULL,
    topic_key TEXT,
    title TEXT,
    summary TEXT NOT NULL,
    content_text TEXT,
    content_json TEXT,
    source_generation_id TEXT,
    source_job_id TEXT,
    usefulness_score REAL DEFAULT 1.0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
, last_used_at TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS "legacy_observability_gemma_script_chunks" (
    id TEXT PRIMARY KEY,
    generation_id TEXT NOT NULL,
    channel_id TEXT NOT NULL DEFAULT 'default',
    chunk_index INTEGER NOT NULL,
    chunk_type TEXT DEFAULT 'paragraph',
    topic_key TEXT,
    title TEXT,
    text TEXT NOT NULL,
    search_text TEXT NOT NULL,
    embedding_json TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY(generation_id) REFERENCES "legacy_observability_gemma_script_outputs"(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_research_cache" (
    key TEXT PRIMARY KEY,
    topic TEXT NOT NULL,
    language TEXT NOT NULL,
    max_steps INTEGER NOT NULL,
    source_text TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_used TEXT NOT NULL DEFAULT (datetime('now'))
, concept_id TEXT, topic_fingerprint TEXT, source_fingerprint TEXT, resolver_version TEXT, research_version TEXT, hit_count INTEGER NOT NULL DEFAULT 0, expires_at DATETIME, updated_at TEXT NOT NULL DEFAULT (datetime('now')), source_text_hash TEXT NOT NULL DEFAULT '', research_report_json TEXT NOT NULL DEFAULT '', sources_count INTEGER NOT NULL DEFAULT 0, claims_verified INTEGER NOT NULL DEFAULT 0, claims_rejected INTEGER NOT NULL DEFAULT 0, search_query_count INTEGER NOT NULL DEFAULT 0, pages_fetched INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS "legacy_observability_category_channels" (
    id TEXT PRIMARY KEY,
    category TEXT NOT NULL,           
    channel_url TEXT NOT NULL,         
    channel_name TEXT NOT NULL DEFAULT '',
    keywords TEXT NOT NULL DEFAULT '[]',  
    min_views INTEGER NOT NULL DEFAULT 0,
    max_clip_duration INTEGER NOT NULL DEFAULT 60,
    drive_folder_id TEXT NOT NULL DEFAULT '',  

    
    semantic_keywords TEXT NOT NULL DEFAULT '[]',  
    min_semantic_score INTEGER NOT NULL DEFAULT 60,  
    playlist_end INTEGER NOT NULL DEFAULT -1,  

    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
, check_interval TEXT NOT NULL DEFAULT '24h', max_videos_per_run INTEGER NOT NULL DEFAULT 0, priority INTEGER NOT NULL DEFAULT 2, lookback_days INTEGER NOT NULL DEFAULT 0, max_segments INTEGER NOT NULL DEFAULT 0, segment_prompt TEXT NOT NULL DEFAULT '', enabled INTEGER NOT NULL DEFAULT 1, next_check_at TEXT, last_checked_at TEXT, consecutive_failures INTEGER NOT NULL DEFAULT 0, last_error TEXT, last_success_at TEXT, lease_owner TEXT, lease_until TEXT, monitor_source_kind TEXT NOT NULL DEFAULT 'youtube', monitor_handler_pin TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS "legacy_observability_transcript_cache" (
    video_id TEXT PRIMARY KEY,           
    transcript_text TEXT NOT NULL,        
    language TEXT NOT NULL DEFAULT 'en',  
    cached_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS "legacy_observability_search_queries" (
    id TEXT PRIMARY KEY,
    query TEXT NOT NULL,                     
    category TEXT NOT NULL,                  
    drive_folder_id TEXT DEFAULT '',         
    min_score INTEGER DEFAULT 60,            
    max_results INTEGER DEFAULT 5,           
    check_interval TEXT DEFAULT '7d',        
    last_run_at TEXT,                        
    last_video_published_at TEXT,            
    is_active INTEGER DEFAULT 1,             
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS "legacy_observability_search_query_results" (
    query_id TEXT NOT NULL,
    video_id TEXT NOT NULL,                  
    video_title TEXT NOT NULL DEFAULT '',
    channel_name TEXT DEFAULT '',
    published_at TEXT,                       
    processed_at TEXT DEFAULT (datetime('now')),
    score INTEGER DEFAULT 0,                 
    PRIMARY KEY (query_id, video_id)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_dead_letter_jobs" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    job_type TEXT NOT NULL,
    correlation_id TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL,
    payload_json TEXT,
    retry_count INTEGER NOT NULL DEFAULT 0,
    failed_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS "legacy_observability_translation_cache" (
    cache_key TEXT PRIMARY KEY,
    source_text_hash TEXT NOT NULL,
    target_language TEXT NOT NULL,
    translated_text TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    last_used TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS "legacy_observability_script_research_sources" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    query TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL DEFAULT '',
    snippet TEXT NOT NULL DEFAULT '',
    source_type TEXT NOT NULL DEFAULT 'web',
    used_in_sections TEXT NOT NULL DEFAULT '[]',
    relevance_score REAL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (script_id) REFERENCES "legacy_observability_scripts"(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_script_generation_logs" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    phase TEXT NOT NULL DEFAULT '',
    prompt_hash TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    input_words INTEGER DEFAULT 0,
    output_words INTEGER DEFAULT 0,
    duration_ms INTEGER DEFAULT 0,
    retry_count INTEGER DEFAULT 0,
    cache_status TEXT DEFAULT 'miss',
    error TEXT DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (script_id) REFERENCES "legacy_observability_scripts"(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_gen_logs_script ON "legacy_observability_script_generation_logs"(script_id);
CREATE INDEX IF NOT EXISTS idx_gen_logs_phase ON "legacy_observability_script_generation_logs"(script_id, phase);
CREATE TABLE IF NOT EXISTS "legacy_observability_script_outline_sections" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    section_index INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    purpose TEXT NOT NULL DEFAULT '',
    target_words INTEGER NOT NULL DEFAULT 0,
    key_points_json TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL DEFAULT (datetime('now')), emotional_role TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (script_id) REFERENCES "legacy_observability_scripts"(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_script_versions" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    final_text TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    FOREIGN KEY (script_id) REFERENCES "legacy_observability_scripts"(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_media_assets" (
    id TEXT PRIMARY KEY,
    source TEXT,
    name TEXT,
    tags TEXT,
    tags_norm TEXT,
    duration_ms INTEGER,
    url TEXT,
    media_type TEXT,
    local_path TEXT,
    relative_path TEXT,
    drive_file_id TEXT,
    drive_folder_id TEXT,
    drive_link TEXT,
    download_link TEXT,
    file_hash TEXT,
    embedding_json TEXT,
    metadata_json TEXT,
    visual_embedding TEXT,
    transcript_embedding TEXT,
    created_at TEXT,
    updated_at TEXT
, lifecycle_state TEXT NOT NULL DEFAULT 'ready', deleted_at      TEXT NOT NULL DEFAULT '', folder_id       TEXT NOT NULL DEFAULT '', parent_folder_id TEXT NOT NULL DEFAULT '', folder_path     TEXT NOT NULL DEFAULT '', category        TEXT NOT NULL DEFAULT '', filename        TEXT NOT NULL DEFAULT '', error           TEXT NOT NULL DEFAULT '', thumb_url       TEXT NOT NULL DEFAULT '', phash           TEXT NOT NULL DEFAULT '', search_text     TEXT NOT NULL DEFAULT '', scene_type      TEXT NOT NULL DEFAULT '', quality_score   REAL NOT NULL DEFAULT 0.0, reuse_count     INTEGER NOT NULL DEFAULT 0, last_used_at    TEXT NOT NULL DEFAULT '', thumbnail_url   TEXT    NOT NULL DEFAULT '', clip_page_url   TEXT    NOT NULL DEFAULT '', width INTEGER NOT NULL DEFAULT 0, height INTEGER NOT NULL DEFAULT 0, group_name TEXT NOT NULL DEFAULT '', search_terms TEXT NOT NULL DEFAULT '', index_state TEXT NOT NULL DEFAULT 'DISCOVERED', index_state_updated_at TEXT NOT NULL DEFAULT '', collection_version TEXT NOT NULL DEFAULT '', audio_embedding TEXT NOT NULL DEFAULT '[]', language TEXT NOT NULL DEFAULT '', youtube_video_id TEXT NOT NULL DEFAULT '', youtube_url TEXT NOT NULL DEFAULT '', start_time TEXT NOT NULL DEFAULT '', end_time TEXT NOT NULL DEFAULT '', workspace_id TEXT NOT NULL DEFAULT '', channel_id TEXT NOT NULL DEFAULT '', license TEXT NOT NULL DEFAULT '', source_version TEXT NOT NULL DEFAULT '', style TEXT NOT NULL DEFAULT '', origin TEXT NOT NULL DEFAULT 'retrieved', provider TEXT NOT NULL DEFAULT '', enrich_state TEXT NOT NULL DEFAULT 'PENDING', enrich_state_updated_at TEXT NOT NULL DEFAULT '', source_provider   TEXT    NOT NULL DEFAULT '', source_video_id   TEXT    NOT NULL DEFAULT '', source_channel_id TEXT    NOT NULL DEFAULT '', source_url        TEXT    NOT NULL DEFAULT '', start_ms          INTEGER NOT NULL DEFAULT 0, end_ms            INTEGER NOT NULL DEFAULT 0, original_language TEXT    NOT NULL DEFAULT '', title             TEXT    NOT NULL DEFAULT '', binary_sha256     TEXT    NOT NULL DEFAULT '', semantic_hash     TEXT    NOT NULL DEFAULT '', rights_status     TEXT    NOT NULL DEFAULT 'review_required', policy_version    TEXT    NOT NULL DEFAULT 'v1', lifecycle_status  TEXT    NOT NULL DEFAULT 'ACTIVE', asset_state TEXT NOT NULL DEFAULT 'DISCOVERED', license_basis TEXT NOT NULL DEFAULT '', owner_channel_id TEXT NOT NULL DEFAULT '', allowed_channels TEXT NOT NULL DEFAULT '[]', allowed_regions TEXT NOT NULL DEFAULT '[]', expires_at TEXT NOT NULL DEFAULT '', review_status TEXT NOT NULL DEFAULT 'none', asset_version  TEXT NOT NULL DEFAULT '', asset_location TEXT NOT NULL DEFAULT '', rendition      TEXT NOT NULL DEFAULT '', admin_version INTEGER NOT NULL DEFAULT 0);
CREATE TABLE IF NOT EXISTS "legacy_observability_artifact_sources" (
    source_id         TEXT PRIMARY KEY,
    artifact_id       TEXT NOT NULL,
    source_type       TEXT NOT NULL DEFAULT '',
    source_reference  TEXT NOT NULL DEFAULT '',
    source_account_id TEXT NOT NULL DEFAULT '',
    imported_at       TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS "legacy_observability_job_artifacts" (
    job_id      TEXT NOT NULL,
    artifact_id TEXT NOT NULL,
    role        TEXT NOT NULL DEFAULT '',
    ordinal     INTEGER NOT NULL DEFAULT 0,
    required    INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (job_id, artifact_id)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_artifacts" (
    id              TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL DEFAULT 'unknown',
    status          TEXT NOT NULL DEFAULT 'STAGING'
        CHECK (status IN ('STAGING','VERIFYING','READY','FAILED','QUARANTINED','DELETED')),
    storage_backend TEXT NOT NULL DEFAULT 'local',
    storage_key     TEXT NOT NULL DEFAULT '',
    sha256          TEXT NOT NULL DEFAULT '',
    size_bytes      INTEGER NOT NULL DEFAULT 0,
    mime_type       TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now')),
    verified_at     TEXT
);
CREATE TABLE IF NOT EXISTS "legacy_observability_deliveries" (
    id               TEXT PRIMARY KEY,
    artifact_id      TEXT NOT NULL,
    target_id        TEXT NOT NULL DEFAULT '',
    provider         TEXT NOT NULL DEFAULT 'drive',
    status           TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING','LEASED','RUNNING','RETRY_WAIT','SUCCEEDED','FAILED','BLOCKED_AUTH','CANCELLED')),
    attempt_count    INTEGER NOT NULL DEFAULT 0,
    max_attempts     INTEGER NOT NULL DEFAULT 3,
    next_attempt_at  TEXT,
    lease_id         TEXT NOT NULL DEFAULT '',
    lease_expires_at TEXT,
    remote_id        TEXT NOT NULL DEFAULT '',
    remote_url       TEXT NOT NULL DEFAULT '',
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT NOT NULL DEFAULT (datetime('now')),
    completed_at     TEXT
);
CREATE TABLE IF NOT EXISTS "legacy_observability_assets" (
    asset_id TEXT PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('voiceover','scene_image','stock_clip','music','font','subtitle','thumbnail')),
    status TEXT NOT NULL DEFAULT 'PENDING'
        CHECK (status IN ('PENDING','READY','FAILED','DELETED')),

    sha256 TEXT NOT NULL UNIQUE,
    storage_backend TEXT NOT NULL DEFAULT 'local',
    storage_key TEXT NOT NULL UNIQUE,

    mime_type TEXT,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    duration_ms INTEGER,
    width INTEGER,
    height INTEGER,

    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    verified_at TEXT,
    last_accessed_at TEXT,
    deleted_at TEXT
);
CREATE TABLE IF NOT EXISTS "legacy_observability_asset_sources" (
    source_id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL,
    source_type TEXT NOT NULL,
    source_reference TEXT NOT NULL,
    source_account_id TEXT,
    imported_at TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY(asset_id) REFERENCES "legacy_observability_assets"(asset_id)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_asset_locations" (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id        TEXT NOT NULL
                    REFERENCES "legacy_observability_media_assets"(id)
                    ON DELETE CASCADE,
    location_kind   TEXT NOT NULL        
                    CHECK (location_kind IN ('local', 'drive', 'object_storage')),
    uri             TEXT NOT NULL,       
    mime_type       TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    file_hash       TEXT NOT NULL DEFAULT '',
    is_primary      INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT '', external_id  TEXT NOT NULL DEFAULT '', access_url   TEXT NOT NULL DEFAULT '', download_url TEXT NOT NULL DEFAULT '', web_view_link TEXT NOT NULL DEFAULT '',
    UNIQUE (asset_id, location_kind)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_worker_nodes" (
    id                  TEXT PRIMARY KEY,
    name                TEXT NOT NULL,
    status              TEXT NOT NULL,
    session_id          TEXT NOT NULL,
    session_expires_at  TEXT NOT NULL,
    capabilities_json   TEXT NOT NULL,
    version             TEXT NOT NULL,
    hostname            TEXT NOT NULL,
    last_seen_at        TEXT NOT NULL,
    created_at          TEXT NOT NULL,
    updated_at          TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS "legacy_observability_workflows" (
    id              TEXT PRIMARY KEY,
    type            TEXT NOT NULL,
    version         INTEGER NOT NULL,
    status          TEXT NOT NULL,
    correlation_id  TEXT NOT NULL DEFAULT '',
    idempotency_key TEXT NOT NULL DEFAULT '',
    input_json      TEXT NOT NULL,
    output_json     TEXT NOT NULL DEFAULT '{}',
    error_code      TEXT NOT NULL DEFAULT '',
    error_message   TEXT NOT NULL DEFAULT '',
    revision        INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    started_at      TEXT,
    completed_at    TEXT,
    cancelled_at    TEXT
);
CREATE TABLE IF NOT EXISTS "legacy_observability_workflow_steps" (
    id              TEXT PRIMARY KEY,
    workflow_id     TEXT NOT NULL,
    step_key        TEXT NOT NULL,
    step_type       TEXT NOT NULL,
    status          TEXT NOT NULL,
    position        INTEGER NOT NULL,
    job_id          TEXT,
    attempt_count   INTEGER NOT NULL DEFAULT 0,
    max_attempts    INTEGER NOT NULL DEFAULT 3,
    input_json      TEXT NOT NULL DEFAULT '{}',
    output_json     TEXT NOT NULL DEFAULT '{}',
    error_code      TEXT NOT NULL DEFAULT '',
    error_message   TEXT NOT NULL DEFAULT '',
    available_at    TEXT,
    started_at      TEXT,
    completed_at    TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,

    FOREIGN KEY(workflow_id) REFERENCES "legacy_observability_workflows"(id),
    FOREIGN KEY(job_id) REFERENCES "legacy_observability_jobs"(id),
    UNIQUE(workflow_id, step_key)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_workflow_step_dependencies" (
    workflow_id        TEXT NOT NULL,
    step_id            TEXT NOT NULL,
    depends_on_step_id TEXT NOT NULL,

    PRIMARY KEY(step_id, depends_on_step_id),
    FOREIGN KEY(workflow_id) REFERENCES "legacy_observability_workflows"(id),
    FOREIGN KEY(step_id) REFERENCES "legacy_observability_workflow_steps"(id),
    FOREIGN KEY(depends_on_step_id) REFERENCES "legacy_observability_workflow_steps"(id)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_asset_processing" (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id        TEXT NOT NULL,
    step            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'running', 'completed', 'failed')),
    started_at      TEXT,
    completed_at    TEXT,
    error_message   TEXT NOT NULL DEFAULT '',
    attempt_count   INTEGER NOT NULL DEFAULT 1,
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT '',
    updated_at      TEXT NOT NULL DEFAULT '',
    UNIQUE (asset_id, step)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_delivery_log" (
  id            INTEGER PRIMARY KEY AUTOINCREMENT,
  asset_id      TEXT NOT NULL,
  endpoint_url  TEXT NOT NULL,
  delivery_id   TEXT NOT NULL UNIQUE,
  status_code   INTEGER,
  response_hash TEXT,
  delivered_at  TEXT,
  created_at    TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS "legacy_observability_jobs" (
    id              TEXT    NOT NULL PRIMARY KEY,
    type            TEXT    NOT NULL,
    status          TEXT    NOT NULL CHECK(status IN (
                            'QUEUED',
                            'LEASED',
                            'RUNNING',
                            'SUCCEEDED',
                            'RETRY_WAIT',
                            'FAILED',
                            'CANCELLED'
                        )),
    priority        INTEGER NOT NULL DEFAULT 0,
    project         TEXT    NOT NULL DEFAULT '',
    video_name      TEXT    NOT NULL DEFAULT '',
    active_key      TEXT    NOT NULL DEFAULT '',
    correlation_id  TEXT    NOT NULL DEFAULT '',
    payload_json    TEXT    NOT NULL DEFAULT '{}',
    result_json     TEXT    NOT NULL DEFAULT '{}',
    progress        INTEGER NOT NULL DEFAULT 0,
    error           TEXT    NOT NULL DEFAULT '',
    retry_count     INTEGER NOT NULL DEFAULT 0,
    max_retries     INTEGER NOT NULL DEFAULT 3,
    worker_id       TEXT    NOT NULL DEFAULT '',
    lease_id        TEXT    NOT NULL DEFAULT '',
    lease_expiry    TEXT,
    created_at      TEXT    NOT NULL,
    updated_at      TEXT    NOT NULL,
    started_at      TEXT,
    completed_at    TEXT,
    cancelled_at    TEXT,
    revision        INTEGER NOT NULL DEFAULT 1
, parent_state_typed TEXT NOT NULL DEFAULT '', client_id TEXT NOT NULL DEFAULT '', idempotency_key TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS "legacy_observability_job_assets" (
    job_id TEXT NOT NULL,
    asset_id TEXT NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('voiceover','scene_image','stock_clip','music','font','subtitle','thumbnail')),
    ordinal INTEGER NOT NULL DEFAULT 0,
    required INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),

    PRIMARY KEY(job_id, role, ordinal),
    FOREIGN KEY(job_id) REFERENCES "legacy_observability_jobs"(id),
    FOREIGN KEY(asset_id) REFERENCES "legacy_observability_assets"(asset_id)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_outbox_events" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL DEFAULT '',
    aggregate_type TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL DEFAULT '',
    event_key TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'pending',
    attempt_count INTEGER NOT NULL DEFAULT 0,
    max_attempts INTEGER NOT NULL DEFAULT 10,
    last_error TEXT NOT NULL DEFAULT '',
    next_attempt_at TEXT,
    worker_id TEXT NOT NULL DEFAULT '',
    lease_id TEXT NOT NULL DEFAULT '',
    lease_expiry TEXT,
    completed_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT ''
, priority INTEGER NOT NULL DEFAULT 5);
CREATE TABLE IF NOT EXISTS "legacy_observability_clip_folders" (
    id TEXT PRIMARY KEY,
    source TEXT NOT NULL DEFAULT '',
    source_url TEXT NOT NULL DEFAULT '',
    video_id TEXT NOT NULL DEFAULT '',
    folder_id TEXT NOT NULL DEFAULT '',
    folder_path TEXT NOT NULL DEFAULT '',
    local_folder_path TEXT NOT NULL DEFAULT '',
    group_name TEXT NOT NULL DEFAULT '',
    manifest_txt_path TEXT NOT NULL DEFAULT '',
    manifest_json_path TEXT NOT NULL DEFAULT '',
    clip_count INTEGER NOT NULL DEFAULT 0,
    processed_count INTEGER NOT NULL DEFAULT 0,
    failed_count INTEGER NOT NULL DEFAULT 0,
    skipped_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL DEFAULT '',
    search_key TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS "legacy_observability_idempotency_keys" (
    key TEXT PRIMARY KEY,                       
    body_hash TEXT NOT NULL DEFAULT '',         
    status TEXT NOT NULL DEFAULT 'in_flight',   
    response_status INTEGER NOT NULL DEFAULT 0, 
    response_body TEXT NOT NULL DEFAULT '',     
    response_content_type TEXT NOT NULL DEFAULT '', 
    created_at TEXT NOT NULL,                   
    expires_at TEXT NOT NULL,                   
    last_replayed_at TEXT NOT NULL DEFAULT ''   
);
CREATE TABLE IF NOT EXISTS "legacy_observability_qdrant_cleanup_audit" (
    run_id           TEXT PRIMARY KEY,
    collection       TEXT NOT NULL,
    started_at       TEXT NOT NULL,
    completed_at     TEXT NOT NULL,
    status           TEXT NOT NULL,
    points_scanned   INTEGER NOT NULL DEFAULT 0,
    points_affected  INTEGER NOT NULL DEFAULT 0,
    errors_json      TEXT NOT NULL DEFAULT '[]',
    dry_run          INTEGER NOT NULL DEFAULT 0,
    keys_redacted_json TEXT NOT NULL DEFAULT '[]'
);
CREATE TABLE IF NOT EXISTS "legacy_observability_voiceovers" (
    id TEXT PRIMARY KEY,
    request_id TEXT NOT NULL DEFAULT '',
    text_hash TEXT NOT NULL DEFAULT '',
    text_preview TEXT NOT NULL DEFAULT '',
    language TEXT NOT NULL DEFAULT 'it',
    voice TEXT NOT NULL DEFAULT '',
    filename TEXT NOT NULL DEFAULT '',
    local_path TEXT NOT NULL DEFAULT '',
    cleaned_path TEXT NOT NULL DEFAULT '',
    folder_id TEXT NOT NULL DEFAULT '',
    folder_path TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link TEXT NOT NULL DEFAULT '',
    download_link TEXT NOT NULL DEFAULT '',
    file_hash TEXT NOT NULL DEFAULT '',
    fingerprint TEXT NOT NULL DEFAULT '',
    duration_seconds REAL NOT NULL DEFAULT 0.0,
    status TEXT NOT NULL DEFAULT 'pending',
    error TEXT NOT NULL DEFAULT '',
    strategy TEXT NOT NULL DEFAULT '',
    metadata TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
, job_id TEXT NOT NULL DEFAULT '', idempotency_key TEXT NOT NULL DEFAULT '');
CREATE INDEX IF NOT EXISTS idx_voiceovers_folder_id ON "legacy_observability_voiceovers"(folder_id);
CREATE TABLE IF NOT EXISTS "legacy_observability_subjects" (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    metadata_json TEXT NOT NULL DEFAULT '{}',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
, slug            TEXT    NOT NULL DEFAULT '', uuid            TEXT    NOT NULL DEFAULT '', display_name    TEXT    NOT NULL DEFAULT '', display_name_norm TEXT NOT NULL DEFAULT '', aliases         TEXT    NOT NULL DEFAULT '[]', kind            TEXT    NOT NULL DEFAULT 'person', origin          TEXT    NOT NULL DEFAULT 'image', category        TEXT    NOT NULL DEFAULT '', wikidata_id     TEXT    NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS "legacy_observability_asset_versions" (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id        TEXT NOT NULL
                    REFERENCES "legacy_observability_media_assets"(id)
                    ON DELETE CASCADE,
    version_number  INTEGER NOT NULL,
    source_uri      TEXT NOT NULL DEFAULT '',
    file_hash       TEXT NOT NULL DEFAULT '',
    file_size_bytes INTEGER NOT NULL DEFAULT 0,
    mime_type       TEXT NOT NULL DEFAULT '',
    metadata_json   TEXT NOT NULL DEFAULT '{}',
    created_at      TEXT NOT NULL DEFAULT '',

    
    
    
    
    
    UNIQUE (asset_id, version_number)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_qdrantprojection_checkpoints" (
    
    
    
    job_id              TEXT PRIMARY KEY,

    
    
    
    target_collection   TEXT NOT NULL,

    
    
    
    
    last_indexed_id     TEXT NOT NULL DEFAULT '',

    
    
    indexed_count       INTEGER NOT NULL DEFAULT 0,

    
    
    
    
    error_count         INTEGER NOT NULL DEFAULT 0,

    
    
    skipped_count       INTEGER NOT NULL DEFAULT 0,

    
    
    
    
    started_at          TEXT NOT NULL,

    
    
    
    
    
    finished_at         TEXT,

    
    
    
    last_batch_at       TEXT,

    
    
    
    
    
    
    
    
    
    
    status              TEXT NOT NULL DEFAULT 'running'
        CHECK (status IN ('running','succeeded','failed','abandoned')),

    
    
    
    
    
    updated_at          TEXT NOT NULL,

    
    
    
    last_error          TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS "legacy_observability_qdrantprojection_dlq" (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,

    
    
    job_id              TEXT NOT NULL,

    
    
    
    asset_id            TEXT NOT NULL DEFAULT '',

    
    
    
    
    
    
    
    reason_category     TEXT NOT NULL DEFAULT 'other'
        CHECK (reason_category IN ('embedding_obsolete','content_hash_missing','dimension_mismatch','payload_invalid','other')),

    
    
    last_error          TEXT NOT NULL DEFAULT '',

    
    observed_at         TEXT NOT NULL,

    
    
    resolved_at         TEXT,

    
    
    
    resolved_by         TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS "legacy_observability_qdrant_collections" (
    
    
    
    
    collection_name        TEXT PRIMARY KEY,

    
    
    
    
    schema_version         TEXT NOT NULL DEFAULT 'v3',

    
    
    created_at             TEXT NOT NULL,

    
    
    
    
    indexed_at             TEXT,

    
    
    verified_at            TEXT,

    
    
    
    
    
    
    promoted_at            TEXT,

    
    retired_at             TEXT,

    
    
    
    
    point_count            INTEGER NOT NULL DEFAULT 0,

    
    
    
    
    
    verification_hash      TEXT,

    
    
    
    
    
    
    
    
    
    
    status                 TEXT NOT NULL DEFAULT 'created'
        CHECK (status IN ('created','indexing','verified','active','reindexing','in_use','retired')),

    
    
    updated_at             TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS "legacy_observability_youtube_discoveries" (
    id                TEXT PRIMARY KEY,
    channel_id        TEXT NOT NULL,
    video_id          TEXT NOT NULL,
    policy_version    TEXT NOT NULL DEFAULT 'v1',
    state             TEXT NOT NULL DEFAULT 'pending',
    attempt_count     INTEGER NOT NULL DEFAULT 0,
    discovered_at     TEXT NOT NULL DEFAULT (datetime('now')),
    enqueued_at       TEXT,
    next_retry_at     TEXT,
    lease_owner       TEXT,
    lease_until       TEXT,
    job_id            TEXT,
    last_error        TEXT,
    source_url        TEXT,
    title             TEXT,
    
    
    
    outcome           TEXT NOT NULL DEFAULT 'pending',
    rejection_reason  TEXT,
    updated_at        TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE(channel_id, video_id, policy_version)
);
CREATE INDEX IF NOT EXISTS idx_media_assets_provider ON "legacy_observability_media_assets"(provider);
CREATE TABLE IF NOT EXISTS "legacy_observability_upload_intents" (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    voiceover_id    TEXT    NOT NULL,
    drive_file_id   TEXT    NOT NULL DEFAULT '',
    status          TEXT    NOT NULL
        CHECK (status IN ('pending', 'uploaded', 'finalized', 'completed', 'failed')),
    reason          TEXT    NOT NULL DEFAULT '',
    attempts        INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL,

    
    
    
    
    UNIQUE(voiceover_id)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_retrieved_image_details" (
    asset_id         TEXT PRIMARY KEY,
    source_image_url TEXT NOT NULL DEFAULT '',
    source_page_url  TEXT NOT NULL DEFAULT '',
    license          TEXT NOT NULL DEFAULT '',
    author           TEXT NOT NULL DEFAULT '',
    search_query     TEXT NOT NULL DEFAULT '',
    retrieved_at     TEXT NOT NULL DEFAULT '',
    provider         TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (asset_id) REFERENCES "legacy_observability_media_assets"(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_publication_intents" (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id           TEXT    NOT NULL DEFAULT '',
    attempt          INTEGER NOT NULL DEFAULT 0,
    artifact_id      TEXT    NOT NULL DEFAULT '',
    idempotency_key  TEXT    NOT NULL DEFAULT '',
    provider         TEXT    NOT NULL DEFAULT 'drive',
    state            TEXT    NOT NULL DEFAULT 'PREPARED'
        CHECK (state IN (
            'PREPARED', 'UPLOADING', 'PUBLISHED', 'COMMITTED',
            'ORPHANED', 'CLEANUP_PENDING', 'CLEANED', 'FAILED'
        )),
    remote_file_id   TEXT    NOT NULL DEFAULT '',
    last_error       TEXT    NOT NULL DEFAULT '',
    created_at       TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT    NOT NULL DEFAULT (datetime('now')),

    UNIQUE(idempotency_key)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_job_results" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    attempt INTEGER NOT NULL DEFAULT 0,
    result_hash TEXT NOT NULL DEFAULT '',
    codec_id TEXT NOT NULL DEFAULT '',
    result_payload TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    FOREIGN KEY (job_id) REFERENCES "legacy_observability_jobs"(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_monitor_enqueue_outbox" (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    discovery_id      TEXT NOT NULL,
    idempotency_key   TEXT NOT NULL UNIQUE,
    payload_json      TEXT NOT NULL,
    state             TEXT NOT NULL DEFAULT 'pending',
    created_at        TEXT NOT NULL DEFAULT (datetime('now')),
    dispatched_at     TEXT,
    job_id            TEXT,
    error             TEXT
, retry_count INTEGER NOT NULL DEFAULT 0, next_retry_at TEXT, lease_id TEXT NOT NULL DEFAULT '', lease_until TEXT);
CREATE TABLE IF NOT EXISTS "legacy_observability_execution_steps" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    job_id TEXT NOT NULL,
    step_key TEXT NOT NULL,
    input_fingerprint TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    attempt INTEGER NOT NULL DEFAULT 0,
    result_json TEXT NOT NULL DEFAULT '{}',
    artifact_refs_json TEXT NOT NULL DEFAULT '[]',
    started_at TEXT NOT NULL DEFAULT '',
    completed_at TEXT NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT ''
, lease_until TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS "legacy_observability_generated_image_details" (
    asset_id          TEXT PRIMARY KEY,
    prompt_original   TEXT NOT NULL DEFAULT '',
    prompt_resolved   TEXT NOT NULL DEFAULT '',
    style_id          TEXT NOT NULL DEFAULT '',
    style_version     TEXT NOT NULL DEFAULT '',
    model             TEXT NOT NULL DEFAULT '',
    seed              INTEGER NOT NULL DEFAULT 0,
    generation_job_id TEXT NOT NULL DEFAULT '',
    source_hash       TEXT NOT NULL DEFAULT '',
    FOREIGN KEY (asset_id) REFERENCES "legacy_observability_media_assets"(id) ON DELETE CASCADE
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_voiceovers_idempotency
    ON "legacy_observability_voiceovers"(idempotency_key) WHERE idempotency_key != '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_voiceovers_job_language
    ON "legacy_observability_voiceovers"(job_id, language) WHERE job_id != '';
CREATE TABLE IF NOT EXISTS "legacy_observability_artlist_download_audit" (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL DEFAULT 'artlist',
    account_id TEXT NOT NULL DEFAULT 'default',
    asset_id TEXT NOT NULL,
    external_url TEXT,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TEXT DEFAULT (datetime('now'))
, downloaded_at TEXT, license_id TEXT
    REFERENCES "legacy_observability_asset_licenses"(id) ON DELETE SET NULL, release_id TEXT
    REFERENCES "legacy_observability_asset_releases"(id) ON DELETE SET NULL, project_id TEXT, downloaded_by TEXT);
CREATE TABLE IF NOT EXISTS "legacy_observability_asset_licenses" (
    id TEXT PRIMARY KEY,
    provider TEXT NOT NULL,
    account_id TEXT NOT NULL DEFAULT 'default',
    project_id TEXT,
    asset_id TEXT NOT NULL,
    license_type TEXT NOT NULL DEFAULT 'standard',
    license_name TEXT,
    license_url TEXT,
    license_terms TEXT,
    receipt_url TEXT,
    receipt_path TEXT,
    certificate_url TEXT,
    certificate_path TEXT,
    valid_from TEXT,
    valid_until TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES "legacy_observability_media_assets"(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_asset_releases" (
    id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL,
    release_type TEXT NOT NULL CHECK (release_type IN ('model', 'property', 'both')),
    model_release_url TEXT,
    model_release_path TEXT,
    property_release_url TEXT,
    property_release_path TEXT,
    certificate_url TEXT,
    certificate_path TEXT,
    receipt_url TEXT,
    receipt_path TEXT,
    status TEXT DEFAULT 'pending',
    verified_at TEXT,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES "legacy_observability_media_assets"(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_asset_renditions" (
    id TEXT PRIMARY KEY,
    asset_id TEXT NOT NULL,
    location_id INTEGER,
    kind TEXT NOT NULL DEFAULT 'master',
    container TEXT,
    codec TEXT,
    width INTEGER,
    height INTEGER,
    fps REAL,
    bitrate INTEGER,
    color_space TEXT,
    sha256 TEXT,
    size_bytes INTEGER NOT NULL DEFAULT 0,
    created_at TEXT DEFAULT (datetime('now')),
    updated_at TEXT DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES "legacy_observability_media_assets"(id) ON DELETE CASCADE,
    FOREIGN KEY (location_id) REFERENCES "legacy_observability_asset_locations"(id) ON DELETE SET NULL
);
CREATE TABLE IF NOT EXISTS "legacy_observability_asset_text_track_segments" (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    track_id    INTEGER NOT NULL,
    sequence_no INTEGER NOT NULL,
    start_ms    INTEGER NOT NULL,
    end_ms      INTEGER NOT NULL,
    text        TEXT NOT NULL, text_hash TEXT NOT NULL DEFAULT '',

    FOREIGN KEY (track_id)
        REFERENCES "legacy_observability_asset_text_tracks"(id)
        ON DELETE CASCADE,
    UNIQUE(track_id, sequence_no)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_operations" (
    operation_id            TEXT PRIMARY KEY,
    scope                   TEXT NOT NULL,
    idempotency_key         TEXT NOT NULL,
    request_hash            TEXT NOT NULL,
    job_id                  TEXT NOT NULL,
    state                   TEXT NOT NULL,
    created_at              TEXT NOT NULL,
    updated_at              TEXT NOT NULL,
    supersedes_operation_id TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS "legacy_observability_artifact_stages" (
    id                 TEXT PRIMARY KEY,
    job_id             TEXT NOT NULL DEFAULT '',
    local_path         TEXT NOT NULL DEFAULT '',
    hash               TEXT NOT NULL DEFAULT '',
    size               INTEGER NOT NULL DEFAULT 0,
    mime               TEXT NOT NULL DEFAULT '',
    requirement        TEXT NOT NULL DEFAULT 'optional'
        CHECK (requirement IN ('required','optional')),
    destination        TEXT NOT NULL DEFAULT '',
    state              TEXT NOT NULL DEFAULT 'STAGED'
        CHECK (state IN ('STAGED','PUBLISHED','SUCCEEDED','FAILED_PERMANENT')),
    attempt_count      INTEGER NOT NULL DEFAULT 0,
    last_error         TEXT NOT NULL DEFAULT '',
    published_location TEXT NOT NULL DEFAULT '',
    published_at       TEXT,
    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS "legacy_observability_drive_folder_catalog" (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    destination      TEXT NOT NULL,
    namespace        TEXT NOT NULL DEFAULT '',
    path             TEXT NOT NULL,
    folder_id        TEXT NOT NULL DEFAULT '',
    parent_folder_id TEXT NOT NULL DEFAULT '',
    source           TEXT NOT NULL DEFAULT 'created',
    status           TEXT NOT NULL DEFAULT 'active',
    created_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),
    updated_at       TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ', 'now')),

    UNIQUE(destination, path)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_media_assets_pipeline_events" (
    id           TEXT PRIMARY KEY,
    clip_id      TEXT NOT NULL,
    run_id       TEXT NOT NULL DEFAULT '',
    fase         TEXT NOT NULL,
    attempt      INTEGER NOT NULL DEFAULT 1,
    error_code   TEXT NOT NULL DEFAULT '',
    safe_message TEXT NOT NULL DEFAULT '',
    retryable    INTEGER NOT NULL DEFAULT 0,
    source_url   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS "legacy_observability_asset_visual_summaries" (
    asset_id              TEXT PRIMARY KEY NOT NULL,

    visual_summary_text   TEXT NOT NULL DEFAULT '',
    visible_actions_json  TEXT NOT NULL DEFAULT '[]',  
    visible_entities_json TEXT NOT NULL DEFAULT '[]',  

    frame_count           INTEGER NOT NULL DEFAULT 0
                          CHECK (frame_count >= 0),
    interval_seconds      REAL NOT NULL DEFAULT 0.0
                          CHECK (interval_seconds >= 0.0),

    preprocessing_version TEXT NOT NULL DEFAULT '',  
    model_name            TEXT NOT NULL DEFAULT '',  
    model_version         TEXT NOT NULL DEFAULT '',  

    source_hash           TEXT NOT NULL DEFAULT '',
    sampled_at            TEXT NOT NULL DEFAULT '',  
    sampled_at_unix       INTEGER NOT NULL DEFAULT 0,

    created_at            TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at            TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES "legacy_observability_media_assets"(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_asset_artifacts" (
    id            TEXT PRIMARY KEY,
    asset_id      TEXT NOT NULL,

    role          TEXT NOT NULL
                  CHECK (role IN ('render_master','preview','thumbnail','waveform','source_archive')),
    mime_type     TEXT NOT NULL DEFAULT '',

    local_path    TEXT NOT NULL DEFAULT '',
    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link    TEXT NOT NULL DEFAULT '',

    file_size     INTEGER NOT NULL DEFAULT 0,
    file_sha256   TEXT NOT NULL DEFAULT '',

    width         INTEGER NOT NULL DEFAULT 0,
    height        INTEGER NOT NULL DEFAULT 0,
    frame_rate    REAL NOT NULL DEFAULT 0.0,
    duration_ms   INTEGER NOT NULL DEFAULT 0,

    status        TEXT NOT NULL DEFAULT 'pending'
                  CHECK (status IN ('pending','uploaded','verified','deleted')),

    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (asset_id) REFERENCES "legacy_observability_media_assets"(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_script_localizations" (
    script_id          INTEGER NOT NULL,

    source_script_hash TEXT NOT NULL
                       CHECK (length(source_script_hash) > 0),

    language_code      TEXT NOT NULL
                       CHECK (length(language_code) >= 2),

    specscene_json     TEXT NOT NULL DEFAULT ''
                       CHECK (status != 'ready' OR length(specscene_json) > 0),

    translation_model  TEXT NOT NULL DEFAULT '',
    model_version      TEXT NOT NULL DEFAULT '',
    prompt_version     TEXT NOT NULL DEFAULT '',

    status             TEXT NOT NULL DEFAULT 'pending'
                       CHECK (status IN ('pending','running','ready','failed')),

    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT NOT NULL DEFAULT (datetime('now')),

    FOREIGN KEY (script_id) REFERENCES "legacy_observability_scripts"(id) ON DELETE CASCADE,

    UNIQUE(script_id, source_script_hash, language_code, model_version, prompt_version)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_asset_text_tracks" (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,

    asset_id            TEXT NOT NULL,
    language_code       TEXT NOT NULL,
    text_kind           TEXT NOT NULL,

    text_content        TEXT NOT NULL DEFAULT '',

    source_type         TEXT NOT NULL DEFAULT 'provided',
    source_language_code TEXT NOT NULL DEFAULT '',
    is_original         INTEGER NOT NULL DEFAULT 0,

    provider            TEXT NOT NULL DEFAULT '',
    model_name          TEXT NOT NULL DEFAULT '',
    model_version       TEXT NOT NULL DEFAULT '',
    prompt_version      TEXT NOT NULL DEFAULT '',

    text_hash           TEXT NOT NULL DEFAULT '',
    source_version      TEXT NOT NULL DEFAULT '',
    translation_key     TEXT NOT NULL DEFAULT '',
    is_current          INTEGER NOT NULL DEFAULT 1,

    confidence          REAL,  
    status              TEXT NOT NULL DEFAULT 'READY'
                        CHECK (status IN ('READY', 'PENDING', 'FAILED')),

    created_at          TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at          TEXT NOT NULL DEFAULT (datetime('now')), source_track_id INTEGER
    REFERENCES "legacy_observability_asset_text_tracks"(id) ON DELETE SET NULL, source_text_hash TEXT NOT NULL DEFAULT '',

    FOREIGN KEY (asset_id) REFERENCES "legacy_observability_media_assets"(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_stock_source_cache" (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    cache_key       TEXT    NOT NULL,
    provider        TEXT    NOT NULL DEFAULT '',
    external_id     TEXT    NOT NULL DEFAULT '',
    source_url      TEXT    NOT NULL,
    local_path      TEXT    NOT NULL,
    file_size       INTEGER NOT NULL DEFAULT 0,
    file_hash       TEXT    NOT NULL DEFAULT '',
    download_section TEXT   NOT NULL DEFAULT '',
    merge_format    TEXT    NOT NULL DEFAULT '',
    force_keyframes INTEGER NOT NULL DEFAULT 0,
    state           TEXT    NOT NULL DEFAULT 'active'
                            CHECK (state IN ('active', 'invalidated', 'expired')),
    last_verified_at TEXT,
    created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT    NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS pipeline_runs (
    id TEXT PRIMARY KEY
, job_id TEXT, idempotency_key TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'PENDING', current_stage TEXT NOT NULL DEFAULT 'NORMALIZING', requested_payload_json TEXT NOT NULL DEFAULT '{}', result_json TEXT, error_code TEXT, error_message TEXT, failed_stage TEXT, attempt_count INTEGER NOT NULL DEFAULT 0, next_retry_at TEXT, created_at TEXT NOT NULL DEFAULT (datetime('now')), updated_at TEXT NOT NULL DEFAULT (datetime('now')));
CREATE INDEX IF NOT EXISTS idx_pipeline_runs_job_id ON pipeline_runs(job_id);
CREATE INDEX IF NOT EXISTS idx_pipeline_runs_idempotency_key ON pipeline_runs(idempotency_key);
CREATE INDEX IF NOT EXISTS idx_pipeline_runs_status ON pipeline_runs(status);
CREATE INDEX IF NOT EXISTS idx_pipeline_runs_next_retry_at ON pipeline_runs(next_retry_at);
CREATE TABLE IF NOT EXISTS "legacy_observability_stock_batches" (
    id                TEXT PRIMARY KEY,
    fingerprint       TEXT NOT NULL DEFAULT '',
    source_url        TEXT NOT NULL DEFAULT '',
    source_cache_key  TEXT NOT NULL DEFAULT '',
    root_folder_id    TEXT NOT NULL DEFAULT '',
    root_folder_name  TEXT NOT NULL DEFAULT '',
    status            TEXT NOT NULL DEFAULT 'PLANNED'
                      CHECK (status IN ('PLANNED','RUNNING','SUCCEEDED','FAILED','RETRY_WAIT')),
    expected_groups   INTEGER NOT NULL DEFAULT 0,
    expected_clips    INTEGER NOT NULL DEFAULT 0,
    verified_clips    INTEGER NOT NULL DEFAULT 0,
    policy_version    TEXT NOT NULL DEFAULT '',
    last_error        TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at        TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS "legacy_observability_stock_batch_groups" (
    id               TEXT PRIMARY KEY,
    batch_id         TEXT NOT NULL REFERENCES "legacy_observability_stock_batches"(id) ON DELETE CASCADE,
    group_key        TEXT NOT NULL DEFAULT '',
    title            TEXT NOT NULL DEFAULT '',
    folder_name      TEXT NOT NULL DEFAULT '',
    drive_folder_id  TEXT NOT NULL DEFAULT '',
    start_sec        REAL NOT NULL DEFAULT 0,
    end_sec          REAL NOT NULL DEFAULT 0,
    expected_clips   INTEGER NOT NULL DEFAULT 0,
    verified_clips   INTEGER NOT NULL DEFAULT 0,
    status           TEXT NOT NULL DEFAULT 'PLANNED'
                     CHECK (status IN ('PLANNED','RUNNING','SUCCEEDED','FAILED','RETRY_WAIT')),
    child_job_id     TEXT NOT NULL DEFAULT '',
    attempts         INTEGER NOT NULL DEFAULT 0,
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at       TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS "legacy_observability_stock_artifacts" (
    id                   TEXT PRIMARY KEY,
    batch_id             TEXT NOT NULL REFERENCES "legacy_observability_stock_batches"(id) ON DELETE CASCADE,
    group_id             TEXT NOT NULL REFERENCES "legacy_observability_stock_batch_groups"(id) ON DELETE CASCADE,
    ordinal              INTEGER NOT NULL DEFAULT 0,
    artifact_key         TEXT NOT NULL DEFAULT '',
    source_url           TEXT NOT NULL DEFAULT '',
    start_sec            REAL NOT NULL DEFAULT 0,
    end_sec              REAL NOT NULL DEFAULT 0,
    expected_duration_ms INTEGER NOT NULL DEFAULT 0,
    actual_duration_ms   INTEGER NOT NULL DEFAULT 0,
    local_path           TEXT NOT NULL DEFAULT '',
    sha256               TEXT NOT NULL DEFAULT '',
    status               TEXT NOT NULL DEFAULT 'PLANNED'
                         CHECK (status IN ('PLANNED','EXTRACTING','EXTRACTED','COMPOSING','COMPOSED','PUBLISHING','PUBLISHED','VERIFIED','RETRY_WAIT','FAILED_PERMANENT','QUARANTINED')),
    drive_file_id        TEXT NOT NULL DEFAULT '',
    drive_folder_id      TEXT NOT NULL DEFAULT '',
    drive_link           TEXT NOT NULL DEFAULT '',
    attempts             INTEGER NOT NULL DEFAULT 0,
    last_error           TEXT NOT NULL DEFAULT '',
    created_at           TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at           TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS "legacy_observability_media_concepts" (
    id                  TEXT     PRIMARY KEY,
    canonical_text      TEXT     NOT NULL,
    language            TEXT     NOT NULL,
    normalized_text     TEXT     NOT NULL,
    phrase_fingerprint  TEXT     NOT NULL,
    concept_type        TEXT     NOT NULL,
    embedding_version   TEXT,
    created_at          DATETIME NOT NULL,
    updated_at          DATETIME NOT NULL,
    UNIQUE(language, phrase_fingerprint)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_media_bindings" (
    id                TEXT     PRIMARY KEY,
    concept_id        TEXT     NOT NULL,
    asset_id          TEXT     NOT NULL,
    start_ms          INTEGER,
    end_ms            INTEGER,
    slot_kind         TEXT     NOT NULL,
    origin            TEXT     NOT NULL,
    approval_status   TEXT     NOT NULL,
    manual_score      REAL     NOT NULL DEFAULT 0,
    semantic_score    REAL     NOT NULL DEFAULT 0,
    quality_score     REAL     NOT NULL DEFAULT 0,
    success_score     REAL     NOT NULL DEFAULT 0,
    usage_count       INTEGER  NOT NULL DEFAULT 0,
    last_used_at      DATETIME,
    created_at        DATETIME NOT NULL,
    updated_at        DATETIME NOT NULL, provider TEXT NOT NULL DEFAULT 'local',
    UNIQUE(concept_id, asset_id, slot_kind),
    FOREIGN KEY(concept_id) REFERENCES "legacy_observability_media_concepts"(id)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_media_query_cache" (
    id                  TEXT     PRIMARY KEY,
    phrase_fingerprint  TEXT     NOT NULL,
    language            TEXT     NOT NULL,
    request_json        TEXT     NOT NULL,
    result_json         TEXT     NOT NULL,
    provider_state_json TEXT,
    hit_count           INTEGER  NOT NULL DEFAULT 0,
    expires_at          DATETIME,
    created_at          DATETIME NOT NULL,
    updated_at          DATETIME NOT NULL,
    UNIQUE(phrase_fingerprint)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_media_candidates" (
    id                     TEXT     PRIMARY KEY,
    provider               TEXT     NOT NULL,
    provider_asset_id      TEXT     NOT NULL,
    source_url             TEXT     NOT NULL,
    thumbnail_url          TEXT,
    title                  TEXT,
    description            TEXT,
    duration_ms            INTEGER,
    candidate_score        REAL     NOT NULL DEFAULT 0,
    rights_status          TEXT     NOT NULL,
    license_basis          TEXT,
    allowed_channels       TEXT,
    allowed_regions        TEXT,
    owner                  TEXT,
    expiration             DATETIME,
    discovery_status       TEXT     NOT NULL,
    materialization_status TEXT     NOT NULL,
    asset_id               TEXT,
    created_at             DATETIME NOT NULL,
    updated_at             DATETIME NOT NULL,
    UNIQUE(provider, provider_asset_id)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_media_usage_events" (
    id                TEXT     PRIMARY KEY,
    project_id        TEXT     NOT NULL,
    scene_id          TEXT     NOT NULL,
    concept_id        TEXT     NOT NULL,
    asset_id          TEXT     NOT NULL,
    binding_id        TEXT     NOT NULL,
    slot_kind         TEXT     NOT NULL,
    selected          INTEGER  NOT NULL DEFAULT 0,
    manually_selected INTEGER  NOT NULL DEFAULT 0,
    rejected          INTEGER  NOT NULL DEFAULT 0,
    render_completed  INTEGER  NOT NULL DEFAULT 0,
    created_at        DATETIME NOT NULL
, channel_id TEXT NOT NULL DEFAULT '', video_id TEXT NOT NULL DEFAULT '');
CREATE TABLE IF NOT EXISTS "legacy_observability_asset_subtitle_artifacts" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id TEXT NOT NULL,
    text_track_id INTEGER NOT NULL,
    language_code TEXT NOT NULL,
    format TEXT NOT NULL CHECK (format IN ('ass', 'srt', 'vtt')),

    local_path TEXT NOT NULL,
    drive_file_id TEXT NOT NULL DEFAULT '',

    file_hash TEXT NOT NULL,
    text_hash TEXT NOT NULL,
    cues_hash TEXT NOT NULL,
    clip_content_hash TEXT NOT NULL,

    cue_count INTEGER NOT NULL,
    clip_duration_ms INTEGER NOT NULL,
    last_cue_end_ms INTEGER NOT NULL,

    style_version TEXT NOT NULL,
    generator_version TEXT NOT NULL,

    status TEXT NOT NULL
        CHECK (status IN ('PENDING', 'READY', 'FAILED', 'STALE')),

    is_current INTEGER NOT NULL DEFAULT 1,
    validation_error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP, drive_url TEXT NOT NULL DEFAULT '',

    FOREIGN KEY (asset_id) REFERENCES "legacy_observability_media_assets"(id) ON DELETE CASCADE,
    FOREIGN KEY (text_track_id) REFERENCES "legacy_observability_asset_text_tracks"(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_clip_search_terms" (
    clip_id TEXT NOT NULL,
    term    TEXT NOT NULL,
    source  TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (clip_id, term)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_clip_storage_index" (
    clip_key        TEXT PRIMARY KEY,        
    asset_id        TEXT,                    
    has_db          INTEGER NOT NULL,        
    has_drive       INTEGER NOT NULL,        
    has_qdrant      INTEGER NOT NULL,        
    drive_file_id   TEXT,                    
    drive_link      TEXT,                    
    qdrant_point_id TEXT,                    
    persisted_at    TEXT,                    
    uploaded_at     TEXT,                    
    indexed_at      TEXT,                    
    created_at      TEXT NOT NULL,           
    updated_at      TEXT NOT NULL            
);
CREATE TABLE IF NOT EXISTS job_attempts (
 attempt_id TEXT PRIMARY KEY,
 job_id TEXT NOT NULL,
 run_id TEXT NOT NULL UNIQUE,
 attempt_number INTEGER NOT NULL DEFAULT 1,
 worker_id TEXT,
 lease_id TEXT,
 status TEXT NOT NULL,
 available_at TEXT,
 started_at TEXT,
 finished_at TEXT,
 lease_expires_at TEXT,
 error_code TEXT,
 error TEXT,
 created_at TEXT NOT NULL,
 updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_job_attempts_job ON job_attempts(job_id, attempt_number);
CREATE INDEX IF NOT EXISTS idx_job_attempts_recovery ON job_attempts(status, lease_expires_at);
CREATE TABLE IF NOT EXISTS run_observability (
 run_id TEXT PRIMARY KEY,
 job_id TEXT NOT NULL,
 job_type TEXT NOT NULL DEFAULT '',
 attempt_id TEXT NOT NULL UNIQUE,
 parent_run_id TEXT,
 worker_id TEXT,
 lease_id TEXT,
 lease_expires_at TEXT,
 status TEXT NOT NULL CHECK(status IN ('RUNNING','SUCCEEDED','FAILED','CANCELLED','ABANDONED')),
 created_at TEXT NOT NULL,
 started_at TEXT NOT NULL,
 finished_at TEXT,
 queue_wait_ms INTEGER NOT NULL DEFAULT 0,
 wall_time_ms INTEGER NOT NULL DEFAULT 0,
 active_ms INTEGER NOT NULL DEFAULT 0,
 blocked_ms INTEGER NOT NULL DEFAULT 0,
 accumulated_operation_ms INTEGER NOT NULL DEFAULT 0,
 error_code TEXT,
 error TEXT,
 counters_json TEXT NOT NULL DEFAULT '{}',
 children_json TEXT,
 report_json TEXT NOT NULL DEFAULT '{}',
 observability_degraded INTEGER NOT NULL DEFAULT 0,
 updated_at TEXT NOT NULL, workflow_payload_json TEXT NOT NULL DEFAULT '{}',
 FOREIGN KEY(attempt_id) REFERENCES job_attempts(attempt_id)
);
CREATE INDEX IF NOT EXISTS idx_run_observability_job ON run_observability(job_id, created_at);
CREATE INDEX IF NOT EXISTS idx_run_observability_recovery ON run_observability(status, lease_expires_at);
CREATE TABLE IF NOT EXISTS run_stage_observations (
 observation_id TEXT PRIMARY KEY,
 run_id TEXT NOT NULL,
 name TEXT NOT NULL,
 status TEXT NOT NULL,
 duration_ms INTEGER NOT NULL DEFAULT 0,
 attempts INTEGER NOT NULL DEFAULT 0,
 cache_status TEXT,
 error_code TEXT,
 items_input INTEGER NOT NULL DEFAULT 0,
 items_completed INTEGER NOT NULL DEFAULT 0,
 items_failed INTEGER NOT NULL DEFAULT 0,
 bytes_processed INTEGER NOT NULL DEFAULT 0, started_at TEXT, finished_at TEXT,
 FOREIGN KEY(run_id) REFERENCES run_observability(run_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_run_stage_observations_run ON run_stage_observations(run_id);
CREATE TABLE IF NOT EXISTS run_operation_observations (
 observation_id TEXT PRIMARY KEY,
 run_id TEXT NOT NULL,
 stage TEXT NOT NULL,
 component TEXT NOT NULL,
 operation TEXT NOT NULL,
 provider TEXT,
 status TEXT NOT NULL,
 duration_ms INTEGER NOT NULL DEFAULT 0,
 attempts INTEGER NOT NULL DEFAULT 0,
 items INTEGER NOT NULL DEFAULT 0,
 bytes INTEGER NOT NULL DEFAULT 0,
 cache_status TEXT,
 error_code TEXT, queue_wait_ms INTEGER NOT NULL DEFAULT 0, worker_id TEXT, queued_at TEXT, started_at TEXT, finished_at TEXT, metadata_json TEXT NOT NULL DEFAULT '{}', output_duration_ms INTEGER NOT NULL DEFAULT 0, source_sha256 TEXT NOT NULL DEFAULT '', source_duration_ms INTEGER NOT NULL DEFAULT 0, source_size_bytes INTEGER NOT NULL DEFAULT 0, width INTEGER NOT NULL DEFAULT 0, height INTEGER NOT NULL DEFAULT 0, fps REAL NOT NULL DEFAULT 0, input_codec TEXT NOT NULL DEFAULT '', output_codec TEXT NOT NULL DEFAULT '', cache_hit INTEGER NOT NULL DEFAULT 0, strategy TEXT NOT NULL DEFAULT '', output_size_bytes INTEGER NOT NULL DEFAULT 0, cpu_user_ms INTEGER NOT NULL DEFAULT 0, cpu_system_ms INTEGER NOT NULL DEFAULT 0, created_at TEXT,
 FOREIGN KEY(run_id) REFERENCES run_observability(run_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_run_operation_observations_run ON run_operation_observations(run_id);
CREATE TABLE IF NOT EXISTS run_artifact_observations (
 observation_id TEXT PRIMARY KEY,
 run_id TEXT NOT NULL,
 kind TEXT NOT NULL,
 ref TEXT,
 url TEXT,
 stage TEXT,
 bytes INTEGER NOT NULL DEFAULT 0,
 reused INTEGER NOT NULL DEFAULT 0,
 FOREIGN KEY(run_id) REFERENCES run_observability(run_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_run_artifact_observations_run ON run_artifact_observations(run_id);
CREATE TABLE IF NOT EXISTS run_child_observations (
 parent_run_id TEXT NOT NULL,
 child_job_id TEXT NOT NULL,
 child_run_id TEXT NOT NULL,
 status TEXT NOT NULL,
 wall_time_ms INTEGER NOT NULL DEFAULT 0,
 updated_at TEXT NOT NULL,
 PRIMARY KEY(parent_run_id, child_job_id),
 FOREIGN KEY(parent_run_id) REFERENCES run_observability(run_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS "legacy_observability_source_identity_registry" (
    source_type         TEXT NOT NULL,
    source_key          TEXT NOT NULL,
    content_sha256      TEXT NOT NULL,
    source_version      TEXT NOT NULL DEFAULT '',
    discovered_at       TEXT NOT NULL,
    last_seen_at        TEXT NOT NULL,
    verification_status TEXT NOT NULL DEFAULT 'UNVERIFIED',
    PRIMARY KEY (source_type, source_key)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_asset_render_variants" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_clip_id TEXT NOT NULL,
    language_code TEXT NOT NULL,

    fingerprint TEXT NOT NULL,
    source_clip_sha256 TEXT NOT NULL,
    transcript_sha256 TEXT NOT NULL,
    translation_version TEXT NOT NULL DEFAULT '',
    subtitle_style_version TEXT NOT NULL DEFAULT '',
    render_profile_version TEXT NOT NULL DEFAULT '',

    subtitle_hash TEXT NOT NULL DEFAULT '',
    output_hash TEXT NOT NULL DEFAULT '',

    drive_file_id TEXT NOT NULL DEFAULT '',
    drive_link TEXT NOT NULL DEFAULT '',

    duration_ms INTEGER NOT NULL DEFAULT 0,
    size_bytes INTEGER NOT NULL DEFAULT 0,

    status TEXT NOT NULL
        CHECK (status IN ('PENDING', 'READY', 'FAILED')),
    validation_error TEXT NOT NULL DEFAULT '',

    is_current INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,

    FOREIGN KEY (source_clip_id) REFERENCES "legacy_observability_media_assets"(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_run_operation_observations_lifecycle
    ON run_operation_observations(run_id, started_at, finished_at);
CREATE INDEX IF NOT EXISTS idx_run_operation_observations_worker
    ON run_operation_observations(run_id, worker_id, started_at);
CREATE TABLE IF NOT EXISTS run_resource_reports (
    run_id             TEXT PRIMARY KEY,
    job_id             TEXT NOT NULL,
    attempt_id         TEXT NOT NULL UNIQUE,
    schema_version     INTEGER NOT NULL,
    started_at         TEXT,
    finished_at        TEXT,
    sample_count       INTEGER NOT NULL DEFAULT 0,
    report_json        TEXT NOT NULL,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS run_resource_samples (
    sample_id          TEXT PRIMARY KEY,
    run_id             TEXT NOT NULL,
    job_id             TEXT NOT NULL,
    attempt_id         TEXT NOT NULL,
    observed_at        TEXT NOT NULL,
    sample_json        TEXT NOT NULL,
    created_at         TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_run_resource_reports_job
    ON run_resource_reports(job_id, updated_at);
CREATE INDEX IF NOT EXISTS idx_run_resource_samples_run
    ON run_resource_samples(run_id, observed_at);
CREATE INDEX IF NOT EXISTS idx_run_resource_samples_job
    ON run_resource_samples(job_id, observed_at);
CREATE TABLE IF NOT EXISTS run_resource_aggregates (
    run_id          TEXT PRIMARY KEY,
    job_id          TEXT NOT NULL,
    attempt_id      TEXT NOT NULL UNIQUE,
    schema_version  INTEGER NOT NULL,
    sample_count    INTEGER NOT NULL DEFAULT 0,
    first_observed_at TEXT,
    last_observed_at  TEXT,
    aggregate_json  TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_run_resource_samples_retention
    ON run_resource_samples(observed_at);
CREATE INDEX IF NOT EXISTS idx_run_resource_aggregates_retention
    ON run_resource_aggregates(updated_at);
CREATE TABLE IF NOT EXISTS "legacy_observability_preparation_attempts" (
    attempt_id          TEXT PRIMARY KEY,

    unit_fingerprint    TEXT NOT NULL,

    trigger_job_id      TEXT NOT NULL DEFAULT '',

    worker_id           TEXT NOT NULL DEFAULT '',
    host                TEXT NOT NULL DEFAULT '',

    execution_mode      TEXT NOT NULL
                        CHECK (execution_mode IN ('SPECULATIVE', 'ACTIVE', 'ADOPTION_CHECK')),

    resource_class      TEXT NOT NULL,

    scheduler_priority  REAL NOT NULL DEFAULT 0,

    status              TEXT NOT NULL
                        CHECK (status IN ('RUNNING', 'READY', 'FAILED', 'CANCELLED', 'PREEMPTED', 'HIT')),

    expected_work_ms    INTEGER NOT NULL DEFAULT 0,

    workload_dimension  TEXT NOT NULL DEFAULT '',
    workload_amount     REAL NOT NULL DEFAULT 0,

    queued_at           TEXT,
    started_at          TEXT NOT NULL,
    finished_at         TEXT,

    queue_wait_ms       INTEGER NOT NULL DEFAULT 0,
    wall_ms             INTEGER NOT NULL DEFAULT 0,

    singleflight_wait_ms INTEGER NOT NULL DEFAULT 0,

    bytes_read          INTEGER NOT NULL DEFAULT 0,
    bytes_written       INTEGER NOT NULL DEFAULT 0,
    network_rx_bytes    INTEGER NOT NULL DEFAULT 0,
    network_tx_bytes    INTEGER NOT NULL DEFAULT 0,

    cache_hit           INTEGER NOT NULL DEFAULT 0,

    preempted_by_active INTEGER NOT NULL DEFAULT 0,

    estimated_saved_ms  INTEGER NOT NULL DEFAULT 0,

    error_code          TEXT NOT NULL DEFAULT '',
    error_message       TEXT NOT NULL DEFAULT '',

    created_at          TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS "legacy_observability_preparation_claim_snapshots" (
    job_id                  TEXT NOT NULL,
    attempt_id              TEXT NOT NULL,

    job_revision            INTEGER NOT NULL DEFAULT 0,

    claimed_at              TEXT NOT NULL,

    total_units             INTEGER NOT NULL DEFAULT 0,
    required_units          INTEGER NOT NULL DEFAULT 0,

    ready_units             INTEGER NOT NULL DEFAULT 0,
    running_units           INTEGER NOT NULL DEFAULT 0,
    missing_units           INTEGER NOT NULL DEFAULT 0,

    prepared_ratio          REAL NOT NULL DEFAULT 0,

    estimated_saved_ms      INTEGER NOT NULL DEFAULT 0,

    speculative_work_ms     INTEGER NOT NULL DEFAULT 0,

    queue_wait_ms           INTEGER NOT NULL DEFAULT 0,

    queue_position_at_plan  INTEGER NOT NULL DEFAULT 0,

    metadata_json           TEXT NOT NULL DEFAULT '{}',

    PRIMARY KEY (job_id, attempt_id)
);
CREATE TABLE IF NOT EXISTS "legacy_observability_asset_links" (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    asset_id TEXT NOT NULL,
    link_type TEXT NOT NULL,
    url TEXT NOT NULL,
    label TEXT,
    FOREIGN KEY (asset_id) REFERENCES "legacy_observability_asset_index"(asset_id) ON DELETE CASCADE
);
INSERT INTO control_plane_meta (singleton_id, database_id, schema_family, instance_role, canonical_version, created_at) SELECT 1, 'cp_baseline', 'pipelinegen-control-plane', 'CANONICAL', 1, datetime('now') WHERE NOT EXISTS (SELECT 1 FROM control_plane_meta);
