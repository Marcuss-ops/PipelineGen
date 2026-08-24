-- =============================================================================
-- 108_consolidate_monitored_sources_into_category_channels.sql
-- =============================================================================
--
-- Context (June 2026, Wave "CONFORMANCE-001"):
--   The policy canonical source for channel-monitor state is
--   `data/media/media.db.sqlite::category_channels`. The legacy
--   `monitored_sources` table is left in place ONLY as a transitional
--   shadow and is targeted for CONTRACT removal in a follow-up migration.
--
--   The columns a user normally expects to see for "monitor state"
--   (lease_*, next_check_at, last_checked_at, consecutive_failures,
--    last_error, enabled, last_success_at) are ALREADY in category_channels
--   via migrations 106 (PR 2 monitoring state) and 107 (PR 3 scheduling
--   state). This migration is the lock-in step:
--
--     1. (DIAG) Adds an idempotent PRAGMA-anchored inventory view that the
--        CI lint (scripts/ci-architectural-checks.sh::Check 36) reads to
--        assert the column set. Future drift triggers a gate failure.
--     2. (IDX) Adds the SUPPORTING INDEXES needed by the ClaimDue scheduler
--        and the lease-reclaimer goroutine. CREATE INDEX IF NOT EXISTS is
--        SQLite-native idempotent; safe to replay.
--     3. (FW) Adds 2 forward-looking bookkeeping columns for the multi-source
--        monitor expansion (kind discriminator + handler pin). Defaults are
--        safe for legacy rows.
--
-- Idempotency:
--   * All CREATE INDEX use `IF NOT EXISTS` → idempotent.
--   * All CREATE VIEW use `IF NOT EXISTS` → idempotent.
--   * The two ALTER TABLE ADD COLUMNs are NOT idempotent — SQLite has no
--     `ADD COLUMN IF NOT EXISTS`. The canonical protection is the
--     schema_migrations ledger (INT version PK). The runner skips a file
--     whose version row is already present. The manual one-shot apply
--     path documented below uses the same ledger.
--
-- Anti-pattern note (do NOT repeat):
--   An earlier draft of this migration attempted to log intent via
--   `INSERT INTO schema_migrations(version) SELECT 'literal_string'
--   WHERE NOT EXISTS (...)`. That raised `datatype mismatch (20)` on
--   apply because schema_migrations.version is INT PRIMARY KEY. The
--   literal was a TEXT string that cannot be auto-cast to INT. Recorded
--   here so future agents do not repeat the mistake.
--
-- Migration tracking:
--   File is numbered 108 to follow the existing 001..107 sequence. The
--   runner (`internal/platform/sqlite/migrations.go`) indexes it
--   via the schema_migrations ledger on next boot. For the MANUAL
--   one-shot apply below, the operator MUST ALSO INSERT a row with the
--   file's SHA256 checksum into schema_migrations AFTER the apply so
--   the runner does not double-apply the file. Use this exact pattern:
--
--     CHECKSUM=$(sha256sum migrations/sqlite/108_consolidate_monitored_sources_into_category_channels.sql | awk '{print $1}')
--     sqlite3 data/media/media.db.sqlite "INSERT INTO schema_migrations(version, filename, checksum, applied_at) VALUES (108, '108_consolidate_monitored_sources_into_category_channels.sql', '${CHECKSUM}', datetime('now'));"
--
--   A re-run is safe ONLY after this row exists: the operator must
--   `SELECT 1 FROM schema_migrations WHERE version=108` and bail out
--   if the row exists, otherwise DELETE the ledger row FIRST and then
--   reapply. The CREATE VIEW + INDEX statements are idempotent so a
--   blind re-run only risks tripping the ALTER ADD COLUMN errors. Idempotent
--   re-application after the partial-apply bug of June 2026 is the reason
--   for the explicit "delete-LEDGER-first" sequence.
--
-- Policy references:
--   - ARCHITECTURE.md §6 (category_channels single source of truth)
--   - AGENTS.md Migration Status (PR 2 + PR 3 June 2026 done)
--   - architecture/current.yaml::id-24 CONFORMANCE-001 (Wave tracker)
--   - godlike/07 §Zero-legacy-policy (deprecation record required for
--     monitored_sources residual)
-- =============================================================================

-- ---------------------------------------------------------------------------
-- (DIAG) Inventory view — CI Check 36 reads from this on every lint run.
-- Lint failure: if any "required" column disappears from the view, the gate
-- emits `Check 36: category_channels dropped monitor-state column X` and
-- exits 1.
-- ---------------------------------------------------------------------------
CREATE VIEW IF NOT EXISTS v_category_channels_monitor_state_inventory AS
SELECT
    -- lease columns (PR 3)
    (SELECT 1 FROM pragma_table_info('category_channels') WHERE name='lease_owner')        AS has_lease_owner,
    (SELECT 1 FROM pragma_table_info('category_channels') WHERE name='lease_until')        AS has_lease_until,
    -- scheduling columns (PR 2)
    (SELECT 1 FROM pragma_table_info('category_channels') WHERE name='enabled')            AS has_enabled,
    (SELECT 1 FROM pragma_table_info('category_channels') WHERE name='next_check_at')     AS has_next_check_at,
    (SELECT 1 FROM pragma_table_info('category_channels') WHERE name='last_checked_at')    AS has_last_checked_at,
    (SELECT 1 FROM pragma_table_info('category_channels') WHERE name='last_success_at')    AS has_last_success_at,
    -- failure tracking (PR 3)
    (SELECT 1 FROM pragma_table_info('category_channels') WHERE name='consecutive_failures') AS has_consecutive_failures,
    (SELECT 1 FROM pragma_table_info('category_channels') WHERE name='last_error')         AS has_last_error,
    -- baseline config (015 + 017 + 019)
    (SELECT 1 FROM pragma_table_info('category_channels') WHERE name='check_interval')     AS has_check_interval,
    (SELECT 1 FROM pragma_table_info('category_channels') WHERE name='max_videos_per_run') AS has_max_videos_per_run,
    (SELECT 1 FROM pragma_table_info('category_channels') WHERE name='priority')           AS has_priority,
    (SELECT 1 FROM pragma_table_info('category_channels') WHERE name='lookback_days')      AS has_lookback_days;

-- ---------------------------------------------------------------------------
-- (IDX) Supporting indexes for ClaimDue scheduler + lease reclaimer.
-- Use `IF NOT EXISTS` — these will be no-ops on databases that already have
-- them; safe to apply on production snapshots without a lock window.
-- ---------------------------------------------------------------------------

-- ClaimDue scan: WHERE enabled=1 AND (next_check_at IS NULL OR next_check_at <= ?)
CREATE INDEX IF NOT EXISTS idx_category_channels_monitor_due
    ON category_channels(enabled, next_check_at)
    WHERE enabled = 1;

-- Lease reclaimer scan: WHERE lease_until IS NOT NULL AND lease_until <= ?
CREATE INDEX IF NOT EXISTS idx_category_channels_lease_reclaim
    ON category_channels(lease_until)
    WHERE lease_until IS NOT NULL;

-- Failure backoff computation: WHERE consecutive_failures > 0
CREATE INDEX IF NOT EXISTS idx_category_channels_consecutive_failures
    ON category_channels(consecutive_failures)
    WHERE consecutive_failures > 0;

-- Last-success progress reporting: ORDER BY last_success_at DESC
CREATE INDEX IF NOT EXISTS idx_category_channels_last_success_at
    ON category_channels(last_success_at);

-- ---------------------------------------------------------------------------
-- (FW) Forward-looking bookkeeping columns.
--
-- monitor_source_kind: future multi-source monitor (youtube today; spotify,
--   vimeo later). Default 'youtube' keeps legacy rows valid.
-- monitor_handler_pin: optionally pin a specific server-side handler
--   implementation (CAS-style override). Default '' = canonical handler.
--
-- Idempotency contract:
--   These ALTERs are NOT idempotent at the SQL level (SQLite lacks
--   ADD COLUMN IF NOT EXISTS). Protection is the schema_migrations ledger.
--   An operator re-applying this file MUST first DELETE the ledger row,
--   run `SELECT 1 FROM pragma_table_info('category_channels')` to spot any
--   leftover monitor_* columns, and either DROP them (to revert) or accept
--   the failure and fix forward.
-- ---------------------------------------------------------------------------

ALTER TABLE category_channels ADD COLUMN monitor_source_kind TEXT NOT NULL DEFAULT 'youtube';
ALTER TABLE category_channels ADD COLUMN monitor_handler_pin TEXT NOT NULL DEFAULT '';

-- ---------------------------------------------------------------------------
-- Migration applicability check (annihilates on re-run):
--
--   After this migration, the inventory view
--   v_category_channels_monitor_state_inventory MUST return all `has_*`
--   columns = 1 on a fresh database. Downstream CI gate Check 36 reads
--   this view and exits 0 only when the lock-in is intact.
--
--   If you ALTER this migration later, you MUST append a new migration
--   file (109, 110, ...) — never modify 108 in place. AGENTS.md Migration
--   Status convention.
-- ---------------------------------------------------------------------------
