/**
 * Artlist video downloader using Puppeteer/Chromium.
 *
 * Artlist HLS streams (.m3u8) require browser session cookies and headers.
 * This module navigates to the clip page, captures the stream URL, and
 * delegates authenticated transfer to the HTTP/HLS sibling modules.
 */

import fs from 'node:fs';
import path from 'node:path';
import { importCookies } from '../driver/cookies.js';
import { downloadHLSWithCookies } from './download-hls.js';
import {
  downloadFileWithCookies,
} from './download-http.js';

export async function downloadClipVideo(browser, clipPageUrl, clipId, outputDir) {
  if (!browser) throw new Error('Browser instance is required');
  fs.mkdirSync(outputDir, { recursive: true });

  const page = await browser.newPage();
  await page.setViewport({ width: 1440, height: 900 });
  await page.setUserAgent('Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36');
  const cookiePath = process.env.ARTLIST_COOKIE_FILE?.trim() || '';
  await importCookies(page, cookiePath);

  const streamUrls = new Set();
  const mp4Urls = new Set();
  const addCandidateUrl = (url) => {
    if (typeof url !== 'string') return;
    const trimmed = url.trim().replace(/\+$/, '');
    if (!trimmed || trimmed === clipPageUrl) return;
    if (trimmed.includes('.m3u8')) {
      streamUrls.add(trimmed);
    } else if (trimmed.includes('.mp4')) {
      mp4Urls.add(trimmed);
    }
  };
  const onRequest = (req) => {
    const url = req.url();
    if (url.includes('.m3u8')) streamUrls.add(url.replace(/\+$/, ''));
    if (url.includes('.mp4') && !url.includes('.m3u8')) mp4Urls.add(url);
  };
  const onResponse = (res) => {
    const url = res.url();
    if (url.includes('.m3u8')) streamUrls.add(url.replace(/\+$/, ''));
    if (url.includes('.mp4') && !url.includes('.m3u8')) mp4Urls.add(url);
  };

  page.on('request', onRequest);
  page.on('response', onResponse);
  try {
    await page.goto(clipPageUrl, { waitUntil: 'networkidle2', timeout: 120000 });
    await page.waitForSelector('video, [class*="player"], [class*="video"]', { timeout: 15000 }).catch(() => {});
    if (mp4Urls.size === 0 && streamUrls.size === 0) await new Promise((resolve) => setTimeout(resolve, 500));
    await page.evaluate(() => {
      const video = document.querySelector('video');
      if (video) video.scrollIntoView({ behavior: 'instant', block: 'center' });
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
      if (video) return video.src || video.currentSrc || '';
      const source = document.querySelector('source');
      return source ? source.src : '';
    });
    if (videoSrc && !streamUrls.has(videoSrc) && !mp4Urls.has(videoSrc)) addCandidateUrl(videoSrc);

    const cookies = await page.cookies();
    const cookieHeader = cookies.map((cookie) => `${cookie.name}=${cookie.value}`).join('; ');
    let downloadUrl = '';
    let isM3u8 = false;
    if (mp4Urls.size > 0) downloadUrl = Array.from(mp4Urls)[0];
    else if (streamUrls.size > 0) {
      downloadUrl = Array.from(streamUrls)[0];
      isM3u8 = true;
    }
    if (!downloadUrl) throw new Error(`No video stream URL found for ${clipPageUrl}`);

    console.log(`[download] Found video URL: ${downloadUrl.substring(0, 100)} (HLS: ${isM3u8})`);
    const ext = isM3u8 ? '.ts' : '.mp4';
    const outputPath = path.join(outputDir, `${clipId || 'clip'}${ext}`);
    if (isM3u8) await downloadHLSWithCookies(downloadUrl, cookieHeader, outputPath);
    else await downloadFileWithCookies(downloadUrl, cookieHeader, outputPath);

    const stats = fs.statSync(outputPath);
    console.log(`[download] Successfully downloaded to ${outputPath} (${stats.size} bytes)`);
    return {
      local_path: outputPath,
      file_size: stats.size,
      duration_seconds: 0,
      width: 0,
      height: 0,
    };
  } finally {
    await page.close().catch(() => {});
  }
}
