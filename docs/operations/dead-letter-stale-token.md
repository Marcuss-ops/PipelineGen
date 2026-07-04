# Stale-token Dead-Letter Sweep

**Runbook**: `scripts/admin/dead_letter_stale_token_jobs.sh`
**Wave-tracker**: `architecture/current.yaml#CUTOVER-COMPLETE-WITH-ARTIFACTS.linked_issues[PR-CUTOVER-AZIONE-14-DEAD-LETTER-SCRIPT]`
**godlike/06 SSOT**: single canonical owner of the "stale-token lease handling" fact.

## What it does

Sweeps `jobs` rows whose `lease_token` is set AND `lease_expiry` is `<= CURRENT_TIMESTAMP`
AND a `completion_fingerprint` exists, into terminal status `DEAD_LETTERED`. Each affected
row is concurrently audited in `dead_letter_audit`, with `(job_id, sweep_run)` as the
deduplication key so a second run on the same DB yields zero new audit rows.

## Idempotency guarantee

The script is structurally idempotent:

- `UPDATE` flips rows whose `status IN ('RUNNING', 'LEASED')` — already-`DEAD_LETTERED`
  rows are not re-stamped.
- `INSERT INTO dead_letter_audit SELECT ... WHERE NOT EXISTS (... sweep_run = ?)`
  is the per-sweep dedup; re-running within the same `sweep_run` does nothing.

Operator contract: rerun freely after partial failures; the `sweep_run` timestamp is the
audit key per `geometry/sweep_run` cli flag (`SWEEP_TS` env var).

## Effect of partial failure

The script uses an idempotent UPDATE + audit-log INSERT pair, both wrapped in a single
SQLite transaction (per `BEGIN; ...; COMMIT;` block at script foot). The audit log row is
the canonical recovery surface: incomplete sweeps can be detected via
`SELECT job_id, sweep_run FROM dead_letter_audit ORDER BY sweep_run DESC LIMIT 1`.

## Cron / manual execution

```bash
# Ad-hoc:
bash scripts/admin/dead_letter_stale_token_jobs.sh

# Cron (every 30 minutes):
*/30 * * * *  bash scripts/admin/dead_letter_stale_token_jobs.sh >> /var/log/velox-dlq-$(date +\%F).log 2>&1
```

## Operational runbook (incident-response)

If a downstream consumer reports "missing" `DEAD_LETTERED` rows that the worker should
have surfaced:

1. Run the script in DRY-RUN mode (re-export with `DRY_RUN=1`) — verify count matches
   expected.
2. Re-run without `DRY_RUN=1` — verify `dead_letter_audit` row count increments only for
   the new sweep timestamp, never duplicates.
3. If `dead_letter_audit` shows unexpected `sweep_run` for a job, surface via
   `SELECT job_id, sweep_run, reason FROM dead_letter_audit ORDER BY sweep_run DESC LIMIT 50;`
   and inspect the `reason` column for the sweep-time description.
