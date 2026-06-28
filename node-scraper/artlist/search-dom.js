// Search via DOM scroll (FALLBACK PATH).
//
// Provides two helpers:
//
//   chunkArray(arr, size)                  — pure; safe to unit-test
//   collectClipLinksFromHrefs(page, fn, n) — puppeteer-bound; walks the search
//                                            page in scroll rounds, calls fn
//                                            with each batch of newly-found
//                                            clip page hrefs; the caller is
//                                            responsible for deduping and
//                                            tracking target count.
//
// `chunkArray` is the single most testable helper exposed here — it
// is what powers the "concurrency limit" guarantee inside
// search.js::fetchClipDetailsBatch. Splitting the chunking logic out
// makes the per-tab concurrency limit trivially verifiable under
// `node --test` without needing a real Chromium process.
//
// The puppeteer-bound `collectClipLinksFromHrefs` deliberately keeps
// a small surface: it does not own the dedup `Set`, the `targetCount`
// check, or the early-break condition — those belong to the caller so
// the test that exercises the chunking semantics stays pure.

/**
 * Upper bound on scroll rounds before we give up on the DOM fallback.
 * Legacy value carried over from the original artlist_search.js.
 */
export const MAX_SCROLL_ROUNDS = 8;

/**
 * Splits an array into fixed-size chunks (last chunk may be smaller).
 * Pure function — safe to unit-test without Puppeteer / IO.
 *
 * @template T
 * @param {T[]} arr
 * @param {number} size — chunk size; values <= 0 collapse to a single chunk of arr
 * @returns {T[][]}
 */
export function chunkArray(arr, size) {
  if (!Array.isArray(arr)) return [];
  if (!Number.isFinite(size) || size <= 0) return [arr.slice()];
  const out = [];
  for (let i = 0; i < arr.length; i += size) {
    out.push(arr.slice(i, i + size));
  }
  return out;
}

/**
 * Scrolls the search page up to MAX_SCROLL_ROUNDS times, each round
 * collecting newly-discovered clip page hrefs (via `page.evaluate`)
 * and feeding them to the caller-supplied `onHrefs` callback. The
 * callback is responsible for the dedup `Set` and the early-stop
 * condition; this function only enforces the round cap.
 *
 * @param {import('puppeteer-core').Page} page
 * @param {(hrefs: string[]) => void} onHrefs — dedup accumulator callback
 * @param {number} targetCount — caps the upper bound on round count
 * @returns {Promise<number>} — number of scroll rounds actually performed
 */
export async function collectClipLinksFromHrefs(page, onHrefs, targetCount) {
  const maxRounds = Math.max(
    1,
    Math.min(MAX_SCROLL_ROUNDS, Math.ceil(targetCount / 2) + 1)
  );
  let performed = 0;

  for (let round = 0; round < maxRounds; round++) {
    await new Promise((resolve) => setTimeout(resolve, 300));

    const newlyFound = await page.evaluate(() => {
      const found = [];
      const seenLocal = new Set();
      document
        .querySelectorAll('a[href*="/stock-footage/clip/"]')
        .forEach((el) => {
          const href = el.href || el.getAttribute('href') || '';
          if (!href || seenLocal.has(href)) return;
          seenLocal.add(href);
          found.push(href);
        });
      return found;
    });

    onHrefs(newlyFound);

    await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
    performed = round + 1;
  }

  return performed;
}
