import fs from 'node:fs';
import crypto from 'node:crypto';
import { Readable, Transform } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { Agent, setGlobalDispatcher } from 'undici';
import { globalSegmentQueue } from './segment-queue.js';

const CDN_POOL = new Agent({
  connections: Number.parseInt(process.env.ARTLIST_CDN_CONNECTIONS || '6', 10),
  pipelining: 1,
  keepAliveTimeout: 10_000,
  keepAliveMaxTimeout: 30_000,
});
setGlobalDispatcher(CDN_POOL);

const RETRYABLE_STATUSES = new Set([403, 429, 500, 502, 503, 504]);
const MAX_ATTEMPTS = 3;

function retryDelay(attempt) {
  const jitter = Math.floor(Math.random() * 100);
  return (250 * (2 ** (attempt - 1))) + jitter;
}

export function authenticatedHeaders(cookieHeader) {
  return {
    Cookie: cookieHeader,
    'User-Agent': 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36',
    Referer: 'https://artlist.io/',
    Origin: 'https://artlist.io/',
  };
}

export async function downloadFileWithCookies(url, cookieHeader, outputPath) {
  let lastError;
  for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt += 1) {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), 60000);
      try {
        const response = await fetch(url, {
          signal: controller.signal,
          headers: authenticatedHeaders(cookieHeader),
        });
        if (!response.ok) {
          const error = new Error(`HTTP ${response.status} downloading ${url}`);
          error.status = response.status;
          throw error;
        }
        if (!response.body) throw new Error(`Empty response body downloading ${url}`);
        const source = Readable.fromWeb(response.body);
        const hash = crypto.createHash('sha256');
        const hashing = new Transform({
          transform(chunk, _encoding, callback) {
            hash.update(chunk);
            callback(null, chunk);
          },
        });
        await pipeline(source, hashing, fs.createWriteStream(outputPath));
        return { sha256: hash.digest('hex') };
      } catch (error) {
        lastError = error;
        await fs.promises.rm(outputPath, { force: true }).catch(() => {});
        if (!RETRYABLE_STATUSES.has(error?.status) || attempt === MAX_ATTEMPTS) throw error;
        await new Promise((resolve) => setTimeout(resolve, retryDelay(attempt)));
      } finally {
        clearTimeout(timeoutId);
      }
    }
  throw lastError;
}

export async function fetchWithCookies(url, cookieHeader, asBuffer = false) {
  let lastError;
  for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt += 1) {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), 60000);
      try {
        const response = await fetch(url, {
          signal: controller.signal,
          headers: authenticatedHeaders(cookieHeader),
        });
        if (!response.ok) {
          const error = new Error(`HTTP ${response.status} fetching ${url.substring(0, 80)}`);
          error.status = response.status;
          throw error;
        }
        if (asBuffer) return Buffer.from(await response.arrayBuffer());
        return await response.text();
      } catch (error) {
        lastError = error;
        if (!RETRYABLE_STATUSES.has(error?.status) || attempt === MAX_ATTEMPTS) throw error;
        await new Promise((resolve) => setTimeout(resolve, retryDelay(attempt)));
      } finally {
        clearTimeout(timeoutId);
      }
    }
  throw lastError;
}

export async function downloadSegmentBuffer(url, cookieHeader) {
  return globalSegmentQueue.run(async () => {
    let lastError;
    for (let attempt = 1; attempt <= MAX_ATTEMPTS; attempt += 1) {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), 60000);
      try {
        const response = await fetch(url, {
          signal: controller.signal,
          headers: authenticatedHeaders(cookieHeader),
        });
        if (!response.ok) {
          const error = new Error(`HTTP ${response.status} downloading segment ${url}`);
          error.status = response.status;
          throw error;
        }
        return Buffer.from(await response.arrayBuffer());
      } catch (error) {
        lastError = error;
        if (!RETRYABLE_STATUSES.has(error?.status) || attempt === MAX_ATTEMPTS) throw error;
        await new Promise((resolve) => setTimeout(resolve, retryDelay(attempt)));
      } finally {
        clearTimeout(timeoutId);
      }
    }
    throw lastError;
  });
}

export function resolveUrl(base, relative) {
  if (relative.startsWith('http://') || relative.startsWith('https://')) return relative;
  const baseUrl = new URL(base);
  return new URL(
    relative,
    baseUrl.origin + baseUrl.pathname.substring(0, baseUrl.pathname.lastIndexOf('/') + 1),
  ).href;
}
