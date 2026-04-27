#!/usr/bin/env node

import path from 'node:path';
import { fileURLToPath } from 'node:url';

import {
  categories,
  closeDB,
  searchTerms,
  videoLinks,
} from '../../src/node-scraper/src/db.js';
import {
  isRelevantClip,
  searchArtlistPreview,
} from '../../src/node-scraper/artlist_search.js';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

const DEFAULT_TARGET = 10;
const DEFAULT_SEARCH_LIMIT = 25;

const STOP_TERMS = new Set([
  'a', 'an', 'and', 'are', 'as', 'at', 'be', 'by', 'con', 'da', 'del', 'di',
  'e', 'for', 'from', 'il', 'in', 'la', 'le', 'of', 'on', 'or', 'per', 'the',
  'to', 'un', 'una', 'with',
]);

const QUERY_EXPANSIONS = {
  city: ['cityscape', 'urban', 'downtown', 'skyline', 'metropolis'],
  nature: ['forest', 'landscape', 'wildlife', 'mountain', 'outdoor'],
  people: ['person', 'crowd', 'group', 'family', 'friends', 'portrait'],
  technology: ['tech', 'digital', 'computer', 'software', 'data', 'ai'],
  business: ['office', 'startup', 'meeting', 'corporate', 'team'],
  car: ['vehicle', 'driving', 'road', 'automotive', 'traffic'],
  ocean: ['sea', 'water', 'waves', 'beach', 'coast'],
  mountain: ['hiking', 'peak', 'landscape', 'alpine', 'outdoor'],
  spider: ['web', 'arachnid', 'insect', 'nature'],
  tiger: ['wildcat', 'safari', 'jungle', 'big cat', 'lion'],
  lion: ['wildcat', 'safari', 'jungle', 'big cat', 'tiger'],
};

function parseArgs(argv) {
  const args = {
    category: 'Artlist',
    target: DEFAULT_TARGET,
    searchLimit: DEFAULT_SEARCH_LIMIT,
    maxTerms: 0,
    onlyPending: true,
    saveDb: true,
    profileDir: process.env.CHROME_PROFILE_DIR || '/tmp/velox-chrome-profile',
    terms: [],
  };

  for (let i = 2; i < argv.length; i++) {
    const arg = argv[i];
    const next = argv[i + 1];

    if (arg === '--category') {
      args.category = next || args.category;
      i++;
    } else if (arg === '--target') {
      args.target = Number.parseInt(next || String(DEFAULT_TARGET), 10) || DEFAULT_TARGET;
      i++;
    } else if (arg === '--search-limit') {
      args.searchLimit = Number.parseInt(next || String(DEFAULT_SEARCH_LIMIT), 10) || DEFAULT_SEARCH_LIMIT;
      i++;
    } else if (arg === '--max-terms') {
      args.maxTerms = Number.parseInt(next || '0', 10) || 0;
      i++;
    } else if (arg === '--terms') {
      args.terms = String(next || '')
        .split(',')
        .map((term) => term.trim())
        .filter(Boolean);
      i++;
    } else if (arg === '--all-terms') {
      args.onlyPending = false;
    } else if (arg === '--no-save-db') {
      args.saveDb = false;
    } else if (arg === '--profile-dir') {
      args.profileDir = next || args.profileDir;
      i++;
    }
  }

  return args;
}

function normalizeTerm(term) {
  return String(term || '')
    .trim()
    .toLowerCase();
}

function shouldSkipTerm(term) {
  const normalized = normalizeTerm(term);
  if (!normalized) return true;
  if (STOP_TERMS.has(normalized)) return true;
  if (normalized.length < 3) return true;
  return false;
}

function dedupeByPrimaryUrl(clips) {
  const seen = new Set();
  const out = [];
  for (const clip of clips) {
    const key = clip.primary_url || clip.clip_page_url;
    if (!key || seen.has(key)) continue;
    seen.add(key);
    out.push(clip);
  }
  return out;
}

function expandQueries(term) {
  const normalized = normalizeTerm(term);
  const extras = QUERY_EXPANSIONS[normalized] || [];
  return dedupeStrings([normalized, ...extras.map(normalizeTerm)].filter(Boolean));
}

function dedupeStrings(values) {
  return [...new Set(values.map((value) => normalizeTerm(value)).filter(Boolean))];
}

function clipMetadata(clip) {
  return {
    video_id: clip.clip_id || clip.primary_url || clip.clip_page_url || '',
    file_name: clip.title || clip.clip_id || '',
    width: 0,
    height: 0,
    duration: 0,
  };
}

async function populateKeyword(categoryName, term, args) {
  const queries = expandQueries(term);
  const accepted = [];
  const seen = new Set();

  for (const query of queries) {
    if (accepted.length >= args.target) break;

    const search = await searchArtlistPreview(query, args.searchLimit, args.profileDir);
    const relevant = dedupeByPrimaryUrl(
      search.clips.filter((clip) => isRelevantClip(query, clip))
    );

    for (const clip of relevant) {
      const key = clip.primary_url || clip.clip_page_url;
      if (!key || seen.has(key)) continue;
      seen.add(key);
      accepted.push(clip);
      if (accepted.length >= args.target) break;
    }
  }

  let saved = 0;
  if (args.saveDb && accepted.length > 0) {
    categories.add(categoryName, `Imported ${categoryName} footage`);
    searchTerms.addMultiple(categoryName, [term]);
    saved = videoLinks.addMultipleWithSource(
      categoryName,
      term,
      accepted.map((clip) => clip.primary_url),
      'artlist',
      accepted.map(clipMetadata),
    );
  }

  return {
    term,
    search_url: queries[0] ? `https://artlist.io/stock-footage/search?terms=${encodeURIComponent(queries[0])}` : '',
    queries,
    found: accepted.length,
    relevant: accepted.length,
    saved,
    clips: accepted,
  };
}

async function main() {
  const args = parseArgs(process.argv);
  const categoryName = args.category;
  const availableTerms = args.terms.length > 0
    ? args.terms
    : searchTerms.list(categoryName, { onlyPending: args.onlyPending, minVideoCount: args.target });

  const terms = availableTerms
    .map((entry) => (typeof entry === 'string' ? entry : entry.term))
    .map(normalizeTerm)
    .filter((term) => !shouldSkipTerm(term));

  const limitedTerms = args.maxTerms > 0 ? terms.slice(0, args.maxTerms) : terms;

  const results = [];
  for (const term of limitedTerms) {
    // Skip terms that are already fully populated unless the caller forced a custom list.
    const current = searchTerms.list(categoryName, { limit: 1000, minVideoCount: args.target, onlyPending: false }).find((row) => normalizeTerm(row.term) === term);
    if (!args.terms.length && current && Number(current.video_count || 0) >= args.target) {
      continue;
    }

    const result = await populateKeyword(categoryName, term, args);
    results.push(result);
    process.stdout.write(`${term}: ${result.saved}/${args.target} saved, ${result.relevant} relevant\n`);
  }

  const summary = {
    ok: true,
    category: categoryName,
    target_per_keyword: args.target,
    processed: results.length,
    results,
  };

  console.log(JSON.stringify(summary, null, 2));
  closeDB();
}

main().catch((err) => {
  closeDB();
  console.error(JSON.stringify({
    ok: false,
    error: err?.message || String(err),
  }));
  process.exit(1);
});
