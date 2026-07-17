// Search via Artlist API response interception (FAST PATH).
//
// Flat-path facade so consumers can import from `artlist/search-api.js`
// without reaching into `src/scrape/`. Heuristics, JSON traversal,
// and the response listener live upstream in
// `src/scrape/api-interception.js` — kept in one place so the network
// concerns (content-type filtering, depth-limited recursion, dedup by
// id) are not split across files.
//
// Exports:
//
//   setupApiInterception(page, apiResponses)         — attach response listener
//   extractClipsFromApiResponses(responses, term)    — pull clip-like arrays out
//
// Both are async (setupApiInterception returns a synchronous handler
// but the handler awaits page response.json() internally).

export {
  setupApiInterception,
  extractClipsFromApiResponses,
} from '../src/scrape/api-interception.js';
