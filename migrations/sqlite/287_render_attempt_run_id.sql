-- database: primary
-- Migration 287: explicit run identity on render_attempt_analytics.
--
-- WHY. The per-item analytics rows embed the render fan-out run identity
-- inside `job_id` (`<run_id>:item:<nnn>:<item_id>` — the child plan identity
-- built by render_queue_items.go). Every per-run query therefore had to join
-- with `job_id LIKE '%<run_id>%'` (census "Zone confuse e da bonificare"
-- item 4): fragile by construction — a substring collision bleeds rows from
-- another run — and it defeats plain per-run grouping.
--
-- WHAT. `run_id` is the explicit correlation column: the fan-out run identity
-- the job id embeds, extracted at write time by the analytics builder
-- (RenderRunID in internal/capabilities/scripts/render_attempt_analytics.go,
-- pure projection of the same ids) and backfilled below with the SAME
-- derivation for historical rows. Legacy full-timeline rows carry no run
-- identity in their ids and keep `run_id = ''` — never a fabricated value.
--
-- The backfill mirrors scriptgeneration.RenderRunID EXACTLY (same CASE order):
-- a job id carrying `:item:` yields its prefix; a legacy `run_`-prefixed id IS
-- the run identity and is mirrored verbatim; anything else keeps ''.
--
-- The backfill rewrites no state vocabulary (run_id is a correlation key, not
-- a status/state column), so the state-rewrite gate does not apply.
ALTER TABLE render_attempt_analytics ADD COLUMN run_id TEXT NOT NULL DEFAULT '';

UPDATE render_attempt_analytics
   SET run_id = CASE
        WHEN instr(job_id, ':item:') > 1 THEN substr(job_id, 1, instr(job_id, ':item:') - 1)
        WHEN substr(job_id, 1, 4) = 'run_' THEN job_id
        ELSE run_id
       END
 WHERE run_id = ''
   AND (instr(job_id, ':item:') > 1 OR substr(job_id, 1, 4) = 'run_');

CREATE INDEX IF NOT EXISTS idx_render_attempt_analytics_run
    ON render_attempt_analytics(run_id, recorded_at);
