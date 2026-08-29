-- 242_preparation_units.sql
-- Global deterministic preparation state keyed by reusable unit fingerprint.
-- Binary outputs remain owned by artifact_cache_entries/content_objects/CAS.
-- database: primary

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
