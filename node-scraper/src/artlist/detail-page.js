import { normalizeLinks, extractClipId } from './url.js';

/**
 * Fetches details for a single clip from its detail page.
 * @param {object} browser Puppeteer browser instance
 * @param {string} clipPageUrl 
 * @returns {Promise<object|null>}
 */
export async function fetchClipDetails(browser, clipPageUrl) {
  const detailPage = await browser.newPage();
  await detailPage.setViewport({ width: 1440, height: 900 });
  await detailPage.setUserAgent('Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36');

  const streamSet = new Set();
  const capture = (url) => {
    if (typeof url === 'string' && url.includes('.m3u8')) {
      streamSet.add(url.replace(/\\+$/, ''));
    }
  };
  const onRequest = (req) => capture(req.url());
  const onResponse = (res) => capture(res.url());
  detailPage.on('request', onRequest);
  detailPage.on('response', onResponse);

  try {
    await detailPage.goto(clipPageUrl, { waitUntil: 'networkidle2', timeout: 60000 });
    await detailPage.waitForSelector('video, [class*="player"], [class*="video"]', { timeout: 10000 }).catch(() => {});
    await new Promise((resolve) => setTimeout(resolve, 1000));
    const title = await detailPage.title();
    
    if (title.includes('Just a moment')) {
      console.error(`[artlist] Cloudflare block detected for ${clipPageUrl}`);
      return null;
    }

    const html = (await detailPage.evaluate(() => document.documentElement.outerHTML))
      .replace(/\\\//g, '/')
      .replace(/\\u0026/g, '&');
    const streams = normalizeLinks([
      ...streamSet,
      ...((html.match(/https?:\/\/[^"'\\s>]+\.m3u8[^"'\\s>]*/g) || [])),
      ...((html.match(/https?:\/\/[^"'\\s>]+\.mp4[^"'\\s>]*/g) || [])),
      ...((html.match(/https?:\/\/[^"'\\s>]+cdn[^"'\\s>]*/g) || [])),
    ]);
    const videoSrc = await detailPage.evaluate(() => {
      const video = document.querySelector('video');
      return video ? (video.src || video.currentSrc || '') : '';
    });
    if (videoSrc && !streams.includes(videoSrc)) {
      streams.push(videoSrc);
    }
    return {
      title,
      clip_page_url: clipPageUrl,
      stream_urls: streams,
      primary_url: streams[0] || videoSrc || clipPageUrl,
      clip_id: extractClipId(clipPageUrl),
    };
  } catch (e) {
    console.error(`[artlist] failed to fetch detail for ${clipPageUrl}:`, e.message);
    return null;
  } finally {
    await detailPage.close().catch(() => {});
  }
}
