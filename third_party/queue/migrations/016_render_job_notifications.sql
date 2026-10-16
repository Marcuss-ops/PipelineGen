-- Wake queue replicas immediately when work becomes claimable. PostgreSQL is
-- still the source of truth: NOTIFY is only an edge signal; workers perform
-- the normal atomic SELECT ... FOR UPDATE SKIP LOCKED claim after waking.
CREATE OR REPLACE FUNCTION notify_rendering_jobs() RETURNS trigger AS '
BEGIN
    IF TG_OP = ''INSERT'' THEN
        IF NEW.state IN (''pending'', ''rendered'') THEN
            PERFORM pg_notify(''rendering_jobs'', NEW.state);
        END IF;
    ELSIF NEW.state IS DISTINCT FROM OLD.state AND NEW.state IN (''pending'', ''rendered'') THEN
        PERFORM pg_notify(''rendering_jobs'', NEW.state);
    END IF;
    RETURN NEW;
END
' LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS render_jobs_notify_claimable ON render_jobs;
CREATE TRIGGER render_jobs_notify_claimable
AFTER INSERT OR UPDATE OF state ON render_jobs
FOR EACH ROW EXECUTE FUNCTION notify_rendering_jobs();
