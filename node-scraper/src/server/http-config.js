export const PORT = parseInt(process.env.ARTLIST_SCRAPER_PORT || '9123', 10);
export const BIND = process.env.ARTLIST_SCRAPER_BIND || '0.0.0.0';
export const PROFILE_DIR = process.env.CHROME_PROFILE_DIR || '';
export const DEFAULT_LIMIT = 8;
export const MAX_LIMIT = 50;
const SEARCH_TIMEOUT_SECONDS = Number.parseInt(process.env.SCROLL_TIMEOUT || '120', 10);
export const SEARCH_TIMEOUT_MS = (Number.isFinite(SEARCH_TIMEOUT_SECONDS) && SEARCH_TIMEOUT_SECONDS > 0
  ? SEARCH_TIMEOUT_SECONDS
  : 120) * 1000;
export const HB_INTERVAL_MS = 30_000;
export const HB_FRESH_WINDOW_MS = 60_000;
