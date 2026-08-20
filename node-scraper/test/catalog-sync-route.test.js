import { test, describe } from 'node:test';
import assert from 'node:assert/strict';

import { handleCatalogSync, handleCatalogSyncStatus } from '../src/server/routes.js';

function createReq(body = '', method = 'POST') {
  return {
    method,
    [Symbol.asyncIterator]: async function* () {
      if (body) yield Buffer.from(body);
    },
  };
}

function createRes() {
  return {
    statusCode: null,
    headers: null,
    body: null,
    writeHead(status, headers) {
      this.statusCode = status;
      this.headers = headers;
    },
    end(data) {
      this.body = data;
    },
  };
}

function status(syncId = 'sync-123') {
  return {
    sync_id: syncId,
    query: '',
    normalized_query: '',
    status: 'running',
    sync_scope: 'full_catalog',
    provider_total: 0,
    pages_expected: 0,
    pages_completed: 0,
    raw_results: 0,
    unique_clip_ids: 0,
    duplicates: 0,
    missing: 0,
    last_page: 0,
    snapshot_complete: 0,
    last_complete_at: null,
    last_complete_sync_id: null,
    started_at: '2026-08-20T00:00:00.000Z',
    updated_at: '2026-08-20T00:00:00.000Z',
    completed_at: null,
    last_error: '',
  };
}

describe('handleCatalogSync', () => {
  test('enqueues a full catalog sync and returns immediately', async () => {
    let request;
    const res = createRes();
    await handleCatalogSync(createReq(), res, {
      deps: {
        enqueueArtlistCatalogSync: (input) => {
          request = input;
          return status('sync-full-1');
        },
      },
    });

    assert.equal(res.statusCode, 202);
    assert.deepEqual(request, {
      query: '',
      filters: { sortType: 1 },
      concurrency: 4,
      maxPages: 20_000,
    });
    const payload = JSON.parse(res.body);
    assert.equal(payload.sync_id, 'sync-full-1');
    assert.equal(payload.status, 'running');
    assert.equal(payload._meta.initial_sync, true);
    assert.equal(payload._meta.resumed, false);
  });

  test('enqueues a resume request with the supplied sync id', async () => {
    let request;
    const res = createRes();
    await handleCatalogSync(createReq(JSON.stringify({
      query: 'electricity meter',
      filters: { sortType: 1 },
      resume_sync_id: 'sync-123',
    })), res, {
      deps: {
        enqueueArtlistCatalogSync: (input) => {
          request = input;
          return status('sync-123');
        },
      },
    });

    assert.equal(res.statusCode, 202);
    assert.equal(request.resumeSyncId, 'sync-123');
    const payload = JSON.parse(res.body);
    assert.equal(payload._meta.resumed, true);
    assert.equal(payload._meta.resume_sync_id, 'sync-123');
  });

  test('returns persisted sync status and metrics', () => {
    const res = createRes();
    handleCatalogSyncStatus(createReq('', 'GET'), res, {
      deps: { getArtlistCatalogSyncStatus: () => ({
        ...status('sync-status-1'),
        status: 'succeeded',
        provider_total: 104,
        pages_expected: 3,
        pages_completed: 3,
        raw_results: 104,
        unique_clip_ids: 104,
        snapshot_complete: 1,
      }) },
    }, 'sync-status-1');

    assert.equal(res.statusCode, 200);
    const payload = JSON.parse(res.body);
    assert.equal(payload.sync_id, 'sync-status-1');
    assert.equal(payload.status, 'succeeded');
    assert.equal(payload.pages_completed, 3);
    assert.equal(payload.unique_clip_ids, 104);
  });

  test('returns 400 when enqueue rejects an invalid resume checkpoint', async () => {
    const res = createRes();
    await handleCatalogSync(createReq(JSON.stringify({ resume_sync_id: 'missing-sync' })), res, {
      deps: {
        enqueueArtlistCatalogSync: () => {
          const error = new Error('checkpoint not found');
          error.code = 'ARTLIST_RESUME_NOT_FOUND';
          error.syncId = 'missing-sync';
          throw error;
        },
      },
    });

    assert.equal(res.statusCode, 400);
    const payload = JSON.parse(res.body);
    assert.equal(payload.error, 'ARTLIST_RESUME_NOT_FOUND');
    assert.equal(payload.sync_id, 'missing-sync');
  });
});
