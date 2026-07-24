/**
 * Extracts clip ID from Artlist URL.
 * @param {string} url - Clip URL
 * @returns {string} Clip ID
 */
export function extractClipId(url) {
  const match = String(url || '').match(/\/clip\/(?:[^/]+\/)?(\d+)(?:[/?#]|$)/);
  return match ? match[1] : '';
}

/**
 * Normalizes links by removing duplicates and trailing backslashes.
 *
 * Defensive against non-array input (returns `[]` for null /
 * undefined / strings / numbers, matching `chunkArray`'s contract).
 *
 * @param {string[]} values - Array of URLs (anything else is treated as empty)
 * @returns {string[]} Normalized URLs, preserving first-seen order.
 */
export function normalizeLinks(values) {
  if (!Array.isArray(values)) return [];
  return [...new Set(values.filter(Boolean).map((value) => String(value).trim().replace(/\\+$/, '')))];
}
