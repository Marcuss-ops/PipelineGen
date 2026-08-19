import crypto from 'node:crypto';
import {
  downloadSegmentBuffer,
  fetchWithCookies,
  resolveUrl,
} from './download-http.js';
import {
  normalizeSegmentConcurrency,
  spoolSegmentsToFile,
} from './segment-spool.js';

async function downloadHLSOnce(m3u8Url, cookieHeader, outputPath) {
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
  if (!selectedPlaylistUrl) throw new Error('Could not find media playlist URL in HLS master playlist');

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
      if (uriMatch) keyUrl = resolveUrl(selectedPlaylistUrl, uriMatch[1]);
      if (ivMatch) iv = ivMatch[1];
    }
    if (line && !line.startsWith('#')) segmentUrls.push(resolveUrl(selectedPlaylistUrl, line));
  }
  if (segmentUrls.length === 0) throw new Error('No video segments found in HLS playlist');

  if (keyUrl) {
    encryptionKey = await fetchWithCookies(keyUrl, cookieHeader, true);
    console.log(`[download] AES-128 encryption detected, key size: ${encryptionKey.length} bytes, IV: ${iv || 'playlist sequence'}`);
  }

  const concurrency = normalizeSegmentConcurrency(process.env.ARTLIST_HLS_SEGMENT_CONCURRENCY);
  const hash = crypto.createHash('sha256');
  console.log(`[download] Downloading ${segmentUrls.length} segments with concurrency=${concurrency}...`);
  await spoolSegmentsToFile({
    segmentUrls,
    outputPath,
    concurrency,
    hash,
    downloadSegmentBuffer: async (segmentUrl) => {
      return downloadSegmentBuffer(segmentUrl, cookieHeader);
    },
  });
  console.log(`[download] All ${segmentUrls.length} segments concatenated to ${outputPath}`);
  return { sha256: hash.digest('hex') };
}

export async function downloadHLSWithCookies(m3u8Url, cookieHeader, outputPath) {
  let lastError;
  for (let attempt = 1; attempt <= 2; attempt += 1) {
    try {
      return await downloadHLSOnce(m3u8Url, cookieHeader, outputPath);
    } catch (error) {
      lastError = error;
      if (![403, 429, 500, 502, 503, 504].includes(error?.status) || attempt === 2) throw error;
      await new Promise((resolve) => setTimeout(resolve, 200 + Math.floor(Math.random() * 300)));
    }
  }
  throw lastError;
}
