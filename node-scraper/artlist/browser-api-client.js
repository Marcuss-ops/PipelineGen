import { importCookies, DEFAULT_COOKIE_FILE_PATH } from '../src/driver/cookies.js';

function buildRequestBody(endpoint, term, page, limit, filters) {
  if (endpoint.kind === 'graphql') {
    return {
      operationName: endpoint.operationName || undefined,
      variables: {
        term,
        query: term,
        page,
        limit,
        filters,
        ...filters,
      },
    };
  }

  return {
    query: term,
    term,
    page,
    limit,
    filters,
    ...filters,
  };
}

export class ArtlistBrowserApiClient {
  constructor({ browser, registry, logger = console, cookiePath = process.env.ARTLIST_COOKIE_FILE || DEFAULT_COOKIE_FILE_PATH }) {
    this.browser = browser;
    this.registry = registry;
    this.logger = logger;
    this.cookiePath = cookiePath;
  }

  async searchFootage({ term, page = 1, limit = 24, filters = {} }) {
    const endpoint = this.registry?.footage_search;
    if (!endpoint?.enabled) {
      throw new Error('Artlist footage search endpoint disabled');
    }

    const context = await this.browser.createBrowserContext();
    const browserPage = await context.newPage();

    try {
      await browserPage.setViewport({ width: 1440, height: 900 });
      await browserPage.setUserAgent(
        'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36',
      );

      if (this.cookiePath) {
        await importCookies(browserPage, this.cookiePath);
      }

      const pageResponse = await browserPage.goto(`https://artlist.io/stock-footage/search?terms=${encodeURIComponent(term)}`, {
        waitUntil: 'domcontentloaded',
        timeout: 60_000,
      });

      if (pageResponse?.status?.() === 429) {
        const err = new Error('Artlist returned an anti-bot or rate-limit challenge page');
        err.code = 'ARTLIST_RATE_LIMITED';
        throw err;
      }

      const requestBody = buildRequestBody(endpoint, term, page, limit, filters);
      const result = await browserPage.evaluate(
        async ({ endpointUrl, method, requestBody }) => {
          const init = {
            method,
            credentials: 'include',
            headers: {
              accept: 'application/json',
              'content-type': 'application/json',
            },
          };

          if (method !== 'GET') {
            init.body = JSON.stringify(requestBody);
          }

          const response = await fetch(endpointUrl, init);
          const text = await response.text();
          let data = null;
          try {
            data = JSON.parse(text);
          } catch {
            data = null;
          }
          return {
            status: response.status,
            ok: response.ok,
            contentType: response.headers.get('content-type'),
            data,
            rawText: data ? null : text.slice(0, 2000),
          };
        },
        {
          endpointUrl: endpoint.url,
          method: (endpoint.method || 'POST').toUpperCase(),
          requestBody,
        },
      );

      if (result.status === 401 || result.status === 403) {
        const err = new Error(`Artlist session unavailable: HTTP ${result.status}`);
        err.code = 'SESSION_EXPIRED';
        throw err;
      }

      if (result.status === 429) {
        const err = new Error('Artlist API request was rate limited');
        err.code = 'ARTLIST_RATE_LIMITED';
        err.status = result.status;
        err.body = result.rawText || result.data;
        throw err;
      }

      if (!result.ok) {
        const err = new Error(`Artlist API request failed: HTTP ${result.status}`);
        err.code = 'ARTLIST_API_ERROR';
        err.status = result.status;
        err.body = result.rawText || result.data;
        throw err;
      }

      return result;
    } finally {
      await browserPage.close().catch(() => {});
      await context.close().catch(() => {});
    }
  }
}
