/**
 * Extracts clip ID from Artlist URL.
 * @param {string} url - Clip URL
 * @returns {string} Clip ID
 */
export function extractClipId(url) {
  const match = String(url || '').match(/\/clip\/[^/]+\/(\d+)/);
  return match ? match[1] : '';
}

/**
 * Normalizes links by removing duplicates and trailing backslashes.
 * @param {string[]} values - Array of URLs
 * @returns {string[]} Normalized URLs
 */
export function normalizeLinks(values) {
  return [...new Set(values.filter(Boolean).map((value) => String(value).trim().replace(/\\+$/, '')))];
}
