import { test, describe } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';

import { createCatalogSyncJobManager } from '../src/server/catalog-sync-job.js';
import { buildSearchQueryKey, createSearchCache } from '../artlist/search-cache.js';

function tempCache(prefix) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), prefix));
  return createSearchCache(path.join(dir, 'cache.sqlite'), 60_000);
}

function waitForWorker() {
  return new Promise((resolve) => setTimeout(resolve, 20));
}

describe('CatalogSyncJobManager', () => {
  test('enqueues immediately and persists worker completion metrics', async () => {
    const cache = tempCache('artlist-job-');
    const calls = [];
    const manager = createCatalogSyncJobManager({
      cache,
      logger: { error() {} },
      syncCatalog: async (input) => {
        calls.push(input);
        const queryKey = buildSearchQueryKey({ query: input.query, filters: input.filters });
        cache.completeCatalogSync(queryKey, {
          providerTotal: 2,
          resultCount: 2,
          rawResults: 2,
          uniqueClipIds: 2,
          pageCount: 1,
        });
      },
    });

    const queued = manager.enqueue({ query: 'electricity meter', filters: { sortType: 1 } });
    assert.equal(queued.status, 'running');
    assert.match(queued.sync_id, /^[0-9a-f-]{36}$/);

    await waitForWorker();
    const final = manager.getStatus(queued.sync_id);
    assert.equal(calls.length, 1);
    assert.equal(calls[0].resumeSyncId, queued.sync_id);
    assert.equal(final.status, 'succeeded');
    assert.equal(final.provider_total, 2);
    assert.equal(final.pages_expected, 1);
    assert.equal(final.unique_clip_ids, 2);
  });

  test('resumes an existing running sync without creating a new sync id', async () => {
    const cache = tempCache('artlist-job-resume-');
    const query = 'electricity meter';
    const filters = { sortType: 1 };
    const queryKey = cache.startCatalogSync({ query, filters });
    const initial = cache.getCatalogSync(queryKey);
    const calls = [];
    const manager = createCatalogSyncJobManager({
      cache,
      logger: { error() {} },
      syncCatalog: async (input) => {
        calls.push(input);
        cache.completeCatalogSync(queryKey, {
          providerTotal: 1,
          resultCount: 1,
          rawResults: 1,
          uniqueClipIds: 1,
          pageCount: 1,
        });
      },
    });

    const queued = manager.enqueue({ resumeSyncId: initial.sync_id });
    assert.equal(queued.sync_id, initial.sync_id);
    await waitForWorker();

    assert.equal(calls.length, 1);
    assert.equal(calls[0].resumeSyncId, initial.sync_id);
    assert.equal(manager.getStatus(initial.sync_id).status, 'succeeded');
  });

  test('schedules an incremental delta when its persisted deadline is due', async () => {
    const cache = tempCache('artlist-job-schedule-');
    const calls = [];
    const now = Date.now();
    cache.configureCatalogSyncSchedule({
      now: now - 120_000,
      incrementalIntervalMs: 60_000,
      reconciliationIntervalMs: 7 * 24 * 60 * 60 * 1000,
    });
    const fullKey = cache.startCatalogSync({ query: '', filters: { sortType: 1 } });
    cache.completeCatalogSync(fullKey, {
      providerTotal: 0,
      resultCount: 0,
      rawResults: 0,
      uniqueClipIds: 0,
      missing: 0,
      pageCount: 1,
    });
    const manager = createCatalogSyncJobManager({
      cache,
      logger: { error() {} },
      syncCatalog: async () => {
        throw new Error('full reconciliation should not run');
      },
      syncIncremental: async (input) => {
        calls.push(input);
        const queryKey = buildSearchQueryKey({ query: input.query, filters: input.filters });
        cache.completeCatalogSync(queryKey, {
          providerTotal: 100,
          resultCount: 2,
          rawResults: 2,
          uniqueClipIds: 2,
          newClipIds: 2,
          knownClipIds: 0,
          missing: 0,
          pageCount: 1,
        });
      },
    });

    const queued = await manager.pollScheduler({ now });
    assert.equal(queued.sync_scope, 'incremental');
    await waitForWorker();
    assert.equal(calls.length, 1);
    assert.equal(calls[0].filters.sortType, 1);
    assert.equal(manager.getStatus(queued.sync_id).status, 'succeeded');
    assert.equal(manager.getSchedule().last_incremental_sync_id, queued.sync_id);
  });

  test('persists worker failures as failed sync status', async () => {
    const cache = tempCache('artlist-job-failure-');
    const manager = createCatalogSyncJobManager({
      cache,
      logger: { error() {} },
      syncCatalog: async () => {
        throw new Error('provider unavailable');
      },
    });

    const queued = manager.enqueue({ query: 'electricity meter', filters: { sortType: 1 } });
    await waitForWorker();

    const failed = manager.getStatus(queued.sync_id);
    assert.equal(failed.status, 'failed');
    assert.match(failed.last_error, /provider unavailable/);
  });
});
