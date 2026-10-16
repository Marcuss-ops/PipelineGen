-- Reference to the raw deep-profile Chronon timing sidecar
-- (`<output>.timing.json`, including the unbounded per-frame frame_times_ms
-- array) preserved verbatim in the object store under its content address.
-- The bounded render_telemetry JSONB document remains the ledger copy; these
-- columns only carry the reference (key/url/sha/size) so the full per-frame
-- profile stays fetchable for post-mortem without ever inlining the array
-- into the artifact row. All columns are NULL when the sidecar file did not
-- exist or could not be preserved (fail-open).
ALTER TABLE render_artifacts
    ADD COLUMN IF NOT EXISTS chronon_timing_storage_key TEXT,
    ADD COLUMN IF NOT EXISTS chronon_timing_url TEXT,
    ADD COLUMN IF NOT EXISTS chronon_timing_sha256 TEXT,
    ADD COLUMN IF NOT EXISTS chronon_timing_size_bytes BIGINT,
    ADD COLUMN IF NOT EXISTS chronon_timing_content_type TEXT;
