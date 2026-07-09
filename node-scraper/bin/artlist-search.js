#!/usr/bin/env node

// Canonical CLI entry-point for Artlist search.
//
// Usage:
//   node bin/artlist-search.js --term "cinematic boxing" --limit 8 --profile-dir /tmp/chrome-profile
//   node bin/artlist-search.js -t "cinematic boxing" -l 8
//
// This is the canonical CLI surface — all other entry points
// (artlist_search.js, artlist_server.js's import path) delegate to
// the same search orchestrator at artlist/search.js.
//
// The backward-compat shim at ../artlist_search.js prints the same
// JSON shape when invoked via `node artlist_search.js --term ...`.
//
// Exit codes:
//   0 — success (JSON result printed to stdout)
//   1 — search failed (structured error to stderr)
//   2 — missing required --term flag

import { parseArgs } from '../cli/args.js';
import { searchArtlist } from '../artlist/search.js';

const args = parseArgs(process.argv);

if (!args.term) {
  console.error(JSON.stringify({ ok: false, error: 'missing --term' }));
  process.exit(2);
}

searchArtlist(args.term, args.limit, args.profileDir)
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
