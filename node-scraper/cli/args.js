// CLI argument parser for the artlist-search CLI.
//
// Pure function — no Puppeteer / filesystem dependencies — so the
// module can be loaded and exercised under node --test without
// spinning up a browser or a network.
//
// Recognized flags:
//
//   --term       / -t   <value>   search term (required for the CLI to produce output)
//   --limit      / -l   <int>     max number of clips (default 8)
//   --profile-dir        <path>   Chrome profile dir (default $CHROME_PROFILE_DIR or '')
//
// Behavior:
//   - Unknown flags are ignored (kept permissive for forward-compat).
//   - --limit accepts any int that parses cleanly and is > 0; non-numeric / <=0 falls back to 8.
//   - --profile-dir with no value preserves the previous value (env default).
//
// Public surface:
//
//   parseArgs(argv) -> {term: string, limit: number, profileDir: string}

const DEFAULT_LIMIT = 8;

/**
 * Parses CLI argv into the canonical search-options object.
 * Pure: argv is the only input that affects the output.
 *
 * @param {string[]} argv — typically process.argv
 * @returns {{term: string, limit: number, profileDir: string}}
 */
export function parseArgs(argv) {
  const args = {
    term: '',
    limit: DEFAULT_LIMIT,
    profileDir: process.env.CHROME_PROFILE_DIR || '',
  };

  if (!Array.isArray(argv)) return args;

  for (let i = 2; i < argv.length; i++) {
    const arg = argv[i];
    const next = argv[i + 1];
    if (arg === '--term' || arg === '-t') {
      args.term = next || '';
      i++;
    } else if (arg === '--limit' || arg === '-l') {
      const parsed = Number.parseInt(next || `${DEFAULT_LIMIT}`, 10);
      args.limit = Number.isFinite(parsed) && parsed > 0 ? parsed : DEFAULT_LIMIT;
      i++;
    } else if (arg === '--profile-dir') {
      args.profileDir = next || args.profileDir;
      i++;
    }
    // Unknown flags are intentionally ignored.
  }

  return args;
}

export const __testing = { DEFAULT_LIMIT };
