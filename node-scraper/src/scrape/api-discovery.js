const SENSITIVE_KEYS = new Set([
  'authorization',
  'cookie',
  'set-cookie',
  'x-csrf-token',
]);

function sanitizeHeaders(headers = {}) {
  return Object.fromEntries(
    Object.entries(headers).filter(
      ([key]) => !SENSITIVE_KEYS.has(String(key).toLowerCase()),
    ),
  );
}

export function startApiDiscovery(page, logger = console) {
  const records = [];

  const handler = (request) => {
    const type = request.resourceType();
    if (type !== 'xhr' && type !== 'fetch') {
      return;
    }

    const url = request.url();
    if (!url.includes('artlist')) {
      return;
    }

    const record = {
      timestamp: new Date().toISOString(),
      url,
      method: request.method(),
      resource_type: type,
      post_data: request.postData() || null,
      headers: sanitizeHeaders(request.headers()),
    };

    records.push(record);

    if (logger && typeof logger.log === 'function') {
      logger.log(`[api-discovery] ${record.method} ${record.url}`);
    }
  };

  page.on('request', handler);

  return {
    records,

    stop() {
      if (typeof page.off === 'function') {
        page.off('request', handler);
      } else if (typeof page.removeListener === 'function') {
        page.removeListener('request', handler);
      }
      return [...records];
    },
  };
}
