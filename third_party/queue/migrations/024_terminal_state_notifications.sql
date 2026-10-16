-- Wake queue replicas on TERMINAL state transitions, not only on claimable
-- work. Migration 016 emits `rendering_jobs` notifications only for
-- pending/rendered (claimable) rows, which is what claim long-polls need. A
-- producer long-polling GET /jobs/{id}/wait blocks on the same Notifier, and
-- that Notifier is local to one replica: without a notification on
-- completed/failed/cancelled, a producer connected to replica B would only
-- observe a completion that happened on replica A after its bounded re-poll.
-- PostgreSQL remains the source of truth: NOTIFY is only an edge signal, every
-- waiter re-reads render_jobs after waking.
CREATE OR REPLACE FUNCTION notify_rendering_jobs() RETURNS trigger AS '
BEGIN
    IF TG_OP = ''INSERT'' THEN
        IF NEW.state IN (''pending'', ''rendered'', ''completed'', ''failed'', ''cancelled'') THEN
            PERFORM pg_notify(''rendering_jobs'', NEW.state);
        END IF;
    ELSIF NEW.state IS DISTINCT FROM OLD.state AND NEW.state IN (''pending'', ''rendered'', ''completed'', ''failed'', ''cancelled'') THEN
        PERFORM pg_notify(''rendering_jobs'', NEW.state);
    END IF;
    RETURN NEW;
END
' LANGUAGE plpgsql;
