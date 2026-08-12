// Shared request parsing and method/error helpers for HTTP route handlers.

export const MAX_SEARCH_BODY_BYTES = 8192;
export const MAX_DOWNLOAD_BODY_BYTES = 32768;
export const MAX_DISCOVERY_BODY_BYTES = 8192;

export async function readBody(req, maxBytes) {
  let body = '';
  for await (const chunk of req) {
    body += chunk;
    if (body.length > maxBytes) {
      const err = new Error(`Request body exceeds ${maxBytes} bytes`);
      err.statusCode = 413;
      throw err;
    }
  }
  return body;
}

export function rejectIfNotMethod(req, res, allowedMethod, endpointLabel) {
  if (req.method !== allowedMethod) {
    res.writeHead(405, { 'Content-Type': 'application/json' });
    res.end(JSON.stringify({ ok: false, error: `Method not allowed, use ${allowedMethod} ${endpointLabel}` }));
    return true;
  }
  return false;
}

export function isArtlistRateLimitedError(err) {
  return err && err.code === 'ARTLIST_RATE_LIMITED';
}
