// Canonical Artlist footage search URL used by search and preview.
// Artlist's documented Footage catalog entry point. Search is performed by
// the catalog search bar, not by assuming a legacy `/search?terms=` route.
const DEFAULT_SEARCH_PATH = '/stock-footage';

export function buildArtlistSearchURL(term, baseURL = 'https://artlist.io') {
  const base = String(baseURL || 'https://artlist.io').replace(/\/+$/, '');
  const configuredPath = String(process.env.ARTLIST_SEARCH_PATH || DEFAULT_SEARCH_PATH).trim();
  const path = configuredPath.startsWith('/') ? configuredPath : `/${configuredPath}`;
  const url = new URL(`${base}${path}`);
  url.searchParams.set('terms', String(term || '').trim());
  return url.toString();
}

export const __testing = { DEFAULT_SEARCH_PATH };
