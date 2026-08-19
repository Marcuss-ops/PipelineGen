import fs from 'node:fs';
import path from 'node:path';
import { Transform } from 'node:stream';
import { pipeline } from 'node:stream/promises';

const DEFAULT_CONCURRENCY = 4;
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
  downloadSegmentBuffer,
  hash = null,
}) {
  if (!Array.isArray(segmentUrls) || segmentUrls.length === 0) {
    throw new Error('segmentUrls must contain at least one URL');
  }
  if (!outputPath) {
    throw new Error('outputPath is required');
  }
  if (typeof downloadSegment !== 'function' && typeof downloadSegmentBuffer !== 'function') {
    throw new Error('downloadSegment callback is required');
  }

  const outputDir = path.dirname(outputPath);
  await fs.promises.mkdir(outputDir, { recursive: true });
  const effectiveConcurrency = Math.min(
    normalizeSegmentConcurrency(concurrency),
    segmentUrls.length,
  );
  const width = Math.max(6, String(segmentUrls.length - 1).length);
  let nextIndex = 0;

  if (typeof downloadSegmentBuffer === 'function') {
    const pending = new Map();
    let nextIndex = 0;
    let failed = null;
    let wake = null;
    const notify = () => {
      if (wake) {
        const resolve = wake;
        wake = null;
        resolve();
      }
    };
    const waitForChange = () => new Promise((resolve) => { wake = resolve; });
    const writeChunk = (stream, chunk) => new Promise((resolve, reject) => {
      stream.write(chunk, (error) => (error ? reject(error) : resolve()));
    });
    const output = fs.createWriteStream(outputPath);
    try {
      const worker = async () => {
        while (nextIndex < segmentUrls.length) {
          const index = nextIndex;
          nextIndex += 1;
          try {
            pending.set(index, await downloadSegmentBuffer(segmentUrls[index], index));
            notify();
          } catch (error) {
            failed = error;
            notify();
            return;
          }
        }
      };
      const workers = Array.from({ length: effectiveConcurrency }, worker);
      for (let index = 0; index < segmentUrls.length; index += 1) {
        while (!pending.has(index)) {
          if (failed) throw failed;
          await waitForChange();
        }
        const chunk = pending.get(index);
        pending.delete(index);
        if (hash) hash.update(chunk);
        await writeChunk(output, chunk);
      }
      if (failed) throw failed;
      await Promise.all(workers);
      await new Promise((resolve, reject) => output.end((error) => (error ? reject(error) : resolve())));
      return;
    } catch (error) {
      output.destroy();
      await fs.promises.rm(outputPath, { force: true }).catch(() => {});
      throw error;
    }
  }

  const spoolPrefix = path.join(outputDir, `.hls-${path.basename(outputPath)}-`);
  const spoolDir = await fs.promises.mkdtemp(spoolPrefix);
  const segmentPath = (index) => path.join(
    spoolDir,
    `${String(index).padStart(width, '0')}.segment`,
  );

  try {
    async function worker() {
      while (nextIndex < segmentUrls.length) {
        const index = nextIndex;
        nextIndex += 1;
        await downloadSegment(segmentUrls[index], segmentPath(index), index);
      }
    }

    const workerResults = await Promise.allSettled(
      Array.from({ length: effectiveConcurrency }, () => worker()),
    );
    const failedWorker = workerResults.find((result) => result.status === 'rejected');
    if (failedWorker) {
      throw failedWorker.reason;
    }

    for (let index = 0; index < segmentUrls.length; index += 1) {
      const source = fs.createReadStream(segmentPath(index));
      const destination = fs.createWriteStream(outputPath, { flags: index === 0 ? 'w' : 'a' });
      if (hash) {
        const hashing = new Transform({
          transform(chunk, _encoding, callback) {
            hash.update(chunk);
            callback(null, chunk);
          },
        });
        await pipeline(source, hashing, destination);
      } else {
        await pipeline(source, destination);
      }
    }
  } catch (error) {
    await fs.promises.rm(outputPath, { force: true }).catch(() => {});
    throw error;
  } finally {
    await fs.promises.rm(spoolDir, { recursive: true, force: true }).catch(() => {});
  }
}
