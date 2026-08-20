import fs from 'node:fs';
import path from 'node:path';
import { execFileSync } from 'node:child_process';

import { downloadHLSWithCookies } from './download-hls.js';
import { downloadFileWithCookies, httpDownloadSnapshot } from './download-http.js';
import { segmentQueueSnapshot } from './segment-queue.js';
import { globalProbePool, globalVideoDownloadPool, downloadPoolSnapshot } from './download-pool.js';

function streamUrlForClip(clip) {
  const candidates = [
    clip?.primary_url,
    clip?.preview_url,
    ...(Array.isArray(clip?.stream_urls) ? clip.stream_urls : []),
  ];
  return candidates
    .map((value) => String(value || '').trim())
    .find((value) => /\.(?:m3u8|mp4)(?:[?#]|$)/i.test(value)) || '';
}

function probeMedia(filePath) {
  const probe = JSON.parse(execFileSync('ffprobe', [
    '-v', 'error',
    '-show_entries', 'format=duration,size:stream=codec_type,codec_name,width,height,r_frame_rate',
    '-of', 'json', filePath,
  ], { encoding: 'utf8' }));
  const stream = (probe.streams || []).find((item) => item.codec_type === 'video');
  const duration = Number(probe.format?.duration || 0);
  if (!stream || duration <= 0 || Number(stream.width) <= 0 || Number(stream.height) <= 0) {
    const error = new Error('Downloaded media has no valid video stream or dimensions');
    error.code = 'MEDIA_VERIFY_FAILED';
    throw error;
  }
  return {
    duration_seconds: duration,
    width: Number(stream.width),
    height: Number(stream.height),
    codec_name: stream.codec_name || '',
  };
}

/**
 * Downloads a discovered Artlist preview without Chromium. The destination
 * is committed only after download and ffprobe validation succeed.
 */
async function downloadDirectClipOnce({ clip, outputDir, cookieHeader = '' } = {}) {
  const startedAt = Date.now();
  const url = streamUrlForClip(clip);
  if (!url) {
    const error = new Error('clip has no downloadable media stream URL');
    error.code = 'STREAM_NOT_FOUND';
    throw error;
  }
  const clipId = String(clip.clip_id || clip.id || 'clip').replace(/[^a-zA-Z0-9_-]/g, '_');
  const isHls = /\.m3u8(?:\?|$)/i.test(url);
  const extension = isHls ? '.ts' : path.extname(new URL(url).pathname) || '.mp4';
  fs.mkdirSync(outputDir, { recursive: true });
  const outputPath = path.join(outputDir, `${clipId}${extension}`);
  const tempPath = `${outputPath}.tmp-${process.pid}-${Date.now()}`;
  try {
    const transfer = isHls
      ? await downloadHLSWithCookies(url, cookieHeader, tempPath)
      : await downloadFileWithCookies(url, cookieHeader, tempPath);
    const media = await globalProbePool.run(null, () => probeMedia(tempPath));
    const sha256 = transfer?.sha256 || '';
    fs.renameSync(tempPath, outputPath);
    const fileSize = fs.statSync(outputPath).size;
    return {
      ...media,
      local_path: outputPath,
      file_size: fileSize,
      sha256,
      stream_url: url,
      browser_launched: false,
      atomic_commit: true,
      elapsed_ms: Date.now() - startedAt,
      metrics: {
        pools: downloadPoolSnapshot(),
        segments: segmentQueueSnapshot(),
        http: httpDownloadSnapshot(),
      },
    };
  } catch (error) {
    fs.rmSync(tempPath, { force: true });
    throw error;
  }
}

export async function downloadDirectClip({ clip, outputDir, cookieHeader = '' } = {}) {
  const clipId = String(clip?.clip_id || clip?.id || '').trim();
  const url = streamUrlForClip(clip);
  const key = `${clipId}|${url}|${path.resolve(outputDir || '.')}`;
  return globalVideoDownloadPool.run(key, () => downloadDirectClipOnce({ clip, outputDir, cookieHeader }));
}
