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
import { extractClipId } from '../scrape/url.js';
import { importCookies, DEFAULT_COOKIE_FILE_PATH } from '../driver/cookies.js';
import { fetchClipDetails } from '../scrape/detail-page.js';

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

  // Ensure output directory exists
  fs.mkdirSync(outputDir, { recursive: true });

  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900 });
  await page.setUserAgent('Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36');

  // PR-ARTLIST-COOKIE-IMPORT (July 2026): reuse the same session cookie
  // file so the /download endpoint can also reach authenticated streams.
  const cookiePath = process.env.ARTLIST_COOKIE_FILE || DEFAULT_COOKIE_FILE_PATH;
  await importCookies(page, cookiePath);

  // Capture video stream URLs from network requests
  const streamUrls = new Set();
  const mp4Urls = new Set();

  const addCandidateUrl = (url) => {
    if (typeof url !== 'string') {
      return;
    }

    const trimmed = url.trim().replace(/\\+$/, '');
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

  const addDetailCandidates = (detail) => {
    if (!detail || typeof detail !== 'object') {
      return;
    }

    addCandidateUrl(detail.primary_url);
    addCandidateUrl(detail.preview_url);
    if (Array.isArray(detail.stream_urls)) {
      for (const url of detail.stream_urls) {
        addCandidateUrl(url);
      }
    }
  };

  const onRequest = (req) => {
    const url = req.url();
    if (url.includes('.m3u8')) {
      streamUrls.add(url.replace(/\\+$/, ''));
    }
    if (url.includes('.mp4') && !url.includes('.m3u8')) {
      mp4Urls.add(url);
    }
  };

  const onResponse = (res) => {
    const url = res.url();
    if (url.includes('.m3u8')) {
      streamUrls.add(url.replace(/\\+$/, ''));
    }
    if (url.includes('.mp4') && !url.includes('.m3u8')) {
      mp4Urls.add(url);
    }
  };

  page.on('request', onRequest);
  page.on('response', onResponse);

  try {
    // Navigate to the clip page
    await page.goto(clipPageUrl, { waitUntil: 'networkidle2', timeout: 120000 });
    
    // Wait for video player to appear
    await page.waitForSelector('video, [class*="player"], [class*="video"]', { timeout: 15000 }).catch(() => {});
    
    // ─── Force video player interaction ────────────────────────────────
    // Artlist loads the HLS stream lazily — the video element exists but
    // the .m3u8 URL is only requested when the user scrolls the player
    // into view AND clicks play. Without this interaction, no stream URLs
    // are ever captured and the download fails with "No video stream found".
    //
    // Strategy:
    //   1. Scroll any video element into view so the player initializes
    //   2. Try clicking the play button to trigger HLS stream loading
    //   3. Wait for the network request to appear
    //
    // If the selectors fail (different page layout), we still fall through
    // to the existing URL capture logic (network listeners, DOM extraction).

    // Prefer the structured detail extractor first. It can often see the
    // authenticated stream URLs from API / JSON-LD without needing the
    // player interaction to fire. When it yields a direct asset URL we can
    // download immediately and skip the flaky play-trigger path.
    try {
      const detail = await fetchClipDetails(browser, clipPageUrl);
      addDetailCandidates(detail);
    } catch (err) {
      console.log(`[download] detail probe failed for ${clipPageUrl}: ${err.message}`);
    }

    if (mp4Urls.size === 0 && streamUrls.size === 0) {
      await new Promise((resolve) => setTimeout(resolve, 500));
    }

    await page.evaluate(() => {
      // Scroll the first video element into view
      const video = document.querySelector('video');
      if (video) {
        video.scrollIntoView({ behavior: 'instant', block: 'center' });
      }
    });
    await new Promise((resolve) => setTimeout(resolve, 500));

    await page.evaluate(() => {
      // Try clicking the play button on the video player
      const video = document.querySelector('video');
      if (video) {
        video.play().catch(() => {
          // Autoplay may be blocked — try clicking the player container
          const player = video.closest('[class*="player"]') || video.parentElement;
          if (player) player.click();
        });
      }
      // If no <video> element, try clicking visible play buttons
      const playBtn = document.querySelector('[class*="play"], [class*="Play"], [aria-label*="play"], [aria-label*="Play"]');
      if (playBtn) playBtn.click();
    });
    // Wait for the HLS stream request to be captured by the network listeners
    await new Promise((resolve) => setTimeout(resolve, 3000));

    // Also try to get video source from page context
    const videoSrc = await page.evaluate(() => {
      const video = document.querySelector('video');
      if (video) {
        return video.src || video.currentSrc || '';
      }
      // Try to find any source elements
      const source = document.querySelector('source');
      return source ? source.src : '';
    });

    if (videoSrc && !streamUrls.has(videoSrc) && !mp4Urls.has(videoSrc)) {
      addCandidateUrl(videoSrc);
    }

    // Get cookies from the page for authenticated download
    const cookies = await page.cookies();
    const cookieHeader = cookies.map(c => `${c.name}=${c.value}`).join('; ');

    // Determine the best URL to download
    let downloadUrl = '';
    let isM3u8 = false;

    // Prefer MP4 direct links.
    if (mp4Urls.size > 0) {
      downloadUrl = Array.from(mp4Urls)[0];
      isM3u8 = false;
    } else if (streamUrls.size > 0) {
      downloadUrl = Array.from(streamUrls)[0];
      isM3u8 = true;
    }

    if (!downloadUrl) {
      throw new Error(`No video stream URL found for ${clipPageUrl}`);
    }

    console.log(`[download] Found video URL: ${downloadUrl.substring(0, 100)} (HLS: ${isM3u8})`);

    // Save as .ts for HLS (raw MPEG-TS segments concatenated), FFmpeg will re-encode to proper MP4
    // For direct MP4, save as .mp4
    const ext = isM3u8 ? '.ts' : '.mp4';
    const outputPath = path.join(outputDir, `${clipId || 'clip'}${ext}`);

    if (isM3u8) {
      // For HLS streams: download the .m3u8, parse segments, download each, concatenate
      await downloadHLSWithCookies(downloadUrl, cookieHeader, outputPath);
    } else {
      // Direct MP4: download with cookies
      await downloadFileWithCookies(downloadUrl, cookieHeader, outputPath);
    }

    // Get file info
    const stats = fs.statSync(outputPath);
    const fileSize = stats.size;

    console.log(`[download] Successfully downloaded to ${outputPath} (${fileSize} bytes)`);

    return {
      local_path: outputPath,
      file_size: fileSize,
      duration_seconds: 0,  // FFmpeg will determine this later
      width: 0,
      height: 0,
    };

  } finally {
    // page.close() automatically cleans up all event listeners
    await page.close().catch(() => {});
  }
}

/**
 * Downloads an HLS stream (m3u8) by fetching the playlist, parsing segments,
 * downloading each segment, and concatenating them into a single MP4-like file.
 */
async function downloadHLSWithCookies(m3u8Url, cookieHeader, outputPath) {
  console.log(`[download] Downloading HLS stream from ${m3u8Url.substring(0, 80)}...`);

  // Fetch the master playlist
  const masterPlaylist = await fetchWithCookies(m3u8Url, cookieHeader);
  
  // Parse the master playlist to find the best quality stream
  const lines = masterPlaylist.split('\n');
  let selectedPlaylistUrl = null;
  let maxBandwidth = 0;

  // Look for the highest bandwidth variant
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i].trim();
    if (line.startsWith('#EXT-X-STREAM-INF')) {
      const bwMatch = line.match(/BANDWIDTH=(\d+)/);
      const bandwidth = bwMatch ? parseInt(bwMatch[1], 10) : 0;
      const nextLine = lines[i + 1] ? lines[i + 1].trim() : '';
      if (nextLine && !nextLine.startsWith('#') && bandwidth > maxBandwidth) {
        maxBandwidth = bandwidth;
        selectedPlaylistUrl = resolveUrl(m3u8Url, nextLine);
      }
    }
  }

  // If no variant found, the URL might already be a media playlist
  if (!selectedPlaylistUrl) {
    // Check if this is already a media playlist (has #EXTINF)
    if (masterPlaylist.includes('#EXTINF')) {
      selectedPlaylistUrl = m3u8Url;
    } else {
      // Take the first non-comment, non-empty line as fallback
      for (const l of lines) {
        const trimmed = l.trim();
        if (trimmed && !trimmed.startsWith('#') && !trimmed.startsWith('#')) {
          selectedPlaylistUrl = resolveUrl(m3u8Url, trimmed);
          break;
        }
      }
    }
  }

  if (!selectedPlaylistUrl) {
    throw new Error('Could not find media playlist URL in HLS master playlist');
  }

  // Fetch the media playlist
  const mediaPlaylist = await fetchWithCookies(selectedPlaylistUrl, cookieHeader);
  const mediaLines = mediaPlaylist.split('\n');

  // Parse EXT-X-KEY (if encryption is used - Artlist may use AES-128)
  let encryptionKey = null;
  let keyUrl = null;
  let iv = null;

  // Download all segments
  const segmentUrls = [];
  for (let i = 0; i < mediaLines.length; i++) {
    const line = mediaLines[i].trim();

    // Check for encryption key
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

    // Collect segment URLs
    if (line && !line.startsWith('#')) {
      segmentUrls.push(resolveUrl(selectedPlaylistUrl, line));
    }
  }

  if (segmentUrls.length === 0) {
    throw new Error('No video segments found in HLS playlist');
  }

  // Fetch encryption key if present
  if (keyUrl) {
    encryptionKey = await fetchWithCookies(keyUrl, cookieHeader, true);
    console.log(`[download] AES-128 encryption detected, key size: ${encryptionKey.length} bytes`);
  }

  // Download segments and concatenate
  console.log(`[download] Downloading ${segmentUrls.length} segments...`);
  const writeStream = fs.createWriteStream(outputPath);
  
  try {
    for (let i = 0; i < segmentUrls.length; i++) {
      const segmentData = await fetchWithCookies(segmentUrls[i], cookieHeader, true);
      writeStream.write(segmentData);
      
      if ((i + 1) % 10 === 0) {
        console.log(`[download] Downloaded ${i + 1}/${segmentUrls.length} segments`);
      }
    }
  } finally {
    writeStream.end();
    await new Promise((resolve) => writeStream.on('finish', resolve));
  }

  console.log(`[download] All ${segmentUrls.length} segments concatenated to ${outputPath}`);
}

/**
 * Downloads a file with authentication cookies using Node.js fetch.
 */
async function downloadFileWithCookies(url, cookieHeader, outputPath) {
  const response = await fetch(url, {
    headers: {
      'Cookie': cookieHeader,
      'User-Agent': 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36',
      'Referer': 'https://artlist.io/',
      'Origin': 'https://artlist.io/',
    },
  });

  if (!response.ok) {
    throw new Error(`HTTP ${response.status} downloading ${url}`);
  }

  const buffer = await response.arrayBuffer();
  fs.writeFileSync(outputPath, Buffer.from(buffer));
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
      headers: {
        'Cookie': cookieHeader,
        'User-Agent': 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36',
        'Referer': 'https://artlist.io/',
        'Origin': 'https://artlist.io/',
      },
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

/**
 * Resolves a relative URL against a base URL.
 */
function resolveUrl(base, relative) {
  if (relative.startsWith('http://') || relative.startsWith('https://')) {
    return relative;
  }
  const baseUrl = new URL(base);
  return new URL(relative, baseUrl.origin + baseUrl.pathname.substring(0, baseUrl.pathname.lastIndexOf('/') + 1)).href;
}
