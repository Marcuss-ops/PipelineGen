// Stream URL detection and candidate collection for Artlist detail pages.
// exported so the unit-test net (detail-page.test.js) can probe the
// happy-path matcher in isolation; otherwise it would be unreachable
// from outside the IIFE-shaped fetchClipDetails function body.

function toString(value) {
  if (value == null) return '';
  return String(value).trim();
}

export function looksLikeStreamUrl(url) {
  const trimmed = toString(url);
  if (!trimmed) return false;
  return (
    /\.m3u8(?:\?|$)/i.test(trimmed) ||
    /\.mp4(?:\?|$)/i.test(trimmed) ||
    /\/(?:manifest|playlist)(?:[./?#]|$)/i.test(trimmed)
  );
}

export function addCandidateStream(streamSet, url, clipPageUrl) {
  if (typeof url !== 'string') return;
  const trimmed = url.trim().replace(/\\+$/, '');
  if (!trimmed || trimmed === clipPageUrl) return;
  if (looksLikeStreamUrl(trimmed)) {
    streamSet.add(trimmed);
  }
}


