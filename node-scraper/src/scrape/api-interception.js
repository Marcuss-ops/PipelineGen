/**
 * Artlist API Response Interception Module
 *
 * Provides utilities for intercepting Artlist's GraphQL/XHR API responses
 * during a search page visit to extract clip preview URLs directly,
 * eliminating the need to open individual clip detail pages.
 *
 * The heuristics search for arrays of clip-like objects in any JSON
 * response from Artlist's API endpoints. Clip objects are identified
 * by having id, title, and/or URL fields.
 *
 * NOTE: Based on testing (June 2026), Artlist does NOT expose preview/stream
 * URLs in search page API responses. Clip metadata (IDs, titles) may be
 * found in tracking/analytics payloads, but actual stream URLs are only
 * available on individual clip detail pages. The interception serves as a
 * mild optimization to pre-populate clip page URLs for the fallback path.
 */

/**
 * Recursively searches for clip data in intercepted API response JSON.
 * Handles GraphQL responses (which nest data under `data` key),
 * REST API responses, and any JSON structure containing clip arrays.
 *
 * @param {Array<{url: string, data: any}>} apiResponses - Intercepted API responses
 * @param {string} term - Original search term (used as fallback title)
 * @returns {Array<{clip_id, id, title, name, primary_url, stream_urls, clip_page_url}>}
 */
export function extractClipsFromApiResponses(apiResponses, term) {
  const clips = [];
  const seenIds = new Set();

  for (const { url, data } of apiResponses) {
    if (!data || typeof data !== 'object') continue;

    const candidates = [];

    function findClips(obj, depth = 0) {
      if (depth > 5 || !obj || typeof obj !== 'object') return;

      if (Array.isArray(obj)) {
        if (obj.length > 0) {
          const first = obj[0];
          if (first && typeof first === 'object') {
            const keys = Object.keys(first);
            // Heuristic: clip-like objects have id, title, and/or URL fields
            const hasId = keys.some(k => /^(id|clipId|_id)$/i.test(k));
            const hasTitle = keys.some(k => /^(title|name|caption)$/i.test(k));
            const hasUrl = keys.some(k => /^(url|previewUrl|streamUrl|downloadUrl|src)$/i.test(k));
            if (hasId || (hasTitle && hasUrl)) {
              candidates.push(obj);
            }
          }
        }
        for (const item of obj) findClips(item, depth + 1);
      } else {
        for (const val of Object.values(obj)) findClips(val, depth + 1);
      }
    }

    findClips(data);

    for (const clipArray of candidates) {
      for (const item of clipArray) {
        const itemId = item.id || item.clipId || item._id || '';
        const itemTitle = item.title || item.name || item.caption || '';
        const itemUrl = item.previewUrl || item.streamUrl || item.downloadUrl || item.url || item.src || '';
        const clipPageUrl = item.clipPageUrl || item.pageUrl || item.permalink || item.link || '';

        if (!itemId && !itemUrl) continue;
        const key = itemId || itemUrl;
        if (seenIds.has(key)) continue;
        seenIds.add(key);

        const tags = Array.isArray(item.tags) ? item.tags.filter((t) => t && typeof t === 'string') : [];
        const categories = Array.isArray(item.categories)
          ? item.categories.filter((c) => c && typeof c === 'string')
          : [];
        const creator =
          item.creator || item.author || item.artist || item.contributor || '';
        const description = item.description || '';
        const thumbnailUrl = item.thumbnailUrl || item.thumbnail_url || item.image || '';
        const previewUrl = item.previewUrl || item.preview_url || item.video || '';

        const isMediaURL = (value) =>
          /\.(?:m3u8|mp4)(?:[?#]|$)|\/(?:manifest|playlist)(?:[/?#]|$)/i.test(String(value || ''));
        // When Artlist supplies an explicit clip page, only known media URLs
        // may become streams. Legacy/API fixtures without a page URL still
        // use the generic `url` field as their stream reference.
        const mediaURL = (isMediaURL(itemUrl) || !clipPageUrl) ? String(itemUrl) : '';
        const pageURL = String(clipPageUrl || (!mediaURL && itemUrl ? itemUrl : ''));

        clips.push({
          clip_id: String(itemId),
          id: String(itemId),
          title: String(itemTitle || term),
          name: String(itemTitle || term),
          description: String(description),
          creator: String(creator),
          tags,
          categories,
          primary_url: mediaURL || pageURL,
          stream_urls: mediaURL ? [mediaURL] : [],
          clip_page_url: pageURL,
          thumbnail_url: String(thumbnailUrl),
          preview_url: String(isMediaURL(previewUrl) ? previewUrl : mediaURL),
        });

        if (clips.length >= 50) break;
      }
      if (clips.length >= 50) break;
    }
    if (clips.length >= 50) break;
  }

  return clips;
}

/**
 * Sets up response interception on a Puppeteer page to capture Artlist API
 * responses. Filters for JSON content from artlist domains or common API
 * patterns (/graphql, /api/).
 *
 * @param {import('puppeteer-core').Page} page - Puppeteer page instance
 * @param {Array<{url: string, data: any}>} apiResponses - Array to collect responses into
 * @returns {Function} Handler function to pass to page.on / page.removeListener
 */
export function setupApiInterception(page, apiResponses) {
  const handler = async (response) => {
    const respUrl = response.url();
    try {
      const contentType = response.headers()['content-type'] || '';
      if (
        (contentType.includes('json') || contentType.includes('text/plain')) &&
        (respUrl.includes('artlist') || respUrl.includes('/graphql') || respUrl.includes('/api/'))
      ) {
        try {
          const json = await response.json();
          apiResponses.push({ url: respUrl, data: json });
        } catch {
          // Non-JSON responses are expected
        }
      }
    } catch {
      // Header access errors for redirect responses
    }
  };

  page.on('response', handler);
  return handler;
}
