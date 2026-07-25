import fs from 'node:fs';
import path from 'node:path';
import { pipeline } from 'node:stream/promises';

const DEFAULT_CONCURRENCY = 5;
const MAX_CONCURRENCY = 16;

export function normalizeSegmentConcurrency(value, fallback = DEFAULT_CONCURRENCY) {
  const parsed = Number.parseInt(String(value ?? ''), 10);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return fallback;
  }
  return Math.min(parsed, MAX_CONCURRENCY);
}

/**
 * Downloads HLS segments concurrently into a temporary spool directory, then
 * concatenates them in playlist order using streams. Memory usage stays bounded
 * to stream buffers instead of growing with the complete video size.
 */
export async function spoolSegmentsToFile({
  segmentUrls,
  outputPath,
  concurrency = DEFAULT_CONCURRENCY,
  downloadSegment,
}) {
  if (!Array.isArray(segmentUrls) || segmentUrls.length === 0) {
    throw new Error('segmentUrls must contain at least one URL');
  }
  if (!outputPath) {
    throw new Error('outputPath is required');
  }
  if (typeof downloadSegment !== 'function') {
    throw new Error('downloadSegment callback is required');
  }

  const outputDir = path.dirname(outputPath);
  await fs.promises.mkdir(outputDir, { recursive: true });
  const spoolPrefix = path.join(outputDir, `.hls-${path.basename(outputPath)}-`);
  const spoolDir = await fs.promises.mkdtemp(spoolPrefix);
  const effectiveConcurrency = Math.min(
    normalizeSegmentConcurrency(concurrency),
    segmentUrls.length,
  );
  const width = Math.max(6, String(segmentUrls.length - 1).length);
  let nextIndex = 0;

  const segmentPath = (index) => path.join(
    spoolDir,
    `${String(index).padStart(width, '0')}.segment`,
  );

  try {
    async function worker() {
      while (true) {
        const index = nextIndex;
        nextIndex += 1;
        if (index >= segmentUrls.length) {
          return;
        }
        await downloadSegment(segmentUrls[index], segmentPath(index), index);
      }
    }

    await Promise.all(
      Array.from({ length: effectiveConcurrency }, () => worker()),
    );

    for (let index = 0; index < segmentUrls.length; index += 1) {
      await pipeline(
        fs.createReadStream(segmentPath(index)),
        fs.createWriteStream(outputPath, { flags: index === 0 ? 'w' : 'a' }),
      );
    }
  } catch (error) {
    await fs.promises.rm(outputPath, { force: true }).catch(() => {});
    throw error;
  } finally {
    await fs.promises.rm(spoolDir, { recursive: true, force: true }).catch(() => {});
  }
}
