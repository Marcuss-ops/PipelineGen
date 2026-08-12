import fs from 'node:fs';
import { Readable } from 'node:stream';
import { pipeline } from 'node:stream/promises';

export function authenticatedHeaders(cookieHeader) {
  return {
    Cookie: cookieHeader,
    'User-Agent': 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36',
    Referer: 'https://artlist.io/',
    Origin: 'https://artlist.io/',
  };
}

export async function downloadFileWithCookies(url, cookieHeader, outputPath) {
  const controller = new AbortController();
  const timeoutId = setTimeout(() => controller.abort(), 60000);
  try {
    const response = await fetch(url, {
      signal: controller.signal,
      headers: authenticatedHeaders(cookieHeader),
    });
    if (!response.ok) throw new Error(`HTTP ${response.status} downloading ${url}`);
    if (!response.body) throw new Error(`Empty response body downloading ${url}`);
    await pipeline(Readable.fromWeb(response.body), fs.createWriteStream(outputPath));
  } catch (error) {
    await fs.promises.rm(outputPath, { force: true }).catch(() => {});
    throw error;
  } finally {
    clearTimeout(timeoutId);
  }
}

export async function fetchWithCookies(url, cookieHeader, asBuffer = false) {
  const controller = new AbortController();
  const timeoutId = setTimeout(() => controller.abort(), 60000);
  try {
    const response = await fetch(url, {
      signal: controller.signal,
      headers: authenticatedHeaders(cookieHeader),
    });
    if (!response.ok) throw new Error(`HTTP ${response.status} fetching ${url.substring(0, 80)}`);
    if (asBuffer) return Buffer.from(await response.arrayBuffer());
    return await response.text();
  } finally {
    clearTimeout(timeoutId);
  }
}

export function resolveUrl(base, relative) {
  if (relative.startsWith('http://') || relative.startsWith('https://')) return relative;
  const baseUrl = new URL(base);
  return new URL(
    relative,
    baseUrl.origin + baseUrl.pathname.substring(0, baseUrl.pathname.lastIndexOf('/') + 1),
  ).href;
}
