-- migrations/sqlite/129_media_assets_pipeline_events.sql
--
-- PR-FASE-4-PIPELINE-EVENTS (July 2026) — per-item append-only
-- event log for the PipelineState state machine.
--
-- CONTEXT
--
-- Commit 1 of Fase 4 (eeb84ea4e, with follow-ups 5e2e347c9 +
-- c3e9b9498) introduced the canonical PipelineState enum (12
-- values) + IsValidTransition matrix + SanitizeSafeMessage
-- function. This migration is Commit 4 — the append-only
-- event log that backs every state transition. Future commits
-- add the *ClipsRepository.AppendPipelineEvent writer
-- (Commit 5) + the *PipelineEvent struct + integration tests
-- (Commit 6+).
--
-- SCHEMA (10 columns)
--
-- The 10 columns map to the 9 fields the user spec asks for
-- per transition (fase, attempt, codice errore tipizzato,
-- messaggio safe, retryability, timestamp, URL sorgente,
-- clip ID, run ID) plus an auto-generated id column (UUID).
-- See the COLUMN RATIONALE block below for the per-column
-- design notes.
--
-- godlike/06 SSOT: *ClipsRepository.AppendPipelineEvent is
-- the SOLE canonical writer of media_assets_pipeline_events.
-- Direct INSERTs from anywhere else are a godlike/06 violation
-- (scattered writers defeat the audit trail). The table is
-- APPEND-ONLY — no UPDATE or DELETE on existing rows. Drift
-- between the writer's INSERT column list and this schema is
-- caught at the SQL-layer fence in AppendPipelineEvent.
--
-- godlike/07 NO-FAKE-AVAILABILITY: every transition is a
-- typed event written here. The current PipelineState for a
-- clip is the fase column of the most-recent event for that
-- clip_id (read via MAX(created_at) subquery). The writer
-- gates on PipelineState.IsValidTransition so an illegal
-- transition surfaces as a typed error rather than a
-- silent row write.
--
-- IDEMPOTENT: CREATE TABLE IF NOT EXISTS + CREATE INDEX IF
-- NOT EXISTS. A re-run is a no-op. Test fixtures that call
-- drive.NewTestDBWithSchema and the production migration
-- runner both apply this file without coordination.
--
-- COLUMN RATIONALE
--
--   id           TEXT PRIMARY KEY (UUID v4). The writer
--                generates the UUID via uuid.New().String();
--                the schema does NOT use SQLite-side
--                randomness (godlike/06 SSOT — the writer
--                is the canonical producer, not the
--                database). TEXT PK matches the
--                media_assets.id column type for join
--                consistency.
--
--   clip_id      TEXT NOT NULL. The media_assets row this
--                event belongs to. No FK constraint — the
--                codebase convention (see canonical.go +
--                the migration history) is to enforce
--                referential integrity at the application
--                layer, not the SQL layer. The writer
--                validates the clip_id exists in
--                media_assets before INSERT.
--
--   run_id       TEXT NOT NULL DEFAULT ''. The artlist run
--                (or other batch identifier) that initiated
--                this transition. Empty when the event is
--                not associated with a run (e.g. a one-off
--                re-index triggered by the reconciler).
--
--   fase         TEXT NOT NULL. The PipelineState value
--                (DISCOVERED, DOWNLOAD_PENDING, etc.). No
--                CHECK constraint — the writer's
--                PipelineState.IsValidTransition gate is
--                the canonical validator (godlike/06 SSOT).
--                A future migration could add a CHECK
--                constraint if drift becomes a concern; the
--                current schema trusts the application-layer
--                gate.
--
--   attempt      INTEGER NOT NULL DEFAULT 1. The retry
--                attempt number for this transition. The
--                column default of 1 is the user-friendly
--                "first attempt" sentinel used when the
--                writer omits the value (a buggy INSERT
--                that forgets to set attempt would land
--                as 1, not 0, which matches the canonical
--                "first attempt = 1" mental model
--                operators expect in dashboards). The
--                writer (Commit 5+) sets this explicitly
--                per transition; a self-loop (idempotent
--                re-assertion of the same state) keeps
--                the counter unchanged; a retry that
--                triggers a new event row increments
--                this counter. Note: pkg/retry's internal
--                loop is 0-indexed (for i := 0; i <
--                opts.MaxAttempts; i++) — the
--                application-level column is intentionally
--                1-indexed for operator-triage clarity.
--
--   error_code   TEXT NOT NULL DEFAULT ''. The typed error
--                code (e.g. "DOWNLOAD_HTTP_404",
--                "PROCESSING_FFMPEG_INVALID_ARGUMENT").
--                Empty when the transition succeeded. The
--                code is a string identifier; a future
--                pkg/pipelineerrors package (Commit 8+)
--                will own the canonical list of sentinels
--                per the SanitizeSafeMessage forward-pointer.
--
--   safe_message TEXT NOT NULL DEFAULT ''. The sanitized
--                diagnostic line. The writer calls
--                SanitizeSafeMessage (defined in
--                domain/asset/pipeline_state.go) BEFORE
--                INSERT so the column never holds raw
--                operator logs (godlike/07 — no PII, no
--                control chars, no silent truncation).
--                The PII/secret detection is intentionally
--                NOT in SanitizeSafeMessage (Commit 1 scope
--                limit); the future pkg/safemessage package
--                (Commit 6+) is the layered PII sanitizer.
--
--   retryable    INTEGER NOT NULL DEFAULT 0. SQLite has
--                no native bool; the codebase convention
--                is INTEGER 0/1. 0 = non-retryable
--                (terminal error, e.g. 404, content
--                filter), 1 = retryable (transient
--                error, e.g. network 5xx, 429). The
--                canonical typed-sentinel mapping lives
--                in the future pkg/pipelineerrors
--                package; for now the writer classifies
--                at the call site.
--
--   source_url   TEXT NOT NULL DEFAULT ''. The URL the
--                worker is currently fetching or has just
--                fetched. Empty for transitions that don't
--                have a URL (e.g. INDEX_PENDING from
--                PUBLISHED, where the worker is enqueueing
--                a Qdrant request, not fetching bytes).
--                The source_url field is the per-event
--                URL context; the media_assets.url column
--                is the canonical URL for the clip (the
--                one stored at ingest time).
--
--   created_at   TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP.
--                Matches the media_assets.created_at
--                column convention (see canonical.go +
--                migrations/sqlite/033_*). Format:
--                "2026-07-12 10:00:00" (space separator,
--                no T/Z). The writer does NOT need to
--                insert an explicit value; the column
--                default is sufficient. The writer MAY
--                insert an explicit value when the event
--                is replayed from a backfill or migration
--                (the explicit value is preserved verbatim
--                via the same string-lex ordering the
--                production rows use).

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

-- Indexes for the two read-back patterns:
--
--   1. "All events for this clip, in chronological order"
--      → SELECT * FROM media_assets_pipeline_events
--        WHERE clip_id = ? ORDER BY created_at
--   2. "All events for this run, in chronological order"
--      → SELECT * FROM media_assets_pipeline_events
--        WHERE run_id = ? ORDER BY created_at
--
-- The (col, created_at) composite index supports both
-- the WHERE filter and the ORDER BY clause without a
-- filesort. The current-state read pattern
-- ("most-recent event for this clip") uses
--   SELECT fase FROM media_assets_pipeline_events
--   WHERE clip_id = ? ORDER BY created_at DESC LIMIT 1
-- which is also index-supported (the index is ordered on
-- (clip_id, created_at) ascending; the DESC + LIMIT 1
-- subquery uses the index in reverse).
--
-- No (retryable) index: that is a read-back pattern
-- via clip_id or run_id with a WHERE filter, not a
-- direct lookup. The composite indexes above support
-- the time-window queries operators care about (e.g.
-- "retryable errors in the last hour for run X" is
-- WHERE run_id = ? AND created_at > ? AND retryable = 1,
-- which the (run_id, created_at) index covers with the
-- retryable filter as a residual). A (fase) index IS
-- added below (NICE-TO-HAVE from the Commit 4 code
-- review) because the aggregate count query "how many
-- clips are currently in <state>?" is a high-frequency
-- operational read, not a per-clip / per-run lookup.

CREATE INDEX IF NOT EXISTS idx_media_assets_pipeline_events_clip_id_created_at
    ON media_assets_pipeline_events(clip_id, created_at);

CREATE INDEX IF NOT EXISTS idx_media_assets_pipeline_events_run_id_created_at
    ON media_assets_pipeline_events(run_id, created_at);

-- Index for the canonical operational query: "how many clips
-- are currently in <state>?". The Fase 2 diagnostics
-- wire-by-wire surface already does similar aggregate counts
-- (see clips_statistics.go::CountBySource for the parallel
-- pattern); the event log will likely surface the same shape
-- soon (e.g. "current DOWNLOADING count"). The index is
-- cheap (12 distinct values per the canonical PipelineState
-- enum; low-cardinality index is small) and avoids a future
-- migration just for the index. The composite (clip_id,
-- created_at) + (run_id, created_at) indexes above are NOT
-- a substitute for this — they support per-clip / per-run
-- chronological reads, not the "all rows where fase = X"
-- aggregate query.
CREATE INDEX IF NOT EXISTS idx_media_assets_pipeline_events_fase
    ON media_assets_pipeline_events(fase);
