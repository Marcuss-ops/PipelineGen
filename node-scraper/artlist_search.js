// Legacy entry-point — preserved as a thin facade for backward compat.
//
// Historical callers:
//
//   - artlist_server.js              (import { searchArtlist })
//   - scripts/ + docker images       (node artlist_search.js --term ...)
//
// import searchArtlist / searchArtlistPreview / extractClipId /
// normalizeLinks / scoreClipRelevance / isRelevantClip. After the
// modularization the canonical implementation lives in:
//
//   - cli/args.js                   — CLI flag parsing
//   - artlist/search.js             — orchestration (FAST PATH + FALLBACK)
//   - artlist/search-api.js         — API response interception
//   - artlist/search-dom.js         — DOM scroll + chunking
//   - artlist/detail-fetcher.js     — per-clip detail page
//   - artlist/browser-session.js    — puppeteer lifecycle
//   - bin/artlist-search.js         — canonical CLI entry
//
// The lower half of this file keeps a 5-line `node artlist_search.js`
// shim so any pre-modularization caller (`node artlist_search.js
// --term foo`) still prints the JSON search result. Canonical
// invocation is `node bin/artlist-search.js` — this shim exists for
// back-compat only.

import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from './cli/args.js';
import { searchArtlist as _searchArtlist } from './artlist/search.js';

export {
  searchArtlist,
  searchArtlistPreview,
} from './artlist/search.js';

// Re-export the pure helpers that artlist_search.js historically
// exposed so legacy callers that imported them directly still work.
export { extractClipId, normalizeLinks } from './src/scrape/url.js';
export {
  scoreClipRelevance,
  isRelevantClip,
} from './src/scrape/scoring.js';

// ─── Legacy CLI shim ──────────────────────────────────────────────────────
// Preserved so legacy invocations `node artlist_search.js --term foo`
// keep printing JSON instead of silently exiting 0. Canonical
// invocation lives in `bin/artlist-search.js`.
const _isMain =
  process.argv[1] &&
  path.resolve(process.argv[1]) === fileURLToPath(import.meta.url);

if (_isMain) {
  const args = parseArgs(process.argv);
  if (!args.term) {
    console.error(JSON.stringify({ ok: false, error: 'missing --term' }));
    process.exit(2);
  }
  _searchArtlist(args.term, args.limit, args.profileDir)
    .then((result) => {
      console.log(
        JSON.stringify(
          {
            ok: true,
            term: result.term,
            search_url: result.search_url,
            saved: 0,
            clips: result.clips,
          },
          null,
          2
        )
      );
    })
    .catch((err) => {
      console.error(
        JSON.stringify({ ok: false, error: err?.message || String(err) })
      );
      process.exit(1);
    });
}
