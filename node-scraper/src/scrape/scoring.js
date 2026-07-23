/**
 * Normalizes a query string for scoring purposes.
 */
export function normalizeQuery(value) {
  return String(value || '')
    .toLowerCase()
    .normalize('NFKD')
    .replace(/[\u0300-\u036f]/g, '')
    .replace(/[^a-z0-9]+/g, ' ')
    .trim();
}

/**
 * Tokenizes a query into meaningful tokens.
 */
export function tokenizeQuery(value) {
  return normalizeQuery(value)
    .split(/\s+/)
    .map((part) => part.trim())
    .filter((part) => part.length > 2);
}

/**
 * Scores clip relevance based on search term.
 * @param {string} term - Search term
 * @param {object} clip - Clip object
 * @returns {number} Relevance score (0-100)
 */
export function scoreClipRelevance(term, clip) {
  const tokens = tokenizeQuery(term);
  if (tokens.length === 0) return 0;

  const haystack = normalizeQuery([
    clip?.title,
    clip?.description,
    clip?.creator,
    clip?.country,
    clip?.location,
    Array.isArray(clip?.tags) ? clip.tags.join(' ') : '',
    Array.isArray(clip?.categories) ? clip.categories.join(' ') : '',
    clip?.clip_page_url,
    clip?.primary_url,
    clip?.stream_urls?.join(' '),
  ].filter(Boolean).join(' '));

  let hits = 0;
  for (const token of tokens) {
    if (haystack.includes(token)) {
      hits += 1;
    }
  }

  if (hits === 0) return 0;
  if (tokens.length === 1) return hits >= 1 ? 100 : 0;

  const score = Math.round((hits / tokens.length) * 100);
  return score;
}

/**
 * Determines if a clip is relevant based on score threshold.
 * Single-word terms require a full 100% match to avoid substring
 * false-positives (e.g. "sun" matching "sunny"). Multi-word terms
 * accept partial overlap.
 * @param {string} term - Search term
 * @param {object} clip - Clip object
 * @returns {boolean}
 */
export function isRelevantClip(term, clip) {
  const tokens = tokenizeQuery(term);
  return scoreClipRelevance(term, clip) >= (tokens.length > 1 ? 60 : 100);
}
