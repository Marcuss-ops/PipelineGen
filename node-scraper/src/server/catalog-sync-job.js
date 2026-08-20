import { getSearchCache, buildSearchQueryKey } from '../../artlist/search-cache.js';
import { searchArtlistAllPages, searchArtlistIncremental } from '../../artlist/gateway-search.js';

const DEFAULT_MAX_PAGES = 20_000;
const DEFAULT_INCREMENTAL_MAX_PAGES = 2_000;
const DEFAULT_SCHEDULER_POLL_MS = 60_000;
const DEFAULT_INCREMENTAL_INTERVAL_MS = 24 * 60 * 60 * 1000;
const DEFAULT_RECONCILIATION_INTERVAL_MS = 7 * 24 * 60 * 60 * 1000;

export class CatalogSyncJobManager {
  constructor({
    cache = getSearchCache(),
    syncCatalog = searchArtlistAllPages,
    syncIncremental = searchArtlistIncremental,
    logger = console,
    scheduleKey = 'default',
  } = {}) {
    this.cache = cache;
    this.syncCatalog = syncCatalog;
    this.syncIncremental = syncIncremental;
    this.logger = logger;
    this.scheduleKey = scheduleKey;
    this.jobs = new Map();
    this.schedulerTimer = null;
  }

  enqueue({
    query = '',
    filters = {},
    concurrency = 4,
    maxPages = DEFAULT_MAX_PAGES,
    resumeSyncId = '',
    syncType = 'auto',
    scheduleKind = '',
  } = {}) {
    const normalizedResumeId = String(resumeSyncId || '').trim();
    let queryKey;
    let syncId;
    let jobQuery = String(query || '');
    let jobFilters = { sortType: 1, ...filters };
    let resolvedSyncType = syncType === 'incremental'
      ? 'incremental'
      : syncType === 'full_catalog' && String(jobQuery).trim() === ''
        ? 'full_catalog'
        : 'query';

    if (normalizedResumeId) {
      const existing = this.cache.getCatalogSyncBySyncId(normalizedResumeId);
      if (!existing || existing.status !== 'running') {
        const error = new Error(`Cannot enqueue catalog resume ${normalizedResumeId}: running checkpoint not found`);
        error.code = 'ARTLIST_RESUME_NOT_FOUND';
        throw error;
      }
      queryKey = existing.query_key;
      syncId = existing.sync_id;
      jobQuery = existing.query;
      resolvedSyncType = existing.sync_scope === 'incremental'
        ? 'incremental'
        : existing.sync_scope === 'full_catalog'
          ? 'full_catalog'
          : 'query';
      try { jobFilters = JSON.parse(existing.filters_json || '{}'); } catch { jobFilters = { sortType: 1 }; }
    } else {
      queryKey = buildSearchQueryKey({ query: jobQuery, filters: jobFilters });
      const existing = this.cache.getCatalogSync(queryKey);
      if (existing?.run_status === 'running') {
        syncId = existing.sync_id;
        resolvedSyncType = existing.sync_scope === 'incremental'
          ? 'incremental'
          : existing.sync_scope === 'full_catalog'
            ? 'full_catalog'
            : 'query';
        try { jobFilters = JSON.parse(existing.filters_json || '{}'); } catch { /* keep request filters */ }
      } else {
        queryKey = this.cache.startCatalogSync({
          query: jobQuery,
          filters: jobFilters,
          providerSortType: jobFilters.sortType,
          providerTotalAuthoritative: resolvedSyncType !== 'incremental' && jobFilters.sortType === 1,
          syncScope: resolvedSyncType === 'query' ? 'auto' : resolvedSyncType,
        });
        syncId = this.cache.getCatalogSync(queryKey)?.sync_id || '';
      }
    }

    if (!syncId) {
      const error = new Error('Catalog sync could not create a sync_id');
      error.code = 'ARTLIST_SYNC_ID_MISSING';
      throw error;
    }

    if (this.jobs.has(syncId)) return this.getStatus(syncId);

    const job = {
      syncId,
      queryKey,
      query: jobQuery,
      filters: { ...jobFilters },
      concurrency,
      maxPages: resolvedSyncType === 'incremental'
        ? Math.min(maxPages, DEFAULT_INCREMENTAL_MAX_PAGES)
        : maxPages,
      syncType: resolvedSyncType,
      scheduleKind,
    };
    this.jobs.set(syncId, job);
    setImmediate(() => {
      this.run(job).catch((error) => {
        this.logger?.error?.('artlist catalog worker crashed', error);
      });
    });
    return this.getStatus(syncId);
  }

  async run(job) {
    let failure = null;
    try {
      const sync = job.syncType === 'incremental' ? this.syncIncremental : this.syncCatalog;
      await sync({
        query: job.query,
        filters: job.filters,
        concurrency: job.concurrency,
        maxPages: job.maxPages,
        resumeSyncId: job.syncId,
        searchCache: this.cache,
        logger: this.logger,
      });
    } catch (error) {
      failure = error;
      const status = this.cache.getCatalogSyncBySyncId(job.syncId);
      if (status?.status === 'running') {
        this.cache.failCatalogSync(status.query_key, error);
      }
      this.logger?.error?.('artlist catalog worker failed', {
        sync_id: job.syncId,
        code: error?.code,
        message: error?.message || String(error),
      });
    } finally {
      if (job.scheduleKind) {
        this.cache.recordCatalogSyncScheduleRun({
          scheduleKey: this.scheduleKey,
          kind: job.scheduleKind,
          syncId: job.syncId,
          error: failure,
        });
      }
      this.jobs.delete(job.syncId);
    }
  }

  getStatus(syncId) {
    return this.cache.getCatalogSyncBySyncId(syncId);
  }

  getSchedule() {
    return this.cache.getCatalogSyncSchedule(this.scheduleKey);
  }

  configureScheduler({
    incrementalIntervalMs = DEFAULT_INCREMENTAL_INTERVAL_MS,
    reconciliationIntervalMs = DEFAULT_RECONCILIATION_INTERVAL_MS,
    now = Date.now(),
  } = {}) {
    return this.cache.configureCatalogSyncSchedule({
      scheduleKey: this.scheduleKey,
      incrementalIntervalMs,
      reconciliationIntervalMs,
      now,
    });
  }

  startScheduler({
    pollIntervalMs = DEFAULT_SCHEDULER_POLL_MS,
    incrementalIntervalMs = DEFAULT_INCREMENTAL_INTERVAL_MS,
    reconciliationIntervalMs = DEFAULT_RECONCILIATION_INTERVAL_MS,
  } = {}) {
    this.configureScheduler({ incrementalIntervalMs, reconciliationIntervalMs });
    if (this.schedulerTimer) return this.cache.getCatalogSyncSchedule(this.scheduleKey);
    this.schedulerTimer = setInterval(() => {
      this.pollScheduler().catch((error) => {
        this.logger?.error?.('artlist catalog scheduler failed', error);
      });
    }, Math.max(1_000, Number(pollIntervalMs) || DEFAULT_SCHEDULER_POLL_MS));
    this.schedulerTimer.unref?.();
    void this.pollScheduler();
    return this.cache.getCatalogSyncSchedule(this.scheduleKey);
  }

  stopScheduler() {
    if (!this.schedulerTimer) return;
    clearInterval(this.schedulerTimer);
    this.schedulerTimer = null;
  }

  async pollScheduler({ now = Date.now() } = {}) {
    const due = this.cache.claimDueCatalogSyncSchedule({ scheduleKey: this.scheduleKey, now });
    if (due.reconciliation) {
      return this.enqueue({
        query: '',
        filters: { sortType: 1 },
        maxPages: DEFAULT_MAX_PAGES,
        syncType: 'full_catalog',
        scheduleKind: 'reconciliation',
      });
    }
    if (due.incremental) {
      // Do not let an empty catalog turn the incremental pass into an
      // accidental bounded full crawl. The first full reconciliation seeds
      // the known-clip boundary.
      if (!this.cache.hasCompleteFullCatalog({ filters: { sortType: 1 } })) return null;
      return this.enqueue({
        query: '',
        filters: { sortType: 1 },
        maxPages: DEFAULT_INCREMENTAL_MAX_PAGES,
        syncType: 'incremental',
        scheduleKind: 'incremental',
      });
    }
    return null;
  }
}

export function createCatalogSyncJobManager(options) {
  return new CatalogSyncJobManager(options);
}
