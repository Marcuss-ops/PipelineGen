import { MAX_SEARCH_BODY_BYTES, readBody, rejectIfNotMethod } from './route-utils.js';

function positiveInt(value, fallback, maximum) {
  const parsed = Number.parseInt(String(value ?? ''), 10);
  if (!Number.isFinite(parsed) || parsed <= 0) return fallback;
  return Math.min(parsed, maximum);
}

function statusEnvelope(status) {
  return {
    ok: true,
    provider: 'artlist',
    sync_id: status.sync_id,
    query: status.query,
    normalized_query: status.normalized_query,
    status: status.status,
    sync_scope: status.sync_scope,
    provider_total: status.provider_total,
    pages_expected: status.pages_expected,
    pages_completed: status.pages_completed,
    raw_results: status.raw_results,
    unique_clip_ids: status.unique_clip_ids,
    duplicates: status.duplicates,
    missing: status.missing,
    new_clip_ids: status.new_clip_ids,
    known_clip_ids: status.known_clip_ids,
    known_streak: status.known_streak,
    stopped_on_known: status.stopped_on_known,
    stop_reason: status.stop_reason,
    last_page: status.last_page,
    snapshot_complete: status.snapshot_complete,
    last_complete_at: status.last_complete_at,
    last_complete_sync_id: status.last_complete_sync_id,
    next_refresh_at: status.next_refresh_at,
    started_at: status.started_at,
    updated_at: status.updated_at,
    completed_at: status.completed_at,
    last_error: status.last_error,
  };
}

export async function handleCatalogSync(req, res, ctx) {
  if (rejectIfNotMethod(req, res, 'POST', '/v1/catalog/sync')) return;

  let body;
  try {
    body = await readBody(req, MAX_SEARCH_BODY_BYTES);
  } catch (error) {
    res.writeHead(error.statusCode || 400, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: error.message }));
    return;
  }

  let payload = {};
  if (body.trim()) {
    try {
      payload = JSON.parse(body);
    } catch {
      res.writeHead(400, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ ok: false, error: 'Invalid JSON' }));
      return;
    }
  }

  const query = String(payload.query ?? '').trim();
  const filters = payload.filters && typeof payload.filters === 'object'
    ? { ...payload.filters }
    : {};
  filters.sortType = Number.isFinite(Number(filters.sortType)) ? Number(filters.sortType) : 1;
  const concurrency = positiveInt(payload.concurrency, 4, 8);
  const syncType = payload.sync_type === 'incremental' ? 'incremental' : payload.sync_type === 'full_catalog' ? 'full_catalog' : 'auto';
  const maxPages = positiveInt(payload.maxPages, syncType === 'incremental' ? 2_000 : 20_000, 20_000);
  const knownStreak = positiveInt(payload.known_streak, 50, 5_000);
  const resumeSyncId = String(payload.resume_sync_id ?? payload.sync_id ?? '').trim();
  const startedAt = Date.now();

  try {
    const status = ctx.deps.enqueueArtlistCatalogSync({
      query,
      filters,
      concurrency,
      maxPages,
      ...(syncType !== 'auto' ? { syncType } : {}),
      ...(syncType === 'incremental' ? { knownStreak } : {}),
      ...(resumeSyncId ? { resumeSyncId } : {}),
    });
    res.writeHead(202, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      ...statusEnvelope(status),
      _meta: {
        elapsed_ms: Date.now() - startedAt,
        initial_sync: query === '' && filters.sortType === 1 && syncType !== 'incremental' && !resumeSyncId,
        incremental: syncType === 'incremental',
        known_streak: syncType === 'incremental' ? knownStreak : null,
        resumed: Boolean(resumeSyncId),
        ...(resumeSyncId ? { resume_sync_id: resumeSyncId } : {}),
      },
    }));
  } catch (error) {
    const status = error?.code === 'ARTLIST_RESUME_NOT_FOUND'
      || error?.code === 'ARTLIST_RESUME_SCOPE_MISMATCH'
      || error?.code === 'ARTLIST_RESUME_STALE'
      ? 400
      : 500;
    res.writeHead(status, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({
      ok: false,
      error: error?.code || 'ARTLIST_CATALOG_SYNC_ENQUEUE_FAILED',
      detail: error?.message || String(error),
      sync_id: error?.syncId ?? null,
      _meta: { elapsed_ms: Date.now() - startedAt },
    }));
  }
}

export function handleCatalogSyncSchedule(req, res, ctx) {
  if (rejectIfNotMethod(req, res, 'GET', '/v1/catalog/schedule')) return;
  const schedule = ctx.deps.getArtlistCatalogSyncSchedule();
  if (!schedule) {
    res.writeHead(404, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'ARTLIST_SCHEDULE_NOT_CONFIGURED' }));
    return;
  }
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify({ ok: true, provider: 'artlist', ...schedule }));
}

export function handleCatalogSyncStatus(req, res, ctx, syncId) {
  if (rejectIfNotMethod(req, res, 'GET', `/v1/catalog/sync/${syncId}`)) return;
  const status = ctx.deps.getArtlistCatalogSyncStatus(syncId);
  if (!status) {
    res.writeHead(404, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: 'ARTLIST_SYNC_NOT_FOUND', sync_id: syncId }));
    return;
  }
  res.writeHead(200, { 'Content-Type': 'application/json' });
  res.end(JSON.stringify(statusEnvelope(status)));
}
