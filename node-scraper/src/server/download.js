/**
 * Artlist video downloader using Puppeteer/Chromium.
 *
 * Artlist HLS streams (.m3u8) require browser session cookies and headers.
 * FFmpeg cannot download them directly (403 Forbidden), but Chromium can
 * access them because it carries the proper authentication context.
 *
 * This module navigates to the Artlist clip page in Chromium,
 * captures the video stream URL from network requests, then uses
 * Node.js to download the MP4/TS segments with the browser's cookies.
 */

import fs from 'node:fs';
import path from 'node:path';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { importCookies } from '../driver/cookies.js';
import {
  normalizeSegmentConcurrency,
  spoolSegmentsToFile,
} from './segment-spool.js';

/**
 * Downloads a video from Artlist.
 *
 * Opens the clip page in Chromium, interacts with the video player (scroll
 * into view + click play to trigger lazy-loaded HLS stream), captures the
 * stream URL from network requests, then downloads with browser cookies.
 *
 * @param {object} browser - Puppeteer Browser instance
 * @param {string} clipPageUrl - The Artlist clip page URL
 * @param {string} clipId - Clip identifier for filename
 * @param {string} outputDir - Directory to save the downloaded video
 * @returns {Promise<object>} { local_path, file_size, duration_seconds, width, height }
 */
export async function downloadClipVideo(browser, clipPageUrl, clipId, outputDir) {
  if (!browser) {
    throw new Error('Browser instance is required');
  }

  fs.mkdirSync(outputDir, { recursive: true });

  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900 });
  await page.setUserAgent('Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36');

  const cookiePath = process.env.ARTLIST_COOKIE_FILE?.trim() || '';
  await importCookies(page, cookiePath);

  const streamUrls = new Set();
  const mp4Urls = new Set();

  const addCandidateUrl = (url) => {
    if (typeof url !== 'string') {
      return;
    }

    const trimmed = url.trim().replace(/\+$/, '');
    if (!trimmed || trimmed === clipPageUrl) {
      return;
    }

    if (trimmed.includes('.m3u8')) {
      streamUrls.add(trimmed);
      return;
    }

    if (trimmed.includes('.mp4')) {
      mp4Urls.add(trimmed);
    }
  };

  const onRequest = (req) => {
    const url = req.url();
    if (url.includes('.m3u8')) {
      streamUrls.add(url.replace(/\+$/, ''));
    }
    if (url.includes('.mp4') && !url.includes('.m3u8')) {
      mp4Urls.add(url);
    }
  };

  const onResponse = (res) => {
    const url = res.url();
    if (url.includes('.m3u8')) {
      streamUrls.add(url.replace(/\+$/, ''));
    }
    if (url.includes('.mp4') && !url.includes('.m3u8')) {
      mp4Urls.add(url);
    }
  };

  page.on('request', onRequest);
  page.on('response', onResponse);

  try {
    await page.goto(clipPageUrl, { waitUntil: 'networkidle2', timeout: 120000 });
    await page.waitForSelector('video, [class*="player"], [class*="video"]', { timeout: 15000 }).catch(() => {});

    if (mp4Urls.size === 0 && streamUrls.size === 0) {
      await new Promise((resolve) => setTimeout(resolve, 500));
    }

    await page.evaluate(() => {
      const video = document.querySelector('video');
      if (video) {
        video.scrollIntoView({ behavior: 'instant', block: 'center' });
      }
    });
    await new Promise((resolve) => setTimeout(resolve, 500));

    await page.evaluate(() => {
      const video = document.querySelector('video');
      if (video) {
        video.play().catch(() => {
          const player = video.closest('[class*="player"]') || video.parentElement;
          if (player) player.click();
        });
      }
      const playBtn = document.querySelector('[class*="play"], [class*="Play"], [aria-label*="play"], [aria-label*="Play"]');
      if (playBtn) playBtn.click();
    });
    await new Promise((resolve) => setTimeout(resolve, 3000));

    const videoSrc = await page.evaluate(() => {
      const video = document.querySelector('video');
      if (video) {
        return video.src || video.currentSrc || '';
      }
      const source = document.querySelector('source');
      return source ? source.src : '';
    });

    if (videoSrc && !streamUrls.has(videoSrc) && !mp4Urls.has(videoSrc)) {
      addCandidateUrl(videoSrc);
    }

    const cookies = await page.cookies();
    const cookieHeader = cookies.map((cookie) => `${cookie.name}=${cookie.value}`).join('; ');

    let downloadUrl = '';
    let isM3u8 = false;

    if (mp4Urls.size > 0) {
      downloadUrl = Array.from(mp4Urls)[0];
    } else if (streamUrls.size > 0) {
      downloadUrl = Array.from(streamUrls)[0];
      isM3u8 = true;
    }

    if (!downloadUrl) {
      throw new Error(`No video stream URL found for ${clipPageUrl}`);
    }

    console.log(`[download] Found video URL: ${downloadUrl.substring(0, 100)} (HLS: ${isM3u8})`);

    const ext = isM3u8 ? '.ts' : '.mp4';
    const outputPath = path.join(outputDir, `${clipId || 'clip'}${ext}`);

    if (isM3u8) {
      await downloadHLSWithCookies(downloadUrl, cookieHeader, outputPath);
    } else {
      await downloadFileWithCookies(downloadUrl, cookieHeader, outputPath);
    }

    const stats = fs.statSync(outputPath);
    const fileSize = stats.size;

    console.log(`[download] Successfully downloaded to ${outputPath} (${fileSize} bytes)`);

    return {
      local_path: outputPath,
      file_size: fileSize,
      duration_seconds: 0,
      width: 0,
      height: 0,
    };
  } finally {
    await page.close().catch(() => {});
  }
}

/**
 * Downloads an HLS stream by spooling segments to disk concurrently and then
 * concatenating them in playlist order. This keeps memory bounded even when
 * many clips are downloaded in parallel.
 */
async function downloadHLSWithCookies(m3u8Url, cookieHeader, outputPath) {
  console.log(`[download] Downloading HLS stream from ${m3u8Url.substring(0, 80)}...`);

  const masterPlaylist = await fetchWithCookies(m3u8Url, cookieHeader);
  const lines = masterPlaylist.split('\n');
  let selectedPlaylistUrl = null;
  let maxBandwidth = 0;

  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i].trim();
    if (line.startsWith('#EXT-X-STREAM-INF')) {
      const bwMatch = line.match(/BANDWIDTH=(\d+)/);
      const bandwidth = bwMatch ? Number.parseInt(bwMatch[1], 10) : 0;
      const nextLine = lines[i + 1] ? lines[i + 1].trim() : '';
      if (nextLine && !nextLine.startsWith('#') && bandwidth > maxBandwidth) {
        maxBandwidth = bandwidth;
        selectedPlaylistUrl = resolveUrl(m3u8Url, nextLine);
      }
    }
  }

  if (!selectedPlaylistUrl) {
    if (masterPlaylist.includes('#EXTINF')) {
      selectedPlaylistUrl = m3u8Url;
    } else {
      for (const line of lines) {
        const trimmed = line.trim();
        if (trimmed && !trimmed.startsWith('#')) {
          selectedPlaylistUrl = resolveUrl(m3u8Url, trimmed);
          break;
        }
      }
    }
  }

  if (!selectedPlaylistUrl) {
    throw new Error('Could not find media playlist URL in HLS master playlist');
  }

  const mediaPlaylist = await fetchWithCookies(selectedPlaylistUrl, cookieHeader);
  const mediaLines = mediaPlaylist.split('\n');

  let encryptionKey = null;
  let keyUrl = null;
  let iv = null;
  const segmentUrls = [];

  for (let i = 0; i < mediaLines.length; i += 1) {
    const line = mediaLines[i].trim();

    if (line.startsWith('#EXT-X-KEY')) {
      const uriMatch = line.match(/URI="([^"]+)"/);
      const ivMatch = line.match(/IV=0x([0-9A-Fa-f]+)/);
      if (uriMatch) {
        keyUrl = resolveUrl(selectedPlaylistUrl, uriMatch[1]);
      }
      if (ivMatch) {
        iv = ivMatch[1];
      }
    }

    if (line && !line.startsWith('#')) {
      segmentUrls.push(resolveUrl(selectedPlaylistUrl, line));
    }
  }

  if (segmentUrls.length === 0) {
    throw new Error('No video segments found in HLS playlist');
  }

  if (keyUrl) {
    encryptionKey = await fetchWithCookies(keyUrl, cookieHeader, true);
    console.log(`[download] AES-128 encryption detected, key size: ${encryptionKey.length} bytes, IV: ${iv || 'playlist sequence'}`);
  }

  const concurrency = normalizeSegmentConcurrency(
    process.env.ARTLIST_HLS_SEGMENT_CONCURRENCY,
  );
  console.log(`[download] Downloading ${segmentUrls.length} segments with concurrency=${concurrency}...`);

  await spoolSegmentsToFile({
    segmentUrls,
    outputPath,
    concurrency,
    downloadSegment: async (segmentUrl, segmentPath) => {
      await downloadFileWithCookies(segmentUrl, cookieHeader, segmentPath);
    },
  });

  console.log(`[download] All ${segmentUrls.length} segments concatenated to ${outputPath}`);
}

function authenticatedHeaders(cookieHeader) {
  return {
    'Cookie': cookieHeader,
    'User-Agent': 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36',
    'Referer': 'https://artlist.io/',
    'Origin': 'https://artlist.io/',
  };
}

/**
 * Streams a binary response directly to disk. The previous arrayBuffer path
 * retained the whole MP4/segment in memory and multiplied RAM usage by clip
 * concurrency.
 */
async function downloadFileWithCookies(url, cookieHeader, outputPath) {
  const controller = new AbortController();
  const timeoutId = setTimeout(() => controller.abort(), 60000);

  try {
    const response = await fetch(url, {
      signal: controller.signal,
      headers: authenticatedHeaders(cookieHeader),
    });

    if (!response.ok) {
      throw new Error(`HTTP ${response.status} downloading ${url}`);
    }
    if (!response.body) {
      throw new Error(`Empty response body downloading ${url}`);
    }

    await pipeline(
      Readable.fromWeb(response.body),
      fs.createWriteStream(outputPath),
    );
  } catch (error) {
    await fs.promises.rm(outputPath, { force: true }).catch(() => {});
    throw error;
  } finally {
    clearTimeout(timeoutId);
  }
}

/**
 * Fetches a URL with cookies. Returns text by default, or Buffer if asBuffer is true.
 * Has a 60-second timeout per request.
 */
async function fetchWithCookies(url, cookieHeader, asBuffer = false) {
  const controller = new AbortController();
  const timeoutId = setTimeout(() => controller.abort(), 60000);

  try {
    const response = await fetch(url, {
      signal: controller.signal,
      headers: authenticatedHeaders(cookieHeader),
    });

    if (!response.ok) {
      throw new Error(`HTTP ${response.status} fetching ${url.substring(0, 80)}`);
    }

    if (asBuffer) {
      const buffer = await response.arrayBuffer();
      return Buffer.from(buffer);
    }

    return await response.text();
  } finally {
    clearTimeout(timeoutId);
  }
}

function resolveUrl(base, relative) {
  if (relative.startsWith('http://') || relative.startsWith('https://')) {
    return relative;
  }
  const baseUrl = new URL(base);
  return new URL(relative, baseUrl.origin + baseUrl.pathname.substring(0, baseUrl.pathname.lastIndexOf('/') + 1)).href;
}
