import {
  DEFAULT_INCREMENTAL_INTERVAL_MS,
  DEFAULT_RECONCILIATION_INTERVAL_MS,
} from './search-cache-util.js';

export const searchCacheScheduleMethods = {
  configureCatalogSyncSchedule({
    scheduleKey = 'default',
    incrementalIntervalMs = DEFAULT_INCREMENTAL_INTERVAL_MS,
    reconciliationIntervalMs = DEFAULT_RECONCILIATION_INTERVAL_MS,
    now = Date.now(),
  } = {}) {
    const key = String(scheduleKey || 'default');
    const current = this.db.prepare(
      'SELECT * FROM artlist_catalog_sync_schedule WHERE schedule_key = ?',
    ).get(key);
    const nowMs = Number(now) || Date.now();
    const incrementalMs = Math.max(60_000, Number(incrementalIntervalMs) || DEFAULT_INCREMENTAL_INTERVAL_MS);
    const reconciliationMs = Math.max(60_000, Number(reconciliationIntervalMs) || DEFAULT_RECONCILIATION_INTERVAL_MS);
    const nowISO = new Date(nowMs).toISOString();
    const nextIncremental = current?.next_incremental_at || new Date(nowMs + incrementalMs).toISOString();
    const nextReconciliation = current?.next_reconciliation_at || new Date(nowMs + reconciliationMs).toISOString();
    this.db.prepare(`
      INSERT INTO artlist_catalog_sync_schedule (
        schedule_key, incremental_interval_ms, reconciliation_interval_ms,
        next_incremental_at, next_reconciliation_at, updated_at
      ) VALUES (@schedule_key, @incremental_interval_ms, @reconciliation_interval_ms,
        @next_incremental_at, @next_reconciliation_at, @updated_at)
      ON CONFLICT(schedule_key) DO UPDATE SET
        incremental_interval_ms = excluded.incremental_interval_ms,
        reconciliation_interval_ms = excluded.reconciliation_interval_ms,
        updated_at = excluded.updated_at
    `).run({
      schedule_key: key,
      incremental_interval_ms: incrementalMs,
      reconciliation_interval_ms: reconciliationMs,
      next_incremental_at: nextIncremental,
      next_reconciliation_at: nextReconciliation,
      updated_at: nowISO,
    });
    return this.getCatalogSyncSchedule(key);
  }

,
  getCatalogSyncSchedule(scheduleKey = 'default') {
    return this.db.prepare(
      'SELECT * FROM artlist_catalog_sync_schedule WHERE schedule_key = ?',
    ).get(String(scheduleKey || 'default')) || null;
  }

,
  claimDueCatalogSyncSchedule({ scheduleKey = 'default', now = Date.now() } = {}) {
    const schedule = this.getCatalogSyncSchedule(scheduleKey) || this.configureCatalogSyncSchedule({ scheduleKey, now });
    const nowMs = Number(now) || Date.now();
    const dueIncremental = Date.parse(schedule.next_incremental_at) <= nowMs;
    const dueReconciliation = Date.parse(schedule.next_reconciliation_at) <= nowMs;
    if (!dueIncremental && !dueReconciliation) return { incremental: false, reconciliation: false, schedule };

    const nowISO = new Date(nowMs).toISOString();
    const nextIncremental = dueReconciliation || dueIncremental
      ? new Date(nowMs + schedule.incremental_interval_ms).toISOString()
      : schedule.next_incremental_at;
    const nextReconciliation = dueReconciliation
      ? new Date(nowMs + schedule.reconciliation_interval_ms).toISOString()
      : schedule.next_reconciliation_at;
    this.db.prepare(`
      UPDATE artlist_catalog_sync_schedule
      SET next_incremental_at = @next_incremental_at,
          next_reconciliation_at = @next_reconciliation_at,
          updated_at = @updated_at,
          last_error = ''
      WHERE schedule_key = @schedule_key
    `).run({
      schedule_key: String(scheduleKey || 'default'),
      next_incremental_at: nextIncremental,
      next_reconciliation_at: nextReconciliation,
      updated_at: nowISO,
    });
    return {
      incremental: dueIncremental && !dueReconciliation,
      reconciliation: dueReconciliation,
      schedule: this.getCatalogSyncSchedule(scheduleKey),
    };
  }

,
  recordCatalogSyncScheduleRun({ scheduleKey = 'default', kind, syncId, error = null, now = Date.now() } = {}) {
    if (kind !== 'incremental' && kind !== 'reconciliation') return;
    const nowISO = new Date(Number(now) || Date.now()).toISOString();
    const column = kind === 'incremental' ? 'incremental' : 'reconciliation';
    this.db.prepare(`
      UPDATE artlist_catalog_sync_schedule
      SET last_${column}_sync_id = @sync_id,
          last_${column}_at = @now,
          last_error = @last_error,
          updated_at = @now
      WHERE schedule_key = @schedule_key
    `).run({
      schedule_key: String(scheduleKey || 'default'),
      sync_id: String(syncId || ''),
      now: nowISO,
      last_error: error ? String(error.message || error).slice(0, 2_000) : '',
    });
  }

,
};
