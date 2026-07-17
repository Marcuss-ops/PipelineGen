// Per-clip detail page fetcher.
//
// Flat-path facade so consumers can import the helper from
// `artlist/detail-fetcher.js` without reaching into `src/scrape/`.
// Implementation lives in `src/scrape/detail-page.js` — the page-
// opening, m3u8 request interception, video-src extraction, and
// "Just a moment" Cloudflare-block guard are kept in one place there.
//
// Export:
//
//   fetchClipDetails(browser, clipPageUrl) -> Promise<Clip|null>
//
// Returns null when Cloudflare is detected or the navigation
// throws — the caller drops null entries from its accumulator.

export { fetchClipDetails } from '../src/scrape/detail-page.js';
