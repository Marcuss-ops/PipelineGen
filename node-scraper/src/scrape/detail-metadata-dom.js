export function extractFromDom(documentOrPageUrl, maybeClipPageUrl) {
  const runningInPage = typeof documentOrPageUrl === 'string';
  const document = runningInPage ? globalThis.document : documentOrPageUrl;
  const clipPageUrl = runningInPage ? documentOrPageUrl : maybeClipPageUrl;
  if (!document) return {};

  // This helper is intentionally local: extractFromDom is serialized and
  // executed through Puppeteer's page.evaluate(), which cannot carry module
  // imports or closures into the browser context.
  const toStringArray = (value) => {
    if (value == null || value === '') return [];
    if (typeof value === 'string') return value.split(',').map((s) => s.trim()).filter(Boolean);
    if (Array.isArray(value)) {
      return value
        .filter((v) => v != null && v !== '')
        .map((v) => (typeof v === 'string' ? v.trim() : String(v).trim()))
        .filter(Boolean);
    }
    return [];
  };

  const meta = (name) => {
    const el = document.querySelector(`meta[property="${name}"], meta[name="${name}"]`);
    return el ? el.getAttribute('content') || '' : '';
  };
  const queryText = (selectors) => {
    for (const selector of selectors) {
      const el = document.querySelector(selector);
      if (el && el.textContent) return el.textContent.trim();
    }
    return '';
  };
  const queryMany = (selectors) => {
    const out = [];
    for (const selector of selectors) {
      try {
        const nodes = document.querySelectorAll(selector);
        for (const node of nodes) {
          const text = node.textContent?.trim();
          if (text) out.push(text);
        }
      } catch {
        // Ignore invalid selectors.
      }
    }
    return out;
  };

  const title = document.title?.trim() || queryText(['h1', '[data-testid="clip-title"]', '.clip-title']) || '';
  const description = meta('og:description') || meta('description') || queryText(['[data-testid="clip-description"]', '.clip-description']) || '';
  const creator = queryText([
    '[data-testid="creator-name"]',
    '[data-testid="artist-name"]',
    '.creator-name',
    '.artist-name',
    '[itemprop="author"]',
  ]);
  const country = queryText(['[data-testid="country"]', '.country', '[itemprop="location"]']);
  const location = queryText(['[data-testid="location"]', '.location', '[itemprop="place"]']) || country;
  const tags = toStringArray(queryMany([
    '[data-testid="clip-tag"], [data-testid="tag"], .clip-tag, a[href*="/tag/"], a[href*="/stock-footage/tag/"]',
  ]));
  const categories = toStringArray(queryMany([
    '[data-testid="clip-category"], .clip-category, a[href*="/category/"], a[href*="/stock-footage/category/"]',
  ]));

  return {
    title,
    description,
    creator,
    country,
    location,
    tags,
    categories,
    thumbnail_url: meta('og:image') || meta('twitter:image') || '',
    preview_url: meta('og:video') || meta('twitter:player') || '',
    clip_page_url: clipPageUrl,
  };
}
